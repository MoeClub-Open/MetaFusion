package catalog

// 外部目录导入器（OmniImportModal 后端）。
//
// 通过已实现的适配器预览并导入外部目录，可用来源由 /importer/sources 自描述；
// 未实现适配器的来源返回 not_supported。图片只做远端 URL 引用，不抓取、不转存
// （转存归存储子系统）；
// download_cover 显式 false 时不写 Picture，见 importerApplyFieldSwitches。
//
// 落库全部走 Store.Save / Store.SaveRelation，证据（edit_note + sources）必填；
// 幂等键 external_ids.metafusion_import={source}:{resource-kind}:{id}，
// 具体资源类型由来源适配器决定，已有实体按导入模式复用。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
)

var (
	// bangumiAPIBase 允许单测替换为 httptest 服务；生产默认指向 Bangumi 公开 API。
	bangumiAPIBase     = "https://api.bgm.tv"
	importerHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

const importerUserAgent = "MetaFusion/1.0 (catalog importer)"

// ---- 前端契约 DTO（字段与 frontend/src/lib/api.ts 逐一对齐） ----
// http.go 的 body() 使用 DisallowUnknownFields，缺字段会导致 invalid_payload，
// 因此请求结构必须包含前端可能发送的全部字段；松散对象统一用 any / map 承接。

type ImporterPreviewRequest struct {
	Source     string `json:"source"`
	URLOrID    string `json:"url_or_id"`
	EntityType string `json:"entity_type"`
}

type ImporterTranslationItem struct {
	Locale  string   `json:"locale"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Aliases []string `json:"aliases,omitempty"`
}

type ImporterWorkPreview struct {
	Title            string                    `json:"title"`
	OriginalTitle    string                    `json:"original_title"`
	Aliases          []string                  `json:"aliases"`
	ReleaseDate      string                    `json:"release_date"`
	BeginDate        string                    `json:"begin_date,omitempty"`
	Country          string                    `json:"country"`
	Language         string                    `json:"language"`
	OriginalLanguage string                    `json:"original_language,omitempty"`
	Summary          string                    `json:"summary"`
	CoverImageURL    string                    `json:"cover_image_url"`
	CoverAspect      string                    `json:"cover_aspect"`
	ContentRating    string                    `json:"content_rating"`
	Tags             []string                  `json:"tags"`
	Translations     []ImporterTranslationItem `json:"translations"`
	CatalogMetadata  any                       `json:"catalog_metadata,omitempty"`
	// Fields 是按 infobox 映射出的动态字段值（键为已声明的字段码）；
	// Infobox 是上游资料表的完整原文快照，用于追溯与后续补充映射。
	Fields  map[string]any   `json:"fields,omitempty"`
	Infobox []map[string]any `json:"infobox,omitempty"`
}

type ImporterArtistPreview struct {
	ID             string                    `json:"id,omitempty"`
	Name           string                    `json:"name"`
	OriginalName   string                    `json:"original_name,omitempty"`
	Role           string                    `json:"role"`
	EntityType     string                    `json:"entity_type"`
	Country        string                    `json:"country,omitempty"`
	Biography      string                    `json:"biography,omitempty"`
	Disambiguation string                    `json:"disambiguation,omitempty"`
	Language       string                    `json:"language,omitempty"`
	AvatarURL      string                    `json:"avatar_url,omitempty"`
	CharacterName  string                    `json:"character_name,omitempty"`
	Aliases        []string                  `json:"aliases,omitempty"`
	ExternalIDs    map[string]any            `json:"external_ids,omitempty"`
	Translations   []ImporterTranslationItem `json:"translations,omitempty"`
	MatchedArtist  any                       `json:"matched_artist,omitempty"`
	// RelationType 是该关联应落到 definitions 的关系码（如 directed_by / voiced_by / character_in）；
	// RelationRole 是角色番位的 character_rank 词表项（main/supporting/guest/ensemble/narrator/cameo，
	// 落 character_in 的 attributes.character_rank）。
	RelationType string `json:"relation_type,omitempty"`
	RelationRole string `json:"relation_role,omitempty"`
}

type ImporterStaffAssociation struct {
	ParsedName     string                    `json:"parsed_name"`
	ParsedOriginal string                    `json:"parsed_original,omitempty"`
	ParsedRole     string                    `json:"parsed_role"`
	EntityType     string                    `json:"entity_type"`
	Action         string                    `json:"action"`
	TargetArtistID string                    `json:"target_artist_id,omitempty"`
	CharacterName  string                    `json:"character_name,omitempty"`
	Country        string                    `json:"country,omitempty"`
	Biography      string                    `json:"biography,omitempty"`
	Language       string                    `json:"language,omitempty"`
	AvatarURL      string                    `json:"avatar_url,omitempty"`
	ExternalIDs    map[string]any            `json:"external_ids,omitempty"`
	Translations   []ImporterTranslationItem `json:"translations,omitempty"`
	RelationType   string                    `json:"relation_type,omitempty"`
	RelationRole   string                    `json:"relation_role,omitempty"`
}

type ImporterTrackPreview struct {
	Position        int     `json:"position"`
	Title           string  `json:"title"`
	DurationSeconds float64 `json:"duration_seconds"`
	ArtistCredit    string  `json:"artist_credit,omitempty"`
	ISRC            string  `json:"isrc,omitempty"`
	RecordingMBID   string  `json:"recording_mbid,omitempty"`
	// ExpressionID 显式复用的既有表达（用户在预览中手工匹配的录音），
	// 优先于自动对齐；校验必须属于同一 Work。
	ExpressionID string `json:"expression_id,omitempty"`
	// EntryIndex 指向同一 canonical_entries 数组内的条目下标，用于把曲目结构绑定到
	// 本次清单里的稳定节点，不再靠标题在条目之间传递绑定。
	EntryIndex *int `json:"entry_index,omitempty"`
}

// ImporterMediumPreview 是载体预览。OriginalLanguage / Translations 落 medium **实体本体**
// （不是 attributes）：载体题名的原语言行与各语种译名行，随载体落库写入（importReleaseChain），
// 幂等命中时补齐缺失行、不覆盖已有行。
type ImporterMediumPreview struct {
	Position         int                    `json:"position"`
	Number           string                 `json:"number,omitempty"`
	Name             string                 `json:"name"`
	Format           string                 `json:"format"`
	Role             string                 `json:"role,omitempty"`
	OriginalLanguage string                 `json:"original_language,omitempty"`
	Translations     any                    `json:"translations,omitempty"`
	Tracks           []ImporterTrackPreview `json:"tracks"`
}

// ImporterReleasePreview 是发行版预览。与载体同口径：OriginalLanguage / Translations 落
// release 实体本体（发行版题名的原语言行与各语种译名行），随发行链写入。
//
// 封面 CoverImageURL 与 work 封面同口径地兑现：透传为 release 实体的 Picture（只引用远端
// URL，不抓取、不转存），新建与幂等复用都写（复用只补空、不覆盖已有图）。
// CoverAspect / Notes / CatalogMetadata / Language 在模型里没有落点，由预检明确拒绝
// （unsupported_field_for_entity_type，逐条理由见 importerUnsupportedPayloadFields）。
type ImporterReleasePreview struct {
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

type ImporterCanonicalEntryPreview struct {
	Title            string         `json:"title"`
	Translations     any            `json:"translations,omitempty"`
	Position         int            `json:"position"`
	Number           string         `json:"number,omitempty"`
	EntryRole        string         `json:"entry_role,omitempty"`
	OriginalLanguage string         `json:"original_language,omitempty"`
	DurationSeconds  float64        `json:"duration_seconds,omitempty"`
	Attributes       map[string]any `json:"attributes,omitempty"`
	ExternalIDs      map[string]any `json:"external_ids,omitempty"`
	// EntryKind 条目落库层级："content_unit"（篇目/分集，可带下级）或
	// "expression"（默认，录音/正文）。由来源结构决定，不由标题猜测。
	EntryKind string `json:"entry_kind,omitempty"`
	// ParentIndex 指向同一 canonical_entries 数组内父级条目的下标（-1/省略为顶层），
	// 用于表达章节树；仅在同 Work 内成立。
	ParentIndex *int `json:"parent_index,omitempty"`
	// ExpressionID 显式指定复用的既有表达（用户在预览中手工匹配），
	// 优先于自动对齐；校验必须存在、可见且属于同一 Work。
	ExpressionID string `json:"expression_id,omitempty"`
}

type ImporterPreviewResponse struct {
	Source           string                          `json:"source"`
	EntityType       string                          `json:"entity_type,omitempty"`
	ExternalID       string                          `json:"external_id"`
	ExternalURL      string                          `json:"external_url"`
	MediaType        string                          `json:"media_type"`
	Work             *ImporterWorkPreview            `json:"work,omitempty"`
	Artist           *ImporterArtistPreview          `json:"artist,omitempty"`
	Artists          []ImporterArtistPreview         `json:"artists,omitempty"`
	HasRelease       bool                            `json:"has_release,omitempty"`
	CanonicalEntries []ImporterCanonicalEntryPreview `json:"canonical_entries,omitempty"`
	Release          *ImporterReleasePreview         `json:"release,omitempty"`
	Mediums          []ImporterMediumPreview         `json:"mediums,omitempty"`
	Tags             []string                        `json:"tags"`
	// Warnings 记录来源抓取不完整等需要人工留意的情况（如分集总数与实取不符），
	// 前端据此提示，避免把不完整预览当成完整清单落库。
	Warnings []string `json:"warnings,omitempty"`
}

type ImporterImportRequest struct {
	EntityType        string                     `json:"entity_type,omitempty"`
	Source            string                     `json:"source,omitempty"`
	URLOrID           string                     `json:"url_or_id,omitempty"`
	ExternalID        string                     `json:"external_id,omitempty"`
	MediaTypeHint     string                     `json:"media_type_hint,omitempty"`
	Work              *ImporterWorkPreview       `json:"work,omitempty"`
	Artist            *ImporterArtistPreview     `json:"artist,omitempty"`
	Artists           []ImporterArtistPreview    `json:"artists,omitempty"`
	StaffAssociations []ImporterStaffAssociation `json:"staff_associations,omitempty"`
	// HasRelease 是预览侧的声明位（预览响应目前不产出它）；写路径以 mediums 为写指令：
	// has_release=true 却没有 mediums、以及 release 带了数据却没有 mediums，都在零写入预检里
	// 明确拒绝（importerApplyFieldSwitches / importerReleaseRequiresMediums），不静默丢发行。
	HasRelease       bool                            `json:"has_release,omitempty"`
	CanonicalEntries []ImporterCanonicalEntryPreview `json:"canonical_entries,omitempty"`
	Release          *ImporterReleasePreview         `json:"release,omitempty"`
	Mediums          []ImporterMediumPreview         `json:"mediums,omitempty"`
	// DownloadCover 只决定是否把远端封面/头像作为 Picture 引用写库（目录侧不抓取、不转存，
	// 转存归存储子系统）：指针用于区分"没传"与"显式 false"——未传保持既有透传行为，
	// 显式 false 表示调用方不要封面（见 importerApplyFieldSwitches）。
	DownloadCover *bool    `json:"download_cover,omitempty"`
	EditNote      string   `json:"edit_note,omitempty"`
	SourceURLs    []string `json:"source_urls,omitempty"`
	TargetWorkID  string   `json:"target_work_id,omitempty"`
	LinkMode      string   `json:"link_mode,omitempty"`
	RelationType  string   `json:"relation_type,omitempty"`
}

type ImporterImportedCounts struct {
	Artists          int `json:"artists"`
	Relations        int `json:"relations"`
	SkippedRelations int `json:"skipped_relations"`
	Mediums          int `json:"mediums"`
	Tracks           int `json:"tracks"`
	ContentUnits     int `json:"content_units"`
}

type ImporterImportResponse struct {
	Success        bool                   `json:"success"`
	EntityType     string                 `json:"entity_type,omitempty"`
	WorkID         string                 `json:"work_id,omitempty"`
	ReleaseID      string                 `json:"release_id,omitempty"`
	ArtistID       string                 `json:"artist_id,omitempty"`
	Work           Entity                 `json:"work,omitempty"`
	Release        Entity                 `json:"release,omitempty"`
	Artist         Entity                 `json:"artist,omitempty"`
	ImportedCounts ImporterImportedCounts `json:"imported_counts"`
	RedirectURL    string                 `json:"redirect_url"`
}

// ---- 参数归一化 ----

func normalizeImporterSource(source string) (string, error) {
	src := strings.ToLower(strings.TrimSpace(source))
	if src == "" {
		src = "auto"
	}
	// auto 是解析别名而非法定来源：Preview 先按 URL/ID 判定具体适配器，
	// Import 不做来源探测，空值/auto 在这里默认 bangumi；客户端应回传预览的具体 source。
	if src == "auto" {
		return "bangumi", nil
	}
	if importerAdapterSet()[src] {
		return src, nil
	}
	return "", fmt.Errorf("not_supported")
}

// detectImporterSource 按 URL/ID 字面形态判定 auto 下的具体来源；
// 无法判定时回落到 bangumi（默认适配器）。纯本地解析，不发网络请求。
func detectImporterSource(raw string) string {
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

func normalizeImporterEntityType(entityType string) (string, error) {
	et := strings.ToLower(strings.TrimSpace(entityType))
	if et == "" {
		et = "work"
	}
	if !contains([]string{"work", "artist", "organization", "character"}, et) {
		return "", fmt.Errorf("invalid_entity_type")
	}
	return et, nil
}

// normalizeImporterLinkMode 归一关联策略。merge_translations 已从契约移除：它曾与
// append_release_to_work 同义（都走发行链），而词典承诺的"补译名/简介/外部 ID"从未实现；
// 与其继续静默同义，不如显式拒绝（invalid_link_mode），需要补译名的路径走常规编辑。
func normalizeImporterLinkMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = "new_work"
	}
	if !contains([]string{"new_work", "append_release_to_work", "create_relation"}, m) {
		return "", fmt.Errorf("invalid_link_mode")
	}
	return m, nil
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

func fetchBangumi(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bangumiAPIBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", importerUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := importerHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("upstream_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream_error")
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("upstream_error")
	}
	return nil
}

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
		if barcode, ok := importerISBNBarcode(raw); ok {
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
func previewBangumiSubjectRelations(ctx context.Context, subjectID int) []ImporterArtistPreview {
	out := []ImporterArtistPreview{}

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
			out = append(out, ImporterArtistPreview{
				Name:         name,
				OriginalName: name,
				Role:         role,
				EntityType:   agentType,
				Biography:    biography,
				Language:     detectEntityLanguage(name, biography),
				AvatarURL:    bangumiAvatarURL(p.Images),
				ExternalIDs:  map[string]any{"bangumi_person": p.ID, "metafusion_import": "bangumi:person:" + strconv.Itoa(p.ID)},
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
			out = append(out, ImporterArtistPreview{
				Name:         name,
				OriginalName: name,
				Role:         rank,
				EntityType:   bangumiCharacterAgentType(nil, c.Summary),
				Biography:    strings.TrimSpace(c.Summary),
				Language:     detectEntityLanguage(name, c.Summary),
				AvatarURL:    bangumiAvatarURL(c.Images),
				ExternalIDs:  map[string]any{"bangumi_character": c.ID, "metafusion_import": "bangumi:character:" + strconv.Itoa(c.ID)},
				RelationType: "character_in",
				RelationRole: bangumiCharacterRankRole(rank),
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
				out = append(out, ImporterArtistPreview{
					Name:          an,
					OriginalName:  an,
					Role:          "配音",
					EntityType:    agentType,
					Biography:     biography,
					Language:      detectEntityLanguage(an, biography),
					AvatarURL:     bangumiAvatarURL(a.Images),
					CharacterName: name,
					ExternalIDs:   map[string]any{"bangumi_person": a.ID, "metafusion_import": "bangumi:person:" + strconv.Itoa(a.ID)},
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
func previewBangumiEpisodes(ctx context.Context, subjectID int) ([]ImporterCanonicalEntryPreview, []string) {
	out := []ImporterCanonicalEntryPreview{}
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
				entry := ImporterCanonicalEntryPreview{
					Title:           title,
					Translations:    bangumiTranslationItems(ep.Name, ep.NameCN, ""),
					Position:        pos,
					Number:          number,
					EntryRole:       bangumiEpisodeRole(ep.Type),
					EntryKind:       "content_unit",
					DurationSeconds: bangumiDurationSeconds(ep.Duration),
					ExternalIDs:     map[string]any{"bangumi_episode": ep.ID},
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

func bangumiTranslationItems(name, nameCN, summary string) []ImporterTranslationItem {
	items := []ImporterTranslationItem{}
	if strings.TrimSpace(nameCN) != "" && strings.TrimSpace(nameCN) != strings.TrimSpace(name) {
		items = append(items, ImporterTranslationItem{Locale: "zh-CN", Title: strings.TrimSpace(nameCN), Summary: summary})
	}
	if strings.TrimSpace(name) != "" {
		items = append(items, ImporterTranslationItem{Locale: "ja", Title: strings.TrimSpace(name)})
	}
	return items
}

// Preview 通过已实现的来源适配器生成预览，返回具体 source 与实际 entity_type。
// 空来源/auto 按 URL/ID 探测；Bangumi 支持条目、人物与角色，DLsite/DMM 商品返回 work，
// 不使用请求 entity_type 做人物/角色路由或校验，各来源的字段与关联覆盖由适配器决定。
func (s *Store) Preview(ctx context.Context, source, urlOrID, entityType string) (ImporterPreviewResponse, error) {
	// auto（含空值）按 URL/ID 字面形态判定具体来源；显式指定来源时以指定为准。
	rawSource := strings.ToLower(strings.TrimSpace(source))
	if rawSource == "" || rawSource == "auto" {
		rawSource = detectImporterSource(urlOrID)
	}
	resolvedSource, err := normalizeImporterSource(rawSource)
	if err != nil {
		return ImporterPreviewResponse{}, err
	}
	switch resolvedSource {
	case "dlsite":
		ref, perr := parseDLsiteRef(urlOrID)
		if perr != nil {
			return ImporterPreviewResponse{}, perr
		}
		return previewDLsite(ctx, ref)
	case "dmm":
		ref, perr := parseDMMRef(urlOrID)
		if perr != nil {
			return ImporterPreviewResponse{}, perr
		}
		return previewDMM(ctx, ref)
	}
	et, err := normalizeImporterEntityType(entityType)
	if err != nil {
		return ImporterPreviewResponse{}, err
	}
	ref, err := resolveBangumiRef(urlOrID, et)
	if err != nil {
		return ImporterPreviewResponse{}, err
	}
	switch ref.Kind {
	case "subject":
		return previewBangumiSubject(ctx, resolvedSource, ref.ID)
	case "person":
		return previewBangumiPerson(ctx, resolvedSource, ref.ID)
	case "character":
		return previewBangumiCharacter(ctx, resolvedSource, ref.ID)
	default:
		return ImporterPreviewResponse{}, fmt.Errorf("not_supported")
	}
}

func previewBangumiSubject(ctx context.Context, source string, id int) (ImporterPreviewResponse, error) {
	var sub bangumiSubject
	if err := fetchBangumi(ctx, "/v0/subjects/"+strconv.Itoa(id), &sub); err != nil {
		return ImporterPreviewResponse{}, err
	}
	if strings.TrimSpace(sub.Name) == "" {
		return ImporterPreviewResponse{}, fmt.Errorf("upstream_error")
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
	return ImporterPreviewResponse{
		Source:      source,
		EntityType:  "work",
		ExternalID:  strconv.Itoa(sub.ID),
		ExternalURL: "https://bgm.tv/subject/" + strconv.Itoa(sub.ID),
		MediaType:   mediaType,
		Work: &ImporterWorkPreview{
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

func previewBangumiPerson(ctx context.Context, source string, id int) (ImporterPreviewResponse, error) {
	var p bangumiPerson
	if err := fetchBangumi(ctx, "/v0/persons/"+strconv.Itoa(id), &p); err != nil {
		return ImporterPreviewResponse{}, err
	}
	if strings.TrimSpace(p.Name) == "" {
		return ImporterPreviewResponse{}, fmt.Errorf("upstream_error")
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
	return ImporterPreviewResponse{
		Source:      source,
		EntityType:  entityType,
		ExternalID:  strconv.Itoa(p.ID),
		ExternalURL: "https://bgm.tv/person/" + strconv.Itoa(p.ID),
		MediaType:   agentType,
		Artist: &ImporterArtistPreview{
			Name:         name,
			OriginalName: original,
			Role:         role,
			EntityType:   agentType,
			Biography:    p.Summary,
			AvatarURL:    p.Images.best(),
			Aliases:      []string{},
			ExternalIDs:  map[string]any{"bangumi_person": p.ID},
			Translations: bangumiTranslationItems(p.Name, p.NameCN, p.Summary),
		},
		Tags: []string{},
	}, nil
}

func previewBangumiCharacter(ctx context.Context, source string, id int) (ImporterPreviewResponse, error) {
	var ch bangumiCharacter
	if err := fetchBangumi(ctx, "/v0/characters/"+strconv.Itoa(id), &ch); err != nil {
		return ImporterPreviewResponse{}, err
	}
	if strings.TrimSpace(ch.Name) == "" {
		return ImporterPreviewResponse{}, fmt.Errorf("upstream_error")
	}
	name, original := bangumiTitlePair(ch.Name, ch.NameCN)
	// 顶层 EntityType 保持 character：预览→导入回传与 dedup key 都以 URL 推断为准；
	// 乐队型条目的类型细化落在 Artist.EntityType，导入侧据此建 group agent。
	agentType := bangumiCharacterAgentType(ch.Gender, ch.Summary)
	return ImporterPreviewResponse{
		Source:      source,
		EntityType:  "character",
		ExternalID:  strconv.Itoa(ch.ID),
		ExternalURL: "https://bgm.tv/character/" + strconv.Itoa(ch.ID),
		MediaType:   "character",
		Artist: &ImporterArtistPreview{
			Name:         name,
			OriginalName: original,
			Role:         "Character",
			EntityType:   agentType,
			Biography:    ch.Summary,
			AvatarURL:    ch.Images.best(),
			Aliases:      []string{},
			ExternalIDs:  map[string]any{"bangumi_character": ch.ID},
			Translations: bangumiTranslationItems(ch.Name, ch.NameCN, ch.Summary),
		},
		Tags: []string{},
	}, nil
}

// ---- 落库 ----

var importerDatePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

func cleanImporterDate(v string) string {
	v = strings.TrimSpace(v)
	if !importerDatePattern.MatchString(v) {
		return ""
	}
	return v
}

func sanitizePosition(v, fallback int) int {
	if v < 0 {
		return fallback
	}
	return v
}

func toEntityTranslations(items []ImporterTranslationItem) map[string]Translation {
	out := map[string]Translation{}
	for _, it := range items {
		loc := strings.TrimSpace(it.Locale)
		title := strings.TrimSpace(it.Title)
		if loc == "" || title == "" {
			continue
		}
		if _, err := language.Parse(loc); err != nil {
			continue
		}
		out[loc] = Translation{Title: title, Summary: it.Summary, Aliases: it.Aliases}
	}
	return out
}

// scalarStringList 宽松取字符串列表（属性经 JSON 往返后可能是 []string 或 []any）。
func scalarStringList(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []string:
		for _, s := range x {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, raw := range x {
			if s := scalarString(raw); s != "" {
				out = append(out, s)
			}
		}
	case string:
		if s := strings.TrimSpace(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// importerTranslationsFromAny 归一化 canonical entry / 载体预览里的 translations：
// JSON 往返后可能是 []{locale,title,summary,aliases} 数组，也可能是
// {locale:{title,summary,aliases}} 映射；两种形态都收敛为 Entity.Translations，
// 非法 locale 与空标题丢弃。aliases 一并保留——预览已解析的译名别名不能丢，
// 否则别名只能进实体级 aliases，失去语种归属。
func importerTranslationsFromAny(v any) map[string]Translation {
	out := map[string]Translation{}
	for _, it := range importerTranslationDecls(v) {
		loc, title := strings.TrimSpace(it.Locale), strings.TrimSpace(it.Title)
		// 写路径的宽容口径：非法 locale 与空标题行丢弃（缺值不阻断整条导入），
		// 预检要用同一批声明却**不**做这层过滤，故形态解析单独一项（见 importerTranslationDecls）。
		if loc == "" || title == "" {
			continue
		}
		if _, err := language.Parse(loc); err != nil {
			continue
		}
		if _, exists := out[loc]; exists {
			continue
		}
		out[loc] = Translation{Title: title, Summary: it.Summary, Aliases: it.Aliases}
	}
	return out
}

// importerTranslationDecls 把载荷里声明的翻译行按原文展开（locale/title/summary/aliases），
// **不做**合法性过滤：三种形态（条目数组 / 语种映射 / 字符串映射）的解析只此一份，
// 过滤口径由调用方决定——写路径丢非法行（importerTranslationsFromAny），
// 预检按 Save 的 invalid_locale / invalid_translation 拒绝（importerPreflightValues）。
func importerTranslationDecls(v any) []ImporterTranslationItem {
	out := []ImporterTranslationItem{}
	switch x := v.(type) {
	case nil:
		return out
	case []ImporterTranslationItem:
		for _, it := range x {
			out = append(out, ImporterTranslationItem{Locale: it.Locale, Title: it.Title, Summary: it.Summary, Aliases: scalarStringList(it.Aliases)})
		}
	case []any:
		for _, raw := range x {
			if m, ok := raw.(map[string]any); ok {
				out = append(out, ImporterTranslationItem{Locale: scalarString(m["locale"]), Title: scalarString(m["title"]), Summary: scalarString(m["summary"]), Aliases: scalarStringList(m["aliases"])})
			}
		}
	case map[string]any:
		for loc, raw := range x {
			switch e := raw.(type) {
			case map[string]any:
				out = append(out, ImporterTranslationItem{Locale: loc, Title: scalarString(e["title"]), Summary: scalarString(e["summary"]), Aliases: scalarStringList(e["aliases"])})
			case string:
				out = append(out, ImporterTranslationItem{Locale: loc, Title: e})
			}
		}
	case map[string]Translation:
		for loc, tr := range x {
			out = append(out, ImporterTranslationItem{Locale: loc, Title: tr.Title, Summary: tr.Summary, Aliases: tr.Aliases})
		}
	}
	return out
}

// importerDeclaredTranslations 把载荷声明的翻译行按原文收进 Entity.Translations（不做写路径
// 的丢弃过滤）：非法 locale 与空标题在这里保留，交给 Save 的同一实现判定
// （invalid_locale / invalid_translation），从而"预检通过"与"Save 通过"用同一套判据。
// 同一 locale 多行时取首行（与 importerTranslationsFromAny 同序），仅用于校验不用于写入。
func importerDeclaredTranslations(decls []ImporterTranslationItem) map[string]Translation {
	out := map[string]Translation{}
	for _, it := range decls {
		loc := strings.TrimSpace(it.Locale)
		if _, exists := out[loc]; exists {
			continue
		}
		out[loc] = Translation{Title: strings.TrimSpace(it.Title), Summary: it.Summary, Aliases: it.Aliases}
	}
	return out
}

// importerContentUnitIndex 索引既有篇目，供导入复用。以外部标识（如 bangumi_episode）
// 为第一身份；无来源 ID 时用 (父篇目, 规范化标题) 作键——不再整 Work 按标题去重，
// 否则"上篇/第一章"与"下篇/第一章"会被误并。编号键只在**无 entry_role** 的条目间
// 生效：本篇与 OP/ED 的集数各自从 1 起算，同父同号不同 role 会被误并（entry_role
// 未持久化，无法对既有篇目取 role，故带 role 的条目只按外部 ID/标题复用）。
type importerContentUnitIndex struct {
	byExternal     map[string]string // 外部标识键 -> unit id
	byParentTitle  map[string]string
	byParentNumber map[string]string
	// ambiguous 标记出现多次的 (父, 标题)/(父, 编号) 键：同名条目身份不明，不做自动绑定。
	ambiguous map[string]bool
}

func newImporterContentUnitIndex(units []Entity) *importerContentUnitIndex {
	idx := &importerContentUnitIndex{byExternal: map[string]string{}, byParentTitle: map[string]string{}, byParentNumber: map[string]string{}, ambiguous: map[string]bool{}}
	for _, u := range units {
		hasExternal := false
		for k, v := range u.ExternalIDs {
			if v = strings.TrimSpace(v); v != "" {
				hasExternal = true
				idx.byExternal[strings.ToLower(k)+"|"+strings.ToLower(v)] = u.ID
			}
		}
		// 有来源 ID 的篇目只以来源 ID 认身份；登记标题/编号会与"不同来源 ID 同标题"
		// 的条目误并（已有正片 id=101、新增 OP id=201 都叫"第1话"）。
		if hasExternal {
			continue
		}
		idx.rememberTitleKeys(u.ParentID, u.Title, u.Number, u.ID)
	}
	return idx
}

// rememberTitleKeys 登记 (父, 标题)/(父, 编号) 复用键；同一键出现两次即标记歧义，
// 之后不再自动绑定（同名条目不能靠"先到先得/后来覆盖"决定身份）。
func (x *importerContentUnitIndex) rememberTitleKeys(parentID, title, number, id string) {
	if tk := normalizeImporterTitleKey(title); tk != "" {
		key := parentID + "\x00" + tk
		if _, ok := x.byParentTitle[key]; ok {
			x.ambiguous[key] = true
		} else {
			x.byParentTitle[key] = id
		}
	}
	if num := strings.TrimSpace(number); num != "" {
		key := parentID + "\x00" + num
		if _, ok := x.byParentNumber[key]; ok {
			x.ambiguous[key] = true
		} else {
			x.byParentNumber[key] = id
		}
	}
}

// lookup 按来源 ID → (父, 标题) → (父, 编号) 依次匹配既有篇目。
// **条目自身声明了来源 ID 时只用来源 ID**：来源 ID 未命中就新建，绝不按标题回退——
// 同一来源的两条记录来源 ID 不同即两个不同篇目，标题相同不代表同一条。
func (x *importerContentUnitIndex) lookup(ce ImporterCanonicalEntryPreview, parentID string) (string, bool) {
	external := stringScalarMap(ce.ExternalIDs)
	declaredExternal := false
	for k, v := range external {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		declaredExternal = true
		if id, ok := x.byExternal[strings.ToLower(k)+"|"+strings.ToLower(v)]; ok {
			return id, true
		}
	}
	if declaredExternal {
		return "", false
	}
	if tk := normalizeImporterTitleKey(ce.Title); tk != "" {
		key := parentID + "\x00" + tk
		if id, ok := x.byParentTitle[key]; ok && !x.ambiguous[key] {
			return id, true
		}
	}
	// 编号键只在**无 entry_role** 的条目间生效：本篇与 OP/ED 的集数各自从 1 起算，
	// 同父同号不同 role 会被误并。
	if num := strings.TrimSpace(ce.Number); num != "" && strings.TrimSpace(ce.EntryRole) == "" {
		key := parentID + "\x00" + num
		if id, ok := x.byParentNumber[key]; ok && !x.ambiguous[key] {
			return id, true
		}
	}
	return "", false
}

func (x *importerContentUnitIndex) remember(ce ImporterCanonicalEntryPreview, parentID, id string) {
	external := stringScalarMap(ce.ExternalIDs)
	declared := false
	for k, v := range external {
		if v = strings.TrimSpace(v); v != "" {
			declared = true
			x.byExternal[strings.ToLower(k)+"|"+strings.ToLower(v)] = id
		}
	}
	if declared {
		return
	}
	x.rememberTitleKeys(parentID, ce.Title, ce.Number, id)
}

// validateImporterEntryTree 校验 canonical entries 的 parent_index：只允许指向
// **前面**的条目（顺序即拓扑序，先父后子）。越界、负数、自指、指向后继都报错，
// 不再静默把节点降为顶层（那会丢失层级且无告警）。
func validateImporterEntryTree(entries []ImporterCanonicalEntryPreview) error {
	for i, ce := range entries {
		if ce.ParentIndex == nil {
			continue
		}
		idx := *ce.ParentIndex
		if idx < 0 {
			continue // -1 表示顶层，合法
		}
		if idx >= len(entries) {
			return fmt.Errorf("invalid_parent_index")
		}
		if idx >= i {
			// 指向自身或后继：顺序不构成有效拓扑序（先子后父）。
			return fmt.Errorf("invalid_parent_index")
		}
		if strings.TrimSpace(entries[idx].EntryKind) != "content_unit" {
			return fmt.Errorf("invalid_parent_index")
		}
	}
	return nil
}

func originalLanguageOrEmpty(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if _, err := language.Parse(v); err != nil {
		return ""
	}
	return v
}

// stringScalarMap 只收录标量值，保证 Entity.ExternalIDs 全为字符串。
// int/int64 也收录：预览 DTO 以 Go 结构体构造时 ID 是 int，
// 若只认 float64 会静默丢弃外部 ID（JSON 往返场景才会变成 float64）。
func stringScalarMap(in map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			out[k] = strconv.Itoa(x)
		case int64:
			out[k] = strconv.FormatInt(x, 10)
		case bool:
			out[k] = strconv.FormatBool(x)
		}
	}
	return out
}

func importerEvidence(req ImporterImportRequest, source string) (string, []Source) {
	note := strings.TrimSpace(req.EditNote)
	if note == "" {
		note = "从 " + source + " 导入"
		if id := strings.TrimSpace(req.URLOrID); id != "" {
			if len(id) > 120 {
				id = id[:120]
			}
			note += " " + id
		}
	}
	sources := []Source{}
	for _, raw := range req.SourceURLs {
		u := strings.TrimSpace(raw)
		if validURL(u) {
			sources = append(sources, Source{Kind: "url", Citation: u, URL: u})
		}
	}
	return note, sources
}

// importerApplyFieldSwitches 收口四个此前"只有声明、没有读取点"的字段，返回可能被改写的请求：
//   - is_master_verified=true：目录模型没有"核验主版"这一维（状态只有 draft/pending_review/
//     published/deleted/merged），接受后无法记录，因此明确拒绝而不是静默忽略；
//   - media_type_hint 非空：来源解析按 URL/ID 判定媒介类型，不接受调用方覆盖，同样明确拒绝；
//   - has_release=true 但没有 mediums：写路径会跳过整条发行链、把 release 载荷静默丢弃，拒绝；
//     （反向的"mediums 非空、has_release 缺省/false"以载荷为准：mediums 是写指令，has_release
//     只是预览侧声明，且 false 与缺省在 DTO 里不可区分，不能据此拒绝。
//     缺省 has_release 却带非空 release 的同一类残留按载荷侧对象判，见 importerReleaseRequiresMediums。）
//   - download_cover 显式 false：本服务不抓取、不转存图片（转存归存储子系统），字段只决定
//     是否把远端封面/头像作为 Picture 引用写库；未传或 true 保持既有透传行为。
func importerApplyFieldSwitches(req ImporterImportRequest) (ImporterImportRequest, error) {
	if req.HasRelease && len(req.Mediums) == 0 {
		return req, fmt.Errorf("invalid_payload: has_release=true requires mediums")
	}
	if req.DownloadCover != nil && !*req.DownloadCover {
		req = importerWithoutRemotePictures(req)
	}
	return req, nil
}

// importerWithoutRemotePictures 清掉载荷里所有远端封面/头像 URL（work 封面、release 封面、
// 顶层 artist 头像、staff 关联头像）。只改本次请求的副本，不动调用方数据。
func importerWithoutRemotePictures(req ImporterImportRequest) ImporterImportRequest {
	if req.Work != nil {
		w := *req.Work
		w.CoverImageURL = ""
		req.Work = &w
	}
	if req.Release != nil {
		r := *req.Release
		r.CoverImageURL = ""
		req.Release = &r
	}
	if req.Artist != nil {
		a := *req.Artist
		a.AvatarURL = ""
		req.Artist = &a
	}
	if len(req.Artists) > 0 {
		artists := make([]ImporterArtistPreview, len(req.Artists))
		for i, a := range req.Artists {
			a.AvatarURL = ""
			artists[i] = a
		}
		req.Artists = artists
	}
	if len(req.StaffAssociations) > 0 {
		assocs := make([]ImporterStaffAssociation, len(req.StaffAssociations))
		for i, a := range req.StaffAssociations {
			a.AvatarURL = ""
			assocs[i] = a
		}
		req.StaffAssociations = assocs
	}
	return req
}

// importDedupKey 由 url_or_id 本地解析幂等键，不发网络请求。
func importDedupKey(source string, req ImporterImportRequest, entityType string) (string, bool) {
	switch source {
	case "bangumi":
		ref, err := resolveBangumiRef(req.URLOrID, entityType)
		if err != nil {
			return "", false
		}
		return "bangumi:" + ref.Kind + ":" + strconv.Itoa(ref.ID), true
	case "dlsite":
		ref, err := parseDLsiteRef(req.URLOrID)
		if err != nil {
			return "", false
		}
		return "dlsite:work:" + ref.ProductID, true
	case "dmm":
		ref, err := parseDMMRef(req.URLOrID)
		if err != nil {
			return "", false
		}
		return "dmm:work:" + ref.CID, true
	default:
		return "", false
	}
}

func splitDedupKey(key string) (kind, id string) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return "", ""
	}
	return parts[1], parts[2]
}

// importerAuthorityPair 从导入幂等键推导应写入 external_ids 的权威库码与值。
// bangumi 的 subject/person/character 分别对应 bangumi / bangumi_person / bangumi_character；
// dlsite、dmm 的 work 直接对应同名权威库，其余种类加后缀，保证外部身份可追溯。
func importerAuthorityPair(key string) (code, id string) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return "", ""
	}
	source, kind, v := parts[0], parts[1], parts[2]
	switch source {
	case "bangumi":
		switch kind {
		case "subject":
			return "bangumi", v
		case "person":
			return "bangumi_person", v
		case "character":
			return "bangumi_character", v
		}
	case "dlsite":
		if kind == "work" {
			return "dlsite", v
		}
		return "dlsite_" + kind, v
	case "dmm":
		if kind == "work" {
			return "dmm", v
		}
		return "dmm_" + kind, v
	}
	return "", ""
}

// importerKeyPageURL 把导入幂等键还原为来源条目页，作为图片 Source.URL，便于考据。
func importerKeyPageURL(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return ""
	}
	source, kind, id := parts[0], parts[1], parts[2]
	switch source {
	case "bangumi":
		switch kind {
		case "subject":
			return "https://bgm.tv/subject/" + id
		case "person":
			return "https://bgm.tv/person/" + id
		case "character":
			return "https://bgm.tv/character/" + id
		}
	case "dlsite":
		if kind == "work" {
			site := "maniax"
			if strings.HasPrefix(id, "BJ") || strings.HasPrefix(id, "VJ") {
				site = "books"
			}
			return "https://www.dlsite.com/" + site + "/work/=/product_id/" + id + ".html"
		}
		if kind == "circle" {
			return "https://www.dlsite.com/maniax/circle/profile/=/maker_id/" + id + ".html"
		}
	case "dmm":
		if kind == "work" {
			// cid 本身反不出频道，数字同人详情页可识别任意 cid 并自动归位。
			return "https://www.dmm.co.jp/dc/doujin/-/detail/=/cid=" + id + "/"
		}
	}
	return ""
}

// findImported 按内部幂等键找已导入实体：merged 行跟随重定向到存活实体
// （M04：此前查询过滤了 merged/deleted，下面的 merged→Resolve 分支永远命中不了，
// 带导入键的源合入无键目标后重导会建重复）。deleted 是终态，在此视为未命中
// （墓碑行仍占着唯一索引，直接新建会撞键；恢复/清理墓碑走手工流程，不在此复活）。
func (s *Store) findImported(ctx context.Context, key string, actor *User) (Entity, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Entity{}, false
	}
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM catalog.entities WHERE document->'external_ids'->>'metafusion_import'=$1 LIMIT 1`, key).Scan(&id); err != nil {
		return Entity{}, false
	}
	e, err := s.Get(ctx, id, actor)
	if err != nil {
		return Entity{}, false
	}
	if e.Status == "deleted" {
		return Entity{}, false
	}
	// 合并后重导：命中已合入他处的旧身份时跟随重定向到存活实体，
	// 避免在旧 ID 旁新建一份重复（Resolve 只跟 merged 链，不改可见性语义）。
	if e.Status == "merged" {
		if r, rerr := s.Resolve(ctx, e.ID, actor); rerr == nil {
			return r, true
		}
		return Entity{}, false
	}
	return e, true
}

// deletedImportTombstone 按内部幂等键找已删除墓碑（删后禁自动重导的定位依据）：
// 墓碑仍占唯一索引，调用方在此直接返回稳定业务错误（见 deletedImportError），
// 不建新行撞唯一索引、不删墓碑、不自动复活。返回的 id 仅用于恢复入口定位。
func (s *Store) deletedImportTombstone(ctx context.Context, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM catalog.entities WHERE document->'external_ids'->>'metafusion_import'=$1 AND status='deleted' LIMIT 1`, key).Scan(&id); err != nil {
		return "", false
	}
	return id, true
}

// deletedImportError 是删后禁自动重导的稳定业务错误：墓碑占位中，自动重导直接拒绝
// （import_deleted），恢复走显式手工流程（按墓碑 id 找回条目处理），不删墓碑、
// 不自动复活、不撞唯一索引。错误码与响应体同值（码: 细节，前端按冒号取码）。
func deletedImportError(key, tombstoneID string) error {
	return fmt.Errorf("import_deleted: %s（墓碑 %s 占位中，自动重导已禁用；如需恢复请按墓碑走显式手工流程）", key, tombstoneID)
}

// findAgentByTitle 按标题精确匹配已有可见 agent（无外部键的手工载荷去重用）。
// 只做大小写不敏感的精确匹配：normalizeImporterTitleKey 折叠空白与大小写，
// SQL 侧用 lower(title)=lower($1) 命中后再由调用方按折叠键确认。
// 不同大小写/空白变体命中同一行即复用，避免"MyGO!!!!!"与"mygo!!!!!"各建一份。
func (s *Store) findAgentByTitle(ctx context.Context, title string, actor *User) (Entity, bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Entity{}, false
	}
	var id, dbTitle string
	if err := s.DB.QueryRowContext(ctx, `SELECT id, title FROM catalog.entities WHERE kind='agent' AND lower(title)=lower($1) AND status NOT IN ('deleted','merged') LIMIT 1`, title).Scan(&id, &dbTitle); err != nil {
		return Entity{}, false
	}
	if normalizeImporterTitleKey(dbTitle) != normalizeImporterTitleKey(title) {
		return Entity{}, false
	}
	e, err := s.Get(ctx, id, actor)
	if err != nil {
		return Entity{}, false
	}
	return e, true
}

func (s *Store) importerSave(ctx context.Context, e Entity, actor User, note string, sources []Source) (Entity, error) {
	return s.importerSaveVersioned(ctx, e, 0, actor, note, sources)
}

// importerSaveVersioned 与 importerSave 相同，但显式带乐观锁版本。
// 更新已存在实体时必须传其当前版本，否则 Save 会以 version_conflict 拒绝。
//
// 并发双插兜底：幂等键唯一索引（entities_metafusion_import_key，见结构基线）
// 会让后到者在 Save 提交时拿到 23505（constraint_violation）。此处不吞该错误——
// 调用方（Import 重试）应按幂等键复用已建实体再继续补齐，而不是静默成功掩盖
// "本次新建未发生"的事实。直接返回错误即保留该语义。
func (s *Store) importerSaveVersioned(ctx context.Context, e Entity, expectedVersion int64, actor User, note string, sources []Source) (Entity, error) {
	if e.Translations == nil {
		e.Translations = map[string]Translation{}
	}
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	if e.ExternalIDs == nil {
		e.ExternalIDs = map[string]string{}
	}
	// internal=true：导入链路是内部幂等键的唯一合法写入者（见 validation.go 的 guardImportKey）。
	return s.Save(ctx, Edit{Entity: e, ExpectedVersion: expectedVersion, EditNote: note, Sources: sources, internal: true}, actor)
}

// assocImportKey 取关联项的导入键，**必须与落库的 external_ids.metafusion_import 格式一致**
// （findImported 按该字段查询）。键由服务端从来源 ID 派生：bangumi_person/character 拼成
// `bangumi:{kind}:{id}`（与落库格式一致）；载荷自带的 metafusion_import 只是预览回带的副本，
// 只在派生不出键时兜底——冲突时以派生键为准，否则载荷能用别人的幂等键把实体写进另一条链路。
// 曾因返回 `bangumi_person:{id}`（下划线）与落库的 `bangumi:person:{id}` 不匹配，
// 导致重复导入每次都新建一份实体。
func assocImportKey(externalIDs map[string]any) string {
	for _, k := range []string{"bangumi_character", "bangumi_person"} {
		if v, ok := externalIDs[k]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return "bangumi:" + strings.TrimPrefix(k, "bangumi_") + ":" + s
			}
		}
	}
	// DLsite 社团按权威库 dlsite_maker（RG id）服务端派生，与落库键格式一致；
	// 不直接采信载荷里的 metafusion_import，防止用别人的幂等键串到另一条链路。
	if v, ok := externalIDs["dlsite_maker"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			return "dlsite:circle:" + s
		}
	}
	if v, ok := externalIDs["metafusion_import"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			return s
		}
	}
	return ""
}

// importerEntryExternalIDs 归一表达要落库的 external_ids：派生键（条目签名键）非空即覆盖
// 载荷自带值，载荷的 metafusion_import 只在无来源身份（派生键为空）时保留，供同值写回。
func importerEntryExternalIDs(ce ImporterCanonicalEntryPreview, importKey string) map[string]string {
	out := stringScalarMap(ce.ExternalIDs)
	if importKey = strings.TrimSpace(importKey); importKey != "" {
		out["metafusion_import"] = importKey
	}
	return out
}

// importerAgentExternalIDs 归一关联 agent 要落库的 external_ids：同上，metafusion_import 以
// assocImportKey 的派生结果为准（同值写回不受影响），其余来源键原样保留。
func importerAgentExternalIDs(externalIDs map[string]any) map[string]string {
	out := stringScalarMap(externalIDs)
	if key := assocImportKey(externalIDs); key != "" {
		out["metafusion_import"] = key
	}
	return out
}

// assocAgentDedup 复算第一趟使用的去重键，保证两趟指向同一 agent。
func assocAgentDedup(a ImporterStaffAssociation) string {
	if tid := strings.TrimSpace(a.TargetArtistID); tid != "" {
		return "id:" + tid
	}
	if k := assocImportKey(a.ExternalIDs); k != "" {
		return k
	}
	return "name:" + strings.ToLower(strings.TrimSpace(a.ParsedName)) + "|" + staffAgentType(a.EntityType)
}

// importerRelationSkippable 判断关系写入失败是否属于外部数据形态导致的既定跳过。
// 只收窄到"重复/端点不匹配"这类明确的外部形态问题：
//   - duplicate_relation：同一载荷内重复边（去重键已尽力，残留的由服务端判重）；
//   - invalid_endpoints：关联端点结构层级不在该关系定义内。
//
// 以下一律不吞（调用方直接返回错误，避免掩盖真实完整性冲突）：
//   - invalid_relation_type：关系码本身不存在/被禁用，须由预检提前暴露；
//   - cardinality_exceeded / relation_cycle：基数与无环是数据完整性约束，
//     吞掉会静默丢边且让用户以为导入成功，必须显式失败。
func importerRelationSkippable(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case "duplicate_relation", "invalid_endpoints":
		return true
	}
	return false
}

func applyWorkSummary(e *Entity, summary string) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	// Entity 没有简介列，简介只能落在翻译行。
	if e.Translations == nil {
		e.Translations = map[string]Translation{}
	}
	if e.OriginalLanguage != "" {
		tr := e.Translations[e.OriginalLanguage]
		if tr.Title == "" {
			tr.Title = e.Title
		}
		tr.Summary = summary
		e.Translations[e.OriginalLanguage] = tr
		return
	}
	if len(e.Translations) == 1 {
		for k, tr := range e.Translations {
			tr.Summary = summary
			e.Translations[k] = tr
		}
		return
	}
	// 实体原语言未知且没有可归属的翻译行时（常见于导入的 agent），
	// 按简介**自身文字**选择语种行：假名→ja、汉字→zh-CN。
	// 这里只决定简介存放在哪一行，不改写 original_language（不把"简介的语言"
	// 冒充成实体原语言）；实在无法判定才放弃，避免丢失上游已有的简介。
	loc := detectTextLocale(summary)
	if loc == "" {
		return
	}
	tr := e.Translations[loc]
	if tr.Title == "" {
		tr.Title = e.Title
	}
	tr.Summary = summary
	e.Translations[loc] = tr
}

// detectTextLocale 按文本自身文字判断语种：含假名→ja，含汉字→zh-CN，其余为空。
// 仅用于给简介找一个存放的翻译行，不代表实体原语言。
func detectTextLocale(s string) string {
	if detectJapaneseScript(s) == "ja" {
		return "ja"
	}
	if hasHan(s) {
		return "zh-CN"
	}
	return ""
}

// pictureFromRemote 把外部目录的远端图片 URL 透传为 Picture（不抓取、不转存）。
// Source.URL 优先用对应条目的公开页面（可考据），取不到时回退图片 URL。
func pictureFromRemote(imageURL, citation, key string, hasKey bool) (Picture, bool) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" || !validURL(imageURL) {
		return Picture{}, false
	}
	pageURL := imageURL
	if hasKey {
		if u := importerKeyPageURL(key); u != "" {
			pageURL = u
		}
	}
	if !validURL(pageURL) {
		pageURL = imageURL
	}
	return Picture{URL: imageURL, Caption: Names{}, Source: Source{Kind: "url", Citation: citation, URL: pageURL}}, true
}

// applyAliasesByScript 把异名按自身文字特征归入对应语种翻译行并返回是否有变化。
// 假名 → ja；含汉字 → zh-CN；拉丁/无法判定 → 跳过（不硬塞进任何语种，遵循
// AGENTS.md：不能把其它语种题名塞进原语言行）。已存在的标题与别名去重。
func applyAliasesByScript(e *Entity, aliases []string) bool {
	if e == nil || len(aliases) == 0 {
		return false
	}
	if e.Translations == nil {
		e.Translations = map[string]Translation{}
	}
	changed := false
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		loc := ""
		switch {
		case detectJapaneseScript(a) == "ja":
			loc = "ja"
		case hasHan(a):
			loc = "zh-CN"
		default:
			continue // 拉丁等无法判定语种，丢弃而不猜
		}
		tr := e.Translations[loc]
		// 该语种即实体原语言时，实体标题就是它的主标题，别名不得顶替。
		if strings.TrimSpace(tr.Title) == "" && loc == e.OriginalLanguage {
			tr.Title = e.Title
		}
		// 与实体主标题、该语种标题、已有别名重复的一律跳过。
		if a == e.Title || a == tr.Title || contains(tr.Aliases, a) {
			continue
		}
		// 校验要求每个语种行标题非空：该语种还没有标题时，用首个异名充当其主标题
		//（Bangumi 别名里常含该语种的正式译名），否则整条校验会以 invalid_translation 拒绝。
		if strings.TrimSpace(tr.Title) == "" {
			tr.Title = a
		} else {
			tr.Aliases = append(tr.Aliases, a)
		}
		e.Translations[loc] = tr
		changed = true
	}
	return changed
}

// hasHan 判断是否含CJK统一表意文字（用于把纯汉字异名归入中文行）。
func hasHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// importerReleaseMeta 取发行预览声明的原语言与翻译行（rel 为 nil 时为空）。
// 新建与"幂等命中补齐"共用同一口径，避免两处各解析一遍。
func importerReleaseMeta(rel *ImporterReleasePreview) (string, map[string]Translation) {
	if rel == nil {
		return "", nil
	}
	return originalLanguageOrEmpty(rel.OriginalLanguage), importerTranslationsFromAny(rel.Translations)
}

// mergeImporterMeta 为已存在的实体（发行/载体）补齐缺失的原语言与翻译行，返回结果与是否有变化。
// 与 mergeWorkMetadata/mergeAgentMetadata 同口径：只在缺值时写、**不覆盖**已有行——
// 人工修订过的译名不该被上游下一次导入改回去；载荷新增的语种行则补齐，保证
// "载荷声明过的语言字段不会因为这次是复用分支就被丢掉"。
func mergeImporterMeta(existing Entity, lang string, translations map[string]Translation) (Entity, bool) {
	changed := false
	if existing.OriginalLanguage == "" {
		if lang = originalLanguageOrEmpty(lang); lang != "" {
			existing.OriginalLanguage = lang
			changed = true
		}
	}
	for loc, tr := range translations {
		if _, exists := existing.Translations[loc]; exists {
			continue
		}
		if existing.Translations == nil {
			existing.Translations = map[string]Translation{}
		}
		existing.Translations[loc] = tr
		changed = true
	}
	return existing, changed
}

// mergeAgentMetadata 为已存在的 agent 补齐/纠正元数据，返回结果与是否有变化。
// 只在原值为空时补齐；简介与语言同理只在缺失时写。
func mergeAgentMetadata(existing Entity, assoc ImporterStaffAssociation) (Entity, bool) {
	changed := false
	if existing.OriginalLanguage == "" {
		if lang := originalLanguageOrEmpty(assoc.Language); lang != "" {
			existing.OriginalLanguage = lang
			changed = true
		}
	}
	if strings.TrimSpace(assoc.Biography) != "" {
		if !agentHasSummary(existing) {
			applyWorkSummary(&existing, assoc.Biography)
			changed = true
		}
	}
	if len(existing.Pictures) == 0 {
		if p, ok := pictureFromRemote(assoc.AvatarURL, "导入头像", "", false); ok {
			existing.Pictures = PicturesJSON{p}
			changed = true
		}
	}
	return existing, changed
}

// agentHasSummary 判断 agent 是否已有任何简介（任一翻译行的 summary 非空）。
func agentHasSummary(e Entity) bool {
	for _, tr := range e.Translations {
		if strings.TrimSpace(tr.Summary) != "" {
			return true
		}
	}
	return false
}

// mergeWorkMetadata 为已存在的 work 补齐缺失元数据；返回合并结果与是否有变化。
// 只在原值为空时填入，绝不覆盖已有值（保护人工编辑）。封面同理：无图才补。
func mergeWorkMetadata(existing Entity, w *ImporterWorkPreview, dateField string) (Entity, bool) {
	if w == nil {
		return existing, false
	}
	changed := false
	if existing.OriginalLanguage == "" {
		if lang := originalLanguageOrEmpty(w.OriginalLanguage); lang != "" {
			existing.OriginalLanguage = lang
			changed = true
		}
	}
	// 属性补齐：仅限字段自身声明适用于 work 的字段，且只在缺值时写入。
	if existing.Attributes == nil {
		existing.Attributes = map[string]any{}
	}
	if dateField != "" {
		if _, ok := existing.Attributes[dateField]; !ok {
			if d := cleanImporterDate(w.ReleaseDate); d != "" {
				existing.Attributes[dateField] = d
				changed = true
			}
		}
	}
	// 品番（catalog_number）是发行层标识，不写作品层；由发行属性改写补入 Release。
	// 标签与 infobox 派生字段：只在缺失时补，绝不覆盖已编目的值。
	if _, ok := existing.Attributes["tags"]; !ok && len(w.Tags) > 0 {
		existing.Attributes["tags"] = toAnySlice(w.Tags)
		changed = true
	}
	for k, v := range w.Fields {
		// 产品标识（条码/ISBN）归发行层，由发行属性改写，不写作品。
		if isReleaseLevelField(k) {
			continue
		}
		if _, ok := existing.Attributes[k]; ok {
			continue
		}
		if val, ok := dynamicFieldValue(v); ok {
			existing.Attributes[k] = val
			changed = true
		}
	}
	if _, ok := existing.Attributes["infobox"]; !ok && len(w.Infobox) > 0 {
		existing.Attributes["infobox"] = toAnySlice(w.Infobox)
		changed = true
	}
	// 外部 ID：官网等只在缺失时补。
	if existing.ExternalIDs == nil {
		existing.ExternalIDs = map[string]string{}
	}
	if v, ok := w.CatalogMetadata.(map[string]any); ok {
		if _, ok := existing.ExternalIDs["official_website"]; !ok {
			if site := scalarString(v["official_website"]); site != "" {
				existing.ExternalIDs["official_website"] = site
				changed = true
			}
		}
	}
	// 翻译行：补原语言行标题；别名按语种分派（去重），不覆盖已有标题/简介。
	if lang := existing.OriginalLanguage; lang != "" {
		tr := existing.Translations[lang]
		if tr.Title == "" {
			tr.Title = existing.Title
			changed = true
		}
		existing.Translations[lang] = tr
	}
	changed = applyAliasesByScript(&existing, w.Aliases) || changed
	if len(existing.Pictures) == 0 {
		if p, ok := pictureFromRemote(w.CoverImageURL, "Bangumi 条目封面", "", false); ok {
			existing.Pictures = PicturesJSON{p}
			changed = true
		}
	}
	return existing, changed
}

// buildWorkEntity 组装 work 实体；dateField 由已发布字段定义决定。
func buildWorkEntity(w *ImporterWorkPreview, source, key, sourceID string, hasKey bool, dateField string) (Entity, error) {
	if w == nil || strings.TrimSpace(w.Title) == "" {
		return Entity{}, fmt.Errorf("invalid_payload")
	}
	e := Entity{
		Kind:             "work",
		Title:            strings.TrimSpace(w.Title),
		OriginalLanguage: originalLanguageOrEmpty(w.OriginalLanguage),
		Translations:     toEntityTranslations(w.Translations),
		ExternalIDs:      map[string]string{},
		Attributes:       map[string]any{},
	}
	if lang := strings.TrimSpace(w.Language); lang != "" {
		e.Attributes["language"] = lang
	}
	if d := cleanImporterDate(w.ReleaseDate); d != "" && dateField != "" {
		e.Attributes[dateField] = d
	}
	if len(w.Tags) > 0 {
		e.Attributes["tags"] = toAnySlice(w.Tags)
	}
	for k, v := range w.Fields {
		if isReleaseLevelField(k) {
			continue
		}
		if val, ok := dynamicFieldValue(v); ok {
			e.Attributes[k] = val
		}
	}
	if len(w.Infobox) > 0 {
		e.Attributes["infobox"] = toAnySlice(w.Infobox)
	}
	if hasKey {
		e.ExternalIDs["metafusion_import"] = key
		if code, id := importerAuthorityPair(key); code != "" && id != "" {
			e.ExternalIDs[code] = id
		}
	} else if strings.TrimSpace(sourceID) != "" {
		e.ExternalIDs[source] = strings.TrimSpace(sourceID)
	}
	// 官网来自 infobox，落 external_ids（前端 official_website 读这里，可考据且不占用类型字段）。
	// 品番不写作品层：它是发行标识，由发行属性改写补入 Release。
	if v, ok := w.CatalogMetadata.(map[string]any); ok {
		if site := scalarString(v["official_website"]); site != "" {
			e.ExternalIDs["official_website"] = site
		}
	}
	// infobox 别名按**自身语种**归入对应翻译行：假名→ja，含汉字→中文行。
	// 不能无差别塞进原语言行，否则会把中日异名混在同一语种（AGENTS.md 语义）。
	applyAliasesByScript(&e, w.Aliases)
	applyWorkSummary(&e, w.Summary)
	if p, ok := pictureFromRemote(w.CoverImageURL, "导入条目封面", key, hasKey); ok {
		e.Pictures = PicturesJSON{p}
	}
	return e, nil
}

func agentTypeForPreviewValue(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "person", "organization", "group", "character":
		return strings.ToLower(strings.TrimSpace(v))
	case "studio", "publisher", "company", "label", "circle":
		return "organization"
	default:
		return ""
	}
}

func buildAgentEntity(name, originalName, biography, avatarURL, lang, entityType string, translations []ImporterTranslationItem, externalIDs map[string]any, key string, hasKey bool) (Entity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Entity{}, fmt.Errorf("invalid_payload")
	}
	e := Entity{
		Kind:             "agent",
		Title:            name,
		OriginalLanguage: originalLanguageOrEmpty(lang),
		Translations:     toEntityTranslations(translations),
		ExternalIDs:      stringScalarMap(externalIDs),
		Attributes:       map[string]any{},
	}
	if p, ok := pictureFromRemote(avatarURL, "导入头像", key, hasKey); ok {
		e.Pictures = PicturesJSON{p}
	}
	if strings.TrimSpace(originalName) != "" && strings.TrimSpace(originalName) != name {
		if e.OriginalLanguage != "" {
			tr := e.Translations[e.OriginalLanguage]
			if tr.Title == "" {
				tr.Title = originalName
				e.Translations[e.OriginalLanguage] = tr
			}
		}
	}
	if strings.TrimSpace(biography) != "" {
		applyWorkSummary(&e, biography)
	}
	if hasKey {
		e.ExternalIDs["metafusion_import"] = key
		if kind, id := splitDedupKey(key); kind != "" && id != "" {
			e.ExternalIDs["bangumi_"+kind] = id
		}
	}
	return e, nil
}

// staffAgentType 把演职员行的自由文本类型收敛到 agent 定义类型。
func staffAgentType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "organization", "group", "character":
		return strings.ToLower(strings.TrimSpace(v))
	case "studio", "publisher", "company", "label", "circle":
		return "organization"
	default:
		return "person"
	}
}

var importerMediumFormats = map[string]string{
	"cd": "cd", "bd": "bd", "blu-ray": "bd", "bluray": "bd", "uhd_bd": "uhd_bd",
	"dvd": "dvd", "vinyl": "vinyl", "lp": "vinyl", "sacd": "sacd",
	"cassette": "cassette", "tape": "cassette", "paper": "paper", "book": "paper",
	"digital": "digital", "mp3": "digital", "flac": "digital", "web": "web",
}

// importerMediumRoles 是载体 role 属性的允许值收敛：与 definitions 的 role 词表
// 同口径（见 defaults.go 的 role terms：primary/supplement/side/extra/commentary）。
// importerMediumAttrs 只从这份白名单取值；后台若改动词表，前置校验
// （importerPreflightCodeCheck）会先报错而不是落库时才报 unknown_term。
var importerMediumRoles = []string{"primary", "supplement", "side", "extra", "commentary"}

func importerEnum(v string, allowed []string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if contains(allowed, v) {
		return v
	}
	return ""
}

// normalizeImporterTitleKey 表达对齐的标题键：小写并折叠空白（含全角空格），
// 只用于匹配，不落库。
func normalizeImporterTitleKey(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// listWorkExpressions 取全 work 下既有表达，供导入对齐复用，避免重复录音。
// 必须用 ListAll：底层 List 会把 >100 的 Limit 收敛为 50，直接传 500 并据长度
// 判断"取完"只会在 50 条处提前终止。
func (s *Store) listWorkExpressions(ctx context.Context, workID string, u *User) ([]Entity, error) {
	return s.ListAll(ctx, ListOptions{Kind: "expression", WorkID: workID}, u)
}

// listWorkContentUnits 取全 work 下既有篇目，供导入按标题去重（同样走 ListAll，
// 避免 List 的 Limit>100→50 收敛导致只看到前 50 条）。
func (s *Store) listWorkContentUnits(ctx context.Context, workID string, u *User) ([]Entity, error) {
	return s.ListAll(ctx, ListOptions{Kind: "content_unit", WorkID: workID}, u)
}

// createExpression 建单个 expression（canonical entry 或曲目回退）。
// contentUnitID 非空时表达归属该篇目（Work → ContentUnit → Expression）。
func (s *Store) createExpression(ctx context.Context, actor User, note string, sources []Source, workID, contentUnitID, title, number string, pos int, duration float64, externalIDs map[string]any) (Entity, error) {
	exprAttrs := map[string]any{}
	if duration > 0 {
		exprAttrs["duration"] = duration
	}
	return s.importerSave(ctx, Entity{
		Kind:          "expression",
		Title:         title,
		WorkID:        workID,
		ContentUnitID: contentUnitID,
		Number:        strings.TrimSpace(number),
		Position:      pos,
		Attributes:    exprAttrs,
		ExternalIDs:   stringScalarMap(externalIDs),
	}, actor, note, sources)
}

// importerEntryExpressionAttrs 计算表达本体属性；写路径与预检共用。
func importerEntryExpressionAttrs(ce ImporterCanonicalEntryPreview) map[string]any {
	attrs := map[string]any{}
	for k, v := range ce.Attributes {
		if v != nil {
			attrs[k] = v
		}
	}
	if ce.DurationSeconds > 0 {
		attrs["duration"] = ce.DurationSeconds
	}
	return attrs
}

// createExpressionWithMeta 从 canonical entry 建表达，除标题/编号/时长/外部编号外
// 一并落多语言与原语言（旧实现漏掉这些字段，导致预览里已解析的翻译丢失）。
// importKey 非空时写入 external_ids.metafusion_import，供同一清单重试时幂等复用。
func (s *Store) createExpressionWithMeta(ctx context.Context, actor User, note string, sources []Source, workID, contentUnitID, title string, ce ImporterCanonicalEntryPreview, pos int, importKey string) (Entity, error) {
	exprAttrs := importerEntryExpressionAttrs(ce)
	externalIDs := importerEntryExternalIDs(ce, importKey)
	return s.importerSave(ctx, Entity{
		Kind:             "expression",
		Title:            title,
		WorkID:           workID,
		ContentUnitID:    contentUnitID,
		Number:           strings.TrimSpace(ce.Number),
		Position:         pos,
		Attributes:       exprAttrs,
		ExternalIDs:      externalIDs,
		Translations:     importerTranslationsFromAny(ce.Translations),
		OriginalLanguage: originalLanguageOrEmpty(ce.OriginalLanguage),
	}, actor, note, sources)
}

// releaseLevelFromWork 列出"上游放在作品条目里、但语义属于发行层"的字段键：
// 品番（catalog_number）与条码/ISBN（barcode）都是**具体产品标识**，按元数据模型
// 归 Release/Medium（同一个作品的不同发行各有品番）。上游资料表以作品为主表，
// 这些值会跟着作品条目进来，落库时改写到 Release，作品层不再保留。
var releaseLevelFromWork = []string{"catalog_number", "barcode"}

// isReleaseLevelField 判定某 infobox 派生键是否应改写发行层而非留在作品上。
func isReleaseLevelField(code string) bool {
	return slices.Contains(releaseLevelFromWork, code)
}

// releaseLevelValues 收集同一载荷里属于发行层、需要改写的产品标识值。
// catalog_number 来自作品预览的 catalog_metadata，条码/ISBN 来自 infobox 派生字段。
func releaseLevelValues(work *ImporterWorkPreview) map[string]any {
	if work == nil {
		return nil
	}
	out := map[string]any{}
	for _, k := range releaseLevelFromWork {
		if val, ok := dynamicFieldValue(work.Fields[k]); ok {
			out[k] = val
		}
	}
	if cm, ok := work.CatalogMetadata.(map[string]any); ok {
		if _, ok := out["catalog_number"]; !ok {
			if no := scalarString(cm["catalog_number"]); no != "" {
				out["catalog_number"] = no
			}
		}
	}
	return out
}

// 发行层枚举的写入口径：值先命中这些白名单才会写进 attributes（未命中的自由文本不虚构映射），
// 因此预检只按命中的值校验词表项（见 importerMappingUsageFromPayload）。命中后它们同时进入
// 发行版本签名（importerReleaseVariantKey），互为表里。
var (
	importerEditionTypes         = []string{"standard", "limited", "deluxe", "boxset"}
	importerEditionBatches       = []string{"regular", "first_press", "reissue", "reprint"}
	importerPackagings           = []string{"standard", "jewel", "slipcase", "box", "boxset", "digipak"}
	importerDistributionChannels = []string{"mixed", "physical", "digital", "web"}
)

// importerReleaseAttrs 从预览计算发行版属性。publisher 是自由文本、无法解析为
// Agent 引用（entity 类型），仍不写入；edition_type/edition_batch/packaging/
// distribution_channel 只有命中词表才写（未命中则丢弃该维度、不虚构），
// 命中后它们同时进入发行版本签名（importerReleaseVariantKey），互为表里。
//
// work 是同一载荷的作品预览：产品标识（品番/条码/ISBN）语义属发行层，上游却常把
// 它们放在作品条目里，因此这里把发行层缺的值补进来——只补发行层字段集内的键、
// 发行已有值时不覆盖。这样书本 ISBN 落到发行 barcode、专辑品番落到发行
// catalog_number，而不是留在作品层（defaults 已不再给 Work 声明这些字段）。
func importerReleaseAttrs(rel *ImporterReleasePreview, work *ImporterWorkPreview) map[string]any {
	out := map[string]any{}
	if rel != nil {
		if v := strings.TrimSpace(rel.CatalogNumber); v != "" {
			out["catalog_number"] = v
		}
		if v := strings.TrimSpace(rel.Barcode); v != "" {
			out["barcode"] = v
		}
		if v := strings.TrimSpace(rel.Country); v != "" {
			out["country"] = v
		}
		if v := cleanImporterDate(rel.EditionDate); v != "" {
			out["edition_date"] = v
		}
		// 版本维度只写词表命中的值：未命中的自由文本不虚构映射，
		// 由发行版本签名保留区分度（签名用原文，属性用词表项）。
		if v := importerEnum(rel.EditionType, importerEditionTypes); v != "" {
			out["edition_type"] = v
		}
		if v := importerEnum(rel.EditionBatch, importerEditionBatches); v != "" {
			out["edition_batch"] = v
		}
		if v := importerEnum(rel.Packaging, importerPackagings); v != "" {
			out["packaging"] = v
		}
		if v := importerEnum(rel.DistributionChannel, importerDistributionChannels); v != "" {
			out["distribution_channel"] = v
		}
	}
	for k, v := range releaseLevelValues(work) {
		if _, ok := out[k]; ok {
			continue
		}
		out[k] = v
	}
	return out
}

// importerMediumAttrs 从预览计算载体属性（format 与 role 均须在词表内）。
// role 白名单见 importerMediumRoles，与 definitions 的 role 词表同口径。
func importerMediumAttrs(m ImporterMediumPreview) map[string]any {
	out := map[string]any{}
	if f, ok := importerMediumFormats[strings.ToLower(strings.TrimSpace(m.Format))]; ok {
		out["format"] = f
	}
	if r := importerEnum(m.Role, importerMediumRoles); r != "" {
		out["role"] = r
	}
	return out
}

// importerTrackAttrs 从预览计算曲目属性。注意 ISRC 不在此处：它属于录音本体，
// 写入 expression.ExternalIDs；track 定义只声明 duration/role。
func importerTrackAttrs(t ImporterTrackPreview) map[string]any {
	out := map[string]any{}
	if t.DurationSeconds > 0 {
		out["duration"] = t.DurationSeconds
	}
	return out
}

// importerContentUnitAttrs 计算篇目属性：载荷声明的 attributes，加上 entry_role
// （篇目类型：本篇/OP/ED/预告）。集数编号在本篇与 OP 各自从 1 起算，仅凭编号或标题
// 无法区分，因此 entry_role 必须落库，否则预览里已识别的篇目类型会丢失。
// entry_role 只在目标实例已声明该字段时才写：旧实例的已发布定义尚未升级时写入会被
// unknown_field 拒绝，此时降级为不写而非让整条导入失败。
func importerContentUnitAttrs(fields map[string]bool, ce ImporterCanonicalEntryPreview) map[string]any {
	out := map[string]any{}
	for k, v := range ce.Attributes {
		if v != nil {
			out[k] = v
		}
	}
	if role := strings.TrimSpace(ce.EntryRole); role != "" && fields["entry_role"] {
		out["entry_role"] = role
	}
	return out
}

// importerFieldSet 按实体 kind 汇总字段自身的适用范围，用于写库前预检
// unknown_field，避免先建发行/载体再在曲目处失败留下半成品。
// 该预检只是提前失败，最终判定仍是 validateEntityContent，两边须同口径。
func importerFieldSet(defs Definitions, kind string) map[string]bool {
	set := map[string]bool{}
	for _, code := range defs.attributeKeys(kind) {
		set[code] = true
	}
	return set
}

// importerCheckAttrs 校验属性键都适用于目标结构层级。
func importerCheckAttrs(defs Definitions, kind string, attrs map[string]any) error {
	set := importerFieldSet(defs, kind)
	for k, v := range attrs {
		if v == nil {
			continue
		}
		if !set[k] {
			return fmt.Errorf("unknown_field: %s", k)
		}
		if kind == "release" && k == "barcode" {
			if raw, ok := v.(string); ok {
				if err := importerBarcodeError(raw); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// importerExplicitExpressions 解析载荷中所有显式指定的既有表达（canonical entries
// 与各轨），返回 id→实体。显式引用是用户选择，允许跨 Work（收录别的作品的录音），
// 只校验存在且 kind=expression。
func (s *Store) importerExplicitExpressions(ctx context.Context, actor User, entries []ImporterCanonicalEntryPreview, mediums []ImporterMediumPreview) (map[string]Entity, error) {
	out := map[string]Entity{}
	collect := func(raw string) error {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil
		}
		if _, ok := out[id]; ok {
			return nil
		}
		e, err := s.Get(ctx, id, &actor)
		if err != nil || e.Kind != "expression" {
			return fmt.Errorf("invalid_expression_reference")
		}
		out[id] = e
		return nil
	}
	for _, ce := range entries {
		if err := collect(ce.ExpressionID); err != nil {
			return nil, err
		}
	}
	for _, m := range mediums {
		for _, t := range m.Tracks {
			if err := collect(t.ExpressionID); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// importerMappingUsage 收集本次载荷**实际会用到**的写死映射：关系码与词表项。
// 取值口径必须与写路径同源（见 importerMappingUsageFromPayload）。
type importerMappingUsage struct {
	relations map[string]bool
	terms     map[string]map[string]bool
}

func (u *importerMappingUsage) relation(code string) {
	if code = strings.TrimSpace(code); code != "" {
		if u.relations == nil {
			u.relations = map[string]bool{}
		}
		u.relations[code] = true
	}
}

func (u *importerMappingUsage) term(vocab, term string) {
	vocab, term = strings.TrimSpace(vocab), strings.TrimSpace(term)
	if vocab == "" || term == "" {
		return
	}
	if u.terms == nil {
		u.terms = map[string]map[string]bool{}
	}
	if u.terms[vocab] == nil {
		u.terms[vocab] = map[string]bool{}
	}
	u.terms[vocab][term] = true
}

// importerMappingUsageFromPayload 从载荷推导本次会用到的关系码与词表项，口径与写路径一致：
//   - 关系码：staff_associations 的 relation_type（skip 项不落库，不计）+ create_relation 的目标码；
//   - 词表项：importerReleaseAttrs/importerMediumAttrs 白名单命中的发行/载体枚举值，
//     以及 entry_kind=content_unit 且目标实例声明了 entry_role 时写入的篇目角色。
//
// 只统计"确实会被写进库"的值：载荷里未命中白名单的自由文本本来就丢弃，不应要求词表项存在。
func importerMappingUsageFromPayload(req ImporterImportRequest, mode string, cuFields map[string]bool) importerMappingUsage {
	u := importerMappingUsage{}
	for _, a := range req.StaffAssociations {
		if strings.EqualFold(strings.TrimSpace(a.Action), "skip") {
			continue
		}
		u.relation(a.RelationType)
	}
	if mode == "create_relation" {
		u.relation(req.RelationType)
	}
	if rel := req.Release; rel != nil {
		if t := importerEnum(rel.EditionType, importerEditionTypes); t != "" {
			u.term("edition_type", t)
		}
		if t := importerEnum(rel.EditionBatch, importerEditionBatches); t != "" {
			u.term("edition_batch", t)
		}
		if t := importerEnum(rel.Packaging, importerPackagings); t != "" {
			u.term("packaging", t)
		}
		if t := importerEnum(rel.DistributionChannel, importerDistributionChannels); t != "" {
			u.term("distribution_channel", t)
		}
	}
	for _, m := range req.Mediums {
		if f, ok := importerMediumFormats[strings.ToLower(strings.TrimSpace(m.Format))]; ok {
			u.term("format", f)
		}
		if r := importerEnum(m.Role, importerMediumRoles); r != "" {
			u.term("role", r)
		}
	}
	if cuFields["entry_role"] {
		for _, ce := range req.CanonicalEntries {
			if strings.TrimSpace(ce.EntryKind) == "content_unit" {
				u.term("entry_role", ce.EntryRole)
			}
		}
	}
	return u
}

// importerPreflightCodeCheck 在写库前校验"本次载荷实际用到的写死映射仍被当前已发布定义支持"：
// 用到的关系码必须存在且启用，用到的词表项必须仍在词表内且启用。
//
// 只校验用到的项：旧实现无条件要求 11 个关系码 + format/role/entry_role 全量词表项存在并启用，
// 后台停用一个与本次无关的码，连"只建一个 person"的导入都会被 400 importer_mapping_stale
// （写路径本身反而宽容，见 relations.go 的 historical=true）。
//
// 背景：后台改码（如禁用某关系/词表项）后，旧的导入载荷与写死映射会在 Save/关系
// 落库阶段才报 invalid_relation_type/unknown_term，前面已建的 work/release/medium
// 变成半成品。本检查把这类失败提前到零写入阶段，错误码为 importer_mapping_stale:*。
// 只读已发布定义快照，无写入。
func importerPreflightCodeCheck(doc Definitions, usage importerMappingUsage) error {
	for _, code := range sortedKeys(usage.relations) {
		rel, ok := doc.Relations[code]
		if !ok {
			return fmt.Errorf("importer_mapping_stale:relation=%s", code)
		}
		if !rel.Enabled {
			return fmt.Errorf("importer_mapping_stale:relation_disabled=%s", code)
		}
	}
	for _, vocab := range sortedKeys(usage.terms) {
		v, ok := doc.Vocabularies[vocab]
		if !ok {
			return fmt.Errorf("importer_mapping_stale:vocab=%s", vocab)
		}
		for _, term := range sortedKeys(usage.terms[vocab]) {
			t, ok := v.Terms[term]
			if !ok || !t.Enabled {
				return fmt.Errorf("importer_mapping_stale:vocab=%s term=%s", vocab, term)
			}
		}
	}
	return nil
}

// importerPreflightTitles 预检标题与条目形态：新建作品（entityType=work 且非挂靠模式）、
// 篇目、曲目都必须有标题，entry_kind 只能是 content_unit/expression，entry_role 只能落在篇目条目上。
// 写路径里缺标题要到建完 work/release/medium 之后才报 invalid_payload，留下半成品。
// entityType 是归一化后的目标类型：导入 agent 时载荷不带 work，也不该要求它。
func importerPreflightTitles(req ImporterImportRequest, mode, entityType string) error {
	if mode != "append_release_to_work" && entityType == "work" && (req.Work == nil || strings.TrimSpace(req.Work.Title) == "") {
		return fmt.Errorf("invalid_payload: work.title")
	}
	for i, ce := range req.CanonicalEntries {
		if strings.TrimSpace(ce.Title) == "" {
			return fmt.Errorf("invalid_payload: canonical_entries[%d].title", i)
		}
		switch strings.TrimSpace(ce.EntryKind) {
		case "", "expression", "content_unit":
		default:
			return fmt.Errorf("invalid_payload: canonical_entries[%d].entry_kind=%s", i, ce.EntryKind)
		}
		// entry_role 只在篇目（content_unit）上声明；留给表达条目会被写路径静默丢弃。
		if strings.TrimSpace(ce.EntryRole) != "" && strings.TrimSpace(ce.EntryKind) != "content_unit" {
			return fmt.Errorf("invalid_payload: canonical_entries[%d].entry_role", i)
		}
	}
	for i, m := range req.Mediums {
		for j, t := range m.Tracks {
			if strings.TrimSpace(t.Title) == "" {
				return fmt.Errorf("invalid_payload: mediums[%d].tracks[%d].title", i, j)
			}
		}
	}
	return nil
}

// importerUnsupportedEntityTypeFields 拒绝"该类型的写路径绝不会读取"的顶层载荷对象。
//
// entity_type != work 的导入只建一个 agent（importNewAgent 压根不碰这三项）：agent 不是 Work，
// 既没有 Work→ContentUnit→Expression 的创作层级，也没有 Work→Release→Medium→Track 的发行承载，
// 所以 canonical_entries / mediums / release 在这条路径上**没有落点**——不是"暂时没实现"，
// 而是实体边界不允许（见 AGENTS.md §4 的层级不变量），也就不该在这里假装支持。
// 既然兑现不了，就不能收下：静默忽略会让调用方以为结构已经写进去，实际丢数据。
// 空壳不算声明：null / [] / {} 与"没传"同义（前端为作品导入无条件带 release 键，手工载荷
// 也可能带空对象），只有带内容的对象才拒绝，避免误伤合法载荷。
// append_release_to_work 是例外：它借用作品载荷里的发行层字段、真的会写发行链，
// 这三项在那里有落点（见 Import 的分派），故不拦。
func importerUnsupportedEntityTypeFields(req ImporterImportRequest, mode, entityType string) error {
	if entityType == "work" || mode == "append_release_to_work" {
		return nil
	}
	for _, f := range []struct {
		code     string
		declared bool
	}{
		{"canonical_entries", len(req.CanonicalEntries) > 0},
		{"mediums", len(req.Mediums) > 0},
		{"release", importerReleaseDeclaresData(req.Release)},
	} {
		if f.declared {
			// 错误信息带对象标识（entity_type）与字段码，便于调用方定位是哪个字段在此类型上不可用。
			return fmt.Errorf("unsupported_field_for_entity_type: entity_type=%s field=%s", entityType, f.code)
		}
	}
	return nil
}

// importerReleaseDeclaresData 判断发行预览是否真的声明了内容（空对象 {} 与 null 同义）。
// 逐字段判空而非与零值比较：Translations/CatalogMetadata 是松散 JSON 值（map/slice 不可比较），
// 直接比较结构体会 panic。
func importerReleaseDeclaresData(rel *ImporterReleasePreview) bool {
	if rel == nil {
		return false
	}
	if importerJSONDeclaresData(rel.Translations) || importerJSONDeclaresData(rel.CatalogMetadata) {
		return true
	}
	for _, s := range []string{
		rel.CoverImageURL, rel.CoverAspect, rel.OriginalLanguage, rel.EditionName, rel.CatalogNumber,
		rel.Barcode, rel.Publisher, rel.Packaging, rel.Country, rel.Language,
		rel.DistributionChannel, rel.EditionType, rel.EditionBatch, rel.EditionDate, rel.Notes,
	} {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// importerJSONDeclaresData 判断松散 JSON 值是否"声明了内容"：nil / 空串 / 空数组 / 空对象
// 都算没声明，其余（含非空容器）算声明。
func importerJSONDeclaresData(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case []ImporterTranslationItem:
		return len(x) > 0
	case map[string]Translation:
		return len(x) > 0
	default:
		return true
	}
}

// importerReleaseRequiresMediums 拒绝"发行对象真的带了数据、却没有载体"的载荷：写路径里 mediums
// 才是发行链的写指令，new_work / create_relation 在 mediums 为空时走不到 importReleaseChain
// （importNewWork 直接返回，canonical entries 至多走 importExpressionsOnly），载荷里的 release
// 对象没有任何读取点，收下就是静默丢数据。
//
// 与 importerApplyFieldSwitches 里 has_release=true 的那条是同一规则，判据不同：布尔在 JSON 里
// 缺省与 false 不可区分、预览响应也从不产出它，调用方实际会漏的正是这个声明，所以这里按"对象是否
// 带了数据"（importerReleaseDeclaresData）判；空壳（null / {}）与"没传"同义，不算声明。
//
// 判为调用方漏声明而非"合法无载体发行草稿"：能建无载体发行的路径是 append_release_to_work
// （显式挂靠已有 work、只要发行链，它不依赖 mediums），new_work 模式下 release 只是作品的附属结构，
// 没有载体就没有链路落点；要表达无载体发行应改走那条模式，拒绝这里不丢能力。
func importerReleaseRequiresMediums(req ImporterImportRequest, mode string) error {
	if mode == "append_release_to_work" || len(req.Mediums) > 0 {
		return nil
	}
	if !importerReleaseDeclaresData(req.Release) {
		return nil
	}
	return fmt.Errorf("invalid_payload: release requires mediums")
}

// importerUnsupportedPayloadFields 拒绝载荷里"模型没有对应字段、写路径读不了"的对象级字段。
//
// 与 importerUnsupportedEntityTypeFields 同一判据（声明了就必须被兑现，否则明确报错），区别在
// 判据的粒度：那条按整类实体判（canonical_entries/mediums/release 在 agent 路径上没有落点），
// 这条按字段判——字段在**任何**路径上都没有落点，静默收下就是丢数据。错误码复用同一个
// unsupported_field_for_entity_type，field 带对象定位（mediums[0].media_category / release.notes），
// 调用方对"载荷声明了但写不进去"只需处理一种分支。
//
// 空壳（空串 / null / [] / {}）与"没传"同义，不算声明：调用方常把预览响应原样转交，
// 把空值也当声明就会把正常的预览→导入往返自己拒掉。
func importerUnsupportedPayloadFields(req ImporterImportRequest, entityType string) error {
	reject := func(field string) error {
		return fmt.Errorf("unsupported_field_for_entity_type: entity_type=%s field=%s", entityType, field)
	}
	rel := req.Release
	if rel == nil {
		return nil
	}
	// 封面比例：Picture 只有 url/caption/taken_at/source，没有比例列；比例是展示建议
	// （AGENTS.md），由前端按标签推断，写不进模型。
	if strings.TrimSpace(rel.CoverAspect) != "" {
		return reject("release.cover_aspect")
	}
	// 发行备注：Entity 没有 notes 列，发行可用字段也没有同义字段。语种翻译行的 summary
	// 是"某个语种的题名简介"，本条备注没有语种归属，塞进去等于编造语种。
	if strings.TrimSpace(rel.Notes) != "" {
		return reject("release.notes")
	}
	// catalog_metadata：作品路径上的同名对象是**来源结构载体**（bangumi_type → tags、
	// official_website → external_ids、catalog_number → 发行属性），发行预览没有对应的消费
	// 口径，模型里也没有可存 blob 的列，收下只能丢。品番/条码有各自的一等字段，不走这里。
	if importerJSONDeclaresData(rel.CatalogMetadata) {
		return reject("release.catalog_metadata")
	}
	// language：release 的适用字段不含 language，而
	// original_language 是另一个**已兑现**的槽位（Entity.OriginalLanguage）。两者合并进同一槽会
	// 变成"同时声明时谁静默覆盖谁"，故不合并：要写语言用 original_language / translations。
	if strings.TrimSpace(rel.Language) != "" {
		return reject("release.language")
	}
	return nil
}

// importerPreflightExternalIDs 预检载荷会落库的外部编号：格式层（validateExternalIDs）与
// 预设层（validateExternalIDsAgainstDB：键必须在 external_databases 预设里、值按正则、
// 分类与实体 kind 一致）与 Store.Save 同一套校验，只是提前到零写入阶段执行。
// 覆盖会落库的三类：条目表达/篇目、曲目写进表达本体的 recording_mbid/isrc、关联 agent。
func (s *Store) importerPreflightExternalIDs(ctx context.Context, defs Definitions, req ImporterImportRequest) error {
	check := func(kind string, externalIDs map[string]string) error {
		if len(externalIDs) == 0 {
			return nil
		}
		e := Entity{Kind: kind, ExternalIDs: externalIDs}
		if err := defs.validateExternalIDs(e); err != nil {
			return err
		}
		return validateExternalIDsAgainstDB(ctx, s.DB, e)
	}
	for i, ce := range req.CanonicalEntries {
		kind := "expression"
		if strings.TrimSpace(ce.EntryKind) == "content_unit" {
			kind = "content_unit"
		}
		if err := check(kind, importerEntryExternalIDs(ce, "")); err != nil {
			return fmt.Errorf("canonical_entries[%d]: %w", i, err)
		}
	}
	for i, m := range req.Mediums {
		for j, t := range m.Tracks {
			if err := check("expression", importerTrackExpressionExternalIDs(t)); err != nil {
				return fmt.Errorf("mediums[%d].tracks[%d]: %w", i, j, err)
			}
		}
	}
	for i, a := range req.StaffAssociations {
		// 与写路径同样的跳过口径：skip 项、显式关联既有实体（target_artist_id）、无名字项都不落库。
		if strings.EqualFold(strings.TrimSpace(a.Action), "skip") || strings.TrimSpace(a.TargetArtistID) != "" || strings.TrimSpace(a.ParsedName) == "" {
			continue
		}
		if err := check("agent", importerAgentExternalIDs(a.ExternalIDs)); err != nil {
			return fmt.Errorf("staff_associations[%d]: %w", i, err)
		}
	}
	return nil
}

// importerTrackExpressionExternalIDs 取曲目写在**表达本体**上的外部编号（recording_mbid/isrc，
// 见 importReleaseChain 的 trackExternal）：track 定义只声明 duration/role，ISRC 不是 track 属性。
func importerTrackExpressionExternalIDs(t ImporterTrackPreview) map[string]string {
	out := map[string]string{}
	if v := strings.TrimSpace(t.RecordingMBID); v != "" {
		out["recording_mbid"] = v
	}
	if v := strings.TrimSpace(t.ISRC); v != "" {
		out["isrc"] = v
	}
	return out
}

// importerPreflightAssociations 校验导入关联的关系码与词表项：
// 空关系码跳过（落库侧计入 SkippedRelations，不在这里拒绝）；
// 非空关系码必须存在且启用，character_in 的番位（relation_role）非空时必须仍在
// character_rank 词表内且启用（且该关系定义确实声明了 character_rank）。voiced_by 的 character 名是引用配对线索（落库时按名找角色
// 实体，找不到则降级为 credit_role 文本），不是词表项，不在这里校验。
// 错误码与 importerPreflightCodeCheck 同系列，便于前端区分"载荷映射过期"。
func importerPreflightAssociations(doc Definitions, assocs []ImporterStaffAssociation) error {
	for i, a := range assocs {
		if strings.ToLower(strings.TrimSpace(a.Action)) == "skip" {
			continue
		}
		code := strings.TrimSpace(a.RelationType)
		if code == "" {
			continue
		}
		rel, ok := doc.Relations[code]
		if !ok {
			return fmt.Errorf("importer_mapping_stale:association[%d].relation=%s", i, code)
		}
		if !rel.Enabled {
			return fmt.Errorf("importer_mapping_stale:association[%d].relation_disabled=%s", i, code)
		}
		// 番位落 character_rank 字段（不是 role）：只在关系定义声明了该字段时才写，
		// 因此也只在声明时校验词表项——旧实例降级为不写词表项，不该被预检拦住。
		if rr := strings.TrimSpace(a.RelationRole); rr != "" && code == "character_in" && contains(rel.Fields, "character_rank") {
			v, ok := doc.Vocabularies["character_rank"]
			if !ok {
				return fmt.Errorf("importer_mapping_stale:association[%d].vocab=character_rank", i)
			}
			t, ok := v.Terms[rr]
			if !ok || !t.Enabled {
				return fmt.Errorf("importer_mapping_stale:association[%d].character_rank=%s", i, rr)
			}
		}
	}
	return nil
}

// importerPreflight 在写库前只读校验整份载荷，保证校验失败时零写入：
//   - 载荷声明的对象在该 entity_type 的写路径上是否有落点
//     （importerUnsupportedEntityTypeFields：没有就报 unsupported_field_for_entity_type，不静默忽略）；
//   - 章节树（parent_index）结构合法（顺序即拓扑序）；
//   - 标题与条目形态（importerPreflightTitles）：缺标题在写路径里要到建完 work/release/medium 才报错；
//   - 显式表达引用（canonical entries 与各轨）必须存在且 kind=expression；
//   - 载荷会落库的外部编号与 Store.Save 同口径（importerPreflightExternalIDs）；
//   - 载荷声明的属性字段码、以及代码将写入的 release/medium/track 属性，
//     必须属于目标 kind 的适用字段（unknown_field 提前暴露）；
//   - 写死映射（关系码/词表输出）仍被当前已发布定义支持（importer_mapping_stale
//     提前暴露，避免后台改码后在 Save 阶段才报 invalid_relation_type 留半成品），
//     只校验本次载荷实际用到的码与词表项；
//   - 属性**取值**与载荷声明的原语言/翻译行/日期（importerPreflightValues）：与
//     Store.Save 同一实现同一口径，提前到零写入阶段，见该函数注释。
func (s *Store) importerPreflight(ctx context.Context, actor User, req ImporterImportRequest, mode, entityType, source string) error {
	// 该类型的写路径绝不会读取的顶层对象先拒绝：静默忽略等于让调用方以为写进去了
	//（unsupported_field_for_entity_type，见该函数注释），且比结构类校验更基础，先报更好定位。
	if err := importerUnsupportedEntityTypeFields(req, mode, entityType); err != nil {
		return err
	}
	// 字段级同判据：模型里根本没有落点的对象级字段（media_category、release 的
	// aspect/notes/catalog_metadata/language）同样在零写入阶段拒绝，不吃下再丢。
	if err := importerUnsupportedPayloadFields(req, entityType); err != nil {
		return err
	}
	// 对象级的同类判据：release 带了数据却没有 mediums，发行链在这条模式的写路径上走不到
	// （见 importerReleaseRequiresMediums），同样零写入拒绝而不是静默丢弃。
	if err := importerReleaseRequiresMediums(req, mode); err != nil {
		return err
	}
	entries, work, assocs := req.CanonicalEntries, req.Work, req.StaffAssociations
	if err := validateImporterEntryTree(entries); err != nil {
		return err
	}
	if err := importerPreflightTitles(req, mode, entityType); err != nil {
		return err
	}
	if _, err := s.importerExplicitExpressions(ctx, actor, entries, req.Mediums); err != nil {
		return err
	}
	defs, err := s.Definitions(ctx)
	if err != nil {
		return err
	}
	if err := s.importerPreflightExternalIDs(ctx, defs.Document, req); err != nil {
		return err
	}
	// 写死映射先验：后台改码/禁用词表后，旧映射在 Save 阶段才报
	// invalid_relation_type/unknown_term 会留下半成品；此处零写入提前暴露，且只查用到的项。
	if err := importerPreflightCodeCheck(defs.Document, importerMappingUsageFromPayload(req, mode, importerFieldSet(defs.Document, "content_unit"))); err != nil {
		return err
	}
	// create_relation 的端点类型先验：新作品 → 目标作品，关系两端都得接受 work，
	// 否则要等关系落库才报 invalid_endpoints，而作品已经建好了。
	if mode == "create_relation" {
		rel, ok := defs.Document.Relations[strings.TrimSpace(req.RelationType)]
		if !ok || !contains(rel.SourceKinds, "work") || !contains(rel.TargetKinds, "work") {
			return fmt.Errorf("invalid_endpoints: relation=%s", strings.TrimSpace(req.RelationType))
		}
	}
	if err := importerPreflightAssociations(defs.Document, assocs); err != nil {
		return err
	}
	if err := importerCheckAttrs(defs.Document, "release", importerReleaseAttrs(req.Release, work)); err != nil {
		return err
	}
	for _, ce := range entries {
		kind := strings.TrimSpace(ce.EntryKind)
		if kind == "" {
			kind = "expression"
		}
		if err := importerCheckAttrs(defs.Document, kind, ce.Attributes); err != nil {
			return err
		}
	}
	for _, m := range req.Mediums {
		if err := importerCheckAttrs(defs.Document, "medium", importerMediumAttrs(m)); err != nil {
			return err
		}
		for _, t := range m.Tracks {
			if err := importerCheckAttrs(defs.Document, "track", importerTrackAttrs(t)); err != nil {
				return err
			}
			// 曲目的 entry_index 必须指向同一清单里已声明的**表达型**条目
			// （顶层篇目是容器，曲目不应结构绑定到它）。
			if t.EntryIndex != nil {
				idx := *t.EntryIndex
				if idx < 0 || idx >= len(entries) {
					return fmt.Errorf("invalid_entry_index: %d", idx)
				}
				if strings.TrimSpace(entries[idx].EntryKind) == "content_unit" {
					return fmt.Errorf("invalid_entry_index: %d", idx)
				}
			}
		}
	}
	// 最后才是"属性取值 + 语言/翻译/日期"：前面的结构与映射问题更基础，先报更好定位；
	// 判定与 Store.Save 同源，见 importerPreflightValues。
	return s.importerPreflightValues(ctx, actor, defs.Document, req, mode, entityType, source)
}

// importerPreflightValues 把"属性取值 + 原语言/翻译行/日期"的校验前移到零写入阶段。
//
// 判定不复制第二份：实体由写路径**同一批构造函数**重建（buildWorkEntity /
// importerReleaseAttrs / importerMediumAttrs / importerTrackAttrs / importerContentUnitAttrs /
// importerEntryExpressionAttrs / buildAgentEntity），再用 Store.Save 的同一实现
// （Definitions.validateEntityContent）与同一 historical 口径校验；关系边属性同理走
// importerAssociationRelationAttrs + validateRelationAttributes（SaveRelation 的同一实现）。
// 因此"预检通过 ⇒ 导入不会再因属性值/语言/翻译中途失败"由构造保证，预检只是提前失败，
// 不放宽也不收紧 Save 的判定。
//
// 范围与写路径对齐：entity_type=work 时校验 work/release/载体/曲目/条目/关联，其它
// entity_type 只校验顶层 artist——载荷里的 canonical_entries/mediums/release 在 agent
// 路径上没有落点，由 importerPreflight 以 unsupported_field_for_entity_type 提前拒绝，
// 不在这里假装校验；append_release_to_work 只借用作品载荷里的发行层字段、不写作品。
// 发行与载体的 original_language / translations 会随发行链落库（见 importReleaseChain），
// 因此同样按 Save 的实现预检：非法 locale / 空标题行在零写入阶段报 invalid_locale /
// invalid_translation，而不是留到 medium/release Save 时才失败。
//
// 载荷里会在写路径被复用（幂等键命中、显式表达引用、同父同号篇目）的对象同样预检：与既有
// 外部编号预检同口径——宁可让调用方改载荷，也不让同一份载荷这次通过、下次（复用失效时）
// 才在 Save 阶段失败。写路径不读取的载荷字段一律不查（见上一段）。
//
// 只读：全部走已有的构造函数与只读查询，不产生任何写入。
func (s *Store) importerPreflightValues(ctx context.Context, actor User, defs Definitions, req ImporterImportRequest, mode, entityType, source string) error {
	ref := reference(ctx, s.DB, &actor)
	check := func(at string, e Entity) error {
		if err := defs.validateEntityContent(e, ref, true); err != nil {
			return importerValueError(at, defs, e, ref, err)
		}
		// 停用项（字段/词表项）：Store.Save 在 validateEntity 之后还有 retiredEntity——
		// 新建实体没有旧值可比，用到的停用码一律拒绝（disabled_field/disabled_term）。
		// 预检按同样的"全新实体"判定，否则后台停用某个词表项后，载荷仍会写到一半才失败。
		if err := defs.retiredEntity(e, Entity{}); err != nil {
			if code, value, ok := importerRetiredAttribute(e, err); ok {
				return fmt.Errorf("invalid_attribute_value: %s.%s=%s: %w", at, code, value, err)
			}
			return fmt.Errorf("invalid_attribute_value: %s: %w", at, err)
		}
		return nil
	}
	// 载荷声明的原语言与翻译行：写路径对非法值**静默丢弃**（originalLanguageOrEmpty /
	// importerTranslationsFromAny），调用方会以为已经写进去。这里按 Save 同一实现判定
	// 载荷原文，非法 locale / 空标题行以 invalid_locale / invalid_translation 提前拒绝。
	checkMeta := func(at, originalLanguage string, decls []ImporterTranslationItem) error {
		e := Entity{OriginalLanguage: strings.TrimSpace(originalLanguage), Translations: importerDeclaredTranslations(decls)}
		if e.OriginalLanguage == "" && len(e.Translations) == 0 {
			return nil
		}
		return check(at, e)
	}

	if entityType != "work" {
		a := importerTopArtist(req)
		if a == nil {
			return nil
		}
		key, hasKey := importDedupKey(source, req, entityType)
		agent, err := buildAgentEntity(a.Name, a.OriginalName, a.Biography, a.AvatarURL, a.Language, entityType, a.Translations, a.ExternalIDs, key, hasKey)
		if err != nil {
			return err
		}
		// 与 importNewAgent 同序：预览侧细化类型覆盖 URL 推断。
		if err := check("artist", agent); err != nil {
			return err
		}
		return checkMeta("artist", a.Language, a.Translations)
	}

	key, hasKey := importDedupKey(source, req, entityType)
	// 作品本体与作品层声明的语言/翻译：append_release_to_work 不写作品，跳过。
	if w := req.Work; w != nil && mode != "append_release_to_work" {
		work, err := buildWorkEntity(w, source, key, req.ExternalID, hasKey, defs.PrimaryDateField())
		if err != nil {
			return err
		}
		if err := check("work", work); err != nil {
			return err
		}
		if err := checkMeta("work", w.OriginalLanguage, w.Translations); err != nil {
			return err
		}
	}
	// 发行：属性同源。edition_date 经 cleanImporterDate 归一后仍可能是日历非法值
	// （如 2024-13-45），Save 会以 invalid_date 拒绝——这里同样提前拦。
	if err := check("release", Entity{Kind: "release", Attributes: importerReleaseAttrs(req.Release, req.Work)}); err != nil {
		return err
	}
	// 发行本体声明的语言/翻译与 medium 同源：写路径会写进发行实体，预检用同一实现判定。
	if rel := req.Release; rel != nil {
		if err := checkMeta("release", rel.OriginalLanguage, importerTranslationDecls(rel.Translations)); err != nil {
			return err
		}
	}
	for i, m := range req.Mediums {
		if err := check(fmt.Sprintf("mediums[%d]", i), Entity{Kind: "medium", Attributes: importerMediumAttrs(m)}); err != nil {
			return err
		}
		// 载体声明的原语言/翻译行会写进 medium 实体（见 importReleaseChain 的载体落库），
		// 非法值必须在零写入阶段就报出来。
		if err := checkMeta(fmt.Sprintf("mediums[%d]", i), m.OriginalLanguage, importerTranslationDecls(m.Translations)); err != nil {
			return err
		}
		for j, t := range m.Tracks {
			if err := check(fmt.Sprintf("mediums[%d].tracks[%d]", i, j), Entity{Kind: "track", Attributes: importerTrackAttrs(t)}); err != nil {
				return err
			}
		}
	}
	// 条目：篇目与表达的属性各自与写路径同一构造函数（表达无时长时类型为空，
	// 属性按 kind 回退校验，写路径同样如此，见 validation.go 的 attributeKeys）。
	cuFields := importerFieldSet(defs, "content_unit")
	for i, ce := range req.CanonicalEntries {
		at := fmt.Sprintf("canonical_entries[%d]", i)
		if strings.TrimSpace(ce.EntryKind) == "content_unit" {
			if err := check(at, Entity{Kind: "content_unit", Attributes: importerContentUnitAttrs(cuFields, ce)}); err != nil {
				return err
			}
		} else {
			attrs := importerEntryExpressionAttrs(ce)
			if err := check(at, Entity{Kind: "expression", Attributes: attrs}); err != nil {
				return err
			}
		}
		if err := checkMeta(at, ce.OriginalLanguage, importerTranslationDecls(ce.Translations)); err != nil {
			return err
		}
	}
	// 关联：agent 本体只在写路径会新建时校验（target_artist_id 复用既有实体、无名字项不落库），
	// 关系边属性则一律按同一构造函数算出来再校验（复用端点也照样写边）。
	for i, a := range req.StaffAssociations {
		if strings.EqualFold(strings.TrimSpace(a.Action), "skip") {
			continue
		}
		at := fmt.Sprintf("staff_associations[%d]", i)
		if strings.TrimSpace(a.TargetArtistID) == "" && strings.TrimSpace(a.ParsedName) != "" {
			iKey := assocImportKey(a.ExternalIDs)
			staff, err := buildAgentEntity(a.ParsedName, a.ParsedOriginal, a.Biography, a.AvatarURL, a.Language, staffAgentType(a.EntityType), a.Translations, a.ExternalIDs, iKey, iKey != "")
			if err != nil {
				return err
			}
			if err := check(at, staff); err != nil {
				return err
			}
			if err := checkMeta(at, a.Language, a.Translations); err != nil {
				return err
			}
		}
		code := strings.TrimSpace(a.RelationType)
		relDef, ok := defs.Relations[code]
		if code == "" || !ok {
			// 关系码缺失/未知已由 importerPreflightCodeCheck 与 importerPreflightAssociations
			// 按 importer_mapping_stale 拦下，这里不重复报错。
			continue
		}
		// character 属性填的是本次导入在上游解析出的角色实体 ID（预检阶段尚不存在），
		// 故按"未解析"分支计算——那是写路径的 credit_role 文本分支；写路径拿到的 ID 来自
		// 本次刚保存的实体，必然可解析。
		attrs := importerAssociationRelationAttrs(code, relDef, a, "")
		if err := validateRelationAttributes(defs, code, attrs, ref, true); err != nil {
			return fmt.Errorf("invalid_attribute_value: %s.attributes=%s: %w", at, importerValueText(attrs), err)
		}
		// 与 SaveRelation 同口径：新建边还要过一遍停用检查（retiredAttributes，旧值为空）。
		if err := defs.retiredAttributes(attrs, nil); err != nil {
			return fmt.Errorf("invalid_attribute_value: %s.attributes=%s: %w", at, importerValueText(attrs), err)
		}
	}
	return nil
}

// importerRetiredAttribute 定位停用项错误里的字段码与取值：retiredValue 把错误构造成
// "<字段码>: <错误码>"，字段码若在实体属性里就回显取值。
func importerRetiredAttribute(e Entity, err error) (code, value string, ok bool) {
	head, _, _ := strings.Cut(err.Error(), ":")
	code = strings.TrimSpace(head)
	if v, exists := e.Attributes[code]; exists {
		return code, importerValueText(v), true
	}
	return "", "", false
}

// importerValueError 给预检失败补上"对象标识 + 字段码 + 具体值"，错误码沿用 Save 的原始码
// （unknown_field / invalid_term / invalid_date / invalid_locale / invalid_translation…）。
// 判定与失败顺序都不受影响：这里只在 Store.Save 同一实现的失败结果上补定位信息，
// 错误链用 %w 保留，调用方仍可按码判定。
func importerValueError(at string, defs Definitions, e Entity, ref func(string, []string) error, err error) error {
	if code, value, unknown, ok := importerOffendingAttribute(defs, e, ref); ok {
		if unknown {
			return fmt.Errorf("unknown_field: %s.%s=%s", at, code, value)
		}
		// d.attributes 的错误已带字段码前缀，去掉重复（值已单独回显）。
		cause := err
		if inner := errors.Unwrap(err); inner != nil && strings.HasPrefix(err.Error(), code+": ") {
			cause = inner
		}
		return fmt.Errorf("invalid_attribute_value: %s.%s=%s: %w", at, code, value, cause)
	}
	if err.Error() == "invalid_locale" {
		return fmt.Errorf("invalid_locale: %s.original_language=%s", at, importerValueText(e.OriginalLanguage))
	}
	if err.Error() == "invalid_translation" {
		if loc, title, ok := importerOffendingTranslation(e.Translations); ok {
			return fmt.Errorf("invalid_translation: %s.translations[%s].title=%s", at, loc, importerValueText(title))
		}
	}
	return fmt.Errorf("%s: %w", at, err)
}

// importerOffendingAttribute 在已失败的属性集里定位出错的字段码与取值（仅用于补充定位）。
// 顺序与 Definitions.attributes 一致：先未声明字段（unknown_field），再按声明字段用同一个
// d.value 复算；定位不到（理论上不会发生）时返回 ok=false，错误原文照旧透出。
func importerOffendingAttribute(defs Definitions, e Entity, ref func(string, []string) error) (code, value string, unknown, ok bool) {
	keys := defs.attributeKeys(e.Kind)
	for k := range e.Attributes {
		if !contains(keys, k) {
			return k, importerValueText(e.Attributes[k]), true, true
		}
	}
	for _, k := range keys {
		f, exists := defs.Fields[k]
		if !exists {
			return k, "", true, true
		}
		if derr := defs.value(f, e.Attributes[k], ref, true); derr != nil {
			return k, importerValueText(e.Attributes[k]), false, true
		}
	}
	return "", "", false, false
}

// importerOffendingTranslation 找出首个被判非法的翻译行（locale 解析失败或标题为空），
// 按 locale 字典序输出，保证同一载荷的错误稳定可复现。
func importerOffendingTranslation(translations map[string]Translation) (loc, title string, ok bool) {
	for _, key := range sortedKeys(translations) {
		tr := translations[key]
		if _, err := language.Parse(key); err != nil || strings.TrimSpace(tr.Title) == "" {
			return key, tr.Title, true
		}
	}
	return "", "", false
}

// importerValueText 把值渲染成错误里的短文本：字符串带引号，其余走 JSON；
// 超过 80 个字符按 rune 截断（载荷可能是整段 infobox 快照），避免错误响应被撑爆。
func importerValueText(v any) string {
	out := fmt.Sprintf("%v", v)
	if s, isString := v.(string); isString {
		out = strconv.Quote(s)
	} else if b, err := json.Marshal(v); err == nil {
		out = string(b)
	}
	if r := []rune(out); len(r) > 80 {
		out = string(r[:80]) + "…"
	}
	return out
}

// importerEntryExprKey 生成条目级表达的导入幂等键：同一 work 的同一份清单重试时，
// 无权威外部编号（recording_mbid/isrc）的条目也能找回上次新建的表达，而不是重复
// 创建一批无引用的孤儿表达。节点签名含下标/父下标/标题/外部编号——清单重排或
// 改题名都会得到新键；跨作品的标题重名不共享（workKey 已隔离）。workKey 为空
// （来源无幂等键）时返回空串，不做条目幂等。
func importerEntryExprKey(workKey string, i int, ce ImporterCanonicalEntryPreview, parentID string) string {
	workKey = strings.TrimSpace(workKey)
	if workKey == "" {
		return ""
	}
	ids := make([]string, 0, len(ce.ExternalIDs))
	for k, v := range stringScalarMap(ce.ExternalIDs) {
		if v = strings.TrimSpace(v); v != "" {
			ids = append(ids, strings.ToLower(k)+"="+strings.ToLower(v))
		}
	}
	sort.Strings(ids)
	parent := ""
	if ce.ParentIndex != nil && *ce.ParentIndex >= 0 {
		parent = strconv.Itoa(*ce.ParentIndex)
	}
	sig := strings.Join([]string{strconv.Itoa(i), parent, parentID, strings.TrimSpace(ce.Title), strings.TrimSpace(ce.EntryKind), strings.Join(ids, ";")}, "\x1f")
	sum := sha256.Sum256([]byte(sig))
	return workKey + ":e" + hex.EncodeToString(sum[:8])
}

// importExpressionsOnly 无发行版时只建表达，不建 release 链；entry_kind=content_unit
// 的条目先建章节树（parent_index 指父级），下级表达挂到所属单元。
// 与有发行路径同样先校验树、复用既有篇目（来源 ID/父+标题/父+编号），并写入多语言。
// 表达按"权威外部编号 → 导入幂等键"复用，无依据则新建。
func (s *Store) importExpressionsOnly(ctx context.Context, actor User, note string, sources []Source, workID, workKey string, entries []ImporterCanonicalEntryPreview) (ImporterImportedCounts, error) {
	counts := ImporterImportedCounts{}
	if err := validateImporterEntryTree(entries); err != nil {
		return counts, err
	}
	// 篇目字段集：entry_role 只在目标实例已声明该字段时写入（见 importerContentUnitAttrs）。
	cuFields := map[string]bool{}
	if defs, derr := s.Definitions(ctx); derr == nil {
		cuFields = importerFieldSet(defs.Document, "content_unit")
	}
	existingUnits, err := s.listWorkContentUnits(ctx, workID, &actor)
	if err != nil {
		return counts, err
	}
	index := newImporterContentUnitIndex(existingUnits)
	unitIDs := make([]string, len(entries))
	for i, ce := range entries {
		title := strings.TrimSpace(ce.Title)
		if title == "" {
			return counts, fmt.Errorf("invalid_payload")
		}
		pos := sanitizePosition(ce.Position, i)
		parent := ""
		if ce.ParentIndex != nil && *ce.ParentIndex >= 0 && *ce.ParentIndex < len(entries) {
			parent = unitIDs[*ce.ParentIndex]
		}
		if strings.TrimSpace(ce.EntryKind) == "content_unit" {
			// 重复导入复用：来源 ID 优先，其次 (父篇目, 标题)/(父篇目, 编号)。
			if existingID, ok := index.lookup(ce, parent); ok {
				unitIDs[i] = existingID
				continue
			}
			unit, err := s.importerSave(ctx, Entity{
				Kind:             "content_unit",
				Title:            title,
				WorkID:           workID,
				ParentID:         parent,
				Number:           strings.TrimSpace(ce.Number),
				Position:         pos,
				Attributes:       importerContentUnitAttrs(cuFields, ce),
				ExternalIDs:      stringScalarMap(ce.ExternalIDs),
				Translations:     importerTranslationsFromAny(ce.Translations),
				OriginalLanguage: originalLanguageOrEmpty(ce.OriginalLanguage),
			}, actor, note, sources)
			if err != nil {
				return counts, err
			}
			unitIDs[i] = unit.ID
			index.remember(ce, parent, unit.ID)
			counts.ContentUnits++
			continue
		}
		// 手工匹配优先：显式指定的既有表达直接引用（允许跨 Work），
		// 只建立引用，不改动其原有章节归属。
		if explicit := strings.TrimSpace(ce.ExpressionID); explicit != "" {
			e, err := s.Get(ctx, explicit, &actor)
			if err != nil || e.Kind != "expression" {
				return counts, fmt.Errorf("invalid_expression_reference")
			}
			continue
		}
		// 条目幂等：上次同一清单已建的表达按导入键找回；无权威编号的条目重试
		// 不再重复创建。
		exprKey := importerEntryExprKey(workKey, i, ce, parent)
		if exprKey != "" {
			if prev, ok := s.findImported(ctx, exprKey, &actor); ok && prev.Kind == "expression" && prev.WorkID == workID {
				continue
			}
		}
		if _, err := s.createExpressionWithMeta(ctx, actor, note, sources, workID, parent, title, ce, pos, exprKey); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// importerReleaseVariantKey 在导入基础键上附加"发行内容签名"，用于区分
// 同一来源下的不同版本：同一份载荷重试得到同一键（幂等补齐），
// 追加另一个版本（不同版名/载体/曲目）则得到不同键（新建发行，不误并）。
// 签名必须覆盖发行身份数据（品番/条码/地区/发行日期）：版名与曲目结构完全
// 相同的两个地区版是两个发行，缺了这些字段会被误并成同一个。
// base 为空（来源无幂等键）时返回空，不做幂等。
func importerReleaseVariantKey(base string, rel *ImporterReleasePreview, mediums []ImporterMediumPreview) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	edition := ""
	identity := ""
	if rel != nil {
		edition = strings.TrimSpace(rel.EditionName)
		// 发行身份签名必须覆盖全部"版本区分维度"：品番/条码/地区/日期之外，
		// 发行主体（publisher 自由文本）、包装（packaging 词表项）、渠道
		// （distribution_channel 词表项）、版本类别/批次（edition_type/batch）
		// 与语言（language）同样区分版本，缺了会被误并成同一发行。
		identity = strings.Join([]string{
			strings.TrimSpace(rel.CatalogNumber),
			strings.TrimSpace(rel.Barcode),
			strings.TrimSpace(rel.Country),
			cleanImporterDate(rel.EditionDate),
			strings.TrimSpace(rel.Publisher),
			strings.ToLower(strings.TrimSpace(rel.Packaging)),
			strings.ToLower(strings.TrimSpace(rel.DistributionChannel)),
			strings.ToLower(strings.TrimSpace(rel.EditionType)),
			strings.ToLower(strings.TrimSpace(rel.EditionBatch)),
			strings.TrimSpace(rel.Language),
		}, "\x1f")
	}
	sig := strings.Builder{}
	sig.WriteString(edition)
	sig.WriteByte('\n')
	sig.WriteString(identity)
	sig.WriteByte('\n')
	for _, m := range mediums {
		fmt.Fprintf(&sig, "%d|%s|%s|%s|%d\n", sanitizePosition(m.Position, -1), strings.TrimSpace(m.Format), strings.TrimSpace(m.Number), strings.TrimSpace(m.Name), len(m.Tracks))
		for _, t := range m.Tracks {
			// 曲目签名覆盖全部身份维度：标题之外，recording_mbid/isrc 是权威
			// 外部编号，duration 区分同名不同录音版本（如单曲版/专辑版），
			// artist_credit 区分同名翻唱/合作版本。缺了会被误并。
			fmt.Fprintf(&sig, "  %d|%s|%s|%s|%v|%s\n",
				sanitizePosition(t.Position, -1), strings.TrimSpace(t.Title),
				strings.TrimSpace(t.ISRC), strings.TrimSpace(t.RecordingMBID),
				t.DurationSeconds, strings.TrimSpace(t.ArtistCredit))
		}
	}
	sum := sha256.Sum256([]byte(sig.String()))
	return base + ":r" + hex.EncodeToString(sum[:8])
}

// releaseDeclaresWork 判断发行 subjects 是否已声明某作品（跨作品收录需显式声明）。
func releaseDeclaresWork(release Entity, workID string) bool {
	for _, s := range release.Subjects {
		if s.WorkID == workID {
			return true
		}
	}
	return false
}

// importerReleasePictures 把发行预览声明的远端封面透传为 Picture（与 work 封面同口径：
// 只引用远端 URL，不抓取、不转存；Source.URL 指回来源条目页便于考据）。
// download_cover 显式 false 时 URL 已在 importerWithoutRemotePictures 里清空，这里自然不写。
// 幂等复用分支用同一函数补齐（只在无图时补，不覆盖已有图，见调用点）。
func importerReleasePictures(rel *ImporterReleasePreview, workKey string) []Picture {
	if rel == nil {
		return nil
	}
	// 发行封面与作品封面来自同一条上游条目：页面 URL 用作品幂等键还原（bangumi:subject:<id>）。
	key := strings.TrimSpace(workKey)
	if p, ok := pictureFromRemote(rel.CoverImageURL, "导入发行版封面", key, key != ""); ok {
		return []Picture{p}
	}
	return nil
}

// importReleaseChain 按 work → expression → release → medium → track 建链。
// 表达对齐只认权威依据：用户手工指定的 expression_id，或 recording_mbid/isrc 等
// 外部编号；标题、时长、轨号相近不再自动合并身份（同名录音室版/现场版会被误并），
// 交由预览中的候选选择或新建。多盘各自从 1 重排轨号，跨盘绝不按轨号对齐。
// workKey 是 work 的导入幂等键，用于条目级表达复用；可为空（无来源幂等键）。
func (s *Store) importReleaseChain(ctx context.Context, actor User, note string, sources []Source, workID, workKey, workTitle string, entries []ImporterCanonicalEntryPreview, rel *ImporterReleasePreview, mediums []ImporterMediumPreview, releaseKey string, work *ImporterWorkPreview) (Entity, ImporterImportedCounts, error) {
	counts := ImporterImportedCounts{}
	// 章节树先整体校验（越界/自指/指向后继/父级非篇目都拒绝），再做任何写入。
	if err := validateImporterEntryTree(entries); err != nil {
		return Entity{}, counts, err
	}
	// 篇目字段集：entry_role 只在目标实例已声明该字段时写入。
	cuFields := map[string]bool{}
	if defs, derr := s.Definitions(ctx); derr == nil {
		cuFields = importerFieldSet(defs.Document, "content_unit")
	}
	// 发行链幂等：把传入的基础键按"发行内容签名"具体化为本版本的键，再按它复用。
	// 同一份载荷重试 → 同键 → 复用已建发行并继续补齐载体/曲目（上次可能中途失败）；
	// 追加另一个版本（不同版名/载体/曲目）→ 不同键 → 新建发行，不误并。
	releaseKey = importerReleaseVariantKey(releaseKey, rel, mediums)
	var existingRelease *Entity
	if strings.TrimSpace(releaseKey) != "" {
		if existing, ok := s.findImported(ctx, strings.TrimSpace(releaseKey), &actor); ok && existing.Kind == "release" {
			// 幂等键相同不代表"同一个发行"：换 target_work_id 或改过 subjects 后，
			// 命中的可能是别人的发行。复用前必须确认它已声明本次的作品，
			// 否则本次载体/曲目会被挂进一份没收录该作品的发行（触发
			// undeclared_release_subject 或留下孤儿表达），且不修改他人发行的收录声明。
			if !releaseDeclaresWork(existing, workID) {
				return Entity{}, counts, fmt.Errorf("undeclared_release_subject: release=%s work=%s", existing.ID, workID)
			}
			existingRelease = &existing
		}
	}
	// 权威标识索引：仅用于外部编号（recording_mbid/isrc）自动复用与显式引用解析。
	// 不再维护标题/位置索引——标题相近不再自动合并身份。
	type exprCandidate struct {
		id          string
		number      string
		externalIDs map[string]string
	}
	byID := map[string]exprCandidate{}
	byExternal := map[string]string{}
	register := func(c exprCandidate) {
		byID[c.id] = c
		for k, v := range c.externalIDs {
			if v = strings.TrimSpace(v); v != "" {
				byExternal[strings.ToLower(k)+"|"+strings.ToLower(v)] = c.id
			}
		}
	}
	// 显式引用解析：用户选择的既有表达，允许跨 Work；其所属 Work 需补进发行 subjects。
	explicitExprs, err := s.importerExplicitExpressions(ctx, actor, entries, mediums)
	if err != nil {
		return Entity{}, counts, err
	}
	// 被引用到的其它作品（非本发行主 Work）：落 subjects，满足
	// undeclared_release_subject 校验（收录的表达所属 Work 必须声明在发行上）。
	// 在创建发行前就要定稿，因此直接从显式引用集合推导（覆盖 canonical 与各轨）。
	referencedWorks := map[string]bool{}
	for _, e := range explicitExprs {
		if e.WorkID != "" && e.WorkID != workID {
			referencedWorks[e.WorkID] = true
		}
	}
	existingExprs, err := s.listWorkExpressions(ctx, workID, &actor)
	if err != nil {
		return Entity{}, counts, err
	}
	for _, e := range existingExprs {
		register(exprCandidate{id: e.ID, number: e.Number, externalIDs: e.ExternalIDs})
	}
	// 既有篇目索引：来源 ID / (父, 标题) / (父, 编号)，与无发行路径同一套去重语义。
	existingUnits, err := s.listWorkContentUnits(ctx, workID, &actor)
	if err != nil {
		return Entity{}, counts, err
	}
	unitIndex := newImporterContentUnitIndex(existingUnits)
	// 曲目挂载到篇目：标题只作为候选线索，同标题出现多个篇目时视为歧义、不自动挂载。
	unitByTitle := map[string]string{}
	unitTitleDup := map[string]bool{}
	rememberUnitTitle := func(title, id string) {
		tk := normalizeImporterTitleKey(title)
		if tk == "" || id == "" {
			return
		}
		if prev, ok := unitByTitle[tk]; ok {
			if prev != id {
				unitTitleDup[tk] = true
			}
			return
		}
		unitByTitle[tk] = id
	}
	unitIDByTitle := func(title string) string {
		tk := normalizeImporterTitleKey(title)
		if tk == "" || unitTitleDup[tk] {
			return ""
		}
		return unitByTitle[tk]
	}
	for _, u := range existingUnits {
		rememberUnitTitle(u.Title, u.ID)
	}
	// 自动复用仅认权威标识：recording_mbid / isrc 等外部编号对得上才复用。
	// 标题、时长、轨号相近不再自动合并身份（同名录音室版/现场版会被误并），
	// 这类情况交给用户在预览中选择既有表达，或新建。
	lookupAuthoritative := func(external map[string]string) (exprCandidate, bool) {
		for k, v := range external {
			if v = strings.TrimSpace(v); v == "" {
				continue
			}
			if id, ok := byExternal[strings.ToLower(k)+"|"+strings.ToLower(v)]; ok {
				return byID[id], true
			}
		}
		return exprCandidate{}, false
	}
	// 本次载荷声明的 canonical 表达按标题建索引：曲目要绑定到"同一份清单里声明的"
	// 表达，这是同一次导入内的结构绑定，不是对库里既有实体的身份猜测。
	// 同名多个时视为歧义，不自动绑定（返回 false）。
	localByTitle := map[string][]exprCandidate{}
	registerLocal := func(c exprCandidate, title string) {
		if tk := normalizeImporterTitleKey(title); tk != "" {
			localByTitle[tk] = append(localByTitle[tk], c)
		}
	}
	lookupLocalTitle := func(title string) (exprCandidate, bool) {
		tk := normalizeImporterTitleKey(title)
		if tk == "" {
			return exprCandidate{}, false
		}
		cands := localByTitle[tk]
		if len(cands) != 1 {
			return exprCandidate{}, false
		}
		return cands[0], true
	}
	// 手工绑定表：canonical entry 显式指定的表达按标题登记，作为曲目绑定的兜底线索。
	// 同名条目声明了不同表达时视为歧义并整体放弃该标题（不能"后到覆盖先到"）。
	boundByTitle := map[string]string{}
	boundTitleDup := map[string]bool{}
	rememberBound := func(title, exprID string) {
		tk := normalizeImporterTitleKey(title)
		if tk == "" || exprID == "" {
			return
		}
		if prev, ok := boundByTitle[tk]; ok {
			if prev != exprID {
				boundTitleDup[tk] = true
			}
			return
		}
		boundByTitle[tk] = exprID
	}
	lookupBound := func(title string) (string, bool) {
		tk := normalizeImporterTitleKey(title)
		if tk == "" || boundTitleDup[tk] {
			return "", false
		}
		id, ok := boundByTitle[tk]
		return id, ok
	}
	// canonical entries：entry_kind=content_unit 的先建章节树（parent_index 指父级），
	// 其余条目仅在显式引用或权威外部编号命中时复用，否则新建。树已在进入前校验。
	unitIDs := make([]string, len(entries))
	// 条目下标 → 该条目的表达 ID：曲目按 entry_index 结构绑定到清单里的稳定节点，
	// 不靠标题在条目之间传递绑定。
	entryExprIDs := make([]string, len(entries))
	for i, ce := range entries {
		title := strings.TrimSpace(ce.Title)
		if title == "" {
			return Entity{}, counts, fmt.Errorf("invalid_payload")
		}
		pos := sanitizePosition(ce.Position, i)
		parent := ""
		if ce.ParentIndex != nil && *ce.ParentIndex >= 0 && *ce.ParentIndex < len(entries) {
			parent = unitIDs[*ce.ParentIndex]
		}
		if strings.TrimSpace(ce.EntryKind) == "content_unit" {
			// 重复导入复用：来源 ID 优先，其次 (父篇目, 标题)/(父篇目, 编号)。
			if existingID, ok := unitIndex.lookup(ce, parent); ok {
				unitIDs[i] = existingID
				rememberUnitTitle(title, existingID)
				continue
			}
			unit, err := s.importerSave(ctx, Entity{
				Kind:             "content_unit",
				Title:            title,
				WorkID:           workID,
				ParentID:         parent,
				Number:           strings.TrimSpace(ce.Number),
				Position:         pos,
				Attributes:       importerContentUnitAttrs(cuFields, ce),
				ExternalIDs:      stringScalarMap(ce.ExternalIDs),
				Translations:     importerTranslationsFromAny(ce.Translations),
				OriginalLanguage: originalLanguageOrEmpty(ce.OriginalLanguage),
			}, actor, note, sources)
			if err != nil {
				return Entity{}, counts, err
			}
			unitIDs[i] = unit.ID
			unitIndex.remember(ce, parent, unit.ID)
			rememberUnitTitle(title, unit.ID)
			counts.ContentUnits++
			continue
		}
		contentUnitID := parent
		// 手工绑定优先：显式 expression_id 命中既有表达即直接引用（允许跨 Work）。
		// 只登记绑定，不改动被复用表达原有的章节归属（复用只应建立引用）。
		if explicit := strings.TrimSpace(ce.ExpressionID); explicit != "" {
			e, ok := explicitExprs[explicit]
			if !ok {
				return Entity{}, counts, fmt.Errorf("invalid_expression_reference")
			}
			register(exprCandidate{id: e.ID, number: e.Number, externalIDs: e.ExternalIDs})
			registerLocal(exprCandidate{id: e.ID, number: e.Number, externalIDs: e.ExternalIDs}, title)
			rememberBound(title, e.ID)
			entryExprIDs[i] = e.ID
			continue
		}
		if cand, ok := lookupAuthoritative(stringScalarMap(ce.ExternalIDs)); ok {
			registerLocal(cand, title)
			entryExprIDs[i] = cand.id
			continue
		}
		// 条目幂等：上次同一清单已建的表达按导入键找回；无权威编号的条目重试
		// 不再重复创建（旧实现会先建表达、后因曲目已存在而跳过，留下孤儿表达）。
		exprKey := importerEntryExprKey(workKey, i, ce, parent)
		if exprKey != "" {
			if prev, ok := s.findImported(ctx, exprKey, &actor); ok && prev.Kind == "expression" && prev.WorkID == workID {
				cand := exprCandidate{id: prev.ID, number: prev.Number, externalIDs: prev.ExternalIDs}
				register(cand)
				registerLocal(cand, title)
				entryExprIDs[i] = prev.ID
				continue
			}
		}
		saved, err := s.createExpressionWithMeta(ctx, actor, note, sources, workID, contentUnitID, title, ce, pos, exprKey)
		if err != nil {
			return Entity{}, counts, err
		}
		register(exprCandidate{id: saved.ID, number: saved.Number, externalIDs: saved.ExternalIDs})
		registerLocal(exprCandidate{id: saved.ID, number: saved.Number, externalIDs: saved.ExternalIDs}, title)
		entryExprIDs[i] = saved.ID
	}
	releaseTitle := strings.TrimSpace(workTitle)
	if rel != nil && strings.TrimSpace(rel.EditionName) != "" {
		releaseTitle = strings.TrimSpace(rel.EditionName)
	}
	// 作品载荷里属于发行层的字段（条码/ISBN）作为发行属性的兜底来源。
	releaseAttrs := importerReleaseAttrs(rel, work)
	if releaseTitle == "" {
		return Entity{}, counts, fmt.Errorf("invalid_payload")
	}
	releaseExternalIDs := map[string]string{}
	if strings.TrimSpace(releaseKey) != "" {
		releaseExternalIDs["metafusion_import"] = strings.TrimSpace(releaseKey)
	}
	// 发行本体声明的原语言/翻译行：写进发行实体，与载体同口径（载荷声明了就得兑现）。
	releaseLang, releaseTranslations := importerReleaseMeta(rel)
	var release Entity
	if existingRelease != nil {
		// 复用已建发行，但**继续走载体/曲目循环**：上次导入可能中途失败，
		// 只建了部分结构，重试需要补齐，而不是整链跳过。
		// 复用时不改写既有 subjects（可能含管理员人工补充），仅记录本次是否需补声明。
		release = *existingRelease
		// 语言/翻译行同理补齐：只在缺值时写，不覆盖已有行。
		merged, changed := mergeImporterMeta(release, releaseLang, releaseTranslations)
		// 封面同口径补齐：只在复用实体尚无图时补。人工换过的图不该被上游下一次导入改回去，
		// 但"上次没写进去"（本次修复前的历史数据、或上次 download_cover=false）要能补上。
		if len(merged.Pictures) == 0 {
			if pictures := importerReleasePictures(rel, workKey); len(pictures) > 0 {
				merged.Pictures = PicturesJSON(pictures)
				changed = true
			}
		}
		if changed {
			updated, uerr := s.importerSaveVersioned(ctx, merged, release.Version, actor, note, sources)
			if uerr != nil {
				return Entity{}, counts, uerr
			}
			release = updated
		}
	} else {
		subjects := []Subject{{WorkID: workID, Role: "primary", Position: 0}}
		// 跨作品收录：被引用作品的 Work 必须声明在发行上（release_role=compilation）。
		otherWorks := make([]string, 0, len(referencedWorks))
		for wid := range referencedWorks {
			otherWorks = append(otherWorks, wid)
		}
		sort.Strings(otherWorks)
		for idx, wid := range otherWorks {
			subjects = append(subjects, Subject{WorkID: wid, Role: "compilation", Position: idx + 1})
		}
		created, cerr := s.importerSave(ctx, Entity{
			Kind:             "release",
			Title:            releaseTitle,
			Attributes:       releaseAttrs,
			ExternalIDs:      releaseExternalIDs,
			Subjects:         subjects,
			Pictures:         PicturesJSON(importerReleasePictures(rel, workKey)),
			OriginalLanguage: releaseLang,
			Translations:     releaseTranslations,
		}, actor, note, sources)
		if cerr != nil {
			return Entity{}, counts, cerr
		}
		release = created
	}
	for i, m := range mediums {
		mediumTitle := strings.TrimSpace(m.Name)
		if mediumTitle == "" {
			mediumTitle = "Disc " + strconv.Itoa(i+1)
		}
		mediumAttrs := importerMediumAttrs(m)
		medPos := sanitizePosition(m.Position, i)
		mediumKey := ""
		if rk := strings.TrimSpace(releaseKey); rk != "" {
			mediumKey = rk + ":m" + strconv.Itoa(medPos)
		}
		mediumExternal := map[string]string{}
		if mediumKey != "" {
			mediumExternal["metafusion_import"] = mediumKey
		}
		var medium Entity
		if mediumKey != "" {
			if ex, ok := s.findImported(ctx, mediumKey, &actor); ok && ex.Kind == "medium" && ex.ReleaseID == release.ID {
				medium = ex
			}
		}
		if medium.ID == "" {
			created, merr := s.importerSave(ctx, Entity{
				Kind:             "medium",
				Title:            mediumTitle,
				ReleaseID:        release.ID,
				Number:           strings.TrimSpace(m.Number),
				Position:         medPos,
				Attributes:       mediumAttrs,
				ExternalIDs:      mediumExternal,
				OriginalLanguage: originalLanguageOrEmpty(m.OriginalLanguage),
				Translations:     importerTranslationsFromAny(m.Translations),
			}, actor, note, sources)
			if merr != nil {
				return Entity{}, counts, merr
			}
			medium = created
			counts.Mediums++
		} else if merged, changed := mergeImporterMeta(medium, m.OriginalLanguage, importerTranslationsFromAny(m.Translations)); changed {
			// 幂等命中的载体同样要补齐本次声明的原语言/翻译行：否则同一份载荷
			// "首次（新建）写进去、重试（复用）被丢掉"，结果取决于上次是否成功。
			updated, uerr := s.importerSaveVersioned(ctx, merged, medium.Version, actor, note, sources)
			if uerr != nil {
				return Entity{}, counts, uerr
			}
			medium = updated
		}
		for j, t := range m.Tracks {
			trackTitle := strings.TrimSpace(t.Title)
			if trackTitle == "" {
				return Entity{}, counts, fmt.Errorf("invalid_payload")
			}
			pos := sanitizePosition(t.Position, j)
			trackKey := ""
			if mediumKey != "" {
				trackKey = mediumKey + ":t" + strconv.Itoa(pos)
			}
			// 曲目级幂等：先查上次是否已建这条轨道。已建时跳过录音解析与新建——
			// 旧实现先建 canonical 表达、到末尾才按轨位键 continue，重试会留下无引用的
			// 重复表达。仅当用户显式重选录音（expression_id）且与已存绑定不同时，才明确
			// 比较并更新收录；自动依据（权威编号/标题结构）不覆盖既有绑定。
			if trackKey != "" {
				if ex, ok := s.findImported(ctx, trackKey, &actor); ok && ex.Kind == "track" && ex.MediumID == medium.ID {
					if explicit := strings.TrimSpace(t.ExpressionID); explicit != "" {
						if _, ok := explicitExprs[explicit]; !ok {
							return Entity{}, counts, fmt.Errorf("invalid_expression_reference")
						}
						cur := ""
						if len(ex.Contents) > 0 {
							cur = ex.Contents[0].ExpressionID
						}
						if cur != explicit {
							// 跨作品重选：被引用作品必须声明在发行 subjects 上，
							// 否则 undeclared_release_subject 校验会拒绝保存。
							if ref := explicitExprs[explicit]; ref.WorkID != "" && ref.WorkID != workID && !releaseDeclaresWork(release, ref.WorkID) {
								withSubject := release
								withSubject.Subjects = append(append([]Subject{}, release.Subjects...), Subject{WorkID: ref.WorkID, Role: "compilation", Position: len(release.Subjects)})
								savedRel, serr := s.importerSaveVersioned(ctx, withSubject, release.Version, actor, note, sources)
								if serr != nil {
									return Entity{}, counts, serr
								}
								release = savedRel
							}
							contents := ex.Contents
							if len(contents) == 0 {
								contents = []Inclusion{{Position: 0}}
							}
							contents[0].ExpressionID = explicit
							ex.Contents = contents
							if _, err := s.importerSaveVersioned(ctx, ex, ex.Version, actor, note, sources); err != nil {
								return Entity{}, counts, err
							}
						}
					}
					continue
				}
			}
			// 与预检同源：recording_mbid/isrc 属于录音本体，落 expression.external_ids。
			trackExternal := importerTrackExpressionExternalIDs(t)
			expressionID := ""
			number := ""
			// 解析优先级：曲目显式指定 → 按 entry_index 结构绑定（同次清单的稳定节点）
			// → 权威外部编号 → 清单内唯一同名条目 → 新建。
			// 标题只在清单内唯一同名时作线索，多义时一律不自动绑定。
			if explicit := strings.TrimSpace(t.ExpressionID); explicit != "" {
				e, ok := explicitExprs[explicit]
				if !ok {
					return Entity{}, counts, fmt.Errorf("invalid_expression_reference")
				}
				expressionID, number = e.ID, e.Number
			} else if t.EntryIndex != nil && *t.EntryIndex >= 0 && *t.EntryIndex < len(entryExprIDs) && entryExprIDs[*t.EntryIndex] != "" {
				expressionID = entryExprIDs[*t.EntryIndex]
				if c, ok := byID[expressionID]; ok {
					number = c.number
				}
			} else if bound, ok := lookupBound(trackTitle); ok {
				// canonical entry 已显式绑定该标题 → 曲目复用同一表达（用户声明的绑定，非猜测）。
				expressionID = bound
				if c, ok := byID[bound]; ok {
					number = c.number
				}
			} else if cand, ok := lookupAuthoritative(trackExternal); ok {
				// 权威外部编号（recording_mbid/isrc）命中即复用。
				expressionID, number = cand.id, cand.number
			} else if cand, ok := lookupLocalTitle(trackTitle); ok {
				// 同一份载荷中声明的 canonical 表达（唯一同名）→ 曲目引用之，
				// 这是同次导入的结构绑定，不是对库里既有实体的身份猜测。
				expressionID, number = cand.id, cand.number
			} else {
				externalAny := make(map[string]any, len(trackExternal))
				for k, v := range trackExternal {
					externalAny[k] = v
				}
				// 曲目标题命中同 Work 的篇目/分集时，新建的表达归属该单元
				// （分集录像/正文挂在对应篇目下），否则保持 Work 直接下属。
				// 同标题多个篇目视为歧义，不自动挂载。
				cuID := unitIDByTitle(trackTitle)
				expr, err := s.createExpression(ctx, actor, note, sources, workID, cuID, trackTitle, "", pos, t.DurationSeconds, externalAny)
				if err != nil {
					return Entity{}, counts, err
				}
				expressionID = expr.ID
				register(exprCandidate{id: expr.ID, number: expr.Number, externalIDs: expr.ExternalIDs})
			}
			trackAttrs := importerTrackAttrs(t)
			// ISRC 属于录音本体：已写入 expression.ExternalIDs（见上方 trackExternal），
			// track 定义只声明 duration/role，不能再写 isrc（会 unknown_field 拒绝）。
			trackNumber := number
			if trackNumber == "" && pos > 0 {
				trackNumber = strconv.Itoa(pos)
			}
			trackExternalIDs := map[string]string{}
			if trackKey != "" {
				trackExternalIDs["metafusion_import"] = trackKey
			}
			if _, err := s.importerSave(ctx, Entity{
				Kind:        "track",
				Title:       trackTitle,
				MediumID:    medium.ID,
				Number:      trackNumber,
				Position:    pos,
				Attributes:  trackAttrs,
				ExternalIDs: trackExternalIDs,
				Contents:    []Inclusion{{ExpressionID: expressionID, Position: 0}},
			}, actor, note, sources); err != nil {
				return Entity{}, counts, err
			}
			counts.Tracks++
		}
	}
	return release, counts, nil
}

// buildReleaseTitle import 载荷可直接决定 release 标题；缺失时回退 work 标题。
func buildReleaseTitle(workTitle string, rel *ImporterReleasePreview) string {
	if rel != nil && strings.TrimSpace(rel.EditionName) != "" {
		return strings.TrimSpace(rel.EditionName)
	}
	return strings.TrimSpace(workTitle)
}

// Import 落库：work/artist 分支 + link_mode（new_work / append_release_to_work /
// create_relation）；append_release_to_work 挂靠目标 work 只补发行链。
func (s *Store) Import(ctx context.Context, req ImporterImportRequest, actor User) (ImporterImportResponse, error) {
	// 所有来源的导入载荷都校验 entity_type，非法值返回 invalid_entity_type；
	// Bangumi Preview 也用此函数，DLsite/DMM Preview 则固定返回 work。
	entityType, err := normalizeImporterEntityType(req.EntityType)
	if err != nil {
		return ImporterImportResponse{}, err
	}
	// 与 Preview 共用具体来源归一化，但这里不调用探测函数：空值/auto 默认 bangumi。
	// 客户端应提交 Preview 返回的 source，保证幂等键、external_ids 和证据使用真实来源。
	source, err := normalizeImporterSource(req.Source)
	if err != nil {
		return ImporterImportResponse{}, err
	}
	note, sources := importerEvidence(req, source)
	mode, err := normalizeImporterLinkMode(req.LinkMode)
	if err != nil {
		return ImporterImportResponse{}, err
	}
	// 四个此前"只有声明、没有读取点"的字段在此收口（is_master_verified / media_type_hint /
	// has_release / download_cover，逐条见 importerApplyFieldSwitches）。
	// download_cover 显式 false 会改写请求（不引用远端封面/头像），后续预检与落库用改写后的载荷。
	if req, err = importerApplyFieldSwitches(req); err != nil {
		return ImporterImportResponse{}, err
	}
	// 先做纯参数校验（不触库），保持"非法载荷在写库前失败"的既有约定。
	switch mode {
	case "append_release_to_work":
		if strings.TrimSpace(req.TargetWorkID) == "" {
			return ImporterImportResponse{}, fmt.Errorf("invalid_payload")
		}
	case "create_relation":
		if strings.TrimSpace(req.TargetWorkID) == "" || strings.TrimSpace(req.RelationType) == "" || req.Work == nil {
			return ImporterImportResponse{}, fmt.Errorf("invalid_payload")
		}
	}
	// 写库前整体预检：属性字段码、显式表达引用、写死映射与关联关系码先校验，
	// 避免先建 work/release/medium 再在某条曲目/关系处失败，留下半成品结构。
	if pfErr := s.importerPreflight(ctx, actor, req, mode, entityType, source); pfErr != nil {
		return ImporterImportResponse{}, pfErr
	}
	if err := validateSources(note, sources); err != nil {
		return ImporterImportResponse{}, err
	}
	if mode == "append_release_to_work" {
		target, gerr := s.Get(ctx, strings.TrimSpace(req.TargetWorkID), &actor)
		if gerr != nil || target.Kind != "work" {
			return ImporterImportResponse{}, fmt.Errorf("not_found")
		}
		workTitle := target.Title
		if req.Work != nil && strings.TrimSpace(req.Work.Title) != "" {
			workTitle = strings.TrimSpace(req.Work.Title)
		}
		// 挂靠已有 work 补发行链：幂等基础键优先取**本次载荷自身的来源身份**（如另一张
		// 专辑/另一个地区版的 subject ID）。若一律沿用目标 work 的导入键，同一作品下
		// 追加的不同地区同名版本会与首个发行共用基础键，内容签名稍有重叠即被误并。
		// 来源身份不可解析（手工载荷无 url_or_id）时回退目标 work 的导入键。
		appendReleaseKey := ""
		if payloadKey, pok := importDedupKey(source, req, entityType); pok {
			appendReleaseKey = payloadKey + ":release"
		} else if wk := strings.TrimSpace(target.ExternalIDs["metafusion_import"]); wk != "" {
			appendReleaseKey = wk + ":release"
		}
		release, counts, rerr := s.importReleaseChain(ctx, actor, note, sources, target.ID, strings.TrimSpace(target.ExternalIDs["metafusion_import"]), buildReleaseTitle(workTitle, req.Release), req.CanonicalEntries, req.Release, req.Mediums, appendReleaseKey, req.Work)
		if rerr != nil {
			return ImporterImportResponse{}, rerr
		}
		return ImporterImportResponse{
			Success: true, EntityType: "work",
			WorkID: target.ID, ReleaseID: release.ID,
			Work: target, Release: release,
			ImportedCounts: counts,
			RedirectURL:    "/releases/" + release.ID,
		}, nil
	}
	if mode == "create_relation" {
		target, gerr := s.Get(ctx, strings.TrimSpace(req.TargetWorkID), &actor)
		if gerr != nil || target.Kind != "work" {
			return ImporterImportResponse{}, fmt.Errorf("not_found")
		}
		out, werr := s.importNewWork(ctx, actor, note, sources, source, req, entityType)
		if werr != nil {
			return ImporterImportResponse{}, werr
		}
		rel, rerr := s.SaveRelation(ctx, RelationEdit{
			Relation: Relation{Type: strings.TrimSpace(req.RelationType), SourceID: out.WorkID, TargetID: target.ID, Attributes: map[string]any{}},
			EditNote: note, Sources: sources,
		}, actor)
		if rerr != nil {
			return ImporterImportResponse{}, rerr
		}
		_ = rel
		out.RedirectURL = "/works/" + out.WorkID
		return out, nil
	}
	return s.importNewWork(ctx, actor, note, sources, source, req, entityType)
}

// importerAssociationRelationAttrs 计算一条关联要写进关系边的属性。
// characterID 是本次导入在上游解析到的角色实体 ID（voiced_by 专用：**entity 引用字段**填 ID
// 而非角色名；解析不到则降级为 credit_role 文本，不丢信息）。
// 写路径（importNewWork 第二趟）与导入预检共用同一实现，两边属性集必然一致。
func importerAssociationRelationAttrs(code string, relDef RelationDefinition, assoc ImporterStaffAssociation, characterID string) map[string]any {
	attrs := map[string]any{}
	if cr := strings.TrimSpace(assoc.ParsedRole); cr != "" {
		attrs["credit_role"] = cr
	}
	// 番位落 character_rank（definitions 的角色番位字段），不再借 role（载体用途词表）。
	// 关系定义未声明该字段的旧实例降级为不写：原始番位文本已进 credit_role，不丢信息，
	// 也不会因 unknown_field 让整条导入失败。
	if rr := strings.TrimSpace(assoc.RelationRole); rr != "" && code == "character_in" && contains(relDef.Fields, "character_rank") {
		attrs["character_rank"] = rr
	}
	if ch := strings.TrimSpace(assoc.CharacterName); ch != "" && code == "voiced_by" {
		if characterID != "" {
			attrs["character"] = characterID
		} else {
			attrs["credit_role"] = "配音：" + ch
		}
	}
	return attrs
}

// importNewWork 新建 work（或 agent）并按需建发行链与演职员。
//
// 非原子说明：导入是"循环逐个 Save"，预检只读，Save 阶段仍可中途失败留下半成品
// （例如发行链建到一半、关系建到一半）。不做跨多次 Save 的大事务重构（Save 内部
// 各自独立事务 + 审计，合并事务会改变审计/版本语义），补偿方式是**重试补齐**：
// work/release/medium/track/expression 都有导入幂等键，重试会复用已建部分并继续
// 补齐（见 importerReleaseVariantKey/importerEntryExprKey）。需要整体回退时按该
// 导入键前缀走常规删除/合并流程。
func (s *Store) importNewWork(ctx context.Context, actor User, note string, sources []Source, source string, req ImporterImportRequest, entityType string) (ImporterImportResponse, error) {
	if entityType != "work" {
		return s.importNewAgent(ctx, actor, note, sources, source, req, entityType)
	}
	if req.Work == nil {
		return ImporterImportResponse{}, fmt.Errorf("invalid_payload")
	}
	key, hasKey := importDedupKey(source, req, entityType)
	// 幂等：已导入过同一外部条目时复用该 work，但**不再提前返回**——
	// 否则再次导入无法为已有作品补齐关联演职员/角色与关系（增量补录场景）。
	// 主日期字段码由模板声明（默认 edition_date）。过去这里误用"作品类型码"
	// 当字段名，会把 attributes["animation"] 之类的非法键写进去，导致增量补录
	// 被校验拒绝（unknown_field）。改为与新建路径同一来源。
	dateField := ""
	if defs, derr := s.Definitions(ctx); derr == nil {
		dateField = defs.Document.PrimaryDateField()
	}
	var savedWork Entity
	if hasKey {
		if existing, ok := s.findImported(ctx, key, &actor); ok && existing.Kind == "work" {
			// 已导入过：补齐**当时缺失**的元数据（原语言/别名/官网/品番/日期/标签/
			// infobox 派生字段），便于老条目增量补录；已有值一律不覆盖。
			merged, changed := mergeWorkMetadata(existing, req.Work, dateField)
			if changed {
				updated, uerr := s.importerSaveVersioned(ctx, merged, existing.Version, actor, note, sources)
				if uerr != nil {
					return ImporterImportResponse{}, uerr
				}
				savedWork = updated
			} else {
				savedWork = existing
			}
		}
	}
	// 删后禁自动重导：findImported 未命中但墓碑仍占唯一键时，在此直接返回稳定业务错误
	// （import_deleted + 墓碑恢复入口），不建新行撞唯一索引、不删墓碑、不自动复活。
	if hasKey && savedWork.ID == "" {
		if tombID, ok := s.deletedImportTombstone(ctx, key); ok {
			return ImporterImportResponse{}, deletedImportError(key, tombID)
		}
	}
	if savedWork.ID == "" {
		work, err := buildWorkEntity(req.Work, source, key, req.ExternalID, hasKey, dateField)
		if err != nil {
			return ImporterImportResponse{}, err
		}
		savedWork, err = s.importerSave(ctx, work, actor, note, sources)
		if err != nil {
			// 并发双插：唯一索引让后到者失败，此处按幂等键复用胜者实体
			// 并继续补齐（与重试语义一致），而不是报重复错误。
			// 无键载荷（hasKey=false）没有复用依据，直接返回原错。
			if hasKey {
				if existing, ok := s.findImported(ctx, key, &actor); ok && existing.Kind == "work" {
					savedWork = existing
				} else {
					return ImporterImportResponse{}, err
				}
			} else {
				return ImporterImportResponse{}, err
			}
		}
	}
	out := ImporterImportResponse{
		Success: true, EntityType: "work",
		WorkID: savedWork.ID, Work: savedWork,
		RedirectURL: "/works/" + savedWork.ID,
	}
	counts := ImporterImportedCounts{}
	// 两趟：先生成/复用全部 agent（按导入键与名称去重，同一人物多职位只建一个实体），
	// 再建立关系。角色必须先建好，声优关系才能用它的实体 ID 填 character 引用。
	agentByKey := map[string]Entity{} // 导入键 → agent 实体
	characterByName := map[string]Entity{}
	for _, assoc := range req.StaffAssociations {
		if strings.ToLower(strings.TrimSpace(assoc.Action)) == "skip" {
			continue
		}
		if tid := strings.TrimSpace(assoc.TargetArtistID); tid != "" {
			if e, gerr := s.Get(ctx, tid, &actor); gerr == nil && e.Kind == "agent" {
				agentByKey["id:"+tid] = e
			}
			continue
		}
		name := strings.TrimSpace(assoc.ParsedName)
		if name == "" {
			continue
		}
		entityType := staffAgentType(assoc.EntityType)
		dedup := assocImportKey(assoc.ExternalIDs)
		if dedup == "" {
			dedup = "name:" + strings.ToLower(name) + "|" + entityType
		}
		if _, ok := agentByKey[dedup]; ok {
			continue
		}
		// 先按外部导入键复用已存在的 agent，避免重复导入产生同名副本。
		if iKey := assocImportKey(assoc.ExternalIDs); iKey != "" {
			if existing, ok := s.findImported(ctx, iKey, &actor); ok && existing.Kind == "agent" {
				// 已知 agent 补齐缺失元数据（类型纠正/简介/语言/封面），不覆盖已有值。
				if merged, changed := mergeAgentMetadata(existing, assoc); changed {
					if updated, uerr := s.importerSaveVersioned(ctx, merged, existing.Version, actor, note, sources); uerr == nil {
						existing = updated
						counts.Artists++
					}
				}
				agentByKey[dedup] = existing
				if strings.TrimSpace(assoc.RelationType) == "character_in" {
					characterByName[strings.ToLower(name)] = existing
				}
				continue
			}
		} else if existing, ok := s.findAgentByTitle(ctx, name, &actor); ok {
			// 无外部键的关联（手工载荷）按标题复用，避免重复导入产生同名 agent。
			if merged, changed := mergeAgentMetadata(existing, assoc); changed {
				if updated, uerr := s.importerSaveVersioned(ctx, merged, existing.Version, actor, note, sources); uerr == nil {
					existing = updated
				}
			}
			agentByKey[dedup] = existing
			if strings.TrimSpace(assoc.RelationType) == "character_in" {
				characterByName[strings.ToLower(name)] = existing
			}
			continue
		}
		staff := Entity{
			Kind:             "agent",
			Title:            name,
			OriginalLanguage: originalLanguageOrEmpty(assoc.Language),
			Translations:     toEntityTranslations(assoc.Translations),
			Attributes:       map[string]any{},
			ExternalIDs:      importerAgentExternalIDs(assoc.ExternalIDs),
		}
		if p, ok := pictureFromRemote(assoc.AvatarURL, "导入头像", "", false); ok {
			staff.Pictures = PicturesJSON{p}
		}
		if strings.TrimSpace(assoc.Biography) != "" {
			applyWorkSummary(&staff, assoc.Biography)
		}
		agent, aerr := s.importerSave(ctx, staff, actor, note, sources)
		if aerr != nil {
			// 并发双插：有键时按导入键复用胜者（与 work 路径一致）；
			// 无键手工载荷无复用依据，直接返回原错（空键零幂等的明确语义）。
			if iKey := assocImportKey(assoc.ExternalIDs); iKey != "" {
				if existing, ok := s.findImported(ctx, iKey, &actor); ok && existing.Kind == "agent" {
					agentByKey[dedup] = existing
					if strings.TrimSpace(assoc.RelationType) == "character_in" {
						characterByName[strings.ToLower(name)] = existing
					}
					continue
				}
			}
			return ImporterImportResponse{}, aerr
		}
		agentByKey[dedup] = agent
		counts.Artists++
		if strings.TrimSpace(assoc.RelationType) == "character_in" {
			characterByName[strings.ToLower(name)] = agent
		}
	}

	// 第二趟：把关联落到关系上。方向按 definitions 判定（character_in 是 agent → work，
	// 其余署名关系是 work → agent），因此不能写死。
	relDefs, derr := s.Definitions(ctx)
	if derr != nil {
		return ImporterImportResponse{}, derr
	}
	// 关系去重键：同一载荷内同一（类型|源|目标|属性）只建一次。服务端判重
	// （duplicate_relation）是最后一道网：并发双写时仍可能一方判重失败，
	// 此时按 importerRelationSkippable 计入跳过，不视为导入失败。
	created := map[string]bool{}
	// 已存在边预查：Import 建关系无去重键、过去靠吞 duplicate_relation 幂等——
	// 重试每次都尝试重建，错误计数与审计噪音都大。此处按（类型|源|目标|属性）
	// 先查一次已存在边，命中直接跳过；查不到仍走 SaveRelation，判重失败兜底。
	existingRels := map[string]bool{}
	if rels, rerr := s.Relations(ctx, savedWork.ID, &actor); rerr == nil {
		for _, r := range rels {
			existingRels[r.Type+"|"+r.SourceID+"|"+r.TargetID+"|"+encode(r.Attributes)] = true
		}
	}
	for _, assoc := range req.StaffAssociations {
		if strings.ToLower(strings.TrimSpace(assoc.Action)) == "skip" {
			counts.SkippedRelations++
			continue
		}
		relType := strings.TrimSpace(assoc.RelationType)
		if relType == "" {
			counts.SkippedRelations++
			continue
		}
		relDef, ok := relDefs.Document.Relations[relType]
		if !ok {
			// 无此关系定义：不虚构，跳过并计数（响应不再静默丢边）。
			counts.SkippedRelations++
			continue
		}
		agent, ok := agentByKey[assocAgentDedup(assoc)]
		if !ok {
			counts.SkippedRelations++
			continue
		}
		characterID := ""
		if ch := strings.TrimSpace(assoc.CharacterName); ch != "" && relType == "voiced_by" {
			if ce, ok := characterByName[strings.ToLower(ch)]; ok {
				characterID = ce.ID
			}
		}
		attrs := importerAssociationRelationAttrs(relType, relDef, assoc, characterID)
		src, tgt := savedWork.ID, agent.ID
		if contains(relDef.SourceKinds, "agent") && !contains(relDef.SourceKinds, "work") {
			src, tgt = agent.ID, savedWork.ID
		}
		key := relType + "|" + src + "|" + tgt + "|" + encode(attrs)
		if created[key] || existingRels[key] {
			continue
		}
		created[key] = true
		if _, rerr := s.SaveRelation(ctx, RelationEdit{
			Relation: Relation{Type: relType, SourceID: src, TargetID: tgt, Attributes: attrs},
			EditNote: note, Sources: sources,
		}, actor); rerr != nil {
			if importerRelationSkippable(rerr) {
				counts.SkippedRelations++
				continue
			}
			return ImporterImportResponse{}, rerr
		}
		counts.Relations++
	}
	out.ImportedCounts.Artists = counts.Artists
	out.ImportedCounts.Relations = counts.Relations
	out.ImportedCounts.SkippedRelations = counts.SkippedRelations
	if len(req.Mediums) == 0 && len(req.CanonicalEntries) == 0 {
		return out, nil
	}
	if len(req.Mediums) == 0 {
		cuCounts, err := s.importExpressionsOnly(ctx, actor, note, sources, savedWork.ID, key, req.CanonicalEntries)
		if err != nil {
			return ImporterImportResponse{}, err
		}
		// 无发行链时也要把篇目计数并入响应，否则前端看不到导入的章节树。
		out.ImportedCounts.ContentUnits = cuCounts.ContentUnits
		return out, nil
	}
	releaseKey := ""
	if hasKey {
		releaseKey = key + ":release"
	}
	release, rcounts, rerr := s.importReleaseChain(ctx, actor, note, sources, savedWork.ID, key, req.Work.Title, req.CanonicalEntries, req.Release, req.Mediums, releaseKey, req.Work)
	if rerr != nil {
		return ImporterImportResponse{}, rerr
	}
	// 发行链统计需保留已建的关联与篇目计数，否则响应会把关联上报成 0。
	rcounts.Artists = counts.Artists
	rcounts.Relations = counts.Relations
	rcounts.SkippedRelations = counts.SkippedRelations
	rcounts.ContentUnits = counts.ContentUnits
	out.ReleaseID, out.Release = release.ID, release
	out.ImportedCounts = rcounts
	out.RedirectURL = "/releases/" + release.ID
	return out, nil
}

// importerTopArtist 取顶层 agent 导入的预览：Artist 优先，其次 Artists 的首个。
// 写路径（importNewAgent）与预检共用，避免两边对"哪个预览才是本次要建的 agent"判断不一。
func importerTopArtist(req ImporterImportRequest) *ImporterArtistPreview {
	switch {
	case req.Artist != nil:
		return req.Artist
	case len(req.Artists) > 0:
		return &req.Artists[0]
	}
	return nil
}

// importNewAgent 新建 agent（artist / organization / character）。
func (s *Store) importNewAgent(ctx context.Context, actor User, note string, sources []Source, source string, req ImporterImportRequest, entityType string) (ImporterImportResponse, error) {
	a := importerTopArtist(req)
	if a == nil {
		return ImporterImportResponse{}, fmt.Errorf("invalid_payload")
	}
	lang := a.Language
	key, hasKey := importDedupKey(source, req, entityType)
	if hasKey {
		if existing, ok := s.findImported(ctx, key, &actor); ok && existing.Kind == "agent" {
			// 顶层 agent 命中已存在：同样回填缺失元数据（简介/语言/封面/类型纠正），
			// 与 work 导入关联路径的增量补录对齐；已有值不覆盖。
			merged, changed := mergeAgentMetadata(existing, ImporterStaffAssociation{
				EntityType:  a.EntityType,
				Language:    a.Language,
				Biography:   a.Biography,
				AvatarURL:   a.AvatarURL,
				ExternalIDs: a.ExternalIDs,
			})
			if changed {
				if updated, uerr := s.importerSaveVersioned(ctx, merged, existing.Version, actor, note, sources); uerr == nil {
					existing = updated
				}
			}
			return ImporterImportResponse{
				Success: true, EntityType: entityType,
				ArtistID: existing.ID, Artist: existing,
				RedirectURL: "/artists/" + existing.ID,
			}, nil
		}
		// 删后禁自动重导（与 work 路径同口径）：墓碑占位时直接返回稳定业务错误。
		if tombID, ok := s.deletedImportTombstone(ctx, key); ok {
			return ImporterImportResponse{}, deletedImportError(key, tombID)
		}
	}
	agent, err := buildAgentEntity(a.Name, a.OriginalName, a.Biography, a.AvatarURL, lang, entityType, a.Translations, a.ExternalIDs, key, hasKey)
	if err != nil {
		return ImporterImportResponse{}, err
	}
	// 预览侧细化类型优先于 URL 推断：乐队型"角色"（如 MyGO!!!!!）在预览时
	// 识别为 group，导入保持 group agent；character 行为不变；
	// person 不覆盖请求推断（URL 推断的 artist/organization 语义更具体）。
	if et := agentTypeForPreviewValue(a.EntityType); et != "" && et != "person" {
		entityType = et
	}
	saved, serr := s.importerSave(ctx, agent, actor, note, sources)
	if serr != nil {
		return ImporterImportResponse{}, serr
	}
	return ImporterImportResponse{
		Success: true, EntityType: entityType,
		ArtistID: saved.ID, Artist: saved,
		ImportedCounts: ImporterImportedCounts{Artists: 1},
		RedirectURL:    "/artists/" + saved.ID,
	}, nil
}
