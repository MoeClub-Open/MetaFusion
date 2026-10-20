package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
)

const catalogAssetPreviewLimit = 16 << 20

// Only catalog-verified references cross this boundary. The browser supplies
// neither the upstream URL nor a storage credential.
type catalogAssetPreviewReference struct {
	WorkspaceID string
	EntityID    string
	AssetID     string
}

func previewReferenceInDocuments(workspaceID, entityID, assetID string, documents ...json.RawMessage) (catalogAssetPreviewReference, bool) {
	for _, raw := range documents {
		var entity Entity
		if json.Unmarshal(raw, &entity) != nil || !collaborationID(entity.ID) || entity.ID != entityID {
			continue
		}
		for _, picture := range entity.Pictures {
			if picture.AssetID == assetID {
				return catalogAssetPreviewReference{WorkspaceID: workspaceID, EntityID: entity.ID, AssetID: assetID}, true
			}
		}
	}
	return catalogAssetPreviewReference{}, false
}

func (s *Store) workspaceAssetPreview(ctx context.Context, workspaceID, assetID string, u User) (catalogAssetPreviewReference, error) {
	if !collaborationID(workspaceID) || !collaborationID(assetID) {
		return catalogAssetPreviewReference{}, sql.ErrNoRows
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, workspaceID, u, false)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	if !allowed {
		return catalogAssetPreviewReference{}, sql.ErrNoRows
	}
	// Both the working tree and retained immutable commits are visible to its
	// members. A reviewer alone never receives this live-working-tree access.
	var entityID string
	err = tx.QueryRowContext(ctx, `SELECT target_id::text FROM (
		SELECT target_id,base_document AS document FROM catalog.workspace_objects WHERE workspace_id=$1 AND target='entity'
		UNION ALL SELECT target_id,head_document FROM catalog.workspace_objects WHERE workspace_id=$1 AND target='entity'
		UNION ALL SELECT target_id,document FROM catalog.workspace_objects WHERE workspace_id=$1 AND target='entity'
		UNION ALL SELECT o.target_id,v.document FROM catalog.commit_objects o JOIN catalog.workspace_commits c ON c.commit_id=o.commit_id
		CROSS JOIN LATERAL (VALUES(o.base_document),(o.before_document),(o.after_document)) v(document)
		WHERE c.workspace_id=$1 AND o.target='entity'
		UNION ALL SELECT o.target_id,v.document FROM catalog.commit_snapshots o JOIN catalog.workspace_commits c ON c.commit_id=o.commit_id CROSS JOIN LATERAL(VALUES(o.base_document),(o.document)) v(document) WHERE c.workspace_id=$1 AND o.target='entity'
	) refs WHERE COALESCE(document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',$2::text))
	ORDER BY target_id LIMIT 1`, workspaceID, assetID).Scan(&entityID)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	return catalogAssetPreviewReference{WorkspaceID: workspaceID, EntityID: entityID, AssetID: assetID}, tx.Commit()
}

func (s *Store) pullRequestAssetPreview(ctx context.Context, id, assetID string, u User) (catalogAssetPreviewReference, error) {
	if !collaborationID(id) || !collaborationID(assetID) {
		return catalogAssetPreviewReference{}, sql.ErrNoRows
	}
	p, err := s.GetPullRequest(ctx, id, u)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	// GetPullRequest resolves only the frozen source-head ancestry. In
	// particular, a later save/commit cannot smuggle an image into this review.
	for _, commit := range p.Commits {
		for _, change := range commit.Changes {
			if change.Target != "entity" {
				continue
			}
			if ref, ok := previewReferenceInDocuments(p.WorkspaceID, change.ID, assetID, change.Base, change.Before, change.After); ok {
				return ref, nil
			}
		}
	}
	return catalogAssetPreviewReference{}, sql.ErrNoRows
}

func (s *Store) commitAssetPreview(ctx context.Context, id, assetID string, u User) (catalogAssetPreviewReference, error) {
	if !collaborationID(id) || !collaborationID(assetID) {
		return catalogAssetPreviewReference{}, sql.ErrNoRows
	}
	commit, err := s.GetCollaborationCommit(ctx, id, u)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	if commit.WorkspaceID == nil {
		return s.catalogVersionAssetPreview(ctx, id, assetID, commit, u)
	}
	for _, change := range commit.Changes {
		if change.Target != "entity" {
			continue
		}
		if ref, ok := previewReferenceInDocuments(*commit.WorkspaceID, change.ID, assetID, change.Base, change.Before, change.After); ok {
			return ref, nil
		}
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	defer tx.Rollback()
	objects, _, err := readableVersionSnapshot(ctx, tx, id, u)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	for _, o := range objects {
		if o.Target == "entity" {
			if ref, ok := previewReferenceInDocuments(*commit.WorkspaceID, o.ID, assetID, o.Base, o.Document); ok {
				return ref, tx.Commit()
			}
		}
	}
	return catalogAssetPreviewReference{}, sql.ErrNoRows
}

func (c *storageLifecycleClient) assetPreview(ctx context.Context, ref catalogAssetPreviewReference) ([]byte, string, int) {
	u, err := url.Parse(c.endpoint)
	if err != nil || !collaborationID(ref.WorkspaceID) || !collaborationID(ref.EntityID) || !collaborationID(ref.AssetID) {
		return nil, "", http.StatusServiceUnavailable
	}
	u.Path = "/internal/catalog/workspaces/" + ref.WorkspaceID + "/entities/" + ref.EntityID + "/assets/" + ref.AssetID + "/content"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", http.StatusServiceUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/avif")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, "", http.StatusServiceUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, "", http.StatusNotFound
	}
	if res.StatusCode != http.StatusOK || res.ContentLength > catalogAssetPreviewLimit {
		return nil, "", http.StatusServiceUnavailable
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"}, contentType) {
		return nil, "", http.StatusServiceUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, catalogAssetPreviewLimit+1))
	if err != nil || len(raw) == 0 || len(raw) > catalogAssetPreviewLimit {
		return nil, "", http.StatusServiceUnavailable
	}
	// The response is embedded as an image, never interpreted as HTML or SVG.
	return raw, contentType, http.StatusOK
}

func (h HTTP) registerAssetPreviewRoutes(cat *gin.RouterGroup) {
	type lookup func(context.Context, string, string, User) (catalogAssetPreviewReference, error)
	handler := func(find lookup) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "private, no-store")
			c.Header("Vary", "Cookie, Authorization")
			c.Header("X-Content-Type-Options", "nosniff")
			ref, err := find(c.Request.Context(), c.Param("id"), c.Param("assetID"), collaborationViewer(c))
			if err != nil {
				respond(c, nil, err)
				return
			}
			client, err := catalogAssetClient()
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage_unavailable"})
				return
			}
			raw, contentType, status := client.assetPreview(c.Request.Context(), ref)
			if status != http.StatusOK {
				code := "storage_unavailable"
				if status == http.StatusNotFound {
					code = "not_found"
				}
				c.JSON(status, gin.H{"error": code})
				return
			}
			c.Header("Content-Length", strconv.Itoa(len(raw)))
			c.Header("Content-Disposition", "inline")
			c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
			c.Data(http.StatusOK, contentType, raw)
		}
	}
	cat.GET("/workspaces/:id/assets/:assetID/content", handler(h.Store.workspaceAssetPreview))
	cat.GET("/pull-requests/:id/assets/:assetID/content", handler(h.Store.pullRequestAssetPreview))
	cat.GET("/commits/:id/assets/:assetID/content", handler(h.Store.commitAssetPreview))
}
