//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (ClickHouse syntax)
// Using ReplacingMergeTree with updated_at as version column for deduplication.
// Partition by (app_name, cityHash64(user_id) % 64) for user-centric query optimization.
// ORDER BY includes deleted_at for soft delete support (reserved for future use).
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			state       JSON,
			extra_data  JSON,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	// expires_at is a reserved field for future use.
	// Events are bound to session lifecycle - when session expires/is deleted, its events are also deleted.
	// event_id is included in ORDER BY to ensure uniqueness and prevent deduplication by ReplacingMergeTree.
	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			event_id    String,
			event       JSON,
			extra_data  JSON,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, event_id, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	// expires_at is a reserved field for future use.
	// Summaries are bound to session lifecycle - when session expires/is deleted, its summaries are also deleted.
	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			filter_key  String,
			summary     JSON,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, filter_key, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			key         String,
			value       String,
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY app_name
		ORDER BY (app_name, key, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			key         String,
			value       String,
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, key, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`
)

// tableDefinition defines a table with its SQL template
type tableDefinition struct {
	name     string
	template string
}

// Global table definitions
var tableDefs = []tableDefinition{
	{sqldb.TableNameSessionStates, sqlCreateSessionStatesTable},
	{sqldb.TableNameSessionEvents, sqlCreateSessionEventsTable},
	{sqldb.TableNameSessionSummaries, sqlCreateSessionSummariesTable},
	{sqldb.TableNameAppStates, sqlCreateAppStatesTable},
	{sqldb.TableNameUserStates, sqlCreateUserStatesTable},
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.Info("initializing clickhouse session database schema...")

	// Create tables
	for _, tableDef := range tableDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)
		sql := strings.ReplaceAll(tableDef.template, "{{TABLE_NAME}}", fullTableName)

		if err := s.chClient.Exec(ctx, sql); err != nil {
			return fmt.Errorf("create table %s failed: %w", fullTableName, err)
		}
		log.Infof("created table: %s", fullTableName)
	}

	log.Info("clickhouse session database schema initialized successfully")
	return nil
}
