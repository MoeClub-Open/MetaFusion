DO $metadata_taxonomy_and_names$
BEGIN
  PERFORM pg_advisory_xact_lock(740206);
END
$metadata_taxonomy_and_names$;

-- Include nonofficial_aliases in PostgreSQL identity candidate terms so
-- community/unofficial aliases participate in candidate discovery.
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

DROP INDEX IF EXISTS catalog.entities_identity_candidates_idx;
CREATE INDEX entities_identity_candidates_idx ON catalog.entities
    USING gin (catalog.identity_candidate_terms(document)) WHERE state <> 'deleted';

-- Upgrade default system shelves from free-text tags to controlled creation_form
-- vocabulary terms when their query still matches the untouched built-in seed.
UPDATE catalog.shelves AS s
SET query = u.new_query
FROM (VALUES
  ('music',     '["music","song","album"]'::jsonb,             '{"vocab_terms":{"creation_form":["original_album","single","doujin_album","music"]}}'::jsonb),
  ('music',     '["song","album"]'::jsonb,                     '{"vocab_terms":{"creation_form":["original_album","single","doujin_album","music"]}}'::jsonb),
  ('anime',     '["animation"]'::jsonb,                        '{"vocab_terms":{"creation_form":["tv_anime","theater_anime","ova"]}}'::jsonb),
  ('films',     '["film"]'::jsonb,                             '{"vocab_terms":{"creation_form":["film","tv_drama","screen"]}}'::jsonb),
  ('novels',    '["novel"]'::jsonb,                            '{"vocab_terms":{"creation_form":["light_novel","novel_general","manga_serialized","manga_volume","literature"]}}'::jsonb),
  ('games',     '["game","indie_game","visual_novel"]'::jsonb, '{"vocab_terms":{"creation_form":["game","commercial_game","indie_game","visual_novel","eroge"]}}'::jsonb),
  ('creations', '["personal","photobook"]'::jsonb,             '{"vocab_terms":{"creation_form":["photography","photobook","personal","audio_drama","radio_drama"]}}'::jsonb)
) AS u(slug, old_tags, new_query)
WHERE s.slug = u.slug
  AND s.query->'tags' = u.old_tags
  AND COALESCE(s.query->'fields', '{}'::jsonb) = '{}'::jsonb
  AND COALESCE(s.query->'vocab_terms', '{}'::jsonb) = '{}'::jsonb
  AND COALESCE(s.query->'relations', '[]'::jsonb) = '[]'::jsonb;

UPDATE catalog.schema_contract SET version = 39 WHERE singleton = true;
