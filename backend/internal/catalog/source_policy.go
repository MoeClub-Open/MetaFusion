package catalog

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"

	"golang.org/x/net/idna"
)

type sourcePolicy struct {
	blockedHosts []string
}

var configuredSourcePolicy atomic.Pointer[sourcePolicy]
var defaultSourcePolicy = sourcePolicy{blockedHosts: []string{"findverse.cc"}}

// ConfigureSourcePolicy configures the instance's public hostname and optional
// comma-separated additional hosts that cannot serve as independent evidence.
// An empty public URL uses the documented default https://findverse.cc.
// Configure once before serving requests; failed configuration changes nothing.
func ConfigureSourcePolicy(publicURL, blockedHosts string) error {
	policy, err := newSourcePolicy(publicURL, blockedHosts)
	if err != nil {
		return err
	}
	configuredSourcePolicy.Store(policy)
	return nil
}

func newSourcePolicy(publicURL, blockedHosts string) (*sourcePolicy, error) {
	publicURL = strings.TrimSpace(publicURL)
	if publicURL == "" {
		publicURL = "https://findverse.cc"
	}
	if !validURL(publicURL) {
		return nil, fmt.Errorf("invalid_catalog_public_url")
	}
	u, _ := url.Parse(publicURL)
	host, err := sourceHost(u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("invalid_catalog_public_url")
	}
	policy := &sourcePolicy{blockedHosts: []string{host}}
	for _, raw := range strings.Split(blockedHosts, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse("https://" + raw)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid_catalog_source_blocked_hosts")
		}
		host, err := sourceHost(u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("invalid_catalog_source_blocked_hosts")
		}
		if !contains(policy.blockedHosts, host) {
			policy.blockedHosts = append(policy.blockedHosts, host)
		}
	}
	return policy, nil
}

func sourceHost(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(raw), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	host, err := idna.Lookup.ToASCII(host)
	if err != nil || host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", fmt.Errorf("invalid_source_host")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid_source_host")
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return "", fmt.Errorf("invalid_source_host")
			}
		}
	}
	return host, nil
}

func (p *sourcePolicy) eligible(source Source) bool {
	if source.Kind == "self" {
		return false
	}
	if source.URL == "" {
		return true
	}
	u, err := url.Parse(source.URL)
	if err != nil {
		return false
	}
	host, err := sourceHost(u.Hostname())
	if err != nil {
		return false
	}
	for _, blocked := range p.blockedHosts {
		if host == blocked || net.ParseIP(blocked) == nil && strings.HasSuffix(host, "."+blocked) {
			return false
		}
	}
	return true
}

func validateSourcesWithPolicy(note string, sources []Source, policy *sourcePolicy) error {
	if err := validateSourceShapes(note, sources); err != nil {
		return err
	}
	for _, source := range sources {
		if !policy.eligible(source) {
			return fmt.Errorf("source_not_eligible")
		}
	}
	return nil
}

// validateChangedEntitySources preserves unchanged historical evidence without
// allowing new or changed picture/inclusion facts to introduce self evidence.
// History and definitions impact checks validate shape separately; they do not
// rewrite saved sources or retrospectively enforce today's source policy.
func validateChangedEntitySources(next, previous Entity) error {
	oldPictures := make(map[string]bool, len(previous.Pictures))
	for _, picture := range previous.Pictures {
		oldPictures[pictureEvidenceFact(picture)] = true
	}
	for _, picture := range next.Pictures {
		if !oldPictures[pictureEvidenceFact(picture)] {
			if err := validateSources("picture", []Source{picture.Source}); err != nil {
				return err
			}
		}
	}
	oldInclusions := make(map[string]bool, len(previous.Contents))
	for _, inclusion := range previous.Contents {
		oldInclusions[inclusionEvidenceFact(inclusion)] = true
	}
	for _, inclusion := range next.Contents {
		if !oldInclusions[inclusionEvidenceFact(inclusion)] {
			if err := validateSources("inclusion", inclusion.Sources); err != nil {
				return err
			}
		}
	}
	return nil
}

// Use JSON fact equality rather than Go slice/map allocation identity. A commit
// round trip omits empty optional sources/attributes and may decode them as nil;
// that must not turn unchanged legacy evidence into a new assertion. Indexing
// the canonical facts also keeps this guard linear for long track directories.
func pictureEvidenceFact(picture Picture) string {
	if len(picture.Caption) == 0 {
		picture.Caption = nil
	}
	return encode(picture)
}

func inclusionEvidenceFact(inclusion Inclusion) string {
	if len(inclusion.Locator) == 0 {
		inclusion.Locator = nil
	}
	return encode(inclusion)
}
