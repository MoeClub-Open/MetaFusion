package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type versionCommitMetaKey struct{}
type versionRestoreKey struct{}
type versionAssetSourceKey struct{}
type versionAssetSourcesKey struct{}
type versionCommitMeta struct {
	PrimaryAuthor   *CollaborationAuthor
	Authors         []CollaborationAuthor
	PreserveAuthors bool
	Operation       string
	Sources         []string
	MergeParent     *string
	PreviousHead    *string
	Rewriting       bool
}

func freezePullRequestCommits(ctx context.Context, tx *sql.Tx, id, wid string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog.pull_request_commits WHERE pull_request_id=$1`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog.pull_request_commits(pull_request_id,commit_id,position) SELECT $1,commit_id,position FROM catalog.workspace_commits WHERE workspace_id=$2 AND active AND NOT integrated`, id, wid)
	return err
}

func versionCommitReadable(ctx context.Context, q queryer, id string, u User) (bool, error) {
	if !collaborationID(id) {
		return false, nil
	}
	var allowed bool
	if collaborationUser(u) == nil {
		err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.workspace_commits wc JOIN catalog.workspaces w ON w.id=wc.workspace_id WHERE wc.commit_id=$1 AND (w.owner_id=$2 OR EXISTS(SELECT 1 FROM catalog.workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$2))) OR EXISTS(SELECT 1 FROM catalog.pull_request_commits pc JOIN catalog.pull_requests p ON p.id=pc.pull_request_id WHERE pc.commit_id=$1 AND p.status<>'merged' AND $3::boolean)`, id, u.ID, u.Can(PermissionReviewManage)).Scan(&allowed)
		if err != nil || allowed {
			return allowed, err
		}
	}
	err := q.QueryRowContext(ctx, `SELECT (`+collaborationCommitPublicPredicate+`) OR (c.snapshot_scope='catalog' AND ($2::boolean OR EXISTS(SELECT 1 FROM catalog.commit_operations op WHERE op.commit_id=c.id AND op.operation='baseline'))) FROM catalog.commits c WHERE c.id=$1`, id, u.Can(PermissionLifecycleManage)).Scan(&allowed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return allowed, err
}

func visibleVersionLinks(ctx context.Context, q queryer, c *CollaborationCommit, u User) error {
	if c.SnapshotScope == "catalog" {
		if err := visibleVersionChanges(ctx, q, c, u); err != nil {
			return err
		}
	}
	for _, link := range []**string{&c.ParentID, &c.MergeParentID} {
		if *link != nil {
			allowed, err := versionCommitReadable(ctx, q, **link, u)
			if err != nil {
				return err
			}
			if !allowed {
				*link = nil
			}
		}
	}
	for _, list := range []*[]string{&c.SourceIDs, &c.ReplacedBy} {
		visible := []string{}
		for _, id := range *list {
			allowed, err := versionCommitReadable(ctx, q, id, u)
			if err != nil {
				return err
			}
			if allowed {
				visible = append(visible, id)
			}
		}
		*list = visible
	}
	return nil
}

func versionSnapshotFrom(ctx context.Context, q queryer, id string) ([]WorkspaceObject, error) {
	rows, err := q.QueryContext(ctx, `SELECT target,target_id::text,base_version,COALESCE(base_document,'null'::jsonb),COALESCE(document,'null'::jsonb) FROM catalog.commit_snapshots WHERE commit_id=$1 ORDER BY target,target_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceObject{}
	for rows.Next() {
		var o WorkspaceObject
		if err = rows.Scan(&o.Target, &o.ID, &o.BaseVersion, &o.Base, &o.Document); err != nil {
			return nil, err
		}
		o.Committed = o.Document
		out = append(out, o)
	}
	return out, rows.Err()
}

// A public commit's changed records do not grant access to unrelated records
// tracked by its branch. Verify the complete snapshot before returning totals.
func readableVersionSnapshot(ctx context.Context, q queryer, id string, u User) ([]WorkspaceObject, string, error) {
	allowed, err := versionCommitReadable(ctx, q, id, u)
	if err != nil {
		return nil, "", err
	}
	if !allowed {
		return nil, "", sql.ErrNoRows
	}
	var scope string
	if err = q.QueryRowContext(ctx, `SELECT snapshot_scope FROM catalog.commits WHERE id=$1`, id).Scan(&scope); err != nil {
		return nil, "", err
	}
	objects, err := versionSnapshotFrom(ctx, q, id)
	if err != nil {
		return nil, "", err
	}
	private := false
	if collaborationUser(u) == nil {
		err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.workspace_commits wc JOIN catalog.workspaces w ON w.id=wc.workspace_id WHERE wc.commit_id=$1 AND (w.owner_id=$2 OR EXISTS(SELECT 1 FROM catalog.workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$2))) OR ($3::boolean AND EXISTS(SELECT 1 FROM catalog.pull_request_commits pc JOIN catalog.pull_requests p ON p.id=pc.pull_request_id WHERE pc.commit_id=$1 AND p.status<>'merged'))`, id, u.ID, u.Can(PermissionReviewManage)).Scan(&private)
		if err != nil {
			return nil, "", err
		}
	}
	if !private {
		ids := []string{}
		for _, o := range objects {
			if o.Target == "entity" {
				ids = append(ids, o.ID)
			} else {
				raw := o.Document
				if rawNull(raw) {
					raw = o.Base
				}
				var r Relation
				if json.Unmarshal(raw, &r) != nil {
					return nil, "", sql.ErrNoRows
				}
				ids = append(ids, r.SourceID, r.TargetID)
			}
		}
		entities, err := entityDocuments(ctx, q, ids)
		if err != nil {
			return nil, "", err
		}
		for _, id := range ids {
			if e, ok := entities[id]; !ok || !visible(e, &u) {
				return nil, "", sql.ErrNoRows
			}
		}
		if err = visibleVersionObjects(ctx, q, objects, u); err != nil {
			return nil, "", err
		}
	}
	return objects, scope, nil
}

func (s *Store) GetCommitSnapshot(ctx context.Context, id string, query VersionReadQuery, u User) (CommitSnapshot, error) {
	query, queryErr := query.normalized()
	if queryErr != nil {
		return CommitSnapshot{}, queryErr
	}
	limit, offset := query.Limit, query.Offset
	out := CommitSnapshot{CommitID: id, Limit: limit, Offset: offset, Objects: []WorkspaceObject{}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var scope string
	if !collaborationID(id) {
		return out, sql.ErrNoRows
	}
	if err = tx.QueryRowContext(ctx, `SELECT snapshot_scope FROM catalog.commits WHERE id=$1`, id).Scan(&scope); err != nil {
		return out, err
	}
	if scope == "catalog" {
		out, err = catalogVersionSnapshotPage(ctx, tx, id, query, u)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	objects, scope, err := readableVersionSnapshot(ctx, tx, id, u)
	if err != nil {
		return out, err
	}
	filtered := []WorkspaceObject{}
	for _, o := range objects {
		if query.matches(o.Target, o.ID) {
			filtered = append(filtered, o)
		}
	}
	objects = filtered
	out.Scope = scope
	out.Total = int64(len(objects))
	if offset < len(objects) {
		out.Objects = objects[offset:min(offset+limit, len(objects))]
	}
	return out, tx.Commit()
}

func (s *Store) CompareVersions(ctx context.Context, base, head string, query VersionReadQuery, u User) (VersionComparison, error) {
	query, queryErr := query.normalized()
	if queryErr != nil {
		return VersionComparison{}, queryErr
	}
	limit, offset := query.Limit, query.Offset
	out := VersionComparison{BaseID: base, HeadID: head, Changes: []CommitObject{}, Limit: limit, Offset: offset}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if !collaborationID(base) || !collaborationID(head) {
		return out, sql.ErrNoRows
	}
	var leftScope, rightScope string
	if err = tx.QueryRowContext(ctx, `SELECT a.snapshot_scope,b.snapshot_scope FROM catalog.commits a,catalog.commits b WHERE a.id=$1 AND b.id=$2`, base, head).Scan(&leftScope, &rightScope); err != nil {
		return out, err
	}
	if leftScope == "catalog" && rightScope == "catalog" {
		out, err = compareCatalogVersions(ctx, tx, base, head, query, u)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	var a, b []WorkspaceObject
	if leftScope != "catalog" {
		a, _, err = readableVersionSnapshot(ctx, tx, base, u)
		if err != nil {
			return out, err
		}
	}
	if rightScope != "catalog" {
		b, _, err = readableVersionSnapshot(ctx, tx, head, u)
		if err != nil {
			return out, err
		}
	}
	if leftScope == "catalog" {
		a, err = catalogVersionObjects(ctx, tx, base, b, u)
	}
	if rightScope == "catalog" && err == nil {
		b, err = catalogVersionObjects(ctx, tx, head, a, u)
	}
	if err != nil {
		return out, err
	}
	left, right := map[string]WorkspaceObject{}, map[string]WorkspaceObject{}
	for _, o := range a {
		left[o.Target+":"+o.ID] = o
	}
	for _, o := range b {
		right[o.Target+":"+o.ID] = o
	}
	keys := map[string]bool{}
	for k := range left {
		keys[k] = true
	}
	for k := range right {
		keys[k] = true
	}
	for _, key := range sortedMapKeys(keys) {
		l, lok := left[key]
		r, rok := right[key]
		if !lok {
			l = r
			l.Document = nil
		}
		if !rok {
			r = l
			r.Document = nil
		}
		if query.matches(r.Target, r.ID) && !equalDocument(l.Document, r.Document) {
			out.Changes = append(out.Changes, CommitObject{Target: r.Target, ID: r.ID, BaseVersion: l.BaseVersion, Base: l.Base, Before: l.Document, After: r.Document})
		}
	}
	out.Total = int64(len(out.Changes))
	if offset >= len(out.Changes) {
		out.Changes = []CommitObject{}
	} else {
		out.Changes = out.Changes[offset:min(offset+limit, len(out.Changes))]
	}
	return out, tx.Commit()
}

func (s *Store) VersionHistory(ctx context.Context, wid string, query VersionHistoryQuery, u User) (CommitPage, error) {
	query, queryErr := query.normalized(true)
	if queryErr != nil {
		return CommitPage{}, queryErr
	}
	limit, offset := query.Limit, query.Offset
	out := CommitPage{Items: []CollaborationCommit{}, Limit: limit, Offset: offset}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, wid, u, false)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, sql.ErrNoRows
	}
	args := []any{wid}
	state := "wc.active"
	switch query.State {
	case "pending":
		state = "wc.active AND NOT wc.integrated AND c.merged_at IS NULL"
	case "integrated":
		state = "wc.active AND (wc.integrated OR c.merged_at IS NOT NULL)"
	case "replaced":
		state = "NOT wc.active"
	case "all":
		state = "true"
	}
	where := ` FROM catalog.workspace_commits wc JOIN catalog.commits c ON c.id=wc.commit_id WHERE wc.workspace_id=$1 AND (` + state + `)` + query.sqlFilters(&args, "c.created_at")
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, limit, offset)
	ids, err := collaborationIDs(ctx, tx, `SELECT c.id::text`+where+fmt.Sprintf(` ORDER BY wc.position DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		c, err := collaborationCommitFrom(ctx, tx, id)
		if err != nil {
			return out, err
		}
		if err = visibleVersionLinks(ctx, tx, &c, u); err != nil {
			return out, err
		}
		out.Items = append(out.Items, c)
	}
	return out, tx.Commit()
}

func (s *Store) WorkspaceEvents(ctx context.Context, wid string, limit, offset int, u User) (WorkspaceEventPage, error) {
	limit, offset = collaborationPage(limit, offset)
	out := WorkspaceEventPage{Items: []WorkspaceEvent{}, Limit: limit, Offset: offset}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, wid, u, false)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, sql.ErrNoRows
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog.workspace_events WHERE workspace_id=$1`, wid).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,request_id::text,actor_id,action,old_head::text,new_head::text,detail,created_at FROM catalog.workspace_events WHERE workspace_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, wid, limit, offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e WorkspaceEvent
		if err = rows.Scan(&e.ID, &e.RequestID, &e.ActorID, &e.Action, &e.OldHead, &e.NewHead, &e.Detail, &e.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func versionMessage(value, operation string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = operation
	}
	if len([]rune(value)) > 2000 {
		return "", fmt.Errorf("invalid_commit_message")
	}
	return value, nil
}

func (s *Store) WorkspaceVersionStatus(ctx context.Context, wid string, u User) (WorkspaceStatus, error) {
	out := WorkspaceStatus{Conflicts: []WorkspaceConflict{}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, wid, u, false)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, sql.ErrNoRows
	}
	w, err := workspaceFrom(ctx, tx, wid, u, false, false)
	if err != nil {
		return out, err
	}
	out.Version = w.Version
	out.Head = w.HeadCommitID
	out.DirtyCount = w.DirtyCount
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog.workspace_commits WHERE workspace_id=$1 AND active AND NOT integrated`, wid).Scan(&out.PendingCommits); err != nil {
		return out, err
	}
	_, err = workspaceMergePlan(ctx, tx, w, u, nil)
	var conflict *workspaceConflictError
	if errors.As(err, &conflict) {
		out.Conflicts = conflict.Conflicts
	} else if err != nil {
		return out, err
	}
	out.Mergeable = len(out.Conflicts) == 0 && w.DirtyCount == 0 && out.PendingCommits > 0 && w.State == "active"
	return out, tx.Commit()
}

func (s *Store) VersionFieldAuthor(ctx context.Context, head, target, id, pointer string, u User) (CollaborationCommit, error) {
	if !contains([]string{"entity", "relation"}, target) || !collaborationID(id) {
		return CollaborationCommit{}, fmt.Errorf("invalid_version_target")
	}
	parts, err := pointerParts(pointer)
	if err != nil {
		return CollaborationCommit{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return CollaborationCommit{}, err
	}
	defer tx.Rollback()
	if _, _, err = versionTreeForTargets(ctx, tx, head, []WorkspaceObject{{Target: target, ID: id}}, u); err != nil {
		return CollaborationCommit{}, err
	}
	ids, err := collaborationIDs(ctx, tx, `WITH RECURSIVE reachable(id) AS(SELECT id FROM catalog.commits WHERE id=$1
 UNION SELECT p.id FROM reachable r JOIN catalog.commits c ON c.id=r.id CROSS JOIN LATERAL(VALUES(c.parent_id),(c.merge_parent_id)) p(id) WHERE p.id IS NOT NULL)
 SELECT c.id::text FROM reachable r JOIN catalog.commits c ON c.id=r.id JOIN catalog.commit_objects o ON o.commit_id=c.id
 WHERE o.target=$2 AND o.target_id=$3 AND c.kind='edit' ORDER BY c.created_at DESC,c.id DESC`, head, target, id)
	if err != nil {
		return CollaborationCommit{}, err
	}
	for _, cid := range ids {
		allowed, err := versionCommitReadable(ctx, tx, cid, u)
		if err != nil {
			return CollaborationCommit{}, err
		}
		if !allowed {
			continue
		}
		c, err := collaborationCommitFrom(ctx, tx, cid)
		if err != nil {
			return c, err
		}
		for _, co := range c.Changes {
			if co.Target != target || co.ID != id {
				continue
			}
			a, aok, _ := pointerValue(commitMap(co.Before), parts)
			b, bok, _ := pointerValue(commitMap(co.After), parts)
			if !equalCommitValue(a, aok, b, bok) {
				if err = visibleVersionLinks(ctx, tx, &c, u); err != nil {
					return c, err
				}
				return c, tx.Commit()
			}
		}
	}
	return CollaborationCommit{}, sql.ErrNoRows
}
