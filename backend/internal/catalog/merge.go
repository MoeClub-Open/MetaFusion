package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Only fields declared as references are rewritten; arbitrary text is evidence.
func replaceReference(f Field, value any, source, target string) any {
	switch f.Type {
	case "entity":
		if value == source {
			return target
		}
	case "list":
		if items, ok := value.([]any); ok && f.Items != nil {
			for i, v := range items {
				items[i] = replaceReference(*f.Items, v, source, target)
			}
		}
	case "group":
		if fields, ok := value.(map[string]any); ok {
			for k, v := range fields {
				fields[k] = replaceReference(f.Fields[k], v, source, target)
			}
		}
	}
	return value
}
func rewriteAttributes(d Definitions, attrs map[string]any, source, target string) {
	for k, v := range attrs {
		attrs[k] = replaceReference(d.Fields[k], v, source, target)
	}
}

// rewriteGroupRefs 改写一个"记录级结构属性"（值形如 {子字段码: 值}）里的实体引用。
// 子字段不在顶层 fields 里，而是声明在 group 字段自己的 Fields 下，因此必须先取
// group 定义再逐子字段递归——用顶层 rewriteAttributes 会全部漏掉。
func rewriteGroupRefs(group Field, attrs map[string]any, source, target string) {
	for k, v := range attrs {
		attrs[k] = replaceReference(group.Fields[k], v, source, target)
	}
}

// rewriteEntityRefs 覆盖实体的**全部**动态属性落点：普通属性、发行对象附加属性、
// 收录附加属性与定位组。这些落点由同一套 definitions 声明，合并时若只改普通属性，
// 结构属性里引用的实体就会留下指向已合并身份的悬空引用。
func rewriteEntityRefs(d Definitions, e *Entity, source, target string) {
	rewriteAttributes(d, e.Attributes, source, target)
	for i := range e.Subjects {
		rewriteGroupRefs(d.Fields["subject_attributes"], e.Subjects[i].Attributes, source, target)
	}
	for i := range e.Contents {
		rewriteGroupRefs(d.Fields["locator"], map[string]any(e.Contents[i].Locator), source, target)
		rewriteGroupRefs(d.Fields["inclusion_attributes"], e.Contents[i].Attributes, source, target)
	}
}

// mergeExternalIDs 把源 external_ids 缺的键补给目标（幂等——重复合并结果一致）；
// 同键不同值即冲突报错，不静默覆盖。metafusion_import 这类内部幂等键不复制：
// 源行（merged）保留该键作为别名，目标若复制则撞唯一索引（见结构基线
// entities_metafusion_import_key，它覆盖 merged/deleted 行）；重导经
// findImported→Resolve 回到存活实体（X01 别名语义的目录侧部分）。
// 调用方先手工去重的口径不变（与 000013 唯一索引"存量重复即失败"同策略）。
func mergeExternalIDs(target, source map[string]string) (map[string]string, error) {
	if len(source) == 0 {
		return target, nil
	}
	if target == nil {
		target = map[string]string{}
	}
	for k, v := range source {
		if legacyImportKeys[k] {
			continue
		}
		if cur, ok := target[k]; ok && cur != v {
			return nil, fmt.Errorf("merge_external_conflict: %s", k)
		}
		if _, ok := target[k]; !ok {
			target[k] = v
		}
	}
	return target, nil
}

// relationReferencesID 判定关系属性里是否有 definitions 声明的 entity 引用指向 id：
// 按该关系类型的 Fields 逐字段递归（entity 直接比对，list/group 下钻），
// 自由文本里的巧合子串不算——只认声明过的引用路径（与 replaceReference 同源）。
// 未知关系类型返回 false：无声明可依，端点引用仍由 relationsWithEndpoint 覆盖。
func relationReferencesID(d Definitions, r Relation, id string) bool {
	rt, ok := d.Relations[r.Type]
	if !ok {
		return false
	}
	for _, code := range rt.Fields {
		if referencesID(d.Fields[code], r.Attributes[code], id) {
			return true
		}
	}
	return false
}

func referencesID(f Field, value any, id string) bool {
	switch f.Type {
	case "entity":
		s, _ := value.(string)
		return s == id
	case "list":
		if items, ok := value.([]any); ok && f.Items != nil {
			for _, v := range items {
				if referencesID(*f.Items, v, id) {
					return true
				}
			}
		}
	case "group":
		if m, ok := value.(map[string]any); ok {
			for k, v := range m {
				cf, ok := f.Fields[k]
				if !ok {
					continue
				}
				if referencesID(cf, v, id) {
					return true
				}
			}
		}
	}
	return false
}

// relationsWithAttributeReference 取"端点不含 source、但属性引用 source"的边（M03）：
// 待合并角色只存在于配音关系的 character/context 属性而不在端点时，端点候选查不到它。
// SQL 只做文本预过滤（合并期一次性扫描），是否真引用由 relationReferencesID 按
// definitions 声明的 entity 路径判定——不做 JSON 字符串替换。
func relationsWithAttributeReference(ctx context.Context, tx *sql.Tx, d Definitions, sourceID string, skip map[string]bool) ([]Relation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT document FROM catalog.relations WHERE document::text LIKE '%'||$1||'%' ORDER BY id", sourceID)
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
		if skip[r.ID] {
			continue
		}
		if relationReferencesID(d, r, sourceID) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// mergeReferences moves identity references atomically and audits every affected record.
// Conflicting relationship cardinality and containment are rejected, never discarded.
func mergeReferences(ctx context.Context, tx *sql.Tx, source, target Entity, u User, in LifecycleEdit) error {
	// M02：合并改写同样定义敏感（类型/字段/关系重验），先取共享再读定义
	// Lifecycle 已建立 SERIALIZABLE 事务，局部锁顺序见 store.go。
	if err := lockDefinitionsShared(ctx, tx); err != nil {
		return err
	}
	v, err := definitions(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM catalog.entities WHERE status NOT IN ('deleted','merged') ORDER BY id")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	changed := []Entity{}
	for _, id := range ids {
		if id == source.ID {
			continue
		}
		e, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		before := encode(e)
		for _, p := range []*string{&e.WorkID, &e.ContentUnitID, &e.ReleaseID, &e.MediumID, &e.ParentID} {
			if *p == source.ID {
				*p = target.ID
			}
		}
		rewriteEntityRefs(v.Document, &e, source.ID, target.ID)
		for i := range e.Contents {
			if e.Contents[i].ExpressionID == source.ID {
				e.Contents[i].ExpressionID = target.ID
			}
		}
		for i := range e.Subjects {
			if e.Subjects[i].WorkID == source.ID {
				e.Subjects[i].WorkID = target.ID
			}
		}
		if id == target.ID && source.Kind == "release" {
			// Subjects 按（work, role）合并键保留：目标已有同键时若 attributes
			// 不同即冲突报错（merge_subject_conflict），不再静默丢弃一方；
			// 同键同值视为重复，直接跳过。position 保留目标值。
			for _, s := range source.Subjects {
				hit := -1
				for i, t := range e.Subjects {
					if t.WorkID == s.WorkID && t.Role == s.Role {
						hit = i
						break
					}
				}
				if hit < 0 {
					e.Subjects = append(e.Subjects, s)
					continue
				}
				if encode(e.Subjects[hit].Attributes) != encode(s.Attributes) {
					return fmt.Errorf("merge_subject_conflict")
				}
			}
		}
		if id == target.ID && source.Kind == "track" {
			// 同表达不同位置不得双留：合并键为 expression_id（与 validateEntity 的
			// duplicate_content 同口径），源表达在目标已存在即冲突检查——
			// locator/attributes 不同报 merge_content_conflict，同值视为重复跳过；
			// 同 position 不同表达同样冲突。position 保留目标值。
			for _, c := range source.Contents {
				hit := -1
				for i, other := range e.Contents {
					if c.ExpressionID == other.ExpressionID || c.Position == other.Position {
						hit = i
						break
					}
				}
				if hit < 0 {
					e.Contents = append(e.Contents, c)
					continue
				}
				hitRow, srcRow := e.Contents[hit], c
				if hitRow.ExpressionID != srcRow.ExpressionID || hitRow.Position != srcRow.Position || encode(hitRow.Locator) != encode(srcRow.Locator) || encode(hitRow.Attributes) != encode(srcRow.Attributes) {
					return fmt.Errorf("merge_content_conflict")
				}
			}
		}
		// ExternalIDs 合并：目标缺的键从源补齐（幂等——重复合并结果一致）；
		// 外部键合并口径见 mergeExternalIDs：缺键补齐、同值幂等、异值冲突，
		// 内部幂等键不复制（源行保留做别名）。
		if id == target.ID && len(source.ExternalIDs) > 0 {
			var err error
			if e.ExternalIDs, err = mergeExternalIDs(e.ExternalIDs, source.ExternalIDs); err != nil {
				return err
			}
		}
		// Subjects 去重键与 validateEntity 同口径（work, role），position 不计入；
		// 凡属性不同直接报 merge_subject_conflict（涵盖 Work 合并导致下游 Release
		// 内两条记录 WorkID 改写收敛碰撞，以及 Release 实体合并碰撞），
		// 属性完全相同视为重复幂等跳过，绝不静默丢弃任何附加属性。
		unique := []Subject{}
		seen := map[string]Subject{}
		for _, subject := range e.Subjects {
			key := subject.WorkID + ":" + subject.Role
			if prev, ok := seen[key]; ok {
				if encode(prev.Attributes) != encode(subject.Attributes) {
					return fmt.Errorf("merge_subject_conflict")
				}
				continue
			}
			seen[key] = subject
			unique = append(unique, subject)
		}
		if len(e.Subjects) > 0 {
			e.Subjects = unique
		}
		if before == encode(e) {
			continue
		}
		e.Version++
		e.UpdatedAt = time.Now().UTC()
		changed = append(changed, e)
	}
	// M01：合并改写多个发行的映射（subjects/contents），先按发行粒度取锁
	// （ID 排序后依次取，与 Save 的单锁顺序一致），再落库。medium 归属不可变
	// 但复核同发行，同样纳入；expression/agent 等不碰发行映射，不取。
	releaseIDs := map[string]bool{}
	for _, e := range changed {
		switch e.Kind {
		case "release":
			releaseIDs[e.ID] = true
		case "medium":
			if e.ReleaseID != "" {
				releaseIDs[e.ReleaseID] = true
			}
		case "track":
			if e.MediumID == "" {
				continue
			}
			var rid string
			if err := tx.QueryRowContext(ctx, "SELECT release_id FROM catalog.mediums WHERE id=$1", e.MediumID).Scan(&rid); err != nil {
				return err
			}
			if rid != "" {
				releaseIDs[rid] = true
			}
		}
	}
	ordered := make([]string, 0, len(releaseIDs))
	for rid := range releaseIDs {
		ordered = append(ordered, rid)
	}
	sort.Strings(ordered)
	for _, rid := range ordered {
		if err := lockRelease(ctx, tx, rid); err != nil {
			return err
		}
	}
	// Deferred composite foreign keys permit moving a complete logical subtree.
	for _, e := range changed {
		switch e.Kind {
		case "content_unit":
			_, err = tx.ExecContext(ctx, "UPDATE catalog.content_units SET work_id=$2,parent_id=$3 WHERE id=$1", e.ID, e.WorkID, nullable(e.ParentID))
		case "expression":
			_, err = tx.ExecContext(ctx, "UPDATE catalog.expressions SET work_id=$2,content_unit_id=$3 WHERE id=$1", e.ID, e.WorkID, nullable(e.ContentUnitID))
		case "medium":
			_, err = tx.ExecContext(ctx, "UPDATE catalog.mediums SET release_id=$2,parent_id=$3 WHERE id=$1", e.ID, e.ReleaseID, nullable(e.ParentID))
		case "track":
			_, err = tx.ExecContext(ctx, "UPDATE catalog.tracks SET medium_id=$2,parent_id=$3 WHERE id=$1", e.ID, e.MediumID, nullable(e.ParentID))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "DELETE FROM catalog.track_contents WHERE track_id=$1", e.ID)
			if err != nil {
				return err
			}
			for _, c := range e.Contents {
				_, err = tx.ExecContext(ctx, "INSERT INTO catalog.track_contents(track_id,expression_id,position,locator,attributes,sources) VALUES($1,$2,$3,$4,$5,$6)", e.ID, c.ExpressionID, c.Position, encode(c.Locator), encode(c.Attributes), encode(inclusionSources(c.Sources)))
				if err != nil {
					return err
				}
			}
		case "release":
			_, err = tx.ExecContext(ctx, "DELETE FROM catalog.release_subjects WHERE release_id=$1", e.ID)
			if err != nil {
				return err
			}
			for _, s := range e.Subjects {
				_, err = tx.ExecContext(ctx, "INSERT INTO catalog.release_subjects(release_id,work_id,role,position,attributes) VALUES($1,$2,$3,$4,$5)", e.ID, s.WorkID, s.Role, s.Position, encode(s.Attributes))
				if err != nil {
					return err
				}
			}
		}
		if err != nil {
			return err
		}
		stored := e
		stored.WorkID = ""
		stored.ContentUnitID = ""
		stored.ReleaseID = ""
		stored.MediumID = ""
		stored.ParentID = ""
		stored.Contents = nil
		stored.Subjects = nil
		// 与 Save 同一原则：引用改写也是"读版本 → 写"，版本条件进 WHERE。
		// e.Version 是本事务读到的版本 +1（见上面的 e.Version++），所以条件用 e.Version-1。
		var res sql.Result
		if res, err = tx.ExecContext(ctx, "UPDATE catalog.entities SET version=$3,document=$4,updated_at=$5 WHERE id=$1 AND version=$2", e.ID, e.Version-1, e.Version, encode(stored), e.UpdatedAt); err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errVersionConflict
		}
		if err = audit(ctx, tx, e.ID, e.Version, u, in.EditNote, in.Sources, e, "entity.saved"); err != nil {
			return err
		}
	}
	// 收藏（以及其它服务里指向旧身份的引用）**不在这里改写**：community.favorites 归互动服务，
	// 目录写别人的表会破坏子系统边界。合并事实写进 catalog.outbox（type=entity.merged），
	// 但**当前没有跨服务消费者**（投递函数 Store.Deliver 只在测试里被调用）：互动服务的收藏
	// 与互动记录收敛走的是主动查询——GET /api/catalog/entities/{id}/resolve 跟随重定向
	//（见其 internal/catalog 客户端）。将来引入投递时的契约见 lifecycle.go 的 Deliver。
	// 合并只改写以旧身份为端点的边：按端点取候选而非全表加载
	//（relations_endpoints 索引命中，避免关系量大时退化）。
	all, err := relationsWithEndpoint(ctx, tx, source.ID)
	if err != nil {
		return err
	}
	// M03：属性引用候选与端点候选合并后走同一改写/审计/校验循环。
	handled := map[string]bool{}
	for _, r := range all {
		handled[r.ID] = true
	}
	extra, err := relationsWithAttributeReference(ctx, tx, v.Document, source.ID, handled)
	if err != nil {
		return err
	}
	all = append(all, extra...)
	for i := range all {
		r := &all[i]
		before := encode(r)
		if r.SourceID == source.ID {
			r.SourceID = target.ID
		}
		if r.TargetID == source.ID {
			r.TargetID = target.ID
		}
		rewriteAttributes(v.Document, r.Attributes, source.ID, target.ID)
		if encode(r) == before {
			continue
		}
		r.Version++
		if r.SourceID == r.TargetID {
			return fmt.Errorf("merge_relation_conflict")
		}
		// 同上：r.Version 是读到的版本 +1（见上面的 r.Version++）。
		var res sql.Result
		if res, err = tx.ExecContext(ctx, "UPDATE catalog.relations SET source_id=$3,target_id=$4,version=$5,document=$6 WHERE id=$1 AND version=$2", r.ID, r.Version-1, r.SourceID, r.TargetID, r.Version, encode(r)); err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errVersionConflict
		}
		if err = audit(ctx, tx, r.ID, r.Version, u, in.EditNote, in.Sources, r, "relation.saved"); err != nil {
			return err
		}
	}
	// 校验只复核被改写的边所在类型：validateRelation 的构图/计数/判重
	// 只看同 type（异类跳过），同类全集按类型取，不再全表加载。
	seenType := map[string]bool{}
	for _, r := range all {
		if seenType[r.Type] {
			continue
		}
		seenType[r.Type] = true
		same, err := relationsForRule(ctx, tx, v.Document, r.Type)
		if err != nil {
			return err
		}
		for _, x := range same {
			src, err := get(ctx, tx, x.SourceID)
			if err != nil {
				return err
			}
			tgt, err := get(ctx, tx, x.TargetID)
			if err != nil {
				return err
			}
			if err = validateRelation(v.Document, x, src, tgt, same, reference(ctx, tx, &u), true, func(id string) (Entity, error) { return get(ctx, tx, id) }); err != nil {
				return fmt.Errorf("merge_relation_conflict: %w", err)
			}
		}
	}
	return nil
}
