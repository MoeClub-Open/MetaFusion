// Package publicweb provides an HTTP client for untrusted public endpoints.
// It validates DNS at connection time and dials only the validated address.
package publicweb

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"), netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}

func isPublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range deniedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func parseURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("public_url_required")
	}
	if strings.Contains(u.Hostname(), "%") || strings.ContainsAny(u.Hostname(), "\\\r\n\t") {
		return nil, fmt.Errorf("public_url_required")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid_port")
		}
	}
	u.Fragment = ""
	return u, nil
}

func publicAddresses(ctx context.Context, host string, r resolver) ([]net.IPAddr, error) {
	var addrs []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addrs = []net.IPAddr{{IP: ip}}
	} else {
		var err error
		addrs, err = r.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return nil, fmt.Errorf("public_host_unresolved")
		}
	}
	for _, addr := range addrs {
		if addr.Zone != "" || !isPublicIP(addr.IP) {
			return nil, fmt.Errorf("public_address_required")
		}
	}
	return addrs, nil
}

// ValidateURL accepts only HTTP(S) URLs whose current DNS addresses are public.
// NewClient independently checks DNS when it connects; callers must not replace
// its Transport with a default transport after validation.
func ValidateURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := parseURL(rawURL)
	if err != nil {
		return nil, err
	}
	if _, err := publicAddresses(ctx, u.Hostname(), net.DefaultResolver); err != nil {
		return nil, err
	}
	return u, nil
}

type publicTransport struct {
	transport *http.Transport
}

func (t *publicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := parseURL(req.URL.String())
	if err != nil {
		return nil, err
	}
	if req.Host != "" && !strings.EqualFold(req.Host, u.Host) {
		return nil, fmt.Errorf("host_override_forbidden")
	}
	return t.transport.RoundTrip(req)
}

func newClient(timeout time.Duration, r resolver, dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if dial == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid_address")
		}
		addrs, err := publicAddresses(ctx, host, r)
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			var conn net.Conn
			conn, err = dial(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, fmt.Errorf("public_connection_failed")
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &publicTransport{transport: transport},
		// Never forward Authorization or a request body to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NewClient returns a client with no proxy, cookie jar or automatic redirects.
// The caller owns request headers and response-size limits.
func NewClient(timeout time.Duration) *http.Client {
	return newClient(timeout, net.DefaultResolver, nil)
}
