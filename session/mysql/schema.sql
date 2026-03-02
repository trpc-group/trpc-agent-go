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
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}session_states_unique_active` (`app_name`,`user_id`,`session_id`,`deleted_at`),
    KEY `idx_{{PREFIX}}session_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Table: session_track_events
-- Description: Stores session track events
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_track_events` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `track` VARCHAR(255) NOT NULL,
    `event` JSON NOT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_{{PREFIX}}session_track_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_track_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Table: session_summaries
-- Description: Stores AI-generated session summaries
-- Note: No created_at column because summaries use upsert pattern (overwrite on duplicate)
-- Note: Uses unique index on business key only (no deleted_at) to prevent duplicate records
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_summaries` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(255) NOT NULL,
    `filter_key` VARCHAR(255) NOT NULL DEFAULT '',
    `summary` JSON DEFAULT NULL,
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}session_summaries_unique_active` (`app_name`(191),`user_id`(191),`session_id`(191),`filter_key`(191)),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Table: app_states
-- Description: Stores application-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}app_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}app_states_unique_active` (`app_name`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}app_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Table: user_states
-- Description: Stores user-level state data
-- ============================================================================
CREATE TABLE IF NOT EXISTS `{{PREFIX}}user_states` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(255) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    `value` TEXT DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_{{PREFIX}}user_states_unique_active` (`app_name`,`user_id`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}user_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Notes:
-- ============================================================================
-- 1. Table Prefix:
--    - Replace {{PREFIX}} with your actual table prefix (e.g., trpc_)
--    - Example: {{PREFIX}}session_states becomes trpc_session_states
--
-- 2. Character Set:
--    - Using utf8mb4 for full Unicode support (including emojis)
--    - Using utf8mb4_unicode_ci collation for case-insensitive comparisons
--
-- 3. Storage Engine:
--    - Using InnoDB for transaction support and ACID compliance
--
-- 4. MySQL Version Requirements:
--    - MySQL 5.6.5+ is required for multiple TIMESTAMP columns with CURRENT_TIMESTAMP
--    - TIMESTAMP(6) provides microsecond precision (6 digits after decimal point)
--    - Older MySQL versions may not support multiple TIMESTAMP DEFAULT CURRENT_TIMESTAMP
--
-- 5. Primary Keys:
--    - All tables use BIGINT AUTO_INCREMENT for id
--    - Defined inline with column definition
--
-- 6. Indexes:
--    - All indexes are defined inline in CREATE TABLE statements
--    - UNIQUE indexes for most tables include deleted_at column
--    - Note: MySQL UNIQUE constraint doesn't prevent duplicate NULL values
--    - Multiple records with deleted_at=NULL can coexist (NULL != NULL in MySQL)
--    - Application code handles uniqueness for active records (deleted_at IS NULL)
--    - Exception: session_summaries uses unique index WITHOUT deleted_at to prevent
--      duplicate active records, since summary data is regenerable and uses upsert pattern
--
-- 7. TTL and Soft Delete:
--    - expires_at: Used for automatic data cleanup (NULL = never expires)
--    - deleted_at: Used for soft delete functionality (NULL = active record)
--    - Both are nullable TIMESTAMP(6) fields
--
-- 8. Reserved Keywords:
--    - 'key' is a MySQL reserved word, enclosed in backticks
--
-- 9. Session Summaries Table:
--    - No created_at column because summaries use upsert pattern
--    - Application code uses updated_at >= session.created_at for filtering
--    - When session expires and is recreated, old summaries are identified by updated_at
--
-- 10. Column Definitions:
--     - All NOT NULL columns are explicitly marked
--     - All nullable columns use NULL (DEFAULT NULL added for clarity)
--     - JSON columns can be NULL (empty summary not yet generated)
--     - TEXT columns for potentially large values (e.g., app_states.value)



