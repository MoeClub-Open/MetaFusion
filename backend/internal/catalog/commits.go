package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type CommitContentChange struct {
	Action    string     `json:"action"`
	Position  int        `json:"position"`
	Inclusion *Inclusion `json:"inclusion,omitempty"`
}

// SourceRef preserves references already stored on external links.
type SourceRef struct {
	Source   string   `json:"source"`
	Pointers []string `json:"pointers,omitempty"`
}

// Paths use JSON Pointer escaping. Arrays are atomic values; indexes cannot be
// edited independently because their identity changes when another editor sorts.
type CommitPatch struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value,omitempty"`
	Remove bool            `json:"remove,omitempty"`
}
type commitAuthor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// FieldConflict is one divergent JSON Pointer from a three-way merge.
// Values are decoded JSON (null when the pointer was absent). Suggestions use
// the project vocabulary: manual_merge, take_current, rebase_keep_mine.
type FieldConflict struct {
	Operation   int      `json:"operation"`
	Pointer     string   `json:"pointer"`
	Base        any      `json:"base"`
	Current     any      `json:"current"`
	Proposed    any      `json:"proposed"`
	Remove      bool     `json:"remove,omitempty"`
	Kind        string   `json:"kind"`
	Suggestions []string `json:"suggestions"`
}

type commitOperationError struct {
	Index int
	Cause error
}

func (e *commitOperationError) Error() string { return e.Cause.Error() }
func (e *commitOperationError) Unwrap() error { return e.Cause }

var commitRef = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

var entityCommitFields = strings.Fields("title original_language translations attributes external_links pictures content_unit_id parent_id position number contents subjects")
var relationCommitFields = strings.Fields("position attributes")

func pointerParts(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("invalid_patch_path")
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) > 16 {
		return nil, fmt.Errorf("invalid_patch_path")
	}
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				if j+1 == len(p) || (p[j+1] != '0' && p[j+1] != '1') {
					return nil, fmt.Errorf("invalid_patch_path")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		if parts[i] == "" {
			return nil, fmt.Errorf("invalid_patch_path")
		}
	}
	return parts, nil
}

// Local references are explicit objects {"$ref":"name"}, never title strings.
func resolveCommitRefs(raw json.RawMessage, refs map[string]string) (json.RawMessage, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid_document")
	}
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["$ref"]; ok {
				name, ok := r.(string)
				id, found := refs[name]
				if !ok || !found || len(x) != 1 {
					return nil, fmt.Errorf("invalid_local_reference")
				}
				return id, nil
			}
			for k, child := range x {
				val, err := walk(child)
				if err != nil {
					return nil, err
				}
				x[k] = val
			}
		case []any:
			for i, child := range x {
				val, err := walk(child)
				if err != nil {
					return nil, err
				}
				x[i] = val
			}
		}
		return v, nil
	}
	v, err := walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func strictCommitDocument(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid_document")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid_document")
	}
	return nil
}
func commitMap(v any) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(encode(v)), &m)
	return m
}
func pointerValue(doc map[string]json.RawMessage, parts []string) (json.RawMessage, bool, error) {
	value, ok := doc[parts[0]]
	for _, k := range parts[1:] {
		if !ok {
			return nil, false, nil
		}
		var obj map[string]json.RawMessage
		if string(value) == "null" {
			return nil, false, nil
		}
		if err := json.Unmarshal(value, &obj); err != nil {
			return nil, false, fmt.Errorf("patch_requires_object")
		}
		value, ok = obj[k]
	}
	return value, ok, nil
}
func equalCommitValue(a json.RawMessage, aok bool, b json.RawMessage, bok bool) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	// Canonicalize numbers and maps using the same decoder on both sides.
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return encode(av) == encode(bv)
}
func putPointer(doc map[string]json.RawMessage, parts []string, value json.RawMessage, remove bool) error {
	if len(parts) == 1 {
		if remove {
			delete(doc, parts[0])
		} else {
			doc[parts[0]] = value
		}
		return nil
	}
	child := map[string]json.RawMessage{}
	if raw, ok := doc[parts[0]]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &child); err != nil {
			return fmt.Errorf("patch_requires_object")
		}
	}
	if err := putPointer(child, parts[1:], value, remove); err != nil {
		return err
	}
	doc[parts[0]] = json.RawMessage(encode(child))
	return nil
}

// decodePointerValue turns a present/absent RawMessage into a JSON value for
// conflict payloads (absent → null).
func decodePointerValue(v json.RawMessage, ok bool) any {
	if !ok {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(v, &decoded); err != nil {
		return nil
	}
	return decoded
}

// fieldConflictSuggestions picks resolution hints for one divergent pointer.
// Vocabulary: manual_merge (inspect both), take_current (accept server),
// rebase_keep_mine (re-checkout then re-apply proposed).
func fieldConflictSuggestions(kind string) []string {
	switch kind {
	case "stale_base":
		return []string{"take_current", "rebase_keep_mine"}
	default:
		return []string{"manual_merge", "take_current", "rebase_keep_mine"}
	}
}

func mergeCommitPatch(base, current map[string]json.RawMessage, patch []CommitPatch) (map[string]json.RawMessage, []FieldConflict, bool, error) {
	out := map[string]json.RawMessage{}
	for k, v := range current {
		out[k] = v
	}
	conflicts := []FieldConflict{}
	changed := false
	for _, p := range patch {
		parts, _ := pointerParts(p.Path)
		b, bok, err := pointerValue(base, parts)
		if err != nil {
			return nil, nil, false, err
		}
		c, cok, err := pointerValue(current, parts)
		if err != nil {
			return nil, nil, false, err
		}
		proposedOK := !p.Remove
		if equalCommitValue(c, cok, p.Value, proposedOK) {
			continue
		}
		if !equalCommitValue(b, bok, c, cok) {
			kind := "concurrent_edit"
			if equalCommitValue(b, bok, p.Value, proposedOK) {
				kind = "stale_base"
			}
			conflicts = append(conflicts, FieldConflict{
				Pointer:     p.Path,
				Base:        decodePointerValue(b, bok),
				Current:     decodePointerValue(c, cok),
				Proposed:    decodePointerValue(p.Value, proposedOK),
				Remove:      p.Remove,
				Kind:        kind,
				Suggestions: fieldConflictSuggestions(kind),
			})
			continue
		}
		if err := putPointer(out, parts, p.Value, p.Remove); err != nil {
			return nil, nil, false, err
		}
		changed = true
	}
	return out, conflicts, changed, nil
}

func commitIdentityQuery(e Entity) IdentityCandidateQuery {
	q := IdentityCandidateQuery{Kind: e.Kind, Titles: []string{e.Title}}
	for _, translation := range e.Translations {
		if translation.Title != "" {
			q.Titles = append(q.Titles, translation.Title)
		}
	}
	for _, link := range e.ExternalLinks {
		value := link.ExternalID
		if value == "" {
			value = link.URL
		}
		if value == "" || link.Kind == "" {
			continue
		}
		q.ExternalLinks = append(q.ExternalLinks, IdentityExternalCriterion{Kind: link.Kind, Value: value})
	}
	switch e.Kind {
	case "expression", "content_unit":
		q.WorkID = e.WorkID
	case "medium":
		q.ReleaseID = e.ReleaseID
	case "track":
		q.MediumID = e.MediumID
	}
	return q
}
