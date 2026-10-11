-- Retention must never erase push receipts, creation guards or lifecycle facts.
-- The live instance may already have the deliveries index added during recovery.
CREATE INDEX IF NOT EXISTS deliveries_event_id_idx ON catalog.deliveries(event_id);
CREATE INDEX revisions_target_version_idx ON catalog.revisions(target_id, version, id);
CREATE INDEX outbox_unconsumed_created_id ON catalog.outbox(created_at, id)
    WHERE type IN ('relation.saved', 'relation.deleted', 'definitions.updated', 'rate_limits.updated');
CREATE INDEX commits_request_retention_idx ON catalog.commits(created_at, id)
    WHERE request <> 'null'::jsonb;

-- No foreign keys: queue/history maintenance must not erase an action that happened.
-- revision_id preserves the former count(DISTINCT revisions.id) contribution unit.
CREATE TABLE catalog.lifecycle_actions (
    revision_id bigint PRIMARY KEY,
    event_id uuid NOT NULL,
    target_id text NOT NULL,
    version bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('entity.deleted', 'entity.merged')),
    actor_id uuid,
    actor_name text NOT NULL,
    edit_note text NOT NULL,
    sources jsonb NOT NULL,
    occurred_at timestamptz NOT NULL
);
CREATE INDEX lifecycle_actions_actor_idx ON catalog.lifecycle_actions(actor_id);
CREATE INDEX lifecycle_actions_target_version_idx ON catalog.lifecycle_actions(target_id, version, action);

-- Hold writers from backfill through trigger installation, including old binaries.
LOCK TABLE catalog.outbox IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO catalog.lifecycle_actions(revision_id, event_id, target_id, version, action,
    actor_id, actor_name, edit_note, sources, occurred_at)
SELECT DISTINCT ON (r.id) r.id, o.id, r.target_id, r.version, o.type,
    r.actor_id, r.actor_name, r.edit_note, r.sources, r.created_at
FROM catalog.revisions r
JOIN catalog.outbox o ON o.entity_id = r.target_id AND o.version = r.version
WHERE (o.type = 'entity.deleted' AND r.snapshot ->> 'status' = 'deleted')
   OR (o.type = 'entity.merged' AND r.snapshot ->> 'status' = 'merged')
ORDER BY r.id, o.created_at, o.id;

CREATE FUNCTION catalog.record_lifecycle_action() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO catalog.lifecycle_actions(revision_id, event_id, target_id, version, action,
        actor_id, actor_name, edit_note, sources, occurred_at)
    SELECT r.id, NEW.id, r.target_id, r.version, NEW.type,
        r.actor_id, r.actor_name, r.edit_note, r.sources, r.created_at
    FROM catalog.revisions r
    WHERE r.target_id = NEW.entity_id AND r.version = NEW.version
      AND r.snapshot ->> 'status' = substring(NEW.type FROM 8)
    ON CONFLICT (revision_id) DO NOTHING;
    RETURN NEW;
END $$;
CREATE TRIGGER record_lifecycle_action AFTER INSERT ON catalog.outbox
FOR EACH ROW WHEN (NEW.type IN ('entity.deleted', 'entity.merged'))
EXECUTE FUNCTION catalog.record_lifecycle_action();

UPDATE catalog.schema_contract SET version = 27 WHERE singleton = true;
