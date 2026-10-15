package catalog

import (
	"regexp"
	"testing"
)

func externalSeedsByCode(t *testing.T) map[string]ExternalDatabase {
	t.Helper()
	seeds := map[string]ExternalDatabase{}
	for _, d := range externalDatabaseSeeds() {
		if _, exists := seeds[d.Code]; exists {
			t.Fatalf("duplicate external database seed: %s", d.Code)
		}
		if err := validateExternalDatabase(d); err != nil {
			t.Fatalf("invalid external database seed %s: %v", d.Code, err)
		}
		seeds[d.Code] = d
	}
	return seeds
}

func TestExternalDatabaseSeedsIncludeRuntimeCodes(t *testing.T) {
	seeds := externalSeedsByCode(t)
	for code, category := range map[string]string{
		"bilibili_bangumi": "work", "bilibili_media": "work",
		"bilibili_episode": "content_unit", "bilibili_video": "content_unit",
		"discogs_release": "release", "musicbrainz_release": "release",
		"steamdb": "work", "tmdb_tv": "work", "netflix": "work",
		"fandom": "all", "moegirlpedia": "all",
	} {
		d, ok := seeds[code]
		if !ok || d.Category != category || !d.IsEnabled || !d.IsSystem {
			t.Errorf("missing or incompatible seed %s: %+v", code, d)
		}
	}
	legacy := seeds["bilibili"]
	if legacy.IsEnabled || legacy.Category != "work" {
		t.Errorf("legacy bilibili must remain compatible and hidden from new selections: %+v", legacy)
	}
	// 注册表包含引用目标；可抓取能力仍只能来自实际实现的适配器。
	if len(buildExternalSourceDefinitions(externalDatabaseSeeds())) != len(externalSourceAdapterCodes) {
		t.Fatal("reference-only external databases expanded externalSource sources")
	}
}

func TestExternalDatabaseSeedIDShapes(t *testing.T) {
	seeds := externalSeedsByCode(t)
	for _, tt := range []struct {
		code string
		good []string
		bad  []string
	}{
		{"openbd", []string{"9784088820118", "978-4-08-882011-8", "4088820118", "4-08-882011-8", "080442957X"}, []string{"----------", "1234567890123", "978--4088820118", "X234567890", "978408882011X"}},
		{"ndl_search", []string{"R100000002-I029763265", "R100000001-I011111589-00", "R100000039-I1234567"}, []string{"-", "anything", "R100000002-I", "R1-I123", "R100000002-I123--456"}},
		{"coverartarchive", []string{"c1df9f7b-6fbd-4062-a981-034abdeb949d", "C1DF9F7B-6FBD-4062-A981-034ABDEB949D"}, []string{"------------------------------------", "c1df9f7b6fbd4062a981034abdeb949d", "g1df9f7b-6fbd-4062-a981-034abdeb949d"}},
		{"bilibili_bangumi", []string{"ss25739"}, []string{"ep257552", "md28223043"}},
		{"bilibili_media", []string{"md28223043"}, []string{"ss25739"}},
		{"bilibili_episode", []string{"ep257552"}, []string{"ss25739"}},
		{"bilibili_video", []string{"BV1xx411c7mD", "av170001"}, []string{"ep257552", "BV1", "av0"}},
		{"bilibili", []string{"ss25739", "ep257552"}, []string{"md28223043", "BV1xx411c7mD"}},
	} {
		t.Run(tt.code, func(t *testing.T) {
			rx := regexp.MustCompile(seeds[tt.code].ValidationRegex)
			for _, id := range tt.good {
				if !rx.MatchString(id) {
					t.Errorf("valid ID rejected: %s", id)
				}
			}
			for _, id := range tt.bad {
				if rx.MatchString(id) {
					t.Errorf("invalid ID accepted: %s", id)
				}
			}
		})
	}
}
