package catalog

// DLsite（www.dlsite.com）导入适配器。
//
// 商品页是服务端渲染 HTML，解析按"先结构、后兜底"组织：
//   - 名称：h1#work_name / GA4 块 data-work_name / 底部 contents JSON；
//   - 类型码：GA4 块 data-work_type（maniax：SOU=音声、MNG=漫画、MUS=音乐、
//     RPG/ADV/SLN…=游戏、ICG=CG、MOV=动画），contents JSON 兜底；
//   - 社团 / 发售日 / 年龄指定 / 标签 / 分职署名：作品信息表（table#work_maker、
//     table#work_outline），用 table→tr→th/td 分层提取，避免跨表错位；
//   - 简介：itemprop="description" 的 work_parts_container；
//   - 封面：itemprop="image"。
//
// 地区受限页面（お住いの国・地域からは購入できません）不渲染信息表与简介容器，
// 但底部仍输出 `var contents = {...}`（含 work_type / regist_date / brand /
// category），顶部 Vue template 与微数据 meta 仍带社团名与简介，据此兜底而非留空。
// 旧的 /api/=/product/view 端点已 404，统一解析商品页 HTML；抓不到（404 /
// 非 200 / 解析不出名称）一律返回 not_found / upstream_error，不伪造字段。
//
// 落库走通用 Import 流程：社团经 staff_associations 建 organization 并挂 created_by，
// 分职个人挂 illustrated_by / written_by / composed_by 等；发行链为一个 digital 载体。
// 图片只做远端 URL 引用，不抓取、不转存。

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// dlsiteWWWBase 允许单测替换为 httptest 服务；生产指向 DLsite 官网。
var dlsiteWWWBase = "https://www.dlsite.com"

// dlsiteRef 是解析后的 DLsite 商品坐标。
type dlsiteRef struct {
	Site      string // maniax / home / girls / books / eng / ja-jp ...
	ProductID string // RJ / RE / BJ / VJ + 数字，统一大写
}

// dlsiteProductIDPattern 收敛 DLsite 商品号：两位字母前缀 + 5~8 位数字
// （历史商品号较短，新式为 8 位）。
var dlsiteProductIDPattern = regexp.MustCompile(`^(RJ|RE|BJ|VJ)[0-9]{5,8}$`)

// dlsiteLooseProductIDPattern 只判定"看起来像商品号"，用于把畸形号收敛为格式错误。
var dlsiteLooseProductIDPattern = regexp.MustCompile(`^[A-Za-z]{2}[0-9]+$`)

// dlsiteSiteSegments 是路径中合法的站点段白名单；"work" 等功能段不得被当成站点。
var dlsiteSiteSegments = map[string]bool{
	"maniax": true, "home": true, "girls": true, "pro": true, "books": true,
	"eng": true, "ja-jp": true, "ko-kr": true, "zh-cn": true,
}

// dlsiteDefaultSite 按商品号前缀给裸 ID 选择站点：书籍系归 books，其余归 maniax。
func dlsiteDefaultSite(productID string) string {
	switch productID[:2] {
	case "BJ", "VJ":
		return "books"
	default:
		return "maniax"
	}
}

// parseDLsiteRef 解析商品号或 DLsite URL。
func parseDLsiteRef(raw string) (dlsiteRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return dlsiteRef{}, fmt.Errorf("invalid_payload")
	}
	upper := strings.ToUpper(s)
	if dlsiteProductIDPattern.MatchString(upper) {
		return dlsiteRef{Site: dlsiteDefaultSite(upper), ProductID: upper}, nil
	}
	// 形如商品号（两位字母+数字）但位数/前缀不合法：属格式错误而非来源不支持。
	if dlsiteLooseProductIDPattern.MatchString(s) {
		return dlsiteRef{}, fmt.Errorf("invalid_payload")
	}
	normalized := s
	if !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" {
		return dlsiteRef{}, fmt.Errorf("invalid_payload")
	}
	host := strings.ToLower(u.Host)
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // 去掉端口
	}
	// 后缀匹配，避免把 notdlsite.com 这类仿冒域名当成合法来源。
	if host != "dlsite.com" && !strings.HasSuffix(host, ".dlsite.com") {
		return dlsiteRef{}, fmt.Errorf("not_supported")
	}
	subLabel := strings.Split(host, ".")[0]
	site := ""
	pid := ""
	for _, seg := range strings.Split(u.Path, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || strings.ToLower(seg) == "product_id" {
			continue
		}
		clean := strings.TrimSuffix(seg, ".html")
		clean = strings.TrimSuffix(clean, ".htm")
		if candidate := strings.ToUpper(clean); dlsiteProductIDPattern.MatchString(candidate) {
			pid = candidate
			continue
		}
		lower := strings.ToLower(clean)
		if site == "" && dlsiteSiteSegments[lower] {
			site = lower
		}
	}
	if pid == "" {
		return dlsiteRef{}, fmt.Errorf("invalid_payload")
	}
	if site == "" {
		switch subLabel {
		case "eng", "ja-jp", "ko-kr", "zh-cn":
			site = subLabel
		default:
			site = dlsiteDefaultSite(pid)
		}
	}
	return dlsiteRef{Site: site, ProductID: pid}, nil
}

// pageURL 返回抓取用商品页地址（base 可被测试替换）。
func (r dlsiteRef) pageURL() string {
	return fmt.Sprintf("%s/%s/work/=/product_id/%s.html", dlsiteWWWBase, r.Site, r.ProductID)
}

// canonicalURL 返回对外引用的官方规范地址。
func (r dlsiteRef) canonicalURL() string {
	return fmt.Sprintf("https://www.dlsite.com/%s/work/=/product_id/%s.html", r.Site, r.ProductID)
}

// ---- 商品页 HTML 解析 ----

var (
	dlsiteTagPattern = regexp.MustCompile(`<[^>]+>`)
	dlsiteScriptRe   = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	dlsiteStyleRe    = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	dlsiteBrRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	dlsiteBlockEndRe = regexp.MustCompile(`(?is)</(p|div|h[1-6]|li|tr)>`)
	dlsiteDivOpenRe  = regexp.MustCompile(`(?is)<div\b[^>]*>`)
	dlsiteDivCloseRe = regexp.MustCompile(`(?i)</div>`)
	dlsiteLinkRe     = regexp.MustCompile(`(?is)<a\b[^>]*>(.*?)</a>`)

	// 分层表格：先切 table，再在每个 table 内切 tr，行内分别取 th / td。
	// 不使用跨标签的单一行正则：表头行（如下载统计表首行全是 th、无 td）会让
	// 跨表非贪婪匹配吞掉后面 work_maker 的社团行。
	dlsiteTableRe = regexp.MustCompile(`(?is)<table\b[^>]*>(.*?)</table>`)
	dlsiteTrRe    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	dlsiteThRe    = regexp.MustCompile(`(?is)<th\b[^>]*>(.*?)</th>`)
	dlsiteTdRe    = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td>`)

	dlsiteJPDateRe = regexp.MustCompile(`([0-9]{4})年([0-9]{1,2})月([0-9]{1,2})日`)

	dlsiteGA4BlockRe  = regexp.MustCompile(`(?is)<div hidden class="ga4_event_item_[A-Z0-9]+"([^>]+)>`)
	dlsiteH1Re        = regexp.MustCompile(`(?is)<h1[^>]+id="work_name"[^>]*>(.*?)</h1>`)
	dlsiteAltNameRe   = regexp.MustCompile(`(?is)<meta\b[^>]*itemprop="alternateName"[^>]*content="([^"]+)"`)
	dlsiteMetaImageRe = regexp.MustCompile(`(?is)<meta\b[^>]*itemprop="image"[^>]*content="([^"]+)"`)
	dlsiteIntroOpenRe = regexp.MustCompile(`(?is)<div\b[^>]*itemprop="description"[^>]*class="work_parts_container"[^>]*>`)

	// 地区受限兜底：底部 contents JSON、顶部 Vue template、微数据简介 meta。
	dlsiteContentsRe  = regexp.MustCompile(`(?is)var\s+contents\s*=\s*(\{.*?\})\s*;\s*</script>`)
	dlsiteVueTopicRe  = regexp.MustCompile(`(?is)<template\b[^>]*data-vue-component="dlchannel-topic"([^>]*)>`)
	dlsiteMetaDescRe  = regexp.MustCompile(`(?is)<meta\b[^>]*itemprop="description"[^>]*content="([^"]+)"`)
	dlsiteMakerIDHref = regexp.MustCompile(`maker_id/(RG[0-9]+)`)
)

// dlsiteParsed 是商品页解析结果。
type dlsiteParsed struct {
	Name       string
	Romaji     string // alternateName，实为假名罗马音，落别名
	TypeID     string // GA4 / contents 的 work_type
	Maker      string
	MakerID    string // RG id
	Category   string // 来源声明站点类别（maniax/girls/home/books…）
	Age        string // R-18 / R-15 / 空
	Genres     []string
	Intro      string
	Cover      string
	Date       string
	SeriesName string
	Credits    map[string][]string // 作者 / シナリオ / イラスト / 音楽
}

// dlsiteContentsData 是页面底部 `var contents` 的最小结构。
type dlsiteContentsData struct {
	Detail []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Category   string `json:"category"`
		Brand      string `json:"brand"` // RG id
		RegistDate string `json:"regist_date"`
		WorkType   string `json:"work_type"`
		ImageMain  string `json:"image_main"`
		SeriesID   string `json:"series_id"`
		SeriesName string `json:"series_name"`
	} `json:"detail"`
}

// dlsiteAttr 从标签属性串中取指定属性值。
func dlsiteAttr(attrs, key string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `=["']([^"']*)["']`)
	if m := re.FindStringSubmatch(attrs); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// dlsiteTextClean 剥离标签、反转义实体并压缩空白。
func dlsiteTextClean(s string) string {
	s = dlsiteTagPattern.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// dlsiteAbsoluteURL 把协议相对地址（//img.dlsite.jp/...）补成 https。
func dlsiteAbsoluteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

// dlsiteUnique 去重并保持顺序。
func dlsiteUnique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// dlsiteCellNames 取表格单元格中的链接文本；无链接时取整块文本。
func dlsiteCellNames(td string) []string {
	names := []string{}
	for _, m := range dlsiteLinkRe.FindAllStringSubmatch(td, -1) {
		if n := dlsiteTextClean(m[1]); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		if n := dlsiteTextClean(td); n != "" {
			names = []string{n}
		}
	}
	return dlsiteUnique(names)
}

// dlsiteMakerIDFromCell 从社团单元格链接里提取 RG id。
func dlsiteMakerIDFromCell(td string) string {
	if m := dlsiteMakerIDHref.FindStringSubmatch(td); m != nil {
		return m[1]
	}
	return ""
}

// dlsiteBalancedDiv 返回从 start 处开标签到其匹配闭标签的完整块
// （正则不支持嵌套计数，用深度遍历处理简介容器内的嵌套 div）。
func dlsiteBalancedDiv(s string, start int) string {
	depth := 0
	pos := start
	for pos < len(s) {
		op := dlsiteDivOpenRe.FindStringIndex(s[pos:])
		cl := dlsiteDivCloseRe.FindStringIndex(s[pos:])
		if cl == nil {
			return s[start:]
		}
		if op != nil && op[0] < cl[0] {
			depth++
			pos += op[1]
		} else {
			depth--
			pos += cl[1]
			if depth == 0 {
				return s[start:pos]
			}
		}
	}
	return s[start:]
}

// dlsiteIntroText 把简介容器 HTML 转为纯文本（保留换行）。
func dlsiteIntroText(block string) string {
	s := dlsiteScriptRe.ReplaceAllString(block, "")
	s = dlsiteStyleRe.ReplaceAllString(s, "")
	s = dlsiteBrRe.ReplaceAllString(s, "\n")
	s = dlsiteBlockEndRe.ReplaceAllString(s, "\n")
	s = dlsiteTagPattern.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := []string{}
	for _, line := range strings.Split(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// dlsiteSlashDate 把 contents 的 regist_date（2022/01/15）归一为 YYYY-MM-DD。
func dlsiteSlashDate(raw string) string {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	if len(parts) != 3 || len(parts[0]) != 4 {
		return ""
	}
	y, m, d := parts[0], parts[1], parts[2]
	if len(m) == 1 {
		m = "0" + m
	}
	if len(d) == 1 {
		d = "0" + d
	}
	return y + "-" + m + "-" + d
}

// parseDLsitePage 解析商品页 HTML。
func parseDLsitePage(htmlText string) *dlsiteParsed {
	out := &dlsiteParsed{Credits: map[string][]string{}}

	// 1. GA4 隐藏数据块：商品坐标与站内类型码（社团属性是 data-maker_id，非名称）。
	if m := dlsiteGA4BlockRe.FindStringSubmatch(htmlText); m != nil {
		attrs := m[1]
		out.TypeID = strings.ToUpper(strings.TrimSpace(dlsiteAttr(attrs, "data-work_type")))
		out.Name = dlsiteAttr(attrs, "data-work_name")
		out.MakerID = dlsiteAttr(attrs, "data-maker_id")
	}
	// 2. h1#work_name 兜底名称。
	if out.Name == "" {
		if m := dlsiteH1Re.FindStringSubmatch(htmlText); m != nil {
			out.Name = dlsiteTextClean(m[1])
		}
	}
	// 3. 罗马音别名。
	if m := dlsiteAltNameRe.FindStringSubmatch(htmlText); m != nil {
		out.Romaji = strings.TrimSpace(m[1])
	}
	// 4. 封面。
	if m := dlsiteMetaImageRe.FindStringSubmatch(htmlText); m != nil {
		out.Cover = dlsiteAbsoluteURL(m[1])
	}

	// 5. 信息表：table → tr → th/td 分层提取；行内不同时含 th 与 td（如下载
	// 统计表的全 th 表头、全 td 数据行）一律跳过，不会跨表错位。
	for _, tm := range dlsiteTableRe.FindAllStringSubmatch(htmlText, -1) {
		for _, rm := range dlsiteTrRe.FindAllStringSubmatch(tm[1], -1) {
			row := rm[1]
			thm := dlsiteThRe.FindStringSubmatch(row)
			tdm := dlsiteTdRe.FindStringSubmatch(row)
			if thm == nil || tdm == nil {
				continue
			}
			key := dlsiteTextClean(thm[1])
			td := tdm[1]
			switch key {
			case "サークル名":
				if out.Maker == "" {
					if names := dlsiteCellNames(td); len(names) > 0 {
						out.Maker = names[0]
					}
				}
				if out.MakerID == "" {
					out.MakerID = dlsiteMakerIDFromCell(td)
				}
			case "販売日", "発売日":
				if out.Date == "" {
					if dm := dlsiteJPDateRe.FindStringSubmatch(td); dm != nil {
						month, day := dm[2], dm[3]
						if len(month) == 1 {
							month = "0" + month
						}
						if len(day) == 1 {
							day = "0" + day
						}
						out.Date = dm[1] + "-" + month + "-" + day
					}
				}
			case "年齢指定":
				if out.Age == "" {
					age := dlsiteTextClean(td)
					switch {
					case strings.Contains(age, "R18"), strings.Contains(age, "18禁"):
						out.Age = "R-18"
					case strings.Contains(age, "R15"):
						out.Age = "R-15"
					}
				}
			case "ジャンル":
				if len(out.Genres) == 0 {
					out.Genres = dlsiteCellNames(td)
				}
			case "作者":
				out.Credits["作者"] = dlsiteCellNames(td)
			case "シナリオ":
				out.Credits["シナリオ"] = dlsiteCellNames(td)
			case "イラスト":
				out.Credits["イラスト"] = dlsiteCellNames(td)
			case "音楽":
				out.Credits["音楽"] = dlsiteCellNames(td)
			}
		}
	}

	// 6. 简介容器（嵌套 div，按深度截取）。
	if m := dlsiteIntroOpenRe.FindStringIndex(htmlText); m != nil {
		out.Intro = dlsiteIntroText(dlsiteBalancedDiv(htmlText, m[0]))
	}

	// 7. 底部 contents JSON：地区受限页面仍存在，补类型 / 日期 / 站点 / 社团 id / 封面。
	if m := dlsiteContentsRe.FindStringSubmatch(htmlText); m != nil {
		var contents dlsiteContentsData
		if json.Unmarshal([]byte(m[1]), &contents) == nil && len(contents.Detail) > 0 {
			d := contents.Detail[0]
			if out.TypeID == "" {
				out.TypeID = strings.ToUpper(strings.TrimSpace(d.WorkType))
			}
			if out.Date == "" {
				out.Date = dlsiteSlashDate(d.RegistDate)
			}
			if out.Category == "" {
				out.Category = strings.TrimSpace(d.Category)
			}
			if out.MakerID == "" {
				out.MakerID = strings.TrimSpace(d.Brand)
			}
			if out.Cover == "" {
				out.Cover = dlsiteAbsoluteURL(d.ImageMain)
			}
			if out.SeriesName == "" {
				out.SeriesName = strings.TrimSpace(d.SeriesName)
			}
			if out.Name == "" {
				out.Name = d.Name
			}
		}
	}

	// 8. 社团名兜底：顶部 Vue template 的 data-maker-name（受限页面）。
	if out.Maker == "" {
		if m := dlsiteVueTopicRe.FindStringSubmatch(htmlText); m != nil {
			out.Maker = dlsiteAttr(m[1], "data-maker-name")
		}
	}
	// 9. 简介兜底：微数据 meta itemprop=description（受限页面）。
	if out.Intro == "" {
		if m := dlsiteMetaDescRe.FindStringSubmatch(htmlText); m != nil {
			out.Intro = dlsiteTextClean(m[1])
		}
	}
	return out
}

// dlsiteGameWorkTypes 是 DLsite 游戏类 work_type 码。
var dlsiteGameWorkTypes = map[string]bool{
	"GAM": true, "ACN": true, "ADV": true, "RPG": true, "STG": true,
	"TBL": true, "TYP": true, "QIZ": true, "SLN": true, "DNV": true, "DNT": true, "ETC": true,
}

// dlsiteMediaType 把 DLsite work_type 码映射为预览标记，不落实体业务类型。
// 已按真实码校准（maniax）：SOU=音声→audio、MNG=漫画→comic、MUS=音乐、
// DNV=视觉小说、其余游戏码→game、ICG=CG 集→photobook、MOV=动画。
// 无法判定时落到 personal（通用预览标记，不虚构实体属性）。
func dlsiteMediaType(typeID string) string {
	switch strings.ToUpper(strings.TrimSpace(typeID)) {
	case "DNV", "DNT":
		return "visual_novel"
	case "GAM", "ACN", "ADV", "RPG", "STG", "TBL", "TYP", "QIZ", "SLN", "ETC":
		return "game"
	case "SOU", "SVC":
		return "audio"
	case "MNG", "MRE":
		return "comic"
	case "NRE":
		return "novel"
	case "ICG":
		return "photobook"
	case "MUS", "SMT":
		return "music"
	case "MOV":
		return "animation"
	}
	return "personal"
}

// fetchDLsitePage 抓取商品页 HTML。
func fetchDLsitePage(ctx context.Context, pageURL string) ([]byte, error) {
	if err := waitOutbound(ctx, "dlsite"); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("upstream_error")
	}
	req.Header.Set("User-Agent", externalSourceUserAgent)
	req.Header.Set("Accept-Language", "ja,en-US;q=0.8")
	resp, err := externalSourceHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream_error")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("not_found")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("upstream_error")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("upstream_error")
	}
	meta := FetchMeta{URL: pageURL, FetchedAt: time.Now().UTC(), ContentHash: contentHash(body)}
	recorderFrom(ctx).add(meta, body)
	return body, nil
}

// dlsiteArtistPreviews 把商品上的创作身份转为预览关联。
func dlsiteArtistPreviews(p *dlsiteParsed) []ExternalSourceArtistPreview {
	out := []ExternalSourceArtistPreview{}
	seen := map[string]bool{}
	add := func(names []string, entityType, role, relationType string, external map[string]any) {
		for _, name := range names {
			name = strings.TrimSpace(name)
			key := name + "|" + entityType + "|" + relationType
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ExternalSourceArtistPreview{
				Name:         name,
				OriginalName: name,
				Role:         role,
				EntityType:   entityType,
				Language:     "ja",
				RelationType: relationType,
				ExternalLinks: previewLinks(external),
			})
		}
	}
	// 社团是主要创作/出品方：建 organization、挂 created_by，并带 RG 外部身份。
	if p.Maker != "" {
		external := map[string]any{}
		if p.MakerID != "" {
			external["dlsite_maker"] = p.MakerID
			external["metafusion_import"] = "dlsite:circle:" + p.MakerID
		}
		add([]string{p.Maker}, "organization", "サークル", "created_by", external)
	}
	// 信息表中的分职署名：作者 / 剧本 / 插画 / 音乐，挂精确关系码。
	add(p.Credits["作者"], "person", "著者", "credit_for", nil)
	add(p.Credits["シナリオ"], "person", "シナリオ", "written_by", nil)
	add(p.Credits["イラスト"], "person", "イラスト", "illustrated_by", nil)
	add(p.Credits["音楽"], "person", "音楽", "composed_by", nil)
	return out
}

// previewDLsite 产出 DLsite 商品的完整预览。
func previewDLsite(ctx context.Context, ref dlsiteRef) (ExternalSourcePreviewResponse, error) {
	body, err := fetchDLsitePage(ctx, ref.pageURL())
	if err != nil {
		return ExternalSourcePreviewResponse{}, err
	}
	page := parseDLsitePage(string(body))
	if strings.TrimSpace(page.Name) == "" {
		return ExternalSourcePreviewResponse{}, fmt.Errorf("upstream_error")
	}
	// 来源声明的分类校正站点（如 RJ367004 实属 girls 而非默认 maniax）。
	if page.Category != "" && page.Category != ref.Site {
		ref.Site = page.Category
	}
	workType := dlsiteMediaType(page.TypeID)
	tags := make([]string, 0, len(page.Genres)+1)
	tags = append(tags, page.Genres...)
	if page.Age != "" {
		tags = append(tags, page.Age)
	}
	aliases := []string{}
	if page.Romaji != "" {
		aliases = []string{page.Romaji}
	}
	return ExternalSourcePreviewResponse{
		Source:      "dlsite",
		EntityType:  "work",
		ExternalID:  ref.ProductID,
		ExternalURL: ref.canonicalURL(),
		MediaType:   workType,
		Work: &ExternalSourceWorkPreview{
			Title:            page.Name,
			OriginalTitle:    page.Name,
			Aliases:          aliases,
			ReleaseDate:      page.Date,
			Language:         "ja",
			OriginalLanguage: "ja",
			Summary:          page.Intro,
			CoverImageURL:    page.Cover,
			Tags:             tags,
			CatalogMetadata: map[string]any{
				"work_type":        workType,
				"dlsite_site":      ref.Site,
				"dlsite_work_type": page.TypeID,
				"dlsite_age":       page.Age,
				"series_name":      page.SeriesName,
			},
		},
		Tags:    tags,
		Artists: dlsiteArtistPreviews(page),
		Release: &ExternalSourceReleasePreview{
			EditionDate:         page.Date,
			DistributionChannel: "digital",
		},
		Mediums: []ExternalSourceMediumPreview{{
			Position: 1,
			Name:     "ダウンロード",
			Format:   "digital",
			Role:     "primary",
		}},
	}, nil
}
