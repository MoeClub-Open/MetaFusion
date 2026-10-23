package catalog

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
)

// Read changed facts and their prior versions before serializing the main head.
// These reads share the writer's SERIALIZABLE transaction and are retried with it.
func pendingCatalogRecords(ctx context.Context, tx *sql.Tx) ([]CommitObject, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.target,p.target_id::text,COALESCE(v.document,'null'::jsonb)
		FROM catalog.pending_record_versions p LEFT JOIN LATERAL (
			SELECT document FROM catalog.catalog_record_versions
			WHERE target=p.target AND target_id=p.target_id ORDER BY sequence DESC LIMIT 1
		) v ON true WHERE p.transaction_id=txid_current() ORDER BY p.target,p.target_id`)
	if err != nil {
		return nil, err
	}
	objects := []CommitObject{}
	entityIDs, relationIDs := []string{}, []string{}
	for rows.Next() {
		var o CommitObject
		if err = rows.Scan(&o.Target, &o.ID, &o.Before); err != nil {
			rows.Close()
			return nil, err
		}
		objects = append(objects, o)
		if o.Target == "entity" {
			entityIDs = append(entityIDs, o.ID)
		} else {
			relationIDs = append(relationIDs, o.ID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	entities, err := entityDocuments(ctx, tx, entityIDs)
	if err != nil {
		return nil, err
	}
	for id, e := range entities {
		if e.Kind == "release" {
			e.Subjects = []Subject{}
			entities[id] = e
		}
	}
	entities, err = fillStructural(ctx, tx, entities)
	if err != nil {
		return nil, err
	}
	relations := map[string]Relation{}
	if len(relationIDs) > 0 {
		rows, err = tx.QueryContext(ctx, `SELECT id::text,document FROM catalog.relations WHERE id=ANY($1::uuid[])`, pq.Array(relationIDs))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var raw []byte
			var r Relation
			if err = rows.Scan(&id, &raw); err == nil {
				err = json.Unmarshal(raw, &r)
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			relations[id] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for i := range objects {
		o := &objects[i]
		o.BaseVersion = versionDocumentNumber(o.Before, 0)
		if o.Target == "entity" {
			if e, ok := entities[o.ID]; ok {
				o.After = json.RawMessage(encode(e))
			}
		} else if r, ok := relations[o.ID]; ok {
			o.After = json.RawMessage(encode(r))
		}
	}
	return objects, nil
}

func finalizeCatalogVersion(ctx context.Context, tx *sql.Tx) error {
	objects, err := pendingCatalogRecords(ctx, tx)
	if err != nil {
		return err
	}
	var actor, name, source, message string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(current_setting('metafusion.main_actor',true),''),'system'),COALESCE(current_setting('metafusion.main_actor_name',true),''),COALESCE(current_setting('metafusion.main_source',true),''),COALESCE(NULLIF(current_setting('metafusion.main_message',true),''),'Catalog changes')`).Scan(&actor, &name, &source, &message); err != nil {
		return err
	}
	if len(objects) == 0 && source == "" {
		return nil
	}
	changes := []CommitObject{}
	for _, o := range objects {
		if !equalDocument(o.Before, o.After) {
			changes = append(changes, o)
		}
	}
	// Restrict attribution to this transaction's touched records before taking
	// the global head lock; the target/version index avoids a whole-history walk.
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.user_id,a.name
		FROM catalog.pending_record_versions p JOIN catalog.revisions r ON r.target_id=p.target_id::text
		JOIN catalog.revision_commits rc ON rc.revision_id=r.id JOIN catalog.commit_authors a ON a.commit_id=rc.commit_id
		WHERE p.transaction_id=txid_current() AND r.xmin::text::bigint=(txid_current() & 4294967295)`)
	if err != nil {
		return err
	}
	authors := []CollaborationAuthor{}
	for rows.Next() {
		var a CollaborationAuthor
		if err = rows.Scan(&a.ID, &a.Name); err != nil {
			rows.Close()
			return err
		}
		authors = append(authors, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(authors) == 0 && actor != "system" {
		authors = append(authors, CollaborationAuthor{ID: actor, Name: name})
	}
	changeJSON, authorJSON := encode(changes), encode(authors)
	var parent string
	if err = tx.QueryRowContext(ctx, `SELECT commit_id::text FROM catalog.catalog_head WHERE singleton FOR UPDATE`).Scan(&parent); err != nil {
		return err
	}
	id := newID()
	request := map[string]any{"operation": "integrate", "source_head": source}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,parent_id,merge_parent_id,message,kind,snapshot_scope,merged_at,merged_by) VALUES($1,$2,$3,$4,'{}',$5,$6,$7,$8,'rebase','catalog',now(),$2)`, id, actor, requestHash(request), encode(request), encode(commitAuthor{ID: actor, Username: name}), parent, nullable(source), message); err != nil {
		return err
	}
	sources := []string{}
	if source != "" {
		sources = append(sources, source)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_operations VALUES($1,'integrate',$2::uuid[])`, id, pq.Array(sources)); err != nil {
		return err
	}
	var sequence int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO catalog.catalog_versions(commit_id) VALUES($1) RETURNING sequence`, id).Scan(&sequence); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH changes AS (
		SELECT * FROM jsonb_to_recordset($3::jsonb) AS c(target text,id uuid,base_version bigint,"before" jsonb,"after" jsonb)
	), records AS (
		INSERT INTO catalog.catalog_record_versions(sequence,target,target_id,document)
		SELECT $2,target,id,NULLIF("after",'null'::jsonb) FROM changes
	) INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document)
		SELECT $1,target,id,base_version,NULLIF("before",'null'::jsonb),NULLIF("before",'null'::jsonb),NULLIF("after",'null'::jsonb) FROM changes`, id, sequence, changeJSON); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name)
		SELECT $1,id,name FROM jsonb_to_recordset($2::jsonb) AS a(id text,name text) ON CONFLICT DO NOTHING`, id, authorJSON); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog.catalog_head SET commit_id=$1 WHERE singleton`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog.pending_record_versions WHERE transaction_id=txid_current()`)
	return err
}
