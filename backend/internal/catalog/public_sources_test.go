package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicFetchHTMLAndJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected method or credentials")
		}
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"source":"original"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><style>.secret{}</style><script>steal()</script></head><body><h1>原始标题</h1><p>可读 &amp; 内容</p></body></html>`)
	}))
	defer server.Close()
	out, err := fetchPublicSource(context.Background(), server.Client(), PublicFetchRequest{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "原始标题 可读 & 内容" || out.ContentHash == "" || out.FetchedAt.IsZero() || out.URL != server.URL {
		t.Fatalf("incomplete/scripting output: %#v", out)
	}
	out, err = fetchPublicSource(context.Background(), server.Client(), PublicFetchRequest{URL: server.URL + "/json"})
	if err != nil || out.Data.(map[string]any)["source"] != "original" {
		t.Fatalf("JSON not preserved: %#v %v", out, err)
	}
}

func TestPublicFetchBoundsAndExplicitUpstreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		kind   string
		want   string
	}{
		{"denied", 403, "private upstream diagnostics", "text/plain", "public_source_access_denied"},
		{"limited", 429, "", "text/plain", "public_source_rate_limited"},
		{"binary", 200, "binary", "image/png", "public_source_content_type_unsupported"},
		{"oversize", 200, strings.Repeat("x", maxPublicSourceBytes+1), "text/plain", "public_source_too_large"},
		{"bad_json", 200, "not JSON", "application/json", "public_source_invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.kind)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := fetchPublicSource(context.Background(), server.Client(), PublicFetchRequest{URL: server.URL})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("want %s got %v", tc.want, err)
			}
		})
	}
}

func TestPublicFetchRedirectBudget(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/again")
		w.WriteHeader(302)
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	_, err := fetchPublicSource(context.Background(), client, PublicFetchRequest{URL: server.URL})
	if err == nil || err.Error() != "public_source_redirect_limit" || calls != 4 {
		t.Fatalf("redirect bound: calls=%d err=%v", calls, err)
	}
}

func TestWikiReadsOriginalRevisionWithAttribution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("action") != "query" || q.Get("titles") != "棘アリ" || q.Get("rvslots") != "main" || q.Get("formatversion") != "2" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"query":{"pages":[{"pageid":42,"title":"棘アリ","revisions":[{"revid":10,"slots":{"main":{"content":"== 原文 ==\n資料"}}}]}]}}`)
	}))
	defer server.Close()
	settings := PublicSourceSettings{Enabled: true, WikiSites: []PublicWikiSite{{ID: "fixture", Enabled: true, APIURL: server.URL + "/api.php", ArticleURL: server.URL + "/wiki/{title}", License: "source-license", Attribution: "Original contributors"}}}
	out, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Title: "棘アリ"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "== 原文 ==\n資料" || out.Title != "棘アリ" || out.License != "source-license" || !strings.Contains(out.Attribution, "Original contributors") || !strings.Contains(out.Attribution, "/wiki/") || out.ContentHash == "" {
		t.Fatalf("original or attribution lost: %#v", out)
	}
}

func TestWikiSearchAndDisabledSites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") != "search" || r.URL.Query().Get("srlimit") != "5" {
			t.Errorf("unexpected search query %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": []any{map[string]any{"title": "Original title"}}}})
	}))
	defer server.Close()
	settings := PublicSourceSettings{Enabled: true, WikiSites: []PublicWikiSite{{ID: "fixture", Enabled: true, APIURL: server.URL, ArticleURL: server.URL + "/{title}"}}}
	if _, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Query: "original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Query: strings.Repeat("字", 500)}); err != nil {
		t.Fatalf("500 Unicode characters were counted as bytes: %v", err)
	}
	if _, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Query: strings.Repeat("字", 501)}); err == nil || err.Error() != "invalid_wiki_query" {
		t.Fatalf("overlong Unicode query accepted: %v", err)
	}
	settings.WikiSites[0].Enabled = false
	_, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Title: "original"})
	var sourceErr *publicSourceError
	if !errors.As(err, &sourceErr) || sourceErr.code != "wiki_site_disabled" {
		t.Fatalf("disabled site was queried: %v", err)
	}
	if _, err := wikiPublicSource(context.Background(), server.Client(), settings, PublicWikiRequest{Site: "fixture", Title: "original", Query: "also"}); err == nil {
		t.Fatal("ambiguous title+query accepted")
	}
}

func TestPublicSourceSettingsRejectsPrivateAndDuplicateSites(t *testing.T) {
	site := PublicWikiSite{ID: "fixture", Name: "Fixture", APIURL: "https://8.8.8.8/api.php", ArticleURL: "https://8.8.8.8/{title}"}
	settings := PublicSourceSettings{Enabled: true, WikiSites: []PublicWikiSite{site}}
	if err := validatePublicSourceSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	settings.WikiSites = append(settings.WikiSites, site)
	if err := validatePublicSourceSettings(context.Background(), settings); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	settings.WikiSites = settings.WikiSites[:1]
	settings.WikiSites[0].APIURL = "http://169.254.169.254/latest/meta-data/"
	if err := validatePublicSourceSettings(context.Background(), settings); err == nil {
		t.Fatal("metadata endpoint accepted")
	}
}

func TestPostgresPublicSourceSettingsConcurrencyAndPermissions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	initial, err := f.s.PublicSourceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Enabled || len(initial.WikiSites) != 2 || initial.UpdatedAt.IsZero() {
		t.Fatalf("missing usable initial defaults: %#v", initial)
	}
	next := PublicSourceSettings{Enabled: false, WikiSites: []PublicWikiSite{{ID: "fixture", Name: "Fixture", APIURL: "https://8.8.8.8/api.php", ArticleURL: "https://8.8.8.8/{title}", Enabled: true, License: "source-license", Attribution: "original contributors"}}}
	request := PublicSourceSave{Settings: next, ExpectedUpdatedAt: initial.UpdatedAt}
	if _, err := f.s.SavePublicSourceSettings(ctx, request, User{ID: "normal"}); !errors.Is(err, errForbidden) {
		t.Fatalf("ordinary user changed source configuration: %v", err)
	}
	saved, err := f.s.SavePublicSourceSettings(ctx, request, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Enabled || saved.UpdatedAt.Equal(initial.UpdatedAt) || saved.WikiSites[0].License != "source-license" {
		t.Fatalf("configuration not saved: %#v", saved)
	}
	if _, err := f.s.SavePublicSourceSettings(ctx, request, f.u); !errors.Is(err, errVersionConflict) {
		t.Fatalf("stale administrator overwrote settings: %v", err)
	}
	read, err := f.s.PublicSourceSettings(ctx)
	if err != nil || read.Enabled || len(read.WikiSites) != 1 || read.WikiSites[0].Attribution != "original contributors" {
		t.Fatalf("persisted configuration differs: %#v %v", read, err)
	}
	if _, err := f.s.FetchPublicSource(ctx, PublicFetchRequest{URL: "https://8.8.8.8/"}); err == nil || err.Error() != "public_sources_disabled" {
		t.Fatalf("disabled source fetched content: %v", err)
	}
}
