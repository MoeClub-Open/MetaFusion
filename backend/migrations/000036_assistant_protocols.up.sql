ALTER TABLE assistant.providers DROP CONSTRAINT providers_protocol_check;
ALTER TABLE assistant.providers ADD CONSTRAINT providers_protocol_check
    CHECK (protocol IN ('openai_compatible','openai_responses','anthropic_messages','gemini_generate_content'));
ALTER TABLE assistant.byo_credentials ADD COLUMN protocol text NOT NULL DEFAULT 'openai_compatible'
    CHECK (protocol IN ('openai_compatible','openai_responses','anthropic_messages','gemini_generate_content'));
UPDATE catalog.schema_contract SET version=36 WHERE singleton;
