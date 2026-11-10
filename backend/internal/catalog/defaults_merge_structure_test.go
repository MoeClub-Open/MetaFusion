package catalog

import "testing"

func TestMergeSeedBackfillsFixedStructureAdditions(t *testing.T) {
	seed := Defaults()
	current := seed
	current.Structure = make(map[string]StructureRule, len(seed.Structure))
	for kind, rule := range seed.Structure {
		current.Structure[kind] = rule
	}
	track := current.Structure["track"]
	track.Contents = false
	track.Resources = true // 资源开关仍由管理员配置。
	track.Fields = append([]StructureField{}, track.Fields[:1]...)
	current.Structure["track"] = track

	merged, added := mergeSeedDefinitions(current, seed)
	track = merged.Structure["track"]
	if !track.Contents {
		t.Fatal("track.contents 应作为固定结构标记补齐")
	}
	if !track.Resources {
		t.Fatal("不得覆盖管理员配置的 resources 开关")
	}
	if len(track.Fields) != 2 || track.Fields[1].Code != "parent_id" {
		t.Fatalf("缺失固定结构字段应按种子顺序插入，实际 %#v", track.Fields)
	}
	if !contains(added, "structure.track.contents") || !contains(added, "structure.track.fields.parent_id") {
		t.Fatalf("结构补齐项应写入合并变更清单：%v", added)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("合并后的定义应满足固定结构校验：%v", err)
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("重复合并应幂等，实际新增：%v", again)
	}
}
