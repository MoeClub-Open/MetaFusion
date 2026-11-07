package curator

import (
	"encoding/json"
	"testing"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

func rawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func pullRequestFor(base, next catalog.Entity) *catalog.PullRequest {
	return &catalog.PullRequest{ID: "pr", Changes: []catalog.WorkspaceObject{{
		Target: "entity", ID: base.ID, Base: rawJSON(base), Committed: rawJSON(next),
	}}}
}

func TestLowRiskTagsDedupe(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000010", Kind: "work", Title: "A",
		Attributes: map[string]any{"tags": []any{"a", "a", "b"}}}
	next := base
	next.Attributes = map[string]any{"tags": []any{"a", "b"}}
	ok, reason := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "tags")
	if !ok {
		t.Fatalf("expected low risk, got ok=%v reason=%s ", ok, reason)
	}
}

func TestLowRiskPicturesDedupe(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000011", Kind: "agent", Title: "A",
		Pictures: catalog.PicturesJSON{{URL: "u1"}, {URL: "u1"}, {URL: "u2"}}}
	next := base
	next.Pictures = catalog.PicturesJSON{{URL: "u1"}, {URL: "u2"}}
	ok, reason := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "pictures")
	if !ok {
		t.Fatalf("expected low risk, got ok=%v reason=%s ", ok, reason)
	}
}

func TestLowRiskRejectsTagAdditions(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000012", Kind: "work", Title: "A",
		Attributes: map[string]any{"tags": []any{"a"}}}
	next := base
	next.Attributes = map[string]any{"tags": []any{"a", "b"}}
	ok, _ := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "tags")
	if ok {
		t.Fatalf("expected rejection, got ok=%v ", ok)
	}
}

func TestLowRiskRejectsTitleChange(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000013", Kind: "work", Title: "A",
		Attributes: map[string]any{"tags": []any{"a", "a"}}}
	next := base
	next.Title = "B"
	next.Attributes = map[string]any{"tags": []any{"a"}}
	ok, _ := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "tags")
	if ok {
		t.Fatalf("expected rejection, got ok=%v ", ok)
	}
}

func TestLowRiskRejectsStateChange(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000013", Kind: "work", Title: "A", State: "active",
		Attributes: map[string]any{"tags": []any{"a", "a"}}}
	next := base
	next.State = "deleted"
	next.Attributes = map[string]any{"tags": []any{"a"}}
	if ok, _ := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "tags"); ok {
		t.Fatal("deduplication must not also change record state")
	}
}

func TestLowRiskRejectsMixedChange(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000014", Kind: "work", Title: "A",
		Attributes: map[string]any{"tags": []any{"a", "a"}}, Pictures: catalog.PicturesJSON{{URL: "u"}}}
	next := base
	next.Attributes = map[string]any{"tags": []any{"a"}}
	next.Pictures = catalog.PicturesJSON{{URL: "u"}, {URL: "u"}}
	ok, _ := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "tags")
	if ok {
		t.Fatalf("expected rejection, got ok=%v ", ok)
	}
}

func TestLowRiskRejectsChangesOutsideFinding(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000015", Kind: "work", Title: "A",
		Attributes: map[string]any{"tags": []any{"a", "a"}}}
	next := base
	next.Attributes = map[string]any{"tags": []any{"a"}}
	ok, reason := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: "00000000-0000-4000-8000-000000000016"}, "tags")
	if ok || reason != "outside_finding_scope" {
		t.Fatalf("expected scope rejection, got ok=%v reason=%s ", ok, reason)
	}
}

func TestLowRiskRejectsTrackRenumbering(t *testing.T) {
	base := catalog.Entity{ID: "00000000-0000-4000-8000-000000000017", Kind: "track", Title: "A", Position: 3}
	next := base
	next.Position = 2
	if ok, _ := lowRiskPR(pullRequestFor(base, next), Finding{EntityID: base.ID}, "position"); ok || autoMergeClass(CodeTrackPositionGap) != "" {
		t.Fatal("renumbering may hide missing tracks and must require review")
	}
}

func TestParseDecision(t *testing.T) {
	d := parseDecision("说明\n```json\n{\"decision\":\"no_change\",\"reason\":\"没有官方来源\",\"sources\":[\"https://example.com/x\",\"javascript:alert(1)\"],\"confidence\":\"high\"}\n```")
	if d.Decision != "no_change" || d.Reason != "没有官方来源" || len(d.Sources) != 1 || d.Sources[0] != "https://example.com/x" {
		t.Fatalf("unexpected decision: %#v", d)
	}
	if got := parseDecision("no json"); got.Decision != "" {
		t.Fatalf("expected empty decision, got %#v", got)
	}
}

func TestQualityCandidateEntityMapping(t *testing.T) {
	reports := []catalog.QualityReport{
		{EntityID: "release", Kind: "release", Issues: []catalog.QualityIssue{{Code: CodeReleaseMissingMedia, Severity: "warning", EntityID: "release"}}},
		{EntityID: "release", Kind: "release", Issues: []catalog.QualityIssue{{Code: CodeTrackPositionGap, Severity: "warning", EntityID: "medium"}}},
		{EntityID: "medium", Kind: "medium", Issues: []catalog.QualityIssue{{Code: CodeTrackEmptyContents, Severity: "warning", EntityID: "track"}}},
	}
	got := qualityCandidates(reports)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(got))
	}
	if got[0].EntityID != "release" || got[0].Kind != "release" {
		t.Fatalf("unexpected release candidate: %#v", got[0])
	}
	if got[1].EntityID != "medium" || got[1].Kind != "medium" {
		t.Fatalf("unexpected position candidate: %#v", got[1])
	}
	if got[2].EntityID != "track" || got[2].Kind != "track" {
		t.Fatalf("unexpected track candidate: %#v", got[2])
	}
}

func TestStableDedupeAndJSONEquality(t *testing.T) {
	if got := stableDedupe([]string{"a", "a", "b", "a"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected dedupe: %#v", got)
	}
	if !jsonEqual(map[string]any{}, map[string]any(nil)) {
		t.Fatal("empty object and null should compare equal for diff gating")
	}
	if !jsonEqual([]any{}, []any(nil)) {
		t.Fatal("empty array and null should compare equal for diff gating")
	}
}
