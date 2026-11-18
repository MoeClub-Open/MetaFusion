package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestEvalTemporalBoundsHalfOpenPartialAndDisputed(t *testing.T) {
	// [valid_from, valid_to) 左闭右开：起始包含、结束不包含。
	halfOpen := temporalBounds{ValidFrom: "2021-02-04", ValidTo: "2023-08-01"}
	for _, tc := range []struct {
		at   string
		want temporalEvalState
	}{
		{"2021-02-03", temporalExcluded},
		{"2021-02-04", temporalConfirmed},
		{"2022-06-15", temporalConfirmed},
		{"2023-07-31", temporalConfirmed},
		{"2023-08-01", temporalExcluded},
		{"2023-09-01", temporalExcluded},
	} {
		if got := evalTemporalBounds(halfOpen, tc.at); got != tc.want {
			t.Fatalf("[2021-02-04, 2023-08-01) at %s: got=%v want=%v", tc.at, got, tc.want)
		}
	}

	// 仅知年月（2021-02）：月内具体日期为 disputed，月级本身或次月起为 confirmed，上月为 excluded。
	monthOnly := temporalBounds{ValidFrom: "2021-02"}
	if got := evalTemporalBounds(monthOnly, "2021-01-31"); got != temporalExcluded {
		t.Fatalf("before coarse month must be excluded, got %v", got)
	}
	if got := evalTemporalBounds(monthOnly, "2021-02"); got != temporalConfirmed {
		t.Fatalf("exact coarse month must be confirmed, got %v", got)
	}
	if got := evalTemporalBounds(monthOnly, "2021-02-10"); got != temporalDisputed {
		t.Fatalf("day inside coarse start month must be disputed, got %v", got)
	}
	if got := evalTemporalBounds(monthOnly, "2021-03-01"); got != temporalConfirmed {
		t.Fatalf("after coarse start month must be confirmed, got %v", got)
	}

	// 存在时间争议（earliest_date..latest_date + disputed）。
	disputed := temporalBounds{
		DatePrecision: "disputed",
		EarliestDate:  "2020-10",
		LatestDate:    "2020-12",
	}
	if got := evalTemporalBounds(disputed, "2020-09-30"); got != temporalExcluded {
		t.Fatalf("before earliest_date must be excluded, got %v", got)
	}
	if got := evalTemporalBounds(disputed, "2020-11-15"); got != temporalDisputed {
		t.Fatalf("inside disputed range must be disputed, got %v", got)
	}

	// 无起止日期的现值绝不能倒填为历史确认事实。
	if got := evalTemporalBounds(temporalBounds{}, "2022-01-01"); got != temporalExcluded {
		t.Fatalf("undated fact must not be backfilled, got %v", got)
	}
}

func TestSelectTemporalPictureRuleOrderAndAppIcon(t *testing.T) {
	relJP := uuid.NewString()
	candidates := []pictureCandidate{
		{
			ownerID:    "work-1",
			ownerKind:  "work",
			ownerStart: "2021-02-04",
			index:      0,
			picture:    Picture{URL: "https://example.test/undated-current.jpg", Role: "cover_art"},
		},
		{
			ownerID:    "work-1",
			ownerKind:  "work",
			ownerStart: "2021-02-04",
			index:      1,
			picture:    Picture{URL: "https://example.test/pinned-kv.jpg", Role: "key_visual", Pinned: true},
		},
		{
			ownerID:    "work-1",
			ownerKind:  "work",
			ownerStart: "2021-02-04",
			index:      2,
			picture:    Picture{URL: "https://example.test/v1-global.jpg", Role: "cover_art", ValidFrom: "2021-02-04", ValidTo: "2022-02-04"},
		},
		{
			ownerID:    relJP,
			ownerKind:  "release",
			ownerStart: "2021-02-04",
			index:      0,
			picture:    Picture{URL: "https://example.test/v1-jp.jpg", Role: "cover_art", Region: "JP", ReleaseID: relJP, ValidFrom: "2021-02-04", ValidTo: "2022-02-04"},
		},
		{
			ownerID:    relJP,
			ownerKind:  "release",
			ownerStart: "2021-02-04",
			index:      1,
			picture:    Picture{URL: "https://example.test/icon-v1.png", Role: "app_icon", Region: "JP", ReleaseID: relJP, ValidFrom: "2021-02-04", ValidTo: "2022-01-01"},
		},
		{
			ownerID:    relJP,
			ownerKind:  "release",
			ownerStart: "2021-02-04",
			index:      2,
			picture:    Picture{URL: "https://example.test/icon-v2.png", Role: "app_icon", Region: "JP", ReleaseID: relJP, ValidFrom: "2022-01-01"},
		},
	}

	// 早于实体起始日：即使有 pinned 或无区间当前图，也必须返回 unknown（不倒填）。
	pre := selectTemporalPicture(candidates, isCoverRole, "2020-12-01", TimelineScope{Region: "JP"})
	if pre.Status != "unknown" || pre.Picture != nil {
		t.Fatalf("before entity start must be unknown, got %+v", pre)
	}

	// 2021-06-01 + JP：命中有效区间且范围更特异的 JP 封面优先于通用有效图与 pinned。
	jp2021 := selectTemporalPicture(candidates, isCoverRole, "2021-06-01", TimelineScope{Region: "JP", ReleaseID: relJP})
	if jp2021.Status != "confirmed" || jp2021.Picture == nil || jp2021.Picture.URL != "https://example.test/v1-jp.jpg" {
		t.Fatalf("expected JP confirmed cover, got %+v", jp2021)
	}

	// 2022-02-04（valid_to 当天）：v1 区间已半开结束，无新 confirmed 封面时回退到 pinned，绝不选无区间的 undated-current。
	afterV1 := selectTemporalPicture(candidates, isCoverRole, "2022-02-04", TimelineScope{Region: "JP"})
	if afterV1.Status != "pinned" || afterV1.Picture == nil || afterV1.Picture.URL != "https://example.test/pinned-kv.jpg" {
		t.Fatalf("expected pinned fallback at valid_to boundary, got %+v", afterV1)
	}

	// app_icon 与封面独立按区间切换。
	isIcon := func(r string) bool { return r == "app_icon" }
	icon2021 := selectTemporalPicture(candidates, isIcon, "2021-12-31", TimelineScope{Region: "JP"})
	if icon2021.Status != "confirmed" || icon2021.Picture == nil || icon2021.Picture.URL != "https://example.test/icon-v1.png" {
		t.Fatalf("expected icon-v1 on 2021-12-31, got %+v", icon2021)
	}
	icon2022 := selectTemporalPicture(candidates, isIcon, "2022-01-01", TimelineScope{Region: "JP"})
	if icon2022.Status != "confirmed" || icon2022.Picture == nil || icon2022.Picture.URL != "https://example.test/icon-v2.png" {
		t.Fatalf("expected icon-v2 on 2022-01-01, got %+v", icon2022)
	}
}

func TestReconstructTitleStatusAndDiffSnapshots(t *testing.T) {
	work := Entity{
		ID:    uuid.NewString(),
		Kind:  "work",
		Title: "ブルーアーカイブ -Blue Archive-",
		Attributes: map[string]any{
			"begin_date": "2020-02-14",
			"former_names": []any{
				map[string]any{
					"name":       "Project MX",
					"valid_from": "2020-02-14",
					"valid_to":   "2020-07-17",
				},
			},
		},
	}
	if title, st, _ := reconstructEntityTitleAt(work, "2019-12-31", TimelineScope{}); st != "unknown" || title != "" {
		t.Fatalf("expected unknown before announcement, got title=%q status=%q", title, st)
	}
	if title, st, _ := reconstructEntityTitleAt(work, "2020-05-01", TimelineScope{}); st != "confirmed" || title != "Project MX" {
		t.Fatalf("expected Project MX during former_names window, got title=%q status=%q", title, st)
	}
	if title, st, _ := reconstructEntityTitleAt(work, "2020-07-17", TimelineScope{}); st != "confirmed" || title != work.Title {
		t.Fatalf("expected canonical title at valid_to handover, got title=%q status=%q", title, st)
	}

	rel := Entity{
		ID:    uuid.NewString(),
		Kind:  "release",
		Title: "蔚蓝档案（简中服）",
		Attributes: map[string]any{
			"edition_date": "2023-08-03",
			"attribute_history": []any{
				map[string]any{"field": "release_status", "value": "testing", "valid_from": "2023-06-22", "valid_to": "2023-07-06"},
				map[string]any{"field": "release_status", "value": "operating", "valid_from": "2023-08-03"},
			},
		},
	}
	d := Defaults()
	if attrs, states, _, _, _ := reconstructAttributesAt(d, rel, "2023-06-25", TimelineScope{}); states["release_status"] != "confirmed" || attrs["release_status"] != "testing" {
		t.Fatalf("expected testing status, got attrs=%#v states=%#v", attrs, states)
	}
	if attrs, states, _, _, _ := reconstructAttributesAt(d, rel, "2023-07-20", TimelineScope{}); states["release_status"] != "unknown" || attrs["release_status"] != nil {
		t.Fatalf("expected unknown status in gap between CBT and launch, got attrs=%#v states=%#v", attrs, states)
	}
	if attrs, states, _, _, _ := reconstructAttributesAt(d, rel, "2023-08-03", TimelineScope{}); states["release_status"] != "confirmed" || attrs["release_status"] != "operating" {
		t.Fatalf("expected operating on launch day, got attrs=%#v states=%#v", attrs, states)
	}

	agentA, agentB := uuid.NewString(), uuid.NewString()
	snapFrom := EntityAsOfResponse{
		EntityID: work.ID, Kind: "work", At: "2020-05-01",
		Title: "Project MX", TitleStatus: "confirmed",
		Cover:   TemporalPictureSelection{Status: "unknown"},
		AppIcon: TemporalPictureSelection{Status: "unknown"},
		Releases: []TemporalReleaseView{{
			ReleaseID: rel.ID, Region: "CN", Platform: "ios",
			ReleaseStatus: "testing", StatusState: "confirmed",
			Operators: []TemporalAgentFact{{AgentID: agentA, Title: "Op A"}}, OperatorState: "confirmed",
		}},
		ActiveRelations: []EntityLink{{Key: "r1", RuleCode: "developed_by", SourceID: work.ID, TargetID: agentA}},
	}
	snapTo := EntityAsOfResponse{
		EntityID: work.ID, Kind: "work", At: "2023-09-01",
		Title: work.Title, TitleStatus: "confirmed",
		Cover:   TemporalPictureSelection{Status: "confirmed", Picture: &Picture{URL: "https://example.test/kv.jpg"}},
		AppIcon: TemporalPictureSelection{Status: "confirmed", Picture: &Picture{URL: "https://example.test/icon.png"}},
		Releases: []TemporalReleaseView{{
			ReleaseID: rel.ID, Region: "CN", Platform: "ios",
			ReleaseStatus: "operating", StatusState: "confirmed",
			Operators: []TemporalAgentFact{{AgentID: agentB, Title: "Op B"}}, OperatorState: "confirmed",
		}},
		ActiveRelations: []EntityLink{{Key: "r2", RuleCode: "developed_by", SourceID: work.ID, TargetID: agentB}},
	}
	fDiffs := diffTemporalSnapshots(snapFrom, snapTo)
	changedFields := []string{}
	for _, d := range fDiffs {
		changedFields = append(changedFields, d.Field)
	}
	for _, want := range []string{"title", "cover", "app_icon", "release.release_status", "release.operators"} {
		if !slices.Contains(changedFields, want) {
			t.Fatalf("missing expected field diff %q in %v", want, changedFields)
		}
	}
	rDiffs := diffTemporalRelations(snapFrom.ActiveRelations, snapTo.ActiveRelations)
	if len(rDiffs) != 2 {
		t.Fatalf("expected 2 relation diffs (removed+added), got %+v", rDiffs)
	}
}

func TestTemporalDefinitionsAppendOnlyPicturesAndHTTPRoutes(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("Defaults().Validate() failed: %v", err)
	}
	for _, relCode := range []string{"published_by", "operated_by"} {
		relDef, ok := d.Relations[relCode]
		if !ok || !slices.Contains(relDef.SourceKinds, "release") || !slices.Contains(relDef.TargetKinds, "agent") {
			t.Fatalf("relation %s must map release -> agent: %+v", relCode, relDef)
		}
	}
	if _, ok := d.Vocabularies["picture_role"].Terms["app_icon"]; !ok {
		t.Fatal("picture_role must include app_icon")
	}
	for _, evCode := range []string{"announcement", "testing", "official_release", "version_update", "visual_change", "rename", "publisher_change", "operator_change", "service_end", "service_resume", "major_event"} {
		if _, ok := d.Vocabularies["event_type"].Terms[evCode]; !ok {
			t.Fatalf("event_type missing %s", evCode)
		}
		if _, ok := d.Vocabularies["embedded_event_kind"].Terms[evCode]; !ok {
			t.Fatalf("embedded_event_kind missing %s", evCode)
		}
	}

	// 同一 URL 在不同有效期区间复出及同日换图合法；但倒置区间拒绝。
	relID := uuid.NewString()
	var checkedReleaseRef string
	refCheck := func(id string, kinds []string) error {
		if slices.Contains(kinds, "release") {
			checkedReleaseRef = id
		}
		return nil
	}
	p1 := Picture{URL: "https://example.test/icon-a.png", Role: "app_icon", ValidFrom: "2021-01-01", ValidTo: "2022-01-01", ReleaseID: relID}
	p2 := Picture{URL: "https://example.test/icon-b.png", Role: "app_icon", ValidFrom: "2022-01-01", ValidTo: "2022-01-01", ReleaseID: relID}
	p3 := Picture{URL: "https://example.test/icon-a.png", Role: "app_icon", ValidFrom: "2023-01-01", ReleaseID: relID}
	if err := d.validateEntityContent(pictureOnlyEntity(p1, p2, p3), refCheck, false); err != nil {
		t.Fatalf("append-only multi-icon and distinct intervals should pass: %v", err)
	}
	if checkedReleaseRef != relID {
		t.Fatalf("Picture.ReleaseID was not checked via reference callback, got %q", checkedReleaseRef)
	}
	reversedInterval := Picture{URL: "https://example.test/bad.png", Role: "app_icon", ValidFrom: "2022-02-01", ValidTo: "2022-01-01"}
	if err := d.validateEntityContent(pictureOnlyEntity(reversedInterval), allowAllRef, false); err == nil || err.Error() != "invalid_picture_period" {
		t.Fatalf("reversed [valid_from, valid_to) must fail with invalid_picture_period, got %v", err)
	}

	// HTTP 参数校验层：非法 UUID / 缺失 at / 反转区间在访问数据库前即返回明确 400 机器码。
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	HTTP{Store: &Store{}}.Register(engine)
	validID := uuid.NewString()
	for _, tc := range []struct {
		url      string
		wantCode string
	}{
		{"/api/catalog/entities/not-a-uuid/timeline", "invalid_id"},
		{"/api/catalog/entities/" + validID + "/timeline?from=2025-05&to=2024-01", "invalid_date_range"},
		{"/api/catalog/entities/" + validID + "/as-of", "at_required"},
		{"/api/catalog/entities/" + validID + "/as-of?at=2025-13-01", "invalid_date"},
		{"/api/catalog/entities/" + validID + "/compare?from=2024-01", "invalid_date"},
		{"/api/catalog/entities/" + validID + "/compare?from=2025-01&to=2023-01", "invalid_date_range"},
		{"/api/catalog/entities/" + validID + "/pictures?at=bad", "invalid_date"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status=%d want=400 body=%s", tc.url, rec.Code, rec.Body.String())
		}
		var resp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp["error"] != tc.wantCode {
			t.Fatalf("GET %s error=%q want=%q", tc.url, resp["error"], tc.wantCode)
		}
	}
}

func TestPostgresTemporalTimelineAsOfCompareAndAssetRetention(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	dev := f.save(Entity{Kind: "agent", Title: "NEXON Games", Attributes: map[string]any{"agent_type": "organization"}})
	pubJP := f.save(Entity{Kind: "agent", Title: "株式会社Yostar", Attributes: map[string]any{"agent_type": "organization"}})
	opOld := f.save(Entity{Kind: "agent", Title: "Old Operator", Attributes: map[string]any{"agent_type": "organization"}})
	opNew := f.save(Entity{Kind: "agent", Title: "New Operator", Attributes: map[string]any{"agent_type": "organization"}})

	oldIconAsset := uuid.NewString()
	newIconAsset := uuid.NewString()
	work := f.save(Entity{
		Kind:  "work",
		Title: "ブルーアーカイブ",
		Attributes: map[string]any{
			"creation_form": "game",
			"begin_date":    "2020-02-14",
			"former_names": []any{
				map[string]any{"name": "Project MX", "valid_from": "2020-02-14", "valid_to": "2020-07-17"},
			},
			"events": []any{
				map[string]any{"event_kind": "rename", "date": "2020-07-17", "label": map[string]any{"zh-CN": "正式定名"}},
			},
		},
		Pictures: PicturesJSON{
			{URL: "/api/storage/assets/" + oldIconAsset + "/content", AssetID: oldIconAsset, Role: "app_icon", ValidFrom: "2021-02-04", ValidTo: "2022-02-04"},
		},
	})
	// 即使后续版本不再出现在当前 pictures 数组中，历史修订的旧图片资产仍须被生命周期快照保留。
	work.Pictures = PicturesJSON{
		{URL: "/api/storage/assets/" + newIconAsset + "/content", AssetID: newIconAsset, Role: "app_icon", ValidFrom: "2022-02-04"},
		{URL: "/api/storage/assets/" + oldIconAsset + "/content", AssetID: oldIconAsset, Role: "app_icon", ValidFrom: "2021-02-04", ValidTo: "2022-02-04"},
	}
	var err error
	work, err = f.s.Save(ctx, Edit{Entity: work, ExpectedVersion: work.Version, EditNote: "append new icon", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}

	lifeSnap, err := f.s.currentStorageLifecycle(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(lifeSnap.CoverAssetIDs, oldIconAsset) || !slices.Contains(lifeSnap.CoverAssetIDs, newIconAsset) {
		t.Fatalf("expected both historical and current asset IDs retained, got %v", lifeSnap.CoverAssetIDs)
	}

	relJP := f.save(Entity{
		Kind:     "release",
		Title:    "ブルーアーカイブ（日本版）",
		Subjects: []Subject{{WorkID: work.ID}},
		Attributes: map[string]any{
			"edition_date":   "2021-02-04",
			"country":        "JP",
			"platform":       "ios/android",
			"release_status": "operating",
			"publisher":      pubJP.ID,
			"attribute_history": []any{
				map[string]any{"field": "release_status", "value": "testing", "valid_from": "2020-08-06", "valid_to": "2020-08-13"},
				map[string]any{"field": "release_status", "value": "operating", "valid_from": "2021-02-04"},
			},
			"events": []any{
				map[string]any{"event_kind": "official_release", "date": "2021-02-04", "region": "JP", "label": map[string]any{"zh-CN": "日服正式开服"}},
			},
		},
	})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "developed_by", SourceID: work.ID, TargetID: dev.ID, Attributes: map[string]any{"valid_from": "2020-02-14"}},
		EditNote: "dev", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "operated_by", SourceID: relJP.ID, TargetID: opOld.ID, Position: 1, Attributes: map[string]any{"valid_from": "2021-02-04", "valid_to": "2023-05-01", "region": "JP"}},
		EditNote: "op1", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "operated_by", SourceID: relJP.ID, TargetID: opNew.ID, Position: 2, Attributes: map[string]any{"valid_from": "2023-05-01", "region": "JP"}},
		EditNote: "op2", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}

	tl, err := f.s.EntityTimeline(ctx, work.ID, TimelineQuery{}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Items) != 2 {
		t.Fatalf("expected 2 timeline events on Work (rename + JP release), got %d: %+v", len(tl.Items), tl.Items)
	}

	cmp, err := f.s.EntityTemporalCompare(ctx, work.ID, TemporalCompareQuery{From: "2021-06-01", To: "2023-06-01", Region: "JP"}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmp.FromSnapshot.Operators) != 1 || cmp.FromSnapshot.Operators[0].AgentID != opOld.ID {
		t.Fatalf("expected opOld in 2021-06-01 snapshot, got %+v", cmp.FromSnapshot.Operators)
	}
	if len(cmp.ToSnapshot.Operators) != 1 || cmp.ToSnapshot.Operators[0].AgentID != opNew.ID {
		t.Fatalf("expected opNew in 2023-06-01 snapshot, got %+v", cmp.ToSnapshot.Operators)
	}

	// 验证存量迁移只读核查：Work 上的地区运营 produced_by 识别为 operated_by 候选；
	// 被旧版覆盖的图片出现在 AuditHistoricalRevisionPictures 且绝不把修订时间冒充 valid_from/valid_to。
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "produced_by", SourceID: work.ID, TargetID: pubJP.ID, Attributes: map[string]any{"credit_role": "日服代理运营", "region": "JP"}},
		EditNote: "legacy work operator", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	ownershipIssues, err := f.s.AuditWorkReleaseOwnership(ctx, []string{work.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownershipIssues) != 1 || ownershipIssues[0].SuggestedRelation != "operated_by" || !slices.Contains(ownershipIssues[0].CandidateReleases, relJP.ID) {
		t.Fatalf("unexpected ownership audit issues: %+v", ownershipIssues)
	}

	overwrittenAsset := uuid.NewString()
	relWithOldCover := f.save(Entity{
		Kind:     "release",
		Title:    "ブルーアーカイブ（グローバル版）",
		Subjects: []Subject{{WorkID: work.ID}},
		Pictures: PicturesJSON{{URL: "/api/storage/assets/" + overwrittenAsset + "/content", AssetID: overwrittenAsset, Role: "cover_art"}},
	})
	relWithOldCover.Pictures = PicturesJSON{{URL: "https://example.test/new-global-cover.jpg", Role: "cover_art"}}
	if _, err := f.s.Save(ctx, Edit{Entity: relWithOldCover, ExpectedVersion: relWithOldCover.Version, EditNote: "replace cover", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	histPicCandidates, err := f.s.AuditHistoricalRevisionPictures(ctx, []string{relWithOldCover.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(histPicCandidates) != 1 || histPicCandidates[0].SuggestedPicture.AssetID != overwrittenAsset {
		t.Fatalf("expected 1 unarchived historical picture candidate, got %+v", histPicCandidates)
	}
	if histPicCandidates[0].SuggestedPicture.ValidFrom != "" || histPicCandidates[0].SuggestedPicture.ValidTo != "" {
		t.Fatalf("revision timestamp must never be copied into valid_from/valid_to: %+v", histPicCandidates[0].SuggestedPicture)
	}
}

func TestUniversalNonPictureTemporalReconstructionAndCompare(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	parentOld := f.save(Entity{Kind: "agent", Title: "Microsoft", Attributes: map[string]any{"agent_type": "organization", "formation_date": "1975-04-04"}})
	parentNew := f.save(Entity{Kind: "agent", Title: "Sony Interactive Entertainment", Attributes: map[string]any{"agent_type": "organization", "formation_date": "1993-11-16"}})
	studio := f.save(Entity{
		Kind:             "agent",
		Title:            "Bungie, Inc.",
		OriginalLanguage: "en-US",
		Translations: map[string]Translation{
			"zh-CN": {Title: "棒鸡工作室", Nonofficial: true},
		},
		Attributes: map[string]any{
			"agent_type":       "organization",
			"formation_date":   "1991-05-01",
			"dissolution_date": "2099-12-31",
			"occupations":      []any{"game_developer", "live_service_publisher"},
			"former_names": []any{
				map[string]any{"name": "Bungie Software Products Corporation", "valid_from": "1991-05-01", "valid_to": "2000-06-19"},
				map[string]any{"name": "Bungie软体", "language": "zh-CN", "nonofficial": true, "valid_from": "1995-01-01", "valid_to": "2007-10-05"},
			},
			"attribute_history": []any{
				map[string]any{"field": "occupations", "value": "mac_game_developer", "valid_from": "1991-05-01", "valid_to": "2000-06-19"},
			},
		},
		ExternalLinks: []ExternalLink{
			{Kind: "website", URL: "https://legacy.bungie.example", ValidFrom: "1997-01-01", ValidTo: "2007-10-05"},
			{Kind: "website", URL: "https://www.bungie.net", ValidFrom: "2007-10-05"},
		},
	})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "subsidiary_of", SourceID: studio.ID, TargetID: parentOld.ID, Attributes: map[string]any{"valid_from": "2000-06-19", "valid_to": "2007-10-05"}},
		EditNote: "msft acquisition", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "subsidiary_of", SourceID: studio.ID, TargetID: parentNew.ID, Attributes: map[string]any{"valid_from": "2022-07-15"}},
		EditNote: "sie acquisition", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}

	// 1998 年快照：旧英文主名、旧中文译名、旧职业列表、旧官网，且尚未被收购、dissolution_date 未来值不提前暴露。
	snap1998, err := f.s.EntityAsOf(ctx, studio.ID, TemporalAsOfQuery{At: "1998-06-01"}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if snap1998.Title != "Bungie Software Products Corporation" {
		t.Fatalf("1998 title got=%q", snap1998.Title)
	}
	if snap1998.Translations["zh-CN"].Title != "Bungie软体" {
		t.Fatalf("1998 zh-CN translation got=%+v", snap1998.Translations["zh-CN"])
	}
	occ1998, _ := snap1998.Attributes["occupations"].([]any)
	if len(occ1998) != 1 || occ1998[0] != "mac_game_developer" {
		t.Fatalf("1998 occupations got=%#v", snap1998.Attributes["occupations"])
	}
	if _, hasDissolve := snap1998.Attributes["dissolution_date"]; hasDissolve {
		t.Fatalf("future dissolution_date must not appear in 1998 snapshot: %#v", snap1998.Attributes)
	}
	if len(snap1998.ExternalLinks) != 1 || snap1998.ExternalLinks[0].URL != "https://legacy.bungie.example" {
		t.Fatalf("1998 external_links got=%+v", snap1998.ExternalLinks)
	}
	if len(snap1998.ActiveRelations) != 0 {
		t.Fatalf("expected 0 active relations in 1998, got=%+v", snap1998.ActiveRelations)
	}

	// 2005 vs 2023 对比：标题/译名/职业已切换为现值，母公司关系从 Microsoft 变更为 Sony，官网切换。
	cmp, err := f.s.EntityTemporalCompare(ctx, studio.ID, TemporalCompareQuery{From: "1998-06-01", To: "2023-01-01"}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	changed := []string{}
	for _, d := range cmp.FieldChanges {
		changed = append(changed, d.Field)
	}
	for _, want := range []string{"title", "translations.zh-CN", "attributes.occupations", "external_links"} {
		if !slices.Contains(changed, want) {
			t.Fatalf("missing field diff %q in %v", want, changed)
		}
	}
	if len(cmp.RelationChanges) != 1 || cmp.RelationChanges[0].Change != "added" || cmp.RelationChanges[0].TargetID != parentNew.ID {
		t.Fatalf("expected subsidiary_of SIE added between 1998 and 2023, got %+v", cmp.RelationChanges)
	}

	// Work 年龄分级随时间调整（attribute_history enum 校验与重建）。
	ratedWork := f.save(Entity{
		Kind:  "work",
		Title: "Age Rating Shift Work",
		Attributes: map[string]any{
			"creation_form":  "game",
			"begin_date":     "2021-01-01",
			"content_rating": "r15",
			"attribute_history": []any{
				map[string]any{"field": "content_rating", "value": "general", "valid_from": "2021-01-01", "valid_to": "2022-10-15"},
			},
		},
	})
	w2021, err := f.s.EntityAsOf(ctx, ratedWork.ID, TemporalAsOfQuery{At: "2021-06-01"}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if w2021.Attributes["content_rating"] != "general" {
		t.Fatalf("expected general rating in 2021, got %#v", w2021.Attributes["content_rating"])
	}
	w2023, err := f.s.EntityAsOf(ctx, ratedWork.ID, TemporalAsOfQuery{At: "2023-01-01"}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if w2023.Attributes["content_rating"] != "r15" {
		t.Fatalf("expected r15 rating after 2022-10-15 handover, got %#v", w2023.Attributes["content_rating"])
	}
}
