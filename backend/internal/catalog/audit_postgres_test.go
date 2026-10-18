package catalog

// 真库端到端（契约 §6.2）：每个被审计动作恰好一行、整行文本零敏感命中、失败路径
// result=failure + error_code、X-Request-Id 透传、豁免路由与 GET 一行不写。
// 无 MF_V2_TEST_DSN 时 testutil.Database 自动跳过。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

// auditForbiddenInRow 是整行文本零命中的敏感正则（契约 §4/§6.2）。键名本身会出现在 changes
// 的 JSON 文本里，所以这条断言依赖"调用方不把敏感键名写进 changes"——目录侧的 changes 键
// 全是 kind/status/title/slug 这类业务字段，脱敏负责的是值。
var auditForbiddenInRow = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password`),
	regexp.MustCompile(`(?i)token`),
	regexp.MustCompile(`(?i)secret`),
	regexp.MustCompile(`mfp_|mf_pat_`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`),
}

type auditFixture struct {
	t    *testing.T
	s    *Store
	db   *sql.DB
	user *User
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := testutil.Database(t)
	s := &Store{DB: db}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Audit = auditlog.NewRecorder(db, auditlog.ServiceName)
	u := fixtureUser("admin")
	return &auditFixture{t: t, s: s, db: db, user: &u}
}

// engine 只挂目录路由并注入身份：X-Request-Id 不经过 main.go 的全局中间件，
// 正好用来验证审计中间件自己生成/回写的行为（生产里那个中间件会先写同名头，被读到就复用）。
func (f *auditFixture) engine(identity *User) *gin.Engine {
	r := gin.New()
	if identity != nil {
		r.Use(func(c *gin.Context) { c.Set("catalog_user", identity); c.Next() })
	}
	HTTP{Store: f.s}.Register(r)
	return r
}

func (f *auditFixture) do(engine http.Handler, method, path, body, requestID string) *httptest.ResponseRecorder {
	f.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set(auditlog.RequestIDHeader, requestID)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

type auditRow struct {
	service, action, targetType, targetID, changes, result, errorCode string
	username, credential, method, route, requestID, agent             string
	status                                                            int
}

func (f *auditFixture) queryRows(where string, args ...any) []auditRow {
	f.t.Helper()
	rows, err := f.db.QueryContext(context.Background(),
		"SELECT service, action, target_type, target_id, changes::text, coalesce(actor_username,''), credential_type,"+
			" request_method, route, http_status, request_id, result, error_code, actor_user_agent"+
			" FROM audit.audit_log "+where, args...)
	if err != nil {
		f.t.Fatalf("查审计行: %v", err)
	}
	defer rows.Close()
	out := []auditRow{}
	for rows.Next() {
		var r auditRow
		if err = rows.Scan(&r.service, &r.action, &r.targetType, &r.targetID, &r.changes, &r.username, &r.credential,
			&r.method, &r.route, &r.status, &r.requestID, &r.result, &r.errorCode, &r.agent); err != nil {
			f.t.Fatalf("扫审计行: %v", err)
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

// waitRow 等该 request_id 的审计行出现（Recorder 是异步落库的），并要求恰好一行。
func (f *auditFixture) waitRow(requestID string) auditRow {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows := f.queryRows("WHERE request_id=$1", requestID)
		switch {
		case len(rows) == 1:
			return rows[0]
		case len(rows) > 1:
			f.t.Fatalf("request_id=%s 写了 %d 行（每个被审计动作必须恰好一行）", requestID, len(rows))
		case time.Now().After(deadline):
			f.t.Fatalf("request_id=%s 没有审计行（超时）", requestID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *auditFixture) expect(status int, w *httptest.ResponseRecorder, what string) {
	f.t.Helper()
	if w.Code != status {
		f.t.Fatalf("%s: 状态 %d，响应体 %s", what, w.Code, w.Body.String())
	}
}

// changeWorkspace executes each public editing step and verifies its actual
// audit action. A batch has one route audit per request, regardless of how many
// entities or relations it materializes when pushed.
func (f *auditFixture) changeWorkspace(engine http.Handler, workspace Workspace, requestPrefix string, operations ...WorkspaceOperation) (Workspace, map[string]string, CollaborationCommit) {
	f.t.Helper()
	check := func(requestID, action, targetType, targetID, method, route string) auditRow {
		f.t.Helper()
		row := f.waitRow(requestID)
		if row.action != action || row.targetType != targetType || row.targetID != targetID || row.result != "success" || row.status != http.StatusOK || row.service != auditlog.ServiceName {
			f.t.Fatalf("working-copy audit: %#v", row)
		}
		if row.username != f.user.Username || row.credential != "session" || row.method != method || row.route != route || row.requestID != requestID {
			f.t.Fatalf("working-copy audit identity/request: %#v", row)
		}
		return row
	}
	if workspace.ID == "" {
		w := f.do(engine, http.MethodPost, "/api/catalog/workspaces", `{"name":"audit working copy"}`, requestPrefix+"-workspace")
		f.expect(http.StatusOK, w, "创建工作副本")
		if err := json.Unmarshal(w.Body.Bytes(), &workspace); err != nil {
			f.t.Fatal(err)
		}
		check(requestPrefix+"-workspace", "workspace.created", "workspace", workspace.ID, http.MethodPost, "/api/catalog/workspaces")
	}
	path := "/api/catalog/workspaces/" + workspace.ID
	in := WorkspaceChanges{ExpectedVersion: workspace.Version, Operations: operations}
	w := f.do(engine, http.MethodPut, path+"/changes", encode(in), requestPrefix+"-changes")
	f.expect(http.StatusOK, w, "保存副本变更")
	var changes WorkspaceChangesResult
	if err := json.Unmarshal(w.Body.Bytes(), &changes); err != nil {
		f.t.Fatal(err)
	}
	row := check(requestPrefix+"-changes", "workspace.changes_saved", "workspace", workspace.ID, http.MethodPut, "/api/catalog/workspaces/:id/changes")
	var detail map[string]any
	if err := json.Unmarshal([]byte(row.changes), &detail); err != nil || detail["operation_count"] != float64(len(operations)) {
		f.t.Fatalf("stage audit operation count: %s %v", row.changes, err)
	}
	commitInput := WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: changes.Workspace.Version, Message: "audit committed change"}
	w = f.do(engine, http.MethodPost, path+"/commits", encode(commitInput), requestPrefix+"-commit")
	f.expect(http.StatusOK, w, "提交副本变更")
	var commit CollaborationCommit
	if err := json.Unmarshal(w.Body.Bytes(), &commit); err != nil {
		f.t.Fatal(err)
	}
	check(requestPrefix+"-commit", "workspace.committed", "commit", commit.ID, http.MethodPost, "/api/catalog/workspaces/:id/commits")
	workspace, err := f.s.GetWorkspace(context.Background(), workspace.ID, *f.user)
	if err != nil {
		f.t.Fatal(err)
	}
	w = f.do(engine, http.MethodPost, path+"/push", encode(WorkspacePush{ExpectedVersion: workspace.Version, ExpectedHead: *workspace.HeadCommitID}), requestPrefix+"-push")
	f.expect(http.StatusOK, w, "推送并合入副本")
	var pushed WorkspacePushResult
	if err := json.Unmarshal(w.Body.Bytes(), &pushed); err != nil {
		f.t.Fatal(err)
	}
	if !pushed.Applied || pushed.Workspace == nil {
		f.t.Fatalf("bypass user did not merge: %s", w.Body.String())
	}
	check(requestPrefix+"-push", "workspace.pushed", "workspace", workspace.ID, http.MethodPost, "/api/catalog/workspaces/:id/push")
	return *pushed.Workspace, changes.Refs, commit
}

func TestPostgresAuditTrailPerAction(t *testing.T) {
	f := newAuditFixture(t)
	engine := f.engine(f.user)
	ctx := context.Background()

	// Structured changes are audited as editing actions; private document text
	// stays in commit history instead of being copied into the account audit log.
	email := "jane.doe@example.com"
	workspace, refs, _ := f.changeWorkspace(engine, Workspace{}, "rid-entity-create", WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(encode(Entity{Kind: "work", Title: "审计用例 联系 " + email}))})
	created, err := f.s.Get(ctx, refs["work"], f.user)
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, update := f.changeWorkspace(engine, workspace, "rid-entity-update", WorkspaceOperation{Target: "entity", Action: "update", ID: created.ID, Patch: []CommitPatch{patchValue("/title", "审计用例 改过")}})
	created, err = f.s.Get(ctx, created.ID, f.user)
	if err != nil || created.Title != "审计用例 改过" || created.Version != 2 {
		t.Fatalf("committed fact: %+v %v", created, err)
	}
	history, err := f.s.Revisions(ctx, created.ID, f.user)
	if err != nil || len(history) != 2 || history[0]["commit_id"] != update.ID {
		t.Fatalf("materialized history provenance: %+v %v", history, err)
	}
	for _, row := range f.queryRows("") {
		if strings.Contains(row.changes, email) || strings.Contains(row.changes, "审计用例") {
			t.Fatalf("working-copy content leaked to account audit: %s", row.changes)
		}
	}

	// Lifecycle actions remain separate and leave their terminal state in audit.
	lifecycleBody := fmt.Sprintf(`{"expected_version":%d,"edit_note":"audit e2e delete"}`, created.Version)
	w := f.do(engine, http.MethodPost, "/api/catalog/entities/"+created.ID+"/lifecycle", lifecycleBody, "rid-entity-lifecycle")
	f.expect(200, w, "生命周期删除")
	row := f.waitRow("rid-entity-lifecycle")
	if row.action != "entity.lifecycle_changed" || row.targetID != created.ID || !strings.Contains(row.changes, `"after": "deleted"`) {
		t.Fatalf("生命周期审计行: %#v", row)
	}

	// One batch creates all relation endpoints; another batch creates the edge.
	workspace, refs, _ = f.changeWorkspace(engine, workspace, "rid-endpoints",
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "actor", Document: json.RawMessage(`{"kind":"agent","title":"演职人员"}`)},
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "group", Document: json.RawMessage(`{"kind":"agent","title":"所属团体"}`)})
	actorID, groupID := refs["actor"], refs["group"]
	workspace, refs, _ = f.changeWorkspace(engine, workspace, "rid-relation-create", WorkspaceOperation{Target: "relation", Action: "create", Ref: "edge", Document: json.RawMessage(encode(Relation{Type: "member_of", SourceID: actorID, TargetID: groupID, Attributes: map[string]any{"language": "zh-CN"}}))})
	relationID := refs["edge"]
	workspace, _, _ = f.changeWorkspace(engine, workspace, "rid-relation-update", WorkspaceOperation{Target: "relation", Action: "update", ID: relationID, Patch: []CommitPatch{patchValue("/attributes/language", "en-US")}})
	relation, err := relationByID(ctx, f.db, relationID)
	if err != nil || relation.Version != 2 || relation.Attributes["language"] != "en-US" {
		t.Fatalf("relation commit: %+v %v", relation, err)
	}
	workspace, _, _ = f.changeWorkspace(engine, workspace, "rid-relation-delete", WorkspaceOperation{Target: "relation", Action: "delete", ID: relationID})
	if _, err := relationByID(ctx, f.db, relationID); err == nil {
		t.Fatal("relation deletion did not merge")
	}

	// 6) 首页偏好（自服务写，非管理员）
	prefsBody := "{\"order\":[\"system-top\"],\"hidden\":[],\"sections\":[{\"slug\":\"audit-e2e-shelf\",\"names\":{\"zh-CN\":\"审计分区\"},\"query\":{},\"sort\":\"\",\"icon\":\"\"}]}"
	w = f.do(engine, http.MethodPut, "/api/catalog/me/home-preferences", prefsBody, "rid-preferences")
	f.expect(200, w, "写首页偏好")
	row = f.waitRow("rid-preferences")
	if row.action != "preference.home_updated" || row.targetType != "preference" || row.targetID != f.user.ID {
		t.Fatalf("偏好审计行: %#v", row)
	}
	if !strings.Contains(row.changes, "audit-e2e-shelf") {
		t.Fatalf("偏好摘要要能看出改了哪个分区: %s", row.changes)
	}

	// 7) 货架 CRUD
	shelf := map[string]any{"slug": "audit-e2e-shelf", "names": map[string]string{"zh-CN": "审计货架", "zh-TW": "審計貨架", "en-US": "Audit shelf", "ja-JP": "監査シェルフ"}, "query": map[string]any{}, "sort": "", "icon": "", "enabled": false, "sort_order": 90}
	shelfBody, _ := json.Marshal(shelf)
	w = f.do(engine, http.MethodPost, "/api/admin/shelves", string(shelfBody), "rid-shelf-create")
	f.expect(200, w, "建货架")
	var shelfCreated struct {
		Data Shelf `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &shelfCreated); err != nil {
		t.Fatal(err)
	}
	row = f.waitRow("rid-shelf-create")
	if row.action != "shelf.created" || row.targetID != fmt.Sprint(shelfCreated.Data.ID) {
		t.Fatalf("建货架审计行: %#v", row)
	}
	shelf["enabled"] = true
	shelfBody, _ = json.Marshal(shelf)
	w = f.do(engine, http.MethodPut, "/api/admin/shelves/"+fmt.Sprint(shelfCreated.Data.ID), string(shelfBody), "rid-shelf-update")
	f.expect(200, w, "改货架")
	row = f.waitRow("rid-shelf-update")
	if row.action != "shelf.updated" || !strings.Contains(row.changes, "\"before\": \"false\"") {
		t.Fatalf("改货架审计行（要 before）: %#v", row)
	}
	w = f.do(engine, http.MethodDelete, "/api/admin/shelves/"+fmt.Sprint(shelfCreated.Data.ID), "", "rid-shelf-delete")
	f.expect(200, w, "删货架")
	row = f.waitRow("rid-shelf-delete")
	if row.action != "shelf.deleted" || !strings.Contains(row.changes, "\"before\"") {
		t.Fatalf("删货架审计行（要 before）: %#v", row)
	}

	// 8) 外部数据库 CRUD
	extDB := map[string]any{"code": "audit_e2e", "names": map[string]string{"zh-CN": "审计来源", "zh-TW": "審計來源", "en-US": "Audit source", "ja-JP": "監査ソース"}, "category": "all", "url_pattern": "https://example.com/{id}", "is_enabled": false, "sort_order": 99}
	extBody, _ := json.Marshal(extDB)
	w = f.do(engine, http.MethodPost, "/api/admin/external-databases", string(extBody), "rid-extdb-create")
	f.expect(200, w, "建外部来源")
	row = f.waitRow("rid-extdb-create")
	if row.action != "external_database.created" || row.targetID != "audit_e2e" {
		t.Fatalf("建外部来源审计行: %#v", row)
	}
	extDB["category"] = "work"
	extBody, _ = json.Marshal(extDB)
	w = f.do(engine, http.MethodPut, "/api/admin/external-databases/audit_e2e", string(extBody), "rid-extdb-update")
	f.expect(200, w, "改外部来源")
	row = f.waitRow("rid-extdb-update")
	if row.action != "external_database.updated" || !strings.Contains(row.changes, "\"before\": \"all\"") {
		t.Fatalf("改外部来源审计行: %#v", row)
	}
	w = f.do(engine, http.MethodDelete, "/api/admin/external-databases/audit_e2e", "", "rid-extdb-delete")
	f.expect(200, w, "删外部来源")
	row = f.waitRow("rid-extdb-delete")
	if row.action != "external_database.deleted" || !strings.Contains(row.changes, "\"before\"") {
		t.Fatalf("删外部来源审计行: %#v", row)
	}

	// 9) 定义：单份配置更新
	current, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defBody, err := json.Marshal(map[string]any{
		"document":      current.Document,
		"expected_etag": current.ETag,
		"edit_note":     "audit e2e update",
		"sources":       []map[string]string{{"kind": "url", "citation": "audit e2e", "url": "https://example.org/fixture"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w = f.do(engine, http.MethodPut, "/api/admin/catalog-definitions", string(defBody), "rid-def-update")
	f.expect(200, w, "更新定义")
	row = f.waitRow("rid-def-update")
	if row.action != "definition.updated" || row.targetID != "definitions" || !strings.Contains(row.changes, "document_counts") {
		t.Fatalf("更新定义审计行: %#v", row)
	}

	// A stale working-copy CAS fails without changing facts and records the same
	// stable error code returned to the caller.
	conflictBody := encode(WorkspaceChanges{ExpectedVersion: 999, Operations: []WorkspaceOperation{{Target: "entity", Action: "update", ID: actorID, Patch: []CommitPatch{patchValue("/title", "stale title")}}}})
	w = f.do(engine, http.MethodPut, "/api/catalog/workspaces/"+workspace.ID+"/changes", conflictBody, "rid-conflict")
	f.expect(409, w, "保存过时副本")
	row = f.waitRow("rid-conflict")
	if row.action != "workspace.changes_saved" || row.result != "failure" || row.errorCode != "workspace_version_conflict" || row.status != 409 || row.targetID != workspace.ID {
		t.Fatalf("失败路径审计行: %#v", row)
	}
	if !strings.Contains(w.Body.String(), row.errorCode) {
		t.Fatalf("响应体错误码要与审计 error_code 同值: %s", w.Body.String())
	}

	// 12) 未登录的写请求同样留痕（401 + anonymous）
	anonEngine := f.engine(nil)
	w = f.do(anonEngine, http.MethodPut, "/api/catalog/me/home-preferences", prefsBody, "rid-anon")
	f.expect(401, w, "匿名写偏好")
	row = f.waitRow("rid-anon")
	if row.action != "preference.home_updated" || row.result != "failure" || row.errorCode != "authentication_required" || row.status != 401 {
		t.Fatalf("匿名写审计行: %#v", row)
	}
	if row.username != "" || row.credential != "anonymous" {
		t.Fatalf("匿名写不该有操作者: %#v", row)
	}

	// 13) 豁免路由（POST 但零写入）与 GET：一行都不写
	w = f.do(engine, http.MethodPost, "/api/catalog/expressions/details", "{\"ids\":[\""+actorID+"\"]}", "rid-exempt")
	f.expect(200, w, "批量读表达")
	w = f.do(engine, http.MethodGet, "/api/catalog/entities/"+actorID, "", "rid-get")
	f.expect(200, w, "读实体")
	for _, rid := range []string{"rid-exempt", "rid-get"} {
		if rows := f.queryRows("WHERE request_id=$1", rid); len(rows) != 0 {
			t.Fatalf("豁免路由与 GET 不该写审计: %v", rows)
		}
	}

	// 13.5) X-Request-Id 缺省时由审计中间件生成 uuid 并回写响应头（契约 §1）
	w = f.do(engine, http.MethodPut, "/api/catalog/me/home-preferences", prefsBody, "")
	f.expect(200, w, "写偏好（请求不带 X-Request-Id）")
	generated := w.Header().Get(auditlog.RequestIDHeader)
	if generated == "" {
		t.Fatal("缺省 request id 必须回写同名响应头")
	}
	row = f.waitRow(generated)
	if row.requestID != generated || row.action != "preference.home_updated" {
		t.Fatalf("生成的 request id 必须与审计行一致: %#v", row)
	}

	for _, r := range f.queryRows("") {
		for _, forbidden := range auditForbiddenInRow {
			if forbidden.MatchString(fmt.Sprintf("%+v", r)) {
				t.Fatalf("sensitive audit row matched %s: %#v", forbidden.String(), r)
			}
		}
	}

}
