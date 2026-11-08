ALTER TABLE assistant.models
    ADD COLUMN IF NOT EXISTS reasoning_effort text NOT NULL DEFAULT '';
ALTER TABLE assistant.models
    DROP CONSTRAINT IF EXISTS models_reasoning_effort_check,
    ADD CONSTRAINT models_reasoning_effort_check
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh','max'));

ALTER TABLE assistant.byo_models
    ADD COLUMN IF NOT EXISTS reasoning_effort text NOT NULL DEFAULT '';
ALTER TABLE assistant.byo_models
    DROP CONSTRAINT IF EXISTS byo_models_reasoning_effort_check,
    ADD CONSTRAINT byo_models_reasoning_effort_check
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh','max'));

ALTER TABLE assistant.conversations
    ADD COLUMN IF NOT EXISTS reasoning_effort text NOT NULL DEFAULT '';
ALTER TABLE assistant.conversations
    DROP CONSTRAINT IF EXISTS conversations_reasoning_effort_check,
    ADD CONSTRAINT conversations_reasoning_effort_check
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh','max'));

ALTER TABLE assistant.missions
    ADD COLUMN IF NOT EXISTS reasoning_effort text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS worker_reasoning_effort text NOT NULL DEFAULT '';
ALTER TABLE assistant.missions
    DROP CONSTRAINT IF EXISTS missions_reasoning_effort_check,
    DROP CONSTRAINT IF EXISTS missions_worker_reasoning_effort_check,
    ADD CONSTRAINT missions_reasoning_effort_check
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh','max')),
    ADD CONSTRAINT missions_worker_reasoning_effort_check
    CHECK (worker_reasoning_effort IN ('','none','minimal','low','medium','high','xhigh','max'));

ALTER TABLE curator.settings
    ALTER COLUMN execution SET DEFAULT '{"child_model_id":"","reasoning_effort":"","child_reasoning_effort":"","billing_owner_id":"","search_provider_id":"","search_byo_provider_id":"","max_children":3,"allow_reviews":true,"auto_merge_reviews":false}'::jsonb;

UPDATE curator.settings
SET execution = jsonb_build_object('reasoning_effort', '', 'child_reasoning_effort', '') || execution;

UPDATE catalog.schema_contract SET version = 42 WHERE singleton;
