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
	switch section {
	case "index":
		fields, relations := map[string]catalog.Field{}, map[string]catalog.RelationDefinition{}
		for code, field := range defs.Fields {
			if in.Kind == "" || slices.Contains(field.ApplicableKinds, in.Kind) {
				fields[code] = field
			}
		}
		for code, relation := range defs.Relations {
			if in.Kind == "" || slices.Contains(relation.SourceKinds, in.Kind) || slices.Contains(relation.TargetKinds, in.Kind) {
				relations[code] = relation
			}
		}
		out["kinds"] = catalog.KindNameRecords()
		out["entity_top_level_fields"] = entityTopLevelFields
		out["field_codes"], out["relation_codes"] = definitionCodes(fields), definitionCodes(relations)
		out["vocabulary_codes"], out["structure_kinds"] = definitionCodes(defs.Vocabularies), definitionCodes(defs.Structure)
		return out, nil
	case "fields":
		value, err = selectDefinitions(defs.Fields, in.Codes)
	case "relations":
		value, err = selectDefinitions(defs.Relations, in.Codes)
	case "structure":
		value, err = selectDefinitions(defs.Structure, in.Codes)
	case "vocabularies":
		var selected map[string]catalog.Vocabulary
		selected, err = selectDefinitions(defs.Vocabularies, in.Codes)
		metadata := map[string]any{}
		for code, vocabulary := range selected {
			metadata[code] = map[string]any{"names": vocabulary.Names, "term_codes": definitionCodes(vocabulary.Terms)}
		}
		value = metadata
	case "terms":
		vocabulary, ok := defs.Vocabularies[in.Vocabulary]
		if !ok {
			return nil, errors.New("definition_vocabulary_not_found")
		}
		out["vocabulary"] = in.Vocabulary
		value, err = selectDefinitions(vocabulary.Terms, in.Codes)
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
