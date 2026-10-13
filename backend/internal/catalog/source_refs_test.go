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
		{"missing refs", refCommit(pool[:1], create()), "source_refs_required"},
		{"empty refs", refCommit(pool[:1], func() CommitOperation { op := create(); op.SourceRefs = []SourceRef{}; return op }()), "source_refs_required"},
		{"source without key", refCommit([]Source{{Kind: "url", Citation: "c", URL: "https://a.example"}}, create(SourceRef{Source: "a"})), "source_key_required"},
		{"client scope in pool", refCommit([]Source{{Kind: "url", Citation: "c", URL: "https://a.example", Key: "a", Scope: SourceScopeCommit}}, create(SourceRef{Source: "a"})), "source_scope_not_allowed"},
		{"unused source", refCommit(pool, create(SourceRef{Source: "a"})), "source_unused"},
		{"duplicate ref", refCommit(pool, create(SourceRef{Source: "a"}, SourceRef{Source: "a"})), "duplicate_source_ref"},
		{"bad pointer", refCommit(pool, create(SourceRef{Source: "a", Pointers: []string{"title"}})), "invalid_source_pointer"},
		{"create pointer missing", refCommit(pool, create(SourceRef{Source: "a", Pointers: []string{"/attributes/barcode"}})), "source_pointer_not_in_document"},
		{"update pointer not patched", refCommit(pool, update(SourceRef{Source: "a", Pointers: []string{"/title"}})), "source_pointer_not_patched"},
	}
	for _, tc := range cases {
		if err := validateCommitSourceRefs(tc.in); errCode(err) != tc.want {
			t.Errorf("%s: got %v want %s", tc.name, err, tc.want)
		}
	}
	// 合法：创建指针存在于文档；更新指针等于、前缀于或位于补丁路径之下。
	for _, ptrs := range [][]string{{"/attributes/catalog_number"}, {"/attributes"}, nil} {
		err := validateCommitSourceRefs(refCommit(pool, create(SourceRef{Source: "a", Pointers: ptrs}), func() CommitOperation {
			op := update(SourceRef{Source: "b", Pointers: ptrs})
			op.Ref = ""
			return op
		}()))
		if err != nil {
			t.Fatalf("valid refs %v rejected: %v", ptrs, err)
		}
	}
	whole := update(SourceRef{Source: "a", Pointers: []string{"/attributes/catalog_number/x"}})
	whole.Patch = []CommitPatch{{Path: "/attributes", Value: json.RawMessage(`{}`)}}
	if err := validateCommitSourceRefs(refCommit(pool[:1], whole)); err != nil {
		t.Fatalf("pointer under replaced object rejected: %v", err)
	}
}

// 历史修订的 scope="commit" 来源能正常读出（一次性迁移写入，新提交不得自带）。
func TestSourceScopeCommitRoundTrip(t *testing.T) {
	var got []Source
	raw := `[{"kind":"url","citation":"c","url":"https://a.example","scope":"commit"}]`
	if err := json.Unmarshal([]byte(raw), &got); err != nil || len(got) != 1 || got[0].Scope != SourceScopeCommit {
		t.Fatalf("scope round trip: %+v %v", got, err)
	}
	if b, _ := json.Marshal(Source{Kind: "url", Citation: "c"}); string(b) != `{"kind":"url","citation":"c"}` {
		t.Fatalf("new sources must not carry scope: %s", b)
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
	if err != nil || preview.Applied {
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
	// 池里有未被引用的来源：preview 与 push 都是错误，不再只是警告。
	up.Sources = []Source{keyed("rye_page", in.Sources[0].URL), keyed("other", "https://example.org/unrelated")}
	up.Operations[0].SourceRefs = []SourceRef{{Source: "rye_page", Pointers: []string{"/title"}}}
	for _, preview := range []bool{true, false} {
		if _, err := f.s.PushCommit(ctx, up, f.u, preview); errCode(err) != "source_unused" {
			t.Fatalf("unused source (preview=%v): %v", preview, err)
		}
	}
	up.Sources = up.Sources[:1]
	if _, err := f.s.PushCommit(ctx, up, f.u, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := revisionSources(e.ID); len(got) != 1 || got[0].Key != "rye_page" {
		t.Fatalf("update revision sources: %+v", got)
	}
	// 没写 source_refs 的操作：preview 与 push 都直接拒绝，不再继承来源池。
	missing := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "l", Document: doc("missing", "")})
	missing.Operations[0].SourceRefs = nil
	missing.Sources = in.Sources[:1]
	for _, preview := range []bool{true, false} {
		if _, err := f.s.PushCommit(ctx, missing, f.u, preview); errCode(err) != "source_refs_required" {
			t.Fatalf("missing refs (preview=%v): %v", preview, err)
		}
	}
}
