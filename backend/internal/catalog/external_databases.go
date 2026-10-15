package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/lib/pq"
	"regexp"
	"strings"
)

// ExternalDatabase 是后台可配的外部来源预设。
// 官方网站、Bushiroad、Bangumi 与 MusicBrainz 等在此同级定义：
// 前端统一渲染为同一组外链，后台可增删改，前端不 hardcode。
type ExternalDatabase struct {
	Code            string            `json:"code"`
	Names           map[string]string `json:"names"`
	Category        string            `json:"category"`
	URLPattern      string            `json:"url_pattern"`
	Icon            string            `json:"icon"`
	IconURL         string            `json:"icon_url"`
	ValidationRegex string            `json:"validation_regex"`
	Description     string            `json:"description"`
	SortOrder       int               `json:"sort_order"`
	IsEnabled       bool              `json:"is_enabled"`
	IsSystem        bool              `json:"is_system"`
}

// externalDatabaseCategories 允许的适用 kind：all 表示所有实体，其余为固定八实体 kind。
var externalDatabaseCategories = map[string]bool{
	"all": true, "agent": true, "collection": true, "work": true, "content_unit": true,
	"expression": true, "release": true, "medium": true, "track": true,
}

// 外链地址由**前端**按 url_pattern 拼（{id} 替换，见 ExternalAuthorityLinks.tsx）：
// 服务端只回 url_pattern 这个字符串。此前这里还有一份 BuildURL（含"pattern 里没有 {id} 就退回
// 字符串拼接"的兜底分支），零生产引用，同一条规则两份实现会漂移，已删（2026-09-19 第二轮审计 #2）。

var externalDatabaseCodePattern = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)

func scanExternalDatabase(row func(...any) error) (ExternalDatabase, error) {
	var e ExternalDatabase
	var names []byte
	err := row(&e.Code, &names, &e.Category, &e.URLPattern,
		&e.Icon, &e.IconURL, &e.ValidationRegex, &e.Description, &e.SortOrder, &e.IsEnabled, &e.IsSystem)
	if err != nil {
		return e, err
	}
	e.Names = map[string]string{}
	if len(names) > 0 {
		_ = json.Unmarshal(names, &e.Names)
	}
	return e, nil
}

func validateExternalDatabase(e ExternalDatabase) error {
	if !externalDatabaseCodePattern.MatchString(e.Code) {
		return fmt.Errorf("invalid_code")
	}
	// 外部库名会显示在实体详情页的外链区，与其它名称同一口径：四语齐备。
	// 原来只要求中文，导致后台能建出只在简中界面正确的条目。
	if err := validateNames(e.Names); err != nil {
		return err
	}
	if !externalDatabaseCategories[e.Category] {
		return fmt.Errorf("invalid_category")
	}
	if strings.TrimSpace(e.URLPattern) == "" {
		return fmt.Errorf("invalid_url_pattern")
	}
	if e.ValidationRegex != "" {
		if _, err := regexp.Compile(e.ValidationRegex); err != nil {
			return fmt.Errorf("invalid_validation_regex")
		}
	}
	return nil
}

// validateExternalLinksAgainstDB 按 external_databases 预设复核实体的 external_links：
// kind 必须已在预设表（含停用项）；external_id 按 validation_regex 收敛（官网类存完整 URL，
// 走 validURL）。metafusion_import 内部 kind 不在预设表，跳过。q 用事务内 queryer。
func validateExternalLinksAgainstDB(ctx context.Context, q queryer, e Entity) error {
	if len(e.ExternalLinks) == 0 {
		return nil
	}
	need := []string{}
	seen := map[string]bool{}
	for _, link := range e.ExternalLinks {
		if legacyImportKeys[link.Kind] || seen[link.Kind] {
			continue
		}
		seen[link.Kind] = true
		need = append(need, link.Kind)
	}
	if len(need) == 0 {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT code,validation_regex,category FROM catalog.external_databases WHERE code = ANY($1)`, pq.Array(need))
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[string]struct {
		regex, category string
	}{}
	for rows.Next() {
		var code, rx, cat string
		if err = rows.Scan(&code, &rx, &cat); err != nil {
			return err
		}
		found[code] = struct {
			regex, category string
		}{rx, cat}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, link := range e.ExternalLinks {
		if legacyImportKeys[link.Kind] {
			continue
		}
		preset, ok := found[link.Kind]
		if !ok {
			return fmt.Errorf("invalid_external_key: %s", link.Kind)
		}
		if preset.category != "all" && preset.category != e.Kind {
			return fmt.Errorf("invalid_external_category: %s", link.Kind)
		}
		if link.Kind == "official_website" || link.Kind == "publisher_website" {
			if !validURL(link.URL) {
				return fmt.Errorf("invalid_external_link")
			}
			continue
		}
		if link.ExternalID != "" && preset.regex != "" {
			rx, rerr := regexp.Compile(preset.regex)
			if rerr != nil || !rx.MatchString(link.ExternalID) {
				return fmt.Errorf("invalid_external_id: %s", link.Kind)
			}
		}
	}
	return nil
}

// ListExternalDatabases 返回外部来源预设（按 sort_order 排序）。
// 公开接口只返回启用项；category 非空时同时返回该分类与 all。
func (s *Store) ListExternalDatabases(ctx context.Context, category string, enabledOnly bool) ([]ExternalDatabase, error) {
	q := `SELECT code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system FROM catalog.external_databases`
	var args []any
	var conds []string
	if enabledOnly {
		conds = append(conds, `is_enabled`)
	}
	if strings.TrimSpace(category) != "" {
		conds = append(conds, `(category='all' OR category=$1)`)
		args = append(args, strings.TrimSpace(category))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY sort_order,code`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExternalDatabase{}
	for rows.Next() {
		e, err := scanExternalDatabase(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// 没有"按 code 取单条预设"的端点：管理台编辑走的是列表数据（GET /api/admin/external-databases
// 回整行）。此前这里的 GetExternalDatabase 零引用，改 DTO 形状时静态检查也发现不了它脱节，已删
// （2026-09-19 第二轮审计 #3）。

func (s *Store) CreateExternalDatabase(ctx context.Context, in ExternalDatabase) (ExternalDatabase, error) {
	in.Code = strings.TrimSpace(in.Code)
	if in.Category == "" {
		in.Category = "all"
	}
	if in.Icon == "" {
		in.Icon = "Globe"
	}
	if in.Names == nil {
		in.Names = map[string]string{}
	}
	if err := validateExternalDatabase(in); err != nil {
		return ExternalDatabase{}, err
	}
	names, _ := json.Marshal(in.Names)
	var created ExternalDatabase
	err := s.write(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.external_databases WHERE code=$1)`, in.Code).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("constraint_violation")
		}
		row := tx.QueryRowContext(ctx, `INSERT INTO catalog.external_databases(code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,false) RETURNING code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system`,
			in.Code, string(names), in.Category, in.URLPattern, in.Icon, in.IconURL, in.ValidationRegex, in.Description, in.SortOrder)
		e, err := scanExternalDatabase(row.Scan)
		if err != nil {
			return err
		}
		created = e
		return nil
	})
	return created, err
}

// UpdateExternalDatabase 更新预设；系统预设的 code 与 is_system 不可改（靠**下面 UPDATE 的 set 里
// 没有这两列**实现，不是靠判定），停用保留历史展示。
func (s *Store) UpdateExternalDatabase(ctx context.Context, code string, in ExternalDatabase) (ExternalDatabase, error) {
	in.Code = code
	if in.Category == "" {
		in.Category = "all"
	}
	if in.Icon == "" {
		in.Icon = "Globe"
	}
	if in.Names == nil {
		in.Names = map[string]string{}
	}
	if err := validateExternalDatabase(in); err != nil {
		return ExternalDatabase{}, err
	}
	names, _ := json.Marshal(in.Names)
	var updated ExternalDatabase
	// 不再先查一次 is_system：这个值只用于"系统预设不可改"的保护，而保护本身由 UPDATE 的
	// set 清单不含 code/is_system 完成（下面那条语句一次往返即可）。原先那次查询每次 PUT 都多
	// 打一次库、结果还被丢弃，读起来却像这里有显式判定（2026-09-19 第二轮审计 #4）。
	err := s.write(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `UPDATE catalog.external_databases SET names=$2,category=$3,url_pattern=$4,icon=$5,icon_url=$6,validation_regex=$7,description=$8,sort_order=$9,is_enabled=$10 WHERE code=$1 RETURNING code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system`,
			code, string(names), in.Category, in.URLPattern, in.Icon, in.IconURL, in.ValidationRegex, in.Description, in.SortOrder, in.IsEnabled)
		e, err := scanExternalDatabase(row.Scan)
		if err != nil {
			return err
		}
		updated = e
		return nil
	})
	return updated, err
}

// DeleteExternalDatabase 删除预设；系统预设只能停用不能删除，已有数据引用到的也建议停用而非删除。
func (s *Store) DeleteExternalDatabase(ctx context.Context, code string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var isSystem bool
		if err := tx.QueryRowContext(ctx, `SELECT is_system FROM catalog.external_databases WHERE code=$1`, code).Scan(&isSystem); err != nil {
			return err
		}
		if isSystem {
			return fmt.Errorf("system_protected")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM catalog.external_databases WHERE code=$1`, code)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// externalDatabaseSeeds 是系统预设的外部权威库清单。
// 单独成函数而不是写在播种循环里：名称的四语覆盖由 names_coverage_test 直接断言，
// 否则"种子改名"只能靠启动写库这一条路径被发现。
// 官网、Bangumi 单集、Bushiroad 与其它权威库同级存放。
func externalDatabaseSeeds() []ExternalDatabase {
	return []ExternalDatabase{
		{Code: "official_website", Names: map[string]string{"zh-CN": "官方网站", "zh-TW": "官方網站", "ja": "公式サイト", "ja-JP": "公式サイト", "en-US": "Official Website"}, Category: "all", URLPattern: "{id}", Icon: "Globe", Description: "作品 / 创作者 / 发行官方主页（存完整 URL）", SortOrder: 5, IsEnabled: true, IsSystem: true},
		{Code: "publisher_website", Names: map[string]string{"zh-CN": "出版社 / 厂商官网", "zh-TW": "出版社 / 廠商官網", "ja": "出版社・メーカー公式サイト", "ja-JP": "出版社・メーカー公式サイト", "en-US": "Publisher Website"}, Category: "all", URLPattern: "{id}", Icon: "Building2", Description: "出版社 / 社团 / 厂商官方主页（存完整 URL）", SortOrder: 6, IsEnabled: true, IsSystem: true},
		{Code: "wikipedia", Names: map[string]string{"zh-CN": "维基百科", "zh-TW": "維基百科", "ja": "ウィキペディア", "ja-JP": "ウィキペディア", "en-US": "Wikipedia"}, Category: "all", URLPattern: "https://zh.wikipedia.org/wiki/{id}", Icon: "Globe", Description: "全球多语言自由百科全书（填词条标题或完整 URL）", SortOrder: 10, IsEnabled: true, IsSystem: true},
		{Code: "wikidata", Names: map[string]string{"zh-CN": "维基数据", "zh-TW": "維基數據", "ja": "ウィキデータ", "ja-JP": "ウィキデータ", "en-US": "Wikidata"}, Category: "all", URLPattern: "https://www.wikidata.org/wiki/{id}", Icon: "Database", ValidationRegex: `^Q\d+$`, Description: "维基媒体结构化知识图谱实体项 (如 Q11303)", SortOrder: 20, IsEnabled: true, IsSystem: true},
		{Code: "moegirlpedia", Names: map[string]string{"zh-CN": "萌娘百科", "zh-TW": "萌娘百科", "ja": "萌えっ娘百科", "ja-JP": "萌えっ娘百科", "en-US": "Moegirlpedia"}, Category: "all", URLPattern: "{id}", Icon: "BookOpen", ValidationRegex: `^https?://(?:[A-Za-z0-9.-]+\.)?moegirl\.org\.cn/.+$`, Description: "萌娘百科词条（存完整 URL）", SortOrder: 11, IsEnabled: true, IsSystem: true},
		{Code: "fandom", Names: map[string]string{"zh-CN": "Fandom", "zh-TW": "Fandom", "ja": "Fandom", "ja-JP": "Fandom", "en-US": "Fandom"}, Category: "all", URLPattern: "{id}", Icon: "BookOpen", ValidationRegex: `^https?://[A-Za-z0-9.-]+\.fandom\.com/.+$`, Description: "Fandom 维基词条（存含维基子域名的完整 URL）", SortOrder: 12, IsEnabled: true, IsSystem: true},
		{Code: "musicbrainz", Names: map[string]string{"zh-CN": "MusicBrainz", "zh-TW": "MusicBrainz", "ja": "MusicBrainz", "ja-JP": "MusicBrainz", "en-US": "MusicBrainz"}, Category: "all", URLPattern: "https://musicbrainz.org/release-group/{id}", Icon: "Disc3", ValidationRegex: `^[0-9a-fA-F\-]{36}$`, Description: "开放音乐元数据百科全书 (MBID UUID)", SortOrder: 30, IsEnabled: true, IsSystem: true},
		{Code: "musicbrainz_release", Names: map[string]string{"zh-CN": "MusicBrainz 发行", "zh-TW": "MusicBrainz 發行", "ja": "MusicBrainz リリース", "ja-JP": "MusicBrainz リリース", "en-US": "MusicBrainz Release"}, Category: "release", URLPattern: "https://musicbrainz.org/release/{id}", Icon: "Disc3", ValidationRegex: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`, Description: "MusicBrainz 具体发行版本（存 release MBID，与 release-group 区分）", SortOrder: 31, IsEnabled: true, IsSystem: true},
		{Code: "discogs", Names: map[string]string{"zh-CN": "Discogs", "zh-TW": "Discogs", "ja": "Discogs", "ja-JP": "Discogs", "en-US": "Discogs"}, Category: "all", URLPattern: "https://www.discogs.com/master/{id}", Icon: "Disc", ValidationRegex: `^\d+$`, Description: "全球权威黑胶与实体唱片数据库 (Master/Release ID)", SortOrder: 40, IsEnabled: true, IsSystem: true},
		{Code: "discogs_release", Names: map[string]string{"zh-CN": "Discogs 发行版本", "zh-TW": "Discogs 發行版本", "ja": "Discogs リリース", "ja-JP": "Discogs リリース", "en-US": "Discogs Release"}, Category: "release", URLPattern: "https://www.discogs.com/release/{id}", Icon: "Disc", ValidationRegex: `^\d+$`, Description: "Discogs 具体发行版本（存 release 数字 ID，与 master 区分）", SortOrder: 41, IsEnabled: true, IsSystem: true},
		{Code: "vgmdb", Names: map[string]string{"zh-CN": "VGMdb", "zh-TW": "VGMdb", "ja": "VGMdb", "ja-JP": "VGMdb", "en-US": "VGMdb"}, Category: "all", URLPattern: "https://vgmdb.net/album/{id}", Icon: "Music2", ValidationRegex: `^\d+$`, Description: "电子游戏与动漫原声音乐专题数据库", SortOrder: 50, IsEnabled: true, IsSystem: true},
		{Code: "bushiroad_music", Names: map[string]string{"zh-CN": "Bushiroad 官方唱片", "zh-TW": "Bushiroad 官方唱片", "ja": "ブシロード ミュージック", "ja-JP": "ブシロード ミュージック", "en-US": "Bushiroad Music"}, Category: "all", URLPattern: "https://bushiroad-music.com/musics/{id}/", Icon: "Disc", ValidationRegex: `^[a-z0-9_\-]+$`, Description: "Bushiroad 官方唱片发售页 slug", SortOrder: 55, IsEnabled: false, IsSystem: true},
		{Code: "spotify", Names: map[string]string{"zh-CN": "Spotify", "zh-TW": "Spotify", "ja": "Spotify", "ja-JP": "Spotify", "en-US": "Spotify"}, Category: "all", URLPattern: "https://open.spotify.com/album/{id}", Icon: "PlayCircle", ValidationRegex: `^[0-9A-Za-z]{22}$`, Description: "全球流媒体音乐服务平台 (Album / Artist ID)", SortOrder: 60, IsEnabled: true, IsSystem: true},
		{Code: "apple_music", Names: map[string]string{"zh-CN": "Apple Music", "zh-TW": "Apple Music", "ja": "Apple Music", "ja-JP": "Apple Music", "en-US": "Apple Music"}, Category: "all", URLPattern: "https://music.apple.com/album/{id}", Icon: "Apple", ValidationRegex: `^\d+$`, Description: "苹果音乐数字专辑与创作者页面", SortOrder: 70, IsEnabled: true, IsSystem: true},
		{Code: "imdb", Names: map[string]string{"zh-CN": "IMDb 互联网电影资料库", "zh-TW": "IMDb 網際網路電影資料庫", "ja": "IMDb（インターネット・ムービー・データベース）", "ja-JP": "IMDb（インターネット・ムービー・データベース）", "en-US": "IMDb"}, Category: "work", URLPattern: "https://www.imdb.com/title/{id}/", Icon: "Film", ValidationRegex: `^tt\d+$`, Description: "全球权威互联网电影资料库 (如 tt0816692 / nm0000001)", SortOrder: 80, IsEnabled: true, IsSystem: true},
		{Code: "tmdb", Names: map[string]string{"zh-CN": "TMDB 影视数据库", "zh-TW": "TMDB 影視資料庫", "ja": "TMDB（The Movie Database）", "ja-JP": "TMDB（The Movie Database）", "en-US": "The Movie Database"}, Category: "work", URLPattern: "https://www.themoviedb.org/movie/{id}", Icon: "Clapperboard", ValidationRegex: `^\d+$`, Description: "开放社区影视元数据与海报媒体库", SortOrder: 90, IsEnabled: true, IsSystem: true},
		{Code: "tmdb_tv", Names: map[string]string{"zh-CN": "TMDB 电视剧", "zh-TW": "TMDB 電視劇", "ja": "TMDB テレビ", "ja-JP": "TMDB テレビ", "en-US": "TMDB TV"}, Category: "work", URLPattern: "https://www.themoviedb.org/tv/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "TMDB 电视剧与电视节目条目（存电视条目数字 ID）", SortOrder: 91, IsEnabled: true, IsSystem: true},
		{Code: "netflix", Names: map[string]string{"zh-CN": "Netflix", "zh-TW": "Netflix", "ja": "Netflix", "ja-JP": "Netflix", "en-US": "Netflix"}, Category: "work", URLPattern: "https://www.netflix.com/title/{id}", Icon: "Clapperboard", ValidationRegex: `^\d+$`, Description: "Netflix 影视作品页面（存 title 数字 ID）", SortOrder: 95, IsEnabled: true, IsSystem: true},
		{Code: "douban_movie", Names: map[string]string{"zh-CN": "豆瓣电影", "zh-TW": "豆瓣電影", "ja": "豆瓣映画", "ja-JP": "豆瓣映画", "en-US": "Douban Movie"}, Category: "work", URLPattern: "https://movie.douban.com/subject/{id}/", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "中文影视与文化评论社区 (条目 ID)", SortOrder: 100, IsEnabled: true, IsSystem: true},
		{Code: "bangumi", Names: map[string]string{"zh-CN": "Bangumi 番组计划", "zh-TW": "Bangumi 番組計畫", "ja": "Bangumi（番組計画）", "ja-JP": "Bangumi（番組計画）", "en-US": "Bangumi"}, Category: "all", URLPattern: "https://bgm.tv/subject/{id}", Icon: "Tv2", ValidationRegex: `^\d+$`, Description: "中文 ACG 二次元动画/漫画/游戏/音乐条目索引", SortOrder: 110, IsEnabled: true, IsSystem: true},
		{Code: "vndb", Names: map[string]string{"zh-CN": "VNDB 视觉小说数据库", "zh-TW": "VNDB 視覺小說資料庫", "ja": "VNDB（ビジュアルノベル・データベース）", "ja-JP": "VNDB（ビジュアルノベル・データベース）", "en-US": "VNDB"}, Category: "work", URLPattern: "https://vndb.org/v{id}", Icon: "BookHeart", ValidationRegex: `^v?\d+$`, Description: "全球权威视觉小说条目数据库 (如 v17)", SortOrder: 120, IsEnabled: true, IsSystem: true},
		{Code: "steam", Names: map[string]string{"zh-CN": "Steam 游戏商店", "zh-TW": "Steam 遊戲商店", "ja": "Steam ストア", "ja-JP": "Steam ストア", "en-US": "Steam"}, Category: "work", URLPattern: "https://store.steampowered.com/app/{id}", Icon: "Gamepad2", ValidationRegex: `^\d+$`, Description: "Valve 旗下一体化数字游戏分发与社群平台 (App ID)", SortOrder: 130, IsEnabled: true, IsSystem: true},
		{Code: "steamdb", Names: map[string]string{"zh-CN": "SteamDB", "zh-TW": "SteamDB", "ja": "SteamDB", "ja-JP": "SteamDB", "en-US": "SteamDB"}, Category: "work", URLPattern: "https://steamdb.info/app/{id}", Icon: "Gamepad2", ValidationRegex: `^\d+$`, Description: "SteamDB 应用页面（存 Steam AppID）", SortOrder: 131, IsEnabled: true, IsSystem: true},
		// 保留实例既有码的作用域与 ID 语义；外部库注册不代表存在导入适配器。
		{Code: "bilibili_bangumi", Names: map[string]string{"zh-CN": "哔哩哔哩番剧", "zh-TW": "嗶哩嗶哩番劇", "ja": "bilibili 番組", "ja-JP": "bilibili 番組", "en-US": "Bilibili Bangumi"}, Category: "work", URLPattern: "https://www.bilibili.com/bangumi/play/{id}", Icon: "Tv", ValidationRegex: `^ss\d+$`, Description: "哔哩哔哩番剧季条目（存 ss ID）", SortOrder: 146, IsEnabled: true, IsSystem: true},
		{Code: "bilibili_media", Names: map[string]string{"zh-CN": "哔哩哔哩媒体条目", "zh-TW": "嗶哩嗶哩媒體條目", "ja": "bilibili メディア", "ja-JP": "bilibili メディア", "en-US": "Bilibili Media"}, Category: "work", URLPattern: "https://www.bilibili.com/bangumi/media/{id}", Icon: "Tv", ValidationRegex: `^md\d+$`, Description: "哔哩哔哩番剧或影视媒体条目（存 md ID）", SortOrder: 147, IsEnabled: true, IsSystem: true},
		{Code: "bilibili_episode", Names: map[string]string{"zh-CN": "哔哩哔哩单集", "zh-TW": "嗶哩嗶哩單集", "ja": "bilibili エピソード", "ja-JP": "bilibili エピソード", "en-US": "Bilibili Episode"}, Category: "content_unit", URLPattern: "https://www.bilibili.com/bangumi/play/{id}", Icon: "Tv", ValidationRegex: `^ep\d+$`, Description: "哔哩哔哩番剧单集条目（存 ep ID）", SortOrder: 148, IsEnabled: true, IsSystem: true},
		{Code: "bilibili_video", Names: map[string]string{"zh-CN": "哔哩哔哩视频", "zh-TW": "嗶哩嗶哩影片", "ja": "bilibili 動画", "ja-JP": "bilibili 動画", "en-US": "Bilibili Video"}, Category: "content_unit", URLPattern: "https://www.bilibili.com/video/{id}", Icon: "Play", ValidationRegex: `^(?:BV[0-9A-Za-z]{10}|av[1-9]\d*)$`, Description: "哔哩哔哩视频条目（存 BVID 或旧 AID）", SortOrder: 149, IsEnabled: true, IsSystem: true},
		{Code: "dlsite", Names: map[string]string{"zh-CN": "DLsite", "zh-TW": "DLsite", "ja": "DLsite", "ja-JP": "DLsite", "en-US": "DLsite"}, Category: "work", URLPattern: "https://www.dlsite.com/maniax/work/=/product_id/{id}.html", Icon: "Package", ValidationRegex: `^(RJ|RE|BJ|VJ)\d{5,8}$`, Description: "日本同人作品、同人音声与游戏下载销售平台（商品号 RJ/RE/BJ/VJ）", SortOrder: 132, IsEnabled: true, IsSystem: true},
		{Code: "dlsite_maker", Names: map[string]string{"zh-CN": "DLsite 社团", "zh-TW": "DLsite 社團", "ja": "DLsite サークル", "ja-JP": "DLsite サークル", "en-US": "DLsite Circle"}, Category: "agent", URLPattern: "https://www.dlsite.com/maniax/circle/profile/=/maker_id={id}.html", Icon: "Users2", ValidationRegex: `^RG\d+$`, Description: "DLsite 社团/品牌（maker_id，RG 开头）", SortOrder: 133, IsEnabled: false, IsSystem: true},
		{Code: "dmm", Names: map[string]string{"zh-CN": "DMM", "zh-TW": "DMM", "ja": "DMM", "ja-JP": "DMM", "en-US": "DMM"}, Category: "work", URLPattern: "https://www.dmm.co.jp/dc/doujin/-/detail/=/cid={id}/", Icon: "ShoppingBag", ValidationRegex: `^[a-z0-9_]+$`, Description: "日本同人、游戏、动画等数字与实体商品销售平台（商品 cid）", SortOrder: 134, IsEnabled: true, IsSystem: true},

		{Code: "anilist", Names: map[string]string{"zh-CN": "AniList", "zh-TW": "AniList", "ja": "AniList", "ja-JP": "AniList", "en-US": "AniList"}, Category: "all", URLPattern: "https://anilist.co/anime/{id}", Icon: "Sparkles", ValidationRegex: `^\d+$`, Description: "现代动画与漫画社交追踪数据库", SortOrder: 140, IsEnabled: true, IsSystem: true},
		{Code: "goodreads", Names: map[string]string{"zh-CN": "Goodreads", "zh-TW": "Goodreads", "ja": "Goodreads", "ja-JP": "Goodreads", "en-US": "Goodreads"}, Category: "work", URLPattern: "https://www.goodreads.com/book/show/{id}", Icon: "BookOpen", ValidationRegex: `^\d+.*$`, Description: "全球读者书评与阅读记录平台", SortOrder: 150, IsEnabled: true, IsSystem: true},
		{Code: "douban_book", Names: map[string]string{"zh-CN": "豆瓣读书", "zh-TW": "豆瓣讀書", "ja": "豆瓣読書", "ja-JP": "豆瓣読書", "en-US": "Douban Book"}, Category: "work", URLPattern: "https://book.douban.com/subject/{id}/", Icon: "Book", ValidationRegex: `^\d+$`, Description: "中文书籍条目与读书笔记社区", SortOrder: 160, IsEnabled: true, IsSystem: true},
		{Code: "isbndb", Names: map[string]string{"zh-CN": "ISBNdb 国际标准书号库", "zh-TW": "ISBNdb 國際標準書號庫", "ja": "ISBNdb（国際標準図書番号データベース）", "ja-JP": "ISBNdb（国際標準図書番号データベース）", "en-US": "ISBNdb"}, Category: "release", URLPattern: "https://isbndb.com/book/{id}", Icon: "Barcode", ValidationRegex: `^[0-9\-]{10,17}$`, Description: "国际标准书号全球注册库 (ISBN-10 / ISBN-13)", SortOrder: 170, IsEnabled: true, IsSystem: true},
		{Code: "isni", Names: map[string]string{"zh-CN": "ISNI 国际标准名称标识", "zh-TW": "ISNI 國際標準名稱識別碼", "ja": "ISNI（国際標準名称識別子）", "ja-JP": "ISNI（国際標準名称識別子）", "en-US": "ISNI"}, Category: "agent", URLPattern: "https://isni.org/isni/{id}", Icon: "UserCheck", ValidationRegex: `^\d{15}[\dX]$`, Description: "ISO 国际标准名称标识符 (16 位数字或 X)", SortOrder: 180, IsEnabled: true, IsSystem: true},
		{Code: "orcid", Names: map[string]string{"zh-CN": "ORCID 学者标识", "zh-TW": "ORCID 學者識別碼", "ja": "ORCID（研究者識別子）", "ja-JP": "ORCID（研究者識別子）", "en-US": "ORCID"}, Category: "agent", URLPattern: "https://orcid.org/{id}", Icon: "GraduationCap", ValidationRegex: `^\d{4}-\d{4}-\d{4}-\d{3}[\dX]$`, Description: "全球科研人员与学者开放唯一标识符", SortOrder: 190, IsEnabled: true, IsSystem: true},
		{Code: "twitter_x", Names: map[string]string{"zh-CN": "X (原 Twitter)", "zh-TW": "X（原 Twitter）", "ja": "X（旧 Twitter）", "ja-JP": "X（旧 Twitter）", "en-US": "X (Twitter)"}, Category: "agent", URLPattern: "https://x.com/{id}", Icon: "AtSign", ValidationRegex: `^[A-Za-z0-9_]{1,15}$`, Description: "官方社交媒体账号 ID", SortOrder: 200, IsEnabled: true, IsSystem: true},
		{Code: "bangumi_person", Names: map[string]string{"zh-CN": "Bangumi 人物", "zh-TW": "Bangumi 人物", "ja": "Bangumi 人物", "ja-JP": "Bangumi 人物", "en-US": "Bangumi Person"}, Category: "agent", URLPattern: "https://bgm.tv/person/{id}", Icon: "User", ValidationRegex: `^\d+$`, Description: "Bangumi 创作者与演职员人物条目", SortOrder: 210, IsEnabled: true, IsSystem: true},
		{Code: "bangumi_ep", Names: map[string]string{"zh-CN": "Bangumi 单集", "zh-TW": "Bangumi 單集", "ja": "Bangumi 単話", "ja-JP": "Bangumi 単話", "en-US": "Bangumi Episode"}, Category: "work", URLPattern: "https://bgm.tv/ep/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "Bangumi 动画单集条目", SortOrder: 215, IsEnabled: true, IsSystem: true},
		{Code: "bangumi_episode", Names: map[string]string{"zh-CN": "Bangumi 单集/篇目", "zh-TW": "Bangumi 單集／篇目", "ja": "Bangumi 単話・篇目", "ja-JP": "Bangumi 単話・篇目", "en-US": "Bangumi Episode"}, Category: "all", URLPattern: "https://bgm.tv/ep/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "Bangumi 单集与篇目条目", SortOrder: 216, IsEnabled: true, IsSystem: true},
		{Code: "isrc", Names: map[string]string{"zh-CN": "ISRC 国际标准录音码", "zh-TW": "ISRC 國際標準錄音代碼", "ja": "ISRC（国際標準レコーディングコード）", "ja-JP": "ISRC（国際標準レコーディングコード）", "en-US": "ISRC"}, Category: "all", URLPattern: "https://isrc.soundexchange.com/#!/search?isrcCode={id}", Icon: "Disc", ValidationRegex: `^[A-Z]{2}[A-Z0-9]{3}\d{7}$`, Description: "国际标准录音制品编码 (ISO 3901)", SortOrder: 217, IsEnabled: true, IsSystem: true},
		{Code: "recording_mbid", Names: map[string]string{"zh-CN": "MusicBrainz 录音", "zh-TW": "MusicBrainz 錄音", "ja": "MusicBrainz 録音", "ja-JP": "MusicBrainz 録音", "en-US": "MusicBrainz Recording"}, Category: "all", URLPattern: "https://musicbrainz.org/recording/{id}", Icon: "Music", ValidationRegex: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`, Description: "MusicBrainz 录音资产标识符 (UUID)", SortOrder: 218, IsEnabled: true, IsSystem: true},
		{Code: "bangumi_character", Names: map[string]string{"zh-CN": "Bangumi 角色", "zh-TW": "Bangumi 角色", "ja": "Bangumi キャラクター", "ja-JP": "Bangumi キャラクター", "en-US": "Bangumi Character"}, Category: "agent", URLPattern: "https://bgm.tv/character/{id}", Icon: "Smile", ValidationRegex: `^\d+$`, Description: "Bangumi 虚拟角色条目", SortOrder: 220, IsEnabled: true, IsSystem: true},
		{Code: "myanimelist", Names: map[string]string{"zh-CN": "MyAnimeList", "zh-TW": "MyAnimeList", "ja": "MyAnimeList", "ja-JP": "MyAnimeList", "en-US": "MyAnimeList"}, Category: "work", URLPattern: "https://myanimelist.net/anime/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "全球动画与漫画数据库 (MAL ID)", SortOrder: 230, IsEnabled: true, IsSystem: true},
		{Code: "mal_person", Names: map[string]string{"zh-CN": "MyAnimeList 影人", "zh-TW": "MyAnimeList 影人", "ja": "MyAnimeList 人物", "ja-JP": "MyAnimeList 人物", "en-US": "MyAnimeList Person"}, Category: "agent", URLPattern: "https://myanimelist.net/people/{id}", Icon: "User", ValidationRegex: `^\d+$`, Description: "MyAnimeList 创作者/声优档案", SortOrder: 240, IsEnabled: true, IsSystem: true},
		{Code: "mal_character", Names: map[string]string{"zh-CN": "MyAnimeList 角色", "zh-TW": "MyAnimeList 角色", "ja": "MyAnimeList キャラクター", "ja-JP": "MyAnimeList キャラクター", "en-US": "MyAnimeList Character"}, Category: "agent", URLPattern: "https://myanimelist.net/character/{id}", Icon: "Smile", ValidationRegex: `^\d+$`, Description: "MyAnimeList 角色档案", SortOrder: 250, IsEnabled: true, IsSystem: true},
		{Code: "tmdb_person", Names: map[string]string{"zh-CN": "TMDB 影人", "zh-TW": "TMDB 影人", "ja": "TMDB 人物", "ja-JP": "TMDB 人物", "en-US": "TMDB Person"}, Category: "agent", URLPattern: "https://www.themoviedb.org/person/{id}", Icon: "User", ValidationRegex: `^\d+$`, Description: "The Movie Database 演职员条目", SortOrder: 260, IsEnabled: true, IsSystem: true},
		{Code: "imdb_person", Names: map[string]string{"zh-CN": "IMDb 影人", "zh-TW": "IMDb 影人", "ja": "IMDb 人物", "ja-JP": "IMDb 人物", "en-US": "IMDb Person"}, Category: "agent", URLPattern: "https://www.imdb.com/name/{id}/", Icon: "User", ValidationRegex: `^nm\d+$`, Description: "IMDb 影人档案 (如 nm0000001)", SortOrder: 270, IsEnabled: true, IsSystem: true},
		{Code: "vndb_character", Names: map[string]string{"zh-CN": "VNDB 角色", "zh-TW": "VNDB 角色", "ja": "VNDB キャラクター", "ja-JP": "VNDB キャラクター", "en-US": "VNDB Character"}, Category: "agent", URLPattern: "https://vndb.org/c{id}", Icon: "Smile", ValidationRegex: `^c?\d+$`, Description: "VNDB 视觉小说角色档案", SortOrder: 280, IsEnabled: true, IsSystem: true},
		{Code: "vndb_staff", Names: map[string]string{"zh-CN": "VNDB 职员", "zh-TW": "VNDB 職員", "ja": "VNDB スタッフ", "ja-JP": "VNDB スタッフ", "en-US": "VNDB Staff"}, Category: "agent", URLPattern: "https://vndb.org/s{id}", Icon: "User", ValidationRegex: `^s?\d+$`, Description: "VNDB 制作人员档案", SortOrder: 290, IsEnabled: true, IsSystem: true},

		// ===== 2026-10-11 新增：agent / 编目常引用、但此前无码可记的外部库 =====
		// 当天本机可达性实测：openbd(api)、gutenberg、kitsu、bilibili 均 HTTP 200；coverartarchive
		// 端点在线（/release/{mbid} 命中封面 200、无封面 404，回 JSON）。其余（ndl_search、openlibrary、
		// google_books、igdb、mobygames、ann、thetvdb、viaf、oclc_fast，另加 anidb 403、asin 的
		// amazon.co.jp curl 000）本机被反爬 / 地域 / TLS 挡成 000/403——注册表记的是"这个 ID 该往哪记、
		// 人点外链去哪看"，与本机能否抓取解耦：这些项仅作引用目标；兼容泛码 bilibili 默认停用，
		// 其余项可用于记录对应外部标识。**没有任何一个有导入适配器**
		// （externalSourceAdapterCodes 只有 bangumi/dlsite/dmm），别把它们当成能抓取的来源。
		// SortOrder 从 300 起、同类相邻，不重排既有行。

		// openbd 的 URLPattern 取舍（2026-10-11 实测）：hanmoto.com/bd/isbn/{id} 是 isbndb.com/book/{id}
		// 的同类"人看书目页"，但对 openbd 确有数据的四个真实 ISBN（角川 / 集英社 / 文艺春秋）全部 404——
		// hanmoto 只收参加社，作外链不可靠；openbd.jp/cover/{id} 本机 000（反爬）且只回封面图、非书目页；
		// api.openbd.jp/v1/get?isbn={id} 本机 200（application/json，返回权威 ONIX 书目），是唯一稳定解析
		// 的 openbd 原生资源，故取它（查询串形态与 isrc 的 search 外链同构，前端按 {id} 替换即可）。
		{Code: "openbd", Names: map[string]string{"zh-CN": "OpenBD 日本出版书目", "zh-TW": "OpenBD 日本出版書目", "ja": "OpenBD（日本の書籍書誌データベース）", "ja-JP": "OpenBD（日本の書籍書誌データベース）", "en-US": "OpenBD"}, Category: "release", URLPattern: "https://api.openbd.jp/v1/get?isbn={id}", Icon: "Barcode", ValidationRegex: `^(?:(?:[0-9]-?){9}[0-9Xx]|9-?7-?[89]-?(?:[0-9]-?){9}[0-9])$`, Description: "日本出版物开放书目 API，按 ISBN 检索（存 ISBN-13 / ISBN-10）", SortOrder: 300, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 000/403（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "ndl_search", Names: map[string]string{"zh-CN": "日本国立国会图书馆检索", "zh-TW": "日本國立國會圖書館檢索", "ja": "国立国会図書館サーチ", "ja-JP": "国立国会図書館サーチ", "en-US": "NDL Search"}, Category: "release", URLPattern: "https://ndlsearch.ndl.go.jp/books/{id}", Icon: "Book", ValidationRegex: `^R[0-9]{9}-I[A-Za-z0-9]+(?:[-_.:][A-Za-z0-9]+)*$`, Description: "日本国立国会图书馆书目检索（NDL 书目 ID，形如 R100000002-I…）", SortOrder: 310, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 000/403（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "openlibrary", Names: map[string]string{"zh-CN": "开放图书馆", "zh-TW": "開放圖書館", "ja": "Open Library（オープンライブラリ）", "ja-JP": "Open Library（オープンライブラリ）", "en-US": "Open Library"}, Category: "release", URLPattern: "https://openlibrary.org/books/{id}", Icon: "BookOpen", ValidationRegex: `^OL\d+[A-Z]$`, Description: "互联网档案馆的开放图书目录（Open Library 图书 ID，形如 OL7030731M）", SortOrder: 320, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 000/403（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "google_books", Names: map[string]string{"zh-CN": "谷歌图书", "zh-TW": "Google 圖書", "ja": "Google ブックス", "ja-JP": "Google ブックス", "en-US": "Google Books"}, Category: "release", URLPattern: "https://books.google.com/books?id={id}", Icon: "Book", ValidationRegex: `^[A-Za-z0-9_\-]{8,}$`, Description: "谷歌图书检索与预览（Google Books 卷册 ID）", SortOrder: 330, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 /ebooks/1342 HTTP 200（可达）
		{Code: "gutenberg", Names: map[string]string{"zh-CN": "古腾堡计划", "zh-TW": "古騰堡計劃", "ja": "プロジェクト・グーテンベルク", "ja-JP": "プロジェクト・グーテンベルク", "en-US": "Project Gutenberg"}, Category: "release", URLPattern: "https://www.gutenberg.org/ebooks/{id}", Icon: "BookOpen", ValidationRegex: `^\d+$`, Description: "公版电子书库（Project Gutenberg 电子书编号）", SortOrder: 340, IsEnabled: true, IsSystem: true},
		// Cover Art Archive 按具体 release MBID 查询，UUID 采用完整 8-4-4-4-12 分组形状。
		{Code: "coverartarchive", Names: map[string]string{"zh-CN": "Cover Art Archive 封面存档", "zh-TW": "Cover Art Archive 封面存檔", "ja": "Cover Art Archive（カバーアート・アーカイブ）", "ja-JP": "Cover Art Archive（カバーアート・アーカイブ）", "en-US": "Cover Art Archive"}, Category: "release", URLPattern: "https://coverartarchive.org/release/{id}", Icon: "Disc", ValidationRegex: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`, Description: "MusicBrainz 封面图存档，按 release MBID 取封面（UUID）", SortOrder: 350, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 curl amazon.co.jp 000（反爬）；浏览器可达，仅引用目标，无适配器
		{Code: "asin", Names: map[string]string{"zh-CN": "亚马逊 ASIN 商品编号", "zh-TW": "亞馬遜 ASIN 商品編號", "ja": "Amazon ASIN（アマゾン標準識別番号）", "ja-JP": "Amazon ASIN（アマゾン標準識別番号）", "en-US": "Amazon ASIN"}, Category: "release", URLPattern: "https://www.amazon.co.jp/dp/{id}", Icon: "ShoppingBag", ValidationRegex: `^[A-Z0-9]{10}$`, Description: "亚马逊商品编号 ASIN（10 位大写字母数字），链到 amazon.co.jp 商品页", SortOrder: 360, IsEnabled: true, IsSystem: true},
		// ean_jan 无权威可解析的单码页（GS1 / JIPDEC 都不提供免费的按 GTIN 查询页），故按 official_website
		// 的先例用 URLPattern:"{id}" 存裸值。isrc 那种"链到检索页"这里没有权威对应：go-upc.com/search?q={id}
		// 本机 200 但只是非权威聚合站，按来源底线不取。前端将 {id} 展开后只链接绝对 HTTP(S) URL，
		// 条码以普通文字展示；这里的 {id} 同时兼容存完整 URL 的其它预设，不新增虚假检索地址。
		{Code: "ean_jan", Names: map[string]string{"zh-CN": "EAN / JAN 商品条码", "zh-TW": "EAN / JAN 商品條碼", "ja": "EAN / JAN コード（商品バーコード）", "ja-JP": "EAN / JAN コード（商品バーコード）", "en-US": "EAN / JAN"}, Category: "release", URLPattern: "{id}", Icon: "Barcode", ValidationRegex: `^(?:\d{8}|\d{12}|\d{13}|\d{14})$`, Description: "国际商品条码 EAN-13 / EAN-8 / JAN（存裸值，无独立条目页）", SortOrder: 370, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 /anime/1 HTTP 200（可达）
		{Code: "kitsu", Names: map[string]string{"zh-CN": "Kitsu 动画数据库", "zh-TW": "Kitsu 動畫資料庫", "ja": "Kitsu（アニメ・マンガデータベース）", "ja-JP": "Kitsu（アニメ・マンガデータベース）", "en-US": "Kitsu"}, Category: "work", URLPattern: "https://kitsu.io/anime/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "动画与漫画社区追踪数据库（Kitsu 条目 ID）", SortOrder: 380, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 anidb.net 403（反爬，同 discogs/vgmdb）；浏览器可达，仅引用目标，无适配器
		{Code: "anidb", Names: map[string]string{"zh-CN": "AniDB 动画数据库", "zh-TW": "AniDB 動畫資料庫", "ja": "AniDB（アニメデータベース）", "ja-JP": "AniDB（アニメデータベース）", "en-US": "AniDB"}, Category: "work", URLPattern: "https://anidb.net/anime/{id}", Icon: "Tv", ValidationRegex: `^\d+$`, Description: "日本动画条目数据库（AniDB 动画 ID）", SortOrder: 390, IsEnabled: true, IsSystem: true},
		// 泛码保留原 work/ss/ep 语义供历史值使用，空库默认不列入公开可选项。
		// 新建条目采用上方四个明确作用域码；ON CONFLICT DO NOTHING 不会停用实例已经启用的泛码。
		{Code: "bilibili", Names: map[string]string{"zh-CN": "哔哩哔哩番剧（兼容）", "zh-TW": "嗶哩嗶哩番劇（相容）", "ja": "bilibili（互換）", "ja-JP": "bilibili（互換）", "en-US": "Bilibili (Legacy)"}, Category: "work", URLPattern: "https://www.bilibili.com/bangumi/play/{id}", Icon: "Tv", ValidationRegex: `^(?:ss|ep)\d+$`, Description: "历史兼容码；新条目使用 bilibili_bangumi / bilibili_media / bilibili_episode / bilibili_video", SortOrder: 400, IsEnabled: false, IsSystem: true},
		// 2026-10-11 本机 000/403（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "ann", Names: map[string]string{"zh-CN": "Anime News Network 动画百科", "zh-TW": "Anime News Network 動畫百科", "ja": "Anime News Network（アニメニュースネットワーク）", "ja-JP": "Anime News Network（アニメニュースネットワーク）", "en-US": "Anime News Network"}, Category: "work", URLPattern: "https://www.animenewsnetwork.com/encyclopedia/anime.php?id={id}", Icon: "BookOpen", ValidationRegex: `^\d+$`, Description: "Anime News Network 百科条目库（anime.php?id 条目 ID）", SortOrder: 410, IsEnabled: true, IsSystem: true},
		// thetvdb regex 取舍：thetvdb.com/series/{id} 的 {id} 实测形态是 slug（如 /series/breaking-bad），
		// 数字 id 走 /dereferrer/ 重定向；本机 thetvdb.com 被挡（403/000）未能直连复核，故 regex 取同时接受
		// slug 与纯数字的 ^[a-z0-9\-]+$，两种存法都不误拒。仅引用目标，无适配器。
		{Code: "thetvdb", Names: map[string]string{"zh-CN": "TheTVDB 电视剧数据库", "zh-TW": "TheTVDB 電視劇資料庫", "ja": "TheTVDB（テレビ番組データベース）", "ja-JP": "TheTVDB（テレビ番組データベース）", "en-US": "TheTVDB"}, Category: "work", URLPattern: "https://www.thetvdb.com/series/{id}", Icon: "Tv2", ValidationRegex: `^[a-z0-9\-]+$`, Description: "TheTVDB 电视剧与电视节目条目（series slug 或数字 ID）", SortOrder: 420, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 403/000（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "igdb", Names: map[string]string{"zh-CN": "IGDB 游戏数据库", "zh-TW": "IGDB 遊戲資料庫", "ja": "IGDB（ゲームデータベース）", "ja-JP": "IGDB（ゲームデータベース）", "en-US": "IGDB"}, Category: "work", URLPattern: "https://www.igdb.com/games/{id}", Icon: "Gamepad2", ValidationRegex: `^[a-z0-9\-]+$`, Description: "互联网游戏数据库 IGDB（/games/ 用 slug，数字 id 亦兼容）", SortOrder: 430, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 403/000（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "mobygames", Names: map[string]string{"zh-CN": "MobyGames 游戏数据库", "zh-TW": "MobyGames 遊戲資料庫", "ja": "MobyGames（ゲームデータベース）", "ja-JP": "MobyGames（ゲームデータベース）", "en-US": "MobyGames"}, Category: "work", URLPattern: "https://www.mobygames.com/game/{id}", Icon: "Gamepad2", ValidationRegex: `^\d+$`, Description: "MobyGames 电子游戏条目数据库（游戏数字 ID，/game/{id} 会重定向到 slug 页）", SortOrder: 440, IsEnabled: true, IsSystem: true},
		// 2026-10-11 本机 403/000（反爬 / 地域 / TLS）；浏览器可达，仅引用目标，无适配器
		{Code: "viaf", Names: map[string]string{"zh-CN": "VIAF 虚拟国际规范文档", "zh-TW": "VIAF 虛擬國際權威檔", "ja": "VIAF（仮想国際規範ファイル）", "ja-JP": "VIAF（仮想国際規範ファイル）", "en-US": "VIAF"}, Category: "agent", URLPattern: "https://viaf.org/viaf/{id}", Icon: "UserCheck", ValidationRegex: `^\d+$`, Description: "虚拟国际规范文档，聚合各国名称规范（VIAF 聚类 ID）", SortOrder: 450, IsEnabled: true, IsSystem: true},
		// oclc_fast regex 取舍（2026-10-11 依 MARC 权威记录核实）：id.worldcat.org/fast/{id} 两种形态都能
		// 解析——裸数字 http://id.worldcat.org/fast/857375（MARC $0 常见写法）与带前缀 .../fast/fst00857500。
		// 故不采用任务初拟的 ^fst?\d+$（它要求 fs 前缀、会误拒裸数字这个最常用形态），改 ^(?:fst)?0*\d+$ 两者都收。
		// 本机 403/000；浏览器可达，仅引用目标，无适配器。
		{Code: "oclc_fast", Names: map[string]string{"zh-CN": "OCLC FAST 主题规范", "zh-TW": "OCLC FAST 主題權威", "ja": "OCLC FAST（分面適用主題詞表）", "ja-JP": "OCLC FAST（分面適用主題詞表）", "en-US": "OCLC FAST"}, Category: "work", URLPattern: "https://id.worldcat.org/fast/{id}", Icon: "Database", ValidationRegex: `^(?:fst)?0*\d+$`, Description: "OCLC 分面应用主题词表 FAST，主题规范 ID（fst 号或裸数字）", SortOrder: 460, IsEnabled: true, IsSystem: true},
	}
}

// backfillExternalDatabaseNames 把种子里的语种译文补进已有行（只补缺失与英文占位，不动已有译文）。
//
// 为什么需要它：播种是 ON CONFLICT DO NOTHING，"种子后来补齐 zh-TW/ja"这件事本身进不了存量库，
// 而名称四语齐备现在是写入硬约束——缺语种的存量行一旦在后台被编辑或启停就会被拒。
// 先补数据、再谈约束，否则升级后管理员连停用一行都做不到。
func backfillExternalDatabaseNames(ctx context.Context, tx *sql.Tx) error {
	seeds := make(map[string]Names, len(externalDatabaseSeeds()))
	for _, d := range externalDatabaseSeeds() {
		seeds[d.Code] = d.Names
	}
	type pending struct {
		code  string
		names Names
	}
	var todo []pending
	// 先读完再写：同一事务里带着未关闭的 Rows 发 UPDATE 会把连接占死。
	rows, err := tx.QueryContext(ctx, `SELECT code,names FROM catalog.external_databases`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		var raw []byte
		if err := rows.Scan(&code, &raw); err != nil {
			rows.Close()
			return err
		}
		seed, ok := seeds[code]
		if !ok {
			continue
		}
		cur := Names{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &cur)
		}
		if len(cur) == 0 {
			continue
		}
		var added []string
		merged := mergeNames("external_databases."+code, cur, seed, &added)
		if len(added) > 0 {
			todo = append(todo, pending{code, merged})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range todo {
		names, _ := json.Marshal(p.names)
		if _, err := tx.ExecContext(ctx, `UPDATE catalog.external_databases SET names=$2 WHERE code=$1`, p.code, string(names)); err != nil {
			return err
		}
	}
	return nil
}

// EnsureSeedExternalDatabases 播种外部权威库并补齐存量行缺失的语种译文（一个事务内完成）。
func (s *Store) EnsureSeedExternalDatabases(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := seedExternalDatabases(ctx, tx); err != nil {
			return err
		}
		return backfillExternalDatabaseNames(ctx, tx)
	})
}

// seedExternalDatabases 写入系统预设；已存在的 code 不覆盖（保留后台自定义），
// 新增的 code 自动补齐。已有行的语种补齐走 backfillExternalDatabaseNames。
func seedExternalDatabases(ctx context.Context, tx *sql.Tx) error {
	for _, d := range externalDatabaseSeeds() {
		names, _ := json.Marshal(d.Names)
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.external_databases(code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (code) DO NOTHING`,
			d.Code, string(names), d.Category, d.URLPattern, d.Icon, d.IconURL, d.ValidationRegex, d.Description, d.SortOrder, d.IsEnabled, d.IsSystem); err != nil {
			return err
		}
	}
	return nil
}
