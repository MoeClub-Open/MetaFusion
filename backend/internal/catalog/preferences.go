package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// maxHomeSections 是单个用户的分区上限：首页只展示有限行，整份偏好又存在单行 JSONB 里，
// 不设上限等于给自己开一张无界的规则表。
const maxHomeSections = 20

// customShelfSortBase 是用户自建分区在 feed 里的排序位基准：自建分区没有后台配置的
// sort_order，取一个大于系统货架常见取值的基准，客户端若自行按 sort_order 兜底排序，
// 仍然把它们排在系统分区之后（feed 数组本身已是合并后的最终顺序）。
const customShelfSortBase = 1000

// HomePreferences 是用户对首页推荐分区的自定义：展示顺序、隐藏项，以及自己的分区列表。
// 分区内容本身由 catalog.shelves 规则驱动，这里只记录"给谁看、按什么顺序、覆盖什么"，
// 整份偏好存在 catalog.user_preferences.home_shelves（JSONB），因此管理台调整规则时无需迁移。
type HomePreferences struct {
	// Order 是用户排定的分区 slug 顺序，系统与自建 slug 混排；未列出的分区排在其后。
	Order []string `json:"order"`
	// Hidden 是用户选择不展示的分区 slug，对系统与自建分区都生效。
	Hidden []string `json:"hidden"`
	// Sections 是"覆盖 + 自建"的分区列表：slug 命中系统货架即覆盖该货架给本人看的
	// names/query/sort/icon（系统默认对其他用户不变），不命中即追加一个只属于本人的分区。
	Sections []HomeSection `json:"sections"`
}

// HomeSection 是一条用户分区声明。slug 允许与系统货架同名——这正是"覆盖"的表达方式，
// 不是冲突；只有不命中系统货架才是新增分区，因此这里不做唯一性冲突判定。
type HomeSection struct {
	Slug  string     `json:"slug"`
	Names Names      `json:"names"`
	Query ShelfQuery `json:"query"`
	Sort  string     `json:"sort"`
	Icon  string     `json:"icon"`
}

// emptyHomePreferences 返回归一化的空偏好：三个字段都是空数组而不是 null，前端不必判空。
func emptyHomePreferences() HomePreferences {
	return HomePreferences{Order: []string{}, Hidden: []string{}, Sections: []HomeSection{}}
}

func (s *Store) GetHomePreferences(ctx context.Context, userID string) (HomePreferences, error) {
	if userID == "" {
		return emptyHomePreferences(), nil
	}
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT home_shelves FROM catalog.user_preferences WHERE user_id=$1`, userID).Scan(&b)
	if err == sql.ErrNoRows {
		return emptyHomePreferences(), nil
	}
	if err != nil {
		return emptyHomePreferences(), err
	}
	if len(b) == 0 {
		return HomePreferences{}, fmt.Errorf("invalid_home_preferences")
	}
	var out HomePreferences
	if err = json.Unmarshal(b, &out); err != nil {
		return HomePreferences{}, err
	}
	if out.Order == nil || out.Hidden == nil || out.Sections == nil {
		return HomePreferences{}, fmt.Errorf("invalid_home_preferences")
	}
	return out, nil
}

// GetUserFeedPreferences 一次读取用户的首页分区偏好与 18+ 成人内容隐藏标志，
// 避免 /shelves/feed 在每个货架求值时重复查询同一行 catalog.user_preferences。
func (s *Store) GetUserFeedPreferences(ctx context.Context, userID string) (HomePreferences, bool, error) {
	if strings.TrimSpace(userID) == "" || s == nil || s.DB == nil {
		return emptyHomePreferences(), false, nil
	}
	var b []byte
	var mode string
	err := s.DB.QueryRowContext(ctx, `SELECT home_shelves, adult_content_mode FROM catalog.user_preferences WHERE user_id=$1`, userID).Scan(&b, &mode)
	if err == sql.ErrNoRows {
		return emptyHomePreferences(), false, nil
	}
	if err != nil {
		return emptyHomePreferences(), false, err
	}
	if len(b) == 0 {
		return HomePreferences{}, false, fmt.Errorf("invalid_home_preferences")
	}
	var out HomePreferences
	if err = json.Unmarshal(b, &out); err != nil {
		return HomePreferences{}, false, err
	}
	if out.Order == nil || out.Hidden == nil || out.Sections == nil {
		return HomePreferences{}, false, fmt.Errorf("invalid_home_preferences")
	}
	hideAdult := false
	if norm, nerr := normalizeAdultContentMode(mode); nerr == nil {
		hideAdult = norm == AdultContentHide
	}
	return out, hideAdult, nil
}

// SaveHomePreferences 归一化并整份保存偏好。
//
// order/hidden 里的未知 slug 不校验、原样保留（旧实现报 unknown_shelf）：偏好属于用户，
// 管理员删货架后不能让用户连保存都失败；这些 slug 在 feed 合并阶段自然被忽略，
// 货架以同名重建时用户的排序也立刻恢复。
func (s *Store) SaveHomePreferences(ctx context.Context, userID string, in HomePreferences) (HomePreferences, error) {
	if in.Order == nil || in.Hidden == nil || in.Sections == nil {
		return HomePreferences{}, fmt.Errorf("invalid_home_preferences")
	}
	out, err := normalizeHomePreferences(in)
	if err != nil {
		return HomePreferences{}, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return HomePreferences{}, err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `
			INSERT INTO catalog.user_preferences(user_id,home_shelves)
			VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET home_shelves=EXCLUDED.home_shelves`,
			userID, string(b))
		return e
	})
	return out, err
}

// normalizeHomePreferences 归一化偏好并校验用户分区，返回的 Order/Hidden/Sections 都非 nil。
// 上限按提交的条目数判定（不是去重后的条目数）：超出即拒，报文错在哪就报哪，不做静默截断。
func normalizeHomePreferences(in HomePreferences) (HomePreferences, error) {
	if len(in.Sections) > maxHomeSections {
		return HomePreferences{}, fmt.Errorf("too_many_sections")
	}
	out := HomePreferences{Order: dedupeSlugs(in.Order), Hidden: dedupeSlugs(in.Hidden), Sections: []HomeSection{}}
	seen := map[string]bool{}
	for _, sec := range in.Sections {
		sec, err := normalizeHomeSection(sec)
		if err != nil {
			return HomePreferences{}, err
		}
		// 同 slug 只保留首次声明：一条 slug 只能有一种含义（覆盖同名系统货架，或自建），
		// 后面重复的声明无法表达新意图，与其让它静默改写前面那条，不如显式去重。
		if seen[sec.Slug] {
			continue
		}
		seen[sec.Slug] = true
		out.Sections = append(out.Sections, sec)
	}
	return out, nil
}

// normalizeHomeSection 校验并归一化单条分区声明，失败只给稳定错误码（前端按码提示）。
func normalizeHomeSection(sec HomeSection) (HomeSection, error) {
	sec.Slug = strings.TrimSpace(sec.Slug)
	if !shelfSlugPattern.MatchString(sec.Slug) {
		return HomeSection{}, fmt.Errorf("invalid_slug")
	}
	names := Names{}
	for key, value := range sec.Names {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// 空值不是"覆盖成空"，直接丢弃：留空只会把系统货架对应语种的名字抹掉。
		if key == "" || value == "" {
			continue
		}
		names[key] = value
	}
	sec.Names = names
	// 分区名只强制 zh-CN：它是用户给自己首页起的标题，其余语种缺省时前端按回退链显示；
	// 系统货架名是站内分类入口，仍走四语铁律（validateShelf）。
	if strings.TrimSpace(names["zh-CN"]) == "" {
		return HomeSection{}, fmt.Errorf("invalid_name")
	}
	if err := validateNameLocales(names); err != nil {
		return HomeSection{}, err
	}
	// 空 sort 与系统货架同一口径：白名单里的 "" 表示"未配置"，等价 updated，这里落成显式取值。
	if sec.Sort = strings.TrimSpace(sec.Sort); sec.Sort == "" {
		sec.Sort = "updated"
	}
	if !contains(shelfSortKeys, sec.Sort) {
		return HomeSection{}, fmt.Errorf("invalid_sort")
	}
	// 收录规则与系统货架共用同一份形状校验，避免两处实现走样。
	if err := validateShelfQuery(sec.Query); err != nil {
		return HomeSection{}, err
	}
	sec.Icon = strings.TrimSpace(sec.Icon)
	return sec, nil
}

func dedupeSlugs(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// applyHomePreferences 把系统货架与用户分区合并成该用户可见的分区序列，也是 /shelves/feed
// 的排序依据：
//  1. sections 命中系统货架 slug 即覆盖（只影响本人），未命中即作为自建分区追加；
//  2. 按 order 重排——系统与自建 slug 混排，未列出的按"系统 sort_order 优先、自建按声明顺序"排后；
//  3. 过滤 hidden，对系统与自建分区都生效。
//
// 每条的 source 在此标记：命中系统货架的记 system，用户自建的记 custom。
// 未知 slug 一律忽略：管理员删货架后旧偏好不能让首页缺内容。
func applyHomePreferences(shelves []Shelf, p HomePreferences) []Shelf {
	if len(shelves) == 0 && len(p.Sections) == 0 {
		return shelves
	}
	merged := make([]Shelf, 0, len(shelves)+len(p.Sections))
	at := map[string]int{}
	for _, sh := range shelves {
		sh.Source = ShelfSourceSystem
		at[sh.Slug] = len(merged)
		merged = append(merged, sh)
	}
	for i, sec := range p.Sections {
		// 停用（不在 enabledOnly 列表里）的系统货架当作自建：它本来就不上首页，谈不上覆盖。
		if pos, ok := at[sec.Slug]; ok {
			merged[pos] = sec.applyTo(merged[pos])
			continue
		}
		at[sec.Slug] = len(merged)
		merged = append(merged, sec.toShelf(i))
	}
	hidden := map[string]bool{}
	for _, s := range p.Hidden {
		hidden[s] = true
	}
	out := make([]Shelf, 0, len(merged))
	seen := map[string]bool{}
	// 先按用户顺序收录已具名的分区（系统与自建混排）。
	for _, s := range p.Order {
		if hidden[s] || seen[s] {
			continue
		}
		if pos, ok := at[s]; ok {
			out = append(out, merged[pos])
			seen[s] = true
		}
	}
	// 其余分区按默认序追加；显式排过序的已处理过。
	for _, sh := range merged {
		if hidden[sh.Slug] || seen[sh.Slug] {
			continue
		}
		out = append(out, sh)
		seen[sh.Slug] = true
	}
	return out
}

// toShelf 把自建分区转成可求值的货架：没有库内 id 与后台 sort_order，取基准值+声明顺序。
func (sec HomeSection) toShelf(index int) Shelf {
	return Shelf{
		Slug:      sec.Slug,
		Names:     sec.Names,
		Query:     sec.Query,
		Sort:      sec.Sort,
		Icon:      sec.Icon,
		Enabled:   true,
		SortOrder: customShelfSortBase + index,
		Source:    ShelfSourceCustom,
	}
}

// applyTo 把分区声明覆盖到系统货架上，返回该用户视角的货架（系统默认对其他用户不变）。
//
// names 逐语种覆盖：用户给了哪个语种就用哪个，其余保留系统名（分区名缺语种时前端只能
// 退回 slug，而用户通常只想改自己看的那个语种）；query/sort 整体替换——它们是单值声明，
// 半覆盖没有意义，且空 query 本就有"收录全部作品"的既有语义；icon 留空表示没声明，
// 保留系统图标。
func (sec HomeSection) applyTo(s Shelf) Shelf {
	if len(sec.Names) > 0 {
		names := make(Names, len(s.Names)+len(sec.Names))
		for k, v := range s.Names {
			names[k] = v
		}
		for k, v := range sec.Names {
			names[k] = v
		}
		s.Names = names
	}
	s.Query = sec.Query
	s.Sort = sec.Sort
	if sec.Icon != "" {
		s.Icon = sec.Icon
	}
	s.Source = ShelfSourceSystem
	return s
}

const (
	AdultContentHide = "hide"
	AdultContentBlur = "blur"
	AdultContentShow = "show"
)

// ContentPreferences controls per-account visibility for 18+ content (defaults to blur).
type ContentPreferences struct {
	AdultContentMode string `json:"adult_content_mode"`
}

func normalizeAdultContentMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case AdultContentHide, AdultContentBlur, AdultContentShow:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid_adult_content_mode")
	}
}

func (s *Store) GetContentPreferences(ctx context.Context, userID string) (ContentPreferences, error) {
	def := ContentPreferences{AdultContentMode: AdultContentBlur}
	if strings.TrimSpace(userID) == "" || s == nil || s.DB == nil {
		return def, nil
	}
	var mode string
	err := s.DB.QueryRowContext(ctx, `SELECT adult_content_mode FROM catalog.user_preferences WHERE user_id=$1`, userID).Scan(&mode)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	if norm, err := normalizeAdultContentMode(mode); err == nil {
		return ContentPreferences{AdultContentMode: norm}, nil
	}
	return def, nil
}

func (s *Store) SaveContentPreferences(ctx context.Context, userID string, in ContentPreferences) (ContentPreferences, error) {
	mode, err := normalizeAdultContentMode(in.AdultContentMode)
	if err != nil {
		return ContentPreferences{}, err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `
			INSERT INTO catalog.user_preferences(user_id, home_shelves, adult_content_mode)
			VALUES($1, '{"order":[],"hidden":[],"sections":[]}'::jsonb, $2)
			ON CONFLICT(user_id) DO UPDATE SET adult_content_mode=EXCLUDED.adult_content_mode`,
			userID, mode)
		return e
	})
	if err != nil {
		return ContentPreferences{}, err
	}
	return ContentPreferences{AdultContentMode: mode}, nil
}

func (s *Store) shouldHideAdultContent(ctx context.Context, u *User) (bool, error) {
	if u == nil || strings.TrimSpace(u.ID) == "" || s == nil || s.DB == nil {
		return false, nil
	}
	prefs, err := s.GetContentPreferences(ctx, u.ID)
	if err != nil {
		return false, err
	}
	return prefs.AdultContentMode == AdultContentHide, nil
}

// adultExclusionSQL returns a SQL predicate excluding 18+ entities from lists/shelves/search.
func adultExclusionSQL(alias string) string {
	doc := "document"
	if alias != "" {
		doc = alias + ".document"
	}
	return "NOT (" +
		"COALESCE(" + doc + "->'attributes'->>'content_rating','')='r18' " +
		"OR COALESCE(" + doc + "->'attributes'->>'creation_form','')='eroge' " +
		"OR " + doc + "->'attributes'->'tags' @> '[\"r18\"]'::jsonb " +
		"OR " + doc + "->'attributes'->'tags' @> '[\"18+\"]'::jsonb " +
		"OR " + doc + "->'attributes'->'tags' @> '[\"adult\"]'::jsonb" +
		")"
}
