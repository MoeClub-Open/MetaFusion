package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func refCommit(sources []Source, ops ...CommitOperation) CatalogCommit {
	return CatalogCommit{ID: uuid.NewString(), DefinitionsETag: "e", EditNote: "n", Sources: sources, Operations: ops}
}

func keyed(key, url string) Source {
	return Source{Kind: "url", Citation: key + " official page", URL: url, Key: key}
}

func errCode(err error) string {
	if err == nil {
		return ""
	}
	var op *commitOperationError
	if errors.As(err, &op) {
		return op.Cause.Error()
	}
	return err.Error()
}

func TestSourceRefsValidation(t *testing.T) {
	id := uuid.NewString()
	create := func(refs ...SourceRef) CommitOperation {
		return CommitOperation{Target: "entity", Action: "create", Ref: "a", Document: json.RawMessage(`{"kind":"work","title":"t","attributes":{"catalog_number":"X-1"}}`), ReviewedCandidateIDs: []string{}, SourceRefs: refs}
	}
	update := func(refs ...SourceRef) CommitOperation {
		return CommitOperation{Target: "entity", Action: "update", ID: id, BaseVersion: 1, Patch: []CommitPatch{{Path: "/attributes/catalog_number", Value: json.RawMessage(`"X"`)}}, SourceRefs: refs}
	}
	pool := []Source{keyed("a", "https://a.example/1"), keyed("b", "https://b.example/1")}
	cases := []struct {
		name string
		in   CatalogCommit
		want string
	}{
		{"duplicate key", refCommit([]Source{keyed("a", "https://a.example"), keyed("a", "https://b.example")}, create(SourceRef{Source: "a"})), "duplicate_source_key"},
		{"bad key", refCommit([]Source{keyed("1x", "https://a.example")}, create(SourceRef{Source: "1x"})), "invalid_source_key"},
		{"client pointers in pool", refCommit([]Source{{Kind: "url", Citation: "c", URL: "https://a.example", Key: "a", Pointers: []string{"/title"}}}, create(SourceRef{Source: "a"})), "source_pointers_not_allowed"},
		{"unknown ref", refCommit(pool, create(SourceRef{Source: "zzz"})), "unknown_source_ref"},
		{"empty refs", refCommit(pool, func() CommitOperation { op := create(); op.SourceRefs = []SourceRef{}; return op }()), "source_refs_empty"},
		{"duplicate ref", refCommit(pool, create(SourceRef{Source: "a"}, SourceRef{Source: "a"})), "duplicate_source_ref"},
		{"bad pointer", refCommit(pool, create(SourceRef{Source: "a", Pointers: []string{"title"}})), "invalid_source_pointer"},
		{"create pointer missing", refCommit(pool, create(SourceRef{Source: "a", Pointers: []string{"/attributes/barcode"}})), "source_pointer_not_in_document"},
		{"update pointer not patched", refCommit(pool, update(SourceRef{Source: "a", Pointers: []string{"/title"}})), "source_pointer_not_patched"},
	}
	for _, tc := range cases {
		if _, err := validateCommitSourceRefs(tc.in); errCode(err) != tc.want {
			t.Errorf("%s: got %v want %s", tc.name, err, tc.want)
		}
	}
	// 合法：创建指针存在于文档；更新指针等于、前缀于或位于补丁路径之下。
	for _, ptrs := range [][]string{{"/attributes/catalog_number"}, {"/attributes"}, nil} {
		w, err := validateCommitSourceRefs(refCommit(pool, create(SourceRef{Source: "a", Pointers: ptrs}), func() CommitOperation {
			op := update(SourceRef{Source: "b", Pointers: ptrs})
			op.Ref = ""
			return op
		}()))
		if err != nil || len(w) != 0 {
			t.Fatalf("valid refs %v rejected: %v %v", ptrs, err, w)
		}
	}
	whole := update(SourceRef{Source: "a", Pointers: []string{"/attributes/catalog_number/x"}})
	whole.Patch = []CommitPatch{{Path: "/attributes", Value: json.RawMessage(`{}`)}}
	if _, err := validateCommitSourceRefs(refCommit(pool, whole)); err != nil {
		t.Fatalf("pointer under replaced object rejected: %v", err)
	}
}

func TestSourceRefsTransitionWarningsAndStrictMode(t *testing.T) {
	pool := []Source{keyed("a", "https://a.example/1"), keyed("b", "https://b.example/1"), keyed("unused", "https://c.example/1")}
	op := func(ref string, refs ...SourceRef) CommitOperation {
		return CommitOperation{Target: "entity", Action: "create", Ref: ref, Document: json.RawMessage(`{"kind":"work","title":"t"}`), ReviewedCandidateIDs: []string{}, SourceRefs: refs}
	}
	in := refCommit(pool, op("x", SourceRef{Source: "a"}), op("y", SourceRef{Source: "b"}))
	w, err := validateCommitSourceRefs(in)
	if err != nil || len(w) != 1 || w[0].Code != "source_unused" || w[0].Source != "unused" {
		t.Fatalf("unused warning: %+v %v", w, err)
	}
	in = refCommit(pool[:2], op("x", SourceRef{Source: "a"}), op("y"))
	w, err = validateCommitSourceRefs(in)
	if err != nil || len(w) != 1 || w[0].Code != "source_refs_missing" || *w[0].Operation != 1 {
		t.Fatalf("missing warning: %+v %v", w, err)
	}
	// 未写 source_refs 的操作过渡期继承整个池。
	if got := operationSources(in, in.Operations[1]); len(got) != 2 {
		t.Fatalf("legacy op must inherit pool: %+v", got)
	}
	SetStrictSourceRefs(true)
	defer SetStrictSourceRefs(false)
	if _, err = validateCommitSourceRefs(in); errCode(err) != "source_refs_missing" {
		t.Fatalf("strict mode: %v", err)
	}
	// 完全不用 key 的旧式提交在过渡期照旧通过（只有警告）。
	SetStrictSourceRefs(false)
	legacy := refCommit([]Source{{Kind: "url", Citation: "c", URL: "https://a.example"}}, op("x"))
	if w, err = validateCommitSourceRefs(legacy); err != nil || len(w) != 1 {
		t.Fatalf("legacy commit: %+v %v", w, err)
	}
}

// rye 类场景：一次提交写三个发行，每个发行只引用自己的官方页；修订里不再混入别的来源。
func TestPostgresCommitSourceRefsDoNotMix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	doc := func(title, catalog string) json.RawMessage {
		return json.RawMessage(encode(map[string]any{"kind": "work", "title": title, "status": "published", "translations": map[string]any{"en": map[string]any{"title": title}}}))
	}
	in := fixtureCommit(f,
		CommitOperation{Target: "entity", Action: "create", Ref: "rye", Document: doc("rye", "TECI-1676"), SourceRefs: []SourceRef{{Source: "rye_page", Pointers: []string{"/title"}}}},
		CommitOperation{Target: "entity", Action: "create", Ref: "compass", Document: doc("コンパスソング", "TEI-170"), SourceRefs: []SourceRef{{Source: "compass_page"}}},
		CommitOperation{Target: "entity", Action: "create", Ref: "nadame", Document: doc("なだめスかし Negotiation", "TEI-144"), SourceRefs: []SourceRef{{Source: "nadame_page"}, {Source: "rye_page", Pointers: []string{"/translations/en"}}}},
	)
	in.Sources = []Source{
		keyed("rye_page", "https://www.teichiku.co.jp/artist/kano/TECI-1676.html"),
		keyed("compass_page", "https://www.teichiku.co.jp/artist/kano/TEI-170.html"),
		keyed("nadame_page", "https://www.teichiku.co.jp/artist/kano/TEI-144.html"),
	}
	preview, err := f.s.PushCommit(ctx, in, f.u, true)
	if err != nil || len(preview.Warnings) != 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	out, err := f.s.PushCommit(ctx, in, f.u, false)
	if err != nil {
		t.Fatal(err)
	}
	revisionSources := func(id string) []Source {
		var raw []byte
		if err := f.s.DB.QueryRowContext(ctx, `SELECT sources FROM catalog.revisions WHERE target_id=$1 ORDER BY id DESC LIMIT 1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var s []Source
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	rye := revisionSources(out.Refs["rye"])
	if len(rye) != 1 || rye[0].URL != in.Sources[0].URL || len(rye[0].Pointers) != 1 || rye[0].Pointers[0] != "/title" {
		t.Fatalf("rye sources mixed: %+v", rye)
	}
	compass := revisionSources(out.Refs["compass"])
	if len(compass) != 1 || compass[0].Key != "compass_page" || compass[0].Pointers != nil {
		t.Fatalf("compass sources: %+v", compass)
	}
	nadame := revisionSources(out.Refs["nadame"])
	if len(nadame) != 2 || nadame[1].Key != "rye_page" || nadame[1].Pointers[0] != "/translations/en" {
		t.Fatalf("nadame sources: %+v", nadame)
	}
	// 更新操作同样只记录被引用的来源，并保留字段指针。
	e, err := f.s.Get(ctx, out.Refs["rye"], &f.u)
	if err != nil {
		t.Fatal(err)
	}
	up := fixtureCommit(f, updateCommit(e, CommitPatch{Path: "/title", Value: json.RawMessage(`"rye (通常盤)"`)}))
	up.Sources = []Source{keyed("rye_page", in.Sources[0].URL), keyed("other", "https://example.org/unrelated")}
	up.Operations[0].SourceRefs = []SourceRef{{Source: "rye_page", Pointers: []string{"/title"}}}
	receipt, err := f.s.PushCommit(ctx, up, f.u, false)
	if err != nil || len(receipt.Warnings) != 1 || receipt.Warnings[0].Code != "source_unused" {
		t.Fatalf("update: %+v %v", receipt, err)
	}
	if got := revisionSources(e.ID); len(got) != 1 || got[0].Key != "rye_page" {
		t.Fatalf("update revision sources: %+v", got)
	}
	// 过渡期：未写 source_refs 的操作继承整个来源池，并带 source_refs_missing 警告。
	legacy := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "l", Document: doc("legacy", "")})
	legacy.Sources = in.Sources
	lr, err := f.s.PushCommit(ctx, legacy, f.u, false)
	if err != nil || len(lr.Warnings) != 1 || lr.Warnings[0].Code != "source_refs_missing" {
		t.Fatalf("legacy: %+v %v", lr, err)
	}
	if got := revisionSources(lr.Refs["l"]); len(got) != 3 {
		t.Fatalf("legacy inherits pool: %+v", got)
	}
}
