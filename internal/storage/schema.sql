-- Schema version: 6
-- Versioned; never silently rename columns.

CREATE TABLE IF NOT EXISTS schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TIMESTAMPTZ DEFAULT now()
);

-- Raw OTLP spans from Claude Code
CREATE TABLE IF NOT EXISTS spans (
    -- OTLP identity
    trace_id        VARCHAR NOT NULL,
    span_id         VARCHAR NOT NULL PRIMARY KEY,
    parent_span_id  VARCHAR,
    name            VARCHAR NOT NULL,

    -- Timing
    start_time      TIMESTAMPTZ NOT NULL,
    end_time        TIMESTAMPTZ NOT NULL,
    duration_ms     DOUBLE GENERATED ALWAYS AS (
                        epoch_ms(end_time) - epoch_ms(start_time)
                    ),

    -- Service metadata
    service_name    VARCHAR,

    -- Extracted high-cardinality keys (index targets)
    session_id      VARCHAR,
    model           VARCHAR,
    tool_name       VARCHAR,

    -- User identifier: NULL when unauthenticated, UUID from users table when authenticated
    user_id         VARCHAR,

    -- OTLP span status: 0=UNSET, 1=OK, 2=ERROR
    status_code     TINYINT DEFAULT 0,

    -- Token / cost counters
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    cost_usd        DOUBLE,

    -- Full attribute bag (for ad-hoc queries)
    attributes      JSON,
    resource_attrs  JSON,

    ingested_at     TIMESTAMPTZ DEFAULT now()
);

-- Migration v1 → v2: add status_code if upgrading an existing database.
ALTER TABLE spans ADD COLUMN IF NOT EXISTS status_code TINYINT DEFAULT 0;

-- Migration v2 → v3: add user_id for multi-user telemetry separation.
ALTER TABLE spans ADD COLUMN IF NOT EXISTS user_id VARCHAR;

-- Indexes for dashboard hot paths (DuckDB ART indexes).
CREATE INDEX IF NOT EXISTS idx_spans_session_id  ON spans(session_id);
CREATE INDEX IF NOT EXISTS idx_spans_start_time  ON spans(start_time);
CREATE INDEX IF NOT EXISTS idx_spans_name        ON spans(name);
CREATE INDEX IF NOT EXISTS idx_spans_user_id     ON spans(user_id);

-- Daily aggregates for long-term retention (schema version 1)
CREATE TABLE IF NOT EXISTS daily_usage (
    day             DATE NOT NULL,
    session_id      VARCHAR,
    model           VARCHAR,
    tool_name       VARCHAR,
    user_id         VARCHAR,
    span_count      BIGINT,
    total_input_tokens  BIGINT,
    total_output_tokens BIGINT,
    total_cost_usd  DOUBLE,
    PRIMARY KEY (day, session_id, model, tool_name)
);

-- Migration v2 → v3: add user_id to daily_usage.
ALTER TABLE daily_usage ADD COLUMN IF NOT EXISTS user_id VARCHAR;

-- API tokens for OTLP ingest auth (schema version 4) — kept for upgrade safety, not used by new auth
CREATE TABLE IF NOT EXISTS api_tokens (
    id           VARCHAR PRIMARY KEY,
    name         VARCHAR NOT NULL,
    token_hash   VARCHAR NOT NULL UNIQUE,
    prefix       VARCHAR NOT NULL,
    created_at   TIMESTAMPTZ DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

-- Named users with always-visible plaintext tokens (schema version 5)
CREATE TABLE IF NOT EXISTS users (
    id         VARCHAR PRIMARY KEY,
    name       VARCHAR NOT NULL UNIQUE,
    token      VARCHAR NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Runtime key/value settings (schema version 6)
CREATE TABLE IF NOT EXISTS settings (
    key        VARCHAR PRIMARY KEY,
    value      VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Seed allow_anonymous=true (old "no tokens = open" default preserved)
INSERT INTO settings (key, value) VALUES ('allow_anonymous', 'true') ON CONFLICT DO NOTHING;

-- Migration v4→v5: backfill a user row for each distinct span user_id.
-- Uses ON CONFLICT DO NOTHING on the UNIQUE name column to be idempotent.
-- Token is 'cotel_' + 64 hex chars built from two md5 calls to avoid external deps.
INSERT INTO users (id, name, token)
SELECT
    uuid()                                                       AS id,
    user_id                                                      AS name,
    'cotel_' || md5(uuid()::VARCHAR) || md5(uuid()::VARCHAR)     AS token
FROM (SELECT DISTINCT user_id FROM spans WHERE user_id IS NOT NULL AND user_id <> '') t
WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.name = t.user_id);

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (2) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (3) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (4) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (5) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (6) ON CONFLICT DO NOTHING;
