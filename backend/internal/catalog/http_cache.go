package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func viewerCacheScope(u *User) string {
	if u == nil || u.ID == "" {
		return "anon"
	}
	perms := append([]string(nil), u.Permissions...)
	sort.Strings(perms)
	return u.ID + "|" + strconv.FormatBool(u.FromPAT) + "|" + strconv.FormatBool(u.IsThirdParty) + "|" + strconv.FormatBool(u.ForcePullRequest) + "|" + strings.Join(perms, ",")
}

func appendVary(c *gin.Context, tokens ...string) {
	existing := c.Writer.Header().Values("Vary")
	seen := make(map[string]bool, len(existing)+len(tokens))
	merged := make([]string, 0, len(existing)+len(tokens))
	add := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			tok := strings.TrimSpace(part)
			if tok == "" {
				continue
			}
			key := strings.ToLower(tok)
			if !seen[key] {
				seen[key] = true
				merged = append(merged, tok)
			}
		}
	}
	for _, v := range existing {
		add(v)
	}
	for _, t := range tokens {
		add(t)
	}
	if len(merged) > 0 {
		c.Header("Vary", strings.Join(merged, ", "))
	}
}

func ifNoneMatchHit(values []string, etag string) bool {
	target := strings.TrimPrefix(strings.TrimPrefix(etag, "W/"), "w/")
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			cand := strings.TrimSpace(part)
			if cand == "" {
				continue
			}
			if cand == "*" {
				return true
			}
			cand = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(cand, "W/"), "w/"))
			if cand == target {
				return true
			}
		}
	}
	return false
}

func respondCached(c *gin.Context, v any, err error, public bool) {
	if err != nil {
		respond(c, nil, err)
		return
	}
	raw, mErr := json.Marshal(v)
	if mErr != nil {
		respond(c, nil, mErr)
		return
	}
	scope := "public"
	if public {
		c.Header("Cache-Control", "public, no-cache")
	} else {
		scope = viewerCacheScope(user(c))
		c.Header("Cache-Control", "private, no-cache")
		appendVary(c, "Cookie", "Authorization")
	}
	h := sha256.New()
	_, _ = h.Write([]byte(scope))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(raw)
	etag := `"` + hex.EncodeToString(h.Sum(nil)) + `"`
	c.Header("ETag", etag)
	if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && ifNoneMatchHit(c.Request.Header.Values("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}
