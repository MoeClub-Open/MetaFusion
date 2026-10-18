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
	if positionGap([]int{1, 2, 3}) != "" || positionGap([]int{3, 1, 2}) != "" || positionGap([]int{1}) != "" {
		t.Fatal("contiguous from-1 positions flagged")
	}
	if positionGap([]int{0, 1, 2}) == "" {
		t.Fatal("0-based contiguous not flagged")
	}
	if positionGap([]int{2, 3, 4}) == "" || positionGap([]int{2}) == "" {
		t.Fatal("positions starting at 2 not flagged")
	}
	if positionGap([]int{1, 2, 4}) == "" || positionGap([]int{1, 1}) == "" {
		t.Fatal("gap/duplicate not flagged")
	}
}

func TestQualityAuditReleaseWithoutTracks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ref := func(name string) any { return map[string]string{"$ref": name} }
	op := func(kind, name string, extra map[string]any) WorkspaceOperation {
		doc := map[string]any{"kind": kind, "title": name, "translations": map[string]any{"en": map[string]any{"title": name}}}
		for k, v := range extra {
			doc[k] = v
		}
		return WorkspaceOperation{Target: "entity", Action: "create", Ref: name, Document: json.RawMessage(encode(doc))}
	}
	operations := []WorkspaceOperation{op("work", "w", nil),
		op("release", "r", map[string]any{"subjects": []any{map[string]any{"work_id": ref("w"), "role": "primary", "position": 0}}}),
		op("medium", "m", map[string]any{"release_id": ref("r"), "attributes": map[string]any{"format": "cd"}})}

	changes, commit, w := prepareWorkspace(t, f.s, f.u, operations...)
	if len(commit.Changes) != 3 || commit.MergedAt != nil {
		t.Fatalf("incomplete catalog data must remain committable: %+v", commit)
	}
	if _, err := f.s.Get(ctx, changes.Refs["r"], nil); err == nil {
		t.Fatal("working copy leaked before push")
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, WorkspacePush{ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID}, f.u)
	if err != nil || !out.Applied {
		t.Fatalf("incomplete catalog data must remain editable: %+v %v", out, err)
	}
	rep, err := f.s.EntityQuality(ctx, changes.Refs["r"], &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != QualityPartial || len(rep.Issues) == 0 {
		t.Fatalf("release report: %+v", rep)
	}
	batch, err := f.s.QualityBatch(ctx, QualityBatchRequest{IDs: []string{changes.Refs["w"], changes.Refs["r"], "bad", "00000000-0000-0000-0000-000000000000"}}, &f.u)
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

func TestQualityAuditMediumTrackPositionsStartAt2(t *testing.T) {
	// Regression: 《アルストロメリア》 style CD where top-level track numbers start at 2
	// (missing track 1). Auditing the medium alone must be partial with track_position_gap,
	// not complete. Within one medium, positions must be contiguous from 1.
	f := newFixture(t)
	ctx := context.Background()
	ref := func(name string) any { return map[string]string{"$ref": name} }
	op := func(kind, name string, extra map[string]any) WorkspaceOperation {
		doc := map[string]any{"kind": kind, "title": name, "translations": map[string]any{"en": map[string]any{"title": name}}}
		for k, v := range extra {
			doc[k] = v
		}
		return WorkspaceOperation{Target: "entity", Action: "create", Ref: name, Document: json.RawMessage(encode(doc))}
	}
	operations := []WorkspaceOperation{
		op("work", "w", nil),
		op("expression", "x1", map[string]any{"work_id": ref("w")}),
		op("expression", "x2", map[string]any{"work_id": ref("w")}),
		op("release", "r", map[string]any{"subjects": []any{map[string]any{"work_id": ref("w"), "role": "primary", "position": 0}}}),
		op("medium", "m", map[string]any{"release_id": ref("r"), "attributes": map[string]any{"format": "cd"}}),
		op("track", "t2", map[string]any{"medium_id": ref("m"), "position": 2, "number": "2", "contents": []any{map[string]any{"expression_id": ref("x1"), "position": 0}}}),
		op("track", "t3", map[string]any{"medium_id": ref("m"), "position": 3, "number": "3", "contents": []any{map[string]any{"expression_id": ref("x2"), "position": 0}}}),
	}
	changes, _, w := prepareWorkspace(t, f.s, f.u, operations...)
	_, err := f.s.PushWorkspace(ctx, w.ID, WorkspacePush{ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	mediumID := changes.Refs["m"]
	rep, err := f.s.EntityQuality(ctx, mediumID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != QualityPartial {
		t.Fatalf("medium with tracks 2..3 must be partial, got %+v", rep)
	}
	found := false
	for _, issue := range rep.Issues {
		if issue.Code == "track_position_gap" && issue.EntityID == mediumID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected track_position_gap on medium alone: %+v", rep.Issues)
	}
}

func TestQualityAuditMediumAllNonNumericTrackNumbers(t *testing.T) {
	// Synthetic fixture: printed track numbers are letters/roman only (no digit
	// positions like 1..n). Contiguous integer Position still orders them; quality
	// must not report track_position_gap or force partial solely for non-numeric numbers.
	f := newFixture(t)
	ctx := context.Background()
	ref := func(name string) any { return map[string]string{"$ref": name} }
	op := func(kind, name string, extra map[string]any) WorkspaceOperation {
		doc := map[string]any{"kind": kind, "title": name, "translations": map[string]any{"en": map[string]any{"title": name}}}
		for k, v := range extra {
			doc[k] = v
		}
		return WorkspaceOperation{Target: "entity", Action: "create", Ref: name, Document: json.RawMessage(encode(doc))}
	}
	operations := []WorkspaceOperation{
		op("work", "w", nil),
		op("expression", "x1", map[string]any{"work_id": ref("w")}),
		op("expression", "x2", map[string]any{"work_id": ref("w")}),
		op("expression", "x3", map[string]any{"work_id": ref("w")}),
		op("release", "r", map[string]any{"subjects": []any{map[string]any{"work_id": ref("w"), "role": "primary", "position": 0}}}),
		op("medium", "m", map[string]any{"release_id": ref("r"), "attributes": map[string]any{"format": "cd"}}),
		op("track", "ta", map[string]any{"medium_id": ref("m"), "position": 1, "number": "A", "contents": []any{map[string]any{"expression_id": ref("x1"), "position": 0}}}),
		op("track", "tb", map[string]any{"medium_id": ref("m"), "position": 2, "number": "B", "contents": []any{map[string]any{"expression_id": ref("x2"), "position": 0}}}),
		op("track", "tii", map[string]any{"medium_id": ref("m"), "position": 3, "number": "II", "contents": []any{map[string]any{"expression_id": ref("x3"), "position": 0}}}),
	}
	changes, _, w := prepareWorkspace(t, f.s, f.u, operations...)
	_, err := f.s.PushWorkspace(ctx, w.ID, WorkspacePush{ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	mediumID := changes.Refs["m"]
	rep, err := f.s.EntityQuality(ctx, mediumID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range rep.Issues {
		if issue.Code == "track_position_gap" {
			t.Fatalf("all-non-numeric track numbers must not report track_position_gap: %+v", rep)
		}
	}
	if rep.Status != QualityComplete || len(rep.Issues) != 0 {
		t.Fatalf("letter/roman track numbers must not force partial: %+v", rep)
	}
}
