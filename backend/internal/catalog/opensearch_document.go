package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type searchDocument struct {
	RecordType       string            `json:"record_type"`
	EntityID         string            `json:"entity_id"`
	EntityVersion    int64             `json:"entity_version"`
	Kind             string            `json:"kind"`
	Status           string            `json:"status"`
	CreatedBy        string            `json:"created_by"`
	Tags             []string          `json:"tags"`
	OriginalLanguage string            `json:"original_language"`
	WorkID           string            `json:"work_id"`
	ContentUnitID    string            `json:"content_unit_id"`
	ReleaseID        string            `json:"release_id"`
	MediumID         string            `json:"medium_id"`
	ParentID         string            `json:"parent_id"`
	WorkIDs          []string          `json:"work_ids"`
	SortTitle        string            `json:"sort_title"`
	Titles           map[string]string `json:"titles"`
	Fields           []searchField     `json:"fields"`
	HasPictures      bool              `json:"has_pictures"`
	UpdatedAt        time.Time         `json:"updated_at"`
	TitleText        []string          `json:"title_text"`
	SearchText       []string          `json:"search_text"`
	SearchTextGrams  []string          `json:"search_text_grams"`
	SearchTextExact  []string          `json:"search_text_exact"`
}

type searchField struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

// Flatten lists without their row indexes so field=a.b means any record's b.
func searchFields(path string, value any, out *[]searchField) {
	if value == nil {
		return
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case map[string]any:
		if path != "" {
			*out = append(*out, searchField{path, searchJSONText(v)})
		}
		for key, child := range v {
			p := key
			if path != "" {
				p = path + "." + key
			}
			searchFields(p, child, out)
		}
		return
	case []any:
		for _, child := range v {
			switch child.(type) {
			case map[string]any, []any:
				searchFields(path, child, out)
			}
		}
		// A list leaf is queried as its JSON text, as with PostgreSQL ->>.
		text = searchJSONText(v)
	case []string:
		values := make([]any, len(v))
		for i, s := range v {
			values[i] = s
		}
		text = searchJSONText(values)
	default:
		text = fmt.Sprint(v)
	}
	*out = append(*out, searchField{path, text})
}

// jsonb ->> serializes container values with PostgreSQL's key ordering and
// spaces; indexing that representation preserves exact field/value filters.
func searchJSONText(value any) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) < len(keys[j])
			}
			return keys[i] < keys[j]
		})
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, searchJSONText(key)+": "+searchJSONText(v[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, searchJSONText(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(value)
		return strings.TrimSuffix(b.String(), "\n")
	}
}

func makeSearchDocument(e Entity) searchDocument {
	titles := make([]string, 0, len(e.Translations)+1)
	text := make([]string, 0, 1+len(e.Translations)*3)
	exact := make([]string, 0, 1+len(e.Translations)*3)
	fields := []searchField{}
	searchFields("", e.Attributes, &fields)
	workIDs := []string{}
	if (e.Kind == "content_unit" || e.Kind == "expression") && e.WorkID != "" {
		workIDs = append(workIDs, e.WorkID)
	}
	for _, subject := range e.Subjects {
		workIDs = append(workIDs, subject.WorkID)
		searchFields("subject_attributes", subject.Attributes, &fields)
	}
	for _, content := range e.Contents {
		searchFields("locator", map[string]any(content.Locator), &fields)
		searchFields("inclusion_attributes", content.Attributes, &fields)
	}
	titleMap := map[string]string{}
	for locale, row := range e.Translations {
		if sortLocaleOK(locale) && row.Title != "" {
			titleMap[locale] = row.Title
		}
	}
	sortTitle := e.Title
	if row, ok := e.Translations["en-US"]; ok && row.Title != "" {
		sortTitle = row.Title
	}
	if row, ok := e.Translations[e.OriginalLanguage]; ok && row.Title != "" {
		sortTitle = row.Title
	}
	add := func(dst *[]string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if utf8.RuneCountInString(value) > 4096 {
			value = string([]rune(value)[:4096])
		}
		*dst = append(*dst, value)
	}
	addText := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		add(&text, value)
		exact = append(exact, splitSearchText(value)...)
	}
	add(&titles, e.Title)
	addText(e.Title)
	locales := make([]string, 0, len(e.Translations))
	for locale := range e.Translations {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		translation := e.Translations[locale]
		add(&titles, translation.Title)
		addText(translation.Title)
		addText(translation.Summary)
		// Index the full article rather than silently truncating it at add's
		// 4096-rune limit. Overlap preserves substring matches across chunks.
		for _, chunk := range splitSearchText(translation.Body) {
			addText(chunk)
		}
		for _, alias := range translation.Aliases {
			addText(alias)
		}
	}

	for _, tag := range searchTags(e.Attributes["tags"]) {
		addText(tag)
	}
	// External IDs are searchable alongside titles, aliases, and summaries.
	externalKeys := make([]string, 0, len(e.ExternalIDs))
	for key := range e.ExternalIDs {
		externalKeys = append(externalKeys, key)
	}
	sort.Strings(externalKeys)
	for _, key := range externalKeys {
		value := strings.TrimSpace(e.ExternalIDs[key])
		if value == "" {
			continue
		}
		addText(key + ":" + value)
		addText(value)
	}
	return searchDocument{
		RecordType:       "entity",
		EntityID:         e.ID,
		EntityVersion:    e.Version,
		Kind:             e.Kind,
		Status:           e.Status,
		CreatedBy:        e.CreatedBy,
		Tags:             searchTags(e.Attributes["tags"]),
		OriginalLanguage: e.OriginalLanguage,
		WorkID:           e.WorkID,
		ContentUnitID:    e.ContentUnitID,
		ReleaseID:        e.ReleaseID,
		MediumID:         e.MediumID,
		ParentID:         e.ParentID,
		WorkIDs:          workIDs,
		SortTitle:        sortTitle,
		Titles:           titleMap,
		Fields:           fields,
		HasPictures:      len(e.Pictures) > 0,
		UpdatedAt:        e.UpdatedAt,
		TitleText:        titles,
		SearchText:       text,
		SearchTextGrams:  exact,
		SearchTextExact:  exact,
	}
}

// Keyword values are split into overlapping chunks so the exact substring
// matching covers long summaries without OpenSearch's per-term size limit.
func splitSearchText(value string) []string {
	runes := []rune(value)
	const chunkSize, overlap = 2048, 255
	chunks := make([]string, 0, len(runes)/chunkSize+1)
	for start := 0; start < len(runes); {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return chunks
}

func searchTags(value any) []string {
	switch tags := value.(type) {
	case []string:
		return tags
	case []any:
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if value, ok := tag.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}
