-- Finish the publication removal in persisted projections. Runtime readers use
-- state exclusively; no aliases, dual writes or historical-field fallback.
ALTER TABLE catalog.entities RENAME COLUMN status TO state;
ALTER TABLE catalog.entities RENAME CONSTRAINT entities_status_check TO entities_state_check;
ALTER INDEX catalog.entities_kind_status RENAME TO entities_kind_state;

UPDATE catalog.revisions
SET snapshot = (snapshot - 'status') || jsonb_build_object(
    'state', CASE WHEN COALESCE(snapshot->>'state',snapshot->>'status') IN ('deleted','merged')
        THEN COALESCE(snapshot->>'state',snapshot->>'status') ELSE 'active' END,
    'review_status', 'reviewed')
WHERE snapshot->>'kind' IN ('agent','collection','work','content_unit','expression','release','medium','track');

UPDATE catalog.outbox
SET payload = (payload - 'status') || jsonb_build_object(
    'state', CASE WHEN COALESCE(payload->>'state',payload->>'status') IN ('deleted','merged')
        THEN COALESCE(payload->>'state',payload->>'status') ELSE 'active' END,
    'review_status', 'reviewed')
WHERE payload->>'kind' IN ('agent','collection','work','content_unit','expression','release','medium','track');
UPDATE catalog.outbox SET type='entity.saved' WHERE type='entity.unpublished';

-- Pre-review submissions were made by administrators and already applied.
-- Their immutable request/hash remains original evidence, never a runtime input.
UPDATE catalog.commits SET review_decision='exempt',
    result=jsonb_set(result,'{review_decision}','"exempt"'::jsonb)
WHERE review_decision='legacy';
ALTER TABLE catalog.commits DROP CONSTRAINT commits_review_decision_check;
ALTER TABLE catalog.commits ADD CONSTRAINT commits_review_decision_check
    CHECK (review_decision IN ('pending','approved','rejected','exempt'));
ALTER TABLE catalog.commits ALTER COLUMN review_decision SET DEFAULT 'exempt';
ALTER TABLE catalog.commits ADD CONSTRAINT commits_pending_author_check
    CHECK (review_status <> 'pending_review' OR
        (author_snapshot->>'id' IS NOT NULL AND author_snapshot->>'id'=actor_id::text));

CREATE OR REPLACE FUNCTION catalog.record_lifecycle_action() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO catalog.lifecycle_actions(revision_id,event_id,target_id,version,action,actor_id,actor_name,edit_note,sources,occurred_at)
    SELECT r.id,NEW.id,r.target_id,r.version,NEW.type,r.actor_id,r.actor_name,r.edit_note,r.sources,r.created_at
    FROM catalog.revisions r
    WHERE r.target_id=NEW.entity_id AND r.version=NEW.version
      AND r.snapshot->>'state'=substring(NEW.type FROM 8)
    ON CONFLICT(revision_id) DO NOTHING;
    RETURN NEW;
END $$;
UPDATE catalog.schema_contract SET version=32 WHERE singleton=true;
