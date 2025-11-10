-- PostgreSQL Session Service Schema
-- This file contains the schema for the PostgreSQL session service.
-- You don't need to execute this manually, it's only for reference.

-- Create database (optional)
-- CREATE DATABASE trpc_sessions;

-- Session States Table
-- Stores session metadata and state
CREATE TABLE IF NOT EXISTS session_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  state JSONB DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

-- Partial unique index - only for non-deleted records (supports soft delete)
CREATE UNIQUE INDEX IF NOT EXISTS idx_session_states_unique_active
ON session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;

-- TTL index - partial index for non-null values
CREATE INDEX IF NOT EXISTS idx_session_states_expires
ON session_states(expires_at)
WHERE expires_at IS NOT NULL;

-- Session Events Table
-- Stores session events
CREATE TABLE IF NOT EXISTS session_events (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  event JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

-- Lookup index
CREATE INDEX IF NOT EXISTS idx_session_events_lookup
ON session_events(app_name, user_id, session_id, created_at);

-- TTL index - partial index for non-null values
CREATE INDEX IF NOT EXISTS idx_session_events_expires
ON session_events(expires_at)
WHERE expires_at IS NOT NULL;

-- Session Summaries Table
-- Stores session summaries (supports branch summaries)
CREATE TABLE IF NOT EXISTS session_summaries (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  filter_key VARCHAR(255) NOT NULL DEFAULT '',
  summary JSONB DEFAULT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

-- Partial unique index - only for non-deleted records (supports soft delete)
CREATE UNIQUE INDEX IF NOT EXISTS idx_session_summaries_unique_active
ON session_summaries(app_name, user_id, session_id, filter_key)
WHERE deleted_at IS NULL;

-- TTL index - partial index for non-null values
CREATE INDEX IF NOT EXISTS idx_session_summaries_expires
ON session_summaries(expires_at)
WHERE expires_at IS NOT NULL;

-- App States Table
-- Stores application-level state
CREATE TABLE IF NOT EXISTS app_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  key VARCHAR(255) NOT NULL,
  value TEXT DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

-- Partial unique index - only for non-deleted records (supports soft delete)
CREATE UNIQUE INDEX IF NOT EXISTS idx_app_states_unique_active
ON app_states(app_name, key)
WHERE deleted_at IS NULL;

-- TTL index - partial index for non-null values
CREATE INDEX IF NOT EXISTS idx_app_states_expires
ON app_states(expires_at)
WHERE expires_at IS NOT NULL;

-- User States Table
-- Stores user-level state
CREATE TABLE IF NOT EXISTS user_states (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  key VARCHAR(255) NOT NULL,
  value TEXT DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

-- Partial unique index - only for non-deleted records (supports soft delete)
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_states_unique_active
ON user_states(app_name, user_id, key)
WHERE deleted_at IS NULL;

-- TTL index - partial index for non-null values
CREATE INDEX IF NOT EXISTS idx_user_states_expires
ON user_states(expires_at)
WHERE expires_at IS NOT NULL;



