-- 一次性数据迁移：在操作级 source_refs 之前写入的修订来源没有逐操作指针（没有 key），
-- 它们是整笔提交的来源池，不能证明支撑该修订的某个字段。统一标记 scope = "commit"，
-- 读取/展示端据此区分"整笔提交的来源"与"本条修订引用的来源"。
--
-- 幂等：只改写仍存在"无 key 且无 scope"元素的行；再次执行不会命中任何行。
-- 新代码写入的来源一律带 key，不会被这条规则误标。
-- 必须先于新版服务启动执行：schema_contract 升到 29，新二进制只接受 29。

UPDATE catalog.revisions
SET sources = (SELECT COALESCE(jsonb_agg(
        CASE WHEN jsonb_typeof(elem) = 'object' AND NOT (elem ? 'key') AND NOT (elem ? 'scope')
             THEN elem || '{"scope":"commit"}'::jsonb
             ELSE elem END
        ORDER BY ord), '[]'::jsonb)
     FROM jsonb_array_elements(sources) WITH ORDINALITY AS t(elem, ord))
WHERE jsonb_typeof(sources) = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(sources) e
              WHERE jsonb_typeof(e) = 'object' AND NOT (e ? 'key') AND NOT (e ? 'scope'));

-- lifecycle_actions 保存的是修订来源的副本（000027），同样标记，保持两处一致。
UPDATE catalog.lifecycle_actions
SET sources = (SELECT COALESCE(jsonb_agg(
        CASE WHEN jsonb_typeof(elem) = 'object' AND NOT (elem ? 'key') AND NOT (elem ? 'scope')
             THEN elem || '{"scope":"commit"}'::jsonb
             ELSE elem END
        ORDER BY ord), '[]'::jsonb)
     FROM jsonb_array_elements(sources) WITH ORDINALITY AS t(elem, ord))
WHERE jsonb_typeof(sources) = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(sources) e
              WHERE jsonb_typeof(e) = 'object' AND NOT (e ? 'key') AND NOT (e ? 'scope'));

UPDATE catalog.schema_contract SET version = 29 WHERE singleton = true;
