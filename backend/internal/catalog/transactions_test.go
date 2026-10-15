package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

func TestCatalogRetryRequiresConfirmedAbort(t *testing.T) {
	for _, code := range []pq.ErrorCode{"40001", "40P01", "55P03"} {
		if !retryableCatalogTransaction(fmt.Errorf("operation: %w", &pq.Error{Code: code})) {
			t.Fatalf("confirmed abort %s cannot retry", code)
		}
	}
	for _, err := range []error{nil, io.EOF, context.DeadlineExceeded, errVersionConflict, &pq.Error{Code: "23505"}, &pq.Error{Code: "08006"}} {
		if retryableCatalogTransaction(err) {
			t.Fatalf("unexpected automatic retry: %v", err)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondCommit(c, CommitReceipt{}, errTransactionBusy)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"applied":false`) || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy response: %d %s", w.Code, w.Body.String())
	}
}

func TestPostgresCatalogRetryRollsBackWholeAttempt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.DB.Exec(`CREATE TABLE catalog.transaction_probe(value int)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		failure      error
		wantAttempts int
	}{
		{"serialization", &pq.Error{Code: "40001"}, 2},
		{"deadlock", &pq.Error{Code: "40P01"}, 2},
		{"unknown connection", io.EOF, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = f.s.DB.Exec(`TRUNCATE catalog.transaction_probe`)
			attempts := 0
			err := f.s.writeCatalog(ctx, func(tx *sql.Tx) error {
				attempts++
				if _, err := tx.Exec(`INSERT INTO catalog.transaction_probe VALUES($1)`, attempts); err != nil {
					return err
				}
				if attempts == 1 {
					return tc.failure
				}
				return nil
			})
			if attempts != tc.wantAttempts || (tc.wantAttempts == 2 && err != nil) || (tc.wantAttempts == 1 && !errors.Is(err, io.EOF)) {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
			var count, value int
			if err := f.s.DB.QueryRow(`SELECT count(*),coalesce(max(value),0) FROM catalog.transaction_probe`).Scan(&count, &value); err != nil {
				t.Fatal(err)
			}
			if tc.wantAttempts == 2 && (count != 1 || value != 2) || tc.wantAttempts == 1 && count != 0 {
				t.Fatalf("rolled-back effects escaped: %d %d", count, value)
			}
		})
	}
	attempts := 0
	err := f.s.writeCatalog(ctx, func(tx *sql.Tx) error { attempts++; return &pq.Error{Code: "40001"} })
	if !errors.Is(err, errTransactionBusy) || attempts != catalogTransactionAttempts {
		t.Fatalf("unbounded retries: %d %v", attempts, err)
	}
}

func TestPostgresStructuralCommitIgnoresFormerGlobalLock(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w := f.save(Entity{Kind: "work", Title: "parallel structure"})
	a := f.save(Entity{Kind: "content_unit", WorkID: w.ID, Title: "unit a"})
	b := f.save(Entity{Kind: "content_unit", WorkID: w.ID, Title: "unit b"})
	holder, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err = holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock(740202)`); err != nil {
		t.Fatal(err)
	}
	in := fixtureCommit(f, updateCommit(a, patchValue("/parent_id", b.ID)))
	if out, err := f.s.PushCommit(ctx, in, f.u, false); err != nil || len(out.Items) != 1 {
		t.Fatalf("structure still waits on global lock: %+v %v", out, err)
	}
	in = fixtureCommit(f, CommitOperation{Target: "relation", Action: "create", Ref: "edge", Document: json.RawMessage(encode(Relation{Type: "includes", SourceID: w.ID, TargetID: f.save(Entity{Kind: "work", Title: "other work"}).ID}))})
	if out, err := f.s.PushCommit(ctx, in, f.u, false); err != nil || len(out.Items) != 1 {
		t.Fatalf("relation still waits on global lock: %+v %v", out, err)
	}
}

func TestPostgresIndependentReleaseCommitCompletesWhileOtherWaits(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w := f.save(Entity{Kind: "work", Title: "parallel releases"})
	r1 := f.save(Entity{Kind: "release", Title: "r1", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	r2 := f.save(Entity{Kind: "release", Title: "r2", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	m1 := f.save(Entity{Kind: "medium", Title: "m1", ReleaseID: r1.ID})
	m2 := f.save(Entity{Kind: "medium", Title: "m2", ReleaseID: r2.ID})
	holder, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if err = lockRelease(ctx, holder, r1.ID); err != nil {
		t.Fatal(err)
	}
	in1 := fixtureCommit(f, updateCommit(m1, patchValue("/title", "m1 changed")))
	in2 := fixtureCommit(f, updateCommit(m2, patchValue("/title", "m2 changed")))
	done := make(chan error, 1)
	go func() { _, err := f.s.PushCommit(ctx, in1, f.u, false); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("held release unexpectedly completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = f.s.PushCommit(ctx, in2, f.u, false); err != nil {
		t.Fatalf("unrelated release blocked: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("first release stopped waiting: %v", err)
	default:
	}
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPostgresConcurrentInverseParentsCannotBothCommit(t *testing.T) {
	f := newFixture(t)
	w := f.save(Entity{Kind: "work", Title: "parent race"})
	a := f.save(Entity{Kind: "content_unit", WorkID: w.ID, Title: "a"})
	b := f.save(Entity{Kind: "content_unit", WorkID: w.ID, Title: "b"})
	// Direct SQL cannot bypass concurrency validation with READ COMMITTED.
	if _, err := f.s.DB.Exec(`UPDATE catalog.content_units SET parent_id=$2 WHERE id=$1`, a.ID, b.ID); err == nil {
		t.Fatal("unsafe parent transaction accepted")
	}
	ctx := context.Background()
	tx1, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback()
	tx2, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()
	for i, tx := range []*sql.Tx{tx1, tx2} {
		ids := []string{a.ID, b.ID}
		if i == 1 {
			ids = []string{b.ID, a.ID}
		}
		if _, err = tx.Exec(`UPDATE catalog.content_units SET parent_id=$2 WHERE id=$1`, ids[0], ids[1]); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			t.Fatal(err)
		}
	}
	e1, e2 := tx1.Commit(), tx2.Commit()
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("one parent edit must survive: %v / %v", e1, e2)
	}
	if !retryableCatalogTransaction(e1) && !retryableCatalogTransaction(e2) {
		t.Fatalf("race not detected by SSI: %v / %v", e1, e2)
	}
}

func TestPostgresConcurrentRelationsPreserveCrossCodeCycleAndCardinality(t *testing.T) {
	for _, mode := range []string{"cross_code_cycle", "cardinality"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			v, err := f.s.Definitions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rule := RelationDefinition{Names: names("并发测试", "Concurrency"), ReverseNames: names("并发测试逆", "Reverse"), SourceKinds: []string{"work"}, TargetKinds: []string{"work"}, ParticipantSlot: "peer", Enabled: true}
			if mode == "cross_code_cycle" {
				rule.Acyclic = true
				rule.CycleGroup = "concurrency_cycle"
			} else {
				rule.MaxOutgoing = 1
			}
			v.Document.Relations["concurrency_a"] = rule
			if mode == "cross_code_cycle" {
				v.Document.Relations["concurrency_b"] = rule
			}
			f.publish(v.Document, v.ETag)
			a := f.save(Entity{Kind: "work", Title: "a"})
			b := f.save(Entity{Kind: "work", Title: "b"})
			c := f.save(Entity{Kind: "work", Title: "c"})
			d := f.save(Entity{Kind: "work", Title: "d"})
			r1 := Relation{Type: "concurrency_a", SourceID: a.ID, TargetID: b.ID}
			r2 := Relation{Type: "concurrency_a", SourceID: a.ID, TargetID: c.ID}
			if mode == "cross_code_cycle" {
				for _, r := range []Relation{{Type: "concurrency_a", SourceID: b.ID, TargetID: c.ID}, {Type: "concurrency_b", SourceID: d.ID, TargetID: a.ID}} {
					if _, err = f.s.SaveRelation(ctx, RelationEdit{Relation: r, EditNote: "prepare", Sources: fixtureSources()}, f.u); err != nil {
						t.Fatal(err)
					}
				}
				r2 = Relation{Type: "concurrency_b", SourceID: c.ID, TargetID: d.ID}
			}
			tx1, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				t.Fatal(err)
			}
			defer tx1.Rollback()
			tx2, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				t.Fatal(err)
			}
			defer tx2.Rollback()
			for i, tx := range []*sql.Tx{tx1, tx2} {
				r := []Relation{r1, r2}[i]
				if _, err = f.s.saveRelationTx(ctx, tx, RelationEdit{Relation: r, EditNote: "race", Sources: fixtureSources()}, f.u); err != nil {
					t.Fatal(err)
				}
			}
			e1, e2 := tx1.Commit(), tx2.Commit()
			if (e1 == nil) == (e2 == nil) || (!retryableCatalogTransaction(e1) && !retryableCatalogTransaction(e2)) {
				t.Fatalf("invalid graph race survived: %v / %v", e1, e2)
			}
			loser := r2
			if e1 != nil {
				loser = r1
			}
			_, err = f.s.SaveRelation(ctx, RelationEdit{Relation: loser, EditNote: "recheck", Sources: fixtureSources()}, f.u)
			want := "cardinality_exceeded"
			if mode == "cross_code_cycle" {
				want = "relation_cycle"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("new snapshot must reject %s: %v", want, err)
			}
		})
	}
}

func TestPostgresConcurrentDeleteAndNewRelationCannotLeaveLiveEdge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.save(Entity{Kind: "work", Title: "deleted endpoint"})
	b := f.save(Entity{Kind: "work", Title: "remaining endpoint"})
	relTx, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer relTx.Rollback()
	if _, err = f.s.saveRelationTx(ctx, relTx, RelationEdit{Relation: Relation{Type: "includes", SourceID: a.ID, TargetID: b.ID}, EditNote: "new edge", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	delTx, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer delTx.Rollback()
	// The same predicate and status write used by Lifecycle's delete branch.
	if _, err = delTx.Exec(`DELETE FROM catalog.relations WHERE source_id=$1 OR target_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = delTx.Exec(`UPDATE catalog.entities SET status='deleted',document=jsonb_set(document,'{status}','"deleted"'),version=version+1 WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	e1, e2 := relTx.Commit(), delTx.Commit()
	if (e1 == nil) == (e2 == nil) || (!retryableCatalogTransaction(e1) && !retryableCatalogTransaction(e2)) {
		t.Fatalf("delete/reference write skew survived: %v / %v", e1, e2)
	}
	if e2 != nil {
		if _, err = f.s.Lifecycle(ctx, a.ID, LifecycleEdit{ExpectedVersion: a.Version, EditNote: "delete after edge", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatal(err)
		}
	} else if _, err = f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "includes", SourceID: a.ID, TargetID: b.ID}, EditNote: "retry edge", Sources: fixtureSources()}, f.u); !errors.Is(err, errForbidden) {
		t.Fatalf("deleted endpoint accepted: %v", err)
	}
	var count int
	if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.relations WHERE source_id=$1 OR target_id=$1`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted endpoint retains edge: %d %v", count, err)
	}
}

func TestPostgresCommitRetryDiscardsAbandonedRefsAndItems(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.DB.Exec(`CREATE SEQUENCE catalog.retry_probe;
	CREATE FUNCTION catalog.fail_second_commit_entity_once() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
	 IF NEW.title='retry second' AND nextval('catalog.retry_probe')=1 THEN
	  RAISE EXCEPTION 'injected serialization failure' USING ERRCODE='40001';
	 END IF;
	 RETURN NEW;
	END $$;
	CREATE TRIGGER retry_probe AFTER INSERT ON catalog.entities FOR EACH ROW EXECUTE FUNCTION catalog.fail_second_commit_entity_once();`); err != nil {
		t.Fatal(err)
	}
	in := fixtureCommit(f,
		CommitOperation{Target: "entity", Action: "create", Ref: "first", Document: json.RawMessage(`{"kind":"work","title":"retry first"}`)},
		CommitOperation{Target: "entity", Action: "create", Ref: "second", Document: json.RawMessage(`{"kind":"work","title":"retry second"}`)})
	out, err := f.s.PushCommit(context.Background(), in, f.u, false)
	if err != nil || len(out.Items) != 2 || len(out.Refs) != 2 {
		t.Fatalf("abandoned result retained: %+v %v", out, err)
	}
	for i, item := range out.Items {
		if out.Refs[in.Operations[i].Ref] != item.ID {
			t.Fatalf("refs disagree: %+v", out)
		}
		if _, err = f.s.Get(context.Background(), item.ID, &f.u); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"entities", "revisions", "outbox"} {
		var count int
		if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.` + table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("retry leaked %s: %d %v", table, count, err)
		}
	}
	var count int
	if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.revisions WHERE commit_id=$1`, in.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("provenance: %d %v", count, err)
	}
	replay, err := f.s.PushCommit(context.Background(), in, f.u, false)
	if err != nil || encode(replay) != encode(out) {
		t.Fatalf("receipt changed: %+v %v", replay, err)
	}
}

func TestPostgres32ConcurrentStructuralCommits(t *testing.T) {
	f := newFixture(t)
	f.s.DB.SetMaxOpenConns(20)
	f.s.DB.SetMaxIdleConns(10)
	const n = 32
	inputs := make([]CatalogCommit, n)
	for i := range inputs {
		w := f.save(Entity{Kind: "work", Title: fmt.Sprintf("parallel owner %d", i)})
		e := f.save(Entity{Kind: "content_unit", WorkID: w.ID, Title: fmt.Sprintf("parallel unit %d", i)})
		inputs[i] = fixtureCommit(f, updateCommit(e, patchValue("/title", fmt.Sprintf("updated unit %d", i))))
	}
	start := make(chan struct{})
	type result struct {
		input CatalogCommit
		err   error
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for _, in := range inputs {
		wg.Add(1)
		go func(in CatalogCommit) {
			defer wg.Done()
			<-start
			out, err := f.s.PushCommit(context.Background(), in, f.u, false)
			if err == nil && len(out.Items) != 1 {
				err = fmt.Errorf("unexpected receipt size %d", len(out.Items))
			}
			results <- result{in, err}
		}(in)
	}
	began := time.Now()
	close(start)
	wg.Wait()
	close(results)
	pending := []CatalogCommit{}
	for result := range results {
		if errors.Is(result.err, errTransactionBusy) {
			// SSI may reject even disjoint writes (for example page/predicate
			// lock escalation in a tiny fixture under -race). A bounded push is
			// allowed to be busy; it must leave no partial receipt or revision.
			if _, err := f.s.CommitReceipt(context.Background(), result.input.ID, f.u); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("busy push has receipt: %v", err)
			}
			var revisions int
			if err := f.s.DB.QueryRow(`SELECT count(*) FROM catalog.revisions WHERE commit_id=$1`, result.input.ID).Scan(&revisions); err != nil || revisions != 0 {
				t.Fatalf("busy push leaked revisions: %d %v", revisions, err)
			}
			pending = append(pending, result.input)
		} else if result.err != nil {
			t.Fatal(result.err)
		}
	}
	if len(pending) > 0 {
		// Explicit caller recovery after Retry-After, keeping the immutable ID
		// and payload. No automatic replay of network/unknown failures.
		time.Sleep(time.Second)
		for _, in := range pending {
			if out, err := f.s.PushCommit(context.Background(), in, f.u, false); err != nil || len(out.Items) != 1 {
				t.Fatalf("same-ID busy recovery failed: %+v %v", out, err)
			}
		}
	}
	var count int
	if err := f.s.DB.QueryRow(`SELECT count(*) FROM catalog.revisions WHERE commit_id IS NOT NULL`).Scan(&count); err != nil || count != n {
		t.Fatalf("duplicate/lost revisions: %d %v", count, err)
	}
	for _, in := range inputs {
		out, err := f.s.CommitReceipt(context.Background(), in.ID, f.u)
		if err != nil || len(out.Items) != 1 || out.Items[0].Version != 2 {
			t.Fatalf("missing/duplicate final receipt: %+v %v", out, err)
		}
	}
	t.Logf("%d simultaneous structural commits completed in %s; busy pushes recovered with the same ID: %d (isolated local PostgreSQL correctness test)", n, time.Since(began), len(pending))
}
