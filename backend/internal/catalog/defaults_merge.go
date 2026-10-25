package catalog

import "sort"

// mergeSeedDefinitions 把种子里**新增**的定义并进当前定义：只补当前缺失的键
// 已存在的字段/词表/关系/模板/方案一律原样保留。
//
// 为什么需要它：定义种子原先只在空库播种，存量实例拿不到新版本新增的关系码与字段，
// 只能靠导入预检兜底。但"只空库播种"的初衷是怕覆盖人工编目决策（禁用某关系码、
// 词表降级等），而不是拒绝新增——只增不改同样能满足这个初衷。
// 返回合并结果与被新增的键（"relations.member_of" 形式），供调用方写审计说明。
func mergeSeedDefinitions(current, seed Definitions) (Definitions, []string) {
	out := Definitions{
		Fields:       make(map[string]Field, len(current.Fields)),
		Vocabularies: make(map[string]Vocabulary, len(current.Vocabularies)),
		Relations:    make(map[string]RelationDefinition, len(current.Relations)),
		Templates:    make(map[string]Template, len(current.Templates)),
		Schemes:      make(map[string]Scheme, len(current.Schemes)),
		Structure:    make(map[string]StructureRule, len(current.Structure)),
	}
	for k, v := range current.Structure {
		out.Structure[k] = v
	}
	for k, v := range current.Fields {
		out.Fields[k] = v
	}
	for k, v := range current.Vocabularies {
		out.Vocabularies[k] = v
	}
	for k, v := range current.Relations {
		out.Relations[k] = v
	}
	for k, v := range current.Templates {
		out.Templates[k] = v
	}
	for k, v := range current.Schemes {
		out.Schemes[k] = v
	}

	var added []string
	for k, v := range seed.Fields {
		if _, ok := out.Fields[k]; !ok {
			out.Fields[k] = v
			added = append(added, "fields."+k)
		}
	}
	// 种子新增的适用 kind 只做并集，不覆盖人工限定。
	for code, seeded := range seed.Fields {
		currentField := out.Fields[code]
		for _, kind := range seeded.ApplicableKinds {
			if !contains(currentField.ApplicableKinds, kind) {
				currentField.ApplicableKinds = append(currentField.ApplicableKinds, kind)
				added = append(added, "fields."+code+".applicable_kinds."+kind)
			}
		}
		out.Fields[code] = currentField
	}
	for k, v := range seed.Vocabularies {
		if _, ok := out.Vocabularies[k]; !ok {
			out.Vocabularies[k] = v
			added = append(added, "vocabularies."+k)
		}
	}
	for code, seedVocabulary := range seed.Vocabularies {
		currentVocabulary, ok := out.Vocabularies[code]
		if !ok {
			continue
		}
		terms := make(map[string]Term, len(currentVocabulary.Terms))
		for termCode, term := range currentVocabulary.Terms {
			terms[termCode] = term
		}
		changed := false
		for termCode, seedTerm := range seedVocabulary.Terms {
			term, exists := terms[termCode]
			if !exists {
				terms[termCode] = seedTerm
				added = append(added, "vocabularies."+code+".terms."+termCode)
				changed = true
				continue
			}
			if term.IsBonus != nil || seedTerm.IsBonus == nil {
				continue
			}
			term.IsBonus = seedTerm.IsBonus
			terms[termCode] = term
			added = append(added, "vocabularies."+code+".terms."+termCode+".is_bonus")
			changed = true
		}
		if changed {
			currentVocabulary.Terms = terms
			out.Vocabularies[code] = currentVocabulary
		}
	}
	for k, v := range seed.Relations {
		if _, ok := out.Relations[k]; !ok {
			out.Relations[k] = v
			added = append(added, "relations."+k)
		}
	}
	for k, v := range seed.Templates {
		if _, ok := out.Templates[k]; !ok {
			out.Templates[k] = v
			added = append(added, "templates."+k)
		}
	}
	// New selector properties were never editable in the old contract. Fill
	// only absent selectors; an explicit [] and custom priority are preserved.
	for code, seedTemplate := range seed.Templates {
		cur := out.Templates[code]
		if cur.Match != nil || seedTemplate.Match == nil {
			continue
		}
		cur.Match = seedTemplate.Match
		if cur.Priority == 0 {
			cur.Priority = seedTemplate.Priority
		}
		out.Templates[code] = cur
		added = append(added, "templates."+code+".match")
	}
	for k, v := range seed.Schemes {
		if _, ok := out.Schemes[k]; !ok {
			out.Schemes[k] = v
			added = append(added, "schemes."+k)
		}
	}
	// 仅补旧定义缺失的限制；显式空数组代表管理员取消限制，必须保留。
	for code, seedScheme := range seed.Schemes {
		cur, ok := out.Schemes[code]
		if !ok || cur.MediumFormats != nil || seedScheme.MediumFormats == nil {
			continue
		}
		formats := append([]string{}, (*seedScheme.MediumFormats)...)
		cur.MediumFormats = &formats
		out.Schemes[code] = cur
		added = append(added, "schemes."+code+".medium_formats")
	}
	// 已发布关系的署名开关与参与者槽位由定义持有；种子只添加缺失的关系。
	for k, v := range seed.Structure {
		if _, ok := out.Structure[k]; !ok {
			out.Structure[k] = v
			added = append(added, "structure."+k)
		}
	}
	// 固定结构随目录骨架扩展时，只补新固定字段与必需标记；
	// 资源开关由管理员配置，既有结构字段声明也不覆盖。
	for kind, seedRule := range seed.Structure {
		cur, ok := out.Structure[kind]
		if !ok {
			continue
		}
		if seedRule.Subjects && !cur.Subjects {
			cur.Subjects = true
			added = append(added, "structure."+kind+".subjects")
		}
		if seedRule.Contents && !cur.Contents {
			cur.Contents = true
			added = append(added, "structure."+kind+".contents")
		}
		for i, seedField := range seedRule.Fields {
			found := false
			for _, currentField := range cur.Fields {
				if currentField.Code == seedField.Code {
					found = true
					break
				}
			}
			if found {
				continue
			}
			fields := append([]StructureField{}, cur.Fields...)
			if i > len(fields) {
				i = len(fields)
			}
			fields = append(fields, StructureField{})
			copy(fields[i+1:], fields[i:])
			fields[i] = seedField
			cur.Fields = fields
			added = append(added, "structure."+kind+".fields."+seedField.Code)
		}
		out.Structure[kind] = cur
	}
	// 名称译文补丁：只填仍是英文占位的语种（见 mergeNames 注释），不覆盖已有译文。
	added = append(added, backfillTranslations(&out, seed)...)
	sort.Strings(added)
	return out, added
}
