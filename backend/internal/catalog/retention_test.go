package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func retentionFixturePolicy(t *testing.T) RetentionPolicy {
	t.Helper()
	p, err := RetentionPolicyFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRetentionPolicyConfiguration(t *testing.T) {
	p := retentionFixturePolicy(t)
	if p.OutboxDays != 7 || p.CommitRequestDays != 90 || p.Interval != time.Hour ||
		len(p.RequiredConsumers) != 1 || p.RequiredConsumers[0] != openSearchConsumer {
		t.Fatalf("defaults: %+v", p)
	}
	values := map[string]string{"CATALOG_OUTBOX_RETENTION_DAYS": "0", "CATALOG_COMMIT_REQUEST_RETENTION_DAYS": "0", "CATALOG_RETENTION_INTERVAL_SECONDS": "60"}
	p, err := RetentionPolicyFromEnv(func(key string) string { return values[key] })
	if err != nil || p.OutboxDays != 0 || p.CommitRequestDays != 0 || p.Interval != time.Minute {
		t.Fatalf("forever retention: %+v %v", p, err)
	}
	for key, bad := range map[string]string{
		"CATALOG_OUTBOX_RETENTION_DAYS":         "-1",
		"CATALOG_COMMIT_REQUEST_RETENTION_DAYS": "36501",
		"CATALOG_RETENTION_INTERVAL_SECONDS":    "59",
	} {
		t.Run(key, func(t *testing.T) {
			if _, err := RetentionPolicyFromEnv(func(k string) string {
				if k == key {
					return bad
				}
				return ""
			}); err == nil {
				t.Fatal("invalid retention accepted")
			}
		})
	}
	p = retentionFixturePolicy(t)
	p.RequiredConsumers = []string{"optional-integration"}
	if p.validate() == nil {
		t.Fatal("retention without mandatory search acknowledgement accepted")
	}
}

func TestPostgresRetentionConsumersAndLifecycleFacts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	deleted := f.save(Entity{Kind: "work", Title: "retention deleted", State: "active"})
	merged := f.save(Entity{Kind: "work", Title: "retention duplicate"})
	canonical := f.save(Entity{Kind: "work", Title: "retention canonical"})
	_ = f.save(Entity{Kind: "work", Title: "retention unpublished"})

	for _, item := range []struct {
		entity Entity
		target string
	}{{deleted, ""}, {merged, canonical.ID}} {
		if _, err := f.s.Lifecycle(ctx, item.entity.ID, LifecycleEdit{ExpectedVersion: item.entity.Version,
			TargetID: item.target, EditNote: "retain action evidence", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.outbox SET created_at=now()-interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	insert := func(eventType string, old bool) string {
		t.Helper()
		id := uuid.NewString()
		age := "0 days"
		if old {
			age = "8 days"
		}
		if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.outbox(id,type,entity_id,version,payload,created_at)
			VALUES($1,$2,$3,1,'{}',now()-$4::interval)`, id, eventType, uuid.NewString(), age); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, eventType := range []string{"relation.saved", "relation.deleted", "definitions.updated", "rate_limits.updated"} {
		insert(eventType, true)
	}
	recent := insert("relation.saved", false)
	unknown := insert("entity.future_action", true)
	undelivered := insert("entity.saved", true)
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.deliveries(consumer,event_id)
		SELECT $1,id FROM catalog.outbox WHERE type LIKE 'entity.%' AND id <> $2::uuid`, openSearchConsumer, undelivered); err != nil {
		t.Fatal(err)
	}
	p := retentionFixturePolicy(t)
	p.RequiredConsumers = append(p.RequiredConsumers, "optional-storage")
	result, err := f.s.CleanupRetention(ctx, p)
	if err != nil || result.Events != 4 {
		t.Fatalf("unconsumed config/relation events only: %+v %v", result, err)
	}
	var entityEvents int64
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.outbox WHERE type IN ('entity.saved','entity.deleted','entity.merged')`).Scan(&entityEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.deliveries(consumer,event_id)
		SELECT 'optional-storage',id FROM catalog.outbox WHERE type LIKE 'entity.%' AND id <> $1::uuid`, undelivered); err != nil {
		t.Fatal(err)
	}
	result, err = f.s.CleanupRetention(ctx, p)
	if err != nil || result.Events != entityEvents-1 || result.Deliveries != result.Events*2 {
		t.Fatalf("both enabled consumers acknowledged: %+v %v (eligible=%d)", result, err, entityEvents-1)
	}
	for _, id := range []string{recent, unknown, undelivered} {
		var exists bool
		if err := f.s.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM catalog.outbox WHERE id=$1)", id).Scan(&exists); err != nil || !exists {
			t.Fatalf("pending/recent/unknown event erased: %s %v", id, err)
		}
	}
	// Facts have no cascading link to their queue event, revision or target.
	if _, err := f.s.DB.ExecContext(ctx, "DELETE FROM catalog.revisions WHERE target_id=$1", deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, "DELETE FROM catalog.entities WHERE id=$1", deleted.ID); err != nil {
		t.Fatal(err)
	}
	stats, err := f.s.UserContributions(ctx, f.u.ID, "all", 1, 20, &f.u)
	if err != nil || stats.Stats.AuditActions != 2 {
		t.Fatalf("delete and merge actions must survive queue cleanup: %+v %v", stats.Stats, err)
	}
	var orphans int
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.deliveries d LEFT JOIN catalog.outbox o ON o.id=d.event_id WHERE o.id IS NULL`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("delivery FK integrity: %d %v", orphans, err)
	}
}

func TestPostgresRetentionPreservesCommitAndCreateReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	in := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "w",
		Document: json.RawMessage(`{"kind":"work","title":"retention push"}`)})
	receipt, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil {
		t.Fatal(err)
	}
	edit := Edit{Entity: Entity{Kind: "work", Title: "retention idempotent", State: "active"},
		EditNote: "create guard", Sources: fixtureSources()}
	edit.idempotency = &IdempotencyClaim{Operation: IdempotencyOpEntityCreate, UserID: f.u.ID, Key: "retention-key", Hash: requestHash(edit)}
	created, err := f.s.Save(ctx, edit, f.u)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"commits", "idempotency_keys"} {
		if _, err := f.s.DB.ExecContext(ctx, "UPDATE catalog."+table+" SET created_at=now()-interval '100 days'"); err != nil {
			t.Fatal(err)
		}
	}
	p := retentionFixturePolicy(t)
	p.OutboxDays, p.CommitRequestDays = 0, 0
	if result, err := f.s.CleanupRetention(ctx, p); err != nil || result.CommitRequests != 0 {
		t.Fatalf("forever request policy: %+v %v", result, err)
	}
	p.CommitRequestDays = 90
	if result, err := f.s.CleanupRetention(ctx, p); err != nil || result.CommitRequests != 1 {
		t.Fatalf("only old request payload pruned: %+v %v", result, err)
	}
	var raw string
	if err := f.s.DB.QueryRowContext(ctx, `SELECT request::text FROM catalog.commits WHERE id=$1`, in.ID).Scan(&raw); err != nil || raw != "null" {
		t.Fatalf("old request remains: %s %v", raw, err)
	}
	for _, read := range []func() (CommitReceipt, error){
		func() (CommitReceipt, error) { return f.s.PushCommit(ctx, in, f.u, false) },
		func() (CommitReceipt, error) { return f.s.CommitReceipt(ctx, in.ID, f.u) },
	} {
		got, err := read()
		if err != nil || encode(got) != encode(receipt) {
			t.Fatalf("old commit receipt/replay changed: %+v %v", got, err)
		}
	}
	mutated := in
	mutated.EditNote = "different content"
	if _, err := f.s.PushCommit(ctx, mutated, f.u, false); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("old ID lost conflict guard: %v", err)
	}
	replay, err := f.s.Save(ctx, edit, f.u)
	if err != nil || replay.ID != created.ID {
		t.Fatalf("old create key lost replay: %+v %v", replay, err)
	}
	var revisions int
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.revisions WHERE commit_id=$1`, in.ID).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("commit provenance changed: %d %v", revisions, err)
	}
}

func TestPostgresRetentionBoundedAndCoordinated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.outbox(id,type,entity_id,version,payload,created_at)
		SELECT gen_random_uuid(),'relation.saved','fixture',n,'{}',now()-interval '8 days' FROM generate_series(1,$1) n`, retentionBatchSize+1); err != nil {
		t.Fatal(err)
	}
	p := retentionFixturePolicy(t)
	lock, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", retentionLockKey); err != nil {
		t.Fatal(err)
	}
	if result, err := f.s.CleanupRetention(ctx, p); err != nil || result.Events != 0 {
		t.Fatalf("competing replica cleaned: %+v %v", result, err)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{retentionBatchSize, 1} {
		if result, err := f.s.CleanupRetention(ctx, p); err != nil || result.Events != want {
			t.Fatalf("bounded cleanup: %+v %v, want %d", result, err, want)
		}
	}
}

func TestPostgresLifecycleRetentionMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	before := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name()[:6] >= "000027" {
			continue
		}
		content, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		before[entry.Name()] = &fstest.MapFile{Data: content}
	}
	if err := migrator.New(s.DB, before).Up(ctx); err != nil {
		t.Fatal(err)
	}
	seedMigrationFixture(t, s)
	// Build the historical rows directly: current Save requires the latest schema.
	f := fixture{t: t, s: s, u: fixtureUser("admin")}
	id := uuid.NewString()
	snapshot := encode(map[string]any{"id": id, "kind": "work", "title": "before migration deletion", "status": "deleted"})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,'work',1,'before migration deletion','deleted',$2,$3)`, id, f.u.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,1,$2,'fixture','prior action',$3,$4)`, id, f.u.ID, encode(fixtureSources()), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog.outbox(id,type,entity_id,version,payload) VALUES($1,'entity.deleted',$2,1,'{}')`, uuid.NewString(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog.outbox(id,type,entity_id,version,payload)
		SELECT gen_random_uuid(),type,entity_id,version,payload FROM catalog.outbox WHERE type='entity.deleted'`); err != nil {
		t.Fatal(err)
	}
	// The production emergency index already exists before applying this migration.
	if _, err := s.DB.ExecContext(ctx, `CREATE INDEX deliveries_event_id_idx ON catalog.deliveries(event_id)`); err != nil {
		t.Fatal(err)
	}
	if err := migrator.New(s.DB, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	var actions int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.lifecycle_actions WHERE actor_id=$1", f.u.ID).Scan(&actions); err != nil || actions != 1 {
		t.Fatalf("prior lifecycle fact not backfilled: %d %v", actions, err)
	}
	if err := migrator.New(s.DB, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.lifecycle_actions WHERE actor_id=$1", f.u.ID).Scan(&actions); err != nil || actions != 1 {
		t.Fatalf("repeated up duplicated action: %d %v", actions, err)
	}
}
