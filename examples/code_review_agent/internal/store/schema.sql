-- schema.sql defines the SQLite schema for the code review agent's persistent
-- store. Every child table references review_task(task_id) with ON DELETE
-- CASCADE so that deleting a task removes all of its associated rows. Foreign
-- keys are enabled at runtime via "PRAGMA foreign_keys=ON" (modernc.org/sqlite
-- defaults them OFF).

CREATE TABLE IF NOT EXISTS review_task (
    task_id             TEXT    PRIMARY KEY,
    created_at          TEXT    NOT NULL,
    repo_path           TEXT    NOT NULL,
    diff_source         TEXT    NOT NULL,
    status              TEXT    NOT NULL,
    conclusion          TEXT    NOT NULL,
    total_duration_ms   INTEGER NOT NULL,
    sandbox_duration_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS finding (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT    NOT NULL,
    severity        TEXT    NOT NULL,
    category        TEXT    NOT NULL,
    file            TEXT    NOT NULL,
    line            INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    evidence        TEXT    NOT NULL,
    recommendation  TEXT    NOT NULL,
    confidence      REAL    NOT NULL,
    source          TEXT    NOT NULL,
    rule_id         TEXT    NOT NULL,
    fingerprint     TEXT    NOT NULL UNIQUE,
    created_at      TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_finding_task_id ON finding(task_id);

CREATE TABLE IF NOT EXISTS sandbox_run (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id      TEXT    NOT NULL,
    command      TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    exit_code    INTEGER,
    duration_ms  INTEGER NOT NULL,
    timed_out    INTEGER NOT NULL,
    truncated    INTEGER NOT NULL,
    stdout       TEXT,
    stderr       TEXT,
    created_at   TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sandbox_run_task_id ON sandbox_run(task_id);

CREATE TABLE IF NOT EXISTS permission_decision (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT    NOT NULL,
    command    TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    reason     TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_permission_decision_task_id ON permission_decision(task_id);

CREATE TABLE IF NOT EXISTS artifact (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL,
    created_at  TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS report (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT    NOT NULL UNIQUE,
    json_path     TEXT    NOT NULL,
    markdown_path TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_artifact_task_id ON artifact(task_id);

CREATE TABLE IF NOT EXISTS telemetry_metrics (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id                 TEXT    NOT NULL UNIQUE,
    total_duration_ms       INTEGER NOT NULL,
    sandbox_duration_ms     INTEGER NOT NULL,
    tool_calls              INTEGER NOT NULL,
    permission_blocked_count INTEGER NOT NULL,
    finding_count           INTEGER NOT NULL,
    severity_critical       INTEGER NOT NULL,
    severity_high           INTEGER NOT NULL,
    severity_medium         INTEGER NOT NULL,
    severity_low            INTEGER NOT NULL,
    created_at              TEXT    NOT NULL,
    FOREIGN KEY(task_id) REFERENCES review_task(task_id) ON DELETE CASCADE
);
