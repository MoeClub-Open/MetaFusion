-- 000051: 为带标签的活跃实体建立局部索引，避免 /api/catalog/tags 冷计算扫描全部实体。
CREATE INDEX IF NOT EXISTS entities_active_with_tags
    ON catalog.entities (id)
    WHERE state = 'active' AND jsonb_typeof(document->'attributes'->'tags') = 'array';

UPDATE catalog.schema_contract SET version = 51 WHERE singleton;
