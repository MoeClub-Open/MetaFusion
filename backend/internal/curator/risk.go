package curator

import (
	"encoding/json"
	"reflect"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

func autoMergeClass(code string) string {
	switch code {
	case CodeDuplicateTagValues:
		return "tags"
	case CodeDuplicatePictureURLs:
		return "pictures"
	}
	return ""
}

// Only exact deduplication of the finding's own record can bypass review.
func lowRiskPR(pr *catalog.PullRequest, finding Finding, expected string) (bool, string) {
	if pr == nil {
		return false, "missing_pull_request"
	}
	if len(pr.Changes) != 1 {
		return false, "repair_must_change_one_record"
	}
	object := pr.Changes[0]
	if object.Target != "entity" {
		return false, "relation_change"
	}
	if object.ID == "" || rawNull(object.Base) || rawNull(object.Committed) {
		return false, "create_or_delete"
	}
	if object.ID != finding.EntityID {
		return false, "outside_finding_scope"
	}
	var base, next catalog.Entity
	if json.Unmarshal(object.Base, &base) != nil || json.Unmarshal(object.Committed, &next) != nil {
		return false, "invalid_document"
	}
	if base.ID != object.ID || next.ID != object.ID {
		return false, "object_identity_mismatch"
	}
	if !sameEntityOther(base, next) {
		return false, "non_whitelisted_field"
	}
	switch expected {
	case "tags":
		if tagsOnlyDedupe(base, next) {
			return true, "low_risk"
		}
	case "pictures":
		if picturesOnlyDedupe(base, next) {
			return true, "low_risk"
		}
	}
	return false, "not_exact_deduplication"
}

func sameEntityOther(a, b catalog.Entity) bool {
	return a.Kind == b.Kind && a.Title == b.Title && a.OriginalLanguage == b.OriginalLanguage &&
		a.State == b.State && a.ReviewStatus == b.ReviewStatus && a.RedirectID == b.RedirectID && a.CreatedBy == b.CreatedBy &&
		jsonEqual(a.Translations, b.Translations) && jsonEqual(a.ExternalLinks, b.ExternalLinks) &&
		a.WorkID == b.WorkID && a.ContentUnitID == b.ContentUnitID && a.ReleaseID == b.ReleaseID &&
		a.MediumID == b.MediumID && a.ParentID == b.ParentID && a.Number == b.Number &&
		jsonEqual(a.Contents, b.Contents) && jsonEqual(a.Subjects, b.Subjects)
}

func tagsOnlyDedupe(base, next catalog.Entity) bool {
	otherA := copyAttrs(base.Attributes)
	otherB := copyAttrs(next.Attributes)
	delete(otherA, "tags")
	delete(otherB, "tags")
	if !jsonEqual(otherA, otherB) || !jsonEqual(base.Pictures, next.Pictures) || base.Position != next.Position {
		return false
	}
	oldTags := stringSlice(base.Attributes["tags"])
	newTags := stringSlice(next.Attributes["tags"])
	if equalStringSlices(oldTags, newTags) {
		return false
	}
	return len(newTags) < len(oldTags) && equalStringSlices(stableDedupe(oldTags), newTags)
}

func picturesOnlyDedupe(base, next catalog.Entity) bool {
	if !jsonEqual(base.Attributes, next.Attributes) || base.Position != next.Position {
		return false
	}
	oldPics := make([]string, len(base.Pictures))
	for i := range base.Pictures {
		raw, _ := json.Marshal(base.Pictures[i])
		oldPics[i] = string(raw)
	}
	newPics := make([]string, len(next.Pictures))
	for i := range next.Pictures {
		raw, _ := json.Marshal(next.Pictures[i])
		newPics[i] = string(raw)
	}
	if equalStringSlices(oldPics, newPics) {
		return false
	}
	return len(newPics) < len(oldPics) && equalStringSlices(stableDedupe(oldPics), newPics)
}

func copyAttrs(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stableDedupe(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeJSON(a), normalizeJSON(b))
}

func normalizeJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return normalizeJSONValue(out)
}

func normalizeJSONValue(v any) any {
	switch value := v.(type) {
	case []any:
		if len(value) == 0 {
			return nil
		}
		for i := range value {
			value[i] = normalizeJSONValue(value[i])
		}
		return value
	case map[string]any:
		if len(value) == 0 {
			return nil
		}
		for k := range value {
			value[k] = normalizeJSONValue(value[k])
		}
		return value
	default:
		return value
	}
}

func rawNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
