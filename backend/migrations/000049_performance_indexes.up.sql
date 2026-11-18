DROP INDEX IF EXISTS catalog.entities_attributes_lookup;
CREATE INDEX entities_attributes_lookup ON catalog.entities
    USING gin ((document->'attributes') jsonb_path_ops);

DROP INDEX IF EXISTS catalog.relations_document_attributes_gin;
CREATE INDEX relations_document_attributes_gin ON catalog.relations
    USING gin ((document->'attributes') jsonb_path_ops);

UPDATE catalog.schema_contract SET version = 49 WHERE singleton;
