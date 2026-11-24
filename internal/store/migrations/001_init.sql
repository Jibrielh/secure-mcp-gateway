CREATE TABLE users (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('viewer', 'developer', 'admin', 'approver')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL,
    model TEXT NOT NULL,
    tools JSONB NOT NULL,
    max_steps INTEGER NOT NULL CHECK (max_steps BETWEEN 1 AND 8),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX agents_owner ON agents (owner_user_id, updated_at DESC);

CREATE TABLE agent_versions (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    config JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agent_id, version)
);

CREATE TABLE integrations (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    status TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    title TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX conversations_owner ON conversations (owner_user_id, updated_at DESC);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool', 'system')),
    body TEXT NOT NULL,
    seq BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (conversation_id, seq)
);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id),
    agent_id TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_state JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX runs_owner ON runs (owner_user_id, created_at DESC);

CREATE TABLE run_events (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, seq)
);

CREATE TABLE tool_calls (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    arguments_digest TEXT NOT NULL,
    arguments JSONB NOT NULL,
    status TEXT NOT NULL,
    result_text TEXT NOT NULL DEFAULT '',
    approval_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE approvals (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    arguments JSONB NOT NULL,
    digest TEXT NOT NULL,
    status TEXT NOT NULL,
    execution_outcome TEXT NOT NULL DEFAULT 'not_run',
    operator_id TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ
);
CREATE INDEX approvals_status ON approvals (status, expires_at);

CREATE TABLE audit_events (
    id BIGSERIAL PRIMARY KEY,
    request_id TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    operator_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    service TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL,
    reason TEXT NOT NULL,
    risk TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL DEFAULT '',
    approval_id TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    outcome TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_events_time ON audit_events (occurred_at DESC);
CREATE INDEX audit_events_user ON audit_events (user_id, occurred_at DESC);
CREATE INDEX audit_events_request ON audit_events (request_id, occurred_at DESC);

CREATE TABLE findings (
    id TEXT PRIMARY KEY,
    rule_name TEXT NOT NULL,
    rule_version TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL,
    evidence JSONB NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    explanation TEXT NOT NULL DEFAULT '',
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE policy_restrictions (
    tool_name TEXT PRIMARY KEY,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    roles JSONB NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
