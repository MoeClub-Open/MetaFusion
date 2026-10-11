package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

// TestBackfillExternalDatabaseNamesRepairsStoredRows 覆盖升级后的存量实例：
// 外部权威库的播种是 ON CONFLICT DO NOTHING，种子后来补的 zh-TW/ja 进不了已有行；
// 而名称四语齐备已是写入硬约束，缺语种的行会在后台被拒。显式 seed 回填必须补上缺口，
// 同时不能覆盖后台已经改过的人工译文（与定义种子同一「只增不改」精神）。
func TestBackfillExternalDatabaseNamesRepairsStoredRows(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	read := func(code string) Names {
		t.Helper()
		var raw []byte
		if err := s.DB.QueryRowContext(ctx, `SELECT names FROM catalog.external_databases WHERE code=$1`, code).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out := Names{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// 造出两种存量行：只有中英的，和日文被人工改过的。
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.external_databases SET names='{"zh-CN":"官方网站","en-US":"Official Website"}'::jsonb WHERE code='official_website'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.external_databases SET names=jsonb_set(names,'{ja-JP}','"手で直した公式サイト"') WHERE code='wikidata'`); err != nil {
		t.Fatal(err)
	}
	// 泛码的默认停用只作用于空库；已有实例的启用状态和自定义模板不能被 seed 覆盖。
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.external_databases SET is_enabled=true,url_pattern='https://example.org/legacy/{id}' WHERE code='bilibili'`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeedExternalDatabases(ctx); err != nil {
		t.Fatal(err)
	}
	// 缺语种的行补齐，且译文来自种子。
	got := read("official_website")
	if got["zh-TW"] != "官方網站" || got["ja-JP"] != "公式サイト" {
		t.Errorf("存量行未按种子补齐语种：%v", got)
	}
	// 人工改过的日文不被覆盖。
	if w := read("wikidata"); w["ja-JP"] != "手で直した公式サイト" {
		t.Errorf("人工译文被种子覆盖：%v", w["ja-JP"])
	}
	var legacyEnabled bool
	var legacyPattern string
	if err := s.DB.QueryRowContext(ctx, `SELECT is_enabled,url_pattern FROM catalog.external_databases WHERE code='bilibili'`).Scan(&legacyEnabled, &legacyPattern); err != nil {
		t.Fatal(err)
	}
	if !legacyEnabled || legacyPattern != "https://example.org/legacy/{id}" {
		t.Errorf("已有泛码配置被 seed 覆盖：enabled=%v pattern=%q", legacyEnabled, legacyPattern)
	}
	// 全部种子行回填后都应满足写路径的四语判据。
	for _, d := range externalDatabaseSeeds() {
		if err := validateNames(read(d.Code)); err != nil {
			t.Errorf("回填后仍不满足四语判据 %s: %v", d.Code, err)
		}
	}
	// 幂等：再跑一次不改变结果。
	before := read("official_website")["zh-TW"]
	if err := s.EnsureSeedExternalDatabases(ctx); err != nil {
		t.Fatal(err)
	}
	if after := read("official_website")["zh-TW"]; after != before {
		t.Errorf("回填不幂等：%q -> %q", before, after)
	}
}

func TestSeedExternalDatabasesPreservesExistingConfiguration(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// 模拟实例已有的人工运行码：管理员停用它并调整全部可配置字段。
	// 种子新增同码只能补缺语种，不能升级系统标记、重新启用或替换人工配置。
	want := ExternalDatabase{
		Code: "bilibili_bangumi", Names: map[string]string{
			"zh-CN": "手工番剧库", "zh-TW": "手動番劇庫", "ja-JP": "手動番組登録",
			"en-US": "Custom seasonal registry", "fr": "Catalogue personnalisé",
		},
		Category: "all", URLPattern: "https://example.org/season/{id}",
		Icon: "Database", IconURL: "https://example.org/icon.svg",
		ValidationRegex: `^custom-\d+$`, Description: "人工维护的配置",
		SortOrder: 999, IsEnabled: false, IsSystem: false,
	}
	names, err := json.Marshal(want.Names)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.external_databases SET names=$2,category=$3,url_pattern=$4,icon=$5,icon_url=$6,validation_regex=$7,description=$8,sort_order=$9,is_enabled=$10,is_system=$11 WHERE code=$1`,
		want.Code, string(names), want.Category, want.URLPattern, want.Icon, want.IconURL,
		want.ValidationRegex, want.Description, want.SortOrder, want.IsEnabled, want.IsSystem); err != nil {
		t.Fatal(err)
	}
	// 缺少 ja 别名可补齐；已有 ja-JP 译文与额外语种必须保持原样。
	want.Names["ja"] = externalSeedsByCode(t)[want.Code].Names["ja"]
	for i := 0; i < 2; i++ {
		if err := s.EnsureSeedExternalDatabases(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := scanExternalDatabase(s.DB.QueryRowContext(ctx, `SELECT code,names,category,url_pattern,icon,icon_url,validation_regex,description,sort_order,is_enabled,is_system FROM catalog.external_databases WHERE code=$1`, want.Code).Scan)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("seed pass %d overwrote existing registry configuration: got %+v want %+v", i+1, got, want)
		}
	}
}
