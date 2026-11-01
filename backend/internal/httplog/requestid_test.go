package httplog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDAcceptGenerate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
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

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	rid := w.Header().Get(Header)
	if rid == "" || rid != w.Body.String() {
		t.Fatalf("generate: header=%q body=%q", rid, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(Header, "bad\rid")
	r.ServeHTTP(w, req)
	if w.Header().Get(Header) == "bad\rid" || w.Header().Get(Header) == "" {
		t.Fatalf("unsafe header accepted: %q", w.Header().Get(Header))
	}
}
