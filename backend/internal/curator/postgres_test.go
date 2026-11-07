package curator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func curatorFixture(t *testing.T) *Service {
	t.Helper()
	db := testutil.Database(t)
	if err := migrator.New(db, migrations.FS).Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return New(&catalog.Store{DB: db}, nil)
}

func insertEntity(t *testing.T, s *Service, document string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := s.Catalog.DB.Exec(`INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document)
		SELECT $1::uuid,'work',1,'test','active',system_user_id::uuid,jsonb_build_object('title','test') || $2::jsonb
		FROM curator.settings WHERE singleton`, id, document); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgresSettingsDoNotUndoPause(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	settings, err := s.SetPaused(ctx, true, "manual")
	if err != nil {
		t.Fatal(err)
	}
	// A settings form opened before pausing carries an obsolete pause value.
	settings.Paused, settings.PausedReason = false, ""
	saved, err := s.SaveSettings(ctx, settings)
	if err != nil || !saved.Paused || saved.PausedReason != "manual" {
		t.Fatalf("saving the form silently resumed execution: %+v %v", saved, err)
	}
	if _, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET consecutive_failures=5 WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	saved, err = s.SetPaused(ctx, false, "")
	if err != nil || saved.Paused || saved.ConsecutiveFailures != 0 {
		t.Fatalf("resuming retained the old failure streak: %+v %v", saved, err)
	}
}

func TestPostgresScanHandlesNullMissingAndMalformedPictures(t *testing.T) {
	s := curatorFixture(t)
	for _, document := range []string{
		`{"pictures":null,"external_links":[{"url":"https://example.com"}]}`,
		`{"external_links":[{"url":"https://example.com"}]}`,
		`{"pictures":[],"external_links":[{"url":"https://example.com"}]}`,
		`{"pictures":false,"external_links":[{"url":"https://example.com"}]}`,
		`{"pictures":null,"external_links":null}`,
	} {
		insertEntity(t, s, document)
	}
	rows, err := s.detectMissingPicture(t.Context())
	if err != nil || len(rows) != 4 {
		t.Fatalf("scan stalled on legal empty fields: %d %v", len(rows), err)
	}
	if _, err = s.detectDuplicatePictureValues(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresDisabledQueueAndRestartRecovery(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	id := insertEntity(t, s, `{"pictures":null}`)
	_, err := s.UpsertFindings(ctx, []candidate{
		{EntityID: id, Kind: "work", Code: CodeTranslationZHKana, Severity: severityWarning, Priority: 1, Signature: "name"},
		{EntityID: id, Kind: "work", Code: CodeWorkMissingForm, Severity: severityWarning, Priority: 2, Signature: "form"},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{AllowStructures: true}
	finding, err := s.ClaimFinding(ctx, settings)
	if err != nil || finding.Code != CodeWorkMissingForm {
		t.Fatalf("disabled name task was claimed: %s %v", finding.Code, err)
	}
	p := agentProgress{}
	call, err := p.prepare(toolCall("catalog_workspace_create", map[string]any{"name": "repair"}), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := json.Marshal(p)
	run, err := s.CreateRun(ctx, kindAgent, triggerSchedule, &finding, initial)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.saveProgress(cancelled, finding, &run, &p); err != nil {
		t.Fatalf("shutdown lost checkpoint: %v", err)
	}
	finding, err = s.GetFinding(ctx, finding.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Catalog, nil)
	restored, err := restarted.loadProgress(ctx, finding)
	if err != nil || restored.Pending == nil || restored.WorkspaceID != p.WorkspaceID {
		t.Fatalf("restart lost original mutation: %+v %v", restored, err)
	}
	if !jsonEqual(restored.Pending.Arguments, json.RawMessage(call.Function.Arguments)) {
		t.Fatal("restart changed original arguments")
	}
	initial, _ = json.Marshal(restored)
	if _, err = restarted.CreateRun(ctx, kindAgent, triggerSchedule, &finding, initial); err != nil {
		t.Fatal(err)
	}
	finding, _ = restarted.GetFinding(ctx, finding.ID)
	if restored, err = restarted.loadProgress(ctx, finding); err != nil || restored.Pending == nil {
		t.Fatalf("claiming the next run lost its inherited checkpoint: %v", err)
	}
}

func TestPostgresRecurringFindingStartsNewAttempt(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	id := insertEntity(t, s, `{"pictures":null}`)
	item := candidate{EntityID: id, Kind: "work", Code: CodeWorkMissingForm, Severity: severityWarning, Priority: 2, Signature: "form"}
	if _, err := s.UpsertFindings(ctx, []candidate{item}); err != nil {
		t.Fatal(err)
	}
	for _, manual := range []bool{false, true} {
		if _, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET status='resolved',last_run_id=$1,
			workspace_id=$2,pull_request_id=$3,merged=true,auto_merged=true,attempts=5,decision='changed',resolved_at=now()`,
			uuid.NewString(), uuid.NewString(), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		var findingID string
		if err := s.Catalog.DB.QueryRowContext(ctx, `SELECT id::text FROM curator.findings`).Scan(&findingID); err != nil {
			t.Fatal(err)
		}
		if manual {
			if _, err := s.ReopenFinding(ctx, findingID); err != nil {
				t.Fatal(err)
			}
		} else if _, err := s.UpsertFindings(ctx, []candidate{item}); err != nil {
			t.Fatal(err)
		}
		f, err := s.GetFinding(ctx, findingID)
		if err != nil {
			t.Fatal(err)
		}
		if f.Status != statusOpen || f.LastRunID != "" || f.WorkspaceID != "" || f.PullRequestID != "" || f.Merged || f.AutoMerged || f.Attempts != 0 || f.ResolvedAt != nil || f.Decision != "" {
			t.Fatalf("new attempt inherited old completion (manual=%v): %+v", manual, f)
		}
	}
}

func TestPostgresFinishRejectsSupersededRun(t *testing.T) {
	s := curatorFixture(t)
	ctx := t.Context()
	id := insertEntity(t, s, `{"pictures":null}`)
	if _, err := s.UpsertFindings(ctx, []candidate{{EntityID: id, Kind: "work", Code: CodeWorkMissingForm, Severity: severityWarning, Priority: 2, Signature: "form"}}); err != nil {
		t.Fatal(err)
	}
	f, err := s.ClaimFinding(ctx, Settings{AllowStructures: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, kindAgent, triggerSchedule, &f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET last_run_id=$2 WHERE id=$1`, f.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Catalog.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.finishFinding(ctx, tx, f, run, statusResolved, run.StartedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("superseded run completed the finding: %v", err)
	}
}
