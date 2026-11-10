ALTER TABLE catalog.pending_record_versions SET UNLOGGED;

CREATE OR REPLACE FUNCTION catalog.queue_record_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE new_id uuid; old_id uuid;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW IS NOT DISTINCT FROM OLD THEN
            RETURN NULL;
        END IF;
    END IF;
    IF TG_ARGV[1] = 'id' THEN
        IF TG_OP <> 'DELETE' THEN new_id := NEW.id; END IF;
        IF TG_OP <> 'INSERT' THEN old_id := OLD.id; END IF;
    ELSIF TG_ARGV[1] = 'release_id' THEN
        IF TG_OP <> 'DELETE' THEN new_id := NEW.release_id; END IF;
        IF TG_OP <> 'INSERT' THEN old_id := OLD.release_id; END IF;
    ELSIF TG_ARGV[1] = 'track_id' THEN
        IF TG_OP <> 'DELETE' THEN new_id := NEW.track_id; END IF;
        IF TG_OP <> 'INSERT' THEN old_id := OLD.track_id; END IF;
    ELSE
        IF TG_OP <> 'DELETE' THEN new_id := (to_jsonb(NEW)->>TG_ARGV[1])::uuid; END IF;
        IF TG_OP <> 'INSERT' THEN old_id := (to_jsonb(OLD)->>TG_ARGV[1])::uuid; END IF;
    END IF;
    IF new_id IS NOT NULL THEN
        INSERT INTO catalog.pending_record_versions VALUES(txid_current(), TG_ARGV[0], new_id) ON CONFLICT DO NOTHING;
    END IF;
    IF old_id IS NOT NULL AND old_id IS DISTINCT FROM new_id THEN
        INSERT INTO catalog.pending_record_versions VALUES(txid_current(), TG_ARGV[0], old_id) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION catalog.identity_candidate_terms(doc jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$
    WITH translations AS (
        SELECT value FROM jsonb_each(CASE WHEN jsonb_typeof(doc->'translations')='object'
            THEN doc->'translations' ELSE '{}'::jsonb END)
    ), titles AS (
        SELECT doc->>'title' AS value
        UNION ALL SELECT value->>'title' FROM translations
        UNION ALL SELECT alias #>> '{}' FROM translations,
            LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(value->'aliases')='array'
                THEN value->'aliases' ELSE '[]'::jsonb END) alias
            WHERE jsonb_typeof(alias)='string'
        UNION ALL SELECT alias #>> '{}' FROM translations,
            LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(value->'nonofficial_aliases')='array'
                THEN value->'nonofficial_aliases' ELSE '[]'::jsonb END) alias
            WHERE jsonb_typeof(alias)='string'
        UNION ALL SELECT alias #>> '{}' FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc->'aliases')='array' THEN doc->'aliases' ELSE '[]'::jsonb END) alias
            WHERE jsonb_typeof(alias)='string'
    ), attrs AS (
        SELECT key, element FROM jsonb_each(CASE WHEN jsonb_typeof(doc->'attributes')='object'
            THEN doc->'attributes' ELSE '{}'::jsonb END),
            LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(value)='array'
                THEN value ELSE jsonb_build_array(value) END) element
        WHERE jsonb_typeof(element) IN ('string','number','boolean')
    ), links AS (
        SELECT link->>'kind' AS kind, COALESCE(NULLIF(link->>'external_id',''), link->>'url') AS val
        FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc->'external_links')='array'
                THEN doc->'external_links' ELSE '[]'::jsonb END) link
        WHERE jsonb_typeof(link)='object'
          AND COALESCE(link->>'kind','') <> ''
    ), terms AS (
        SELECT jsonb_build_array('title', normalized)::text AS term
            FROM titles, LATERAL (SELECT catalog.identity_title(value) AS normalized) n
            WHERE value IS NOT NULL AND n.normalized <> ''
        UNION ALL SELECT jsonb_build_array('external', kind, val)::text
            FROM links WHERE val IS NOT NULL
        UNION ALL SELECT jsonb_build_array('attribute', key, catalog.identity_scalar(element))::text
            FROM attrs
    ) SELECT coalesce(array_agg(DISTINCT md5(term)), ARRAY[]::text[]) FROM terms
$$;

CREATE INDEX IF NOT EXISTS entities_kind_state_title_idx
    ON catalog.entities (kind, state, title, id);
CREATE INDEX IF NOT EXISTS entities_active_kind_btrim_title_idx
    ON catalog.entities (kind, btrim(title)) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS revisions_actor_created_idx
    ON catalog.revisions (actor_id, created_at DESC) WHERE commit_id IS NULL;
CREATE INDEX IF NOT EXISTS revisions_target_actor_created_idx
    ON catalog.revisions (target_id, actor_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS commits_pull_request_idx
    ON catalog.commits (pull_request_id) WHERE pull_request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS curator_runs_kind_started_idx
    ON curator.runs (kind, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS usage_budget_idx
    ON assistant.usage (user_id, budget_month, purpose);

UPDATE catalog.schema_contract SET version = 43 WHERE singleton;
