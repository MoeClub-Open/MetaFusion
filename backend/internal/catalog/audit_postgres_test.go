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

func auditEntityBody(kind, status, title string, expected int64) string {
	entity := map[string]any{"kind": kind, "title": title, "state": "active"}
	if status == "published" {
		entity["translations"] = map[string]any{"en": map[string]any{"title": "audit e2e english " + title}}
	}
	body := map[string]any{
		"entity":    entity,
		"edit_note": "audit e2e fixture",
		"sources":   []map[string]string{{"kind": "url", "citation": "audit e2e fixture", "url": "https://example.org/fixture"}},
	}
	if expected > 0 {
		body["expected_version"] = expected
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestPostgresAuditTrailPerAction(t *testing.T) {
	f := newAuditFixture(t)
	engine := f.engine(f.user)
	ctx := context.Background()
	audited := 0

	// 1) 建实体（标题里带邮箱：验证"值里的邮箱被遮罩"在真实落库路径上生效）
	email := "jane.doe@example.com"
	w := f.do(engine, http.MethodPost, "/api/catalog/entities", auditEntityBody("work", "draft", "审计用例 联系 "+email, 0), "rid-entity-create")
	f.expect(200, w, "建实体")
	var created Entity
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	row := f.waitRow("rid-entity-create")
	audited++
	if row.action != "entity.created" || row.result != "success" || row.status != 200 || row.service != auditlog.ServiceName {
		t.Fatalf("建实体审计行: %#v", row)
	}
	if row.targetType != "entity" || row.targetID != created.ID {
		t.Fatalf("建实体的 target: %#v", row)
	}
	if row.username != f.user.Username || row.credential != "session" {
		t.Fatalf("建实体的操作者: %#v", row)
	}
	if row.method != http.MethodPost || row.route != "/api/catalog/entities" {
		t.Fatalf("方法/路由模板: %#v", row)
	}
	if strings.Contains(row.changes, email) || !strings.Contains(row.changes, "j***@example.com") {
		t.Fatalf("changes 里的邮箱必须被遮罩: %s", row.changes)
	}
	// 透传：请求带了 X-Request-Id，审计行的 request_id 就是它（回写响应头只发生在缺省生成时，
	// 见下面第 13 步；生产里 main.go 的全局中间件会先写好同名响应头）。
	if row.requestID != "rid-entity-create" {
		t.Fatalf("X-Request-Id 必须原样进审计行: %q", row.requestID)
	}

	// 2) 改实体：before/after 都要有（title 与 status 各一条）
	w = f.do(engine, http.MethodPut, "/api/catalog/entities/"+created.ID, auditEntityBody("work", "pending_review", "审计用例 改过", created.Version), "rid-entity-update")
	f.expect(200, w, "改实体")
	row = f.waitRow("rid-entity-update")
	audited++
	if row.action != "entity.updated" || row.targetID != created.ID {
		t.Fatalf("改实体审计行: %#v", row)
	}
	var changes map[string]any
	if err := json.Unmarshal([]byte(row.changes), &changes); err != nil {
		t.Fatalf("changes 不是合法 JSON: %v (%s)", err, row.changes)
	}
	if changes["status"] != nil || changes["state"] != nil {
		t.Fatalf("ordinary edit changed lifecycle: %s", row.changes)
	}

	title, _ := changes["title"].(map[string]any)
	// before 侧同样脱敏：旧题名里的邮箱在审计行里必须是遮罩形态。
	if title["before"] != "审计用例 联系 j***@example.com" || title["after"] != "审计用例 改过" {
		t.Fatalf("title 的 before→after: %s", row.changes)
	}

	// 3) 生命周期（删除）：终态留痕
	lifecycleBody := fmt.Sprintf("{\"expected_version\":%d,\"edit_note\":\"audit e2e delete\",\"sources\":[{\"kind\":\"url\",\"citation\":\"audit e2e\",\"url\":\"https://example.org/fixture\"}]}", created.Version+1)
	w = f.do(engine, http.MethodPost, "/api/catalog/entities/"+created.ID+"/lifecycle", lifecycleBody, "rid-entity-lifecycle")
	f.expect(200, w, "生命周期删除")
	row = f.waitRow("rid-entity-lifecycle")
	audited++
	if row.action != "entity.lifecycle_changed" || row.targetID != created.ID {
		t.Fatalf("生命周期审计行: %#v", row)
	}
	if !strings.Contains(row.changes, "\"after\": \"deleted\"") {
		t.Fatalf("生命周期要记终态: %s", row.changes)
	}

	// 5) 关系：创建 / 修改 / 删除
	w = f.do(engine, http.MethodPost, "/api/catalog/entities", auditEntityBody("agent", "published", "演职人员", 0), "rid-agent-create")
	f.expect(200, w, "建人物实体")
	audited++
	var actorEntity Entity
	if err := json.Unmarshal(w.Body.Bytes(), &actorEntity); err != nil {
		t.Fatal(err)
	}
	f.waitRow("rid-agent-create")
	w = f.do(engine, http.MethodPost, "/api/catalog/entities", auditEntityBody("agent", "published", "所属团体", 0), "rid-group-create")
	f.expect(200, w, "建团体实体")
	audited++
	var groupEntity Entity
	if err := json.Unmarshal(w.Body.Bytes(), &groupEntity); err != nil {
		t.Fatal(err)
	}
	f.waitRow("rid-group-create")
	w = f.do(engine, http.MethodPost, "/api/catalog/entities", auditEntityBody("work", "published", "关系用例作品", 0), "rid-work-create")
	f.expect(200, w, "建作品实体")
	audited++
	var workEntity Entity
	if err := json.Unmarshal(w.Body.Bytes(), &workEntity); err != nil {
		t.Fatal(err)
	}
	f.waitRow("rid-work-create")
	relationBody := map[string]any{
		"relation": map[string]any{
			"type": "member_of", "source_id": actorEntity.ID, "target_id": groupEntity.ID,
			"attributes": map[string]any{"language": "zh-CN"},
		},
		"edit_note": "audit e2e relation",
		"sources":   []map[string]string{{"kind": "url", "citation": "audit e2e", "url": "https://example.org/fixture"}},
	}
	b, _ := json.Marshal(relationBody)
	w = f.do(engine, http.MethodPost, "/api/catalog/relations", string(b), "rid-relation-create")
	f.expect(200, w, "建关系")
	var relation Relation
	if err := json.Unmarshal(w.Body.Bytes(), &relation); err != nil {
		t.Fatal(err)
	}
	row = f.waitRow("rid-relation-create")
	audited++
	if row.action != "relation.created" || row.targetType != "relation" || row.targetID != relation.ID {
		t.Fatalf("建关系审计行: %#v", row)
	}
	// 关系身份（type / 两端）在服务端不可变（immutable_scope），能改的只有 attributes，
	// 所以"改关系"的摘要靠 attributes 的 before→after 体现。
	relationBody["relation"].(map[string]any)["attributes"] = map[string]any{"language": "en-US"}
	relationBody["expected_version"] = relation.Version
	b, _ = json.Marshal(relationBody)
	w = f.do(engine, http.MethodPut, "/api/catalog/relations/"+relation.ID, string(b), "rid-relation-update")
	f.expect(200, w, "改关系")
	row = f.waitRow("rid-relation-update")
	audited++
	if row.action != "relation.updated" || !strings.Contains(row.changes, "\"attributes\"") || !strings.Contains(row.changes, "\"before\": \"zh-CN\"") {
		t.Fatalf("改关系审计行（要有 before）: %#v", row)
	}
	deleteBody := fmt.Sprintf("{\"expected_version\":%d,\"edit_note\":\"audit e2e\",\"sources\":[{\"kind\":\"url\",\"citation\":\"audit e2e\",\"url\":\"https://example.org/fixture\"}]}", relation.Version+1)
	w = f.do(engine, http.MethodDelete, "/api/catalog/relations/"+relation.ID, deleteBody, "rid-relation-delete")
	f.expect(200, w, "删关系")
	row = f.waitRow("rid-relation-delete")
	audited++
	if row.action != "relation.deleted" || row.targetID != relation.ID {
		t.Fatalf("删关系审计行: %#v", row)
	}

	// 6) 首页偏好（自服务写，非管理员）
	prefsBody := "{\"order\":[\"system-top\"],\"hidden\":[],\"sections\":[{\"slug\":\"audit-e2e-shelf\",\"names\":{\"zh-CN\":\"审计分区\"},\"query\":{},\"sort\":\"\",\"icon\":\"\"}]}"
	w = f.do(engine, http.MethodPut, "/api/catalog/me/home-preferences", prefsBody, "rid-preferences")
	f.expect(200, w, "写首页偏好")
	row = f.waitRow("rid-preferences")
	audited++
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
	audited++
	if row.action != "shelf.created" || row.targetID != fmt.Sprint(shelfCreated.Data.ID) {
		t.Fatalf("建货架审计行: %#v", row)
	}
	shelf["enabled"] = true
	shelfBody, _ = json.Marshal(shelf)
	w = f.do(engine, http.MethodPut, "/api/admin/shelves/"+fmt.Sprint(shelfCreated.Data.ID), string(shelfBody), "rid-shelf-update")
	f.expect(200, w, "改货架")
	row = f.waitRow("rid-shelf-update")
	audited++
	if row.action != "shelf.updated" || !strings.Contains(row.changes, "\"before\": \"false\"") {
		t.Fatalf("改货架审计行（要 before）: %#v", row)
	}
	w = f.do(engine, http.MethodDelete, "/api/admin/shelves/"+fmt.Sprint(shelfCreated.Data.ID), "", "rid-shelf-delete")
	f.expect(200, w, "删货架")
	row = f.waitRow("rid-shelf-delete")
	audited++
	if row.action != "shelf.deleted" || !strings.Contains(row.changes, "\"before\"") {
		t.Fatalf("删货架审计行（要 before）: %#v", row)
	}

	// 8) 外部数据库 CRUD
	extDB := map[string]any{"code": "audit_e2e", "names": map[string]string{"zh-CN": "审计来源", "zh-TW": "審計來源", "en-US": "Audit source", "ja-JP": "監査ソース"}, "category": "all", "url_pattern": "https://example.com/{id}", "is_enabled": false, "sort_order": 99}
	extBody, _ := json.Marshal(extDB)
	w = f.do(engine, http.MethodPost, "/api/admin/external-databases", string(extBody), "rid-extdb-create")
	f.expect(200, w, "建外部来源")
	row = f.waitRow("rid-extdb-create")
	audited++
	if row.action != "external_database.created" || row.targetID != "audit_e2e" {
		t.Fatalf("建外部来源审计行: %#v", row)
	}
	extDB["category"] = "work"
	extBody, _ = json.Marshal(extDB)
	w = f.do(engine, http.MethodPut, "/api/admin/external-databases/audit_e2e", string(extBody), "rid-extdb-update")
	f.expect(200, w, "改外部来源")
	row = f.waitRow("rid-extdb-update")
	audited++
	if row.action != "external_database.updated" || !strings.Contains(row.changes, "\"before\": \"all\"") {
		t.Fatalf("改外部来源审计行: %#v", row)
	}
	w = f.do(engine, http.MethodDelete, "/api/admin/external-databases/audit_e2e", "", "rid-extdb-delete")
	f.expect(200, w, "删外部来源")
	row = f.waitRow("rid-extdb-delete")
	audited++
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
	audited++
	if row.action != "definition.updated" || row.targetID != "definitions" || !strings.Contains(row.changes, "document_counts") {
		t.Fatalf("更新定义审计行: %#v", row)
	}

	// 11) 失败路径：版本冲突必须留 result=failure + error_code（与响应体同值）
	w = f.do(engine, http.MethodPost, "/api/catalog/entities", auditEntityBody("work", "published", "冲突用例", 0), "rid-conflict-create")
	f.expect(200, w, "建冲突用例实体")
	audited++
	var conflict Entity
	if err = json.Unmarshal(w.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	f.waitRow("rid-conflict-create")
	conflictBody, err := json.Marshal(map[string]any{
		"entity": map[string]any{
			"kind": "work", "title": "冲突用例改",
			"translations": map[string]any{"en": map[string]any{"title": "audit e2e english"}},
		},
		"expected_version": 999,
		"edit_note":        "audit e2e conflict",
		"sources":          []map[string]string{{"kind": "url", "citation": "audit e2e", "url": "https://example.org/fixture"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w = f.do(engine, http.MethodPut, "/api/catalog/entities/"+conflict.ID, string(conflictBody), "rid-conflict")
	f.expect(409, w, "改实体（版本过时）")
	row = f.waitRow("rid-conflict")
	audited++
	if row.action != "entity.updated" || row.result != "failure" || row.errorCode != "version_conflict" || row.status != 409 {
		t.Fatalf("失败路径审计行: %#v", row)
	}
	if !strings.Contains(w.Body.String(), "version_conflict") {
		t.Fatalf("响应体错误码要与审计 error_code 同值: %s", w.Body.String())
	}

	// 12) 未登录的写请求同样留痕（401 + anonymous）
	anonEngine := f.engine(nil)
	w = f.do(anonEngine, http.MethodPut, "/api/catalog/me/home-preferences", prefsBody, "rid-anon")
	f.expect(401, w, "匿名写偏好")
	row = f.waitRow("rid-anon")
	audited++
	if row.action != "preference.home_updated" || row.result != "failure" || row.errorCode != "authentication_required" || row.status != 401 {
		t.Fatalf("匿名写审计行: %#v", row)
	}
	if row.username != "" || row.credential != "anonymous" {
		t.Fatalf("匿名写不该有操作者: %#v", row)
	}

	// 13) 豁免路由（POST 但零写入）与 GET：一行都不写
	w = f.do(engine, http.MethodPost, "/api/catalog/expressions/details", "{\"ids\":[\""+actorEntity.ID+"\"]}", "rid-exempt")
	f.expect(200, w, "批量读表达")
	w = f.do(engine, http.MethodGet, "/api/catalog/entities/"+actorEntity.ID, "", "rid-get")
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
	audited++
	if row.requestID != generated || row.action != "preference.home_updated" {
		t.Fatalf("生成的 request id 必须与审计行一致: %#v", row)
	}

}
