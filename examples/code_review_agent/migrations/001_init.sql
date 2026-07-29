-- Code Review Agent Schema
-- Migration 001: Initial schema

CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    repo_path TEXT DEFAULT '',
    diff_file TEXT DEFAULT '',
    diff_summary TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    dry_run INTEGER NOT NULL DEFAULT 0,
    sandbox_type TEXT DEFAULT 'local',
    total_duration_ms INTEGER NOT NULL DEFAULT 0,
    sandbox_duration_ms INTEGER NOT NULL DEFAULT 0,
    tool_call_count INTEGER NOT NULL DEFAULT 0,
    permission_deny_count INTEGER NOT NULL DEFAULT 0,
    findings_total INTEGER NOT NULL DEFAULT 0,
    findings_critical INTEGER NOT NULL DEFAULT 0,
    findings_high INTEGER NOT NULL DEFAULT 0,
    findings_medium INTEGER NOT NULL DEFAULT 0,
    findings_low INTEGER NOT NULL DEFAULT 0,
    findings_warning INTEGER NOT NULL DEFAULT 0,
    need_human_review_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT DEFAULT '',
    created_at INTEGER NOT NULL,
    completed_at INTEGER DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_review_tasks_status ON review_tasks(status);
CREATE INDEX IF NOT EXISTS idx_review_tasks_created ON review_tasks(created_at);

CREATE TABLE IF NOT EXISTS findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id),
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    file_path TEXT NOT NULL,
    line INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    evidence TEXT DEFAULT '',
    recommendation TEXT DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT 'rule',
    rule_id TEXT DEFAULT '',
    needs_human_review INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);

CREATE TABLE IF NOT EXISTS sandbox_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES review_tasks(id),
    command TEXT NOT NULL,
    exit_code INTEGER NOT NULL DEFAULT 0,
    stdout TEXT DEFAULT '',
    stderr TEXT DEFAULT '',
    timed_out INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error TEXT DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id);

CREATE TABLE IF NOT EXISTS permission_decisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id),
    tool_name TEXT NOT NULL,
    action TEXT NOT NULL,
    reason TEXT DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_permission_decisions_task ON permission_decisions(task_id);
