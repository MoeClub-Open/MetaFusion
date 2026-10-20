package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A published carrier remains published when one recording is unpublished.
// Every public projection must hide that recording's inclusion while owners and
// reviewers can still inspect it, and the underlying structural fact survives.
func TestPostgresUnpublishedExpressionInclusionVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Song"})
	unit := f.save(Entity{Kind: "content_unit", Title: "Movement", WorkID: work.ID})
	public := f.save(Entity{Kind: "expression", Title: "Original recording", WorkID: work.ID, ContentUnitID: unit.ID})
	hidden := f.save(Entity{Kind: "expression", Title: "Unpublished alternate take", WorkID: work.ID, ContentUnitID: unit.ID})
	release := f.save(Entity{Kind: "release", Title: "CD edition", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	otherRelease := f.save(Entity{Kind: "release", Title: "Vinyl edition", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	medium := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: release.ID, Attributes: map[string]any{"format": "cd"}})
	track := f.save(Entity{Kind: "track", Title: "Two takes", MediumID: medium.ID, Contents: []Inclusion{
		{ExpressionID: public.ID},
		{ExpressionID: hidden.ID, Position: 1, Locator: Locator{"relative_to": "track", "chapter": "alternate take"}},
	}})
	// Two distinct snapshots of one Track exercise batch history projection:
	// keying by entity ID would accidentally replace the earlier locator too.
	track.Title = "Two takes revised"
	track.Contents[0].Locator = Locator{"relative_to": "track", "chapter": "main take"}
	track = f.save(track)
	if _, err := f.s.Lifecycle(ctx, hidden.ID, LifecycleEdit{
		ExpectedVersion: hidden.Version, EditNote: "remove alternate take from public view", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	creator := f.u
	creator.Permissions = nil
	outsider := fixtureUser("editor")
	reviewer := fixtureUser("moderator")
	for _, tc := range []struct {
		name string
		user *User
		want int
	}{
		{"anonymous", nil, 1},
		{"unrelated editor", &outsider, 1},
		{"creator", &creator, 2},
		{"reviewer", &reviewer, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(label string, e Entity) {
				t.Helper()
				if len(e.Contents) != tc.want || e.Contents[0].ExpressionID != public.ID {
					t.Fatalf("%s inclusion visibility: %+v", label, e.Contents)
				}
			}
			e, err := f.s.Get(ctx, track.ID, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			check("Get", e)
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				if tc.user != nil {
					c.Set("catalog_user", tc.user)
				}
				c.Next()
			})
			HTTP{Store: f.s}.Register(engine)
			exported := httptest.NewRecorder()
			engine.ServeHTTP(exported, httptest.NewRequest(http.MethodGet, "/api/exchange/entities/"+track.ID, nil))
			if exported.Code != http.StatusOK {
				t.Fatalf("Exchange: %d %s", exported.Code, exported.Body.String())
			}
			var export Entity
			if err := json.Unmarshal(exported.Body.Bytes(), &export); err != nil {
				t.Fatal(err)
			}
			check("Exchange", export)
			revisions, err := f.s.Revisions(ctx, track.ID, RevisionQuery{}, tc.user)
			if err != nil || len(revisions.Items) != 2 {
				t.Fatalf("Revisions: %d rows, %v", len(revisions.Items), err)
			}
			for i, revision := range revisions.Items {
				var snapshot Entity
				if err := json.Unmarshal(revision["snapshot"].(json.RawMessage), &snapshot); err != nil {
					t.Fatal(err)
				}
				check("Revision snapshot", snapshot)
				if chapter, _ := snapshot.Contents[0].Locator["chapter"].(string); (i == 0 && chapter != "main take") || (i == 1 && chapter != "") {
					t.Fatalf("distinct revision locators were overwritten: %+v", snapshot.Contents)
				}
			}
			e, err = f.s.Resolve(ctx, track.ID, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			check("Resolve", e)
			identity, err := f.s.ResolveIdentity(ctx, track.ID, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			check("ResolveIdentity", identity.Entity)
			tracks, err := f.s.ListAll(ctx, ListOptions{MediumID: medium.ID}, tc.user)
			if err != nil || len(tracks) != 1 {
				t.Fatalf("List: %d tracks, %v", len(tracks), err)
			}
			check("List", tracks[0])
			toc, err := f.s.ReleaseTableOfContents(ctx, release.ID, tc.user)
			if err != nil || len(toc.Media) != 1 || len(toc.Media[0].Tracks) != 1 {
				t.Fatalf("TOC: %+v, %v", toc, err)
			}
			check("TOC", toc.Media[0].Tracks[0])
			for _, id := range []string{work.ID, unit.ID} {
				occurrences, err := f.s.Occurrences(ctx, id, tc.user)
				if err != nil || len(occurrences) != tc.want {
					t.Fatalf("Occurrences: %d rows, %v", len(occurrences), err)
				}
				for _, row := range occurrences {
					if tc.want == 1 && row["expression_id"] == hidden.ID {
						t.Fatal("hidden recording leaked through reverse inclusion")
					}
					check("Occurrences track", row["track"].(Entity))
				}
			}
			batch, err := f.s.ExpressionDetailsBatch(ctx, []string{work.ID, unit.ID, public.ID}, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			if len(batch.Items[work.ID].Occurrences) != tc.want || len(batch.Items[unit.ID].Occurrences) != tc.want ||
				len(batch.Items[public.ID].Occurrences) != 1 || len(batch.Items[public.ID].Siblings) != tc.want-1 {
				t.Fatalf("batch aggregate/sibling visibility: %+v", batch.Items)
			}
			if _, leaked := batch.Entities[hidden.ID]; leaked && tc.want == 1 {
				t.Fatal("hidden recording leaked through shared entities")
			}
			check("Batch track", batch.Entities[track.ID])
			comparison, err := f.s.Compare(ctx, []string{release.ID, otherRelease.ID}, tc.user)
			if err != nil {
				t.Fatal(err)
			}
			children := comparison[0]["children"].([]map[string]any)
			check("Compare", children[0]["tracks"].([]Entity)[0])
		})
	}
	stored, err := get(ctx, f.s.DB, track.ID)
	if err != nil || len(stored.Contents) != 2 {
		t.Fatalf("read filtering changed stored structural facts: %+v, %v", stored.Contents, err)
	}
	var storedHistory int
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.revisions
		WHERE target_id=$1 AND jsonb_array_length(snapshot->'contents')=2`, track.ID).Scan(&storedHistory); err != nil || storedHistory != 2 {
		t.Fatalf("read filtering changed stored revision facts: %d snapshots, %v", storedHistory, err)
	}
	filtered, err := f.s.Get(ctx, track.ID, &outsider)
	if err != nil {
		t.Fatal(err)
	}
	filtered.Title = "Unrelated title edit"
	if _, err = f.s.Save(ctx, Edit{Entity: filtered, ExpectedVersion: filtered.Version,
		EditNote: "edit from a filtered read", Sources: fixtureSources()}, outsider); !errors.Is(err, errForbidden) {
		t.Fatalf("filtered PUT must not silently erase hidden facts: %v", err)
	}
	stored, err = get(ctx, f.s.DB, track.ID)
	if err != nil || stored.Version != track.Version || len(stored.Contents) != 2 {
		t.Fatalf("rejected edit changed structural facts: %+v, %v", stored, err)
	}
	// A reviewer can deliberately remove the historical inclusion after seeing
	// the full record; this is a normal, versioned edit rather than read filtering.
	stored.Contents = stored.Contents[:1]
	stored = f.save(stored)
	if len(stored.Contents) != 1 || stored.Version != track.Version+1 {
		t.Fatalf("explicit reviewer edit was blocked: %+v", stored)
	}
}
