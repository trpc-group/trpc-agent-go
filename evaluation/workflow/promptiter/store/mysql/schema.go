//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

const (
	tableNameRuns        = "promptiter_runs"
	runIDUniqueIndexName = "uniq_promptiter_runs_run_id"
	sqlCreateRunsTable   = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT NOT NULL AUTO_INCREMENT,
			run_id VARCHAR(255) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT '',
			run_result JSON NOT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	sqlCreateRunIDUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(run_id)`
)

func ensureSchema(ctx context.Context, db storage.Client, tableName string) error {
	createTableQuery := strings.ReplaceAll(sqlCreateRunsTable, "{{TABLE_NAME}}", tableName)
	if _, err := db.Exec(ctx, createTableQuery); err != nil {
		return fmt.Errorf("create table %s failed: %w", tableName, err)
	}
	createIndexQuery := strings.ReplaceAll(sqlCreateRunIDUniqueIndex, "{{TABLE_NAME}}", tableName)
	createIndexQuery = strings.ReplaceAll(createIndexQuery, "{{INDEX_NAME}}", runIDUniqueIndexName)
	if _, err := db.Exec(ctx, createIndexQuery); err != nil {
		if mysqldb.IsDuplicateKeyName(err) {
			return nil
		}
		return fmt.Errorf("create index %s on table %s failed: %w", runIDUniqueIndexName, tableName, err)
	}
	return nil
}
