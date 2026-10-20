package catalog

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func previewPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCatalogAssetPreviewUsesOnlyServiceCredentialAndFixedRoute(t *testing.T) {
	raw := previewPNG(t)
	ref := catalogAssetPreviewReference{WorkspaceID: uuid.NewString(), EntityID: uuid.NewString(), AssetID: uuid.NewString()}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/internal/catalog/workspaces/" + ref.WorkspaceID + "/entities/" + ref.EntityID + "/assets/" + ref.AssetID + "/content"
		if r.URL.RequestURI() != want || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer service-secret" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected private image request: method=%s uri=%s", r.Method, r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(raw)
	}))
	defer upstream.Close()
	client, err := newStorageLifecycleClient(upstream.URL, "service-secret")
	if err != nil {
		t.Fatal(err)
	}
	got, mime, status := client.assetPreview(context.Background(), ref)
	if status != 200 || mime != "image/png" || !bytes.Equal(got, raw) {
		t.Fatalf("private image body changed: status=%d mime=%s length=%d", status, mime, len(got))
	}
}

func TestCatalogAssetPreviewRejectsRedirectsAndNonImages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
		want        int
	}{
		{"redirect", 302, "image/png", "", 503},
		{"missing", 404, "application/json", `{\"secret\":true}`, 404},
		{"credential", 401, "application/json", `{\"secret\":true}`, 503},
		{"html", 200, "text/html", "<html>private upstream data</html>", 503},
		{"svg", 200, "image/svg+xml", "<svg/>", 503},
		{"empty", 200, "image/png", "", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirected := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/steal" {
					redirected = true
				}
				w.Header().Set("Location", "/steal")
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			client, err := newStorageLifecycleClient(upstream.URL, "service-secret")
			if err != nil {
				t.Fatal(err)
			}
			got, mime, status := client.assetPreview(context.Background(), catalogAssetPreviewReference{WorkspaceID: uuid.NewString(), EntityID: uuid.NewString(), AssetID: uuid.NewString()})
			if status != tc.want || len(got) != 0 || mime != "" || redirected {
				t.Fatalf("unsafe response accepted: status=%d length=%d mime=%s redirected=%v", status, len(got), mime, redirected)
			}
		})
	}
}

func TestPostgresCatalogAssetPreviewKeepsFrozenReviewAndMemberBoundaries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := reviewEditor()
	member := fixtureUser("member")
	outsider := fixtureUser("outsider")
	reviewer := User{ID: uuid.NewString(), Permissions: []string{PermissionReviewManage}}
	asset, dirtyAsset, unknownAsset := uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw := previewPNG(t)
	requests := 0
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer asset-preview-test" || r.Header.Get("Cookie") != "" {
			t.Error("browser credential crossed service boundary")
		}
		if r.URL.Path == "/internal/catalog/assets/prepare" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		requests++
		w.Header().Set("Content-Type", "image/png")
		w.Write(raw)
	}))
	defer storage.Close()
	t.Setenv("STORAGE_URL", storage.URL)
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "asset-preview-test")
	work := f.save(Entity{Kind: "work", Title: "Private proposed cover"})
	w := testWorkspace(t, f, owner, work)
	w, err := f.s.SetWorkspaceMembers(ctx, w.ID, WorkspaceMembersEdit{ExpectedVersion: w.Version, UserIDs: []string{member.ID}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	pictures := func(id string) []Picture {
		return []Picture{{AssetID: id, URL: "/api/storage/assets/" + id + "/content"}}
	}
	saved := testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/pictures", pictures(asset))))
	w, commit := testWorkspaceCommit(t, f, saved.Workspace, owner, "Propose cover")
	push, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), owner)
	if err != nil || push.PullRequest == nil {
		t.Fatalf("push: %+v %v", push, err)
	}
	pr := *push.PullRequest
	if push.Workspace != nil {
		w = *push.Workspace
	} else {
		w, err = f.s.GetWorkspace(ctx, w.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	saved = testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/pictures", pictures(dirtyAsset))))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	actors := map[string]*User{"owner": &owner, "member": &member, "outsider": &outsider, "reviewer": &reviewer}
	router.Use(func(c *gin.Context) {
		if actor := actors[c.GetHeader("X-Test-Actor")]; actor != nil {
			c.Set("catalog_user", actor)
		}
		c.Next()
	})
	(HTTP{Store: f.s}).registerAssetPreviewRoutes(router.Group("/api/catalog"))
	check := func(actor, scope, id, image string, status int) {
		t.Helper()
		before := requests
		req := httptest.NewRequest("GET", "/api/catalog/"+scope+"/"+id+"/assets/"+image+"/content", nil)
		req.Header.Set("X-Test-Actor", actor)
		req.Header.Set("Cookie", "platform-session=private")
		req.Header.Set("Authorization", "Bearer browser-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("%s %s image=%s: status=%d body=%s", actor, scope, image, rec.Code, rec.Body.String())
		}
		if status == 200 {
			if !bytes.Equal(rec.Body.Bytes(), raw) || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatal("real private image was not returned")
			}
		} else if requests != before {
			t.Fatal("unauthorized/nonreferenced image reached storage")
		}
	}
	check("owner", "workspaces", w.ID, dirtyAsset, 200)
	check("member", "workspaces", w.ID, dirtyAsset, 200)
	check("outsider", "workspaces", w.ID, dirtyAsset, 404)
	check("reviewer", "workspaces", w.ID, dirtyAsset, 404)
	check("owner", "workspaces", w.ID, unknownAsset, 404)
	check("reviewer", "pull-requests", pr.ID, asset, 200)
	check("reviewer", "pull-requests", pr.ID, dirtyAsset, 404)
	check("outsider", "pull-requests", pr.ID, asset, 404)
	check("", "pull-requests", pr.ID, asset, 404)
	check("reviewer", "commits", commit.ID, asset, 200)
	check("reviewer", "commits", commit.ID, dirtyAsset, 404)
	check("outsider", "commits", commit.ID, asset, 404)
	// A later committed image still cannot enter the already frozen review.
	_, later := testWorkspaceCommit(t, f, saved.Workspace, owner, "Later private cover")
	check("reviewer", "pull-requests", pr.ID, dirtyAsset, 404)
	check("reviewer", "commits", later.ID, dirtyAsset, 404)
	var official Entity
	if official, err = f.s.Get(ctx, work.ID, nil); err != nil || len(official.Pictures) != 0 {
		t.Fatalf("preview changed official cover: %+v %v", official.Pictures, err)
	}
}

func TestPostgresCatalogVersionPreviewUsesExactBaselineRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	asset := uuid.NewString()
	e := f.save(Entity{Kind: "work", Title: "Existing cover", Pictures: []Picture{{AssetID: asset, URL: "/api/storage/assets/" + asset + "/content"}}})
	head, err := f.s.CatalogVersionHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := f.s.commitAssetPreview(ctx, head.CommitID, asset, User{})
	if err != nil || ref.EntityID != e.ID || ref.AssetID != asset || ref.WorkspaceID != head.CommitID {
		t.Fatalf("baseline preview context: %+v %v", ref, err)
	}
	if _, err = f.s.commitAssetPreview(ctx, head.CommitID, uuid.NewString(), User{}); err == nil {
		t.Fatal("unreferenced asset was accepted")
	}
}
