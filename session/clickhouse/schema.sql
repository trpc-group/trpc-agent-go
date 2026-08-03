-- ClickHouse Session Service Schema
-- This file provides reference SQL for manual database initialization.
-- The service will automatically create these tables and indexes if skipDBInit is false.
--
-- Requirements:
--   - ClickHouse 22.3+
--   - Database must be created before running these statements
--
-- The service stores JSON payloads as JSON-encoded String columns. When using
-- WithSkipDBInit, pre-created tables should match these column types and the
-- session_summaries version_at column used by automatic initialization.
--
-- Compatibility and migration:
-- This schema is not an in-place compatibility change for installations that
-- already have JSON-typed state, event, or summary columns. CREATE TABLE IF
-- NOT EXISTS will not alter those existing tables. Before enabling
-- WithSkipDBInit against such an installation, stop writes, create replacement
-- tables from this schema (preferably under a new table prefix), and migrate
-- the rows explicitly:
--
--   * state, extra_data, event, and summary: use toJSONString(column) when
--     copying from the old JSON columns into the new String columns.
--   * session_summaries: populate version_at from updated_at with
--     toDateTime64(updated_at, 9); the new table must use
--     ReplacingMergeTree(version_at).
--   * validate row counts and representative JSON payloads before switching
--     the service to the replacement tables.
--
-- Do not change an existing table only by changing the application option.
-- Test the migration on a backup first; the replacement-table approach keeps
-- the old JSON-backed tables available for rollback.

-- ============================================================================
-- Table: session_states
-- Description: Stores session state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_states (
    app_name    String,
    user_id     String,
    session_id  String,
    state       String COMMENT 'Session state as JSON-encoded text',
    extra_data  String COMMENT 'Additional metadata as JSON-encoded text',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session states table';

-- ============================================================================
-- Table: session_events
-- Description: Stores session events/messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_events (
    app_name    String,
    user_id     String,
    session_id  String,
    event_id    String,
    event       String COMMENT 'Event data as JSON-encoded text',
    extra_data  String COMMENT 'Additional metadata as JSON-encoded text',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id, event_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session events table';

-- ============================================================================
-- Table: session_summaries
-- Description: Stores AI-generated session summaries
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_summaries (
    app_name    String,
    user_id     String,
    session_id  String,
    filter_key  String COMMENT 'Filter key for multiple summaries per session',
    summary     String COMMENT 'Summary data as JSON-encoded text',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    version_at  DateTime64(9),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(version_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id, filter_key)
SETTINGS allow_nullable_key = 1
COMMENT 'Session summaries table';

-- ============================================================================
-- Table: app_states
-- Description: Stores application-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS app_states (
    app_name    String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY app_name
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, key)
SETTINGS allow_nullable_key = 1
COMMENT 'Application states table';

-- ============================================================================
-- Table: user_states
-- Description: Stores user-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_states (
    app_name    String,
    user_id     String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, key)
SETTINGS allow_nullable_key = 1
COMMENT 'User states table';
