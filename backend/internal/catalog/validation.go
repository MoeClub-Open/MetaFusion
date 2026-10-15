package catalog

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/text/language"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var reserved = map[string]bool{"id": true, "kind": true, "version": true, "status": true, "created_by": true, "work_id": true, "parent_id": true, "release_id": true, "medium_id": true, "content_unit_id": true, "contents": true, "subjects": true, "redirect_id": true}

// StructuralAttributeFields 是"记录级动态结构"的入口字段码：收录位置（locator）、
// 收录附加属性、发行对象附加属性。它们不是普通实体属性，没有独立 Go 字段或数据库列，
// 全靠 definitions 声明子字段。因此入口本身属于固定契约：允许删除或改成非 group 类型，
// 会让校验空转——取不到定义时未声明的数据反而被放过。入口常驻，内部子字段仍由后台扩展。
var StructuralAttributeFields = []string{"locator", "inclusion_attributes", "subject_attributes"}

// structuralFieldsPresent 校验结构属性入口存在且仍为 group 类型。
func (d Definitions) structuralFieldsPresent() error {
	for _, code := range StructuralAttributeFields {
		f, ok := d.Fields[code]
		if !ok {
			return fmt.Errorf("structural_field_required: %s", code)
		}
		if f.Type != "group" {
			return fmt.Errorf("structural_field_type: %s", code)
		}
	}
	return nil
}

func validURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// toFloat 把数值统一为 float64：JSON 往返是 float64，而代码内部构造的属性
// （如 track duration）可能是 int/int64，两者都必须接受。
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	}
	return 0, false
}

// sortedFieldKeys 返回组内子字段码的字典序，用于让校验报错稳定可复现。
func sortedFieldKeys(f Field) []string {
	keys := make([]string, 0, len(f.Fields))
	for k := range f.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validateGroupRanges 校验显式区间声明：range_start 只能写在 number 子字段上，
// 必须指向同组另一个 number 子字段，且两者不能再各自声明区间（拒绝链式与自环）。
// validPictureTime 校验图片时间：允许空（老数据或来源未注明），
// 允许部分书目日期（YYYY / YYYY-MM / YYYY-MM-DD，与动态 date 字段同一口径）或 RFC3339 时刻。
func validPictureTime(s string) bool {
	v := strings.TrimSpace(s)
	if v == "" {
		return true
	}
	layout := "2006-01-02"
	switch len(v) {
	case 4:
		layout = "2006"
	case 7:
		layout = "2006-01"
	}
	if _, err := time.Parse(layout, v); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// 部分日期按所声明精度的起止边界比较；例如 2025-12 至 2025 不是倒置。
// 仅用于拒绝确定不可能的区间，不做自动选图或互斥性判断。
func pictureTimeStart(s string) time.Time {
	v := strings.TrimSpace(s)
	switch len(v) {
	case 4:
		t, _ := time.Parse("2006", v)
		return t
	case 7:
		t, _ := time.Parse("2006-01", v)
		return t
	case 10:
		t, _ := time.Parse("2006-01-02", v)
		return t
	default:
		t, _ := time.Parse(time.RFC3339, v)
		return t
	}
}

func pictureTimeEnd(s string) time.Time {
	t := pictureTimeStart(s)
	switch len(strings.TrimSpace(s)) {
	case 4:
		return t.AddDate(1, 0, 0).Add(-time.Nanosecond)
	case 7:
		return t.AddDate(0, 1, 0).Add(-time.Nanosecond)
	case 10:
		return t.AddDate(0, 0, 1).Add(-time.Nanosecond)
	default:
		return t
	}
}

func validateGroupRanges(f Field) error {
	for _, key := range sortedFieldKeys(f) {
		child := f.Fields[key]
		if child.RangeStart == "" {
			continue
		}
		if child.Type != "number" {
			return fmt.Errorf("range_start_not_number: %s", key)
		}
		if child.RangeStart == key {
			return fmt.Errorf("range_start_self: %s", key)
		}
		start, ok := f.Fields[child.RangeStart]
		if !ok {
			return fmt.Errorf("range_start_unknown: %s", child.RangeStart)
		}
		if start.Type != "number" {
			return fmt.Errorf("range_start_not_number: %s", child.RangeStart)
		}
		if start.RangeStart != "" {
			return fmt.Errorf("range_start_chain: %s", key)
		}
	}
	return nil
}

// validateGroupOrder 校验组内声明的区间顺序与锚点：range_start 显式声明
// "终点子字段 → 起点子字段"的配对，只有显式声明的区间才比较大小。
// 不再按字段名（start/end、begin/end、_max…）猜测配对：那会让后台新加的两个
// 数字字段在没有任何配置的情况下触发隐含规则，规则来源不可见也不可控。
// 子字段码按字典序遍历，保证多条违规时报错稳定。
func validateGroupOrder(m map[string]any, f Field) error {
	for _, key := range sortedFieldKeys(f) {
		child := f.Fields[key]
		if child.RangeStart == "" {
			continue
		}
		sv, sok := toFloat(m[child.RangeStart])
		ev, eok := toFloat(m[key])
		if sok && eok && sv > ev {
			return fmt.Errorf("invalid_range: %s", child.RangeStart)
		}
	}
	// 锚点规则：组内任一其它子字段有值，锚点子字段必须有值。
	if f.AnchorKey != "" {
		anchorSet := !isEmptyValue(m[f.AnchorKey])
		anyOther := false
		for k, v := range m {
			if k == f.AnchorKey {
				continue
			}
			if !isEmptyValue(v) {
				anyOther = true
				break
			}
		}
		if anyOther && !anchorSet {
			return fmt.Errorf("anchor_required: %s", f.AnchorKey)
		}
	}
	return nil
}

// isEmptyValue 判断属性值是否"未提供"（nil、空串、空数组、空对象）。
func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
func validateSources(note string, sources []Source) error {
	policy := configuredSourcePolicy.Load()
	if policy == nil {
		policy = &defaultSourcePolicy
	}
	return validateSourcesWithPolicy(note, sources, policy)
}

// validateSourceShapes accepts historical self sources for read/impact checks.
// New submissions additionally pass validateSources and changed nested evidence
// passes validateChangedEntitySources before it is written.
func validateSourceShapes(note string, sources []Source) error {
	if strings.TrimSpace(note) == "" || len(sources) == 0 {
		return fmt.Errorf("evidence_required")
	}
	for _, s := range sources {
		if !contains([]string{"url", "publication", "self"}, s.Kind) || strings.TrimSpace(s.Citation) == "" || (s.Kind == "url" && !validURL(s.URL)) || (s.URL != "" && !validURL(s.URL)) {
			return fmt.Errorf("invalid_source")
		}
	}
	return nil
}

// requiredNameLocales 是每个名称都必须齐备的语种（命名四语铁律）。
// ja 与 ja-JP 视为同一语种（种子两个键都写，只写其一也放行），单独判定。
var requiredNameLocales = []string{"zh-CN", "zh-TW", "en-US"}

// validateNames 要求名称四语齐备、取值非空、语种码合法。
//
// 只查「键在且非空」而不查「是否与英文不同」：Spotify / ISBNdb / CD 这类专有名词
// 在几种文字里本就同形，把"值与英文相同"当硬错误会连合法名称一起拒掉。
// 是否仍是英文占位属审查口径，由 scripts/check_data.py 报 P2 与种子棘轮测试看守。
func validateNames(n Names) error {
	var missing []string
	for _, loc := range requiredNameLocales {
		if strings.TrimSpace(n[loc]) == "" {
			missing = append(missing, loc)
		}
	}
	if strings.TrimSpace(n["ja"]) == "" && strings.TrimSpace(n["ja-JP"]) == "" {
		missing = append(missing, "ja-JP")
	}
	if len(missing) > 0 {
		return fmt.Errorf("four_locale_names_required: %s", strings.Join(missing, ","))
	}
	return validateNameLocales(n)
}

// validateNameLocales 校验名称里的语种键能否解析（ja 与 ja-JP 都合法）。
// 与四语铁律分开：用户首页分区名只强制 zh-CN，其余语种缺省时前端按回退链显示。
func validateNameLocales(n Names) error {
	for k := range n {
		if _, e := language.Parse(k); e != nil {
			return fmt.Errorf("invalid_locale")
		}
	}
	return nil
}
func validateKinds(kinds []string) error {
	if len(kinds) == 0 {
		return fmt.Errorf("kinds_required")
	}
	for _, k := range kinds {
		if !contains(Kinds, k) {
			return fmt.Errorf("invalid_kind")
		}
	}
	return nil
}
func (d Definitions) Validate() error {
	if d.Fields == nil || d.Relations == nil || d.Vocabularies == nil || d.Templates == nil || d.Schemes == nil || d.Structure == nil {
		return fmt.Errorf("definitions_required")
	}
	fields := func(keys []string) error {
		for _, k := range keys {
			if _, ok := d.Fields[k]; !ok {
				return fmt.Errorf("unknown_field: %s", k)
			}
		}
		return nil
	}
	// 结构属性入口属于固定契约：常驻且必须仍是 group，否则校验会因取不到定义而空转。
	if err := d.structuralFieldsPresent(); err != nil {
		return err
	}
	for code, v := range d.Vocabularies {
		if !codePattern.MatchString(code) {
			return fmt.Errorf("invalid_code")
		}
		if e := validateNames(v.Names); e != nil {
			return e
		}
		for k, t := range v.Terms {
			if !codePattern.MatchString(k) {
				return fmt.Errorf("invalid_code")
			}
			if t.Enabled {
				if e := validateNames(t.Names); e != nil {
					return e
				}
			}
		}
	}
	for code, f := range d.Fields {
		if !codePattern.MatchString(code) || reserved[code] {
			return fmt.Errorf("reserved_field: %s", code)
		}
		if e := d.validateField(f, 0); e != nil {
			return fmt.Errorf("%s: %w", code, e)
		}
	}
	for code, r := range d.Relations {
		if !codePattern.MatchString(code) {
			return fmt.Errorf("invalid_code")
		}
		if r.Enabled {
			if e := validateNames(r.Names); e != nil {
				return e
			}
			if e := validateNames(r.ReverseNames); e != nil {
				return e
			}
			// 分组名此前完全没进校验：缺语种时前端只能退回兜底分组，用户看不到真实分组名。
			if len(r.GroupNames) > 0 {
				if e := validateNames(r.GroupNames); e != nil {
					return e
				}
			}
		}
		if e := validateKinds(r.SourceKinds); e != nil {
			return e
		}
		if e := validateKinds(r.TargetKinds); e != nil {
			return e
		}
		if r.Symmetric && r.Acyclic {
			return fmt.Errorf("symmetric_acyclic_conflict")
		}
		// 署名槽位是闭集（见 types.go 的 ParticipantSlot 注释）：person 对端是署名主体、
		// character 对端是虚构角色、peer 是同层级对象不产生署名。
		// GUI 用下拉约束，服务端再拦一次手造载荷；拼错的槽位会让展示端静默丢署名。
		switch r.ParticipantSlot {
		case "person", "character", "peer":
		default:
			return fmt.Errorf("invalid_participant_slot: %s", code)
		}
		if r.MaxIncoming < 0 || r.MaxOutgoing < 0 {
			return fmt.Errorf("invalid_cardinality")
		}
		if e := d.validateRelationConstraints(code, r); e != nil {
			return e
		}
		if e := fields(r.Fields); e != nil {
			return e
		}
	}
	for code, s := range d.Schemes {
		if err := d.validateScheme(code, s); err != nil {
			return err
		}
	}
	// 结构规则此前完全不校验：码拼错、scoped_by 指向不存在的上级字段这类错误会在写实体时
	// 变成"必填判定永远取不到值"（每次保存都 parent_required）或悄悄失去过滤候选的能力。
	// 字段码的权威集合是 Entity.structuralRefs（validateEntity 用它取值），两边同源。
	for kind, rule := range d.Structure {
		if !contains(Kinds, kind) {
			return fmt.Errorf("%s: %w", kind, fmt.Errorf("invalid_kind"))
		}
		declared := map[string]bool{}
		for _, f := range rule.Fields {
			code := strings.TrimSpace(f.Code)
			if _, ok := (Entity{}).structuralRefs()[code]; !ok {
				return fmt.Errorf("%s: %w", kind, fmt.Errorf("invalid_structural_field: %s", f.Code))
			}
			if declared[code] {
				return fmt.Errorf("%s: %w", kind, fmt.Errorf("duplicate_field: %s", f.Code))
			}
			declared[code] = true
			for _, tk := range f.TargetKinds {
				if !contains(Kinds, tk) {
					return fmt.Errorf("%s: %w", kind, fmt.Errorf("invalid_kind: %s", tk))
				}
			}
		}
		for _, f := range rule.Fields {
			if strings.TrimSpace(f.ScopedBy) == "" {
				continue
			}
			// scoped_by 只能指向同一规则里声明过的结构字段（通常是上级归属），且不能自指：
			// 取不到被指的字段时候选过滤整组失效。
			if !declared[f.ScopedBy] || f.ScopedBy == f.Code {
				return fmt.Errorf("%s: %w", kind, fmt.Errorf("invalid_scoped_by: %s", f.ScopedBy))
			}
		}
	}
	if len(d.Structure) > 0 {
		fixed := Defaults().Structure
		for _, kind := range Kinds {
			want, required := fixed[kind]
			got, present := d.Structure[kind]
			if required && !present {
				return fmt.Errorf("fixed_structure_mismatch: %s", kind)
			}
			if !present {
				continue
			}
			if got.Subjects != want.Subjects || got.Contents != want.Contents || len(got.Fields) != len(want.Fields) {
				return fmt.Errorf("fixed_structure_mismatch: %s", kind)
			}
			fields := make(map[string]StructureField, len(got.Fields))
			for _, field := range got.Fields {
				fields[field.Code] = field
			}
			for _, field := range want.Fields {
				actual, ok := fields[field.Code]
				if !ok || actual.Required != field.Required || actual.ScopedBy != field.ScopedBy || len(actual.TargetKinds) != len(field.TargetKinds) {
					return fmt.Errorf("fixed_structure_mismatch: %s.%s", kind, field.Code)
				}
				for _, target := range field.TargetKinds {
					if !contains(actual.TargetKinds, target) {
						return fmt.Errorf("fixed_structure_mismatch: %s.%s", kind, field.Code)
					}
				}
			}
		}
	}
	for kind, rule := range d.Structure {
		if rule.Subjects {
			if err := validateNames(rule.SubjectNames); err != nil {
				return fmt.Errorf("%s.subject_names: %w", kind, err)
			}
			if err := validateNames(rule.SubjectReverseNames); err != nil {
				return fmt.Errorf("%s.subject_reverse_names: %w", kind, err)
			}
		}
		if rule.Contents {
			if err := validateNames(rule.ContentNames); err != nil {
				return fmt.Errorf("%s.content_names: %w", kind, err)
			}
			if err := validateNames(rule.ContentReverseNames); err != nil {
				return fmt.Errorf("%s.content_reverse_names: %w", kind, err)
			}
		}
		for _, f := range rule.Fields {
			if err := validateNames(f.Names); err != nil {
				return fmt.Errorf("%s.%s.names: %w", kind, f.Code, err)
			}
			if err := validateNames(f.ReverseNames); err != nil {
				return fmt.Errorf("%s.%s.reverse_names: %w", kind, f.Code, err)
			}
		}
	}
	for code, t := range d.Templates {
		if !codePattern.MatchString(code) {
			return fmt.Errorf("invalid_code")
		}
		if e := validateNames(t.Names); e != nil {
			return e
		}
		if e := d.validateTemplateRules(code, t); e != nil {
			return e
		}
		if !contains([]string{"tree", "list", "discs"}, t.Directory) {
			return fmt.Errorf("invalid_directory")
		}
		if e := fields(t.Columns); e != nil {
			return e
		}
		for _, s := range t.Sections {
			if e := validateNames(s.Names); e != nil {
				return e
			}
			if e := fields(s.Fields); e != nil {
				return e
			}
		}
		// 模板声明的主日期/徽章字段必须真实存在，否则展示层会取到空值而不报错。
		if t.PrimaryDateField != "" {
			if _, ok := d.Fields[t.PrimaryDateField]; !ok {
				return fmt.Errorf("unknown_field: %s", t.PrimaryDateField)
			}
		}
		if e := fields(t.BadgeFields); e != nil {
			return e
		}
		if e := fields(t.FacetFields); e != nil {
			return e
		}
	}
	return nil
}

// validateScheme 校验单个场景声明：码合规、slot 命中结构入口、
// fields 全部已在全局组声明、required ⊆ fields、启用中的场景名称四语齐备；类型错一律拒绝。
func (d Definitions) validateScheme(code string, s Scheme) error {
	if !codePattern.MatchString(code) {
		return fmt.Errorf("invalid_code")
	}
	if !contains(StructuralAttributeFields, s.Slot) {
		return fmt.Errorf("%s: %w", code, fmt.Errorf("invalid_slot"))
	}
	if s.Enabled {
		if err := validateNames(s.Names); err != nil {
			return fmt.Errorf("%s: %w", code, err)
		}
	}
	for _, k := range s.Kinds {
		if !contains(Kinds, k) {
			return fmt.Errorf("%s: %w", code, fmt.Errorf("invalid_kind"))
		}
	}
	if s.MediumFormats != nil {
		if s.Slot == "subject_attributes" || len(s.Kinds) > 0 && !contains(s.Kinds, "track") {
			return fmt.Errorf("%s: invalid_medium_format_scope", code)
		}
		for _, format := range *s.MediumFormats {
			if _, ok := d.Vocabularies["format"].Terms[format]; !ok {
				return fmt.Errorf("%s: unknown_medium_format: %s", code, format)
			}
		}
	}
	group, ok := d.Fields[s.Slot]
	if !ok || group.Type != "group" {
		return fmt.Errorf("%s: %w", code, fmt.Errorf("structural_field_type: %s", s.Slot))
	}
	declared := map[string]bool{}
	for k := range group.Fields {
		declared[k] = true
	}
	for _, k := range s.Fields {
		if !declared[k] {
			return fmt.Errorf("%s: %w", code, fmt.Errorf("unknown_field: %s", k))
		}
	}
	allowed := map[string]bool{}
	for _, k := range s.Fields {
		allowed[k] = true
	}
	for _, k := range s.Required {
		if !allowed[k] {
			return fmt.Errorf("%s: %w", code, fmt.Errorf("required_outside_fields: %s", k))
		}
	}
	// 若全局组声明了 AnchorKey（如 relative_to），方案字段集必须包含该锚点，
	// 否则录入时会陷入“不填报缺锚点、填了报未知字段”的死锁。
	if group.AnchorKey != "" && !allowed[group.AnchorKey] {
		return fmt.Errorf("%s: %w", code, fmt.Errorf("scheme_missing_anchor: %s", group.AnchorKey))
	}
	return nil
}

// matchSchemes 找出与结构层级和载体格式匹配的场景。未匹配时使用全局结构字段。
func (d Definitions) matchSchemes(slot, ownerKind string, mediumFormat ...string) []Scheme {
	var out []Scheme
	for _, s := range d.Schemes {
		if !s.Enabled || s.Slot != slot {
			continue
		}
		if len(s.Kinds) > 0 && !contains(s.Kinds, ownerKind) {
			continue
		}
		if s.MediumFormats != nil && len(*s.MediumFormats) > 0 &&
			(ownerKind != "track" || len(mediumFormat) == 0 || !contains(*s.MediumFormats, mediumFormat[0])) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// effectiveGroupField 用"并集 fields"构造有效组定义：拷贝全局组定义、
// Fields 过滤到并集、Required 按并集 required 设置；无匹配时回退全局组。
// 入口缺失时返回零值 Field：调用方 value 的 default 分支报 unknown_field_type
// 而非静默放过——入口缺失是固定契约被破坏，定义层由 structuralFieldsPresent
// 在 Validate 拒绝；实体层此处同样失败（空数据已被 isEmptyValue 提前放行，
// 见 TestStructuralEntryMissingRejectsEntityData）。
func (d Definitions) effectiveGroupField(slot, ownerKind string, mediumFormat ...string) Field {
	group, ok := d.Fields[slot]
	if !ok {
		return Field{}
	}
	matched := d.matchSchemes(slot, ownerKind, mediumFormat...)
	if len(matched) == 0 {
		return group
	}
	// 并集只用于"哪些子字段可用"（顺序由 group.Fields 的 map 决定，前端按 scheme 的
	// fields 顺序展示），因此这里不需要另外累积顺序。
	union := map[string]bool{}
	required := map[string]bool{}
	for _, s := range matched {
		for _, k := range s.Fields {
			union[k] = true
		}
		for _, k := range s.Required {
			required[k] = true
		}
	}
	// 若全局组有 AnchorKey，自动确保 effectiveGroup 包含该锚点字段定义，双重保障
	if group.AnchorKey != "" && group.Fields[group.AnchorKey].Enabled {
		union[group.AnchorKey] = true
	}
	eff := group
	eff.Fields = map[string]Field{}
	for k, c := range group.Fields {
		if union[k] {
			if required[k] {
				c.Required = true
			} else {
				c.Required = false
			}
			eff.Fields[k] = c
		}
	}
	return eff
}

// requireRangeSchemes 返回匹配场景中 require_range 为 true 的那些（仅 locator 有意义）。
func requireRangeSchemes(matched []Scheme) bool {
	for _, s := range matched {
		if s.RequireRange {
			return true
		}
	}
	return false
}

// checkRangeRequired 要求 locator 至少一个 semantics=content 的子字段非空。
func checkRangeRequired(group Field, m map[string]any) error {
	for k, c := range group.Fields {
		if c.Semantics == "content" && !isEmptyValue(m[k]) {
			return nil
		}
	}
	return fmt.Errorf("range_required")
}

func (d Definitions) validateField(f Field, depth int) error {
	if depth > 4 {
		return fmt.Errorf("field_nesting_limit")
	}
	if len(f.ApplicableKinds) > 0 {
		if e := validateKinds(f.ApplicableKinds); e != nil {
			return e
		}
	}
	// 与类型/关系同一口径：四语只为启用中的字段强制。
	// 单位名（unit）只在声明了单位时校验——未声明单位是常态，不能反过来当缺项。
	if f.Enabled {
		if e := validateNames(f.Names); e != nil {
			return e
		}
		if len(f.Unit) > 0 {
			if e := validateNames(f.Unit); e != nil {
				return e
			}
		}
	}
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		return fmt.Errorf("invalid_range")
	}
	// 对比语义是有限声明的闭集：后台只能选择系统真正支持的规则，
	// 不允许自由填写（避免出现"配置了但没有任何运行时效果"的选项）。
	if f.Semantics != "" && f.Semantics != "content" && f.Semantics != "locating" {
		return fmt.Errorf("invalid_semantics")
	}
	switch f.Type {
	case "text", "multilingual", "number", "date", "boolean", "url":
	case "enum":
		if _, ok := d.Vocabularies[f.Vocabulary]; !ok {
			return fmt.Errorf("unknown_vocabulary")
		}
	case "entity":
		return validateKinds(f.Kinds)
	case "list":
		if f.Items == nil {
			return fmt.Errorf("items_required")
		}
		return d.validateField(*f.Items, depth+1)
	case "group":
		for k, c := range f.Fields {
			if !codePattern.MatchString(k) || reserved[k] {
				return fmt.Errorf("invalid_field")
			}
			if e := d.validateField(c, depth+1); e != nil {
				return e
			}
		}
		if e := validateGroupRanges(f); e != nil {
			return e
		}
	default:
		return fmt.Errorf("invalid_field_type")
	}
	return nil
}

// text/url 长度上限：标题外最长的自由文本（简介、引用、URL）统一截断口径，
// 防止超大载荷进 JSONB 拖慢索引与 revisions 快照。数值/日期走各自格式校验。
const (
	maxTextLen = 20000
	maxURLLen  = 4000
	// maxPictures 是单实体的封面张数上限。每张都随 document 落库、并再复制进每次保存的
	// revisions 全量快照，不封顶就等于把"一次写能撑多大"交给调用方决定。
	maxPictures = 40
	// pictureRoleVocabulary 是 pictures[].role 用的词表码，词条在 defaults.go 播种、
	// 后台可增删；role 为空表示未声明用途（存量数据全部为空，照常放行）。
	pictureRoleVocabulary = "picture_role"
)

func (d Definitions) value(f Field, v any, reference func(string, []string) error, historical bool) error {
	// nil 视为未提供：仅在必填或 group 包含必填子字段时拒绝，其余类型非必填放行。
	if v == nil {
		if f.Required {
			return fmt.Errorf("required_field")
		}
		if f.Type == "group" {
			for _, c := range f.Fields {
				if c.Required {
					return fmt.Errorf("required_field")
				}
			}
		}
		return nil
	}
	// 零值 Field（如 effectiveGroupField 入口缺失）：空数据放行（整轨收录不受影响），
	// 携带数据则落入 default 报 unknown_field_type 拦截。
	if f.Type == "" && isEmptyValue(v) {
		return nil
	}
	// 先验类型（Type-check first）：绝不能在类型判断前将空数组 [] 或空对象 {}
	// 误判为数值、布尔等类型的空值放行（防止数字字段接受 []、列表字段接受 {} 导致前端崩溃）。
	switch f.Type {
	case "text":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("expected_text")
		}
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		if len(s) > maxTextLen {
			return fmt.Errorf("text_too_long")
		}
	case "url":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid_url")
		}
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		if len(s) > maxURLLen || !validURL(s) {
			return fmt.Errorf("invalid_url")
		}
	case "date":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid_date")
		}
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		layout := "2006-01-02"
		if len(s) == 4 {
			layout = "2006"
		} else if len(s) == 7 {
			layout = "2006-01"
		}
		if _, e := time.Parse(layout, s); e != nil {
			return fmt.Errorf("invalid_date")
		}
	case "number":
		// Number 必须能转为合法数值（拒绝 []、{}、非数值字符串等）
		n, ok := toFloat(v)
		if !ok || math.IsInf(n, 0) || math.IsNaN(n) || f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			return fmt.Errorf("invalid_number")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected_boolean")
		}
	case "multilingual":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected_object")
		}
		if len(m) == 0 {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		for k, x := range m {
			if _, e := language.Parse(k); e != nil {
				return fmt.Errorf("invalid_locale")
			}
			s, ok := x.(string)
			if !ok {
				return fmt.Errorf("expected_text")
			}
			if len(s) > maxTextLen {
				return fmt.Errorf("text_too_long")
			}
		}
	case "enum":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid_term")
		}
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		t, exists := d.Vocabularies[f.Vocabulary].Terms[s]
		if !exists || !historical && !t.Enabled {
			return fmt.Errorf("invalid_term")
		}
	case "entity":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid_reference")
		}
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		return reference(s, f.Kinds)
	case "list":
		items, ok := v.([]any)
		if !ok {
			return fmt.Errorf("invalid_list")
		}
		if len(items) == 0 {
			if f.Required {
				return fmt.Errorf("required_field")
			}
			return nil
		}
		if len(items) > 1000 {
			return fmt.Errorf("invalid_list")
		}
		for _, x := range items {
			if e := d.value(*f.Items, x, reference, historical); e != nil {
				return e
			}
		}
	case "group":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected_object")
		}
		if len(m) == 0 && f.Required {
			return fmt.Errorf("required_field")
		}
		for k := range m {
			if _, ok := f.Fields[k]; !ok {
				return fmt.Errorf("unknown_field: %s", k)
			}
		}
		for k, c := range f.Fields {
			if e := d.value(c, m[k], reference, historical); e != nil {
				return e
			}
		}
		if e := validateGroupOrder(m, f); e != nil {
			return e
		}
	default:
		return fmt.Errorf("unknown_field_type: %s", f.Type)
	}
	return nil
}
func (d Definitions) attributes(keys []string, values map[string]any, reference func(string, []string) error, historical bool) error {
	for k := range values {
		if !contains(keys, k) {
			return fmt.Errorf("unknown_field: %s", k)
		}
	}
	for _, k := range keys {
		f, ok := d.Fields[k]
		if !ok {
			return fmt.Errorf("unknown_field: %s", k)
		}
		if !historical && !f.Enabled && values[k] != nil {
			return fmt.Errorf("disabled_field: %s", k)
		}
		if e := d.value(f, values[k], reference, historical); e != nil {
			return fmt.Errorf("%s: %w", k, e)
		}
	}
	return nil
}

// legacyImportKeys 保留旧导入标识的格式校验；历史实体可保留或删除已有值，
// 所有写路径均由 guardImportKey 禁止新声明或改写。
var legacyImportKeys = map[string]bool{"metafusion_import": true}

// guardImportKey 阻止非导入写入**新声明或改写**内部幂等键。
// 同值放行：PUT 是全量替换（AGENTS.md 的"先读全量再写"），已带该键的条目原样写回
// 必须通过，否则任何一次常规编辑都会被自己的历史键挡住。
func guardImportKey(next, prev map[string]string) error {
	key := strings.TrimSpace(next["metafusion_import"])
	if key == "" || key == strings.TrimSpace(prev["metafusion_import"]) {
		return nil
	}
	return fmt.Errorf("invalid_import_key")
}

// validImportKey 校验幂等键格式。基础键按来源各自的 ID 形态收敛：
//   - bangumi：bangumi:{subject|person|character}:{数字id}
//   - dlsite：dlsite:work:{RJ/RE/BJ/VJ+数字}；社团为 dlsite:circle:RG{数字}
//   - dmm：dmm:work:{cid}
//
// 保留旧导入器已生成的派生后缀格式，供历史数据正常编辑。
var importKeyPattern = regexp.MustCompile(`^(?:bangumi:(?:subject|person|character):[1-9][0-9]*|dlsite:work:(?:RJ|RE|BJ|VJ)[0-9]{5,8}|dlsite:circle:RG[0-9]+|dmm:work:[a-z0-9_]{3,32})(?::e[0-9a-f]+|(?::release)?(?::r[0-9a-f]+)?(?::m[0-9]+(?::t[0-9]+)?)?)?$`)

func validImportKey(s string) bool { return importKeyPattern.MatchString(strings.TrimSpace(s)) }

// validateExternalIDs 校验 external_ids：
//   - 键必须符合 codePattern（与 external_databases.code 的库约束同口径）；
//     具体"是否存在+正则+分类"由 Store.Save 经 validateExternalIDsAgainstDB
//     按预设表复核（需读库，此处无库）；
//   - 值非空、长度收敛；metafusion_import 内部键只验格式（invalid_import_key）。
func (d Definitions) validateExternalIDs(e Entity) error {
	if len(e.ExternalIDs) == 0 {
		return nil
	}
	for k, v := range e.ExternalIDs {
		v = strings.TrimSpace(v)
		if v == "" {
			return fmt.Errorf("invalid_external_id: %s", k)
		}
		if len(k) > 64 || len(v) > 2000 {
			return fmt.Errorf("invalid_external_id: %s", k)
		}
		if legacyImportKeys[k] {
			if !validImportKey(v) {
				return fmt.Errorf("invalid_import_key")
			}
			continue
		}
		if !codePattern.MatchString(k) {
			return fmt.Errorf("invalid_external_key: %s", k)
		}
	}
	return nil
}

// structuralRefs 返回"结构字段码 → 取值"。结构校验（validateEntity）与 definitions 的
// structure 规则校验共用它，保证规则里能声明的码与实体上真正会读取的列是同一批。
func (e Entity) structuralRefs() map[string]string {
	return map[string]string{"work_id": e.WorkID, "parent_id": e.ParentID, "content_unit_id": e.ContentUnitID, "release_id": e.ReleaseID, "medium_id": e.MediumID}
}

// validateEntityContent 校验与"实体在结构里的位置"无关的内容：类型声明、属性取值
// （词表项/引用可达/日期与数值格式/列表与子组）、原语言、翻译行、图片。
//
// Store.Save 与提交校验共用内容验证。
// historical 语义与 Save 一致（true：停用的码与词表项不断读，只影响新增使用）。
func (d Definitions) validateEntityContent(e Entity, reference func(string, []string) error, historical bool) error {
	if e.OriginalLanguage != "" {
		if _, err := language.Parse(e.OriginalLanguage); err != nil {
			return fmt.Errorf("invalid_locale")
		}
	}
	for loc, tr := range e.Translations {
		if _, err := language.Parse(loc); err != nil || strings.TrimSpace(tr.Title) == "" {
			return fmt.Errorf("invalid_translation")
		}
		if len(tr.Title) > 2000 || len(tr.Summary) > maxTextLen {
			return fmt.Errorf("translation_too_long")
		}
		if !utf8.ValidString(tr.Body) || strings.ContainsRune(tr.Body, '\x00') {
			return fmt.Errorf("invalid_article_body")
		}
		if utf8.RuneCountInString(tr.Body) > MaxArticleLength {
			return fmt.Errorf("article_too_long")
		}
		for _, a := range tr.Aliases {
			if len(a) > 500 {
				return fmt.Errorf("translation_too_long")
			}
		}
	}
	// 零翻译发布由 Store.Save 显式拦截（translation_required），此处不重复：
	// 调用方统一 historical=true（存量/impact 宽容）。保留注释以防回退。
	keys := d.attributeKeys(e.Kind)
	if err := d.attributes(keys, e.Attributes, reference, historical); err != nil {
		return err
	}
	// 多图契约（见 types.go 的 PicturesJSON）：数组顺序即展示顺序、pictures[0] 即封面，
	// 服务端**不重排**。校验数量、URL 唯一性、适用区间与用途词表：
	// 数量封顶（每张都进 document 与每次保存的 revisions 快照，无上限等于让
	// 单实体写放大不设防）、同实体内 URL 去重（同一张图挂两次只会让画廊出现重复格子）、
	// role 必须落在 picture_role 词表里（用途码不许自由填写，否则前端无法多语言解析）。
	if len(e.Pictures) > maxPictures {
		return fmt.Errorf("too_many_pictures")
	}
	seenPictureURLs := make(map[string]bool, len(e.Pictures))
	for _, p := range e.Pictures {
		if !validURL(p.URL) {
			return fmt.Errorf("invalid_picture")
		}
		if !validPictureTime(p.TakenAt) {
			return fmt.Errorf("invalid_picture_time")
		}
		if p.UsagePeriod != nil {
			begin, end := strings.TrimSpace(p.UsagePeriod.Begin), strings.TrimSpace(p.UsagePeriod.End)
			if (begin == "" && end == "") || !validPictureTime(begin) || !validPictureTime(end) {
				return fmt.Errorf("invalid_picture_period")
			}
			if begin != "" && end != "" && pictureTimeStart(begin).After(pictureTimeEnd(end)) {
				return fmt.Errorf("invalid_picture_period")
			}
		}
		if err := validateSourceShapes("picture", []Source{p.Source}); err != nil {
			return err
		}
		if p.Role != "" {
			v, ok := d.Vocabularies[pictureRoleVocabulary]
			if !ok {
				return fmt.Errorf("unknown_vocabulary")
			}
			t, exists := v.Terms[p.Role]
			if !exists || !historical && !t.Enabled {
				return fmt.Errorf("invalid_term")
			}
		}
		// asset_id 是存储服务 assets 的主键（uuid）。目录侧不跨服务查它存不存在
		// （没有跨服务事务，查了也挡不住随后解绑/封禁），只保证它是个可寻址的 id。
		if p.AssetID != "" {
			if _, err := uuid.Parse(strings.TrimSpace(p.AssetID)); err != nil {
				return fmt.Errorf("invalid_picture_asset")
			}
		}
		url := strings.TrimSpace(p.URL)
		if seenPictureURLs[url] {
			return fmt.Errorf("duplicate_picture")
		}
		seenPictureURLs[url] = true
	}
	return nil
}

// attributeKeys 按字段自身声明的适用层级组成实体属性白名单。
// 字段取值由 Field.Type 校验；适用范围不依赖出版物分类或展示模板。
func (d Definitions) attributeKeys(kind string) []string {
	keys := make([]string, 0)
	for code, field := range d.Fields {
		if contains(field.ApplicableKinds, kind) {
			keys = append(keys, code)
		}
	}
	sort.Strings(keys)
	return keys
}
func (d Definitions) validateEntity(e Entity, reference func(string, []string) error, historical bool, mediumFormat ...string) error {
	if !contains(Kinds, e.Kind) || strings.TrimSpace(e.Title) == "" || len(e.Title) > 2000 || e.Position < 0 {
		return fmt.Errorf("invalid_entity")
	}
	if !contains([]string{"draft", "pending_review", "published", "deleted", "merged"}, e.Status) {
		return fmt.Errorf("invalid_status")
	}
	if err := d.validateEntityContent(e, reference, historical); err != nil {
		return err
	}
	// 结构归属规则来自已发布 definitions：哪些结构字段可用、哪些必填。
	rule := d.Structure[e.Kind]
	allowedField := map[string]bool{}
	requiredField := map[string]bool{}
	for _, f := range rule.Fields {
		allowedField[f.Code] = true
		if f.Required {
			requiredField[f.Code] = true
		}
	}
	refs := e.structuralRefs()
	for k, v := range refs {
		if v != "" && !allowedField[k] {
			return fmt.Errorf("invalid_structural_field: %s", k)
		}
		if v != "" {
			if _, err := uuid.Parse(v); err != nil {
				return fmt.Errorf("invalid_id")
			}
		}
	}
	for code := range requiredField {
		if refs[code] == "" {
			return fmt.Errorf("parent_required")
		}
	}
	if e.Kind != "release" && len(e.Subjects) > 0 || e.Kind != "track" && len(e.Contents) > 0 {
		return fmt.Errorf("invalid_structural_field")
	}
	// Subjects 应用层去重键为（work, role）：同一作品同一角色只允许一条，
	// position 不同也不行（position 是展示序，不是身份）。DB 主键
	// release_subjects(release_id,work_id,role) 同口径，应用层先拦。
	seenSubject := map[string]bool{}
	for _, s := range e.Subjects {
		if s.Position < 0 {
			return fmt.Errorf("invalid_position")
		}
		key := s.WorkID + "\x00" + s.Role
		if seenSubject[key] {
			return fmt.Errorf("duplicate_subject")
		}
		seenSubject[key] = true
		term, ok := d.Vocabularies["release_role"].Terms[s.Role]
		if !ok || !historical && !term.Enabled {
			return fmt.Errorf("invalid_term")
		}
		if err := reference(s.WorkID, []string{"work"}); err != nil {
			return err
		}
		// 发行对象附加属性：有匹配 scheme 时按并集收敛到场景子集，
		// 无匹配时使用全局组。
		if err := d.value(d.effectiveGroupField("subject_attributes", e.Kind), s.Attributes, reference, historical); err != nil {
			return fmt.Errorf("subject_attributes: %w", err)
		}
	}
	positions := map[int]bool{}
	// 同一 Track 允许多次引用同一 Expression（如混音轨分别引用 0-30 秒与 60-90 秒切片）；
	// 仅当 ExpressionID 与 Locator 定位切片完全相同时，才判定为无意义的重复收录报 duplicate_content。
	seenExprLoc := map[string]bool{}
	for _, c := range e.Contents {
		if len(c.Sources) > 0 {
			if err := validateSourceShapes("inclusion", c.Sources); err != nil {
				return err
			}
		}
		if c.Position < 0 || positions[c.Position] {
			return fmt.Errorf("duplicate_position")
		}
		positions[c.Position] = true
		exprLocKey := c.ExpressionID + "\x00" + encode(c.Locator)
		if seenExprLoc[exprLocKey] {
			return fmt.Errorf("duplicate_content")
		}
		seenExprLoc[exprLocKey] = true
		if err := reference(c.ExpressionID, []string{"expression"}); err != nil {
			return err
		}
		// 定位方案（页码/时间码/路径/章节…）与收录附加属性同样走 definitions，
		// 不再为每种媒体硬编码字段；locator 允许为空（如整轨收录）。
		// 有匹配 scheme 时按场景并集收敛，无匹配回退全局组；匹配场景任一
		// require_range 时 locator 至少一个 content 语义子字段非空。
		locatorField := d.effectiveGroupField("locator", e.Kind, mediumFormat...)
		if err := d.value(locatorField, map[string]any(c.Locator), reference, historical); err != nil {
			return fmt.Errorf("locator: %w", err)
		}
		if requireRangeSchemes(d.matchSchemes("locator", e.Kind, mediumFormat...)) {
			if err := checkRangeRequired(d.Fields["locator"], map[string]any(c.Locator)); err != nil {
				return fmt.Errorf("locator: %w", err)
			}
		}
		if err := d.value(d.effectiveGroupField("inclusion_attributes", e.Kind, mediumFormat...), c.Attributes, reference, historical); err != nil {
			return fmt.Errorf("inclusion_attributes: %w", err)
		}
	}
	return nil
}
