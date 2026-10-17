package catalog

import (
	"regexp"
	"sort"
	"testing"
)

// 安装基线只描述终态结构；已发布后不可变，后续变化必须另加增量。
// 新库不得重放删列、删表或业务数据搬迁。
func TestStartupSchemaHasNoDestructiveStatements(t *testing.T) {
	baseline, err := catalogBaseline()
	if err != nil {
		t.Fatalf("读不到结构基线: %v", err)
	}
	destructive := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`),
		regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
		regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`),
		regexp.MustCompile(`(?i)\bTRUNCATE\b`),
		regexp.MustCompile(`(?i)\bALTER\s+TABLE\b[^;]*\bDROP\b`),
		regexp.MustCompile(`(?i)\bALTER\s+TABLE\b[^;]*\bALTER\s+COLUMN\b`),
	}
	for _, re := range destructive {
		if loc := re.FindString(baseline); loc != "" {
			t.Errorf("结构基线含破坏性语句 %q；一次性数据迁移应另开迁移，不要写进基线", loc)
		}
	}
}

// 默认定义必须自洽：字段的适用层级、模板引用的字段、词表归属都要能通过校验，
// 否则空库首次 Initialize 就会失败。
func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v", err)
	}
}

// 普通曲目与专辑主作品分别声明，不能因为种子缺少词项而借用附加作品。
func TestDefaultsValidateReleaseTrackWorks(t *testing.T) {
	d := Defaults()
	albumID := "00000000-0000-0000-0000-000000000001"
	songID := "00000000-0000-0000-0000-000000000002"
	release := Entity{
		Kind: "release", Title: "Album", State: "active",
		Translations: map[string]Translation{"en-US": {Title: "Album"}},
		Subjects: []Subject{
			{WorkID: albumID, Role: "primary", Position: 0},
			{WorkID: songID, Role: "track_work", Position: 1},
		},
	}
	seen := map[string]bool{}
	reference := func(id string, kinds []string) error {
		if id != albumID && id != songID || len(kinds) != 1 || kinds[0] != "work" {
			t.Fatalf("unexpected subject reference: id=%s kinds=%v", id, kinds)
		}
		seen[id] = true
		return nil
	}
	if err := d.validateEntity(release, reference, false); err != nil {
		t.Fatalf("ordinary album tracks rejected: %v", err)
	}
	if !seen[albumID] || !seen[songID] {
		t.Fatalf("subject references were not validated: %v", seen)
	}
	release.Subjects[1].Role = "unknown_role"
	if err := d.validateEntity(release, reference, false); errCode(err) != "invalid_term" {
		t.Fatalf("unknown release role: got %v, want invalid_term", err)
	}
}

// 版本维度必须各自独立：类别/批次/地区/渠道是能同时成立的维度，
// 把限定/初回/地区/再版塞进同一个互斥枚举就无法表达"日本初回限定再版"。
func TestDefaultsEditionDimensionsAreSeparate(t *testing.T) {
	d := Defaults()
	cases := map[string][]string{
		"edition_type":         {"standard", "limited", "deluxe", "boxset"},
		"edition_batch":        {"regular", "first_press", "reissue", "reprint"},
		"distribution_channel": {"mixed", "physical", "digital", "web"},
	}
	for vocab, want := range cases {
		v, ok := d.Vocabularies[vocab]
		if !ok {
			t.Fatalf("vocabulary %q missing", vocab)
		}
		got := make([]string, 0, len(v.Terms))
		for code := range v.Terms {
			got = append(got, code)
		}
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("vocabulary %q terms = %v, want %v", vocab, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("vocabulary %q terms = %v, want %v", vocab, got, want)
			}
		}
		if f := d.Fields[vocab]; f.Vocabulary != vocab {
			t.Fatalf("field %q not bound to its vocabulary: %+v", vocab, f)
		}
	}
	// "地区版" 与 "数字版" 不再作为类别词条存在：前者归 country，后者归 distribution_channel。
	for _, gone := range []string{"regional"} {
		if _, ok := d.Vocabularies["edition_type"].Terms[gone]; ok {
			t.Fatalf("edition_type should not carry %q any more", gone)
		}
	}
	if _, ok := d.Vocabularies["edition_type"].Terms["digital"]; ok {
		t.Fatalf("edition_type should not carry digital any more")
	}
}

// 字段适用范围由结构 kind 声明；同一 Work 可以组合不同媒介事实。
func TestDefaultsFieldApplicability(t *testing.T) {
	d := Defaults()
	for _, field := range []string{"duration", "volume_count", "magazine", "broadcast_start", "air_network"} {
		if !contains(d.Fields[field].ApplicableKinds, "work") {
			t.Errorf("work 应可组合字段 %s", field)
		}
	}
	for _, field := range []string{"catalog_number", "barcode", "isbn", "publisher"} {
		if contains(d.Fields[field].ApplicableKinds, "work") {
			t.Errorf("产品标识 %s 不应直接属于 work", field)
		}
		if !contains(d.Fields[field].ApplicableKinds, "release") {
			t.Errorf("产品标识 %s 应属于 release", field)
		}
	}
}

// Scheme 非法必须被 Validate 拒绝：slot 非法、字段未在全局组声明、
// required 越界；类型错一律拒绝。
func TestSchemesRejectedWhenInvalid(t *testing.T) {
	mk := func() Definitions {
		d := Defaults()
		d.Schemes = map[string]Scheme{
			"paper_pages": {
				Names: names("纸书页码", "Paper pages"), Slot: "locator",
				Kinds:    []string{"track"},
				Fields:   []string{"relative_to", "page_start", "page_end"},
				Required: []string{"page_start"},
			},
		}
		return d
	}
	if err := mk().Validate(); err != nil {
		t.Fatalf("valid scheme rejected: %v", err)
	}
	badSlot := mk()
	s := badSlot.Schemes["paper_pages"]
	s.Slot = "no_such_slot"
	badSlot.Schemes["paper_pages"] = s
	if err := badSlot.Validate(); err == nil {
		t.Fatal("scheme with invalid slot accepted")
	}
	unDeclared := mk()
	s = unDeclared.Schemes["paper_pages"]
	s.Fields = []string{"relative_to", "no_such_subfield"}
	unDeclared.Schemes["paper_pages"] = s
	if err := unDeclared.Validate(); err == nil {
		t.Fatal("scheme with undeclared field accepted")
	}
	outside := mk()
	s = outside.Schemes["paper_pages"]
	s.Required = []string{"chapter"}
	outside.Schemes["paper_pages"] = s
	if err := outside.Validate(); err == nil {
		t.Fatal("scheme with required outside fields accepted")
	}
	badKind := mk()
	s = badKind.Schemes["paper_pages"]
	s.Kinds = []string{"no_such_kind"}
	badKind.Schemes["paper_pages"] = s
	if err := badKind.Validate(); err == nil {
		t.Fatal("scheme with invalid kind accepted")
	}
	// 方案未包含全局组声明的 AnchorKey 必须被拒绝（防止录入时出现缺锚点死锁）
	missingAnchor := mk()
	s = missingAnchor.Schemes["paper_pages"]
	s.Fields = []string{"page_start", "page_end"}
	s.Required = []string{"page_start"}
	missingAnchor.Schemes["paper_pages"] = s
	if err := missingAnchor.Validate(); err == nil {
		t.Fatal("scheme missing anchor key relative_to must be rejected")
	}
}

// 词表必须包含编目常用的作品间关系；反向名需成对声明。
func TestDefaultsWorkRelations(t *testing.T) {
	d := Defaults()
	for _, code := range []string{"adaptation_of", "sequel_of", "spin_off_of", "soundtrack_of", "character_in", "written_by", "illustrated_by"} {
		rel, ok := d.Relations[code]
		if !ok || !rel.Enabled {
			t.Fatalf("relation %q missing or disabled", code)
		}
		if rel.Names["zh-CN"] == "" || rel.Names["en-US"] == "" || rel.ReverseNames["zh-CN"] == "" {
			t.Fatalf("relation %q names incomplete: %+v", code, rel)
		}
	}
	if rel := d.Relations["spin_off_of"]; !rel.Acyclic || rel.SourceKinds[0] != "work" || rel.TargetKinds[0] != "work" {
		t.Fatalf("spin_off_of misconfigured: %+v", rel)
	}
}
