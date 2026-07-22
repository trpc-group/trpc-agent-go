PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('running','completed','completed_with_warnings','failed')),
    input_kind TEXT NOT NULL,
    input_digest TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    conclusion TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS input_summaries (
    task_id TEXT PRIMARY KEY NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    file_count INTEGER NOT NULL CHECK (file_count >= 0),
    hunk_count INTEGER NOT NULL CHECK (hunk_count >= 0),
    added_lines INTEGER NOT NULL CHECK (added_lines >= 0),
    packages_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    check_id TEXT NOT NULL,
    runtime TEXT NOT NULL,
    status TEXT NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    exit_code INTEGER NOT NULL,
    timed_out INTEGER NOT NULL CHECK (timed_out IN (0,1)),
    output_truncated INTEGER NOT NULL CHECK (output_truncated IN (0,1)),
    stdout TEXT NOT NULL,
    stderr TEXT NOT NULL,
    error_type TEXT NOT NULL,
    error TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task ON sandbox_runs(task_id);

CREATE TABLE IF NOT EXISTS governance_decisions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    stage TEXT NOT NULL,
    tool TEXT NOT NULL,
    check_id TEXT NOT NULL,
    args_digest TEXT NOT NULL,
    risk TEXT NOT NULL,
	action TEXT NOT NULL,
	reason TEXT NOT NULL,
	decided_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_governance_decisions_task ON governance_decisions(task_id);

CREATE TABLE IF NOT EXISTS findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    bucket TEXT NOT NULL CHECK (bucket IN ('findings','warnings','needs_human_review')),
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    file TEXT NOT NULL,
    line INTEGER NOT NULL CHECK (line >= 0),
    title TEXT NOT NULL,
    evidence TEXT NOT NULL,
    recommendation TEXT NOT NULL,
    confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    UNIQUE(task_id, dedup_key)
);
CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id);

CREATE TABLE IF NOT EXISTS review_metrics (
    task_id TEXT PRIMARY KEY NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    total_duration_ms INTEGER NOT NULL CHECK (total_duration_ms >= 0),
    sandbox_duration_ms INTEGER NOT NULL CHECK (sandbox_duration_ms >= 0),
    tool_calls INTEGER NOT NULL CHECK (tool_calls >= 0),
    permission_blocks INTEGER NOT NULL CHECK (permission_blocks >= 0),
    finding_count INTEGER NOT NULL CHECK (finding_count >= 0),
    severity_json TEXT NOT NULL,
    error_types_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES sandbox_runs(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id);

CREATE TABLE IF NOT EXISTS reports (
    task_id TEXT PRIMARY KEY NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    conclusion TEXT NOT NULL,
    canonical_json TEXT NOT NULL,
    canonical_markdown TEXT NOT NULL,
    json_path TEXT NOT NULL,
    json_sha256 TEXT NOT NULL,
    markdown_path TEXT NOT NULL,
    markdown_sha256 TEXT NOT NULL
);
