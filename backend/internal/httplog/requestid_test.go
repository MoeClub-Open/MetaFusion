package httplog

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDAcceptGenerateAndAccessLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := gin.New()
	r.Use(RequestID(), AccessLogger(&buf))
	r.GET("/ok", func(c *gin.Context) {
		c.String(200, FromContext(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(Header, "client-rid-001")
	r.ServeHTTP(w, req)
	if w.Header().Get(Header) != "client-rid-001" || w.Body.String() != "client-rid-001" {
		t.Fatalf("accept: %q body=%q", w.Header().Get(Header), w.Body.String())
	}
	if !strings.Contains(buf.String(), "client-rid-001") || !strings.Contains(buf.String(), "GET") {
		t.Fatalf("access log missing request_id: %q", buf.String())
	}

	buf.Reset()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	rid := w.Header().Get(Header)
	if rid == "" || rid != w.Body.String() || !strings.Contains(buf.String(), rid) {
		t.Fatalf("generate: header=%q body=%q log=%q", rid, w.Body.String(), buf.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(Header, "bad\rid")
	r.ServeHTTP(w, req)
	if w.Header().Get(Header) == "bad\rid" || w.Header().Get(Header) == "" {
		t.Fatalf("unsafe header accepted: %q", w.Header().Get(Header))
	}
}
