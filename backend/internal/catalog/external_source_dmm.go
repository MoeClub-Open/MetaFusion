package catalog

// DMM（www.dmm.co.jp / dlsoft.dmm.co.jp）导入适配器。
//
// DMM 没有免费公开的商品 JSON API（旧商品検索 API 需要 affiliate_id / api_id），
// 但其详情页内嵌 application/ld+json（Product：名称、封面、简介、ジャンル、
// 发售日、brand サークル/メーカー），并以 og:* meta 作为兜底。适配器抓取详情页
// HTML（带 age_check_done cookie 通过年龄确认）后解析这些结构化片段；
// 解析不到名称时返回 upstream_error，不伪造字段。
//
// 落库与 DLsite 同一套通用 Import 流程，图片只做远端 URL 引用。

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// dmmCoJPBase / dmmSoftBase / dmmGateBase 允许单测替换为 httptest 服务。
// 年龄门（含 dlsoft 子域）统一在 www.dmm.co.jp 的 /age_check 处理。
var (
	dmmCoJPBase = "https://www.dmm.co.jp"
	dmmSoftBase = "https://dlsoft.dmm.co.jp"
	dmmGateBase = "https://www.dmm.co.jp"
)

// dmmHTTPClient 带 cookie jar：DMM 年龄确认要求先走 declared=yes 取得
// age_check_done / ckcy / guest_id 整套 cookie（只发 age_check_done 会被重定向），
// 且该 cookie 对 dlsoft 子域同样生效。
var dmmHTTPClient = newDMMHTTPClient()

func newDMMHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 20 * time.Second, Jar: jar}
}

// dmmRef 是解析后的 DMM 商品坐标。
type dmmRef struct {
	Channel string // dc/doujin、mono/doujin、digital/anim、dlsoft ...
	CID     string
}

// dmmKnownChannels 是 www.dmm.co.jp 上可识别的商品频道（路径前两段）。
var dmmKnownChannels = map[string]bool{
	"dc/doujin": true, "mono/doujin": true,
	"digital/anim": true, "digital/vug2": true, "digital/comic": true, "digital/book": true,
	"mono/anime": true, "mono/game": true,
}

// dmmBareCIDPattern 收敛裸商品号（字母数字下划线）。
var dmmBareCIDPattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// parseDMMRef 解析商品号或 DMM 详情页 URL。
func parseDMMRef(raw string) (dmmRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return dmmRef{}, fmt.Errorf("invalid_payload")
	}
	normalized := s
	if !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	// 裸 cid（无路径分隔形态）：默认按数字同人频道处理。
	if !strings.Contains(s, "/") && dmmBareCIDPattern.MatchString(strings.ToLower(s)) {
		return dmmRef{Channel: "dc/doujin", CID: strings.ToLower(s)}, nil
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" {
		return dmmRef{}, fmt.Errorf("invalid_payload")
	}
	host := strings.ToLower(u.Host)
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	isSoft := host == "dlsoft.dmm.co.jp"
	// 后缀匹配，避免仿冒域名。
	if !isSoft && host != "dmm.co.jp" && !strings.HasSuffix(host, ".dmm.co.jp") {
		return dmmRef{}, fmt.Errorf("not_supported")
	}
	channel := ""
	cid := ""
	// 路径以 / 开头，Split 首段为空串：先过滤空段，频道取前两个非空段。
	segments := []string{}
	for _, seg := range strings.Split(u.Path, "/") {
		if strings.TrimSpace(seg) != "" {
			segments = append(segments, seg)
		}
	}
	if isSoft {
		// /detail/{cid}/
		for i, seg := range segments {
			if strings.ToLower(seg) == "detail" && i+1 < len(segments) {
				cid = strings.ToLower(strings.TrimSpace(segments[i+1]))
			}
		}
		channel = "dlsoft"
	} else {
		if len(segments) >= 2 {
			candidate := strings.ToLower(segments[0]) + "/" + strings.ToLower(segments[1])
			if dmmKnownChannels[candidate] {
				channel = candidate
			}
		}
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if strings.HasPrefix(seg, "cid=") {
				cid = strings.ToLower(strings.TrimPrefix(seg, "cid="))
			}
		}
		if channel == "" && len(segments) >= 2 {
			// 未在白名单的频道仍保留路径前两段，交由兜底类型处理。
			channel = strings.ToLower(segments[0]) + "/" + strings.ToLower(segments[1])
		}
	}
	cid = strings.Trim(cid, "?=&")
	if cid == "" {
		return dmmRef{}, fmt.Errorf("invalid_payload")
	}
	return dmmRef{Channel: channel, CID: cid}, nil
}

// canonicalURL 返回面向用户的规范地址。
func (r dmmRef) canonicalURL() string {
	if r.Channel == "dlsoft" {
		return fmt.Sprintf("https://dlsoft.dmm.co.jp/detail/%s/", r.CID)
	}
	return fmt.Sprintf("https://www.dmm.co.jp/%s/-/detail/=/cid=%s/", r.Channel, r.CID)
}

// requestURL 返回实际抓取地址（base 可被单测替换）。
func (r dmmRef) requestURL() string {
	if r.Channel == "dlsoft" {
		return dmmSoftBase + "/detail/" + r.CID + "/"
	}
	return dmmCoJPBase + "/" + r.Channel + "/-/detail/=/cid=" + r.CID + "/"
}

// ---- 页面结构化片段 ----

// dmmJSONLD 是详情页 Product JSON-LD 的最小映射。
type dmmJSONLD struct {
	Type          string          `json:"@type"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Image         json.RawMessage `json:"image"`
	Genre         []string        `json:"genre"`
	DatePublished string          `json:"datePublished"`
	Brand         dmmLDName       `json:"brand"`
	Author        dmmLDName       `json:"author"`
	SKU           string          `json:"sku"`
	// WorkForm 是信息表「作品形式」（コミック / ボイス・ASMR / ゲーム…），
	// 部分商品 JSON-LD 不含 datePublished / genre，由信息表兜底。
	WorkForm string `json:"-"`
}

type dmmLDName struct {
	Name string `json:"name"`
}

var (
	dmmJSONLDPattern = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	dmmMetaPattern   = regexp.MustCompile(`(?is)<meta[^>]+>`)
	dmmAttrPattern   = regexp.MustCompile(`(?:property|name|content)=["']([^"']*)["']`)
	// 新版详情页商品信息：<dt class="informationList__ttl">項目</dt>
	// <dd class="informationList__txt">値</dd>；ジャンル是标签列表，dd class 为
	// informationList__item，故两种 class 都要匹配。
	dmmInfoPairRe  = regexp.MustCompile(`(?is)<dt class="informationList__ttl">(.*?)</dt>\s*<dd class="informationList__(txt|item)">(.*?)</dd>`)
	dmmSlashDateRe = regexp.MustCompile(`([0-9]{4})/([0-9]{1,2})/([0-9]{1,2})`)
)

// dmmFirstImage 解析 JSON-LD image（字符串或字符串数组）。
func dmmFirstImage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}

// dmmMeta 从 HTML 的 meta 标签中取指定 property 的 content（属性顺序不定，逐标签解析）。
func dmmMeta(page, property string) string {
	for _, m := range dmmMetaPattern.FindAllString(page, -1) {
		attrs := dmmAttrPattern.FindAllStringSubmatch(m, -1)
		matched := false
		content := ""
		for _, a := range attrs {
			if a[1] == property {
				matched = true
			}
			if strings.HasPrefix(a[1], "http") || (a[1] != property && !strings.HasPrefix(a[1], "og:") && !strings.HasPrefix(a[1], "twitter:")) {
				content = a[1]
			}
		}
		if matched && content != "" {
			return html.UnescapeString(content)
		}
	}
	return ""
}

// parseDMMPage 从详情页 HTML 提取结构化数据；JSON-LD 优先，og:* 兜底。
func parseDMMPage(page string) dmmJSONLD {
	var ld dmmJSONLD
	found := false
	for _, m := range dmmJSONLDPattern.FindAllStringSubmatch(page, -1) {
		body := strings.TrimSpace(m[1])
		var candidate dmmJSONLD
		if err := json.Unmarshal([]byte(body), &candidate); err != nil {
			continue
		}
		if !strings.Contains(candidate.Type, "Product") {
			continue
		}
		ld = candidate
		found = true
		break
	}
	if !found {
		ld.Type = "Product"
	}
	if strings.TrimSpace(ld.Name) == "" {
		ld.Name = dmmMeta(page, "og:title")
	}
	if strings.TrimSpace(ld.Description) == "" {
		ld.Description = dmmMeta(page, "og:description")
	}
	if len(dmmFirstImage(ld.Image)) == 0 {
		if img := dmmMeta(page, "og:image"); img != "" {
			raw, _ := json.Marshal(img)
			ld.Image = raw
		}
	}
	// 信息表兜底：部分商品 JSON-LD 只有 name/image/brand/description，
	// 日期、ジャンル、作品形式需从 informationList 补。
	for _, pair := range dmmInfoPairRe.FindAllStringSubmatch(page, -1) {
		key := dlsiteTextClean(pair[1])
		td := pair[3]
		switch key {
		case "配信開始日", "発売日", "販売日", "販売開始日":
			if ld.DatePublished == "" {
				if dm := dmmSlashDateRe.FindStringSubmatch(td); dm != nil {
					ld.DatePublished = fmt.Sprintf("%s-%02s-%02s", dm[1], dm[2], dm[3])
				}
			}
		case "ジャンル":
			if len(ld.Genre) == 0 {
				ld.Genre = dmmGenreCell(td)
			}
		case "作品形式":
			if form := dlsiteTextClean(td); form != "" {
				ld.WorkForm = strings.SplitN(form, " ", 2)[0]
			}
		}
	}
	return ld
}

// dmmGenreCell 解析信息表ジャンル单元格：链接文本优先，无链接按空格拆分多标签。
func dmmGenreCell(td string) []string {
	names := []string{}
	for _, m := range dlsiteLinkRe.FindAllStringSubmatch(td, -1) {
		if n := dlsiteTextClean(m[1]); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		names = strings.Split(dlsiteTextClean(td), " ")
	}
	return dlsiteUnique(names)
}

// externalSourceDatePart 从 ISO 时间（2023-01-01T00:00:00+09:00）截取 YYYY-MM-DD。
func externalSourceDatePart(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 10 && raw[4] == '-' && raw[7] == '-' {
		return raw[:10]
	}
	return ""
}

// fetchDMMDetail 经年龄门抓取详情页 HTML：GET declared=yes（rurl 填目标详情页），
// 服务端 Set-Cookie 后 302 回目标页，响应体即详情页；最终仍落在 /age_check/
// 说明过门失败，报 upstream_error。
func fetchDMMDetail(ctx context.Context, ref dmmRef) (string, error) {
	if err := waitOutbound(ctx, "dmm"); err != nil {
		return "", err
	}
	pageURL := ref.requestURL()
	gateURL := dmmGateBase + "/age_check/=/declared=yes/?rurl=" + url.QueryEscape(pageURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateURL, nil)
	if err != nil {
		return "", fmt.Errorf("upstream_error")
	}
	req.Header.Set("User-Agent", externalSourceUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := dmmHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upstream_error")
	}
	defer resp.Body.Close()
	if strings.Contains(resp.Request.URL.Path, "/age_check/") {
		return "", fmt.Errorf("upstream_error")
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("not_found")
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("upstream_error")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("upstream_error")
	}
	finalURL := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	meta := FetchMeta{URL: finalURL, FetchedAt: time.Now().UTC(), ContentHash: contentHash(body)}
	recorderFrom(ctx).add(meta, body)
	return string(body), nil
}

// dmmWorkType 把作品形式 / 频道 / ジャンル映射为 definitions 合法 work 类型码。
// 作品形式是商品页对自身内容的直接声明，优先级最高；其后按频道、标签兜底。
func dmmWorkType(channel, form string, genres []string) string {
	// 作品形式优先。
	switch {
	case containsAny(form, "コミック", "漫画", "マンガ"):
		return "comic"
	case containsAny(form, "雑誌", "書籍"):
		return "novel"
	case containsAny(form, "ASMR", "ボイス", "音声"):
		return "audio"
	case containsAny(form, "アニメ", "動画"):
		return "animation"
	case containsAny(form, "音楽", "ミュージック"):
		return "music"
	case containsAny(form, "ゲーム", "ソフト"):
		if channel == "dc/doujin" {
			return "indie_game"
		}
		return "game"
	}
	genreText := strings.Join(genres, " ")
	switch channel {
	case "digital/comic":
		return "comic"
	case "mono/doujin", "digital/book":
		return "novel"
	case "digital/anim", "mono/anime":
		return "animation"
	case "digital/vug2":
		return "film"
	case "mono/game":
		return "game"
	case "dlsoft":
		return "game"
	case "dc/doujin":
		switch {
		case containsAny(genreText, "ASMR", "ボイス", "音声"):
			return "audio"
		case containsAny(genreText, "ゲーム", "ソフト"):
			return "indie_game"
		case containsAny(genreText, "コミック", "マンガ", "漫画"):
			return "comic"
		case containsAny(genreText, "アニメ", "動画"):
			return "animation"
		case containsAny(genreText, "音楽", "ミュージック"):
			return "music"
		default:
			return "personal"
		}
	default:
		return "personal"
	}
}

// dmmAdultChannels 是明确的成人向商品频道，用于补 R-18 标签。
var dmmAdultChannels = map[string]bool{
	"dc/doujin": true, "mono/doujin": true, "dlsoft": true,
	"digital/anim": true, "digital/vug2": true,
	"mono/anime": true, "mono/game": true,
}

// dmmMediumFormat 按频道给载体 format；实体游戏无匹配词表时留空。
func dmmMediumFormat(channel string) string {
	switch channel {
	case "mono/doujin":
		return "paper"
	case "mono/anime":
		return "dvd"
	case "mono/game":
		return ""
	default:
		return "digital"
	}
}

// dmmArtistRelation 按频道决定品牌方的实体类型、职位原文与关系码。
func dmmArtistRelation(channel string) (entityType, role, relationType string) {
	switch channel {
	case "dc/doujin", "mono/doujin":
		return "organization", "サークル", "created_by"
	case "dlsoft", "mono/game":
		return "organization", "メーカー", "developed_by"
	case "digital/anim", "mono/anime", "digital/vug2":
		return "organization", "メーカー", "created_by"
	case "digital/comic", "digital/book":
		return "organization", "出版社", "created_by"
	default:
		return "organization", "発売元", "credit_for"
	}
}

// dmmArtistPreviews 把品牌方 / 作者转为预览关联。
func dmmArtistPreviews(ref dmmRef, ld dmmJSONLD) []ExternalSourceArtistPreview {
	out := []ExternalSourceArtistPreview{}
	entityType, role, relation := dmmArtistRelation(ref.Channel)
	seen := map[string]bool{}
	add := func(name, et, rl, relType string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name+"|"+relType] {
			return
		}
		seen[name+"|"+relType] = true
		out = append(out, ExternalSourceArtistPreview{
			Name:         name,
			OriginalName: name,
			Role:         rl,
			EntityType:   et,
			Language:     "ja",
			RelationType: relType,
		})
	}
	add(ld.Brand.Name, entityType, role, relation)
	add(ld.Author.Name, "person", "著者", "credit_for")
	return out
}

// previewDMM 产出 DMM 商品的完整预览。
func previewDMM(ctx context.Context, ref dmmRef) (ExternalSourcePreviewResponse, error) {
	page, err := fetchDMMDetail(ctx, ref)
	if err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	ld := parseDMMPage(page)
	name := strings.TrimSpace(ld.Name)
	if name == "" {
		return ExternalSourcePreviewResponse{}, fmt.Errorf("upstream_error")
	}
	date := externalSourceDatePart(ld.DatePublished)
	summary := strings.TrimSpace(html.UnescapeString(ld.Description))
	cover := dmmFirstImage(ld.Image)
	tags := []string{}
	for _, g := range ld.Genre {
		if g = strings.TrimSpace(g); g != "" {
			tags = append(tags, g)
		}
	}
	if dmmAdultChannels[ref.Channel] {
		tags = append(tags, "R-18")
	}
	workType := dmmWorkType(ref.Channel, ld.WorkForm, tags)
	translations := []ExternalSourceTranslationItem{
		{Locale: "ja-JP", Title: name, Summary: summary},
	}
	externalURL := ref.canonicalURL()
	channelKind := "digital"
	mediumFormat := dmmMediumFormat(ref.Channel)
	if strings.HasPrefix(ref.Channel, "mono/") {
		channelKind = "physical"
	}
	mediumName := "ダウンロード"
	if channelKind == "physical" {
		mediumName = "パッケージ"
	}
	return ExternalSourcePreviewResponse{
		Source:      "dmm",
		EntityType:  "work",
		ExternalID:  ref.CID,
		ExternalURL: externalURL,
		MediaType:   workType,
		Work: &ExternalSourceWorkPreview{
			Title:            name,
			OriginalTitle:    name,
			ReleaseDate:      date,
			Language:         "ja",
			OriginalLanguage: "ja",
			Summary:          summary,
			CoverImageURL:    cover,
			Tags:             tags,
			Translations:     translations,
			CatalogMetadata: map[string]any{
				"work_type":   workType,
				"dmm_channel": ref.Channel,
			},
		},
		Tags:    tags,
		Artists: dmmArtistPreviews(ref, ld),
		Release: &ExternalSourceReleasePreview{
			EditionDate:         date,
			DistributionChannel: channelKind,
		},
		Mediums: []ExternalSourceMediumPreview{{
			Position: 1,
			Name:     mediumName,
			Format:   mediumFormat,
			Role:     "primary",
		}},
	}, nil
}
