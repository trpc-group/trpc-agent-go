-- Code review agent SQLite schema.
CREATE TABLE IF NOT EXISTS review_task (
  id            TEXT PRIMARY KEY,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  status        TEXT NOT NULL,
  mode          TEXT NOT NULL,
  executor      TEXT NOT NULL,
  repo_path     TEXT,
  input_kind    TEXT NOT NULL,
  input_digest  TEXT NOT NULL,
  input_summary TEXT,
  conclusion    TEXT,
  error         TEXT
);

CREATE TABLE IF NOT EXISTS review_input (
  task_id            TEXT PRIMARY KEY REFERENCES review_task(id),
  diff_text_redacted TEXT,
  file_list_json     TEXT,
  package_list_json  TEXT
);

CREATE TABLE IF NOT EXISTS sandbox_run (
  id            TEXT PRIMARY KEY,
  task_id       TEXT NOT NULL REFERENCES review_task(id),
  executor      TEXT NOT NULL,
  command       TEXT NOT NULL,
  started_at    TEXT NOT NULL,
  ended_at      TEXT,
  timeout_ms    INTEGER,
  exit_code     INTEGER,
  status        TEXT NOT NULL,
  stdout_bytes  INTEGER,
  stderr_bytes  INTEGER,
  truncated     INTEGER NOT NULL DEFAULT 0,
  error         TEXT
);

CREATE TABLE IF NOT EXISTS permission_decision (
  id         TEXT PRIMARY KEY,
  task_id    TEXT NOT NULL REFERENCES review_task(id),
  tool_name  TEXT,
  command    TEXT NOT NULL,
  action     TEXT NOT NULL,
  reason     TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finding (
  id             TEXT PRIMARY KEY,
  task_id        TEXT NOT NULL REFERENCES review_task(id),
  severity       TEXT NOT NULL,
  category       TEXT NOT NULL,
  file           TEXT NOT NULL,
  line           INTEGER NOT NULL,
  title          TEXT NOT NULL,
  evidence       TEXT,
  recommendation TEXT,
  confidence     REAL NOT NULL,
  source         TEXT NOT NULL,
  rule_id        TEXT NOT NULL,
  bucket         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS artifact (
  id          TEXT PRIMARY KEY,
  task_id     TEXT NOT NULL REFERENCES review_task(id),
  name        TEXT NOT NULL,
  mime        TEXT,
  path_or_ref TEXT NOT NULL,
  size_bytes  INTEGER,
  sha256      TEXT
);

CREATE TABLE IF NOT EXISTS metrics_summary (
  task_id             TEXT PRIMARY KEY REFERENCES review_task(id),
  total_ms            INTEGER NOT NULL,
  sandbox_ms          INTEGER NOT NULL,
  tool_calls          INTEGER NOT NULL,
  permission_denies   INTEGER NOT NULL,
  permission_asks     INTEGER NOT NULL,
  finding_count       INTEGER NOT NULL,
  warning_count       INTEGER NOT NULL,
  severity_json       TEXT NOT NULL,
  exception_json      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS report (
  task_id     TEXT PRIMARY KEY REFERENCES review_task(id),
  json_path   TEXT,
  md_path     TEXT,
  report_json TEXT NOT NULL,
  report_md   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_finding_task ON finding(task_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_task ON sandbox_run(task_id);
CREATE INDEX IF NOT EXISTS idx_perm_task ON permission_decision(task_id);
