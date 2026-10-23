package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"
)

type CatalogVersionHead struct {
	CommitID string `json:"commit_id"`
	Sequence int64  `json:"sequence"`
}

func setCatalogVersionActor(ctx context.Context, tx *sql.Tx, u User, head *string, message string) error {
	value := ""
	if head != nil {
		value = *head
	}
	_, err := tx.ExecContext(ctx, `SELECT set_config('metafusion.main_actor',$1,true),set_config('metafusion.main_actor_name',$2,true),set_config('metafusion.main_source',$3,true),set_config('metafusion.main_message',$4,true)`, u.ID, u.Username, value, message)
	return err
}

func (s *Store) CatalogVersionHead(ctx context.Context) (CatalogVersionHead, error) {
	var out CatalogVersionHead
	err := s.DB.QueryRowContext(ctx, `SELECT h.commit_id::text,v.sequence FROM catalog.catalog_head h JOIN catalog.catalog_versions v ON v.commit_id=h.commit_id WHERE singleton`).Scan(&out.CommitID, &out.Sequence)
	return out, err
}

// Visibility is checked against current authoritative records, including both
// historical endpoints for deleted relations. It is applied before pagination.
const catalogSnapshotVisibleSQL = `(v.target='entity' AND EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=v.target_id AND (e.state='active' OR e.created_by::text=$2 OR $3::boolean))) OR
 (v.target='relation' AND EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=CASE WHEN v.document->>'source_id' ~ '^[0-9a-f-]{36}$' THEN (v.document->>'source_id')::uuid END AND (e.state='active' OR e.created_by::text=$2 OR $3::boolean))
 AND EXISTS(SELECT 1 FROM catalog.entities e WHERE e.id=CASE WHEN v.document->>'target_id' ~ '^[0-9a-f-]{36}$' THEN (v.document->>'target_id')::uuid END AND (e.state='active' OR e.created_by::text=$2 OR $3::boolean)))`

func catalogSnapshotQuery(filter bool) string {
	where := ""
	if filter {
		where = " AND target_id=ANY($4::uuid[])"
	}
	return catalogSnapshotFilteredQuery(where)
}

func catalogSnapshotFilteredQuery(where string) string {
	return `WITH latest AS (SELECT DISTINCT ON(target,target_id) target,target_id,document FROM catalog.catalog_record_versions WHERE sequence<=(SELECT sequence FROM catalog.catalog_versions WHERE commit_id=$1)` + where + ` ORDER BY target,target_id,sequence DESC) SELECT v.target,v.target_id::text,v.document FROM latest v WHERE v.document IS NOT NULL AND (` + catalogSnapshotVisibleSQL + `) ORDER BY v.target,v.target_id`
}

func catalogVersionObjects(ctx context.Context, q queryer, id string, targets []WorkspaceObject, u User) ([]WorkspaceObject, error) {
	ids := []string{}
	for _, o := range targets {
		ids = append(ids, o.ID)
	}
	args := []any{id, u.ID, u.Can(PermissionLifecycleManage)}
	if targets != nil {
		args = append(args, pq.Array(ids))
	}
	rows, err := q.QueryContext(ctx, catalogSnapshotQuery(targets != nil), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceObject{}
	wanted := map[string]bool{}
	for _, o := range targets {
		wanted[o.Target+":"+o.ID] = true
	}
	for rows.Next() {
		var o WorkspaceObject
		if err = rows.Scan(&o.Target, &o.ID, &o.Document); err != nil {
			return nil, err
		}
		if targets != nil && !wanted[o.Target+":"+o.ID] {
			continue
		}
		o.Base = o.Document
		o.Committed = o.Document
		o.BaseVersion = versionDocumentNumber(o.Document, 0)
		out = append(out, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = visibleVersionObjects(ctx, q, out, u); err != nil {
		return nil, err
	}
	return out, nil
}

func versionTreeForTargets(ctx context.Context, q queryer, id string, targets []WorkspaceObject, u User) ([]WorkspaceObject, string, error) {
	var scope string
	if !collaborationID(id) {
		return nil, "", sql.ErrNoRows
	}
	if err := q.QueryRowContext(ctx, `SELECT snapshot_scope FROM catalog.commits WHERE id=$1`, id).Scan(&scope); err != nil {
		return nil, "", err
	}
	if scope != "catalog" {
		return readableVersionSnapshot(ctx, q, id, u)
	}
	if targets == nil {
		c, err := collaborationCommitFrom(ctx, q, id)
		if err != nil {
			return nil, "", err
		}
		targets = []WorkspaceObject{}
		for _, co := range c.Changes {
			targets = append(targets, WorkspaceObject{Target: co.Target, ID: co.ID})
		}
	}
	objects, err := catalogVersionObjects(ctx, q, id, targets, u)
	return objects, scope, err
}

func (s *Store) CatalogVersionHistory(ctx context.Context, query VersionHistoryQuery, u User) (CommitPage, error) {
	query, queryErr := query.normalized(false)
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
	args := []any{u.Can(PermissionLifecycleManage)}
	where := ` FROM catalog.catalog_versions v JOIN catalog.commits c ON c.id=v.commit_id WHERE ((` + collaborationCommitPublicPredicate + `) OR $1::boolean OR EXISTS(SELECT 1 FROM catalog.commit_operations op WHERE op.commit_id=c.id AND op.operation='baseline'))` + query.sqlFilters(&args, "c.merged_at")
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, limit, offset)
	ids, err := collaborationIDs(ctx, tx, `SELECT c.id::text`+where+fmt.Sprintf(` ORDER BY v.sequence DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
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

func catalogVersionSnapshotPage(ctx context.Context, tx *sql.Tx, id string, queryFilter VersionReadQuery, u User) (CommitSnapshot, error) {
	limit, offset := queryFilter.Limit, queryFilter.Offset
	out := CommitSnapshot{CommitID: id, Scope: "catalog", Objects: []WorkspaceObject{}, Limit: limit, Offset: offset}
	query := catalogSnapshotFilteredQuery(` AND ($4::text='' OR target=$4) AND ($5::text='' OR target_id=NULLIF($5,'')::uuid)`)
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+`) visible`, id, u.ID, u.Can(PermissionLifecycleManage), queryFilter.Target, queryFilter.RecordID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, query+` LIMIT $6 OFFSET $7`, id, u.ID, u.Can(PermissionLifecycleManage), queryFilter.Target, queryFilter.RecordID, limit, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var o WorkspaceObject
		if err = rows.Scan(&o.Target, &o.ID, &o.Document); err != nil {
			return out, err
		}
		o.Base = o.Document
		o.Committed = o.Document
		o.BaseVersion = versionDocumentNumber(o.Document, 0)
		out.Objects = append(out.Objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = visibleVersionObjects(ctx, tx, out.Objects, u); err != nil {
		return out, err
	}
	return out, nil
}

func compareCatalogVersions(ctx context.Context, tx *sql.Tx, base, head string, queryFilter VersionReadQuery, u User) (VersionComparison, error) {
	limit, offset := queryFilter.Limit, queryFilter.Offset
	out := VersionComparison{BaseID: base, HeadID: head, Changes: []CommitObject{}, Limit: limit, Offset: offset}
	if base == head {
		return out, nil
	}
	// Only records touched between the two versions can differ. Resolve those
	// records with the lookup index, including reverse comparisons and deletions.
	query := `WITH bounds AS (
		SELECT a.sequence AS base,b.sequence AS head FROM catalog.catalog_versions a,catalog.catalog_versions b WHERE a.commit_id=$1 AND b.commit_id=$4
	), touched AS (
		SELECT DISTINCT r.target,r.target_id FROM catalog.catalog_record_versions r,bounds b
		WHERE r.sequence>LEAST(b.base,b.head) AND r.sequence<=GREATEST(b.base,b.head)
		AND ($5::text='' OR r.target=$5) AND ($6::text='' OR r.target_id=NULLIF($6,'')::uuid)
	), changes AS (
		SELECT k.target,k.target_id,a.document AS before,b.document AS after,COALESCE(b.document,a.document) AS document
		FROM touched k CROSS JOIN bounds x
		LEFT JOIN LATERAL (SELECT document FROM catalog.catalog_record_versions WHERE target=k.target AND target_id=k.target_id AND sequence<=x.base ORDER BY sequence DESC LIMIT 1) a ON true
		LEFT JOIN LATERAL (SELECT document FROM catalog.catalog_record_versions WHERE target=k.target AND target_id=k.target_id AND sequence<=x.head ORDER BY sequence DESC LIMIT 1) b ON true
		WHERE a.document IS DISTINCT FROM b.document
	), visible AS MATERIALIZED (
		SELECT v.target,v.target_id AS id,v.before,v.after FROM changes v WHERE (` + catalogSnapshotVisibleSQL + `)
	), page AS (SELECT * FROM visible ORDER BY target,id LIMIT $7 OFFSET $8)
	SELECT (SELECT count(*) FROM visible),COALESCE(jsonb_agg(to_jsonb(page) ORDER BY target,id),'[]'::jsonb) FROM page`
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, base, u.ID, u.Can(PermissionLifecycleManage), head, queryFilter.Target, queryFilter.RecordID, limit, offset).Scan(&out.Total, &raw); err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out.Changes); err != nil {
		return out, err
	}
	for i := range out.Changes {
		co := &out.Changes[i]
		co.Base = co.Before
		co.BaseVersion = versionDocumentNumber(co.Before, 0)
	}
	tmp := CollaborationCommit{Changes: out.Changes}
	if err := visibleVersionChanges(ctx, tx, &tmp, u); err != nil {
		return out, err
	}
	out.Changes = tmp.Changes
	return out, nil
}
