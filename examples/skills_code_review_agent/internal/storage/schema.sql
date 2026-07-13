-- Review task records.
CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    input_summary TEXT NOT NULL,
    repo_path TEXT,
    created_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL
);

-- Structured findings.
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
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

-- Monitoring summary per task.
CREATE TABLE IF NOT EXISTS review_metrics (
    task_id TEXT PRIMARY KEY,
    finding_count INTEGER NOT NULL,
    warning_count INTEGER NOT NULL,
    total_duration_ms INTEGER NOT NULL,
    sandbox_duration_ms INTEGER NOT NULL DEFAULT 0,
    tool_call_count INTEGER NOT NULL DEFAULT 0,
    permission_deny_count INTEGER NOT NULL DEFAULT 0,
    severity_json TEXT NOT NULL,
    exception_json TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

-- Saved report artifacts.
CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

-- Sandbox execution records.
CREATE TABLE IF NOT EXISTS sandbox_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL,
    runtime TEXT NOT NULL,
    status TEXT NOT NULL,
    exit_code INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    stdout TEXT,
    stderr TEXT,
    error_type TEXT,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);

-- Permission / filter decisions.
CREATE TABLE IF NOT EXISTS permission_decisions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    command TEXT NOT NULL,
    action TEXT NOT NULL,
    reason TEXT,
    FOREIGN KEY (task_id) REFERENCES review_tasks(id)
);
