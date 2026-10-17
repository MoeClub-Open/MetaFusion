package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

// These tests keep the HTTP identity, catalog, migrations, reservation, provider
// protocol, accounting and persistence real. Only the external LLM is replaced;
// its next turn must consume the actual catalog tool result from the previous
// one. The code-only transport never makes an outbound connection.
type assistantE2E struct {
	t     *testing.T
	s     *Store
	cat   *catalog.Store
	model Model
	user  catalog.User
	other catalog.User
	admin catalog.User
}

func assistantE2EFixture(t *testing.T) *assistantE2E {
	t.Helper()
	s, model, owner := accountingFixture(t)
	cat := &catalog.Store{DB: s.DB}
	if err := cat.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, update := range []struct {
		query string
		args  []any
	}{
		{`UPDATE assistant.providers SET base_url='https://93.184.216.34/v1',monthly_budget_microusd=1000000`, nil},
		// Leave room for several full-context reservations after actual charges.
		// Runtime reserves the configured token ceiling, not the prompt estimate.
		{`UPDATE assistant.models SET context_window=200000,cached_input_price_microusd_per_million=1000000 WHERE id=$1`, []any{model.ID}},
		{`UPDATE assistant.settings SET registration_credits=1000000,monthly_budget_microusd=1000000,max_tool_rounds=4`, nil},
	} {
		if _, err := s.DB.Exec(update.query, update.args...); err != nil {
			t.Fatal(err)
		}
	}
	return &assistantE2E{t: t, s: s, cat: cat, model: model,
		user:  catalog.User{ID: owner, Username: "editor", Permissions: []string{catalog.PermissionEntityEdit}},
		other: catalog.User{ID: uuid.NewString(), Username: "other", Permissions: []string{catalog.PermissionEntityEdit}},
		admin: catalog.User{ID: uuid.NewString(), Username: "admin", Permissions: []string{"*"}},
	}
}

func (f *assistantE2E) seedWork() catalog.Entity {
	f.t.Helper()
	entity, err := f.cat.Save(context.Background(), catalog.Edit{
		Entity: catalog.Entity{Kind: "work", Title: "Original Work", State: "active",
			Attributes:   map[string]any{"language": "en-US", "edition_date": "2024-01-02"},
			Translations: map[string]catalog.Translation{"en-US": {Title: "Original Work", Body: "Original body"}}},
		EditNote: "Publisher source fixture", Sources: []catalog.Source{{Kind: "url", Citation: "Publisher record", URL: "https://publisher.example/product"}},
	}, f.admin)
	if err != nil {
		f.t.Fatal(err)
	}
	return entity
}

func (f *assistantE2E) engine(identity *catalog.User) http.Handler {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// This is the resolved identity boundary used by catalog's real middleware;
	// no user ID or permissions from an assistant JSON body are ever installed.
	if identity != nil {
		r.Use(func(c *gin.Context) { c.Set("catalog_user", identity); c.Next() })
	}
	HTTP{Store: f.s, Catalog: f.cat}.Register(r.Group("/api"))
	return r
}

func (f *assistantE2E) request(identity *catalog.User, method, path string, body any, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/assistant"+path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine(identity).ServeHTTP(w, req)
	if w.Code != want {
		f.t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func e2eDecode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v: %s", err, w.Body.String())
	}
	return out
}

type e2eRoundTripper func(*http.Request) (*http.Response, error)

func (f e2eRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type e2eProviderPayload struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools"`
}

func (f *assistantE2E) provider(owner, key, model string, next func(int, e2eProviderPayload) Message) *int {
	f.t.Helper()
	calls := 0
	f.s.CompletionClient = &http.Client{Transport: e2eRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://93.184.216.34/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			f.t.Fatal("provider request used the wrong endpoint, method or credential")
		}
		var body e2eProviderPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != model {
			f.t.Fatalf("provider payload: model=%s err=%v", body.Model, err)
		}
		var pending int
		var reservedCredits, reservedCost int64
		var byo bool
		if err := f.s.DB.QueryRow(`SELECT count(*),coalesce(sum(reserved_credits),0),coalesce(sum(reserved_cost_microusd),0),coalesce(bool_or(byo_credential_id IS NOT NULL),false) FROM assistant.usage WHERE user_id=$1 AND status='reserved' AND invoked_at IS NOT NULL`, owner).Scan(&pending, &reservedCredits, &reservedCost, &byo); err != nil || pending != 1 {
			f.t.Fatalf("provider called without exactly one claimed reservation: %d %v", pending, err)
		}
		if !byo && (reservedCredits <= 0 || reservedCost <= 0) || byo && (reservedCredits != 0 || reservedCost != 0) {
			f.t.Fatalf("wrong budget reservation before provider invocation: byo=%t credits=%d cost=%d", byo, reservedCredits, reservedCost)
		}
		message := next(calls, body)
		raw, err := json.Marshal(map[string]any{"id": fmt.Sprintf("completion-%d", calls), "choices": []any{map[string]any{"message": message, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 40, "completion_tokens": 7, "prompt_tokens_details": map[string]any{"cached_tokens": 5}}})
		if err != nil {
			f.t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
	})}
	return &calls
}

func e2eTool(name string, arguments any) Message {
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: uuid.NewString(), Type: "function"}
	call.Function.Name, call.Function.Arguments = name, string(raw)
	return Message{Role: "assistant", ToolCalls: []ToolCall{call}}
}

func e2eLastTool[T any](t *testing.T, p e2eProviderPayload) T {
	t.Helper()
	if len(p.Messages) == 0 || p.Messages[len(p.Messages)-1].Role != "tool" {
		t.Fatal("next completion did not receive the real tool result")
	}
	var out T
	if err := json.Unmarshal([]byte(p.Messages[len(p.Messages)-1].Content), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *assistantE2E) assertPlatformCharges(user catalog.User, rounds int, editRewards int64) {
	f.t.Helper()
	usage, err := f.s.UsageRecords(context.Background(), user.ID, false, 50, 0)
	if err != nil || usage.Total != rounds {
		f.t.Fatalf("persisted usage: %+v %v", usage, err)
	}
	for _, u := range usage.Items {
		if u.UserID != user.ID || u.ProviderID != f.model.ProviderID || u.ModelID != f.model.ID || u.Status != "completed" || u.Usage != (TokenUsage{InputTokens: 40, OutputTokens: 7, CachedInputTokens: 5}) || u.Credits != 47 || u.CostMicroUSD != 47 {
			f.t.Fatalf("incorrect actual usage or owner: %+v", u)
		}
	}
	balance, err := f.s.SyncCredits(context.Background(), user.ID)
	if err != nil || balance.Credits != 1000000-int64(rounds*47)+editRewards || balance.ReservedCredits != 0 {
		f.t.Fatalf("credit settlement: %+v %v", balance, err)
	}
	budgets, err := f.s.Budgets(context.Background())
	if err != nil || len(budgets) != 2 {
		f.t.Fatalf("platform and provider budgets missing: %+v %v", budgets, err)
	}
	for _, budget := range budgets {
		if budget.SpentMicroUSD != int64(rounds*47) || budget.ReservedMicroUSD != 0 {
			f.t.Fatalf("budget leaked reservation or charged estimate: %+v", budget)
		}
	}
}

func TestPostgresAssistantHTTPReadsCatalogAndReplaysWithoutCharging(t *testing.T) {
	f := assistantE2EFixture(t)
	work := f.seedWork()
	calls := f.provider(f.user.ID, "integration-test-secret", "test-model", func(round int, payload e2eProviderPayload) Message {
		if round == 1 {
			if len(payload.Tools) == 0 {
				t.Fatal("catalog tools were not supplied")
			}
			return e2eTool("catalog_get", map[string]any{"id": work.ID})
		}
		got := e2eLastTool[catalog.Entity](t, payload)
		if got.ID != work.ID || got.Title != work.Title || got.Version != work.Version {
			t.Fatalf("catalog tool returned a fabricated or incomplete entity: %+v", got)
		}
		return Message{Role: "assistant", Content: "The catalog records Original Work."}
	})
	req := ChatRequest{Message: "Read the recorded work title", ClientRequestID: uuid.NewString()}
	w := f.request(&f.user, "POST", "/chat", req, 200)
	response := e2eDecode[ChatResponse](t, w)
	if response.Error != "" || response.Answer != "The catalog records Original Work." || response.ConversationID == "" || len(response.ToolResults) != 1 || response.Usage.InputTokens != 80 || response.Usage.CachedInputTokens != 10 || response.CreditsSpent != 94 || *calls != 2 {
		t.Fatalf("HTTP tool loop: %+v, calls=%d", response, *calls)
	}
	f.assertPlatformCharges(f.user, 2, 0)
	replay := e2eDecode[ChatResponse](t, f.request(&f.user, "POST", "/chat", req, 200))
	if !reflect.DeepEqual(response, replay) || *calls != 2 {
		t.Fatalf("sealed request reran or changed: %+v calls=%d", replay, *calls)
	}
	req.Message = "Different request with the same ID"
	f.request(&f.user, "POST", "/chat", req, 409)
	f.request(&f.other, "GET", "/conversations/"+response.ConversationID, nil, 404)
	f.request(nil, "POST", "/chat", req, 401)
	thirdParty := f.user
	thirdParty.IsThirdParty = true
	f.request(&thirdParty, "POST", "/chat", req, 403)
	f.request(&f.user, "POST", "/chat", map[string]any{"message": "edit", "client_request_id": uuid.NewString(), "actor_id": f.admin.ID}, 400)
	if *calls != 2 {
		t.Fatal("an unauthorized request invoked the provider")
	}
	f.assertPlatformCharges(f.user, 2, 0)
	conversation := e2eDecode[Conversation](t, f.request(&f.user, "GET", "/conversations/"+response.ConversationID, nil, 200))
	if conversation.OwnerID != f.user.ID || len(conversation.Messages) < 4 {
		t.Fatalf("conversation was not persisted for the authenticated owner: %+v", conversation)
	}
}

func TestPostgresAssistantHTTPQuotaDenialNeverInvokesProvider(t *testing.T) {
	f := assistantE2EFixture(t)
	// All three failures must occur before any upstream request is possible.
	calls := f.provider(f.user.ID, "integration-test-secret", "test-model", func(_ int, _ e2eProviderPayload) Message {
		t.Fatal("quota denial invoked the provider")
		return Message{}
	})
	for _, tc := range []struct {
		name     string
		platform int64
		provider int64
		credits  int64
		want     string
	}{
		{"platform", 1, 1000000, 1000000, "assistant_budget_exceeded"},
		{"provider", 1000000, 1, 1000000, "assistant_budget_exceeded"},
		{"credits", 1000000, 1000000, 0, "assistant_insufficient_credits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.s.DB.Exec(`UPDATE assistant.settings SET registration_credits=$1,monthly_budget_microusd=$2`, tc.credits, tc.platform); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.DB.Exec(`UPDATE assistant.providers SET monthly_budget_microusd=$1`, tc.provider); err != nil {
				t.Fatal(err)
			}
			user := f.user
			user.ID = uuid.NewString()
			response := e2eDecode[ChatResponse](t, f.request(&user, "POST", "/chat", ChatRequest{Message: "Read catalog", ClientRequestID: uuid.NewString()}, 200))
			if response.Error != tc.want || response.CreditsSpent != 0 || *calls != 0 {
				t.Fatalf("quota bypassed: %+v calls=%d", response, *calls)
			}
			var usage int
			if err := f.s.DB.QueryRow(`SELECT count(*) FROM assistant.usage WHERE user_id=$1`, user.ID).Scan(&usage); err != nil || usage != 0 {
				t.Fatalf("denied request left usage reservations: %d %v", usage, err)
			}
		})
	}
}

func TestPostgresAssistantHTTPCommitUsesCallerReviewAndEvidence(t *testing.T) {
	for _, bypass := range []bool{false, true} {
		t.Run(fmt.Sprintf("bypass=%t", bypass), func(t *testing.T) {
			f := assistantE2EFixture(t)
			if bypass {
				f.user.Permissions = append(f.user.Permissions, catalog.PermissionReviewBypass)
			}
			work := f.seedWork()
			commitID := uuid.NewString()
			calls := f.provider(f.user.ID, "integration-test-secret", "test-model", func(round int, payload e2eProviderPayload) Message {
				switch round {
				case 1:
					return e2eTool("catalog_checkout", map[string]any{"entity_ids": []string{work.ID}})
				case 2:
					checked := e2eLastTool[catalog.CatalogCheckout](t, payload)
					if len(checked.Entities) != 1 || checked.Entities[0].ID != work.ID || checked.DefinitionsETag == "" {
						t.Fatalf("cannot prepare edit from checkout: %+v", checked)
					}
					value, _ := json.Marshal("Corrected sourced body")
					commit := catalog.CatalogCommit{ID: commitID, DefinitionsETag: checked.DefinitionsETag, EditNote: "Correct article from publisher source",
						Sources: []catalog.Source{{Key: "publisher", Kind: "url", Citation: "Publisher product description", URL: "https://publisher.example/product"}},
						Operations: []catalog.CommitOperation{{Target: "entity", Action: "update", ID: work.ID, BaseVersion: checked.Entities[0].Version,
							Patch:      []catalog.CommitPatch{{Path: "/translations/en-US/body", Value: value}},
							SourceRefs: []catalog.SourceRef{{Source: "publisher", Pointers: []string{"/translations/en-US/body"}}}}},
					}
					return e2eTool("catalog_commit", map[string]any{"commit": commit})
				default:
					receipt := e2eLastTool[catalog.CommitReceipt](t, payload)
					if receipt.ID != commitID || receipt.Applied != bypass {
						t.Fatalf("commit tool did not return the actual review decision: %+v", receipt)
					}
					return Message{Role: "assistant", Content: "The requested edit was submitted."}
				}
			})
			req := ChatRequest{Message: "Update this article using the publisher description", ClientRequestID: uuid.NewString()}
			response := e2eDecode[ChatResponse](t, f.request(&f.user, "POST", "/chat", req, 200))
			if response.Error != "" || len(response.CommitReceipts) != 1 || response.CommitReceipts[0].Applied != bypass || *calls != 3 {
				t.Fatalf("edit was lost or elevated: %+v calls=%d", response, *calls)
			}
			current, err := f.cat.Get(context.Background(), work.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantBody, wantVersion, wantStatus := "Original body", work.Version, "pending_review"
			rewards := int64(0)
			if bypass {
				wantBody, wantVersion, wantStatus = "Corrected sourced body", work.Version+1, "reviewed"
				rewards = 10
			}
			if current.Translations["en-US"].Body != wantBody || current.Version != wantVersion || response.CommitReceipts[0].ReviewStatus != wantStatus {
				t.Fatalf("review changed the wrong facts: entity=%+v receipt=%+v", current, response.CommitReceipts[0])
			}
			var author string
			var raw []byte
			if err = f.s.DB.QueryRow(`SELECT actor_id,request FROM catalog.commits WHERE id=$1`, commitID).Scan(&author, &raw); err != nil || author != f.user.ID {
				t.Fatalf("assistant changed commit authorship: %s %v", author, err)
			}
			var saved catalog.CatalogCommit
			if json.Unmarshal(raw, &saved) != nil || len(saved.Sources) != 1 || saved.Sources[0].Key != "publisher" || len(saved.Operations) != 1 || len(saved.Operations[0].SourceRefs) != 1 || saved.Operations[0].SourceRefs[0].Source != "publisher" {
				t.Fatalf("commit evidence was not preserved: %s", raw)
			}
			f.request(&f.user, "POST", "/chat", req, 200)
			var commits int
			if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.commits WHERE actor_id=$1`, f.user.ID).Scan(&commits); err != nil || commits != 1 || *calls != 3 {
				t.Fatalf("replay duplicated a mutation: commits=%d calls=%d err=%v", commits, *calls, err)
			}
			f.assertPlatformCharges(f.user, 3, rewards)
		})
	}
}

func TestPostgresAssistantHTTPBYOCredentialAccountingAndIsolation(t *testing.T) {
	f := assistantE2EFixture(t)
	secret := "user-key-never-public-123456789"
	w := f.request(&f.user, "POST", "/credentials", map[string]any{"name": "My API", "base_url": "https://93.184.216.34/v1", "upstream_model": "my-upstream", "enabled": true, "api_key": secret, "owner_id": f.other.ID}, 200)
	credential := e2eDecode[BYOCredential](t, w)
	if credential.OwnerID != f.user.ID || !credential.HasKey || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("credential leaked or accepted an impersonated owner: %s", w.Body.String())
	}
	var ciphertext []byte
	if err := f.s.DB.QueryRow(`SELECT encrypted_key FROM assistant.byo_credentials WHERE id=$1`, credential.ID).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatal("BYO key was not encrypted at rest")
	}
	calls := f.provider(f.user.ID, secret, "my-upstream", func(_ int, _ e2eProviderPayload) Message {
		return Message{Role: "assistant", Content: "Answer using your API."}
	})
	response := e2eDecode[ChatResponse](t, f.request(&f.user, "POST", "/chat", ChatRequest{Message: "Use my own model", CredentialID: credential.ID, ClientRequestID: uuid.NewString()}, 200))
	if response.Error != "" || response.CreditsSpent != 0 || response.Usage.InputTokens != 40 || *calls != 1 {
		t.Fatalf("BYO chat was not accounted separately: %+v calls=%d", response, *calls)
	}
	balance, err := f.s.SyncCredits(context.Background(), f.user.ID)
	if err != nil || balance.Credits != 1000000 || balance.ReservedCredits != 0 {
		t.Fatalf("BYO spent platform credits: %+v %v", balance, err)
	}
	budgets, err := f.s.Budgets(context.Background())
	if err != nil || len(budgets) != 0 {
		t.Fatalf("BYO spent platform budget: %+v %v", budgets, err)
	}
	list := f.request(&f.user, "GET", "/credentials", nil, 200)
	if strings.Contains(list.Body.String(), secret) || strings.Contains(list.Body.String(), "encrypted_key") {
		t.Fatalf("credential list exposed secret material: %s", list.Body.String())
	}
	otherList := e2eDecode[Page[BYOCredential]](t, f.request(&f.other, "GET", "/credentials", nil, 200))
	if otherList.Total != 0 {
		t.Fatalf("other account can enumerate credentials: %+v", otherList)
	}
	f.request(&f.other, "DELETE", "/credentials/"+credential.ID, nil, 404)
	denied := e2eDecode[ChatResponse](t, f.request(&f.other, "POST", "/chat", ChatRequest{Message: "Use another account's key", CredentialID: credential.ID, ClientRequestID: uuid.NewString()}, 200))
	if denied.Error != "assistant_not_found" || *calls != 1 {
		t.Fatalf("other account used the credential: %+v calls=%d", denied, *calls)
	}
}

func TestPostgresAssistantHTTPGeneratedCacheIsFreeAndBYOPrivate(t *testing.T) {
	for _, byo := range []bool{false, true} {
		t.Run(fmt.Sprintf("byo=%t", byo), func(t *testing.T) {
			f := assistantE2EFixture(t)
			work := f.seedWork()
			agent, err := f.cat.Save(context.Background(), catalog.Edit{
				Entity:   catalog.Entity{Kind: "agent", Title: "Recorded Creator", State: "active", Translations: map[string]catalog.Translation{"en-US": {Title: "Recorded Creator"}}},
				EditNote: "Creator source fixture", Sources: []catalog.Source{{Kind: "url", Citation: "Publisher credits", URL: "https://publisher.example/product"}},
			}, f.admin)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.cat.SaveRelation(context.Background(), catalog.RelationEdit{
				Relation: catalog.Relation{Type: "created_by", SourceID: work.ID, TargetID: agent.ID},
				EditNote: "Publisher creator credit", Sources: []catalog.Source{{Kind: "url", Citation: "Publisher credits", URL: "https://publisher.example/product"}},
			}, f.admin); err != nil {
				t.Fatal(err)
			}
			key, upstream, credentialID := "integration-test-secret", "test-model", ""
			if byo {
				key, upstream = "private-generation-key", "personal-model"
				credential := e2eDecode[BYOCredential](t, f.request(&f.user, "POST", "/credentials", map[string]any{"name": "Private", "base_url": "https://93.184.216.34/v1", "upstream_model": upstream, "enabled": true, "api_key": key}, 200))
				credentialID = credential.ID
			}
			calls := f.provider(f.user.ID, key, upstream, func(_ int, payload e2eProviderPayload) Message {
				if len(payload.Tools) != 0 || !strings.Contains(payload.Messages[len(payload.Messages)-1].Content, work.Title) {
					t.Fatal("overview did not use structured catalog facts without mutation tools")
				}
				return Message{Role: "assistant", Content: `{"markdown":"Original Work is recorded in the catalog."}`}
			})
			path := "/generated/" + work.ID
			input := map[string]any{"locale": "en-US", "client_request_id": uuid.NewString(), "credential_id": credentialID}
			var generated struct {
				Item         *GeneratedOverview `json:"item"`
				CreditsSpent int64              `json:"credits_spent"`
				Error        string             `json:"error"`
			}
			generated = e2eDecode[struct {
				Item         *GeneratedOverview `json:"item"`
				CreditsSpent int64              `json:"credits_spent"`
				Error        string             `json:"error"`
			}](t, f.request(&f.user, "POST", path, input, 200))
			if generated.Item == nil || generated.Error != "" || len(generated.Item.Blocks) < 2 || generated.Item.Blocks[0].Type != "markdown" || *calls != 1 {
				t.Fatalf("structured cached overview missing: %+v calls=%d", generated, *calls)
			}
			visuals := map[string]bool{}
			for _, block := range generated.Item.Blocks {
				switch block.Type {
				case "table":
					for _, row := range block.Rows {
						if len(row) == 2 && row[1] == "en-US" {
							visuals["table"] = true
						}
					}
				case "timeline":
					if len(block.Events) == 1 && block.Events[0].Date == "2024-01-02" && block.Events[0].EntityID == work.ID {
						visuals["timeline"] = true
					}
				case "chart":
					if len(block.Series) == 1 && len(block.Series[0].Values) == 1 && block.Series[0].Values[0] == 1 {
						visuals["chart"] = true
					}
				}
			}
			if len(visuals) != 3 {
				t.Fatalf("visuals were not constructed from real fields and relationships: %+v", generated.Item.Blocks)
			}
			cached := e2eDecode[map[string]json.RawMessage](t, f.request(&f.user, "GET", path+"?locale=en-US", nil, 200))
			if string(cached["item"]) == "null" || *calls != 1 {
				t.Fatalf("owner GET did not reuse cache: %s calls=%d", cached["item"], *calls)
			}
			reused := e2eDecode[map[string]json.RawMessage](t, f.request(&f.user, "POST", path, input, 200))
			if string(reused["credits_spent"]) != "0" || *calls != 1 {
				t.Fatalf("reusing cache charged another generation: %v calls=%d", reused, *calls)
			}
			public := e2eDecode[map[string]json.RawMessage](t, f.request(nil, "GET", path+"?locale=en-US", nil, 200))
			other := e2eDecode[map[string]json.RawMessage](t, f.request(&f.other, "GET", path+"?locale=en-US", nil, 200))
			for _, item := range []json.RawMessage{public["item"], other["item"]} {
				if (string(item) == "null") != byo {
					t.Fatalf("cache visibility does not match credential ownership: byo=%t item=%s", byo, item)
				}
			}
			usage, err := f.s.UsageRecords(context.Background(), f.user.ID, false, 50, 0)
			if err != nil || usage.Total != 1 || *calls != 1 {
				t.Fatalf("reading cache invoked or charged the model: %+v %v", usage, err)
			}
			current, err := f.cat.Get(context.Background(), work.ID, nil)
			if err != nil || current.Version != work.Version || current.Translations["en-US"].Body != "Original body" {
				t.Fatalf("generation replaced authoritative article content: %+v %v", current, err)
			}
			if !byo {
				f.assertPlatformCharges(f.user, 1, 0)
			} else if generated.CreditsSpent != 0 || usage.Items[0].CostMicroUSD != 0 {
				t.Fatal("BYO overview charged platform credits or budget")
			}
		})
	}
}
