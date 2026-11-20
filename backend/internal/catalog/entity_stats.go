package catalog

import (
	"context"
	"strings"
	"sync"
	"time"
)

type EntityCoverageCounts struct {
	Total        int64 `json:"total"`
	WithPictures int64 `json:"with_pictures"`
	WithTags     int64 `json:"with_tags"`
}

type CatalogStats struct {
	EntityCoverageCounts
	Kinds map[string]EntityCoverageCounts `json:"kinds"`
}

type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

const (
	entityReadCacheMaxAge      = 5 * time.Minute
	entityReadCacheLoadTimeout = 15 * time.Second
)

type cacheInflightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// readCache caches an aggregate read snapshot keyed by a cheap database version
// token. Concurrent misses for the same token share a single DB query.
type readCache[T any] struct {
	mu       sync.RWMutex
	token    string
	loadedAt time.Time
	value    T
	valid    bool
	inflight map[string]*cacheInflightCall[T]
}

func (c *readCache[T]) getOrLoad(ctx context.Context, token string, loader func(context.Context) (T, error)) (T, error) {
	c.mu.RLock()
	if c.valid && c.token == token && time.Since(c.loadedAt) < entityReadCacheMaxAge {
		val := c.value
		c.mu.RUnlock()
		return val, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if c.valid && c.token == token && time.Since(c.loadedAt) < entityReadCacheMaxAge {
		val := c.value
		c.mu.Unlock()
		return val, nil
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*cacheInflightCall[T])
	}
	call, waiting := c.inflight[token]
	if !waiting {
		call = &cacheInflightCall[T]{done: make(chan struct{})}
		c.inflight[token] = call
		go func() {
			loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), entityReadCacheLoadTimeout)
			defer cancel()
			val, err := loader(loadCtx)
			call.val = val
			call.err = err
			c.mu.Lock()
			if err == nil {
				c.token = token
				c.value = val
				c.loadedAt = time.Now()
				c.valid = true
			}
			delete(c.inflight, token)
			c.mu.Unlock()
			close(call.done)
		}()
	}
	c.mu.Unlock()

	select {
	case <-call.done:
		return call.val, call.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// entityReadCacheToken returns a cheap, strictly advancing token using pure
// index/PK lookups (catalog_head singleton, revisions_pkey MAX, and
// entities_public_recent top row).
func (s *Store) entityReadCacheToken(ctx context.Context) (string, error) {
	var token string
	err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT commit_id::text FROM catalog.catalog_head WHERE singleton), '')
		|| '|' ||
		COALESCE((SELECT id::text FROM catalog.revisions ORDER BY id DESC LIMIT 1), '0')
		|| '|' ||
		COALESCE((SELECT updated_at::text || ':' || id::text FROM catalog.entities WHERE state='active' ORDER BY updated_at DESC, id ASC LIMIT 1), '')`).Scan(&token)
	return token, err
}

func cloneCatalogStats(in CatalogStats) CatalogStats {
	out := CatalogStats{
		EntityCoverageCounts: in.EntityCoverageCounts,
		Kinds:                make(map[string]EntityCoverageCounts, len(in.Kinds)),
	}
	for k, v := range in.Kinds {
		out.Kinds[k] = v
	}
	return out
}

// PublicStats aggregates active facts in one database snapshot. It does not
// expose tombstones, private workspace activity or review queues.
func (s *Store) PublicStats(ctx context.Context) (CatalogStats, error) {
	token, err := s.entityReadCacheToken(ctx)
	if err != nil {
		return CatalogStats{}, err
	}
	stats, err := s.publicStatsCache.getOrLoad(ctx, token, s.loadPublicStats)
	if err != nil {
		return CatalogStats{}, err
	}
	return cloneCatalogStats(stats), nil
}

func (s *Store) loadPublicStats(ctx context.Context) (CatalogStats, error) {
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

// ListTags returns active entity tag frequencies ordered by count descending
// and tag name ascending, filtered by case-insensitive literal substring q.
func (s *Store) ListTags(ctx context.Context, q string, limit int) ([]TagCount, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	token, err := s.entityReadCacheToken(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.tagsCache.getOrLoad(ctx, token, s.loadAllTagCounts)
	if err != nil {
		return nil, err
	}
	return filterTagCounts(all, q, limit), nil
}

func (s *Store) loadAllTagCounts(ctx context.Context) ([]TagCount, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.name, count(*) AS n
		FROM catalog.entities e,
		     jsonb_array_elements_text(e.document->'attributes'->'tags') AS t(name)
		WHERE e.state='active' AND jsonb_typeof(e.document->'attributes'->'tags')='array'
		GROUP BY t.name
		ORDER BY n DESC, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TagCount{}
	for rows.Next() {
		var item TagCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func filterTagCounts(all []TagCount, q string, limit int) []TagCount {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q = strings.TrimSpace(q)
	if q == "" {
		n := min(limit, len(all))
		out := make([]TagCount, n)
		copy(out, all[:n])
		return out
	}
	needle := strings.ToLower(q)
	out := make([]TagCount, 0, min(limit, len(all)))
	for _, item := range all {
		if strings.Contains(strings.ToLower(item.Name), needle) {
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
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
