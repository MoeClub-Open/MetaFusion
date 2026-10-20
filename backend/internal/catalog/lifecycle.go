package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
)

type LifecycleEdit struct {
	ExpectedVersion int64    `json:"expected_version"`
	TargetID        string   `json:"target_id"`
	EditNote        string   `json:"edit_note"`
	Sources         []Source `json:"sources"`
}

// 生命周期只维护删除和合并。正常条目为 active，审核发生在提交上。
// 终态仍由权限与身份解析控制读取；恢复事实使用新提交，不改历史快照。
func (s *Store) Lifecycle(ctx context.Context, id string, input LifecycleEdit, u User) (Entity, error) {
	var e Entity
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		if err := setCatalogVersionActor(ctx, tx, u, nil, input.EditNote); err != nil {
			return err
		}
		var err error
		e, err = s.lifecycleTx(ctx, tx, id, input, u)
		return err
	})
	return e, err
}

func (s *Store) lifecycleTx(ctx context.Context, tx *sql.Tx, id string, input LifecycleEdit, u User) (Entity, error) {
	var e Entity
	// SSI includes concurrent references and relation changes in lifecycle checks.
	err := func() error {
		if !u.Can(PermissionLifecycleManage) {
			return errForbidden
		}
		if err := validateSources(input.Sources); err != nil {
			return err
		}
		var err error
		e, err = get(ctx, tx, id)
		if err != nil {
			return err
		}
		if e.Version != input.ExpectedVersion {
			return errVersionConflict
		}
		if e.State == "merged" || e.State == "deleted" {
			return errInvalidState
		}
		e.Version++
		e.State = "deleted"
		e.UpdatedAt = time.Now().UTC()
		if input.TargetID != "" {
			target, err := get(ctx, tx, input.TargetID)
			if err != nil {
				return err
			}
			// 合并目标必须同 kind、同归属（Work/Release/Medium/ContentUnitID 均相等，
			// 含 ParentID）：跨父合并会撕裂层级，由复合外键在提交时拦截，
			// 此处提前报 invalid_merge_target。目标必须 active。
			if target.Kind != e.Kind || target.ID == e.ID || target.State == "deleted" || target.State == "merged" || target.WorkID != e.WorkID || target.ReleaseID != e.ReleaseID || target.MediumID != e.MediumID || target.ParentID != e.ParentID {
				return fmt.Errorf("invalid_merge_target")
			}
			e.RedirectID = target.ID
			e.State = "merged"
			if target.ContentUnitID != e.ContentUnitID || target.State != "active" {
				return fmt.Errorf("invalid_merge_target")
			}
			if err = mergeReferences(ctx, tx, e, target, u, input); err != nil {
				return err
			}
		}
		stored := e
		stored.WorkID = ""
		stored.ContentUnitID = ""
		stored.ReleaseID = ""
		stored.MediumID = ""
		stored.ParentID = ""
		stored.Contents = nil
		stored.Subjects = nil
		// 纯删除（非合并）级联清理关系边：实体作为 source 或 target 的边都不再有意义。
		// 合并分支由 mergeReferences 把端点改写到 target，不能在这里重复删。
		// 不逐条记 relation.deleted 审计——entity.deleted 的修订已留痕，边是其级联后果。
		if input.TargetID == "" {
			if _, err = tx.ExecContext(ctx, "DELETE FROM catalog.relations WHERE source_id=$1 OR target_id=$1", e.ID); err != nil {
				return err
			}
		}
		// 与 Save 同一原则：版本条件进 WHERE，读版本与写入是同一次原子操作。
		// 事务重试之后也要复核业务版本。
		var res sql.Result
		if res, err = tx.ExecContext(ctx, "UPDATE catalog.entities SET version=$3,state=$4,document=$5,updated_at=$6 WHERE id=$1 AND version=$2", e.ID, input.ExpectedVersion, e.Version, e.State, encode(stored), e.UpdatedAt); err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errVersionConflict
		}
		return audit(ctx, tx, e.ID, e.Version, u, input.EditNote, input.Sources, e, "entity."+e.State)
	}()
	return e, err
}

// IdentityResolution 是目录侧统一身份解析契约（X01）：canonical_id 为存活身份，
// aliases 为历史别名集合（merged 链上依次经过的旧 ID，含请求 ID 自身若已合并）。
// 新写入归一：reference() 拒绝 merged/deleted 行的引用，调用方先经本契约
// （GET /api/catalog/entities/{id}/identity，批量走 POST /api/catalog/entities/identity）
// 拿到 canonical 再写；读取汇别名：Occurrences 等读路径先 Resolve 到存活身份再聚合。
// 跨服务（文件/评论/收藏）按别名集合聚合，不直接写别人的库；outbox 的
// entity.merged 仍是唯一的跨服务事实来源（见 merge.go），批量端点只是它的只读投影。
type IdentityResolution struct {
	CanonicalID string   `json:"canonical_id"`
	Aliases     []string `json:"aliases"`
	Entity      Entity   `json:"entity"`
	// Complete 为真表示别名集合是全集（反向遍历全部成功）。跨服务聚合只能在
	// complete=true 时按全集收敛；失败路径返回 error 且 complete=false，调用方不得
	// 把部分结果当全集用（R2：此前 Query/Scan 失败会伪装成成功的部分结果）。
	Complete bool `json:"complete"`
}

// errRedirectCycle 是循环重定向的稳定码：哨兵化以便调用方 errors.Is 区分
// "不存在"与"查询失败"（见 isIdentityNotFound），文本与此前一致。
var errRedirectCycle = errors.New("redirect_cycle")

// isIdentityNotFound 报告身份解析错误是否属于"不存在"（未知 ID、不可见终点、循环链）：
// 批量端点按此进 missing；其它错误（超时、中断、连接失败）是查询失败，必须整批 500。
func isIdentityNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, errRedirectCycle)
}

// ResolveIdentity 跟随 merged 链并收集别名：循环重定向报 redirect_cycle；
// 终点不可见按不存在处理，与 Resolve 同口径（同为 404 not_found）。
// 别名集合含两部分（去重）：向前链上依次经过的旧 ID，以及存活身份的全部历史别名
// （反向遍历 redirect 边，见 reverseAliases）：A→C、B→C、C→D 后从 D 可枚举 A/B/C，
// 分支合并与多跳链一次收齐。跨服务（文件/评论/收藏）按此集合聚合。
func (s *Store) ResolveIdentity(ctx context.Context, id string, u *User) (IdentityResolution, error) {
	var out IdentityResolution
	seen := map[string]bool{}
	cur := id
	for {
		if seen[cur] {
			return out, errRedirectCycle
		}
		seen[cur] = true
		e, err := get(ctx, s.DB, cur)
		if err != nil {
			return out, err
		}
		if e.State == "merged" {
			out.Aliases = append(out.Aliases, cur)
			cur = e.RedirectID
			continue
		}
		if !visible(e, u) {
			return out, sql.ErrNoRows
		}
		e, err = visibleEntityContents(ctx, s.DB, e, u)
		if err != nil {
			return out, err
		}
		out.CanonicalID, out.Entity = e.ID, e
		extra, err := s.reverseAliases(ctx, e.ID, seen)
		if err != nil {
			return out, err
		}
		out.Aliases = append(out.Aliases, extra...)
		out.Complete = true
		return out, nil
	}
}

// reverseAliases 从存活身份反向枚举全部历史别名：逐层查直接指向本层节点的 merged 行
// （document->>'redirect_id'，按 ANY($1) 批量查一层，见 000008 的表达式索引；
// redirect_id 列已随 000004 删除）。向前链已收集的只去重不丢弃：已见节点仍要展开
// （其上游可能不在向前链上，如 X→A→C 时从 A 查必须带回 X），增补部分排序后返回
// （确定性顺序）；环数据由 visited 截断（向前链的 redirect_cycle 仍由主循环报）。
//
// R2：Query/Scan/rows.Err 的任何失败都返回 error，不再伪装成成功的部分结果。
// 调用方用 isIdentityNotFound 区分"不存在"与"查询失败"。
func (s *Store) reverseAliases(ctx context.Context, canonical string, seen map[string]bool) ([]string, error) {
	visited := map[string]bool{canonical: true}
	extra := []string{}
	frontier := []string{canonical}
	for len(frontier) > 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT id::text FROM catalog.entities WHERE state='merged' AND document->>'redirect_id' = ANY($1::text[])`, pq.Array(frontier))
		if err != nil {
			return nil, err
		}
		var next []string
		scanErr := func() error {
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				if visited[id] {
					continue
				}
				visited[id] = true
				next = append(next, id)
				if !seen[id] {
					extra = append(extra, id)
				}
			}
			return rows.Err()
		}()
		if scanErr != nil {
			return nil, scanErr
		}
		frontier = next
	}
	sort.Strings(extra)
	return extra, nil
}

// Resolve preserves old identifiers without rewriting the evidence of a merge.
func (s *Store) Resolve(ctx context.Context, id string, u *User) (Entity, error) {
	return resolveFrom(ctx, s.DB, id, u)
}

func resolveFrom(ctx context.Context, q queryer, id string, u *User) (Entity, error) {
	seen := map[string]bool{}
	for !seen[id] {
		seen[id] = true
		e, err := get(ctx, q, id)
		if err != nil {
			return e, err
		}
		if e.State == "merged" {
			id = e.RedirectID
			continue
		}
		if !visible(e, u) {
			return Entity{}, sql.ErrNoRows
		}
		return visibleEntityContents(ctx, q, e, u)
	}
	return Entity{}, errRedirectCycle
}
