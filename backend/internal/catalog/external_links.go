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

// linkFromID is the Entity-side replacement for a former external_ids map entry.
func linkFromID(kind, value string) ExternalLink {
	kind = strings.TrimSpace(kind)
	value = strings.TrimSpace(value)
	link := ExternalLink{Kind: kind, URL: externalLinkURL(kind, value, "")}
	if !(strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://")) {
		link.ExternalID = value
	}
	return link
}

func linksFromIDMap(ids map[string]string) []ExternalLink {
	if len(ids) == 0 {
		return nil
	}
	out := make([]ExternalLink, 0, len(ids))
	for k, v := range ids {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out = append(out, linkFromID(k, v))
	}
	return out
}
