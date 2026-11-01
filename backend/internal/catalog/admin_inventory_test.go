package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func adminInventoryEngine(s *Store, u *User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u != nil {
			c.Set("catalog_user", u)
		}
		c.Next()
	})
	HTTP{Store: s}.registerAdminInventory(r.Group("/api"))
	return r
}

func adminInventoryRequest(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestAdminInventoryPermissionAndValidation(t *testing.T) {
	manager := &User{ID: uuid.NewString(), Permissions: []string{PermissionLifecycleManage}}
	for _, path := range []string{"/api/admin/catalog-entities"} {
		for _, tc := range []struct {
			name   string
			user   *User
			status int
		}{
			{"anonymous", nil, 401},
			{"editor", &User{ID: uuid.NewString(), Permissions: []string{PermissionEntityEdit}}, 403},
			{"oauth", &User{ID: uuid.NewString(), IsThirdParty: true, Permissions: []string{PermissionLifecycleManage}}, 403},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				got := adminInventoryRequest(adminInventoryEngine(&Store{}, tc.user), path)
				if got.Code != tc.status {
					t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
				}
			})
		}
		// An unavailable database is a failure, never a successful empty list,
		// and its diagnostics do not become public error text.
		got := adminInventoryRequest(adminInventoryEngine(&Store{}, manager), path)
		if got.Code != 500 || got.Body.String() != `{"error":"internal_error"}` {
			t.Fatalf("database failure: %d %s", got.Code, got.Body.String())
		}
	}
	for _, path := range []string{
		"/api/admin/catalog-entities?state=draft",
		"/api/admin/catalog-entities?kind=person",
		"/api/admin/catalog-entities?limit=101",
		"/api/admin/catalog-entities?offset=-1",
		"/api/admin/catalog-entities?q=%00",
	} {
		got := adminInventoryRequest(adminInventoryEngine(&Store{}, manager), path)
		if got.Code != 400 {
			t.Errorf("%s: %d %s", path, got.Code, got.Body.String())
		}
	}
}

func TestPostgresAdminEntityInventoryAllStatesAndFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	active := f.save(Entity{Kind: "agent", Title: "inventory needle active", Translations: map[string]Translation{"ja": {Title: "管理一覧の別名"}}})
	deleted := f.retire(f.save(Entity{Kind: "agent", Title: "inventory needle deleted"}))
	merged := f.save(Entity{Kind: "agent", Title: "inventory needle merged"})
	var err error
	merged, err = f.s.Lifecycle(ctx, merged.ID, LifecycleEdit{TargetID: active.ID, ExpectedVersion: merged.Version, EditNote: "inventory merge", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	f.save(Entity{Kind: "work", Title: "inventory needle work"})
	// A base title without localized rows is legal; JSON null translations must
	// not make unrelated inventory searches fail in jsonb_each.
	if _, err = f.s.Save(ctx, Edit{Entity: Entity{Kind: "agent", Title: "base title without localized rows"}, EditNote: "inventory base-title fixture", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	// Inventory state/ownership must follow the columns even when the stored
	// document is stale. It must not silently use the public active-only list.
	owner := uuid.NewString()
	if _, err = f.s.DB.Exec(`UPDATE catalog.entities SET created_by=$2,document=jsonb_set(document,'{state}','"active"'::jsonb) WHERE id=$1`, deleted.ID, owner); err != nil {
		t.Fatal(err)
	}
	engine := adminInventoryEngine(f.s, &f.u)
	read := func(query string) AdminEntityInventory {
		t.Helper()
		response := adminInventoryRequest(engine, "/api/admin/catalog-entities?"+query)
		var result AdminEntityInventory
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
		return result
	}
	all := read("q=inventory%20needle&kind=agent&limit=1")
	next := read("q=inventory%20needle&kind=agent&limit=1&offset=1")
	last := read("q=inventory%20needle&kind=agent&limit=1&offset=2")
	if all.Total != 3 || next.Total != 3 || last.Total != 3 || len(all.Items) != 1 || len(next.Items) != 1 || len(last.Items) != 1 || !all.HasMore || !next.HasMore || last.HasMore || all.Items[0].ID == next.Items[0].ID || all.Limit != 1 || next.Offset != 1 {
		t.Fatalf("incomplete pagination: %+v %+v %+v", all, next, last)
	}
	for _, entity := range []Entity{active, deleted, merged} {
		result := read("q=inventory%20needle&kind=agent&state=" + entity.State)
		if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != entity.ID || result.Items[0].State != entity.State || result.HasMore {
			t.Fatalf("state=%s: %+v", entity.State, result)
		}
		if entity.ID == deleted.ID && result.Items[0].CreatedBy != owner {
			t.Fatal("inventory returned stale ownership")
		}
	}
	translated := read("q=%E7%AE%A1%E7%90%86%E4%B8%80%E8%A6%A7")
	byID := read("q=" + active.ID)
	if translated.Total != 1 || translated.Items[0].ID != active.ID || byID.Total != 1 || byID.Items[0].ID != active.ID {
		t.Fatalf("translated/UUID search missing: %+v %+v", translated, byID)
	}
	literal := read("q=%25")
	if literal.Total != 0 {
		t.Fatalf("LIKE metacharacter became wildcard: %+v", literal)
	}
	public, err := f.s.List(ctx, ListOptions{Kind: "agent", Limit: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range public {
		if entity.State != "active" || entity.ID == deleted.ID || entity.ID == merged.ID {
			t.Fatalf("admin inventory changed public list: %+v", entity)
		}
	}
}

func TestAdminInventoryOpenAPI(t *testing.T) {
	paths := adminInventoryPaths()
	for _, path := range []string{"/admin/catalog-entities"} {
		item, ok := paths[path].(map[string]any)
		if !ok || item["get"] == nil {
			t.Fatalf("missing inventory contract: %s", path)
		}
		if len(item) != 1 {
			t.Fatalf("inventory must remain read-only: %s", path)
		}
	}
}
