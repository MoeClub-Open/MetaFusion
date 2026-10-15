package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Both export and batch identity are read boundaries: unavailable storage and
// cancelled queries must not masquerade as missing entities or partial success.
// A closed DB and an already-cancelled context fail before any network dial.
func TestExchangeAndIdentityQueryFailuresAreServerErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []string{"closed database", "cancelled query"} {
		t.Run(failure, func(t *testing.T) {
			// Exercise read-handler failures after the budget gate. Shared budget
			// failures have their own 503 regression coverage.
			storeRateLimitSnapshot(RateLimitConfig{Policy: RateLimitPolicy{DefaultUnlimited: true}}, nil)
			t.Cleanup(InvalidateRateLimitPolicyCache)
			db, err := sql.Open("postgres", "postgres://127.0.0.1:1/nope?sslmode=disable")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			requestContext := context.Background()
			if failure == "closed database" {
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				var cancel context.CancelFunc
				requestContext, cancel = context.WithCancel(requestContext)
				cancel()
			}
			engine := gin.New()
			HTTP{Store: &Store{DB: db}}.Register(engine)
			for _, route := range []struct{ method, path, payload string }{
				{http.MethodGet, "/api/exchange/entities/22222222-2222-2222-2222-222222222222", ""},
				{http.MethodPost, "/api/catalog/entities/identity", `{"ids":["22222222-2222-2222-2222-222222222222"]}`},
				{http.MethodGet, "/api/catalog/entities/22222222-2222-2222-2222-222222222222/revisions", ""},
			} {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.payload)).WithContext(requestContext)
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, req)
				var result map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusInternalServerError || string(result["error"]) != `"internal_error"` ||
					len(result) != 1 || leakPattern.MatchString(response.Body.String()) {
					t.Fatalf("%s must return a generic server failure: %d %s", route.path, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestPostgresIdentityBatchDoesNotReturnPartialSuccessOnReadFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	ctx := context.Background()
	public := f.save(Entity{Kind: "work", Title: "Visible identity"})
	hidden := f.save(Entity{Kind: "work", Title: "Private identity", Status: "active"})
	hidden = f.retire(hidden)
	broken := f.save(Entity{Kind: "work", Title: "Unreadable identity"})
	engine := gin.New()
	HTTP{Store: f.s}.Register(engine)
	request := func(ids []string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"ids": ids})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
			"/api/catalog/entities/identity", strings.NewReader(string(payload))))
		return response
	}
	missing := "22222222-2222-2222-2222-222222222222"
	response := request([]string{public.ID, hidden.ID, missing})
	var result struct {
		Items   map[string]IdentityResolution `json:"items"`
		Missing []string                      `json:"missing"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK ||
		len(result.Items) != 1 || result.Items[public.ID].CanonicalID != public.ID || !sameIDSet(result.Missing, []string{hidden.ID, missing}) {
		t.Fatalf("only missing/invisible identities should be absent: %d %s, %v", response.Code, response.Body.String(), err)
	}
	// The second lookup now fails decoding stored data, after the first lookup
	// succeeded. No successful items or missing array may escape that failure.
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.entities SET document=jsonb_set(document,'{title}','123'::jsonb) WHERE id=$1`, broken.ID); err != nil {
		t.Fatal(err)
	}
	response = request([]string{public.ID, broken.ID})
	var failed map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &failed); err != nil || response.Code != http.StatusInternalServerError ||
		len(failed) != 1 || string(failed["error"]) != `"internal_error"` || leakPattern.MatchString(response.Body.String()) {
		t.Fatalf("a mid-batch read failure must abort all items: %d %s, %v", response.Code, response.Body.String(), err)
	}
}

func TestPostgresRelationRevisionEndpointReadFailuresAreNotMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"source", "target"} {
		t.Run(endpoint, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			source := f.save(Entity{Kind: "work", Title: "Sequel"})
			target := f.save(Entity{Kind: "work", Title: "Original"})
			relation, err := f.s.SaveRelation(ctx, RelationEdit{
				Relation: Relation{Type: "sequel_of", SourceID: source.ID, TargetID: target.ID},
				EditNote: "relation revision read fixture", Sources: fixtureSources(),
			}, f.u)
			if err != nil {
				t.Fatal(err)
			}
			if revisions, err := f.s.Revisions(ctx, relation.ID, nil); err != nil || len(revisions) != 1 {
				t.Fatalf("visible relation history: %d revisions, %v", len(revisions), err)
			}
			brokenID := source.ID
			if endpoint == "target" {
				brokenID = target.ID
			}
			if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.entities SET document=jsonb_set(document,'{title}','123'::jsonb) WHERE id=$1`, brokenID); err != nil {
				t.Fatal(err)
			}
			engine := gin.New()
			HTTP{Store: f.s}.Register(engine)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
				"/api/catalog/entities/"+relation.ID+"/revisions", nil))
			if response.Code != http.StatusInternalServerError || response.Body.String() != `{"error":"internal_error"}` {
				t.Fatalf("%s read failure must not become missing: %d %s", endpoint, response.Code, response.Body.String())
			}
		})
	}
}
