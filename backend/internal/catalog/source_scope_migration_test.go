package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/metafusion/metafusion-app/migrations"
)

// 000029：历史修订里无 key 的来源标记 scope="commit"；带 key 的新来源不动；重复执行无副作用。
func TestPostgresRevisionSourceScopeMigration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sqlText, err := migrations.FS.ReadFile("000029_revision_source_scope.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacy := `[{"kind":"url","citation":"old","url":"https://a.example"},{"kind":"publication","citation":"book"}]`
	keyed := `[{"kind":"url","citation":"new","url":"https://b.example","key":"b","pointers":["/title"]}]`
	ids := map[string]int64{}
	for name, src := range map[string]string{"legacy": legacy, "keyed": keyed, "empty": `[]`} {
		var id int64
		if err := f.s.DB.QueryRowContext(ctx, `INSERT INTO catalog.revisions(target_id,version,edit_note,sources,snapshot) VALUES($1,1,'n',$2::jsonb,'{}') RETURNING id`, "scope-"+name, src).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	read := func(name string) []Source {
		var raw []byte
		if err := f.s.DB.QueryRowContext(ctx, `SELECT sources FROM catalog.revisions WHERE id=$1`, ids[name]).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var s []Source
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for run := 0; run < 2; run++ {
		if _, err := f.s.DB.ExecContext(ctx, string(sqlText)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := read("legacy"); len(got) != 2 || got[0].Scope != SourceScopeCommit || got[1].Scope != SourceScopeCommit || got[0].URL != "https://a.example" {
			t.Fatalf("run %d legacy: %+v", run, got)
		}
		if got := read("keyed"); len(got) != 1 || got[0].Scope != "" || got[0].Key != "b" || got[0].Pointers[0] != "/title" {
			t.Fatalf("run %d keyed: %+v", run, got)
		}
		if got := read("empty"); len(got) != 0 {
			t.Fatalf("run %d empty: %+v", run, got)
		}
	}
	var pending int
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.revisions WHERE EXISTS (SELECT 1 FROM jsonb_array_elements(sources) e WHERE jsonb_typeof(e)='object' AND NOT (e ? 'key') AND NOT (e ? 'scope'))`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending after migration: %d %v", pending, err)
	}
}
