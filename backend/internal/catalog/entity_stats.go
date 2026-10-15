package catalog

import "context"

// 状态计数（GET /api/catalog/entities/stats）。
//
// 存在的理由：列表端点共用的 listFilter 恒带 status NOT IN ('deleted','merged')，
// 那是刻意的可见性口径（不为统计改它），所以墓碑数只能由一条独立的聚合查询给出，
// 而不是把全表拉进应用层再数——线上同表 deleted 已有数百行，拉全表的代价随库增长。

// entityStatuses 是 catalog.entities.status 的全集，逐字对齐迁移 000001 的 CHECK 约束；
// 计数端点据此把"没有行"补成真实的 0：响应的键缺失与"计数为 0"因此是两件可分辨的事。
var entityStatuses = []string{"active", "deleted", "merged"}

// EntityStatusCounts 是 /api/catalog/entities/stats 的响应：按状态分组的真实计数 + 全表总数。
type EntityStatusCounts struct {
	PendingCommits int64            `json:"pending_commits"`
	Statuses       map[string]int64 `json:"states"`
	Total          int64            `json:"total"`
}

// statusGroup 是 GROUP BY status 的一行。
type statusGroup struct {
	Status string
	Count  int64
}

// StatusCounts 用一条 GROUP BY status 聚合给出全表按状态的计数，口径是全表（含 deleted/merged，
// 与列表的可见性过滤无关）。路由闸门是 catalog.lifecycle.manage：只有这个码的持有者能在
// /entities 列表里看全量状态，聚合因此不比列表多露一行。
func (s *Store) StatusCounts(ctx context.Context) (EntityStatusCounts, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT status, count(*) FROM catalog.entities GROUP BY status")
	if err != nil {
		return EntityStatusCounts{}, err
	}
	defer rows.Close()
	groups := []statusGroup{}
	for rows.Next() {
		var g statusGroup
		if err := rows.Scan(&g.Status, &g.Count); err != nil {
			return EntityStatusCounts{}, err
		}
		groups = append(groups, g)
	}
	// Next 的终止原因要暴露：迭代中途断连不能当成"某个状态是 0"。
	if err := rows.Err(); err != nil {
		return EntityStatusCounts{}, err
	}
	out := foldStatusCounts(groups)
	err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.commits WHERE review_status='pending_review'").Scan(&out.PendingCommits)
	return out, err
}

// foldStatusCounts 是纯函数（不碰库）：补齐 entityStatuses 的零值，total 取所有分组之和。
// 未知状态值（理论上被 CHECK 约束挡住）也计入 total，不静默丢行。
func foldStatusCounts(groups []statusGroup) EntityStatusCounts {
	out := EntityStatusCounts{Statuses: make(map[string]int64, len(entityStatuses)+len(groups))}
	for _, code := range entityStatuses {
		out.Statuses[code] = 0
	}
	for _, g := range groups {
		out.Statuses[g.Status] = g.Count
		out.Total += g.Count
	}
	return out
}
