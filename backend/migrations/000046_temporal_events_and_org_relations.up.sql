-- Extend entity kind and external database constraints to support top-level event entities.
ALTER TABLE catalog.entities DROP CONSTRAINT entities_kind_check;
ALTER TABLE catalog.entities ADD CONSTRAINT entities_kind_check
    CHECK (kind = ANY (ARRAY['agent'::text, 'collection'::text, 'work'::text, 'content_unit'::text, 'expression'::text, 'release'::text, 'medium'::text, 'track'::text, 'event'::text]));

ALTER TABLE catalog.external_databases DROP CONSTRAINT external_databases_category_check;
ALTER TABLE catalog.external_databases ADD CONSTRAINT external_databases_category_check
    CHECK (category = ANY (ARRAY['all'::text, 'agent'::text, 'collection'::text, 'work'::text, 'content_unit'::text, 'expression'::text, 'release'::text, 'medium'::text, 'track'::text, 'event'::text]));

ALTER TABLE catalog.external_links DROP CONSTRAINT external_links_entity_kind_check;
ALTER TABLE catalog.external_links ADD CONSTRAINT external_links_entity_kind_check
    CHECK (entity_kind = ANY (ARRAY['agent'::text, 'collection'::text, 'work'::text, 'content_unit'::text, 'expression'::text, 'release'::text, 'medium'::text, 'track'::text, 'event'::text]));

UPDATE catalog.schema_contract SET version = 46 WHERE singleton;
