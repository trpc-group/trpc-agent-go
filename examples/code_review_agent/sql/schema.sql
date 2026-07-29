-- code_review DDL (SQLite)
-- Version: 1.0
-- 7+1 tables per design spec v1.2

-- ============================================
-- Table 1: review_task
-- ============================================
CREATE TABLE IF NOT EXISTS review_task (
    id              TEXT PRIMARY KEY,
    status          TEXT    NOT NULL DEFAULT 'pending',  -- pending|running|completed|failed
    input_type      TEXT    NOT NULL,                     -- diff_file|diff_text|repo_path
    input_source    TEXT,                                 -- file path or "stdin"
    input_diff_hash TEXT    NOT NULL,                     -- SHA-256 of normalized diff
    base_ref        TEXT    DEFAULT 'origin/main',
    total_files     INTEGER DEFAULT 0,
    total_hunks     INTEGER DEFAULT 0,
    model_mode      TEXT    NOT NULL DEFAULT 'live',      -- live|dry_run
    error_message   TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    started_at      TEXT,
    completed_at    TEXT,
    total_duration_ms INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_task_status ON review_task(status);
CREATE INDEX IF NOT EXISTS idx_task_created ON review_task(created_at);

-- ============================================
-- Table 2: review_finding
-- No UNIQUE constraint: dedup is handled by DedupEngine in code.
-- ============================================
CREATE TABLE IF NOT EXISTS review_finding (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    severity        TEXT    NOT NULL,  -- critical|high|medium|low|warning
    category        TEXT    NOT NULL,  -- security|error_handling|...
    file            TEXT    NOT NULL,
    line            INTEGER NOT NULL DEFAULT 0,
    title           TEXT    NOT NULL,
    evidence        TEXT,
    recommendation  TEXT,
    confidence      REAL    NOT NULL DEFAULT 1.0,
    source          TEXT    NOT NULL,  -- rule_engine|llm|go_vet|staticcheck
    decision_kind   TEXT    NOT NULL DEFAULT 'heuristic',
    rule_id         TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_finding_task ON review_finding(task_id);
CREATE INDEX IF NOT EXISTS idx_finding_severity ON review_finding(severity);
CREATE INDEX IF NOT EXISTS idx_finding_category ON review_finding(category);
CREATE INDEX IF NOT EXISTS idx_finding_source ON review_finding(source);

-- ============================================
-- Table 3: sandbox_run
-- ============================================
CREATE TABLE IF NOT EXISTS sandbox_run (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    executor_type   TEXT    NOT NULL,
    command_name    TEXT    NOT NULL,  -- go_vet|staticcheck|go_test|go_build|custom
    command         TEXT    NOT NULL,
    exit_code       INTEGER NOT NULL DEFAULT -1,
    stdout          TEXT,
    stderr          TEXT,
    duration_ms     INTEGER DEFAULT 0,
    timed_out       INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    error_type      TEXT    DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_sandbox_task ON sandbox_run(task_id);

-- ============================================
-- Table 4: permission_decision
-- ============================================
CREATE TABLE IF NOT EXISTS permission_decision (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    command         TEXT    NOT NULL,
    risk_level      TEXT    NOT NULL,
    decision        TEXT    NOT NULL,  -- allow|deny|needs_human_review
    reason          TEXT,
    decided_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_perm_task ON permission_decision(task_id);

-- ============================================
-- Table 5: review_artifact
-- ============================================
CREATE TABLE IF NOT EXISTS review_artifact (
    id              TEXT PRIMARY KEY,
    task_id         TEXT    NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    artifact_type   TEXT    NOT NULL,  -- json_report|md_report|diff_copy|sandbox_log
    file_path       TEXT    NOT NULL,
    size_bytes      INTEGER DEFAULT 0,
    content_hash    TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_artifact_task ON review_artifact(task_id);

-- ============================================
-- Table 6: review_report
-- ============================================
CREATE TABLE IF NOT EXISTS review_report (
    id                      TEXT PRIMARY KEY,
    task_id                 TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    findings_count          INTEGER DEFAULT 0,
    warnings_count          INTEGER DEFAULT 0,
    severity_distribution   TEXT,  -- JSON
    category_distribution   TEXT,  -- JSON
    json_report_path        TEXT,
    md_report_path          TEXT,
    summary                 TEXT,
    created_at              TEXT  NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_report_task ON review_report(task_id);

-- ============================================
-- Table 7: monitor_metric
-- ============================================
CREATE TABLE IF NOT EXISTS monitor_metric (
    id                      TEXT PRIMARY KEY,
    task_id                 TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    total_duration_ms       INTEGER DEFAULT 0,
    diff_parse_ms           INTEGER DEFAULT 0,
    permission_filter_ms    INTEGER DEFAULT 0,
    sandbox_total_ms        INTEGER DEFAULT 0,
    rule_engine_ms          INTEGER DEFAULT 0,
    llm_analyzer_ms         INTEGER DEFAULT 0,
    dedup_ms                INTEGER DEFAULT 0,
    report_gen_ms           INTEGER DEFAULT 0,
    storage_ms              INTEGER DEFAULT 0,
    tool_calls_count        INTEGER DEFAULT 0,
    permission_blocks_count INTEGER DEFAULT 0,
    findings_critical       INTEGER DEFAULT 0,
    findings_high           INTEGER DEFAULT 0,
    findings_medium         INTEGER DEFAULT 0,
    findings_low            INTEGER DEFAULT 0,
    findings_warning        INTEGER DEFAULT 0,
    llm_tokens_prompt       INTEGER DEFAULT 0,
    llm_tokens_completion   INTEGER DEFAULT 0,
    llm_tokens_total        INTEGER DEFAULT 0,
    created_at              TEXT  NOT NULL DEFAULT (datetime('now'))
);

-- ============================================
-- Table +1: metrics_exception
-- ============================================
CREATE TABLE IF NOT EXISTS metrics_exception (
    id              TEXT PRIMARY KEY,
    task_id         TEXT  NOT NULL REFERENCES review_task(id) ON DELETE CASCADE,
    error_type      TEXT  NOT NULL,
    error_count     INTEGER NOT NULL DEFAULT 1,
    error_detail    TEXT,
    created_at      TEXT  NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_exception_task ON metrics_exception(task_id);
CREATE INDEX IF NOT EXISTS idx_exception_type ON metrics_exception(error_type);
