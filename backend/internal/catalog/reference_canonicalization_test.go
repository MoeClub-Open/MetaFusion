package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func referenceCanonicalizationDefinitions(d Definitions) Definitions {
	n := names4("规范化测试", "正規化測試", "正規化テスト", "Normalization test")
	ref := Field{Names: n, Type: "entity", Kinds: []string{"agent"}, Enabled: true}
	payload := Field{Names: n, Type: "group", Enabled: true, Fields: map[string]Field{
		"ref":       ref,
		"refs":      {Names: n, Type: "list", Enabled: true, Items: &Field{Names: n, Type: "list", Enabled: true, Items: &ref}},
		"label":     {Names: n, Type: "text", Enabled: true},
		"text_list": {Names: n, Type: "list", Enabled: true, Items: &Field{Names: n, Type: "text", Enabled: true}},
	}}
	root := payload
	root.ApplicableKinds = []string{"work", "release", "track"}
	d.Fields["normalization_payload"] = root
	for _, code := range []string{"locator", "subject_attributes", "inclusion_attributes"} {
		f := d.Fields[code]
		if f.Fields == nil {
			f.Fields = map[string]Field{}
		}
		f.Fields["normalization_payload"] = payload
		d.Fields[code] = f
	}
	d.Relations["normalization_link"] = RelationDefinition{Names: n, ReverseNames: n, SourceKinds: []string{"agent"}, TargetKinds: []string{"work"}, Fields: []string{"normalization_payload"}, ParticipantSlot: "peer", Enabled: true}
	return d
}

func replayReferenceCanonicalization(t *testing.T, db *sql.DB) error {
	t.Helper()
	b, err := migrations.FS.ReadFile("000023_reference_canonicalization.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(context.Background(), string(b)); err != nil {
		tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var helpers int
	if err = conn.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_proc WHERE pronamespace=pg_my_temp_schema() AND proname LIKE 'mf_reference_%'`).Scan(&helpers); err != nil {
		t.Fatal(err)
	}
	if helpers != 0 {
		t.Fatalf("one-time functions survived migration: %d", helpers)
	}
	return nil
}

func normalizationExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func normalizationSnapshot(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var b string
	if err := db.QueryRowContext(context.Background(), query).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func normalizationJSON(t *testing.T, db *sql.DB, query string, args ...any) map[string]any {
	t.Helper()
	var b []byte
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&b); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPostgresReferenceCanonicalization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(referenceCanonicalizationDefinitions(v.Document), v.ETag)
	ref := f.save(Entity{Kind: "agent", Title: "unchanged reference"})
	canonical := ref.ID
	upper := strings.ToUpper(canonical)
	raw32 := strings.ReplaceAll(upper, "-", "")
	urn := "URN:UUID:" + upper
	brace := "{" + upper + "}"
	wrapped := "!" + upper + "?"
	work := f.save(Entity{Kind: "work", Title: "normalization owner", Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}})
	expression := f.save(Entity{Kind: "expression", Title: "expression", WorkID: work.ID})
	release := f.save(Entity{Kind: "release", Title: "release", Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}, Subjects: []Subject{{WorkID: work.ID, Role: "primary", Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}}}})
	medium := f.save(Entity{Kind: "medium", Title: "medium", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "track", MediumID: medium.ID, Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}, Contents: []Inclusion{{ExpressionID: expression.ID, Position: 0, Locator: Locator{"relative_to": "track", "normalization_payload": map[string]any{"ref": canonical}}, Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}}}})
	relation, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "normalization_link", SourceID: ref.ID, TargetID: work.ID, Attributes: map[string]any{"normalization_payload": map[string]any{"ref": canonical}}}, EditNote: "normalization fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}

	// Introduce historical encodings through SQL because current writes require
	// canonical IDs. An undeclared UUID-looking value and all text stay untouched.
	workAttrs := map[string]any{"normalization_payload": map[string]any{
		"ref":         upper,
		"refs":        []any{[]any{raw32, urn, nil}, []any{brace, wrapped, "", "\u2003\t\v"}},
		"label":       upper,
		"text_list":   "historical unrelated shape",
		"unknown_ref": upper,
	}, "unknown_root": map[string]any{"ref": upper}}
	normalizationExec(t, f.s.DB, `UPDATE catalog.entities SET document=jsonb_set(document,'{attributes}',$2::jsonb,false),updated_at='2000-01-01' WHERE id=$1`, work.ID, encode(workAttrs))
	for _, owner := range []Entity{release, track} {
		normalizationExec(t, f.s.DB, `UPDATE catalog.entities SET document=jsonb_set(document,'{attributes,normalization_payload,ref}',$2::jsonb,false),updated_at='2000-01-01' WHERE id=$1`, owner.ID, encode(upper))
	}
	subjectAttrs := map[string]any{"normalization_payload": map[string]any{"ref": raw32, "label": upper}, "unknown": upper}
	contentAttrs := map[string]any{"normalization_payload": map[string]any{"ref": wrapped}, "unknown": upper}
	locator := map[string]any{"relative_to": "track", "normalization_payload": map[string]any{"ref": urn}, "unknown": upper}
	relationAttrs := map[string]any{"normalization_payload": map[string]any{"ref": brace, "refs": []any{[]any{wrapped, raw32}}, "label": upper}, "unknown": upper}
	normalizationExec(t, f.s.DB, `UPDATE catalog.release_subjects SET attributes=$2::jsonb WHERE release_id=$1`, release.ID, encode(subjectAttrs))
	normalizationExec(t, f.s.DB, `UPDATE catalog.track_contents SET attributes=$2::jsonb,locator=$3::jsonb WHERE track_id=$1`, track.ID, encode(contentAttrs), encode(locator))
	normalizationExec(t, f.s.DB, `UPDATE catalog.relations SET document=jsonb_set(document,'{attributes}',$2::jsonb,false) WHERE id=$1`, relation.ID, encode(relationAttrs))
	history := normalizationSnapshot(t, f.s.DB, `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id),'[]')::text FROM catalog.revisions r`)
	if err = replayReferenceCanonicalization(t, f.s.DB); err != nil {
		t.Fatal(err)
	}

	wantWork := map[string]any{"normalization_payload": map[string]any{
		"ref":   canonical,
		"refs":  []any{[]any{canonical, canonical, nil}, []any{canonical, canonical, "", "\u2003\t\v"}},
		"label": upper, "text_list": "historical unrelated shape", "unknown_ref": upper,
	}, "unknown_root": map[string]any{"ref": upper}}
	gotWork := normalizationJSON(t, f.s.DB, `SELECT document->'attributes' FROM catalog.entities WHERE id=$1`, work.ID)
	if !reflect.DeepEqual(gotWork, wantWork) {
		t.Fatalf("nested references/text/unknown keys or list order: got %s want %s", encode(gotWork), encode(wantWork))
	}
	for _, check := range []struct {
		query, id string
		expected  map[string]any
	}{
		{`SELECT attributes FROM catalog.release_subjects WHERE release_id=$1`, release.ID, map[string]any{"normalization_payload": map[string]any{"ref": canonical, "label": upper}, "unknown": upper}},
		{`SELECT attributes FROM catalog.track_contents WHERE track_id=$1`, track.ID, map[string]any{"normalization_payload": map[string]any{"ref": canonical}, "unknown": upper}},
		{`SELECT locator FROM catalog.track_contents WHERE track_id=$1`, track.ID, map[string]any{"relative_to": "track", "normalization_payload": map[string]any{"ref": canonical}, "unknown": upper}},
		{`SELECT document->'attributes' FROM catalog.relations WHERE id=$1`, relation.ID, map[string]any{"normalization_payload": map[string]any{"ref": canonical, "refs": []any{[]any{canonical, canonical}}, "label": upper}, "unknown": upper}},
	} {
		got := normalizationJSON(t, f.s.DB, check.query, check.id)
		if !reflect.DeepEqual(got, check.expected) {
			t.Fatalf("record normalization: got %s want %s", encode(got), encode(check.expected))
		}
	}
	for _, e := range []Entity{work, release, track, ref, expression, medium} {
		var version, documentVersion int64
		var updated time.Time
		var matchesTimestamp bool
		if err := f.s.DB.QueryRowContext(ctx, `SELECT version,(document->>'version')::bigint,updated_at,updated_at=(document->>'updated_at')::timestamptz FROM catalog.entities WHERE id=$1`, e.ID).Scan(&version, &documentVersion, &updated, &matchesTimestamp); err != nil {
			t.Fatal(err)
		}
		want := e.Version
		if e.ID == work.ID || e.ID == release.ID || e.ID == track.ID {
			want++
			if !updated.After(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || !matchesTimestamp {
				t.Fatalf("normalized owner timestamp not synchronized: %s", e.ID)
			}
		}
		if version != want || documentVersion != want {
			t.Fatalf("owner should advance exactly once: %s got column/doc %d/%d want %d", e.ID, version, documentVersion, want)
		}
	}
	if after := normalizationSnapshot(t, f.s.DB, `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id),'[]')::text FROM catalog.revisions r`); after != history {
		t.Fatal("normalization rewrote history or invented user revisions")
	}
	var entityEvents, relationEvents, invalidEvents int
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE type='entity.normalized'),count(*) FILTER(WHERE type='relation.normalized'),count(*) FILTER(WHERE (payload->>'version')::bigint<>version) FROM catalog.outbox WHERE type IN('entity.normalized','relation.normalized')`).Scan(&entityEvents, &relationEvents, &invalidEvents); err != nil {
		t.Fatal(err)
	}
	if entityEvents != 3 || relationEvents != 1 || invalidEvents != 0 {
		t.Fatalf("deduplicated normalization outbox: entity=%d relation=%d invalid=%d", entityEvents, relationEvents, invalidEvents)
	}
	var relationVersion, relationDocumentVersion int64
	if err = f.s.DB.QueryRowContext(ctx, `SELECT version,(document->>'version')::bigint FROM catalog.relations WHERE id=$1`, relation.ID).Scan(&relationVersion, &relationDocumentVersion); err != nil {
		t.Fatal(err)
	}
	if relationVersion != relation.Version+1 || relationDocumentVersion != relationVersion {
		t.Fatal("relation version not synchronized")
	}
	before := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('entities',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM catalog.entities e),'relations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM catalog.relations r),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
	if err = replayReferenceCanonicalization(t, f.s.DB); err != nil {
		t.Fatal(err)
	}
	after := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('entities',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM catalog.entities e),'relations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM catalog.relations r),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
	if before != after {
		t.Fatal("second execution changed already canonical data")
	}
}

func TestPostgresReferenceCanonicalizationInvalidIsAtomic(t *testing.T) {
	f := newFixture(t)
	v, err := f.s.Definitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.publish(referenceCanonicalizationDefinitions(v.Document), v.ETag)
	owner := f.save(Entity{Kind: "work", Title: "invalid owner"})
	canonical := "abcdefab-0123-4567-89ab-cdef01234567"
	for _, raw := range []any{"not-a-uuid", " " + canonical, "é" + canonical, canonical + "é", strings.ReplaceAll(canonical, "-", "") + "-", "abcd-efab-0123-4567-89ab-cdef01234567", 5, []any{canonical}} {
		t.Run(encode(raw), func(t *testing.T) {
			if s, ok := raw.(string); ok {
				if _, err := uuid.Parse(s); err == nil {
					t.Fatal("invalid fixture is accepted by uuid.Parse")
				}
			}
			attrs := map[string]any{"normalization_payload": map[string]any{"refs": []any{[]any{strings.ToUpper(canonical), raw}}}}
			normalizationExec(t, f.s.DB, `UPDATE catalog.entities SET document=jsonb_set(document,'{attributes}',$2::jsonb,false) WHERE id=$1`, owner.ID, encode(attrs))
			before := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('entities',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM catalog.entities e),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
			if err = replayReferenceCanonicalization(t, f.s.DB); err == nil || !strings.Contains(err.Error(), "invalid entity reference") {
				t.Fatalf("invalid UUID/type must fail rather than be hidden: %v", err)
			}
			after := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('entities',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM catalog.entities e),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
			if before != after {
				t.Fatal("failed migration changed data or emitted outbox")
			}
		})
	}
}

func TestPostgresReferenceCanonicalizationPreservesDanglingAndRetired(t *testing.T) {
	f := newFixture(t)
	v, err := f.s.Definitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.publish(referenceCanonicalizationDefinitions(v.Document), v.ETag)
	retired := f.save(Entity{Kind: "agent", Title: "retired target"})
	owner := f.save(Entity{Kind: "work", Title: "owner"})
	missing := uuid.NewString()
	attrs := map[string]any{"normalization_payload": map[string]any{"ref": strings.ToUpper(retired.ID), "refs": []any{[]any{strings.ToUpper(missing)}}}}
	normalizationExec(t, f.s.DB, `UPDATE catalog.entities SET state='merged',document=jsonb_set(document,'{state}','"merged"',true) WHERE id=$1`, retired.ID)
	normalizationExec(t, f.s.DB, `UPDATE catalog.entities SET document=jsonb_set(document,'{attributes}',$2::jsonb,false) WHERE id=$1`, owner.ID, encode(attrs))
	if err = replayReferenceCanonicalization(t, f.s.DB); err != nil {
		t.Fatal(err)
	}
	got := normalizationJSON(t, f.s.DB, `SELECT document->'attributes' FROM catalog.entities WHERE id=$1`, owner.ID)
	want := map[string]any{"normalization_payload": map[string]any{"ref": retired.ID, "refs": []any{[]any{missing}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalization must not erase/redirect missing or merged references: %s", encode(got))
	}
}

func TestPostgresReferenceCanonicalizationDefinitionPrecondition(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty unseeded", true: "nonempty missing definitions"}[populated], func(t *testing.T) {
			db := testutil.Database(t)
			for _, name := range []string{"000021_catalog_baseline.up.sql", "000022_query_indexes.up.sql"} {
				b, err := migrations.FS.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				normalizationExec(t, db, string(b))
			}
			if populated {
				normalizationExec(t, db, `INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,'work',1,'unseeded entity','published',$2,'{}')`, uuid.NewString(), uuid.NewString())
			}
			err := replayReferenceCanonicalization(t, db)
			if populated {
				if err == nil || !strings.Contains(err.Error(), "requires published definitions") {
					t.Fatalf("nonempty unseeded catalog must fail explicitly: %v", err)
				}
				var exists bool
				if err = db.QueryRow(`SELECT to_regclass('catalog.schema_contract') IS NOT NULL`).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if exists {
					t.Fatal("failed normalization advertised completed protocol")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var version int
				if err = db.QueryRow(`SELECT version FROM catalog.schema_contract WHERE singleton`).Scan(&version); err != nil || version != 23 {
					t.Fatalf("fresh schema protocol marker: %d %v", version, err)
				}
			}
		})
	}
}

func TestPostgresReferenceCanonicalizationDuplicateRelationIsAtomic(t *testing.T) {
	f := newFixture(t)
	v, err := f.s.Definitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.publish(referenceCanonicalizationDefinitions(v.Document), v.ETag)
	ref := f.save(Entity{Kind: "agent", Title: "reference"})
	work := f.save(Entity{Kind: "work", Title: "work"})
	r, err := f.s.SaveRelation(context.Background(), RelationEdit{Relation: Relation{Type: "normalization_link", SourceID: ref.ID, TargetID: work.ID, Attributes: map[string]any{"normalization_payload": map[string]any{"ref": ref.ID}}}, EditNote: "normalization fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	normalizationExec(t, f.s.DB, `INSERT INTO catalog.relations(id,version,type,source_id,target_id,document) SELECT $2::uuid,version,type,source_id,target_id,jsonb_set(jsonb_set(document,'{id}',to_jsonb($2::uuid::text),true),'{attributes,normalization_payload,ref}',$3::jsonb,false) FROM catalog.relations WHERE id=$1`, r.ID, uuid.NewString(), encode(strings.ToUpper(ref.ID)))
	before := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('relations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM catalog.relations r),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
	if err = replayReferenceCanonicalization(t, f.s.DB); err == nil || !strings.Contains(err.Error(), "relations_no_exact_dup") {
		t.Fatalf("normalization collision must fail without silent deduplication: %v", err)
	}
	after := normalizationSnapshot(t, f.s.DB, `SELECT jsonb_build_object('relations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM catalog.relations r),'events',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM catalog.outbox o))::text`)
	if before != after {
		t.Fatal("failed duplicate normalization mutated relation/event history")
	}
}
