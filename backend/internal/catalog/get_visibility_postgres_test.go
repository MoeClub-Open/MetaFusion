package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresSingleAndBatchReadsUseAuthoritativeVisibilityColumns(t *testing.T) {
	f := newFixture(t)
	e := f.save(Entity{Kind: "agent", Title: "Visibility fixture"})
	oldOwner := User{ID: e.CreatedBy}
	newOwner := User{ID: uuid.NewString()}
	ctx := context.Background()
	// Deliberately leave the embedded snapshot active and owned by oldOwner.
	// Both read paths must nevertheless honor the canonical state/owner columns.
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='deleted',created_by=$2 WHERE id=$1`, e.ID, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		user    *User
		visible bool
	}{{"anonymous", nil, false}, {"previous_owner", &oldOwner, false}, {"current_owner", &newOwner, true}, {"lifecycle_manager", &f.u, true}} {
		t.Run(test.name, func(t *testing.T) {
			item, err := f.s.Get(ctx, e.ID, test.user)
			items, batchErr := f.s.GetManyVisible(ctx, []string{e.ID}, test.user)
			if batchErr != nil {
				t.Fatal(batchErr)
			}
			if !test.visible {
				if !errors.Is(err, sql.ErrNoRows) || len(items) != 0 {
					t.Fatalf("single err=%v batch=%+v", err, items)
				}
				return
			}
			if err != nil || item.State != "deleted" || item.CreatedBy != newOwner.ID || items[e.ID].State != item.State || items[e.ID].CreatedBy != item.CreatedBy {
				t.Fatalf("single=%+v err=%v batch=%+v", item, err, items)
			}
		})
	}
	// The inverse mismatch must not hide an active entity either.
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='active',document=jsonb_set(document,'{state}','"deleted"'::jsonb) WHERE id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	item, err := f.s.Get(ctx, e.ID, nil)
	if err != nil || item.State != "active" || item.CreatedBy != newOwner.ID {
		t.Fatalf("active column ignored: %+v %v", item, err)
	}
}
