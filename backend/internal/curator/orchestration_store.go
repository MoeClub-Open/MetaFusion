package curator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

const CodeReview = "review_pull_request"
const CodeManualTask = "manual_task"

type ExecutionConfig struct {
	ChildModelID         string `json:"child_model_id"`
	ReasoningEffort      string `json:"reasoning_effort"`
	ChildReasoningEffort string `json:"child_reasoning_effort"`
	BillingOwnerID       string `json:"billing_owner_id"`
	SearchProviderID     string `json:"search_provider_id"`
	SearchBYOProviderID  string `json:"search_byo_provider_id"`
	MaxChildren          int    `json:"max_children"`
	AllowReviews         bool   `json:"allow_reviews"`
	AutoMergeReviews     bool   `json:"auto_merge_reviews"`
}

func (v *ExecutionConfig) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("curator_invalid_execution_config")
	}
	return json.Unmarshal(b, v)
}

func (s *Service) SaveSettingsForUser(ctx context.Context, in Settings, user catalog.User) (Settings, error) {
	previous, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	c := &in.Execution
	c.ReasoningEffort = strings.TrimSpace(c.ReasoningEffort)
	c.ChildReasoningEffort = strings.TrimSpace(c.ChildReasoningEffort)
	mainBYO, err := s.isBYOModelID(ctx, in.ModelID)
	if err != nil {
		return Settings{}, err
	}
	childBYO, err := s.isBYOModelID(ctx, c.ChildModelID)
	if err != nil {
		return Settings{}, err
	}
	usesBYO := mainBYO || childBYO || c.SearchBYOProviderID != ""
	unchangedBYO := in.ModelID == previous.ModelID &&
		c.ChildModelID == previous.Execution.ChildModelID &&
		c.SearchBYOProviderID == previous.Execution.SearchBYOProviderID
	if !usesBYO {
		c.BillingOwnerID = ""
	} else if unchangedBYO && previous.Execution.BillingOwnerID != "" {
		c.BillingOwnerID = previous.Execution.BillingOwnerID
	} else {
		c.BillingOwnerID = user.ID
	}
	if usesBYO && (in.Enabled || !unchangedBYO) {
		if mainBYO {
			if _, _, err = s.Assistant.ResolveBYOModel(ctx, in.ModelID, c.BillingOwnerID, in.Enabled); err != nil {
				return Settings{}, ErrInvalidSettings
			}
		}
		if childBYO {
			if _, _, err = s.Assistant.ResolveBYOModel(ctx, c.ChildModelID, c.BillingOwnerID, in.Enabled); err != nil {
				return Settings{}, ErrInvalidSettings
			}
		}
		if c.SearchBYOProviderID != "" {
			if err = s.Assistant.ValidateOwnedBYOProvider(ctx, c.BillingOwnerID, c.SearchBYOProviderID); err != nil {
				return Settings{}, ErrInvalidSettings
			}
		}
	}
	return s.SaveSettings(ctx, in)
}

func (s *Service) validateExecution(ctx context.Context, c ExecutionConfig, enabled bool) error {
	if c.MaxChildren < 1 || c.MaxChildren > 8 {
		return ErrInvalidSettings
	}
	if !assistant.ValidReasoningEffort(strings.TrimSpace(c.ReasoningEffort)) || !assistant.ValidReasoningEffort(strings.TrimSpace(c.ChildReasoningEffort)) {
		return ErrInvalidSettings
	}
	for _, id := range []string{c.ChildModelID, c.SearchProviderID, c.SearchBYOProviderID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return ErrInvalidSettings
			}
		}
	}
	if enabled && c.ChildModelID != "" {
		if _, _, err := s.resolveModel(ctx, c.ChildModelID, c.BillingOwnerID, true); err != nil {
			return err
		}
	}
	if enabled && c.SearchProviderID != "" {
		var providerEnabled bool
		if err := s.Catalog.DB.QueryRowContext(ctx, `SELECT enabled FROM assistant.search_providers WHERE id=$1`, c.SearchProviderID).Scan(&providerEnabled); err != nil || !providerEnabled {
			return ErrInvalidSettings
		}
	}
	if enabled && c.SearchBYOProviderID != "" {
		if c.BillingOwnerID == "" || s.Assistant.ValidateOwnedBYOProvider(ctx, c.BillingOwnerID, c.SearchBYOProviderID) != nil {
			return ErrInvalidSettings
		}
	}
	return nil
}

func (s *Service) resolveModel(ctx context.Context, modelID, billingOwner string, enabledOnly bool) (assistant.Model, bool, error) {
	if modelID == "" {
		return assistant.Model{}, false, ErrModelRequired
	}
	models, err := s.Assistant.Models(ctx, enabledOnly)
	if err != nil {
		return assistant.Model{}, false, err
	}
	for _, m := range models {
		if m.ID == modelID {
			return m, false, nil
		}
	}
	if billingOwner != "" {
		m, _, err := s.Assistant.ResolveBYOModel(ctx, modelID, billingOwner, enabledOnly)
		if err == nil {
			return m, true, nil
		}
		if !errors.Is(err, assistant.ErrNotFound) && !errors.Is(err, assistant.ErrDisabled) {
			return assistant.Model{}, false, err
		}
	}
	return assistant.Model{}, false, ErrModelRequired
}

func (s *Service) isBYOModelID(ctx context.Context, modelID string) (bool, error) {
	if modelID == "" {
		return false, nil
	}
	if _, err := uuid.Parse(modelID); err != nil {
		return false, ErrInvalidSettings
	}
	models, err := s.Assistant.Models(ctx, false)
	if err != nil {
		return false, err
	}
	for _, m := range models {
		if m.ID == modelID {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) modelBillingUser(ctx context.Context, settings Settings, modelID string) string {
	if settings.Execution.BillingOwnerID != "" {
		if _, isBYO, err := s.resolveModel(ctx, modelID, settings.Execution.BillingOwnerID, true); err == nil && isBYO {
			return settings.Execution.BillingOwnerID
		}
	}
	return settings.SystemUserID
}

func searchBillingUser(settings Settings) string {
	if settings.Execution.SearchBYOProviderID != "" && settings.Execution.BillingOwnerID != "" {
		return settings.Execution.BillingOwnerID
	}
	return settings.SystemUserID
}

func (s *Service) checkpointRun(ctx context.Context, run *Run, result assistant.CompletionRun) error {
	run.Messages = result.Messages
	run.InputTokens = result.Usage.InputTokens
	run.OutputTokens = result.Usage.OutputTokens
	run.Credits = result.CreditsSpent
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.Catalog.DB.ExecContext(cleanup, `UPDATE curator.runs SET messages=$2,input_tokens=$3,output_tokens=$4,credits=$5,model_id=$6 WHERE id=$1 AND status='running'`, run.ID, assistantJSON(result.Messages), run.InputTokens, run.OutputTokens, run.Credits, nullableID(run.ModelID))
	return err
}

func assistantJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// Called only after acquiring the exclusive scheduler lease. The former owner
// is gone; completed runs and their receipts remain immutable and are not queued.
func (s *Service) recoverInterrupted(ctx context.Context) error {
	_, err := s.Catalog.DB.ExecContext(ctx, `WITH interrupted AS (
 UPDATE curator.findings SET status='open',next_attempt_at=now(),last_error='run_interrupted',updated_at=now() WHERE status='running' RETURNING id
 ) UPDATE curator.runs SET status='failed',error='run_interrupted',finished_at=now() WHERE status='running'`)
	return err
}

func (s *Service) dueTasks(ctx context.Context, settings Settings) ([]Finding, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT `+findingColumns+` FROM curator.findings f LEFT JOIN catalog.entities e ON e.id=f.entity_id
 WHERE f.status IN ('open','failed') AND f.next_attempt_at<=now() AND f.code=ANY($1::text[]) AND (f.entity_id IS NULL OR e.state='active')
 ORDER BY f.priority,f.next_attempt_at,f.created_at,f.id LIMIT 24`, pq.Array(s.enabledCodes(settings)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		v, e := scanFinding(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) claimTask(ctx context.Context, id string) (Finding, error) {
	res, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET status='running',attempts=attempts+1,updated_at=now() WHERE id=$1 AND status IN ('open','failed') AND next_attempt_at<=now()`, id)
	if err != nil {
		return Finding{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Finding{}, ErrConflict
	}
	return s.GetFinding(ctx, id)
}

func taskRole(f Finding) string {
	if f.Code == CodeReview {
		return "review"
	}
	if autoMergeClass(f.Code) != "" || f.Code == CodeTrackPositionGap {
		return "repair"
	}
	return "research"
}

func (s *Service) discoverReviews(ctx context.Context, settings Settings) error {
	if !settings.Execution.AllowReviews {
		return nil
	}
	// A superseded or closed frozen version is no longer work for a model.
	if _, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings f SET status='resolved',decision='skipped',reason='pull_request_no_longer_matches_task',merged=p.status='merged',resolved_at=now(),updated_at=now()
	FROM catalog.pull_requests p WHERE f.review_pr_id=p.id AND f.status IN ('open','failed') AND (p.status<>'open' OR f.signature<>p.source_head_id::text||':'||p.version::text)`); err != nil {
		return err
	}
	// Only enqueue new frozen heads. Never ask the agent to approve its own PR.
	_, err := s.Catalog.DB.ExecContext(ctx, `INSERT INTO curator.findings(id,entity_kind,code,severity,priority,signature,details,review_pr_id)
 SELECT gen_random_uuid(),'pull_request',$1,'info',1,p.source_head_id::text||':'||p.version::text,jsonb_build_object('title',p.title,'expected_head',p.source_head_id::text,'expected_version',p.version),p.id
 FROM catalog.pull_requests p WHERE p.status='open' AND p.author_id NOT IN ($2,$3)
 AND NOT EXISTS(SELECT 1 FROM curator.findings f WHERE f.review_pr_id=p.id AND f.signature=p.source_head_id::text||':'||p.version::text)
 ORDER BY p.updated_at,p.id LIMIT 50 ON CONFLICT DO NOTHING`, CodeReview, settings.SystemUserID, settings.ReviewerUserID)
	return err
}

type TaskInput struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	EntityID string `json:"entity_id,omitempty"`
}

func (s *Service) AddTask(ctx context.Context, in TaskInput, actor string) (Finding, error) {
	in.Message = strings.TrimSpace(in.Message)
	if _, err := uuid.Parse(in.ID); err != nil || in.Message == "" || len(in.Message) > 16000 {
		return Finding{}, ErrInvalidSettings
	}
	kind := "task"
	if in.EntityID != "" {
		e, err := s.Catalog.Get(ctx, in.EntityID, nil)
		if err != nil {
			return Finding{}, err
		}
		kind = e.Kind
	}
	detail := assistantJSON(map[string]any{"instruction": in.Message, "requester_id": actor})
	_, err := s.Catalog.DB.ExecContext(ctx, `INSERT INTO curator.findings(id,entity_id,entity_kind,code,severity,priority,signature,details) VALUES($1,$2,$3,$4,'info',1,$6,$5) ON CONFLICT(id) DO NOTHING`, in.ID, nullableID(in.EntityID), kind, CodeManualTask, detail, in.ID)
	if err != nil {
		return Finding{}, err
	}
	f, err := s.GetFinding(ctx, in.ID)
	if err != nil {
		return f, err
	}
	var saved map[string]any
	_ = json.Unmarshal(f.Details, &saved)
	if saved["requester_id"] != actor || saved["instruction"] != in.Message || f.EntityID != in.EntityID {
		return Finding{}, ErrConflict
	}
	return f, nil
}

func (s *Service) Children(ctx context.Context, parent string) ([]Run, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT `+runColumns+` FROM curator.runs
		WHERE parent_run_id=$1 ORDER BY started_at DESC,id DESC LIMIT 200`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var mid string
		var messages []byte
		if err := rows.Scan(&r.ID, &r.FindingID, &r.EntityID, &r.Kind, &r.Trigger, &r.Status, &mid, &r.Decision, &r.Reason,
			&r.Sources, &r.ToolTrace, &r.Detail, &r.WorkspaceID, &r.PullRequestID, &r.Merged, &r.AutoMerged,
			&r.Error, &r.InputTokens, &r.OutputTokens, &r.Credits, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.ParentRunID, &r.Role, &messages); err != nil {
			return nil, err
		}
		r.ModelID = mid
		_ = json.Unmarshal(messages, &r.Messages)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) finishManager(ctx context.Context, run Run) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err := s.Catalog.DB.BeginTx(cleanup, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.finishRun(cleanup, tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) delegate(ctx context.Context, parent Run, task Finding, role, instruction string) (Run, error) {
	if role != taskRole(task) {
		return Run{}, errors.New("curator_wrong_specialist")
	}
	var id string
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT id FROM curator.runs WHERE parent_run_id=$1 AND finding_id=$2`, parent.ID, task.ID).Scan(&id)
	if err == nil {
		return s.GetRun(ctx, id)
	}
	if err != sql.ErrNoRows {
		return Run{}, err
	}
	settings, err := s.executionSettings(ctx, task.Code)
	if err != nil {
		return Run{}, err
	}
	var children int
	if err = s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM curator.runs WHERE parent_run_id=$1`, parent.ID).Scan(&children); err != nil {
		return Run{}, err
	}
	if children >= settings.Execution.MaxChildren {
		return Run{}, errors.New("curator_child_limit")
	}
	n, err := s.countRunsLastHour(ctx)
	if err != nil {
		return Run{}, err
	}
	if n >= int64(settings.MaxRunsPerHour) {
		return Run{}, errors.New("curator_hourly_limit")
	}
	finding, err := s.claimTask(ctx, task.ID)
	if err != nil {
		return Run{}, err
	}
	if role == "review" {
		return s.runReview(ctx, finding, parent.ID, instruction)
	}
	return s.runFinding(ctx, finding, triggerSchedule, parent.ID, role, instruction)
}

func taskDescription(f Finding) map[string]any {
	return map[string]any{"id": f.ID, "role": taskRole(f), "code": f.Code, "entity_id": f.EntityID, "title": f.EntityTitle, "details": json.RawMessage(f.Details), "review_pr_id": f.ReviewPRID}
}

func childReceipt(r Run) map[string]any {
	return map[string]any{"run_id": r.ID, "role": r.Role, "status": r.Status, "decision": r.Decision, "reason": r.Reason, "error": r.Error, "pull_request_id": r.PullRequestID, "merged": r.Merged, "credits": r.Credits}
}

func managerTask(tasks []Finding, max int) string {
	list := []map[string]any{}
	for _, f := range tasks {
		list = append(list, taskDescription(f))
	}
	return fmt.Sprintf("当前待办如下。最多创建 %d 个子 Agent，按指定 role 委派，优先审核和可完成的任务。每个子 Agent 返回后再继续分工，最后用中文汇总实际完成结果和剩余任务。必须调用 curator_delegate 处理至少一项；无法处理时解释原因。\n%s", max, assistantJSON(list))
}

func (s *Service) requiresPlatformCredits(ctx context.Context, settings Settings) (bool, error) {
	if settings.Execution.BillingOwnerID == "" || settings.ModelID == "" {
		return true, nil
	}
	_, mainBYO, err := s.resolveModel(ctx, settings.ModelID, settings.Execution.BillingOwnerID, false)
	if err != nil {
		return false, err
	}
	if !mainBYO {
		return true, nil
	}
	if settings.Execution.ChildModelID != "" {
		_, childBYO, err := s.resolveModel(ctx, settings.Execution.ChildModelID, settings.Execution.BillingOwnerID, false)
		if err != nil {
			return false, err
		}
		if !childBYO {
			return true, nil
		}
	}
	if settings.Execution.SearchBYOProviderID != "" {
		return false, nil
	}
	var credits int64
	err = s.Catalog.DB.QueryRowContext(ctx, `SELECT credits_per_request FROM assistant.search_providers WHERE enabled AND (id=$1::uuid OR ($1::uuid IS NULL AND is_default))`, nullableID(settings.Execution.SearchProviderID)).Scan(&credits)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return credits > 0, err
}
