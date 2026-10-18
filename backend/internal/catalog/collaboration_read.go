package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

func collaborationPage(limit, offset int) (int, int) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func workspaceReadable(ctx context.Context, q queryer, id string, u User, reviewer bool) (bool, error) {
	if collaborationUser(u) != nil || !collaborationID(id) {
		return false, nil
	}
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.workspaces w
		WHERE w.id=$1 AND (w.owner_id=$2 OR $3::boolean OR EXISTS(
			SELECT 1 FROM catalog.workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$2)))`,
		id, u.ID, reviewer && u.Can(PermissionReviewManage)).Scan(&allowed)
	return allowed, err
}

func (s *Store) GetWorkspace(ctx context.Context, id string, u User) (Workspace, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, id, u, false)
	if err != nil {
		return Workspace{}, err
	}
	if !allowed {
		return Workspace{}, sql.ErrNoRows
	}
	w, err := workspaceFrom(ctx, tx, id, u, false, false)
	if err != nil {
		return Workspace{}, err
	}
	return w, tx.Commit()
}

func workspacePageFrom(ctx context.Context, q queryer, limit, offset int, u User) (WorkspacePage, error) {
	out := WorkspacePage{Items: []Workspace{}, Limit: limit, Offset: offset}
	if err := collaborationUser(u); err != nil {
		return out, err
	}
	where := ` FROM catalog.workspaces w WHERE w.owner_id=$1 OR EXISTS(
		SELECT 1 FROM catalog.workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$1)`
	if err := q.QueryRowContext(ctx, `SELECT count(*)`+where, u.ID).Scan(&out.Total); err != nil {
		return out, err
	}
	query := `SELECT w.id::text` + where + ` ORDER BY w.updated_at DESC,w.id DESC`
	args := []any{u.ID}
	if limit > 0 {
		query += ` LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	}
	ids, err := collaborationIDs(ctx, q, query, args...)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		w, err := workspaceFrom(ctx, q, id, u, false, false)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, w)
	}
	return out, nil
}

func (s *Store) ListWorkspacePage(ctx context.Context, limit, offset int, u User) (WorkspacePage, error) {
	limit, offset = collaborationPage(limit, offset)
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return WorkspacePage{}, err
	}
	defer tx.Rollback()
	out, err := workspacePageFrom(ctx, tx, limit, offset, u)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Internal callers that need every accessible workspace must not silently use
// an HTTP page. The public endpoint always calls ListWorkspacePage instead.
func (s *Store) ListWorkspaces(ctx context.Context, u User) ([]Workspace, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := workspacePageFrom(ctx, tx, 0, 0, u)
	if err != nil {
		return nil, err
	}
	return out.Items, tx.Commit()
}

func collaborationIDs(ctx context.Context, q queryer, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func collaborationCommitFrom(ctx context.Context, q queryer, id string) (CollaborationCommit, error) {
	var c CollaborationCommit
	err := q.QueryRowContext(ctx, `SELECT id::text,workspace_id::text,parent_id::text,actor_id,
		COALESCE(NULLIF(author_snapshot->>'username',''),actor_id),message,kind,created_at,
		merged_at,merged_by,pull_request_id::text FROM catalog.commits WHERE id=$1`, id).
		Scan(&c.ID, &c.WorkspaceID, &c.ParentID, &c.Author.ID, &c.Author.Name, &c.Message,
			&c.Kind, &c.CreatedAt, &c.MergedAt, &c.MergedBy, &c.PullRequestID)
	if err != nil {
		return c, err
	}
	c.Authors = []CollaborationAuthor{}
	rows, err := q.QueryContext(ctx, `SELECT user_id,COALESCE(NULLIF(name,''),user_id)
		FROM catalog.commit_authors WHERE commit_id=$1 ORDER BY user_id`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var a CollaborationAuthor
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			rows.Close()
			return c, err
		}
		c.Authors = append(c.Authors, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	c.Changes = []CommitObject{}
	rows, err = q.QueryContext(ctx, `SELECT target,target_id::text,base_version,
		COALESCE(base_document,'null'::jsonb),COALESCE(before_document,'null'::jsonb),COALESCE(after_document,'null'::jsonb)
		FROM catalog.commit_objects WHERE commit_id=$1 ORDER BY target,target_id`, id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var o CommitObject
		if err := rows.Scan(&o.Target, &o.ID, &o.BaseVersion, &o.Base, &o.Before, &o.After); err != nil {
			rows.Close()
			return c, err
		}
		c.Changes = append(c.Changes, o)
	}
	err = rows.Err()
	rows.Close()
	return c, err
}

func workspaceCommitsFrom(ctx context.Context, q queryer, id string, pendingOnly bool) ([]CollaborationCommit, error) {
	query := `SELECT id::text FROM catalog.commits WHERE workspace_id=$1`
	if pendingOnly {
		query += ` AND merged_at IS NULL`
	}
	ids, err := collaborationIDs(ctx, q, query+` ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	out := []CollaborationCommit{}
	for _, id := range ids {
		c, err := collaborationCommitFrom(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// The same predicate protects detail reads and list totals. A historical
// commit without retained objects is public only when its actual revision
// links prove every affected target. Unknown history is never guessed public.
// Both sides of relation changes must remain public, including deletions.
// Keep UUID columns indexed. Historical JSON/text IDs must be canonical before
// casting, so malformed snapshots remain private without aborting the query.
const collaborationCommitPublicPredicate = `c.merged_at IS NOT NULL AND COALESCE((
	SELECT bool_and(CASE WHEN o.target='entity' THEN
		EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=CASE
			WHEN o.target_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN o.target_id::uuid END AND e.state='active')
		AND NOT EXISTS(SELECT 1 FROM (VALUES(o.base_document),(o.before_document),(o.after_document)) v(document)
			CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(v.document->'contents')='array'
				THEN v.document->'contents' ELSE '[]'::jsonb END) inclusion
			WHERE v.document->>'kind'='track' AND NOT EXISTS(SELECT 1 FROM catalog.entities e
				WHERE e.id=CASE WHEN inclusion->>'expression_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
					THEN (inclusion->>'expression_id')::uuid END AND e.kind='expression' AND e.state='active'))
	WHEN o.target='relation' THEN
		(o.base_document IS NOT NULL OR o.before_document IS NOT NULL OR o.after_document IS NOT NULL) AND NOT EXISTS(
			SELECT 1 FROM (VALUES(o.base_document),(o.before_document),(o.after_document)) v(document)
			WHERE v.document IS NOT NULL AND NOT (
				EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=CASE WHEN v.document->>'source_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
					THEN (v.document->>'source_id')::uuid END AND e.state='active')
				AND EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=CASE WHEN v.document->>'target_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
					THEN (v.document->>'target_id')::uuid END AND e.state='active')))
	ELSE false END)
	FROM (
		SELECT target,target_id::text,base_document,before_document,after_document FROM catalog.commit_objects WHERE commit_id=c.id
		UNION ALL
		SELECT CASE WHEN r.snapshot->>'kind' IN ('agent','collection','work','content_unit','expression','release','medium','track') THEN 'entity'
			WHEN r.snapshot ? 'source_id' AND r.snapshot ? 'target_id' AND r.snapshot ? 'type' THEN 'relation' ELSE '' END,
			r.target_id,NULL::jsonb,r.snapshot,NULL::jsonb
		FROM catalog.revision_commits rc JOIN catalog.revisions r ON r.id=rc.revision_id
		WHERE rc.commit_id=c.id AND NOT EXISTS(SELECT 1 FROM catalog.commit_objects
			WHERE commit_id=c.id AND target_id=CASE WHEN r.target_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
				THEN r.target_id::uuid END)
	) o
),false)`

const collaborationPullRequestPublicPredicate = `p.status='merged'
	AND EXISTS(SELECT 1 FROM catalog.commits c WHERE c.pull_request_id=p.id)
	AND NOT EXISTS(SELECT 1 FROM catalog.commits c WHERE c.pull_request_id=p.id AND NOT (` + collaborationCommitPublicPredicate + `))`

func collaborationCommitPublic(ctx context.Context, q queryer, id string) (bool, error) {
	var public bool
	err := q.QueryRowContext(ctx, `SELECT `+collaborationCommitPublicPredicate+` FROM catalog.commits c WHERE c.id=$1`, id).Scan(&public)
	return public, err
}

func reviewerCanReadCommit(ctx context.Context, q queryer, id, workspaceID string, u User) (bool, error) {
	if collaborationUser(u) != nil || !u.Can(PermissionReviewManage) {
		return false, nil
	}
	var allowed bool
	err := q.QueryRowContext(ctx, `WITH RECURSIVE submitted AS (
		SELECT p.id AS pr_id,p.source_base_id,c.id,c.parent_id,ARRAY[c.id] AS path
		FROM catalog.pull_requests p JOIN catalog.commits c ON c.id=p.source_head_id
		WHERE p.workspace_id=$2 AND c.workspace_id=p.workspace_id
			AND (p.source_base_id IS NULL OR c.id<>p.source_base_id)
		UNION ALL
		SELECT submitted.pr_id,submitted.source_base_id,c.id,c.parent_id,submitted.path||c.id
		FROM submitted JOIN catalog.commits c ON c.id=submitted.parent_id
		WHERE c.workspace_id=$2 AND (submitted.source_base_id IS NULL OR c.id<>submitted.source_base_id)
			AND NOT c.id=ANY(submitted.path)
	) SELECT EXISTS(SELECT 1 FROM submitted WHERE id=$1)`, id, workspaceID).Scan(&allowed)
	return allowed, err
}

func (s *Store) GetCollaborationCommit(ctx context.Context, id string, u User) (CollaborationCommit, error) {
	if !collaborationID(id) {
		return CollaborationCommit{}, sql.ErrNoRows
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return CollaborationCommit{}, err
	}
	defer tx.Rollback()
	c, err := collaborationCommitFrom(ctx, tx, id)
	if err != nil {
		return CollaborationCommit{}, err
	}
	allowed := false
	if c.MergedAt != nil {
		allowed, err = collaborationCommitPublic(ctx, tx, id)
	} else if c.WorkspaceID != nil {
		allowed, err = workspaceReadable(ctx, tx, *c.WorkspaceID, u, false)
		if err == nil && !allowed {
			allowed, err = reviewerCanReadCommit(ctx, tx, id, *c.WorkspaceID, u)
		}
	} else {
		allowed = collaborationUser(u) == nil && c.Author.ID == u.ID
	}
	if err != nil {
		return CollaborationCommit{}, err
	}
	if !allowed {
		return CollaborationCommit{}, sql.ErrNoRows
	}
	return c, tx.Commit()
}

func (s *Store) WorkspaceCommits(ctx context.Context, id string, u User) ([]CollaborationCommit, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, id, u, false)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, sql.ErrNoRows
	}
	out, err := workspaceCommitsFrom(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func pullRequestCommitsFrom(ctx context.Context, q queryer, p PullRequest) ([]CollaborationCommit, error) {
	// The base is exclusive. Only immutable ancestors of the submitted head can
	// enter a review; later commits and uncommitted workspace changes are absent.
	ids, err := collaborationIDs(ctx, q, `WITH RECURSIVE chain AS (
		SELECT id,parent_id,workspace_id,0 AS depth,ARRAY[id] AS path FROM catalog.commits
		WHERE id=$1 AND workspace_id=$2 AND ($3::uuid IS NULL OR id<>$3)
		UNION ALL
		SELECT c.id,c.parent_id,c.workspace_id,chain.depth+1,chain.path||c.id
		FROM catalog.commits c JOIN chain ON c.id=chain.parent_id
		WHERE c.workspace_id=$2 AND ($3::uuid IS NULL OR c.id<>$3) AND NOT c.id=ANY(chain.path)
	)
	SELECT chain.id::text FROM chain JOIN catalog.commits c ON c.id=chain.id
	WHERE $4::text<>'merged' OR c.pull_request_id=$5::uuid ORDER BY chain.depth DESC`,
		p.SourceHeadID, p.WorkspaceID, p.SourceBaseID, p.Status, p.ID)
	if err != nil {
		return nil, err
	}
	out := []CollaborationCommit{}
	for _, id := range ids {
		c, err := collaborationCommitFrom(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func pullRequestChanges(commits []CollaborationCommit) []WorkspaceObject {
	byObject := map[string]WorkspaceObject{}
	for _, c := range commits {
		for _, co := range c.Changes {
			key := co.Target + ":" + co.ID
			o := byObject[key]
			o.Target, o.ID, o.BaseVersion, o.Base = co.Target, co.ID, co.BaseVersion, co.Base
			o.Committed, o.Document = co.After, co.After
			byObject[key] = o
		}
	}
	out := []WorkspaceObject{}
	for _, key := range sortedMapKeys(byObject) {
		o := byObject[key]
		if !equalDocument(o.Base, o.Committed) {
			out = append(out, o)
		}
	}
	return out
}

func pullRequestFrom(ctx context.Context, q queryer, id string, u User, lock bool) (PullRequest, error) {
	var p PullRequest
	if !collaborationID(id) {
		return p, sql.ErrNoRows
	}
	query := `SELECT id::text,workspace_id::text,author_id,title,status,source_head_id::text,source_base_id::text,
		version,merged_at,merged_by,created_at,updated_at FROM catalog.pull_requests WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	if err := q.QueryRowContext(ctx, query, id).Scan(&p.ID, &p.WorkspaceID, &p.AuthorID, &p.Title,
		&p.Status, &p.SourceHeadID, &p.SourceBaseID, &p.Version, &p.MergedAt, &p.MergedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return PullRequest{}, err
	}
	var allowed bool
	var err error
	if p.Status == "merged" {
		err = q.QueryRowContext(ctx, `SELECT `+collaborationPullRequestPublicPredicate+` FROM catalog.pull_requests p WHERE p.id=$1`, id).Scan(&allowed)
	} else {
		allowed, err = workspaceReadable(ctx, q, p.WorkspaceID, u, true)
	}
	if err != nil {
		return PullRequest{}, err
	}
	if !allowed {
		return PullRequest{}, sql.ErrNoRows
	}
	p.Commits, err = pullRequestCommitsFrom(ctx, q, p)
	if err != nil {
		return PullRequest{}, err
	}
	p.Changes = pullRequestChanges(p.Commits)
	return p, nil
}

func (s *Store) GetPullRequest(ctx context.Context, id string, u User) (PullRequest, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return PullRequest{}, err
	}
	defer tx.Rollback()
	out, err := pullRequestFrom(ctx, tx, id, u, false)
	if err != nil {
		return PullRequest{}, err
	}
	return out, tx.Commit()
}

func pullRequestPageFrom(ctx context.Context, q queryer, status, workspaceID string, limit, offset int, u User) (PullRequestPage, error) {
	out := PullRequestPage{Items: []PullRequest{}, Limit: limit, Offset: offset}
	if status != "" && status != "open" && status != "merged" && status != "closed" {
		return out, fmt.Errorf("invalid_pull_request_status")
	}
	if workspaceID != "" && !collaborationID(workspaceID) {
		return out, fmt.Errorf("invalid_id")
	}
	firstParty := collaborationUser(u) == nil
	where := ` FROM catalog.pull_requests p JOIN catalog.workspaces w ON w.id=p.workspace_id
		WHERE ($4::text='' OR p.status=$4) AND ($5::text='' OR p.workspace_id::text=$5)
		AND ((` + collaborationPullRequestPublicPredicate + `) OR (p.status<>'merged' AND $3::boolean AND (
			$2::boolean OR w.owner_id=$1 OR EXISTS(SELECT 1 FROM catalog.workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$1))))`
	args := []any{u.ID, firstParty && u.Can(PermissionReviewManage), firstParty, status, workspaceID}
	if err := q.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	query := `SELECT p.id::text` + where + ` ORDER BY p.updated_at DESC,p.id DESC`
	if limit > 0 {
		query += ` LIMIT $6 OFFSET $7`
		args = append(args, limit, offset)
	}
	ids, err := collaborationIDs(ctx, q, query, args...)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		p, err := pullRequestFrom(ctx, q, id, u, false)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

func (s *Store) ListPullRequestPage(ctx context.Context, status, workspaceID string, limit, offset int, u User) (PullRequestPage, error) {
	limit, offset = collaborationPage(limit, offset)
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return PullRequestPage{}, err
	}
	defer tx.Rollback()
	out, err := pullRequestPageFrom(ctx, tx, status, workspaceID, limit, offset, u)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) ListPullRequests(ctx context.Context, u User) ([]PullRequest, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := pullRequestPageFrom(ctx, tx, "", "", 0, 0, u)
	if err != nil {
		return nil, err
	}
	return out.Items, tx.Commit()
}
