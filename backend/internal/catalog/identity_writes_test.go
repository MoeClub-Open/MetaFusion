package catalog

import (
	"context"
	"testing"
	"time"
)

func TestPostgresEntitySaveSharesIdentityTermLocks(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := Entity{Kind: "work", Title: "Locked entity identity", State: "active"}
	holder, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	keys := map[string]bool{}
	if err = addIdentityWriteKeys(ctx, holder, e, keys); err != nil {
		t.Fatal(err)
	}
	if err = lockIdentityWriteKeys(ctx, holder, keys); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Save(ctx, Edit{Entity: e, EditNote: "identity lock", Sources: fixtureSources()}, f.u)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
            WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Save bypassed held identity lock: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !waiting {
		t.Fatal("entity Save did not wait for the shared identity term lock")
	}
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("entity Save did not resume after the identity lock was released")
	}
}
