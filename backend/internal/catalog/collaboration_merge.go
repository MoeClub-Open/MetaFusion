package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type workspaceMergeObject struct {
	Object         WorkspaceObject
	Current        json.RawMessage
	CurrentVersion int64
	Merged         json.RawMessage
}
type collaborationAttributionKey struct{}
type collaborationReferencesKey struct{}

func collaborationAttribution(ctx context.Context, u User) User {
	if author, ok := ctx.Value(collaborationAttributionKey{}).(CollaborationAuthor); ok && author.ID != "" {
		u.ID = author.ID
		u.Username = author.Name
	}
	return u
}

// Compute a minimal object delta. Arrays have an intentional, stable boundary:
// ordered entries are resolved together, rather than merging shifted indexes.
func workspaceDocumentPatch(base, after json.RawMessage, target string) []CommitPatch {
	b, a := workspaceEditableMap(base, target), workspaceEditableMap(after, target)
	out := []CommitPatch{}
	var walk func(map[string]json.RawMessage, map[string]json.RawMessage, string)
	walk = func(before, next map[string]json.RawMessage, prefix string) {
		keys := map[string]bool{}
		for k := range before {
			keys[k] = true
		}
		for k := range next {
			keys[k] = true
		}
		for _, k := range sortedMapKeys(keys) {
			bv, bok := before[k]
			av, aok := next[k]
			if equalCommitValue(bv, bok, av, aok) {
				continue
			}
			path := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			var bm, am map[string]json.RawMessage
			if bok && aok && json.Unmarshal(bv, &bm) == nil && json.Unmarshal(av, &am) == nil && bm != nil && am != nil {
				walk(bm, am, path)
				continue
			}
			p := CommitPatch{Path: path, Remove: !aok}
			if aok {
				p.Value = av
			}
			out = append(out, p)
		}
	}
	walk(b, a, "")
	return out
}
func workspaceSameFact(a, b json.RawMessage, target string) bool {
	if rawNull(a) || rawNull(b) {
		return rawNull(a) && rawNull(b)
	}
	return encode(workspaceEditableMap(a, target)) == encode(workspaceEditableMap(b, target))
}
func mergeWorkspaceObject(o WorkspaceObject, current json.RawMessage, version int64) (json.RawMessage, []WorkspaceConflict, error) {
	conflict := func(kind string) []WorkspaceConflict {
		return []WorkspaceConflict{{Target: o.Target, ID: o.ID, BaseVersion: o.BaseVersion, CurrentVersion: version, Pointer: "", Base: decodePointerValue(o.Base, !rawNull(o.Base)), Current: decodePointerValue(current, !rawNull(current)), Proposed: decodePointerValue(o.Committed, !rawNull(o.Committed)), BasePresent: !rawNull(o.Base), CurrentPresent: !rawNull(current), ProposedPresent: !rawNull(o.Committed), Remove: rawNull(o.Committed), Kind: kind}}
	}
	if workspaceSameFact(o.Base, o.Committed, o.Target) {
		return current, nil, nil
	}
	if rawNull(o.Base) {
		if rawNull(current) {
			if version > 0 {
				return current, conflict("object_deleted"), nil
			}
			return o.Committed, nil, nil
		}
		if workspaceSameFact(current, o.Committed, o.Target) {
			return current, nil, nil
		}
		return current, conflict("object_created"), nil
	}
	if rawNull(o.Committed) {
		if rawNull(current) {
			return nil, nil, nil
		}
		if workspaceSameFact(o.Base, current, o.Target) {
			return nil, nil, nil
		}
		return current, conflict("delete_modified"), nil
	}
	if rawNull(current) {
		return nil, conflict("object_deleted"), nil
	}
	merged, conflicts, _, err := mergeCommitPatch(commitMap(json.RawMessage(o.Base)), commitMap(json.RawMessage(current)), workspaceDocumentPatch(o.Base, o.Committed, o.Target))
	if err != nil {
		return nil, nil, err
	}
	out := []WorkspaceConflict{}
	for _, c := range conflicts {
		parts, _ := pointerParts(c.Pointer)
		_, basePresent, _ := pointerValue(commitMap(json.RawMessage(o.Base)), parts)
		_, currentPresent, _ := pointerValue(commitMap(json.RawMessage(current)), parts)
		out = append(out, WorkspaceConflict{Target: o.Target, ID: o.ID, BaseVersion: o.BaseVersion, CurrentVersion: version, Pointer: c.Pointer, Base: c.Base, Current: c.Current, Proposed: c.Proposed, BasePresent: basePresent, CurrentPresent: currentPresent, ProposedPresent: !c.Remove, Remove: c.Remove, Kind: c.Kind})
	}
	return json.RawMessage(encode(merged)), out, nil
}
func workspaceMergePlan(ctx context.Context, tx *sql.Tx, w Workspace, u User, resolutions []WorkspaceResolution) ([]workspaceMergeObject, error) {
	resolutionMap := map[string]WorkspaceResolution{}
	for _, r := range resolutions {
		key := r.Target + ":" + r.ID + ":" + r.Pointer
		if _, exists := resolutionMap[key]; exists || r.Remove && len(r.Value) > 0 || !r.Remove && !json.Valid(r.Value) {
			return nil, fmt.Errorf("invalid_resolution")
		}
		resolutionMap[key] = r
	}
	used := map[string]bool{}
	plan := []workspaceMergeObject{}
	all := []WorkspaceConflict{}
	for _, o := range w.Objects {
		current, version, err := workspaceMainObject(ctx, tx, o.Target, o.ID, u)
		if err != nil {
			return nil, err
		}
		merged, conflicts, err := mergeWorkspaceObject(o, current, version)
		if err != nil {
			return nil, err
		}
		for _, c := range conflicts {
			key := c.Target + ":" + c.ID + ":" + c.Pointer
			r, ok := resolutionMap[key]
			if !ok {
				all = append(all, c)
				continue
			}
			used[key] = true
			if r.CurrentVersion != version {
				return nil, fmt.Errorf("resolution_version_conflict")
			}
			if c.Pointer == "" {
				if r.Remove {
					merged = nil
				} else {
					if rawNull(current) && version > 0 {
						return nil, fmt.Errorf("use_lifecycle_endpoint")
					}
					merged = r.Value
					if !rawNull(current) {
						merged, err = normalizeWorkspaceDocument(merged, current, o.Target, o.ID, false)
					} else {
						var doc map[string]json.RawMessage
						if json.Unmarshal(merged, &doc) != nil {
							return nil, fmt.Errorf("invalid_document")
						}
						doc["id"] = json.RawMessage(encode(o.ID))
						merged = json.RawMessage(encode(doc))
					}
				}
			} else {
				parts, e := pointerParts(c.Pointer)
				if e != nil {
					return nil, e
				}
				doc := commitMap(json.RawMessage(merged))
				err = putPointer(doc, parts, r.Value, r.Remove)
				merged = json.RawMessage(encode(doc))
			}
			if err != nil {
				return nil, err
			}
		}
		plan = append(plan, workspaceMergeObject{o, current, version, merged})
	}
	if len(all) > 0 {
		return nil, &workspaceConflictError{all}
	}
	for key := range resolutionMap {
		if !used[key] {
			return nil, fmt.Errorf("invalid_resolution")
		}
	}
	return plan, nil
}
func commitContributesToObject(c CollaborationCommit, p workspaceMergeObject) bool {
	if c.Kind != "edit" || workspaceSameFact(p.Current, p.Merged, p.Object.Target) {
		return false
	}
	for _, co := range c.Changes {
		if co.Target != p.Object.Target || co.ID != p.Object.ID {
			continue
		}
		if rawNull(co.Before) && !rawNull(co.After) && !rawNull(p.Merged) {
			return true
		}
		if rawNull(co.After) && rawNull(p.Merged) {
			return true
		}
		if rawNull(p.Merged) {
			continue
		}
		final := commitMap(json.RawMessage(p.Merged))
		current := commitMap(json.RawMessage(p.Current))
		for _, patch := range workspaceDocumentPatch(co.Before, co.After, co.Target) {
			parts, _ := pointerParts(patch.Path)
			value, ok, err := pointerValue(final, parts)
			old, oldOK, oldErr := pointerValue(current, parts)
			if err == nil && oldErr == nil && !equalCommitValue(old, oldOK, value, ok) && equalCommitValue(value, ok, patch.Value, !patch.Remove) {
				return true
			}
		}
	}
	return false
}

func workspacePlanOrder(plan []workspaceMergeObject) ([]workspaceMergeObject, error) {
	byID := map[string]int{}
	for i, p := range plan {
		if p.Object.Target == "entity" && !rawNull(p.Merged) {
			byID[p.Object.ID] = i
		}
	}
	state := make([]int, len(plan))
	out := make([]workspaceMergeObject, 0, len(plan))
	var visit func(int) error
	visit = func(index int) error {
		if state[index] == 2 {
			return nil
		}
		if state[index] == 1 {
			return fmt.Errorf("invalid_reference_cycle")
		}
		state[index] = 1
		p := plan[index]
		if p.Object.Target == "entity" && !rawNull(p.Merged) {
			var entity Entity
			if err := json.Unmarshal(p.Merged, &entity); err != nil {
				return err
			}
			deps := map[int]bool{}
			add := func(id string) {
				if dep, ok := byID[id]; ok && dep != index && rawNull(plan[dep].Current) {
					deps[dep] = true
				}
			}
			for _, id := range []string{entity.WorkID, entity.ContentUnitID, entity.ReleaseID, entity.MediumID, entity.ParentID} {
				add(id)
			}
			for _, subject := range entity.Subjects {
				add(subject.WorkID)
			}
			for _, content := range entity.Contents {
				add(content.ExpressionID)
			}
			ordered := make([]int, 0, len(deps))
			for dep := range deps {
				ordered = append(ordered, dep)
			}
			sort.Slice(ordered, func(i, j int) bool { return plan[ordered[i]].Object.ID < plan[ordered[j]].Object.ID })
			for _, dep := range ordered {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[index] = 2
		out = append(out, p)
		return nil
	}
	// Delete edges first; then create/update their endpoints before adding edges.
	for i, p := range plan {
		if p.Object.Target == "relation" && rawNull(p.Merged) {
			if err := visit(i); err != nil {
				return nil, err
			}
		}
	}
	for i, p := range plan {
		if p.Object.Target == "entity" {
			if err := visit(i); err != nil {
				return nil, err
			}
		}
	}
	for i := range plan {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) applyWorkspacePlan(ctx context.Context, tx *sql.Tx, w Workspace, plan []workspaceMergeObject, commits []CollaborationCommit, u User) error {
	plannedEntities := map[string]Entity{}
	for _, p := range plan {
		if p.Object.Target == "entity" && !rawNull(p.Merged) {
			var e Entity
			if err := json.Unmarshal(p.Merged, &e); err != nil {
				return err
			}
			e.State = "active"
			plannedEntities[e.ID] = e
		}
	}
	ctx = context.WithValue(ctx, collaborationReferencesKey{}, plannedEntities)
	identityKeys := map[string]bool{}
	for _, p := range plan {
		if p.Object.Target != "entity" {
			continue
		}
		for _, raw := range []json.RawMessage{p.Current, p.Merged} {
			if rawNull(raw) {
				continue
			}
			var e Entity
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			if err := addIdentityWriteKeys(ctx, tx, e, identityKeys); err != nil {
				return err
			}
		}
	}
	if err := lockIdentityWriteKeys(ctx, tx, identityKeys); err != nil {
		return err
	}
	if err := lockDefinitionsShared(ctx, tx); err != nil {
		return err
	}
	defs, err := definitions(ctx, tx)
	if err != nil {
		return err
	}
	releases := map[string]bool{}
	for _, p := range plan {
		if p.Object.Target != "entity" || rawNull(p.Merged) {
			continue
		}
		var e Entity
		_ = json.Unmarshal(p.Merged, &e)
		if e.ReleaseID != "" {
			releases[e.ReleaseID] = true
		}
		if e.Kind == "release" && !rawNull(p.Current) {
			releases[e.ID] = true
		}
		if e.MediumID != "" {
			var rid string
			if err := tx.QueryRowContext(ctx, `SELECT release_id FROM catalog.mediums WHERE id=$1`, e.MediumID).Scan(&rid); err == nil {
				releases[rid] = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
	}
	for _, rid := range sortedMapKeys(releases) {
		if err := lockRelease(ctx, tx, rid); err != nil {
			return err
		}
	}
	plan, err = workspacePlanOrder(plan)
	if err != nil {
		return err
	}
	for _, p := range plan {
		if workspaceSameFact(p.Current, p.Merged, p.Object.Target) {
			continue
		}
		if !u.Can(PermissionReviewManage) {
			if p.Object.Target == "relation" && !u.Can(PermissionRelationEdit) {
				return errForbidden
			}
			if p.Object.Target == "entity" {
				if !u.Can(PermissionEntityEdit) {
					var e Entity
					_ = json.Unmarshal(p.Current, &e)
					if rawNull(p.Current) || !canEditEntity(u, e) {
						return errForbidden
					}
				}
			}
		}
		contributors := []CollaborationCommit{}
		for _, c := range commits {
			if commitContributesToObject(c, p) {
				contributors = append(contributors, c)
			}
		}
		if len(contributors) == 0 {
			return fmt.Errorf("invalid_commit_provenance")
		}
		author := contributors[len(contributors)-1].Author
		if rawNull(p.Current) && !rawNull(p.Merged) {
			for _, c := range contributors {
				created := false
				for _, co := range c.Changes {
					if co.ID == p.Object.ID && co.Target == p.Object.Target && rawNull(co.Before) && !rawNull(co.After) {
						author = c.Author
						created = true
						break
					}
				}
				if created {
					break
				}
			}
		}
		// Authorization belongs to the current merger. Audit attribution belongs
		// to the actual contributing author; old permission snapshots are never
		// restored or trusted as a capability.
		actor := u
		if u.Can(PermissionReviewManage) {
			actor.Permissions = append(append([]string{}, u.Permissions...), PermissionEntityEdit, PermissionRelationEdit)
			actor.FromPAT = false
		}
		writeCtx := context.WithValue(ctx, collaborationAttributionKey{}, author)
		origin := contributors[len(contributors)-1]
		if _, err = tx.ExecContext(ctx, `SELECT set_config('metafusion.commit_id',$1,true)`, origin.ID); err != nil {
			return err
		}
		if p.Object.Target == "entity" {
			if rawNull(p.Merged) {
				return fmt.Errorf("use_lifecycle_endpoint")
			}
			var e Entity
			if err = json.Unmarshal(p.Merged, &e); err != nil {
				return err
			}
			input := Edit{Entity: e, ExpectedVersion: p.CurrentVersion, EditNote: origin.Message, definitions: &defs, identityLocksHeld: true}
			if rawNull(p.Current) {
				input.Entity.ID = ""
				input.Entity.Version = 0
				input.Entity.State = ""
				input.Entity.ReviewStatus = ""
				input.Entity.CreatedBy = ""
				input.Entity.RedirectID = ""
				input.createID = p.Object.ID
				input.ExpectedVersion = 0
			}
			if _, err = s.saveEntityTx(writeCtx, tx, input, actor); err != nil {
				return err
			}
		} else if rawNull(p.Merged) {
			if err = s.deleteRelationTx(writeCtx, tx, p.Object.ID, p.CurrentVersion, origin.Message, nil, actor); err != nil {
				return err
			}
		} else {
			var r Relation
			if err = json.Unmarshal(p.Merged, &r); err != nil {
				return err
			}
			input := RelationEdit{Relation: r, ExpectedVersion: p.CurrentVersion, EditNote: origin.Message, definitions: &defs}
			if rawNull(p.Current) {
				input.Relation.ID = ""
				input.Relation.Version = 0
				input.createID = p.Object.ID
				input.ExpectedVersion = 0
			}
			if _, err = s.saveRelationTx(writeCtx, tx, input, actor); err != nil {
				return err
			}
		}
		var revisionID int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM catalog.revisions WHERE target_id=$1 ORDER BY version DESC,id DESC LIMIT 1`, p.Object.ID).Scan(&revisionID); err != nil {
			return err
		}
		for _, c := range contributors {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.revision_commits(revision_id,commit_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, revisionID, c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) mergeWorkspaceTx(ctx context.Context, tx *sql.Tx, w Workspace, pr *PullRequest, u User) (WorkspacePushResult, error) {
	out := WorkspacePushResult{Applied: true, Commits: []CollaborationCommit{}, PullRequest: pr}
	if w.DirtyCount > 0 {
		return out, fmt.Errorf("uncommitted_changes")
	}
	commits, err := workspaceCommitsFrom(ctx, tx, w.ID, true)
	if err != nil {
		return out, err
	}
	if len(commits) == 0 {
		return out, fmt.Errorf("no_changes")
	}
	plan, err := workspaceMergePlan(ctx, tx, w, u, nil)
	if err != nil {
		return out, err
	}
	if err = s.validateWorkspaceCoverAssets(ctx, tx, plan, commits, u); err != nil {
		return out, err
	}
	if err = s.applyWorkspacePlan(ctx, tx, w, plan, commits, u); err != nil {
		return out, err
	}
	var prid any
	if pr != nil {
		prid = pr.ID
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE catalog.commits SET merged_at=$2,merged_by=$3,pull_request_id=$4 WHERE workspace_id=$1 AND merged_at IS NULL`, w.ID, now, u.ID, prid); err != nil {
		return out, err
	}
	for _, p := range plan {
		current, version, err := workspaceMainObject(ctx, tx, p.Object.Target, p.Object.ID, u)
		if err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET base_version=$4,base_document=$5,head_document=$5,document=$5,updated_at=now() WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, w.ID, p.Object.Target, p.Object.ID, version, nullableJSON(current)); err != nil {
			return out, err
		}
	}
	if err = bumpWorkspace(ctx, tx, w.ID); err != nil {
		return out, err
	}
	if pr != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.pull_requests SET status='merged',merged_at=$2,merged_by=$3,version=version+1,updated_at=now() WHERE id=$1`, pr.ID, now, u.ID); err != nil {
			return out, err
		}
		p, err := pullRequestFrom(ctx, tx, pr.ID, u, false)
		if err != nil {
			return out, err
		}
		out.PullRequest = &p
		if err = notifyPullRequestDecision(ctx, tx, p, u, true); err != nil {
			return out, err
		}
	}
	canRead, err := workspaceReadable(ctx, tx, w.ID, u, false)
	if err != nil {
		return out, err
	}
	if canRead {
		current, err := workspaceFrom(ctx, tx, w.ID, u, false, false)
		if err != nil {
			return out, err
		}
		out.Workspace = &current
	}
	for _, c := range commits {
		cc, e := collaborationCommitFrom(ctx, tx, c.ID)
		if e != nil {
			return out, e
		}
		out.Commits = append(out.Commits, cc)
	}
	return out, nil
}
func workspacePushReplay(ctx context.Context, tx *sql.Tx, w Workspace, head string, u User) (WorkspacePushResult, bool, error) {
	if !collaborationID(head) {
		return WorkspacePushResult{}, false, nil
	}
	c, err := collaborationCommitFrom(ctx, tx, head)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspacePushResult{}, false, nil
	}
	if err != nil {
		return WorkspacePushResult{}, false, err
	}
	if c.WorkspaceID == nil || *c.WorkspaceID != w.ID || c.MergedAt == nil {
		return WorkspacePushResult{}, false, nil
	}
	out := WorkspacePushResult{Applied: true, Workspace: &w, Commits: []CollaborationCommit{c}}
	if c.PullRequestID != nil {
		p, e := pullRequestFrom(ctx, tx, *c.PullRequestID, u, false)
		if e != nil {
			return out, false, e
		}
		out.PullRequest = &p
	}
	return out, true, nil
}
func (s *Store) PushWorkspace(ctx context.Context, id string, in WorkspacePush, u User) (WorkspacePushResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out WorkspacePushResult
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		replay, ok, err := workspacePushReplay(ctx, tx, w, in.ExpectedHead, u)
		if err != nil {
			return err
		}
		if ok {
			out = replay
			return nil
		}
		if err = workspaceAuthorizeSubmission(ctx, tx, w, u, false); err != nil {
			return err
		}
		if !u.CanBypassReview() && collaborationID(in.ExpectedHead) {
			var pid string
			err = tx.QueryRowContext(ctx, `SELECT id::text FROM catalog.pull_requests WHERE workspace_id=$1 AND source_head_id=$2 AND status='open'`, id, in.ExpectedHead).Scan(&pid)
			if err == nil {
				p, e := pullRequestFrom(ctx, tx, pid, u, false)
				if e != nil {
					return e
				}
				if in.Title == "" || strings.TrimSpace(in.Title) == p.Title {
					out = WorkspacePushResult{Workspace: &w, Commits: p.Commits, PullRequest: &p}
					return nil
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if !u.CanBypassReview() {
			p, err := createPullRequestTx(ctx, tx, w, in, u)
			if err != nil {
				return err
			}
			commits, err := workspaceCommitsFrom(ctx, tx, w.ID, true)
			if err != nil {
				return err
			}
			out = WorkspacePushResult{Workspace: &w, Commits: commits, PullRequest: &p}
			return nil
		}
		var pr *PullRequest
		var pid string
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM catalog.pull_requests WHERE workspace_id=$1 AND status='open'`, id).Scan(&pid)
		if err == nil {
			p, e := pullRequestFrom(ctx, tx, pid, u, true)
			if e != nil {
				return e
			}
			if p.SourceHeadID != in.ExpectedHead {
				if _, e = tx.ExecContext(ctx, `UPDATE catalog.pull_requests SET source_head_id=$2,version=version+1,updated_at=now() WHERE id=$1`, p.ID, in.ExpectedHead); e != nil {
					return e
				}
				p.SourceHeadID = in.ExpectedHead
			}
			pr = &p
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out, err = s.mergeWorkspaceTx(ctx, tx, w, pr, u)
		return err
	})
	return out, err
}
func (s *Store) MergePullRequest(ctx context.Context, id string, in PullRequestMerge, u User) (WorkspacePushResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out WorkspacePushResult
	if !u.Can(PermissionReviewManage) {
		return out, errForbidden
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		p, err := pullRequestFrom(ctx, tx, id, u, false)
		if err != nil {
			return err
		}
		w, err := workspaceFrom(ctx, tx, p.WorkspaceID, u, true, false)
		if err != nil {
			return err
		}
		p, err = pullRequestFrom(ctx, tx, id, u, true)
		if err != nil {
			return err
		}
		if p.Status == "merged" && p.SourceHeadID == in.ExpectedHead {
			out = WorkspacePushResult{Applied: true, Commits: p.Commits, PullRequest: &p}
			member, err := workspaceReadable(ctx, tx, w.ID, u, false)
			if err != nil {
				return err
			}
			if member {
				out.Workspace = &w
			}
			return nil
		}
		if p.Status != "open" {
			return fmt.Errorf("pull_request_closed")
		}
		if p.Version != in.ExpectedVersion {
			return fmt.Errorf("pull_request_version_conflict")
		}
		if p.SourceHeadID != in.ExpectedHead || w.HeadCommitID == nil || *w.HeadCommitID != p.SourceHeadID {
			return fmt.Errorf("workspace_head_conflict")
		}
		if w.State != "active" {
			return fmt.Errorf("workspace_archived")
		}
		out, err = s.mergeWorkspaceTx(ctx, tx, w, &p, u)
		return err
	})
	return out, err
}
func (s *Store) RebaseWorkspace(ctx context.Context, id string, in WorkspaceRebase, u User) (WorkspaceRebaseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out WorkspaceRebaseResult
	if !collaborationID(in.ID) {
		return out, fmt.Errorf("invalid_commit_id")
	}
	message := strings.TrimSpace(in.Message)
	if message == "" {
		message = "Rebase"
	}
	if len([]rune(message)) > 2000 {
		return out, fmt.Errorf("invalid_commit_message")
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		var hash, actor, wid string
		err = tx.QueryRowContext(ctx, `SELECT request_hash,actor_id,workspace_id::text FROM catalog.commits WHERE id=$1`, in.ID).Scan(&hash, &actor, &wid)
		if err == nil {
			if hash != requestHash(in) || actor != u.ID || wid != id {
				return errIdempotencyConflict
			}
			c, err := collaborationCommitFrom(ctx, tx, in.ID)
			if err != nil {
				return err
			}
			out = WorkspaceRebaseResult{w, &c}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if w.DirtyCount > 0 {
			return fmt.Errorf("uncommitted_changes")
		}
		if w.HeadCommitID == nil || in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if u.FromPAT && !u.Can(PermissionEntityEdit) && !u.Can(PermissionRelationEdit) {
			return errForbidden
		}
		for _, resolution := range in.Resolutions {
			found := false
			for _, o := range w.Objects {
				if o.Target == resolution.Target && o.ID == resolution.ID {
					raw := o.Committed
					if rawNull(raw) {
						raw = o.Base
					}
					if err = workspaceAuthorizeObject(ctx, tx, id, o.Target, raw, u); err != nil {
						return err
					}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("invalid_resolution")
			}
		}
		plan, err := workspaceMergePlan(ctx, tx, w, u, in.Resolutions)
		if err != nil {
			return err
		}
		for _, p := range plan {
			if p.Object.Target == "entity" {
				if err = s.reserveWorkspaceCoverAssets(ctx, id, p.Object.Committed, p.Merged, u); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET base_version=$4,base_document=$5,document=$6,updated_at=now() WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, id, p.Object.Target, p.Object.ID, p.CurrentVersion, nullableJSON(p.Current), nullableJSON(p.Merged)); err != nil {
				return err
			}
		}
		w, err = workspaceFrom(ctx, tx, id, u, false, false)
		if err != nil {
			return err
		}
		kind := "rebase"
		if len(in.Resolutions) > 0 {
			kind = "edit"
		}
		c, err := insertWorkspaceCommit(ctx, tx, w, in.ID, message, kind, in, u)
		if err != nil {
			return err
		}
		out.Commit = &c
		out.Workspace, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}

func notifyPullRequestDecision(ctx context.Context, tx *sql.Tx, p PullRequest, actor User, approved bool) error {
	typ := NotificationReviewRejected
	if approved {
		typ = NotificationReviewApproved
	}
	recipients := map[string]bool{p.AuthorID: true}
	for _, c := range p.Commits {
		recipients[c.Author.ID] = true
		for _, a := range c.Authors {
			recipients[a.ID] = true
		}
	}
	for recipient := range recipients {
		if recipient == "" || recipient == actor.ID {
			continue
		}
		if err := notify(ctx, tx, NotificationInput{RecipientID: recipient, Type: typ, ActorID: actor.ID, ActorName: actor.Username, SubjectType: "pull_request", SubjectID: p.ID, Payload: map[string]any{"pull_request_id": p.ID}, DedupeKey: typ + ":pull_request:" + p.ID}); err != nil {
			return err
		}
	}
	return nil
}
