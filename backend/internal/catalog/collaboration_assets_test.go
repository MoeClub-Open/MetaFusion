package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestWorkspaceCoverSaveReservesPrivatelyWithAuthenticatedActor(t *testing.T) {
	asset, eid, wid, uid := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	e := Entity{ID: eid, Kind: "work", Pictures: PicturesJSON{{AssetID: asset, URL: "/api/storage/assets/" + asset + "/content"}}}
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/internal/catalog/assets/prepare" || r.Header.Get("Authorization") != "Bearer test-assets-secret" {
			t.Errorf("wrong private storage endpoint or credential")
		}
		var in storageAssetReferences
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.WorkspaceID != wid || in.Prepare || in.Retain || len(in.References) != 1 || in.References[0].EntityID != eid || in.References[0].AssetID != asset || encode(in.References[0].ActorIDs) != encode([]string{uid}) || in.References[0].ModerateActorID != "" {
			t.Errorf("untrusted or public reservation: %+v", in)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("STORAGE_URL", server.URL)
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "test-assets-secret")
	s := &Store{}
	raw := json.RawMessage(encode(e))
	if err := s.reserveWorkspaceCoverAssets(context.Background(), wid, nil, raw, User{ID: uid}); err != nil {
		t.Fatal(err)
	}
	// Even an unchanged cover must retain its exact private workspace scope.
	if err := s.reserveWorkspaceCoverAssets(context.Background(), wid, raw, raw, User{ID: uid}); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatalf("reservations=%d", called)
	}
	e.Pictures = nil
	if err := s.reserveWorkspaceCoverAssets(context.Background(), wid, nil, json.RawMessage(encode(e)), User{ID: uid}); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatal("an ordinary edit contacted storage")
	}
}

func TestCatalogAssetReferenceConfirmationRequiresServiceCredentialAndStrictBody(t *testing.T) {
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "test-assets-secret")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(HTTP{}).registerStorageAssetReferences(r)
	for _, tc := range []struct {
		auth, payload string
		want          int
	}{
		{"", `{}`, 401}, {"Bearer user-session", `{}`, 401}, {"Bearer test-assets-secret", `{"references":[]}`, 400},
		{"Bearer test-assets-secret", `{"references":[{"workspace_id":"invalid"}]}`, 400},
		{"Bearer test-assets-secret", `{"references":[]} {}`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, "/internal/storage/asset-references", strings.NewReader(tc.payload))
		req.Header.Set("Authorization", tc.auth)
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		if out.Code != tc.want {
			t.Fatalf("status=%d want=%d body=%s", out.Code, tc.want, out.Body.String())
		}
	}
}

func TestWorkspaceCanonicalAssetURLRequiresExplicitAssetIDForNewPictures(t *testing.T) {
	id := uuid.NewString()
	canonical := "/api/storage/assets/" + id + "/content"
	encodePictures := func(pictures ...Picture) json.RawMessage {
		return json.RawMessage(encode(Entity{Kind: "work", Pictures: pictures}))
	}
	legacy := encodePictures(Picture{URL: canonical})
	for _, tc := range []struct {
		name          string
		before, after json.RawMessage
		wantErr       bool
	}{
		{"new_relative", nil, legacy, true},
		{"new_relative_with_query", nil, encodePictures(Picture{URL: canonical + "?preview=1"}), true},
		{"relative_route_redirect", nil, encodePictures(Picture{URL: canonical + "/"}), true},
		{"relative_uppercase_uuid", nil, encodePictures(Picture{URL: "/api/storage/assets/" + strings.ToUpper(id) + "/content"}), true},
		{"unchanged_legacy", legacy, legacy, false},
		{"new_missing_id_next_to_legacy", legacy, encodePictures(Picture{URL: canonical}, Picture{URL: "/api/storage/assets/" + uuid.NewString() + "/content"}), true},
		{"cannot_drop_asset_id", encodePictures(Picture{URL: canonical, AssetID: id}), legacy, true},
		{"new_valid_asset_id", nil, encodePictures(Picture{URL: canonical, AssetID: id}), false},
		{"external_absolute_similar_path", nil, encodePictures(Picture{URL: "https://external.example" + canonical}), false},
		{"external_protocol_relative", nil, encodePictures(Picture{URL: "//external.example" + canonical}), false},
		{"external_image", nil, encodePictures(Picture{URL: "https://external.example/cover.png"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorkspacePictureAssetIDs(tc.after, tc.before)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validation=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
	// The actual save and merge hooks must reject before making a storage call.
	s := &Store{}
	if err := s.reserveWorkspaceCoverAssets(context.Background(), uuid.NewString(), nil, legacy, User{ID: uuid.NewString()}); err == nil || err.Error() != "invalid_picture_asset" {
		t.Fatalf("save silently accepted an unmanaged self-hosted cover: %v", err)
	}
	if err := s.validateWorkspaceCoverAssets(context.Background(), nil, []workspaceMergeObject{{Object: WorkspaceObject{Target: "entity", ID: uuid.NewString()}, Merged: legacy}}, nil, User{}); err == nil || err.Error() != "invalid_picture_asset" {
		t.Fatalf("merge silently accepted an unmanaged self-hosted cover: %v", err)
	}
}

func TestCatalogAssetConfirmationProtectsWorkspaceAndCommitHistoryOnPostgres(t *testing.T) {
	f := newFixture(t)
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "test-assets-secret")
	e := f.save(Entity{Kind: "work", Title: "Private retained history"})
	w := testWorkspace(t, f, f.u, e)
	base, head, current, missing := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx := context.Background()
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.workspace_objects SET base_document=jsonb_set(base_document,'{pictures}',$3::jsonb),head_document=jsonb_set(head_document,'{pictures}',$4::jsonb),document=jsonb_set(document,'{pictures}',$5::jsonb) WHERE workspace_id=$1 AND target_id=$2`, w.ID, e.ID, encode(PicturesJSON{{AssetID: base}}), encode(PicturesJSON{{AssetID: head}}), encode(PicturesJSON{{AssetID: current}})); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	(HTTP{Store: f.s}).registerStorageAssetReferences(r)
	check := func() []catalogAssetReferenceCheck {
		t.Helper()
		refs := []catalogAssetReferenceCheck{}
		for _, id := range []string{base, head, current, missing} {
			refs = append(refs, catalogAssetReferenceCheck{WorkspaceID: w.ID, EntityID: e.ID, AssetID: id})
		}
		refs = append(refs, catalogAssetReferenceCheck{WorkspaceID: uuid.NewString(), EntityID: e.ID, AssetID: current}, catalogAssetReferenceCheck{WorkspaceID: w.ID, EntityID: uuid.NewString(), AssetID: current})
		req := httptest.NewRequest(http.MethodPost, "/internal/storage/asset-references", strings.NewReader(encode(map[string]any{"references": refs})))
		req.Header.Set("Authorization", "Bearer test-assets-secret")
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		if out.Code != 200 {
			t.Fatalf("confirmation: %d %s", out.Code, out.Body.String())
		}
		var result struct {
			References []catalogAssetReferenceCheck `json:"references"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.References) != 6 {
			t.Fatalf("confirmation result count=%d", len(result.References))
		}
		return result.References
	}
	for i, ref := range check() {
		if ref.Referenced != (i < 3) || ref.Committed {
			t.Fatalf("workspace exact reference %d: %+v", i, ref)
		}
	}
	w, err := f.s.GetWorkspace(ctx, w.ID, f.u)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = testWorkspaceCommit(t, f, w, f.u, "Retain covers in immutable history")
	if _, err = f.s.DB.ExecContext(ctx, `UPDATE catalog.workspace_objects SET base_document=jsonb_set(base_document,'{pictures}','[]'),head_document=jsonb_set(head_document,'{pictures}','[]'),document=jsonb_set(document,'{pictures}','[]') WHERE workspace_id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	for i, ref := range check() {
		if ref.Referenced != (i < 3) || ref.Committed != (i < 3) {
			t.Fatalf("immutable exact reference %d: %+v", i, ref)
		}
	}
}

func TestPostgresWorkspaceRejectsUntrackedNewSelfHostedCoverAndKeepsLegacy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	e := f.save(Entity{Kind: "work", Title: "Before image"})
	w := testWorkspace(t, f, f.u, e)
	url := "/api/storage/assets/" + uuid.NewString() + "/content"
	before := factCounts(t, f.s)
	_, err := f.s.SaveWorkspaceChanges(ctx, w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: []WorkspaceOperation{workspaceUpdate(e, CommitPatch{Path: "/pictures", Value: json.RawMessage(encode(PicturesJSON{{URL: url}}))})}}, f.u)
	if err == nil || err.Error() != "invalid_picture_asset" {
		t.Fatalf("new unmanaged internal picture accepted: %v", err)
	}
	after, err := f.s.GetWorkspace(ctx, w.ID, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != w.Version || after.DirtyCount != 0 || factCounts(t, f.s) != before {
		t.Fatal("failed picture save changed workspace or public facts")
	}
	// Existing raw values remain editable; this boundary does not rewrite or
	// invent an ownership grant for historical pictures missing asset metadata.
	legacy := f.save(Entity{Kind: "work", Title: "Legacy image", Pictures: PicturesJSON{{URL: url}}})
	copy := testWorkspace(t, f, f.u, legacy)
	saved := testWorkspaceSave(t, f, copy, f.u, workspaceUpdate(legacy, CommitPatch{Path: "/title", Value: json.RawMessage(encode("Edited legacy title"))}))
	copy, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Edit existing text")
	if _, err = f.s.PushWorkspace(ctx, copy.ID, pushWorkspaceInput(copy), f.u); err != nil {
		t.Fatal(err)
	}
	got, err := get(ctx, f.s.DB, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Edited legacy title" || len(got.Pictures) != 1 || got.Pictures[0].URL != url || got.Pictures[0].AssetID != "" {
		t.Fatalf("historical picture was rewritten: %+v", got.Pictures)
	}
}

func TestPostgresWorkspaceSelfHostedCoverStagesCommitsAndPushes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	assetID := uuid.NewString()
	url := "/api/storage/assets/" + assetID + "/content"
	var stages, prepares atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/catalog/assets/prepare" || r.Header.Get("Authorization") != "Bearer cover-e2e-test-secret" {
			t.Error("unexpected storage request")
			w.WriteHeader(403)
			return
		}
		var in storageAssetReferences
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(in.References) != 1 || in.References[0].AssetID != assetID || len(in.References[0].ActorIDs) != 1 || in.References[0].ActorIDs[0] != f.u.ID {
			t.Errorf("incorrect asset scope or original author: %+v", in)
			w.WriteHeader(400)
			return
		}
		if in.Prepare {
			if stages.Load() == 0 || !in.Retain {
				t.Error("prepared without authenticated stage")
			}
			prepares.Add(1)
		} else {
			stages.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("STORAGE_URL", server.URL)
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "cover-e2e-test-secret")
	e := f.save(Entity{Kind: "work", Title: "Self-hosted cover"})
	copy := testWorkspace(t, f, f.u, e)
	pictures := PicturesJSON{{URL: url, AssetID: assetID}}
	saved := testWorkspaceSave(t, f, copy, f.u, workspaceUpdate(e, CommitPatch{Path: "/pictures", Value: json.RawMessage(encode(pictures))}))
	if current, err := get(ctx, f.s.DB, e.ID); err != nil || len(current.Pictures) != 0 {
		t.Fatalf("stage changed formal cover: %+v %v", current.Pictures, err)
	}
	copy, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Add uploaded cover")
	if current, err := get(ctx, f.s.DB, e.ID); err != nil || len(current.Pictures) != 0 {
		t.Fatalf("commit changed formal cover: %+v %v", current.Pictures, err)
	}
	if _, err := f.s.PushWorkspace(ctx, copy.ID, pushWorkspaceInput(copy), f.u); err != nil {
		t.Fatal(err)
	}
	formal, err := get(ctx, f.s.DB, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(formal.Pictures) != 1 || formal.Pictures[0].URL != url || formal.Pictures[0].AssetID != assetID || stages.Load() < 1 || prepares.Load() < 1 {
		t.Fatalf("cover did not pass full stage/commit/push: %+v stages=%d prepares=%d", formal.Pictures, stages.Load(), prepares.Load())
	}
	snapshot, err := f.s.currentStorageLifecycle(ctx, e.ID)
	if err != nil || !contains(snapshot.CoverAssetIDs, assetID) {
		t.Fatalf("merged asset missing from lifecycle facts: %+v %v", snapshot, err)
	}
}
