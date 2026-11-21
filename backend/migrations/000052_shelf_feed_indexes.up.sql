-- 000052: 为活跃作品的 creation_form 排序与计数建立局部表达式索引，避免首页货架推荐流扫描全部作品。
CREATE INDEX IF NOT EXISTS entities_active_work_creation_form_updated_idx
    ON catalog.entities ((document->'attributes'->>'creation_form'), updated_at DESC, id)
    WHERE state = 'active' AND kind = 'work';

UPDATE catalog.schema_contract SET version = 52 WHERE singleton;
