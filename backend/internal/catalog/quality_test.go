package catalog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestQualityStatusAndPositionGap(t *testing.T) {
	if got := qualityStatus(nil); got != QualityComplete {
		t.Fatalf("empty: %s", got)
	}
	if got := qualityStatus([]QualityIssue{{Severity: qualitySeverityWarning}}); got != QualityPartial {
		t.Fatalf("warning: %s", got)
	}
	if got := qualityStatus([]QualityIssue{{Severity: qualitySeverityWarning}, {Severity: qualitySeverityError}}); got != QualityNeedsVerification {
		t.Fatalf("error: %s", got)
	}
	if positionGap([]int{0, 1, 2}) != "" || positionGap([]int{3, 1, 2}) != "" || positionGap([]int{1}) != "" {
		t.Fatal("contiguous positions flagged")
	}
	if positionGap([]int{1, 2, 4}) == "" || positionGap([]int{1, 1}) == "" {
		t.Fatal("gap/duplicate not flagged")
	}
}

func TestQualityAuditReleaseWithoutTracks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ref := func(name string) any { return map[string]string{"$ref": name} }
	op := func(kind, name string, extra map[string]any) CommitOperation {
		doc := map[string]any{"kind": kind, "title": name, "status": "published", "translations": map[string]any{"en": map[string]any{"title": name}}}
		for k, v := range extra {
			doc[k] = v
		}
		return CommitOperation{Target: "entity", Action: "create", Ref: name, Document: json.RawMessage(encode(doc)), ReviewedCandidateIDs: []string{}}
	}
	in := fixtureCommit(f, op("work", "w", nil),
		op("release", "r", map[string]any{"subjects": []any{map[string]any{"work_id": ref("w"), "role": "primary", "position": 0}}}),
		op("medium", "m", map[string]any{"release_id": ref("r"), "attributes": map[string]any{"format": "cd"}}))

	preview, err := f.s.PushCommit(ctx, in, f.u, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range preview.QualityWarnings {
		if w.Code == "medium_missing_tracks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preview quality_warnings missing medium_missing_tracks: %+v", preview.QualityWarnings)
	}

	out, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.QualityWarnings != nil {
		t.Fatal("applied commit must not carry quality_warnings")
	}
	rep, err := f.s.EntityQuality(ctx, out.Refs["r"], &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != QualityPartial || len(rep.Issues) == 0 {
		t.Fatalf("release report: %+v", rep)
	}
	batch, err := f.s.QualityBatch(ctx, QualityBatchRequest{IDs: []string{out.Refs["w"], out.Refs["r"], "bad", "00000000-0000-0000-0000-000000000000"}}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Reports) != 2 || batch.Errors["bad"] != "invalid_id" || batch.Errors["00000000-0000-0000-0000-000000000000"] != "not_found" {
		t.Fatalf("batch: %+v", batch)
	}
	if batch.Reports[0].Status != QualityComplete {
		t.Fatalf("work should have no auto-detected issue: %+v", batch.Reports[0])
	}
}
