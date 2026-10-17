package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"sort"
	"strconv"
	"strings"
)

// entityPlaceholders 为 IN 批量查询生成 ($k,$k+1,...) 占位符, 复用字符串
// UUID 绑定风格(PG 侧 uuid 列与文本参数可比), 不新增驱动依赖。
func entityPlaceholders(ids []string, start int) string {
	ph := make([]string, len(ids))
	for i := range ids {
		ph[i] = "$" + strconv.Itoa(start+i)
	}
	return strings.Join(ph, ",")
}

func entityArgs(ids []string) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// getMany 一次 IN 批量拉取实体并按 kind 批量补齐侧表, 替代逐行 Get 的 N+1。
// 仅返回 visible 的实体; 不可见/缺失直接从 map 中省略, 调用方按“缺失即跳过”处理。
func (s *Store) getMany(ctx context.Context, ids []string, u *User) (map[string]Entity, error) {
	return getManyFrom(ctx, s.DB, ids, u)
}

func getManyFrom(ctx context.Context, q queryer, ids []string, u *User) (map[string]Entity, error) {
	out := map[string]Entity{}
	uniq := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT id::text, document, state, created_by::text FROM catalog.entities WHERE id = ANY($1::uuid[])", pq.Array(uniq))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state, createdBy string
		var b []byte
		var e Entity
		if err = rows.Scan(&id, &b, &state, &createdBy); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			rows.Close()
			return nil, err
		}
		e.ID, e.State, e.CreatedBy = id, state, createdBy
		if !visible(e, u) {
			continue
		}
		out[id] = e
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if out, err = fillStructural(ctx, q, out); err != nil {
		return nil, err
	}
	if err = filterVisibleTrackContents(ctx, q, out, u); err != nil {
		return nil, err
	}
	return out, nil
}

// fillStructural 按 kind 批量补齐结构侧表字段（content_unit/expression 的 work 与父级、
// medium/track 的所属与父子、track 的收录内容、release 的 subjects）。
// document 里这些字段落库时被清空，只解 JSON 会拿到空值，因此任何要消费结构字段的
// 读取路径都必须经此补齐；按 kind 分批查询，避免全库检查超过 PostgreSQL 参数上限。
// 取 queryer 而非 *sql.DB：启动期定义回放在事务外跑，而 Publish 的回放要在事务快照内看同一批数据。
func fillStructural(ctx context.Context, q queryer, out map[string]Entity) (map[string]Entity, error) {
	byKind := map[string][]string{}
	for id, e := range out {
		byKind[e.Kind] = append(byKind[e.Kind], id)
	}
	link2 := func(kind, query string, scan func(*sql.Rows) error, suffix ...string) error {
		sub := byKind[kind]
		if len(sub) == 0 {
			return nil
		}
		statement := query + " = ANY($1::uuid[])"
		if len(suffix) > 0 {
			statement += suffix[0]
		}
		for start := 0; start < len(sub); start += refLookupChunk {
			end := min(start+refLookupChunk, len(sub))
			r, qerr := q.QueryContext(ctx, statement, pq.Array(sub[start:end]))
			if qerr != nil {
				return qerr
			}
			for r.Next() {
				if err := scan(r); err != nil {
					r.Close()
					return err
				}
			}
			qerr = r.Err()
			r.Close()
			if qerr != nil {
				return qerr
			}
		}
		return nil
	}
	put := func(id string, e Entity) {
		if _, ok := out[id]; ok {
			out[id] = e
		}
	}
	if err := link2("content_unit", "SELECT id::text, work_id::text, coalesce(parent_id::text,'') FROM catalog.content_units WHERE id", func(r *sql.Rows) error {
		var id, work, parent string
		if err := r.Scan(&id, &work, &parent); err != nil {
			return err
		}
		cur := out[id]
		cur.WorkID, cur.ParentID = work, parent
		put(id, cur)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := link2("expression", "SELECT id::text, work_id::text, coalesce(content_unit_id::text,'') FROM catalog.expressions WHERE id", func(r *sql.Rows) error {
		var id, work, unit string
		if err := r.Scan(&id, &work, &unit); err != nil {
			return err
		}
		cur := out[id]
		cur.WorkID, cur.ContentUnitID = work, unit
		put(id, cur)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := link2("medium", "SELECT id::text, release_id::text, coalesce(parent_id::text,'') FROM catalog.mediums WHERE id", func(r *sql.Rows) error {
		var id, rel, parent string
		if err := r.Scan(&id, &rel, &parent); err != nil {
			return err
		}
		cur := out[id]
		cur.ReleaseID, cur.ParentID = rel, parent
		put(id, cur)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := link2("track", "SELECT id::text, medium_id::text, coalesce(parent_id::text,'') FROM catalog.tracks WHERE id", func(r *sql.Rows) error {
		var id, med, parent string
		if err := r.Scan(&id, &med, &parent); err != nil {
			return err
		}
		cur := out[id]
		cur.MediumID, cur.ParentID = med, parent
		cur.Contents = []Inclusion{}
		put(id, cur)
		return nil
	}); err != nil {
		return nil, err
	}
	if len(byKind["track"]) > 0 {
		r, err := q.QueryContext(ctx, "SELECT track_id::text, expression_id::text, position, locator, attributes,sources FROM catalog.track_contents WHERE track_id = ANY($1::uuid[]) ORDER BY track_id, position", pq.Array(byKind["track"]))
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var tid string
			var c Inclusion
			var loc, attrs, sources []byte
			if err = r.Scan(&tid, &c.ExpressionID, &c.Position, &loc, &attrs, &sources); err != nil {
				r.Close()
				return nil, err
			}
			if err = json.Unmarshal(sources, &c.Sources); err != nil {
				r.Close()
				return nil, err
			}
			if len(loc) > 0 {
				if err = json.Unmarshal(loc, &c.Locator); err != nil {
					r.Close()
					return nil, err
				}
			}
			if len(attrs) > 0 {
				if err = json.Unmarshal(attrs, &c.Attributes); err != nil {
					r.Close()
					return nil, err
				}
			}
			if cur, ok := out[tid]; ok && cur.Kind == "track" {
				cur.Contents = append(cur.Contents, c)
				out[tid] = cur
			}
		}
		if err = r.Err(); err != nil {
			r.Close()
			return nil, err
		}
		r.Close()
	}
	if err := link2("release", "SELECT release_id::text, work_id::text, role, position, attributes FROM catalog.release_subjects WHERE release_id", func(r *sql.Rows) error {
		var rid string
		var x Subject
		var attrs []byte
		if err := r.Scan(&rid, &x.WorkID, &x.Role, &x.Position, &attrs); err != nil {
			return err
		}
		if err := json.Unmarshal(attrs, &x.Attributes); err != nil {
			return err
		}
		if cur, ok := out[rid]; ok && cur.Kind == "release" {
			if cur.Subjects == nil {
				cur.Subjects = []Subject{}
			}
			cur.Subjects = append(cur.Subjects, x)
			out[rid] = cur
		}
		return nil
	}, " ORDER BY release_id,position,work_id"); err != nil {
		return nil, err
	}
	return out, nil
}

func relations(ctx context.Context, q queryer) ([]Relation, error) {
	rows, err := q.QueryContext(ctx, "SELECT document FROM catalog.relations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Relation{}
	for rows.Next() {
		var b []byte
		var r Relation
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// relationByID 按关系 ID 取单条关系；不存在时返回 sql.ErrNoRows。
func relationByID(ctx context.Context, q queryer, id string) (Relation, error) {
	var b []byte
	var r Relation
	if err := q.QueryRowContext(ctx, "SELECT document FROM catalog.relations WHERE id=$1", id).Scan(&b); err != nil {
		return r, err
	}
	err := json.Unmarshal(b, &r)
	return r, err
}

// relationsWithEndpoint 取以某实体为任一端点的全部边：合并改写引用时用，
// 校验阶段由调用方按涉及的关系规则读取受影响集合。
func relationsWithEndpoint(ctx context.Context, q queryer, id string) ([]Relation, error) {
	rows, err := q.QueryContext(ctx, "SELECT document FROM catalog.relations WHERE source_id=$1 OR target_id=$1 ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Relation{}
	for rows.Next() {
		var b []byte
		var r Relation
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Relations selects editable semantic facts from the unified relationship projection.
func (s *Store) Relations(ctx context.Context, id string, u *User) ([]Relation, error) {
	out := []Relation{}
	for offset := 0; ; {
		page, err := s.EntityLinks(ctx, id, 100, offset, u)
		if err != nil {
			return nil, err
		}
		for _, link := range page.Items {
			if link.Class != "semantic" {
				continue
			}
			if link.Version == nil {
				return nil, fmt.Errorf("missing relation version")
			}
			out = append(out, Relation{ID: link.Key, Version: *link.Version,
				Type: strings.TrimPrefix(link.RuleCode, "relation:"), SourceID: link.SourceID,
				TargetID: link.TargetID, Position: link.Position, Attributes: link.Attributes})
		}
		if !page.HasMore {
			break
		}
		offset += page.Limit
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

// 关系源端遵循实体编辑权限（catalog.entity.edit）；公开目标可由受信任编辑员建立关系。
// 两端的实际可见性与生命周期仍在 SaveRelation 中复核。
func canWriteRelation(u User, src Entity) bool {
	if u.FromPAT && u.Can(PermissionRelationEdit) && ownedBy(src, u) && src.State == "active" {
		return true
	}
	return canEditEntity(u, src)
}

func canAttachToTarget(u User, tgt Entity) bool {
	// 审核/生命周期权（旧 admin）不受目标端否决权约束。
	if u.Can(PermissionLifecycleManage) {
		return true
	}
	if tgt.State == "deleted" || tgt.State == "merged" {
		return false
	}
	// 目标端否决权：自己的条目，或持实体编辑权时对公开条目建边/拆边（旧 editor）。
	return ownedBy(tgt, u) || tgt.State == "active" && u.Can(PermissionEntityEdit)
}

// validateRelationAttributes 校验关系边的属性：字段码必须属于该关系声明的字段集，
// 取值按字段类型/词表/引用校验。SaveRelation（经 validateRelation）与导入预检共用同一
// 实现与同一 historical 口径（true），预检因此既不比 Save 严也不比 Save 松。
func validateRelationAttributes(d Definitions, code string, attrs map[string]any, ref func(string, []string) error, historical bool) error {
	rt, ok := d.Relations[code]
	if !ok {
		return fmt.Errorf("invalid_relation_type")
	}
	return d.attributes(rt.Fields, attrs, ref, historical)
}

func validateRelation(d Definitions, r Relation, src, tgt Entity, existing []Relation, ref func(string, []string) error, historical bool, lookups ...entityLookup) error {
	rt, ok := d.Relations[r.Type]
	if !ok || !historical && !rt.Enabled {
		return fmt.Errorf("invalid_relation_type")
	}
	if r.SourceID == r.TargetID || !contains(rt.SourceKinds, src.Kind) || !contains(rt.TargetKinds, tgt.Kind) {
		return fmt.Errorf("invalid_endpoints")
	}
	if r.Position < 0 {
		return fmt.Errorf("invalid_position")
	}
	if err := validateRelationAttributes(d, r.Type, r.Attributes, ref, historical); err != nil {
		return err
	}
	var lookup entityLookup
	if len(lookups) > 0 {
		lookup = lookups[0]
	}
	if err := validateRelationScopes(rt, r, src, tgt, lookup); err != nil {
		return err
	}
	incoming, outgoing := 0, 0
	// position 只决定展示顺序，不属于关系身份；同端点、类型和属性的边
	// 即使 position 不同也要按 relations_no_exact_dup 的数据库口径判重。
	// 同一演员的不同角色应写进 character 等属性，而非只改 position。
	graph := map[string][]string{}
	for _, x := range existing {
		if x.ID == r.ID {
			continue
		}
		if sharesCycleGroup(d, r.Type, x.Type) {
			graph[x.SourceID] = append(graph[x.SourceID], x.TargetID)
		}
		sameUsage := rt.Usage != "" && rt.Usage == d.Relations[x.Type].Usage
		if x.Type != r.Type && !sameUsage {
			continue
		}
		if rt.UniquePosition && x.SourceID == r.SourceID && x.Position == r.Position {
			return fmt.Errorf("duplicate_relation_position")
		}
		if x.SourceID == r.SourceID {
			outgoing++
		}
		if x.TargetID == r.TargetID {
			incoming++
		}
		if (x.SourceID == r.SourceID && x.TargetID == r.TargetID || rt.Symmetric && x.SourceID == r.TargetID && x.TargetID == r.SourceID) &&
			(rt.Usage == "expression_composition" || x.Type == r.Type && encode(attrsOrEmpty(x.Attributes)) == encode(attrsOrEmpty(r.Attributes))) {
			return fmt.Errorf("duplicate_relation")
		}
	}
	// 基数语义：MaxOutgoing/MaxIncoming 为 0 表示不限（Definitions.Validate
	// 只拒绝负数，不拒绝全零），>0 才计数比较；全零即两侧不限，无死代码。
	if rt.MaxOutgoing > 0 && outgoing >= rt.MaxOutgoing || rt.MaxIncoming > 0 && incoming >= rt.MaxIncoming {
		return fmt.Errorf("cardinality_exceeded")
	}
	// 同码或共同 cycle_group 的存活边构图；不同关系的基数仍分别计算，
	// 专用 usage 的组成顺序和发行组基数则在全部同用途关系间共用。
	if rt.Acyclic {
		stack := []string{r.TargetID}
		seen := map[string]bool{}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if id == r.SourceID {
				return fmt.Errorf("relation_cycle")
			}
			if !seen[id] {
				seen[id] = true
				stack = append(stack, graph[id]...)
			}
		}
	}
	return nil
}

// attrsOrEmpty 把"没有属性"与"空属性对象"归一：两者是同一条边的同一属性集。
// 不归一就会绕过判重——同一个 payload 少写一个 "attributes" 键，就能把同一逻辑边存成两行；
// DB 唯一索引 relations_no_exact_dup 也把缺键与 {} 当两个值，归一后两边口径才一致。
func attrsOrEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *Store) SaveRelation(ctx context.Context, input RelationEdit, u User) (Relation, error) {
	if !u.CanBypassReview() {
		return Relation{}, fmt.Errorf("commit_required")
	}
	var r Relation
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = s.saveRelationTx(ctx, tx, input, u)
		return err
	})
	return r, err
}

func (s *Store) saveRelationTx(ctx context.Context, tx *sql.Tx, input RelationEdit, u User) (Relation, error) {
	r := input.Relation
	r.Attributes = attrsOrEmpty(r.Attributes)
	err := func() error {
		if err := validateSources(input.EditNote, input.Sources); err != nil {
			return err
		}
		// M02：关系写同样定义敏感（端点类型/属性/无环），取共享。
		if err := lockDefinitionsShared(ctx, tx); err != nil {
			return err
		}
		var v DefinitionConfig
		var err error
		if input.definitions != nil {
			v = *input.definitions
		} else {
			v, err = definitions(ctx, tx)
		}
		if err != nil {
			return err
		}
		all, err := relationsForSave(ctx, tx, v.Document, r)
		if err != nil {
			return err
		}
		var old *Relation
		if r.ID == "" {
			if input.ExpectedVersion != 0 {
				return errVersionConflict
			}
			r.ID = input.createID
			if r.ID == "" {
				r.ID = newID()
			}
			r.Version = 1
			// R1 幂等声明与业务写入同一事务（用法同 Save，见 idempotency.go）。
			if input.idempotency != nil {
				prior, claimed, cerr := claimIdempotencyTx(ctx, tx, input.idempotency)
				if cerr != nil {
					return cerr
				}
				if !claimed {
					if r, cerr = replayRelation(prior); cerr != nil {
						return cerr
					}
					return errIdempotentReplay
				}
			}
		} else {
			// 更新行的旧版本按 ID 单行取：同类集合只用于判重/计数/构图，
			// 不把旧版本混进去（否则 immutable_scope 自比较恒过且计数多算自己）。
			prev, perr := relationByID(ctx, tx, r.ID)
			if perr != nil {
				return perr
			}
			old = &prev
			if old.Version != input.ExpectedVersion {
				return errVersionConflict
			}
			if old.SourceID != r.SourceID || old.TargetID != r.TargetID || old.Type != r.Type {
				return fmt.Errorf("immutable_scope")
			}
			r.Version = old.Version + 1
		}
		src, err := get(ctx, tx, r.SourceID)
		if err != nil {
			return err
		}
		tgt, err := get(ctx, tx, r.TargetID)
		if err != nil {
			return err
		}
		if !visible(src, &u) || !visible(tgt, &u) || src.State == "deleted" || src.State == "merged" || tgt.State == "deleted" || tgt.State == "merged" {
			return errForbidden
		}
		if !canWriteRelation(u, src) {
			return errForbidden
		}
		// 目标端沿用角色检查，普通用户仍不能修改他人的公开关系。
		// 删除码后旧边读路径与此无关——Relations 读路径按对端可见性过滤，
		// 不在此做停用/删除码判断。
		if !canAttachToTarget(u, tgt) {
			return errForbidden
		}
		if err = validateRelation(v.Document, r, src, tgt, all, reference(ctx, tx, &u), true, func(id string) (Entity, error) { return get(ctx, tx, id) }); err != nil {
			return err
		}
		if !v.Document.Relations[r.Type].Enabled && old == nil {
			return fmt.Errorf("disabled_relation_type")
		}
		var previous map[string]any
		if old != nil {
			previous = old.Attributes
		}
		if err = v.Document.retiredAttributes(r.Attributes, previous); err != nil {
			return err
		}
		// 与实体写同一原则：版本条件进 WHERE，读完旧版本与写入是同一次原子操作。
		// SSI 重试之后仍必须复核业务版本，不能覆盖其他编辑。
		if old == nil {
			if _, err = tx.ExecContext(ctx, "INSERT INTO catalog.relations(id,version,type,source_id,target_id,document) VALUES($1,$2,$3,$4,$5,$6)", r.ID, r.Version, r.Type, r.SourceID, r.TargetID, encode(r)); err != nil {
				return err
			}
		} else {
			var res sql.Result
			if res, err = tx.ExecContext(ctx, "UPDATE catalog.relations SET version=$3,document=$4 WHERE id=$1 AND version=$2", r.ID, input.ExpectedVersion, r.Version, encode(r)); err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errVersionConflict
			}
		}
		if err := audit(ctx, tx, r.ID, r.Version, u, input.EditNote, input.Sources, r, "relation.saved"); err != nil {
			return err
		}
		// R1 首创响应与业务写入同一事务回填。
		if old == nil && input.idempotency != nil {
			if err := setIdempotencyResponseTx(ctx, tx, input.idempotency, r); err != nil {
				return err
			}
		}
		// 只有新建关系才是"一次收录事件"；改属性（换 position、补 attributes）不重复通知。
		if old == nil {
			return notifyIncludedRelation(ctx, tx, r, src, tgt, u)
		}
		return nil
	}()
	if errors.Is(err, errIdempotentReplay) {
		return r, nil
	}
	return r, err
}

func (s *Store) DeleteRelation(ctx context.Context, id string, expected int64, note string, sources []Source, u User) error {
	if !u.CanBypassReview() {
		return fmt.Errorf("commit_required")
	}
	return s.writeCatalog(ctx, func(tx *sql.Tx) error { return s.deleteRelationTx(ctx, tx, id, expected, note, sources, u) })
}

func (s *Store) deleteRelationTx(ctx context.Context, tx *sql.Tx, id string, expected int64, note string, sources []Source, u User) error {
	if err := validateSources(note, sources); err != nil {
		return err
	}
	var b []byte
	var r Relation
	if err := tx.QueryRowContext(ctx, "SELECT document FROM catalog.relations WHERE id=$1", id).Scan(&b); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if expected != r.Version {
		return errVersionConflict
	}
	src, err := get(ctx, tx, r.SourceID)
	if err != nil {
		return err
	}
	if !canWriteRelation(u, src) {
		return errForbidden
	}
	// 删除同样受目标端否决权约束：他人已发布条目上的边不得单方面拆除。
	tgt, err := get(ctx, tx, r.TargetID)
	if err != nil {
		return err
	}
	if !canAttachToTarget(u, tgt) {
		return errForbidden
	}
	// 删除同样收敛到版本条件：读到版本与真正删除之间可能被并发更新插队，
	// 无条件 DELETE 会把对方刚落库的写入一并抹掉（丢更新）。
	var res sql.Result
	if res, err = tx.ExecContext(ctx, "DELETE FROM catalog.relations WHERE id=$1 AND version=$2", id, expected); err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errVersionConflict
	}
	return audit(ctx, tx, id, r.Version+1, u, note, sources, r, "relation.deleted")
}

// occurrenceRow 是 track_contents 上的一行收录原语（不含 release/medium/track 实体）。
type occurrenceRow struct {
	track, medium, release, expr string
	pos                          int
	loc, attrs, sources          json.RawMessage
}

// occurrenceScopeExpressionIDs 按实体 kind 求值"该实体自身收录"指向的表达集合：
//   - expression: 仅该表达本身（精确匹配，不再按 work/unit 泛化）；
//   - content_unit: 该篇目下全部表达；
//   - work: 该作品下全部表达（母体级聚合）；
//   - 其它 kind: 空。
//
// 批量路径要求实体已由 fillStructural 补齐结构字段——侧表查询与可见性过滤不能
// 按实体逐条再做，否则一次批量请求会退化成 O(表达数) 次查询。
func (s *Store) occurrenceScopeExpressionIDs(ctx context.Context, e Entity) ([]string, error) {
	switch e.Kind {
	case "expression":
		return []string{e.ID}, nil
	case "content_unit", "work":
		col := "content_unit_id"
		if e.Kind == "work" {
			col = "work_id"
		}
		rows, err := s.DB.QueryContext(ctx, "SELECT id::text FROM catalog.expressions WHERE "+col+"=$1 ORDER BY id", e.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	return []string{}, nil
}

// expressionIDsByContentUnit 一次批量解析若干篇目下的全部表达，供批量端点求兄弟集合。
func (s *Store) expressionIDsByContentUnit(ctx context.Context, unitIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	uniq := []string{}
	seen := map[string]bool{}
	for _, id := range unitIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT coalesce(content_unit_id::text,''), id::text FROM catalog.expressions WHERE content_unit_id IN ("+entityPlaceholders(uniq, 1)+") ORDER BY id", entityArgs(uniq)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var unit, id string
		if err = rows.Scan(&unit, &id); err != nil {
			return nil, err
		}
		out[unit] = append(out[unit], id)
	}
	return out, rows.Err()
}

// selfExpressionIDs 求"实体自身收录"指向的表达集合，不触库：要求实体已由
// fillStructural 补齐结构字段（expression 的自身即其 id，work/content_unit 需组合
// 其下属表达——批量路径另用一次 IN 查询求，见 occurrenceScopesForAggregates）。
func selfExpressionIDs(e Entity) []string {
	if e.Kind == "expression" {
		return []string{e.ID}
	}
	return []string{}
}

// occurrenceScopesForAggregates 批量求 work/content_unit 实体的下属表达集合：
// work 按 work_id 一次 IN 查询、content_unit 按 content_unit_id 一次 IN 查询，
// 不逐实体访问数据库。
func (s *Store) occurrenceScopesForAggregates(ctx context.Context, ents map[string]Entity, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	workIDs := []string{}
	unitIDs := []string{}
	for _, id := range ids {
		switch ents[id].Kind {
		case "work":
			workIDs = append(workIDs, id)
		case "content_unit":
			unitIDs = append(unitIDs, id)
		}
	}
	collect := func(col string, values []string) error {
		if len(values) == 0 {
			return nil
		}
		rows, err := s.DB.QueryContext(ctx, "SELECT "+col+"::text, id::text FROM catalog.expressions WHERE "+col+" IN ("+entityPlaceholders(values, 1)+") ORDER BY id", entityArgs(values)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var parent, id string
			if err = rows.Scan(&parent, &id); err != nil {
				return err
			}
			out[parent] = append(out[parent], id)
		}
		return rows.Err()
	}
	if err := collect("work_id", workIDs); err != nil {
		return nil, err
	}
	if err := collect("content_unit_id", unitIDs); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchOccurrenceRows 一次取回给定表达集合的收录行（去重后 IN 查询）。
func (s *Store) fetchOccurrenceRows(ctx context.Context, expressionIDs []string) ([]occurrenceRow, error) {
	uniq := []string{}
	seen := map[string]bool{}
	for _, id := range expressionIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return nil, nil
	}
	q, err := s.DB.QueryContext(ctx, `SELECT c.track_id::text,m.id::text,m.release_id::text,c.expression_id::text,c.position,c.locator,c.attributes,c.sources FROM catalog.track_contents c JOIN catalog.tracks t ON t.id=c.track_id JOIN catalog.mediums m ON m.id=t.medium_id WHERE c.expression_id IN (`+entityPlaceholders(uniq, 1)+`) ORDER BY m.release_id,c.position`, entityArgs(uniq)...)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	rows := []occurrenceRow{}
	for q.Next() {
		var r occurrenceRow
		if err = q.Scan(&r.track, &r.medium, &r.release, &r.expr, &r.pos, &r.loc, &r.attrs, &r.sources); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, q.Err()
}

// occurrenceEntry 用批量补齐的实体表组装一条收录；载体链与表达任一不可见/缺失返回 false。
func occurrenceEntry(r occurrenceRow, got map[string]Entity) (map[string]any, bool) {
	rel, ok1 := got[r.release]
	med, ok2 := got[r.medium]
	track, ok3 := got[r.track]
	_, ok4 := got[r.expr]
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, false
	}
	return map[string]any{"release": rel, "medium": med, "track": track, "expression_id": r.expr, "position": r.pos, "locator": r.loc, "attributes": r.attrs, "sources": r.sources}, true
}

func (s *Store) Occurrences(ctx context.Context, id string, u *User) ([]map[string]any, error) {
	// X01 读取汇别名：先跟随 merged 链到存活身份再聚合收录，旧 ID 的收录
	// 在合并时已改写到存活链（见 mergeReferences），读侧不再散落两处。
	e, err := s.Resolve(ctx, id, u)
	if err != nil {
		return nil, err
	}
	ids, err := s.occurrenceScopeExpressionIDs(ctx, e)
	if err != nil {
		return nil, err
	}
	rows, err := s.fetchOccurrenceRows(ctx, ids)
	if err != nil {
		return nil, err
	}
	need := []string{}
	for _, r := range rows {
		need = append(need, r.release, r.medium, r.track, r.expr)
	}
	// 去 N+1: 收集全部 release/medium/track/expression ID, 一次 IN 批量拉取(含侧表),
	// 不可见/缺失的按原语义跳过该行。
	got, err := s.getMany(ctx, need, u)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rows {
		if entry, ok := occurrenceEntry(r, got); ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

// OccurrenceRef 是收录行的引用形态：只带 release/medium/track 的 id，实体本身
// 放在批量响应的共享 entities 表里。大目录下同一实体被大量收录重复引用，
// 引用形态避免把完整 Release/Medium/Track 在每条收录里重复传输。
type OccurrenceRef struct {
	ReleaseID    string          `json:"release_id"`
	MediumID     string          `json:"medium_id"`
	TrackID      string          `json:"track_id"`
	ExpressionID string          `json:"expression_id"`
	Position     int             `json:"position"`
	Locator      json.RawMessage `json:"locator,omitempty"`
	Attributes   json.RawMessage `json:"attributes,omitempty"`
}

func occurrenceRef(r occurrenceRow) OccurrenceRef {
	return OccurrenceRef{
		ReleaseID:    r.release,
		MediumID:     r.medium,
		TrackID:      r.track,
		ExpressionID: r.expr,
		Position:     r.pos,
		Locator:      r.loc,
		Attributes:   r.attrs,
	}
}

// ExpressionDetail 是发行详情页渲染一条表达所需的只读聚合：
// 表达实体、该表达自身的收录（occurrences）、同篇目其它表达的收录（siblings）、
// 首个署名（演出/配音/创作）目标标题。收录以引用形态返回，实体在共享表中。
type ExpressionDetail struct {
	Entity      Entity          `json:"entity"`
	Occurrences []OccurrenceRef `json:"occurrences"`
	Siblings    []OccurrenceRef `json:"siblings"`
	CreditTitle string          `json:"credit_title,omitempty"`
}

// ExpressionDetailsResult 是批量上屏的完整载荷：每条表达只带引用 id，
// 实体（表达自身 + 收录引用的 release/medium/track）汇总在 Entities 表里。
type ExpressionDetailsResult struct {
	Items    map[string]ExpressionDetail `json:"items"`
	Entities map[string]Entity           `json:"entities"`
}

// ExpressionDetailsBatch 一次取回多条表达的上屏数据，替代发行页对每条表达
// 分别请求 entities/:id + occurrences + relations + 对端实体的四类 N+1 请求。
// 语义与单条端点一致：occurrences 按 kind 解释为该实体自身的收录，
// siblings 为同篇目兄弟表达的收录；仅返回可见数据。
func (s *Store) ExpressionDetailsBatch(ctx context.Context, ids []string, u *User) (ExpressionDetailsResult, error) {
	out := ExpressionDetailsResult{Items: map[string]ExpressionDetail{}, Entities: map[string]Entity{}}
	uniq := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	// 请求实体经 fillStructural 补齐结构侧表：document 落库时清空了 work_id/content_unit_id，
	// 只解 JSON 拿不到，前端据 content_unit_id 做章节级对齐会全部落到 work 级。
	ents, err := s.GetManyVisible(ctx, uniq, u)
	if err != nil {
		return out, err
	}
	ents, err = fillStructural(ctx, s.DB, ents)
	if err != nil {
		return out, err
	}
	if len(ents) == 0 {
		return out, nil
	}
	// 每个请求实体的自身收录集合与同篇目兄弟集合，并集一次取回全部候选收录行。
	selfScopes := map[string][]string{}
	siblingScopes := map[string][]string{}
	// 兄弟篇目一次批量解析（同一 Work 内多个表达只会查一次），不再逐条访问数据库。
	unitIDs := []string{}
	for _, e := range ents {
		if e.Kind == "expression" && strings.TrimSpace(e.ContentUnitID) != "" {
			unitIDs = append(unitIDs, e.ContentUnitID)
		}
	}
	siblingsByUnit, err := s.expressionIDsByContentUnit(ctx, unitIDs)
	if err != nil {
		return out, err
	}
	allIDs := map[string]bool{}
	// 请求实体已是 expression 时自身收录即自身；work/content_unit 的聚合另走批量查询。
	aggregateScopeIDs := []string{}
	for _, e := range ents {
		if e.Kind == "work" || e.Kind == "content_unit" {
			aggregateScopeIDs = append(aggregateScopeIDs, e.ID)
		}
	}
	aggScopes, err := s.occurrenceScopesForAggregates(ctx, ents, aggregateScopeIDs)
	if err != nil {
		return out, err
	}
	for id, e := range ents {
		selfScopes[id] = selfExpressionIDs(e)
		if agg := aggScopes[id]; e.Kind == "work" || e.Kind == "content_unit" {
			selfScopes[id] = agg
		}
		for _, x := range selfScopes[id] {
			allIDs[x] = true
		}
		sib := []string{}
		if e.Kind == "expression" {
			for _, x := range siblingsByUnit[e.ContentUnitID] {
				if x != e.ID {
					sib = append(sib, x)
				}
			}
		}
		siblingScopes[id] = sib
		for _, x := range sib {
			allIDs[x] = true
		}
	}
	candidateIDs := keysOf(allIDs)
	rows, err := s.fetchOccurrenceRows(ctx, candidateIDs)
	if err != nil {
		return out, err
	}
	// 批量解析收录行引用的 release/medium/track/expression，不可见者跳过。
	need := []string{}
	for _, r := range rows {
		need = append(need, r.release, r.medium, r.track, r.expr)
	}
	got, err := s.getMany(ctx, need, u)
	if err != nil {
		return out, err
	}
	// 收录行只需引用 id；实体统一进共享表 Entities（同一 release/medium/track
	// 可能被多条收录引用，避免重复传输）。got 已按可见性过滤，直接采用。
	type matRow struct {
		expr string
		ref  OccurrenceRef
	}
	mat := make([]matRow, 0, len(rows))
	for _, r := range rows {
		if _, ok1 := got[r.release]; !ok1 {
			continue
		}
		if _, ok2 := got[r.medium]; !ok2 {
			continue
		}
		if _, ok3 := got[r.track]; !ok3 {
			continue
		}
		if _, ok4 := got[r.expr]; !ok4 {
			continue
		}
		mat = append(mat, matRow{expr: r.expr, ref: occurrenceRef(r)})
	}
	// 署名：一次取候选表达的相关关系，按请求表达过滤后取对端标题。
	// 署名码按每条关系自己的 CountsAsCredit 声明动态取（见 creditRelationTypes），
	// 不看分组码、不硬编码名单：后台挪分组只改展示，不改变署名口径。
	creditPeer := map[string]string{}
	if len(candidateIDs) > 0 {
		dv, derr := s.Definitions(ctx)
		if derr != nil {
			return out, derr
		}
		creditCodes := creditRelationTypes(dv.Document)
		if len(creditCodes) > 0 {
			// pq.Array 保证 text[] 绑定；IN 占位符拼接在候选量大时 SQL 过长，
			// 且与 Save 路径的 pq.Array 风格不一致。
			relRows, err := s.DB.QueryContext(ctx, `SELECT source_id::text,target_id::text FROM catalog.relations WHERE type = ANY($1) AND (source_id = ANY($2) OR target_id = ANY($2)) ORDER BY id`, pq.Array(creditCodes), pq.Array(candidateIDs))
			if err != nil {
				return out, err
			}
			type edge struct{ src, tgt string }
			var edges []edge
			for relRows.Next() {
				var e edge
				if err = relRows.Scan(&e.src, &e.tgt); err != nil {
					relRows.Close()
					return out, err
				}
				edges = append(edges, e)
			}
			if err = relRows.Err(); err != nil {
				relRows.Close()
				return out, err
			}
			relRows.Close()
			// 对端解析：表达可能是 source 或 target，取另一侧且必须在候选集合内。
			candidate := map[string]bool{}
			for _, x := range candidateIDs {
				candidate[x] = true
			}
			peerIDs := []string{}
			for _, e := range edges {
				if candidate[e.src] {
					peerIDs = append(peerIDs, e.tgt)
				}
				if candidate[e.tgt] {
					peerIDs = append(peerIDs, e.src)
				}
			}
			peers, err := s.GetManyVisible(ctx, peerIDs, u)
			if err != nil {
				return out, err
			}
			for _, e := range edges {
				if candidate[e.src] {
					if _, seen := creditPeer[e.src]; !seen {
						if p, ok := peers[e.tgt]; ok {
							creditPeer[e.src] = p.Title
						}
					}
				}
				if candidate[e.tgt] {
					if _, seen := creditPeer[e.tgt]; !seen {
						if p, ok := peers[e.src]; ok {
							creditPeer[e.tgt] = p.Title
						}
					}
				}
			}
		}
	}
	// 共享实体表：请求的表达自身 + 收录行引用到的 release/medium/track。
	for id, e := range ents {
		out.Entities[id] = e
	}
	for _, e := range got {
		if e.ID != "" {
			out.Entities[e.ID] = e
		}
	}
	for id, e := range ents {
		detail := ExpressionDetail{Entity: e, Occurrences: []OccurrenceRef{}, Siblings: []OccurrenceRef{}}
		self := map[string]bool{}
		for _, x := range selfScopes[id] {
			self[x] = true
		}
		sib := map[string]bool{}
		for _, x := range siblingScopes[id] {
			sib[x] = true
		}
		for _, mr := range mat {
			if self[mr.expr] {
				detail.Occurrences = append(detail.Occurrences, mr.ref)
			}
			if sib[mr.expr] {
				detail.Siblings = append(detail.Siblings, mr.ref)
			}
		}
		detail.CreditTitle = creditPeer[id]
		out.Items[id] = detail
	}
	return out, nil
}

// creditRelationTypes 返回"参与批量署名聚合"的关系码集合
// （ExpressionDetailsBatch 的 CreditTitle 用它拼 IN 查询）。
//
// 判定严格来自每条关系自己的 CountsAsCredit 声明，不再看分组码：分组是展示归类，
// 后台把某条关系挪出 credits 组属于改展示，不该静默改变"哪些关系算署名"的行为口径。
// 停用码一律不计入。返回排序后的码，保证 SQL 占位符顺序稳定。
func creditRelationTypes(d Definitions) []string {
	out := []string{}
	for code, rt := range d.Relations {
		if !rt.Enabled {
			continue
		}
		if rt.CountsAsCredit {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// keysOf 返回 set 的键（顺序无关，仅用于构造 IN 查询）。
func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// Compare returns the entity (only comparable metadata fields) plus its
// structural children for kinds that have a containment tree:
// release → [{medium, tracks}], medium → [{medium: self, tracks}],
// other kinds → []. Any mix of kinds is allowed; attribute union and content
// alignment are the caller's job (frontend compares the entity rows,
// revision history diff reuses the same table).
func (s *Store) Compare(ctx context.Context, ids []string, u *User) ([]map[string]any, error) {
	if len(ids) < 2 || len(ids) > 6 {
		return nil, fmt.Errorf("compare_requires_two_to_six")
	}
	d, err := s.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, id := range ids {
		e, err := s.Get(ctx, id, u)
		if err != nil {
			return nil, err
		}
		attrs := map[string]any{}
		for k, v := range e.Attributes {
			if d.Document.Fields[k].Comparable {
				attrs[k] = v
			}
		}
		e.Attributes = attrs
		rows := []map[string]any{}
		if e.Kind == "release" || e.Kind == "medium" {
			media := []Entity{}
			if e.Kind == "release" {
				if media, err = s.ListAll(ctx, ListOptions{ReleaseID: id}, u); err != nil {
					return nil, err
				}
			} else {
				media = []Entity{e}
			}
			for _, m := range media {
				tracks, err := s.ListAll(ctx, ListOptions{MediumID: m.ID}, u)
				if err != nil {
					return nil, err
				}
				rows = append(rows, map[string]any{"medium": m, "tracks": tracks})
			}
		}
		out = append(out, map[string]any{"entity": e, "children": rows})
	}
	return out, nil
}
