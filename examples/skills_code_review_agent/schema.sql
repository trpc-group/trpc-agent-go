-- Schema for Code Review Agent

CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    diff_summary TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL,
    status TEXT NOT NULL,
    exit_code INTEGER NOT NULL,
    output_snippet TEXT,
    duration_ms INTEGER NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS permission_decisions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL,
    decision TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS findings (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    file TEXT NOT NULL,
    line INTEGER NOT NULL,
    title TEXT NOT NULL,
    evidence TEXT NOT NULL,
    recommendation TEXT NOT NULL,
    confidence REAL NOT NULL,
    source TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);
