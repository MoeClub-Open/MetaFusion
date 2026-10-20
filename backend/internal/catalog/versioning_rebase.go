package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Rebase replays a selected ordered edit sequence onto a frozen base, replacing
// branch references while keeping all original commits and author snapshots.
// A conflict rolls back the complete replay, including intermediate commits.
func (s *Store) replayWorkspaceHistory(ctx context.Context, wid string, in WorkspaceRebase, u User) (WorkspaceRebaseResult, error) {
	var out WorkspaceRebaseResult
	if !collaborationID(in.ID) || in.OntoCommitID != "" && !collaborationID(in.OntoCommitID) {
		return out, fmt.Errorf("invalid_commit_id")
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, wid, u, true, true)
		if err != nil {
			return err
		}
		if replay, ok, err := versionActionReplay(ctx, tx, w, in.ID, in, u); err != nil {
			return err
		} else if ok {
			out = WorkspaceRebaseResult{Workspace: replay.Workspace, Commit: replay.Commit}
			return nil
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if w.HeadCommitID == nil || in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if w.DirtyCount > 0 {
			return fmt.Errorf("uncommitted_changes")
		}
		if err = workspaceAuthorizeSubmission(ctx, tx, w, u, false); err != nil {
			return err
		}
		pending, err := workspaceCommitsFrom(ctx, tx, wid, true)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return fmt.Errorf("no_changes")
		}
		oldHead := w.HeadCommitID
		resolutions := newVersionResolutions(in.Resolutions)
		originals := []string{}
		edits := []CollaborationCommit{}
		byID := map[string]CollaborationCommit{}
		for _, c := range pending {
			originals = append(originals, c.ID)
			byID[c.ID] = c
			if c.Kind == "edit" {
				edits = append(edits, c)
			}
		}
		if in.Order != nil {
			edits = []CollaborationCommit{}
			seen := map[string]bool{}
			for _, id := range *in.Order {
				c, ok := byID[id]
				if !ok || seen[id] || c.Kind != "edit" {
					return fmt.Errorf("invalid_commit_range")
				}
				seen[id] = true
				edits = append(edits, c)
			}
		}
		onto := in.OntoCommitID
		if onto == "" {
			if err = tx.QueryRowContext(ctx, `SELECT commit_id::text FROM catalog.catalog_head WHERE singleton`).Scan(&onto); err != nil {
				return err
			}
		}
		for _, id := range originals {
			if id == onto {
				return fmt.Errorf("invalid_rebase_target")
			}
		}
		tree, scope, err := versionTreeForTargets(ctx, tx, onto, w.Objects, u)
		if err != nil {
			return err
		}
		objects := map[string]WorkspaceObject{}
		for _, o := range w.Objects {
			o.Base = nil
			o.Committed = nil
			o.Document = nil
			o.BaseVersion = 0
			objects[o.Target+":"+o.ID] = o
		}
		for _, o := range tree {
			if scope == "catalog" {
				o.Base = o.Document
				o.BaseVersion = versionDocumentNumber(o.Document, o.BaseVersion)
			}
			if o.Target == "entity" {
				var e Entity
				_ = json.Unmarshal(o.Document, &e)
				if e.State == "deleted" || e.State == "merged" {
					o.Committed = nil
					o.Document = nil
				}
			}
			objects[o.Target+":"+o.ID] = o
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_commits SET active=false WHERE workspace_id=$1 AND NOT integrated`, wid); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspaces SET head_commit_id=$2,base_commit_id=$2 WHERE id=$1`, wid, onto); err != nil {
			return err
		}
		if scope != "catalog" {
			ids, err := versionBranchCommits(ctx, tx, onto, nil)
			if err != nil {
				return err
			}
			for _, id := range ids {
				allowed, err := versionCommitReadable(ctx, tx, id, u)
				if err != nil {
					return err
				}
				if !allowed {
					return sql.ErrNoRows
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_commits(workspace_id,commit_id,position,integrated) SELECT $1,c.id,(SELECT COALESCE(max(position),0)+1 FROM catalog.workspace_commits WHERE workspace_id=$1),c.merged_at IS NOT NULL FROM catalog.commits c WHERE c.id=$2 ON CONFLICT(workspace_id,commit_id) DO UPDATE SET active=true`, wid, id); err != nil {
					return err
				}
			}
		}
		assetCtx := ctx
		var sourceWorkspace *string
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id::text FROM catalog.commits WHERE id=$1`, onto).Scan(&sourceWorkspace); err != nil {
			return err
		}
		if sourceWorkspace != nil {
			assetCtx = context.WithValue(ctx, versionAssetSourceKey{}, *sourceWorkspace)
		}
		for _, key := range sortedMapKeys(objects) {
			o := objects[key]
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document,staged_authors) VALUES($1,$2,$3,$4,$5,$6,$6,'{}') ON CONFLICT(workspace_id,target,target_id) DO UPDATE SET base_version=EXCLUDED.base_version,base_document=EXCLUDED.base_document,head_document=EXCLUDED.head_document,document=EXCLUDED.document,staged_authors='{}'`, wid, o.Target, o.ID, o.BaseVersion, nullableJSON(o.Base), nullableJSON(o.Document)); err != nil {
				return err
			}
			if o.Target == "entity" {
				objectCtx := assetCtx
				if sourceWorkspace == nil {
					objectCtx, err = versionAssetSources(ctx, tx, o.Document)
					if err != nil {
						return err
					}
				}
				if err = s.reserveWorkspaceCoverAssets(objectCtx, wid, o.Document, o.Document, u); err != nil {
					return err
				}
			}
		}
		w, err = workspaceFrom(ctx, tx, wid, u, false, false)
		if err != nil {
			return err
		}
		replayed := map[string]bool{}
		if len(edits) == 0 {
			meta := versionCommitMeta{Operation: "rebase", Sources: originals, PreserveAuthors: true, Rewriting: true, PreviousHead: oldHead}
			c, err := insertWorkspaceCommit(context.WithValue(ctx, versionCommitMetaKey{}, meta), tx, w, in.ID, "Rebase", "rebase", in, u)
			if err != nil {
				return err
			}
			out.Commit = &c
		}
		for step, source := range edits {
			resolvedChange := false
			for _, co := range source.Changes {
				key := co.Target + ":" + co.ID
				o, ok := objects[key]
				if !ok {
					o = WorkspaceObject{Target: co.Target, ID: co.ID}
					if err = s.versionCheckoutObject(ctx, tx, wid, &o, u); err != nil {
						return err
					}
				}
				candidate := WorkspaceObject{Target: co.Target, ID: co.ID, BaseVersion: o.BaseVersion, Base: co.Before, Committed: co.After}
				version := versionDocumentNumber(o.Document, o.BaseVersion)
				merged, conflicts, err := mergeWorkspaceObject(candidate, o.Document, version)
				if err != nil {
					return err
				}
				for i := range conflicts {
					conflicts[i].SourceCommitID = source.ID
				}
				var changed bool
				merged, changed, err = resolutions.resolve(merged, conflicts, version)
				if err != nil {
					return err
				}
				resolvedChange = resolvedChange || changed
				o.Document = merged
				objects[key] = o
				if err = s.versionSaveObject(ctx, tx, wid, o, u); err != nil {
					return err
				}
			}
			w, err = workspaceFrom(ctx, tx, wid, u, false, false)
			if err != nil {
				return err
			}
			id := uuid.NewSHA1(uuid.MustParse(in.ID), []byte(source.ID)).String()
			if step == len(edits)-1 {
				id = in.ID
			}
			message := source.Message
			if step == len(edits)-1 && in.Message != "" {
				message, err = versionMessage(in.Message, "Rebase")
				if err != nil {
					return err
				}
			}
			authors := append([]CollaborationAuthor{}, source.Authors...)
			if resolvedChange {
				authors = append(authors, CollaborationAuthor{ID: u.ID, Name: u.Username})
			}
			meta := versionCommitMeta{Operation: "rebase", Sources: []string{source.ID}, PreserveAuthors: true, PrimaryAuthor: &source.Author, Authors: authors, Rewriting: step == 0, PreviousHead: oldHead}
			c, err := insertWorkspaceCommit(context.WithValue(ctx, versionCommitMetaKey{}, meta), tx, w, id, message, "edit", in, u)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_replacements VALUES($1,$2)`, source.ID, c.ID); err != nil {
				return err
			}
			out.Commit = &c
			replayed[source.ID] = true
			w, err = workspaceFrom(ctx, tx, wid, u, false, false)
			if err != nil {
				return err
			}
			objects = map[string]WorkspaceObject{}
			for _, o := range w.Objects {
				objects[o.Target+":"+o.ID] = o
			}
		}
		if err = resolutions.validate(); err != nil {
			return err
		}
		if out.Commit != nil {
			dropped := []string{}
			for _, id := range originals {
				if !replayed[id] {
					dropped = append(dropped, id)
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_replacements(original_id,replacement_id) SELECT unnest($1::uuid[]),$2::uuid ON CONFLICT DO NOTHING`, pq.Array(dropped), out.Commit.ID); err != nil {
				return err
			}
		}
		out.Workspace, err = workspaceFrom(ctx, tx, wid, u, false, false)
		return err
	})
	return out, err
}
