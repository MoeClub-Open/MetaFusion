package catalog

// 审计留痕接线（契约 §2/§3）：写路由 → 动作码注册表、豁免表、操作者解析、变更摘要。
//
// 本包已有同名的私有函数 audit()（修订留痕，见 store.go），所以审计包在本包内一律以
// auditlog 别名引入；包本身的 API（audit.Middleware/Options/Describe/Fail/SanitizeChanges…）
// 与另外三个服务逐字一致，改名只发生在调用点。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

// AuditActions 是写路由 → 动作码注册表（契约 §2/§3）：键是 gin 的路由模板（c.FullPath()），
// 只有登记在册的请求才写审计行。动作码只增不改——改名等于改历史。
// 覆盖守卫测试（audit_routes_test.go）遍历真实路由树，保证新增写端点不会漏登记。
func AuditActions() map[string]string {
	return map[string]string{
		"POST /api/catalog/commits/:id/review":              "commit.reviewed",
		"POST /api/catalog/commits":                         "commit.pushed",
		"POST /api/catalog/entities":                        "entity.created",
		"PUT /api/catalog/entities/:id":                     "entity.updated",
		"POST /api/catalog/tracks/:id/contents":             "inclusion.created",
		"PUT /api/catalog/tracks/:id/contents/:position":    "inclusion.updated",
		"DELETE /api/catalog/tracks/:id/contents/:position": "inclusion.deleted",
		"POST /api/catalog/entities/:id/lifecycle":          "entity.lifecycle_changed",
		"PUT /api/catalog/me/home-preferences":              "preference.home_updated",
		"POST /api/catalog/relations":                       "relation.created",
		"PUT /api/catalog/relations/:id":                    "relation.updated",
		"DELETE /api/catalog/relations/:id":                 "relation.deleted",
		"PUT /api/admin/catalog-definitions":                "definition.updated",
		"PUT /api/admin/rate-limits":                        "rate_limit.updated",
		"POST /api/admin/external-databases":                "external_database.created",
		"PUT /api/admin/external-databases/:code":           "external_database.updated",
		"DELETE /api/admin/external-databases/:code":        "external_database.deleted",
		"POST /api/admin/shelves":                           "shelf.created",
		"PUT /api/admin/shelves/:id":                        "shelf.updated",
		"DELETE /api/admin/shelves/:id":                     "shelf.deleted",
		// 站内通知（2026-09-20）：标记已读是自服务的写操作，同样要留痕；
		// internal 投递端由互动服务调用，审计行的 actor 是转发过来的终端用户令牌。
		"POST /api/notifications/:id/read": "notification.read",
		"POST /api/notifications/read-all": "notification.all_read",
		"POST /api/notifications/internal": "notification.delivered",
	}
}

// AuditExempt 是"用了写方法但没有写入语义"的路由 → 豁免理由（契约 §6.3）。
// 运行期不读它（未登记动作码的路由本就不写行），它由覆盖守卫测试读取：
// "这条写路由为什么不记"必须有据可查，而不是被人忘掉。模块开关墓碑
func AuditExempt() map[string]string {
	return map[string]string{
		"POST /api/catalog/checkout":                  "版本化工作副本：只读重复读事务",
		"POST /api/catalog/commits/preview":           "运行写入校验后整笔回滚，提交、修订、消息、outbox 均不持久化",
		"POST /api/catalog/entities/candidates":       "身份候选查询：POST 承载查重条件，在同一只读快照内查找并解析身份，不写目录数据",
		"POST /api/catalog/entities/candidates/batch": "批量身份候选查询：POST 承载最多 50 条查重条件，在同一只读快照内查找并评估（context 只排名不筛选），零写入",
		"POST /api/catalog/expressions/details":       "批量读：POST 只为把最多 500 个 id 放进 body（GET query 会撞 8KB 请求行上限），零写入",
		"POST /api/catalog/relationships/query":       "批量关系查询：POST 承载实体集合与筛选条件，目录数据仅在只读事务中读取，零写入",
		"POST /api/catalog/quality/batch":             "批量质量审计：POST 承载最多 100 个 id，按需计算、只读事务、不落库，零写入",
		"POST /api/catalog/entities/identity":         "批量身份解析（X01 别名集合的只读投影）：POST 只为把最多 500 个 id 放进 body，零写入",
		"POST /api/admin/catalog-definitions/impact":  "定义影响检查：只读回放提交的文档，零写入",
		"POST /api/external-sources/query":            "外部来源只读查询：出站抓取原文与预览映射，不写目录",
	}
}

// DirectoryActor 解析操作者：身份由 attachUser / AdminGate 放进 catalog_user。
// 目录侧只验签、不查账号库，区分不出会话令牌与 OAuth 令牌（契约 §7），能给的只有
// pat（PAT 内省命中，FromPAT）与 session（其余已登录请求）这两个近似值；未登录为 anonymous。
func DirectoryActor(c *gin.Context) auditlog.Actor {
	u := user(c)
	if u == nil {
		return auditlog.Actor{CredentialType: "anonymous"}
	}
	credential := "session"
	if u.FromPAT {
		credential = "pat"
	}
	return auditlog.Actor{UserID: u.ID, Username: u.Username, CredentialType: credential}
}

// auditMiddleware 给 /api 组接线：必须在 attachUser 之后（草稿在 c.Next() 之前读身份）、
// 在所有写路由注册之前（gin 的 RouterGroup.Use 只对之后注册的路由生效）。
func auditMiddleware(s *Store) gin.HandlerFunc {
	return auditlog.Middleware(auditlog.Options{
		Recorder: s.Audit,
		Actions:  AuditActions(),
		Exempt:   AuditExempt(),
		Actor:    DirectoryActor,
	})
}

// summaryFields 生成"字段 → {before, after}"摘要。两侧都有且相同则整条不记——摘要只描述
// 真实变化，不然每次编辑都会重复一堆没变的字段。before/after 传空切片表示该侧缺失
// （新建没有 before；旧值读不到时也不硬造一个假的 before）。
func summaryFields(names []string, before, after []string) map[string]any {
	out := map[string]any{}
	for i, name := range names {
		hasBefore, hasAfter := i < len(before), i < len(after)
		switch {
		case hasBefore && hasAfter && before[i] == after[i]:
			continue
		case hasBefore && hasAfter:
			out[name] = map[string]any{"before": before[i], "after": after[i]}
		case hasAfter:
			out[name] = map[string]any{"after": after[i]}
		case hasBefore:
			out[name] = map[string]any{"before": before[i]}
		}
	}
	return out
}

// entityChangeDetail 是实体变更摘要：只记 kind / status / title（契约要求的最少信息，
// 题名可读且不算敏感），不塞整份 attributes——体积与脱敏都不可控。
func entityChangeDetail(before, after *Entity) map[string]any {
	var b, a []string
	if before != nil {
		b = []string{before.Kind, before.Status, before.Title}
	}
	if after != nil {
		a = []string{after.Kind, after.Status, after.Title}
	}
	return summaryFields([]string{"kind", "state", "title"}, b, a)
}

// relationChangeDetail 是关系变更摘要。关系的 type/source_id/target_id 在服务端是**不可变**的
// （SaveRelation 的 immutable_scope 校验），能变的只有 attributes——所以摘要里必须有 attributes，
// 否则每次"改关系"都会得到一份空摘要。attributes 逐键对比，只记真正变化的键。
func relationChangeDetail(before, after *Relation) map[string]any {
	var b, a []string
	if before != nil {
		b = []string{before.Type, before.SourceID, before.TargetID}
	}
	if after != nil {
		a = []string{after.Type, after.SourceID, after.TargetID}
	}
	changes := summaryFields([]string{"type", "source_id", "target_id"}, b, a)
	attrs := map[string]any{}
	beforeAttrs, afterAttrs := map[string]any{}, map[string]any{}
	if before != nil {
		beforeAttrs = before.Attributes
	}
	if after != nil {
		afterAttrs = after.Attributes
	}
	keys := make([]string, 0, len(beforeAttrs)+len(afterAttrs))
	for k := range beforeAttrs {
		keys = append(keys, k)
	}
	for k := range afterAttrs {
		if _, seen := beforeAttrs[k]; !seen {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		bv, hasBefore := beforeAttrs[k]
		av, hasAfter := afterAttrs[k]
		switch {
		case hasBefore && hasAfter && fmt.Sprint(bv) == fmt.Sprint(av):
		case hasBefore && hasAfter:
			attrs[k] = map[string]any{"before": bv, "after": av}
		case hasAfter:
			attrs[k] = map[string]any{"after": av}
		case hasBefore:
			attrs[k] = map[string]any{"before": bv}
		}
	}
	if len(attrs) > 0 {
		changes["attributes"] = attrs
	}
	return changes
}

// shelfChangeDetail 是货架变更摘要：slug 是人的可读标识，enabled / sort_order 决定它是否
// 出现在首页与排在第几（query 规则是配置块，改动由版本化的管理界面负责，不进摘要）。
func shelfChangeDetail(before, after *Shelf) map[string]any {
	pick := func(s *Shelf) []string {
		if s == nil {
			return nil
		}
		return []string{s.Slug, strconv.FormatBool(s.Enabled), strconv.Itoa(s.SortOrder)}
	}
	return summaryFields([]string{"slug", "enabled", "sort_order"}, pick(before), pick(after))
}

// externalDatabaseDetail 是外部数据库预设的变更摘要：code / category 决定它出现在哪类详情页，
// url_pattern 是外链模板（改它就是改链接目的地），is_enabled 决定是否对外可见。
func externalDatabaseDetail(before, after *ExternalDatabase) map[string]any {
	pick := func(e *ExternalDatabase) []string {
		if e == nil {
			return nil
		}
		return []string{e.Code, e.Category, e.URLPattern, strconv.FormatBool(e.IsEnabled)}
	}
	return summaryFields([]string{"code", "category", "url_pattern", "is_enabled"}, pick(before), pick(after))
}

// definitionsSummary 是定义文档的规模摘要（各分区条目数）：审计要回答的是"这一版动了多大"，
// 不是"文档全文是什么"——整份定义文档既不合适进审计行，也不该用 8KB 截断去猜。
func definitionsSummary(d Definitions) map[string]int {
	return map[string]int{
		"fields":       len(d.Fields),
		"vocabularies": len(d.Vocabularies),
		"relations":    len(d.Relations),
		"templates":    len(d.Templates),
	}
}

// externalDatabaseBefore 按 code 取现有预设作为"变更前"值：目录侧只有列表入口
// （ListExternalDatabases），没有按 code 取单行的入口；这张表只有几十行，列表扫描足够。
// 读不到（或列表读失败）就返回 nil —— 不硬造 before，摘要少一侧也不影响判定。
func externalDatabaseBefore(c *gin.Context, s *Store, code string) *ExternalDatabase {
	items, err := s.ListExternalDatabases(c.Request.Context(), "", false)
	if err != nil {
		return nil
	}
	for i := range items {
		if items[i].Code == code {
			return &items[i]
		}
	}
	return nil
}

// homePreferencesDetail 是首页偏好（自服务写）的变更摘要：只记分区 slug 列表，
// 不记用户的 query 规则内容——审计要回答"谁改了首页排版"，不是复刻这份配置。
func homePreferencesDetail(before, after *HomePreferences) map[string]any {
	pick := func(p *HomePreferences) (order, hidden, sections []string, ok bool) {
		if p == nil {
			return nil, nil, nil, false
		}
		for _, sec := range p.Sections {
			sections = append(sections, sec.Slug)
		}
		return p.Order, p.Hidden, sections, true
	}
	changes := map[string]any{}
	bOrder, bHidden, bSections, hasBefore := pick(before)
	aOrder, aHidden, aSections, hasAfter := pick(after)
	for _, f := range []struct {
		name          string
		before, after []string
	}{
		{"order", bOrder, aOrder},
		{"hidden", bHidden, aHidden},
		{"sections", bSections, aSections},
	} {
		switch {
		case hasBefore && hasAfter && strings.Join(f.before, "\x00") == strings.Join(f.after, "\x00"):
		case hasBefore && hasAfter:
			changes[f.name] = map[string]any{"before": f.before, "after": f.after}
		case hasAfter:
			changes[f.name] = map[string]any{"after": f.after}
		case hasBefore:
			changes[f.name] = map[string]any{"before": f.before}
		}
	}
	return changes
}
