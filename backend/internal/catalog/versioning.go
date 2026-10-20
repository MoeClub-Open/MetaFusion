package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

func (s *Store) ForkWorkspace(ctx context.Context, in WorkspaceFork, u User) (Workspace, error) {
	if collaborationUser(u) != nil {
		return Workspace{}, errForbidden
	}
	if !collaborationID(in.ID) || !collaborationID(in.CommitID) {
		return Workspace{}, fmt.Errorf("invalid_commit_id")
	}
	name, err := collaborationName(in.Name)
	if err != nil {
		return Workspace{}, err
	}
	var out Workspace
	err = s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var owner, hash string
		err := tx.QueryRowContext(ctx, `SELECT owner_id,creation_hash FROM catalog.workspaces WHERE id=$1`, in.ID).Scan(&owner, &hash)
		if err == nil {
			if owner != u.ID || hash != requestHash(in) {
				return errIdempotencyConflict
			}
			out, err = workspaceFrom(ctx, tx, in.ID, u, false, false)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var targets []WorkspaceObject
		if len(in.EntityIDs)+len(in.RelationIDs) > 0 {
			targets = []WorkspaceObject{}
			for _, id := range in.EntityIDs {
				if !collaborationID(id) {
					return fmt.Errorf("invalid_id")
				}
				targets = append(targets, WorkspaceObject{Target: "entity", ID: id})
			}
			for _, id := range in.RelationIDs {
				if !collaborationID(id) {
					return fmt.Errorf("invalid_id")
				}
				targets = append(targets, WorkspaceObject{Target: "relation", ID: id})
			}
		}
		objects, scope, err := versionTreeForTargets(ctx, tx, in.CommitID, targets, u)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspaces(id,owner_id,name,head_commit_id,creation_hash,base_commit_id) VALUES($1,$2,$3,$4,$5,$4)`, in.ID, u.ID, name, in.CommitID, requestHash(in)); err != nil {
			return err
		}
		var sourceWid *string
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id::text FROM catalog.commits WHERE id=$1`, in.CommitID).Scan(&sourceWid); err != nil {
			return err
		}
		assetCtx := ctx
		if sourceWid != nil {
			assetCtx = context.WithValue(ctx, versionAssetSourceKey{}, *sourceWid)
		}
		var merged bool
		if err = tx.QueryRowContext(ctx, `SELECT merged_at IS NOT NULL FROM catalog.commits WHERE id=$1`, in.CommitID).Scan(&merged); err != nil {
			return err
		}
		for _, o := range objects {
			if merged {
				o.Base = o.Document
				o.BaseVersion = versionDocumentNumber(o.Document, o.BaseVersion)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document) VALUES($1,$2,$3,$4,$5,$6,$6)`, in.ID, o.Target, o.ID, o.BaseVersion, nullableJSON(o.Base), nullableJSON(o.Document)); err != nil {
				return err
			}
			// Forking grants access to the frozen records, never to the source's
			// later live edits. New publication still checks this user's rights.
			if o.Target == "entity" {
				objectCtx := assetCtx
				if sourceWid == nil {
					objectCtx, err = versionAssetSources(ctx, tx, o.Document)
					if err != nil {
						return err
					}
				}
				if err = s.reserveWorkspaceCoverAssets(objectCtx, in.ID, o.Document, o.Document, u); err != nil {
					return err
				}
			}
		}
		ids, err := versionBranchCommits(ctx, tx, in.CommitID, nil)
		if err != nil {
			return err
		}
		if scope == "catalog" {
			ids = []string{}
		}
		for position, id := range ids {
			allowed, err := versionCommitReadable(ctx, tx, id, u)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_commits(workspace_id,commit_id,position,integrated) SELECT $1,id,$3,merged_at IS NOT NULL FROM catalog.commits WHERE id=$2`, in.ID, id, position+1); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_events(workspace_id,request_id,request_hash,actor_id,action,new_head) VALUES($1,$1,$2,$3,'fork',$4)`, in.ID, requestHash(in), u.ID, in.CommitID); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, in.ID, u, false, false)
		return err
	})
	return out, err
}

func versionDocumentNumber(raw json.RawMessage, fallback int64) int64 {
	var d struct {
		Version int64 `json:"version"`
	}
	if json.Unmarshal(raw, &d) == nil && d.Version > 0 {
		return d.Version
	}
	return fallback
}

func versionActionReplay(ctx context.Context, tx *sql.Tx, w Workspace, id string, request any, u User) (WorkspaceVersionResult, bool, error) {
	var hash, actor string
	var head *string
	err := tx.QueryRowContext(ctx, `SELECT request_hash,actor_id,new_head::text FROM catalog.workspace_events WHERE workspace_id=$1 AND request_id=$2`, w.ID, id).Scan(&hash, &actor, &head)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceVersionResult{}, false, nil
	}
	if err != nil {
		return WorkspaceVersionResult{}, false, err
	}
	if hash != requestHash(request) || actor != u.ID {
		return WorkspaceVersionResult{}, true, errIdempotencyConflict
	}
	out := WorkspaceVersionResult{Workspace: w}
	if head != nil {
		c, err := collaborationCommitFrom(ctx, tx, *head)
		if err != nil {
			return out, true, err
		}
		out.Commit = &c
	}
	return out, true, nil
}

func recordVersionEvent(ctx context.Context, tx *sql.Tx, w Workspace, id, action string, request any, u User) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog.workspace_events(workspace_id,request_id,request_hash,actor_id,action,old_head,new_head,detail) VALUES($1,$2,$3,$4,$5,$6,$6,$7)`, w.ID, id, requestHash(request), u.ID, action, w.HeadCommitID, encode(request))
	return err
}

// Every operation computes a prospective tree first. Conflicts abort the
// transaction, so retries never have partially staged or rewritten branches.
func (s *Store) ApplyWorkspaceVersion(ctx context.Context, wid string, in WorkspaceVersionAction, u User) (WorkspaceVersionResult, error) {
	var out WorkspaceVersionResult
	if !collaborationID(in.ID) {
		return out, fmt.Errorf("invalid_commit_id")
	}
	if !contains([]string{"merge", "cherry_pick", "revert", "restore", "amend", "squash", "reset", "discard"}, in.Operation) {
		return out, fmt.Errorf("invalid_version_operation")
	}
	if len(in.CommitIDs) > 100 {
		return out, fmt.Errorf("invalid_commit_size")
	}
	seen := map[string]bool{}
	for _, id := range in.CommitIDs {
		if !collaborationID(id) || seen[id] {
			return out, fmt.Errorf("invalid_commit_id")
		}
		seen[id] = true
	}
	if in.Mode != "" && !(in.Operation == "reset" && contains([]string{"soft", "hard"}, in.Mode)) {
		return out, fmt.Errorf("invalid_reset_mode")
	}
	message, err := versionMessage(in.Message, in.Operation)
	if err != nil {
		return out, err
	}
	err = s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, wid, u, true, true)
		if err != nil {
			return err
		}
		if replay, ok, err := versionActionReplay(ctx, tx, w, in.ID, in, u); err != nil {
			return err
		} else if ok {
			out = replay
			return nil
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if w.HeadCommitID != nil && in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if in.Operation == "discard" {
			if len(in.CommitIDs) > 0 || len(in.Resolutions) > 0 {
				return fmt.Errorf("invalid_version_operation")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET document=head_document,staged_authors='{}' WHERE workspace_id=$1`, wid); err != nil {
				return err
			}
			if err = recordVersionEvent(ctx, tx, w, in.ID, "discard", in, u); err != nil {
				return err
			}
			if err = bumpWorkspace(ctx, tx, wid); err != nil {
				return err
			}
			out.Workspace, err = workspaceFrom(ctx, tx, wid, u, false, false)
			return err
		}
		if w.DirtyCount > 0 && in.Operation != "amend" && in.Operation != "reset" {
			return fmt.Errorf("uncommitted_changes")
		}
		objects := map[string]WorkspaceObject{}
		resolutions := newVersionResolutions(in.Resolutions)
		resolutionAuthored := false
		stagedAuthors := []CollaborationAuthor{}
		originalHead := w.HeadCommitID
		assetSources := map[string]string{}
		for _, o := range w.Objects {
			objects[o.Target+":"+o.ID] = o
			if o.Dirty {
				for id, name := range o.Contributors {
					stagedAuthors = append(stagedAuthors, CollaborationAuthor{ID: id, Name: name})
				}
			}
		}
		sources := []CollaborationCommit{}
		for _, id := range in.CommitIDs {
			allowed, err := versionCommitReadable(ctx, tx, id, u)
			if err != nil {
				return err
			}
			if !allowed {
				return sql.ErrNoRows
			}
			c, err := collaborationCommitFrom(ctx, tx, id)
			if err != nil {
				return err
			}
			sources = append(sources, c)
		}
		replaced := []string{}
		var parent *string
		var mergeParent *string
		switch in.Operation {
		case "amend", "squash", "reset":
			if w.OwnerID != u.ID {
				return errForbidden
			}
			pending, err := workspaceCommitsFrom(ctx, tx, wid, true)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				return fmt.Errorf("published_history")
			}
			start := len(pending) - 1
			if in.Operation == "squash" {
				if len(in.CommitIDs) < 2 {
					return fmt.Errorf("invalid_commit_range")
				}
				start = len(pending) - len(in.CommitIDs)
				if start < 0 {
					return fmt.Errorf("invalid_commit_range")
				}
				for i, id := range in.CommitIDs {
					if pending[start+i].ID != id {
						return fmt.Errorf("invalid_commit_range")
					}
				}
			}
			if in.Operation == "reset" {
				if len(in.CommitIDs) != 1 {
					return fmt.Errorf("invalid_commit_range")
				}
				start = -1
				for i, c := range pending {
					if c.ID == in.CommitIDs[0] {
						start = i + 1
					}
				}
				if w.HeadCommitID != nil {
					found, err := versionFirstParentContains(ctx, tx, *w.HeadCommitID, in.CommitIDs[0])
					if err != nil {
						return err
					}
					if !found {
						return fmt.Errorf("invalid_commit_range")
					}
				}
				if start < 0 {
					var boundary *string
					if err = tx.QueryRowContext(ctx, `SELECT (SELECT commit_id::text FROM catalog.workspace_commits WHERE workspace_id=$1 AND active AND integrated ORDER BY position DESC LIMIT 1)`, wid).Scan(&boundary); err != nil {
						return err
					}
					if boundary != nil && *boundary == in.CommitIDs[0] || w.BaseCommitID != nil && *w.BaseCommitID == in.CommitIDs[0] {
						start = 0
					}
				}
				if start < 0 || start >= len(pending) {
					return fmt.Errorf("invalid_commit_range")
				}
			}
			for _, c := range pending[start:] {
				replaced = append(replaced, c.ID)
			}
			parent = pending[start].ParentID
			if in.Operation == "amend" && len(in.CommitIDs) > 0 {
				return fmt.Errorf("invalid_commit_range")
			}
			if in.Operation == "reset" {
				tree, _, err := versionTreeForTargets(ctx, tx, in.CommitIDs[0], w.Objects, u)
				if err != nil {
					return err
				}
				wanted := map[string]WorkspaceObject{}
				for _, o := range tree {
					wanted[o.Target+":"+o.ID] = o
				}
				for key, o := range objects {
					desired, ok := wanted[key]
					if ok {
						o.Committed = desired.Document
					} else {
						o.Committed = nil
					}
					if in.Mode == "hard" {
						o.Document = o.Committed
					}
					objects[key] = o
				}
			} else {
				// A replacement's before state is the state immediately before
				// the replaced suffix, not the old tip being replaced.
				before := map[string]json.RawMessage{}
				for _, c := range pending[start:] {
					for _, co := range c.Changes {
						key := co.Target + ":" + co.ID
						if _, ok := before[key]; !ok {
							before[key] = co.Before
						}
					}
				}
				for key, o := range objects {
					if raw, ok := before[key]; ok {
						o.Committed = raw
					}
					if in.Operation == "squash" {
						o.Document = objects[key].Committed
					}
					objects[key] = o
				}
			}
		case "restore":
			if len(sources) != 1 {
				return fmt.Errorf("invalid_commit_range")
			}
			var targets []WorkspaceObject
			if len(w.Objects) > 0 {
				targets = w.Objects
			}
			tree, _, err := versionTreeForTargets(ctx, tx, sources[0].ID, targets, u)
			if err != nil {
				return err
			}
			wanted := map[string]WorkspaceObject{}
			for _, o := range tree {
				wanted[o.Target+":"+o.ID] = o
				if sources[0].WorkspaceID != nil {
					assetSources[o.Target+":"+o.ID] = *sources[0].WorkspaceID
				}
			}
			for key, o := range objects {
				desired, ok := wanted[key]
				if ok {
					o.Document = desired.Document
					delete(wanted, key)
				} else {
					o.Document = o.Base
				}
				objects[key] = o
			}
			for key, o := range wanted {
				if err = s.versionCheckoutObject(ctx, tx, wid, &o, u); err != nil {
					return err
				}
				o.Document = wanted[key].Document
				objects[key] = o
			}
		case "merge", "cherry_pick", "revert":
			if len(sources) == 0 || in.Operation == "merge" && len(sources) != 1 {
				return fmt.Errorf("invalid_commit_range")
			}
			if in.Operation == "merge" {
				value := sources[0].ID
				mergeParent = &value
			}
			for _, source := range sources {
				changes := source.Changes
				if in.Operation == "merge" {
					var targets []WorkspaceObject
					if len(w.Objects) > 0 {
						targets = w.Objects
					}
					tree, _, err := versionTreeForTargets(ctx, tx, source.ID, targets, u)
					if err != nil {
						return err
					}
					targetHead := w.HeadCommitID
					if targetHead == nil {
						targetHead = w.BaseCommitID
					}
					common := (*string)(nil)
					if targetHead != nil {
						common, err = versionMergeBase(ctx, tx, *targetHead, source.ID)
						if err != nil {
							return err
						}
					}
					baseObjects := map[string]WorkspaceObject{}
					if common != nil {
						if *common == source.ID {
							return fmt.Errorf("no_changes")
						}
						baseTree, _, err := versionTreeForTargets(ctx, tx, *common, tree, u)
						if err != nil {
							return err
						}
						for _, o := range baseTree {
							baseObjects[o.Target+":"+o.ID] = o
						}
					}
					// A branch merge retains original source commits as members
					// of the target DAG. The merge node is not a fabricated edit.
					incoming, err := versionBranchCommits(ctx, tx, source.ID, common)
					if err != nil {
						return err
					}
					for _, id := range incoming {
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
					changes = []CommitObject{}
					for _, o := range tree {
						base := o.Base
						if ancestor, ok := baseObjects[o.Target+":"+o.ID]; ok {
							base = ancestor.Document
						}
						changes = append(changes, CommitObject{Target: o.Target, ID: o.ID, BaseVersion: o.BaseVersion, Before: base, After: o.Document})
					}
				}
				for _, co := range changes {
					key := co.Target + ":" + co.ID
					if source.WorkspaceID != nil {
						assetSources[key] = *source.WorkspaceID
					}
					o, ok := objects[key]
					if !ok {
						o = WorkspaceObject{Target: co.Target, ID: co.ID}
						if err = s.versionCheckoutObject(ctx, tx, wid, &o, u); err != nil {
							return err
						}
					}
					base, after := co.Before, co.After
					if in.Operation == "revert" {
						base, after = after, base
					}
					candidate := WorkspaceObject{Target: o.Target, ID: o.ID, BaseVersion: o.BaseVersion, Base: base, Committed: after}
					conflictVersion := versionDocumentNumber(o.Document, o.BaseVersion)
					currentForMerge := o.Document
					if o.Target == "entity" && rawNull(currentForMerge) && !rawNull(o.Base) && !rawNull(after) && u.Can(PermissionLifecycleManage) {
						var tombstone Entity
						_ = json.Unmarshal(o.Base, &tombstone)
						if tombstone.State == "deleted" {
							var sourceBase Entity
							_ = json.Unmarshal(base, &sourceBase)
							if sourceBase.State == "deleted" {
								currentForMerge = o.Base
							} else if rawNull(base) && o.BaseVersion == co.BaseVersion+1 {
								candidate.Base = o.Base
								currentForMerge = o.Base
							}
						}
					}
					merged, conflicts, err := mergeWorkspaceObject(candidate, currentForMerge, conflictVersion)
					if err != nil {
						return err
					}
					for i := range conflicts {
						conflicts[i].SourceCommitID = source.ID
					}
					var changed bool
					merged, changed, err = resolutions.resolve(merged, conflicts, conflictVersion)
					if err != nil {
						return err
					}
					resolutionAuthored = resolutionAuthored || changed
					o.Document = merged
					objects[key] = o
				}
			}
		}
		if err = resolutions.validate(); err != nil {
			return err
		}
		// Replacements retain originals and their attribution. Only the active
		// branch sequence changes, and publication remains an explicit push.
		if len(replaced) > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_commits SET active=false WHERE workspace_id=$1 AND commit_id=ANY($2::uuid[]) AND NOT integrated`, wid, pq.Array(replaced)); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspaces SET head_commit_id=$2 WHERE id=$1`, wid, parent); err != nil {
				return err
			}
			w.HeadCommitID = parent
		}
		for _, key := range sortedMapKeys(objects) {
			o := objects[key]
			assetCtx := ctx
			if source, ok := assetSources[key]; ok {
				assetCtx = context.WithValue(ctx, versionAssetSourceKey{}, source)
			} else if in.Operation == "restore" && o.Target == "entity" {
				assetCtx, err = versionAssetSources(ctx, tx, o.Document)
				if err != nil {
					return err
				}
			}
			if err = s.versionSaveObject(assetCtx, tx, wid, o, u); err != nil {
				return err
			}
		}
		w, err = workspaceFrom(ctx, tx, wid, u, false, false)
		if err != nil {
			return err
		}
		if w.DirtyCount == 0 && len(replaced) == 0 && mergeParent == nil {
			return fmt.Errorf("no_changes")
		}
		meta := versionCommitMeta{Operation: in.Operation, Sources: append([]string{}, in.CommitIDs...), MergeParent: mergeParent, PreviousHead: originalHead, Rewriting: len(replaced) > 0}
		if len(replaced) > 0 {
			meta.Sources = replaced
		}
		switch in.Operation {
		case "merge":
			meta.PreserveAuthors = true
			if resolutionAuthored {
				meta.Authors = []CollaborationAuthor{{ID: u.ID, Name: u.Username}}
			}
		case "cherry_pick":
			meta.PreserveAuthors = true
			meta.PrimaryAuthor = &sources[0].Author
			for _, source := range sources {
				meta.Authors = append(meta.Authors, source.Authors...)
			}
			if resolutionAuthored {
				meta.Authors = append(meta.Authors, CollaborationAuthor{ID: u.ID, Name: u.Username})
			}
		case "amend", "squash":
			meta.PreserveAuthors = true
			for _, id := range replaced {
				original, err := collaborationCommitFrom(ctx, tx, id)
				if err != nil {
					return err
				}
				meta.Authors = append(meta.Authors, original.Authors...)
				if meta.PrimaryAuthor == nil {
					a := original.Author
					meta.PrimaryAuthor = &a
				}
			}
			if in.Operation == "amend" {
				meta.Authors = append(meta.Authors, stagedAuthors...)
			}
		}
		commitCtx := context.WithValue(ctx, versionCommitMetaKey{}, meta)
		// Soft reset keeps edits unstaged; the new reset checkpoint captures
		// the selected committed tree, then restores the working documents.
		working := map[string]json.RawMessage{}
		workingAuthors := map[string]map[string]string{}
		if in.Operation == "reset" && in.Mode != "hard" {
			for _, o := range w.Objects {
				working[o.Target+":"+o.ID] = o.Document
				workingAuthors[o.Target+":"+o.ID] = o.Contributors
			}
			for _, o := range objects {
				if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET document=$4 WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, wid, o.Target, o.ID, nullableJSON(o.Committed)); err != nil {
					return err
				}
			}
			w, err = workspaceFrom(ctx, tx, wid, u, false, false)
			if err != nil {
				return err
			}
		}
		kind := "edit"
		if in.Operation == "merge" && !resolutionAuthored {
			kind = "rebase"
		}
		c, err := insertWorkspaceCommit(commitCtx, tx, w, in.ID, message, kind, in, u)
		if err != nil {
			return err
		}
		for _, id := range replaced {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_replacements VALUES($1,$2)`, id, c.ID); err != nil {
				return err
			}
		}
		for key, raw := range working {
			parts := strings.SplitN(key, ":", 2)
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET document=$4,staged_authors=$5 WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, wid, parts[0], parts[1], nullableJSON(raw), encode(workingAuthors[key])); err != nil {
				return err
			}
		}
		c, err = collaborationCommitFrom(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		out.Commit = &c
		out.Workspace, err = workspaceFrom(ctx, tx, wid, u, false, false)
		return err
	})
	return out, err
}

func (s *Store) versionCheckoutObject(ctx context.Context, tx *sql.Tx, wid string, o *WorkspaceObject, u User) error {
	raw, version, err := workspaceMainObject(ctx, tx, o.Target, o.ID, u)
	if err != nil {
		return err
	}
	o.BaseVersion = version
	o.Base = raw
	o.Committed = raw
	o.Document = raw
	if rawNull(raw) && version > 0 && o.Target == "entity" {
		if !u.Can(PermissionLifecycleManage) {
			return errForbidden
		}
		e, err := get(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if e.State == "merged" {
			return fmt.Errorf("use_lifecycle_endpoint")
		}
		o.Base = json.RawMessage(encode(e))
	}
	if !rawNull(raw) {
		return workspaceAuthorizeObject(ctx, tx, wid, o.Target, raw, u)
	}
	return nil
}

func (s *Store) versionSaveObject(ctx context.Context, tx *sql.Tx, wid string, o WorkspaceObject, u User) error {
	changed := !equalDocument(o.Committed, o.Document)
	if changed && !rawNull(o.Document) {
		authority := o.Committed
		if rawNull(authority) {
			authority = o.Base
		}
		if !rawNull(authority) {
			normalized, err := normalizeWorkspaceDocument(o.Document, authority, o.Target, o.ID, false)
			if err != nil {
				return err
			}
			o.Document = normalized
			if o.Target == "entity" {
				var e Entity
				_ = json.Unmarshal(o.Document, &e)
				if e.State == "deleted" {
					if !u.Can(PermissionLifecycleManage) {
						return errForbidden
					}
					e.State = "active"
					e.RedirectID = ""
					o.Document = json.RawMessage(encode(e))
				}
			}
		} else {
			if o.Target == "entity" {
				var e Entity
				if err := strictCommitDocument(o.Document, &e); err != nil {
					return err
				}
				if e.ID != "" && e.ID != o.ID {
					return fmt.Errorf("immutable_scope")
				}
				e.ID = ""
				e.Version = 0
				e.State = ""
				e.CreatedBy = ""
				e.ReviewStatus = ""
				e.RedirectID = ""
				raw, err := normalizeWorkspaceDocument(json.RawMessage(encode(e)), nil, o.Target, o.ID, true)
				if err != nil {
					return err
				}
				e = Entity{}
				_ = json.Unmarshal(raw, &e)
				e.CreatedBy = u.ID
				o.Document = json.RawMessage(encode(e))
			} else {
				var r Relation
				if err := strictCommitDocument(o.Document, &r); err != nil {
					return err
				}
				if r.ID != "" && r.ID != o.ID {
					return fmt.Errorf("immutable_scope")
				}
				r.ID = ""
				r.Version = 0
				raw, err := normalizeWorkspaceDocument(json.RawMessage(encode(r)), nil, o.Target, o.ID, true)
				if err != nil {
					return err
				}
				o.Document = raw
			}
		}
	}
	raw := o.Document
	if rawNull(raw) {
		raw = o.Committed
	}
	if rawNull(raw) {
		raw = o.Base
	}
	if changed && !rawNull(raw) {
		if err := workspaceAuthorizeObject(ctx, tx, wid, o.Target, raw, u); err != nil {
			return err
		}
	}
	if changed && rawNull(o.Document) && o.Target == "entity" && !rawNull(o.Base) && !u.Can(PermissionLifecycleManage) {
		return errForbidden
	}
	if o.Target == "entity" {
		retained := o.Document
		if rawNull(retained) {
			retained = o.Committed
		}
		if err := s.reserveWorkspaceCoverAssets(ctx, wid, o.Committed, retained, u); err != nil {
			return err
		}
	}
	authors := map[string]string{}
	for id, name := range o.Contributors {
		authors[id] = name
	}
	authors[u.ID] = u.Username
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document,staged_authors) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(workspace_id,target,target_id) DO UPDATE SET head_document=EXCLUDED.head_document,document=EXCLUDED.document,staged_authors=catalog.workspace_objects.staged_authors||EXCLUDED.staged_authors,updated_at=now()`, wid, o.Target, o.ID, o.BaseVersion, nullableJSON(o.Base), nullableJSON(o.Committed), nullableJSON(o.Document), encode(authors))
	return err
}

type versionResolutions struct {
	items []WorkspaceResolution
	used  map[int]bool
}

func newVersionResolutions(items []WorkspaceResolution) *versionResolutions {
	return &versionResolutions{items: items, used: map[int]bool{}}
}

func (rs *versionResolutions) validate() error {
	if len(rs.used) != len(rs.items) {
		return fmt.Errorf("invalid_resolution")
	}
	return nil
}

// A choice belongs to one source commit and one conflict. Reject unused choices
// after the complete operation so a stale choice cannot manufacture authorship.
func (rs *versionResolutions) resolve(merged json.RawMessage, conflicts []WorkspaceConflict, version int64) (json.RawMessage, bool, error) {
	changed := false
	unresolved := []WorkspaceConflict{}
	for _, c := range conflicts {
		var found *WorkspaceResolution
		index := -1
		for i := range rs.items {
			r := &rs.items[i]
			if r.Target == c.Target && r.ID == c.ID && r.Pointer == c.Pointer && (r.CommitID == "" || r.CommitID == c.SourceCommitID) {
				if found != nil {
					return nil, false, fmt.Errorf("invalid_resolution")
				}
				found = r
				index = i
			}
		}
		if found == nil {
			unresolved = append(unresolved, c)
			continue
		}
		r := *found
		if rs.used[index] {
			return nil, false, fmt.Errorf("invalid_resolution")
		}
		rs.used[index] = true
		if r.CurrentVersion != version {
			return nil, false, fmt.Errorf("resolution_version_conflict")
		}
		if r.Remove && len(r.Value) > 0 || !r.Remove && !json.Valid(r.Value) {
			return nil, false, fmt.Errorf("invalid_resolution")
		}
		changed = changed || !equalCommitValue(json.RawMessage(encode(c.Current)), c.CurrentPresent, r.Value, !r.Remove)
		if c.Pointer == "" {
			if r.Remove {
				merged = nil
			} else {
				merged = r.Value
			}
		} else {
			doc := commitMap(merged)
			parts, err := pointerParts(c.Pointer)
			if err != nil {
				return nil, false, err
			}
			if err = putPointer(doc, parts, r.Value, r.Remove); err != nil {
				return nil, false, err
			}
			merged = json.RawMessage(encode(doc))
		}
	}
	if len(unresolved) > 0 {
		return nil, false, &workspaceConflictError{unresolved}
	}
	return merged, changed, nil
}
