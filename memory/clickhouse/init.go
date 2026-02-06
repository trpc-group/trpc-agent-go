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

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (ClickHouse syntax).
// Using ReplacingMergeTree with updated_at as version column for
// deduplication. Partition by (app_name, cityHash64(user_id) % 64) for
// user-centric query optimization.
const (
	sqlCreateMemoriesTablePattern = `
		CREATE TABLE IF NOT EXISTS %s (
			memory_id   String,
			app_name    String,
			user_id     String,
			memory_data String,
			created_at  DateTime64(6),
			updated_at  DateTime64(6),
			deleted_at  Nullable(DateTime64(6))
		) ENGINE = ReplacingMergeTree(updated_at)
		PARTITION BY (app_name, cityHash64(user_id) %% 64)
		ORDER BY (app_name, user_id, memory_id)
		SETTINGS allow_nullable_key = 1`
)

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(ctx, "initializing clickhouse memory database schema...")

	// Create memories table.
	tableSQL := buildCreateTableSQL(s.tableName)
	if err := s.chClient.Exec(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", s.tableName, err)
	}
	log.InfofContext(ctx, "created table: %s", s.tableName)

	log.InfoContext(ctx, "clickhouse memory database schema initialized successfully")
	return nil
}

// buildCreateTableSQL builds the CREATE TABLE SQL.
func buildCreateTableSQL(tableName string) string {
	return strings.TrimSpace(fmt.Sprintf(sqlCreateMemoriesTablePattern, tableName))
}
