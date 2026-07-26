-- SQLite schema for the code review agent example.
--
-- Review Store tables provide the task-scoped structured queries required by
-- issue #2004. The framework SQLite Session Service initializes and owns its
-- session schema separately.
--
-- Text fields that may contain user code, tool output, model output, or report
-- content store redacted values only.

-- Review task lifecycle, input summary, monitoring summary, final conclusion,
-- and references to input and report artifacts.
-- input_summary_json contains bounded changed-file and Go-package summaries,
-- hunk/candidate counts, and redaction signals. monitoring_summary_json contains
-- total and sandbox
-- duration, tool-call and Permission-interception counts, finding count,
-- severity distribution, and exception-type distribution.
CREATE TABLE IF NOT EXISTS review_tasks (
    -- task_id is generated before the review starts and is passed to Runner as
    -- SessionID. app_name, user_id, and task_id form the complete Session key
    -- used by Session Service and Artifact Service lookups.
    task_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    task_status TEXT NOT NULL,
    input_kind TEXT NOT NULL,
    input_summary_json TEXT NOT NULL DEFAULT '{}',
    input_artifact_name TEXT,
    input_artifact_version INTEGER,
    monitoring_summary_json TEXT NOT NULL DEFAULT '{}',
    conclusion TEXT,
    json_report_name TEXT,
    json_report_version INTEGER,
    markdown_report_name TEXT,
    markdown_report_version INTEGER,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    error_type TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Permission, Filter, and safety decisions made before a governed operation
-- executes. decision stores allow, deny, or ask.
CREATE TABLE IF NOT EXISTS permission_decisions (
    id INTEGER PRIMARY KEY,
    task_id TEXT NOT NULL,
    tool_call_id TEXT,
    decision_kind TEXT NOT NULL,
    operation TEXT NOT NULL,
    tool_name TEXT,
    command_preview TEXT,
    decision TEXT NOT NULL,
    reason TEXT,
    decided_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Isolated execution records for commands, scripts, tests, and static checks.
-- The record is a task-scoped audit projection of facts observed after an
-- allowed workspace_exec call. Framework configuration such as env allowlists
-- and output budgets is not stored as a per-run observation. output_summary is
-- the framework-aggregated terminal output, not separate stdout/stderr streams.
CREATE TABLE IF NOT EXISTS sandbox_runs (
    id INTEGER PRIMARY KEY,
    task_id TEXT NOT NULL,
    tool_call_id TEXT,
    backend TEXT NOT NULL,
    workdir TEXT,
    command_preview TEXT NOT NULL,
    sandbox_status TEXT NOT NULL,
    exit_code INTEGER,
    timed_out INTEGER NOT NULL DEFAULT 0,
    output_summary TEXT,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    redaction_count INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error_type TEXT,
    error_message TEXT
);

-- Findings, warnings, and items that need human review.
CREATE TABLE IF NOT EXISTS review_results (
    id INTEGER PRIMARY KEY,
    task_id TEXT NOT NULL,
    result_kind TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    file_path TEXT NOT NULL,
    line INTEGER NOT NULL DEFAULT 0 CHECK (line >= 0),
    title TEXT NOT NULL,
    evidence TEXT NOT NULL,
    recommendation TEXT,
    confidence REAL NOT NULL DEFAULT 0,
    source TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Accurate source locations and opaque rule IDs form the deterministic
-- rule-location identity. This does not claim semantic root-cause equivalence.
-- line=0 has no exact location and is deliberately excluded.
CREATE UNIQUE INDEX IF NOT EXISTS review_results_task_location_rule_unique
    ON review_results (task_id, file_path, line, rule_id)
    WHERE line > 0;

-- Versioned artifact content for the SQLite artifact.Service adapter.
-- The complete framework SessionInfo key scopes each artifact.
CREATE TABLE IF NOT EXISTS artifact_versions (
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    version INTEGER NOT NULL,
    data BLOB NOT NULL,
    mime_type TEXT NOT NULL,
    display_name TEXT,
    url TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (app_name, user_id, session_id, filename, version)
);
