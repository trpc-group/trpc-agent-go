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
// Most tables use updated_at as the ReplacingMergeTree version column. Session
// summaries keep updated_at as the semantic summary cutoff and use version_at
// only for replacement ordering.
// Partition by (app_name, cityHash64(user_id) % 64) for user-centric query optimization.
// CRITICAL: deleted_at is NOT included in ORDER BY to allow ReplacingMergeTree to collapse deleted records.
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
		ORDER BY (app_name, user_id, session_id)
		SETTINGS allow_nullable_key = 1`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			event_id    String,
			event       JSON,
			event_raw   String,
			extra_data  JSON,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (app_name, user_id, session_id, event_id)
		SETTINGS allow_nullable_key = 1`

	sqlCreateSessionTrackEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			track       String,
			event_index UInt64,
			event_id    String,
			event       String,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (app_name, user_id, session_id, track, event_index, event_id)
		SETTINGS allow_nullable_key = 1`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name    String,
			user_id     String,
			session_id  String,
			filter_key  String,
			summary     JSON,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			version_at  DateTime64(9),
			expires_at  Nullable(DateTime64(6)),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(version_at)
		PARTITION BY (app_name, cityHash64(user_id) % 64)
		ORDER BY (app_name, user_id, session_id, filter_key)
		SETTINGS allow_nullable_key = 1`

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
		ORDER BY (app_name, key)
		SETTINGS allow_nullable_key = 1`

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
		ORDER BY (app_name, user_id, key)
		SETTINGS allow_nullable_key = 1`
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
	{sqldb.TableNameSessionTrackEvents, sqlCreateSessionTrackEventsTable},
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

	// ClickHouse JSON columns normalize dotted keys as nested paths. Preserve an
	// exact event document alongside the JSON index so extension metadata remains
	// round-trippable. Existing installations receive the additive column here.
	if err := s.chClient.Exec(ctx,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS event_raw String DEFAULT '' AFTER event", s.tableSessionEvents)); err != nil {
		return fmt.Errorf("add event_raw column failed: %w", err)
	}
	// Keep concurrently appended track rows distinct even when independent
	// service instances observe the same event count. ClickHouse only permits a
	// new sorting-key expression to reference columns added by the same ALTER,
	// so old tables need a combined migration while new tables need no ALTER.
	rows, err := s.chClient.Query(ctx, `SELECT count() FROM system.tables
		WHERE database = currentDatabase() AND name = ? AND position(sorting_key, 'event_id') > 0`,
		s.tableSessionTrackEvents)
	if err != nil {
		return fmt.Errorf("check track event order key failed: %w", err)
	}
	var hasTrackEventIdentity uint64
	if rows.Next() {
		if err := rows.Scan(&hasTrackEventIdentity); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan track event order key failed: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("check track event order key failed: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close track event order key rows failed: %w", err)
	}
	if hasTrackEventIdentity == 0 {
		if err := s.chClient.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s
			ADD COLUMN event_id String AFTER event_index,
			MODIFY ORDER BY (app_name, user_id, session_id, track, event_index, event_id)`,
			s.tableSessionTrackEvents)); err != nil {
			return fmt.Errorf("migrate track event identity failed: %w", err)
		}
	}

	log.Info("clickhouse session database schema initialized successfully")
	return nil
}
