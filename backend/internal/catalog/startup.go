package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// 启动职责分离（S1）：运行服务不再执行 DDL 与种子发布。
//
// 显式任务入口（生产以它们为准，见 backend/cmd/migrate 与部署作业）：
//  1. 结构迁移：`mf-migrate up`（backend/migrations/*.sql，migrator 自动发现，
//     000021 是不可变的安装基线，后续只加有序增量）；
//  2. 内容种子：`mf-migrate seed`（Store.SeedContent，无 DDL：空库发布内置定义，存量定义/外部库/货架只增不改，D2 保证不夺回人工配置）；
//  3. 完整性扫描：`mf-migrate check-refs`（Store.DanglingReferences，只读体检）；
//  4. HTTP 启动：本文件的 CheckCompatibleVersion（只读，不写库、不全表扫描）。
//
// Store.Initialize 是本地安装/测试的显式组合入口（基线 + 增量 + 空库种子 + 种子升级），
// 生产常驻进程不再调用它。先调职责再谈撤权：运行角色的 DDL 权限本次不动，
// S2 冻结说明见 store.go 的基线注释。

// requiredCatalogTables 是 HTTP 服务运行必需的表：缺任一即不兼容（起不来比半残好查）。
var requiredCatalogTables = []string{
	"catalog.entities",
	"catalog.commits",
	"catalog.relations",
	"catalog.definition_config",
	"catalog.rate_limit_policy",
	"catalog.revisions",
	"catalog.outbox",
	"catalog.lifecycle_actions",
	"catalog.api_request_logs",
	"catalog.idempotency_keys",
	"catalog.schema_contract",
	"catalog.api_rate_windows",
}

// CheckCompatibleVersion 是 HTTP 进程启动的唯一前置检查：只读校验必需表、已发布契约与
// 已发布定义存在，不写库、不加载实体/关系全集（EnsureSeedDefinitions 的启动扫描在此之后
// 不再执行）。失败即 Fatal：库没准备好时拒绝服务，而不是降级成“能起但行为不对”。
func (s *Store) CheckCompatibleVersion(ctx context.Context) error {
	var hasInclusionSources bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='catalog' AND table_name='track_contents' AND column_name='sources')`).Scan(&hasInclusionSources); err != nil {
		return err
	}
	if !hasInclusionSources {
		return fmt.Errorf("incompatible_schema: inclusion sources required (run mf-migrate up)")
	}
	for _, t := range requiredCatalogTables {
		var ok bool
		if err := s.DB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, t).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("incompatible_schema: missing %s (run mf-migrate up)", t)
		}
	}
	var contractVersion int
	if err := s.DB.QueryRowContext(ctx, "SELECT version FROM catalog.schema_contract WHERE singleton=true").Scan(&contractVersion); err != nil {
		return fmt.Errorf("incompatible_schema: current schema contract required (run mf-migrate up): %w", err)
	}
	if contractVersion != 28 {
		return fmt.Errorf("incompatible_schema: catalog schema contract 28 required (run mf-migrate up)")
	}
	var hasCommitProvenance bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='catalog' AND table_name='revisions' AND column_name='commit_id')`).Scan(&hasCommitProvenance); err != nil {
		return err
	}
	if !hasCommitProvenance {
		return fmt.Errorf("incompatible_schema: commit provenance required (run mf-migrate up)")
	}
	var candidateLookupReady bool
	if err := s.DB.QueryRowContext(ctx, `SELECT to_regprocedure('catalog.identity_candidate_terms(jsonb)') IS NOT NULL
		AND EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('catalog.entities_identity_candidates_idx') AND indisvalid)`).Scan(&candidateLookupReady); err != nil {
		return err
	}
	if !candidateLookupReady {
		return fmt.Errorf("incompatible_schema: identity candidate lookup required (run mf-migrate up)")
	}
	var etag string
	var currentContract bool
	// The migration ledger belongs to the DB owner; the runtime role has no
	// SELECT on public.schema_migrations. Check the published contract it uses.
	if err := s.DB.QueryRowContext(ctx, `SELECT etag,
		NOT (document ? 'types')
		AND COALESCE(jsonb_typeof(document #> '{structure,release,subject_names}') = 'object', false)
		FROM catalog.definition_config WHERE singleton=true`).Scan(&etag, &currentContract); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("definitions_missing: no definition configuration (run mf-migrate up, then mf-migrate seed on a fresh install)")
		}
		return err
	}
	if !currentContract {
		return fmt.Errorf("incompatible_schema: generic metadata definitions required (run mf-migrate up, then mf-migrate seed)")
	}
	// 限流策略是运行配置：启动时载入进程内快照，限流热路径（每个受限请求）不再查库。
	// 读失败即拒绝启动：带着"读不到策略"起来会静默退回内置额度，而运维看到的
	// 现象与"配置没生效"完全一样，只能靠读代码猜。
	if _, err := s.RateLimitPolicy(ctx); err != nil {
		return fmt.Errorf("rate_limit_policy_unreadable: %w (run mf-migrate up)", err)
	}
	// 反查索引缺失只影响反向别名查询性能（功能不受影响）：告警，不阻断启动。
	var hasIdx bool
	if err := s.DB.QueryRowContext(ctx, `SELECT to_regclass('catalog.entities_redirect_lookup') IS NOT NULL`).Scan(&hasIdx); err == nil && !hasIdx {
		log.Print("WARNING catalog startup: missing index catalog.entities_redirect_lookup (run mf-migrate up); reverse alias lookup falls back to sequential scan")
	}
	return nil
}
