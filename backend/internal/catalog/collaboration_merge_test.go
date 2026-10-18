package catalog

import (
	"encoding/json"
	"testing"
)

func TestWorkspaceConflictDistinguishesMissingAndNull(t *testing.T) {
	base := json.RawMessage(`{"title":"Example","attributes":{"value":1}}`)
	proposed := json.RawMessage(`{"title":"Example","attributes":{"value":2}}`)
	for _, tc := range []struct {
		name    string
		current json.RawMessage
		present bool
	}{
		{"removed", json.RawMessage(`{"title":"Example","attributes":{}}`), false},
		{"null", json.RawMessage(`{"title":"Example","attributes":{"value":null}}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, conflicts, err := mergeWorkspaceObject(WorkspaceObject{Target: "entity", ID: "record", BaseVersion: 1, Base: base, Committed: proposed}, tc.current, 2)
			if err != nil || len(conflicts) != 1 {
				t.Fatalf("conflict: %+v %v", conflicts, err)
			}
			c := conflicts[0]
			if c.Pointer != "/attributes/value" || !c.BasePresent || c.CurrentPresent != tc.present || !c.ProposedPresent || c.Current != nil {
				t.Fatalf("absence/null lost: %+v", c)
			}
		})
	}
}

func TestWorkspaceContentUsesStoredPosition(t *testing.T) {
	initial := Entity{Kind: "track", Contents: []Inclusion{{ExpressionID: "first", Position: 1}, {ExpressionID: "last", Position: 5}}}
	updated, err := workspaceContent(json.RawMessage(encode(initial)), &CommitContentChange{Action: "replace", Position: 5, Inclusion: &Inclusion{ExpressionID: "replacement", Position: 5}})
	if err != nil {
		t.Fatal(err)
	}
	var e Entity
	if err = json.Unmarshal(updated, &e); err != nil {
		t.Fatal(err)
	}
	if len(e.Contents) != 2 || e.Contents[0].ExpressionID != "first" || e.Contents[1].ExpressionID != "replacement" {
		t.Fatalf("replaced wrong inclusion: %+v", e.Contents)
	}
	deleted, err := workspaceContent(updated, &CommitContentChange{Action: "delete", Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(deleted, &e); err != nil {
		t.Fatal(err)
	}
	if len(e.Contents) != 1 || e.Contents[0].Position != 5 {
		t.Fatalf("deleted wrong inclusion: %+v", e.Contents)
	}
}
