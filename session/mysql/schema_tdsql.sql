-- TDSQL MySQL Session Service Schema
-- Replace {{PREFIX}} with the configured table prefix.
--
-- Sharding strategy:
--   - 5 session/user scoped tables: shardkey = user_id
--   - app_states: broadcast table (noshardkey_allset), full copy on every node.
--     Small dataset, infrequently updated, no need to shard.
--
-- Shard count:
--   TDSQL does NOT support per-table shard count in CREATE TABLE syntax.
--   Shard count = number of physical sets in the TDSQL instance (e.g., 2, 4, 8).
--   All sharded tables share the same set topology.
--
-- Index design notes:
--   - PRIMARY KEY and UNIQUE KEY must include shardkey (TDSQL hard requirement).
--   - user_id / app_name are already present in all existing UNIQUE KEYs, so UNIQUE KEYs are unchanged.
--   - Normal KEY does NOT need shardkey (TDSQL routes via WHERE clause, not index definition).
--   - InnoDB index key length limit is 3072 bytes (DYNAMIC/COMPRESSED row format).
--     VARCHAR(255) utf8mb4 = 1020 bytes, TIMESTAMP(6) = 7 bytes.
--
-- Constraint: user_id and app_name must be ASCII strings.
--   TDSQL proxy does not convert character sets; non-ASCII shardkey values may cause routing instability.

-- ============================================================================
-- Table: session_states
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
    PRIMARY KEY (`id`, `user_id`),
    UNIQUE KEY `idx_{{PREFIX}}session_states_unique_active` (`app_name`,`user_id`,`session_id`,`deleted_at`),
    KEY `idx_{{PREFIX}}session_states_list` (`app_name`,`user_id`,`updated_at`),
    KEY `idx_{{PREFIX}}session_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=user_id;

-- ============================================================================
-- Table: session_events
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
    PRIMARY KEY (`id`, `user_id`),
    KEY `idx_{{PREFIX}}session_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=user_id;

-- ============================================================================
-- Table: session_track_events
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
    PRIMARY KEY (`id`, `user_id`),
    KEY `idx_{{PREFIX}}session_track_events_lookup` (`app_name`,`user_id`,`session_id`,`created_at`),
    KEY `idx_{{PREFIX}}session_track_events_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=user_id;

-- ============================================================================
-- Table: session_summaries
-- ============================================================================
-- Note: app_name/session_id/filter_key use VARCHAR(128) instead of VARCHAR(255)
-- so the UNIQUE KEY fits within InnoDB's 3072-byte limit at full column length:
-- 128*4*3 + 255*4 = 1536 + 1020 = 2556 < 3072. No prefix index needed.
CREATE TABLE IF NOT EXISTS `{{PREFIX}}session_summaries` (
    `id` BIGINT NOT NULL AUTO_INCREMENT,
    `app_name` VARCHAR(128) NOT NULL,
    `user_id` VARCHAR(255) NOT NULL,
    `session_id` VARCHAR(128) NOT NULL,
    `filter_key` VARCHAR(128) NOT NULL DEFAULT '',
    `summary` JSON DEFAULT NULL,
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `expires_at` TIMESTAMP(6) NULL DEFAULT NULL,
    `deleted_at` TIMESTAMP(6) NULL DEFAULT NULL,
    PRIMARY KEY (`id`, `user_id`),
    UNIQUE KEY `idx_{{PREFIX}}session_summaries_unique_active` (`app_name`,`user_id`,`session_id`,`filter_key`),
    KEY `idx_{{PREFIX}}session_summaries_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=user_id;

-- ============================================================================
-- Table: app_states
-- Broadcast table (noshardkey_allset): full copy on every node.
-- app_states is a small, infrequently-updated table (app-level config).
-- Broadcast gives local reads and keeps schema identical to MySQL (no PK change).
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=noshardkey_allset;

-- ============================================================================
-- Table: user_states
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
    PRIMARY KEY (`id`, `user_id`),
    UNIQUE KEY `idx_{{PREFIX}}user_states_unique_active` (`app_name`,`user_id`,`key`,`deleted_at`),
    KEY `idx_{{PREFIX}}user_states_expires` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci shardkey=user_id;
