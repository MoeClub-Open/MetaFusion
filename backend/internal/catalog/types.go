// Package catalog owns metadata only. Optional modules consume its DTOs and events.
package catalog

import (
	"encoding/json"
	"time"
)

var Kinds = []string{"agent", "collection", "work", "content_unit", "expression", "release", "medium", "track"}

type Names map[string]string

// MaxArticleLength bounds one locale's Markdown body in Unicode code points.
// Article text is part of the entity document and shares its revisions and ACL.
const MaxArticleLength = 100000

type Translation struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary,omitempty"`
	Body    string   `json:"body,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}
type Source struct {
	Kind     string `json:"kind"`
	Citation string `json:"citation"`
	URL      string `json:"url,omitempty"`
	// Key 是批量提交里来源池的局部名（同一提交内唯一），供操作级 source_refs 引用。
	Key string `json:"key,omitempty"`
	// Pointers 只由服务端在应用 source_refs 时写入修订：该来源支撑的 JSON Pointer
	// 字段路径；为空表示支撑整个操作。提交来源池里不得自带。
	Pointers []string `json:"pointers,omitempty"`
	// Scope 只出现在读取的历史修订上：SourceScopeCommit 表示该来源是整笔提交的
	// 来源池（本功能之前写入、没有逐操作指针），不能证明支撑本条修订的某个字段。
	// 新提交里客户端不得自带。
	Scope string `json:"scope,omitempty"`
}

// SourceScopeCommit 标记历史修订里"适用于整笔提交"的来源（由一次性迁移写入）。
const SourceScopeCommit = "commit"

// 多图契约（一条数组顺序规则，别在消费端各自发明第二套）：
//   - **数组顺序就是展示顺序**，服务端保存时不重排；`pictures[0]` 即该实体的封面。
//     换封面 = 把它挪到首位，不是加字段标记"主图"——一个布尔位与数组顺序并存
//     迟早互相打脸（导入链、PUT 整组替换与 13 处前端消费点都会踩到）。
//   - `TakenAt` 只是这张图自身的时间元信息（拍摄/发布/改版），**不参与排序**；
//     历史上前端按它重排过，那会静默覆盖作者定的顺序，已收敛为按数组顺序展示。
//   - `Role` 说明"这张图充当什么"（主视觉/立绘/商品 jacket/剧照…），是用途标签，
//     不改变顺序语义：同一实体可以同时有 role=cover_art 与 role=key_visual 的图。
type PicturesJSON []Picture

// TimeSpan 采用 IFLA LRM E11 的区间语义：允许只知一端和部分日期。
// 这是内嵌值结构，不凭时间跨度制造第九种目录实体；需要跨事实共享身份时另立规则。
type TimeSpan struct {
	Begin string `json:"begin,omitempty"`
	End   string `json:"end,omitempty"`
}

type Picture struct {
	URL     string `json:"url"`
	Caption Names  `json:"caption"`
	// TakenAt 这张图自身的时间（拍摄/发布/改版时刻），部分书目日期或 RFC3339。
	// 只作展示元信息，**不决定顺序**：顺序契约见 PicturesJSON 的注释。
	TakenAt string `json:"taken_at,omitempty"`
	// VersionLabel 区分同一实体先后或并行使用的正确图像版本（例如年度主视觉）；
	// 它不是实体版本，也不意味着旧图错误。可空，按语言解析。
	VersionLabel Names `json:"version_label,omitempty"`
	// UsagePeriod 是图像用于该实体的已知时段（两端包含）；可与别的图片重叠。
	// nil=未记录使用期，不参与当前封面的自动挑选。
	UsagePeriod *TimeSpan `json:"usage_period,omitempty"`
	// Role 这张图在该实体的语境里充当什么（主视觉、角色立绘、商品 jacket、剧照、
	// 活动现场……）。取值是 definitions 的 picture_role 词表码，可空=未声明用途，
	// 所以存量 800+ 张没有该键的历史数据照常通过校验；新增用途码只改定义不改代码。
	Role string `json:"role,omitempty"`
	// AssetID 自托管封面在存储服务里的 assets.id（UUID），对应 binding_role=cover_image
	// 的那条绑定。可空：热链封面没有本地资产。
	// 目录侧**不跨服务校验它是否存在或是否已被 block**（无跨服务事务，见 store.go），
	// 所以写入方要先完成上传与绑定；取不到对象时前端会退化成程序封面而不是破图。
	AssetID string `json:"asset_id,omitempty"`
	Source  Source `json:"source"`
}

// Locator 描述"该内容位于载体的何处"：页码、时间码、文件路径、章节……
// 不同媒体的定位方式差异极大（书籍按页、音视频按时间、文件按路径），
// 因此键集合**不硬编码**：由 definitions 的 locator 组字段声明，后台可增删。
type Locator map[string]any

// Inclusion（收录关系）与 Subject（发行对象）除结构性的引用与次序外，
// 其余描述全部走 definitions 校验的动态 attributes，不再为每种媒体加专用字段。
type Inclusion struct {
	ExpressionID string         `json:"expression_id"`
	Position     int            `json:"position"`
	Locator      Locator        `json:"locator"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	Sources      []Source       `json:"sources,omitempty"`
}
type Subject struct {
	WorkID     string         `json:"work_id"`
	Role       string         `json:"role"`
	Position   int            `json:"position"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// ExternalLink is one authority / platform link on an entity.
// Role empty/omit = no badge; 「官方」only when role=official (never inferred on migration).
// Origin=migration marks converted external_ids rows pending real evidence.
type ExternalLink struct {
	Kind       string      `json:"kind"`
	URL        string      `json:"url"`
	ExternalID string      `json:"external_id,omitempty"`
	Role       string      `json:"role,omitempty"`   // official|primary|secondary|operator|""
	Origin     string      `json:"origin,omitempty"` // migration|commit|""
	SourceRefs []SourceRef `json:"source_refs,omitempty"`
}

type Entity struct {
	ID               string                 `json:"id"`
	Kind             string                 `json:"kind"`
	Version          int64                  `json:"version"`
	Title            string                 `json:"title"`
	OriginalLanguage string                 `json:"original_language"`
	Translations     map[string]Translation `json:"translations"`
	Attributes       map[string]any         `json:"attributes"`
	ExternalLinks    []ExternalLink         `json:"external_links"`
	Pictures         PicturesJSON           `json:"pictures"`
	State            string                 `json:"state"`
	ReviewStatus     string                 `json:"review_status"`
	CreatedBy        string                 `json:"created_by"`
	RedirectID       string                 `json:"redirect_id,omitempty"`
	WorkID           string                 `json:"work_id,omitempty"`
	ContentUnitID    string                 `json:"content_unit_id,omitempty"`
	ReleaseID        string                 `json:"release_id,omitempty"`
	MediumID         string                 `json:"medium_id,omitempty"`
	ParentID         string                 `json:"parent_id,omitempty"`
	Position         int                    `json:"position"`
	Number           string                 `json:"number"`
	Contents         []Inclusion            `json:"contents"`
	Subjects         []Subject              `json:"subjects"`
	UpdatedAt        time.Time              `json:"updated_at"`
}
type Edit struct {
	Entity          Entity   `json:"entity"`
	ExpectedVersion int64    `json:"expected_version"`
	EditNote        string   `json:"edit_note"`
	Sources         []Source `json:"sources"`
	// idempotency 是 HTTP 创建端点（POST /entities）的幂等声明：HTTP 层在 body 解码后
	// 按 Idempotency-Key 头填充（见 IdempotencyClaim），Save 在业务事务内声明/回填。
	// PUT 更新不带（键只覆盖创建）。
	idempotency       *IdempotencyClaim
	contentPatch      *trackContentPatch
	preserveContents  bool
	createID          string
	definitions       *DefinitionConfig
	identityLocksHeld bool
}
type Relation struct {
	ID         string         `json:"id"`
	Version    int64          `json:"version"`
	Type       string         `json:"type"`
	SourceID   string         `json:"source_id"`
	TargetID   string         `json:"target_id"`
	Position   int            `json:"position"`
	Attributes map[string]any `json:"attributes"`
}
type RelationEdit struct {
	Relation        Relation `json:"relation"`
	ExpectedVersion int64    `json:"expected_version"`
	EditNote        string   `json:"edit_note"`
	Sources         []Source `json:"sources"`
	// idempotency 是 HTTP 创建端点（POST /relations）的幂等声明，用法同 Edit。
	idempotency *IdempotencyClaim
	createID    string
	definitions *DefinitionConfig
}
type Field struct {
	Names Names  `json:"names"`
	Type  string `json:"type"`
	// ApplicableKinds 决定实体属性可以出现在哪些 kind。
	// 空集合表示只供关系或内嵌结构使用，不可直接写入实体 attributes。
	ApplicableKinds []string         `json:"applicable_kinds,omitempty"`
	Unit            Names            `json:"unit"`
	Required        bool             `json:"required"`
	Enabled         bool             `json:"enabled"`
	Searchable      bool             `json:"searchable"`
	Comparable      bool             `json:"comparable"`
	Vocabulary      string           `json:"vocabulary,omitempty"`
	AllowYearless   bool             `json:"allow_yearless,omitempty"`
	Kinds           []string         `json:"kinds,omitempty"`
	Fields          map[string]Field `json:"fields,omitempty"`
	Items           *Field           `json:"items,omitempty"`
	Min             *float64         `json:"min,omitempty"`
	Max             *float64         `json:"max,omitempty"`
	// AnchorKey 仅用于 group 字段：组内任一其它子字段有值时，该锚点子字段必须同时有值。
	// 例：定位组声明 anchor=relative_to，避免出现"有页码却不知相对谁"的悬空定位。
	AnchorKey string `json:"anchor_key,omitempty"`
	// RangeStart 仅用于 group 内的 number 子字段：声明"本字段是同组 <RangeStart>
	// 子字段的区间终点"，只有显式声明的区间才校验大小关系。
	// 取代按字段名猜测配对（start/end、begin/end、_max…）的隐式约定：管理员新增
	// 两个数字字段时，不会在没有配置的情况下触发隐含规则。
	RangeStart string `json:"range_start,omitempty"`
	// Hidden 表示该字段可写、可检索，但不进详情信息面板（存档/机器用途），
	// 例如资料表原始条目与标签——它们由页面上的专用区块呈现，避免原文 JSON 直出。
	Hidden bool `json:"hidden,omitempty"`
	// Semantics 声明子字段在"对比"中的语义，取值受限于系统支持的规则集合（闭集）：
	//   "content" 描述"实际引用的内容片段范围"（如截取的页段/时间段），参与内容身份对齐；
	//   空值（默认）表示"本版定位"（页码、时间码、文件路径、EPUB 锚点），只反映排版与载体差异。
	// 对比规则据此判定，不按字段名或区间长度猜测内容是否变化。
	Semantics string `json:"semantics,omitempty"`
}
type Term struct {
	Names   Names `json:"names"`
	Enabled bool  `json:"enabled"`
	// IsBonus 只影响发行详情的附赠内容分组；可在定义后台调整，不绑定词条码。
	IsBonus *bool `json:"is_bonus,omitempty"`
}
type Vocabulary struct {
	Names Names           `json:"names"`
	Terms map[string]Term `json:"terms"`
}
type RelationDefinition struct {
	Names        Names    `json:"names"`
	ReverseNames Names    `json:"reverse_names"`
	SourceKinds  []string `json:"source_kinds"`
	TargetKinds  []string `json:"target_kinds"`
	Fields       []string `json:"fields"`
	Symmetric    bool     `json:"symmetric"`
	Acyclic      bool     `json:"acyclic"`
	MaxOutgoing  int      `json:"max_outgoing"`
	MaxIncoming  int      `json:"max_incoming"`
	// Scope compares fixed ownership, never titles or arbitrary attributes.
	Scope          string `json:"scope,omitempty"`
	CycleGroup     string `json:"cycle_group,omitempty"`
	UniquePosition bool   `json:"unique_position,omitempty"`
	// Usage selects a supported projection; relation codes and names remain editable.
	Usage string `json:"usage,omitempty"`
	// ReferenceScopes maps entity fields to source_work / target_release etc.
	ReferenceScopes map[string]string `json:"reference_scopes,omitempty"`
	// Aggregate 声明这条关系表达"组成/聚合"（集合→作品、专辑→曲目等）。
	// 客户端据此区分"结构聚合"与"内容关系"，从而不必写死关系码：新增聚合类关系时
	// 只要在定义里声明它，页面会自动把它算进组成列表。
	Aggregate bool `json:"aggregate,omitempty"`
	// ParticipantSlot 声明这条关系的对端在署名里扮演什么：person（对端是署名主体，
	// 人或机构）、character（对端是虚构角色）、peer（对端是同层级对象，不产生署名主体）。
	// 为什么需要它：29 个关系码共用一份 fields 时，"这条关系是否带角色"对每条关系都成立，
	// 客户端只能拿分组码当语义用。客户端据此判定"算不算演职、图标取哪个"，不写死关系码与组码。
	ParticipantSlot string `json:"participant_slot,omitempty"`
	// CountsAsCredit 声明这条关系参与批量署名聚合（Release 详情的署名列表）。
	// 为什么不用分组码判定：分组是展示归类，后台把某条关系挪出 credits 组是改展示，
	// 不该静默改变"哪些关系算署名"这一行为口径。
	CountsAsCredit bool   `json:"counts_as_credit,omitempty"`
	Group          string `json:"group"`
	GroupNames     Names  `json:"group_names,omitempty"`
	Enabled        bool   `json:"enabled"`
}

type Section struct {
	Names  Names    `json:"names"`
	Fields []string `json:"fields"`
}
type Template struct {
	Names          Names     `json:"names"`
	Sections       []Section `json:"sections"`
	Columns        []string  `json:"columns"`
	RelationGroups []string  `json:"relation_groups"`
	Directory      string    `json:"directory"`
	// PrimaryDateField 指定该模板下代表"作品首发/发行日期"的字段码，
	// 供列表与排序使用。取代代码里硬编码 edition_date 的语义；为空则不展示日期。
	PrimaryDateField string `json:"primary_date_field,omitempty"`
	// BadgeFields 指定在详情页标题旁以徽章形式突出的字段码（如载体格式、平台）。
	// 取代代码里硬编码 format/platform 的做法；顺序即展示顺序。
	BadgeFields []string `json:"badge_fields,omitempty"`
	// FacetFields 指定列表页可用于筛选的字段码（通常是枚举字段）。
	// 取代代码里硬编码 edition_type/format/country 三个下拉；顺序即展示顺序。
	FacetFields []string `json:"facet_fields,omitempty"`
	// Kinds 声明该模板适用的实体 kind 白名单（如 work / release / agent），
	// 前端据此过滤"新建实体时可选哪些模板"。为空表示不限 kind。
	Kinds []string `json:"kinds,omitempty"`
	// Match is an AND of finite predicates. nil disables automatic selection;
	// an explicit empty array is a kind-wide fallback. Equal priorities are ambiguous.
	Match    *[]TemplateCondition `json:"match,omitempty"`
	Priority int                  `json:"priority,omitempty"`
	// nil keeps the kind layout; [] hides optional blocks.
	Blocks *[]string `json:"blocks,omitempty"`
}

type TemplateCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}

// Scheme 是"按使用场景配置"的有限声明式规则：locator / inclusion_attributes /
// subject_attributes 是全局结构，纸书要页码、EPUB 要路径锚点、黑胶要唱片面，
// 必填、排序、范围约束与展示收敛都由它声明，不新增核心实体种类。
//   - Slot 闭集三选一：locator / inclusion_attributes / subject_attributes；
//   - Kinds 拥有者 kind 白名单，空=不限；
//   - MediumFormats 限定 Track 所属 Medium 的格式词条，空=不限；
//   - Fields 该上下文可用子字段码（必须已在全局组声明），顺序即展示编辑顺序；
//   - Required ⊆ Fields；RequireRange 仅 locator 有意义，要求至少一个
//     semantics=content 的子字段有值；Enabled 关闭即不参与匹配，可被后台删除。
type Scheme struct {
	Names         Names     `json:"names"`
	Slot          string    `json:"slot"`
	Kinds         []string  `json:"kinds,omitempty"`
	MediumFormats *[]string `json:"medium_formats,omitempty"`
	Fields        []string  `json:"fields"`
	Required      []string  `json:"required,omitempty"`
	RequireRange  bool      `json:"require_range,omitempty"`
	Enabled       bool      `json:"enabled"`
}
type Definitions struct {
	Fields       map[string]Field              `json:"fields"`
	Vocabularies map[string]Vocabulary         `json:"vocabularies"`
	Relations    map[string]RelationDefinition `json:"relations"`
	Templates    map[string]Template           `json:"templates"`
	Schemes      map[string]Scheme             `json:"schemes"`
	// Structure 声明每个层级的"所属与收录结构"：有哪些结构字段、指向哪些层级、
	// 是否必填、是否按上级字段过滤候选。写在校验与编辑器共用这里。
	Structure map[string]StructureRule `json:"structure"`
}

// StructureRule 是一个层级的结构归属规则。
type StructureRule struct {
	Fields []StructureField `json:"fields"`
	// Resources 表示该层级可以挂资源文件（存储服务里的资产）。
	Resources bool `json:"resources,omitempty"`
	// Subjects 表示该层级有"发行对象"（收录主体）列表：发行版用它声明收录了哪些作品。
	Subjects            bool  `json:"subjects,omitempty"`
	SubjectNames        Names `json:"subject_names,omitempty"`
	SubjectReverseNames Names `json:"subject_reverse_names,omitempty"`
	// Contents 表示该层级有"收录内容"列表：收录位置用它引用内容表达。
	Contents            bool  `json:"contents,omitempty"`
	ContentNames        Names `json:"content_names,omitempty"`
	ContentReverseNames Names `json:"content_reverse_names,omitempty"`
}

// StructureField 是一个结构字段：字段码 + 允许作为目标的层级。
type StructureField struct {
	Code         string `json:"code"`
	Names        Names  `json:"names"`
	ReverseNames Names  `json:"reverse_names"`
	// TargetKinds 为空表示与当前层级同层（同域父节点）。
	TargetKinds []string `json:"target_kinds,omitempty"`
	// ScopedBy 指定候选按哪个结构字段过滤（如 expression 的内容单元按 work_id 过滤）。
	ScopedBy string `json:"scoped_by,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// DefinitionConfig is the only live definition document. ETag prevents stale
// editor tabs from overwriting each other; it cannot resolve an older document.
type DefinitionConfig struct {
	ETag      string      `json:"etag"`
	Document  Definitions `json:"document"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// RateLimitRule 是某个主体（账号或用户组）的限流规则。两个字段的语义刻意分开：
//   - Unlimited 为真：该主体不受限流（"解除限制"），用于受信账号与内部管道；
//   - PerMinute > 0：覆盖下一级额度；
//   - 两者都未声明：继承下一级。
//
// 0 不表示"禁止"：把未配置与熔断混成同一个值，一次误填就会把账号锁死，
// 而运维看到的现象与"没生效"完全一样。
type RateLimitRule struct {
	PerMinute int  `json:"per_minute,omitempty"`
	Unlimited bool `json:"unlimited,omitempty"`
}

// RateLimitPolicy 是唯一的进程外部限流配置文档（catalog.rate_limit_policy）。
//
// 解析优先级：账号 > 用户组 > 全局默认 > 路由内置额度。之所以账号压过组：
// 账号级覆盖是运维对单个账号的即时处置，必须压过批量预设；组是"授予"的集合，
// 多组命中取最宽松的一项（取交集会让多加入一个组反而变慢）。
//
// Groups 里保留 "anonymous" 这个保留组码，用于单独收紧/放宽未登录流量
// （匿名请求主体是其 IP，没有组）。
type RateLimitPolicy struct {
	DefaultPerMinute int                      `json:"default_per_minute,omitempty"`
	DefaultUnlimited bool                     `json:"default_unlimited,omitempty"`
	Groups           map[string]RateLimitRule `json:"groups,omitempty"`
	Accounts         map[string]RateLimitRule `json:"accounts,omitempty"`
}

// RateLimitConfig 是读取/保存限流的唯一响应形状：etag 是**覆盖保护**，不是版本号
// （与 DefinitionConfig 同一口径）。
type RateLimitConfig struct {
	ETag      string          `json:"etag"`
	Policy    RateLimitPolicy `json:"policy"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// RateLimitSave 是限流保存的请求体，字段名与 DefinitionSave 同构。
type RateLimitSave struct {
	Policy       RateLimitPolicy `json:"policy"`
	ExpectedETag string          `json:"expected_etag"`
	EditNote     string          `json:"edit_note"`
	Sources      []Source        `json:"sources"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	// 授权只看权限码。Groups 供展示与审计，不参与判定。
	Groups      []string `json:"groups,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	// FromPAT 标记身份来自 PAT 内省，用于审计，不进 JSON 输出。
	FromPAT bool `json:"-"`
	// Supplied only by account-service introspection; never accepted from edits.
	CatalogReviewExempt bool `json:"-"`
	// IsThirdParty 标记身份来自第三方 OAuth 授权（token_use=oauth/id_token）。
	// 管理 API 默认拒绝此类身份（见 permission.go），
	// 因此不进 JSON 输出、不暴露给调用方。
	IsThirdParty bool `json:"-"`
	// TokenName 是 PAT 令牌名（调用日志 credential_name）。会话身份恒为空；
	// 会话身份没有令牌名，因此不输出空键。
	TokenName string `json:"token_name,omitempty"`
}
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	EntityID  string          `json:"entity_id"`
	Version   int64           `json:"version"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
