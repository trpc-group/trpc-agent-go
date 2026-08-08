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

-- Session Track Events Table
-- Stores protocol-specific track events associated with a session
CREATE TABLE IF NOT EXISTS session_track_events (
  id BIGSERIAL PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  track VARCHAR(255) NOT NULL,
  event JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  deleted_at TIMESTAMP DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_track_events_lookup
ON session_track_events(app_name, user_id, session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_session_track_events_expires
ON session_track_events(expires_at)
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

-- Session Revisions Table
-- Stores the active turn checkpoint and write generation
CREATE TABLE IF NOT EXISTS session_revisions (
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  record JSONB NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  PRIMARY KEY (app_name, user_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_session_revisions_expires
ON session_revisions(expires_at)
WHERE expires_at IS NOT NULL;

-- Session Revision Archives Table
-- Stores immutable snapshots retained for replacement replay
CREATE TABLE IF NOT EXISTS session_revision_archives (
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  session_id VARCHAR(255) NOT NULL,
  generation BIGINT NOT NULL,
  snapshot JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP DEFAULT NULL,
  PRIMARY KEY (app_name, user_id, session_id, generation)
);

CREATE INDEX IF NOT EXISTS idx_session_revision_archives_expires
ON session_revision_archives(expires_at)
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

