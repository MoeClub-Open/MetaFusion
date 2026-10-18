package catalog

// API 限流策略：进程外部配置 + 纯解析函数 + 进程内缓存。
//
// 为什么单独一份文档而不是塞进 definitions：/api/catalog/definitions 是匿名可读的
// 元数据契约，把"哪个账号有多少配额"放进去等于公开账户清单，而且每次配额调整都会
// 改动元数据 etag、触发一次对十万级实体的影响面回放。限流是运行配置，不是元数据。
//
// 缓存口径与 definitionsCache 一致：轻量 etag 检查，读到变化才反序列化整份文档；
// 限流器只读进程内快照，不在请求路径上查库。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// rateLimitAnonymousGroup 是匿名主体的保留组码：未登录请求的主体是其 IP，
// 没有 groups，运维仍需一个能单独收紧/放宽未登录流量的开关。
const rateLimitAnonymousGroup = "anonymous"

const DefaultRateLimitPerMinute = 180

const (
	// rateLimitMaxPerMinute 是单个额度上限：超过它一定是填错（把日配额写进分钟位），
	// 放行会让一次误填把限流整体作废、且从配置上完全看不出来。
	rateLimitMaxPerMinute = 1000000
	// 条目上限：文档整体进内存并被每次解析读取，无上限会让写接口与限流热路径
	// 同时被一份超大 JSON 拖住。
	rateLimitMaxGroups   = 200
	rateLimitMaxAccounts = 2000
	// rateLimitMaxKeyLen 限制主体键长度：超长键只可能是粘贴事故。
	rateLimitMaxKeyLen = 200
)

// rateLimitLockKey 是限流配置写入协调的事务级 advisory 键。与身份协调锁、
// 定义锁 740203、发行类锁 740204、审计契约 740205、migrator 88481001 都不同键：
// 它只保护 catalog.rate_limit_policy 这一张单例表。
const rateLimitLockKey = 740206

// Resolve 解析一次请求实际生效的额度。
//
// identities 是按优先级排列的主体标识（用户 id、用户名……），第一个命中的账号规则生效；
// groups 是令牌里的用户组码；anonymous 为真表示未登录。routeDefault 是调用方在代码里
// 写死的路由内置额度，也是没有任何配置时的返回值——因此空文档下的行为与引入本策略前逐字一致。
//
// 返回的 unlimited 为真时，调用方必须完全跳过计数（"解除限制"），limit 无意义。
func (p RateLimitPolicy) Resolve(identities, groups []string, anonymous bool, routeDefault int) (limit int, unlimited bool) {
	if anonymous {
		// 匿名只有保留组码可用：它没有账号，也没有组。
		if r, ok := p.Groups[rateLimitAnonymousGroup]; ok {
			if r.Unlimited {
				return 0, true
			}
			if r.PerMinute > 0 {
				return r.PerMinute, false
			}
		}
	} else {
		for _, id := range identities {
			if id == "" {
				continue
			}
			if r, ok := p.Accounts[id]; ok {
				if r.Unlimited {
					return 0, true
				}
				if r.PerMinute > 0 {
					return r.PerMinute, false
				}
			}
		}
		// 默认管理员无限，但显式账号规则和 admin 组规则可收回该默认。
		if contains(groups, "admin") {
			if _, explicit := p.Groups["admin"]; !explicit {
				return 0, true
			}
		}
		// 组是"授予"的集合：多组命中取最宽松的一项。取交集（或取最小值）会让
		// 多加入一个组反而变慢，与"组只增权限"的直觉相反。
		best, found := 0, false
		for _, g := range groups {
			r, ok := p.Groups[g]
			if !ok {
				continue
			}
			if r.Unlimited {
				return 0, true
			}
			if r.PerMinute > best {
				best, found = r.PerMinute, true
			}
		}
		if found {
			return best, false
		}
	}
	if p.DefaultUnlimited {
		return 0, true
	}
	if p.DefaultPerMinute > 0 {
		return p.DefaultPerMinute, false
	}
	return routeDefault, false
}

// clone 深拷贝两端映射：保存路径会把策略同时放进缓存与响应，
// 共享底层 map 会让后来的一次写入改到已发布快照。
func (p RateLimitPolicy) clone() RateLimitPolicy {
	out := RateLimitPolicy{DefaultPerMinute: p.DefaultPerMinute, DefaultUnlimited: p.DefaultUnlimited}
	if p.Groups != nil {
		out.Groups = make(map[string]RateLimitRule, len(p.Groups))
		for k, v := range p.Groups {
			out.Groups[k] = v
		}
	}
	if p.Accounts != nil {
		out.Accounts = make(map[string]RateLimitRule, len(p.Accounts))
		for k, v := range p.Accounts {
			out.Accounts[k] = v
		}
	}
	return out
}

// validateRateLimitPolicy 只拒绝"一定是填错"的输入，并把细节留在服务端日志里：
// 对外只回一个稳定码 invalid_rate_limit，前端不必按字段翻译一套表单级错误。
func validateRateLimitPolicy(p RateLimitPolicy) error {
	fail := func(reason string) error {
		log.Printf("WARNING 目录服务：限流策略校验失败: %s", reason)
		return fmt.Errorf("invalid_rate_limit")
	}
	if p.DefaultPerMinute < 0 || p.DefaultPerMinute > rateLimitMaxPerMinute {
		return fail(fmt.Sprintf("default_per_minute=%d 超出 0..%d", p.DefaultPerMinute, rateLimitMaxPerMinute))
	}
	if len(p.Groups) > rateLimitMaxGroups {
		return fail(fmt.Sprintf("groups 条目 %d 超过上限 %d", len(p.Groups), rateLimitMaxGroups))
	}
	if len(p.Accounts) > rateLimitMaxAccounts {
		return fail(fmt.Sprintf("accounts 条目 %d 超过上限 %d", len(p.Accounts), rateLimitMaxAccounts))
	}
	check := func(kind string, k string, r RateLimitRule) error {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" {
			return fail(kind + " 存在空键")
		}
		if trimmed != k {
			return fail(fmt.Sprintf("%s 键 %q 含首尾空白", kind, k))
		}
		if len(k) > rateLimitMaxKeyLen {
			return fail(fmt.Sprintf("%s 键长度 %d 超过 %d", kind, len(k), rateLimitMaxKeyLen))
		}
		if r.PerMinute < 0 || r.PerMinute > rateLimitMaxPerMinute {
			return fail(fmt.Sprintf("%s[%s].per_minute=%d 超出 0..%d", kind, k, r.PerMinute, rateLimitMaxPerMinute))
		}
		// unlimited 与 per_minute 同时给出时以 unlimited 为准（解析函数就是这么做的），
		// 这里不拒绝：管理员想临时解除限制又不丢掉原额度时，这是有用的写法。
		return nil
	}
	for k, r := range p.Groups {
		if err := check("groups", k, r); err != nil {
			return err
		}
	}
	for k, r := range p.Accounts {
		if err := check("accounts", k, r); err != nil {
			return err
		}
	}
	return nil
}

// rateLimitSummary 是限流文档的规模摘要：审计要回答"这一版动了多大"，
// 而不是把（可能含账号 id 的）文档全文写进修订快照。
func rateLimitSummary(p RateLimitPolicy) map[string]any {
	return map[string]any{
		"default_per_minute": p.DefaultPerMinute,
		"default_unlimited":  p.DefaultUnlimited,
		"groups":             len(p.Groups),
		"accounts":           len(p.Accounts),
	}
}

// 进程内快照。判据同 definitionsCache：按 *sql.DB 区分，因为测试夹具为每个用例建隔离库。
var (
	rateLimitCacheMu sync.RWMutex
	rateLimitCache   RateLimitConfig
	rateLimitCacheDB *sql.DB
	rateLimitCacheOK bool
)

// currentRateLimitPolicy 是限流热路径唯一的读取入口：只读快照，不碰数据库。
// 未加载（或加载失败）时返回零值文档，于是解析结果回落到各路由的内置额度——
// 与引入本策略前的行为一致，配置问题不会把站点变成 429 风暴。
func currentRateLimitPolicy() RateLimitPolicy {
	rateLimitCacheMu.RLock()
	defer rateLimitCacheMu.RUnlock()
	if !rateLimitCacheOK {
		return RateLimitPolicy{}
	}
	return rateLimitCache.Policy
}

func storeRateLimitSnapshot(v RateLimitConfig, db *sql.DB) {
	v.Policy = v.Policy.clone()
	rateLimitCacheMu.Lock()
	rateLimitCache, rateLimitCacheDB, rateLimitCacheOK = v, db, true
	rateLimitCacheMu.Unlock()
}

// InvalidateRateLimitPolicyCache 清空进程内限流快照，供测试在改动策略后强制刷新。
func InvalidateRateLimitPolicyCache() {
	rateLimitCacheMu.Lock()
	rateLimitCache, rateLimitCacheDB, rateLimitCacheOK = RateLimitConfig{}, nil, false
	rateLimitCacheMu.Unlock()
}

func rateLimitConfig(ctx context.Context, q queryer) (RateLimitConfig, error) {
	var v RateLimitConfig
	var b []byte
	err := q.QueryRowContext(ctx, "SELECT etag,document,updated_at FROM catalog.rate_limit_policy WHERE singleton=true").Scan(&v.ETag, &b, &v.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(b, &v.Policy)
	}
	return v, err
}

// RateLimitPolicy 读取当前策略；进程内缓存靠 etag 观察其他副本的改动。
func (s *Store) RateLimitPolicy(ctx context.Context) (RateLimitConfig, error) {
	// 空 DB（纯映射单测的 Store{}）直接读库，保持原有错误语义。
	if s.DB == nil {
		return rateLimitConfig(ctx, s.DB)
	}
	rateLimitCacheMu.RLock()
	cached, cachedDB, ok := rateLimitCache, rateLimitCacheDB, rateLimitCacheOK
	rateLimitCacheMu.RUnlock()
	if ok && cachedDB == s.DB {
		// 轻量失效检查：只读 etag，不反序列化整份文档。
		var etag string
		if err := s.DB.QueryRowContext(ctx, "SELECT etag FROM catalog.rate_limit_policy WHERE singleton=true").Scan(&etag); err == nil && etag == cached.ETag {
			return cached, nil
		}
	}
	v, err := rateLimitConfig(ctx, s.DB)
	if err != nil {
		return v, err
	}
	storeRateLimitSnapshot(v, s.DB)
	return v, nil
}

// SaveRateLimitPolicy 原子地校验 etag 并替换唯一一份策略文档。
// expected_etag 是覆盖保护：两个管理员同时打开表单时，后保存的那个拿 409 version_conflict。
func (s *Store) SaveRateLimitPolicy(ctx context.Context, p RateLimitPolicy, expectedETag string, u User, note string, sources []Source) (RateLimitConfig, error) {
	var saved RateLimitConfig
	err := s.write(ctx, func(tx *sql.Tx) error {
		if !u.Can(PermissionDefinitionsManage) {
			return errForbidden
		}
		// 运行配置记录管理员的决定；没有事实来源时不伪造元数据证据。
		if strings.TrimSpace(note) == "" {
			return fmt.Errorf("edit_note_required")
		}
		if len(sources) > 0 {
			if err := validateSources(sources); err != nil {
				return err
			}
		}
		if err := validateRateLimitPolicy(p); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", rateLimitLockKey); err != nil {
			return err
		}
		current, err := rateLimitConfig(ctx, tx)
		if err != nil {
			return err
		}
		if expectedETag == "" || expectedETag != current.ETag {
			return errVersionConflict
		}
		newETag := uuid.NewString()
		var updatedAt time.Time
		if err := tx.QueryRowContext(ctx, "UPDATE catalog.rate_limit_policy SET document=$1,etag=$2,updated_at=now() WHERE singleton=true AND etag=$3 RETURNING updated_at", encode(p), newETag, expectedETag).Scan(&updatedAt); err != nil {
			if err == sql.ErrNoRows {
				return errVersionConflict
			}
			return err
		}
		// 审计只记规模与口径，不落文档全文：账号 id 不该进修订历史。
		var auditSeq int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM catalog.revisions WHERE target_id='rate-limits'").Scan(&auditSeq); err != nil {
			return err
		}
		if err := audit(ctx, tx, "rate-limits", auditSeq, u, note, sources, rateLimitSummary(p), "rate_limits.updated"); err != nil {
			return err
		}
		saved = RateLimitConfig{ETag: newETag, Policy: p.clone(), UpdatedAt: updatedAt}
		return nil
	})
	if err == nil {
		storeRateLimitSnapshot(saved, s.DB)
	}
	return saved, err
}

// RunRateLimitPolicyRefresher 每分钟重新读一次策略：限流配置只有一处写入入口，
// 但服务是多副本的，本副本保存时刷新的只有自己的快照。一次带 etag 的索引查询，
// 没有变化就不反序列化。读取失败只告警：继续用上一版，而不是退回内置额度。
func (s *Store) RunRateLimitPolicyRefresher(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.RateLimitPolicy(ctx); err != nil {
				log.Printf("WARNING 目录服务：限流策略刷新失败，继续使用上一版: %v", err)
			}
			if err := s.pruneRateWindows(ctx); err != nil {
				log.Printf("WARNING 目录服务：过期限流窗口清理失败: %v", err)
			}
		}
	}
}
