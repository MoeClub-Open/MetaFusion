package publicweb

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticResolver []net.IPAddr

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) { return r, nil }

func TestPublicAddressPolicy(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "172.16.0.1", "192.168.1.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "0.0.0.0", "224.0.0.1", "240.0.0.1", "::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "2001:2::1", "2001:20::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if isPublicIP(net.ParseIP(raw)) {
			t.Errorf("accepted non-public IP %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Errorf("rejected public IP %s", raw)
		}
	}
}

func TestPublicClientRejectsMixedDNSBeforeDial(t *testing.T) {
	var dialed atomic.Bool
	client := newClient(time.Second, staticResolver{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, func(context.Context, string, string) (net.Conn, error) {
		dialed.Store(true)
		return nil, fmt.Errorf("unexpected dial")
	})
	_, err := client.Get("http://public.example/test")
	if err == nil || !strings.Contains(err.Error(), "public_address_required") || dialed.Load() {
		t.Fatalf("mixed DNS was not rejected before dial: err=%v dialed=%v", err, dialed.Load())
	}
}

func TestPublicClientPinsIPAndDoesNotFollowRedirect(t *testing.T) {
	var count atomic.Int32
	var actualAddress string
	client := newClient(time.Second, staticResolver{{IP: net.ParseIP("8.8.8.8")}}, func(_ context.Context, _, address string) (net.Conn, error) {
		count.Add(1)
		actualAddress = address
		local, remote := net.Pipe()
		go func() {
			defer remote.Close()
			req, err := http.ReadRequest(bufio.NewReader(remote))
			if err != nil {
				return
			}
			if req.Host != "llm.example" || req.Header.Get("Authorization") != "Bearer only-original-origin" {
				t.Errorf("request authority or key changed: host=%s", req.Host)
			}
			fmt.Fprint(remote, "HTTP/1.1 307 Temporary Redirect\r\nLocation: http://other.example/leak\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}()
		return local, nil
	})
	req, _ := http.NewRequest(http.MethodPost, "http://llm.example/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer only-original-origin")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 307 || count.Load() != 1 || actualAddress != "8.8.8.8:80" {
		t.Fatalf("connection was not pinned or redirect followed: status=%d count=%d address=%s", resp.StatusCode, count.Load(), actualAddress)
	}
}

func TestPublicClientRejectsAuthorityAndUnsafeURLs(t *testing.T) {
	client := NewClient(time.Second)
	for _, raw := range []string{"http://127.0.0.1/", "http://[::1]/", "http://user:secret@8.8.8.8/", "ftp://8.8.8.8/", "file:///etc/passwd", "http://[fe80::1%25eth0]/"} {
		if _, err := client.Get(raw); err == nil {
			t.Errorf("accepted unsafe URL %s", raw)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "http://8.8.8.8/", nil)
	req.Host = "internal.local"
	if _, err := client.Do(req); err == nil || !strings.Contains(err.Error(), "host_override_forbidden") {
		t.Fatalf("host override accepted: %v", err)
	}
	transport := client.Transport.(*publicTransport).transport
	if transport.Proxy != nil || client.Jar != nil {
		t.Fatal("public client inherited a proxy or cookie jar")
	}
}
