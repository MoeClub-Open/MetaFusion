package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const unpublishPath = "/api/catalog/entities/00000000-0000-0000-0000-000000000001/unpublish"

// 路由闸门与请求体契约（不需要数据库）：未登录 401、持别的目录码 403、持 catalog.lifecycle.manage
// 者才进处理器。探针用非法载荷：进处理器后先解析 body，带 target_id 或不合法 JSON 都是 400
// invalid_payload——既证明闸门认的是权限码，也钉住"下架不接受 target_id"（拒绝而不是静默忽略）。
func TestUnpublishRouteGateAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lifecycle := &User{ID: "u-life", Permissions: []string{PermissionLifecycleManage}}
	editor := &User{ID: "u-edit", Permissions: []string{PermissionEntityEdit}}
	validBody := `{"expected_version":1,"edit_note":"n","sources":[{"kind":"url","citation":"c","url":"https://example.org/fixture"}]}`
	for _, tc := range []struct {
		name     string
		user     *User
		body     string
		wantCode int
		want     string
	}{
		{"anonymous", nil, validBody, http.StatusUnauthorized, "authentication_required"},
		{"other catalog code", editor, validBody, http.StatusForbidden, "forbidden"},
		{"target_id is not part of the contract", lifecycle, `{"expected_version":1,"edit_note":"n","target_id":"00000000-0000-0000-0000-000000000002","sources":[{"kind":"url","citation":"c","url":"https://example.org/fixture"}]}`, http.StatusBadRequest, "invalid_payload"},
		{"malformed json", lifecycle, "{", http.StatusBadRequest, "invalid_payload"},
	} {
		w := httptest.NewRecorder()
		gateEngine(tc.user).ServeHTTP(w, httptest.NewRequest(http.MethodPost, unpublishPath, strings.NewReader(tc.body)))
		if w.Code != tc.wantCode || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: status=%d body=%s, want %d %s", tc.name, w.Code, w.Body.String(), tc.wantCode, tc.want)
		}
	}
}

// 真库状态机（无 MF_V2_TEST_DSN 时跳过）：published → draft 是唯一降级路径，
// 权限、证据、并发、留痕四条口径与 Lifecycle 一致；非 published 报稳定码 invalid_status，
// 不是 500，也不是"删掉再建"。
func TestUnpublishDemotesToDraftOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.u
	// 目录编辑码不含生命周期码（permission.go 的角色兜底也一样）。
	editor := fixtureUser("editor")
	pub := f.save(Entity{Kind: "work", Title: "待下架作品"})

	// 权限：无生命周期码者被拒，条目状态不变。
	if _, err := f.s.Unpublish(ctx, pub.ID, UnpublishEdit{ExpectedVersion: pub.Version, EditNote: "n", Sources: fixtureSources()}, editor); !errors.Is(err, errForbidden) {
		t.Fatalf("catalog.entity.edit 持有者不该能下架: %v", err)
	}
	// 证据：与 Save/Lifecycle 同口径（note 非空 + 至少一条来源）。
	if _, err := f.s.Unpublish(ctx, pub.ID, UnpublishEdit{ExpectedVersion: pub.Version}, admin); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("缺证据应报 evidence_required: %v", err)
	}
	// 并发：过时版本报 version_conflict，不写任何一行。
	if _, err := f.s.Unpublish(ctx, pub.ID, UnpublishEdit{ExpectedVersion: pub.Version - 1, EditNote: "n", Sources: fixtureSources()}, admin); !errors.Is(err, errVersionConflict) {
		t.Fatalf("过时版本应报 version_conflict: %v", err)
	}

	draft, err := f.s.Unpublish(ctx, pub.ID, UnpublishEdit{ExpectedVersion: pub.Version, EditNote: "发布错了，退回草稿", Sources: fixtureSources()}, admin)
	if err != nil {
		t.Fatalf("下架: %v", err)
	}
	// 返回形状与 Save 同：同一条实体、版本 +1、只改状态。
	if draft.ID != pub.ID || draft.Status != "draft" || draft.Version != pub.Version+1 || draft.Title != pub.Title || draft.Kind != pub.Kind {
		t.Fatalf("下架返回值不符: %+v", draft)
	}
	// 状态列与文档必须一起改：Get/visible 读文档，GetManyVisible 读列。
	var columnStatus string
	if err = f.s.DB.QueryRowContext(ctx, "SELECT status FROM catalog.entities WHERE id=$1", pub.ID).Scan(&columnStatus); err != nil || columnStatus != "draft" {
		t.Fatalf("状态列=%q err=%v, want draft", columnStatus, err)
	}
	if got, err := f.s.Get(ctx, pub.ID, &admin); err != nil || got.Status != "draft" {
		t.Fatalf("生命周期管理员回读: %+v %v", got, err)
	}
	// 对无生命周期码的他人不可见、匿名列表里不再出现：下架即离开公开面。
	if _, err := f.s.Get(ctx, pub.ID, &editor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("草稿不该对外人可见: %v", err)
	}
	if items, err := f.s.List(ctx, ListOptions{Kind: "work"}, nil); err != nil || len(items) != 0 {
		t.Fatalf("下架后不该出现在公开列表: %v %d", err, len(items))
	}
	// 留痕：既有修订机制写了一条修订（首次创建 + 下架 = 2 条），说明与来源都来自本次下架。
	revs, err := f.s.Revisions(ctx, pub.ID, &admin)
	if err != nil || len(revs) != 2 {
		t.Fatalf("修订历史应含下架一条: %v %d", err, len(revs))
	}
	if revs[0]["version"] != draft.Version || revs[0]["edit_note"] != "发布错了，退回草稿" || !strings.Contains(encode(revs[0]["snapshot"]), `"status":"draft"`) {
		t.Fatalf("最新修订不是本次下架: %+v", revs[0])
	}
	// outbox 事件码固定为 entity.unpublished：它不进 audit_actions（那只数删除/合并）。
	var eventType string
	if err = f.s.DB.QueryRowContext(ctx, "SELECT type FROM catalog.outbox WHERE entity_id=$1 AND version=$2", pub.ID, draft.Version).Scan(&eventType); err != nil || eventType != "entity.unpublished" {
		t.Fatalf("事件码=%q err=%v, want entity.unpublished", eventType, err)
	}
	stats, err := f.s.UserContributions(ctx, admin.ID, "all", 1, 20, &admin)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stats.WorksCreated != 1 || stats.Stats.RevisionsCount != 2 || stats.Stats.AuditActions != 0 {
		t.Fatalf("下架不该改贡献统计口径: %+v", stats.Stats)
	}

	// 只允许 published → draft：草稿再下架报 invalid_status（400，不是 500）。
	if _, err := f.s.Unpublish(ctx, pub.ID, UnpublishEdit{ExpectedVersion: draft.Version, EditNote: "n", Sources: fixtureSources()}, admin); !errors.Is(err, errInvalidStatus) {
		t.Fatalf("草稿再下架应报 invalid_status: %v", err)
	}
	// 终态同样只是 invalid_status：已删除条目没有可下架的内容。
	doomed := f.save(Entity{Kind: "work", Title: "待清退作品"})
	if _, err := f.s.Lifecycle(ctx, doomed.ID, LifecycleEdit{ExpectedVersion: doomed.Version, EditNote: "n", Sources: fixtureSources()}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unpublish(ctx, doomed.ID, UnpublishEdit{ExpectedVersion: doomed.Version + 1, EditNote: "n", Sources: fixtureSources()}, admin); !errors.Is(err, errInvalidStatus) {
		t.Fatalf("已删除条目应报 invalid_status: %v", err)
	}
	// 下架不是终点：草稿能继续编辑并重新发布（Save 只拦"已发布降级"，不拦草稿升格）。
	draft.Title = "待下架作品（改后重发）"
	draft.Status = "published"
	if _, err := f.s.Save(ctx, Edit{Entity: draft, ExpectedVersion: draft.Version, EditNote: "修好再发", Sources: fixtureSources()}, admin); err != nil {
		t.Fatalf("下架后的草稿应能重新发布: %v", err)
	}
}

// unpublishEngine 挂真实路由并注入身份：目录侧鉴权中间件在无令牌时不覆盖已有的 catalog_user，
// 所以这里不需要账号服务，但要用真库（Store）证明 respond 的状态码映射。
func unpublishEngine(s *Store, u *User) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u != nil {
			c.Set("catalog_user", u)
		}
		c.Next()
	})
	HTTP{Store: s}.Register(r)
	return r
}

// 端到端错误码矩阵（真库）：HTTP 层解码 UnpublishEdit、respond 把领域错误映射成状态码。
// 成功 200 + 与 Save 同形状的实体；非 published 400 invalid_status；过时版本 409 version_conflict。
func TestUnpublishHTTPStatusMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	admin := f.u
	engine := unpublishEngine(f.s, &admin)
	pub := f.save(Entity{Kind: "work", Title: "HTTP 矩阵作品"})
	call := func(version int64) *httptest.ResponseRecorder {
		t.Helper()
		payload := fmt.Sprintf(`{"expected_version":%d,"edit_note":"下架","sources":[{"kind":"url","citation":"HTTP 矩阵","url":"https://example.org/fixture"}]}`, version)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/catalog/entities/"+pub.ID+"/unpublish", strings.NewReader(payload)))
		return w
	}

	w := call(pub.Version)
	if w.Code != http.StatusOK {
		t.Fatalf("下架应 200: %d %s", w.Code, w.Body.String())
	}
	var got Entity
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是实体: %v %s", err, w.Body.String())
	}
	if got.ID != pub.ID || got.Status != "draft" || got.Version != pub.Version+1 {
		t.Fatalf("响应实体不符: %+v", got)
	}
	// 已回草稿后用旧版本再试：版本条件先判，报 409 而不是 400。
	if w = call(pub.Version); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "version_conflict") {
		t.Fatalf("过时版本应 409 version_conflict: %d %s", w.Code, w.Body.String())
	}
	// 版本对齐但状态不是 published：400 invalid_status（不是 500，也不是 404）。
	if w = call(pub.Version + 1); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_status") {
		t.Fatalf("非发布态应 400 invalid_status: %d %s", w.Code, w.Body.String())
	}
}
