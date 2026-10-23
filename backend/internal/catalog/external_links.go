package catalog

import (
	"fmt"
	"net/url"
	"strings"
)

// externalLinkURL builds a stable URL for a kind+external_id pair when the
// caller does not already have an absolute URL (migration / import helpers).
func externalLinkURL(kind, externalID, pattern string) string {
	externalID = strings.TrimSpace(externalID)
	kind = strings.TrimSpace(kind)
	if strings.HasPrefix(strings.ToLower(externalID), "http://") || strings.HasPrefix(strings.ToLower(externalID), "https://") {
		return strings.TrimRight(externalID, "/")
	}
	if pattern != "" {
		return strings.ReplaceAll(pattern, "{id}", externalID)
	}
	return fmt.Sprintf("mf://external/%s/%s", kind, strings.ReplaceAll(url.PathEscape(externalID), "+", "%20"))
}

// previewLinks converts adapter preview identifiers into external_links objects.
func previewLinks(ids map[string]any) []map[string]any {
	if len(ids) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(ids))
	for k, v := range ids {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" || s == "<nil>" {
			continue
		}
		link := map[string]any{"kind": k, "url": externalLinkURL(k, s, "")}
		if !(strings.HasPrefix(strings.ToLower(s), "http://") || strings.HasPrefix(strings.ToLower(s), "https://")) {
			link["external_id"] = s
		}
		out = append(out, link)
	}
	return out
}
