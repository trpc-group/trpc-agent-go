-- MySQL Session Service Schema
-- This file provides reference SQL for manual database initialization.
-- The service will automatically create these tables and indexes if skipDBInit is false.
-- Note: Replace {{PREFIX}} with your actual table prefix (e.g., trpc_) in table and index names.

-- ============================================================================
-- Table: session_states
-- Description: Stores session state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `state` JSON DEFAULT NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `expires_at` TIMESTAMP NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}session_states_unique_active` (`app_name`,`user_id`,`session_id`,`deleted_at`),
    KEY `idx_{{PREFIX}}session_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Table: session_events
-- Description: Stores session events/messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_events` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `event` JSON NOT NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `expires_at` TIMESTAMP NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Table: session_track_events
-- Description: Stores session track events
-- ============================================================================
CREATE TABLE IF NOT EXISTS session_track_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    track VARCHAR(255) NOT NULL,
    event JSON NOT NULL COMMENT 'Track event data in JSON format',
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    expires_at TIMESTAMP(6) NULL DEFAULT NULL COMMENT 'Expiration time for TTL',
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL COMMENT 'Soft delete timestamp'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Session track events table';

-- Lookup index for querying track events by session and track
CREATE INDEX idx_session_track_events_lookup
ON session_track_events(app_name, user_id, session_id, track, created_at);

-- TTL cleanup index
CREATE INDEX idx_session_track_events_expires
ON session_track_events(expires_at);

-- ============================================================================
-- Table: session_summaries
-- Description: Stores AI-generated session summaries
-- Note: No created_at column because summaries use upsert pattern (overwrite on duplicate)
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_summaries` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `filter_key` VARCHAR(255) NOT NULL DEFAULT '',
    `summary` JSON DEFAULT NULL,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `expires_at` TIMESTAMP NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_summaries_lookup` (`app_name`,`user_id`,`session_id`,`deleted_at`),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Table: app_states
-- Description: Stores application-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}app_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `expires_at` TIMESTAMP NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}app_states_unique_active` (`app_name`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}app_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Table: user_states
-- Description: Stores user-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}user_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `expires_at` TIMESTAMP NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}user_states_unique_active` (`app_name`,`user_id`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}user_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- Notes:
-- ============================================================================
-- 1. Table Prefix:
--    - Replace {{PREFIX}} with your actual table prefix (e.g., trpc_)
--    - Example: {{PREFIX}}session_states becomes trpc_session_states
--
-- 2. Character Set:
--    - Using utf8mb4 for full Unicode support (including emojis)
--
-- 3. Storage Engine:
--    - Using InnoDB for transaction support and ACID compliance
--
-- 4. Primary Keys:
--    - All tables use BIGINT AUTO_INCREMENT for id
--    - Defined inline with column definition
--
-- 5. Indexes:
--    - All indexes are defined inline in CREATE TABLE statements
--    - UNIQUE indexes include deleted_at column
--    - Note: MySQL UNIQUE constraint doesn't prevent duplicate NULL values
--    - Multiple records with deleted_at=NULL can coexist (NULL != NULL in MySQL)
--    - Application code handles uniqueness for active records (deleted_at IS NULL)
--
-- 6. TTL and Soft Delete:
--    - expires_at: Used for automatic data cleanup (NULL = never expires)
--    - deleted_at: Used for soft delete functionality (NULL = active record)
--    - Both are nullable TIMESTAMP fields
--
-- 7. Reserved Keywords:
--    - 'key' is a MySQL reserved word, enclosed in backticks
--
-- 8. Session Summaries Table:
--    - No created_at column because summaries use upsert pattern
--    - Application code uses updated_at >= session.created_at for filtering
--    - When session expires and is recreated, old summaries are identified by updated_at
--
-- 9. Column Definitions:
--    - All NOT NULL columns are explicitly marked
--    - All nullable columns use NULL (not DEFAULT NULL for clarity)
--    - JSON columns can be NULL (empty summary not yet generated)
--    - TEXT columns for potentially large values (e.g., app_states.value)


