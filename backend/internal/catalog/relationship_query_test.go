package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func queryInt(value int) *int { return &value }

func TestRelationshipQueryValidation(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		name string
		in   RelationshipQueryRequest
		code string
	}{
		{"missing ids", RelationshipQueryRequest{}, "invalid_ids"},
		{"too many ids", RelationshipQueryRequest{IDs: make([]string, 21)}, "invalid_ids"},
		{"bad id", RelationshipQueryRequest{IDs: []string{"broken"}}, "invalid_id"},
		{"empty id", RelationshipQueryRequest{IDs: []string{id, ""}}, "invalid_id"},
		{"direction", RelationshipQueryRequest{IDs: []string{id}, Direction: "recursive"}, "invalid_direction"},
		{"peer kind", RelationshipQueryRequest{IDs: []string{id}, PeerKinds: []string{"album"}}, "invalid_peer_kind"},
		{"zero limit", RelationshipQueryRequest{IDs: []string{id}, Limit: queryInt(0)}, "invalid_limit"},
		{"negative limit", RelationshipQueryRequest{IDs: []string{id}, Limit: queryInt(-1)}, "invalid_limit"},
		{"large limit", RelationshipQueryRequest{IDs: []string{id}, Limit: queryInt(101)}, "invalid_limit"},
		{"negative offset", RelationshipQueryRequest{IDs: []string{id}, Offset: queryInt(-1)}, "invalid_offset"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := normalizeRelationshipQuery(tc.in)
			if err == nil || err.Error() != tc.code {
				t.Fatalf("error=%v, want %s", err, tc.code)
			}
		})
	}
	in, limit, offset, err := normalizeRelationshipQuery(RelationshipQueryRequest{
		IDs:       []string{strings.ToUpper(id), " " + id + " "},
		RuleCodes: []string{"structure:release_subject", "structure:release_subject"},
		PeerKinds: []string{"work", "work"},
	})
	if err != nil || len(in.IDs) != 1 || in.IDs[0] != id || in.Direction != "both" || limit != 25 || offset != 0 || len(in.RuleCodes) != 1 || len(in.PeerKinds) != 1 {
		t.Fatalf("normalized=%+v, limit=%d offset=%d err=%v", in, limit, offset, err)
	}
	_, limit, offset, err = normalizeRelationshipQuery(RelationshipQueryRequest{IDs: []string{id}, Limit: queryInt(100), Offset: queryInt(10100)})
	if err != nil || limit != 100 || offset != 10100 {
		t.Fatalf("valid boundary rejected: %v", err)
	}
}

func TestRelationshipQueryUsesDynamicRegistry(t *testing.T) {
	d := Defaults()
	d.Relations["new_gui_rule"] = RelationDefinition{Enabled: true}
	d.Relations["historical_gui_rule"] = RelationDefinition{Enabled: false}
	for _, code := range []string{"structure:release_subject", "relation:new_gui_rule", "relation:historical_gui_rule"} {
		if err := validateRelationshipQueryRules(RelationshipQueryRequest{RuleCodes: []string{code}}, d); err != nil {
			t.Fatalf("registry code %s rejected: %v", code, err)
		}
	}
	for _, code := range []string{"new_gui_rule", "structure:missing", "relation:missing", ""} {
		if err := validateRelationshipQueryRules(RelationshipQueryRequest{RuleCodes: []string{code}}, d); err == nil || err.Error() != "invalid_rule_code" {
			t.Fatalf("invalid registry code %q: %v", code, err)
		}
	}
}

func TestRelationshipQueryStrictHTTPInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	for _, payload := range []string{
		`{"ids":[],"depth":2}`, `{"ids":[]} {}`, `{"ids":"bad"}`,
		`{"ids":[],"limit":1.5}`, `{"ids":[],"offset":"1"}`, `null`, `{}`, `{"ids":[]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/catalog/relationships/query", strings.NewReader(payload))
		req.RemoteAddr = "198.51.100.43:12345"
		r.ServeHTTP(w, req)
		if w.Code != 400 || strings.Contains(w.Body.String(), "internal_error") {
			t.Fatalf("payload %s: %d %s", payload, w.Code, w.Body.String())
		}
	}
}

func hideQueryEntity(t *testing.T, f fixture, id string) {
	t.Helper()
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='deleted',document=jsonb_set(document,'{state}','"draft"'::jsonb) WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func relationshipQueryFixtureDefinitions(d Definitions) Definitions {
	if d.Relations == nil {
		d.Relations = map[string]RelationDefinition{}
	}
	if d.Fields == nil {
		d.Fields = map[string]Field{}
	}
	d.Relations["query_link"] = RelationDefinition{Names: names4("查询边", "查詢邊", "照会リンク", "Query link"), ReverseNames: names4("被查询", "被查詢", "逆リンク", "Linked by"), SourceKinds: []string{"agent"}, TargetKinds: []string{"agent"}, Fields: []string{"character"}, ParticipantSlot: "peer", Enabled: true}
	for _, code := range []string{"locator", "inclusion_attributes", "subject_attributes"} {
		field := d.Fields[code]
		// Empty fields are omitted during JSON persistence and decode as nil.
		if field.Fields == nil {
			field.Fields = map[string]Field{}
		}
		field.Fields["query_reference"] = Field{Names: names4("引用", "引用", "参照", "Reference"), Type: "entity", Kinds: []string{"agent"}, Enabled: true}
		d.Fields[code] = field
	}
	return d
}

func relationshipQueryFixtureContents(expressionID, hiddenExpressionID, hiddenReferenceID string) []Inclusion {
	return []Inclusion{
		{ExpressionID: hiddenExpressionID, Position: 0},
		{ExpressionID: expressionID, Position: 1, Locator: Locator{"relative_to": "track", "path": "hidden-attributes"}, Attributes: map[string]any{"query_reference": hiddenReferenceID}},
		{ExpressionID: expressionID, Position: 2, Locator: Locator{"relative_to": "track", "query_reference": hiddenReferenceID}},
		{ExpressionID: expressionID, Position: 3},
	}
}

// Run schema validation even when PostgreSQL is unavailable, so the database
// fixture exercises a GUI-compatible rule instead of failing before its query.
func TestRelationshipQueryExtensibleDefinition(t *testing.T) {
	var persisted Definitions
	if err := json.Unmarshal([]byte(encode(Defaults())), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Fields["inclusion_attributes"].Fields != nil || persisted.Fields["subject_attributes"].Fields != nil {
		t.Fatal("JSON round trip should exercise omitted empty record field maps")
	}
	for name, d := range map[string]Definitions{"defaults": Defaults(), "persisted": persisted} {
		t.Run(name, func(t *testing.T) {
			d = relationshipQueryFixtureDefinitions(d)
			if err := d.Validate(); err != nil {
				t.Fatalf("new relationship/record-level reference configuration: %v", err)
			}
			expressionID, hiddenExpressionID, hiddenReferenceID, workID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			kinds := map[string]string{expressionID: "expression", hiddenExpressionID: "expression", hiddenReferenceID: "agent", workID: "work"}
			ref := func(id string, allowed []string) error {
				if !contains(allowed, kinds[id]) {
					t.Fatalf("unexpected fixture reference %s allowed=%v", id, allowed)
				}
				return nil
			}
			for _, entity := range []Entity{
				{Kind: "release", Title: "query release", State: "active", Translations: map[string]Translation{"en": {Title: "query release"}}, Subjects: []Subject{{WorkID: workID, Role: "primary", Attributes: map[string]any{"query_reference": hiddenReferenceID}}}},
				{Kind: "track", Title: "query track", State: "active", MediumID: uuid.NewString(), Translations: map[string]Translation{"en": {Title: "query track"}}, Contents: relationshipQueryFixtureContents(expressionID, hiddenExpressionID, hiddenReferenceID)},
			} {
				if err := d.validateEntity(entity, ref, false); err != nil {
					t.Fatalf("complete %s fixture must satisfy writes: %v", entity.Kind, err)
				}
			}
		})
	}
	empty := relationshipQueryFixtureDefinitions(Definitions{})
	if empty.Relations["query_link"].ParticipantSlot != "peer" || len(empty.Fields) != 3 {
		t.Fatal("fixture should initialize missing optional maps")
	}
}

func TestPostgresRelationshipQueryFiltersBeforePagination(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := relationshipQueryFixtureDefinitions(v.Document)
	f.publish(d, v.ETag)
	subject := f.save(Entity{Kind: "agent", Title: "query subject"})
	peer := f.save(Entity{Kind: "agent", Title: "visible peer"})
	hidden := f.save(Entity{Kind: "agent", Title: "hidden reference"})
	for _, relation := range []Relation{
		{Type: "query_link", SourceID: subject.ID, TargetID: hidden.ID},
		{Type: "query_link", SourceID: subject.ID, TargetID: peer.ID, Position: 1, Attributes: map[string]any{"character": hidden.ID}},
		{Type: "query_link", SourceID: subject.ID, TargetID: peer.ID, Position: 2},
		{Type: "query_link", SourceID: subject.ID, TargetID: peer.ID, Position: 3, Attributes: map[string]any{"character": peer.ID}},
		{Type: "query_link", SourceID: peer.ID, TargetID: subject.ID},
	} {
		if _, err = f.s.SaveRelation(ctx, RelationEdit{Relation: relation, EditNote: "query fixture", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatal(err)
		}
	}
	work := f.save(Entity{Kind: "work", Title: "query work"})
	exp := f.save(Entity{Kind: "expression", WorkID: work.ID, Title: "visible expression"})
	hiddenExp := f.save(Entity{Kind: "expression", WorkID: work.ID, Title: "hidden expression"})
	release := f.save(Entity{Kind: "release", Title: "query release", Subjects: []Subject{{WorkID: work.ID, Role: "primary", Attributes: map[string]any{"query_reference": hidden.ID}}}})
	medium := f.save(Entity{Kind: "medium", ReleaseID: release.ID, Title: "query medium"})
	track := f.save(Entity{Kind: "track", MediumID: medium.ID, Title: "query track", Contents: relationshipQueryFixtureContents(exp.ID, hiddenExp.ID, hidden.ID)})
	hideQueryEntity(t, f, hidden.ID)
	hideQueryEntity(t, f, hiddenExp.ID)
	missing := uuid.NewString()
	in := RelationshipQueryRequest{IDs: []string{subject.ID, subject.ID, hidden.ID, missing}, Direction: "outgoing", RuleCodes: []string{"relation:query_link"}, PeerKinds: []string{"agent"}, Limit: queryInt(1)}
	out, err := f.s.QueryRelationships(ctx, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || len(out.UnavailableIDs) != 2 || out.UnavailableIDs[0] != hidden.ID || out.UnavailableIDs[1] != missing || len(out.Entities) != 2 || out.DefinitionETag == "" {
		t.Fatalf("batch visibility contract: %+v", out)
	}
	page := out.Pages[0]
	if len(page.Items) != 1 || page.Items[0].Position != 2 || page.Items[0].Direction != "outgoing" || !page.HasMore {
		t.Fatalf("visibility must precede pagination: %+v", page)
	}
	in.Offset = queryInt(1)
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || out.Pages[0].Items[0].Position != 3 || out.Pages[0].HasMore {
		t.Fatalf("second visible page: %+v err=%v", out, err)
	}
	in.Offset, in.Direction = nil, "incoming"
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 1 || out.Pages[0].Items[0].SourceID != peer.ID || out.Pages[0].Items[0].Direction != "incoming" {
		t.Fatalf("incoming filter: %+v err=%v", out, err)
	}
	in.PeerKinds = []string{"work"}
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 0 || len(out.Entities) != 1 {
		t.Fatalf("peer kind filter: %+v err=%v", out, err)
	}
	owner := User{ID: f.u.ID}
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{hidden.ID}}, &owner)
	if err != nil || len(out.Pages) != 1 || len(out.UnavailableIDs) != 0 || out.Entities[hidden.ID].ID != hidden.ID {
		t.Fatalf("creator should see own draft: %+v err=%v", out, err)
	}
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{work.ID}, Direction: "outgoing", PeerKinds: []string{"expression"}}, nil)
	if err != nil || len(out.Pages[0].Items) != 1 || out.Pages[0].Items[0].TargetID != exp.ID || out.Pages[0].Items[0].RuleCode != "structure:work_expression" {
		t.Fatalf("structural direction/kind filter: %+v err=%v", out, err)
	}
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{release.ID, track.ID}, Direction: "outgoing", RuleCodes: []string{"structure:release_subject", "structure:track_content"}, Limit: queryInt(1)}, nil)
	if err != nil || len(out.Pages) != 2 || len(out.Pages[0].Items) != 0 || len(out.Pages[1].Items) != 1 || out.Pages[1].Items[0].Position != 3 || out.Pages[1].HasMore {
		t.Fatalf("hidden structural references: %+v err=%v", out, err)
	}
	encoded := encode(out)
	if strings.Contains(encoded, hidden.ID) || strings.Contains(encoded, hiddenExp.ID) || strings.Contains(encoded, "query_reference") || strings.Contains(encoded, `"contents"`) || strings.Contains(encoded, `"subjects"`) {
		t.Fatalf("hidden or unpaged structural records leaked: %s", encoded)
	}
	if _, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{work.ID}, RuleCodes: []string{"relation:unknown_gui_rule"}}, nil); err == nil || err.Error() != "invalid_rule_code" {
		t.Fatalf("unknown live rule: %v", err)
	}
	// The single-subject endpoint returns full records; the batch endpoint
	// intentionally returns summaries to avoid unrelated hidden references.
	single, err := f.s.EntityLinks(ctx, work.ID, 100, 0, nil)
	if err != nil || single.SubjectID != work.ID || single.Entities[work.ID].Kind != "work" || len(single.Items) == 0 {
		t.Fatalf("single-subject records: %+v err=%v", single, err)
	}
}

func TestPostgresRelationshipQuerySnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "snapshot work"})
	expression := f.save(Entity{Kind: "expression", WorkID: work.ID, Title: "snapshot expression"})
	tx, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := definitions(ctx, tx) // Establish the snapshot before changes.
	if err != nil {
		t.Fatal(err)
	}
	hideQueryEntity(t, f, expression.ID)
	newETag := uuid.NewString()
	if _, err = f.s.DB.Exec("UPDATE catalog.definition_config SET etag=$1 WHERE singleton=true", newETag); err != nil {
		t.Fatal(err)
	}
	in, limit, offset, err := normalizeRelationshipQuery(RelationshipQueryRequest{IDs: []string{work.ID, expression.ID}, RuleCodes: []string{"structure:work_expression"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := queryRelationshipsFrom(ctx, tx, in, limit, offset, nil)
	if err != nil || out.DefinitionETag != before.ETag || len(out.Pages) != 2 || len(out.Entities) != 2 || len(out.Pages[0].Items) != 1 {
		t.Fatalf("mixed snapshot: %+v err=%v", out, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || out.DefinitionETag != newETag || len(out.Pages) != 1 || len(out.UnavailableIDs) != 1 || out.UnavailableIDs[0] != expression.ID || len(out.Pages[0].Items) != 0 {
		t.Fatalf("later snapshot: %+v err=%v", out, err)
	}
}

func TestOpenAPIRelationshipQuery(t *testing.T) {
	doc := OpenAPI()
	paths := doc["paths"].(map[string]any)
	op := paths["/catalog/relationships/query"].(map[string]any)["post"].(map[string]any)
	if op["security"] != nil || op["responses"].(map[string]any)["429"] == nil {
		t.Fatal("query must support anonymous reads and document throttling")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	request := schemas["RelationshipQueryRequest"].(map[string]any)
	props := request["properties"].(map[string]any)
	if request["additionalProperties"] != false || props["ids"].(map[string]any)["maxItems"] != 20 || props["limit"].(map[string]any)["default"] != 25 || props["offset"].(map[string]any)["maximum"] != nil {
		t.Fatalf("query schema constraints drift: %+v", request)
	}
	entity := schemas["RelationshipEntity"].(map[string]any)["properties"].(map[string]any)
	if len(entity) != 6 || entity["attributes"] != nil || entity["contents"] != nil || entity["subjects"] != nil {
		t.Fatalf("summary must not leak full entity records: %+v", entity)
	}
	var shape map[string]any
	b, err := json.Marshal(RelationshipQueryResponse{Pages: []RelationshipQueryPage{}, Entities: map[string]RelationshipEntity{}, UnavailableIDs: []string{}})
	if err != nil || json.Unmarshal(b, &shape) != nil || shape["pages"] == nil || shape["entities"] == nil || shape["unavailable_ids"] == nil {
		t.Fatalf("empty response fields must be arrays/maps: %s err=%v", b, err)
	}
}
