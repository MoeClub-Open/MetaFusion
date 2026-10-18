package assistant

import (
	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/catalog"
	"testing"
)

func TestAssistantWriteRoutesAreAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}, Catalog: &catalog.Store{}}.Register(r.Group("/api"))
	actions := AuditActions()
	for _, route := range r.Routes() {
		// Context preview uses a body for the private draft, but performs no writes.
		if route.Method == "GET" || (route.Method == "POST" && route.Path == "/api/assistant/context") {
			continue
		}
		key := route.Method + " " + route.Path
		if _, ok := actions[key]; !ok {
			t.Errorf("unregistered write: %s", key)
		}
		delete(actions, key)
	}
	for key := range actions {
		t.Errorf("stale audit action: %s", key)
	}
}
