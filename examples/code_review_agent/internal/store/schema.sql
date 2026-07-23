-- Tencent is pleased to support the open source community by making trpc-agent-go available.
--
-- Copyright (C) 2026 Tencent.  All rights reserved.
--
-- trpc-agent-go is licensed under the Apache License Version 2.0.

CREATE TABLE IF NOT EXISTS review_tasks (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  input_type TEXT NOT NULL,
  repo_path TEXT,
  diff_hash TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  error TEXT
);

CREATE TABLE IF NOT EXISTS review_inputs (
  task_id TEXT PRIMARY KEY,
  diff_summary TEXT NOT NULL,
  changed_files_json TEXT NOT NULL,
  redacted_diff TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  runtime TEXT NOT NULL,
  command TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  stdout_redacted TEXT,
  stderr_redacted TEXT,
  output_truncated INTEGER NOT NULL,
  error_type TEXT,
  FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS permission_decisions (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  command TEXT,
  framework_action TEXT NOT NULL,
  safety_decision TEXT NOT NULL,
  risk_level TEXT,
  rule_id TEXT,
  reason TEXT,
  blocked INTEGER NOT NULL,
  created_at TEXT NOT NULL,
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
  status TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  UNIQUE(task_id, fingerprint),
  FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS review_artifacts (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  sha256 TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE TABLE IF NOT EXISTS review_reports (
  task_id TEXT PRIMARY KEY,
  json_path TEXT NOT NULL,
  markdown_path TEXT NOT NULL,
  conclusion TEXT NOT NULL,
  metrics_json TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES review_tasks(id)
);

CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task_id ON sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_findings_task_id ON findings(task_id);
CREATE INDEX IF NOT EXISTS idx_permission_decisions_task_id ON permission_decisions(task_id);
