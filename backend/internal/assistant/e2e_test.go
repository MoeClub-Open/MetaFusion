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
		{`UPDATE assistant.settings SET registration_credits=1000000,monthly_budget_microusd=1000000,max_tool_rounds=8`, nil},
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
	ctx := context.Background()
	workspace, err := f.cat.CreateWorkspace(ctx, catalog.WorkspaceCreate{Name: "Fixture work"}, f.admin)
	if err != nil {
		f.t.Fatal(err)
	}
	document, err := json.Marshal(catalog.Entity{Kind: "work", Title: "Original Work",
		Attributes:   map[string]any{"language": "en-US", "edition_date": "2024-01-02"},
		Translations: map[string]catalog.Translation{"en-US": {Title: "Original Work", Body: "Original body"}}})
	if err != nil {
		f.t.Fatal(err)
	}
	staged, err := f.cat.SaveWorkspaceChanges(ctx, workspace.ID, catalog.WorkspaceChanges{
		ExpectedVersion: workspace.Version,
		Operations:      []catalog.WorkspaceOperation{{Target: "entity", Action: "create", Ref: "work", Document: document}},
	}, f.admin)
	if err != nil {
		f.t.Fatal(err)
	}
	commit, err := f.cat.CommitWorkspace(ctx, workspace.ID, catalog.WorkspaceCommitCreate{
		ID: uuid.NewString(), ExpectedVersion: staged.Workspace.Version, Message: "Create fixture work",
	}, f.admin)
	if err != nil {
		f.t.Fatal(err)
	}
	workspace, err = f.cat.GetWorkspace(ctx, workspace.ID, f.admin)
	if err != nil {
		f.t.Fatal(err)
	}
	if receipt, err := f.cat.PushWorkspace(ctx, workspace.ID, catalog.WorkspacePush{ExpectedVersion: workspace.Version, ExpectedHead: commit.ID}, f.admin); err != nil || !receipt.Applied {
		f.t.Fatalf("seed merge: %+v %v", receipt, err)
	}
	entity, err := f.cat.Get(ctx, staged.Refs["work"], nil)
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

func TestPostgresAssistantHTTPWorkspaceUsesCallerPermissionsAndPreservesAuthorship(t *testing.T) {
	for _, bypass := range []bool{false, true} {
		t.Run(fmt.Sprintf("bypass=%t", bypass), func(t *testing.T) {
			f := assistantE2EFixture(t)
			if bypass {
				f.user.Permissions = append(f.user.Permissions, catalog.PermissionReviewBypass)
			}
			work := f.seedWork()
			commitID := uuid.NewString()
			var workspace catalog.Workspace
			assertUnmerged := func() {
				t.Helper()
				current, err := f.cat.Get(context.Background(), work.ID, nil)
				if err != nil || current.Version != work.Version || current.Translations["en-US"].Body != "Original body" {
					t.Fatalf("staging or committing changed main: %+v %v", current, err)
				}
				if _, err = f.s.SyncCredits(context.Background(), f.user.ID); err != nil {
					t.Fatal(err)
				}
				var rewards int
				if err = f.s.DB.QueryRow(`SELECT count(*) FROM assistant.credit_entries WHERE user_id=$1 AND kind='accepted_edit'`, f.user.ID).Scan(&rewards); err != nil || rewards != 0 {
					t.Fatalf("unmerged changes earned credits: %d %v", rewards, err)
				}
			}
			calls := f.provider(f.user.ID, "integration-test-secret", "test-model", func(round int, payload e2eProviderPayload) Message {
				switch round {
				case 1:
					return e2eTool("catalog_workspace_create", catalog.WorkspaceCreate{Name: "Correct article", EntityIDs: []string{work.ID}})
				case 2:
					workspace = e2eLastTool[catalog.Workspace](t, payload)
					if workspace.OwnerID != f.user.ID || len(workspace.Objects) != 1 || workspace.Objects[0].ID != work.ID || workspace.Objects[0].BaseVersion != work.Version {
						t.Fatalf("cannot prepare edit from actual working copy: %+v", workspace)
					}
					value, _ := json.Marshal("Corrected body")
					return e2eTool("catalog_workspace_stage", map[string]any{
						"workspace_id": workspace.ID, "expected_version": workspace.Version,
						"operations": []catalog.WorkspaceOperation{{Target: "entity", Action: "update", ID: work.ID,
							Patch: []catalog.CommitPatch{{Path: "/translations/en-US/body", Value: value}}}},
					})
				case 3:
					staged := e2eLastTool[catalog.WorkspaceChangesResult](t, payload)
					if staged.Workspace.ID != workspace.ID || staged.Workspace.DirtyCount != 1 {
						t.Fatalf("stage did not persist an isolated change: %+v", staged)
					}
					assertUnmerged()
					return e2eTool("catalog_workspace_commit", map[string]any{
						"workspace_id": workspace.ID, "id": commitID, "expected_version": staged.Workspace.Version, "message": "Correct article body",
					})
				case 4:
					commit := e2eLastTool[catalog.CollaborationCommit](t, payload)
					if commit.ID != commitID || commit.Author.ID != f.user.ID || commit.MergedAt != nil || len(commit.Changes) != 1 {
						t.Fatalf("commit lost authorship or changed main: %+v", commit)
					}
					assertUnmerged()
					return e2eTool("catalog_workspace_get", map[string]any{"workspace_id": workspace.ID})
				case 5:
					workspace = e2eLastTool[catalog.Workspace](t, payload)
					if workspace.DirtyCount != 0 || workspace.HeadCommitID == nil || *workspace.HeadCommitID != commitID {
						t.Fatalf("committed head was not retained: %+v", workspace)
					}
					return e2eTool("catalog_workspace_push", map[string]any{
						"workspace_id": workspace.ID, "expected_version": workspace.Version, "expected_head": *workspace.HeadCommitID, "title": "Correct article body",
					})
				default:
					receipt := e2eLastTool[catalog.WorkspacePushResult](t, payload)
					if receipt.Workspace.ID != workspace.ID || receipt.Applied != bypass {
						t.Fatalf("push tool did not return the actual merge decision: %+v", receipt)
					}
					if bypass {
						return Message{Role: "assistant", Content: "The requested edit was merged."}
					}
					if receipt.PullRequest == nil || receipt.PullRequest.Status != "open" || receipt.PullRequest.SourceHeadID != commitID || receipt.PullRequest.AuthorID != f.user.ID {
						t.Fatalf("ordinary push did not open the author's PR: %+v", receipt)
					}
					return Message{Role: "assistant", Content: "The requested edit is in an open pull request."}
				}
			})
			req := ChatRequest{Message: "Correct this article body", ClientRequestID: uuid.NewString()}
			response := e2eDecode[ChatResponse](t, f.request(&f.user, "POST", "/chat", req, 200))
			if response.Error != "" || len(response.PushReceipts) != 1 || response.PushReceipts[0].Applied != bypass || *calls != 6 {
				t.Fatalf("edit was lost or elevated: %+v calls=%d", response, *calls)
			}
			current, err := f.cat.Get(context.Background(), work.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantBody, wantVersion := "Original body", work.Version
			rewards := int64(0)
			if bypass {
				wantBody, wantVersion = "Corrected body", work.Version+1
				rewards = 10
			}
			if current.Translations["en-US"].Body != wantBody || current.Version != wantVersion {
				t.Fatalf("push changed the wrong facts: entity=%+v receipt=%+v", current, response.PushReceipts[0])
			}
			var author, mergedBy string
			var merged bool
			var raw []byte
			if err = f.s.DB.QueryRow(`SELECT actor_id,request,merged_at IS NOT NULL,coalesce(merged_by,'') FROM catalog.commits WHERE id=$1`, commitID).Scan(&author, &raw, &merged, &mergedBy); err != nil || author != f.user.ID || merged != bypass || bypass && mergedBy != f.user.ID {
				t.Fatalf("assistant changed commit authorship or merge attribution: author=%s merged=%t by=%s %v", author, merged, mergedBy, err)
			}
			var saved catalog.WorkspaceCommitCreate
			if json.Unmarshal(raw, &saved) != nil || saved.ID != commitID || saved.Message != "Correct article body" {
				t.Fatalf("immutable commit request was not preserved: %s", raw)
			}
			f.request(&f.user, "POST", "/chat", req, 200)
			var commits int
			if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.commits WHERE actor_id=$1 AND snapshot_scope<>'catalog'`, f.user.ID).Scan(&commits); err != nil || commits != 1 || *calls != 6 {
				t.Fatalf("replay duplicated a mutation: commits=%d calls=%d err=%v", commits, *calls, err)
			}
			f.assertPlatformCharges(f.user, 6, rewards)
			if !bypass {
				pr := response.PushReceipts[0].PullRequest
				mergedReceipt, err := f.cat.MergePullRequest(context.Background(), pr.ID, catalog.PullRequestMerge{
					ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID,
				}, f.admin)
				if err != nil || !mergedReceipt.Applied {
					t.Fatalf("reviewer could not merge the assistant-authored PR: %+v %v", mergedReceipt, err)
				}
				if err = f.s.DB.QueryRow(`SELECT actor_id,coalesce(merged_by,'') FROM catalog.commits WHERE id=$1`, commitID).Scan(&author, &mergedBy); err != nil || author != f.user.ID || mergedBy != f.admin.ID {
					t.Fatalf("PR merge overwrote the author's contribution: author=%s merged_by=%s %v", author, mergedBy, err)
				}
				current, err = f.cat.Get(context.Background(), work.ID, nil)
				if err != nil || current.Version != work.Version+1 || current.Translations["en-US"].Body != "Corrected body" {
					t.Fatalf("PR merge did not materialize the authored edit: %+v %v", current, err)
				}
				// Approval credits the original author; it is unrelated to which
				// account performed the merge or whether the author chats again.
				f.assertPlatformCharges(f.user, 6, 10)
			}
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
			ctx := context.Background()
			workspace, err := f.cat.CreateWorkspace(ctx, catalog.WorkspaceCreate{Name: "Fixture creator credit", EntityIDs: []string{work.ID}}, f.admin)
			if err != nil {
				t.Fatal(err)
			}
			creatorDocument, err := json.Marshal(catalog.Entity{Kind: "agent", Title: "Recorded Creator", Translations: map[string]catalog.Translation{"en-US": {Title: "Recorded Creator"}}})
			if err != nil {
				t.Fatal(err)
			}
			creditDocument, err := json.Marshal(map[string]any{"type": "created_by", "source_id": work.ID, "target_id": map[string]string{"$ref": "creator"}})
			if err != nil {
				t.Fatal(err)
			}
			staged, err := f.cat.SaveWorkspaceChanges(ctx, workspace.ID, catalog.WorkspaceChanges{
				ExpectedVersion: workspace.Version,
				Operations: []catalog.WorkspaceOperation{
					{Target: "entity", Action: "create", Ref: "creator", Document: creatorDocument},
					{Target: "relation", Action: "create", Ref: "credit", Document: creditDocument},
				},
			}, f.admin)
			if err != nil {
				t.Fatal(err)
			}
			commit, err := f.cat.CommitWorkspace(ctx, workspace.ID, catalog.WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: staged.Workspace.Version, Message: "Create fixture creator credit"}, f.admin)
			if err != nil {
				t.Fatal(err)
			}
			workspace, err = f.cat.GetWorkspace(ctx, workspace.ID, f.admin)
			if err != nil {
				t.Fatal(err)
			}
			if receipt, err := f.cat.PushWorkspace(ctx, workspace.ID, catalog.WorkspacePush{ExpectedVersion: workspace.Version, ExpectedHead: commit.ID}, f.admin); err != nil || !receipt.Applied {
				t.Fatalf("creator credit fixture merge: %+v %v", receipt, err)
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
