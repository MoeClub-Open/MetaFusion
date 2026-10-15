package catalog

// 写路由覆盖守卫（跨服务审计契约 §6.3）：遍历真实路由树取所有 POST/PUT/PATCH/DELETE，
// 每条要么在动作码注册表里，要么在豁免表里且带一句理由。新增写端点忘了登记 → 本用例失败。

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// actionCodePattern 是契约 §2 的命名：<域>.<过去式动作>，全小写 + 下划线。
var actionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

var writeMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// auditRegistry 是本包的写路由 → 动作码表。引擎级写路由（不在 /api 组里）必须在这里补登记，
// 否则下面的覆盖守卫看不见它。
func auditRegistry() (actions, exempt map[string]string) {
	return AuditActions(), AuditExempt()
}

func TestEveryWriteRouteIsRegisteredOrReasonedExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 与 cmd/server/main.go 同一装配。例外：/healthz、/health、/ready 是 GET，与本守卫无关；
	// 以后若在引擎上直接注册写路由（而不是挂在 /api 组里），必须在这里补上，否则守卫看不见它。
	engine := gin.New()
	HTTP{Store: &Store{}}.Register(engine)

	actions, exempt := auditRegistry()
	seen := map[string]bool{}
	for _, route := range engine.Routes() {
		if !writeMethods[route.Method] {
			continue
		}
		key := route.Method + " " + route.Path
		seen[key] = true
		action, registered := actions[key]
		reason, isExempt := exempt[key]
		switch {
		case registered && isExempt:
			t.Errorf("%s 既登记了动作码（%s）又在豁免表里（理由：%s）：两者只能取一个", key, action, reason)
		case !registered && !isExempt:
			t.Errorf("%s 是写路由，但既没有动作码也没有豁免理由（契约 §6.3）", key)
		}
		if registered && !actionCodePattern.MatchString(action) {
			t.Errorf("%s 的动作码 %q 不符合 §2 命名（<域>.<过去式动作>，全小写下划线）", key, action)
		}
		if isExempt && strings.TrimSpace(reason) == "" {
			t.Errorf("%s 的豁免必须写明理由", key)
		}
	}
	// 反向：表里不能留路由树里已不存在的键（端点改名/删除后表要跟着改）。
	for key := range actions {
		if !seen[key] {
			t.Errorf("动作码表里的 %s 在路由树里不存在：端点已改名或删除", key)
		}
	}
	for key := range exempt {
		if !seen[key] {
			t.Errorf("豁免表里的 %s 在路由树里不存在：端点已改名或删除", key)
		}
	}
	// 遍历本身不能空转：写路由数掉到这个数以下说明路由树没建起来，用例会假绿。
	if len(seen) < 20 {
		t.Fatalf("只遍历到 %d 条写路由，路由树可能没建起来: %v", len(seen), seen)
	}
	// 一个动作码只对应一条路由：同码多路由会让"按动作码聚合"失去意义。
	byCode := map[string][]string{}
	for key, code := range actions {
		byCode[code] = append(byCode[code], key)
	}
	for code, keys := range byCode {
		if len(keys) > 1 {
			t.Errorf("动作码 %s 对应多条路由 %v", code, keys)
		}
	}
}

// 动作码是历史（契约 §1：只增不改，改名等于改历史），所以把当前清单钉成黄金表：
// 改任何一个已有码都会让本用例失败——那是要契约层面批准的变更，不是顺手重命名。
func TestAuditActionCodesAreStable(t *testing.T) {
	want := map[string]string{
		"POST /api/catalog/commits/:id/review":              "commit.reviewed",
		"POST /api/catalog/commits":                         "commit.pushed",
		"POST /api/catalog/entities":                        "entity.created",
		"PUT /api/catalog/entities/:id":                     "entity.updated",
		"POST /api/catalog/entities/:id/lifecycle":          "entity.lifecycle_changed",
		"PUT /api/catalog/me/home-preferences":              "preference.home_updated",
		"POST /api/catalog/relations":                       "relation.created",
		"PUT /api/catalog/relations/:id":                    "relation.updated",
		"DELETE /api/catalog/relations/:id":                 "relation.deleted",
		"POST /api/catalog/tracks/:id/contents":             "inclusion.created",
		"PUT /api/catalog/tracks/:id/contents/:position":    "inclusion.updated",
		"DELETE /api/catalog/tracks/:id/contents/:position": "inclusion.deleted",
		"PUT /api/admin/catalog-definitions":                "definition.updated",
		"PUT /api/admin/rate-limits":                        "rate_limit.updated",
		"POST /api/admin/external-databases":                "external_database.created",
		"PUT /api/admin/external-databases/:code":           "external_database.updated",
		"DELETE /api/admin/external-databases/:code":        "external_database.deleted",
		"POST /api/admin/shelves":                           "shelf.created",
		"PUT /api/admin/shelves/:id":                        "shelf.updated",
		"DELETE /api/admin/shelves/:id":                     "shelf.deleted",
		"POST /api/notifications/:id/read":                  "notification.read",
		"POST /api/notifications/read-all":                  "notification.all_read",
		"POST /api/notifications/internal":                  "notification.delivered",
	}
	got, _ := auditRegistry()
	if len(got) != len(want) {
		t.Fatalf("动作码条数变了：got %d want %d（新增写端点要同时补这里的黄金表）", len(got), len(want))
	}
	for key, code := range want {
		if got[key] != code {
			t.Errorf("%s 的动作码变了：got %q want %q（动作码只增不改）", key, got[key], code)
		}
	}
}

// 豁免必须真的是"零写入"：这里把豁免端点的语义钉住，免得以后有人把写逻辑挪进它们，
// 却因为在豁免表里而静默不留痕。
func TestAuditExemptRoutesAreReadOnlyByDesign(t *testing.T) {
	exempt := AuditExempt()
	if len(exempt) != 10 {
		t.Fatalf("豁免表条数变了（%d）：新增豁免要在测试里说明它为什么零写入", len(exempt))
	}
	// relationships/query 与 entities/candidates(/batch) 仅在 repeatable-read 的只读事务中读目录投影。 quality/batch 同样只在只读事务中按需计算质量诊断，不落库。external-sources/query 只出站抓取，不写目录。
	for _, key := range []string{"POST /api/catalog/checkout", "POST /api/catalog/commits/preview", "POST /api/catalog/expressions/details", "POST /api/external-sources/query", "POST /api/catalog/entities/identity", "POST /api/catalog/entities/candidates", "POST /api/catalog/entities/candidates/batch", "POST /api/catalog/relationships/query", "POST /api/catalog/quality/batch", "POST /api/admin/catalog-definitions/impact"} {
		if strings.TrimSpace(exempt[key]) == "" {
			t.Fatalf("%s 必须带豁免理由", key)
		}
	}
}
