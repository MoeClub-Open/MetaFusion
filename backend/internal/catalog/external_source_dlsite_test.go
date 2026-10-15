package catalog

import (
	"fmt"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// parseDLsiteRef 各形态：裸商品号、各站点 URL、错误输入。
func TestParseDLsiteRef(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		site    string
		pid     string
		wantErr string
	}{
		{"裸 RJ 商品号", "rj01234567", "maniax", "RJ01234567", ""},
		{"裸 RE 商品号", "RE00123456", "maniax", "RE00123456", ""},
		{"裸 BJ 商品号归 books", "BJ00012345", "books", "BJ00012345", ""},
		{"裸 VJ 商品号归 books", "VJ00012345", "books", "VJ00012345", ""},
		{"maniax URL", "https://www.dlsite.com/maniax/work/=/product_id/RJ01234567.html", "maniax", "RJ01234567", ""},
		{"home URL", "https://www.dlsite.com/home/work/=/product_id/RJ00012345.html", "home", "RJ00012345", ""},
		{"books URL", "https://www.dlsite.com/books/work/=/product_id/BJ00012345.html", "books", "BJ00012345", ""},
		{"英文站 URL", "https://eng.dlsite.com/work/=/product_id/RJ00012345.html", "eng", "RJ00012345", ""},
		{"无协议 URL", "www.dlsite.com/maniax/work/=/product_id/RJ01234567.html", "maniax", "RJ01234567", ""},
		{"仿冒域名", "https://www.notdlsite.com/work/RJ00012345", "", "", "not_supported"},
		{"URL 无商品号", "https://www.dlsite.com/maniax/work/=/product_id/", "", "", "invalid_payload"},
		{"空输入", "  ", "", "", "invalid_payload"},
		{"商品号位数不足", "RJ1234", "", "", "invalid_payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := parseDLsiteRef(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ref.Site != tc.site || ref.ProductID != tc.pid {
				t.Fatalf("ref=%+v, want site=%s pid=%s", ref, tc.site, tc.pid)
			}
		})
	}
}

// dlsiteMediaType 映射到 definitions 合法类型码（含新增 audio/comic）。
func TestDLsiteMediaType(t *testing.T) {
	cases := map[string]string{
		"SOU": "audio",
		"SVC": "audio",
		"MNG": "comic",
		"MRE": "comic",
		"MUS": "music",
		"SMT": "music",
		"MOV": "animation",
		"ICG": "photobook",
		"DNV": "visual_novel",
		"RPG": "game",
		"ADV": "game",
		"SLN": "game",
		"ACN": "game",
		"":    "personal",
		"XXX": "personal",
	}
	for typeID, want := range cases {
		if got := dlsiteMediaType(typeID); got != want {
			t.Errorf("type=%s got=%s want=%s", typeID, got, want)
		}
	}
}

// dlsiteHTMLFixture 是服务端渲染的正常商品页。注意开头放了一个首行全 th、
// 数据行全 td 的下载统计表（work_dl_table）：分层解析必须跳过它、不能让它
// 吞掉后面 work_maker 的社团行（这是此前单一大正则的真实错位场景）。
const dlsiteHTMLFixture = `<!doctype html><html><head>
<meta itemprop="image" content="//img.dlsite.jp/modpub/images2/work/doujin/RJ0130000/RJ01234567_img_main.jpg">
<meta itemprop="alternateName" content="tesutoonseisakuhin">
</head><body>
<h1 itemprop="name" id="work_name">テスト音声作品</h1>
<div hidden class="ga4_event_item_RJ01234567" data-product_id="RJ01234567" data-work_name="テスト音声作品" data-maker_id="RG12345" data-work_type="SOU"></div>
<table class="work_dl_table"><tbody>
<tr><th>言語</th><th>DL数</th></tr>
<tr><td>日本語</td><td>100</td></tr>
</tbody></table>
<div class="work_outline">
<table id="work_maker"><tr><th>サークル名</th><td><span class="maker_name"><a href="https://www.dlsite.com/maniax/circle/profile/=/maker_id/RG12345.html">テストサークル</a></span></td></tr></table>
<table id="work_outline_table">
<tr><th>販売日</th><td><a href="x">2023年05月01日 0時</a></td></tr>
<tr><th>年齢指定</th><td><a href="x">R18</a></td></tr>
<tr><th>ジャンル</th><td><a href="x">ASMR</a><a href="x">耳かき</a></td></tr>
<tr><th>作者</th><td><a href="x">如月十二</a></td></tr>
<tr><th>シナリオ</th><td><a href="x">如月十二</a></td></tr>
<tr><th>イラスト</th><td><a href="x">如月十二</a></td></tr>
<tr><th>音楽</th><td><a href="x">如月十二</a></td></tr>
</table></div>
<div itemprop="description" class="work_parts_container"><div class="work_parts_area">◆作品紹介<br>紹介テキスト1行目<br>2行目</div></div>
</body></html>`

// previewDLsite 端到端解析商品页 HTML。
func TestPreviewDLsite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/work/=/product_id/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(dlsiteHTMLFixture))
	}))
	defer srv.Close()
	old := dlsiteWWWBase
	dlsiteWWWBase = srv.URL
	defer func() { dlsiteWWWBase = old }()

	res, err := previewDLsite(context.Background(), dlsiteRef{Site: "maniax", ProductID: "RJ01234567"})
	if err != nil {
		t.Fatalf("preview err: %v", err)
	}
	if res.Source != "dlsite" || res.ExternalID != "RJ01234567" {
		t.Fatalf("source/id wrong: %+v", res)
	}
	if res.MediaType != "audio" {
		t.Fatalf("mediaType=%s, want audio", res.MediaType)
	}
	if res.Work.Title != "テスト音声作品" || res.Work.OriginalTitle != "テスト音声作品" {
		t.Errorf("title wrong: %+v", res.Work)
	}
	if res.Work.ReleaseDate != "2023-05-01" {
		t.Errorf("date=%s", res.Work.ReleaseDate)
	}
	if res.Work.Language != "ja" || res.Work.OriginalLanguage != "ja" {
		t.Errorf("language=%s/%s", res.Work.Language, res.Work.OriginalLanguage)
	}
	if !strings.HasPrefix(res.Work.CoverImageURL, "https://img.dlsite.jp/") {
		t.Errorf("cover=%s", res.Work.CoverImageURL)
	}
	if !contains(res.Work.Tags, "ASMR") || !contains(res.Work.Tags, "耳かき") || !contains(res.Work.Tags, "R-18") {
		t.Errorf("tags=%v", res.Work.Tags)
	}
	if !strings.Contains(res.Work.Summary, "紹介テキスト1行目") {
		t.Errorf("summary=%q", res.Work.Summary)
	}
	// 罗马音别名。
	if len(res.Work.Aliases) != 1 || res.Work.Aliases[0] != "tesutoonseisakuhin" {
		t.Errorf("aliases=%v", res.Work.Aliases)
	}
	// 社团 1 条 created_by + 个人 4 条分职关系。
	if len(res.Artists) != 5 {
		t.Fatalf("artists=%+v", res.Artists)
	}
	rels := map[string]ExternalSourceArtistPreview{}
	for _, a := range res.Artists {
		rels[a.Name+"|"+a.RelationType] = a
	}
	circle, ok := rels["テストサークル|created_by"]
	if !ok {
		t.Fatalf("missing circle created_by")
	}
	// 社团带 RG 外部身份（GA4 data-maker_id 与表格 href 一致）。
	if fmt.Sprint(previewLinkID(circle.ExternalLinks, "dlsite_maker")) != "RG12345" || fmt.Sprint(previewLinkID(circle.ExternalLinks, "metafusion_import")) != "dlsite:circle:RG12345" {
		t.Errorf("circle external=%v", circle.ExternalLinks)
	}
	for _, key := range []string{
		"如月十二|credit_for",
		"如月十二|written_by", "如月十二|illustrated_by", "如月十二|composed_by",
	} {
		if _, ok := rels[key]; !ok {
			t.Errorf("missing artist relation: %s", key)
		}
	}
	if len(res.Mediums) != 1 || res.Mediums[0].Format != "digital" {
		t.Fatalf("mediums=%+v", res.Mediums)
	}
	if res.Release == nil || res.Release.DistributionChannel != "digital" {
		t.Fatalf("release=%+v", res.Release)
	}
}

// dlsiteRestrictedFixture 模拟地区受限页面：无信息表、无简介容器，只有顶部
// Vue template、微数据 meta 与底部 contents JSON，验证兜底提取。
const dlsiteRestrictedFixture = `<!doctype html><html><head>
<meta itemprop="image" content="//img.dlsite.jp/modpub/images2/work/doujin/RJ0130000/RJ01234567_img_main.jpg">
</head><body>
<template data-vue-component="dlchannel-topic" data-product-id="RJ01234567" data-product-name="テスト作品" data-maker-name="がら堂"></template>
<div itemscope itemtype="Product">
  <meta itemprop="name" content="テスト作品">
  <meta itemprop="description" content="簡易あらすじテキスト">
  <div itemprop="brand"><meta itemprop="name" content="がら堂"></div>
</div>
<script>
var contents = {
  "impression": [],
  "detail": [{
    "id": "RJ01234567", "name": "テスト作品", "category": "girls",
    "brand": "RG56527", "regist_date": "2022/01/15", "work_type": "MNG",
    "image_main": "//img.dlsite.jp/modpub/images2/work/doujin/RJ0130000/RJ01234567_img_main.jpg",
    "series_id": "", "series_name": ""
  }],
  "time": 0.00145
};
</script>
</body></html>`

// 地区受限页面：类型 / 日期 / 站点 / 社团 / 简介全部走兜底。
func TestPreviewDLsiteRestricted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(dlsiteRestrictedFixture))
	}))
	defer srv.Close()
	old := dlsiteWWWBase
	dlsiteWWWBase = srv.URL
	defer func() { dlsiteWWWBase = old }()

	// 默认按 maniax 抓，解析后应被 contents 的 category 校正为 girls。
	res, err := previewDLsite(context.Background(), dlsiteRef{Site: "maniax", ProductID: "RJ01234567"})
	if err != nil {
		t.Fatalf("preview err: %v", err)
	}
	if res.MediaType != "comic" {
		t.Errorf("mediaType=%s, want comic", res.MediaType)
	}
	if res.Work.ReleaseDate != "2022-01-15" {
		t.Errorf("date=%s", res.Work.ReleaseDate)
	}
	if res.Work.CatalogMetadata.(map[string]any)["dlsite_site"] != "girls" {
		t.Errorf("site not corrected to girls: %+v", res.Work.CatalogMetadata)
	}
	if len(res.Artists) != 1 {
		t.Fatalf("artists=%+v", res.Artists)
	}
	circle := res.Artists[0]
	if circle.Name != "がら堂" {
		t.Errorf("maker=%s, want がら堂", circle.Name)
	}
	if fmt.Sprint(previewLinkID(circle.ExternalLinks, "dlsite_maker")) != "RG56527" {
		t.Errorf("maker external=%v", circle.ExternalLinks)
	}
	if !strings.Contains(res.Work.Summary, "簡易あらすじテキスト") {
		t.Errorf("summary=%q", res.Work.Summary)
	}
}

// 商品页 404 → not_found。
func TestPreviewDLsiteErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	old := dlsiteWWWBase
	dlsiteWWWBase = srv.URL
	defer func() { dlsiteWWWBase = old }()
	if _, err := previewDLsite(context.Background(), dlsiteRef{Site: "maniax", ProductID: "RJ00099999"}); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("err=%v, want not_found", err)
	}
}
