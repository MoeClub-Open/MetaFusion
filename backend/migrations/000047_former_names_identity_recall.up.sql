DO $identity_former_names_lock$
BEGIN
  PERFORM pg_advisory_xact_lock(740206);
END
$identity_former_names_lock$;

CREATE OR REPLACE FUNCTION catalog.identity_core_titles(doc jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
    WITH translations AS (
        SELECT value FROM jsonb_each(CASE WHEN jsonb_typeof(doc->'translations')='object'
            THEN doc->'translations' ELSE '{}'::jsonb END)
    ), raw_titles AS (
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
        UNION ALL SELECT fn->>'name' FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc #> '{attributes,former_names}')='array'
                THEN doc #> '{attributes,former_names}' ELSE '[]'::jsonb END) fn
            WHERE jsonb_typeof(fn)='object' AND jsonb_typeof(fn->'name')='string'
    )
    SELECT coalesce(array_agg(DISTINCT c.core), ARRAY[]::text[])
    FROM raw_titles, LATERAL (SELECT catalog.identity_core_title(value) AS core) c
    WHERE value IS NOT NULL AND char_length(c.core) >= 2
$fn$;

CREATE OR REPLACE FUNCTION catalog.identity_search_text(doc jsonb) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
    WITH translations AS (
        SELECT value FROM jsonb_each(CASE WHEN jsonb_typeof(doc->'translations')='object'
            THEN doc->'translations' ELSE '{}'::jsonb END)
    ), raw_titles AS (
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
        UNION ALL SELECT fn->>'name' FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc #> '{attributes,former_names}')='array'
                THEN doc #> '{attributes,former_names}' ELSE '[]'::jsonb END) fn
            WHERE jsonb_typeof(fn)='object' AND jsonb_typeof(fn->'name')='string'
    ), terms AS (
        SELECT DISTINCT term FROM (
            SELECT catalog.identity_title(value) AS term FROM raw_titles WHERE value IS NOT NULL
            UNION ALL
            SELECT catalog.identity_core_title(value) AS term FROM raw_titles WHERE value IS NOT NULL
        ) t WHERE term <> ''
    )
    SELECT coalesce(string_agg(term, chr(10) ORDER BY term), '') FROM terms
$fn$;

CREATE OR REPLACE FUNCTION catalog.identity_exact_terms(doc jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
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
        UNION ALL SELECT fn->>'name' FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc #> '{attributes,former_names}')='array'
                THEN doc #> '{attributes,former_names}' ELSE '[]'::jsonb END) fn
            WHERE jsonb_typeof(fn)='object' AND jsonb_typeof(fn->'name')='string'
    ), links AS (
        SELECT link->>'kind' AS kind, COALESCE(NULLIF(link->>'external_id',''), link->>'url') AS val
        FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc->'external_links')='array'
                THEN doc->'external_links' ELSE '[]'::jsonb END) link
        WHERE jsonb_typeof(link)='object'
          AND COALESCE(link->>'kind','') <> ''
    ), terms AS (
        SELECT jsonb_build_array('title', n.normalized)::text AS term
            FROM titles, LATERAL (SELECT catalog.identity_title(value) AS normalized) n
            WHERE value IS NOT NULL AND n.normalized <> ''
        UNION ALL SELECT jsonb_build_array('external', kind, val)::text
            FROM links WHERE val IS NOT NULL
    ) SELECT coalesce(array_agg(DISTINCT md5(term)), ARRAY[]::text[]) FROM terms
$fn$;

CREATE OR REPLACE FUNCTION catalog.identity_candidate_terms(doc jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
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
        UNION ALL SELECT fn->>'name' FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(doc #> '{attributes,former_names}')='array'
                THEN doc #> '{attributes,former_names}' ELSE '[]'::jsonb END) fn
            WHERE jsonb_typeof(fn)='object' AND jsonb_typeof(fn->'name')='string'
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
        SELECT jsonb_build_array('title', n.normalized)::text AS term
            FROM titles, LATERAL (SELECT catalog.identity_title(value) AS normalized) n
            WHERE value IS NOT NULL AND n.normalized <> ''
        UNION ALL SELECT jsonb_build_array('core_title', c.core)::text AS term
            FROM titles, LATERAL (SELECT catalog.identity_core_title(value) AS core) c
            WHERE value IS NOT NULL AND char_length(c.core) >= 2
        UNION ALL SELECT jsonb_build_array('external', kind, val)::text
            FROM links WHERE val IS NOT NULL
        UNION ALL SELECT jsonb_build_array('attribute', key, catalog.identity_scalar(element))::text
            FROM attrs
    ) SELECT coalesce(array_agg(DISTINCT md5(term)), ARRAY[]::text[]) FROM terms
$fn$;

DROP INDEX IF EXISTS catalog.entities_identity_candidates_idx;
CREATE INDEX entities_identity_candidates_idx ON catalog.entities
    USING gin (catalog.identity_candidate_terms(document)) WHERE state <> 'deleted';

DROP INDEX IF EXISTS catalog.entities_title_trgm_idx;
CREATE INDEX entities_title_trgm_idx ON catalog.entities
    USING gin (catalog.identity_search_text(document) gin_trgm_ops) WHERE state <> 'deleted';

UPDATE catalog.schema_contract SET version = 47 WHERE singleton;
