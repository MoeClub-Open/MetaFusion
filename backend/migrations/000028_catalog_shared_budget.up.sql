-- One shared read/write budget per verified subject, across catalog replicas.
CREATE TABLE catalog.api_rate_windows (
    subject_key text PRIMARY KEY,
    window_start timestamptz NOT NULL,
    used integer NOT NULL CHECK (used > 0)
);
CREATE INDEX api_rate_windows_expiry_idx ON catalog.api_rate_windows(window_start);
UPDATE catalog.schema_contract SET version = 28 WHERE singleton = true;
