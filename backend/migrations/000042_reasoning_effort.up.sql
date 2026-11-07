ALTER TABLE assistant.models
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh'));

ALTER TABLE assistant.byo_models
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh'));

ALTER TABLE assistant.conversations
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh'));

ALTER TABLE assistant.missions
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('','none','minimal','low','medium','high','xhigh')),
    ADD COLUMN worker_reasoning_effort text NOT NULL DEFAULT ''
    CHECK (worker_reasoning_effort IN ('','none','minimal','low','medium','high','xhigh'));

ALTER TABLE curator.settings
    ALTER COLUMN execution SET DEFAULT '{"child_model_id":"","reasoning_effort":"","child_reasoning_effort":"","billing_owner_id":"","search_provider_id":"","search_byo_provider_id":"","max_children":3,"allow_reviews":true,"auto_merge_reviews":false}'::jsonb;

UPDATE curator.settings
SET execution = jsonb_build_object('reasoning_effort', '', 'child_reasoning_effort', '') || execution;

UPDATE catalog.schema_contract SET version = 42 WHERE singleton;
