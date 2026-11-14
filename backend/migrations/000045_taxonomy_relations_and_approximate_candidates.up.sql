DO $taxonomy_lock$
BEGIN
  PERFORM pg_advisory_xact_lock(740206);
END
$taxonomy_lock$;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Locale-independent core title normalization kept identical across Go, PostgreSQL, and TypeScript.
CREATE OR REPLACE FUNCTION catalog.identity_core_title(value text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$
    WITH s0 AS (
        SELECT catalog.identity_title(translate(
            value,
            U&'\3000' || '！＂＃＄％＆＇（）＊＋，－．／０１２３４５６７８９：；＜＝＞？＠ＡＢＣＤＥＦＧＨＩＪＫＬＭＮＯＰＱＲＳＴＵＶＷＸＹＺ［＼］＾＿｀ａｂｃｄｅｆｇｈｉｊｋｌｍｎｏｐｑｒｓｔｕｖｗｘｙｚ｛｜｝～',
            ' ' || '!"#$%&''()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqrstuvwxyz{|}~'
        )) AS cur
    ), s1 AS (
        SELECT CASE
            WHEN cur = '' THEN ''
            ELSE COALESCE(NULLIF(btrim(regexp_replace(
                cur,
                '^(?:株式会社|有限会社|合同会社|合資会社|一般社団法人|公益財団法人|\(株\)|\(有\)|㈱|㈲)\s*|\s*(?:株式会社|有限会社|合同会社|合資会社|一般社団法人|公益財団法人|\(株\)|\(有\)|㈱|㈲)$',
                '',
                'g'
            )), ''), cur)
        END AS cur
        FROM s0
    ), s2 AS (
        SELECT CASE
            WHEN cur = '' THEN ''
            ELSE COALESCE(NULLIF(btrim(regexp_replace(
                cur,
                '(?:\s*,\s*|\s+)(?:co\.?,?\s*ltd\.?|inc\.?|corp\.?|corporation|ltd\.?|llc|gmbh)$',
                '',
                'i'
            )), ''), cur)
        END AS cur
        FROM s1
    ), s3 AS (
        SELECT CASE
            WHEN cur = '' THEN ''
            ELSE COALESCE(NULLIF(btrim(regexp_replace(
                cur,
                '\s*(?:[(]|\[|【)\s*(?:tv\s*anime|tvアニメ|テレビアニメ|アニメ|anime|劇場版|劇場|映画|movie|film|ova|ona|漫画|マンガ|manga|コミック|comic|小説|ライトノベル|novel|light\s*novel|ゲーム|game|シングル|single|アルバム|album|ep|ost|サウンドトラック|soundtrack|cd|bd|dvd|blu-?ray|lp|mv|pv|バンド|band|ユニット|unit|グループ|group|キャラクター|character)\s*(?:[)]|\]|】)$',
                '',
                'i'
            )), ''), cur)
        END AS cur
        FROM s2
    )
    SELECT CASE
        WHEN cur = '' THEN ''
        ELSE COALESCE(NULLIF(regexp_replace(
            cur,
            '[\x01-\x2F\x3A-\x40\x5B-\x60\x7B-\x7F\u00A0-\u00BF\u2000-\u206F\u2E00-\u2E7F\u3000-\u3004\u3008-\u303A\u303D-\u303F\u30FB\uFE30-\uFE6F\uFF61-\uFF65]+',
            '',
            'g'
        ), ''), cur)
    END
    FROM s3
$fn$;

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

-- Upgrade published definition_config idempotently when already seeded.
DO $upgrade_definitions$
DECLARE
    cur_doc jsonb;
    next_doc jsonb;
    tpl_key text;
    tpl_val jsonb;
    rel_item record;
    cur_arr jsonb;
BEGIN
    SELECT document INTO cur_doc FROM catalog.definition_config WHERE singleton FOR UPDATE;
    IF cur_doc IS NOT NULL THEN
        next_doc := cur_doc;

        -- 1. collection_type vocabulary and field
        IF next_doc #> '{vocabularies,collection_type}' IS NULL THEN
            next_doc := jsonb_set(next_doc, '{vocabularies,collection_type}', '{
                "names": {"zh-CN":"集合类型","zh-TW":"集合類型","ja":"コレクション種別","ja-JP":"コレクション種別","en-US":"Collection type"},
                "terms": {}
            }'::jsonb, true);
        END IF;
        IF next_doc #> '{vocabularies,collection_type,terms}' IS NOT NULL THEN
            FOR rel_item IN SELECT * FROM (VALUES
                ('franchise', '{"names":{"zh-CN":"跨媒体企划","zh-TW":"跨媒體企劃","ja":"メディアミックス企画","ja-JP":"メディアミックス企画","en-US":"Media franchise"},"enabled":true}'::jsonb),
                ('series', '{"names":{"zh-CN":"作品系列","zh-TW":"作品系列","ja":"作品シリーズ","ja-JP":"作品シリーズ","en-US":"Work series"},"enabled":true}'::jsonb),
                ('anthology', '{"names":{"zh-CN":"选集 / 丛书","zh-TW":"選集 / 叢書","ja":"アンソロジー・叢書","ja-JP":"アンソロジー・叢書","en-US":"Anthology / imprint series"},"enabled":true}'::jsonb),
                ('event_catalog', '{"names":{"zh-CN":"展会 / 活动特辑","zh-TW":"展會 / 活動特輯","ja":"イベント・特集","ja-JP":"イベント・特集","en-US":"Event / special collection"},"enabled":true}'::jsonb)
            ) AS t(code, val) LOOP
                IF next_doc #> ARRAY['vocabularies', 'collection_type', 'terms', rel_item.code] IS NULL THEN
                    next_doc := jsonb_set(next_doc, ARRAY['vocabularies', 'collection_type', 'terms', rel_item.code], rel_item.val, true);
                END IF;
            END LOOP;
        END IF;
        IF next_doc #> '{fields,collection_type}' IS NULL THEN
            next_doc := jsonb_set(next_doc, '{fields,collection_type}', '{
                "names": {"zh-CN":"集合类型","zh-TW":"集合類型","ja":"コレクション種別","ja-JP":"コレクション種別","en-US":"Collection type"},
                "type": "enum",
                "applicable_kinds": ["collection"],
                "unit": null,
                "required": false,
                "enabled": true,
                "searchable": true,
                "comparable": true,
                "vocabulary": "collection_type"
            }'::jsonb, true);
        END IF;

        -- Ensure begin_date and end_date apply to collection, and creation_form is comparable
        FOR tpl_key IN SELECT unnest(ARRAY['begin_date', 'end_date']) LOOP
            IF next_doc #> ARRAY['fields', tpl_key] IS NOT NULL THEN
                cur_arr := COALESCE(next_doc #> ARRAY['fields', tpl_key, 'applicable_kinds'], '[]'::jsonb);
                IF NOT (cur_arr @> '"collection"'::jsonb) THEN
                    next_doc := jsonb_set(next_doc, ARRAY['fields', tpl_key, 'applicable_kinds'], cur_arr || '["collection"]'::jsonb, true);
                END IF;
            END IF;
        END LOOP;
        IF next_doc #> '{fields,creation_form}' IS NOT NULL THEN
            next_doc := jsonb_set(next_doc, '{fields,creation_form,comparable}', 'true'::jsonb, true);
        END IF;

        -- 2. New creation_form terms
        IF next_doc #> '{vocabularies,creation_form,terms}' IS NOT NULL THEN
            FOR rel_item IN SELECT * FROM (VALUES
                ('ep', '{"names":{"zh-CN":"迷你专辑 / EP","zh-TW":"迷你專輯 / EP","ja":"ミニアルバム・EP","ja-JP":"ミニアルバム・EP","en-US":"Extended play (EP)"},"enabled":true}'::jsonb),
                ('soundtrack', '{"names":{"zh-CN":"原声集","zh-TW":"原聲集","ja":"サウンドトラック","ja-JP":"サウンドトラック","en-US":"Original soundtrack"},"enabled":true}'::jsonb),
                ('compilation_album', '{"names":{"zh-CN":"精选集 / 合辑","zh-TW":"精選輯 / 合輯","ja":"ベスト・コンピレーション","ja-JP":"ベスト・コンピレーション","en-US":"Compilation album"},"enabled":true}'::jsonb),
                ('ona', '{"names":{"zh-CN":"Web动画","zh-TW":"網路動畫","ja":"Webアニメ","ja-JP":"Webアニメ","en-US":"Web animation (ONA)"},"enabled":true}'::jsonb),
                ('artbook', '{"names":{"zh-CN":"画集 / 设定集","zh-TW":"畫集 / 設定集","ja":"画集・設定資料集","ja-JP":"画集・設定資料集","en-US":"Artbook / setting book"},"enabled":true}'::jsonb),
                ('music_video', '{"names":{"zh-CN":"音乐视频","zh-TW":"音樂影片","ja":"ミュージックビデオ","ja-JP":"ミュージックビデオ","en-US":"Music video"},"enabled":true}'::jsonb)
            ) AS t(code, val) LOOP
                IF next_doc #> ARRAY['vocabularies', 'creation_form', 'terms', rel_item.code] IS NULL THEN
                    next_doc := jsonb_set(next_doc, ARRAY['vocabularies', 'creation_form', 'terms', rel_item.code], rel_item.val, true);
                END IF;
            END LOOP;
        END IF;

        -- 3. New work, collection, and structural default templates
        FOR rel_item IN SELECT * FROM (VALUES
            ('ep', '{"names":{"zh-CN":"迷你专辑 / EP","zh-TW":"迷你專輯 / EP","ja":"ミニアルバム・EP","ja-JP":"ミニアルバム・EP","en-US":"Extended play (EP)"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","duration","duration_source"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"edition_date","badge_fields":["creation_form"],"facet_fields":["creation_form"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"ep"}],"priority":10}'::jsonb),
            ('soundtrack', '{"names":{"zh-CN":"原声集","zh-TW":"原聲集","ja":"サウンドトラック","ja-JP":"サウンドトラック","en-US":"Original soundtrack"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","duration","duration_source"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"edition_date","badge_fields":["creation_form"],"facet_fields":["creation_form"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"soundtrack"}],"priority":10}'::jsonb),
            ('compilation_album', '{"names":{"zh-CN":"精选集 / 合辑","zh-TW":"精選輯 / 合輯","ja":"ベスト・コンピレーション","ja-JP":"ベスト・コンピレーション","en-US":"Compilation album"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","duration","duration_source"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"edition_date","badge_fields":["creation_form"],"facet_fields":["creation_form"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"compilation_album"}],"priority":10}'::jsonb),
            ('ona', '{"names":{"zh-CN":"Web动画","zh-TW":"網路動畫","ja":"Webアニメ","ja-JP":"Webアニメ","en-US":"Web animation (ONA)"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","episodes","platform"]},{"names":{"zh-CN":"放送信息","zh-TW":"放送資訊","ja":"放送情報","ja-JP":"放送情報","en-US":"Broadcast"},"fields":["broadcast_start","broadcast_weekday","broadcast_end","air_network"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"broadcast_start","badge_fields":["creation_form","air_network","episodes"],"facet_fields":["creation_form","broadcast_weekday"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"ona"}],"priority":10}'::jsonb),
            ('artbook', '{"names":{"zh-CN":"画集 / 设定集","zh-TW":"畫集 / 設定集","ja":"画集・設定資料集","ja-JP":"画集・設定資料集","en-US":"Artbook / setting book"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","volume_count"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"edition_date","badge_fields":["creation_form","volume_count"],"facet_fields":["creation_form"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"artbook"}],"priority":10}'::jsonb),
            ('music_video', '{"names":{"zh-CN":"音乐视频","zh-TW":"音樂影片","ja":"ミュージックビデオ","ja-JP":"ミュージックビデオ","en-US":"Music video"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["creation_form","language","duration","platform"]},{"names":{"zh-CN":"创作与权利","zh-TW":"創作與權利","ja":"制作・権利","ja-JP":"制作・権利","en-US":"Credits & rights"},"fields":["author","copyright","imdb"]},{"names":{"zh-CN":"首发与收录","zh-TW":"首發與收錄","ja":"初出・収録","ja-JP":"初出・収録","en-US":"Premiere & inclusion"},"fields":["edition_date","events"]}],"columns":["edition_date"],"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"edition_date","badge_fields":["creation_form","platform"],"facet_fields":["creation_form"],"kinds":["work"],"match":[{"field":"creation_form","operator":"equals","value":"music_video"}],"priority":10}'::jsonb),
            ('collection_profile', '{"names":{"zh-CN":"集合资料","zh-TW":"集合資料","ja":"コレクション情報","ja-JP":"コレクション情報","en-US":"Collection profile"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["collection_type","language"]},{"names":{"zh-CN":"时间范围","zh-TW":"時間範圍","ja":"期間","ja-JP":"期間","en-US":"Period"},"fields":["begin_date","end_date"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"begin_date","badge_fields":["collection_type"],"facet_fields":["collection_type"],"kinds":["collection"],"match":[],"priority":10}'::jsonb),
            ('collection_franchise', '{"names":{"zh-CN":"跨媒体企划","zh-TW":"跨媒體企劃","ja":"メディアミックス企画","ja-JP":"メディアミックス企画","en-US":"Media franchise"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["collection_type","language"]},{"names":{"zh-CN":"时间范围","zh-TW":"時間範圍","ja":"期間","ja-JP":"期間","en-US":"Period"},"fields":["begin_date","end_date"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"begin_date","badge_fields":["collection_type"],"facet_fields":["collection_type"],"kinds":["collection"],"match":[{"field":"collection_type","operator":"equals","value":"franchise"}],"priority":20}'::jsonb),
            ('collection_series', '{"names":{"zh-CN":"作品系列","zh-TW":"作品系列","ja":"作品シリーズ","ja-JP":"作品シリーズ","en-US":"Work series"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["collection_type","language"]},{"names":{"zh-CN":"时间范围","zh-TW":"時間範圍","ja":"期間","ja-JP":"期間","en-US":"Period"},"fields":["begin_date","end_date"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"begin_date","badge_fields":["collection_type"],"facet_fields":["collection_type"],"kinds":["collection"],"match":[{"field":"collection_type","operator":"equals","value":"series"}],"priority":20}'::jsonb),
            ('collection_anthology', '{"names":{"zh-CN":"选集 / 丛书","zh-TW":"選集 / 叢書","ja":"アンソロジー・叢書","ja-JP":"アンソロジー・叢書","en-US":"Anthology / imprint series"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["collection_type","language"]},{"names":{"zh-CN":"时间范围","zh-TW":"時間範圍","ja":"期間","ja-JP":"期間","en-US":"Period"},"fields":["begin_date","end_date"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"begin_date","badge_fields":["collection_type"],"facet_fields":["collection_type"],"kinds":["collection"],"match":[{"field":"collection_type","operator":"equals","value":"anthology"}],"priority":20}'::jsonb),
            ('collection_event_catalog', '{"names":{"zh-CN":"展会 / 活动特辑","zh-TW":"展會 / 活動特輯","ja":"イベント・特集","ja-JP":"イベント・特集","en-US":"Event / special collection"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["collection_type","language"]},{"names":{"zh-CN":"时间范围","zh-TW":"時間範圍","ja":"期間","ja-JP":"期間","en-US":"Period"},"fields":["begin_date","end_date"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"begin_date","badge_fields":["collection_type"],"facet_fields":["collection_type"],"kinds":["collection"],"match":[{"field":"collection_type","operator":"equals","value":"event_catalog"}],"priority":20}'::jsonb),
            ('content_unit_default', '{"names":{"zh-CN":"内容单元","zh-TW":"內容單元","ja":"コンテンツ単位","ja-JP":"コンテンツ単位","en-US":"Content unit"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["entry_role","air_date","language"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","primary_date_field":"air_date","badge_fields":["entry_role"],"facet_fields":["entry_role"],"kinds":["content_unit"],"match":[],"priority":10}'::jsonb),
            ('expression_default', '{"names":{"zh-CN":"内容表达","zh-TW":"內容表達","ja":"内容表現","ja-JP":"内容表現","en-US":"Expression"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["version_label","language","duration","isrc","events"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"tree","badge_fields":["version_label","isrc"],"kinds":["expression"],"match":[],"priority":10}'::jsonb),
            ('medium_default', '{"names":{"zh-CN":"载体","zh-TW":"載體","ja":"キャリア","ja-JP":"キャリア","en-US":"Medium"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["format","role","catalog_number"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"discs","badge_fields":["format","role","catalog_number"],"facet_fields":["format","role"],"kinds":["medium"],"match":[],"priority":10}'::jsonb),
            ('track_default', '{"names":{"zh-CN":"收录位置","zh-TW":"收錄位置","ja":"収録位置","ja-JP":"収録位置","en-US":"Track"},"sections":[{"names":{"zh-CN":"基本信息","zh-TW":"基本資訊","ja":"基本情報","ja-JP":"基本情報","en-US":"Basics"},"fields":["role","duration"]}],"columns":null,"relation_groups":["credits","creative","membership"],"directory":"list","badge_fields":["role"],"facet_fields":["role"],"kinds":["track"],"match":[],"priority":10}'::jsonb)
        ) AS t(code, val) LOOP
            IF next_doc #> ARRAY['templates', rel_item.code] IS NULL THEN
                next_doc := jsonb_set(next_doc, ARRAY['templates', rel_item.code], rel_item.val, true);
            END IF;
        END LOOP;

        -- Prepend creation_form to existing work creation_form templates where absent
        FOR tpl_key, tpl_val IN SELECT key, value FROM jsonb_each(COALESCE(next_doc->'templates', '{}'::jsonb)) LOOP
            IF tpl_val->'kinds' = '["work"]'::jsonb AND jsonb_typeof(tpl_val->'match') = 'array' AND jsonb_array_length(tpl_val->'match') = 1 AND (tpl_val #>> '{match,0,field}') = 'creation_form' THEN
                cur_arr := CASE WHEN jsonb_typeof(tpl_val->'badge_fields') = 'array' THEN tpl_val->'badge_fields' ELSE '[]'::jsonb END;
                IF NOT (cur_arr @> '"creation_form"'::jsonb) THEN
                    tpl_val := jsonb_set(tpl_val, '{badge_fields}', '["creation_form"]'::jsonb || cur_arr, true);
                END IF;
                cur_arr := CASE WHEN jsonb_typeof(tpl_val->'facet_fields') = 'array' THEN tpl_val->'facet_fields' ELSE '[]'::jsonb END;
                IF NOT (cur_arr @> '"creation_form"'::jsonb) THEN
                    tpl_val := jsonb_set(tpl_val, '{facet_fields}', '["creation_form"]'::jsonb || cur_arr, true);
                END IF;
                IF jsonb_typeof(tpl_val->'sections') = 'array' AND jsonb_array_length(tpl_val->'sections') > 0 THEN
                    cur_arr := CASE WHEN jsonb_typeof(tpl_val #> '{sections,0,fields}') = 'array' THEN tpl_val #> '{sections,0,fields}' ELSE '[]'::jsonb END;
                    IF NOT (cur_arr @> '"creation_form"'::jsonb) THEN
                        tpl_val := jsonb_set(tpl_val, '{sections,0,fields}', '["creation_form"]'::jsonb || cur_arr, true);
                    END IF;
                END IF;
                next_doc := jsonb_set(next_doc, ARRAY['templates', tpl_key], tpl_val, true);
            END IF;
        END LOOP;

        -- 4. produced_by relation, disambiguated created_by labels, and expanded credit source_kinds
        IF next_doc #> '{relations,produced_by}' IS NULL THEN
            next_doc := jsonb_set(next_doc, '{relations,produced_by}', '{
                "names": {"zh-CN":"制作 / 出品","zh-TW":"製作 / 出品","ja":"制作・プロデュース","ja-JP":"制作・プロデュース","en-US":"Produced by"},
                "reverse_names": {"zh-CN":"制作了","zh-TW":"製作了","ja":"制作・プロデュースした","ja-JP":"制作・プロデュースした","en-US":"Producer of"},
                "source_kinds": ["work","content_unit","expression","release"],
                "target_kinds": ["agent"],
                "fields": ["role","credit_role","character_rank","context","character","language","begin_date","end_date","scope"],
                "symmetric": false,
                "acyclic": false,
                "max_outgoing": 0,
                "max_incoming": 0,
                "participant_slot": "person",
                "counts_as_credit": true,
                "group": "credits",
                "group_names": {"zh-CN":"演职与主体","zh-TW":"演職與主體","ja":"スタッフ・主体","ja-JP":"スタッフ・主体","en-US":"Credits"},
                "enabled": true
            }'::jsonb, true);
        END IF;
        IF next_doc #>> '{relations,created_by,names,zh-CN}' = '创作者' THEN
            next_doc := jsonb_set(next_doc, '{relations,created_by,names}', '{"zh-CN":"原作 / 作者","zh-TW":"原作 / 作者","ja":"原作・著者","ja-JP":"原作・著者","en-US":"Created by"}'::jsonb, true);
        END IF;
        IF next_doc #>> '{relations,created_by,reverse_names,zh-CN}' = '创作了' THEN
            next_doc := jsonb_set(next_doc, '{relations,created_by,reverse_names}', '{"zh-CN":"原作 / 著作","zh-TW":"原作 / 著作","ja":"原作・著作","ja-JP":"原作・著作","en-US":"Creator of"}'::jsonb, true);
        END IF;
        FOR rel_item IN SELECT * FROM (VALUES
            ('voiced_by', 'agent'),
            ('arranged_by', 'content_unit'),
            ('directed_by', 'expression'),
            ('written_by', 'expression'),
            ('illustrated_by', 'expression'),
            ('narrated_by', 'work'),
            ('narrated_by', 'content_unit'),
            ('translated_by', 'release')
        ) AS r(code, kind_code) LOOP
            IF next_doc #> ARRAY['relations', rel_item.code] IS NOT NULL THEN
                cur_arr := COALESCE(next_doc #> ARRAY['relations', rel_item.code, 'source_kinds'], '[]'::jsonb);
                IF NOT (cur_arr @> to_jsonb(rel_item.kind_code)) THEN
                    next_doc := jsonb_set(next_doc, ARRAY['relations', rel_item.code, 'source_kinds'], cur_arr || jsonb_build_array(rel_item.kind_code), true);
                END IF;
            END IF;
        END LOOP;

        IF next_doc IS DISTINCT FROM cur_doc THEN
            UPDATE catalog.definition_config SET document = next_doc, etag = gen_random_uuid()::text, updated_at = now() WHERE singleton;
        END IF;
    END IF;
END
$upgrade_definitions$;

-- Upgrade built-in shelves when still matching untouched 000039 defaults.
UPDATE catalog.shelves AS s
SET query = u.new_query
FROM (VALUES
  ('music',  '{"vocab_terms":{"creation_form":["original_album","single","doujin_album","music"]}}'::jsonb,
             '{"vocab_terms":{"creation_form":["song","single","ep","original_album","doujin_album","soundtrack","compilation_album","music_video","music"]}}'::jsonb),
  ('anime',  '{"vocab_terms":{"creation_form":["tv_anime","theater_anime","ova"]}}'::jsonb,
             '{"vocab_terms":{"creation_form":["tv_anime","theater_anime","ova","ona"]}}'::jsonb),
  ('novels', '{"vocab_terms":{"creation_form":["light_novel","novel_general","manga_serialized","manga_volume","literature"]}}'::jsonb,
             '{"vocab_terms":{"creation_form":["light_novel","novel_general","manga_serialized","manga_volume","artbook","literature"]}}'::jsonb)
) AS u(slug, old_query, new_query)
WHERE s.slug = u.slug AND s.query = u.old_query;

-- Migrate existing catalog relations and primary subtype classifications atomically,
-- emitting outbox notifications and recording a system integrate commit when data changes.
DO $migrate_catalog_facts$
DECLARE
    def_doc jsonb;
    mig_now timestamptz := now();
    mig_now_iso text := to_char(mig_now AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"');
    mig_note text := 'Canonicalize credit relations and primary subtype classifications (000045)';
    sys_commit_id uuid;
    sys_seq bigint;
    change_count int;
BEGIN
    SELECT document INTO def_doc FROM catalog.definition_config WHERE singleton;
    CREATE TEMP TABLE mf045_revisions (revision_id bigint PRIMARY KEY) ON COMMIT DROP;

    -- 1. Reclassify exact canonical credit_role entries from fallback credit_for into specific relation codes.
    CREATE TEMP TABLE mf045_role_map (
        role_label text PRIMARY KEY,
        new_type text NOT NULL,
        source_kinds text[] NOT NULL
    ) ON COMMIT DROP;
    INSERT INTO mf045_role_map(role_label, new_type, source_kinds) VALUES
        ('アニメーション制作', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('动画制作', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('動畫製作', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('制作', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('製作', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('出品', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('制片人', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('製片人', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('プロデューサー', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('サークル', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('メーカー', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('出版社', 'produced_by', ARRAY['work','content_unit','expression','release']),
        ('原作', 'created_by', ARRAY['work','content_unit','expression','release']),
        ('原案', 'created_by', ARRAY['work','content_unit','expression','release']),
        ('著者', 'created_by', ARRAY['work','content_unit','expression','release']),
        ('作者', 'created_by', ARRAY['work','content_unit','expression','release']),
        ('監督', 'directed_by', ARRAY['work','content_unit','expression']),
        ('総監督', 'directed_by', ARRAY['work','content_unit','expression']),
        ('导演', 'directed_by', ARRAY['work','content_unit','expression']),
        ('導演', 'directed_by', ARRAY['work','content_unit','expression']),
        ('脚本', 'written_by', ARRAY['work','content_unit','expression']),
        ('シリーズ構成', 'written_by', ARRAY['work','content_unit','expression']),
        ('系列构成', 'written_by', ARRAY['work','content_unit','expression']),
        ('系列構成', 'written_by', ARRAY['work','content_unit','expression']),
        ('编剧', 'written_by', ARRAY['work','content_unit','expression']),
        ('編劇', 'written_by', ARRAY['work','content_unit','expression']),
        ('シナリオ', 'written_by', ARRAY['work','content_unit','expression']),
        ('作曲', 'composed_by', ARRAY['work','content_unit','expression']),
        ('音楽', 'composed_by', ARRAY['work','content_unit','expression']),
        ('作詞', 'lyricist_of', ARRAY['work','content_unit','expression']),
        ('作词', 'lyricist_of', ARRAY['work','content_unit','expression']),
        ('編曲', 'arranged_by', ARRAY['work','content_unit','expression']),
        ('编曲', 'arranged_by', ARRAY['work','content_unit','expression']),
        ('キャラクターデザイン', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('キャラクター原案', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('人物设定', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('人物設定', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('角色设计', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('角色設計', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('人物原案', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('イラスト', 'illustrated_by', ARRAY['work','content_unit','expression','release']),
        ('撮影', 'photographed_by', ARRAY['work','content_unit','expression','release']),
        ('撮影監督', 'photographed_by', ARRAY['work','content_unit','expression','release']),
        ('摄影监督', 'photographed_by', ARRAY['work','content_unit','expression','release']),
        ('攝影監督', 'photographed_by', ARRAY['work','content_unit','expression','release']),
        ('開発', 'developed_by', ARRAY['work','content_unit','expression','release']),
        ('开发', 'developed_by', ARRAY['work','content_unit','expression','release']),
        ('開發', 'developed_by', ARRAY['work','content_unit','expression','release']),
        ('翻訳', 'translated_by', ARRAY['work','content_unit','expression','release']),
        ('译者', 'translated_by', ARRAY['work','content_unit','expression','release']),
        ('譯者', 'translated_by', ARRAY['work','content_unit','expression','release']),
        ('ナレーション', 'narrated_by', ARRAY['work','content_unit','expression','release']),
        ('旁白', 'narrated_by', ARRAY['work','content_unit','expression','release']),
        ('声優', 'voiced_by', ARRAY['work','content_unit','expression','release','agent']),
        ('配音', 'voiced_by', ARRAY['work','content_unit','expression','release','agent']),
        ('主題歌演出', 'performed_by', ARRAY['work','content_unit','expression','release']),
        ('主题歌演出', 'performed_by', ARRAY['work','content_unit','expression','release']),
        ('挿入歌演出', 'performed_by', ARRAY['work','content_unit','expression','release']),
        ('插入歌演出', 'performed_by', ARRAY['work','content_unit','expression','release']),
        ('歌唱', 'performed_by', ARRAY['work','content_unit','expression','release']),
        ('演唱', 'performed_by', ARRAY['work','content_unit','expression','release']);

    CREATE TEMP TABLE mf045_credit_candidates ON COMMIT DROP AS
    SELECT
        r.id,
        r.source_id,
        r.target_id,
        m.new_type,
        COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) AS norm_attrs
    FROM catalog.relations r
    JOIN mf045_role_map m ON m.role_label = btrim(COALESCE(r.document #>> '{attributes,credit_role}', ''))
    JOIN catalog.entities src_e ON src_e.id = r.source_id AND src_e.kind = ANY(m.source_kinds)
    JOIN catalog.entities tgt_e ON tgt_e.id = r.target_id AND tgt_e.kind = 'agent'
    WHERE r.type = 'credit_for'
      AND (
          def_doc IS NULL OR (
              COALESCE((def_doc #>> ARRAY['relations', m.new_type, 'enabled'])::boolean, false)
              AND (COALESCE(NULLIF(def_doc #> ARRAY['relations', m.new_type, 'source_kinds'], 'null'::jsonb), '[]'::jsonb) @> to_jsonb(src_e.kind))
              AND (COALESCE(NULLIF(def_doc #> ARRAY['relations', m.new_type, 'target_kinds'], 'null'::jsonb), '[]'::jsonb) @> to_jsonb(tgt_e.kind))
              AND COALESCE((def_doc #>> ARRAY['relations', m.new_type, 'max_outgoing'])::int, 0) = 0
              AND COALESCE((def_doc #>> ARRAY['relations', m.new_type, 'max_incoming'])::int, 0) = 0
              AND NOT COALESCE((def_doc #>> ARRAY['relations', m.new_type, 'unique_position'])::boolean, false)
              AND NOT COALESCE((def_doc #>> ARRAY['relations', m.new_type, 'acyclic'])::boolean, false)
              AND COALESCE(def_doc #>> ARRAY['relations', m.new_type, 'scope'], '') = ''
              AND COALESCE(def_doc #>> ARRAY['relations', m.new_type, 'usage'], '') = ''
              AND COALESCE(NULLIF(def_doc #> ARRAY['relations', m.new_type, 'reference_scopes'], 'null'::jsonb), '{}'::jsonb) = '{}'::jsonb
              AND NOT EXISTS (
                  SELECT 1 FROM jsonb_object_keys(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb)) ak
                  WHERE NOT (COALESCE(NULLIF(def_doc #> ARRAY['relations', m.new_type, 'fields'], 'null'::jsonb), '[]'::jsonb) @> to_jsonb(ak))
                     OR NOT COALESCE((def_doc #>> ARRAY['fields', ak, 'enabled'])::boolean, false)
              )
              AND NOT EXISTS (
                  SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(def_doc #> ARRAY['relations', m.new_type, 'fields'], 'null'::jsonb), '[]'::jsonb)) rf
                  WHERE COALESCE((def_doc #>> ARRAY['fields', rf, 'required'])::boolean, false)
                    AND (
                        NOT (COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) ? rf)
                        OR COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) -> rf IN ('null'::jsonb, '[]'::jsonb, '{}'::jsonb)
                        OR (
                            jsonb_typeof(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) -> rf) = 'string'
                            AND btrim(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) ->> rf) = ''
                        )
                    )
              )
          )
      );

    CREATE TEMP TABLE mf045_credit_plan ON COMMIT DROP AS
    SELECT
        c.id,
        c.source_id,
        c.target_id,
        c.new_type,
        c.norm_attrs,
        EXISTS (
            SELECT 1 FROM catalog.relations ex
            WHERE ex.source_id = c.source_id
              AND ex.target_id = c.target_id
              AND ex.type = c.new_type
              AND c.norm_attrs <@ COALESCE(NULLIF(ex.document->'attributes', 'null'::jsonb), '{}'::jsonb)
        ) OR EXISTS (
            SELECT 1 FROM mf045_credit_candidates peer
            WHERE peer.source_id = c.source_id
              AND peer.target_id = c.target_id
              AND peer.new_type = c.new_type
              AND peer.id <> c.id
              AND c.norm_attrs <@ peer.norm_attrs
              AND (NOT (peer.norm_attrs <@ c.norm_attrs) OR peer.id < c.id)
        ) AS subsumed_exists
    FROM mf045_credit_candidates c;

    WITH del_dup AS (
        DELETE FROM catalog.relations r
        USING mf045_credit_plan p
        WHERE r.id = p.id AND p.subsumed_exists
        RETURNING r.id, r.version + 1 AS del_version, r.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, del_version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM del_dup
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'relation.deleted', id::text, del_version, document FROM del_dup
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    WITH upd_rel AS (
        UPDATE catalog.relations r
        SET type = p.new_type,
            version = r.version + 1,
            document = jsonb_set(
                jsonb_set(
                    jsonb_set(r.document, '{attributes}', p.norm_attrs, true),
                    '{type}', to_jsonb(p.new_type), true
                ),
                '{version}', to_jsonb(r.version + 1), true
            )
        FROM mf045_credit_plan p
        WHERE r.id = p.id AND NOT p.subsumed_exists
        RETURNING r.id, r.version, r.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM upd_rel
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'relation.saved', id::text, version, document FROM upd_rel
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    -- 2. Deduplicate or reclassify organization created_by edges (studios/companies/makers) into produced_by.
    -- Only delete when an existing/peer produced_by edge contains every attribute of the old edge (<@);
    -- edges carrying distinct context, language, dates, or role attributes are preserved as produced_by.
    CREATE TEMP TABLE mf045_org_candidates ON COMMIT DROP AS
    SELECT
        r.id,
        r.source_id,
        r.target_id,
        COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) AS norm_attrs
    FROM catalog.relations r
    JOIN catalog.entities src_e ON src_e.id = r.source_id AND src_e.kind IN ('work','content_unit','expression','release')
    JOIN catalog.entities tgt_e ON tgt_e.id = r.target_id AND tgt_e.kind = 'agent'
    WHERE r.type = 'created_by'
      AND tgt_e.document #>> '{attributes,agent_type}' = 'organization'
      AND COALESCE(r.document #>> '{attributes,credit_role}', '') !~* '(原作|原案|著者|作者|original)'
      AND (
          def_doc IS NULL OR (
              COALESCE((def_doc #>> '{relations,produced_by,enabled}')::boolean, false)
              AND (COALESCE(NULLIF(def_doc #> '{relations,produced_by,source_kinds}', 'null'::jsonb), '[]'::jsonb) @> to_jsonb(src_e.kind))
              AND (COALESCE(NULLIF(def_doc #> '{relations,produced_by,target_kinds}', 'null'::jsonb), '[]'::jsonb) @> to_jsonb(tgt_e.kind))
              AND COALESCE((def_doc #>> '{relations,produced_by,max_outgoing}')::int, 0) = 0
              AND COALESCE((def_doc #>> '{relations,produced_by,max_incoming}')::int, 0) = 0
              AND NOT COALESCE((def_doc #>> '{relations,produced_by,unique_position}')::boolean, false)
              AND NOT COALESCE((def_doc #>> '{relations,produced_by,acyclic}')::boolean, false)
              AND COALESCE(def_doc #>> '{relations,produced_by,scope}', '') = ''
              AND COALESCE(def_doc #>> '{relations,produced_by,usage}', '') = ''
              AND COALESCE(NULLIF(def_doc #> '{relations,produced_by,reference_scopes}', 'null'::jsonb), '{}'::jsonb) = '{}'::jsonb
              AND NOT EXISTS (
                  SELECT 1 FROM jsonb_object_keys(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb)) ak
                  WHERE NOT (COALESCE(NULLIF(def_doc #> '{relations,produced_by,fields}', 'null'::jsonb), '[]'::jsonb) @> to_jsonb(ak))
                     OR NOT COALESCE((def_doc #>> ARRAY['fields', ak, 'enabled'])::boolean, false)
              )
              AND NOT EXISTS (
                  SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(def_doc #> '{relations,produced_by,fields}', 'null'::jsonb), '[]'::jsonb)) rf
                  WHERE COALESCE((def_doc #>> ARRAY['fields', rf, 'required'])::boolean, false)
                    AND (
                        NOT (COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) ? rf)
                        OR COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) -> rf IN ('null'::jsonb, '[]'::jsonb, '{}'::jsonb)
                        OR (
                            jsonb_typeof(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) -> rf) = 'string'
                            AND btrim(COALESCE(NULLIF(r.document->'attributes', 'null'::jsonb), '{}'::jsonb) ->> rf) = ''
                        )
                    )
              )
          )
      );

    CREATE TEMP TABLE mf045_org_plan ON COMMIT DROP AS
    SELECT
        c.id,
        c.norm_attrs,
        EXISTS (
            SELECT 1 FROM catalog.relations ex
            WHERE ex.source_id = c.source_id
              AND ex.target_id = c.target_id
              AND ex.type = 'produced_by'
              AND c.norm_attrs <@ COALESCE(NULLIF(ex.document->'attributes', 'null'::jsonb), '{}'::jsonb)
        ) OR EXISTS (
            SELECT 1 FROM mf045_org_candidates peer
            WHERE peer.source_id = c.source_id
              AND peer.target_id = c.target_id
              AND peer.id <> c.id
              AND c.norm_attrs <@ peer.norm_attrs
              AND (NOT (peer.norm_attrs <@ c.norm_attrs) OR peer.id < c.id)
        ) AS subsumed_by_produced_by
    FROM mf045_org_candidates c;

    WITH del_org_dup AS (
        DELETE FROM catalog.relations r
        USING mf045_org_plan p
        WHERE r.id = p.id AND p.subsumed_by_produced_by
        RETURNING r.id, r.version + 1 AS del_version, r.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, del_version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM del_org_dup
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'relation.deleted', id::text, del_version, document FROM del_org_dup
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    WITH upd_org_rel AS (
        UPDATE catalog.relations r
        SET type = 'produced_by',
            version = r.version + 1,
            document = jsonb_set(
                jsonb_set(
                    jsonb_set(r.document, '{attributes}', p.norm_attrs, true),
                    '{type}', '"produced_by"'::jsonb, true
                ),
                '{version}', to_jsonb(r.version + 1), true
            )
        FROM mf045_org_plan p
        WHERE r.id = p.id AND NOT p.subsumed_by_produced_by
        RETURNING r.id, r.version, r.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM upd_org_rel
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'relation.saved', id::text, version, document FROM upd_org_rel
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    -- 3. Fix misclassified Work creation_form (song / soundtrack) and backfill missing Collection collection_type.
    WITH work_fixes AS (
        SELECT
            e.id,
            CASE
                WHEN (
                    EXISTS (
                        SELECT 1 FROM jsonb_array_elements(
                            CASE WHEN jsonb_typeof(e.document->'external_links') = 'array' THEN e.document->'external_links' ELSE '[]'::jsonb END
                        ) l WHERE l->>'kind' IN ('recording_mbid', 'isrc')
                    )
                    AND NOT EXISTS (
                        SELECT 1 FROM jsonb_array_elements(
                            CASE WHEN jsonb_typeof(e.document->'external_links') = 'array' THEN e.document->'external_links' ELSE '[]'::jsonb END
                        ) l WHERE COALESCE(l->>'url', '') LIKE '%/release-group/%'
                    )
                ) OR (
                    EXISTS (SELECT 1 FROM catalog.release_subjects rs WHERE rs.work_id = e.id AND rs.role = 'track_work')
                    AND NOT EXISTS (SELECT 1 FROM catalog.release_subjects rs WHERE rs.work_id = e.id AND rs.role IN ('primary', 'compilation'))
                ) THEN 'song'
                WHEN e.title ~* '(オリジナル・?サウンドトラック|original\s+soundtrack)' THEN 'soundtrack'
                ELSE NULL
            END AS new_form
        FROM catalog.entities e
        WHERE e.kind = 'work'
          AND e.state <> 'deleted'
          AND e.document #>> '{attributes,creation_form}' = 'original_album'
    ), upd_works AS (
        UPDATE catalog.entities e
        SET version = e.version + 1,
            updated_at = mig_now,
            document = jsonb_set(jsonb_set(
                jsonb_set(
                    CASE WHEN jsonb_typeof(e.document->'attributes') = 'object' THEN e.document ELSE jsonb_set(e.document, '{attributes}', '{}'::jsonb, true) END,
                    '{attributes,creation_form}',
                    to_jsonb(wf.new_form),
                    true
                ),
                '{version}',
                to_jsonb(e.version + 1),
                true
            ), '{updated_at}', to_jsonb(mig_now_iso), true)
        FROM work_fixes wf
        WHERE e.id = wf.id AND wf.new_form IS NOT NULL
          AND (
              def_doc IS NULL OR (
                  COALESCE((def_doc #>> '{fields,creation_form,enabled}')::boolean, false)
                  AND (COALESCE(NULLIF(def_doc #> '{fields,creation_form,applicable_kinds}', 'null'::jsonb), '[]'::jsonb) @> '"work"'::jsonb)
                  AND COALESCE((def_doc #>> ARRAY['vocabularies', COALESCE(NULLIF(def_doc #>> '{fields,creation_form,vocabulary}', ''), 'creation_form'), 'terms', wf.new_form, 'enabled'])::boolean, false)
              )
          )
        RETURNING e.id, e.version, e.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM upd_works
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'entity.saved', id::text, version, document FROM upd_works
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    WITH collection_domains AS (
        SELECT
            e.id,
            e.title,
            (
                SELECT count(DISTINCT CASE w.document #>> '{attributes,creation_form}'
                    WHEN 'song' THEN 'music'
                    WHEN 'single' THEN 'music'
                    WHEN 'ep' THEN 'music'
                    WHEN 'original_album' THEN 'music'
                    WHEN 'doujin_album' THEN 'music'
                    WHEN 'soundtrack' THEN 'music'
                    WHEN 'compilation_album' THEN 'music'
                    WHEN 'music_video' THEN 'music'
                    WHEN 'music' THEN 'music'
                    WHEN 'tv_anime' THEN 'anime'
                    WHEN 'theater_anime' THEN 'anime'
                    WHEN 'ova' THEN 'anime'
                    WHEN 'ona' THEN 'anime'
                    WHEN 'manga_serialized' THEN 'manga'
                    WHEN 'manga_volume' THEN 'manga'
                    WHEN 'light_novel' THEN 'novel'
                    WHEN 'novel_general' THEN 'novel'
                    WHEN 'literature' THEN 'novel'
                    WHEN 'photobook' THEN 'photo'
                    WHEN 'photography' THEN 'photo'
                    WHEN 'visual_novel' THEN 'game'
                    WHEN 'eroge' THEN 'game'
                    WHEN 'indie_game' THEN 'game'
                    WHEN 'commercial_game' THEN 'game'
                    WHEN 'game' THEN 'game'
                    WHEN 'audio_drama' THEN 'audio_drama'
                    WHEN 'radio_drama' THEN 'audio_drama'
                    WHEN 'film' THEN 'live_action'
                    WHEN 'tv_drama' THEN 'live_action'
                    WHEN 'screen' THEN 'live_action'
                    ELSE NULL
                END)
                FROM catalog.relations r
                JOIN catalog.entities w ON w.id = r.target_id
                WHERE r.source_id = e.id
                  AND r.type = 'includes'
                  AND w.kind = 'work'
                  AND w.state = 'active'
            ) AS domain_count
        FROM catalog.entities e
        WHERE e.kind = 'collection'
          AND e.state <> 'deleted'
          AND COALESCE(e.document #>> '{attributes,collection_type}', '') = ''
    ), collection_fixes AS (
        SELECT
            cd.id,
            CASE
                WHEN cd.title ~* '(コミックマーケット|コミケ|comiket|例大祭|ワンフェス)' THEN 'event_catalog'
                WHEN cd.title ~* '(文庫|レーベル|アンソロジー|叢書|新書|anthology|imprint)' THEN 'anthology'
                WHEN cd.domain_count >= 2
                  OR (cd.domain_count = 0 AND cd.title ~* '(メディアミックス|跨媒体|跨媒體|franchise)') THEN 'franchise'
                ELSE 'series'
            END AS new_ctype
        FROM collection_domains cd
    ), upd_collections AS (
        UPDATE catalog.entities e
        SET version = e.version + 1,
            updated_at = mig_now,
            document = jsonb_set(jsonb_set(
                jsonb_set(
                    CASE WHEN jsonb_typeof(e.document->'attributes') = 'object' THEN e.document ELSE jsonb_set(e.document, '{attributes}', '{}'::jsonb, true) END,
                    '{attributes,collection_type}',
                    to_jsonb(cf.new_ctype),
                    true
                ),
                '{version}',
                to_jsonb(e.version + 1),
                true
            ), '{updated_at}', to_jsonb(mig_now_iso), true)
        FROM collection_fixes cf
        WHERE e.id = cf.id
          AND (
              def_doc IS NULL OR (
                  COALESCE((def_doc #>> '{fields,collection_type,enabled}')::boolean, false)
                  AND (COALESCE(NULLIF(def_doc #> '{fields,collection_type,applicable_kinds}', 'null'::jsonb), '[]'::jsonb) @> '"collection"'::jsonb)
                  AND COALESCE((def_doc #>> ARRAY['vocabularies', COALESCE(NULLIF(def_doc #>> '{fields,collection_type,vocabulary}', ''), 'collection_type'), 'terms', cf.new_ctype, 'enabled'])::boolean, false)
              )
          )
        RETURNING e.id, e.version, e.document
    ), ins_rev AS (
        INSERT INTO catalog.revisions(target_id, version, actor_id, actor_name, edit_note, sources, snapshot, created_at)
        SELECT id::text, version, NULL, 'system', mig_note, '[]'::jsonb, document, mig_now FROM upd_collections
        RETURNING id
    ), ins_outbox AS (
        INSERT INTO catalog.outbox(id, type, entity_id, version, payload)
        SELECT gen_random_uuid(), 'entity.saved', id::text, version, document FROM upd_collections
    )
    INSERT INTO mf045_revisions(revision_id) SELECT id FROM ins_rev;

    -- 4. Finalize pending_record_versions into a system integrate commit when any entity/relation changed.
    CREATE TEMP TABLE mf045_changes ON COMMIT DROP AS
    WITH pending AS (
        SELECT p.target, p.target_id,
            (
                SELECT v.document FROM catalog.catalog_record_versions v
                WHERE v.target = p.target AND v.target_id = p.target_id
                ORDER BY v.sequence DESC LIMIT 1
            ) AS before_doc,
            CASE
                WHEN p.target = 'entity' THEN (
                    SELECT
                        (e.document - ARRAY['work_id','release_id','medium_id','parent_id','content_unit_id','contents','subjects']) ||
                        jsonb_strip_nulls(jsonb_build_object(
                            'id', e.id, 'kind', e.kind, 'version', e.version, 'title', e.title,
                            'state', e.state, 'created_by', e.created_by,
                            'work_id', COALESCE(cu.work_id, ex.work_id),
                            'release_id', m.release_id,
                            'medium_id', t.medium_id,
                            'parent_id', COALESCE(cu.parent_id, m.parent_id, t.parent_id),
                            'content_unit_id', ex.content_unit_id,
                            'contents', CASE WHEN e.kind = 'track' THEN (
                                SELECT NULLIF(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
                                    'expression_id', x.expression_id, 'position', x.position, 'locator', x.locator,
                                    'attributes', NULLIF(x.attributes, '{}'::jsonb), 'sources', NULLIF(x.sources, '[]'::jsonb)
                                )) ORDER BY x.position), '[]'::jsonb)
                                FROM catalog.track_contents x WHERE x.track_id = e.id
                            ) END,
                            'subjects', CASE WHEN e.kind = 'release' THEN (
                                SELECT NULLIF(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
                                    'work_id', x.work_id, 'role', x.role, 'position', x.position,
                                    'attributes', NULLIF(x.attributes, '{}'::jsonb)
                                )) ORDER BY x.position, x.work_id), '[]'::jsonb)
                                FROM catalog.release_subjects x WHERE x.release_id = e.id
                            ) END
                        ))
                    FROM catalog.entities e
                    LEFT JOIN catalog.content_units cu ON cu.id = e.id
                    LEFT JOIN catalog.expressions ex ON ex.id = e.id
                    LEFT JOIN catalog.mediums m ON m.id = e.id
                    LEFT JOIN catalog.tracks t ON t.id = e.id
                    WHERE e.id = p.target_id
                )
                ELSE (
                    SELECT r.document FROM catalog.relations r WHERE r.id = p.target_id
                )
            END AS after_doc
        FROM catalog.pending_record_versions p
        WHERE p.transaction_id = txid_current()
    )
    SELECT target, target_id, before_doc, after_doc
    FROM pending
    WHERE before_doc IS DISTINCT FROM after_doc;

    SELECT count(*) INTO change_count FROM mf045_changes;
    IF change_count > 0 OR EXISTS (SELECT 1 FROM mf045_revisions) THEN
        sys_commit_id := gen_random_uuid();
        INSERT INTO catalog.commits(id, actor_id, request_hash, request, result, author_snapshot, parent_id, message, kind, snapshot_scope, merged_at, merged_by)
        SELECT
            sys_commit_id,
            'system',
            encode(sha256(convert_to('{"operation":"integrate","migration":45}', 'UTF8')), 'hex'),
            '{"operation":"integrate","migration":45}'::jsonb,
            '{}'::jsonb,
            '{"id":"system","username":"system"}'::jsonb,
            commit_id,
            mig_note,
            'rebase',
            'catalog',
            mig_now,
            'system'
        FROM catalog.catalog_head WHERE singleton FOR UPDATE;

        INSERT INTO catalog.commit_operations(commit_id, operation, source_ids)
        VALUES (sys_commit_id, 'integrate', '{}');

        INSERT INTO catalog.catalog_versions(commit_id)
        VALUES (sys_commit_id)
        RETURNING sequence INTO sys_seq;

        UPDATE catalog.catalog_head SET commit_id = sys_commit_id WHERE singleton;

        INSERT INTO catalog.catalog_record_versions(sequence, target, target_id, document)
        SELECT sys_seq, target, target_id, after_doc FROM mf045_changes;

        INSERT INTO catalog.commit_objects(commit_id, target, target_id, base_version, base_document, before_document, after_document)
        SELECT
            sys_commit_id,
            target,
            target_id,
            COALESCE((before_doc->>'version')::bigint, 0),
            before_doc,
            before_doc,
            after_doc
        FROM mf045_changes;

        INSERT INTO catalog.revision_commits(revision_id, commit_id)
        SELECT revision_id, sys_commit_id FROM mf045_revisions
        ON CONFLICT DO NOTHING;
    END IF;

    DELETE FROM catalog.pending_record_versions WHERE transaction_id = txid_current();
END
$migrate_catalog_facts$;

UPDATE catalog.schema_contract SET version = 45 WHERE singleton;
