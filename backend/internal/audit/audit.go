// Package audit 实现《统一审计留痕（audit log）跨服务契约》的写入侧。
//
// catalog / auth / community / storage 四仓各复制一份**同源代码**：四仓是独立 module，没有跨仓
// 依赖通道，所以这里的 ServiceName / Schema / 函数名与语义必须与其他三份保持一致，改动要四仓同步
// （契约 §3）。本包只依赖标准库、gin 与 uuid，不引用任何服务自己的类型，才能被逐字复制过去。
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ServiceName 是本服务写进 audit_log.service 的值（契约 §1：该列无 CHECK，新增服务不改旧 DDL）。
const ServiceName = "catalog"

// Schema 是契约 §1 的 DDL（四仓逐字一致，改动要四仓同步）。
//
// **必须带存在性守卫**（`IF to_regclass('audit.audit_log') IS NULL`），不能退回顶层的
// `CREATE TABLE IF NOT EXISTS …` + `CREATE INDEX IF NOT EXISTS …`：实测（PostgreSQL 17）
// `CREATE INDEX IF NOT EXISTS` 是**先查表所有权、再看索引是否存在**，而这张表在最小权限部署里
// 由 `mf_audit_owner` 预建、四个运行角色都不是它的所有者 → 无条件重跑会在服务启动路径上拿到
// `42501 must be owner of table audit_log`，四个服务全部起不来（`CREATE TABLE IF NOT EXISTS`
// 不这样，它只要求 schema 上的 CREATE；旧写法只在"表还不存在"时才侥幸通过）。
// 有了守卫，表已存在的实例上整段是纯空转、不触发任何所有权检查；表不存在时才建表，此刻表由
// 执行者自己建出、四条索引随即可建（所以索引不再需要 IF NOT EXISTS）。
// 锁仍在建表之前取（PERFORM），四个服务同时首次启动由 advisory 锁 740205 串行化。
const Schema = `DO $audit_ddl$
BEGIN
  PERFORM pg_advisory_xact_lock(740205);
  IF to_regclass('audit.audit_log') IS NULL THEN
    CREATE SCHEMA IF NOT EXISTS audit;
    CREATE TABLE audit.audit_log (
      id               uuid PRIMARY KEY,
      occurred_at      timestamptz NOT NULL DEFAULT now(),
      service          text NOT NULL,
      action           text NOT NULL,
      actor_user_id    uuid,
      actor_username   text NOT NULL DEFAULT '',
      credential_type  text NOT NULL DEFAULT '',
      actor_ip         text NOT NULL DEFAULT '',
      actor_user_agent text NOT NULL DEFAULT '',
      target_type      text NOT NULL DEFAULT '',
      target_id        text NOT NULL DEFAULT '',
      changes          jsonb NOT NULL DEFAULT '{}'::jsonb,
      result           text NOT NULL DEFAULT 'success' CHECK (result IN ('success','failure')),
      error_code       text NOT NULL DEFAULT '',
      request_method   text NOT NULL DEFAULT '',
      route            text NOT NULL DEFAULT '',
      http_status      int NOT NULL DEFAULT 0,
      request_id       text NOT NULL DEFAULT ''
    );
    CREATE INDEX audit_log_occurred_at_idx ON audit.audit_log(occurred_at DESC);
    CREATE INDEX audit_log_service_action_idx ON audit.audit_log(service, action, occurred_at DESC);
    CREATE INDEX audit_log_actor_idx ON audit.audit_log(actor_user_id, occurred_at DESC);
    CREATE INDEX audit_log_target_idx ON audit.audit_log(target_type, target_id, occurred_at DESC);
  END IF;
END
$audit_ddl$;
`

// 契约写死的上限与上下文键（§1、§3、§4）。
const (
	// QueueSize 是有界队列容量：满则丢行并计数，绝不阻塞业务响应。
	QueueSize = 1024
	// ErrorCodeKey 是失败错误码的 gin 上下文键：处理器用 Fail 写入，中间件在 c.Next() 之后读它。
	ErrorCodeKey = "audit_error_code"
	// RequestIDHeader 是请求 id 的透传头：请求没带时由中间件生成 uuid 并回写同名响应头。
	RequestIDHeader = "X-Request-Id"

	userAgentMax = 512
	valueMax     = 512
	changesMax   = 8 * 1024
	// insertTimeout 是单行落库的上限：后台 goroutine 不能被一次卡死的写入拖住整条队列。
	insertTimeout = 5 * time.Second
)

const (
	redacted     = "[redacted]"
	truncatedKey = "_truncated"
	// detailKey 是 Describe 与中间件之间的上下文键。
	detailKey = "audit_detail"
)

// Entry 是一行审计（字段与契约 §1 的列一一对应）。ID / OccurredAt / Service / Result 为空时
// 由 Recorder 补齐：service 取 Recorder 的服务名，occurred_at 取当前时间，result 取 success。
type Entry struct {
	ID             string
	OccurredAt     time.Time
	Service        string
	Action         string
	ActorUserID    string
	ActorUsername  string
	CredentialType string
	ActorIP        string
	ActorUserAgent string
	TargetType     string
	TargetID       string
	Changes        map[string]any
	Result         string
	ErrorCode      string
	RequestMethod  string
	Route          string
	HTTPStatus     int
	RequestID      string
}

// Detail 是处理器补充的被动对象与变更摘要（契约 §3）：中间件按响应状态定 result，
// 这里的 target/changes 原样进审计行。
type Detail struct {
	TargetType string
	TargetID   string
	Changes    map[string]any
}

// Actor 是操作者快照：UserID 为空表示匿名。CredentialType 取 session / pat / oauth /
// anonymous / system；非账号服务只能给 pat 或 session 这类近似值（契约 §7）。
type Actor struct {
	UserID         string
	Username       string
	CredentialType string
}

// Options 是中间件的接线参数。
type Options struct {
	Recorder *Recorder
	// Actions 是"METHOD 路由模板" → 动作码。只有登记在册的请求才写审计（GET 与豁免路由不写）。
	Actions map[string]string
	// Exempt 是"METHOD 路由模板" → 豁免理由：用了写方法但没有写入语义的路由。
	// 运行期不读它（未登记的路由本就不写），它由各服务的「写路由覆盖守卫测试」读取，
	// 让"这条写路由为什么不记"有据可查，而不是被忘掉。
	Exempt map[string]string
	// Actor 从请求上下文取操作者；为 nil 时操作者留空（等同匿名）。
	Actor func(*gin.Context) Actor
}

// Recorder 是审计写入器：一个后台 goroutine + 有界 channel。
// 零值不可用，必须经 NewRecorder 构造。
type Recorder struct {
	db      *sql.DB
	service string
	queue   chan Entry
	wg      sync.WaitGroup
	dropped atomic.Int64
	// mu + closed 把"Record 入队"与"Close 关通道"串行化：没有它，Close 之后到达的 Record
	// 会往已关闭的 channel 发送而 panic（业务侧不该因为收尾而崩）。
	mu     sync.RWMutex
	closed bool
}

// NewRecorder 起后台写库 goroutine；db 可为 nil（单测未接库），此时落库会返回错误并被丢弃。
func NewRecorder(db *sql.DB, service string) *Recorder {
	r := &Recorder{db: db, service: service, queue: make(chan Entry, QueueSize)}
	r.wg.Add(1)
	go r.loop()
	return r
}

// Record 非阻塞入队：队列满时打 error 日志并丢弃，绝不阻塞业务响应（契约 §3）。
func (r *Recorder) Record(e Entry) {
	if r == nil {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		// 进程收尾阶段（Close 之后）到达的行直接丢：此时没有消费者了，入队只会堆积。
		return
	}
	select {
	case r.queue <- e:
	default:
		n := r.dropped.Add(1)
		slog.Error("审计入队失败：队列已满，丢弃该行（业务不受影响）",
			"service", r.service, "action", e.Action, "target_type", e.TargetType, "dropped_total", n)
	}
}

// RecordSync 同步写一行（只给测试与"必须强一致"的少数动作用；本服务没有强一致动作）。
func (r *Recorder) RecordSync(ctx context.Context, e Entry) error {
	if r == nil {
		return errors.New("audit: recorder unavailable")
	}
	return r.insert(ctx, e)
}

// Close 排空队列并停掉后台 goroutine（测试收尾用）。可重复调用。
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.queue)
	r.mu.Unlock()
	r.wg.Wait()
}

// Dropped 返回因队列满而丢弃的行数（测试断言"满队列不阻塞"用）。
func (r *Recorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// loop 是唯一的消费者：串行落库，失败只记日志——审计写失败不回滚业务写入（契约 §3）。
func (r *Recorder) loop() {
	defer r.wg.Done()
	for e := range r.queue {
		ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
		err := r.insert(ctx, e)
		cancel()
		if err != nil {
			slog.Error("审计落库失败：丢弃该行（业务写入不回滚）",
				"service", r.service, "action", e.Action, "request_id", e.RequestID, "err", err)
		}
	}
}

const insertSQL = "INSERT INTO audit.audit_log (id, occurred_at, service, action, actor_user_id, actor_username, credential_type, actor_ip, actor_user_agent, target_type, target_id, changes, result, error_code, request_method, route, http_status, request_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)"

// insert 落一行：补齐默认值 → 脱敏 changes → 写库。四个入口（Record/RecordSync）都经这里，
// 保证"写入前必须过滤"（契约 §4）没有旁路。
func (r *Recorder) insert(ctx context.Context, e Entry) error {
	if r.db == nil {
		return errors.New("audit: recorder has no database")
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.Service == "" {
		e.Service = r.service
	}
	if e.Result == "" {
		e.Result = "success"
	}
	payload, err := encodeChanges(SanitizeChanges(e.Changes))
	if err != nil {
		return err
	}
	// actor_user_id 是 uuid 列：为空写 NULL；不是 uuid 时保行丢 id（用户名快照仍在），
	// 否则整行会被 Postgres 拒掉——留痕宁可少一列，也不能整条丢。
	var actorID any
	if raw := strings.TrimSpace(e.ActorUserID); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			slog.Warn("审计：actor_user_id 不是 uuid，按空值写入", "service", r.service, "action", e.Action, "actor_user_id", raw)
		} else {
			actorID = parsed.String()
		}
	}
	_, err = r.db.ExecContext(ctx, insertSQL,
		e.ID, e.OccurredAt, e.Service, e.Action, actorID, e.ActorUsername, e.CredentialType,
		e.ActorIP, truncate(e.ActorUserAgent, userAgentMax), e.TargetType, e.TargetID, payload,
		e.Result, e.ErrorCode, e.RequestMethod, e.Route, e.HTTPStatus, e.RequestID)
	return err
}

// Describe 由处理器调用：登记被动对象与"变更前后"摘要。
func Describe(c *gin.Context, d Detail) {
	c.Set(detailKey, d)
}

// Fail 由处理器/闸门调用：登记失败错误码，值必须与响应体的 error 字段一致；
// 中间件据此写 result=failure + error_code（没登记时回落 http_<status>）。
func Fail(c *gin.Context, code string) {
	if code != "" {
		c.Set(ErrorCodeKey, code)
	}
}

// Middleware 给写路由接线。挂载位置很关键：必须在身份中间件**之后**（草稿里的 actor 要在
// c.Next() 之前可读），且在写路由注册**之前**（gin 的 RouterGroup.Use 只对之后注册的路由生效）。
//
// 同一个引擎可以挂多个实例，各自的动作码表不重叠即可（没有别的办法覆盖注册在路由组之外的
// 端点：引擎级中间件在 c.Next() 之前拿不到组内中间件解析出的身份）。
func Middleware(o Options) gin.HandlerFunc {
	actions := o.Actions
	if actions == nil {
		actions = map[string]string{}
	}
	return func(c *gin.Context) {
		if o.Recorder == nil {
			// 未注入 recorder（单测拿不到库）时不写审计，也不改变请求语义。
			c.Next()
			return
		}
		// 路由模板而不是原始路径：原始路径没有额外信息，模板才能聚合（契约 §1）。
		// FullPath 为空说明没有匹配到路由（404 之类），此时也不该有动作码。
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		action, ok := actions[c.Request.Method+" "+route]
		if !ok {
			c.Next()
			return
		}
		e := Entry{
			OccurredAt:     time.Now().UTC(),
			Action:         action,
			ActorIP:        c.ClientIP(),
			ActorUserAgent: c.GetHeader("User-Agent"),
			RequestMethod:  c.Request.Method,
			Route:          route,
			RequestID:      requestID(c),
		}
		if o.Actor != nil {
			a := o.Actor(c)
			e.ActorUserID, e.ActorUsername, e.CredentialType = a.UserID, a.Username, a.CredentialType
		}
		c.Next()
		status := c.Writer.Status()
		if status == 0 {
			status = http.StatusOK
		}
		e.HTTPStatus = status
		if status < 400 {
			e.Result = "success"
		} else {
			e.Result = "failure"
			e.ErrorCode = c.GetString(ErrorCodeKey)
			if e.ErrorCode == "" {
				e.ErrorCode = fmt.Sprintf("http_%d", status)
			}
		}
		if v, ok := c.Get(detailKey); ok {
			if d, ok := v.(Detail); ok {
				e.TargetType, e.TargetID, e.Changes = d.TargetType, d.TargetID, d.Changes
			}
		}
		o.Recorder.Record(e)
	}
}

// requestID 优先用 httplog.RequestID 写进上下文的值（与访问日志、响应头同一条），
// 再回落到请求头；都没有时才生成（单元测试等没挂 httplog 中间件的场景）。
func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if rid, _ := v.(string); strings.TrimSpace(rid) != "" {
			return rid
		}
	}
	rid := strings.TrimSpace(c.GetHeader(RequestIDHeader))
	if rid == "" {
		rid = uuid.NewString()
		c.Header(RequestIDHeader, rid)
	}
	return rid
}
