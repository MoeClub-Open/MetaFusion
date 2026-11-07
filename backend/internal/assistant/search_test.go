package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestDeepSeekNativeSearchAndFetch(t *testing.T) {
	s := NewStore(nil, nil)
	for _, operation := range []string{"search", "fetch"} {
		s.CompletionClient = &http.Client{Transport: providerRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.deepseek.com/v1/tools/"+operation || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("wrong native request: %s %s", r.Method, r.URL)
			}
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Fatal(err)
			}
			response := `{"title":"Page","content":"Source text"}`
			if operation == "search" {
				if in["query"] != "官方资料" || in["count"] != float64(5) || in["need_content"] != true {
					t.Fatalf("bad search payload: %v", in)
				}
				response = `{"results":[{"title":"Official","url":"https://example.org","snippet":"Summary","content":"Source"}]}`
			} else if in["url"] != "https://example.org" {
				t.Fatal(in)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
		})}
		out, err := s.deepseekSearch(t.Context(), "https://api.deepseek.com/v1", "test-key", operation, SearchInput{Query: "官方资料", Count: 5, NeedContent: true, URL: "https://example.org"})
		if err != nil || operation == "search" && len(out.Results) != 1 || operation == "fetch" && out.Content != "Source text" {
			t.Fatalf("native %s: %+v %v", operation, out, err)
		}
	}
}

func TestDuckSearchReadsResultsAndDecodesRedirect(t *testing.T) {
	html := `<html><div class="result results_links"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fofficial">Official &amp; source</a><a class="result__snippet">Read this source.</a></div></html>`
	out, err := parseDuckSearch([]byte(html), 5)
	if err != nil || len(out.Results) != 1 || out.Results[0].URL != "https://example.org/official" || out.Results[0].Title != "Official & source" || out.Results[0].Snippet != "Read this source." {
		t.Fatalf("parsed results: %+v %v", out, err)
	}
}

func TestPostgresSearchBillingReplayEmptyAndBYO(t *testing.T) {
	s, model, owner := accountingFixture(t)
	ctx := t.Context()
	provider := "3f0c0a17-0000-4000-8000-000000000011"
	encrypted, err := s.encryptSecret("platform-test-key", "search:"+provider)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE assistant.search_providers SET enabled=true,credits_per_request=5,encrypted_key=$1 WHERE id=$2`, encrypted, provider); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.CompletionClient = &http.Client{Transport: providerRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		var in SearchInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Query == "unknown" {
			return nil, errors.New("connection interrupted")
		}
		body := `{"results":[{"title":"Official","url":"https://example.org"}]}`
		if in.Query == "empty" {
			body = `{"results":[]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	opt := SearchOptions{RequestID: uuid.NewString(), UserID: owner, ProviderID: provider, Purpose: "curator:research", Budget: &CreditBudget{PurposePrefix: "curator:", MonthlyLimit: 6}}
	input := SearchInput{Query: "official", Count: 5}
	out, err := s.WebSearch(ctx, opt, "search", input)
	if err != nil || out.Credits != 5 {
		t.Fatalf("search: %+v %v", out, err)
	}
	out, err = s.WebSearch(ctx, opt, "search", input)
	if err != nil || out.Credits != 5 || calls.Load() != 1 {
		t.Fatalf("replay called upstream: %d %v", calls.Load(), err)
	}
	if _, err = s.Reserve(ctx, ReserveRequest{ID: uuid.NewString(), UserID: owner, ModelID: model.ID, InputTokens: 1, OutputTokens: 1, Purpose: "curator:manager", CreditBudget: opt.Budget}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("model ignored search spending: %v", err)
	}
	opt.Budget.MonthlyLimit = 20
	opt.RequestID = uuid.NewString()
	out, err = s.WebSearch(ctx, opt, "search", SearchInput{Query: "empty"})
	if err != nil || out.Credits != 0 {
		t.Fatalf("empty charged: %+v %v", out, err)
	}
	balance, err := s.SyncCredits(ctx, owner)
	if err != nil || balance.Credits != 95 || balance.ReservedCredits != 0 {
		t.Fatalf("balance: %+v %v", balance, err)
	}
	byo := uuid.NewString()
	secret, err := s.encryptSecret("byo-test-key", "byo:"+byo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO assistant.byo_providers(id,owner_id,name,base_url,encrypted_key) VALUES($1,$2,'Search key','https://api.deepseek.com',$3)`, byo, owner, secret); err != nil {
		t.Fatal(err)
	}
	opt.RequestID = uuid.NewString()
	opt.BYOProviderID = byo
	opt.UserID = "other-user"
	if _, err = s.WebSearch(ctx, opt, "search", input); err == nil {
		t.Fatal("used another user's key")
	}
	opt.UserID = owner
	opt.RequestID = uuid.NewString()
	out, err = s.WebSearch(ctx, opt, "search", input)
	if err != nil || out.Credits != 0 {
		t.Fatalf("BYO charged platform: %+v %v", out, err)
	}
	opt.BYOProviderID = ""
	opt.RequestID = uuid.NewString()
	out, err = s.WebSearch(ctx, opt, "search", SearchInput{Query: "unknown"})
	if err == nil || out.Credits != 5 {
		t.Fatalf("unknown outcome silently refunded: %+v %v", out, err)
	}
	before := calls.Load()
	if _, err = s.WebSearch(context.Background(), opt, "search", SearchInput{Query: "unknown"}); !errors.Is(err, ErrConflict) || calls.Load() != before {
		t.Fatal("uncertain call was sent again")
	}
}
