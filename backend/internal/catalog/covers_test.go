package catalog

import (
	"context"
	"testing"
)

func TestDisplayCoversPreservePicturesAndSourceVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Cover work", Pictures: PicturesJSON{{URL: "https://example.test/work.jpg"}}})
	expression := f.save(Entity{Kind: "expression", Title: "Expression", WorkID: work.ID})
	release := f.save(Entity{Kind: "release", Title: "Release", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	medium := f.save(Entity{Kind: "medium", Title: "Medium", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "Track", MediumID: medium.ID, Position: 1, Contents: []Inclusion{{ExpressionID: expression.ID, Position: 1}}})
	items := []Entity{work, expression, release, medium, track}
	covers, err := f.s.DisplayCovers(ctx, items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := covers[work.ID]; ok {
		t.Fatal("own picture should not become an inheritance projection")
	}
	for _, e := range items[1:] {
		if covers[e.ID].URL != ownCover(work) || covers[e.ID].From.ID != work.ID || len(e.Pictures) != 0 {
			t.Fatalf("missing cover or modified own pictures for %s: %+v", e.Kind, covers[e.ID])
		}
	}
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='deleted' WHERE id=$1`, work.ID); err != nil {
		t.Fatal(err)
	}
	covers, err = f.s.DisplayCovers(ctx, []Entity{expression, release, medium, track}, nil)
	if err != nil || len(covers) != 0 {
		t.Fatalf("invisible source leaked: %+v %v", covers, err)
	}
}

func TestDisplayCoversOccurrenceReleaseAndOwnCoverPriority(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Work without artwork"})
	expression := f.save(Entity{Kind: "expression", Title: "Expression", WorkID: work.ID})
	release := f.save(Entity{Kind: "release", Title: "Release artwork", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}, Pictures: PicturesJSON{{URL: "https://example.test/release.jpg"}}})
	medium := f.save(Entity{Kind: "medium", Title: "Medium", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "Track", MediumID: medium.ID, Position: 1, Contents: []Inclusion{{ExpressionID: expression.ID, Position: 1}}})
	covers, err := f.s.DisplayCovers(ctx, []Entity{work, expression, release, medium, track}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entity{work, expression, medium, track} {
		if covers[e.ID].From.ID != release.ID || covers[e.ID].Origin != "release" {
			t.Fatalf("release fallback missing for %s: %+v", e.Kind, covers[e.ID])
		}
	}
}
