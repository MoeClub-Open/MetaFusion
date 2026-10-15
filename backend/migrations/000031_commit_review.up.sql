-- Publication is replaced by commit review. Preserve facts, identity, versions
-- and historical snapshots; legacy normal records are reviewed per the admin's
-- migration decision, without fabricating a reviewer or review timestamp.
ALTER TABLE catalog.entities DROP CONSTRAINT entities_status_check;
UPDATE catalog.entities
SET status = CASE WHEN status IN ('deleted','merged') THEN status ELSE 'active' END,
    document = (document - 'status') || jsonb_build_object(
        'state', CASE WHEN status IN ('deleted','merged') THEN status ELSE 'active' END,
        'review_status', 'reviewed');
ALTER TABLE catalog.entities ADD CONSTRAINT entities_status_check
    CHECK (status IN ('active','deleted','merged'));
DROP INDEX IF EXISTS catalog.entities_public_recent;
CREATE INDEX entities_public_recent ON catalog.entities(updated_at DESC,id) WHERE status='active';

ALTER TABLE catalog.commits
    ADD COLUMN review_status text NOT NULL DEFAULT 'reviewed'
        CHECK (review_status IN ('pending_review','reviewed')),
    ADD COLUMN review_decision text NOT NULL DEFAULT 'legacy'
        CHECK (review_decision IN ('pending','approved','rejected','exempt','legacy')),
    ADD COLUMN reviewed_by text,
    ADD COLUMN reviewed_at timestamptz,
    ADD COLUMN review_note text NOT NULL DEFAULT '',
    ADD COLUMN author_snapshot jsonb NOT NULL DEFAULT '{}';
UPDATE catalog.commits SET result = result || '{"review_status":"reviewed","review_decision":"legacy"}'::jsonb
WHERE jsonb_typeof(result)='object';
CREATE INDEX commits_review_queue ON catalog.commits(review_status,created_at,id);
-- Lifecycle evidence now reads the new payload field while retaining old snapshots.
CREATE OR REPLACE FUNCTION catalog.record_lifecycle_action() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO catalog.lifecycle_actions(revision_id,event_id,target_id,version,action,actor_id,actor_name,edit_note,sources,occurred_at)
    SELECT r.id,NEW.id,r.target_id,r.version,NEW.type,r.actor_id,r.actor_name,r.edit_note,r.sources,r.created_at
    FROM catalog.revisions r
    WHERE r.target_id=NEW.entity_id AND r.version=NEW.version
      AND COALESCE(r.snapshot->>'state',r.snapshot->>'status')=substring(NEW.type FROM 8)
    ON CONFLICT(revision_id) DO NOTHING;
    RETURN NEW;
END $$;
UPDATE catalog.schema_contract SET version=31 WHERE singleton=true;
