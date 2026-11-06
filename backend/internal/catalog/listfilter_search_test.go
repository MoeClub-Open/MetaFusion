package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListFilterTagMatchingModes(t *testing.T) {
	for _, mode := range []string{"", "any", "all"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(), ListOptions{Tags: []string{"A", "B"}, TagsMode: mode})
			join := " OR "
			if mode == "all" {
				join = " AND "
			}
			want := "(document->'attributes'->'tags' @> $1::jsonb" + join + "document->'attributes'->'tags' @> $2::jsonb)"
			if !strings.Contains(joined, want) || len(args) != 2 {
				t.Fatalf("mode %q: unexpected predicate %s, args %v", mode, joined, args)
			}
		})
	}
}

// listFilterSearchDefinitions 构造覆盖全部新语法的 definitions 快照：
// 实体 attributes 内的 group（pack）、list（attachments/store_bonuses），
// 以及 locator / inclusion_attributes / subject_attributes 三个结构入口。
// 叶子默认 searchable+enabled，个别用例单独关掉以断言拒绝路径。
func listFilterSearchDefinitions() Definitions {
	d := Defaults()
	// 实体 attributes 演示：group 中途节点（pack）与 list 中途节点（attachments）。
	d.Fields["pack"] = Field{Names: names("包装组", "Pack group"), Type: "group", Enabled: true, Searchable: true, Fields: map[string]Field{
		"format": {Names: names("格式", "Format"), Type: "text", Enabled: true, Searchable: true},
	}}
	att := d.Fields["attachments"]
	items := *att.Items
	fields := map[string]Field{}
	for k, v := range items.Fields {
		fields[k] = v
	}
	fields["store"] = Field{Names: names("店铺", "Store"), Type: "text", Enabled: true, Searchable: true}
	items.Fields = fields
	items.Enabled = true
	items.Searchable = true
	att.Items = &items
	att.Enabled = true
	att.Searchable = true
	d.Fields["attachments"] = att
	// 结构属性入口：locator 叶子（path）与收录/发行对象附加属性叶子。
	loc := d.Fields["locator"]
	loc.Fields["path"] = Field{Names: names("文件路径", "File path"), Type: "text", Enabled: true, Searchable: true}
	d.Fields["locator"] = loc
	inc := d.Fields["inclusion_attributes"]
	inc.Fields["translator"] = Field{Names: names("译者", "Translator"), Type: "text", Enabled: true, Searchable: true}
	d.Fields["inclusion_attributes"] = inc
	sub := d.Fields["subject_attributes"]
	sub.Fields["seq"] = Field{Names: names("序号", "Sequence"), Type: "text", Enabled: true, Searchable: true}
	d.Fields["subject_attributes"] = sub
	return d
}

func listFilterSearchCtx(d Definitions) context.Context {
	return withListFilterDefinitions(context.Background(), d)
}

func listFilterSearchSQL(t *testing.T, d Definitions, o ListOptions) (string, []any) {
	t.Helper()
	args := []any{}
	parts, err := listFilter(listFilterSearchCtx(d), &Store{}, o, nil, &args)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(parts, " AND "), args
}

// 点分 group 路径逐层下钻，叶子保持等值语义。
func TestListFilterDottedGroupPath(t *testing.T) {
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Field: "pack.format", Value: "bd"})
	if !strings.Contains(joined, "document->'attributes'->'pack'->>'format'=$1") {
		t.Fatalf("group path must drill with -> and compare leaf with ->>, got: %s", joined)
	}
	if len(args) != 1 || args[0] != "bd" {
		t.Fatalf("value must be a single bound arg, got %v", args)
	}
}

// 实体 attributes 内的 list 中途节点按"任一元素命中"编译成 EXISTS。
func TestListFilterListMidpointExists(t *testing.T) {
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Field: "attachments.store", Value: "animate"})
	if !strings.Contains(joined, "jsonb_array_elements(") || !strings.Contains(joined, "EXISTS(SELECT 1 FROM ") {
		t.Fatalf("list midpoint must use EXISTS over jsonb_array_elements, got: %s", joined)
	}
	if !strings.Contains(joined, "->>'store'=$1") {
		t.Fatalf("leaf must keep ->> equality, got: %s", joined)
	}
	if len(args) != 1 || args[0] != "animate" {
		t.Fatalf("value must be a single bound arg, got %v", args)
	}
}

// locator 伪字段命中 track_contents 的 EXISTS 子查询。
func TestListFilterLocatorPseudoField(t *testing.T) {
	joined, _ := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Kind: "track", Field: "locator.path", Value: "/disc1"})
	if !strings.Contains(joined, "catalog.track_contents") {
		t.Fatalf("locator must query catalog.track_contents, got: %s", joined)
	}
	if !strings.Contains(joined, "EXISTS(SELECT 1 FROM ") {
		t.Fatalf("locator must compile to EXISTS, got: %s", joined)
	}
}

// inclusion_attributes 伪字段同样命中 track_contents。
func TestListFilterInclusionAttributesPseudoField(t *testing.T) {
	joined, _ := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Kind: "track", Field: "inclusion_attributes.translator", Value: "translator-a"})
	if !strings.Contains(joined, "catalog.track_contents") {
		t.Fatalf("inclusion_attributes must query catalog.track_contents, got: %s", joined)
	}
}

// subject_attributes 伪字段命中 release_subjects。
func TestListFilterSubjectAttributesPseudoField(t *testing.T) {
	joined, _ := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Kind: "release", Field: "subject_attributes.seq", Value: "1"})
	if !strings.Contains(joined, "catalog.release_subjects") {
		t.Fatalf("subject_attributes must query catalog.release_subjects, got: %s", joined)
	}
}

// 结构伪字段在 kind 显式不匹配时恒假（返回空集）而不是报错。
// kind 本身仍按常规占一位绑定参数；取值参数不再绑定（谓词恒假无需比较）。
func TestListFilterStructuralKindMismatchIsEmpty(t *testing.T) {
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Kind: "release", Field: "locator.path", Value: "/disc1"})
	if !strings.Contains(joined, "1=0") {
		t.Fatalf("kind-mismatched structural predicate must be constantly false, got: %s", joined)
	}
	for _, a := range args {
		if a == "/disc1" {
			t.Fatalf("constantly-false predicate must not bind the value arg, got %v", args)
		}
	}
}

// kind 未指定时结构伪字段按 EXISTS 自然过滤，不恒假。
func TestListFilterStructuralWithoutKindUsesExists(t *testing.T) {
	joined, _ := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{Field: "locator.path", Value: "/disc1"})
	if strings.Contains(joined, "1=0") {
		t.Fatalf("unscoped structural predicate must not be constantly false, got: %s", joined)
	}
	if !strings.Contains(joined, "catalog.track_contents") {
		t.Fatalf("unscoped locator must still query track_contents, got: %s", joined)
	}
}

// 不可检索的叶子（Searchable=false）必须被拒绝。
func TestListFilterUnsearchableLeafRejected(t *testing.T) {
	d := listFilterSearchDefinitions()
	loc := d.Fields["locator"]
	path := loc.Fields["path"]
	path.Searchable = false
	loc.Fields["path"] = path
	d.Fields["locator"] = loc
	args := []any{}
	if _, err := listFilter(listFilterSearchCtx(d), &Store{}, ListOptions{Kind: "track", Field: "locator.path", Value: "x"}, nil, &args); err == nil || err.Error() != "field_not_searchable" {
		t.Fatalf("unsearchable leaf must fail with field_not_searchable, got %v", err)
	}
}

// 链路上任一字段 disabled 也必须被拒绝。
func TestListFilterDisabledChainRejected(t *testing.T) {
	d := listFilterSearchDefinitions()
	att := d.Fields["attachments"]
	att.Enabled = false
	d.Fields["attachments"] = att
	args := []any{}
	if _, err := listFilter(listFilterSearchCtx(d), &Store{}, ListOptions{Field: "attachments.store", Value: "x"}, nil, &args); err == nil || err.Error() != "field_not_searchable" {
		t.Fatalf("disabled chain must fail with field_not_searchable, got %v", err)
	}
}

// 原语言精确匹配 document->>'original_language'，取值走绑定参数。
func TestListFilterOriginalLanguageExact(t *testing.T) {
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{OriginalLanguage: "ja"})
	if !strings.Contains(joined, "document->>'original_language'=$1") {
		t.Fatalf("original language must compare document->>'original_language', got: %s", joined)
	}
	if len(args) != 1 || args[0] != "ja" {
		t.Fatalf("value must be a single bound arg, got %v", args)
	}
}

// 仅有封面：pictures 非空数组。CASE 保序（WHEN 为真才求 array_length），
// 缺键/标量/对象走 ELSE=0；不占绑定参数位。
func TestListFilterHasPicturesGuardsType(t *testing.T) {
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(),
		ListOptions{HasPictures: true})
	if !strings.Contains(joined, "CASE WHEN jsonb_typeof(document->'pictures')='array'") ||
		!strings.Contains(joined, "ELSE 0 END>0") {
		t.Fatalf("has_pictures must guard typeof before array_length, got: %s", joined)
	}
	if len(args) != 0 {
		t.Fatalf("has_pictures must not consume bound args, got %v", args)
	}
}

// 未知路径返回 unknown_field。
func TestListFilterUnknownPathRejected(t *testing.T) {
	for _, field := range []string{"pack.no_such_leaf", "no_such_root.format", "attachments.store.deeper", "locator.no_such_key"} {
		args := []any{}
		_, err := listFilter(listFilterSearchCtx(listFilterSearchDefinitions()), &Store{}, ListOptions{Field: field, Value: "x"}, nil, &args)
		if err == nil || err.Error() != "unknown_field" {
			t.Fatalf("field %q must fail with unknown_field, got %v", field, err)
		}
	}
}

// 单层 field=value 行为保持不变：字段名与取值双绑定、->> 等值。
func TestListFilterSingleFieldUnchanged(t *testing.T) {
	d := listFilterSearchDefinitions()
	args := []any{}
	parts, err := listFilter(listFilterSearchCtx(d), &Store{}, ListOptions{Field: "format", Value: "bd"}, nil, &args)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(parts, " AND ")
	if !strings.Contains(joined, "document->'attributes'->>$1=$2") {
		t.Fatalf("single field must keep document->'attributes'->>$1=$2, got: %s", joined)
	}
	if len(args) < 2 || args[len(args)-2] != "format" || args[len(args)-1] != "bd" {
		t.Fatalf("single field must bind field name and value, got %v", args)
	}
}

func TestListFilterTagsBindValidJSON(t *testing.T) {
	tags := []string{`CD\BD`, `a"b`, `背面\封面`}
	joined, args := listFilterSearchSQL(t, listFilterSearchDefinitions(), ListOptions{Tags: tags})
	if !strings.Contains(joined, "document->'attributes'->'tags' @> $1::jsonb") || len(args) != len(tags) {
		t.Fatalf("标签筛选应逐项绑定 JSON 参数：sql=%s args=%v", joined, args)
	}
	for i, arg := range args {
		raw, ok := arg.(string)
		if !ok {
			t.Fatalf("参数 %d 应为 JSON 字符串：%T", i, arg)
		}
		var parsed []string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil || len(parsed) != 1 || parsed[0] != tags[i] {
			t.Fatalf("参数 %d 未保持原始标签 %q：json=%q parsed=%v err=%v", i, tags[i], raw, parsed, err)
		}
	}
}

func TestListFilterMultiValuesAndExclusion(t *testing.T) {
	d := listFilterSearchDefinitions()
	joined, args := listFilterSearchSQL(t, d, ListOptions{
		Kind:          "work",
		Field:         "creation_form",
		Values:        []string{"original_album", "single"},
		ExcludeField:  "creation_form",
		ExcludeValues: []string{"song"},
	})
	if !strings.Contains(joined, "document->'attributes'->>$2 = ANY($3::text[])") {
		t.Fatalf("values should compile to ANY($n::text[]), got: %s", joined)
	}
	if !strings.Contains(joined, "NOT COALESCE((document->'attributes'->>$4 = ANY($5::text[])), false)") {
		t.Fatalf("exclude_field should guard NULL with NOT COALESCE, got: %s (args=%v)", joined, args)
	}

	for _, bad := range []ListOptions{
		{Values: []string{"film"}},
		{Field: "creation_form", Value: "film", Values: []string{"tv_drama"}},
		{ExcludeField: "creation_form"},
		{ExcludeValues: []string{"song"}},
	} {
		a := []any{}
		if _, err := listFilter(listFilterSearchCtx(d), &Store{}, bad, nil, &a); err == nil || err.Error() != "invalid_query_param" {
			t.Fatalf("expected invalid_query_param for %+v, got %v", bad, err)
		}
	}
}

func TestMakeSearchDocumentNonofficialTitlesAndAliases(t *testing.T) {
	doc := makeSearchDocument(Entity{
		ID:               "00000000-0000-0000-0000-000000000001",
		Kind:             "work",
		Title:            "Original JP",
		OriginalLanguage: "ja",
		Translations: map[string]Translation{
			"ja":    {Title: "Original JP", NonofficialAliases: []string{"JP Nickname"}},
			"zh-CN": {Title: "社区译名", Nonofficial: true, NonofficialAliases: []string{"民间简称"}},
		},
	})
	if _, exists := doc.Titles["zh-CN"]; exists {
		t.Fatalf("nonofficial translation must not populate official sort Titles map: %+v", doc.Titles)
	}
	if doc.Titles["ja"] != "Original JP" || doc.SortTitle != "Original JP" {
		t.Fatalf("official original title must remain sort title: %+v %q", doc.Titles, doc.SortTitle)
	}
	joined := strings.Join(doc.SearchText, "|")
	for _, want := range []string{"社区译名", "民间简称", "JP Nickname"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("search text missing %q: %s", want, joined)
		}
	}
}
