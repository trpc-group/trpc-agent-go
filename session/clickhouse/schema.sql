-- ClickHouse Session Service Schema
-- This file provides reference SQL for manual database initialization.
-- The service will automatically create these tables and indexes if skipDBInit is false.
--
-- Requirements:
--   - ClickHouse 22.3+ (for JSON type support)
--   - Database must be created before running these statements

-- ============================================================================
-- Table: session_states
-- Description: Stores session state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_states (
    app_name    String,
    user_id     String,
    session_id  String,
    state       JSON COMMENT 'Session state in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
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
    event       JSON COMMENT 'Event data in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
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
    summary     JSON COMMENT 'Summary data in JSON format',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
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
