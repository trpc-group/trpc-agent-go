-- Schema for code review agent (SQLite).
-- Prefix all tables with "cr_" to avoid conflict with session tables.

CREATE TABLE IF NOT EXISTS cr_tasks (
    id              TEXT PRIMARY KEY,
    diff_source     TEXT NOT NULL,
    diff_summary    TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    changed_files   TEXT NOT NULL DEFAULT '[]',
    finding_count   INTEGER NOT NULL DEFAULT 0,
    high_risk_count INTEGER NOT NULL DEFAULT 0,
    medium_risk_count INTEGER NOT NULL DEFAULT 0,
    low_risk_count  INTEGER NOT NULL DEFAULT 0,
    warning_count   INTEGER NOT NULL DEFAULT 0,
    permission_denied INTEGER NOT NULL DEFAULT 0,
    permission_asked  INTEGER NOT NULL DEFAULT 0,
    total_duration_ms  INTEGER NOT NULL DEFAULT 0,
    sandbox_duration_ms INTEGER NOT NULL DEFAULT 0,
    tool_call_count    INTEGER NOT NULL DEFAULT 0,
    dry_run         INTEGER NOT NULL DEFAULT 0,
    report_json     TEXT NOT NULL DEFAULT '',
    report_md       TEXT NOT NULL DEFAULT '',
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cr_findings (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES cr_tasks(id),
    sandbox_run_id  TEXT,
    severity        TEXT NOT NULL,
    category        TEXT NOT NULL,
    file            TEXT NOT NULL,
    line            INTEGER NOT NULL DEFAULT 0,
    column          INTEGER,
    title           TEXT NOT NULL,
    evidence        TEXT NOT NULL DEFAULT '',
    sanitized_evidence TEXT,
    recommendation  TEXT NOT NULL DEFAULT '',
    confidence      TEXT NOT NULL DEFAULT 'high',
    source          TEXT NOT NULL DEFAULT 'custom_rule',
    rule_id         TEXT NOT NULL DEFAULT '',
    hunk_id         TEXT,
    is_duplicate    INTEGER NOT NULL DEFAULT 0,
    is_warning      INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cr_sandbox_runs (
    id                TEXT PRIMARY KEY,
    task_id           TEXT NOT NULL REFERENCES cr_tasks(id),
    backend           TEXT NOT NULL,
    command           TEXT NOT NULL,
    sanitized_command TEXT NOT NULL DEFAULT '',
    exit_code         INTEGER NOT NULL DEFAULT -1,
    stdout_summary    TEXT NOT NULL DEFAULT '',
    stderr_summary    TEXT NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    timeout           INTEGER NOT NULL DEFAULT 0,
    permission_action TEXT NOT NULL DEFAULT 'allow',
    error             TEXT,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cr_permission_decisions (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES cr_tasks(id),
    tool_name       TEXT NOT NULL,
    command         TEXT NOT NULL,
    sanitized_cmd   TEXT NOT NULL DEFAULT '',
    decision        TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cr_reports (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES cr_tasks(id),
    report_type     TEXT NOT NULL,
    content         TEXT NOT NULL,
    artifact_ref    TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cr_artifacts (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES cr_tasks(id),
    sandbox_run_id  TEXT,
    name            TEXT NOT NULL,
    mime_type       TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    storage_type    TEXT NOT NULL DEFAULT 'inline',
    data            BLOB,
    file_path       TEXT,
    artifact_uri    TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_cr_tasks_status ON cr_tasks(status);
CREATE INDEX IF NOT EXISTS idx_cr_tasks_created ON cr_tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_cr_findings_task ON cr_findings(task_id);
CREATE INDEX IF NOT EXISTS idx_cr_findings_severity ON cr_findings(severity);
CREATE INDEX IF NOT EXISTS idx_cr_findings_category ON cr_findings(category);
CREATE INDEX IF NOT EXISTS idx_cr_findings_dedup ON cr_findings(task_id, file, line, rule_id);
CREATE INDEX IF NOT EXISTS idx_cr_sandbox_runs_task ON cr_sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_cr_permission_decisions_task ON cr_permission_decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_cr_reports_task ON cr_reports(task_id);
