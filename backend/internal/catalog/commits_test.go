package catalog

import (
	"encoding/json"
	"testing"
)

func patchValue(path string, value any) CommitPatch {
	return CommitPatch{Path: path, Value: json.RawMessage(encode(value))}
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

}

func TestCommitFieldConflictKinds(t *testing.T) {
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

}
