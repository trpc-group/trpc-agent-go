PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS review_tasks (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  conclusion TEXT NOT NULL,
  mode TEXT NOT NULL,
  runtime TEXT NOT NULL,
  skill TEXT NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS review_inputs (
  task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
  input_kind TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  byte_count INTEGER NOT NULL,
  changed_files_json TEXT NOT NULL,
  go_packages_json TEXT NOT NULL,
  redacted_preview TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
  command TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  timed_out INTEGER NOT NULL,
  output TEXT NOT NULL,
  error_type TEXT NOT NULL,
  error_message TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS governance_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
  tool TEXT NOT NULL,
  command TEXT NOT NULL,
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  risk TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
  bucket TEXT NOT NULL,
  severity TEXT NOT NULL,
  category TEXT NOT NULL,
  file_path TEXT NOT NULL,
  line_number INTEGER NOT NULL,
  title TEXT NOT NULL,
  evidence TEXT NOT NULL,
  recommendation TEXT NOT NULL,
  confidence REAL NOT NULL,
  source TEXT NOT NULL,
  rule_id TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS findings_task_lookup
ON findings(task_id, bucket, severity, file_path, line_number);

CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS review_metrics (
  task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
  total_duration_ms INTEGER NOT NULL,
  sandbox_duration_ms INTEGER NOT NULL,
  tool_calls INTEGER NOT NULL,
  permission_blocked INTEGER NOT NULL,
  finding_count INTEGER NOT NULL,
  warning_count INTEGER NOT NULL,
  severity_json TEXT NOT NULL,
  errors_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS review_reports (
  task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
  json_content TEXT NOT NULL,
  markdown_content TEXT NOT NULL
);
