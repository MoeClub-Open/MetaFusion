package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

const overviewRootID = "01a10136-c0f1-7eb5-a66c-112270350f28"
const overviewPeerID = "01a0f8c4-ef4a-732e-a143-c6514957b9a3"

type overviewFixture struct {
	t           *testing.T
	self        catalog.Entity
	definitions catalog.DefinitionConfig
	pages       func(int) catalog.EntityLinksPage
	offsets     []int
}

func (f *overviewFixture) Get(_ context.Context, id string, u *catalog.User) (catalog.Entity, error) {
	f.t.Helper()
	if u != nil {
		f.t.Fatal("generated context used privileged visibility")
	}
	if id != f.self.ID {
		f.t.Fatalf("unexpected root %q", id)
	}
	return f.self, nil
}
func (f *overviewFixture) Definitions(context.Context) (catalog.DefinitionConfig, error) {
	return f.definitions, nil
}
func (f *overviewFixture) EntityLinks(_ context.Context, _ string, limit, offset int, u *catalog.User) (catalog.EntityLinksPage, error) {
	f.t.Helper()
	if u != nil || limit != 100 {
		f.t.Fatalf("nonpublic or unexpected page bounds: %d, %+v", limit, u)
	}
	f.offsets = append(f.offsets, offset)
	return f.pages(offset), nil
}

func newOverviewFixture(t *testing.T) *overviewFixture {
	f := &overviewFixture{t: t, self: catalog.Entity{ID: overviewRootID, Kind: "release", Version: 8, State: "active", Title: "棘アリ [通常盤]", Attributes: map[string]any{"edition_date": "2024-05-22", "count": float64(0)}}}
	f.definitions = catalog.DefinitionConfig{ETag: "definitions-1", Document: catalog.Definitions{Fields: map[string]catalog.Field{
		"edition_date": {Type: "date", Enabled: true, Names: catalog.Names{"en-US": "Publication"}},
		"count":        {Type: "number", Enabled: true, Names: catalog.Names{"en-US": "Recorded amount"}},
	}}}
	f.pages = func(int) catalog.EntityLinksPage {
		return catalog.EntityLinksPage{DefinitionETag: f.definitions.ETag, Entities: map[string]catalog.Entity{f.self.ID: f.self}, Items: []catalog.EntityLink{}}
	}
	return f
}

func TestOverviewFollowsHasMoreAcrossEmptyVisiblePage(t *testing.T) {
	f := newOverviewFixture(t)
	peer := catalog.Entity{ID: overviewPeerID, Kind: "agent", Version: 5, State: "active", Title: "ユニバーサル ミュージック合同会社"}
	f.pages = func(offset int) catalog.EntityLinksPage {
		page := catalog.EntityLinksPage{DefinitionETag: f.definitions.ETag, Entities: map[string]catalog.Entity{f.self.ID: f.self}, HasMore: offset == 0}
		if offset == 100 {
			page.Items = []catalog.EntityLink{{Key: "attribute:publisher", RuleCode: "attribute:publisher", SourceID: f.self.ID, TargetID: peer.ID}}
			page.Entities[peer.ID] = peer
		}
		return page
	}
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.offsets, []int{0, 100}) || len(facts.Links) != 1 || len(facts.Entities) != 2 || facts.Partial {
		t.Fatalf("offsets=%v facts=%+v", f.offsets, facts)
	}
	if facts.Dependencies.EntityVersions[peer.ID] != 5 {
		t.Fatal("neighbor version not retained")
	}
}

func TestOverviewContextLimitsReportPartial(t *testing.T) {
	f := newOverviewFixture(t)
	f.pages = func(offset int) catalog.EntityLinksPage {
		return catalog.EntityLinksPage{DefinitionETag: f.definitions.ETag, Entities: map[string]catalog.Entity{f.self.ID: f.self}, HasMore: true, Items: []catalog.EntityLink{{Key: fmt.Sprint(offset), SourceID: f.self.ID, TargetID: f.self.ID}}}
	}
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.offsets) != 10 || !facts.Partial || !reflect.DeepEqual(facts.Dependencies.PartialReasons, []string{"relationship_limit"}) {
		t.Fatalf("offsets=%v partial=%+v", f.offsets, facts.Dependencies)
	}
}

func TestOverviewRejectsMixedDefinitionAndEntityVersions(t *testing.T) {
	for _, change := range []string{"definitions", "root"} {
		t.Run(change, func(t *testing.T) {
			f := newOverviewFixture(t)
			f.pages = func(offset int) catalog.EntityLinksPage {
				root := f.self
				etag := f.definitions.ETag
				if offset == 100 {
					if change == "root" {
						root.Version++
					} else {
						etag = "definitions-2"
					}
				}
				return catalog.EntityLinksPage{DefinitionETag: etag, Entities: map[string]catalog.Entity{root.ID: root}, HasMore: offset == 0}
			}
			if _, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US"); !errors.Is(err, errOverviewContextChanged) {
				t.Fatalf("mixed snapshot accepted: %v", err)
			}
		})
	}
}

func TestOverviewDoesNotCopyRawDocumentsOrInvisibleNestedReferences(t *testing.T) {
	f := newOverviewFixture(t)
	f.self.CreatedBy = "private-owner"
	f.self.Translations = map[string]catalog.Translation{"en-US": {Title: "Official Title", Body: "Manual article body", Summary: "Manual summary"}}
	f.self.Contents = []catalog.Inclusion{{ExpressionID: "hidden-expression", Locator: catalog.Locator{"secret": "hidden-content"}}}
	f.self.Attributes["hidden"] = "private-raw-data"
	f.self.Attributes["publisher"] = overviewPeerID
	f.self.Attributes["notes"] = map[string]any{"peer": "hidden-peer", "text": "Recorded public text"}
	f.self.Attributes["events"] = []any{map[string]any{"peer": "hidden-peer", "date": "2020"}}
	f.definitions.Document.Fields["hidden"] = catalog.Field{Type: "string", Enabled: true, Hidden: true}
	f.definitions.Document.Fields["publisher"] = catalog.Field{Type: "entity", Enabled: true}
	f.definitions.Document.Fields["notes"] = catalog.Field{Type: "group", Enabled: true, Fields: map[string]catalog.Field{"peer": {Type: "entity", Enabled: true}, "text": {Type: "string", Enabled: true}}}
	f.definitions.Document.Fields["events"] = catalog.Field{Type: "list", Enabled: true, Items: &catalog.Field{Type: "group", Enabled: true, Fields: map[string]catalog.Field{"peer": {Type: "entity", Enabled: true}, "date": {Type: "date", Enabled: true}}}}
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(facts)
	for _, secret := range []string{"private-owner", "Manual article", "Manual summary", "hidden-expression", "hidden-content", "private-raw-data", "hidden-peer", overviewPeerID} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("raw or invisible value leaked: %s", secret)
		}
	}
	if !strings.Contains(string(b), "Official Title") || !strings.Contains(string(b), "Recorded public text") {
		t.Fatalf("public facts lost: %s", b)
	}
	if _, exists := facts.Entities[0].Attributes["publisher"]; exists {
		t.Fatal("unresolved entity reference retained")
	}
	// The generated projection cannot modify the original manually written body.
	if f.self.Translations["en-US"].Body != "Manual article body" || len(f.self.Contents) != 1 {
		t.Fatal("source entity mutated")
	}
}

func TestOverviewHashInvalidatesEverySourceOfChangedFacts(t *testing.T) {
	makeFacts := func(t *testing.T, change string) overviewContext {
		f := newOverviewFixture(t)
		peer := catalog.Entity{ID: overviewPeerID, Kind: "agent", Version: 5, State: "active", Title: "Publisher"}
		version := int64(2)
		link := catalog.EntityLink{Key: "relation:one", RuleCode: "relation:credit_for", Version: &version, SourceID: f.self.ID, TargetID: peer.ID, Attributes: map[string]any{"role": "recorded"}}
		if change == "neighbor" {
			peer.Version++
			peer.Title = "Changed publisher"
		}
		if change == "relation" {
			version++
			link.Attributes["role"] = "changed"
		}
		if change == "definitions" {
			f.definitions.ETag = "definitions-2"
		}
		f.pages = func(int) catalog.EntityLinksPage {
			items := []catalog.EntityLink{link}
			if change == "new_incoming" {
				items = append(items, catalog.EntityLink{Key: "attribute:incoming", SourceID: peer.ID, TargetID: f.self.ID})
			}
			return catalog.EntityLinksPage{DefinitionETag: f.definitions.ETag, Entities: map[string]catalog.Entity{f.self.ID: f.self, peer.ID: peer}, Items: items}
		}
		facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	model := Model{ID: "model-1", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	base := makeFacts(t, "")
	baseHash := overviewContextHash(base, "en-US", model)
	for _, change := range []string{"neighbor", "relation", "definitions", "new_incoming"} {
		t.Run(change, func(t *testing.T) {
			if overviewContextHash(makeFacts(t, change), "en-US", model) == baseHash {
				t.Fatalf("change %s did not invalidate cache", change)
			}
		})
	}
	if overviewContextHash(base, "zh-CN", model) == baseHash {
		t.Fatal("locale shares cache hash")
	}
	model.ID = "model-2"
	if overviewContextHash(base, "en-US", model) == baseHash {
		t.Fatal("model shares cache hash")
	}
	model.ID = "model-1"
	model.UpdatedAt = model.UpdatedAt.Add(time.Minute)
	if overviewContextHash(base, "en-US", model) == baseHash {
		t.Fatal("model config change shares cache hash")
	}
}

func TestOverviewStructuredVisualsUseDefinedDatesAndRecordedCounts(t *testing.T) {
	f := newOverviewFixture(t)
	f.self.Attributes["custom_date"] = "1997"
	f.self.Attributes["disabled_date"] = "1900-01-01"
	f.self.Attributes["events"] = []any{map[string]any{"date": "2002-09"}}
	f.definitions.Document.Fields["custom_date"] = catalog.Field{Type: "date", Enabled: true, Names: catalog.Names{"en-US": "Custom date"}}
	f.definitions.Document.Fields["disabled_date"] = catalog.Field{Type: "date", Enabled: false}
	f.definitions.Document.Fields["events"] = catalog.Field{Type: "list", Enabled: true, Names: catalog.Names{"en-US": "Events"}, Items: &catalog.Field{Type: "group", Enabled: true, Fields: map[string]catalog.Field{"date": {Type: "date", Enabled: true, Names: catalog.Names{"en-US": "Date"}}}}}
	peer := catalog.Entity{ID: overviewPeerID, Kind: "agent", Version: 5, State: "active", Title: "Publisher"}
	f.pages = func(int) catalog.EntityLinksPage {
		return catalog.EntityLinksPage{DefinitionETag: f.definitions.ETag, Entities: map[string]catalog.Entity{f.self.ID: f.self, peer.ID: peer}, Items: []catalog.EntityLink{{Key: "one", SourceID: f.self.ID, TargetID: peer.ID}, {Key: "two", SourceID: f.self.ID, TargetID: peer.ID}}}
	}
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	blocks := overviewFactBlocks(facts, "en-US")
	var events []OverviewEvent
	var chart OverviewBlock
	var rows [][]string
	for _, block := range blocks {
		switch block.Type {
		case "timeline":
			events = block.Events
		case "chart":
			chart = block
		case "table":
			rows = block.Rows
		}
	}
	dates := []string{}
	for _, event := range events {
		dates = append(dates, event.Date)
		if event.EntityID != f.self.ID {
			t.Fatalf("wrong event source: %+v", event)
		}
	}
	if !reflect.DeepEqual(dates, []string{"1997", "2002-09", "2024-05-22"}) {
		t.Fatalf("date precision or definitions lost: %+v", events)
	}
	if chart.ChartType != "bar" || len(chart.Series) != 1 || !reflect.DeepEqual(chart.Series[0].Values, []float64{1}) {
		t.Fatalf("duplicate edges inflated count: %+v", chart)
	}
	zeroFound := false
	for _, row := range rows {
		if row[0] == "Recorded amount" && row[1] == "0" {
			zeroFound = true
		}
	}
	if !zeroFound {
		t.Fatal("zero numeric fact omitted")
	}
}

func TestOverviewProseRejectsExecutableAndInventedOutput(t *testing.T) {
	f := newOverviewFixture(t)
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	blocks := overviewFactBlocks(facts, "en-US")
	valid := `{"markdown":"**棘アリ [通常盤]** was recorded with a publication date of 2024-05-22."}`
	if _, err := validateOverviewProse(valid, facts, blocks); err != nil {
		t.Fatal(err)
	}
	for name, answer := range map[string]string{
		"html":                  `{"markdown":"<script>alert(0)</script>"}`,
		"link":                  `{"markdown":"[title](javascript:alert)"}`,
		"image":                 `{"markdown":"![image](/catalog/x)"}`,
		"chart":                 `{"markdown":"Facts","series":[{"values":[99]}]}`,
		"hallucinated_number":   `{"markdown":"Sold 999999 copies."}`,
		"version_is_not_fact":   `{"markdown":"Sold 8 copies."}`,
		"entity_id_is_not_fact": `{"markdown":"Sold 112270350 copies."}`,
		"trailing_json":         `{"markdown":"Facts"}{"markdown":"Other"}`,
		"table":                 `{"markdown":"|column|value|"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateOverviewProse(answer, facts, blocks); !errors.Is(err, errOverviewOutput) {
				t.Fatalf("unsafe output accepted: %s, %v", answer, err)
			}
		})
	}
}

func TestGeneratedOverviewPreservesAttributionUsageAndPartialStatus(t *testing.T) {
	content, _ := json.Marshal(overviewDocument{Blocks: []OverviewBlock{{Type: "markdown", Markdown: "Recorded facts."}}, Sources: []OverviewSource{{EntityID: overviewRootID, Title: "Original", URL: "/catalog/" + overviewRootID, Version: 8}}, Usage: TokenUsage{InputTokens: 100, OutputTokens: 20}})
	deps, _ := json.Marshal(overviewDependencies{EntityVersion: 8, DefinitionsETag: "definitions", Partial: true, PartialReasons: []string{"relationship_limit"}})
	item, err := generatedOverview(GeneratedContent{ID: "cache", EntityID: overviewRootID, OwnerID: "byo-owner", ModelID: "model", Content: content, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	if item.OwnerID != "byo-owner" || item.EntityVersion != 8 || item.DefinitionsETag != "definitions" || !item.Partial || item.Usage.InputTokens != 100 || item.Sources[0].Version != 8 {
		t.Fatalf("lost generated metadata: %+v", item)
	}
}

func TestOverviewLocaleDoesNotFabricateTranslatedNames(t *testing.T) {
	entity := catalog.Entity{Title: "原題", Translations: map[string]catalog.Translation{"en-US": {Title: "Official English"}}}
	if overviewTitle(entity, "zh-CN") != "原題" {
		t.Fatal("unrecorded Chinese title invented")
	}
	if overviewTitle(entity, "en-US") != "Official English" {
		t.Fatal("recorded localization ignored")
	}
	if _, err := overviewLocale("bogus"); err == nil {
		t.Fatal("invalid locale accepted")
	}
	for _, date := range []string{"1997", "2002-09", "2024-05-22"} {
		if !validOverviewDate(date) {
			t.Fatal(date)
		}
	}
	for _, date := range []string{"2002-13", "2024-02-30", "unknown"} {
		if validOverviewDate(date) {
			t.Fatal(date)
		}
	}
}

func TestOverviewDropsRecordsWhoseReferenceAnchorIsNotPublic(t *testing.T) {
	f := newOverviewFixture(t)
	f.definitions.Document.Fields["events"] = catalog.Field{Type: "list", Enabled: true, Items: &catalog.Field{Type: "group", Enabled: true, AnchorKey: "relative_to", Fields: map[string]catalog.Field{"relative_to": {Type: "entity", Enabled: true}, "date": {Type: "date", Enabled: true}}}}
	f.self.Attributes["events"] = []any{map[string]any{"relative_to": "hidden-reference", "date": "1900"}}
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := facts.Entities[0].Attributes["events"]; exists {
		t.Fatal("orphaned record survived hidden anchor")
	}
	for _, block := range overviewFactBlocks(facts, "en-US") {
		for _, event := range block.Events {
			if event.Date == "1900" {
				t.Fatal("hidden anchor date attributed to root")
			}
		}
	}
}

func TestOverviewVisualLimitsRemainRenderableAndExplicit(t *testing.T) {
	f := newOverviewFixture(t)
	for i := 0; i < 205; i++ {
		code := fmt.Sprintf("field_%03d", i)
		f.definitions.Document.Fields[code] = catalog.Field{Type: "string", Enabled: true}
		f.self.Attributes[code] = "recorded"
	}
	f.definitions.Document.Fields["events"] = catalog.Field{Type: "list", Enabled: true, Items: &catalog.Field{Type: "date", Enabled: true}}
	dates := []any{}
	for i := 0; i < 110; i++ {
		dates = append(dates, fmt.Sprintf("%04d", 1900+i))
	}
	f.self.Attributes["events"] = dates
	f.definitions.Document.Fields["long"] = catalog.Field{Type: "string", Enabled: true}
	f.self.Attributes["long"] = strings.Repeat("🙂", 600)
	facts, err := loadOverviewContext(context.Background(), f, f.self.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Partial {
		t.Fatal("truncated visualization reported complete")
	}
	reasons := strings.Join(facts.Dependencies.PartialReasons, ",")
	for _, reason := range []string{"table_row_limit", "table_cell_limit", "timeline_event_limit"} {
		if !strings.Contains(reasons, reason) {
			t.Fatalf("missing %s: %v", reason, facts.Dependencies.PartialReasons)
		}
	}
	for _, block := range overviewFactBlocks(facts, "en-US") {
		if len(block.Rows) > 200 || len(block.Events) > 100 {
			t.Fatalf("unrenderable block bounds: %+v", block)
		}
		for _, row := range block.Rows {
			for _, cell := range row {
				if overviewTextLength(cell) > 1000 {
					t.Fatal("unrenderable table cell")
				}
			}
		}
	}
	if n := overviewTextLength(overviewBoundText(strings.Repeat("🙂", 600), 1000)); n > 1000 || n < 998 {
		t.Fatalf("UTF16 bound=%d", n)
	}
}

func TestPostgresGeneratedCacheSeparatesOwnersAndUpsertsOneContext(t *testing.T) {
	s, m, owner := accountingFixture(t)
	ctx := context.Background()
	content := jsonBytes(overviewDocument{Blocks: []OverviewBlock{{Type: "markdown", Markdown: "Recorded."}}, Sources: []OverviewSource{}, Usage: TokenUsage{InputTokens: 10}})
	deps := jsonBytes(overviewDependencies{EntityVersion: 1, DefinitionsETag: "definitions"})
	public, err := s.saveGenerated(ctx, GeneratedContent{ID: uuid.NewString(), EntityID: overviewRootID, Locale: "en-US", ContextHash: "same", ModelID: m.ID, Content: content, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.saveGenerated(ctx, GeneratedContent{ID: uuid.NewString(), EntityID: overviewRootID, Locale: "en-US", ContextHash: "same", ModelID: m.ID, OwnerID: owner, Content: content, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	if public.ID == private.ID {
		t.Fatal("private generation overwrote shared cache")
	}
	if _, err = s.findGenerated(ctx, overviewRootID, "en-US", uuid.NewString(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner's cache disclosed: %v", err)
	}
	updated, err := s.saveGenerated(ctx, GeneratedContent{ID: uuid.NewString(), EntityID: overviewRootID, Locale: "en-US", ContextHash: "same", ModelID: m.ID, Content: content, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != public.ID {
		t.Fatal("same context accumulated duplicate cache rows")
	}
	var total int
	if err = s.DB.QueryRow(`SELECT count(*) FROM assistant.generated_content`).Scan(&total); err != nil || total != 2 {
		t.Fatalf("cache rows=%d err=%v", total, err)
	}
	item, err := generatedOverview(updated)
	if err != nil || item.Usage.InputTokens != 10 {
		t.Fatalf("usage lost: %+v %v", item, err)
	}
}

func TestPostgresGeneratedReadsFallBackToValidSharedAndHideDeletedRoot(t *testing.T) {
	s, model, owner := accountingFixture(t)
	ctx := context.Background()
	entity := catalog.Entity{ID: overviewRootID, Kind: "agent", Version: 1, Title: "Public agent", State: "active", CreatedBy: owner, Attributes: map[string]any{}, Translations: map[string]catalog.Translation{}}
	if _, err := s.DB.Exec(`INSERT INTO catalog.definition_config(singleton,document,etag) VALUES(true,$1,'overview-definitions') ON CONFLICT(singleton) DO UPDATE SET document=EXCLUDED.document,etag=EXCLUDED.etag`, jsonBytes(catalog.Definitions{})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document) VALUES($1,'agent',1,$2,'active',$3,$4)`, entity.ID, entity.Title, owner, jsonBytes(entity)); err != nil {
		t.Fatal(err)
	}
	h := HTTP{Store: s, Catalog: &catalog.Store{DB: s.DB}}
	facts, err := loadOverviewContext(ctx, h.Catalog, entity.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	facts.Dependencies.ExecutionConfigHash, err = h.overviewExecutionConfig(ctx, model, "", "")
	if err != nil {
		t.Fatal(err)
	}
	content := jsonBytes(overviewDocument{Blocks: []OverviewBlock{{Type: "markdown", Markdown: "Public facts."}}, Sources: overviewSources(facts)})
	shared, err := s.saveGenerated(ctx, GeneratedContent{ID: uuid.NewString(), EntityID: entity.ID, Locale: "en-US", ModelID: model.ID, ContextHash: overviewContextHash(facts, "en-US", model), Content: content, Dependencies: jsonBytes(facts.Dependencies)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.saveGenerated(ctx, GeneratedContent{ID: uuid.NewString(), EntityID: entity.ID, Locale: "en-US", ModelID: model.ID, OwnerID: owner, ContextHash: "old-personal-context", Content: content, Dependencies: jsonBytes(facts.Dependencies)}); err != nil {
		t.Fatal(err)
	}
	read := func(userID string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "entity_id", Value: entity.ID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/generated/"+entity.ID+"?locale=en-US", nil)
		if userID != "" {
			c.Set("catalog_user", &catalog.User{ID: userID})
		}
		h.getGenerated(c)
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode status=%d body=%s: %v", w.Code, w.Body.String(), err)
		}
		return w.Code, result
	}
	status, result := read(owner)
	if status != 200 || result["stale"] != false || result["item"].(map[string]any)["id"] != shared.ID {
		t.Fatalf("personal stale cache hid fresh shared: %d %+v", status, result)
	}
	if _, err = s.DB.Exec(`DELETE FROM assistant.generated_content WHERE owner_id=''`); err != nil {
		t.Fatal(err)
	}
	status, result = read("")
	if status != 200 || result["item"] != nil || result["stale"] != false {
		t.Fatalf("anonymous read private generation: %d %+v", status, result)
	}
	if _, err = s.DB.Exec(`UPDATE catalog.entities SET state='deleted' WHERE id=$1`, entity.ID); err != nil {
		t.Fatal(err)
	}
	status, result = read(owner)
	if status != 404 || result["item"] != nil {
		t.Fatalf("deleted root cache disclosed: %d %+v", status, result)
	}
	if _, err = h.Catalog.Get(ctx, entity.ID, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fixture visibility mismatch: %v", err)
	}
}
