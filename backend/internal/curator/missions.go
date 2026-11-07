package curator

import (
	"context"
	"errors"

	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type MissionInput struct {
	ClientRequestID  string `json:"client_request_id"`
	Title            string `json:"title,omitempty"`
	Goal             string `json:"goal"`
	BudgetCredits    int64  `json:"budget_credits"`
	MaxWorkers       int    `json:"max_workers,omitempty"`
	MaxTasks         int    `json:"max_tasks,omitempty"`
	MaxPlannerRounds int    `json:"max_planner_rounds,omitempty"`
}

func (s *Service) CreateMission(ctx context.Context, in MissionInput) (assistant.MissionDetail, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return assistant.MissionDetail{}, err
	}
	if err := s.ensureAllowance(ctx, settings); err != nil {
		return assistant.MissionDetail{}, err
	}
	modelID := settings.ModelID
	if modelID == "" {
		as, e := s.Assistant.GetSettings(ctx)
		if e != nil {
			return assistant.MissionDetail{}, e
		}
		modelID = as.DefaultModelID
	}
	if modelID == "" {
		return assistant.MissionDetail{}, ErrModelRequired
	}
	workerModelID := settings.Execution.ChildModelID
	billingUser := settings.SystemUserID
	maxWorkers := in.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = min(4, max(1, settings.Execution.MaxChildren))
	}
	budget := in.BudgetCredits
	required, err := s.requiresPlatformCredits(ctx, settings)
	if err != nil {
		return assistant.MissionDetail{}, err
	}
	if required && budget <= 0 {
		budget = min(200, settings.MonthlyCreditAllowance)
	}
	preferPR := true
	actor := assistant.MissionActor{
		ID:          settings.SystemUserID,
		Username:    "curator",
		Permissions: []string{catalog.PermissionEntityEdit, catalog.PermissionRelationEdit},
	}
	detail, err := s.Assistant.CreateMission(ctx, "curator", settings.SystemUserID, billingUser, actor, assistant.MissionCreateRequest{
		ClientRequestID:     in.ClientRequestID,
		Title:               in.Title,
		Goal:                in.Goal,
		ModelID:             modelID,
		WorkerModelID:       workerModelID,
		SearchProviderID:    settings.Execution.SearchProviderID,
		SearchBYOProviderID: settings.Execution.SearchBYOProviderID,
		BudgetCredits:       budget,
		MaxWorkers:          maxWorkers,
		MaxTasks:            in.MaxTasks,
		MaxPlannerRounds:    in.MaxPlannerRounds,
		PreferPullRequest:   &preferPR,
		BYOOwnerID:          settings.Execution.BillingOwnerID,
	})
	if err != nil {
		if errors.Is(err, assistant.ErrDisabled) {
			return detail, ErrAssistantDisabled
		}
		if errors.Is(err, assistant.ErrInsufficientCredits) || errors.Is(err, assistant.ErrBudgetExceeded) {
			return detail, ErrAllowanceExceeded
		}
		return detail, err
	}
	if eng := s.Assistant.EnsureMissionEngine(s.Catalog); eng != nil && detail.Mission.Status == "running" {
		eng.Wake(detail.Mission.ID)
	}
	return detail, nil
}

func (s *Service) Missions(ctx context.Context, status string, limit, offset int) (assistant.Page[assistant.Mission], error) {
	return s.Assistant.Missions(ctx, assistant.MissionFilter{Scope: "curator", Status: status}, true, limit, offset)
}

func (s *Service) GetMission(ctx context.Context, id string) (assistant.MissionDetail, error) {
	d, err := s.Assistant.GetMissionDetail(ctx, id, "", true)
	if err != nil {
		if errors.Is(err, assistant.ErrNotFound) {
			return d, ErrNotFound
		}
		return d, err
	}
	if d.Mission.Scope != "curator" {
		return assistant.MissionDetail{}, ErrNotFound
	}
	return d, nil
}

func (s *Service) GetMissionTask(ctx context.Context, id, taskID string) (assistant.MissionTask, error) {
	if _, err := s.GetMission(ctx, id); err != nil {
		return assistant.MissionTask{}, err
	}
	t, err := s.Assistant.GetMissionTask(ctx, id, taskID, "", true)
	if errors.Is(err, assistant.ErrNotFound) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Service) GetMissionPlannerRun(ctx context.Context, id, runID string) (assistant.MissionPlannerRun, error) {
	if _, err := s.GetMission(ctx, id); err != nil {
		return assistant.MissionPlannerRun{}, err
	}
	r, err := s.Assistant.GetMissionPlannerRun(ctx, id, runID, "", true)
	if errors.Is(err, assistant.ErrNotFound) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Service) SteerMission(ctx context.Context, id string, in assistant.MissionSteerRequest) (assistant.Mission, error) {
	if _, err := s.GetMission(ctx, id); err != nil {
		return assistant.Mission{}, err
	}
	m, err := s.Assistant.SteerMission(ctx, id, "", true, nil, in)
	if err == nil && m.Status == "running" {
		if eng := s.Assistant.EnsureMissionEngine(s.Catalog); eng != nil {
			eng.Wake(m.ID)
		}
	}
	return m, err
}

func (s *Service) SetMissionStatus(ctx context.Context, id, status string) (assistant.Mission, error) {
	if _, err := s.GetMission(ctx, id); err != nil {
		return assistant.Mission{}, err
	}
	if status == "running" {
		if settings, err := s.Settings(ctx); err == nil {
			if e := s.ensureAllowance(ctx, settings); e != nil {
				return assistant.Mission{}, e
			}
		}
	}
	m, err := s.Assistant.SetMissionStatus(ctx, id, "", true, status, nil)
	if err == nil {
		if eng := s.Assistant.EnsureMissionEngine(s.Catalog); eng != nil {
			if status == "running" {
				eng.Wake(m.ID)
			} else {
				eng.Interrupt(m.ID)
			}
		}
	}
	return m, err
}
