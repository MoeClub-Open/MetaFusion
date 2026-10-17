package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/publicweb"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const maxPublicSourceBytes = 2 << 20

var publicSourceClient = publicweb.NewClient(20 * time.Second)
var publicSourceLimiter = newOutboundLimiter(2, 2)
var wikiLanguagePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
var wikiSiteIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

type PublicWikiSite struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APIURL      string `json:"api_url"`
	ArticleURL  string `json:"article_url"`
	Enabled     bool   `json:"enabled"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
}

type PublicSourceSettings struct {
	Enabled   bool             `json:"enabled"`
	WikiSites []PublicWikiSite `json:"wiki_sites"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type PublicSourceSave struct {
	Settings          PublicSourceSettings `json:"settings"`
	ExpectedUpdatedAt time.Time            `json:"expected_updated_at"`
}

type PublicFetchRequest struct {
	URL    string `json:"url"`
	Format string `json:"format,omitempty"`
}

type PublicWikiRequest struct {
	Site     string `json:"site"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Query    string `json:"query,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type PublicSourceResponse struct {
	URL         string    `json:"url"`
	FetchedAt   time.Time `json:"fetched_at"`
	ContentHash string    `json:"content_hash"`
	ContentType string    `json:"content_type"`
	Site        string    `json:"site,omitempty"`
	Title       string    `json:"title,omitempty"`
	Text        string    `json:"text,omitempty"`
	Data        any       `json:"data,omitempty"`
	Attribution string    `json:"attribution,omitempty"`
	License     string    `json:"license,omitempty"`
}

type publicSourceError struct {
	code   string
	status int
}

func (e *publicSourceError) Error() string { return e.code }

func defaultPublicSourceSettings() PublicSourceSettings {
	return PublicSourceSettings{Enabled: true, UpdatedAt: time.Unix(0, 0).UTC(), WikiSites: []PublicWikiSite{
		{ID: "wikipedia", Name: "Wikipedia", APIURL: "https://{language}.wikipedia.org/w/api.php", ArticleURL: "https://{language}.wikipedia.org/wiki/{title}", Enabled: true,
			License: "https://creativecommons.org/licenses/by-sa/4.0/", Attribution: "Wikipedia contributors; consult the source page for attribution and additional terms."},
		{ID: "moegirl", Name: "萌娘百科", APIURL: "https://zh.moegirl.org.cn/api.php", ArticleURL: "https://zh.moegirl.org.cn/{title}", Enabled: true,
			License: "https://zh.moegirl.org.cn/Moegirl:著作权信息", Attribution: "萌娘百科贡献者；使用前请查阅来源页面的著作权声明。"},
	}}
}

func (s *Store) PublicSourceSettings(ctx context.Context) (PublicSourceSettings, error) {
	settings := defaultPublicSourceSettings()
	var document []byte
	var updated time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT document,updated_at FROM assistant.public_sources WHERE singleton=true`).Scan(&document, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if string(document) != "{}" {
		if err := json.Unmarshal(document, &settings); err != nil {
			return settings, err
		}
	}
	if settings.WikiSites == nil {
		settings.WikiSites = []PublicWikiSite{}
	}
	settings.UpdatedAt = updated
	return settings, nil
}

func validatePublicSourceSettings(ctx context.Context, in PublicSourceSettings) error {
	if len(in.WikiSites) > 32 {
		return fmt.Errorf("too_many_wiki_sites")
	}
	seen := map[string]bool{}
	for _, site := range in.WikiSites {
		if !wikiSiteIDPattern.MatchString(site.ID) || seen[site.ID] || strings.TrimSpace(site.Name) == "" || len(site.Name) > 200 || len(site.License) > 2000 || len(site.Attribution) > 2000 {
			return fmt.Errorf("invalid_wiki_site")
		}
		seen[site.ID] = true
		for _, raw := range []string{site.APIURL, site.ArticleURL} {
			if len(raw) > 2000 {
				return fmt.Errorf("invalid_wiki_site_url")
			}
			sample := strings.ReplaceAll(strings.ReplaceAll(raw, "{language}", "en"), "{title}", "Example")
			if strings.ContainsAny(sample, "{}") {
				return fmt.Errorf("invalid_wiki_site_url")
			}
			if _, err := publicweb.ValidateURL(ctx, sample); err != nil {
				return fmt.Errorf("invalid_wiki_site_url")
			}
		}
	}
	return nil
}

func (s *Store) SavePublicSourceSettings(ctx context.Context, in PublicSourceSave, u User) (PublicSourceSettings, error) {
	if !u.Can(PermissionDefinitionsManage) {
		return PublicSourceSettings{}, errForbidden
	}
	if in.ExpectedUpdatedAt.IsZero() {
		return PublicSourceSettings{}, errVersionConflict
	}
	if in.Settings.WikiSites == nil {
		in.Settings.WikiSites = []PublicWikiSite{}
	}
	if err := validatePublicSourceSettings(ctx, in.Settings); err != nil {
		return PublicSourceSettings{}, err
	}
	in.Settings.UpdatedAt = time.Time{}
	document, err := json.Marshal(in.Settings)
	if err != nil {
		return PublicSourceSettings{}, err
	}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO assistant.public_sources(singleton,document,updated_at) VALUES(true,$1,now())
		ON CONFLICT(singleton) DO UPDATE SET document=EXCLUDED.document,updated_at=now()
		WHERE assistant.public_sources.updated_at=$2 RETURNING updated_at`, document, in.ExpectedUpdatedAt).Scan(&in.Settings.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicSourceSettings{}, errVersionConflict
	}
	return in.Settings, err
}

func publicSourceFailure(code string, status int) error {
	return &publicSourceError{code: code, status: status}
}

// publicGET follows a bounded number of GET redirects without forwarding any
// caller headers. The publicweb transport validates and pins every connection.
func publicGET(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, string, error) {
	for redirects := 0; redirects <= 3; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", "", publicSourceFailure("invalid_public_url", 400)
		}
		req.Header.Set("User-Agent", externalSourceUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, text/html;q=0.9, application/xml;q=0.8")
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", "", publicSourceFailure("public_source_unreachable", 502)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			resp.Body.Close()
			location, err := resp.Location()
			if err != nil || redirects == 3 {
				return nil, "", "", publicSourceFailure("public_source_redirect_limit", 502)
			}
			rawURL = location.String()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
				return nil, "", "", publicSourceFailure("public_source_access_denied", 502)
			}
			if resp.StatusCode == http.StatusNotFound {
				return nil, "", "", publicSourceFailure("public_source_not_found", 404)
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				return nil, "", "", publicSourceFailure("public_source_rate_limited", 429)
			}
			return nil, "", "", publicSourceFailure("public_source_upstream_error", 502)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicSourceBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, "", "", publicSourceFailure("public_source_unreadable", 502)
		}
		if len(body) > maxPublicSourceBytes {
			return nil, "", "", publicSourceFailure("public_source_too_large", 413)
		}
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(body)
		}
		return body, contentType, rawURL, nil
	}
	return nil, "", "", publicSourceFailure("public_source_redirect_limit", 502)
}

func readableHTML(body []byte) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	var parts []string
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
		case html.StartTagToken:
			name, _ := z.TagName()
			if string(name) == "script" || string(name) == "style" || string(name) == "noscript" {
				skip++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if skip > 0 && (string(name) == "script" || string(name) == "style" || string(name) == "noscript") {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				parts = append(parts, string(z.Text()))
			}
		}
	}
}

func fetchPublicSource(ctx context.Context, client *http.Client, in PublicFetchRequest) (PublicSourceResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if in.Format != "" && in.Format != "text" && in.Format != "json" {
		return PublicSourceResponse{}, fmt.Errorf("invalid_public_format")
	}
	u, urlErr := url.Parse(strings.TrimSpace(in.URL))
	if len(in.URL) > 8000 || urlErr != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return PublicSourceResponse{}, fmt.Errorf("invalid_public_url")
	}
	u.Fragment = ""
	body, contentType, fetchedURL, err := publicGET(ctx, client, u.String())
	if err != nil {
		return PublicSourceResponse{}, err
	}
	out := PublicSourceResponse{URL: fetchedURL, FetchedAt: time.Now().UTC(), ContentHash: contentHash(body), ContentType: contentType}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if in.Format == "json" || (in.Format == "" && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))) {
		if json.Unmarshal(body, &out.Data) != nil {
			return PublicSourceResponse{}, publicSourceFailure("public_source_invalid_json", 502)
		}
		return out, nil
	}
	if !strings.HasPrefix(mediaType, "text/") && mediaType != "application/json" && mediaType != "application/xml" && !strings.HasSuffix(mediaType, "+xml") && !strings.HasSuffix(mediaType, "+json") {
		return PublicSourceResponse{}, publicSourceFailure("public_source_content_type_unsupported", 415)
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return PublicSourceResponse{}, publicSourceFailure("public_source_unreadable", 502)
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxPublicSourceBytes+1))
	if err != nil || len(decoded) > maxPublicSourceBytes {
		return PublicSourceResponse{}, publicSourceFailure("public_source_too_large", 413)
	}
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		out.Text = readableHTML(decoded)
	} else {
		out.Text = string(decoded)
	}
	return out, nil
}

func (s *Store) FetchPublicSource(ctx context.Context, in PublicFetchRequest) (PublicSourceResponse, error) {
	settings, err := s.PublicSourceSettings(ctx)
	if err != nil {
		return PublicSourceResponse{}, err
	}
	if !settings.Enabled {
		return PublicSourceResponse{}, publicSourceFailure("public_sources_disabled", 503)
	}
	if err := publicSourceLimiter.Wait(ctx); err != nil {
		return PublicSourceResponse{}, err
	}
	return fetchPublicSource(ctx, publicSourceClient, in)
}

func wikiPublicSource(ctx context.Context, client *http.Client, settings PublicSourceSettings, in PublicWikiRequest) (PublicSourceResponse, error) {
	if !settings.Enabled {
		return PublicSourceResponse{}, publicSourceFailure("public_sources_disabled", 503)
	}
	in.Site, in.Title, in.Query = strings.TrimSpace(in.Site), strings.TrimSpace(in.Title), strings.TrimSpace(in.Query)
	if (in.Title == "") == (in.Query == "") || utf8.RuneCountInString(in.Title) > 500 || utf8.RuneCountInString(in.Query) > 500 || in.Limit < 0 || in.Limit > 20 {
		return PublicSourceResponse{}, fmt.Errorf("invalid_wiki_query")
	}
	if in.Language == "" {
		in.Language = "en"
	}
	if !wikiLanguagePattern.MatchString(in.Language) {
		return PublicSourceResponse{}, fmt.Errorf("invalid_wiki_language")
	}
	var site *PublicWikiSite
	for i := range settings.WikiSites {
		if settings.WikiSites[i].ID == in.Site {
			site = &settings.WikiSites[i]
			break
		}
	}
	if site == nil {
		return PublicSourceResponse{}, publicSourceFailure("wiki_site_not_found", 404)
	}
	if !site.Enabled {
		return PublicSourceResponse{}, publicSourceFailure("wiki_site_disabled", 503)
	}
	u, err := url.Parse(strings.ReplaceAll(site.APIURL, "{language}", in.Language))
	if err != nil {
		return PublicSourceResponse{}, fmt.Errorf("invalid_wiki_site_url")
	}
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	if in.Title != "" {
		q.Set("titles", in.Title)
		q.Set("prop", "info|revisions")
		q.Set("inprop", "url")
		q.Set("rvprop", "content|ids|timestamp")
		q.Set("rvslots", "main")
		q.Set("redirects", "1")
	} else {
		if in.Limit == 0 {
			in.Limit = 5
		}
		q.Set("list", "search")
		q.Set("srsearch", in.Query)
		q.Set("srlimit", strconv.Itoa(in.Limit))
		q.Set("srprop", "snippet|timestamp")
	}
	u.RawQuery = q.Encode()
	out, err := fetchPublicSource(ctx, client, PublicFetchRequest{URL: u.String(), Format: "json"})
	if err != nil {
		return PublicSourceResponse{}, err
	}
	root, ok := out.Data.(map[string]any)
	if !ok {
		return PublicSourceResponse{}, publicSourceFailure("wiki_invalid_response", 502)
	}
	if _, failed := root["error"]; failed {
		return PublicSourceResponse{}, publicSourceFailure("wiki_api_error", 502)
	}
	out.Site, out.Title, out.License, out.Attribution = site.ID, in.Title, site.License, site.Attribution
	query, queryOK := root["query"].(map[string]any)
	if !queryOK {
		return PublicSourceResponse{}, publicSourceFailure("wiki_invalid_response", 502)
	}
	if in.Title != "" {
		if pages, ok := query["pages"].([]any); ok && len(pages) > 0 {
			page, _ := pages[0].(map[string]any)
			if missing, _ := page["missing"].(bool); missing {
				return PublicSourceResponse{}, publicSourceFailure("wiki_page_not_found", 404)
			}
			if title, ok := page["title"].(string); ok {
				out.Title = title
			}
			if revisions, ok := page["revisions"].([]any); ok && len(revisions) > 0 {
				revision, _ := revisions[0].(map[string]any)
				slots, _ := revision["slots"].(map[string]any)
				main, _ := slots["main"].(map[string]any)
				out.Text, _ = main["content"].(string)
			}
		} else {
			return PublicSourceResponse{}, publicSourceFailure("wiki_page_not_found", 404)
		}
	}
	if out.Title != "" {
		articleURL := strings.ReplaceAll(strings.ReplaceAll(site.ArticleURL, "{language}", in.Language), "{title}", url.PathEscape(strings.ReplaceAll(out.Title, " ", "_")))
		out.Attribution = strings.TrimSpace(out.Attribution + " " + articleURL)
	}
	return out, nil
}

func (s *Store) QueryPublicWiki(ctx context.Context, in PublicWikiRequest) (PublicSourceResponse, error) {
	settings, err := s.PublicSourceSettings(ctx)
	if err != nil {
		return PublicSourceResponse{}, err
	}
	if err := publicSourceLimiter.Wait(ctx); err != nil {
		return PublicSourceResponse{}, err
	}
	return wikiPublicSource(ctx, publicSourceClient, settings, in)
}

func respondPublicSource(c *gin.Context, value any, err error) {
	var sourceErr *publicSourceError
	if errors.As(err, &sourceErr) {
		c.JSON(sourceErr.status, gin.H{"error": sourceErr.code})
		return
	}
	respond(c, value, err)
}

func (h HTTP) registerPublicSources(api *gin.RouterGroup) {
	read := api.Group("/external-sources", required(""))
	read.POST("/fetch", func(c *gin.Context) {
		var in PublicFetchRequest
		if !body(c, &in) {
			return
		}
		value, err := h.Store.FetchPublicSource(c.Request.Context(), in)
		respondPublicSource(c, value, err)
	})
	read.POST("/wiki", func(c *gin.Context) {
		var in PublicWikiRequest
		if !body(c, &in) {
			return
		}
		value, err := h.Store.QueryPublicWiki(c.Request.Context(), in)
		respondPublicSource(c, value, err)
	})
	admin := api.Group("/admin/public-sources", required(PermissionDefinitionsManage))
	admin.GET("", func(c *gin.Context) {
		value, err := h.Store.PublicSourceSettings(c.Request.Context())
		respond(c, value, err)
	})
	admin.PUT("", func(c *gin.Context) {
		var in PublicSourceSave
		if !body(c, &in) {
			return
		}
		value, err := h.Store.SavePublicSourceSettings(c.Request.Context(), in, *user(c))
		respond(c, value, err)
	})
}
