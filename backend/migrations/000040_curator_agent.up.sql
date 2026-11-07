-- Continuous catalog curator: deterministic detectors enqueue findings; a
-- background agent researches each finding and writes only through the
-- existing workspace -> commit -> push path. Low-risk repairs may be merged by
-- the server after a mechanical diff check; everything else stays a PR.
CREATE SCHEMA IF NOT EXISTS curator;

CREATE TABLE curator.settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    model_id uuid REFERENCES assistant.models(id) ON DELETE SET NULL,
    interval_seconds integer NOT NULL DEFAULT 300 CHECK (interval_seconds BETWEEN 30 AND 86400),
    max_runs_per_hour integer NOT NULL DEFAULT 12 CHECK (max_runs_per_hour BETWEEN 1 AND 240),
    monthly_credit_allowance bigint NOT NULL DEFAULT 0 CHECK (monthly_credit_allowance BETWEEN 0 AND 1000000000000),
    auto_merge_low_risk boolean NOT NULL DEFAULT true,
    allow_structures boolean NOT NULL DEFAULT true,
    allow_sources boolean NOT NULL DEFAULT true,
    allow_names boolean NOT NULL DEFAULT true,
    allow_dedupe boolean NOT NULL DEFAULT true,
    system_user_id text NOT NULL DEFAULT '3f0c0a17-0000-4000-8000-000000000001' CHECK (length(system_user_id) BETWEEN 1 AND 200),
    reviewer_user_id text NOT NULL DEFAULT '3f0c0a17-0000-4000-8000-000000000002' CHECK (length(reviewer_user_id) BETWEEN 1 AND 200),
    paused boolean NOT NULL DEFAULT false,
    paused_reason text NOT NULL DEFAULT '',
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    sweep_cursor_updated_at timestamptz,
    sweep_cursor_id uuid,
    sweep_detector integer NOT NULL DEFAULT -1,
    last_sweep_at timestamptz,
    last_run_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO curator.settings(singleton) VALUES (true);

CREATE TABLE curator.findings (
    id uuid PRIMARY KEY,
    entity_id uuid NOT NULL REFERENCES catalog.entities(id) ON DELETE CASCADE,
    entity_kind text NOT NULL,
    code text NOT NULL CHECK (code ~ '^[a-z0-9_]{2,64}$'),
    severity text NOT NULL CHECK (severity IN ('error','warning','info')),
    priority integer NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 3),
    signature text NOT NULL CHECK (length(signature) BETWEEN 1 AND 500),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','running','done','failed','ignored','resolved')),
    decision text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    sources jsonb NOT NULL DEFAULT '[]'::jsonb,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    last_run_id uuid,
    workspace_id uuid,
    pull_request_id uuid,
    merged boolean NOT NULL DEFAULT false,
    auto_merged boolean NOT NULL DEFAULT false,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code, signature)
);
CREATE INDEX curator_findings_queue_idx ON curator.findings (status, next_attempt_at, priority, created_at, id);
CREATE INDEX curator_findings_entity_idx ON curator.findings (entity_id, updated_at DESC);
CREATE INDEX curator_findings_code_idx ON curator.findings (code, status, updated_at DESC);

CREATE TABLE curator.runs (
    id uuid PRIMARY KEY,
    finding_id uuid REFERENCES curator.findings(id) ON DELETE SET NULL,
    entity_id uuid,
    kind text NOT NULL CHECK (kind IN ('agent','sweep','quality')),
    trigger_kind text NOT NULL CHECK (trigger_kind IN ('schedule','manual','sweep')),
    status text NOT NULL CHECK (status IN ('running','completed','failed','skipped')),
    model_id uuid,
    decision text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    sources jsonb NOT NULL DEFAULT '[]'::jsonb,
    tool_trace jsonb NOT NULL DEFAULT '[]'::jsonb,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    workspace_id uuid,
    pull_request_id uuid,
    merged boolean NOT NULL DEFAULT false,
    auto_merged boolean NOT NULL DEFAULT false,
    error text NOT NULL DEFAULT '',
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    credits bigint NOT NULL DEFAULT 0 CHECK (credits >= 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX curator_runs_started_idx ON curator.runs (started_at DESC, id DESC);
CREATE INDEX curator_runs_finding_idx ON curator.runs (finding_id, started_at DESC);

UPDATE catalog.schema_contract SET version = 40 WHERE singleton = true;
