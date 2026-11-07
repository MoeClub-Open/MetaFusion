package curator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

var (
	ErrNotFound          = errors.New("curator_not_found")
	ErrConflict          = errors.New("curator_conflict")
	ErrInvalidSettings   = errors.New("curator_invalid_settings")
	ErrAllowanceRequired = errors.New("curator_allowance_required")
	ErrAllowanceExceeded = errors.New("curator_monthly_allowance_exceeded")
	ErrAssistantDisabled = errors.New("curator_assistant_disabled")
	ErrModelRequired     = errors.New("curator_model_required")
	ErrInactive          = errors.New("curator_scope_inactive")
	ErrLeaseLost         = errors.New("curator_scheduler_lease_lost")
)

const settingsColumns = `enabled,COALESCE(model_id::text,''),interval_seconds,max_runs_per_hour,monthly_credit_allowance,auto_merge_low_risk,allow_structures,allow_sources,allow_names,allow_dedupe,system_user_id,reviewer_user_id,paused,paused_reason,consecutive_failures,sweep_cursor_updated_at,COALESCE(sweep_cursor_id::text,''),sweep_detector,last_sweep_at,last_run_at,updated_at,execution`

const findingColumns = `f.id::text,COALESCE(f.entity_id::text,''),f.entity_kind,COALESCE(e.title,''),f.code,f.severity,f.priority,f.signature,f.details,f.status,f.decision,f.reason,f.sources,f.attempts,f.next_attempt_at,f.last_error,COALESCE(f.last_run_id::text,''),COALESCE(f.workspace_id::text,''),COALESCE(f.pull_request_id::text,''),f.merged,f.auto_merged,f.last_seen_at,f.resolved_at,f.created_at,f.updated_at,COALESCE(f.review_pr_id::text,'')`

const runColumns = `id::text,COALESCE(finding_id::text,''),COALESCE(entity_id::text,''),kind,trigger_kind,status,COALESCE(model_id::text,''),decision,reason,sources,tool_trace,detail,COALESCE(workspace_id::text,''),COALESCE(pull_request_id::text,''),merged,auto_merged,error,input_tokens,output_tokens,credits,started_at,finished_at,created_at,COALESCE(parent_run_id::text,''),role,messages`

type Service struct {
	Catalog    *catalog.Store
	Assistant  *assistant.Store
	wake       chan struct{}
	forceSweep atomic.Bool
	leaseHeld  atomic.Bool
	lease      atomic.Pointer[schedulerLease]
}

func New(cat *catalog.Store, asst *assistant.Store) *Service {
	svc := &Service{Catalog: cat, Assistant: asst, wake: make(chan struct{}, 1)}
	if asst != nil && cat != nil {
		eng := asst.EnsureMissionEngine(cat)
		eng.CuratorMonthlyLimit = func(ctx context.Context) (int64, error) {
			st, err := svc.Settings(ctx)
			if err != nil {
				return 0, err
			}
			if err := svc.ensureAllowance(ctx, st); err != nil {
				return 0, err
			}
			return st.MonthlyCreditAllowance, nil
		}
	}
	return svc
}

func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) ForceSweep() {
	s.forceSweep.Store(true)
	s.Wake()
}

func (s *Service) LeaseHeld() bool { return s.leaseHeld.Load() }

func nullableID(id string) any {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return id
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	var model, cursorID string
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT `+settingsColumns+` FROM curator.settings WHERE singleton`).Scan(
		&v.Enabled, &model, &v.IntervalSeconds, &v.MaxRunsPerHour, &v.MonthlyCreditAllowance,
		&v.AutoMergeLowRisk, &v.AllowStructures, &v.AllowSources, &v.AllowNames, &v.AllowDedupe,
		&v.SystemUserID, &v.ReviewerUserID, &v.Paused, &v.PausedReason, &v.ConsecutiveFailures,
		&v.SweepCursorUpdatedAt, &cursorID, &v.SweepDetector, &v.LastSweepAt, &v.LastRunAt, &v.UpdatedAt, &v.Execution,
	)
	v.ModelID, v.SweepCursorID = model, cursorID
	return v, err
}

func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	if err := s.validateSettings(ctx, in); err != nil {
		return Settings{}, err
	}
	var v Settings
	var model, cursorID string
	err := s.Catalog.DB.QueryRowContext(ctx, `UPDATE curator.settings SET
		enabled=$1,model_id=$2,interval_seconds=$3,max_runs_per_hour=$4,monthly_credit_allowance=$5,
		auto_merge_low_risk=$6,allow_structures=$7,allow_sources=$8,allow_names=$9,allow_dedupe=$10,
		execution=$11,updated_at=now()
		WHERE singleton RETURNING `+settingsColumns,
		in.Enabled, nullableID(in.ModelID), in.IntervalSeconds, in.MaxRunsPerHour, in.MonthlyCreditAllowance,
		in.AutoMergeLowRisk, in.AllowStructures, in.AllowSources, in.AllowNames, in.AllowDedupe, assistantJSON(in.Execution),
	).Scan(
		&v.Enabled, &model, &v.IntervalSeconds, &v.MaxRunsPerHour, &v.MonthlyCreditAllowance,
		&v.AutoMergeLowRisk, &v.AllowStructures, &v.AllowSources, &v.AllowNames, &v.AllowDedupe,
		&v.SystemUserID, &v.ReviewerUserID, &v.Paused, &v.PausedReason, &v.ConsecutiveFailures,
		&v.SweepCursorUpdatedAt, &cursorID, &v.SweepDetector, &v.LastSweepAt, &v.LastRunAt, &v.UpdatedAt, &v.Execution,
	)
	v.ModelID, v.SweepCursorID = model, cursorID
	return v, err
}

func (s *Service) SetPaused(ctx context.Context, paused bool, reason string) (Settings, error) {
	var v Settings
	var model, cursorID string
	err := s.Catalog.DB.QueryRowContext(ctx, `UPDATE curator.settings SET paused=$1,paused_reason=$2,
		consecutive_failures=CASE WHEN $1 THEN consecutive_failures ELSE 0 END,updated_at=now()
		WHERE singleton RETURNING `+settingsColumns, paused, truncate(reason, 200)).Scan(
		&v.Enabled, &model, &v.IntervalSeconds, &v.MaxRunsPerHour, &v.MonthlyCreditAllowance,
		&v.AutoMergeLowRisk, &v.AllowStructures, &v.AllowSources, &v.AllowNames, &v.AllowDedupe,
		&v.SystemUserID, &v.ReviewerUserID, &v.Paused, &v.PausedReason, &v.ConsecutiveFailures,
		&v.SweepCursorUpdatedAt, &cursorID, &v.SweepDetector, &v.LastSweepAt, &v.LastRunAt, &v.UpdatedAt, &v.Execution,
	)
	v.ModelID, v.SweepCursorID = model, cursorID
	return v, err
}

func (s *Service) validateSettings(ctx context.Context, in Settings) error {
	if err := s.validateExecution(ctx, in.Execution, in.Enabled); err != nil {
		return err
	}
	if in.IntervalSeconds < 30 || in.IntervalSeconds > 86400 || in.MaxRunsPerHour < 1 || in.MaxRunsPerHour > 240 ||
		in.MonthlyCreditAllowance < 0 || in.MonthlyCreditAllowance > 1_000_000_000_000 {
		return ErrInvalidSettings
	}
	// system_user_id / reviewer_user_id 是迁移写入的固定服务身份，管理接口只读，
	// 防止把自动写入记到真实账号或扣其积分。
	if in.ModelID != "" {
		if _, err := uuid.Parse(in.ModelID); err != nil {
			return ErrInvalidSettings
		}
	}
	if !in.AllowStructures && !in.AllowSources && !in.AllowNames && !in.AllowDedupe && !in.Execution.AllowReviews {
		return ErrInvalidSettings
	}
	if !in.Enabled {
		return nil
	}
	if in.MonthlyCreditAllowance <= 0 {
		required, err := s.requiresPlatformCredits(ctx, in)
		if err != nil {
			return err
		}
		if required {
			return ErrAllowanceRequired
		}
	}
	as, err := s.Assistant.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !as.Enabled {
		return ErrAssistantDisabled
	}
	modelID := in.ModelID
	if modelID == "" {
		modelID = as.DefaultModelID
	}
	if modelID == "" {
		return ErrModelRequired
	}
	_, _, err = s.resolveModel(ctx, modelID, in.Execution.BillingOwnerID, true)
	return err
}

func (s *Service) spentCredits(ctx context.Context, settings Settings, month time.Time) (int64, error) {
	var spent int64
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT (SELECT COALESCE(sum(credits),0) FROM assistant.usage
		WHERE user_id=$1 AND purpose LIKE 'curator:%' AND budget_month=$2 AND status IN ('completed','failed','unknown')) + (SELECT COALESCE(sum(credits),0) FROM assistant.search_requests WHERE user_id=$1 AND purpose LIKE 'curator:%' AND budget_month=$2 AND status IN ('completed','unknown'))`,
		settings.SystemUserID, month).Scan(&spent)
	return spent, err
}

// ensureAllowance funds the service account at the start of each month and
// enforces a monthly usage cap independently of rollover balance.
func (s *Service) ensureAllowance(ctx context.Context, settings Settings) error {
	required, err := s.requiresPlatformCredits(ctx, settings)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	if settings.MonthlyCreditAllowance <= 0 {
		return ErrAllowanceRequired
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spent, err := s.spentCredits(ctx, settings, month)
	if err != nil {
		return err
	}
	if spent >= settings.MonthlyCreditAllowance {
		return ErrAllowanceExceeded
	}
	balance, err := s.Assistant.SyncCredits(ctx, settings.SystemUserID)
	if err != nil {
		return err
	}
	if spent == 0 && balance.Credits < settings.MonthlyCreditAllowance {
		amount := settings.MonthlyCreditAllowance - balance.Credits
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("metafusion-curator-allowance:%s:%d:%d", month.Format("2006-01"), settings.MonthlyCreditAllowance, amount))).String()
		_, err := s.Assistant.AdjustCredits(ctx, assistant.CreditAdjustment{
			ID: id, UserID: settings.SystemUserID, Credits: amount,
			Note: "curator monthly allowance " + month.Format("2006-01"),
		})
		if err != nil && !errors.Is(err, assistant.ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) monthSpent(ctx context.Context, settings Settings) (int64, error) {
	now := time.Now().UTC()
	return s.spentCredits(ctx, settings, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC))
}

func (s *Service) UpsertFindings(ctx context.Context, candidates []candidate) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	tx, err := s.Catalog.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, c := range candidates {
		details, err := json.Marshal(c.Details)
		if err != nil {
			return n, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO curator.findings AS f
			(id,entity_id,entity_kind,code,severity,priority,signature,details,status,next_attempt_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open',now())
			ON CONFLICT (entity_id,code,signature) DO UPDATE SET
				entity_kind=EXCLUDED.entity_kind,severity=EXCLUDED.severity,priority=EXCLUDED.priority,
				details=EXCLUDED.details,last_seen_at=now(),updated_at=now(),
				status=CASE WHEN f.status='resolved' THEN 'open' ELSE f.status END,
				next_attempt_at=CASE WHEN f.status='resolved' THEN now() ELSE f.next_attempt_at END,
				last_run_id=CASE WHEN f.status='resolved' THEN NULL ELSE f.last_run_id END,
				workspace_id=CASE WHEN f.status='resolved' THEN NULL ELSE f.workspace_id END,
				pull_request_id=CASE WHEN f.status='resolved' THEN NULL ELSE f.pull_request_id END,
				attempts=CASE WHEN f.status='resolved' THEN 0 ELSE f.attempts END,
				decision=CASE WHEN f.status='resolved' THEN '' ELSE f.decision END,
				reason=CASE WHEN f.status='resolved' THEN '' ELSE f.reason END,
				sources=CASE WHEN f.status='resolved' THEN '[]'::jsonb ELSE f.sources END,
				last_error=CASE WHEN f.status='resolved' THEN '' ELSE f.last_error END,
				merged=f.status<>'resolved' AND f.merged,auto_merged=f.status<>'resolved' AND f.auto_merged,
				resolved_at=NULL`,
			uuid.NewString(), c.EntityID, c.Kind, c.Code, c.Severity, c.Priority, c.Signature, string(details))
		if err != nil {
			return n, err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n, tx.Commit()
}

// ClaimFinding atomically claims one due finding and marks stale running rows
// as failed so a crashed process cannot strand the queue.
func (s *Service) ClaimFinding(ctx context.Context, settings Settings) (Finding, error) {
	if _, err := s.Catalog.DB.ExecContext(ctx, `WITH interrupted AS (
		UPDATE curator.findings SET status='failed',next_attempt_at=now()+interval '5 minutes',last_error='run_interrupted',updated_at=now()
		WHERE status='running' AND updated_at < now()-interval '5 minutes' RETURNING last_run_id
	) UPDATE curator.runs SET status='failed',error='run_interrupted',finished_at=now()
		WHERE id IN (SELECT last_run_id FROM interrupted) AND status='running'`); err != nil {
		return Finding{}, err
	}
	if _, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings f SET status='resolved',resolved_at=now(),updated_at=now()
		FROM catalog.entities e WHERE e.id=f.entity_id AND e.state<>'active' AND f.status IN ('open','failed')`); err != nil {
		return Finding{}, err
	}
	var id string
	err := s.Catalog.DB.QueryRowContext(ctx, `WITH next AS (
			SELECT f.id FROM curator.findings f
			JOIN catalog.entities e ON e.id=f.entity_id AND e.state='active'
			WHERE f.status IN ('open','failed') AND f.next_attempt_at<=now() AND f.code=ANY($1::text[])
			ORDER BY f.priority,f.next_attempt_at,f.created_at,f.id
			FOR UPDATE OF f SKIP LOCKED LIMIT 1
		)
		UPDATE curator.findings f SET status='running',attempts=f.attempts+1,updated_at=now()
		FROM next WHERE f.id=next.id
		RETURNING f.id::text`, pq.Array(s.enabledCodes(settings))).Scan(&id)
	if err != nil {
		return Finding{}, err
	}
	return s.GetFinding(ctx, id)
}

func (s *Service) releaseFinding(ctx context.Context, id string) error {
	_, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET status='open',attempts=GREATEST(attempts-1,0),updated_at=now()
		WHERE id=$1 AND status='running'`, id)
	return err
}

func (s *Service) GetFinding(ctx context.Context, id string) (Finding, error) {
	return scanFinding(s.Catalog.DB.QueryRowContext(ctx, `SELECT `+findingColumns+` FROM curator.findings f LEFT JOIN catalog.entities e ON e.id=f.entity_id WHERE f.id=$1`, id))
}

func (s *Service) Findings(ctx context.Context, filter FindingFilter, limit, offset int) (Page[Finding], error) {
	limit, offset = pageBounds(limit, offset)
	where := []string{"1=1"}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if filter.Status != "" {
		add("f.status=$%d", filter.Status)
	}
	if filter.Code != "" {
		add("f.code=$%d", filter.Code)
	}
	if filter.Kind != "" {
		add("f.entity_kind=$%d", filter.Kind)
	}
	p := Page[Finding]{Items: []Finding{}, Limit: limit, Offset: offset}
	countSQL := `SELECT count(*) FROM curator.findings f WHERE ` + strings.Join(where, " AND ")
	if err := s.Catalog.DB.QueryRowContext(ctx, countSQL, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	args = append(args, limit, offset)
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT `+findingColumns+` FROM curator.findings f
		LEFT JOIN catalog.entities e ON e.id=f.entity_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY f.updated_at DESC,f.id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, f)
	}
	return p, rows.Err()
}

func (s *Service) ReopenFinding(ctx context.Context, id string) (Finding, error) {
	res, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET status='open',attempts=0,next_attempt_at=now(),last_error='',updated_at=now(),
		last_run_id=CASE WHEN status='resolved' THEN NULL ELSE last_run_id END,
		workspace_id=CASE WHEN status='resolved' THEN NULL ELSE workspace_id END,
		pull_request_id=CASE WHEN status='resolved' THEN NULL ELSE pull_request_id END,
		decision='',reason='',sources='[]',merged=false,auto_merged=false,resolved_at=NULL
		WHERE id=$1 AND status<>'running'`, id)
	if err != nil {
		return Finding{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Finding{}, ErrConflict
	}
	return s.GetFinding(ctx, id)
}

func (s *Service) IgnoreFinding(ctx context.Context, id string) (Finding, error) {
	res, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.findings SET status='ignored',updated_at=now() WHERE id=$1 AND status<>'running'`, id)
	if err != nil {
		return Finding{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Finding{}, ErrConflict
	}
	return s.GetFinding(ctx, id)
}

func scanFinding(row interface{ Scan(...any) error }) (Finding, error) {
	var f Finding
	err := row.Scan(&f.ID, &f.EntityID, &f.EntityKind, &f.EntityTitle, &f.Code, &f.Severity, &f.Priority, &f.Signature,
		&f.Details, &f.Status, &f.Decision, &f.Reason, &f.Sources, &f.Attempts, &f.NextAttemptAt, &f.LastError,
		&f.LastRunID, &f.WorkspaceID, &f.PullRequestID, &f.Merged, &f.AutoMerged, &f.LastSeenAt, &f.ResolvedAt,
		&f.CreatedAt, &f.UpdatedAt, &f.ReviewPRID)
	if err != nil {
		return Finding{}, err
	}
	if f.Details == nil {
		f.Details = json.RawMessage(`{}`)
	}
	if f.Sources == nil {
		f.Sources = json.RawMessage(`[]`)
	}
	return f, nil
}

func (s *Service) CreateRun(ctx context.Context, kind, trigger string, finding *Finding, detail json.RawMessage) (Run, error) {
	return s.createRun(ctx, kind, trigger, finding, detail, "", "")
}
func (s *Service) createRun(ctx context.Context, kind, trigger string, finding *Finding, detail json.RawMessage, parent, role string) (Run, error) {
	var findingID, entityID any
	if finding != nil {
		findingID, entityID = finding.ID, nullableID(finding.EntityID)
	}
	var r Run
	var mid string
	var messages []byte
	tx, err := s.Catalog.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `INSERT INTO curator.runs(id,finding_id,entity_id,kind,trigger_kind,status,detail,parent_run_id,role)
		VALUES($1,$2,$3,$4,$5,'running',$6,$7,$8) RETURNING `+runColumns, uuid.NewString(), findingID, entityID, kind, trigger, defaultJSON(detail, "{}"), nullableID(parent), role).Scan(
		&r.ID, &r.FindingID, &r.EntityID, &r.Kind, &r.Trigger, &r.Status, &mid, &r.Decision, &r.Reason,
		&r.Sources, &r.ToolTrace, &r.Detail, &r.WorkspaceID, &r.PullRequestID, &r.Merged, &r.AutoMerged,
		&r.Error, &r.InputTokens, &r.OutputTokens, &r.Credits, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.ParentRunID, &r.Role, &messages)
	r.ModelID = mid
	_ = json.Unmarshal(messages, &r.Messages)
	if err != nil {
		return r, err
	}
	if finding != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE curator.findings SET last_run_id=$2,updated_at=now() WHERE id=$1 AND status='running'`, finding.ID, r.ID); err != nil {
			return r, err
		}
	}
	return r, tx.Commit()
}

func (s *Service) finishRun(ctx context.Context, tx *sql.Tx, r Run) error {
	res, err := tx.ExecContext(ctx, `UPDATE curator.runs SET status=$2,model_id=$3,decision=$4,reason=$5,sources=$6,tool_trace=$7,detail=$8,
		workspace_id=$9,pull_request_id=$10,merged=$11,auto_merged=$12,error=$13,input_tokens=$14,output_tokens=$15,credits=$16,messages=$17,finished_at=now()
		WHERE id=$1 AND status='running'`,
		r.ID, r.Status, nullableID(r.ModelID), r.Decision, r.Reason, defaultJSON(r.Sources, "[]"), defaultJSON(r.ToolTrace, "[]"),
		defaultJSON(r.Detail, "{}"), nullableID(r.WorkspaceID), nullableID(r.PullRequestID), r.Merged, r.AutoMerged, r.Error,
		r.InputTokens, r.OutputTokens, r.Credits, assistantJSON(r.Messages))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) Runs(ctx context.Context, filter RunFilter, limit, offset int) (Page[Run], error) {
	limit, offset = pageBounds(limit, offset)
	where := "1=1"
	args := []any{}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		where = "kind=$1"
	}
	if filter.ParentID != "" {
		args = append(args, filter.ParentID)
		where += " AND parent_run_id=$" + fmt.Sprint(len(args))
	}
	p := Page[Run]{Items: []Run{}, Limit: limit, Offset: offset}
	if err := s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM curator.runs WHERE `+where, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	args = append(args, limit, offset)
	listColumns := strings.Replace(runColumns, ",messages", ",'[]'::jsonb", 1)
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT `+listColumns+` FROM curator.runs WHERE `+where+`
		ORDER BY started_at DESC,id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Run
		var mid string
		var messages []byte
		if err := rows.Scan(&r.ID, &r.FindingID, &r.EntityID, &r.Kind, &r.Trigger, &r.Status, &mid, &r.Decision, &r.Reason,
			&r.Sources, &r.ToolTrace, &r.Detail, &r.WorkspaceID, &r.PullRequestID, &r.Merged, &r.AutoMerged,
			&r.Error, &r.InputTokens, &r.OutputTokens, &r.Credits, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.ParentRunID, &r.Role, &messages); err != nil {
			return p, err
		}
		r.ModelID = mid
		_ = json.Unmarshal(messages, &r.Messages)
		p.Items = append(p.Items, r)
	}
	return p, rows.Err()
}

func (s *Service) GetRun(ctx context.Context, id string) (Run, error) {
	var r Run
	var mid string
	var messages []byte
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT `+runColumns+` FROM curator.runs WHERE id=$1`, id).Scan(
		&r.ID, &r.FindingID, &r.EntityID, &r.Kind, &r.Trigger, &r.Status, &mid, &r.Decision, &r.Reason,
		&r.Sources, &r.ToolTrace, &r.Detail, &r.WorkspaceID, &r.PullRequestID, &r.Merged, &r.AutoMerged,
		&r.Error, &r.InputTokens, &r.OutputTokens, &r.Credits, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.ParentRunID, &r.Role, &messages)
	r.ModelID = mid
	_ = json.Unmarshal(messages, &r.Messages)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Service) Summary(ctx context.Context) (Summary, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Summary{}, err
	}
	as, err := s.Assistant.GetSettings(ctx)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Settings: settings, AssistantEnabled: as.Enabled, LeaseHeld: s.LeaseHeld(), Counts: map[string]int64{}}
	modelID := settings.ModelID
	if modelID == "" {
		modelID = as.DefaultModelID
	}
	if modelID != "" {
		if m, _, e := s.resolveModel(ctx, modelID, settings.Execution.BillingOwnerID, true); e == nil {
			out.Model = &m
		}
	}
	if out.Balance, err = s.Assistant.SyncCredits(ctx, settings.SystemUserID); err != nil {
		return out, err
	}
	if out.SpentCredits, err = s.monthSpent(ctx, settings); err != nil {
		return out, err
	}
	out.RemainingCredits = settings.MonthlyCreditAllowance - out.SpentCredits
	if out.RemainingCredits < 0 {
		out.RemainingCredits = 0
	}
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT status,count(*) FROM curator.findings GROUP BY status`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts[status] = n
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	err = s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM curator.runs WHERE kind IN ('agent','manager') AND started_at>now()-interval '24 hours'`).Scan(&out.Runs24h)
	return out, err
}

func (s *Service) RecordSweep(ctx context.Context, kind string, detail map[string]any, started time.Time, errText string) {
	status := "completed"
	if errText != "" {
		status = "failed"
	}
	raw, _ := json.Marshal(detail)
	_, _ = s.Catalog.DB.ExecContext(ctx, `INSERT INTO curator.runs(id,kind,trigger_kind,status,reason,detail,error,started_at,finished_at)
		VALUES($1,$2,'sweep',$3,'',$4,$5,$6,now())`, uuid.NewString(), kind, status, string(raw), errText, started)
}

func (s *Service) NoteRunFinished(ctx context.Context, failed bool) {
	if failed {
		_, _ = s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET consecutive_failures=consecutive_failures+1,last_run_at=now(),updated_at=now() WHERE singleton`)
		s.PauseForFailures(ctx)
		return
	}
	_, _ = s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET consecutive_failures=0,last_run_at=now(),updated_at=now() WHERE singleton`)
}

func (s *Service) PauseForFailures(ctx context.Context) {
	_, _ = s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET paused=true,paused_reason='consecutive_failures',updated_at=now()
		WHERE singleton AND enabled AND consecutive_failures>=5`)
}

func (s *Service) countRunsLastHour(ctx context.Context) (int64, error) {
	var n int64
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM curator.runs WHERE kind='agent' AND started_at>now()-interval '1 hour'`).Scan(&n)
	return n, err
}

// PendingCount 是仍未得出结论的候选数（含等待退避重试的失败项）。
func (s *Service) PendingCount(ctx context.Context, settings Settings) (int64, error) {
	var n int64
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM curator.findings WHERE status IN ('open','running','failed') AND code=ANY($1::text[])`, pq.Array(s.enabledCodes(settings))).Scan(&n)
	return n, err
}

func (s *Service) claimLease(ctx context.Context, conn *sql.Conn) (bool, error) {
	var ok bool
	err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(740211)`).Scan(&ok)
	return ok, err
}

func (s *Service) releaseLease(ctx context.Context, conn *sql.Conn) {
	_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(740211)`)
}

func (s *Service) UpdateSweepCursor(ctx context.Context, updatedAt time.Time, id string, nextDetector int) error {
	_, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET sweep_cursor_updated_at=$1,sweep_cursor_id=$2,sweep_detector=$3,last_sweep_at=now(),updated_at=now() WHERE singleton`, updatedAt, nullableID(id), nextDetector)
	return err
}

func (s *Service) ResetSweepCursor(ctx context.Context, nextDetector int) error {
	_, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET sweep_cursor_updated_at=NULL,sweep_cursor_id=NULL,sweep_detector=$1,last_sweep_at=now(),updated_at=now() WHERE singleton`, nextDetector)
	return err
}

func pageBounds(limit, offset int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 || offset > 1_000_000 {
		offset = 0
	}
	return limit, offset
}

func defaultJSON(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	return string(raw)
}
