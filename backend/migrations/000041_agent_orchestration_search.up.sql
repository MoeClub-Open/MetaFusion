CREATE TABLE assistant.byo_providers (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL CHECK (owner_id <> ''),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    base_url text NOT NULL,
    protocol text NOT NULL DEFAULT 'openai_compatible'
        CHECK (protocol IN ('openai_compatible','openai_responses','anthropic_messages','gemini_generate_content')),
    enabled boolean NOT NULL DEFAULT true,
    encrypted_key bytea NOT NULL,
    key_hint text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, owner_id)
);
CREATE INDEX byo_providers_owner_idx ON assistant.byo_providers(owner_id, created_at DESC, id);

CREATE TABLE assistant.byo_models (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL CHECK (owner_id <> ''),
    provider_id uuid NOT NULL,
    upstream_model text NOT NULL CHECK (length(upstream_model) BETWEEN 1 AND 200),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    enabled boolean NOT NULL DEFAULT true,
    max_output_tokens bigint NOT NULL DEFAULT 4096 CHECK (max_output_tokens BETWEEN 1 AND 10000000),
    max_tokens_parameter text NOT NULL DEFAULT 'max_tokens'
        CHECK (max_tokens_parameter IN ('max_tokens','max_completion_tokens')),
    context_window bigint NOT NULL DEFAULT 65536
        CHECK (context_window BETWEEN 1 AND 10000000 AND context_window >= max_output_tokens),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (provider_id, owner_id) REFERENCES assistant.byo_providers(id, owner_id) ON DELETE CASCADE,
    UNIQUE (provider_id, upstream_model)
);
CREATE INDEX byo_models_owner_idx ON assistant.byo_models(owner_id, name, id);

-- Preserve BYO provider IDs so existing AES-GCM AAD ("byo:" || id) ciphertexts remain valid.
INSERT INTO assistant.byo_providers(id, owner_id, name, base_url, protocol, enabled, encrypted_key, key_hint, created_at, updated_at)
SELECT id, owner_id, name, base_url, protocol, enabled, encrypted_key, key_hint, created_at, updated_at
FROM assistant.byo_credentials;

INSERT INTO assistant.byo_models(id, owner_id, provider_id, upstream_model, name, enabled, created_at, updated_at)
SELECT gen_random_uuid(), owner_id, id, upstream_model, left(name || ' · ' || upstream_model, 200), enabled, created_at, updated_at
FROM assistant.byo_credentials
WHERE upstream_model <> '';

UPDATE assistant.conversations c
SET model_id = m.id
FROM assistant.byo_models m
WHERE c.byo_credential_id IS NOT NULL AND m.provider_id = c.byo_credential_id AND m.owner_id = c.owner_id;

ALTER TABLE assistant.conversations DROP COLUMN byo_credential_id;
ALTER TABLE assistant.usage RENAME COLUMN byo_credential_id TO byo_provider_id;
DROP TABLE assistant.byo_credentials;

CREATE TABLE assistant.search_providers (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('duckduckgo','deepseek')),
    base_url text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    is_default boolean NOT NULL DEFAULT false,
    encrypted_key bytea NOT NULL DEFAULT ''::bytea,
    key_hint text NOT NULL DEFAULT '',
    credits_per_request bigint NOT NULL DEFAULT 0 CHECK (credits_per_request BETWEEN 0 AND 1000000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (NOT is_default OR enabled),
    CHECK (kind <> 'duckduckgo' OR credits_per_request = 0)
);
CREATE UNIQUE INDEX search_provider_default_idx ON assistant.search_providers(is_default) WHERE is_default;
INSERT INTO assistant.search_providers(id,name,kind,base_url,enabled,is_default) VALUES
 ('3f0c0a17-0000-4000-8000-000000000010','DuckDuckGo','duckduckgo','https://html.duckduckgo.com',true,true),
 ('3f0c0a17-0000-4000-8000-000000000011','DeepSeek','deepseek','https://api.deepseek.com',false,false);

-- Search uses the same accounts and credit entries as model calls, but is
-- priced per successful request, in its original currency, never as tokens.
CREATE TABLE assistant.search_requests (
    id uuid PRIMARY KEY,
    user_id text NOT NULL,
    provider_id uuid NOT NULL REFERENCES assistant.search_providers(id),
    byo_provider_id uuid REFERENCES assistant.byo_providers(id) ON DELETE SET NULL,
    purpose text NOT NULL,
    operation text NOT NULL CHECK (operation IN ('search','fetch')),
    input jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('reserved','completed','empty','failed','unknown')),
    credits bigint NOT NULL CHECK (credits >= 0),
    upstream_cost_microcny bigint NOT NULL DEFAULT 0,
    result jsonb,
    error text NOT NULL DEFAULT '',
    budget_month date NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX search_requests_owner_idx ON assistant.search_requests(user_id,created_at DESC,id);
CREATE INDEX search_requests_budget_idx ON assistant.search_requests(user_id,budget_month,purpose);

ALTER TABLE curator.settings DROP CONSTRAINT settings_model_id_fkey;
ALTER TABLE curator.settings ADD COLUMN execution jsonb NOT NULL DEFAULT
 '{"child_model_id":"","billing_owner_id":"","search_provider_id":"","search_byo_provider_id":"","max_children":3,"allow_reviews":true,"auto_merge_reviews":false}'::jsonb;
ALTER TABLE curator.findings ALTER COLUMN entity_id DROP NOT NULL;
ALTER TABLE curator.findings ADD COLUMN review_pr_id uuid REFERENCES catalog.pull_requests(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX curator_review_task_idx ON curator.findings(review_pr_id,signature) WHERE review_pr_id IS NOT NULL;
ALTER TABLE curator.runs DROP CONSTRAINT runs_kind_check;
ALTER TABLE curator.runs ADD CONSTRAINT runs_kind_check CHECK (kind IN ('manager','agent','sweep','quality'));
ALTER TABLE curator.runs ADD COLUMN parent_run_id uuid REFERENCES curator.runs(id);
ALTER TABLE curator.runs ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE curator.runs ADD COLUMN messages jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX curator_runs_parent_idx ON curator.runs(parent_run_id,started_at,id);
CREATE UNIQUE INDEX curator_delegate_once_idx ON curator.runs(parent_run_id,finding_id) WHERE parent_run_id IS NOT NULL;

CREATE TABLE assistant.missions (
    id uuid PRIMARY KEY,
    client_request_id uuid NOT NULL UNIQUE,
    owner_id text NOT NULL CHECK (owner_id <> ''),
    scope text NOT NULL CHECK (scope IN ('user','curator')),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    goal text NOT NULL CHECK (length(goal) BETWEEN 1 AND 16000),
    steering text NOT NULL DEFAULT '',
    pending_steer boolean NOT NULL DEFAULT false,
    status text NOT NULL CHECK (status IN ('running','paused','completed','stopped','failed')),
    model_id uuid,
    worker_model_id uuid,
    search_provider_id uuid REFERENCES assistant.search_providers(id) ON DELETE SET NULL,
    search_byo_provider_id uuid REFERENCES assistant.byo_providers(id) ON DELETE SET NULL,
    billing_user_id text NOT NULL CHECK (billing_user_id <> ''),
    byo_owner_id text NOT NULL DEFAULT '',
    prefer_pull_request boolean NOT NULL DEFAULT true,
    actor_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    budget_credits bigint NOT NULL DEFAULT 0 CHECK (budget_credits >= 0),
    spent_credits bigint NOT NULL DEFAULT 0 CHECK (spent_credits >= 0),
    max_workers integer NOT NULL DEFAULT 2 CHECK (max_workers BETWEEN 1 AND 4),
    max_tasks integer NOT NULL DEFAULT 12 CHECK (max_tasks BETWEEN 1 AND 50),
    planner_rounds integer NOT NULL DEFAULT 0 CHECK (planner_rounds >= 0),
    max_planner_rounds integer NOT NULL DEFAULT 8 CHECK (max_planner_rounds BETWEEN 1 AND 20),
    plan jsonb NOT NULL DEFAULT '[]'::jsonb,
    summary text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    lease_owner text NOT NULL DEFAULT '',
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX missions_owner_idx ON assistant.missions(scope, owner_id, updated_at DESC, id);
CREATE INDEX missions_running_idx ON assistant.missions(status, lease_until) WHERE status = 'running';

CREATE TABLE assistant.mission_tasks (
    id uuid PRIMARY KEY,
    mission_id uuid NOT NULL REFERENCES assistant.missions(id) ON DELETE CASCADE,
    seq integer NOT NULL CHECK (seq >= 1),
    role text NOT NULL CHECK (role IN ('recon','research','edit','review')),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 240),
    objective text NOT NULL CHECK (length(objective) BETWEEN 1 AND 8000),
    depends_on jsonb NOT NULL DEFAULT '[]'::jsonb,
    anchor_entity_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL CHECK (status IN ('queued','running','completed','failed','skipped')),
    priority integer NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 5),
    result_summary text NOT NULL DEFAULT '',
    discoveries jsonb NOT NULL DEFAULT '[]'::jsonb,
    sources jsonb NOT NULL DEFAULT '[]'::jsonb,
    workspace_id uuid,
    pull_request_id uuid,
    merged boolean NOT NULL DEFAULT false,
    model_id uuid,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    credits bigint NOT NULL DEFAULT 0,
    messages jsonb NOT NULL DEFAULT '[]'::jsonb,
    error text NOT NULL DEFAULT '',
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (mission_id, seq)
);
CREATE INDEX mission_tasks_mission_idx ON assistant.mission_tasks(mission_id, seq);

CREATE TABLE assistant.mission_planner_runs (
    id uuid PRIMARY KEY,
    mission_id uuid NOT NULL REFERENCES assistant.missions(id) ON DELETE CASCADE,
    round integer NOT NULL CHECK (round >= 1),
    trigger text NOT NULL,
    status text NOT NULL CHECK (status IN ('running','completed','failed')),
    summary text NOT NULL DEFAULT '',
    model_id uuid,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    credits bigint NOT NULL DEFAULT 0,
    messages jsonb NOT NULL DEFAULT '[]'::jsonb,
    error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    UNIQUE (mission_id, round)
);
CREATE INDEX mission_planner_runs_mission_idx ON assistant.mission_planner_runs(mission_id, round);

UPDATE catalog.schema_contract SET version = 41 WHERE singleton;
