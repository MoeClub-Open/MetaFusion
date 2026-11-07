package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type MissionPlanItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type MissionActor struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	Permissions  []string `json:"permissions,omitempty"`
	TokenVersion int64    `json:"token_version,omitempty"`
}

type Mission struct {
	ID                    string            `json:"id"`
	OwnerID               string            `json:"owner_id"`
	Scope                 string            `json:"scope"`
	Title                 string            `json:"title"`
	Goal                  string            `json:"goal"`
	Steering              string            `json:"steering,omitempty"`
	PendingSteer          bool              `json:"pending_steer,omitempty"`
	Status                string            `json:"status"`
	ModelID               string            `json:"model_id"`
	WorkerModelID         string            `json:"worker_model_id,omitempty"`
	ReasoningEffort       string            `json:"reasoning_effort,omitempty"`
	WorkerReasoningEffort string            `json:"worker_reasoning_effort,omitempty"`
	SearchProviderID      string            `json:"search_provider_id,omitempty"`
	SearchBYOProviderID   string            `json:"search_byo_provider_id,omitempty"`
	BillingUserID         string            `json:"-"`
	BYOOwnerID            string            `json:"-"`
	PreferPullRequest     bool              `json:"prefer_pull_request"`
	Actor                 MissionActor      `json:"-"`
	BudgetCredits         int64             `json:"budget_credits"`
	SpentCredits          int64             `json:"spent_credits"`
	MaxWorkers            int               `json:"max_workers"`
	MaxTasks              int               `json:"max_tasks"`
	PlannerRounds         int               `json:"planner_rounds"`
	MaxPlannerRounds      int               `json:"max_planner_rounds"`
	Plan                  []MissionPlanItem `json:"plan"`
	Summary               string            `json:"summary,omitempty"`
	Error                 string            `json:"error,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	FinishedAt            *time.Time        `json:"finished_at,omitempty"`
}

type MissionTask struct {
	ID              string     `json:"id"`
	MissionID       string     `json:"mission_id"`
	Seq             int        `json:"seq"`
	Role            string     `json:"role"`
	Title           string     `json:"title"`
	Objective       string     `json:"objective"`
	DependsOn       []string   `json:"depends_on"`
	AnchorEntityIDs []string   `json:"anchor_entity_ids"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	ResultSummary   string     `json:"result_summary,omitempty"`
	Discoveries     []string   `json:"discoveries"`
	Sources         []string   `json:"sources"`
	WorkspaceID     string     `json:"workspace_id,omitempty"`
	PullRequestID   string     `json:"pull_request_id,omitempty"`
	Merged          bool       `json:"merged"`
	ModelID         string     `json:"model_id,omitempty"`
	InputTokens     int64      `json:"input_tokens"`
	OutputTokens    int64      `json:"output_tokens"`
	Credits         int64      `json:"credits"`
	Messages        []Message  `json:"messages,omitempty"`
	Error           string     `json:"error,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type MissionPlannerRun struct {
	ID           string     `json:"id"`
	MissionID    string     `json:"mission_id"`
	Round        int        `json:"round"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	Summary      string     `json:"summary,omitempty"`
	ModelID      string     `json:"model_id,omitempty"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	Credits      int64      `json:"credits"`
	Messages     []Message  `json:"messages,omitempty"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type MissionDetail struct {
	Mission     Mission             `json:"mission"`
	Tasks       []MissionTask       `json:"tasks"`
	PlannerRuns []MissionPlannerRun `json:"planner_runs"`
	Balance     *Balance            `json:"balance,omitempty"`
}

type MissionCreateRequest struct {
	ClientRequestID       string `json:"client_request_id"`
	Title                 string `json:"title,omitempty"`
	Goal                  string `json:"goal"`
	ModelID               string `json:"model_id,omitempty"`
	WorkerModelID         string `json:"worker_model_id,omitempty"`
	ReasoningEffort       string `json:"reasoning_effort,omitempty"`
	WorkerReasoningEffort string `json:"worker_reasoning_effort,omitempty"`
	SearchProviderID      string `json:"search_provider_id,omitempty"`
	SearchBYOProviderID   string `json:"search_byo_provider_id,omitempty"`
	BudgetCredits         int64  `json:"budget_credits"`
	MaxWorkers            int    `json:"max_workers,omitempty"`
	MaxTasks              int    `json:"max_tasks,omitempty"`
	MaxPlannerRounds      int    `json:"max_planner_rounds,omitempty"`
	PreferPullRequest     *bool  `json:"prefer_pull_request,omitempty"`
	BYOOwnerID            string `json:"-"`
}

type MissionSteerRequest struct {
	Message       string `json:"message,omitempty"`
	BudgetCredits *int64 `json:"budget_credits,omitempty"`
}

type MissionFilter struct {
	Scope   string
	OwnerID string
	Status  string
}

const missionColumns = "id::text,owner_id,scope,title,goal,steering,pending_steer,status,COALESCE(model_id::text,''),COALESCE(worker_model_id::text,''),reasoning_effort,worker_reasoning_effort,COALESCE(search_provider_id::text,''),COALESCE(search_byo_provider_id::text,''),billing_user_id,byo_owner_id,prefer_pull_request,actor_snapshot,budget_credits,spent_credits,max_workers,max_tasks,planner_rounds,max_planner_rounds,plan,summary,error,created_at,updated_at,finished_at"

func scanMission(row scanner) (Mission, error) {
	var m Mission
	var actorBytes, planBytes []byte
	err := row.Scan(
		&m.ID, &m.OwnerID, &m.Scope, &m.Title, &m.Goal, &m.Steering, &m.PendingSteer, &m.Status,
		&m.ModelID, &m.WorkerModelID, &m.ReasoningEffort, &m.WorkerReasoningEffort, &m.SearchProviderID, &m.SearchBYOProviderID, &m.BillingUserID, &m.BYOOwnerID,
		&m.PreferPullRequest, &actorBytes, &m.BudgetCredits, &m.SpentCredits,
		&m.MaxWorkers, &m.MaxTasks, &m.PlannerRounds, &m.MaxPlannerRounds,
		&planBytes, &m.Summary, &m.Error, &m.CreatedAt, &m.UpdatedAt, &m.FinishedAt,
	)
	if err != nil {
		return m, dbError(err)
	}
	_ = json.Unmarshal(actorBytes, &m.Actor)
	_ = json.Unmarshal(planBytes, &m.Plan)
	if m.Plan == nil {
		m.Plan = []MissionPlanItem{}
	}
	return m, nil
}

func missionTaskColumns(includeMessages bool) string {
	msg := "'[]'::jsonb"
	if includeMessages {
		msg = "messages"
	}
	return "id::text,mission_id::text,seq,role,title,objective,depends_on,anchor_entity_ids,status,priority,result_summary,discoveries,sources,COALESCE(workspace_id::text,''),COALESCE(pull_request_id::text,''),merged,COALESCE(model_id::text,''),input_tokens,output_tokens,credits," + msg + ",error,started_at,finished_at,created_at,updated_at"
}

func scanMissionTask(row scanner) (MissionTask, error) {
	var t MissionTask
	var deps, anchors, disc, srcs, msgs []byte
	err := row.Scan(
		&t.ID, &t.MissionID, &t.Seq, &t.Role, &t.Title, &t.Objective,
		&deps, &anchors, &t.Status, &t.Priority, &t.ResultSummary,
		&disc, &srcs, &t.WorkspaceID, &t.PullRequestID, &t.Merged, &t.ModelID,
		&t.InputTokens, &t.OutputTokens, &t.Credits, &msgs, &t.Error,
		&t.StartedAt, &t.FinishedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return t, dbError(err)
	}
	_ = json.Unmarshal(deps, &t.DependsOn)
	_ = json.Unmarshal(anchors, &t.AnchorEntityIDs)
	_ = json.Unmarshal(disc, &t.Discoveries)
	_ = json.Unmarshal(srcs, &t.Sources)
	_ = json.Unmarshal(msgs, &t.Messages)
	if t.DependsOn == nil {
		t.DependsOn = []string{}
	}
	if t.AnchorEntityIDs == nil {
		t.AnchorEntityIDs = []string{}
	}
	if t.Discoveries == nil {
		t.Discoveries = []string{}
	}
	if t.Sources == nil {
		t.Sources = []string{}
	}
	return t, nil
}

func missionPlannerColumns(includeMessages bool) string {
	msg := "'[]'::jsonb"
	if includeMessages {
		msg = "messages"
	}
	return "id::text,mission_id::text,round,trigger,status,summary,COALESCE(model_id::text,''),input_tokens,output_tokens,credits," + msg + ",error,started_at,finished_at"
}

func scanMissionPlannerRun(row scanner) (MissionPlannerRun, error) {
	var r MissionPlannerRun
	var msgs []byte
	err := row.Scan(
		&r.ID, &r.MissionID, &r.Round, &r.Trigger, &r.Status, &r.Summary,
		&r.ModelID, &r.InputTokens, &r.OutputTokens, &r.Credits, &msgs, &r.Error,
		&r.StartedAt, &r.FinishedAt,
	)
	if err != nil {
		return r, dbError(err)
	}
	_ = json.Unmarshal(msgs, &r.Messages)
	return r, nil
}

func ActorFromCatalogUser(u catalog.User) MissionActor {
	return MissionActor{
		ID:           u.ID,
		Username:     u.Username,
		Permissions:  append([]string(nil), u.Permissions...),
		TokenVersion: u.TokenVersion,
	}
}

func (a MissionActor) catalogUser(preferPR bool) catalog.User {
	perms := make([]string, 0, len(a.Permissions))
	for _, p := range a.Permissions {
		if preferPR && p == catalog.PermissionReviewBypass {
			continue
		}
		perms = append(perms, p)
	}
	return catalog.User{
		ID:           a.ID,
		Username:     a.Username,
		Permissions:  perms,
		TokenVersion: a.TokenVersion,
	}
}

func (s *Store) resolveMissionModel(ctx context.Context, modelID, billingUserID string) (Model, bool, error) {
	models, err := s.Models(ctx, true)
	if err != nil {
		return Model{}, false, err
	}
	for _, m := range models {
		if m.ID == modelID {
			return m, false, nil
		}
	}
	m, _, err := s.ResolveBYOModel(ctx, modelID, billingUserID, true)
	if err != nil {
		return Model{}, false, err
	}
	return m, true, nil
}

func (s *Store) effectiveModelBillingUser(ctx context.Context, modelID, billingUserID, byoOwnerID string) (string, bool, error) {
	models, err := s.Models(ctx, true)
	if err != nil {
		return "", false, err
	}
	for _, m := range models {
		if m.ID == modelID {
			return billingUserID, false, nil
		}
	}
	owner := byoOwnerID
	if owner == "" {
		owner = billingUserID
	}
	if _, _, err := s.ResolveBYOModel(ctx, modelID, owner, true); err != nil {
		return "", false, err
	}
	return owner, true, nil
}

func (s *Store) missionRequiresPlatformCredits(ctx context.Context, modelID, workerModelID, searchProviderID, searchBYOProviderID, billingUserID, byoOwnerID string) (bool, error) {
	_, plannerBYO, err := s.effectiveModelBillingUser(ctx, modelID, billingUserID, byoOwnerID)
	if err != nil {
		return false, err
	}
	workerBYO := plannerBYO
	if workerModelID != "" && workerModelID != modelID {
		_, workerBYO, err = s.effectiveModelBillingUser(ctx, workerModelID, billingUserID, byoOwnerID)
		if err != nil {
			return false, err
		}
	}
	if !plannerBYO || !workerBYO {
		return true, nil
	}
	if searchBYOProviderID != "" {
		sOwner := byoOwnerID
		if sOwner == "" {
			sOwner = billingUserID
		}
		if err = s.ValidateOwnedBYOProvider(ctx, sOwner, searchBYOProviderID); err != nil {
			return false, err
		}
		return false, nil
	}
	var credits int64
	err = s.DB.QueryRowContext(ctx, "SELECT credits_per_request FROM assistant.search_providers WHERE enabled AND (id=$1::uuid OR ($1::uuid IS NULL AND is_default))", nullableID(searchProviderID)).Scan(&credits)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return credits > 0, nil
}

func (s *Store) CreateMission(ctx context.Context, scope, ownerID, billingUserID string, actor MissionActor, in MissionCreateRequest) (MissionDetail, error) {
	var out MissionDetail
	if !validID(in.ClientRequestID) {
		return out, errors.New("assistant_request_id_required")
	}
	in.Goal = strings.TrimSpace(in.Goal)
	if in.Goal == "" || len(in.Goal) > 16000 || !utf8.ValidString(in.Goal) {
		return out, errors.New("assistant_invalid_mission_goal")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = shortTitle(in.Goal)
	} else {
		title = shortTitle(title)
	}
	if scope != "user" && scope != "curator" {
		return out, errors.New("assistant_invalid_mission_scope")
	}
	if ownerID == "" || actor.ID == "" {
		return out, errors.New("assistant_owner_required")
	}
	if billingUserID == "" {
		billingUserID = ownerID
	}
	byoOwnerID := in.BYOOwnerID
	if byoOwnerID == "" && scope == "user" {
		byoOwnerID = ownerID
	}
	if in.BudgetCredits < 0 || in.BudgetCredits > 100000000 {
		return out, errors.New("assistant_invalid_mission_budget")
	}
	maxWorkers := in.MaxWorkers
	if maxWorkers == 0 {
		maxWorkers = 2
	}
	if maxWorkers < 1 || maxWorkers > 4 {
		return out, errors.New("assistant_invalid_mission_workers")
	}
	maxTasks := in.MaxTasks
	if maxTasks == 0 {
		maxTasks = 12
	}
	if maxTasks < 1 || maxTasks > 50 {
		return out, errors.New("assistant_invalid_mission_tasks")
	}
	maxRounds := in.MaxPlannerRounds
	if maxRounds == 0 {
		maxRounds = 8
	}
	if maxRounds < 1 || maxRounds > 20 {
		return out, errors.New("assistant_invalid_mission_rounds")
	}
	preferPR := true
	if in.PreferPullRequest != nil {
		preferPR = *in.PreferPullRequest
	}
	for _, id := range []string{in.ModelID, in.WorkerModelID, in.SearchProviderID, in.SearchBYOProviderID} {
		if id != "" && !validID(id) {
			return out, errors.New("assistant_invalid_id")
		}
	}
	in.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	in.WorkerReasoningEffort = strings.TrimSpace(in.WorkerReasoningEffort)
	if !ValidReasoningEffort(in.ReasoningEffort) || !ValidReasoningEffort(in.WorkerReasoningEffort) {
		return out, errors.New("assistant_invalid_reasoning_effort")
	}

	var existingID string
	err := s.DB.QueryRowContext(ctx, "SELECT id::text FROM assistant.missions WHERE client_request_id=$1", in.ClientRequestID).Scan(&existingID)
	if err == nil {
		existing, e := s.GetMissionDetail(ctx, existingID, ownerID, scope == "curator")
		if e != nil {
			return out, e
		}
		if existing.Mission.Goal != in.Goal || existing.Mission.Scope != scope {
			return out, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}

	settings, err := s.GetSettings(ctx)
	if err != nil {
		return out, err
	}
	if !settings.Enabled {
		return out, ErrDisabled
	}
	modelID := in.ModelID
	if modelID == "" {
		modelID = settings.DefaultModelID
	}
	if modelID == "" {
		return out, ErrNotFound
	}
	needsCredits, err := s.missionRequiresPlatformCredits(ctx, modelID, in.WorkerModelID, in.SearchProviderID, in.SearchBYOProviderID, billingUserID, byoOwnerID)
	if err != nil {
		return out, err
	}
	if in.SearchProviderID != "" {
		var spEnabled bool
		if e := s.DB.QueryRowContext(ctx, "SELECT enabled FROM assistant.search_providers WHERE id=$1", in.SearchProviderID).Scan(&spEnabled); e != nil || !spEnabled {
			return out, errors.New("assistant_invalid_search_provider")
		}
	}
	if in.SearchBYOProviderID != "" {
		sOwner := byoOwnerID
		if sOwner == "" {
			sOwner = billingUserID
		}
		if e := s.ValidateOwnedBYOProvider(ctx, sOwner, in.SearchBYOProviderID); e != nil {
			return out, e
		}
	}
	var balance Balance
	if needsCredits {
		if in.BudgetCredits <= 0 {
			return out, errors.New("assistant_mission_budget_required")
		}
		balance, err = s.SyncCredits(ctx, billingUserID)
		if err != nil {
			return out, err
		}
		if balance.AvailableCredits <= 0 {
			return out, ErrInsufficientCredits
		}
	} else {
		balance, _ = s.SyncCredits(ctx, billingUserID)
	}

	id := uuid.NewString()
	m, err := scanMission(s.DB.QueryRowContext(ctx, "INSERT INTO assistant.missions(id,client_request_id,owner_id,scope,title,goal,status,model_id,worker_model_id,reasoning_effort,worker_reasoning_effort,search_provider_id,search_byo_provider_id,billing_user_id,byo_owner_id,prefer_pull_request,actor_snapshot,budget_credits,max_workers,max_tasks,max_planner_rounds) VALUES($1,$2,$3,$4,$5,$6,'running',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING "+missionColumns,
		id, in.ClientRequestID, ownerID, scope, title, in.Goal, nullableID(modelID), nullableID(in.WorkerModelID),
		in.ReasoningEffort, in.WorkerReasoningEffort,
		nullableID(in.SearchProviderID), nullableID(in.SearchBYOProviderID), billingUserID, byoOwnerID, preferPR,
		jsonBytes(actor), in.BudgetCredits, maxWorkers, maxTasks, maxRounds,
	))
	if err != nil {
		return out, err
	}
	return MissionDetail{Mission: m, Tasks: []MissionTask{}, PlannerRuns: []MissionPlannerRun{}, Balance: &balance}, nil
}

func (s *Store) Missions(ctx context.Context, f MissionFilter, admin bool, limit, offset int) (Page[Mission], error) {
	limit, offset = pageBounds(limit, offset)
	p := Page[Mission]{Items: []Mission{}, Limit: limit, Offset: offset}
	where := " WHERE ($1='' OR scope=$1) AND (($4 AND $2='') OR owner_id=$2) AND ($3='' OR status=$3)"
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM assistant.missions"+where, f.Scope, f.OwnerID, f.Status, admin).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+missionColumns+" FROM assistant.missions"+where+" ORDER BY updated_at DESC,id DESC LIMIT $5 OFFSET $6", f.Scope, f.OwnerID, f.Status, admin, limit, offset)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanMission(rows)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, m)
	}
	return p, rows.Err()
}

func (s *Store) getMission(ctx context.Context, id, ownerID string, admin bool) (Mission, error) {
	if !validID(id) {
		return Mission{}, ErrNotFound
	}
	return scanMission(s.DB.QueryRowContext(ctx, "SELECT "+missionColumns+" FROM assistant.missions WHERE id=$1 AND ($2='' OR owner_id=$2 OR $3)", id, ownerID, admin))
}

func (s *Store) GetMissionDetail(ctx context.Context, id, ownerID string, admin bool) (MissionDetail, error) {
	var out MissionDetail
	m, err := s.getMission(ctx, id, ownerID, admin)
	if err != nil {
		return out, err
	}
	tasks, err := s.missionTasks(ctx, m.ID, false)
	if err != nil {
		return out, err
	}
	runs, err := s.missionPlannerRuns(ctx, m.ID, false)
	if err != nil {
		return out, err
	}
	out = MissionDetail{Mission: m, Tasks: tasks, PlannerRuns: runs}
	if b, e := s.SyncCredits(ctx, m.BillingUserID); e == nil {
		out.Balance = &b
	}
	return out, nil
}

func (s *Store) missionTasks(ctx context.Context, missionID string, includeMessages bool) ([]MissionTask, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+missionTaskColumns(includeMessages)+" FROM assistant.mission_tasks WHERE mission_id=$1 ORDER BY seq,id", missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MissionTask{}
	for rows.Next() {
		t, e := scanMissionTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) missionPlannerRuns(ctx context.Context, missionID string, includeMessages bool) ([]MissionPlannerRun, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+missionPlannerColumns(includeMessages)+" FROM assistant.mission_planner_runs WHERE mission_id=$1 ORDER BY round,id", missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MissionPlannerRun{}
	for rows.Next() {
		r, e := scanMissionPlannerRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetMissionTask(ctx context.Context, missionID, taskID, ownerID string, admin bool) (MissionTask, error) {
	if _, err := s.getMission(ctx, missionID, ownerID, admin); err != nil {
		return MissionTask{}, err
	}
	if !validID(taskID) {
		return MissionTask{}, ErrNotFound
	}
	return scanMissionTask(s.DB.QueryRowContext(ctx, "SELECT "+missionTaskColumns(true)+" FROM assistant.mission_tasks WHERE id=$1 AND mission_id=$2", taskID, missionID))
}

func (s *Store) GetMissionPlannerRun(ctx context.Context, missionID, runID, ownerID string, admin bool) (MissionPlannerRun, error) {
	if _, err := s.getMission(ctx, missionID, ownerID, admin); err != nil {
		return MissionPlannerRun{}, err
	}
	if !validID(runID) {
		return MissionPlannerRun{}, ErrNotFound
	}
	return scanMissionPlannerRun(s.DB.QueryRowContext(ctx, "SELECT "+missionPlannerColumns(true)+" FROM assistant.mission_planner_runs WHERE id=$1 AND mission_id=$2", runID, missionID))
}

func (s *Store) SteerMission(ctx context.Context, id, ownerID string, admin bool, actor *MissionActor, in MissionSteerRequest) (Mission, error) {
	m, err := s.getMission(ctx, id, ownerID, admin)
	if err != nil {
		return m, err
	}
	note := strings.TrimSpace(in.Message)
	if note == "" && in.BudgetCredits == nil {
		return m, errors.New("assistant_invalid_mission_steer")
	}
	if len(note) > 8000 || !utf8.ValidString(note) {
		return m, errors.New("assistant_invalid_mission_steer")
	}
	budget := m.BudgetCredits
	if in.BudgetCredits != nil {
		if *in.BudgetCredits < 0 || *in.BudgetCredits > 100000000 || *in.BudgetCredits < m.SpentCredits {
			return m, errors.New("assistant_invalid_mission_budget")
		}
		budget = *in.BudgetCredits
	}
	steering := m.Steering
	if note != "" {
		if steering == "" {
			steering = note
		} else {
			steering = truncateUTF8(steering+"\n"+note, 16000)
		}
	}
	nextStatus := m.Status
	var finishedAt any = m.FinishedAt
	maxRounds := m.MaxPlannerRounds
	maxTasks := m.MaxTasks
	if m.Status == "completed" || m.Status == "failed" {
		nextStatus = "running"
		finishedAt = nil
		if m.PlannerRounds >= maxRounds && maxRounds < 20 {
			maxRounds = min(20, m.PlannerRounds+4)
		}
		var taskCount int
		_ = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM assistant.mission_tasks WHERE mission_id=$1", m.ID).Scan(&taskCount)
		if taskCount >= maxTasks && maxTasks < 50 {
			maxTasks = min(50, maxTasks+6)
		}
	}
	actorJSON := jsonBytes(m.Actor)
	if actor != nil && actor.ID != "" && m.Scope == "user" {
		actorJSON = jsonBytes(*actor)
	}
	return scanMission(s.DB.QueryRowContext(ctx, "UPDATE assistant.missions SET steering=$2,pending_steer=($3 OR pending_steer),budget_credits=$4,status=$5,error=CASE WHEN $5='running' THEN '' ELSE error END,max_planner_rounds=$6,max_tasks=$7,actor_snapshot=$8,finished_at=$9,updated_at=now() WHERE id=$1 RETURNING "+missionColumns,
		m.ID, steering, note != "", budget, nextStatus, maxRounds, maxTasks, actorJSON, finishedAt,
	))
}

func (s *Store) SetMissionStatus(ctx context.Context, id, ownerID string, admin bool, status string, actor *MissionActor) (Mission, error) {
	m, err := s.getMission(ctx, id, ownerID, admin)
	if err != nil {
		return m, err
	}
	switch status {
	case "paused":
		if m.Status != "running" {
			return m, nil
		}
		return scanMission(s.DB.QueryRowContext(ctx, "UPDATE assistant.missions SET status='paused',updated_at=now() WHERE id=$1 RETURNING "+missionColumns, m.ID))
	case "running":
		if m.Status == "running" {
			return m, nil
		}
		if m.Status == "stopped" {
			return m, ErrConflict
		}
		maxRounds := m.MaxPlannerRounds
		maxTasks := m.MaxTasks
		if m.PlannerRounds >= maxRounds && maxRounds < 20 {
			maxRounds = min(20, m.PlannerRounds+4)
		}
		var taskCount int
		_ = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM assistant.mission_tasks WHERE mission_id=$1", m.ID).Scan(&taskCount)
		if taskCount >= maxTasks && maxTasks < 50 {
			maxTasks = min(50, maxTasks+6)
		}
		actorJSON := jsonBytes(m.Actor)
		if actor != nil && actor.ID != "" && m.Scope == "user" {
			actorJSON = jsonBytes(*actor)
		}
		return scanMission(s.DB.QueryRowContext(ctx, "UPDATE assistant.missions SET status='running',error='',finished_at=NULL,max_planner_rounds=$2,max_tasks=$3,actor_snapshot=$4,updated_at=now() WHERE id=$1 RETURNING "+missionColumns, m.ID, maxRounds, maxTasks, actorJSON))
	case "stopped":
		if m.Status == "stopped" || m.Status == "completed" {
			return m, nil
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return m, err
		}
		defer tx.Rollback()
		_, _ = tx.ExecContext(ctx, "UPDATE assistant.mission_tasks SET status='skipped',error='mission_stopped',finished_at=now(),updated_at=now() WHERE mission_id=$1 AND status='queued'", m.ID)
		updated, err := scanMission(tx.QueryRowContext(ctx, "UPDATE assistant.missions SET status='stopped',finished_at=COALESCE(finished_at,now()),updated_at=now() WHERE id=$1 RETURNING "+missionColumns, m.ID))
		if err != nil {
			return m, err
		}
		return updated, tx.Commit()
	default:
		return m, fmt.Errorf("assistant_invalid_status")
	}
}

func (s *Store) DeleteMission(ctx context.Context, id, ownerID string, admin bool) error {
	if !validID(id) {
		return ErrNotFound
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM assistant.missions WHERE id=$1 AND ($2='' OR owner_id=$2 OR $3) AND status <> 'running'", id, ownerID, admin)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var status string
		if e := s.DB.QueryRowContext(ctx, "SELECT status FROM assistant.missions WHERE id=$1 AND ($2='' OR owner_id=$2 OR $3)", id, ownerID, admin).Scan(&status); e == nil && status == "running" {
			return ErrConflict
		}
		return ErrNotFound
	}
	return nil
}

func truncateUTF8(v string, maxBytes int) string {
	if len(v) <= maxBytes {
		return v
	}
	for maxBytes > 0 && !utf8.RuneStart(v[maxBytes]) {
		maxBytes--
	}
	return v[:maxBytes]
}
