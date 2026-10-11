package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// Shelf 是首页货架与探索页共用的聚合规则。
// 前后端共用同一规则：query 描述“收录什么”（tags 按开放标签、fields 按
// 字段取值、vocab_terms 按词表项、relations 按关系存在性），sort/icon/
// enabled 描述“如何展示”。names 为四语名称映射（zh-CN/en-US/zh-TW/ja-JP）。
type Shelf struct {
	ID        int64             `json:"id"`
	Slug      string            `json:"slug"`
	Names     map[string]string `json:"names"`
	Query     ShelfQuery        `json:"query"`
	Sort      string            `json:"sort"`
	Icon      string            `json:"icon"`
	Enabled   bool              `json:"enabled"`
	SortOrder int               `json:"sort_order"`
	// Source 只在 /shelves/feed 里由 applyHomePreferences 置位（system / custom），
	// 库内货架没有这一列，管理端读写时为空并因此不出现在 JSON 里。
	Source string `json:"source,omitempty"`
}

// ShelfSourceSystem / ShelfSourceCustom 是 feed 条目的分区来源：命中系统货架 slug 的覆盖
// 仍是 system（前端据此不给"删除分区"入口——删掉只会露出系统默认），用户自建的是 custom。
// 匿名用户没有 sections，因此永远只见 system。
const (
	ShelfSourceSystem = "system"
	ShelfSourceCustom = "custom"
)

// ShelfQuery 描述货架收录规则。各子条件之间为 AND；同一数组内为 OR。
// 空 query 表示收录全部已发布作品。
type ShelfQuery struct {
	Tags       []string            `json:"tags,omitempty"`
	Fields     map[string][]string `json:"fields,omitempty"`
	VocabTerms map[string][]string `json:"vocab_terms,omitempty"`
	Relations  []string            `json:"relations,omitempty"`
}

// 收录规则是公开契约；拒绝已废弃或拼错的键，避免意外成为“收录全部”。
func (q *ShelfQuery) UnmarshalJSON(data []byte) error {
	type shape ShelfQuery
	var value shape
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*q = ShelfQuery(value)
	return nil
}

var shelfSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

func scanShelf(row func(...any) error) (Shelf, error) {
	var s Shelf
	var names []byte
	var query []byte
	err := row(&s.ID, &s.Slug, &names, &query, &s.Sort, &s.Icon, &s.Enabled, &s.SortOrder)
	if err != nil {
		return s, err
	}
	s.Names = map[string]string{}
	if len(names) > 0 {
		_ = json.Unmarshal(names, &s.Names)
	}
	s.Query = ShelfQuery{}
	if len(query) > 0 {
		if err := json.Unmarshal(query, &s.Query); err != nil {
			return s, err
		}
	}
	if s.Query.Fields == nil {
		s.Query.Fields = map[string][]string{}
	}
	if s.Query.VocabTerms == nil {
		s.Query.VocabTerms = map[string][]string{}
	}
	return s, nil
}

// shelfSortKeys 是货架排序取值的闭集（空串表示未配置，等价于 updated）。
// 校验与求值共用它，测试也逐值遍历：新增排序方式必须同时补 shelfOrderClause。
var shelfSortKeys = []string{"", "updated", "created", "title"}

func validateShelf(s Shelf) error {
	if !shelfSlugPattern.MatchString(s.Slug) {
		return fmt.Errorf("invalid_slug")
	}
	// 货架名是首页/探索的分类入口名，与类型/关系名同一口径：四语齐备（不再只要求双语）。
	if err := validateNames(s.Names); err != nil {
		return err
	}
	// 白名单与 shelfOrderClause 共用同一份闭集：分开写就会出现"接受但求值没实现"的静默回落。
	if !contains(shelfSortKeys, s.Sort) {
		return fmt.Errorf("invalid_sort")
	}
	return validateShelfQuery(s.Query)
}

// validateShelfQuery 校验收录规则的形状：tags / relations 逐项必须是去空白后非空的码，
// fields / vocab_terms 的键必须是合法字段码、取值逐项非空。
//
// 抽成公共函数：系统货架规则（validateShelf）与用户首页分区共用同一份检查。
// 只判形状不判存在性——开放标签无需预先声明；字段/词表项由动态 definitions 决定，写实体与定义发布自会拦住
// 未声明的码；这里要挡的是"写进去必然取不到值"的空承诺（shelfFilter 会把空白项整个丢掉）。
func validateShelfQuery(q ShelfQuery) error {
	for _, v := range q.Tags {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("invalid_tags")
		}
	}
	if err := validateQueryValues("invalid_fields", q.Fields); err != nil {
		return err
	}
	if err := validateQueryValues("invalid_vocab_terms", q.VocabTerms); err != nil {
		return err
	}
	for _, v := range q.Relations {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("invalid_relations")
		}
	}
	return nil
}

// validateQueryValues 校验"字段码 → 取值"映射：键与 definitions 的字段码同一口径（codePattern），
// 值逐项非空。码本身是否已声明不在这一层判定（见 validateShelfQuery）。
func validateQueryValues(code string, m map[string][]string) error {
	for _, field := range sortedKeys(m) {
		if !codePattern.MatchString(field) {
			return fmt.Errorf("%s", code)
		}
		for _, v := range m[field] {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%s", code)
			}
		}
	}
	return nil
}

const shelfColumns = `id,slug,names,query,sort,icon,is_enabled,sort_order`

// ListShelves 返回货架规则（按 sort_order 排序）。
// enabledOnly 为 true 时只返回启用项，供首页/探索等公开入口使用。
func (s *Store) ListShelves(ctx context.Context, enabledOnly bool) ([]Shelf, error) {
	q := `SELECT ` + shelfColumns + ` FROM catalog.shelves`
	if enabledOnly {
		q += ` WHERE is_enabled`
	}
	q += ` ORDER BY sort_order,id`
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Shelf{}
	for rows.Next() {
		item, err := scanShelf(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetShelf 按 id 读取单个货架规则（含停用项，供后台编辑）。
func (s *Store) GetShelf(ctx context.Context, id int64) (Shelf, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+shelfColumns+` FROM catalog.shelves WHERE id=$1`, id)
	return scanShelf(row.Scan)
}

func (s *Store) CreateShelf(ctx context.Context, in Shelf) (Shelf, error) {
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Names == nil {
		in.Names = map[string]string{}
	}
	if in.Query.Fields == nil {
		in.Query.Fields = map[string][]string{}
	}
	if in.Query.VocabTerms == nil {
		in.Query.VocabTerms = map[string][]string{}
	}
	if in.Sort == "" {
		in.Sort = "updated"
	}
	if err := validateShelf(in); err != nil {
		return Shelf{}, err
	}
	names, _ := json.Marshal(in.Names)
	query, _ := json.Marshal(in.Query)
	var created Shelf
	err := s.write(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.shelves WHERE slug=$1)`, in.Slug).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("constraint_violation")
		}
		row := tx.QueryRowContext(ctx, `INSERT INTO catalog.shelves(slug,names,query,sort,icon,is_enabled,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+shelfColumns,
			in.Slug, string(names), string(query), in.Sort, in.Icon, in.Enabled, in.SortOrder)
		e, err := scanShelf(row.Scan)
		if err != nil {
			return err
		}
		created = e
		return nil
	})
	return created, err
}

// UpdateShelf 更新货架规则；slug 不可改（作为前端路由与外链的稳定标识）。
func (s *Store) UpdateShelf(ctx context.Context, id int64, in Shelf) (Shelf, error) {
	if in.Names == nil {
		in.Names = map[string]string{}
	}
	if in.Query.Fields == nil {
		in.Query.Fields = map[string][]string{}
	}
	if in.Query.VocabTerms == nil {
		in.Query.VocabTerms = map[string][]string{}
	}
	if in.Sort == "" {
		in.Sort = "updated"
	}
	// slug 取库内值参与校验，保证不可改。
	var slug string
	if err := s.DB.QueryRowContext(ctx, `SELECT slug FROM catalog.shelves WHERE id=$1`, id).Scan(&slug); err != nil {
		return Shelf{}, err
	}
	in.Slug = slug
	if err := validateShelf(in); err != nil {
		return Shelf{}, err
	}
	names, _ := json.Marshal(in.Names)
	query, _ := json.Marshal(in.Query)
	var updated Shelf
	err := s.write(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `UPDATE catalog.shelves SET names=$2,query=$3,sort=$4,icon=$5,is_enabled=$6,sort_order=$7 WHERE id=$1 RETURNING `+shelfColumns,
			id, string(names), string(query), in.Sort, in.Icon, in.Enabled, in.SortOrder)
		e, err := scanShelf(row.Scan)
		if err != nil {
			return err
		}
		updated = e
		return nil
	})
	return updated, err
}

// DeleteShelf 删除货架规则。货架只是聚合视图，不承载实体数据，可直接删除。
func (s *Store) DeleteShelf(ctx context.Context, id int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM catalog.shelves WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// trimAll 去掉空白项并保持原顺序，空数组返回 nil。
func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sortedKeys 让 map 条件按固定顺序进入 SQL，保证语句可复现（也便于排查）。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// shelfFilter 把货架规则编译成 WHERE 片段：子条件之间 AND，同一数组内 OR。
// 所有规则只收录作品；tags 为开放标签，数组内任一命中即可。
func shelfFilter(sh Shelf, args *[]any, alias string) []string {
	parts := []string{fmt.Sprintf("%s.kind='work'", alias)}
	if tags := trimAll(sh.Query.Tags); len(tags) > 0 {
		ors := make([]string, 0, len(tags))
		for _, tag := range tags {
			*args = append(*args, encode([]string{tag}))
			ors = append(ors, fmt.Sprintf("%s.document->'attributes'->'tags' @> $%d::jsonb", alias, len(*args)))
		}
		parts = append(parts, "("+strings.Join(ors, " OR ")+")")
	}
	// enum 词表项与普通字段一样落在 document.attributes 下，用同一比较方式。
	for _, m := range []map[string][]string{sh.Query.Fields, sh.Query.VocabTerms} {
		for _, field := range sortedKeys(m) {
			vals := trimAll(m[field])
			if len(vals) == 0 {
				continue
			}
			*args = append(*args, field, pq.Array(vals))
			parts = append(parts, fmt.Sprintf("%s.document->'attributes'->>$%d = ANY($%d)", alias, len(*args)-1, len(*args)))
		}
	}
	if rels := trimAll(sh.Query.Relations); len(rels) > 0 {
		*args = append(*args, pq.Array(rels))
		parts = append(parts, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM catalog.relations r WHERE (r.source_id=%s.id OR r.target_id=%s.id) AND r.document->>'type' = ANY($%d))",
			alias, alias, len(*args)))
	}
	return parts
}

// shelfOrderClause 把货架的 sort 编译成 ORDER BY 片段。
//
// 与 validateShelf 的白名单一一对应：白名单放行了却没有求值实现，写进去就等于静默回落
// （历史上 created 正是这样：接受了取值却按 updated_at 排序）。未知值仍回落 updated，
// 这是兜底而不是契约——测试逐值断言每个被接受的取值都有非空片段。
func shelfOrderClause(sortKey string) string {
	switch sortKey {
	case "created":
		// 实体表没有 created_at 列（只有 updated_at），而基线迁移是唯一结构来源、不能为
		// 一个排序键改它。实体 id 由 uuid.NewV7() 生成、是时间有序的，按 id 倒序即创建时间
		// 倒序，且能走主键索引；同毫秒内两条的顺序由随机位决定（毫秒级精度）。
		return "e.id DESC"
	case "title":
		return "e.title ASC, e.id"
	default: // ""（未配置）与 "updated"
		return "e.updated_at DESC, e.id"
	}
}

// ListShelfItems 按货架规则求值出实体列表，返回顺序与 sort 一致。
// 规则里的 fields/vocab_terms/relations 只有服务端能判定，故求值必须在此完成，
// 前端不再自行近似匹配（那会把非作品实体和无关条目混进推荐）。
func (s *Store) ListShelfItems(ctx context.Context, sh Shelf, limit int, u *User) ([]Entity, error) {
	if limit <= 0 || limit > 100 {
		limit = 12
	}
	args := []any{}
	where := shelfVisibilityWhere(u, &args)
	where = append(where, shelfFilter(sh, &args, "e")...)

	order := shelfOrderClause(sh.Sort)
	args = append(args, limit)
	q := "SELECT e.id::text FROM catalog.entities e WHERE " + strings.Join(where, " AND ") +
		" ORDER BY " + order + fmt.Sprintf(" LIMIT $%d", len(args))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	got, err := s.GetManyVisible(ctx, ids, u)
	if err != nil {
		return nil, err
	}
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		if e, ok := got[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// shelfVisibilityWhere 构建货架求值共用的可见性条件：匿名仅 published，登录者
// 另可见自己创建的草稿，持货架管理权者可见未发布条目（便于配置后预览）；
// deleted / merged 一律排除。计数与列表用同一份条件，数量才与实际筛选一致。
func shelfVisibilityWhere(u *User, args *[]any) []string {
	where := []string{}
	if u == nil {
		where = append(where, "e.status='published'")
	} else if !u.Can(PermissionShelvesManage) {
		*args = append(*args, u.ID)
		where = append(where, fmt.Sprintf("(e.status='published' OR e.created_by=$%d)", len(*args)))
	}
	where = append(where, "e.status NOT IN ('deleted','merged')")
	return where
}

// CountShelfItems 返回货架规则在当前调用者可见范围内命中的实体总数（不带 LIMIT），
// 供首页显示真实筛选数量，而不是受限后的 items 条数。
func (s *Store) CountShelfItems(ctx context.Context, sh Shelf, u *User) (int, error) {
	args := []any{}
	where := shelfVisibilityWhere(u, &args)
	where = append(where, shelfFilter(sh, &args, "e")...)
	q := "SELECT count(*) FROM catalog.entities e WHERE " + strings.Join(where, " AND ")
	var n int
	if err := s.DB.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// shelfSeeds 是首页货架的默认规则（四语名称）。
// 单独成函数：名称四语覆盖由 names_coverage_test 直接断言，不只依赖启动写库。
func shelfSeeds() []Shelf {
	return []Shelf{
		{Slug: "music", Names: map[string]string{"zh-CN": "音乐", "zh-TW": "音樂", "ja": "音楽", "ja-JP": "音楽", "en-US": "Music"}, Query: ShelfQuery{Tags: []string{"music", "song", "album"}}, Sort: "updated", Icon: "Disc", Enabled: true, SortOrder: 10},
		{Slug: "anime", Names: map[string]string{"zh-CN": "动画", "zh-TW": "動畫", "ja": "アニメ", "ja-JP": "アニメ", "en-US": "Anime"}, Query: ShelfQuery{Tags: []string{"animation"}}, Sort: "updated", Icon: "Tv", Enabled: true, SortOrder: 20},
		{Slug: "films", Names: map[string]string{"zh-CN": "电影", "zh-TW": "電影", "ja": "映画", "ja-JP": "映画", "en-US": "Films"}, Query: ShelfQuery{Tags: []string{"film"}}, Sort: "updated", Icon: "Film", Enabled: true, SortOrder: 30},
		{Slug: "novels", Names: map[string]string{"zh-CN": "小说", "zh-TW": "小說", "ja": "小説", "ja-JP": "小説", "en-US": "Novels"}, Query: ShelfQuery{Tags: []string{"novel"}}, Sort: "updated", Icon: "BookOpen", Enabled: true, SortOrder: 40},
		{Slug: "games", Names: map[string]string{"zh-CN": "游戏", "zh-TW": "遊戲", "ja": "ゲーム", "ja-JP": "ゲーム", "en-US": "Games"}, Query: ShelfQuery{Tags: []string{"game", "indie_game", "visual_novel"}}, Sort: "updated", Icon: "Gamepad2", Enabled: true, SortOrder: 50},
		{Slug: "creations", Names: map[string]string{"zh-CN": "个人创作", "zh-TW": "個人創作", "ja": "個人制作", "ja-JP": "個人制作", "en-US": "Creations"}, Query: ShelfQuery{Tags: []string{"personal", "photobook"}}, Sort: "updated", Icon: "Camera", Enabled: true, SortOrder: 60},
	}
}

// seedShelves 写入首页货架默认规则；已存在的 slug 不覆盖（保留后台自定义），
// 新增的 slug 自动补齐。前后端共用同一规则，不再各自硬编码。
func seedShelves(ctx context.Context, tx *sql.Tx) error {
	for _, d := range shelfSeeds() {
		names, _ := json.Marshal(d.Names)
		query, _ := json.Marshal(d.Query)
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.shelves(slug,names,query,sort,icon,is_enabled,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (slug) DO NOTHING`,
			d.Slug, string(names), string(query), d.Sort, d.Icon, d.Enabled, d.SortOrder); err != nil {
			return err
		}
	}
	return nil
}
