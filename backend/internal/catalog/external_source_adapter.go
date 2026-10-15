package catalog

// 外部来源只读查询适配器（Bangumi / DLsite / DMM）。
//
// 仅出站抓取与映射为预览结构，不写目录、不自动合并。统一入口见
// POST /api/external-sources/query（search / lookup / detail）与 GET /api/external-sources。
// 图片只做远端 URL 引用，不抓取、不转存。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// bangumiAPIBase 允许单测替换为 httptest 服务；生产默认指向 Bangumi 公开 API。
	bangumiAPIBase           = "https://api.bgm.tv"
	externalSourceHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

const externalSourceUserAgent = "MetaFusion/1.0 (external-source query)"

// ---- 只读预览 DTO（lookup/detail 的 preview 字段沿用此形状） ----

type ExternalSourceTranslationItem struct {
	Locale  string   `json:"locale"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Aliases []string `json:"aliases,omitempty"`
}

type ExternalSourceWorkPreview struct {
	Title            string                          `json:"title"`
	OriginalTitle    string                          `json:"original_title"`
	Aliases          []string                        `json:"aliases"`
	ReleaseDate      string                          `json:"release_date"`
	BeginDate        string                          `json:"begin_date,omitempty"`
	Country          string                          `json:"country"`
	Language         string                          `json:"language"`
	OriginalLanguage string                          `json:"original_language,omitempty"`
	Summary          string                          `json:"summary"`
	CoverImageURL    string                          `json:"cover_image_url"`
	CoverAspect      string                          `json:"cover_aspect"`
	ContentRating    string                          `json:"content_rating"`
	Tags             []string                        `json:"tags"`
	Translations     []ExternalSourceTranslationItem `json:"translations"`
	CatalogMetadata  any                             `json:"catalog_metadata,omitempty"`
	// Fields 是按 infobox 映射出的动态字段值（键为已声明的字段码）；
	// Infobox 是上游资料表的完整原文快照，用于追溯与后续补充映射。
	Fields  map[string]any   `json:"fields,omitempty"`
	Infobox []map[string]any `json:"infobox,omitempty"`
}

type ExternalSourceArtistPreview struct {
	ID             string                          `json:"id,omitempty"`
	Name           string                          `json:"name"`
	OriginalName   string                          `json:"original_name,omitempty"`
	Role           string                          `json:"role"`
	EntityType     string                          `json:"entity_type"`
	Country        string                          `json:"country,omitempty"`
	Biography      string                          `json:"biography,omitempty"`
	Disambiguation string                          `json:"disambiguation,omitempty"`
	Language       string                          `json:"language,omitempty"`
	AvatarURL      string                          `json:"avatar_url,omitempty"`
	CharacterName  string                          `json:"character_name,omitempty"`
	Aliases        []string                        `json:"aliases,omitempty"`
	ExternalLinks  []map[string]any                `json:"external_links,omitempty"`
	Translations   []ExternalSourceTranslationItem `json:"translations,omitempty"`
	// RelationType 是该关联应落到 definitions 的关系码（如 directed_by / voiced_by / character_in）；
	// RelationRole 是角色番位的 character_rank 词表项（main/supporting/guest/ensemble/narrator/cameo，
	// 落 character_in 的 attributes.character_rank）。
	RelationType string `json:"relation_type,omitempty"`
	RelationRole string `json:"relation_role,omitempty"`
}

type ExternalSourceTrackPreview struct {
	Position        int     `json:"position"`
	Title           string  `json:"title"`
	DurationSeconds float64 `json:"duration_seconds"`
	ArtistCredit    string  `json:"artist_credit,omitempty"`
	ISRC            string  `json:"isrc,omitempty"`
	RecordingMBID   string  `json:"recording_mbid,omitempty"`
}

// ExternalSourceMediumPreview 是只读载体预览，翻译与属性仅作为编目参考。
type ExternalSourceMediumPreview struct {
	Position         int                          `json:"position"`
	Number           string                       `json:"number,omitempty"`
	Name             string                       `json:"name"`
	Format           string                       `json:"format"`
	Role             string                       `json:"role,omitempty"`
	OriginalLanguage string                       `json:"original_language,omitempty"`
	Translations     any                          `json:"translations,omitempty"`
	Tracks           []ExternalSourceTrackPreview `json:"tracks"`
}

// ExternalSourceReleasePreview 是只读发行版预览，不执行目录写入或幂等合并。
type ExternalSourceReleasePreview struct {
	CoverImageURL       string `json:"cover_image_url,omitempty"`
	CoverAspect         string `json:"cover_aspect,omitempty"`
	OriginalLanguage    string `json:"original_language,omitempty"`
	Translations        any    `json:"translations,omitempty"`
	EditionName         string `json:"edition_name"`
	CatalogNumber       string `json:"catalog_number,omitempty"`
	Barcode             string `json:"barcode,omitempty"`
	Publisher           string `json:"publisher,omitempty"`
	Packaging           string `json:"packaging,omitempty"`
	Country             string `json:"country,omitempty"`
	Language            string `json:"language,omitempty"`
	DistributionChannel string `json:"distribution_channel,omitempty"`
	EditionType         string `json:"edition_type,omitempty"`
	EditionBatch        string `json:"edition_batch,omitempty"`
	EditionDate         string `json:"edition_date,omitempty"`
	Notes               string `json:"notes,omitempty"`
	CatalogMetadata     any    `json:"catalog_metadata,omitempty"`
}

type ExternalSourceCanonicalEntryPreview struct {
	Title            string           `json:"title"`
	Translations     any              `json:"translations,omitempty"`
	Position         int              `json:"position"`
	Number           string           `json:"number,omitempty"`
	EntryRole        string           `json:"entry_role,omitempty"`
	OriginalLanguage string           `json:"original_language,omitempty"`
	DurationSeconds  float64          `json:"duration_seconds,omitempty"`
	Attributes       map[string]any   `json:"attributes,omitempty"`
	ExternalLinks    []map[string]any `json:"external_links,omitempty"`
	// EntryKind 条目落库层级："content_unit"（篇目/分集，可带下级）或
	// "expression"（默认，录音/正文）。由来源结构决定，不由标题猜测。
	EntryKind string `json:"entry_kind,omitempty"`
	// ParentIndex 指向同一 canonical_entries 数组内父级条目的下标（-1/省略为顶层），
	// 用于表达章节树；仅在同 Work 内成立。
	ParentIndex *int `json:"parent_index,omitempty"`
}

type ExternalSourcePreviewResponse struct {
	Source           string                                `json:"source"`
	EntityType       string                                `json:"entity_type,omitempty"`
	ExternalID       string                                `json:"external_id"`
	ExternalURL      string                                `json:"external_url"`
	MediaType        string                                `json:"media_type"`
	Work             *ExternalSourceWorkPreview            `json:"work,omitempty"`
	Artist           *ExternalSourceArtistPreview          `json:"artist,omitempty"`
	Artists          []ExternalSourceArtistPreview         `json:"artists,omitempty"`
	HasRelease       bool                                  `json:"has_release,omitempty"`
	CanonicalEntries []ExternalSourceCanonicalEntryPreview `json:"canonical_entries,omitempty"`
	Release          *ExternalSourceReleasePreview         `json:"release,omitempty"`
	Mediums          []ExternalSourceMediumPreview         `json:"mediums,omitempty"`
	Tags             []string                              `json:"tags"`
	// Warnings 记录来源抓取不完整等需要人工留意的情况（如分集总数与实取不符），
	// 前端据此提示，避免把不完整预览当成完整清单落库。
	Warnings []string `json:"warnings,omitempty"`
}

// ---- 参数归一化 ----

func normalizeExternalSourceDefinition(source string) (string, error) {
	src := strings.ToLower(strings.TrimSpace(source))
	if src == "" {
		src = "auto"
	}
	// auto 已由调用方按 URL/ID 探测；无法判定时使用默认适配器。
	if src == "auto" {
		return "bangumi", nil
	}
	if externalSourceAdapterSet()[src] {
		return src, nil
	}
	return "", fmt.Errorf("not_supported")
}

// detectExternalSourceDefinition 按 URL/ID 字面形态判定 auto 下的具体来源；
// 无法判定时回落到 bangumi（默认适配器）。纯本地解析，不发网络请求。
func detectExternalSourceDefinition(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "bangumi"
	}
	lower := strings.ToLower(s)
	// 含 dlsite 域名，或裸 DLsite 商品号（RJ/RE/BJ/VJ + 数字）。
	if strings.Contains(lower, "dlsite.com") || dlsiteProductIDPattern.MatchString(strings.ToUpper(strings.TrimSpace(s))) {
		return "dlsite"
	}
	// 含 dmm 域名（含 dlsoft 子域）。
	if strings.Contains(lower, "dmm.co.jp") || strings.Contains(lower, "dmm.com") {
		return "dmm"
	}
	return "bangumi"
}

func normalizeExternalSourceEntityType(entityType string) (string, error) {
	et := strings.ToLower(strings.TrimSpace(entityType))
	if et == "" {
		et = "work"
	}
	if !contains([]string{"work", "artist", "organization", "character"}, et) {
		return "", fmt.Errorf("invalid_entity_type")
	}
	return et, nil
}

// ---- Bangumi ID 解析 ----

type bangumiRef struct {
	Kind string // subject | person | character
	ID   int
}

var bangumiKindSegments = map[string]string{
	"subject": "subject", "subjects": "subject",
	"person": "person", "persons": "person", "mono": "person",
	"character": "character", "characters": "character", "crt": "character",
}

func parseBangumiRef(raw string) (bangumiRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return bangumiRef{}, fmt.Errorf("invalid_payload")
	}
	if id, err := strconv.Atoi(s); err == nil && id > 0 {
		return bangumiRef{Kind: "subject", ID: id}, nil
	}
	normalized := s
	if !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" {
		return bangumiRef{}, fmt.Errorf("invalid_payload")
	}
	if !bangumiHostAllowed(u.Host) {
		return bangumiRef{}, fmt.Errorf("not_supported")
	}
	kind := ""
	id := 0
	expectID := false
	for _, seg := range strings.Split(u.Path, "/") {
		seg = strings.ToLower(strings.TrimSpace(seg))
		if seg == "" {
			continue
		}
		if k, ok := bangumiKindSegments[seg]; ok {
			kind = k
			// 只取种类段之后的第一段数字：/subject/7/ep/3 这类详情子路径
			// 的末段数字（ep 编号）不是条目 ID，必须忽略。
			expectID = id == 0
			continue
		}
		if n, err := strconv.Atoi(seg); err == nil && n > 0 {
			if expectID || id == 0 && kind == "" {
				id = n
				expectID = false
			}
		}
	}
	if id <= 0 {
		return bangumiRef{}, fmt.Errorf("invalid_payload")
	}
	if kind == "" {
		kind = "subject"
	}
	return bangumiRef{Kind: kind, ID: id}, nil
}

// bangumiHostAllowed 判定来源主机是否属于 Bangumi 官方域名（bgm.tv / bangumi.tv / chii.in
// 及其子域）。出站请求始终是常量 base + 数字 ID 的路径，这里只决定"是否接受这个引用"；
// 用后缀匹配而不是 strings.Contains：后者会把 notbgm.tv 这类仿冒域名当成合法来源。
func bangumiHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // 去掉端口
	}
	for _, domain := range []string{"bgm.tv", "bangumi.tv", "chii.in"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// resolveBangumiRef 在纯数字 ID 缺少路径种类的情况下，按实体类型改写目标端点。
func resolveBangumiRef(raw, entityType string) (bangumiRef, error) {
	ref, err := parseBangumiRef(raw)
	if err != nil {
		return ref, err
	}
	if _, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		switch entityType {
		case "artist", "organization":
			ref.Kind = "person"
		case "character":
			ref.Kind = "character"
		}
	}
	return ref, nil
}

// ---- Bangumi 公开 API ----

type bangumiImages struct {
	Large  string `json:"large"`
	Common string `json:"common"`
	Medium string `json:"medium"`
	Small  string `json:"small"`
	Grid   string `json:"grid"`
}

func (im bangumiImages) best() string {
	for _, u := range []string{im.Large, im.Common, im.Medium, im.Small, im.Grid} {
		if strings.TrimSpace(u) != "" {
			return u
		}
	}
	return ""
}

type bangumiTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type bangumiSubject struct {
	ID       int               `json:"id"`
	Type     int               `json:"type"`
	Name     string            `json:"name"`
	NameCN   string            `json:"name_cn"`
	Summary  string            `json:"summary"`
	Date     string            `json:"date"`
	Platform string            `json:"platform"`
	Images   bangumiImages     `json:"images"`
	Tags     []bangumiTag      `json:"tags"`
	Infobox  []bangumiInfoItem `json:"infobox"`
}

// bangumiInfoItem 是 Bangumi infobox 的一行。value 既可能是标量字符串，
// 也可能是 [{"v":"..."}] 形式的别名/版本列表（键名 k/v 或仅 v）。
type bangumiInfoItem struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// bangumiEpisode 是 /v0/episodes 的一条（分集/篇目）。
type bangumiEpisode struct {
	ID       int     `json:"id"`
	Type     int     `json:"type"`
	Name     string  `json:"name"`
	NameCN   string  `json:"name_cn"`
	Sort     float64 `json:"sort"`
	Ep       float64 `json:"ep"`
	Airdate  string  `json:"airdate"`
	Duration string  `json:"duration"`
}

// bangumiEpisodesResponse 是 /v0/episodes 的响应体（分页）。
type bangumiEpisodesResponse struct {
	Data  []bangumiEpisode `json:"data"`
	Total int              `json:"total"`
}

// infoboxStrings 把某键的值统一摊平为字符串列表；缺失返回空。
func (s bangumiSubject) infoboxStrings(key string) []string {
	for _, item := range s.Infobox {
		if strings.TrimSpace(item.Key) != key {
			continue
		}
		var single string
		if err := json.Unmarshal(item.Value, &single); err == nil {
			if v := strings.TrimSpace(single); v != "" {
				return []string{v}
			}
			return nil
		}
		var list []map[string]any
		if err := json.Unmarshal(item.Value, &list); err != nil {
			return nil
		}
		out := []string{}
		for _, m := range list {
			for _, k := range []string{"v", "value"} {
				if v, ok := m[k].(string); ok {
					if v = strings.TrimSpace(v); v != "" {
						out = append(out, v)
						break
					}
				}
			}
		}
		return out
	}
	return nil
}

// infoboxString 取某键的首个字符串值。
func (s bangumiSubject) infoboxString(key string) string {
	if v := s.infoboxStrings(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// bangumiInfoboxAliases 取"别名"行并去掉与主标题重复的项。
func (s bangumiSubject) bangumiInfoboxAliases(title, original string) []string {
	out := []string{}
	for _, v := range s.infoboxStrings("别名") {
		if v == title || v == original {
			continue
		}
		out = append(out, v)
	}
	return out
}

// infoboxFieldKeys 把 infobox 的（多语言）键名映射到已声明的动态字段码。
// 一个字段码可有多个上游键名；取第一个有值的。全部字段码都在 defaults.go 中声明，
// 因此不会写入未声明属性被校验拒绝。kind 决定取值归一化方式：上游数字/日期是
// 自由文本（如「13」「24(22+2)卷完结」「2023年6月29日」），直接落库会被
// invalid_number / invalid_date 拒绝。
//
// 只映射**创作身份**字段：品番/条码/ISBN/出版社是具体产品标识，defaults 已把它们
// 移出 Work 字段集（归 Release/Medium），此处也不再映射到作品——它们仍完整保留在
// `infobox` 原文快照中可追溯。上游资料表以作品为主表、发行细节常缺失，把出版社
// 硬塞到作品层就会出现"某一发行的事实被填到作品上"。
var infoboxFieldKeys = []struct {
	field string
	kind  string
	keys  []string
}{
	{"episodes", "number", []string{"话数", "集数", "話數"}},
	{"volume_count", "number", []string{"册数", "卷数", "冊數", "巻数"}},
	{"broadcast_start", "date", []string{"放送开始", "放送開始", "开始"}},
	{"broadcast_weekday", "text", []string{"放送星期", "放送日"}},
	{"broadcast_end", "date", []string{"放送结束", "放送結束", "播放结束"}},
	{"air_network", "text", []string{"播放电视台", "放送局", "播放电视台"}},
	{"copyright", "text", []string{"Copyright", "©"}},
	// ISBN 是产品标识，落在发行层的 barcode（书本的条码即 ISBN）。
	{"barcode", "isbn", []string{"ISBN"}},
	{"author", "text", []string{"作者"}},
	{"magazine", "text", []string{"连载杂志", "連載雜誌"}},
	{"imdb", "text", []string{"IMDb", "IMDB", "imdb"}},
	{"platform", "text", []string{"平台"}},
}

var (
	// 数字字段：取首个整数片段，「24(22+2)卷完结」→24。
	infoboxIntRE = regexp.MustCompile(`\d+`)
	// 日期字段：支持 2023-06-29 / 2023/6/29 / 2023年6月29日，可只到月或年。
	infoboxDateRE = regexp.MustCompile(`(\d{4})\s*[-/年.]?\s*(\d{1,2})?\s*[-/月.]?\s*(\d{1,2})?`)
)

// normalizeInfoboxValue 把上游原文归一化为字段类型可接受的值；无法解析时
// 返回 nil（丢弃该字段，但原文仍在 infobox 全量快照中可追溯）。
func normalizeInfoboxValue(raw, kind string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	switch kind {
	case "isbn":
		if barcode, ok := externalSourceISBNBarcode(raw); ok {
			return barcode
		}
		return nil
	case "number":
		if m := infoboxIntRE.FindString(raw); m != "" {
			if n, err := strconv.Atoi(m); err == nil {
				return n
			}
		}
		return nil
	case "date":
		m := infoboxDateRE.FindStringSubmatch(raw)
		if m == nil {
			return nil
		}
		year := m[1]
		if m[2] == "" {
			return year
		}
		month := m[2]
		if len(month) == 1 {
			month = "0" + month
		}
		if m[3] == "" {
			return year + "-" + month
		}
		day := m[3]
		if len(day) == 1 {
			day = "0" + day
		}
		return year + "-" + month + "-" + day
	}
	return raw
}

// infoboxEntries 把上游 infobox 完整摊平为键值原文列表（保序、去空）。
// 这是"全量落库"的载体：即便某键尚未映射为一等字段，原文也留在实体上可追溯。
func (s bangumiSubject) infoboxEntries() []map[string]any {
	out := []map[string]any{}
	for _, item := range s.Infobox {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		vals := s.infoboxStrings(key)
		// 单值行也可能是标量字符串，infoboxStrings 已统一处理；逐行拆成
		// 独立的 key/value 对，保持原始顺序与重复行。
		for _, v := range vals {
			if v = strings.TrimSpace(v); v == "" {
				continue
			}
			out = append(out, map[string]any{"key": key, "value": v})
		}
	}
	return out
}

// infoboxValues 收集映射表中所有字段的值，只返回有值的键（已按字段类型归一化）。
func (s bangumiSubject) infoboxValues() map[string]any {
	out := map[string]any{}
	for _, m := range infoboxFieldKeys {
		for _, k := range m.keys {
			if v := s.infoboxString(k); v != "" {
				if nv := normalizeInfoboxValue(v, m.kind); nv != nil {
					out[m.field] = nv
				}
				break
			}
		}
	}
	return out
}

// persons 端点 type 字段存在 int 与 {id} 两种形态，两种都接受。
type bangumiPersonType struct {
	ID int
}

func (t *bangumiPersonType) UnmarshalJSON(b []byte) error {
	var id int
	if err := json.Unmarshal(b, &id); err == nil {
		t.ID = id
		return nil
	}
	var obj struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	t.ID = obj.ID
	return nil
}

type bangumiPerson struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	NameCN  string            `json:"name_cn"`
	Type    bangumiPersonType `json:"type"`
	Career  []string          `json:"career"`
	Summary string            `json:"summary"`
	Images  bangumiImages     `json:"images"`
}

type bangumiCharacter struct {
	ID      int           `json:"id"`
	Name    string        `json:"name"`
	NameCN  string        `json:"name_cn"`
	Gender  *string       `json:"gender"`
	Summary string        `json:"summary"`
	Images  bangumiImages `json:"images"`
}

// /v0/subjects/{id}/persons：条目关联的演职人员（含公司/团体，type 2/3）。
type bangumiSubjectPerson struct {
	ID       int           `json:"id"`
	Name     string        `json:"name"`
	Type     int           `json:"type"`
	Relation string        `json:"relation"`
	Career   []string      `json:"career"`
	Images   bangumiImages `json:"images"`
}

// /v0/subjects/{id}/characters：条目关联角色，actors 为其声优（可多个）。
type bangumiSubjectCharacter struct {
	ID       int                   `json:"id"`
	Name     string                `json:"name"`
	Type     int                   `json:"type"`
	Relation string                `json:"relation"`
	Summary  string                `json:"summary"`
	Images   bangumiImages         `json:"images"`
	Actors   []bangumiSubjectActor `json:"actors"`
}

type bangumiSubjectActor struct {
	ID      int           `json:"id"`
	Name    string        `json:"name"`
	Type    int           `json:"type"`
	Career  []string      `json:"career"`
	Images  bangumiImages `json:"images"`
	Locked  bool          `json:"locked"`
	Summary string        `json:"short_summary"`
}

// bangumiCreditRelation 把 Bangumi 的 relation 中文职位文本映射到 definitions 关系码。
// 返回空表示没有贴切的既有关系码：调用方仍建 agent 实体并把原始职位写进
// credit_role，而不是硬塞一个语义不符的关系码（不虚构）。
//
// 作曲与编曲是不同关系码：作曲→composed_by，编曲（含日文アレンジ）→arranged_by。
// 两者在同一原子里的先后顺序敏感——"編曲"含"曲"但绝不能落到 composed_by。
func bangumiCreditRelation(relation string) string {
	r := strings.TrimSpace(relation)
	switch {
	// 摄影/作画等复合职位要先于"监督/导演"判断，否则"摄影监督"会被
	// 更宽的"监督"规则抢先命中成 directed_by。
	case containsAny(r, "摄影", "攝影"):
		return "photographed_by"
	case containsAny(r, "作画", "作畫", "人物原案", "人物设定", "角色设计", "キャラクターデザイン", "插画", "插畫"):
		return "illustrated_by"
	case containsAny(r, "导演", "監督", "监督", "演出"):
		return "directed_by"
	case containsAny(r, "脚本", "编剧", "系列构成", "劇本"):
		return "written_by"
	case containsAny(r, "作词", "作詞"):
		return "lyricist_of"
	case containsAny(r, "编曲", "編曲", "アレンジ"):
		return "arranged_by"
	case containsAny(r, "作曲"):
		return "composed_by"
	case containsAny(r, "旁白", "ナレーション", "朗读", "朗読"):
		return "narrated_by"
	case containsAny(r, "配音", "声优", "声優", "CV"):
		return "voiced_by"
	}
	return ""
}

// bangumiAvatarURL 从 images 里取最佳头像（large→common→medium→grid→small）。
func bangumiAvatarURL(im bangumiImages) string {
	return im.best()
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// bangumiPersonTypeAgent 把 /subjects/{id}/persons 的 type 映射到 agent 类型。
// 1=个人 2=公司 3=组合；其余保守按个人。
func bangumiPersonTypeAgent(t int) string {
	switch t {
	case 2:
		return "organization"
	case 3:
		return "group"
	default:
		return "person"
	}
}

// bangumiCharacterRankRole 把角色 relation 映射到 **character_rank** 词表项
// （definitions 声明的角色番位字段，见 defaults.go：main/supporting/guest/ensemble/narrator/cameo）。
// 曾误映射到 role（载体用途/收录内容词表），写出的值虽能通过校验，但按 model 的
// character_rank 检索恒空——番位既不可查也不能多语言。未命中词表时返回空：原始文本
// 仍由 credit_role 承载，不虚构番位。
func bangumiCharacterRankRole(relation string) string {
	r := strings.TrimSpace(relation)
	switch {
	case containsAny(r, "主角", "主人公"):
		return "main"
	case containsAny(r, "配角", "配角", "副角"):
		return "supporting"
	case containsAny(r, "客串"):
		return "guest"
	case containsAny(r, "闲角", "閑角", "路人"):
		return "ensemble"
	case containsAny(r, "旁白"):
		return "narrator"
	}
	return ""
}

// bangumiOrgSelfDescription 匹配"条目自述为公司/企业"的高精度模式。
// 经 116 条真实数据校准：零误判（不会把"某某所属"的声优误判为公司），
// 用于纠正 Bangumi 把企业 person 条目标成 type=1（个人）的情况。
var bangumiOrgSelfDescription = regexp.MustCompile(
	`^(株式会社|有限会社|合同会社)\S{1,40}(は|が)[、,。]` + // 日：株式会社Xは、…企業。
		`|を主な事業内容とする` + // 日：…を主な事業内容とする
		`|(企業|会社|法人)である` + // 日：…企業である
		`|(是一家|是日本一家|一家).{0,30}(公司|企业)` + // 中：…(是)一家…公司
		`|专门从事.{0,20}(公司|企业)`)

// bangumiPersonAgentType 判定 person 条目的 agent 类型。
// 上游 type 为权威：2=公司、3=组合；type=1（个人）时再用自述文本纠正
// （Bangumi 有把企业标成个人的数据，如 ブシロード）。
func bangumiPersonAgentType(typeID int, summary string) string {
	switch typeID {
	case 2:
		return "organization"
	case 3:
		return "group"
	}
	if bangumiOrgSelfDescription.MatchString(summary) {
		return "organization"
	}
	return "person"
}

// detectEntityLanguage 从名称与简介推断原语言。假名是日文的可靠信号，
// 标题无信号时看简介（来源原文，同为证据）；都无信号则留空不猜。
func detectEntityLanguage(name, summary string) string {
	if l := detectJapaneseScript(name); l != "" {
		return l
	}
	return detectJapaneseScript(summary)
}

// fetchBangumiPersonDetails 并发拉取 person 详情（含简介），带并发上限与失败降级。
// 列表端点不返回简介，只有详情端点有；失败/超时的条目在返回 map 中缺失，调用方降级。
func fetchBangumiPersonDetails(ctx context.Context, ids []int) map[int]bangumiPerson {
	out := map[int]bangumiPerson{}
	if len(ids) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // 并发上限：避免打爆上游与拖长导入
	for _, id := range ids {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var p bangumiPerson
			if err := fetchBangumi(ctx, "/v0/persons/"+strconv.Itoa(pid), &p); err != nil {
				return // 降级：调用方用列表里的简化数据
			}
			mu.Lock()
			out[pid] = p
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

// previewBangumiSubjectRelations 拉取条目的关联演职人员与角色，组装为可导入的
// agent 预览（两层以内关联实体）。任一端点失败只返回已取到的部分——关联数据
// 是增强项，不应让主条目导入失败。
func previewBangumiSubjectRelations(ctx context.Context, subjectID int) []ExternalSourceArtistPreview {
	out := []ExternalSourceArtistPreview{}

	var persons []bangumiSubjectPerson
	var chars []bangumiSubjectCharacter
	pErr := fetchBangumi(ctx, "/v0/subjects/"+strconv.Itoa(subjectID)+"/persons", &persons)
	cErr := fetchBangumi(ctx, "/v0/subjects/"+strconv.Itoa(subjectID)+"/characters", &chars)

	// 列表端点不含简介，而简介既是展示内容、也是"企业被误标为个人"的纠正依据、
	// 还是推断原语言的证据；因此并发拉取一次详情（失败则降级用列表数据）。
	personIDs := map[int]bool{}
	if pErr == nil {
		for _, p := range persons {
			personIDs[p.ID] = true
		}
	}
	if cErr == nil {
		for _, c := range chars {
			for _, a := range c.Actors {
				personIDs[a.ID] = true
			}
		}
	}
	ids := make([]int, 0, len(personIDs))
	for id := range personIDs {
		ids = append(ids, id)
	}
	details := fetchBangumiPersonDetails(ctx, ids)

	if pErr == nil {
		for _, p := range persons {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				continue
			}
			role := strings.TrimSpace(p.Relation)
			// 语义明确时用精确关系码；否则落到通用署名关系并保留职位原文，
			// 避免有职位却无关系（实体成孤儿）导致关联信息丢失。
			rel := bangumiCreditRelation(role)
			if rel == "" && role != "" {
				rel = "credit_for"
			}
			// 详情里的 type/summary 更权威：摘要能纠正被误标成个人的企业。
			agentType := bangumiPersonTypeAgent(p.Type)
			biography := ""
			if d, ok := details[p.ID]; ok {
				agentType = bangumiPersonAgentType(d.Type.ID, d.Summary)
				biography = strings.TrimSpace(d.Summary)
			}
			out = append(out, ExternalSourceArtistPreview{
				Name:          name,
				OriginalName:  name,
				Role:          role,
				EntityType:    agentType,
				Biography:     biography,
				Language:      detectEntityLanguage(name, biography),
				AvatarURL:     bangumiAvatarURL(p.Images),
				ExternalLinks: previewLinks(map[string]any{"bangumi_person": p.ID, "metafusion_import": "bangumi:person:" + strconv.Itoa(p.ID)}),
				// 职位文本保真：语义明确时给关系码，否则由 credit_role 承载原文。
				RelationType: rel,
			})
		}
	}

	if cErr == nil {
		for _, c := range chars {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				continue
			}
			rank := strings.TrimSpace(c.Relation)
			// 角色本体：character_in → 作品。番位词表项（主角/配角/客串）进 relation_role，
			// 原始文本（含"旁白""闲角"等词表未覆盖的）一律进 credit_role 保证无损。
			out = append(out, ExternalSourceArtistPreview{
				Name:          name,
				OriginalName:  name,
				Role:          rank,
				EntityType:    bangumiCharacterAgentType(nil, c.Summary),
				Biography:     strings.TrimSpace(c.Summary),
				Language:      detectEntityLanguage(name, c.Summary),
				AvatarURL:     bangumiAvatarURL(c.Images),
				ExternalLinks: previewLinks(map[string]any{"bangumi_character": c.ID, "metafusion_import": "bangumi:character:" + strconv.Itoa(c.ID)}),
				RelationType:  "character_in",
				RelationRole:  bangumiCharacterRankRole(rank),
			})
			// 声优：voiced_by → 作品，attributes.character 保留角色名，供前端把配音与登场角色配对展示。
			for _, a := range c.Actors {
				an := strings.TrimSpace(a.Name)
				if an == "" {
					continue
				}
				agentType := bangumiPersonTypeAgent(a.Type)
				biography := ""
				if d, ok := details[a.ID]; ok {
					agentType = bangumiPersonAgentType(d.Type.ID, d.Summary)
					biography = strings.TrimSpace(d.Summary)
				}
				out = append(out, ExternalSourceArtistPreview{
					Name:          an,
					OriginalName:  an,
					Role:          "配音",
					EntityType:    agentType,
					Biography:     biography,
					Language:      detectEntityLanguage(an, biography),
					AvatarURL:     bangumiAvatarURL(a.Images),
					CharacterName: name,
					ExternalLinks: previewLinks(map[string]any{"bangumi_person": a.ID, "metafusion_import": "bangumi:person:" + strconv.Itoa(a.ID)}),
					RelationType:  "voiced_by",
				})
			}
		}
	}
	return out
}

// bangumiBandPattern 匹配简介中的乐队/组合类关键词；\b 限定英文 band 独立成词，
// 避免 husband 等误命中。
var bangumiBandPattern = regexp.MustCompile(`乐队|樂隊|バンド|\bband\b`)

// bangumiCharacterAgentType 把 Bangumi "角色"条目收敛到导入器的 agent 角色标记。
// Bangumi 把乐队/组合等虚构团体也挂在角色列表（如 MyGO!!!!! 是 subject 428735
// 角色表里的"配角"），API 没有类型字段；保守启发：无性别信息且简介含
// 乐队/band 类关键词才判 group，其余保持 character。该判断只作为预览标记与标签建议。
func bangumiCharacterAgentType(gender *string, summary string) string {
	if gender != nil && strings.TrimSpace(*gender) != "" {
		return "character"
	}
	if bangumiBandPattern.MatchString(strings.ToLower(summary)) {
		return "group"
	}
	return "character"
}

// bangumiWorkType 把来源 subject type 映射为预览标记；
// 无法判断时返回空，不在实体上虚构业务类型。
// Bangumi 文档：1=书籍 2=动画 3=音乐 4=游戏 6=三次元。
func bangumiWorkType(t int) string {
	switch t {
	case 1:
		return "novel"
	case 2:
		return "animation"
	case 3:
		return "music"
	case 4:
		return "game"
	case 6:
		return "personal"
	default:
		return ""
	}
}

// bangumiAgentType 把来源 person type 映射为导入器的 agent 角色标记。
// Bangumi 人物：1=个人 2=公司 3=组合。
func bangumiAgentType(t int) string {
	switch t {
	case 2:
		return "organization"
	case 3:
		return "group"
	default:
		return "person"
	}
}

func bangumiTags(tags []bangumiTag, limit int) []string {
	out := []string{}
	for _, t := range tags {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// bangumiEpisodeType 判定分集类型：0=本篇、1=SP、2=OP、3=ED、4=预告/其他。
// 本篇走 content_unit，其余作为附加内容同样保留层级，但标 entry_role。
// entry_role 取值必须是 definitions entry_role 词表项：OP→opening、ED→ending
// （词表另有 ending 项，见 defaults.go），不能把 ED 并入 opening。
func bangumiEpisodeRole(epType int) string {
	switch epType {
	case 0:
		return "main"
	case 2:
		return "opening"
	case 3:
		return "ending"
	case 4:
		return "trailer"
	default:
		return "extra"
	}
}

// bangumiDurationSeconds 把 "24m" / "1h2m" / "300" 之类的时长解析为秒；无信号返回 0。
func bangumiDurationSeconds(raw string) float64 {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0
	}
	var total float64
	var num strings.Builder
	flush := func(unit string) {
		if num.Len() == 0 {
			return
		}
		v, err := strconv.ParseFloat(num.String(), 64)
		num.Reset()
		if err != nil {
			return
		}
		switch unit {
		case "h":
			total += v * 3600
		case "m":
			total += v * 60
		default:
			total += v
		}
	}
	for _, ch := range s {
		switch {
		case ch >= '0' && ch <= '9' || ch == '.':
			num.WriteRune(ch)
		case ch == 'h' || ch == 'm' || ch == 's':
			flush(string(ch))
		case ch == ' ':
			// 忽略分隔
		default:
			// 未知单位：丢弃当前数字段，避免把 "24分" 误当秒
			num.Reset()
		}
	}
	flush("s")
	return total
}

// previewBangumiEpisodes 取分集列表并转换成 canonical entries（带 parent_index 树）。
// 篇目类型（本篇）落 content_unit 且可挂下级；SP/OP/ED 等作为附加内容平铺。
// 分集无名称时用"第N话"补齐（有来源编号，非虚构题名）。
// bangumiEpisodeTypes 是 Bangumi 分集端点的 type 取值：0 本篇、1 特别篇、2 OP、
// 3 ED、4 预告/宣传、5 MAD、6 其它。固定 type=0 会漏掉 SP/OP/ED 等，因此逐个抓取。
var bangumiEpisodeTypes = []int{0, 1, 2, 3, 4, 5, 6}

// previewBangumiEpisodes 抓取某条目的分集/篇目。遍历全部 type 并按 episode ID 去重
// （同一 episode 只落一条，role 由 bangumiEpisodeRole 依 type 决定），
// 某类抓取失败只跳过该类，不使整体失败。返回值第二项为抓取不完整时的告警。
func previewBangumiEpisodes(ctx context.Context, subjectID int) ([]ExternalSourceCanonicalEntryPreview, []string) {
	out := []ExternalSourceCanonicalEntryPreview{}
	warnings := []string{}
	seen := map[int]bool{}
	for _, epType := range bangumiEpisodeTypes {
		limit, offset := 100, 0
		fetched := 0
		expected := -1
		failed := false
		for {
			var resp bangumiEpisodesResponse
			path := "/v0/episodes?subject_id=" + strconv.Itoa(subjectID) + "&type=" + strconv.Itoa(epType) + "&limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
			if err := fetchBangumi(ctx, path, &resp); err != nil {
				failed = true
				break
			}
			if expected < 0 {
				expected = resp.Total
			}
			if len(resp.Data) == 0 {
				break
			}
			for i, ep := range resp.Data {
				if seen[ep.ID] {
					continue
				}
				title := strings.TrimSpace(ep.NameCN)
				if title == "" {
					title = strings.TrimSpace(ep.Name)
				}
				number := ""
				if ep.Ep > 0 {
					number = strconv.FormatFloat(ep.Ep, 'f', -1, 64)
				} else if ep.Sort > 0 {
					number = strconv.FormatFloat(ep.Sort, 'f', -1, 64)
				}
				if title == "" {
					if number == "" {
						continue
					}
					title = "第" + number + "话"
				}
				pos := int(ep.Sort)
				if pos <= 0 {
					pos = offset + i + 1
				}
				entry := ExternalSourceCanonicalEntryPreview{
					Title:           title,
					Translations:    bangumiTranslationItems(ep.Name, ep.NameCN, ""),
					Position:        pos,
					Number:          number,
					EntryRole:       bangumiEpisodeRole(ep.Type),
					EntryKind:       "content_unit",
					DurationSeconds: bangumiDurationSeconds(ep.Duration),
					ExternalLinks:   previewLinks(map[string]any{"bangumi_episode": ep.ID}),
				}
				if strings.TrimSpace(ep.NameCN) != "" && strings.TrimSpace(ep.Name) != "" && ep.NameCN != ep.Name {
					entry.OriginalLanguage = detectJapaneseScript(ep.Name)
				}
				seen[ep.ID] = true
				fetched++
				out = append(out, entry)
			}
			if len(resp.Data) < limit {
				break
			}
			offset += limit
		}
		// 抓取数量与来源声明的 total 不符（含中途失败）→ 明确告警，不静默当作完整。
		if failed || (expected >= 0 && fetched < expected) {
			warnings = append(warnings, "bangumi_episodes_incomplete:type="+strconv.Itoa(epType))
		}
	}
	return out, warnings
}

func bangumiTitlePair(name, nameCN string) (title, original string) {
	name = strings.TrimSpace(name)
	nameCN = strings.TrimSpace(nameCN)
	if nameCN != "" {
		return nameCN, name
	}
	return name, name
}

func bangumiTranslationItems(name, nameCN, summary string) []ExternalSourceTranslationItem {
	items := []ExternalSourceTranslationItem{}
	if strings.TrimSpace(nameCN) != "" && strings.TrimSpace(nameCN) != strings.TrimSpace(name) {
		items = append(items, ExternalSourceTranslationItem{Locale: "zh-CN", Title: strings.TrimSpace(nameCN), Summary: summary})
	}
	if strings.TrimSpace(name) != "" {
		items = append(items, ExternalSourceTranslationItem{Locale: "ja", Title: strings.TrimSpace(name)})
	}
	return items
}

// Preview 通过已实现的来源适配器生成预览，返回具体 source 与实际 entity_type。
// 空来源/auto 按 URL/ID 探测；Bangumi 支持条目、人物与角色，DLsite/DMM 商品返回 work，
// 不使用请求 entity_type 做人物/角色路由或校验，各来源的字段与关联覆盖由适配器决定。
func (s *Store) Preview(ctx context.Context, source, urlOrID, entityType string) (ExternalSourcePreviewResponse, error) {
	// auto（含空值）按 URL/ID 字面形态判定具体来源；显式指定来源时以指定为准。
	rawSource := strings.ToLower(strings.TrimSpace(source))
	if rawSource == "" || rawSource == "auto" {
		rawSource = detectExternalSourceDefinition(urlOrID)
	}
	resolvedSource, err := normalizeExternalSourceDefinition(rawSource)
	if err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	switch resolvedSource {
	case "dlsite":
		ref, perr := parseDLsiteRef(urlOrID)
		if perr != nil {
			return ExternalSourcePreviewResponse{}, perr
		}
		return previewDLsite(ctx, ref)
	case "dmm":
		ref, perr := parseDMMRef(urlOrID)
		if perr != nil {
			return ExternalSourcePreviewResponse{}, perr
		}
		return previewDMM(ctx, ref)
	}
	et, err := normalizeExternalSourceEntityType(entityType)
	if err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	ref, err := resolveBangumiRef(urlOrID, et)
	if err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	switch ref.Kind {
	case "subject":
		return previewBangumiSubject(ctx, resolvedSource, ref.ID)
	case "person":
		return previewBangumiPerson(ctx, resolvedSource, ref.ID)
	case "character":
		return previewBangumiCharacter(ctx, resolvedSource, ref.ID)
	default:
		return ExternalSourcePreviewResponse{}, fmt.Errorf("not_supported")
	}
}

func previewBangumiSubject(ctx context.Context, source string, id int) (ExternalSourcePreviewResponse, error) {
	var sub bangumiSubject
	if err := fetchBangumi(ctx, "/v0/subjects/"+strconv.Itoa(id), &sub); err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	if strings.TrimSpace(sub.Name) == "" {
		return ExternalSourcePreviewResponse{}, fmt.Errorf("upstream_error")
	}
	title, original := bangumiTitlePair(sub.Name, sub.NameCN)
	workType := bangumiWorkType(sub.Type)
	mediaType := workType
	if mediaType == "" {
		mediaType = "unknown"
	}
	tags := bangumiTags(sub.Tags, 12)
	// 原语言推断：以官方原名（name）的语言为准，含假名可判定日文；
	// 标题无可判定信号时，退一步看来源简介（同样是来源原文，仍属证据而非猜测）。
	// 两者都无线索时留空（不虚构），展示回退链仍可工作。
	origLang := detectJapaneseScript(sub.Name)
	if origLang == "" {
		origLang = detectJapaneseScript(sub.Summary)
	}
	// infobox 补充官方字段：官网、品番、别名、出版社等（键名随媒体类型不同）。
	aliases := sub.bangumiInfoboxAliases(title, original)
	website := firstNonEmpty(sub.infoboxString("官方网站"), sub.infoboxString("官方網站"), sub.infoboxString("官网"))
	catalogNo := firstNonEmpty(sub.infoboxString("商品编号"), sub.infoboxString("商品編號"), sub.infoboxString("品番"))
	canonicalEntries, warnings := previewBangumiEpisodes(ctx, sub.ID)
	return ExternalSourcePreviewResponse{
		Source:      source,
		EntityType:  "work",
		ExternalID:  strconv.Itoa(sub.ID),
		ExternalURL: "https://bgm.tv/subject/" + strconv.Itoa(sub.ID),
		MediaType:   mediaType,
		Work: &ExternalSourceWorkPreview{
			Title:            title,
			OriginalTitle:    original,
			Aliases:          aliases,
			ReleaseDate:      strings.TrimSpace(sub.Date),
			Language:         origLang,
			OriginalLanguage: origLang,
			Summary:          sub.Summary,
			CoverImageURL:    sub.Images.best(),
			Tags:             tags,
			Translations:     bangumiTranslationItems(sub.Name, sub.NameCN, sub.Summary),
			CatalogMetadata: map[string]any{
				"bangumi_type":     sub.Type,
				"bangumi_platform": sub.Platform,
				"official_website": website,
				"catalog_number":   catalogNo,
			},
			Fields:  sub.infoboxValues(),
			Infobox: sub.infoboxEntries(),
		},
		Tags: tags,
		// 两层以内的关联演职人员与角色：前端把 artists 转成 staff_associations 提交，
		// 落库时建 agent 实体并把关系挂到作品上。
		Artists: previewBangumiSubjectRelations(ctx, sub.ID),
		// 分集/篇目：动画、剧集类条目有独立分集端点，落 content_unit 树；
		// 无分集（音乐/书籍等）时为空，前端不展示内容区，不用空数组造假。
		CanonicalEntries: canonicalEntries,
		Warnings:         warnings,
	}, nil
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// scalarString 宽松取标量字符串值（属性经 JSON 往返后形态不定）。
func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

// toAnySlice 把类型化切片转成 []any，以便作为 JSONB 值参与属性校验与落库。
func toAnySlice[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// dynamicFieldValue 判断动态字段值是否可落库，并保持原类型。
// 数值/日期字段由规格校验按类型判定，因此不能像文本那样统一转成字符串，
// 否则合法整数会被 stringify 成 "13" 而被 invalid_number 拒绝。
func dynamicFieldValue(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		return strings.TrimSpace(x), true
	case []any:
		if len(x) == 0 {
			return nil, false
		}
		return x, true
	default:
		return v, true
	}
}

// detectJapaneseScript 用假名判定日文原文（假名是日文的可靠信号）。
// 纯汉字/拉丁文本存在简繁歧义，返回空而不猜，避免给条目写错原语言。
func detectJapaneseScript(s string) string {
	for _, r := range s {
		// 平假名 3040-309F、片假名 30A0-30FF（含半角片假名 FF66-FF9D 另计）
		if (r >= 0x3040 && r <= 0x30FF) || (r >= 0xFF66 && r <= 0xFF9D) {
			return "ja"
		}
	}
	return ""
}

func previewBangumiPerson(ctx context.Context, source string, id int) (ExternalSourcePreviewResponse, error) {
	var p bangumiPerson
	if err := fetchBangumi(ctx, "/v0/persons/"+strconv.Itoa(id), &p); err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	if strings.TrimSpace(p.Name) == "" {
		return ExternalSourcePreviewResponse{}, fmt.Errorf("upstream_error")
	}
	agentType := bangumiAgentType(p.Type.ID)
	entityType := "artist"
	if agentType == "organization" || agentType == "group" {
		entityType = "organization"
	}
	name, original := bangumiTitlePair(p.Name, p.NameCN)
	role := ""
	if len(p.Career) > 0 {
		role = strings.TrimSpace(p.Career[0])
	}
	return ExternalSourcePreviewResponse{
		Source:      source,
		EntityType:  entityType,
		ExternalID:  strconv.Itoa(p.ID),
		ExternalURL: "https://bgm.tv/person/" + strconv.Itoa(p.ID),
		MediaType:   agentType,
		Artist: &ExternalSourceArtistPreview{
			Name:          name,
			OriginalName:  original,
			Role:          role,
			EntityType:    agentType,
			Biography:     p.Summary,
			AvatarURL:     p.Images.best(),
			Aliases:       []string{},
			ExternalLinks: previewLinks(map[string]any{"bangumi_person": p.ID}),
			Translations:  bangumiTranslationItems(p.Name, p.NameCN, p.Summary),
		},
		Tags: []string{},
	}, nil
}

func previewBangumiCharacter(ctx context.Context, source string, id int) (ExternalSourcePreviewResponse, error) {
	var ch bangumiCharacter
	if err := fetchBangumi(ctx, "/v0/characters/"+strconv.Itoa(id), &ch); err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	if strings.TrimSpace(ch.Name) == "" {
		return ExternalSourcePreviewResponse{}, fmt.Errorf("upstream_error")
	}
	name, original := bangumiTitlePair(ch.Name, ch.NameCN)
	// 顶层 EntityType 保持 character：预览→导入回传与 dedup key 都以 URL 推断为准；
	// 乐队型条目的类型细化落在 Artist.EntityType，导入侧据此建 group agent。
	agentType := bangumiCharacterAgentType(ch.Gender, ch.Summary)
	return ExternalSourcePreviewResponse{
		Source:      source,
		EntityType:  "character",
		ExternalID:  strconv.Itoa(ch.ID),
		ExternalURL: "https://bgm.tv/character/" + strconv.Itoa(ch.ID),
		MediaType:   "character",
		Artist: &ExternalSourceArtistPreview{
			Name:          name,
			OriginalName:  original,
			Role:          "Character",
			EntityType:    agentType,
			Biography:     ch.Summary,
			AvatarURL:     ch.Images.best(),
			Aliases:       []string{},
			ExternalLinks: previewLinks(map[string]any{"bangumi_character": ch.ID}),
			Translations:  bangumiTranslationItems(ch.Name, ch.NameCN, ch.Summary),
		},
		Tags: []string{},
	}, nil
}

// ---- 落库 ----

var externalSourceDatePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

func cleanExternalSourceDate(v string) string {
	v = strings.TrimSpace(v)
	if !externalSourceDatePattern.MatchString(v) {
		return ""
	}
	return v
}
