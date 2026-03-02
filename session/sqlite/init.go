//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const (
	sqlCreateSessionStatesTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  state BLOB DEFAULT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateSessionEventsTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  event BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateSessionTrackEventsTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  track TEXT NOT NULL,
  event BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateSessionSummariesTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  filter_key TEXT NOT NULL DEFAULT '',
  summary BLOB DEFAULT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateAppStatesTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  key TEXT NOT NULL,
  value BLOB DEFAULT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateUserStatesTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value BLOB DEFAULT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER DEFAULT NULL,
  deleted_at INTEGER DEFAULT NULL
);`
)

const (
	sqlCreateSessionStatesUniqueIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id, session_id)
WHERE deleted_at IS NULL;`

	sqlCreateSessionStatesExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`

	sqlCreateSessionEventsLookupIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id, session_id, created_at);`

	sqlCreateSessionEventsExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`

	sqlCreateSessionTracksLookupIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id, session_id, track, created_at);`

	sqlCreateSessionTracksExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`

	sqlCreateSessionSummariesUniqueIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id, session_id, filter_key);`

	sqlCreateSessionSummariesExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`

	sqlCreateAppStatesUniqueIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, key)
WHERE deleted_at IS NULL;`

	sqlCreateAppStatesExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`

	sqlCreateUserStatesUniqueIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id, key)
WHERE deleted_at IS NULL;`

	sqlCreateUserStatesExpiresIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(expires_at)
WHERE expires_at IS NOT NULL;`
)

type tableDefinition struct {
	name     string
	template string
}

type indexDefinition struct {
	table    string
	suffix   string
	template string
}

func (s *Service) initDB(ctx context.Context) error {
	tables := []tableDefinition{
		{sqldb.TableNameSessionStates, sqlCreateSessionStatesTable},
		{sqldb.TableNameSessionEvents, sqlCreateSessionEventsTable},
		{sqldb.TableNameSessionTrackEvents, sqlCreateSessionTrackEventsTable},
		{sqldb.TableNameSessionSummaries, sqlCreateSessionSummariesTable},
		{sqldb.TableNameAppStates, sqlCreateAppStatesTable},
		{sqldb.TableNameUserStates, sqlCreateUserStatesTable},
	}

	for _, t := range tables {
		full := s.fullTableName(t.name)
		stmt := strings.ReplaceAll(t.template, "{{TABLE_NAME}}", full)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create table %s: %w", full, err)
		}
	}

	indexes := []indexDefinition{
		{
			table:    sqldb.TableNameSessionStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			template: sqlCreateSessionStatesUniqueIndex,
		},
		{
			table:    sqldb.TableNameSessionStates,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateSessionStatesExpiresIndex,
		},
		{
			table:    sqldb.TableNameSessionEvents,
			suffix:   sqldb.IndexSuffixLookup,
			template: sqlCreateSessionEventsLookupIndex,
		},
		{
			table:    sqldb.TableNameSessionEvents,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateSessionEventsExpiresIndex,
		},
		{
			table:    sqldb.TableNameSessionTrackEvents,
			suffix:   sqldb.IndexSuffixLookup,
			template: sqlCreateSessionTracksLookupIndex,
		},
		{
			table:    sqldb.TableNameSessionTrackEvents,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateSessionTracksExpiresIndex,
		},
		{
			table:    sqldb.TableNameSessionSummaries,
			suffix:   sqldb.IndexSuffixUniqueActive,
			template: sqlCreateSessionSummariesUniqueIndex,
		},
		{
			table:    sqldb.TableNameSessionSummaries,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateSessionSummariesExpiresIndex,
		},
		{
			table:    sqldb.TableNameAppStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			template: sqlCreateAppStatesUniqueIndex,
		},
		{
			table:    sqldb.TableNameAppStates,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateAppStatesExpiresIndex,
		},
		{
			table:    sqldb.TableNameUserStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			template: sqlCreateUserStatesUniqueIndex,
		},
		{
			table:    sqldb.TableNameUserStates,
			suffix:   sqldb.IndexSuffixExpires,
			template: sqlCreateUserStatesExpiresIndex,
		},
	}

	for _, idx := range indexes {
		fullTable := s.fullTableName(idx.table)
		indexName := sqldb.BuildIndexName(
			s.opts.tablePrefix,
			idx.table,
			idx.suffix,
		)
		stmt := strings.ReplaceAll(idx.template, "{{TABLE_NAME}}",
			fullTable)
		stmt = strings.ReplaceAll(stmt, "{{INDEX_NAME}}", indexName)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create index %s: %w", indexName, err)
		}
	}

	return nil
}
