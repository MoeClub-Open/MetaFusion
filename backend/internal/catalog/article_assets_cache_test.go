package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestArticleBodyStorageAssetsLifecycleAndValidation(t *testing.T) {
	entityID := uuid.NewString()
	picAsset := uuid.NewString()
	bodyAsset1 := uuid.NewString()
	bodyAsset2 := uuid.NewString()
	bodyAsset3 := uuid.NewString()
	bodyAsset4 := uuid.NewString()
	bodyAsset5 := uuid.NewString()
	bodyAsset6 := uuid.NewString()

	doc := Entity{
		ID:    entityID,
		Kind:  "work",
		Title: "Inline Asset Work",
		Pictures: PicturesJSON{
			{AssetID: picAsset, URL: "/api/storage/assets/" + picAsset + "/content"},
		},
		Translations: map[string]Translation{
			"zh-CN": {
				Title: "正文图片",
				Body: "Intro\n\n![图一 [内页] A](/api/storage/assets/" + bodyAsset1 + "/content)![紧邻图](/api/storage/assets/" + bodyAsset5 + "/content)\n" +
					"![图二](</api/storage/assets/" + bodyAsset2 + "/content> \"Title\")\n" +
					"[Regular link](/api/storage/assets/" + uuid.NewString() + "/content)\n" +
					"[ref-img-1]: /api/storage/assets/" + bodyAsset3 + "/content\n" +
					"[ref-img-2]: </api/storage/assets/" + bodyAsset4 + "/content>\r\n[ref-img-3]: /api/storage/assets/" + bodyAsset6 + "/content",
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspacePictureAssetIDs(raw); err != nil {
		t.Fatalf("canonical inline body assets rejected: %v", err)
	}
	_, assets, err := workspaceCoverAssets(raw)
	if err != nil {
		t.Fatalf("workspaceCoverAssets failed: %v", err)
	}
	for _, want := range []string{picAsset, bodyAsset1, bodyAsset2, bodyAsset3, bodyAsset4, bodyAsset5, bodyAsset6} {
		if !assets[want] {
			t.Fatalf("missing expected asset %s in %v", want, assets)
		}
	}
	if len(assets) != 7 {
		t.Fatalf("plain non-image link should not become an asset reference: %v", assets)
	}
	if ref, ok := previewReferenceInDocuments("ws-1", entityID, bodyAsset2, raw); !ok || ref.AssetID != bodyAsset2 {
		t.Fatalf("previewReferenceInDocuments did not match inline body asset: ok=%v ref=%+v", ok, ref)
	}

	// Newly introduced malformed /api/storage/assets/... image URL must be rejected unless preserved from baseline.
	bad := doc
	bad.Translations = map[string]Translation{
		"zh-CN": {Title: "坏地址", Body: "![bad](/api/storage/assets/" + bodyAsset1 + "/content?raw=1)"},
	}
	badRaw, _ := json.Marshal(bad)
	if err := validateWorkspacePictureAssetIDs(badRaw); err == nil || err.Error() != "invalid_picture_asset" {
		t.Fatalf("expected invalid_picture_asset for malformed inline storage image, got %v", err)
	}
	if err := validateWorkspacePictureAssetIDs(badRaw, badRaw); err != nil {
		t.Fatalf("unchanged legacy baseline URL should remain readable: %v", err)
	}
}

func TestRespondCachedETagAndViewerIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("Origin") != "" {
			c.Header("Vary", "Origin")
		}
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		if uid := c.GetHeader("X-Test-User"); uid != "" {
			c.Set("catalog_user", &User{ID: uid, Username: uid, Permissions: []string{PermissionEntityEdit}})
		}
		c.Next()
	})
	r.GET("/pub", func(c *gin.Context) {
		respondCached(c, gin.H{"etag": "v1"}, nil, true)
	})
	r.GET("/priv", func(c *gin.Context) {
		respondCached(c, gin.H{"id": "e1"}, nil, false)
	})

	do := func(method, path, userID, ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if userID != "" {
			req.Header.Set("X-Test-User", userID)
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	wPub := do(http.MethodGet, "/pub", "", "")
	pubETag := wPub.Header().Get("ETag")
	if wPub.Code != 200 || pubETag == "" || wPub.Header().Get("Cache-Control") != "public, no-cache" {
		t.Fatalf("unexpected public cached headers: code=%d etag=%q cc=%q", wPub.Code, pubETag, wPub.Header().Get("Cache-Control"))
	}
	if w304 := do(http.MethodGet, "/pub", "", `W/`+pubETag+`, "other"`); w304.Code != http.StatusNotModified || w304.Body.Len() != 0 {
		t.Fatalf("expected 304 on weak If-None-Match list, got %d len=%d", w304.Code, w304.Body.Len())
	}

	wAlice := do(http.MethodGet, "/priv", "alice", "")
	aliceETag := wAlice.Header().Get("ETag")
	if wAlice.Code != 200 || aliceETag == "" || wAlice.Header().Get("Cache-Control") != "private, no-cache" || wAlice.Header().Get("Vary") != "Cookie, Authorization" {
		t.Fatalf("unexpected private cached headers: code=%d etag=%q cc=%q vary=%q", wAlice.Code, aliceETag, wAlice.Header().Get("Cache-Control"), wAlice.Header().Get("Vary"))
	}
	if wAlice304 := do(http.MethodGet, "/priv", "alice", aliceETag); wAlice304.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for same viewer ETag, got %d", wAlice304.Code)
	}
	wBob := do(http.MethodGet, "/priv", "bob", aliceETag)
	if wBob.Code != http.StatusOK || wBob.Header().Get("ETag") == aliceETag {
		t.Fatalf("viewer cache scope must isolate Alice and Bob ETags: code=%d bobETag=%q aliceETag=%q", wBob.Code, wBob.Header().Get("ETag"), aliceETag)
	}

	reqOrigin := httptest.NewRequest(http.MethodGet, "/priv", nil)
	reqOrigin.Header.Set("Origin", "https://findverse.cc")
	reqOrigin.Header.Set("X-Test-User", "alice")
	wOrigin := httptest.NewRecorder()
	r.ServeHTTP(wOrigin, reqOrigin)
	if gotVary := wOrigin.Header().Get("Vary"); gotVary != "Origin, Cookie, Authorization" {
		t.Fatalf("expected Vary to preserve Origin, got %q", gotVary)
	}
}
