package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

type HTTP struct {
	Store *Store
	// InternalToken 是跨服务投递端点（POST /api/notifications/internal）的共享密钥。
	// 为空即端点关闭（503 internal_api_disabled）——默认关闭见 notifications_http.go 的说明。
	InternalToken string
}

func respond(c *gin.Context, v any, err error) {
	if err == nil {
		c.JSON(200, v)
		return
	}
	status := 400
	code := err.Error()
	var pg *pq.Error
	if errors.Is(err, errTransactionBusy) {
		status, code = http.StatusServiceUnavailable, "transaction_busy"
		c.Header("Retry-After", "1")
	} else if errors.Is(err, errSearchUnavailable) {
		status, code = http.StatusServiceUnavailable, "search_unavailable"
	} else if errors.Is(err, sql.ErrNoRows) {
		status = 404
		code = "not_found"
	} else if errors.As(err, &pg) {
		switch pg.Code {
		case "23503", "23514", "23505":
			code = "constraint_violation"
		case "22P02":
			code = "invalid_id"
		default:
			status = 500
			code = "database_error"
		}
		if status >= 500 {
			// SQLSTATE 与 detail 只进服务端日志：客户端拿到表名/约束名既看不懂也泄露库结构。
			slog.Error("目录服务：数据库错误", "sqlstate", string(pg.Code), "constraint", pg.Constraint,
				"detail", pg.Detail, "err", err.Error())
		}
	} else if internalFailure(err) {
		// 非 pq 的库层错误（连接被拒、context 取消）与文档反序列化失败都走这里：
		// 默认分支不再把驱动原文当错误码回给客户端（报告 #16）。
		slog.Error("目录服务：未登记的错误（原文不外发）", "err", err.Error())
		status, code = 500, codeInternalError
	} else if errors.Is(err, errForbidden) {
		// 按错误链判定：delivery_partial/merge_relation_conflict 这类 %w 包裹后
		// err.Error() 是拼接串，全等比较会让 403 退化成 400。
		// 401 不在这里产生：未登录由 required() 闸门直接返回 authentication_required，
		// 目录服务内没有 invalid_credentials 的生产者（原分支已删）。
		status = 403
	} else if errors.Is(err, errVersionConflict) {
		status = 409
	} else if errors.Is(err, errIdempotencyConflict) {
		// 同键不同载荷：409 且码与 version_conflict 区分（见 idempotency.go）。
		status = 409
	}
	// 失败路径同样留痕：登记的错误码与响应体的 error 字段同值（契约 §1），
	// 审计中间件据此写 result=failure + error_code（没登记的才回落 http_<status>）。
	auditlog.Fail(c, code)
	// 错误响应统一为单一 error 字段（值为稳定机器码）；database_error 只透出固定码，
	// 不附带 SQL 原文。
	c.JSON(status, gin.H{"error": code})
}
func body(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		auditlog.Fail(c, "invalid_payload")
		c.JSON(400, gin.H{"error": "invalid_payload"})
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		auditlog.Fail(c, "invalid_payload")
		c.JSON(400, gin.H{"error": "invalid_payload"})
		return false
	}
	return true
}
func user(c *gin.Context) *User {
	v, ok := c.Get("catalog_user")
	if !ok {
		return nil
	}
	return v.(*User)
}

// required 是路由级闸门：code 为空只要求已登录，否则要求令牌带对应权限码。
// 角色兜底集中在 User.Can 里，这里不再比角色字符串（见 permission.go）。
func required(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := user(c)
		if u == nil {
			// 越权/未登录的写请求也要留痕（result=failure），错误码与响应体同值。
			auditlog.Fail(c, "authentication_required")
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication_required"})
			return
		}
		// S01 双重收口：第三方 OAuth 身份在治理码上直接 403（与 Can 内一致；
		// 即使将来某码被误标非治理，本层仍按"管理路由默认拒第三方"兜住）。
		if u.IsThirdParty && code != "" && isGovernanceCode(code) {
			auditlog.Fail(c, "forbidden")
			c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
			return
		}
		// L2 默认拒绝第三方写入：仅 openid/profile/email 授权的第三方没有任何写能力。
		// 纯登录路由（code==""，如建草稿、提案、首页偏好写）的读方法仍放行第三方，
		// 写方法一律 403（自助草稿的所有权判定只在第一方内部区分主人）。
		// 需第三方贡献时由显式写 scope 再开专用路由，本层默认关门。
		if u.IsThirdParty && code == "" && !isReadMethod(c.Request.Method) {
			auditlog.Fail(c, "forbidden")
			c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
			return
		}
		if code != "" && !u.Can(code) {
			auditlog.Fail(c, "forbidden")
			c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

// isReadMethod 报告是否为读方法：第三方在纯登录路由上只读，写默认拒绝（L2）。
func isReadMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// routeBucket 仅供未接数据库的单测使用；生产计数在共享数据库中。
type routeBucket struct {
	mu       sync.Mutex
	start    time.Time
	n        int
	lastSeen time.Time
}

var (
	routeAttempts sync.Map // string -> *routeBucket
	routeJanitor  sync.Once
)

// sweepStaleBuckets 每小时清理超 2 小时未见的限流桶，防止 sync.Map 无限增长。
// 进程内存限流本就只防单机突发，多实例一致性放三期（Redis）。
func sweepStaleBuckets(m *sync.Map, janitor *sync.Once) {
	janitor.Do(func() {
		go func() {
			for range time.Tick(time.Hour) {
				cutoff := time.Now().Add(-2 * time.Hour)
				m.Range(func(k, v any) bool {
					if b, ok := v.(*routeBucket); ok {
						b.mu.Lock()
						stale := !b.lastSeen.IsZero() && b.lastSeen.Before(cutoff)
						b.mu.Unlock()
						if stale {
							m.Delete(k)
						}
					}
					return true
				})
			}
		}()
	})
}

// routeLimiter 限流重型查询接口, 超限返回 429 + Retry-After(秒)。
//
// 主体与额度：已登录请求的主体是**账号**（同一账号的多个 IP/标签页共用一个桶），
// 未登录请求的主体是其真实客户端 IP。额度按
// 账号 > 用户组 > 全局默认 > 本函数传入的路由内置额度 逐级回落（见 ratelimit.go 的 Resolve）。
// 命中 unlimited 的主体完全跳过计数（"解除限制"），且不下发 X-RateLimit-* 头——
// 没有窗口就没有"还剩几次"可言，发一个假上限比不发更容易误导调用方。
//
// 生产环境的全部业务读写共用 PostgreSQL 固定分钟窗口；多副本共用同一计数。
func routeLimiter(perMinute int, databases ...*sql.DB) gin.HandlerFunc {
	var db *sql.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	sweepStaleBuckets(&routeAttempts, &routeJanitor)
	return func(c *gin.Context) {
		u := user(c)
		subject := "ip:" + c.ClientIP()
		var identities, groups []string
		if u != nil {
			// 账号身份优先用令牌 subject（目录侧唯一权威的账号标识），
			// 用户名作为第二顺位，方便运维直接按用户名配额度。
			subject = "u:" + u.ID
			identities = []string{u.ID, u.Username}
			groups = u.Groups
		}
		limit, unlimited := currentRateLimitPolicy().Resolve(identities, groups, u == nil, perMinute)
		if unlimited {
			c.Next()
			return
		}
		var used, retrySecs int
		if db != nil {
			var err error
			used, retrySecs, err = consumeRateWindow(c.Request.Context(), db, subject)
			if err != nil {
				slog.Error("目录服务：共享限流计数失败", "err", err)
				c.Header("Retry-After", "1")
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate_limit_unavailable"})
				return
			}
		} else {
			now := time.Now()
			v, _ := routeAttempts.LoadOrStore(subject, &routeBucket{start: now, lastSeen: now})
			b := v.(*routeBucket)
			b.mu.Lock()
			if now.Sub(b.start) > time.Minute {
				b.start, b.n = now, 0
			}
			b.n++
			b.lastSeen = now
			used, retrySecs = b.n, int(time.Until(b.start.Add(time.Minute)).Seconds())+1
			b.mu.Unlock()
		}
		over := used > limit
		remaining := limit - used
		if retrySecs < 1 {
			retrySecs = 1
		}
		if remaining < 0 {
			remaining = 0
		}
		// 剩余额度随每个响应下发：四语字典 settings.patRateLimitHint 承诺过这组头，
		// 服务端此前一个都没发（全仓 grep X-RateLimit = 0），承诺与实现相反。
		// 三个数值都是调用方本就能观测到的语义（窗口上限、窗口内还剩几次、何时重置），
		// 不含任何内部实现细节。
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Header("X-RateLimit-Reset", strconv.Itoa(retrySecs))
		if over {
			c.Header("Retry-After", strconv.Itoa(retrySecs))
			c.AbortWithStatusJSON(429, gin.H{"error": "rate_limited"})
			return
		}
		c.Next()
	}
}

// queryList 读取可重复/逗号分隔的多值查询参数（与 tags 同一约定），去空去重后返回，
// 供 kinds/types 这类多值过滤使用。
func queryList(c *gin.Context, name string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range c.QueryArray(name) {
		for _, v := range strings.Split(raw, ",") {
			if v = strings.TrimSpace(v); v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func (h HTTP) Register(r *gin.Engine) {
	h.registerStorageAssetReferences(r)
	h.registerGroup(r.Group("/api"))
}

func (h HTTP) registerGroup(api *gin.RouterGroup) {
	s := h.Store
	// attachUser 之前的公开端点只有两个：/api/openapi.json 与 /api/version。
	// openapi.json 是**接入方的公开契约**。
	// 前端工具集、Agent 与技能仓库都把"先读 GET /api/openapi.json"写进流程，改鉴权会让
	// 这些接入方先要一张令牌才能发现契约；而 paths 清单本身不是秘密——同一份端点表也在
	// 文档站公开，隐藏它只是隐蔽性而非控制（2026-09-19 审计 S-4 的处置结论）。
	api.GET("/openapi.json", func(c *gin.Context) { c.JSON(200, OpenAPI()) })
	// 版本身份：发布、切流与回滚后第一件事是确认"线上跑的到底是哪一版"，要求登录才能问
	// 等于让运维先借另一套凭据。它只回构建期注入的版本/sha、构建时间与进程启动时间
	// （见 version.go），不含配置、凭据、数据库或主机信息，因此与 openapi.json 同列公开面。
	api.GET("/version", func(c *gin.Context) { c.JSON(200, versionInfo()) })
	// 其余任何注册都必须在 attachUser 之后——gin 的 RouterGroup.Use 只对**之后**注册的
	// 路由生效（注册时复制当时的 handler 链），插到前面会让 user(c) 恒为 nil。0be8ae9 的
	// 回归就是这么来的（/api/exchange/* 提案带合法令牌也 401）。需要身份的注册函数还应把
	// 中间件挂在自己的子组上（见 registerExchange），免得下次再被插入位置决定行为。
	// 身份只来自账号服务签发的 RS256 令牌：目录侧**只验签、不查库、不签发**。
	// 中间件本体与给其它路由组复用的管理员闸门都在 auth_gate.go。
	api.Use(attachUser(s))
	api.Use(routeLimiter(DefaultRateLimitPerMinute, s.DB))
	// 审计留痕（契约 §3）：挂在身份中间件**之后**（草稿里的 actor 要在 c.Next() 之前读得到），
	// 且在下面所有写路由注册**之前**——gin 的 RouterGroup.Use 只对之后注册的路由生效
	//（0be8ae9 的回归就是位置放错导致的）。只有 AuditActions 里登记的路由会写行。
	api.Use(auditMiddleware(s))
	// 调用日志（开发者中心「API 请求日志」）：同样挂在身份中间件之后、所有路由注册之前。
	// 只记已登录请求；读日志端点自身与文档/探针面跳过（见 requestLogMiddleware）。
	api.Use(requestLogMiddleware(s))
	// 交互式文档页是**管理面**：它们在浏览器里执行脚本、与本域同源，匿名可达等于把整份 API 面
	// 连同同源脚本执行面一起交出去（审计 S-4）。移到 attachUser 之后并要求本侧唯一的
	// admin-only 码 catalog.lifecycle.manage（与 AdminGate 同码，不新造码）。
	docs := api.Group("/docs", required(PermissionLifecycleManage))
	docs.GET("", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(200, docsHTML)
	})
	// 文档页的脚本与样式随二进制自托管（docsassets/）：页面一旦改回 CDN，就等于把主站的
	// 脚本执行权交给第三方，所以资源与页面走同一道闸门、同一份白名单。
	docs.GET("/assets/*filepath", docsAssetsHandler)
	api.GET("/swagger", required(PermissionLifecycleManage), func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(200, swaggerHTML)
	})
	// 实例间导入导出：原属模块层，随子系统拆分迁入目录包（见 exchange.go）。
	// 必须在 api.Use(attachUser) 之后：提案作者取自 user(c)，导出可见性也按它判。
	h.registerExchange(api)
	// 站内通知：读取端（收件箱/未读数/标记已读）与跨服务投递端（见 notifications_http.go）。
	// 同样必须在 attachUser 与审计中间件之后——收件人取自 user(c)，标记已读是写操作要留痕。
	h.registerNotifications(api)
	h.registerPublicSources(api)
	h.registerAdminInventory(api)
	cat := api.Group("/catalog")
	// 发布的定义文档 + 固定骨架的多语言名称。kinds 放在文档**外面**：它是骨架的显示名，
	// 不是可编辑的动态定义（放进 document 会被后台保存时当成未知键处理），但同样必须由服务端
	// 提供多语言，前端不硬编码。
	// 本人调用日志（开发者中心「API 请求日志」）：只读自己的行，按时间倒序。
	// 日志读不回写（requestLogMiddleware 跳过本路径），查看不污染列表。
	cat.GET("/developer/request-logs", requestLogEndpoint(s))
	cat.GET("/definitions", func(c *gin.Context) {
		v, err := s.Definitions(c.Request.Context())
		if err != nil {
			respond(c, nil, err)
			return
		}
		respond(c, gin.H{"etag": v.ETag, "document": v.Document, "updated_at": v.UpdatedAt, "kinds": KindNameRecords(), "relationship_rules": ReadRelationshipRules(v.Document)}, nil)
	})
	// 标签聚合：标签不是独立字典表，而是散落在各实体的 attributes.tags 中。
	// jsonb_array_elements_text 展开数组就地统计频次，供前端标签云与筛选建议；
	// 只统计已发布实体（与列表接口的匿名可见性口径一致）。
	// 分页口径：limit 越界（<=0 或 >500）静默收敛为 200，与 List 的静默收敛风格一致，
	// 不硬拒绝；与 expressions/details 的 ids 硬拒绝（400）差异是刻意的：
	// 后者是 POST body 批量参数，超限直接拒绝避免大查询拖库。
	cat.GET("/tags", func(c *gin.Context) {
		// 与 /entities 同一道参数闸门：?q=%00 在 tags 上同样会走 ILIKE 撞库错误。
		if err := validateTextQuery(c, "q"); err != nil {
			respond(c, nil, err)
			return
		}
		args := []any{}
		where := []string{"e.state='active'", "jsonb_typeof(e.document->'attributes'->'tags')='array'"}
		if q := strings.TrimSpace(c.Query("q")); q != "" {
			// 与实体搜索同口径：%/_ 由 likeContains 按字面转义。
			args = append(args, likeContains(q))
			where = append(where, fmt.Sprintf("t.name ILIKE $%d", len(args)))
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		if limit <= 0 || limit > 500 {
			limit = 200
		}
		args = append(args, limit)
		rows, err := s.DB.QueryContext(c.Request.Context(), `
		SELECT t.name, count(*) AS n
		FROM catalog.entities e,
		     jsonb_array_elements_text(e.document->'attributes'->'tags') AS t(name)
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY t.name
		ORDER BY n DESC, t.name
		LIMIT $`+strconv.Itoa(len(args)), args...)
		if err != nil {
			// 查询失败不能再回 200 空表：DB 故障看起来会像"库里没有标签"，
			// 前端标签云与筛选建议会静默变空（错误由 respond 统一成 500/database_error）。
			respond(c, nil, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				respond(c, nil, err)
				return
			}
			items = append(items, map[string]any{"name": name, "count": n})
		}
		// 迭代中途断连同样要暴露：rows.Err() 才记录 Next 的终止原因。
		if err := rows.Err(); err != nil {
			respond(c, nil, err)
			return
		}
		respond(c, gin.H{"items": items, "total": len(items)}, nil)
	})
	cat.GET("/entities", func(c *gin.Context) {
		// 参数闸门：非法 UTF-8/控制字符/超长值 400（见 query_params.go），分页契约
		// 也在这一步归一（page 与 offset 冲突即 400，不再"传了 page 却按 offset 返回"）。
		if err := validateTextQuery(c, listTextParams...); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset, err := listPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		o := ListOptions{Kind: c.Query("kind"), Query: strings.TrimSpace(c.Query("q")), State: c.Query("state"), WorkID: c.Query("work_id"), ContentUnitID: c.Query("content_unit_id"), ReleaseID: c.Query("release_id"), MediumID: c.Query("medium_id"), ParentID: c.Query("parent_id"), Field: c.Query("field"), Value: c.Query("value"), OriginalLanguage: c.Query("original_language"), HasPictures: c.Query("has_pictures") == "1", Sort: c.Query("sort"), Order: c.Query("order"), Locale: c.Query("locale"), Limit: limit, Offset: offset}
		// 排序参数走白名单校验：未知字段/方向返回 400 invalid_sort / invalid_order，
		// 而不是静默按 updated_at 返回另一套顺序（调用方会以为排序生效了）。
		if err := normalizeListSort(&o); err != nil {
			respond(c, nil, err)
			return
		}
		// kinds 支持多次出现或逗号分隔：多值命中在 SQL 侧完成。
		o.Kinds = queryList(c, "kinds")
		o.TagsMode = c.Query("tags_mode")
		if o.TagsMode != "" && o.TagsMode != "any" && o.TagsMode != "all" {
			respond(c, nil, errParam(codeInvalidQueryParam))
			return
		}
		// tags 支持多次出现或逗号分隔，默认任一命中，可选全部命中。
		for _, raw := range c.QueryArray("tags") {
			for _, tag := range strings.Split(raw, ",") {
				if t := strings.TrimSpace(tag); t != "" {
					o.Tags = append(o.Tags, t)
				}
			}
		}
		requestUser := user(c)
		if strings.TrimSpace(o.Query) != "" {
			if c.Query("cursor") != "" && (c.Query("offset") != "" || c.Query("page") != "") {
				respond(c, nil, fmt.Errorf("pagination_conflict"))
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			v, err := s.Search(ctx, o, requestUser, c.Query("cursor"))
			if err == nil {
				v.Covers, err = s.DisplayCovers(ctx, v.Items, requestUser)
			}
			respond(c, v, err)
			return
		}
		if c.Query("cursor") != "" {
			respond(c, nil, fmt.Errorf("invalid_search_cursor"))
			return
		}
		items, err := s.List(c.Request.Context(), o, requestUser)
		if err != nil {
			respond(c, nil, err)
			return
		}
		total, err := s.Count(c.Request.Context(), o, requestUser)
		if err != nil {
			respond(c, nil, err)
			return
		}
		covers, err := s.DisplayCovers(c.Request.Context(), items, requestUser)
		respond(c, gin.H{"items": items, "covers": covers, "total": total, "total_relation": "eq", "has_more": int64(offset+len(items)) < total}, err)
	})
	// 状态计数：概览卡片的待审/已发布/墓碑三个数只从这一条 GROUP BY 拿（列表端点给不出墓碑数）。
	// 闸门是 catalog.lifecycle.manage——只有它的持有者能在 /entities 列表里看全量状态，
	// 聚合口径因此不比列表多露一行；未登录 401 authentication_required、其它目录码 403 forbidden。
	// 放在 /entities/:id 之前：静态段与参数段在 gin 的路由树里是可共存的兄弟节点，
	// 顺序不影响匹配（静态优先），但先声明静态路径省得日后读代码时以为 stats 会被当成 id。
	cat.GET("/entities/stats", required(PermissionLifecycleManage), func(c *gin.Context) {
		stats, err := s.StateCounts(c.Request.Context())
		respond(c, stats, err)
	})
	cat.GET("/entities/:id", func(c *gin.Context) { e, err := s.Get(c.Request.Context(), c.Param("id"), user(c)); respond(c, e, err) })
	cat.GET("/entities/:id/quality", func(c *gin.Context) {
		v, err := s.EntityQuality(c.Request.Context(), c.Param("id"), user(c))
		respond(c, v, err)
	})
	cat.POST("/quality/batch", func(c *gin.Context) {
		var in QualityBatchRequest
		if !body(c, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		v, err := s.QualityBatch(ctx, in, user(c))
		respond(c, v, err)
	})
	cat.GET("/releases/:id/toc", func(c *gin.Context) {
		v, err := s.ReleaseTableOfContents(c.Request.Context(), c.Param("id"), user(c))
		respond(c, v, err)
	})
	cat.GET("/releases/:id/editions", func(c *gin.Context) {
		v, err := s.ReleaseEditions(c.Request.Context(), c.Param("id"), user(c))
		respond(c, v, err)
	})
	cat.GET("/expressions/:id/composition", func(c *gin.Context) {
		v, err := s.ExpressionComposition(c.Request.Context(), c.Param("id"), user(c))
		respond(c, v, err)
	})
	cat.GET("/entities/:id/resolve", func(c *gin.Context) {
		e, err := s.Resolve(c.Request.Context(), c.Param("id"), user(c))
		respond(c, e, err)
	})
	// X01 身份解析契约：存活身份 + 历史别名集合（只读投影，不改写任何行）。
	// 批量端点供跨服务按别名聚合（文件/评论/收藏），上限与 expressions/details 同口径。
	cat.GET("/entities/:id/identity", func(c *gin.Context) {
		v, err := s.ResolveIdentity(c.Request.Context(), c.Param("id"), user(c))
		respond(c, v, err)
	})
	cat.POST("/entities/identity", func(c *gin.Context) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if !body(c, &in) {
			return
		}
		ids := []string{}
		for _, id := range in.IDs {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			c.JSON(400, gin.H{"error": "invalid_payload"})
			return
		}
		if len(ids) > 500 {
			c.JSON(400, gin.H{"error": "too_many_ids"})
			return
		}
		items := map[string]IdentityResolution{}
		missing := []string{}
		for _, id := range ids {
			v, err := s.ResolveIdentity(c.Request.Context(), id, user(c))
			if err == nil {
				items[id] = v
				continue
			}
			// R2：不存在进 missing；查询失败（超时/中断/连接）整批 500，
			// 不把“查不到”伪装成成功的部分结果。
			if isIdentityNotFound(err) {
				missing = append(missing, id)
				continue
			}
			respond(c, nil, err)
			return
		}
		respond(c, gin.H{"items": items, "missing": missing}, nil)
	})
	h.registerCollaboration(cat)
	h.registerAssetPreviewRoutes(cat)
	// 生命周期只处理删除与合并；资料修订通过提交及审核流程应用。
	cat.POST("/entities/:id/lifecycle", required(PermissionLifecycleManage), func(c *gin.Context) {
		auditlog.Describe(c, auditlog.Detail{TargetType: "entity", TargetID: c.Param("id")})
		var in LifecycleEdit
		if !body(c, &in) {
			return
		}
		// 生命周期写的是终态（deleted/merged）：before 只能在这里读——写完状态就变了。
		var before *Entity
		if prev, err := s.Get(c.Request.Context(), c.Param("id"), user(c)); err == nil {
			before = &prev
		}
		e, err := s.Lifecycle(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			changes := entityChangeDetail(before, &e)
			if in.TargetID != "" {
				// 合并要一眼看出"合进了哪个实体"。
				changes["merge_target_id"] = map[string]any{"after": in.TargetID}
			}
			auditlog.Describe(c, auditlog.Detail{TargetType: "entity", TargetID: e.ID, Changes: changes})
		}
		respond(c, e, err)
	})

	cat.GET("/entities/:id/revisions", func(c *gin.Context) {
		if err := versionTextQuery(c, "version"); err != nil {
			respond(c, nil, err)
			return
		}
		q, err := versionHistoryQuery(c, false)
		if err != nil {
			respond(c, nil, err)
			return
		}
		if q.Target != "" || q.RecordID != "" {
			respond(c, nil, fmt.Errorf("invalid_version_target"))
			return
		}
		var version int64
		if raw := c.Query("version"); raw != "" {
			version, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || version < 1 {
				respond(c, nil, fmt.Errorf("invalid_version"))
				return
			}
		}
		v, err := s.Revisions(c.Request.Context(), c.Param("id"), RevisionQuery{Limit: q.Limit, Offset: q.Offset, Version: version, AuthorID: q.AuthorID, Since: q.Since, Until: q.Until}, user(c))
		respond(c, v, err)
	})
	cat.GET("/entities/:id/links", func(c *gin.Context) {
		limit, offset, err := listPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		page, err := s.EntityLinks(c.Request.Context(), c.Param("id"), limit, offset, user(c))
		respond(c, page, err)
	})
	cat.POST("/entities/candidates", func(c *gin.Context) {
		var in IdentityCandidateQuery
		if !body(c, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		v, err := s.FindIdentityCandidates(ctx, in, user(c))
		respond(c, v, err)
	})
	cat.POST("/entities/candidates/batch", func(c *gin.Context) {
		var in IdentityBatchRequest
		if !body(c, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		v, err := s.FindIdentityCandidatesBatch(ctx, in, user(c))
		respond(c, v, err)
	})
	cat.POST("/relationships/query", func(c *gin.Context) {
		var in RelationshipQueryRequest
		if !body(c, &in) {
			return
		}
		v, err := s.QueryRelationships(c.Request.Context(), in, user(c))
		respond(c, v, err)
	})
	cat.GET("/entities/:id/occurrences", func(c *gin.Context) {
		v, err := s.Occurrences(c.Request.Context(), c.Param("id"), user(c))
		respond(c, gin.H{"items": v}, err)
	})
	// 发行详情页批量上屏：一次取多条表达实体 + 自身收录 + 同篇目兄弟收录 + 署名，
	// 替代逐条四类 N+1 请求。用 POST + JSON body 传 ids：300 个 UUID 拼进 GET
	// query 约 11KB，会超过 Nginx 默认 8KB 请求行限制。
	cat.POST("/expressions/details", func(c *gin.Context) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if !body(c, &in) {
			return
		}
		ids := []string{}
		for _, id := range in.IDs {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			c.JSON(400, gin.H{"error": "invalid_payload"})
			return
		}
		if len(ids) > 500 {
			c.JSON(400, gin.H{"error": "too_many_ids"})
			return
		}
		v, err := s.ExpressionDetailsBatch(c.Request.Context(), ids, user(c))
		// 响应含 items（按表达聚合，收录以引用 id 呈现）与共享 entities 表。
		respond(c, v, err)
	})
	cat.GET("/external-databases", func(c *gin.Context) {
		if err := validateTextQuery(c, "category"); err != nil {
			respond(c, nil, err)
			return
		}
		v, err := s.ListExternalDatabases(c.Request.Context(), c.Query("category"), true)
		respond(c, gin.H{"items": v}, err)
	})
	cat.GET("/shelves", func(c *gin.Context) {
		v, err := s.ListShelves(c.Request.Context(), true)
		respond(c, gin.H{"items": v}, err)
	})
	// /shelves/feed 一次返回每个分区及其求值后的条目，供首页直接渲染。
	// 规则里的 fields/vocab_terms/relations 只有服务端能判定，放在这里避免前端近似匹配。
	// 登录用户按个人偏好合并：sections 覆盖同名系统货架/追加自建分区，order 重排，
	// hidden 过滤；每条 shelf 带 source（system/custom）供前端决定能否删除。
	// 匿名与未设置偏好者按 sort_order 默认序，且只有 system。
	cat.GET("/shelves/feed", func(c *gin.Context) {
		perShelf, _ := strconv.Atoi(c.Query("per_shelf"))
		shelves, err := s.ListShelves(c.Request.Context(), true)
		if err != nil {
			respond(c, nil, err)
			return
		}
		prefs := HomePreferences{}
		if u := user(c); u != nil {
			prefs, err = s.GetHomePreferences(c.Request.Context(), u.ID)
			if err != nil {
				respond(c, nil, err)
				return
			}
		}
		shelves = applyHomePreferences(shelves, prefs)
		out := make([]gin.H, 0, len(shelves))
		coverItems := []Entity{}
		for _, sh := range shelves {
			items, ierr := s.ListShelfItems(c.Request.Context(), sh, perShelf, user(c))
			if ierr != nil {
				respond(c, nil, ierr)
				return
			}
			// total 是该规则在当前调用者可见范围内命中的真实总数（不受 per_shelf 限制），
			// 供首页数量徽标显示实际筛选结果。
			total, cerr := s.CountShelfItems(c.Request.Context(), sh, user(c))
			if cerr != nil {
				respond(c, nil, cerr)
				return
			}
			out = append(out, gin.H{"shelf": sh, "items": items, "total": total})
			coverItems = append(coverItems, items...)
		}
		covers, err := s.DisplayCovers(c.Request.Context(), coverItems, user(c))
		respond(c, gin.H{"items": out, "covers": covers}, err)
	})
	// 个人首页偏好读：需登录，未登录返回 401（与 required("") 语义一致，
	// 不再用匿名 404 误导前端走“未找到”分支）。sections 为"覆盖 + 自建"列表，
	// slug 与系统货架同名表示覆盖本人视角（不是冲突），不同名表示新增分区。
	cat.GET("/me/home-preferences", required(""), func(c *gin.Context) {
		v, err := s.GetHomePreferences(c.Request.Context(), user(c).ID)
		respond(c, v, err)
	})
	// 个人首页偏好写：需登录、不限管理员，与读端对称。
	cat.PUT("/me/home-preferences", required(""), func(c *gin.Context) {
		uid := user(c).ID
		// 自服务写也留痕（契约要求"全部写操作"）：被动对象是这名用户自己的偏好行。
		auditlog.Describe(c, auditlog.Detail{TargetType: "preference", TargetID: uid})
		var in HomePreferences
		if !body(c, &in) {
			return
		}
		var before *HomePreferences
		if prev, err := s.GetHomePreferences(c.Request.Context(), uid); err == nil {
			before = &prev
		}
		v, err := s.SaveHomePreferences(c.Request.Context(), uid, in)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "preference", TargetID: uid, Changes: homePreferencesDetail(before, &v)})
		}
		respond(c, v, err)
	})
	// Public contribution activity contains only changes merged into visible catalog facts.
	// The year is interpreted in UTC; private workspace activity is never returned.
	api.GET("/users/:id/contributions", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		year := 0
		if raw, present := c.GetQuery("year"); present {
			var err error
			year, err = strconv.Atoi(raw)
			if err != nil || year == 0 {
				respond(c, nil, fmt.Errorf("invalid_year"))
				return
			}
		}
		page, _ := strconv.Atoi(c.Query("page"))
		pageSize, _ := strconv.Atoi(c.Query("page_size"))
		v, err := s.UserContributions(c.Request.Context(), c.Param("id"), ContributionQuery{Year: year, Page: page, PageSize: pageSize, From: c.Query("from"), To: c.Query("to")})
		respond(c, v, err)
	})
	cat.GET("/compare", func(c *gin.Context) {
		v, err := s.Compare(c.Request.Context(), strings.Split(c.Query("ids"), ","), user(c))
		respond(c, gin.H{"items": v}, err)
	})
	// 外部来源只读查询：出站抓取有放大面，要求登录（required("")）。
	// 出站站点限速见 outbound_throttle.go（Wait），与入站 rate_budget 无关。
	extSrc := api.Group("/external-sources", required(""))
	extSrc.GET("", func(c *gin.Context) {
		v, err := s.ExternalSources(c.Request.Context())
		respond(c, gin.H{"items": v}, err)
	})
	extSrc.POST("/query", func(c *gin.Context) {
		var in ExternalSourceQueryRequest
		if !body(c, &in) {
			return
		}
		v, err := s.QueryExternalSource(c.Request.Context(), in)
		respond(c, v, err)
	})
	defs := api.Group("/admin/catalog-definitions", required(PermissionDefinitionsManage))
	defs.GET("", func(c *gin.Context) {
		v, err := s.Definitions(c.Request.Context())
		respond(c, v, err)
	})
	defs.POST("/impact", func(c *gin.Context) {
		var in struct {
			Document Definitions `json:"document"`
		}
		if !body(c, &in) {
			return
		}
		v, err := s.DefinitionImpactFor(c.Request.Context(), in.Document, *user(c))
		respond(c, v, err)
	})
	defs.PUT("", func(c *gin.Context) {
		auditlog.Describe(c, auditlog.Detail{TargetType: "definition", TargetID: "definitions"})
		var in struct {
			Document     Definitions `json:"document"`
			ExpectedETag string      `json:"expected_etag"`
			EditNote     string      `json:"edit_note"`
			Sources      []Source    `json:"sources"`
		}
		if !body(c, &in) {
			return
		}
		v, err := s.SaveDefinitions(c.Request.Context(), in.Document, in.ExpectedETag, *user(c), in.EditNote, in.Sources)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "definition", TargetID: "definitions", Changes: map[string]any{"document_counts": map[string]any{"after": definitionsSummary(in.Document)}}})
		}
		respond(c, v, err)
	})
	// 限流配置是运行参数，不是元数据定义：单独一组端点、单独一张单例表，
	// 因此调整配额不会改动 /catalog/definitions 的 etag，也不会触发元数据影响面回放。
	// 权限沿用 catalog.definitions.manage（"平台配置管理"）——新增权限码需要账号服务
	// 同步播种，跨仓改动不属于本次范围，而复用已播种的管理码不会漏授。
	limits := api.Group("/admin/rate-limits", required(PermissionDefinitionsManage))
	limits.GET("", func(c *gin.Context) {
		v, err := s.RateLimitPolicy(c.Request.Context())
		respond(c, v, err)
	})
	limits.PUT("", func(c *gin.Context) {
		auditlog.Describe(c, auditlog.Detail{TargetType: "rate_limits", TargetID: "rate-limits"})
		var in RateLimitSave
		if !body(c, &in) {
			return
		}
		v, err := s.SaveRateLimitPolicy(c.Request.Context(), in.Policy, in.ExpectedETag, *user(c), in.EditNote, in.Sources)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "rate_limits", TargetID: "rate-limits", Changes: map[string]any{"policy_counts": map[string]any{"after": rateLimitSummary(in.Policy)}}})
		}
		respond(c, v, err)
	})
	ext := api.Group("/admin/external-databases", required(PermissionDefinitionsManage))
	ext.GET("", func(c *gin.Context) {
		if err := validateTextQuery(c, "category"); err != nil {
			respond(c, nil, err)
			return
		}
		v, err := s.ListExternalDatabases(c.Request.Context(), c.Query("category"), false)
		respond(c, gin.H{"items": v}, err)
	})
	ext.POST("", func(c *gin.Context) {
		auditlog.Describe(c, auditlog.Detail{TargetType: "external_database"})
		var in ExternalDatabase
		if !body(c, &in) {
			return
		}
		v, err := s.CreateExternalDatabase(c.Request.Context(), in)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "external_database", TargetID: v.Code, Changes: externalDatabaseDetail(nil, &v)})
		}
		respond(c, gin.H{"message": "created", "data": v}, err)
	})
	ext.PUT("/:code", func(c *gin.Context) {
		code := c.Param("code")
		auditlog.Describe(c, auditlog.Detail{TargetType: "external_database", TargetID: code})
		var in ExternalDatabase
		if !body(c, &in) {
			return
		}
		var before *ExternalDatabase
		if prev := externalDatabaseBefore(c, s, code); prev != nil {
			before = prev
		}
		v, err := s.UpdateExternalDatabase(c.Request.Context(), code, in)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "external_database", TargetID: v.Code, Changes: externalDatabaseDetail(before, &v)})
		}
		respond(c, gin.H{"message": "updated", "data": v}, err)
	})
	ext.DELETE("/:code", func(c *gin.Context) {
		code := c.Param("code")
		auditlog.Describe(c, auditlog.Detail{TargetType: "external_database", TargetID: code})
		// 删除只留 before（"删掉的是什么"）：删完就查不到了。
		before := externalDatabaseBefore(c, s, code)
		err := s.DeleteExternalDatabase(c.Request.Context(), code)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "external_database", TargetID: code, Changes: externalDatabaseDetail(before, nil)})
		}
		respond(c, gin.H{"message": "deleted"}, err)
	})
	shelves := api.Group("/admin/shelves", required(PermissionShelvesManage))
	shelves.GET("", func(c *gin.Context) {
		v, err := s.ListShelves(c.Request.Context(), false)
		respond(c, gin.H{"items": v}, err)
	})
	shelves.POST("", func(c *gin.Context) {
		auditlog.Describe(c, auditlog.Detail{TargetType: "shelf"})
		var in Shelf
		if !body(c, &in) {
			return
		}
		v, err := s.CreateShelf(c.Request.Context(), in)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "shelf", TargetID: strconv.FormatInt(v.ID, 10), Changes: shelfChangeDetail(nil, &v)})
		}
		respond(c, gin.H{"message": "created", "data": v}, err)
	})
	shelves.GET("/:id", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		v, err := s.GetShelf(c.Request.Context(), id)
		respond(c, gin.H{"data": v}, err)
	})
	shelves.PUT("/:id", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		auditlog.Describe(c, auditlog.Detail{TargetType: "shelf", TargetID: c.Param("id")})
		var in Shelf
		if !body(c, &in) {
			return
		}
		var before *Shelf
		if prev, gerr := s.GetShelf(c.Request.Context(), id); gerr == nil {
			before = &prev
		}
		v, err := s.UpdateShelf(c.Request.Context(), id, in)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "shelf", TargetID: strconv.FormatInt(v.ID, 10), Changes: shelfChangeDetail(before, &v)})
		}
		respond(c, gin.H{"message": "updated", "data": v}, err)
	})
	shelves.DELETE("/:id", func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		auditlog.Describe(c, auditlog.Detail{TargetType: "shelf", TargetID: c.Param("id")})
		// 删除只留 before（"删掉的是什么"）：删完就查不到了。
		var before *Shelf
		if prev, gerr := s.GetShelf(c.Request.Context(), id); gerr == nil {
			before = &prev
		}
		err := s.DeleteShelf(c.Request.Context(), id)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "shelf", TargetID: c.Param("id"), Changes: shelfChangeDetail(before, nil)})
		}
		respond(c, gin.H{"message": "deleted"}, err)
	})
}

const docsHTML = `<!doctype html>
<html lang="zh-CN">
  <head>
    <title>MetaFusion API 交互式文档 (Scalar)</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <style>
      body { margin: 0; padding: 0; background: #0b0f19; }
    </style>
  </head>
  <body>
    <script
      id="api-reference"
      data-url="/api/openapi.json"
      data-configuration='{"theme": "purple", "hideModels": false, "showSidebar": true}'>
    </script>
    <script src="/api/docs/assets/scalar-standalone.js"></script>
  </body>
</html>`

const swaggerHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <title>MetaFusion API 文档 (Swagger UI)</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="icon" type="image/svg+xml" href="/favicon.svg">
  <link rel="stylesheet" href="/api/docs/assets/swagger-ui.css">
  <style>
    body { margin: 0; padding: 0; background: #fafafa; }
    .swagger-ui .topbar { display: none; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/api/docs/assets/swagger-ui-bundle.js"></script>
  <script src="/api/docs/assets/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      SwaggerUIBundle({
        url: "/api/openapi.json",
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          // 预设文件定义的是全局 SwaggerUIStandalonePreset（UMD 导出），
          // SwaggerUIBundle 上并没有这个属性：原先写成 SwaggerUIBundle.SwaggerUIStandalonePreset
          // 传进去的是 undefined。按 swagger-ui-dist 自己的 index.html 取全局，页面才真的渲染。
          SwaggerUIStandalonePreset
        ],
        layout: "BaseLayout"
      });
    };
  </script>
</body>
</html>`
