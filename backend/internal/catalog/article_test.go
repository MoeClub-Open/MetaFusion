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
	newBody := "## Background\nRevised article[^official]\n\n[^official]: https://example.org/source"
	_, _, metadata := prepareWorkspace(t, f.s, f.u, WorkspaceOperation{Target: "entity", Action: "update", ID: initial.ID, Patch: []CommitPatch{patchValue("/attributes/language", "en")}})
	_, articleCommit, article := prepareWorkspace(t, f.s, f.u, WorkspaceOperation{Target: "entity", Action: "update", ID: initial.ID, Patch: []CommitPatch{patchValue("/translations/en/body", newBody)}})
	_, _, conflicting := prepareWorkspace(t, f.s, f.u, WorkspaceOperation{Target: "entity", Action: "update", ID: initial.ID, Patch: []CommitPatch{patchValue("/translations/en/body", "conflicting article")}})
	preview, err := f.s.Get(ctx, initial.ID, &f.u)
	if err != nil || preview.Translations["en"].Body != initial.Translations["en"].Body || preview.Version != 1 || articleCommit.MergedAt != nil {
		t.Fatalf("working copy changed main catalog: %+v %v", preview, err)
	}
	if _, err := f.s.PushWorkspace(ctx, metadata.ID, WorkspacePush{ExpectedVersion: metadata.Version, ExpectedHead: *metadata.HeadCommitID}, f.u); err != nil {
		t.Fatal(err)
	}
	out, err := f.s.PushWorkspace(ctx, article.ID, WorkspacePush{ExpectedVersion: article.Version, ExpectedHead: *article.HeadCommitID}, f.u)
	if err != nil || !out.Applied {
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
	if err := json.Unmarshal([]byte(encode(history[0]["snapshot"])), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Translations["en"].Body != newBody || history[0]["commit_id"] != articleCommit.ID {
		t.Fatalf("article snapshot/provenance: %+v", history[0])
	}
	_, err = f.s.PushWorkspace(ctx, conflicting.ID, WorkspacePush{ExpectedVersion: conflicting.Version, ExpectedHead: *conflicting.HeadCommitID}, f.u)
	var conflict *workspaceConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Pointer != "/translations/en/body" {
		t.Fatalf("concurrent body conflict: %v", err)
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
