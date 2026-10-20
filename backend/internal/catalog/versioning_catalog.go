package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// Finalization runs after all record writes. Only this short metadata step
// serializes the official head; editing and scoped validation remain parallel.
func finalizeCatalogVersion(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT target,target_id::text FROM catalog.pending_record_versions WHERE transaction_id=txid_current() ORDER BY target,target_id`)
	if err != nil {
		return err
	}
	type key struct{ target, id string }
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.target, &k.id); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		var source string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(current_setting('metafusion.main_source',true),'')`).Scan(&source); err != nil {
			return err
		}
		if source == "" {
			return nil
		}
	}
	var parent, actor, name, source, message string
	if err = tx.QueryRowContext(ctx, `SELECT commit_id::text FROM catalog.catalog_head WHERE singleton FOR UPDATE`).Scan(&parent); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(current_setting('metafusion.main_actor',true),''),'system'),COALESCE(current_setting('metafusion.main_actor_name',true),''),COALESCE(current_setting('metafusion.main_source',true),''),COALESCE(NULLIF(current_setting('metafusion.main_message',true),''),'Catalog changes')`).Scan(&actor, &name, &source, &message); err != nil {
		return err
	}
	id := newID()
	request := map[string]any{"operation": "integrate", "source_head": source}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,parent_id,merge_parent_id,message,kind,snapshot_scope,merged_at,merged_by) VALUES($1,$2,$3,$4,'{}',$5,$6,$7,$8,'rebase','catalog',now(),$2)`, id, actor, requestHash(request), encode(request), encode(commitAuthor{ID: actor, Username: name}), parent, nullable(source), message); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_operations VALUES($1,'integrate',$2::uuid[])`, id, pq.Array(func() []string {
		if source == "" {
			return []string{}
		}
		return []string{source}
	}())); err != nil {
		return err
	}
	var sequence int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO catalog.catalog_versions(commit_id) VALUES($1) RETURNING sequence`, id).Scan(&sequence); err != nil {
		return err
	}
	for _, k := range keys {
		var after json.RawMessage
		if k.target == "entity" {
			e, eerr := get(ctx, tx, k.id)
			if eerr != nil && !errors.Is(eerr, sql.ErrNoRows) {
				return eerr
			}
			if eerr == nil {
				after = json.RawMessage(encode(e))
			}
		} else {
			r, eerr := relationByID(ctx, tx, k.id)
			if eerr != nil && !errors.Is(eerr, sql.ErrNoRows) {
				return eerr
			}
			if eerr == nil {
				after = json.RawMessage(encode(r))
			}
		}
		var before json.RawMessage
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(document,'null'::jsonb) FROM catalog.catalog_record_versions WHERE target=$1 AND target_id=$2 ORDER BY sequence DESC LIMIT 1`, k.target, k.id).Scan(&before)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if equalDocument(before, after) {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.catalog_record_versions VALUES($1,$2,$3,$4)`, sequence, k.target, k.id, nullableJSON(after)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document) VALUES($1,$2,$3,$4,$5,$5,$6)`, id, k.target, k.id, versionDocumentNumber(before, 0), nullableJSON(before), nullableJSON(after)); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name) SELECT DISTINCT $1::uuid,a.user_id,a.name FROM catalog.revisions r JOIN catalog.revision_commits rc ON rc.revision_id=r.id JOIN catalog.commit_authors a ON a.commit_id=rc.commit_id WHERE r.xmin::text::bigint=(txid_current() & 4294967295) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 && actor != "system" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_authors VALUES($1,$2,$3)`, id, actor, name); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog.catalog_head SET commit_id=$1 WHERE singleton`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog.pending_record_versions WHERE transaction_id=txid_current()`)
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
	for i := range out {
		if err = visibleVersionObject(ctx, q, &out[i], u); err != nil {
			return nil, err
		}
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
	query := `SELECT * FROM (` + catalogSnapshotQuery(false) + `) scoped WHERE ($4::text='' OR target=$4) AND ($5::text='' OR target_id=$5)`
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+`) visible`, id, u.ID, u.Can(PermissionLifecycleManage), queryFilter.Target, queryFilter.RecordID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY target,target_id LIMIT $6 OFFSET $7`, id, u.ID, u.Can(PermissionLifecycleManage), queryFilter.Target, queryFilter.RecordID, limit, offset)
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
	for i := range out.Objects {
		if err = visibleVersionObject(ctx, tx, &out.Objects[i], u); err != nil {
			return out, err
		}
	}
	return out, nil
}

func compareCatalogVersions(ctx context.Context, tx *sql.Tx, base, head string, queryFilter VersionReadQuery, u User) (VersionComparison, error) {
	limit, offset := queryFilter.Limit, queryFilter.Offset
	out := VersionComparison{BaseID: base, HeadID: head, Changes: []CommitObject{}, Limit: limit, Offset: offset}
	query := `WITH old AS(SELECT DISTINCT ON(target,target_id) target,target_id,document FROM catalog.catalog_record_versions WHERE sequence<=(SELECT sequence FROM catalog.catalog_versions WHERE commit_id=$1) ORDER BY target,target_id,sequence DESC), next AS(SELECT DISTINCT ON(target,target_id) target,target_id,document FROM catalog.catalog_record_versions WHERE sequence<=(SELECT sequence FROM catalog.catalog_versions WHERE commit_id=$4) ORDER BY target,target_id,sequence DESC), changes AS(SELECT COALESCE(a.target,b.target) AS target,COALESCE(a.target_id,b.target_id) AS target_id,a.document AS before,b.document AS after,COALESCE(b.document,a.document) AS document FROM old a FULL JOIN next b USING(target,target_id) WHERE a.document IS DISTINCT FROM b.document) SELECT v.target,v.target_id::text,COALESCE(v.before,'null'::jsonb) AS before,COALESCE(v.after,'null'::jsonb) AS after FROM changes v WHERE (` + catalogSnapshotVisibleSQL + `) ORDER BY v.target,v.target_id`
	query = `SELECT * FROM (` + query + `) scoped WHERE ($5::text='' OR target=$5) AND ($6::text='' OR target_id=$6)`
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+`) d`, base, u.ID, u.Can(PermissionLifecycleManage), head, queryFilter.Target, queryFilter.RecordID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY target,target_id LIMIT $7 OFFSET $8`, base, u.ID, u.Can(PermissionLifecycleManage), head, queryFilter.Target, queryFilter.RecordID, limit, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var co CommitObject
		if err = rows.Scan(&co.Target, &co.ID, &co.Before, &co.After); err != nil {
			return out, err
		}
		co.Base = co.Before
		co.BaseVersion = versionDocumentNumber(co.Before, 0)
		out.Changes = append(out.Changes, co)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	tmp := CollaborationCommit{Changes: out.Changes}
	if err = visibleVersionChanges(ctx, tx, &tmp, u); err != nil {
		return out, err
	}
	out.Changes = tmp.Changes
	return out, nil
}
