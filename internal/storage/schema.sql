-- Schema version: 2
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

-- Indexes for dashboard hot paths (DuckDB ART indexes).
CREATE INDEX IF NOT EXISTS idx_spans_session_id  ON spans(session_id);
CREATE INDEX IF NOT EXISTS idx_spans_start_time  ON spans(start_time);
CREATE INDEX IF NOT EXISTS idx_spans_name        ON spans(name);

-- Daily aggregates for long-term retention (schema version 1)
CREATE TABLE IF NOT EXISTS daily_usage (
    day             DATE NOT NULL,
    session_id      VARCHAR,
    model           VARCHAR,
    tool_name       VARCHAR,
    span_count      BIGINT,
    total_input_tokens  BIGINT,
    total_output_tokens BIGINT,
    total_cost_usd  DOUBLE,
    PRIMARY KEY (day, session_id, model, tool_name)
);

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;
INSERT INTO schema_version (version) VALUES (2) ON CONFLICT DO NOTHING;
