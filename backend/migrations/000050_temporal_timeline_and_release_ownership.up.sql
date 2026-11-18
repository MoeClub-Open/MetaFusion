-- 000050: 统一事件时间线索引（catalog.timeline_events）与历史事实查询索引。
-- 重要原则：
--   1. 仅索引真实事件（独立 event 实体与各实体 attributes.events[]），不将数据库修订时间
--      revisions.created_at 冒充现实世界事件时间或有效期；
--   2. 同一实体或关联发行链上的重复内嵌事件去重，未知日期保持空串；
--   3. recorded_at 记录数据库入库/修订时间（transaction_time），与 occurred_at / effective_at 分离。

CREATE TABLE IF NOT EXISTS catalog.timeline_events (
    id              text PRIMARY KEY,
    origin_kind     text NOT NULL CHECK (origin_kind IN ('event_entity', 'embedded_event')),
    owner_entity_id uuid NOT NULL REFERENCES catalog.entities(id) ON DELETE CASCADE,
    owner_kind      text NOT NULL,
    event_entity_id uuid REFERENCES catalog.entities(id) ON DELETE CASCADE,
    event_type      text NOT NULL DEFAULT 'other',
    title           text NOT NULL DEFAULT '',
    title_names     jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     text NOT NULL DEFAULT '',
    begin_date      text NOT NULL DEFAULT '',
    end_date        text NOT NULL DEFAULT '',
    ended           boolean NOT NULL DEFAULT false,
    effective_at    text NOT NULL DEFAULT '',
    date_precision  text NOT NULL DEFAULT '',
    earliest_date   text NOT NULL DEFAULT '',
    latest_date     text NOT NULL DEFAULT '',
    region          text NOT NULL DEFAULT '',
    platform        text NOT NULL DEFAULT '',
    channel         text NOT NULL DEFAULT '',
    release_id      uuid,
    target_ids      uuid[] NOT NULL DEFAULT '{}'::uuid[],
    related_assets  text[] NOT NULL DEFAULT '{}'::text[],
    source_refs     jsonb NOT NULL DEFAULT '[]'::jsonb,
    sort_date       text NOT NULL DEFAULT '',
    dedupe_key      text NOT NULL DEFAULT '',
    recorded_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS timeline_events_owner_idx
    ON catalog.timeline_events (owner_entity_id, sort_date, id);

CREATE INDEX IF NOT EXISTS timeline_events_targets_gin_idx
    ON catalog.timeline_events USING gin (target_ids);

CREATE INDEX IF NOT EXISTS timeline_events_release_idx
    ON catalog.timeline_events (release_id, sort_date)
    WHERE release_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS timeline_events_event_entity_idx
    ON catalog.timeline_events (event_entity_id)
    WHERE event_entity_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS timeline_events_type_idx
    ON catalog.timeline_events (event_type, sort_date);

-- 辅助函数：校验文本是否为合法 UUID，避免存量脏数据阻断回填。
CREATE OR REPLACE FUNCTION catalog.try_parse_uuid(raw text) RETURNS uuid
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF raw IS NULL OR btrim(raw) = '' THEN
        RETURN NULL;
    END IF;
    RETURN btrim(raw)::uuid;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$;

-- 回填 1：所有 active 独立 event 实体及其关联目标（含关系对端与关联 Release 的主作品）。
INSERT INTO catalog.timeline_events (
    id, origin_kind, owner_entity_id, owner_kind, event_entity_id,
    event_type, title, title_names, occurred_at, begin_date, end_date, ended,
    effective_at, date_precision, earliest_date, latest_date,
    region, platform, channel, release_id,
    target_ids, related_assets, source_refs, sort_date, dedupe_key, recorded_at
)
SELECT
    'event:' || e.id::text AS id,
    'event_entity' AS origin_kind,
    e.id AS owner_entity_id,
    e.kind AS owner_kind,
    e.id AS event_entity_id,
    CASE COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'event_type'), ''), 'other')
        WHEN 'release' THEN 'official_release'
        WHEN 'update' THEN 'version_update'
        WHEN 'discontinued' THEN 'service_end'
        WHEN 'milestone' THEN 'major_event'
        ELSE COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'event_type'), ''), 'other')
    END AS event_type,
    e.title AS title,
    COALESCE(
        (
            SELECT jsonb_object_agg(kv.key, kv.value->>'title')
            FROM jsonb_each(
                CASE WHEN jsonb_typeof(e.document->'translations') = 'object'
                     THEN e.document->'translations' ELSE '{}'::jsonb END
            ) AS kv(key, value)
            WHERE COALESCE(BTRIM(kv.value->>'title'), '') <> ''
        ),
        '{}'::jsonb
    ) AS title_names,
    COALESCE(
        NULLIF(BTRIM(e.document->'attributes'->>'begin_date'), ''),
        NULLIF(BTRIM(e.document->'attributes'->>'effective_at'), ''),
        ''
    ) AS occurred_at,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'begin_date'), ''), '') AS begin_date,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'end_date'), ''), '') AS end_date,
    COALESCE((e.document->'attributes'->>'ended')::boolean, false) AS ended,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'effective_at'), ''), '') AS effective_at,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'date_precision'), ''), '') AS date_precision,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'earliest_date'), ''), '') AS earliest_date,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'latest_date'), ''), '') AS latest_date,
    COALESCE(
        NULLIF(BTRIM(e.document->'attributes'->>'region'), ''),
        NULLIF(BTRIM(e.document->'attributes'->>'country'), ''),
        ''
    ) AS region,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'platform'), ''), '') AS platform,
    COALESCE(NULLIF(BTRIM(e.document->'attributes'->>'channel'), ''), '') AS channel,
    catalog.try_parse_uuid(e.document->'attributes'->>'scoped_release') AS release_id,
    ARRAY(
        SELECT DISTINCT u FROM (
            SELECT e.id AS u
            UNION
            SELECT catalog.try_parse_uuid(e.document->'attributes'->>'scoped_release')
            UNION
            SELECT catalog.try_parse_uuid(t_txt)
            FROM jsonb_array_elements_text(
                CASE WHEN jsonb_typeof(e.document->'attributes'->'target_ids') = 'array'
                     THEN e.document->'attributes'->'target_ids' ELSE '[]'::jsonb END
            ) AS t_txt
            UNION
            SELECT CASE WHEN r.source_id = e.id THEN r.target_id ELSE r.source_id END
            FROM catalog.relations r
            WHERE r.source_id = e.id OR r.target_id = e.id
            UNION
            SELECT rs.work_id
            FROM catalog.relations r
            JOIN catalog.release_subjects rs
              ON rs.release_id = (CASE WHEN r.source_id = e.id THEN r.target_id ELSE r.source_id END)
            WHERE r.source_id = e.id OR r.target_id = e.id
        ) raw_targets
        WHERE u IS NOT NULL
    ) AS target_ids,
    ARRAY(
        SELECT DISTINCT a FROM (
            SELECT NULLIF(BTRIM(pic->>'asset_id'), '') AS a
            FROM jsonb_array_elements(
                CASE WHEN jsonb_typeof(e.document->'pictures') = 'array'
                     THEN e.document->'pictures' ELSE '[]'::jsonb END
            ) AS pic
            UNION
            SELECT NULLIF(BTRIM(asset_txt), '')
            FROM jsonb_array_elements_text(
                CASE WHEN jsonb_typeof(e.document->'attributes'->'related_assets') = 'array'
                     THEN e.document->'attributes'->'related_assets' ELSE '[]'::jsonb END
            ) AS asset_txt
        ) raw_assets
        WHERE a IS NOT NULL
    ) AS related_assets,
    '[]'::jsonb AS source_refs,
    COALESCE(
        NULLIF(BTRIM(e.document->'attributes'->>'begin_date'), ''),
        NULLIF(BTRIM(e.document->'attributes'->>'effective_at'), ''),
        NULLIF(BTRIM(e.document->'attributes'->>'earliest_date'), ''),
        ''
    ) AS sort_date,
    'event_entity:' || e.id::text AS dedupe_key,
    e.updated_at AS recorded_at
FROM catalog.entities e
WHERE e.kind = 'event' AND e.state = 'active'
ON CONFLICT (id) DO NOTHING;

-- 回填 2：各 active 实体 attributes.events[] 规范化并去重（已绑定独立 event 实体的不重复生成，改为补入独立 event 的 target_ids）。
UPDATE catalog.timeline_events te
SET target_ids = (
    SELECT ARRAY(
        SELECT DISTINCT u FROM (
            SELECT unnest(te.target_ids) AS u
            UNION
            SELECT owner.id
            FROM catalog.entities owner,
                 jsonb_array_elements(
                     CASE WHEN jsonb_typeof(owner.document->'attributes'->'events') = 'array'
                          THEN owner.document->'attributes'->'events' ELSE '[]'::jsonb END
                 ) AS ev
            WHERE owner.state = 'active'
              AND catalog.try_parse_uuid(ev->>'event') = te.event_entity_id
            UNION
            SELECT rs.work_id
            FROM catalog.entities owner
            JOIN catalog.release_subjects rs ON rs.release_id = owner.id,
                 jsonb_array_elements(
                     CASE WHEN jsonb_typeof(owner.document->'attributes'->'events') = 'array'
                          THEN owner.document->'attributes'->'events' ELSE '[]'::jsonb END
                 ) AS ev
            WHERE owner.state = 'active'
              AND owner.kind = 'release'
              AND catalog.try_parse_uuid(ev->>'event') = te.event_entity_id
        ) merged_u
        WHERE u IS NOT NULL
    )
)
WHERE te.origin_kind = 'event_entity' AND te.event_entity_id IS NOT NULL;

WITH raw_embedded AS (
    SELECT
        e.id AS owner_entity_id,
        e.kind AS owner_kind,
        e.updated_at AS recorded_at,
        ord::int AS idx,
        ev,
        CASE COALESCE(NULLIF(BTRIM(ev->>'event_kind'), ''), 'other')
            WHEN 'release' THEN 'official_release'
            WHEN 'update' THEN 'version_update'
            WHEN 'discontinued' THEN 'service_end'
            WHEN 'milestone' THEN 'major_event'
            ELSE COALESCE(NULLIF(BTRIM(ev->>'event_kind'), ''), 'other')
        END AS norm_type,
        COALESCE(
            NULLIF(BTRIM(ev->'label'->>'zh-CN'), ''),
            NULLIF(BTRIM(ev->'label'->>'ja-JP'), ''),
            NULLIF(BTRIM(ev->'label'->>'ja'), ''),
            NULLIF(BTRIM(ev->'label'->>'en-US'), ''),
            NULLIF(BTRIM(ev->'label'->>'zh-TW'), ''),
            ''
        ) AS norm_title,
        CASE WHEN jsonb_typeof(ev->'label') = 'object' THEN ev->'label' ELSE '{}'::jsonb END AS title_names,
        COALESCE(NULLIF(BTRIM(ev->>'date'), ''), '') AS occurred_at,
        COALESCE(NULLIF(BTRIM(ev->>'end_date'), ''), '') AS end_date,
        COALESCE((ev->>'ended')::boolean, false) AS ended,
        COALESCE(NULLIF(BTRIM(ev->>'effective_at'), ''), '') AS effective_at,
        COALESCE(NULLIF(BTRIM(ev->>'date_precision'), ''), '') AS date_precision,
        COALESCE(NULLIF(BTRIM(ev->>'earliest_date'), ''), '') AS earliest_date,
        COALESCE(NULLIF(BTRIM(ev->>'latest_date'), ''), '') AS latest_date,
        COALESCE(NULLIF(BTRIM(ev->>'region'), ''), '') AS region,
        COALESCE(NULLIF(BTRIM(ev->>'platform'), ''), '') AS platform,
        COALESCE(NULLIF(BTRIM(ev->>'channel'), ''), '') AS channel,
        COALESCE(
            catalog.try_parse_uuid(ev->>'scoped_release'),
            CASE WHEN e.kind = 'release' THEN e.id END
        ) AS release_id,
        catalog.try_parse_uuid(ev->>'event') AS linked_event_id
    FROM catalog.entities e,
         jsonb_array_elements(
             CASE WHEN jsonb_typeof(e.document->'attributes'->'events') = 'array'
                  THEN e.document->'attributes'->'events' ELSE '[]'::jsonb END
         ) WITH ORDINALITY AS arr(ev, ord)
    WHERE e.state = 'active'
),
deduped_embedded AS (
    SELECT DISTINCT ON (
        owner_entity_id, norm_type, occurred_at, lower(norm_title), lower(region), lower(platform), lower(channel), COALESCE(release_id::text, '')
    )
        *
    FROM raw_embedded
    WHERE linked_event_id IS NULL
    ORDER BY
        owner_entity_id, norm_type, occurred_at, lower(norm_title), lower(region), lower(platform), lower(channel), COALESCE(release_id::text, ''), idx
)
INSERT INTO catalog.timeline_events (
    id, origin_kind, owner_entity_id, owner_kind, event_entity_id,
    event_type, title, title_names, occurred_at, begin_date, end_date, ended,
    effective_at, date_precision, earliest_date, latest_date,
    region, platform, channel, release_id,
    target_ids, related_assets, source_refs, sort_date, dedupe_key, recorded_at
)
SELECT
    'embedded:' || d.owner_entity_id::text || ':' || d.idx::text AS id,
    'embedded_event' AS origin_kind,
    d.owner_entity_id,
    d.owner_kind,
    NULL AS event_entity_id,
    d.norm_type AS event_type,
    d.norm_title AS title,
    d.title_names,
    d.occurred_at,
    d.occurred_at AS begin_date,
    d.end_date,
    d.ended,
    d.effective_at,
    d.date_precision,
    d.earliest_date,
    d.latest_date,
    d.region,
    d.platform,
    d.channel,
    d.release_id,
    ARRAY(
        SELECT DISTINCT u FROM (
            SELECT d.owner_entity_id AS u
            UNION
            SELECT d.release_id
            UNION
            SELECT catalog.try_parse_uuid(d.ev->>'store')
            UNION
            SELECT catalog.try_parse_uuid(d.ev->>'content')
            UNION
            SELECT catalog.try_parse_uuid(t_txt)
            FROM jsonb_array_elements_text(
                CASE WHEN jsonb_typeof(d.ev->'target_ids') = 'array'
                     THEN d.ev->'target_ids' ELSE '[]'::jsonb END
            ) AS t_txt
            UNION
            SELECT rs.work_id FROM catalog.release_subjects rs WHERE d.owner_kind = 'release' AND rs.release_id = d.owner_entity_id
        ) raw_u
        WHERE u IS NOT NULL
    ) AS target_ids,
    ARRAY(
        SELECT DISTINCT a FROM (
            SELECT NULLIF(BTRIM(asset_txt), '') AS a
            FROM jsonb_array_elements_text(
                CASE WHEN jsonb_typeof(d.ev->'related_assets') = 'array'
                     THEN d.ev->'related_assets' ELSE '[]'::jsonb END
            ) AS asset_txt
        ) raw_a
        WHERE a IS NOT NULL
    ) AS related_assets,
    CASE
        WHEN COALESCE(NULLIF(BTRIM(d.ev->>'source_url'), ''), '') <> ''
        THEN jsonb_build_array(jsonb_build_object('kind', 'url', 'citation', d.ev->>'source_url', 'url', d.ev->>'source_url'))
        ELSE '[]'::jsonb
    END AS source_refs,
    COALESCE(NULLIF(d.occurred_at, ''), NULLIF(d.effective_at, ''), NULLIF(d.earliest_date, ''), '') AS sort_date,
    md5(d.owner_entity_id::text || '|' || d.norm_type || '|' || d.occurred_at || '|' || lower(d.norm_title) || '|' || lower(d.region) || '|' || lower(d.platform) || '|' || lower(d.channel) || '|' || COALESCE(d.release_id::text, '')) AS dedupe_key,
    d.recorded_at
FROM deduped_embedded d
ON CONFLICT (id) DO NOTHING;

UPDATE catalog.schema_contract SET version = 50 WHERE singleton;
