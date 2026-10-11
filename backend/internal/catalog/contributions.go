package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// 用户贡献视图：前端用户主页的 all / revisions / works / releases / artists 五个 tab。
//
// 数据只来自目录自己的审计痕迹——catalog.revisions 的 actor 快照列（actor_id/actor_name，
// 见 store.go 的 audit）、catalog.entities 与独立的 lifecycle_actions——**不 JOIN auth / community 的表**：账号表归账号服务、
// 收藏与互动归互动服务，跨 schema 读会让两个系统在数据层重新耦合（同 store.go:401 的边界）。
// 两个直接后果是刻意的：不判断"用户是否存在"（没有任何修订就是零贡献，不查账号服务），
// 也不返回昵称与头像（那些字段属于账号服务，前端另行读取）。
//
// 可见性与 listFilter 同一口径：已删除/已合并对所有人不可见，未发布只有创建者与持
// catalog.lifecycle.manage 者可见。items 与 stats 都按这一口径过滤，因此匿名访客看到的统计只含
// 公开条目——既不泄漏草稿的存在，也不会出现"列表为空、统计却有数"。
//
// 修订查询按 actor_id 过滤；生命周期动作有独立 actor 索引。027 的修订 target/version
// 索引用于动作快照关联和上一版回读，不把事件队列作为贡献统计的事实源。

// ContributionDiffEntry 是一个字段改动的前后值；键是字段路径（顶层字段，attributes/translations
// 下钻一层），值形状与前端 DiffViewer 的输入一致。
type ContributionDiffEntry struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// ContributionItem 是贡献流的一项，两种项共用一个结构体：
//   - 创建项（works / releases / artists 三类 tab，以及 all 里的创建事件）：id 就是实体 id，
//     带 status 与实体自身的 updated_at；
//   - 修订项（revisions tab，以及 all 里的后续改动）：id 是修订行 id（列表去重与排序的键），
//     带 version 与 diff，实体 id 落在 target_id。
//
// 字段命名沿用既有端点：修订项复用 GET /catalog/entities/:id/revisions 的 version/edit_note/
// sources/created_at，实体项复用实体列表的 kind/title/status/updated_at；动作取 edit_type
// （create/update，与前端 edit_type 徽章同一套取值），不新造一套字段名。
type ContributionItem struct {
	ID        string                           `json:"id"`
	Tab       string                           `json:"tab"`
	Kind      string                           `json:"kind"`
	Title     string                           `json:"title"`
	TargetID  string                           `json:"target_id"`
	Version   int64                            `json:"version"`
	EditType  string                           `json:"edit_type"`
	EditNote  string                           `json:"edit_note"`
	Sources   []Source                         `json:"sources,omitempty"`
	Status    string                           `json:"status,omitempty"`
	CreatedAt time.Time                        `json:"created_at"`
	UpdatedAt *time.Time                       `json:"updated_at,omitempty"`
	Diff      map[string]ContributionDiffEntry `json:"diff,omitempty"`
}

// UserContributionStats 是用户贡献的五个计数，全部来自目录 actor 快照列（不 JOIN 账号表）：
// works/releases/artists_created 是 version=1 的首次修订条数（= 该用户创建的条数，
// artists 对应骨架里的 agent kind），revisions_count 是可见实体上的修订行数，
// audit_actions 是生命周期管理动作（删除/合并）的发生次数，不随目标当前状态变化，
// 与前三者与 revisions_count 的差别见 userContributionStats 的注释。
type UserContributionStats struct {
	WorksCreated    int64 `json:"works_created"`
	ReleasesCreated int64 `json:"releases_created"`
	ArtistsCreated  int64 `json:"artists_created"`
	RevisionsCount  int64 `json:"revisions_count"`
	AuditActions    int64 `json:"audit_actions"`
}

// UserContributions 是贡献页的响应：items 是当前页，total 是同口径下的真实计数
// （不是本页条数，与其它列表端点一致），stats 恒为全量五个计数，不随 tab 变化。
type UserContributions struct {
	Items    []ContributionItem    `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	Stats    UserContributionStats `json:"stats"`
}

// contributionCreationKinds 是"创建项"覆盖的 kind：作品、发行、艺术家。前端口径的 artists 在
// 目录骨架里是 agent（types.go 的 Kinds 没有 artist），tab 名与 kind 的映射见 contributionKindTab。
var contributionCreationKinds = []string{"work", "release", "agent"}

// contributionKindTab 把 kind 映射回 tab 名；不在创建项范围内的 kind（篇目、载体、轨道等
// 只能作为修订项出现）返回空串。
func contributionKindTab(kind string) string {
	switch kind {
	case "work":
		return "works"
	case "release":
		return "releases"
	case "agent":
		return "artists"
	}
	return ""
}

// contributionTabValid 校验 tab 取值：目录侧只服务这五个，其余（topics/comments/audits 属互动
// 服务的口径）直接 400 invalid_tab——静默返回空列表会让前端把"不归目录"显示成"没有内容"。
func contributionTabValid(tab string) bool {
	switch tab {
	case "all", "revisions", "works", "releases", "artists":
		return true
	}
	return false
}

// UserContributions 读某个用户（按 actor 快照列归属）的贡献流。
//
// tab 语义：all = 创建项（works/releases/artists 的首次修订）与后续改动（version>1 的实体修订）
// 按时间混排；revisions = 全部实体修订行（含首次创建）；其余三个 tab 只列该用户创建的对应实体。
// 分页口径与拆分前该端点一致：page 从 1 起（越界收敛为 1），page_size 越界（<1 或 >100）收敛为
// 20，不硬拒绝（列表接口的既有风格）。
func (s *Store) UserContributions(ctx context.Context, userID, tab string, page, pageSize int, u *User) (UserContributions, error) {
	// 非法 uuid 与账号 GET /users/{id}、互动 GET /users/{id}/stats 同口径：404 not_found
	// （sql.ErrNoRows 经 respond 映射）。用户主页把三路数据源按"同一类降级"处理，目录这一路
	// 回 400 invalid_id 会让前端把"这个来源取不到"讲成"参数错误"。
	// "账号不存在"与"有这个人但一条贡献都没有"分不出来：账号表归账号服务，目录不查它，
	// 两者都是零贡献的 200（同 community 侧 stats.go 的注释），404 只留给"这个 id 不是 uuid"。
	if _, err := uuid.Parse(userID); err != nil {
		return UserContributions{}, sql.ErrNoRows
	}
	if !contributionTabValid(tab) {
		return UserContributions{}, fmt.Errorf("invalid_tab")
	}
	page, pageSize = contributionPage(page, pageSize)
	stats, err := s.userContributionStats(ctx, userID, u)
	if err != nil {
		return UserContributions{}, err
	}
	rows, total, err := s.userContributionRows(ctx, userID, tab, pageSize, (page-1)*pageSize, u)
	if err != nil {
		return UserContributions{}, err
	}
	items, err := s.userContributionItems(ctx, rows, tab != "revisions")
	if err != nil {
		return UserContributions{}, err
	}
	return UserContributions{Items: items, Total: total, Page: page, PageSize: pageSize, Stats: stats}, nil
}

// contributionPage 收敛分页参数：page 从 1 起，page_size 越界（<1 或 >100）回落到 20。
// 越界静默收敛而不是 400，与实体列表（List 的 limit）同一风格；上限 100 与 List 保持一致。
func contributionPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

// contributionWhere 拼出"该用户 + 查看者可见性"的行过滤条件（列名带 r./e. 前缀）。
// $1 恒为该用户的 actor_id，由调用方先放进 args；非管理员的登录查看者会再追加一个参数。
// 可见性与 listFilter 逐条对应：先剔墓碑状态，匿名只看已发布，登录非管理员额外放行自己创建的未发布项。
func contributionWhere(u *User, args *[]any) string {
	parts := []string{"r.actor_id = $1", "e.status NOT IN ('deleted','merged')"}
	if u == nil {
		parts = append(parts, "e.status = 'published'")
	} else if !u.Can(PermissionLifecycleManage) {
		*args = append(*args, u.ID)
		parts = append(parts, fmt.Sprintf("(e.status = 'published' OR e.created_by = $%d)", len(*args)))
	}
	return strings.Join(parts, " AND ")
}

// contributionFrom 是贡献流两条查询共用的 FROM：实体修订行按 target_id 对上实体。
// 用 JOIN 而不是逐行判定目标类型：关系修订（target_id 是关系 id）与定义修订（target_id 是
// definitions:<id>）在 entities 里没有行，自然被排除——用户主页的口径是"实体贡献"，
// 关系与定义修订不在这里列（它们也没有面向公众的可见性概念）。
const contributionFrom = " FROM catalog.revisions r JOIN catalog.entities e ON e.id::text = r.target_id "

// contributionSelectColumns 与上面的 FROM 配套，供 userContributionRows 扫描。
const contributionSelectColumns = "r.id, r.version, e.id::text, e.kind, e.title, e.status, r.edit_note, r.sources, r.created_at, e.updated_at, r.snapshot"

// contributionRow 是贡献流的原始行：snapshot 只用于算差异，不进响应（整份实体 JSON 按页返回
// 会让响应体随快照大小失控）。
type contributionRow struct {
	revID     int64
	version   int64
	entityID  string
	kind      string
	title     string
	status    string
	editNote  string
	sources   []byte
	createdAt time.Time
	updatedAt time.Time
	snapshot  []byte
}

// userContributionRows 取某一页的行与同谓词下的真实总数（total 不用本页条数代）。
// 排序用 (created_at, 修订行 id) 双键：修订行 id 是自增主键，同一时刻写入的多行也有稳定顺序，
// 翻页不会因并列时间戳漂移。
func (s *Store) userContributionRows(ctx context.Context, userID, tab string, limit, offset int, u *User) ([]contributionRow, int64, error) {
	args := []any{userID}
	where := contributionWhere(u, &args)
	switch tab {
	case "works", "releases", "artists":
		// 创建项 = 该实体 version=1 的首次修订：创建者的判定完全落在 actor 快照列上。
		args = append(args, contributionKindOfTab(tab))
		where = fmt.Sprintf("%s AND r.version = 1 AND e.kind = $%d", where, len(args))
	case "all":
		// 创建项按创建 kind 收敛（篇目/载体/轨道只以改动出现），后续改动取 version>1；
		// 二者互斥，混排后同一事件不会既显示成"创建"又显示成"修订"。
		args = append(args, pq.Array(contributionCreationKinds))
		where = fmt.Sprintf("%s AND (r.version > 1 OR e.kind = ANY($%d::text[]))", where, len(args))
	}
	var total int64
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*)"+contributionFrom+" WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	query := "SELECT " + contributionSelectColumns + contributionFrom + " WHERE " + where +
		fmt.Sprintf(" ORDER BY r.created_at DESC, r.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []contributionRow{}
	for rows.Next() {
		var row contributionRow
		if err = rows.Scan(&row.revID, &row.version, &row.entityID, &row.kind, &row.title, &row.status, &row.editNote, &row.sources, &row.createdAt, &row.updatedAt, &row.snapshot); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// contributionKindOfTab 把 tab 映射成 kind（取值校验已在 UserContributions 完成）。
func contributionKindOfTab(tab string) string {
	switch tab {
	case "works":
		return "work"
	case "releases":
		return "release"
	case "artists":
		return "agent"
	}
	return ""
}

// userContributionStats 统计五个计数，全部落在目录 actor 快照列上，
// 不 JOIN 账号表。前四个与列表共用贡献口径（contributionWhere 的可见性过滤）：
//   - works_created / releases_created / artists_created：version=1 的首次修订条数，即"该用户创建、
//     且当前可见"的实体数（artists 对应 agent kind）；
//   - revisions_count：该用户在可见实体上的全部修订行数（含首次创建行）。
//
// audit_actions 单独一条查询，口径刻意不同：数的是"该用户执行过多少次生命周期管理动作"
// （entity.deleted / entity.merged，由 catalog.lifecycle.manage 端点写出；普通保存写 entity.saved，
// 不计入）。删除与合并在做完的同一刻就把目标移出可见集，若也套可见性过滤，这个数字恒为 0——
// 管理动作按"发生过"计数，与目标当前状态无关，这是它与前四个的唯一差别（注释写清，免得被当成漏过滤）。
// lifecycle_actions 与生命周期事件同事务快照 actor 和动作，按 revision_id 去重；
// 独立持久表不引用 outbox 或目标实体，队列清理和目标历史变化不减少已发生的管理动作。
func (s *Store) userContributionStats(ctx context.Context, userID string, u *User) (UserContributionStats, error) {
	args := []any{userID}
	where := contributionWhere(u, &args)
	var st UserContributionStats
	err := s.DB.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE r.version = 1 AND e.kind = 'work'),
			count(*) FILTER (WHERE r.version = 1 AND e.kind = 'release'),
			count(*) FILTER (WHERE r.version = 1 AND e.kind = 'agent'),
			count(*)
		`+contributionFrom+`
		WHERE `+where, args...).Scan(&st.WorksCreated, &st.ReleasesCreated, &st.ArtistsCreated, &st.RevisionsCount)
	if err != nil {
		return st, err
	}
	err = s.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM catalog.lifecycle_actions
		WHERE actor_id = $1`, userID).Scan(&st.AuditActions)
	return st, err
}

// userContributionItems 把原始行转成响应项：创建项带实体状态，修订项带与上一条修订的差异。
// creationMode 为假（revisions tab）时全部按修订项输出，为真时 version=1 的行输出成创建项。
func (s *Store) userContributionItems(ctx context.Context, rows []contributionRow, creationMode bool) ([]ContributionItem, error) {
	prev, err := s.previousContributionSnapshots(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]ContributionItem, 0, len(rows))
	for _, row := range rows {
		item := ContributionItem{
			Tab: "revisions", Kind: row.kind, Title: row.title, TargetID: row.entityID,
			Version: row.version, EditNote: row.editNote, CreatedAt: row.createdAt,
		}
		if len(row.sources) > 0 {
			var sources []Source
			if json.Unmarshal(row.sources, &sources) == nil {
				item.Sources = sources
			}
		}
		if creationMode && row.version == 1 {
			updated := row.updatedAt
			item.ID, item.Tab, item.EditType, item.Status, item.UpdatedAt = row.entityID, contributionKindTab(row.kind), "create", row.status, &updated
		} else {
			item.ID = strconv.FormatInt(row.revID, 10)
			item.EditType = "create"
			if row.version > 1 {
				item.EditType = "update"
				// 首次修订没有可比的前一版（diff 留空），改动项才有差异可展开。
				if before, ok := prev[contributionSnapshotKey(row.entityID, row.version)]; ok {
					item.Diff = contributionDiff(before, row.snapshot)
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func contributionSnapshotKey(entityID string, version int64) string {
	return entityID + "|" + strconv.FormatInt(version, 10)
}

// previousContributionSnapshots 一次取回本页每条改动项的"上一条修订"快照（同一目标取 version
// 最大且小于本条的一条），供差异比较：逐条查会形成 N+1（页大小最多 100 条）。
// 比较基线是该目标的全局前一条修订，不限于本人——差异要说明的是"这一次改动改了什么"。
func (s *Store) previousContributionSnapshots(ctx context.Context, rows []contributionRow) (map[string][]byte, error) {
	ids, versions := []string{}, []int64{}
	for _, row := range rows {
		if row.version > 1 {
			ids = append(ids, row.entityID)
			versions = append(versions, row.version)
		}
	}
	out := map[string][]byte{}
	if len(ids) == 0 {
		return out, nil
	}
	got, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT ON (p.entity_id, p.version) p.entity_id, p.version, r.snapshot
		FROM unnest($1::text[], $2::bigint[]) AS p(entity_id, version)
		JOIN catalog.revisions r ON r.target_id = p.entity_id AND r.version < p.version
		ORDER BY p.entity_id, p.version, r.version DESC`, pq.Array(ids), pq.Array(versions))
	if err != nil {
		return nil, err
	}
	defer got.Close()
	for got.Next() {
		var entityID string
		var version int64
		var snapshot []byte
		if err = got.Scan(&entityID, &version, &snapshot); err != nil {
			return nil, err
		}
		out[contributionSnapshotKey(entityID, version)] = snapshot
	}
	return out, got.Err()
}

// contributionDiffIgnored 与前端口径一致（frontend/src/components/catalog/revisionData.ts 的
// revisionChanges）：这些键每次写入都会变，列进差异只是噪声。
var contributionDiffIgnored = map[string]bool{"id": true, "version": true, "created_by": true, "created_at": true, "updated_at": true}

// contributionDiffMaxValue 是单个值进响应的字节上限：差异条目是"能展开看的摘要"，不是第二份快照，
// 单个新增的大子树不能把响应拉回 MB 级。
const contributionDiffMaxValue = 512

// contributionDiff 比较两条修订快照，键为字段路径。口径与前端 revisionChanges 逐条对齐：
// attributes 与 translations 下钻一层（attributes.tags、translations.zh-CN），其余按顶层字段比；
// 值经 encode（json.Marshal，键序稳定）归一后比较。没有字段变化时返回 nil，响应里整个 diff 键省略。
func contributionDiff(before, after []byte) map[string]ContributionDiffEntry {
	var b, a map[string]any
	if json.Unmarshal(before, &b) != nil || json.Unmarshal(after, &a) != nil {
		return nil
	}
	out := map[string]ContributionDiffEntry{}
	for _, key := range contributionDiffKeys(b, a) {
		if contributionDiffIgnored[key] || strings.HasPrefix(key, "localized_") {
			continue
		}
		if key == "attributes" || key == "translations" {
			bm, _ := b[key].(map[string]any)
			am, _ := a[key].(map[string]any)
			for _, child := range contributionDiffKeys(bm, am) {
				contributionDiffPut(out, key+"."+child, bm[child], am[child])
			}
			continue
		}
		contributionDiffPut(out, key, b[key], a[key])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// contributionDiffKeys 是两张 map 的键并集（排序后返回，输出可断言）；缺键的取值是 nil，
// 与显式 null 同义——两侧都缺或都为空时 encode 结果相同，自然不算变更。
func contributionDiffKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for key := range m {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

func contributionDiffPut(out map[string]ContributionDiffEntry, key string, before, after any) {
	if encode(before) == encode(after) {
		return
	}
	out[key] = ContributionDiffEntry{Old: contributionDiffValue(before), New: contributionDiffValue(after)}
}

// contributionDiffValue 收敛单个值：字符串按自身截断，其它形态取序列化前缀（复用定义差异的
// utf8 安全截断），超限时类型会退化成字符串——这是有意的，摘要不该带整棵子树。
func contributionDiffValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if len(t) <= contributionDiffMaxValue {
			return t
		}
		return contributionDiffPrefix(t)
	}
	raw := encode(v)
	if len(raw) <= contributionDiffMaxValue {
		return v
	}
	return contributionDiffPrefix(raw)
}

func contributionDiffPrefix(s string) string {
	cut := s[:contributionDiffMaxValue]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
