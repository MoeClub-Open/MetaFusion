-- Step ⑥: replace document.external_ids with external_links (no dual-write).
-- Convert existing map entries into link rows + document array, then drop the
-- old key and identity terms that read it. Migrated links keep role NULL and
-- origin='migration' (no inferred official, no forged source_refs).
--
-- Downtime: one transaction; ACCESS EXCLUSIVE only on the new table create /
-- index builds. Entity document UPDATEs take row locks. No VACUUM FULL / table
-- rewrite of catalog.entities. Estimate: seconds to a few minutes depending on
-- entity + revision row counts (see PR).

CREATE TABLE catalog.external_links (
    entity_id uuid NOT NULL,
    entity_kind text NOT NULL,
    kind text NOT NULL,
    url text NOT NULL,
    external_id text,
    role text,
    origin text,
    source_refs jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT external_links_entity_fkey FOREIGN KEY (entity_id) REFERENCES catalog.entities(id) ON DELETE CASCADE,
    CONSTRAINT external_links_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['agent'::text, 'collection'::text, 'work'::text, 'content_unit'::text, 'expression'::text, 'release'::text, 'medium'::text, 'track'::text]))),
    CONSTRAINT external_links_kind_check CHECK ((length(TRIM(BOTH FROM kind)) > 0 AND length(kind) <= 64)),
    CONSTRAINT external_links_url_check CHECK ((length(TRIM(BOTH FROM url)) > 0 AND length(url) <= 2000)),
    CONSTRAINT external_links_role_check CHECK ((role IS NULL OR role = ANY (ARRAY['official'::text, 'primary'::text, 'secondary'::text, 'operator'::text]))),
    CONSTRAINT external_links_origin_check CHECK ((origin IS NULL OR origin = ANY (ARRAY['migration'::text, 'commit'::text]))),
    CONSTRAINT external_links_pkey PRIMARY KEY (entity_id, kind, url)
);

CREATE INDEX external_links_kind_external_id ON catalog.external_links USING btree (kind, external_id) WHERE (external_id IS NOT NULL AND external_id <> ''::text);
CREATE INDEX external_links_entity ON catalog.external_links USING btree (entity_id);
CREATE UNIQUE INDEX external_links_metafusion_import_key ON catalog.external_links USING btree (external_id) WHERE ((kind = 'metafusion_import'::text) AND (external_id IS NOT NULL) AND (external_id <> ''::text));

-- Normalize URL for migration: prefer http(s) values; else url_pattern; else synthetic.
CREATE FUNCTION catalog.migrate_external_link_url(p_kind text, p_value text, p_pattern text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN p_value ~* '^https?://' THEN regexp_replace(rtrim(p_value, '/'), '/+$', '')
        WHEN p_pattern IS NOT NULL AND p_pattern <> '' THEN replace(p_pattern, '{id}', p_value)
        ELSE 'mf://external/' || p_kind || '/' || replace(replace(p_value, '/', '%2F'), ' ', '%20')
    END
$$;

-- Convert live entity documents: build links array, insert side-table rows, drop external_ids.
WITH raw AS (
    SELECT e.id AS entity_id, e.kind AS entity_kind, kv.key AS link_kind, btrim(kv.value #>> '{}') AS raw_value,
           ed.url_pattern
    FROM catalog.entities e
    CROSS JOIN LATERAL jsonb_each(
        CASE WHEN jsonb_typeof(e.document->'external_ids') = 'object'
             THEN e.document->'external_ids' ELSE '{}'::jsonb END
    ) AS kv(key, value)
    LEFT JOIN catalog.external_databases ed ON ed.code = kv.key
    WHERE jsonb_typeof(kv.value) = 'string' AND btrim(kv.value #>> '{}') <> ''
),
prepared AS (
    SELECT entity_id, entity_kind, link_kind AS kind,
           catalog.migrate_external_link_url(link_kind, raw_value, url_pattern) AS url,
           CASE WHEN raw_value ~* '^https?://' THEN NULL ELSE raw_value END AS external_id
    FROM raw
),
dedup AS (
    -- Unique (entity, kind, url); keep first external_id if collisions after normalize.
    SELECT DISTINCT ON (entity_id, kind, url)
        entity_id, entity_kind, kind, url, external_id
    FROM prepared
    ORDER BY entity_id, kind, url, external_id NULLS LAST
),
inserted AS (
    INSERT INTO catalog.external_links (entity_id, entity_kind, kind, url, external_id, role, origin, source_refs)
    SELECT entity_id, entity_kind, kind, url, external_id, NULL, 'migration', NULL
    FROM dedup
    RETURNING entity_id
)
SELECT count(*) FROM inserted;

-- A separate statement must read the inserted rows: modifying CTEs share a
-- snapshot, so reading external_links alongside INSERT sees the old empty table.
WITH per_entity AS (
    SELECT el.entity_id,
           jsonb_agg(
               jsonb_strip_nulls(jsonb_build_object(
                   'kind', el.kind,
                   'url', el.url,
                   'external_id', el.external_id,
                   'origin', el.origin
               )) ORDER BY el.kind, el.url
           ) AS links
    FROM catalog.external_links el
    WHERE el.origin = 'migration'
    GROUP BY el.entity_id
)
UPDATE catalog.entities e
SET document = (e.document - 'external_ids') || jsonb_build_object('external_links', pe.links)
FROM per_entity pe
WHERE e.id = pe.entity_id;

-- Entities that had an empty/missing external_ids object: ensure key is gone and array present.
UPDATE catalog.entities
SET document = (document - 'external_ids') || jsonb_build_object('external_links', COALESCE(document->'external_links', '[]'::jsonb))
WHERE document ? 'external_ids' OR NOT (document ? 'external_links');

-- Historical revision snapshots: convert external_ids map → external_links array (role null, origin migration).
UPDATE catalog.revisions r
SET snapshot = (
    SELECT (snap - 'external_ids') || jsonb_build_object(
        'external_links',
        COALESCE((
            SELECT jsonb_agg(
                jsonb_strip_nulls(jsonb_build_object(
                    'kind', kv.key,
                    'url', catalog.migrate_external_link_url(kv.key, btrim(kv.value #>> '{}'), ed.url_pattern),
                    'external_id', CASE WHEN btrim(kv.value #>> '{}') ~* '^https?://' THEN NULL ELSE btrim(kv.value #>> '{}') END,
                    'origin', 'migration'
                )) ORDER BY kv.key
            )
            FROM jsonb_each(
                CASE WHEN jsonb_typeof(snap->'external_ids') = 'object'
                     THEN snap->'external_ids' ELSE '{}'::jsonb END
            ) AS kv(key, value)
            LEFT JOIN catalog.external_databases ed ON ed.code = kv.key
            WHERE jsonb_typeof(kv.value) = 'string' AND btrim(kv.value #>> '{}') <> ''
        ), '[]'::jsonb)
    )
    FROM (SELECT r.snapshot AS snap) s
)
WHERE jsonb_typeof(snapshot) = 'object' AND snapshot ? 'external_ids';

-- Keep revision evidence attached to the same fact after the representation change.
CREATE FUNCTION catalog.migrate_external_link_pointer(p_pointer text, p_links jsonb) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN p_pointer = '/external_ids' THEN '/external_links'
  WHEN p_pointer LIKE '/external_ids/%' THEN COALESCE((
   SELECT '/external_links/' || (position - 1)::text ||
    CASE WHEN link ? 'external_id' THEN '/external_id' ELSE '/url' END
   FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p_links)='array' THEN p_links ELSE '[]'::jsonb END)
    WITH ORDINALITY AS links(link,position)
   WHERE link->>'kind'=replace(replace(substring(p_pointer FROM 15),'~1','/'),'~0','~')
   LIMIT 1
  ),p_pointer) ELSE p_pointer END
$$;
UPDATE catalog.revisions r SET sources=(
 SELECT jsonb_agg(CASE WHEN jsonb_typeof(source->'pointers')='array' THEN
  jsonb_set(source,'{pointers}',COALESCE((
   SELECT jsonb_agg(catalog.migrate_external_link_pointer(pointer #>> '{}',r.snapshot->'external_links') ORDER BY pointer_order)
   FROM jsonb_array_elements(source->'pointers') WITH ORDINALITY AS pointers(pointer,pointer_order)
  ),'[]'::jsonb)) ELSE source END ORDER BY source_order)
 FROM jsonb_array_elements(r.sources) WITH ORDINALITY AS sources(source,source_order)
) WHERE jsonb_typeof(r.sources)='array' AND r.sources::text LIKE '%/external_ids%';
DROP FUNCTION catalog.migrate_external_link_pointer(text,jsonb);

DROP INDEX IF EXISTS catalog.entities_metafusion_import_key;

-- Identity terms: read external_links[].kind + external_id/url instead of external_ids map.
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
        SELECT link FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc->'external_links')='array'
                THEN doc->'external_links' ELSE '[]'::jsonb END) link
        WHERE jsonb_typeof(link)='object'
          AND COALESCE(link->>'kind','') <> ''
    ), terms AS (
        SELECT jsonb_build_array('title',catalog.identity_title(value))::text AS term
            FROM titles WHERE catalog.identity_title(value) <> ''
        UNION ALL SELECT jsonb_build_array('external',link->>'kind',
            COALESCE(NULLIF(link->>'external_id',''), link->>'url'))::text
            FROM links
            WHERE COALESCE(NULLIF(link->>'external_id',''), link->>'url') IS NOT NULL
        UNION ALL SELECT jsonb_build_array('attribute',key,catalog.identity_scalar(element))::text FROM attrs
    ) SELECT coalesce(array_agg(DISTINCT md5(term)), ARRAY[]::text[]) FROM terms
$$;

-- Rebuild identity GIN so it uses the replaced function body.
DROP INDEX IF EXISTS catalog.entities_identity_candidates_idx;
CREATE INDEX entities_identity_candidates_idx ON catalog.entities
    USING gin (catalog.identity_candidate_terms(document)) WHERE status <> 'deleted';

DROP FUNCTION catalog.migrate_external_link_url(text, text, text);

UPDATE catalog.schema_contract SET version = 30 WHERE singleton = true;
