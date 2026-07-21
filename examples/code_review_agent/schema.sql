-- Minimal relational schema for the code review agent. The example implementation
-- uses JSONStore to avoid external dependencies in offline fixtures, but these
-- tables map 1:1 to the Store interface and can be backed by SQLite.
CREATE TABLE IF NOT EXISTS review_task (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  input_kind TEXT NOT NULL,
  input_summary TEXT NOT NULL,
  repo_path TEXT,
  skill_path TEXT NOT NULL,
  runtime TEXT NOT NULL,
  dry_run INTEGER NOT NULL,
  rule_only INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  error TEXT
);

CREATE TABLE IF NOT EXISTS permission_decision (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES review_task(id),
  tool TEXT NOT NULL,
  command_json TEXT NOT NULL,
  decision TEXT NOT NULL,
  risk TEXT NOT NULL,
  reason TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_run (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES review_task(id),
  runtime TEXT NOT NULL,
  tool TEXT NOT NULL,
  command_json TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  output TEXT NOT NULL,
  error_type TEXT,
  output_truncated INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finding (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_task(id),
  bucket TEXT NOT NULL,
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
  needs_human INTEGER NOT NULL,
  UNIQUE(task_id, bucket, file, line, category, rule_id)
);

CREATE TABLE IF NOT EXISTS artifact (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES review_task(id),
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS monitoring_summary (
  task_id TEXT PRIMARY KEY REFERENCES review_task(id),
  total_duration_ms INTEGER NOT NULL,
  sandbox_duration_ms INTEGER NOT NULL,
  tool_call_count INTEGER NOT NULL,
  permission_intercepts INTEGER NOT NULL,
  finding_count INTEGER NOT NULL,
  warning_count INTEGER NOT NULL,
  needs_human_review_count INTEGER NOT NULL,
  severity_distribution_json TEXT NOT NULL,
  exception_distribution_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS final_report (
  task_id TEXT PRIMARY KEY REFERENCES review_task(id),
  report_json TEXT NOT NULL,
  report_markdown_path TEXT NOT NULL,
  created_at TEXT NOT NULL
);
