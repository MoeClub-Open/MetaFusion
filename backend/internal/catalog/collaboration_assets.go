package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type storageAssetReference struct {
	EntityID        string   `json:"entity_id"`
	AssetID         string   `json:"asset_id"`
	ActorIDs        []string `json:"actor_ids"`
	ModerateActorID string   `json:"moderate_actor_id,omitempty"`
}
type storageAssetReferences struct {
	WorkspaceID string                  `json:"workspace_id"`
	Prepare     bool                    `json:"prepare"`
	Retain      bool                    `json:"retain"`
	References  []storageAssetReference `json:"references"`
}

// Only the canonical relative storage address identifies a self-hosted image.
// An external site's similar path does not grant access to this site's assets.
func relativeWorkspaceAssetURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" {
		return false
	}
	id, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/api/storage/assets/")
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, "/content")
	return ok && collaborationID(strings.ToLower(id))
}

// Unchanged historical pictures are readable evidence, not new asset grants.
// New canonical storage references must name their asset explicitly so every
// successful save participates in owner authorization, retention and binding.
func validateWorkspacePictureAssetIDs(after json.RawMessage, baselines ...json.RawMessage) error {
	legacy := map[string]bool{}
	for _, raw := range baselines {
		if rawNull(raw) {
			continue
		}
		var e Entity
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		for _, p := range e.Pictures {
			if p.AssetID == "" {
				legacy[p.URL] = true
			}
		}
	}
	if rawNull(after) {
		return nil
	}
	var e Entity
	if err := json.Unmarshal(after, &e); err != nil {
		return err
	}
	for _, p := range e.Pictures {
		if p.AssetID == "" && relativeWorkspaceAssetURL(p.URL) && !legacy[p.URL] {
			return fmt.Errorf("invalid_picture_asset")
		}
	}
	return nil
}

func workspaceCoverAssets(raw json.RawMessage) (Entity, map[string]bool, error) {
	var entity Entity
	assets := map[string]bool{}
	if rawNull(raw) {
		return entity, assets, nil
	}
	if err := json.Unmarshal(raw, &entity); err != nil {
		return entity, nil, err
	}
	for _, picture := range entity.Pictures {
		if picture.AssetID == "" {
			continue
		}
		if !collaborationID(picture.AssetID) {
			return entity, nil, fmt.Errorf("invalid_picture_asset")
		}
		u, err := url.Parse(picture.URL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/storage/assets/"+picture.AssetID+"/content" {
			return entity, nil, fmt.Errorf("picture_asset_url_mismatch")
		}
		assets[picture.AssetID] = true
	}
	return entity, assets, nil
}

func (c *storageLifecycleClient) retainAssets(ctx context.Context, in storageAssetReferences) error {
	if len(in.References) == 0 {
		return nil
	}
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return err
	}
	u.Path = "/internal/catalog/assets/prepare"
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("storage_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusConflict {
			return fmt.Errorf("invalid_picture_asset")
		}
		return fmt.Errorf("storage_unavailable")
	}
	var receipt struct {
		OK bool `json:"ok"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&receipt); err != nil || !receipt.OK {
		return fmt.Errorf("storage_unavailable")
	}
	return nil
}
func catalogAssetClient() (*storageLifecycleClient, error) {
	c, err := newStorageLifecycleClient(os.Getenv("STORAGE_URL"), os.Getenv("STORAGE_CATALOG_LIFECYCLE_SECRET"))
	if err != nil {
		return nil, fmt.Errorf("storage_unavailable")
	}
	return c, nil
}

// A save reserves new bytes privately. Failure of the enclosing catalog
// transaction leaves only a bounded 48-hour speculative hold in storage.
func (s *Store) reserveWorkspaceCoverAssets(ctx context.Context, workspaceID string, before, after json.RawMessage, u User) error {
	if err := validateWorkspacePictureAssetIDs(after, before); err != nil {
		return err
	}
	e, next, err := workspaceCoverAssets(after)
	if err != nil {
		return err
	}
	in := storageAssetReferences{WorkspaceID: workspaceID, References: []storageAssetReference{}}
	for _, id := range sortedMapKeys(next) {
		ref := storageAssetReference{EntityID: e.ID, AssetID: id, ActorIDs: []string{u.ID}}
		if u.Can("storage.asset.moderate") {
			ref.ModerateActorID = u.ID
		}
		in.References = append(in.References, ref)
	}
	if len(in.References) == 0 {
		return nil
	}
	c, err := catalogAssetClient()
	if err != nil {
		return err
	}
	return c.retainAssets(ctx, in)
}

// The merger prepares only net new cover references. Authors come exclusively
// from immutable server commits; the reviewer's identity never replaces them.
func (s *Store) validateWorkspaceCoverAssets(ctx context.Context, tx *sql.Tx, plan []workspaceMergeObject, commits []CollaborationCommit, u User) error {
	in := storageAssetReferences{References: []storageAssetReference{}, Prepare: true, Retain: true}
	for _, p := range plan {
		if p.Object.Target != "entity" || rawNull(p.Merged) {
			continue
		}
		_, old, err := workspaceCoverAssets(p.Current)
		if err != nil {
			return err
		}
		e, next, err := workspaceCoverAssets(p.Merged)
		if err != nil {
			return err
		}
		if err := validateWorkspacePictureAssetIDs(p.Merged, p.Current, p.Object.Base); err != nil {
			return err
		}
		for _, id := range sortedMapKeys(next) {
			if old[id] {
				continue
			}
			authors := map[string]bool{}
			for _, c := range commits {
				if c.WorkspaceID != nil {
					in.WorkspaceID = *c.WorkspaceID
				}
				for _, change := range c.Changes {
					if change.Target != "entity" || change.ID != p.Object.ID {
						continue
					}
					_, prior, err := workspaceCoverAssets(change.Before)
					if err != nil {
						return err
					}
					_, later, err := workspaceCoverAssets(change.After)
					if err != nil {
						return err
					}
					if prior[id] || !later[id] {
						continue
					}
					authors[c.Author.ID] = true
					for _, a := range c.Authors {
						authors[a.ID] = true
					}
				}
			}
			if len(authors) == 0 {
				return fmt.Errorf("invalid_commit_provenance")
			}
			ref := storageAssetReference{EntityID: e.ID, AssetID: id, ActorIDs: sortedMapKeys(authors)}
			if u.Can("storage.asset.moderate") {
				ref.ModerateActorID = u.ID
			}
			in.References = append(in.References, ref)
		}
	}
	if len(in.References) == 0 {
		return nil
	}
	if in.WorkspaceID == "" {
		return fmt.Errorf("invalid_commit_provenance")
	}
	c, err := catalogAssetClient()
	if err != nil {
		return err
	}
	return c.retainAssets(ctx, in)
}
