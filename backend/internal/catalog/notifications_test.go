package catalog

// 站内通知的用例：模型不变量（聚合/幂等/已读/越权）在真库上验，产生端在真库上验，
// HTTP 面（含跨服务投递端点的双凭据）在真库 + 真路由树上验。
// 无 MF_V2_TEST_DSN 时 testutil.Database 自动跳过。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

const testInternalToken = "test-internal-token"

type notifyFixture struct {
	t *testing.T
	s *Store
	// owner 是原作者，夹具免审创建正式条目；PR 用例会移除免审权限。
	owner User
	// reviewer 可合入或关闭 PR，并可维护条目关系。
	reviewer User
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner := fixtureUser("member")
	owner.Permissions = []string{PermissionEntityEdit, PermissionReviewBypass}
	reviewer := fixtureUser("moderator")
	reviewer.Permissions = []string{PermissionEntityEdit, PermissionRelationEdit, PermissionLifecycleManage, PermissionReviewBypass, PermissionReviewManage}
	return &notifyFixture{t: t, s: s, owner: owner, reviewer: reviewer}
}

// saveAs 以指定身份准备一个正式实体。
func (f *notifyFixture) saveAs(u User, e Entity) Entity {
	f.t.Helper()
	if e.State == "" {
		e.State = "active"
	}
	if e.State == "active" && len(e.Translations) == 0 {
		e.Translations = map[string]Translation{"en": {Title: e.Title}}
	}
	var copyE Entity
	if err := json.Unmarshal([]byte(encode(e)), &copyE); err != nil {
		f.t.Fatal(err)
	}
	out, err := f.s.Save(context.Background(), Edit{Entity: copyE, ExpectedVersion: e.Version, EditNote: "notify fixture", Sources: fixtureSources()}, u)
	if err != nil {
		f.t.Fatalf("save %s as %s: %v", e.Title, u.Username, err)
	}
	return out
}

// notifs 读收件箱（断言聚合要看行数与 count，不能只看未读数）。
func (f *notifyFixture) notifs(recipient User) []Notification {
	f.t.Helper()
	items, total, _, err := f.s.ListNotifications(context.Background(), recipient.ID, 50, 0)
	if err != nil {
		f.t.Fatalf("list notifications: %v", err)
	}
	if total != len(items) {
		f.t.Fatalf("total=%d 与 items=%d 不一致", total, len(items))
	}
	return items
}

// engine 挂真实路由树并注入身份；internalToken 为空即"目录侧未配置共享密钥"。
func (f *notifyFixture) engine(identity *User, internalToken string) *gin.Engine {
	r := gin.New()
	if identity != nil {
		r.Use(func(c *gin.Context) { c.Set("catalog_user", identity); c.Next() })
	}
	HTTP{Store: f.s, InternalToken: internalToken}.Register(r)
	return r
}

func (f *notifyFixture) do(engine http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// deliver 模拟互动服务的跨服务投递（社区侧真实走的是它自己的 internal/catalog Notify）。
func (f *notifyFixture) deliver(t *testing.T, recipientID, entityID, commentID, excerpt string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"dedupe_key\":\"comment.replied:entity:%s\",\"event_id\":\"%s\",\"actor_id\":\"%s\",\"actor_name\":\"%s\",\"payload\":{\"entity_id\":\"%s\",\"excerpt\":\"%s\"}}",
		recipientID, entityID, entityID, commentID, f.owner.ID, f.owner.Username, entityID, excerpt)
	return f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal", body,
		map[string]string{InternalTokenHeader: testInternalToken})
}

// ---- 纯逻辑：类型枚举是跨服务契约 ----

func TestNotificationTypesAreStable(t *testing.T) {
	want := []string{"comment.replied", "entity.included", "entity.review_approved", "entity.review_rejected"}
	got := NotificationTypes()
	if len(got) != len(want) {
		t.Fatalf("通知类型条数=%d want %d（类型码是跨服务契约，只增不改）: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("类型码顺序/取值变了：got %v want %v", got, want)
		}
	}
	if validNotificationType("entity.review_done") {
		t.Fatal("未登记的类型不应被接受")
	}
}

// ---- 真库：核心场景（两个用户 + 一条回复 → 1 条未读 → 标记已读归零 + 越权被拒）----

func TestNotificationInboxLifecycleOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "通知用例作品"})

	// 收件箱初始为空：不能把"还没查"与"零条"混为一谈。
	if items, total, unread, err := f.s.ListNotifications(ctx, f.owner.ID, 20, 0); err != nil || total != 0 || unread != 0 || len(items) != 0 {
		t.Fatalf("初始收件箱应为空: %v total=%d unread=%d items=%d", err, total, unread, len(items))
	}

	// 一条回复（跨服务投递端点，与互动服务走的是同一条路径与同一套双凭据）。
	w := f.deliver(t, f.owner.ID, work.ID, "11111111-1111-1111-1111-111111111111", "我也想问这个问题")
	if w.Code != 200 {
		t.Fatalf("投递应 200: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "\"unread\":1") {
		t.Fatalf("投递响应应回执未读数 1: %s", w.Body.String())
	}

	items, total, unread, err := f.s.ListNotifications(ctx, f.owner.ID, 20, 0)
	if err != nil || total != 1 || unread != 1 || len(items) != 1 {
		t.Fatalf("接收者应有 1 条未读: err=%v total=%d unread=%d items=%d", err, total, unread, len(items))
	}
	n := items[0]
	if n.Type != NotificationCommentReplied || n.Read || n.Count != 1 || n.SubjectType != "entity" || n.SubjectID != work.ID {
		t.Fatalf("通知形状不符: %+v", n)
	}
	if n.ActorName != f.owner.Username || n.ActorID != f.owner.ID {
		t.Fatalf("actor 应取自投递请求里的终端用户令牌: %+v", n)
	}
	if got, _ := n.Payload["excerpt"].(string); got != "我也想问这个问题" {
		t.Fatalf("payload.excerpt=%q", got)
	}

	// 越权：另一个人标记这条通知为已读 → 404 not_found，且未读数不变。
	w = f.do(f.engine(&f.reviewer, testInternalToken), http.MethodPost, "/api/notifications/"+n.ID+"/read", "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("他人标记已读应 404 not_found: %d %s", w.Code, w.Body.String())
	}
	// 越权：另一个人的收件箱里看不到这条（列表本身不接受 recipient 参数）。
	if items, total, _, err := f.s.ListNotifications(ctx, f.reviewer.ID, 20, 0); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("他人收件箱不该看见这条: %v total=%d", err, total)
	}
	if unread, err := f.s.NotificationUnreadCount(ctx, f.owner.ID); err != nil || unread != 1 {
		t.Fatalf("越权尝试不该改变未读数: %v %d", err, unread)
	}

	// 本人标记已读 → 未读归零；重复标记仍 200（幂等，read_at 保留首次值）。
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/"+n.ID+"/read", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"unread\":0") {
		t.Fatalf("标记已读应 200 且未读归零: %d %s", w.Code, w.Body.String())
	}
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/"+n.ID+"/read", "", nil)
	if w.Code != 200 {
		t.Fatalf("重复标记已读应仍然 200: %d %s", w.Code, w.Body.String())
	}
	if unread, err := f.s.NotificationUnreadCount(ctx, f.owner.ID); err != nil || unread != 0 {
		t.Fatalf("标记已读后未读数应为 0: %v %d", err, unread)
	}
	// 不存在的 id 与"别人的 id"同解：都 404，不泄露存在性。
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/00000000-0000-0000-0000-0000000000ff/read", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的通知应 404: %d %s", w.Code, w.Body.String())
	}

	// 未读数端点与列表口径一致（角标与列表不该各说各话）。
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodGet, "/api/notifications/unread-count", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"unread\":0") {
		t.Fatalf("未读数端点: %d %s", w.Code, w.Body.String())
	}
}

// ---- 真库：聚合、重试幂等、全部已读 ----

func TestNotificationAggregationAndIdempotencyOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "聚合用例作品"})

	f.deliver(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-000000000001", "第一条")
	// 同一条 dedupe_key 的第二条事件：合并成一行，count 累加。
	f.deliver(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-000000000002", "第二条")
	items := f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 2 {
		t.Fatalf("同键事件应合并成一行 count=2: %+v", items)
	}

	// 用户读掉它，然后再来一条新事件：read_at 必须归零（有新活动就要重新被看见）。
	if _, err := f.s.MarkNotificationRead(ctx, f.owner.ID, items[0].ID); err != nil {
		t.Fatal(err)
	}
	f.deliver(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-000000000003", "第三条")
	items = f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 3 || items[0].Read {
		t.Fatalf("合并后应重新未读且 count=3: %+v", items)
	}

	// 重试幂等：同一个 event_id 再投一次（上游超时后其实已写入的情形），计数与未读都不动。
	f.deliver(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-000000000003", "第三条")
	items = f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("同一 event_id 重投不该累加计数: %+v", items)
	}
	if unread, err := f.s.NotificationUnreadCount(ctx, f.owner.ID); err != nil || unread != 1 {
		t.Fatalf("重投后未读数仍是 1: %v %d", err, unread)
	}

	// 全部标记已读：返回本次条数与之后的未读数（回读，不假设为 0）。
	updated, unread, err := f.s.MarkAllNotificationsRead(ctx, f.owner.ID)
	if err != nil || updated != 1 || unread != 0 {
		t.Fatalf("全部已读: err=%v updated=%d unread=%d", err, updated, unread)
	}
	if updated, _, err = f.s.MarkAllNotificationsRead(ctx, f.owner.ID); err != nil || updated != 0 {
		t.Fatalf("没有未读时再标记应 updated=0: %v %d", err, updated)
	}

	// HTTP 面：全部已读端点同样只动自己那一行。
	f.deliver(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-000000000004", "第四条")
	w := f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/read-all", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"unread\":0") {
		t.Fatalf("全部已读端点: %d %s", w.Code, w.Body.String())
	}
}

// ---- 真库：分页与页宽收敛 ----

func TestNotificationPaginationOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	for i, tag := range []string{"b1", "b2", "b3"} {
		other := f.saveAs(f.owner, Entity{Kind: "work", Title: "分页目标 " + tag})
		f.deliver(t, f.owner.ID, other.ID, fmt.Sprintf("bbbbbbbb-0000-0000-0000-00000000000%d", i+1), "正文 "+tag)
	}

	page1, total, unread, err := f.s.ListNotifications(ctx, f.owner.ID, 2, 0)
	if err != nil || total != 3 || unread != 3 || len(page1) != 2 {
		t.Fatalf("第一页: err=%v total=%d unread=%d len=%d", err, total, unread, len(page1))
	}
	page2, total2, _, err := f.s.ListNotifications(ctx, f.owner.ID, 2, 2)
	if err != nil || total2 != 3 || len(page2) != 1 {
		t.Fatalf("第二页: err=%v total=%d len=%d", err, total2, len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Fatal("两页不应重叠")
	}
	// 越界页宽收敛到缺省值（不 400），钉住"静默收敛"的口径。
	if items, _, _, err := f.s.ListNotifications(ctx, f.owner.ID, 9999, 0); err != nil || len(items) != 3 {
		t.Fatalf("越界 limit 应收敛而不是报错: %v %d", err, len(items))
	}
	// HTTP 分页：limit/offset 透传，total 是真实总数而不是本页长度。
	w := f.do(f.engine(&f.owner, testInternalToken), http.MethodGet, "/api/notifications?limit=2&offset=2", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"total\":3") {
		t.Fatalf("HTTP 分页 total 应为 3: %d %s", w.Code, w.Body.String())
	}
}

// ---- 真库：目录侧三个产生端（PR 合入 / PR 关闭 / 收录）----

func TestNotificationProducersOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()

	submitter := f.owner
	submitter.Permissions = []string{PermissionEntityEdit}
	changes, _, pending := prepareWorkspace(t, f.s, submitter, WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"待合入作品"}`)})
	proposal, err := f.s.PushWorkspace(ctx, pending.ID, WorkspacePush{ExpectedVersion: pending.Version, ExpectedHead: *pending.HeadCommitID}, submitter)
	if err != nil || proposal.Applied || proposal.PullRequest == nil {
		t.Fatalf("open PR: %+v %v", proposal, err)
	}
	pr := proposal.PullRequest
	if _, err := f.s.Get(ctx, changes.Refs["work"], nil); err == nil {
		t.Fatal("open PR changed the main catalog")
	}
	if _, err := f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.reviewer); err != nil {
		t.Fatal(err)
	}
	items := f.notifs(f.owner)
	if len(items) != 1 || items[0].Type != NotificationReviewApproved || items[0].SubjectType != "pull_request" || items[0].SubjectID != pr.ID {
		t.Fatalf("PR merge notification: %+v", items)
	}
	closedChanges, _, rejected := prepareWorkspace(t, f.s, submitter, WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"待关闭作品"}`)})
	proposal, err = f.s.PushWorkspace(ctx, rejected.ID, WorkspacePush{ExpectedVersion: rejected.Version, ExpectedHead: *rejected.HeadCommitID}, submitter)
	if err != nil || proposal.PullRequest == nil {
		t.Fatalf("second PR: %+v %v", proposal, err)
	}
	pr = proposal.PullRequest
	closed := "closed"
	if _, err := f.s.EditPullRequest(ctx, pr.ID, PullRequestEdit{ExpectedVersion: pr.Version, Status: &closed}, f.reviewer); err != nil {
		t.Fatal(err)
	}
	items = f.notifs(f.owner)
	if len(items) != 2 || items[0].Type != NotificationReviewRejected || items[0].SubjectType != "pull_request" || items[0].SubjectID != pr.ID {
		t.Fatalf("PR close notification: %+v", items)
	}
	if _, err := f.s.Get(ctx, closedChanges.Refs["work"], nil); err == nil {
		t.Fatal("closing a PR materialized its proposal")
	}
	// 收录（release 声明 subjects）：owner 的作品被 reviewer 放进发行 → owner 收到 included。
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "被收录的作品"})
	rel := f.saveAs(f.reviewer, Entity{Kind: "release", Title: "收录它的发行", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	items = f.notifs(f.owner)
	if len(items) != 3 || items[0].Type != NotificationEntityIncluded {
		t.Fatalf("收录应给 owner 一条 entity.included: %+v", items)
	}
	if items[0].Payload["container_id"] != rel.ID || items[0].SubjectID != work.ID {
		t.Fatalf("收录通知的容器/落点不符: %+v", items[0])
	}
	// 同一发行再存一次（subjects 未变）不重复通知。
	f.saveAs(f.reviewer, Entity{ID: rel.ID, Version: rel.Version, Kind: "release", Title: "收录它的发行", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	if items = f.notifs(f.owner); len(items) != 3 {
		t.Fatalf("subjects 未变不该重复通知: %+v", items)
	}

	// 收录（includes 关系）：reviewer 建集合 → owner 的作品被收录。
	col := f.saveAs(f.reviewer, Entity{Kind: "collection", Title: "收录用例集合"})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "includes", SourceID: col.ID, TargetID: work.ID, Attributes: map[string]any{}},
		EditNote: "收录用例",
		Sources:  fixtureSources(),
	}, f.reviewer); err != nil {
		t.Fatalf("建 includes 关系: %v", err)
	}
	items = f.notifs(f.owner)
	if len(items) != 4 || items[0].Type != NotificationEntityIncluded || items[0].Payload["container_id"] != col.ID {
		t.Fatalf("includes 关系应产生一条收录通知: %+v", items)
	}
	// 改已存在关系的属性不是新的收录事件：不该再发一条。
	rels, err := f.s.Relations(ctx, work.ID, &f.reviewer)
	if err != nil || len(rels) == 0 {
		t.Fatalf("读关系: %v %d", err, len(rels))
	}
	var rid string
	var version int64
	for _, r := range rels {
		if r.Type == "includes" && r.SourceID == col.ID {
			rid, version = r.ID, r.Version
		}
	}
	if rid == "" {
		t.Fatal("找不到刚建的 includes 关系")
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation:        Relation{ID: rid, Type: "includes", SourceID: col.ID, TargetID: work.ID, Attributes: map[string]any{}},
		ExpectedVersion: version, EditNote: "改属性", Sources: fixtureSources(),
	}, f.reviewer); err != nil {
		t.Fatalf("改 includes 关系: %v", err)
	}
	if items = f.notifs(f.owner); len(items) != 4 {
		t.Fatalf("改关系属性不该重复通知: %+v", items)
	}
}

// ---- 真库：导入完成回执（收件人是发起人自己，且重复导入不刷屏）----

func TestNotificationInternalDeliveryCredentialsOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "投递凭据用例"})
	body := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"payload\":{}}", f.owner.ID, work.ID)

	// 目录侧未配置共享密钥：端点整体关闭（503 internal_api_disabled），不是静默不写。
	w := f.do(f.engine(&f.owner, ""), http.MethodPost, "/api/notifications/internal", body, map[string]string{InternalTokenHeader: "whatever"})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "internal_api_disabled") {
		t.Fatalf("未配置密钥应 503 internal_api_disabled: %d %s", w.Code, w.Body.String())
	}
	// 密钥错：401 invalid_internal_token。
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal", body, map[string]string{InternalTokenHeader: "wrong"})
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid_internal_token") {
		t.Fatalf("错误密钥应 401 invalid_internal_token: %d %s", w.Code, w.Body.String())
	}
	// 投递体无作者快照：400 invalid_actor。
	w = f.do(f.engine(nil, testInternalToken), http.MethodPost, "/api/notifications/internal", body, map[string]string{InternalTokenHeader: testInternalToken})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_actor") {
		t.Fatalf("缺作者快照应 400 invalid_actor: %d %s", w.Code, w.Body.String())
	}
	// 类型不在枚举里：400 invalid_notification_type（不是静默丢弃）。
	bad := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"entity.exploded\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"payload\":{}}", f.owner.ID, work.ID)
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal", bad, map[string]string{InternalTokenHeader: testInternalToken})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_notification_type") {
		t.Fatalf("未知类型应 400: %d %s", w.Code, w.Body.String())
	}
	// 收件人不是 UUID：400 invalid_recipient_id。
	bad = fmt.Sprintf("{\"recipient_id\":\"not-a-uuid\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"payload\":{}}", work.ID)
	w = f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal", bad, map[string]string{InternalTokenHeader: testInternalToken})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_recipient_id") {
		t.Fatalf("非法收件人应 400: %d %s", w.Code, w.Body.String())
	}
	if items := f.notifs(f.owner); len(items) != 0 {
		t.Fatalf("四次失败投递都不该写行: %+v", items)
	}
}

// ---- 真库：服务身份投递（A03）无终端用户令牌也可投递，作者取投递体快照 ----

func TestNotificationServiceIdentityDeliveryOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "服务身份用例"})
	authorID := "22222222-2222-2222-2222-222222222222"
	body := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"actor_id\":\"%s\",\"actor_name\":\"replier\",\"event_id\":\"evt-svc-1\",\"payload\":{}}", f.owner.ID, work.ID, authorID)
	// 无终端用户令牌 + body 作者快照 → 200，作者取快照（退出登录的后台重试即此形态）。
	w := f.do(f.engine(nil, testInternalToken), http.MethodPost, "/api/notifications/internal", body, map[string]string{InternalTokenHeader: testInternalToken})
	if w.Code != 200 {
		t.Fatalf("服务身份投递应 200: %d %s", w.Code, w.Body.String())
	}
	items := f.notifs(f.owner)
	if len(items) != 1 || items[0].ActorID != authorID || items[0].ActorName != "replier" {
		t.Fatalf("作者应取快照: %+v", items)
	}
	// 非法 actor_id → 400 invalid_actor。
	bad := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"actor_id\":\"nope\",\"payload\":{}}", f.owner.ID, work.ID)
	w = f.do(f.engine(nil, testInternalToken), http.MethodPost, "/api/notifications/internal", bad, map[string]string{InternalTokenHeader: testInternalToken})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_actor") {
		t.Fatalf("非法作者应 400 invalid_actor: %d %s", w.Code, w.Body.String())
	}
}

// 读取端三条路径都要登录：匿名 401 authentication_required（不查库，用空 Store 即可）。

func TestNotificationReadEndpointsRequireIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/notifications"},
		{http.MethodGet, "/api/notifications/unread-count"},
		{http.MethodPost, "/api/notifications/read-all"},
		{http.MethodPost, "/api/notifications/00000000-0000-0000-0000-000000000001/read"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "authentication_required") {
			t.Errorf("%s %s 匿名应 401: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
}
