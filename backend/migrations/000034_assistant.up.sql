-- LLM configuration and financial records are separate from catalog facts.
CREATE SCHEMA IF NOT EXISTS assistant;
CREATE TABLE assistant.providers (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    base_url text NOT NULL,
    protocol text NOT NULL CHECK (protocol='openai_compatible'),
    enabled boolean NOT NULL DEFAULT false,
    encrypted_key bytea NOT NULL DEFAULT ''::bytea,
    key_hint text NOT NULL DEFAULT '',
    monthly_budget_microusd bigint NOT NULL DEFAULT 0 CHECK (monthly_budget_microusd>=0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE assistant.models (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES assistant.providers(id) ON DELETE RESTRICT,
    upstream_model text NOT NULL CHECK (length(upstream_model) BETWEEN 1 AND 200),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    enabled boolean NOT NULL DEFAULT false,
    input_price_microusd_per_million bigint NOT NULL DEFAULT 0 CHECK (input_price_microusd_per_million>=0),
    cached_input_price_microusd_per_million bigint NOT NULL DEFAULT 0 CHECK (cached_input_price_microusd_per_million>=0),
    output_price_microusd_per_million bigint NOT NULL DEFAULT 0 CHECK (output_price_microusd_per_million>=0),
    input_credits_per_million bigint NOT NULL DEFAULT 0 CHECK (input_credits_per_million>=0),
    output_credits_per_million bigint NOT NULL DEFAULT 0 CHECK (output_credits_per_million>=0),
    max_output_tokens bigint NOT NULL DEFAULT 4096 CHECK (max_output_tokens BETWEEN 1 AND 10000000),
    max_tokens_parameter text NOT NULL DEFAULT 'max_tokens' CHECK(max_tokens_parameter IN ('max_tokens','max_completion_tokens')),
    context_window bigint NOT NULL DEFAULT 32768 CHECK (context_window BETWEEN 1 AND 10000000 AND context_window>=max_output_tokens),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(provider_id,upstream_model)
);
CREATE TABLE assistant.settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    enabled boolean NOT NULL DEFAULT false,
    default_model_id uuid REFERENCES assistant.models(id) ON DELETE SET NULL,
    registration_credits bigint NOT NULL DEFAULT 1000 CHECK (registration_credits>=0),
    accepted_edit_credits bigint NOT NULL DEFAULT 100 CHECK (accepted_edit_credits>=0),
    reward_effective_at timestamptz NOT NULL DEFAULT now(),
    monthly_budget_microusd bigint NOT NULL DEFAULT 0 CHECK (monthly_budget_microusd>=0),
    max_tool_rounds integer NOT NULL DEFAULT 6 CHECK(max_tool_rounds BETWEEN 1 AND 12),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO assistant.settings(singleton) VALUES(true);
CREATE TABLE assistant.byo_credentials (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL CHECK(owner_id<>''),
    name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
    base_url text NOT NULL,
    upstream_model text NOT NULL DEFAULT '' CHECK(length(upstream_model)<=200),
    enabled boolean NOT NULL DEFAULT true,
    encrypted_key bytea NOT NULL,
    key_hint text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX byo_owner_idx ON assistant.byo_credentials(owner_id,created_at DESC,id);
CREATE TABLE assistant.accounts (
    user_id text PRIMARY KEY,
    credits bigint NOT NULL DEFAULT 0,
    reserved_credits bigint NOT NULL DEFAULT 0 CHECK(reserved_credits>=0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE assistant.credit_entries (
    id uuid PRIMARY KEY,
    user_id text NOT NULL REFERENCES assistant.accounts(user_id),
    kind text NOT NULL CHECK(kind IN ('registration','accepted_edit','usage','adjustment')),
    reference_id text NOT NULL,
    credits bigint NOT NULL,
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id,kind,reference_id)
);
CREATE INDEX credit_entries_user_idx ON assistant.credit_entries(user_id,created_at DESC,id);
CREATE INDEX credit_entries_recent_idx ON assistant.credit_entries(created_at DESC,id DESC);
CREATE TABLE assistant.budget_months (
    scope text NOT NULL,
    month date NOT NULL,
    spent_microusd bigint NOT NULL DEFAULT 0 CHECK(spent_microusd>=0),
    reserved_microusd bigint NOT NULL DEFAULT 0 CHECK(reserved_microusd>=0),
    PRIMARY KEY(scope,month)
);
CREATE TABLE assistant.usage (
    id uuid PRIMARY KEY,
    user_id text NOT NULL,
    provider_id uuid NOT NULL,
    model_id uuid NOT NULL,
    byo_credential_id uuid,
    purpose text NOT NULL,
    entity_id uuid,
    status text NOT NULL CHECK(status IN ('reserved','completed','failed','cancelled','unknown')),
    request_hash text NOT NULL,
    error text NOT NULL DEFAULT '',
    price_snapshot jsonb NOT NULL,
    input_limit bigint NOT NULL CHECK(input_limit>=0),
    output_limit bigint NOT NULL CHECK(output_limit>=0),
    input_tokens bigint NOT NULL DEFAULT 0 CHECK(input_tokens>=0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK(output_tokens>=0),
    cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK(cached_input_tokens>=0 AND cached_input_tokens<=input_tokens),
    usage_estimated boolean NOT NULL DEFAULT false,
    reserved_credits bigint NOT NULL DEFAULT 0 CHECK(reserved_credits>=0),
    reserved_cost_microusd bigint NOT NULL DEFAULT 0 CHECK(reserved_cost_microusd>=0),
    credits bigint NOT NULL DEFAULT 0 CHECK(credits>=0),
    cost_microusd bigint NOT NULL DEFAULT 0 CHECK(cost_microusd>=0),
    budget_month date NOT NULL DEFAULT (date_trunc('month',now() AT TIME ZONE 'UTC')::date),
    created_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    invoked_at timestamptz
);
CREATE INDEX usage_user_idx ON assistant.usage(user_id,created_at DESC,id);
CREATE INDEX usage_recent_idx ON assistant.usage(created_at DESC,id DESC);
CREATE INDEX usage_status_idx ON assistant.usage(status,created_at);
CREATE TABLE assistant.conversations (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    title text NOT NULL DEFAULT '',
    model_id uuid,
    byo_credential_id uuid,
    messages jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversations_owner_idx ON assistant.conversations(owner_id,updated_at DESC,id);
CREATE TABLE assistant.chat_requests (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    request_hash text NOT NULL,
    conversation_id uuid,
    status text NOT NULL CHECK(status IN ('pending','completed','failed')),
    response jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX chat_requests_conversation_pending ON assistant.chat_requests(conversation_id) WHERE status='pending' AND conversation_id IS NOT NULL;
CREATE TABLE assistant.generated_content (
    id uuid PRIMARY KEY,
    entity_id uuid NOT NULL,
    locale text NOT NULL,
    context_hash text NOT NULL,
    model_id uuid,
    owner_id text NOT NULL DEFAULT '',
    content jsonb NOT NULL,
    dependencies jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(entity_id,locale,context_hash,owner_id)
);
CREATE INDEX generated_entity_idx ON assistant.generated_content(entity_id,locale,updated_at DESC);
CREATE TABLE assistant.public_sources (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    document jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO assistant.public_sources(singleton) VALUES(true);
UPDATE catalog.schema_contract SET version=34 WHERE singleton;
