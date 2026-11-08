package assistant

import (
	"errors"
	"slices"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

type definitionQuery struct {
	Section    string   `json:"section"`
	Kind       string   `json:"kind"`
	Codes      []string `json:"codes"`
	Vocabulary string   `json:"vocabulary"`
	Locale     string   `json:"locale"`
}

func definitionCodes[T any](items map[string]T) []string {
	codes := make([]string, 0, len(items))
	for code := range items {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

func selectDefinitions[T any](items map[string]T, codes []string) (map[string]T, error) {
	if len(codes) == 0 || len(codes) > maxAssistantToolItems {
		return nil, errors.New("definition_codes_required")
	}
	out := make(map[string]T, len(codes))
	for _, code := range codes {
		value, ok := items[code]
		if !ok {
			return nil, errors.New("definition_code_not_found")
		}
		out[code] = value
	}
	return out, nil
}

func filterLocaleNames(names catalog.Names, locale string) catalog.Names {
	if locale == "" || len(names) <= 1 {
		return names
	}
	if v, ok := names[locale]; ok && v != "" {
		return catalog.Names{locale: v}
	}
	for _, fallback := range []string{"zh-CN", "en-US", "ja-JP", "zh-TW"} {
		if v, ok := names[fallback]; ok && v != "" {
			return catalog.Names{fallback: v}
		}
	}
	return names
}

var entityTopLevelFields = []string{
	"title", "original_language", "translations", "attributes", "external_links",
	"pictures", "position", "number", "contents", "subjects",
}

func assistantDefinitionOutput(config catalog.DefinitionConfig, in definitionQuery) (map[string]any, error) {
	defs := config.Document
	section := in.Section
	if section == "" {
		section = "index"
	}
	out := map[string]any{"etag": config.ETag, "section": section}
	var value any
	var err error
	filteredFields := func() map[string]catalog.Field {
		res := map[string]catalog.Field{}
		for code, field := range defs.Fields {
			if in.Kind == "" || slices.Contains(field.ApplicableKinds, in.Kind) {
				field.Names = filterLocaleNames(field.Names, in.Locale)
				res[code] = field
			}
		}
		return res
	}
	filteredRelations := func() map[string]catalog.RelationDefinition {
		res := map[string]catalog.RelationDefinition{}
		for code, relation := range defs.Relations {
			if in.Kind == "" || slices.Contains(relation.SourceKinds, in.Kind) || slices.Contains(relation.TargetKinds, in.Kind) {
				relation.Names = filterLocaleNames(relation.Names, in.Locale)
				relation.ReverseNames = filterLocaleNames(relation.ReverseNames, in.Locale)
				relation.GroupNames = filterLocaleNames(relation.GroupNames, in.Locale)
				res[code] = relation
			}
		}
		return res
	}
	switch section {
	case "index":
		fields, relations := filteredFields(), filteredRelations()
		out["kinds"] = catalog.KindNameRecords()
		out["entity_top_level_fields"] = entityTopLevelFields
		out["field_codes"], out["relation_codes"] = definitionCodes(fields), definitionCodes(relations)
		out["vocabulary_codes"], out["structure_kinds"] = definitionCodes(defs.Vocabularies), definitionCodes(defs.Structure)
		return out, nil
	case "fields":
		if len(in.Codes) == 0 {
			value = filteredFields()
		} else {
			var selected map[string]catalog.Field
			selected, err = selectDefinitions(defs.Fields, in.Codes)
			if err == nil && in.Locale != "" {
				for k, f := range selected {
					f.Names = filterLocaleNames(f.Names, in.Locale)
					selected[k] = f
				}
			}
			value = selected
		}
	case "relations":
		if len(in.Codes) == 0 {
			value = filteredRelations()
		} else {
			var selected map[string]catalog.RelationDefinition
			selected, err = selectDefinitions(defs.Relations, in.Codes)
			if err == nil && in.Locale != "" {
				for k, r := range selected {
					r.Names = filterLocaleNames(r.Names, in.Locale)
					r.ReverseNames = filterLocaleNames(r.ReverseNames, in.Locale)
					r.GroupNames = filterLocaleNames(r.GroupNames, in.Locale)
					selected[k] = r
				}
			}
			value = selected
		}
	case "structure":
		codes := in.Codes
		if len(codes) == 0 && in.Kind != "" {
			codes = []string{in.Kind}
		}
		if len(codes) == 0 {
			value = defs.Structure
		} else {
			value, err = selectDefinitions(defs.Structure, codes)
		}
	case "vocabularies":
		var selected map[string]catalog.Vocabulary
		if len(in.Codes) == 0 {
			selected = defs.Vocabularies
		} else {
			selected, err = selectDefinitions(defs.Vocabularies, in.Codes)
		}
		metadata := map[string]any{}
		for code, vocabulary := range selected {
			metadata[code] = map[string]any{"names": filterLocaleNames(vocabulary.Names, in.Locale), "term_codes": definitionCodes(vocabulary.Terms)}
		}
		value = metadata
	case "terms":
		vocabulary, ok := defs.Vocabularies[in.Vocabulary]
		if !ok {
			return nil, errors.New("definition_vocabulary_not_found")
		}
		out["vocabulary"] = in.Vocabulary
		var selected map[string]catalog.Term
		if len(in.Codes) == 0 {
			selected = make(map[string]catalog.Term, len(vocabulary.Terms))
			for k, t := range vocabulary.Terms {
				selected[k] = t
			}
		} else {
			selected, err = selectDefinitions(vocabulary.Terms, in.Codes)
		}
		if err == nil && in.Locale != "" {
			for k, t := range selected {
				t.Names = filterLocaleNames(t.Names, in.Locale)
				selected[k] = t
			}
		}
		value = selected
	default:
		return nil, errors.New("definition_section_invalid")
	}
	if err != nil {
		return nil, err
	}
	out["definitions"] = value
	return out, nil
}

func assistantExternalDatabasesOutput(items []catalog.ExternalDatabase, codes []string) (map[string]map[string]any, error) {
	byCode := make(map[string]map[string]any, len(items))
	for _, d := range items {
		byCode[d.Code] = map[string]any{
			"code":             d.Code,
			"names":            d.Names,
			"category":         d.Category,
			"url_pattern":      d.URLPattern,
			"validation_regex": d.ValidationRegex,
		}
	}
	if len(codes) == 0 {
		return byCode, nil
	}
	return selectDefinitions(byCode, codes)
}
