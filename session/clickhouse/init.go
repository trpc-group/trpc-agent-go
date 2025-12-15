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
			state       String,
			extra_data  String,
			created_at  DateTime64(3),
			updated_at  DateTime64(3),
			expires_at  Nullable(DateTime64(3)),
			deleted_at  Nullable(DateTime64(3))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			event       String,
			extra_data  String,
			created_at  DateTime64(3),
			updated_at  DateTime64(3),
			expires_at  Nullable(DateTime64(3)),
			deleted_at  Nullable(DateTime64(3))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, created_at, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			filter_key  String,
			summary     String,
			created_at  DateTime64(3),
			updated_at  DateTime64(3),
			expires_at  Nullable(DateTime64(3)),
			deleted_at  Nullable(DateTime64(3))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, session_id, filter_key, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			key         String,
			value       String,
			updated_at  DateTime64(3),
			expires_at  Nullable(DateTime64(3)),
			deleted_at  Nullable(DateTime64(3))
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
			updated_at  DateTime64(3),
			expires_at  Nullable(DateTime64(3)),
			deleted_at  Nullable(DateTime64(3))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (user_id, key, deleted_at)
		SETTINGS index_granularity = 8192, allow_nullable_key = 1`

	// Index creation SQL (ClickHouse syntax)
	sqlCreateSessionEventsCreatedAtIndex = `
		ALTER TABLE {{TABLE_NAME}} ADD INDEX IF NOT EXISTS {{INDEX_NAME}} (created_at) TYPE minmax GRANULARITY 4`


)

// tableDefinition defines a table with its SQL template
type tableDefinition struct {
	name     string
	template string
}

// indexDefinition defines an index with its table, suffix and SQL template
type indexDefinition struct {
	table    string
	suffix   string
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

// Global index definitions
var indexDefs = []indexDefinition{
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixCreatedAt, sqlCreateSessionEventsCreatedAtIndex},
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

	// Create indexes
	for _, indexDef := range indexDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, indexDef.table)
		indexName := sqldb.BuildIndexName(s.opts.tablePrefix, indexDef.table, indexDef.suffix)
		sql := indexDef.template
		sql = strings.ReplaceAll(sql, "{{TABLE_NAME}}", fullTableName)
		sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", indexName)

		if err := s.chClient.Exec(ctx, sql); err != nil {
			// ClickHouse ADD INDEX IF NOT EXISTS should not fail if index exists
			// but we log a warning just in case
			log.Warnf("create index %s on table %s: %v", indexName, fullTableName, err)
		} else {
			log.Infof("created index: %s on table %s", indexName, fullTableName)
		}
	}

	log.Info("clickhouse session database schema initialized successfully")
	return nil
}
