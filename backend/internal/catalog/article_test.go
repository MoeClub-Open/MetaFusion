package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestArticleBodyValidationAndSchema(t *testing.T) {
	d := Defaults()
	for _, tc := range []struct{ name, body, want string }{
		{"historical translation", "", ""},
		{"markdown", "## 背景\n本文[^source]\n\n[^source]: https://example.org/source", ""},
		{"unicode limit", strings.Repeat("文", MaxArticleLength), ""},
		{"over limit", strings.Repeat("文", MaxArticleLength+1), "article_too_long"},
		{"null", "text\x00text", "invalid_article_body"},
		{"invalid utf8", string([]byte{0xff}), "invalid_article_body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Entity{Kind: "work", Title: "Article", State: "active", Translations: map[string]Translation{"zh-CN": {Title: "Article", Body: tc.body}}}
			err := d.validateEntityContent(e, allowAllRef, false)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Fatalf("validation = %v, want %q", err, tc.want)
			}
		})
	}
	schemas := OpenAPI()["components"].(map[string]any)["schemas"].(map[string]any)
	body := schemas["Translation"].(map[string]any)["properties"].(map[string]any)["body"].(map[string]any)
	if body["maxLength"] != MaxArticleLength || body["type"] != "string" {
		t.Fatalf("article schema: %v", body)
	}
}

func TestArticleSearchIncludesLateTextAndChunkBoundaries(t *testing.T) {
	body := strings.Repeat("文", 2040) + "跨越分块边界的正文关键词" + strings.Repeat("字", 4000) + "late-article-only-token"
	doc := makeSearchDocument(Entity{Kind: "work", Title: "Title", Translations: map[string]Translation{"zh-CN": {Title: "Title", Summary: "Summary", Body: body}}})
	for _, values := range [][]string{doc.SearchText, doc.SearchTextExact, doc.SearchTextGrams} {
		text := strings.Join(values, "\n")
		for _, token := range []string{"Summary", "跨越分块边界的正文关键词", "late-article-only-token"} {
			if !strings.Contains(text, token) {
				t.Fatalf("search text lost %q", token)
			}
		}
	}
	if strings.Contains(strings.Join(doc.TitleText, "\n"), "late-article-only-token") {
		t.Fatal("article content changed identity/title search")
	}
}

func TestPostgresArticleCommitRevisionsAndConflicts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	initial := f.save(Entity{Kind: "work", Title: "Article work", OriginalLanguage: "en", Attributes: map[string]any{"language": "ja"},
		Translations: map[string]Translation{"en": {Title: "Article work", Summary: "Short introduction", Body: "## Background\nOriginal article"}, "ja": {Title: "記事", Body: "## 背景\n元の本文"}}})
	metadataEdit := fixtureCommit(f, updateCommit(initial, patchValue("/attributes/language", "en")))
	if _, err := f.s.PushCommit(ctx, metadataEdit, f.u, false); err != nil {
		t.Fatal(err)
	}
	newBody := "## Background\nRevised article[^official]\n\n[^official]: https://example.org/source"
	op := updateCommit(initial, patchValue("/translations/en/body", newBody))
	op.SourceRefs = []SourceRef{{Source: "fixture", Pointers: []string{"/translations/en/body"}}}
	articleEdit := fixtureCommit(f, op)
	if _, err := f.s.PushCommit(ctx, articleEdit, f.u, true); err != nil {
		t.Fatalf("article preview: %v", err)
	}
	preview, err := f.s.Get(ctx, initial.ID, &f.u)
	if err != nil || preview.Translations["en"].Body != initial.Translations["en"].Body || preview.Version != 2 {
		t.Fatalf("preview persisted article: %+v %v", preview, err)
	}
	out, err := f.s.PushCommit(ctx, articleEdit, f.u, false)
	if err != nil || len(out.Items) != 1 || !out.Items[0].Merged {
		t.Fatalf("disjoint structured/article edits: %+v %v", out, err)
	}
	current, err := f.s.Get(ctx, initial.ID, nil)
	if err != nil || current.Version != 3 || current.Translations["en"].Body != newBody || current.Attributes["language"] != "en" || current.Translations["en"].Summary != "Short introduction" || current.Translations["ja"].Body != initial.Translations["ja"].Body {
		t.Fatalf("article round-trip lost data: %+v %v", current, err)
	}
	history, err := f.s.Revisions(ctx, initial.ID, nil)
	if err != nil || len(history) != 3 {
		t.Fatalf("history: %d %v", len(history), err)
	}
	var snapshot Entity
	var sources []Source
	if err := json.Unmarshal([]byte(encode(history[0]["snapshot"])), &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encode(history[0]["sources"])), &sources); err != nil {
		t.Fatal(err)
	}
	if snapshot.Translations["en"].Body != newBody || len(sources) != 1 || len(sources[0].Pointers) != 1 || sources[0].Pointers[0] != "/translations/en/body" {
		t.Fatalf("article snapshot/evidence: %+v %+v", snapshot.Translations, sources)
	}
	conflicting := fixtureCommit(f, updateCommit(initial, patchValue("/translations/en/body", "conflicting article")))
	_, err = f.s.PushCommit(ctx, conflicting, f.u, false)
	var conflict *commitConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflict.Conflicts) != 1 || conflict.Conflict.Conflicts[0].Pointer != "/translations/en/body" {
		t.Fatalf("concurrent body conflict: %v", err)
	}
	if _, err = f.s.Save(ctx, Edit{Entity: initial, ExpectedVersion: initial.Version, EditNote: "stale full replacement", Sources: fixtureSources()}, f.u); !errors.Is(err, errVersionConflict) {
		t.Fatalf("stale replacement: %v", err)
	}
	draft := f.save(Entity{Kind: "work", Title: "Private article", State: "active", Translations: map[string]Translation{"en": {Title: "Private article", Body: "private body"}}})
	draft = f.retire(draft)
	if _, err := f.s.Get(ctx, draft.ID, nil); err == nil {
		t.Fatal("draft article leaked to anonymous reader")
	}
	if _, err := f.s.Revisions(ctx, draft.ID, nil); err == nil {
		t.Fatal("draft article revision leaked to anonymous reader")
	}
}
