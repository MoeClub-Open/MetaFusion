package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
)

// 定义发布前的全量回放（impact）把问题分成两类，处置口径**刻意不同**：
//
//   - 定义问题（DefinitionImpact.Issues）：定义自身非法，或定义与存量数据在**取值语义**上冲突
//     （词表项/枚举不认、字段类型不符、关系端点类型不符、无环违例、结构必填缺失、未知字段…）。
//     这类必须阻断发布：发出去就等于宣称一批存量数据非法，且没有任何数据侧修复能替它兜底。
//   - 悬挂引用（DefinitionImpact.Dangling）：定义没问题，是**存量数据**指向了不存在的行
//     （attributes 里的 entity 型取值、关系端点）。这是既有数据欠账，不该由"新增定义项"来付账。
//
// 为什么悬挂引用只警告、不阻断（2026-09 线上事故的整改口径）：
//
//  1. 责任错位：库里 104 条实体带 attributes.publisher，其中 31 条的目标行早已不存在。让
//     "本次部署新增的 45 项种子声明"因为别人的历史欠账而发不出去，等于把数据修复的责任
//     扣在定义发布头上——两者是不同的人、不同的修复动作。
//  2. 阻断挡不住问题：悬挂引用不会因为定义不发布而消失，只会让新定义（新关系码/新字段）永远
//     到不了存量实例，连带把"定义层的新能力"一起冻住。
//  3. 危害不对称：错放（本该阻断却只警告）的后果是读路径上仍有一个指向不存在行的引用——
//     读路径本来就要容忍它（reference 只影响写入与校验，列表/详情按 id 取不到就是取不到）；
//     错拦的后果是整站起不来（本次事故：log.Fatalf → CrashLoop → 网关 502）。
//     因此判据是"定义是否非法"，不是"数据是否干净"；数据是否干净由体检命令单独回答。
//  4. 不静默：警告不是吞掉——报告进日志、进 /admin/catalog-definitions/{id}/impact 响应、
//     进启动状态信号（/health 的 definitions.dangling_references），并有 mf-migrate check-refs
//     可以一次性列出全部。
type DefinitionImpact struct {
	// Issues 是阻断项：定义非法或与存量数据语义冲突，发布必须停。
	Issues []string `json:"issues"`
	// Dangling 是警告项：存量数据指向不存在的行，发布照常，但要在部署前后看得见。
	Dangling []DanglingReference `json:"dangling_references"`
}

// 回放专用哨兵：引用取值不是 uuid、或目标行不存在——即"指向不存在的行"。
// impact 回放里命中悬挂报告索引的取值由 tolerant 引用函数直接放行（局部降级），未命中的仍按 invalid_reference 进阻断项。
type danglingReferenceError struct{ Value string }

// Error 与 reference（store.go）的失败文本保持逐字一致：这个哨兵改变的只是 impact 的分流，
// 不改变任何对外可见的错误码（写路径仍用 reference()，读路径与响应文案不受影响）。
func (e *danglingReferenceError) Error() string { return "invalid_reference" }

// danglingKey 是悬挂引用的索引键：同一作用域（实体或关系）+ 同一个悬挂取值。
// 为什么要按取值而不是按条目判定：悬挂只放行命中的取值，其它字段继续校验，
// 而悬挂报告是全量的——只有"报告里确实有这条取值"才允许放行该取值。
func danglingKey(scopeID, value string) string { return scopeID + "\x00" + value }

// impactReference 与 reference（store.go）判定同一件事，区别只在把"指向不存在的行"
// （非 uuid 取值 / 目标行缺失）包成 danglingReferenceError；其余失败（目标类型不在 kinds 里、
// 不可见、已退役）保持 invalid_reference 字符串，属定义与数据的语义冲突，照旧阻断。
func impactReference(ctx context.Context, q queryer, u *User) func(string, []string) error {
	return func(id string, kinds []string) error {
		if _, err := uuid.Parse(id); err != nil {
			return &danglingReferenceError{Value: id}
		}
		e, err := get(ctx, q, id)
		if err != nil {
			// 只有"行不存在"是悬挂；其它读库失败沿用原判定（invalid_reference），不当作数据欠账。
			if errors.Is(err, sql.ErrNoRows) {
				return &danglingReferenceError{Value: id}
			}
			return fmt.Errorf("invalid_reference")
		}
		if !contains(kinds, e.Kind) || !visible(e, u) || e.State == "deleted" || e.State == "merged" {
			return fmt.Errorf("invalid_reference")
		}
		return nil
	}
}

// impact 以系统上下文回放存量数据，返回阻断项与悬挂引用两份清单。
func impact(ctx context.Context, q queryer, d Definitions) (DefinitionImpact, error) {
	ents, err := liveEntities(ctx, q)
	if err != nil {
		return DefinitionImpact{}, err
	}
	rels, err := relations(ctx, q)
	if err != nil {
		return DefinitionImpact{}, err
	}
	return d.impactOn(ctx, q, ents, rels)
}

// impactOn 在已加载的快照上回放：启动路径为了悬挂扫描已经读过同一批实体与关系，
// 不必读第二遍（全量实体在大目录上是启动期最贵的一次读）。
func (d Definitions) impactOn(ctx context.Context, q queryer, ents []Entity, rels []Relation) (DefinitionImpact, error) {
	out := DefinitionImpact{Issues: []string{}, Dangling: []DanglingReference{}}
	if err := d.Validate(); err != nil {
		out.Issues = append(out.Issues, err.Error())
		return out, nil
	}
	// 悬挂扫描先跑：它同时产出报告与"哪条校验失败属于数据欠账"的判据索引。
	items, index, err := d.scanDangling(ctx, q, ents, rels)
	if err != nil {
		return out, err
	}
	out.Dangling = items
	// impact 以系统上下文回放存量数据：显式持通配权限，不依赖角色兜底。
	system := &User{Permissions: []string{permissionWildcard}}
	ref := impactReference(ctx, q, system)
	// 已登记悬挂引用只做局部降级：该作用域下命中悬挂报告索引的取值直接放行，
	// 同一实体/关系的其它字段继续校验——首个悬挂不再吞掉整条记录的其它问题。
	// 未登记的取值仍走完整判定（fail closed），见 impactReference。
	tolerant := func(scopeID string) func(string, []string) error {
		return func(id string, kinds []string) error {
			if index[danglingKey(scopeID, strings.TrimSpace(id))] {
				return nil
			}
			return ref(id, kinds)
		}
	}
	byID := map[string]Entity{}
	for _, e := range ents {
		byID[e.ID] = e
	}
	liveRelations := []Relation{}
	for _, r := range rels {
		if _, ok := byID[r.SourceID]; !ok {
			continue
		}
		if _, ok := byID[r.TargetID]; !ok {
			continue
		}
		liveRelations = append(liveRelations, r)
	}
	relationCandidates := relationReplayCandidates(d, liveRelations)
	for _, e := range ents {
		mediumFormat := ""
		if e.Kind == "track" {
			if medium, ok := byID[e.MediumID]; ok && medium.Kind == "medium" {
				mediumFormat, _ = medium.Attributes["format"].(string)
			}
		}
		if err := d.validateEntity(e, tolerant(e.ID), true, mediumFormat); err != nil {
			out.Issues = append(out.Issues, e.ID+": "+err.Error())
		}
	}
	for _, r := range rels {
		src, sok := byID[r.SourceID]
		tgt, tok := byID[r.TargetID]
		if !sok || !tok {
			// 端点不在存活集合里：要么是悬挂（已在报告里），要么指向已退役实体（历史边）。
			// 两者都不是"定义非法"，因此都不进阻断项——退役实体的历史边归生命周期路径管。
			continue
		}
		// 删除与停用宽容度对齐：impact 用 historical=true 回放存量，关系码删除
		// （!ok）与停用（Enabled=false）都不报 invalid_relation_type——与 Relations
		// 读路径"删除码不断读"同口径。删除码后新建由 SaveRelation 的
		// disabled_relation_type 拦截；停用码的新增使用由 retiredAttributes 拦截。
		if _, ok := d.Relations[r.Type]; !ok {
			continue
		}
		if err = validateRelation(d, r, src, tgt, relationCandidates(r), tolerant(r.ID), true, func(id string) (Entity, error) {
			if e, ok := byID[id]; ok {
				return e, nil
			}
			return get(ctx, q, id)
		}); err != nil {
			out.Issues = append(out.Issues, r.ID+": "+err.Error())
		}
	}
	return out, nil
}

// A replay checks the same dependencies as an individual save. Index endpoint
// constraints once, and retain the complete rule graph for cycle checks.
func relationReplayCandidates(d Definitions, relations []Relation) func(Relation) []Relation {
	bySource, byTarget := map[string][]Relation{}, map[string][]Relation{}
	byRule := map[string][]Relation{}
	for _, r := range relations {
		bySource[r.SourceID] = append(bySource[r.SourceID], r)
		byTarget[r.TargetID] = append(byTarget[r.TargetID], r)
	}
	return func(r Relation) []Relation {
		rule := d.Relations[r.Type]
		if rule.Acyclic {
			if candidates, ok := byRule[r.Type]; ok {
				return candidates
			}
			candidates := []Relation{}
			for _, other := range relations {
				if sharesCycleGroup(d, r.Type, other.Type) || rule.Usage != "" && rule.Usage == d.Relations[other.Type].Usage {
					candidates = append(candidates, other)
				}
			}
			byRule[r.Type] = candidates
			return candidates
		}
		candidates := append([]Relation{}, bySource[r.SourceID]...)
		if rule.MaxIncoming > 0 {
			for _, other := range byTarget[r.TargetID] {
				if other.SourceID != r.SourceID {
					candidates = append(candidates, other)
				}
			}
		}
		if rule.Symmetric {
			for _, other := range bySource[r.TargetID] {
				if other.TargetID == r.SourceID {
					candidates = append(candidates, other)
				}
			}
		}
		return candidates
	}
}

// danglingSummary 是给日志用的短摘要：条数按类别分开，并给出可执行的下一步。
// 完整清单（含 entity id / 字段 / 悬挂值）走体检命令与 /impact 响应，不往日志里灌几十行。
func danglingSummary(items []DanglingReference) string {
	byKind := map[string]int{}
	for _, x := range items {
		byKind[x.Kind]++
	}
	parts := make([]string, 0, len(byKind))
	for _, k := range []string{kindAttribute, kindStructural, kindRelationEndpoint, kindRelationAttribute} {
		if n := byKind[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, n))
		}
	}
	return fmt.Sprintf("%d 条（%s）", len(items), strings.Join(parts, ", "))
}

// logDangling 把悬挂引用作为**警告**记录下来：不阻断发布，但要可诊断、可巡检。
func logDangling(where string, items []DanglingReference) {
	if len(items) == 0 {
		return
	}
	log.Printf("WARNING %s: 悬挂引用 %s；完整清单见 mf-migrate check-refs", where, danglingSummary(items))
}
