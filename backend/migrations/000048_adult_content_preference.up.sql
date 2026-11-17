ALTER TABLE catalog.user_preferences
  ADD COLUMN IF NOT EXISTS adult_content_mode text NOT NULL DEFAULT 'blur'
  CHECK (adult_content_mode IN ('hide', 'blur', 'show'));

UPDATE catalog.schema_contract SET version = 48 WHERE singleton;
