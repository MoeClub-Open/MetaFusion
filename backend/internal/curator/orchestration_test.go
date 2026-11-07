package curator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type managerTransport func(*http.Request) (*http.Response, error)

func (f managerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPostgresManagerDelegatesAndPersistsChild(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	s.Assistant = assistant.NewStore(s.Catalog.DB, bytes.Repeat([]byte{0x42}, 32))
	key := "test-manager-key"
	provider, err := s.Assistant.SaveProvider(ctx, assistant.ProviderInput{Provider: assistant.Provider{Name: "Test", BaseURL: "https://93.184.216.34/v1", Protocol: "openai_compatible", Enabled: true}, APIKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	model, err := s.Assistant.SaveModel(ctx, assistant.Model{ProviderID: provider.ID, Name: "Test", UpstreamModel: "test", Enabled: true, MaxOutputTokens: 1024, ContextWindow: 262144, MaxTokensParameter: "max_tokens", InputCreditsPerMillion: 1, OutputCreditsPerMillion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Catalog.DB.Exec(`UPDATE assistant.settings SET enabled=true,default_model_id=$1;`, model.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.ModelID = model.ID
	settings.MonthlyCreditAllowance = 100
	settings, err = s.SaveSettings(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	s.lease.Store(&schedulerLease{conn: leaseProbe(func(context.Context) error { return nil }), cancel: func() {}})
	if err = s.discoverReviews(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f, err := s.AddTask(ctx, TaskInput{ID: uuid.NewString(), Message: "核对一条资料"}, "administrator")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.Assistant.CompletionClient = &http.Client{Transport: managerTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		message := map[string]any{"role": "assistant", "content": "任务完成，未改目录"}
		switch calls {
		case 0:
			message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "delegate-1", "type": "function", "function": map[string]any{"name": "curator_delegate", "arguments": string(assistantJSON(map[string]any{"task_id": f.ID, "role": "research"}))}}}}
		case 1:
			message["content"] = `{"decision":"no_change","reason":"已核实，无需修改","sources":[],"confidence":"high"}`
		}
		calls++
		response := assistantJSON(map[string]any{"choices": []any{map[string]any{"message": message}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
	})}
	tasks, err := s.dueTasks(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.runManager(ctx, settings, tasks)
	if err != nil || run.Status != "completed" || calls != 3 {
		t.Fatalf("manager did not delegate: %+v calls=%d err=%v", run, calls, err)
	}
	children, err := s.Children(ctx, run.ID)
	if err != nil || len(children) != 1 || children[0].Status != "completed" || len(children[0].Messages) != 3 {
		t.Fatalf("child not persisted: %+v %v", children, err)
	}
	saved, err := s.GetRun(ctx, run.ID)
	if err != nil || len(saved.Messages) != 5 {
		t.Fatalf("manager lost conversation: %+v %v", saved, err)
	}
	replay, err := s.delegate(ctx, run, f, "research", "")
	if err != nil || replay.ID != children[0].ID || calls != 3 {
		t.Fatalf("repeated delegation ran child again: %+v %v", replay, err)
	}
	if tasks, err = s.dueTasks(ctx, settings); err != nil || len(tasks) != 0 {
		t.Fatalf("completed task remained due: %v %v", tasks, err)
	}
}

func TestEmptyQueueDoesNotCreateRunOrCallModel(t *testing.T) {
	// No stores are present: any accidental read/write/model call would panic.
	run, err := (&Service{}).runManager(context.Background(), Settings{}, nil)
	if err != nil || run.ID != "" {
		t.Fatalf("empty queue ran: %+v %v", run, err)
	}
}

func TestPostgresParentChildCheckpointRecovery(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	settings, err := s.Settings(ctx)
	if err != nil || settings.Execution.MaxChildren != 3 || !settings.Execution.AllowReviews {
		t.Fatalf("migration defaults: %+v %v", settings, err)
	}
	if err = s.discoverReviews(ctx, settings); err != nil {
		t.Fatal(err)
	}
	f, err := s.AddTask(ctx, TaskInput{ID: uuid.NewString(), Message: "核对一条资料"}, "administrator")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.dueTasks(ctx, settings)
	if err != nil || len(tasks) != 1 || tasks[0].ID != f.ID {
		t.Fatalf("manual task missing: %+v %v", tasks, err)
	}
	parent, err := s.createRun(ctx, "manager", triggerSchedule, nil, nil, "", "manager")
	if err != nil {
		t.Fatal(err)
	}
	f, err = s.claimTask(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.createRun(ctx, kindAgent, triggerSchedule, &f, nil, parent.ID, "research")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.checkpointRun(ctx, &child, assistant.CompletionRun{Messages: []assistant.Message{{Role: "assistant", Content: "已查证"}}, CreditsSpent: 3}); err != nil {
		t.Fatal(err)
	}
	children, err := s.Children(ctx, parent.ID)
	if err != nil || len(children) != 1 || children[0].ParentRunID != parent.ID || len(children[0].Messages) != 1 {
		t.Fatalf("lost child transcript: %+v %v", children, err)
	}
	if _, err = s.createRun(ctx, kindAgent, triggerSchedule, &f, nil, parent.ID, "research"); err == nil {
		t.Fatal("created duplicate child")
	}
	if err = s.recoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetRun(ctx, child.ID)
	if err != nil || saved.Status != statusFailed || saved.Credits != 3 || len(saved.Messages) != 1 {
		t.Fatalf("restart lost transcript or spending: %+v %v", saved, err)
	}
	f, err = s.GetFinding(ctx, f.ID)
	if err != nil || f.Status != statusOpen || f.LastRunID != child.ID {
		t.Fatalf("cannot recover original progress: %+v %v", f, err)
	}
	// A completed child is never requeued by lease recovery.
	if _, err = s.Catalog.DB.Exec(`UPDATE curator.findings SET status='done' WHERE id=$1`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.recoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.dueTasks(ctx, settings)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("completed task duplicated: %+v %v", tasks, err)
	}
}

func TestPostgresReviewDiscoveryFreezesVersionAndExcludesSelf(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, author := range []string{"human-editor", settings.SystemUserID} {
		workspace, commit, pr := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if _, err = s.Catalog.DB.Exec(`INSERT INTO catalog.workspaces(id,owner_id) VALUES($1,$2)`, workspace, author); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Catalog.DB.Exec(`INSERT INTO catalog.commits(id,workspace_id,actor_id,request_hash,request,result) VALUES($1,$2,$3,'test','{}','{}')`, commit, workspace, author); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Catalog.DB.Exec(`INSERT INTO catalog.workspace_commits(workspace_id,commit_id,position) VALUES($1,$2,1)`, workspace, commit); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Catalog.DB.Exec(`UPDATE catalog.workspaces SET head_commit_id=$2 WHERE id=$1`, workspace, commit); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Catalog.DB.Exec(`INSERT INTO catalog.pull_requests(id,workspace_id,author_id,title,source_head_id) VALUES($1,$2,$3,'Review test',$4)`, pr, workspace, author, commit); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.discoverReviews(ctx, settings); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.dueTasks(ctx, settings)
	if err != nil || len(tasks) != 1 || tasks[0].ReviewPRID == "" {
		t.Fatalf("self review or missing PR: %+v %v", tasks, err)
	}
	original := tasks[0]
	if err = s.discoverReviews(ctx, settings); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.dueTasks(ctx, settings)
	if err != nil || len(tasks) != 1 {
		t.Fatal("same frozen PR queued twice", err)
	}
	if _, err = s.Catalog.DB.Exec(`UPDATE catalog.pull_requests SET version=version+1 WHERE id=$1`, original.ReviewPRID); err != nil {
		t.Fatal(err)
	}
	if err = s.discoverReviews(ctx, settings); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.dueTasks(ctx, settings)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("new version was not queued: %+v %v", tasks, err)
	}
	if _, err = s.Catalog.DB.Exec(`UPDATE curator.settings SET enabled=true WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReopenFinding(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	s.lease.Store(&schedulerLease{conn: leaseProbe(func(context.Context) error { return nil }), cancel: func() {}})
	original, err = s.claimTask(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Assistant is intentionally nil. A stale frozen version must finish before
	// funding, model calls or an attempted merge.
	run, err := s.runReview(ctx, original, "", "")
	if err != nil || run.Decision != "skipped" || run.Merged {
		t.Fatalf("stale review was used: %+v %v", run, err)
	}
}

func TestPostgresCuratorBYOModelAndSearchProvider(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	s.Assistant = assistant.NewStore(s.Catalog.DB, bytes.Repeat([]byte{0x42}, 32))
	if _, err := s.Catalog.DB.Exec(`UPDATE assistant.settings SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	admin := catalog.User{ID: "admin-byo", Username: "admin", Permissions: []string{"*"}}
	other := catalog.User{ID: "other-admin", Username: "other", Permissions: []string{"*"}}
	secret := "curator-byo-secret-key"
	byoProvider, err := s.Assistant.SaveBYOProvider(ctx, admin.ID, assistant.BYOProviderInput{
		BYOProvider: assistant.BYOProvider{Name: "Admin DeepSeek", BaseURL: "https://93.184.216.34/v1", Enabled: true},
		APIKey:      &secret,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	byoModel, err := s.Assistant.SaveBYOModel(ctx, admin.ID, assistant.BYOModel{
		ProviderID: byoProvider.ID, Name: "Admin Chat", UpstreamModel: "deepseek-chat", Enabled: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.MonthlyCreditAllowance = 0
	settings.ModelID = byoModel.ID
	settings.Execution.SearchProviderID = "3f0c0a17-0000-4000-8000-000000000011"
	if _, err = s.Catalog.DB.Exec(`UPDATE assistant.search_providers SET enabled=true,credits_per_request=5 WHERE id=$1`, settings.Execution.SearchProviderID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSettingsForUser(ctx, settings, other); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("other admin bound someone else's BYO model: %v", err)
	}
	// Platform search requires platform credits unless search BYO provider is also set.
	if _, err = s.SaveSettingsForUser(ctx, settings, admin); !errors.Is(err, ErrAllowanceRequired) {
		t.Fatalf("paid platform search bypassed allowance check: %v", err)
	}
	settings.Execution.SearchBYOProviderID = byoProvider.ID
	saved, err := s.SaveSettingsForUser(ctx, settings, admin)
	if err != nil || saved.Execution.BillingOwnerID != admin.ID {
		t.Fatalf("BYO curator settings failed: %+v %v", saved, err)
	}
	summary, err := s.Summary(ctx)
	if err != nil || summary.Model == nil || summary.Model.ID != byoModel.ID {
		t.Fatalf("summary did not resolve BYO model: %+v %v", summary, err)
	}
}
