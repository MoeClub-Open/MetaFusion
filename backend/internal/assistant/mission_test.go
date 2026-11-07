package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestMissionExecutableFrontierAndPermissions(t *testing.T) {
	t1 := uuid.NewString()
	t2 := uuid.NewString()
	t3 := uuid.NewString()
	t4 := uuid.NewString()

	tasks := []MissionTask{
		{ID: t1, Seq: 1, Status: "queued", Priority: 3},
		{ID: t2, Seq: 2, Status: "queued", Priority: 1},
		{ID: t3, Seq: 3, Status: "queued", Priority: 1, DependsOn: []string{t1, t2}},
		{ID: t4, Seq: 4, Status: "completed", Priority: 2},
	}
	ready := executableFrontier(tasks)
	if len(ready) != 2 || ready[0].ID != t2 || ready[1].ID != t1 {
		t.Fatalf("unexpected initial frontier order: %+v", ready)
	}

	tasks[0].Status = "completed"
	tasks[1].Status = "completed"
	readyAfter := executableFrontier(tasks)
	if len(readyAfter) != 1 || readyAfter[0].ID != t3 {
		t.Fatalf("dependent task did not unlock: %+v", readyAfter)
	}

	actor := MissionActor{
		ID:           uuid.NewString(),
		Username:     "curator-editor",
		Permissions:  []string{catalog.PermissionEntityEdit, catalog.PermissionRelationEdit, catalog.PermissionReviewBypass},
		TokenVersion: 3,
	}
	prUser := actor.catalogUser(true)
	if prUser.Can(catalog.PermissionReviewBypass) {
		t.Fatalf("prefer_pull_request should strip review bypass permission: %+v", prUser.Permissions)
	}
	directUser := actor.catalogUser(false)
	if !directUser.Can(catalog.PermissionReviewBypass) {
		t.Fatalf("prefer_pull_request=false should retain review bypass permission")
	}

	editTools := workerAllowedTools("edit", prUser, false)
	if !slices.Contains(editTools, "catalog_workspace_push") || slices.Contains(editTools, "catalog_pull_request_merge") {
		t.Fatalf("unexpected edit tools: %v", editTools)
	}
	reconTools := workerAllowedTools("recon", prUser, false)
	if slices.Contains(reconTools, "catalog_workspace_create") {
		t.Fatalf("recon worker must remain read-only: %v", reconTools)
	}

	var wsID, prID string
	var merged bool
	fakeWS := uuid.NewString()
	fakePR := uuid.NewString()
	observeWorkspaceArtifacts("catalog_workspace_push", map[string]any{
		"applied":      false,
		"workspace":    map[string]any{"id": fakeWS},
		"pull_request": map[string]any{"id": fakePR},
	}, &wsID, &prID, &merged)
	if wsID != fakeWS || prID != fakePR || merged {
		t.Fatalf("unexpected observed artifacts: ws=%s pr=%s merged=%v", wsID, prID, merged)
	}

	anchorA := uuid.NewString()
	anchorB := uuid.NewString()
	rep := extractWorkerReport(&workerReportPayload{
		Summary:         "  核实完成  ",
		AnchorEntityIDs: []string{anchorB, "not-a-uuid"},
		Discoveries:     []string{"发现新单曲", "发现新单曲"},
		Sources:         []string{"https://bang-dream.com/discographies/1", "javascript:alert(1)"},
	}, "", []string{anchorA})
	if rep.Summary != "核实完成" || len(rep.AnchorEntityIDs) != 2 || len(rep.Discoveries) != 1 || len(rep.Sources) != 1 {
		t.Fatalf("unexpected extracted worker report: %+v", rep)
	}

	hits := searchTraceTasks([]MissionTask{{
		ID:            t1,
		Seq:           2,
		Role:          "research",
		Status:        "completed",
		Title:         "核实重点歌曲的官方资料来源",
		ResultSummary: "已读取官方页面正文，确认 Ave Mujica《KiLLKiSS》2025-01-08 发行",
		Sources:       []string{"https://bang-dream.com/discographies/3919/"},
		Messages:      []Message{{Role: "tool", Content: strings.Repeat("header ", 120) + "Ave Mujica KiLLKiSS TVアニメオープニングテーマ"}},
	}}, "3919 KiLLKiSS 官方 页面 正文")
	if len(hits) != 1 || hits[0]["task_id"] != t1 {
		t.Fatalf("multi-term searchTraceTasks failed: %+v", hits)
	}
}

func TestPostgresMissionTotalBudgetCap(t *testing.T) {
	s, model, owner := accountingFixture(t)
	ctx := t.Context()
	if _, err := s.AdjustCredits(ctx, CreditAdjustment{ID: uuid.NewString(), UserID: owner, Credits: 1000, Note: "mission cap test"}); err != nil {
		t.Fatal(err)
	}
	prefix := "mission:" + uuid.NewString()
	budget := &CreditBudget{
		EnforceTotal: true,
		TotalPrefix:  prefix,
		TotalLimit:   30,
	}
	first := ReserveRequest{ID: uuid.NewString(), UserID: owner, ModelID: model.ID, InputTokens: 10, OutputTokens: 10, Purpose: prefix, CreditBudget: budget}
	if _, err := s.Reserve(ctx, first); err != nil {
		t.Fatalf("first reserve within mission budget failed: %v", err)
	}
	second := ReserveRequest{ID: uuid.NewString(), UserID: owner, ModelID: model.ID, InputTokens: 10, OutputTokens: 10, Purpose: prefix, CreditBudget: budget}
	if _, err := s.Reserve(ctx, second); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded when exceeding mission TotalLimit, got %v", err)
	}
}

func TestPostgresMissionPlannerWorkerLifecycle(t *testing.T) {
	f := assistantE2EFixture(t)
	work := f.seedWork()

	var mu sync.Mutex
	var wsID, commitID string
	editRound := 0

	f.s.CompletionClient = &http.Client{Transport: missionRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Messages []Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		sys := ""
		userPrompt := ""
		for _, m := range body.Messages {
			if m.Role == "system" {
				sys = m.Content
			}
			if m.Role == "user" && userPrompt == "" {
				userPrompt = m.Content
			}
		}
		last := body.Messages[len(body.Messages)-1]

		reply := func(content string, calls ...ToolCall) (*http.Response, error) {
			raw := missionMustJSON(t, map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "tool_calls": calls}}},
				"usage":   map[string]any{"prompt_tokens": 50, "completion_tokens": 20},
			})
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
		}
		call := func(id, name string, args map[string]any) ToolCall {
			var b []byte
			if args != nil {
				b, _ = json.Marshal(args)
			} else {
				b = []byte("{}")
			}
			var tc ToolCall
			tc.ID = id
			tc.Type = "function"
			tc.Function.Name = name
			tc.Function.Arguments = string(b)
			return tc
		}

		isPlanner := strings.Contains(sys, "规划器（Planner）")
		if isPlanner {
			if strings.Contains(userPrompt, "唤醒原因: init") {
				if last.Role == "user" {
					return reply("", call("plan-1", "mission_update_plan", map[string]any{
						"plan": []any{
							map[string]any{"id": "p1", "content": "盘点目标作品现状与外部资料", "status": "in_progress"},
							map[string]any{"id": "p2", "content": "通过工作副本提交修订 PR", "status": "pending"},
						},
						"add_tasks": []any{
							map[string]any{"ref": "recon1", "role": "recon", "title": "读取作品现状", "objective": "读取目标作品记录", "priority": 1, "anchor_entity_ids": []string{work.ID}},
							map[string]any{"ref": "res1", "role": "research", "title": "核对外部权威资料", "objective": "核对作品发行信息", "priority": 1, "anchor_entity_ids": []string{work.ID}},
							map[string]any{"ref": "edit1", "role": "edit", "title": "提交资料补全工作副本", "objective": "根据前置核实结果提交 PR", "priority": 2, "depends_on": []string{"recon1", "res1"}, "anchor_entity_ids": []string{work.ID}},
						},
					}))
				}
				if last.ToolCallID == "plan-1" {
					// Simulate a model prematurely calling mission_complete right after queueing tasks;
					// the engine must defer completion so newly added Worker tasks still execute.
					return reply("", call("premature-done", "mission_complete", map[string]any{
						"summary": "已派发首轮并行探查与后续编辑子任务。",
					}))
				}
				return reply("已完成首轮拆解并派发并行探查与核证任务。")
			}
			if strings.Contains(userPrompt, "唤醒原因: worker_discovery") {
				if last.Role == "user" {
					return reply("", call("trace-list", "list_worker_traces", nil))
				}
				return reply("已确认前置线索，继续执行待解锁的编辑子任务。")
			}
			if last.Role == "user" {
				return reply("", call("done-1", "mission_complete", map[string]any{
					"summary": "已完成目标范围盘点、来源核证并提交工作副本 PR。",
				}))
			}
			return reply("任务全部完成。")
		}

		if strings.Contains(userPrompt, "- role: recon") {
			if last.Role == "user" {
				return reply("", call("w-recon-get", "catalog_get", map[string]any{"id": work.ID}))
			}
			if last.ToolCallID == "w-recon-get" {
				return reply("", call("w-recon-rep", "worker_report", map[string]any{
					"summary":           "已读取目录中 Original Work 当前状态",
					"anchor_entity_ids": []string{work.ID},
				}))
			}
			return reply("探查完成")
		}
		if strings.Contains(userPrompt, "- role: research") {
			if last.Role == "user" {
				return reply("", call("w-res-rep", "worker_report", map[string]any{
					"summary":           "已核对官方资料：英文简介可补充 Verified Mission Body",
					"anchor_entity_ids": []string{work.ID},
					"discoveries":       []string{"关联单曲待后续核查"},
					"sources":           []string{"https://bang-dream.com/discographies"},
				}))
			}
			return reply("资料核证完成")
		}
		if strings.Contains(userPrompt, "- role: edit") {
			mu.Lock()
			step := editRound
			editRound++
			mu.Unlock()
			switch step {
			case 0:
				return reply("",
					call("w-edit-search-trace", "search_worker_traces", map[string]any{"query": "Verified Mission Body"}),
					call("w-edit-ws", "catalog_workspace_create", map[string]any{"name": "Mission Edit Work", "entity_ids": []string{work.ID}}),
				)
			case 1:
				var created struct {
					ID      string `json:"id"`
					Version int64  `json:"version"`
				}
				_ = json.Unmarshal([]byte(last.Content), &created)
				mu.Lock()
				wsID = created.ID
				mu.Unlock()
				return reply("", call("w-edit-stage", "catalog_workspace_stage", map[string]any{
					"workspace_id":     created.ID,
					"expected_version": created.Version,
					"operations": []any{map[string]any{
						"target": "entity",
						"action": "update",
						"id":     work.ID,
						"patch":  []any{map[string]any{"path": "/translations/en-US/body", "value": "Verified Mission Body"}},
					}},
				}))
			case 2:
				var staged struct {
					Workspace struct {
						Version int64 `json:"version"`
					} `json:"workspace"`
				}
				_ = json.Unmarshal([]byte(last.Content), &staged)
				cid := uuid.NewString()
				mu.Lock()
				commitID = cid
				curWS := wsID
				mu.Unlock()
				return reply("", call("w-edit-commit", "catalog_workspace_commit", map[string]any{
					"workspace_id":     curWS,
					"id":               cid,
					"expected_version": staged.Workspace.Version,
					"message":          "补充经核实的简介\n依据: https://bang-dream.com/discographies",
				}))
			case 3:
				mu.Lock()
				curWS := wsID
				mu.Unlock()
				ws, err := f.cat.GetWorkspace(context.Background(), curWS, f.user)
				if err != nil {
					return nil, err
				}
				return reply("", call("w-edit-push", "catalog_workspace_push", map[string]any{
					"workspace_id":     curWS,
					"expected_version": ws.Version,
					"expected_head":    commitID,
					"title":            "补充经核实的简介",
				}))
			case 4:
				return reply("", call("w-edit-rep", "worker_report", map[string]any{
					"summary":           "已通过工作副本提交变更并创建审核 PR",
					"anchor_entity_ids": []string{work.ID},
					"sources":           []string{"https://bang-dream.com/discographies"},
				}))
			default:
				return reply("编辑子任务完成")
			}
		}
		return reply("ok")
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	actorUser := f.user
	router.Use(func(c *gin.Context) {
		u := actorUser
		c.Set("catalog_user", &u)
		c.Next()
	})
	eng := f.s.EnsureMissionEngine(f.cat)
	t.Cleanup(eng.Wait)
	HTTP{Store: f.s, Catalog: f.cat, Missions: eng}.Register(router.Group("/api"))

	// A mission whose budget cannot cover even the first ceiling reservation must fail cleanly rather than complete with 0 tasks.
	tooSmallBody := missionMustJSON(t, map[string]any{
		"client_request_id": uuid.NewString(),
		"goal":              "预算过小测试",
		"budget_credits":    100,
	})
	sw0 := httptest.NewRecorder()
	req0 := httptest.NewRequest("POST", "/api/assistant/missions", bytes.NewReader(tooSmallBody))
	req0.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(sw0, req0)
	if sw0.Code != 200 {
		t.Fatalf("create small-budget mission status=%d body=%s", sw0.Code, sw0.Body.String())
	}
	var smallCreated MissionDetail
	_ = json.Unmarshal(sw0.Body.Bytes(), &smallCreated)
	var smallDetail MissionDetail
	for deadline0 := time.Now().Add(8 * time.Second); time.Now().Before(deadline0); {
		gw0 := httptest.NewRecorder()
		router.ServeHTTP(gw0, httptest.NewRequest("GET", "/api/assistant/missions/"+smallCreated.Mission.ID, nil))
		_ = json.Unmarshal(gw0.Body.Bytes(), &smallDetail)
		if smallDetail.Mission.Status == "completed" || smallDetail.Mission.Status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if smallDetail.Mission.Status != "failed" || smallDetail.Mission.Error != "assistant_budget_exceeded" {
		t.Fatalf("expected failed mission with assistant_budget_exceeded, got status=%s err=%s", smallDetail.Mission.Status, smallDetail.Mission.Error)
	}

	reqBody := missionMustJSON(t, map[string]any{
		"client_request_id":   uuid.NewString(),
		"title":               "完善测试作品资料",
		"goal":                "核实 Original Work 并通过工作副本提交简介更新",
		"budget_credits":      500000,
		"max_workers":         2,
		"max_tasks":           8,
		"max_planner_rounds":  6,
		"prefer_pull_request": true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/assistant/missions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create mission status=%d body=%s", w.Code, w.Body.String())
	}
	var created MissionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	missionID := created.Mission.ID

	deadline := time.Now().Add(15 * time.Second)
	var detail MissionDetail
	for time.Now().Before(deadline) {
		gw := httptest.NewRecorder()
		router.ServeHTTP(gw, httptest.NewRequest("GET", "/api/assistant/missions/"+missionID, nil))
		if gw.Code != 200 {
			t.Fatalf("get mission status=%d body=%s", gw.Code, gw.Body.String())
		}
		_ = json.Unmarshal(gw.Body.Bytes(), &detail)
		if detail.Mission.Status == "completed" || detail.Mission.Status == "failed" {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if detail.Mission.Status != "completed" {
		t.Fatalf("mission did not complete cleanly: status=%s err=%s runs=%+v tasks=%+v", detail.Mission.Status, detail.Mission.Error, detail.PlannerRuns, detail.Tasks)
	}
	if len(detail.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %+v", len(detail.Tasks), detail.Tasks)
	}
	editTask := detail.Tasks[2]
	if editTask.Status != "completed" || editTask.WorkspaceID == "" || editTask.PullRequestID == "" || editTask.Merged {
		t.Fatalf("expected completed edit task with unmerged PR receipt: %+v", editTask)
	}
	if detail.Mission.SpentCredits <= 0 || detail.Mission.SpentCredits > detail.Mission.BudgetCredits {
		t.Fatalf("unexpected spent credits: spent=%d budget=%d", detail.Mission.SpentCredits, detail.Mission.BudgetCredits)
	}

	tw := httptest.NewRecorder()
	router.ServeHTTP(tw, httptest.NewRequest("GET", "/api/assistant/missions/"+missionID+"/tasks/"+editTask.ID, nil))
	if tw.Code != 200 {
		t.Fatalf("get mission task trace failed: %d %s", tw.Code, tw.Body.String())
	}
	var fullTask MissionTask
	_ = json.Unmarshal(tw.Body.Bytes(), &fullTask)
	if len(fullTask.Messages) == 0 {
		t.Fatalf("expected persisted worker messages in task trace")
	}

	sw := httptest.NewRecorder()
	router.ServeHTTP(sw, httptest.NewRequest("POST", "/api/assistant/missions/"+missionID+"/stop", nil))
	if sw.Code != 200 {
		t.Fatalf("stop mission failed: %d %s", sw.Code, sw.Body.String())
	}
	dw := httptest.NewRecorder()
	router.ServeHTTP(dw, httptest.NewRequest("DELETE", "/api/assistant/missions/"+missionID, nil))
	if dw.Code != 204 {
		t.Fatalf("delete mission failed: %d %s", dw.Code, dw.Body.String())
	}
}

type missionRoundTripFunc func(*http.Request) (*http.Response, error)

func (f missionRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func missionMustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
