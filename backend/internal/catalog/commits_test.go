package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func patchValue(path string, value any) CommitPatch {
	return CommitPatch{Path: path, Value: json.RawMessage(encode(value))}
}

// commitFixtureSources 是提交测试用的来源池：source_refs 必填，来源必须带 key。
func commitFixtureSources() []Source {
	return []Source{{Kind: "url", Citation: "isolated acceptance fixture", URL: "https://example.org/fixture", Key: "fixture"}}
}

// withFixtureRefs 给没写 source_refs 的操作补上对 fixture 来源的引用（测试专用）。
func withFixtureRefs(in CatalogCommit) CatalogCommit {
	for i := range in.Operations {
		if len(in.Operations[i].SourceRefs) == 0 {
			in.Operations[i].SourceRefs = []SourceRef{{Source: "fixture"}}
		}
	}
	return in
}

func fixtureCommit(f fixture, ops ...CommitOperation) CatalogCommit {
	f.t.Helper()
	for i := range ops {
		if len(ops[i].SourceRefs) == 0 {
			ops[i].SourceRefs = []SourceRef{{Source: "fixture"}}
		}
		if ops[i].Target == "entity" && ops[i].Action == "create" && ops[i].ReviewedCandidateIDs == nil {
			ops[i].ReviewedCandidateIDs = []string{}
		}
	}
	v, err := f.s.Definitions(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return CatalogCommit{ID: uuid.NewString(), DefinitionsETag: v.ETag, EditNote: "commit fixture", Sources: commitFixtureSources(), Operations: ops}
}
func updateCommit(e Entity, patch ...CommitPatch) CommitOperation {
	op := CommitOperation{Target: "entity", Action: "update", ID: e.ID, BaseVersion: e.Version, Patch: patch}
	if commitNeedsIdentityReview(op) {
		op.ReviewedCandidateIDs = []string{}
	}
	return op
}

func TestCommitMergeAndPatchValidation(t *testing.T) {
	base := commitMap(map[string]any{"title": "base", "attributes": map[string]any{"language": "ja", "duration": 100}, "pictures": []any{}})
	current := commitMap(map[string]any{"title": "other", "attributes": map[string]any{"language": "ja", "duration": 100}, "pictures": []any{}})
	out, conflicts, changed, err := mergeCommitPatch(base, current, []CommitPatch{patchValue("/attributes/duration", 120)})
	if err != nil || len(conflicts) != 0 || !changed || string(out["title"]) != `"other"` {
		t.Fatalf("merge: %v %v %s", err, conflicts, encode(out))
	}
	_, conflicts, _, err = mergeCommitPatch(base, current, []CommitPatch{patchValue("/title", "mine")})
	if err != nil || len(conflicts) != 1 || conflicts[0].Pointer != "/title" || conflicts[0].Kind != "concurrent_edit" {
		t.Fatalf("conflict: %v %v", err, conflicts)
	}
	if conflicts[0].Base != "base" || conflicts[0].Current != "other" || conflicts[0].Proposed != "mine" {
		t.Fatalf("conflict values: %+v", conflicts[0])
	}
	if len(conflicts[0].Suggestions) == 0 || conflicts[0].Suggestions[0] != "manual_merge" {
		t.Fatalf("suggestions: %v", conflicts[0].Suggestions)
	}
	_, conflicts, changed, err = mergeCommitPatch(base, current, []CommitPatch{patchValue("/title", "other")})
	if err != nil || len(conflicts) != 0 || changed {
		t.Fatalf("same change: %v %v %v", err, conflicts, changed)
	}
	for _, path := range []string{"title", "/title/~2", "/", "/attributes//x"} {
		if _, err := pointerParts(path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	ref, err := resolveCommitRefs(json.RawMessage(`{"title":"$ref:work","work_id":{"$ref":"work"}}`), map[string]string{"work": "abc"})
	if err != nil || string(ref) != `{"title":"$ref:work","work_id":"abc"}` {
		t.Fatalf("refs: %s %v", ref, err)
	}
	if _, err := resolveCommitRefs(json.RawMessage(`{"$ref":"missing"}`), map[string]string{}); err == nil {
		t.Fatal("unknown ref")
	}
	in := CatalogCommit{ID: uuid.NewString(), DefinitionsETag: "etag", EditNote: "fixture", Sources: commitFixtureSources(), Operations: []CommitOperation{{Target: "entity", Action: "update", ID: uuid.NewString(), BaseVersion: 1, Patch: []CommitPatch{patchValue("/version", 2)}}}}
	if validateCommit(in) == nil {
		t.Fatal("server field accepted")
	}
	in.Operations[0].Patch = []CommitPatch{patchValue("/attributes", map[string]any{}), patchValue("/attributes/language", "ja")}
	if validateCommit(in) == nil {
		t.Fatal("overlapping patch accepted")
	}
}

func TestCommitFieldConflictKindsAndJSON(t *testing.T) {
	base := commitMap(map[string]any{"title": "base", "attributes": map[string]any{"language": "ja"}})
	current := commitMap(map[string]any{"title": "remote", "attributes": map[string]any{"language": "en"}})

	_, conflicts, _, err := mergeCommitPatch(base, current, []CommitPatch{patchValue("/title", "base")})
	if err != nil || len(conflicts) != 1 || conflicts[0].Kind != "stale_base" {
		t.Fatalf("stale_base: %v %+v", err, conflicts)
	}
	if conflicts[0].Suggestions[0] != "take_current" {
		t.Fatalf("stale suggestions: %v", conflicts[0].Suggestions)
	}

	_, conflicts, _, err = mergeCommitPatch(base, current, []CommitPatch{{Path: "/attributes/language", Remove: true}})
	if err != nil || len(conflicts) != 1 || !conflicts[0].Remove || conflicts[0].Kind != "concurrent_edit" {
		t.Fatalf("remove conflict: %v %+v", err, conflicts)
	}
	if conflicts[0].Proposed != nil || conflicts[0].Current != "en" || conflicts[0].Base != "ja" {
		t.Fatalf("remove values: %+v", conflicts[0])
	}

	payload := &commitConflictError{Conflict: CommitConflict{
		Operation: 0, ID: "11111111-2222-4333-8444-555555555555", BaseVersion: 1, CurrentVersion: 3,
		Conflicts: []FieldConflict{{
			Operation: 0, Pointer: "/title", Base: "base", Current: "remote", Proposed: "local",
			Kind: "concurrent_edit", Suggestions: fieldConflictSuggestions("concurrent_edit"),
		}},
	}}
	raw := encode(map[string]any{"error": "commit_conflict", "applied": false, "conflict": payload.Conflict, "conflicts": payload.Conflict.Conflicts})
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil {
		t.Fatal(raw)
	}
	if body["error"] != "commit_conflict" || body["applied"] != false {
		t.Fatalf("envelope: %s", raw)
	}
	conflictsAny, _ := body["conflicts"].([]any)
	if len(conflictsAny) != 1 {
		t.Fatalf("top-level conflicts: %s", raw)
	}
	fc, _ := conflictsAny[0].(map[string]any)
	for _, key := range []string{"operation", "pointer", "base", "current", "proposed", "kind", "suggestions"} {
		if _, ok := fc[key]; !ok {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
	conflictObj, _ := body["conflict"].(map[string]any)
	if _, ok := conflictObj["paths"]; ok {
		t.Fatalf("legacy paths present: %s", raw)
	}
}

func TestPostgresCommitAtomicPreviewReplayAndRefs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := map[string]any{"kind": "work", "title": "committed work", "status": "published", "translations": map[string]any{"en": map[string]any{"title": "committed work"}}}
	expression := map[string]any{"kind": "expression", "title": "committed expression", "status": "published", "translations": map[string]any{"en": map[string]any{"title": "committed expression"}}, "work_id": map[string]any{"$ref": "w"}}
	in := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "w", Document: json.RawMessage(encode(work))}, CommitOperation{Target: "entity", Action: "create", Ref: "x", Document: json.RawMessage(encode(expression))})
	preview, err := f.s.PushCommit(ctx, in, f.u, true)
	if err != nil || preview.Applied || len(preview.Items) != 2 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	var count int
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.entities`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview persisted entities: %d %v", count, err)
	}
	for _, table := range []string{"commits", "revisions", "outbox", "notifications"} {
		if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.`+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("preview persisted %s: %d %v", table, count, err)
		}
	}
	out, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil || !out.Applied {
		t.Fatalf("push: %+v %v", out, err)
	}
	x, err := f.s.Get(ctx, out.Refs["x"], &f.u)
	if err != nil || x.WorkID != out.Refs["w"] {
		t.Fatalf("resolved scope: %+v %v", x, err)
	}
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.revisions WHERE commit_id=$1`, in.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("provenance: %d %v", count, err)
	}
	replay, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil || encode(replay) != encode(out) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	receipt, err := f.s.CommitReceipt(ctx, in.ID, f.u)
	if err != nil || encode(receipt) != encode(out) {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	other := fixtureUser("editor")
	if _, err = f.s.CommitReceipt(ctx, in.ID, other); err == nil {
		t.Fatal("other actor read receipt")
	}
	in.EditNote = "mutated commit"
	if _, err = f.s.PushCommit(ctx, in, f.u, false); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("different payload: %v", err)
	}
	bad := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "w", Document: json.RawMessage(encode(work))}, CommitOperation{Target: "entity", Action: "create", Ref: "x", Document: json.RawMessage(`{"kind":"expression","title":"bad","work_id":{"$ref":"missing"}}`)})
	if _, err = f.s.PushCommit(ctx, bad, f.u, false); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.entities`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("partial batch persisted: %d %v", count, err)
	}
	// This failure occurs after the first write, not in preflight.
	bad = fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "w", Document: json.RawMessage(encode(work))}, CommitOperation{Target: "entity", Action: "create", Ref: "bad", Document: json.RawMessage(`{"kind":"work","title":"bad","attributes":{"undefined_field":true}}`)})
	if _, err = f.s.PushCommit(ctx, bad, f.u, false); err == nil {
		t.Fatal("invalid field succeeded")
	}
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.entities`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("write-path rollback: %d %v", count, err)
	}
}

func TestPostgresCommitThreeWayAndConcurrentPush(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	e := f.save(Entity{Kind: "work", Title: "base", Attributes: map[string]any{"language": "ja"}})
	a := fixtureCommit(f, updateCommit(e, patchValue("/title", "remote")))
	if _, err := f.s.PushCommit(ctx, a, f.u, false); err != nil {
		t.Fatal(err)
	}
	b := fixtureCommit(f, updateCommit(e, patchValue("/attributes/language", "en")))
	out, err := f.s.PushCommit(ctx, b, f.u, false)
	if err != nil || !out.Items[0].Merged {
		t.Fatalf("disjoint: %+v %v", out, err)
	}
	current, err := f.s.Get(ctx, e.ID, &f.u)
	if err != nil || current.Title != "remote" || current.Attributes["language"] != "en" || current.Version != 3 {
		t.Fatalf("lost update: %+v %v", current, err)
	}
	conflicting := fixtureCommit(f, updateCommit(e, patchValue("/title", "local")))
	_, err = f.s.PushCommit(ctx, conflicting, f.u, false)
	var conflict *commitConflictError
	if !errors.As(err, &conflict) || conflict.Conflict.CurrentVersion != 3 || len(conflict.Conflict.Conflicts) != 1 {
		t.Fatalf("overlap: %v", err)
	}
	fc := conflict.Conflict.Conflicts[0]
	if fc.Pointer != "/title" || fc.Kind != "concurrent_edit" || fc.Base != "base" || fc.Current != "remote" || fc.Proposed != "local" {
		t.Fatalf("field conflict: %+v", fc)
	}
	if fc.Operation != 0 || len(fc.Suggestions) == 0 || fc.Suggestions[0] != "manual_merge" {
		t.Fatalf("field conflict meta: %+v", fc)
	}
	identical := fixtureCommit(f, updateCommit(e, patchValue("/title", "remote")))
	out, err = f.s.PushCommit(ctx, identical, f.u, false)
	if err != nil || out.Items[0].Changed || out.Items[0].Version != 3 {
		t.Fatalf("no-op: %+v %v", out, err)
	}
	co, err := f.s.Checkout(ctx, CheckoutRequest{EntityIDs: []string{e.ID}}, f.u)
	if err != nil || len(co.Entities) != 1 || co.Entities[0].Version != 3 {
		t.Fatalf("checkout: %+v %v", co, err)
	}
	// Many pushes derived from one version modify separate language keys.
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := withFixtureRefs(CatalogCommit{ID: uuid.NewString(), DefinitionsETag: v.ETag, EditNote: "parallel", Sources: commitFixtureSources(), Operations: []CommitOperation{updateCommit(current, patchValue("/translations/"+[]string{"ja", "zh-CN", "zh-TW", "de", "fr", "es", "it", "ko", "pt", "ru", "nl", "ar"}[i], map[string]any{"title": fmt.Sprint(i)}))}})
			_, err := f.s.PushCommit(ctx, in, f.u, false)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err = f.s.Get(ctx, e.ID, &f.u)
	if err != nil || len(current.Translations) != 13 {
		t.Fatalf("concurrent merge lost fields: %d %v", len(current.Translations), err)
	}
}

func TestPostgresCommitConcurrentIdentityReviewAndReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := CommitOperation{Target: "entity", Action: "create", Ref: "work", ReviewedCandidateIDs: []string{}, Document: json.RawMessage(`{"kind":"work","title":"Same identity"}`)}
	makeCommit := func() CatalogCommit {
		return withFixtureRefs(CatalogCommit{ID: uuid.NewString(), DefinitionsETag: v.ETag, EditNote: "parallel identity", Sources: commitFixtureSources(), Operations: []CommitOperation{op}})
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.s.PushCommit(ctx, makeCommit(), f.u, false); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			var identity *commitIdentityError
			if !errors.As(err, &identity) {
				t.Fatal(err)
			}
			if len(identity.CandidateIDs) != 1 {
				t.Fatalf("review ids: %+v", identity)
			}
			conflicts++
		}
	}
	if success != 1 || conflicts != 7 {
		t.Fatalf("duplicate creates: success=%d conflicts=%d", success, conflicts)
	}
	in := makeCommit()
	in.Operations[0].Document = json.RawMessage(`{"kind":"work","title":"Replay identity"}`)
	results := make(chan CommitReceipt, 8)
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := f.s.PushCommit(ctx, in, f.u, false); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for r := range results {
		if id == "" {
			id = r.Refs["work"]
		}
		if r.Refs["work"] != id {
			t.Fatal("idempotent replay changed identity")
		}
	}
}

func TestPostgresCommitAtomicCatalogGraph(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := v.Document
	d.Relations["edited_by"] = RelationDefinition{Names: names("编辑", "Edited by"), ReverseNames: names("编辑了", "Editor of"), SourceKinds: []string{"work"}, TargetKinds: []string{"agent"}, ParticipantSlot: "person", CountsAsCredit: true, Enabled: true}
	f.publish(d, v.ETag)
	ref := func(name string) any { return map[string]string{"$ref": name} }
	entityOp := func(kind, name string, extra map[string]any) CommitOperation {
		doc := map[string]any{"kind": kind, "title": name, "status": "published", "translations": map[string]any{"en": map[string]any{"title": name}}}
		for k, v := range extra {
			doc[k] = v
		}
		return CommitOperation{Target: "entity", Action: "create", Ref: name, Document: json.RawMessage(encode(doc)), ReviewedCandidateIDs: []string{}}
	}
	ops := []CommitOperation{entityOp("agent", "a", nil), entityOp("collection", "c", nil), entityOp("work", "w", nil), entityOp("content_unit", "u", map[string]any{"work_id": ref("w")}), entityOp("expression", "x", map[string]any{"work_id": ref("w"), "content_unit_id": ref("u")}), entityOp("release", "r", map[string]any{"subjects": []any{map[string]any{"work_id": ref("w"), "role": "primary", "position": 0}}}), entityOp("medium", "m", map[string]any{"release_id": ref("r"), "attributes": map[string]any{"format": "cd"}}), entityOp("track", "t", map[string]any{"medium_id": ref("m"), "number": "1", "contents": []any{map[string]any{"expression_id": ref("x"), "position": 0}}}), {Target: "relation", Action: "create", Ref: "credit", Document: json.RawMessage(encode(map[string]any{"type": "edited_by", "source_id": ref("w"), "target_id": ref("a")}))}}
	in := fixtureCommit(f, ops...)
	out, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 9 {
		t.Fatalf("incomplete graph: %+v", out)
	}
	toc, err := f.s.ReleaseTableOfContents(ctx, out.Refs["r"], &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encode(toc), out.Refs["x"]) {
		t.Fatal("missing expression in TOC")
	}
	r, err := relationByID(ctx, f.s.DB, out.Refs["credit"])
	if err != nil || r.SourceID != out.Refs["w"] || r.TargetID != out.Refs["a"] {
		t.Fatalf("credit: %+v %v", r, err)
	}
	relUpdate := fixtureCommit(f, CommitOperation{Target: "relation", Action: "update", ID: r.ID, BaseVersion: r.Version, Patch: []CommitPatch{patchValue("/position", 2)}})
	if _, err = f.s.PushCommit(ctx, relUpdate, f.u, false); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCommitPreservesOpaqueTrackFacts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.save(Entity{Kind: "work", Title: "opaque work"})
	x := f.save(Entity{Kind: "expression", Title: "private take", WorkID: w.ID})
	r := f.save(Entity{Kind: "release", Title: "opaque release", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	m := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r.ID, Attributes: map[string]any{"format": "cd"}})
	track := f.save(Entity{Kind: "track", Title: "old title", MediumID: m.ID, Contents: []Inclusion{{ExpressionID: x.ID}}})
	if _, err := f.s.Unpublish(ctx, x.ID, UnpublishEdit{ExpectedVersion: x.Version, EditNote: "hide recording", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	// Legacy inclusion provenance can be empty; a metadata patch must not attach
	// its title citation to facts the editor cannot read.
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.track_contents SET sources='[]' WHERE track_id=$1`, track.ID); err != nil {
		t.Fatal(err)
	}
	u := fixtureUser("editor")
	co, err := f.s.Checkout(ctx, CheckoutRequest{EntityIDs: []string{track.ID}}, u)
	if err != nil || len(co.Entities[0].Contents) != 0 {
		t.Fatalf("projection: %+v %v", co, err)
	}
	in := fixtureCommit(f, updateCommit(track, patchValue("/title", "new title")))
	if _, err = f.s.PushCommit(ctx, in, u, false); err != nil {
		t.Fatal(err)
	}
	after, err := get(ctx, f.s.DB, track.ID)
	if err != nil || after.Title != "new title" || len(after.Contents) != 1 || after.Contents[0].ExpressionID != x.ID || len(after.Contents[0].Sources) != 0 {
		t.Fatalf("opaque facts changed: %+v %v", after, err)
	}
	in = fixtureCommit(f, updateCommit(after, patchValue("/contents", []any{})))
	if _, err = f.s.PushCommit(ctx, in, u, false); err == nil || err.Error() != "use_track_contents_endpoint" {
		t.Fatalf("hidden contents edit: %v", err)
	}
	history, err := f.s.Revisions(ctx, track.ID, &f.u)
	if err != nil || history[0]["commit_id"] == nil {
		t.Fatalf("revision commit association: %+v %v", history, err)
	}
}
