-- One relationship read projection serves details, editors and graph tools.
-- Correct only the shipped credit labels; preserve administrator customizations.
DO $$
DECLARE
    current_document jsonb;
    next_document jsonb;
    item record;
    label_path text[];
BEGIN
    SELECT document INTO current_document FROM catalog.definition_config WHERE singleton FOR UPDATE;
    IF current_document IS NOT NULL THEN
        next_document := current_document;
        FOR item IN SELECT * FROM (VALUES
            ('names','zh-CN','参与制作','参与主体'),
            ('names','zh-TW','參與製作','參與主體'),
            ('names','ja','クレジット','参加主体'),
            ('names','ja-JP','クレジット','参加主体'),
            ('names','en-US','Credited in','Contributors'),
            ('reverse_names','zh-CN','署名人员','参与记录'),
            ('reverse_names','zh-TW','署名人員','參與記錄'),
            ('reverse_names','ja','クレジット担当','参加記録'),
            ('reverse_names','ja-JP','クレジット担当','参加記録'),
            ('reverse_names','en-US','Credits','Contributions')
        ) AS labels(side,locale,old_label,new_label)
        LOOP
            label_path := ARRAY['relations','credit_for',item.side,item.locale];
            IF next_document #>> label_path = item.old_label THEN
                next_document := jsonb_set(next_document,label_path,to_jsonb(item.new_label),false);
            END IF;
        END LOOP;
        IF next_document IS DISTINCT FROM current_document THEN
            UPDATE catalog.definition_config SET document=next_document,etag=gen_random_uuid(),updated_at=now() WHERE singleton;
        END IF;
    END IF;
END $$;
UPDATE catalog.schema_contract SET version=33 WHERE singleton;
