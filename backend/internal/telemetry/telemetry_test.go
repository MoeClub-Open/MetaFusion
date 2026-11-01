package telemetry

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTrafficIsAggregatedWithoutRecordIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	collector := &Collector{pending: make(map[key]counters)}
	r := gin.New()
	r.Use(collector.Middleware())
	r.GET("/api/items/:id", func(c *gin.Context) { c.Status(200) })
	for _, path := range []string{"/api/items/one?secret=hidden", "/api/items/two", "/api/unknown/one", "/api/unknown/two", "/health"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	if len(collector.pending) != 2 {
		t.Fatalf("expected two aggregate buckets, got %v", collector.pending)
	}
	for k, v := range collector.pending {
		if (k.Route != "/api/items/:id" && k.Route != "<unmatched>") || v.Count != 2 {
			t.Fatalf("unexpected aggregate: %+v %+v", k, v)
		}
	}
}
