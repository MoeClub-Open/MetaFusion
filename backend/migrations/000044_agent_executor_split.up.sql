ALTER TABLE assistant.chat_requests
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'sync' CHECK (mode IN ('sync', 'background')),
    ADD COLUMN IF NOT EXISTS execution_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS lease_owner text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS started_at timestamptz;

CREATE INDEX IF NOT EXISTS chat_requests_bg_pending_idx
    ON assistant.chat_requests (created_at, id)
    WHERE status = 'pending' AND mode = 'background';

ALTER TABLE curator.settings
    ADD COLUMN IF NOT EXISTS sweep_requested_at timestamptz;

UPDATE catalog.schema_contract SET version = 44 WHERE singleton;
