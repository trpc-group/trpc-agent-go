-- code_review_agent 数据库 Schema
-- SQLite 实现，用于持久化审查任务、findings、沙箱记录和监控数据。

-- 审查任务表
CREATE TABLE IF NOT EXISTS review_tasks (
    id TEXT PRIMARY KEY,                  -- 任务唯一标识 (cr-YYYYMMDDHHmmss-hash)
    input_type TEXT NOT NULL DEFAULT '',  -- diff_file / diff_text
    input_hash TEXT NOT NULL DEFAULT '',  -- SHA256 of input
    status TEXT NOT NULL DEFAULT 'pending', -- pending|running|completed|failed
    created_at INTEGER NOT NULL,
    completed_at INTEGER DEFAULT 0,
    total_files INTEGER DEFAULT 0,
    total_findings INTEGER DEFAULT 0,
    critical_count INTEGER DEFAULT 0,
    high_count INTEGER DEFAULT 0,
    medium_count INTEGER DEFAULT 0,
    low_count INTEGER DEFAULT 0,
    warning_count INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    error_message TEXT DEFAULT ''
);

-- 审查发现表
CREATE TABLE IF NOT EXISTS findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    severity TEXT NOT NULL,                -- critical|high|medium|low|warning
    category TEXT NOT NULL,                -- security|goroutine_context|resource_cleanup|error_handling|test_coverage|db_lifecycle|sensitive_info
    file TEXT NOT NULL DEFAULT '',
    line INTEGER DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    evidence TEXT DEFAULT '',
    recommendation TEXT DEFAULT '',
    confidence REAL DEFAULT 1.0,
    source TEXT DEFAULT 'rule',            -- rule|go_vet|sandbox_script
    rule_id TEXT DEFAULT '',
    dedup_key TEXT NOT NULL DEFAULT '',    -- SHA256(file:line:category:rule_id)[:16]
    is_duplicate INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- 沙箱执行记录表
CREATE TABLE IF NOT EXISTS sandbox_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL DEFAULT '',
    exit_code INTEGER DEFAULT 0,
    stdout TEXT DEFAULT '',
    stderr TEXT DEFAULT '',
    duration_ms INTEGER DEFAULT 0,
    timed_out INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- 安全决策记录表
CREATE TABLE IF NOT EXISTS permission_decisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    command TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '',     -- allow|deny|ask|needs_human_review
    rule_id TEXT DEFAULT '',
    risk_level TEXT DEFAULT '',            -- low|medium|high|critical
    reason TEXT DEFAULT '',
    intercepted INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- 监控摘要表
CREATE TABLE IF NOT EXISTS monitoring_summary (
    task_id TEXT PRIMARY KEY,
    total_duration_ms INTEGER DEFAULT 0,
    sandbox_duration_ms INTEGER DEFAULT 0,
    tool_calls_count INTEGER DEFAULT 0,
    permission_intercepts INTEGER DEFAULT 0,
    finding_count INTEGER DEFAULT 0
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id);
CREATE INDEX IF NOT EXISTS idx_findings_dedup ON findings(task_id, dedup_key);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(task_id, severity);
CREATE INDEX IF NOT EXISTS idx_sandbox_task ON sandbox_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_permissions_task ON permission_decisions(task_id);
