package catalog

import "context"

type EntityCoverageCounts struct {
	Total        int64 `json:"total"`
	WithPictures int64 `json:"with_pictures"`
	WithTags     int64 `json:"with_tags"`
}

type CatalogStats struct {
	EntityCoverageCounts
	Kinds map[string]EntityCoverageCounts `json:"kinds"`
}

// PublicStats aggregates active facts in one database snapshot. It does not
// expose tombstones, private workspace activity or review queues.
func (s *Store) PublicStats(ctx context.Context) (CatalogStats, error) {
	out := CatalogStats{Kinds: make(map[string]EntityCoverageCounts, len(Kinds))}
	for _, kind := range Kinds {
		out.Kinds[kind] = EntityCoverageCounts{}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind, count(*),
		count(*) FILTER (WHERE CASE WHEN jsonb_typeof(document->'pictures')='array' THEN jsonb_array_length(document->'pictures')>0 ELSE false END),
		count(*) FILTER (WHERE CASE WHEN jsonb_typeof(document->'attributes'->'tags')='array' THEN jsonb_array_length(document->'attributes'->'tags')>0 ELSE false END)
		FROM catalog.entities WHERE state='active' GROUP BY kind`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var counts EntityCoverageCounts
		if err := rows.Scan(&kind, &counts.Total, &counts.WithPictures, &counts.WithTags); err != nil {
			return out, err
		}
		out.Kinds[kind] = counts
		out.Total += counts.Total
		out.WithPictures += counts.WithPictures
		out.WithTags += counts.WithTags
	}
	return out, rows.Err()
}

// 状态计数（GET /api/catalog/entities/stats）。
//
// 存在的理由：列表端点共用的 listFilter 恒带 state NOT IN ('deleted','merged')，
// 那是刻意的可见性口径（不为统计改它），所以墓碑数只能由一条独立的聚合查询给出，
// 而不是把全表拉进应用层再数——线上同表 deleted 已有数百行，拉全表的代价随库增长。

// entityStates 是 catalog.entities.state 的全集，逐字对齐迁移 000001 的 CHECK 约束；
// 计数端点据此把"没有行"补成真实的 0：响应的键缺失与"计数为 0"因此是两件可分辨的事。
var entityStates = []string{"active", "deleted", "merged"}

// EntityStateCounts 是 /api/catalog/entities/stats 的响应：按状态分组的真实计数 + 全表总数。
type EntityStateCounts struct {
	PendingPullRequests int64            `json:"pending_pull_requests"`
	States              map[string]int64 `json:"states"`
	Total               int64            `json:"total"`
}

// stateGroup 是 GROUP BY state 的一行。
type stateGroup struct {
	State string
	Count int64
}

// StateCounts 用一条 GROUP BY state 聚合给出全表按状态的计数，口径是全表（含 deleted/merged，
// 与列表的可见性过滤无关）。路由闸门是 catalog.lifecycle.manage；
// 普通读取者使用仅统计 active 的 PublicStats。
func (s *Store) StateCounts(ctx context.Context) (EntityStateCounts, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT state, count(*) FROM catalog.entities GROUP BY state")
	if err != nil {
		return EntityStateCounts{}, err
	}
	defer rows.Close()
	groups := []stateGroup{}
	for rows.Next() {
		var g stateGroup
		if err := rows.Scan(&g.State, &g.Count); err != nil {
			return EntityStateCounts{}, err
		}
		groups = append(groups, g)
	}
	// Next 的终止原因要暴露：迭代中途断连不能当成"某个状态是 0"。
	if err := rows.Err(); err != nil {
		return EntityStateCounts{}, err
	}
	out := foldStateCounts(groups)
	err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.pull_requests WHERE status='open'").Scan(&out.PendingPullRequests)
	return out, err
}

// foldStateCounts 是纯函数（不碰库）：补齐 entityStates 的零值，total 取所有分组之和。
// 未知状态值（理论上被 CHECK 约束挡住）也计入 total，不静默丢行。
func foldStateCounts(groups []stateGroup) EntityStateCounts {
	out := EntityStateCounts{States: make(map[string]int64, len(entityStates)+len(groups))}
	for _, code := range entityStates {
		out.States[code] = 0
	}
	for _, g := range groups {
		out.States[g.State] = g.Count
		out.Total += g.Count
	}
	return out
}
