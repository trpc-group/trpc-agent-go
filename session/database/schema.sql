-- MySQL Session Service Schema
-- This file contains the schema for the MySQL session service.
-- Note: GORM can auto-migrate these tables, but this file is provided for reference and manual setup.

-- Create database (optional)
-- CREATE DATABASE IF NOT EXISTS trpc_sessions CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
-- USE trpc_sessions;

-- Session States Table
-- Stores session metadata and state
CREATE TABLE IF NOT EXISTS `session_states` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `user_id` VARCHAR(255) NOT NULL,
  `session_id` VARCHAR(255) NOT NULL,
  `state` MEDIUMBLOB,
  `created_at` DATETIME NOT NULL,
  `updated_at` DATETIME NOT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_app_user_session` (`app_name`, `user_id`, `session_id`),
  INDEX `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Session Events Table
-- Stores session events
CREATE TABLE IF NOT EXISTS `session_events` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `user_id` VARCHAR(255) NOT NULL,
  `session_id` VARCHAR(255) NOT NULL,
  `event_data` MEDIUMBLOB NOT NULL,
  `timestamp` DATETIME NOT NULL,
  `created_at` DATETIME NOT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  INDEX `idx_app_user_session_event` (`app_name`, `user_id`, `session_id`, `timestamp`),
  INDEX `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Session Summaries Table
-- Stores session summaries (supports branch summaries)
CREATE TABLE IF NOT EXISTS `session_summaries` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `user_id` VARCHAR(255) NOT NULL,
  `session_id` VARCHAR(255) NOT NULL,
  `filter_key` VARCHAR(255) NOT NULL DEFAULT '',
  `summary` MEDIUMBLOB NOT NULL,
  `updated_at` DATETIME NOT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  INDEX `idx_app_user_session_filter` (`app_name`, `user_id`, `session_id`, `filter_key`),
  INDEX `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- App States Table
-- Stores application-level state
CREATE TABLE IF NOT EXISTS `app_states` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `state_key` VARCHAR(255) NOT NULL,
  `value` MEDIUMBLOB NOT NULL,
  `updated_at` DATETIME NOT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_app_key` (`app_name`, `state_key`),
  INDEX `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- User States Table
-- Stores user-level state
CREATE TABLE IF NOT EXISTS `user_states` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `user_id` VARCHAR(255) NOT NULL,
  `state_key` VARCHAR(255) NOT NULL,
  `value` MEDIUMBLOB NOT NULL,
  `updated_at` DATETIME NOT NULL,
  `expires_at` DATETIME DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE INDEX `idx_app_user_key` (`app_name`, `user_id`, `state_key`),
  INDEX `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;



