package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Called only after the frozen record has passed visibility checks. Sharing a
// snapshot grants retention of its exact entity/asset pair, never a new target.
func versionAssetSources(ctx context.Context, q queryer, raw json.RawMessage) (context.Context, error) {
	e, assets, err := workspaceCoverAssets(raw)
	if err != nil {
		return ctx, err
	}
	sources := map[string]string{}
	for asset := range assets {
		wid, err := versionAssetHolder(ctx, q, e.ID, asset)
		if err != nil && err != sql.ErrNoRows {
			return ctx, err
		}
		if err == nil {
			sources[asset] = wid
		}
	}
	return context.WithValue(ctx, versionAssetSourcesKey{}, sources), nil
}

func versionAssetHolder(ctx context.Context, q queryer, entity, asset string) (string, error) {
	var wid string
	err := q.QueryRowContext(ctx, `SELECT wc.workspace_id::text FROM catalog.commit_snapshots o JOIN catalog.workspace_commits wc ON wc.commit_id=o.commit_id
 WHERE o.target='entity' AND o.target_id=$1 AND (COALESCE(o.document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',$2::text)) OR COALESCE(o.base_document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',$2::text)))
 ORDER BY wc.workspace_id LIMIT 1`, entity, asset).Scan(&wid)
	return wid, err
}

func (s *Store) catalogVersionAssetPreview(ctx context.Context, id, asset string, c CollaborationCommit, u User) (catalogAssetPreviewReference, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	defer tx.Rollback()
	// Changed before/after records have already been filtered by commit access.
	for _, co := range c.Changes {
		if co.Target != "entity" {
			continue
		}
		if _, ok := previewReferenceInDocuments("", co.ID, asset, co.Before, co.After); ok {
			wid, err := versionAssetHolder(ctx, tx, co.ID, asset)
			if err == nil || err == sql.ErrNoRows {
				// A baseline may predate workspace holds. Storage can still
				// authorize an existing cover binding for this exact target.
				if wid == "" {
					wid = id
				}
				return catalogAssetPreviewReference{WorkspaceID: wid, EntityID: co.ID, AssetID: asset}, tx.Commit()
			}
			if err != sql.ErrNoRows {
				return catalogAssetPreviewReference{}, err
			}
		}
	}
	ids, err := collaborationIDs(ctx, tx, `WITH latest AS(SELECT DISTINCT ON(target_id) target_id,document FROM catalog.catalog_record_versions WHERE target='entity' AND sequence<=(SELECT sequence FROM catalog.catalog_versions WHERE commit_id=$1) ORDER BY target_id,sequence DESC)
 SELECT target_id::text FROM latest WHERE COALESCE(document->'pictures','[]'::jsonb) @> jsonb_build_array(jsonb_build_object('asset_id',$2::text)) ORDER BY target_id`, id, asset)
	if err != nil {
		return catalogAssetPreviewReference{}, err
	}
	for _, entity := range ids {
		objects, err := catalogVersionObjects(ctx, tx, id, []WorkspaceObject{{Target: "entity", ID: entity}}, u)
		if err != nil {
			return catalogAssetPreviewReference{}, err
		}
		if len(objects) == 0 {
			continue
		}
		wid, err := versionAssetHolder(ctx, tx, entity, asset)
		if err == nil || err == sql.ErrNoRows {
			if wid == "" {
				wid = id
			}
			return catalogAssetPreviewReference{WorkspaceID: wid, EntityID: entity, AssetID: asset}, tx.Commit()
		}
		if err != sql.ErrNoRows {
			return catalogAssetPreviewReference{}, err
		}
	}
	return catalogAssetPreviewReference{}, sql.ErrNoRows
}
