package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

func TestReleaseTableOfContents(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	admin := fixtureUser("admin")
	sources := fixtureSources()
	save := func(e Entity) Entity {
		t.Helper()
		v, err := s.Save(ctx, Edit{Entity: e, ExpectedVersion: e.Version, EditNote: "release toc fixture", Sources: sources}, admin)
		if err != nil {
			t.Fatalf("save %s %q: %v", e.Kind, e.Title, err)
		}
		return v
	}
	entity := func(kind, title, status string) Entity {
		return Entity{Kind: kind, Title: title, State: "active", Attributes: map[string]any{}, Translations: map[string]Translation{"en": {Title: title}}}
	}
	work := save(entity("work", "album", "published"))
	expression := entity("expression", "recording", "published")
	expression.WorkID = work.ID
	expression = save(expression)
	hiddenExpression := entity("expression", "unpublished recording", "published")
	hiddenExpression.WorkID = work.ID
	hiddenExpression = save(hiddenExpression)
	release := entity("release", "two disc edition", "published")
	release.Subjects = []Subject{{WorkID: work.ID, Role: "primary"}}
	release = save(release)
	second := entity("medium", "disc 2", "published")
	second.ReleaseID, second.Position = release.ID, 2
	second = save(second)
	first := entity("medium", "disc 1", "published")
	first.ReleaseID, first.Position = release.ID, 1
	first = save(first)
	last := entity("track", "last track", "published")
	last.MediumID, last.Position = first.ID, 2
	last.Contents = []Inclusion{{ExpressionID: expression.ID}, {ExpressionID: hiddenExpression.ID, Position: 2}}
	last = save(last)
	if _, err := s.Lifecycle(ctx, hiddenExpression.ID, LifecycleEdit{
		ExpectedVersion: hiddenExpression.Version, EditNote: "release toc fixture", Sources: sources,
	}, admin); err != nil {
		t.Fatalf("unpublish recording: %v", err)
	}
	opening := entity("track", "opening track", "published")
	opening.MediumID, opening.Position = first.ID, 1
	opening.Contents = []Inclusion{{ExpressionID: expression.ID}}
	opening = save(opening)
	onSecond := entity("track", "second disc track", "published")
	onSecond.MediumID = second.ID
	onSecond.Contents = []Inclusion{{ExpressionID: expression.ID}}
	save(onSecond)

	got, err := s.ReleaseTableOfContents(ctx, release.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Release.ID != release.ID || got.Release.Version != release.Version || got.DefinitionETag == "" {
		t.Fatalf("release/version/definition missing: %+v", got)
	}
	if len(got.Media) != 2 || got.Media[0].Medium.ID != first.ID || got.Media[1].Medium.ID != second.ID {
		t.Fatalf("media order: %+v", got.Media)
	}
	if tracks := got.Media[0].Tracks; len(tracks) != 2 || tracks[0].ID != opening.ID || tracks[1].ID != last.ID {
		t.Fatalf("track order: %+v", tracks)
	}
	if got.Media[0].Tracks[1].Version != last.Version || len(got.Media[0].Tracks[1].Contents) != 1 {
		t.Fatalf("track version or hidden inclusion leaked: %+v", got.Media[0].Tracks[1])
	}
	if len(got.Expressions) != 1 || got.Expressions[expression.ID].WorkID != work.ID {
		t.Fatalf("expression dedupe/structure/visibility: %+v", got.Expressions)
	}
	owned, err := s.ReleaseTableOfContents(ctx, release.ID, &admin)
	if err != nil || len(owned.Expressions) != 2 || len(owned.Media[0].Tracks[1].Contents) != 2 {
		t.Fatalf("reviewer view: %+v, %v", owned, err)
	}
	if _, err := s.ReleaseTableOfContents(ctx, work.ID, nil); err == nil || err.Error() != "not_release" {
		t.Fatalf("non-release error: %v", err)
	}
	if _, err := s.ReleaseTableOfContents(ctx, "bad-id", nil); err == nil || err.Error() != "invalid_id" {
		t.Fatalf("invalid id error: %v", err)
	}
	if _, err := s.ReleaseTableOfContents(ctx, "00000000-0000-0000-0000-000000000000", nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing release error: %v", err)
	}
}
