package catalog

import (
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

type catalogAssetReferenceCheck struct {
	WorkspaceID string `json:"workspace_id"`
	EntityID    string `json:"entity_id"`
	AssetID     string `json:"asset_id"`
	Referenced  bool   `json:"referenced"`
	Committed   bool   `json:"committed"`
}

// This read-only endpoint is private to storage GC. Confirming private
// workspace absence with a public user's filtered view would lose bytes.
func (h HTTP) registerStorageAssetReferences(r *gin.Engine) {
	secret := strings.TrimSpace(os.Getenv("STORAGE_CATALOG_LIFECYCLE_SECRET"))
	if secret == "" {
		return
	}
	r.POST("/internal/storage/asset-references", func(c *gin.Context) {
		want := sha256.Sum256([]byte("Bearer " + secret))
		got := sha256.Sum256([]byte(c.GetHeader("Authorization")))
		if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			c.JSON(401, gin.H{"error": "invalid_service_credential"})
			return
		}
		var in struct {
			References []catalogAssetReferenceCheck `json:"references"`
		}
		if !body(c, &in) {
			return
		}
		if len(in.References) == 0 || len(in.References) > 1000 {
			c.JSON(400, gin.H{"error": "invalid_payload"})
			return
		}
		for _, ref := range in.References {
			if !collaborationID(ref.WorkspaceID) || !collaborationID(ref.EntityID) || !collaborationID(ref.AssetID) || ref.Referenced || ref.Committed {
				c.JSON(400, gin.H{"error": "invalid_payload"})
				return
			}
		}
		rows, err := h.Store.DB.QueryContext(c.Request.Context(), `WITH input AS (
		 SELECT payload,n FROM jsonb_array_elements($1::jsonb) WITH ORDINALITY i(payload,n)
		), matches AS (SELECT payload,n,
		 EXISTS(SELECT 1 FROM catalog.workspace_objects o WHERE o.workspace_id=(payload->>'workspace_id')::uuid AND o.target='entity' AND o.target_id=(payload->>'entity_id')::uuid
		 AND (COALESCE(o.document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id'))
		 OR COALESCE(o.base_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id'))
		 OR COALESCE(o.head_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id')))) AS live,
		 EXISTS(SELECT 1 FROM catalog.commit_objects o JOIN catalog.commits co ON co.id=o.commit_id WHERE co.workspace_id=(payload->>'workspace_id')::uuid AND o.target='entity' AND o.target_id=(payload->>'entity_id')::uuid
		 AND (COALESCE(o.base_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id'))
		 OR COALESCE(o.before_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id'))
		 OR COALESCE(o.after_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',payload->>'asset_id')))) AS committed
		 FROM input) SELECT payload->>'workspace_id',payload->>'entity_id',payload->>'asset_id',live OR committed,committed FROM matches ORDER BY n`, encode(in.References))
		if err != nil {
			respond(c, nil, err)
			return
		}
		defer rows.Close()
		out := []catalogAssetReferenceCheck{}
		for rows.Next() {
			var ref catalogAssetReferenceCheck
			if err = rows.Scan(&ref.WorkspaceID, &ref.EntityID, &ref.AssetID, &ref.Referenced, &ref.Committed); err != nil {
				respond(c, nil, err)
				return
			}
			out = append(out, ref)
		}
		if err = rows.Err(); err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(200, gin.H{"references": out})
	})
}
