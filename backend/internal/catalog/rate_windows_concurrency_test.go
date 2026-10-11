package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestPostgresRateWindowCleanupPreservesConcurrentRefresh(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hash := sha256.Sum256([]byte("u:cleanup-refresh-race"))
	key := hex.EncodeToString(hash[:])
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.api_rate_windows(subject_key,window_start,used)
		VALUES($1,now()-interval '3 hours',100)`, key); err != nil {
		t.Fatal(err)
	}
	refresh, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer refresh.Rollback()
	if _, err := refresh.ExecContext(ctx, `UPDATE catalog.api_rate_windows
		SET window_start=date_trunc('minute',clock_timestamp()),used=180 WHERE subject_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	// The cleanup snapshot sees the previously expired row, then blocks behind
	// the in-flight refresh. Its DELETE must recheck expiry after that commits.
	finished := make(chan error, 1)
	go func() { finished <- f.s.pruneRateWindows(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err := f.s.DB.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			AND query LIKE 'DELETE FROM catalog.api_rate_windows%'
			AND wait_event='transactionid'
		)`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("cleanup completed before competing refresh was committed: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !blocked {
		t.Fatal("cleanup never reached the locked refresh row")
	}
	if err := refresh.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	var used int
	var active bool
	if err := f.s.DB.QueryRowContext(ctx, `SELECT used,window_start >= now()-interval '2 hours'
		FROM catalog.api_rate_windows WHERE subject_key=$1`, key).Scan(&used, &active); err != nil || used != 180 || !active {
		t.Fatalf("cleanup erased or reset the refreshed shared budget: used=%d active=%v err=%v", used, active, err)
	}
}
