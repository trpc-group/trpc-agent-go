CREATE TABLE IF NOT EXISTS review_tasks (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  input_source TEXT NOT NULL,
  diff_sha256 TEXT NOT NULL,
  created_at TEXT NOT NULL,
  duration_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS permission_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id),
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  command_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id),
  status TEXT NOT NULL,
  command_json TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  timed_out INTEGER NOT NULL,
  output TEXT NOT NULL,
  error TEXT NOT NULL,
  duration_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id),
  dedupe_key TEXT NOT NULL,
  file TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  severity TEXT NOT NULL,
  category TEXT NOT NULL,
  confidence REAL NOT NULL,
  source TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL,
  suggestion TEXT NOT NULL,
  UNIQUE(task_id, dedupe_key)
);

CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES review_tasks(id),
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size_bytes INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS reports (
  task_id TEXT PRIMARY KEY REFERENCES review_tasks(id),
  json_path TEXT NOT NULL,
  markdown_path TEXT NOT NULL,
  summary TEXT NOT NULL,
  metrics_json TEXT NOT NULL
);
