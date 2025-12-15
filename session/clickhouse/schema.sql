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
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time for TTL',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (user_id, session_id, deleted_at)
SETTINGS index_granularity = 8192, allow_nullable_key = 1
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
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time for TTL (reserved)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (user_id, session_id, event_id, deleted_at)
SETTINGS index_granularity = 8192, allow_nullable_key = 1
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
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time for TTL (reserved)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (user_id, session_id, filter_key, deleted_at)
SETTINGS index_granularity = 8192, allow_nullable_key = 1
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
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time for TTL',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY app_name
ORDER BY (app_name, key, deleted_at)
SETTINGS index_granularity = 8192, allow_nullable_key = 1
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
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time for TTL',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
ORDER BY (user_id, key, deleted_at)
SETTINGS index_granularity = 8192, allow_nullable_key = 1
COMMENT 'User states table';

-- ============================================================================
-- Notes:
-- ============================================================================
-- 1. Engine: ReplacingMergeTree
--    - Uses updated_at as version column for deduplication
--    - Rows with same ORDER BY keys are deduplicated, keeping the one with highest updated_at
--    - Use FINAL keyword or OPTIMIZE TABLE to force deduplication
--
-- 2. Partitioning Strategy:
--    - session_states/events/summaries: (app_name, cityHash64(user_id) % 64)
--      Optimized for user-centric queries, 64 partitions per app
--    - app_states: app_name only
--    - user_states: (app_name, cityHash64(user_id) % 64)
--
-- 3. ORDER BY Design:
--    - Includes deleted_at for soft delete support
--    - event_id in session_events ensures each event is unique
--    - allow_nullable_key = 1 enables nullable columns in ORDER BY
--
-- 4. DateTime64(6):
--    - Microsecond precision for accurate event ordering
--    - Required to distinguish events created in rapid succession
--
-- 5. JSON Type:
--    - Requires ClickHouse 22.3+
--    - Allows flexible schema for state/event data
--    - Supports JSONPath queries: SELECT event.field FROM session_events
--
-- 6. TTL and Soft Delete:
--    - expires_at: Used for automatic data cleanup (application-level)
--    - deleted_at: Used for soft delete functionality
--    - Both are nullable DateTime64(6) fields
--
-- 7. Index Strategy:
--    - minmax index on created_at for efficient time-range queries
--    - GRANULARITY 4 balances index size and query performance
