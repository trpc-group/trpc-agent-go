//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (MySQL syntax).
// MySQL 9.0+ supports native VECTOR type; for older versions, BLOB is used.
const (
	// sqlCreateTableWithVector uses MySQL 9.0+ native VECTOR type.
	sqlCreateTableWithVector = `
		CREATE TABLE IF NOT EXISTS %s (
			memory_id VARCHAR(64) PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			memory_content TEXT NOT NULL,
			topics JSON,
			embedding VECTOR(%d) NOT NULL,
			memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
			event_time TIMESTAMP(6) NULL,
			participants JSON,
			location VARCHAR(1024) NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
			FULLTEXT INDEX idx_fulltext (memory_content),
			INDEX idx_app_user (app_name, user_id),
			INDEX idx_updated_at (updated_at DESC),
			INDEX idx_deleted_at (deleted_at),
			INDEX idx_event_time (event_time DESC),
			INDEX idx_kind (app_name, user_id, memory_kind)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	// sqlCreateTableWithBlob uses BLOB for MySQL 8.x (no native VECTOR).
	sqlCreateTableWithBlob = `
		CREATE TABLE IF NOT EXISTS %s (
			memory_id VARCHAR(64) PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			memory_content TEXT NOT NULL,
			topics JSON,
			embedding BLOB NOT NULL,
			memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
			event_time TIMESTAMP(6) NULL,
			participants JSON,
			location VARCHAR(1024) NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
			FULLTEXT INDEX idx_fulltext (memory_content),
			INDEX idx_app_user (app_name, user_id),
			INDEX idx_updated_at (updated_at DESC),
			INDEX idx_deleted_at (deleted_at),
			INDEX idx_event_time (event_time DESC),
			INDEX idx_kind (app_name, user_id, memory_kind)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
)

// detectVectorSupport checks if the MySQL server supports native VECTOR type (9.0+).
func (s *Service) detectVectorSupport(ctx context.Context) bool {
	// Try creating a temporary probe to check VECTOR support.
	var result int
	err := s.db.QueryRow(ctx, []any{&result},
		"SELECT 1 FROM (SELECT CAST('[1.0]' AS VECTOR) AS v) t LIMIT 1")
	return err == nil
}

// initDB initializes the database schema.
// Note: s.supportsVector must be set before calling this method.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(ctx, "initializing mysqlvec memory database schema...")

	if s.supportsVector {
		log.InfoContext(ctx, "mysqlvec: MySQL VECTOR type detected")
	} else {
		log.InfoContext(ctx, "mysqlvec: MySQL VECTOR type not available, using BLOB fallback")
	}

	var createSQL string
	if s.supportsVector {
		createSQL = fmt.Sprintf(sqlCreateTableWithVector, s.tableName, s.opts.indexDimension)
	} else {
		createSQL = fmt.Sprintf(sqlCreateTableWithBlob, s.tableName)
	}

	if _, err := s.db.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", s.tableName, err)
	}
	log.InfofContext(ctx, "created table: %s", s.tableName)

	// Add episodic columns for migration from older schemas.
	// MySQL does not support ADD COLUMN IF NOT EXISTS, so we use
	// plain ADD COLUMN and silently ignore error 1060 (Duplicate column name).
	episodicColumns := []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact'", s.tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN event_time TIMESTAMP(6) NULL", s.tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN participants JSON", s.tableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN location VARCHAR(1024) NULL", s.tableName),
	}
	for _, ddl := range episodicColumns {
		if _, err := s.db.Exec(ctx, ddl); err != nil {
			if !isDuplicateColumnError(err) {
				return fmt.Errorf("add episodic column on table %s failed: %w", s.tableName, err)
			}
		}
	}

	log.InfoContext(ctx, "mysqlvec memory database schema initialized successfully")
	return nil
}

// isDuplicateColumnError checks for MySQL error 1060 (Duplicate column name).
func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "1060")
}
