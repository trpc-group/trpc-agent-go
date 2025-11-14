-- MySQL Session Service Schema
-- This file provides reference SQL for manual database initialization.
-- The service will automatically create these tables and indexes if skipDBInit is false.

-- ============================================================================
-- Table: session_states
-- Description: Stores session state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    state JSON DEFAULT NULL COMMENT 'Session state in JSON format',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Session states table';

-- Unique index on (app_name, user_id, session_id, deleted_at)
-- Note: deleted_at is included because MySQL doesn't support partial indexes like PostgreSQL
CREATE UNIQUE INDEX idx_session_states_unique_active
ON session_states(app_name, user_id, session_id, deleted_at);

-- TTL cleanup index
CREATE INDEX idx_session_states_expires
ON session_states(expires_at);

-- ============================================================================
-- Table: session_events
-- Description: Stores session events/messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    event JSON NOT NULL COMMENT 'Event data in JSON format',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Session events table';

-- Lookup index for querying events by session
CREATE INDEX idx_session_events_lookup
ON session_events(app_name, user_id, session_id, created_at);

-- TTL cleanup index
CREATE INDEX idx_session_events_expires
ON session_events(expires_at);

-- ============================================================================
-- Table: session_summaries
-- Description: Stores AI-generated session summaries
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_summaries (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    filter_key VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Filter key for multiple summaries per session',
    summary JSON DEFAULT NULL COMMENT 'Summary data in JSON format',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Session summaries table';

-- Unique index on (app_name, user_id, session_id, filter_key, deleted_at)
CREATE UNIQUE INDEX idx_session_summaries_unique_active
ON session_summaries(app_name, user_id, session_id, filter_key, deleted_at);

-- TTL cleanup index
CREATE INDEX idx_session_summaries_expires
ON session_summaries(expires_at);

-- ============================================================================
-- Table: app_states
-- Description: Stores application-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS app_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL COMMENT 'State key (backticks because key is a reserved word)',
    value TEXT DEFAULT NULL COMMENT 'State value',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Application states table';

-- Unique index on (app_name, key, deleted_at)
CREATE UNIQUE INDEX idx_app_states_unique_active
ON app_states(app_name, `key`, deleted_at);

-- TTL cleanup index
CREATE INDEX idx_app_states_expires
ON app_states(expires_at);

-- ============================================================================
-- Table: user_states
-- Description: Stores user-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL COMMENT 'State key (backticks because key is a reserved word)',
    value TEXT DEFAULT NULL COMMENT 'State value',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='User states table';

-- Unique index on (app_name, user_id, key, deleted_at)
CREATE UNIQUE INDEX idx_user_states_unique_active
ON user_states(app_name, user_id, `key`, deleted_at);

-- TTL cleanup index
CREATE INDEX idx_user_states_expires
ON user_states(expires_at);

-- ============================================================================
-- Notes:
-- ============================================================================
-- 1. MySQL vs PostgreSQL Differences:
--    - JSON type instead of JSONB (MySQL doesn't have JSONB)
--    - BIGINT AUTO_INCREMENT instead of BIGSERIAL
--    - ON DUPLICATE KEY UPDATE instead of ON CONFLICT DO UPDATE
--    - deleted_at included in unique indexes (MySQL doesn't support partial indexes)
--
-- 2. Character Set:
--    - Using utf8mb4 for full Unicode support (including emojis)
--    - Using utf8mb4_unicode_ci for case-insensitive comparisons
--
-- 3. Storage Engine:
--    - Using InnoDB for transaction support and foreign key constraints
--
-- 4. TTL and Soft Delete:
--    - expires_at: Used for automatic data cleanup
--    - deleted_at: Used for soft delete functionality
--    - Both are nullable TIMESTAMP fields
--
-- 5. Reserved Keywords:
--    - 'key' is a MySQL reserved word, so it's enclosed in backticks
--
-- 6. Index Strategy:
--    - Unique indexes include deleted_at to allow same key with different deleted_at values
--    - TTL indexes on expires_at for efficient cleanup
--    - Lookup indexes for common query patterns
