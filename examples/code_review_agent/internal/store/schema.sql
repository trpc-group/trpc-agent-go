--
-- Tencent is pleased to support the open source community by making trpc-agent-go available.
--
-- Copyright (C) 2026 Tencent.  All rights reserved.
--
-- trpc-agent-go is licensed under the Apache License Version 2.0.
--
--

CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,
    schema_version TEXT NOT NULL,
    status TEXT NOT NULL,
    phase TEXT NOT NULL,
    mode TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    terminal_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS review_inputs (
    task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    source TEXT NOT NULL,
    digest TEXT NOT NULL,
    changed_files TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    command TEXT NOT NULL,
    status TEXT NOT NULL,
    duration_ns INTEGER NOT NULL,
    exit_code INTEGER,
    timed_out INTEGER NOT NULL,
    stdout TEXT NOT NULL,
    stderr TEXT NOT NULL,
    truncated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task_id ON sandbox_runs(task_id);

CREATE TABLE IF NOT EXISTS governance_decisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    tool TEXT NOT NULL,
    action TEXT NOT NULL,
    reason TEXT NOT NULL,
    rule TEXT NOT NULL,
    UNIQUE(task_id, kind, decision_id)
);
CREATE INDEX IF NOT EXISTS idx_governance_decisions_task_id ON governance_decisions(task_id);

CREATE TABLE IF NOT EXISTS findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL,
    layer TEXT NOT NULL,
    file TEXT NOT NULL,
    line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    semantic_anchor TEXT NOT NULL,
    title TEXT NOT NULL,
    evidence TEXT NOT NULL,
    recommendation TEXT NOT NULL,
    confidence TEXT NOT NULL,
    source TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    disposition TEXT NOT NULL,
    UNIQUE(task_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_findings_task_id ON findings(task_id);

CREATE TABLE IF NOT EXISTS artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES review_tasks(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('evidence', 'publication')),
    schema_version TEXT NOT NULL,
    name TEXT NOT NULL,
    reference TEXT NOT NULL,
    digest TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    UNIQUE(task_id, role, name, reference)
);
CREATE INDEX IF NOT EXISTS idx_artifacts_task_id ON artifacts(task_id);

CREATE TABLE IF NOT EXISTS review_metrics (
    task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    total_duration_ns INTEGER NOT NULL,
    sandbox_duration_ns INTEGER NOT NULL,
    tool_invocations INTEGER NOT NULL,
    permission_blocks INTEGER NOT NULL,
    finding_total INTEGER NOT NULL,
    severity_counts TEXT NOT NULL,
    warning_count INTEGER NOT NULL,
    human_review_count INTEGER NOT NULL,
    error_type_counts TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS review_reports (
    task_id TEXT PRIMARY KEY REFERENCES review_tasks(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    digest TEXT NOT NULL,
    json_artifact_reference TEXT NOT NULL,
    markdown_artifact_reference TEXT NOT NULL,
    conclusion TEXT NOT NULL
);
