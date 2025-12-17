//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL template for table creation (MySQL syntax)
const (
	sqlCreateMemoriesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			memory_id VARCHAR(64) NOT NULL,
			memory_data JSON NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (app_name, user_id, memory_id),
			INDEX idx_app_user (app_name, user_id),
			INDEX idx_deleted_at (deleted_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
)

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(
		ctx,
		"initializing mysql memory database schema...",
	)

	// Create table
	fullTableName := s.tableName
	sql := strings.ReplaceAll(sqlCreateMemoriesTable, "{{TABLE_NAME}}", fullTableName)

	if _, err := s.db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("create table %s failed: %w", fullTableName, err)
	}
	log.InfofContext(
		ctx,
		"created table: %s",
		fullTableName,
	)

	log.InfoContext(
		ctx,
		"mysql memory database schema initialized successfully",
	)
	return nil
}
