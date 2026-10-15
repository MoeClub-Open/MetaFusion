package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// 悬挂引用（dangling reference）：取值指向的行在库里已经不存在。
// 属性（JSONB）与关系端点没有全库强制的引用完整性——attributes 里的 entity 型取值本来就
// 是自由 JSON，关系端点的外键也只保证"插入时存在"。删除/合并/迁移工具、早期导入脚本都可能
// 留下指向已经不存在的行的取值，而它要到下一次"定义全量回放"时才会暴露（2026-09 事故：
// 31 条 attributes.publisher 指向已不存在的行，把服务启动顶成了 CrashLoop）。
//
// 于是这里提供两条互补的出口：
//   - impact 回放把它当**警告**（不阻断定义发布，见 impact.go 顶部）；
//   - 体检命令 mf-migrate check-refs 把它当**失败**（部署前置检查，非零退出）。
const (
	kindAttribute         = "attribute"          // definitions 声明的属性字段（含组/列表嵌套）
	kindStructural        = "structural"         // 结构归属与记录级引用（work_id / subjects[].work_id …）
	kindRelationEndpoint  = "relation_endpoint"  // 关系的 source_id / target_id
	kindRelationAttribute = "relation_attribute" // 关系属性里的 entity 型取值

	reasonMissing   = "missing"    // 行不存在
	reasonInvalidID = "invalid_id" // 取值不是 uuid，指向不了任何行
)

// DanglingReference 是一条机器可读的悬挂引用证据，字段就是修数据时要用的定位信息：
// 谁（Scope + ID）在哪个字段（Field）上写了什么值（Value），值为什么无效（Reason）。
type DanglingReference struct {
	Scope        string `json:"scope"` // entity | relation：持有这条引用的一侧
	ID           string `json:"id"`    // 实体 id 或关系 id
	Kind         string `json:"kind"`  // attribute | structural | relation_endpoint | relation_attribute
	Field        string `json:"field,omitempty"`
	Value        string `json:"value"`  // 悬挂值（uuid 或垃圾取值）
	Reason       string `json:"reason"` // missing | invalid_id
	RelationType string `json:"relation_type,omitempty"`
}

// DanglingReport 是库级体检结果：判定基准 + 全部悬挂引用。
// 基准必须一起回：字段是否参与判定取决于它有没有被定义声明为 entity 型，
// 所以"0 条"只在"用哪份定义、含哪些新增种子项"说清楚之后才是一个可用的结论。
type DanglingReport struct {
	DefinitionETag string              `json:"definition_etag,omitempty"`
	SeedAdded      []string            `json:"seed_added"`
	Items          []DanglingReference `json:"references"`
}

// refSite 是扫描过程中收集到的一处引用取值（尚未判定是否悬挂）。
type refSite struct {
	scope        string
	id           string
	kind         string
	field        string
	value        string
	relationType string
}

// DanglingReferences 是全库体检：判定基准用"下一次启动会发布的定义"——当前已发布文档与
// 种子新增的键（mergeSeedDefinitions，只增不改）合并后的结果。为什么不用当前已发布文档：
// 部署前置检查要在升级**之前**回答"新定义回放存量数据会不会撞悬挂引用"，只按旧文档扫描会
// 漏掉这次新增的 entity 型字段（新字段没人声明，扫描器根本不知道它该是引用）。
//
// 未播种定义的库（刚 migrate up、服务从未启动）退回内置种子作为基准，并在报告里
// DefinitionID=0 明确标出——体检不该在这时判"库坏了"，也不该假装查过一份已发布定义。
func (s *Store) DanglingReferences(ctx context.Context) (DanglingReport, error) {
	out := DanglingReport{SeedAdded: []string{}, Items: []DanglingReference{}}
	base := Defaults()
	v, err := s.Definitions(ctx)
	switch {
	case err == nil:
		out.DefinitionETag = v.ETag
		base, out.SeedAdded = mergeSeedDefinitions(v.Document, Defaults())
		if out.SeedAdded == nil {
			out.SeedAdded = []string{}
		}
	case errors.Is(err, sql.ErrNoRows):
		// 空库：只有种子可作基准。
	case err != nil:
		return out, err
	}
	ents, err := liveEntities(ctx, s.DB)
	if err != nil {
		return out, err
	}
	rels, err := relations(ctx, s.DB)
	if err != nil {
		return out, err
	}
	items, _, err := base.scanDangling(ctx, s.DB, ents, rels)
	if err != nil {
		return out, err
	}
	out.Items = items
	return out, nil
}

// liveEntities 取全部存活实体（deleted/merged 不进回放也不进体检：它们已退役，
// 引用了它们的边由生命周期路径自行处理，混进来只会让报告长期有噪音）。
//
// 整批一次读 + 按 kind 批量补齐结构字段（fillStructural），不逐条 get：启动期回放与体检都在
// 全量实体上跑，逐条访问在十万级目录上是分钟级开销，而这份开销要出现在**每次部署**的启动路径上。
// 顺序按 id 固定：报告与校验结论必须可复现。
func liveEntities(ctx context.Context, q queryer) ([]Entity, error) {
	rows, err := q.QueryContext(ctx, "SELECT id::text, document FROM catalog.entities WHERE state NOT IN ('deleted','merged') ORDER BY id")
	if err != nil {
		return nil, err
	}
	order := []string{}
	byID := map[string]Entity{}
	for rows.Next() {
		var id string
		var b []byte
		var e Entity
		if err = rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			rows.Close()
			return nil, err
		}
		// document 里的 id 以列为准（历史行可能没有该键）。
		e.ID = id
		byID[id] = e
		order = append(order, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	filled, err := fillStructural(ctx, q, byID)
	if err != nil {
		return nil, err
	}
	out := make([]Entity, 0, len(order))
	for _, id := range order {
		out = append(out, filled[id])
	}
	return out, nil
}

// scanDangling 扫描实体与关系上的全部引用站点，返回悬挂清单与判据索引。
// 索引键是 danglingKey(scopeID, value)，供 impact 判定"这次校验失败是数据欠账还是定义冲突"。
func (d Definitions) scanDangling(ctx context.Context, q queryer, ents []Entity, rels []Relation) ([]DanglingReference, map[string]bool, error) {
	sites := []refSite{}
	for _, e := range ents {
		sites = append(sites, d.entityRefSites(e)...)
	}
	for _, r := range rels {
		sites = append(sites, d.relationRefSites(r)...)
	}
	// 先收集取值再一次性取存在的行：逐个 get 是 N 次往返，而体检要能挂在部署前置里跑。
	candidates := []string{}
	seen := map[string]bool{}
	for _, s := range sites {
		if _, err := uuid.Parse(strings.TrimSpace(s.value)); err != nil || seen[s.value] {
			continue
		}
		seen[s.value] = true
		candidates = append(candidates, s.value)
	}
	existing, err := existingEntityIDs(ctx, q, candidates)
	if err != nil {
		return nil, nil, err
	}
	out := []DanglingReference{}
	index := map[string]bool{}
	for _, s := range sites {
		value := strings.TrimSpace(s.value)
		reason := ""
		if _, err := uuid.Parse(value); err != nil {
			reason = reasonInvalidID
		} else if !existing[value] {
			reason = reasonMissing
		}
		if reason == "" {
			continue
		}
		out = append(out, DanglingReference{Scope: s.scope, ID: s.id, Kind: s.kind, Field: s.field, Value: value, Reason: reason, RelationType: s.relationType})
		index[danglingKey(s.id, value)] = true
	}
	// 顺序稳定（按作用域、字段、取值）：报告给人看，也直接作为测试断言与 -json 输出。
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Value < out[j].Value
	})
	return out, index, nil
}

// refLookupChunk 是存在性查询的分批大小：线上目录实体是十万级，一次性把全部候选塞进
// 一条 ANY($1::uuid[]) 会让语句与数组内存随引用总数线性膨胀。
const refLookupChunk = 1000

// existingEntityIDs 返回这批 id 里在 catalog.entities 真有行的那些。
func existingEntityIDs(ctx context.Context, q queryer, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(ids); start += refLookupChunk {
		end := start + refLookupChunk
		if end > len(ids) {
			end = len(ids)
		}
		rows, err := q.QueryContext(ctx, "SELECT id::text FROM catalog.entities WHERE id = ANY($1::uuid[])", pq.Array(ids[start:end]))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// entityRefSites 收集一个实体上全部"定义声明过的引用"取值：
// attributes 里的 entity 型字段（含 group / list 嵌套）、记录级的发行对象与收录引用、
// 结构归属字段。结构字段（work_id / parent_id / …）有复合外键兜底，这里作为第二道网：
// 引用完整性一旦被绕过（历史数据、手工改库），体检仍要看得见。
func (d Definitions) entityRefSites(e Entity) []refSite {
	out := []refSite{}
	for _, k := range d.attributeKeys(e.Kind) {
		f, ok := d.Fields[k]
		if !ok {
			continue
		}
		collectRefSites(f, k, e.Attributes[k], "entity", e.ID, kindAttribute, &out)
	}
	structural := e.structuralRefs()
	for _, name := range sortedRefKeys(structural) {
		if v := strings.TrimSpace(structural[name]); v != "" {
			out = append(out, refSite{scope: "entity", id: e.ID, kind: kindStructural, field: name, value: v})
		}
	}
	for i, s := range e.Subjects {
		if strings.TrimSpace(s.WorkID) != "" {
			out = append(out, refSite{scope: "entity", id: e.ID, kind: kindStructural, field: fmt.Sprintf("subjects[%d].work_id", i), value: s.WorkID})
		}
		collectRefSites(d.Fields["subject_attributes"], fmt.Sprintf("subjects[%d].attributes", i), s.Attributes, "entity", e.ID, kindAttribute, &out)
	}
	for i, c := range e.Contents {
		if strings.TrimSpace(c.ExpressionID) != "" {
			out = append(out, refSite{scope: "entity", id: e.ID, kind: kindStructural, field: fmt.Sprintf("contents[%d].expression_id", i), value: c.ExpressionID})
		}
		collectRefSites(d.Fields["locator"], fmt.Sprintf("contents[%d].locator", i), c.Locator, "entity", e.ID, kindAttribute, &out)
		collectRefSites(d.Fields["inclusion_attributes"], fmt.Sprintf("contents[%d].attributes", i), c.Attributes, "entity", e.ID, kindAttribute, &out)
	}
	return out
}

// relationRefSites 收集一条关系上的引用：两端端点 + 关系属性里的 entity 型取值。
func (d Definitions) relationRefSites(r Relation) []refSite {
	out := []refSite{
		{scope: "relation", id: r.ID, kind: kindRelationEndpoint, field: "source_id", value: r.SourceID, relationType: r.Type},
		{scope: "relation", id: r.ID, kind: kindRelationEndpoint, field: "target_id", value: r.TargetID, relationType: r.Type},
	}
	rt, ok := d.Relations[r.Type]
	if !ok {
		return out
	}
	for _, k := range rt.Fields {
		f, ok := d.Fields[k]
		if !ok {
			continue
		}
		collectRefSites(f, k, r.Attributes[k], "relation", r.ID, kindRelationAttribute, &out)
	}
	return out
}

// collectRefSites 按字段定义展开一个取值，把 entity 型子字段的引用值收进 out。
// 路径与 definitions 的 diff 键同形（组用 . 连接、列表用 [n]），照着报告就能定位到数据。
func collectRefSites(f Field, path string, v any, scope, id, kind string, out *[]refSite) {
	switch f.Type {
	case "entity":
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			// 非字符串取值（数字、对象…）由定义校验报 invalid_reference，属定义冲突；
			// 这里只负责"取值是一个指向某行的字符串"这一类。
			return
		}
		*out = append(*out, refSite{scope: scope, id: id, kind: kind, field: path, value: s})
	case "group":
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for _, key := range sortedFieldKeys(f) {
			sub, ok := f.Fields[key]
			if !ok {
				continue
			}
			collectRefSites(sub, path+"."+key, m[key], scope, id, kind, out)
		}
	case "list":
		items, ok := v.([]any)
		if !ok || f.Items == nil {
			return
		}
		for i, item := range items {
			collectRefSites(*f.Items, fmt.Sprintf("%s[%d]", path, i), item, scope, id, kind, out)
		}
	}
}

// sortedRefKeys 让结构字段扫描顺序稳定（map 迭代顺序随机，报告必须可复现）。
func sortedRefKeys(refs map[string]string) []string {
	out := make([]string, 0, len(refs))
	for k := range refs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
