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
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (MySQL syntax)
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			state JSON DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			event JSON NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			filter_key VARCHAR(255) NOT NULL DEFAULT '',
			summary JSON DEFAULT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			` + "`key`" + ` VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			` + "`key`" + ` VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	// Index creation SQL (MySQL syntax)
	// Note: MySQL doesn't support IF NOT EXISTS for indexes until MySQL 8.0.13+
	// We'll handle duplicate index errors in the creation logic
	// Note: For utf8mb4, each character takes 4 bytes. VARCHAR(255) = 1020 bytes
	// MySQL InnoDB index key length limit is 3072 bytes (767 bytes for older versions)
	// We use prefix indexes to stay within limits: 191 chars * 4 bytes = 764 bytes per field

	// session_states: unique index on (app_name, user_id, session_id, deleted_at)
	// MySQL doesn't support partial indexes like PostgreSQL, so we include deleted_at in the index
	sqlCreateSessionStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(191), user_id(191), session_id(191), deleted_at)`

	// session_states: TTL index on (expires_at)
	sqlCreateSessionStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// session_events: lookup index on (app_name, user_id, session_id, created_at)
	sqlCreateSessionEventsIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(191), user_id(191), session_id(191), created_at)`

	// session_events: TTL index on (expires_at)
	sqlCreateSessionEventsExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// session_summaries: unique index on (app_name, user_id, session_id, filter_key, deleted_at)
	sqlCreateSessionSummariesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(191), user_id(191), session_id(191), filter_key(191), deleted_at)`

	// session_summaries: TTL index on (expires_at)
	sqlCreateSessionSummariesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// app_states: unique index on (app_name, key, deleted_at)
	sqlCreateAppStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(191), ` + "`key`" + `(191), deleted_at)`

	// app_states: TTL index on (expires_at)
	sqlCreateAppStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`

	// user_states: unique index on (app_name, user_id, key, deleted_at)
	sqlCreateUserStatesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name(191), user_id(191), ` + "`key`" + `(191), deleted_at)`

	// user_states: TTL index on (expires_at)
	sqlCreateUserStatesExpiresIndex = `
		CREATE INDEX {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)`
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
	// Unique indexes (include deleted_at for MySQL compatibility)
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive, sqlCreateSessionStatesUniqueIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive, sqlCreateSessionSummariesUniqueIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive, sqlCreateAppStatesUniqueIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive, sqlCreateUserStatesUniqueIndex},
	// Lookup indexes
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup, sqlCreateSessionEventsIndex},
	// TTL indexes
	{sqldb.TableNameSessionStates, sqldb.IndexSuffixExpires, sqlCreateSessionStatesExpiresIndex},
	{sqldb.TableNameSessionEvents, sqldb.IndexSuffixExpires, sqlCreateSessionEventsExpiresIndex},
	{sqldb.TableNameSessionSummaries, sqldb.IndexSuffixExpires, sqlCreateSessionSummariesExpiresIndex},
	{sqldb.TableNameAppStates, sqldb.IndexSuffixExpires, sqlCreateAppStatesExpiresIndex},
	{sqldb.TableNameUserStates, sqldb.IndexSuffixExpires, sqlCreateUserStatesExpiresIndex},
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(
		ctx,
		"initializing mysql session database schema...",
	)

	// Create tables
	for _, tableDef := range tableDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)
		sql := strings.ReplaceAll(tableDef.template, "{{TABLE_NAME}}", fullTableName)

		if _, err := s.mysqlClient.Exec(ctx, sql); err != nil {
			return fmt.Errorf("create table %s failed: %w", fullTableName, err)
		}
		log.InfofContext(
			ctx,
			"created table: %s",
			fullTableName,
		)
	}

	// Create indexes
	for _, indexDef := range indexDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, indexDef.table)
		indexName := sqldb.BuildIndexName(s.opts.tablePrefix, indexDef.table, indexDef.suffix)
		sql := indexDef.template
		sql = strings.ReplaceAll(sql, "{{TABLE_NAME}}", fullTableName)
		sql = strings.ReplaceAll(sql, "{{INDEX_NAME}}", indexName)

		// MySQL doesn't have "IF NOT EXISTS" for indexes in older versions
		// We'll use a different approach: try to create and ignore duplicate key errors
		if _, err := s.mysqlClient.Exec(ctx, sql); err != nil {
			// Check if it's a duplicate key error (error code 1061)
			// This is more robust than checking error message strings
			if !isDuplicateKeyError(err) {
				return fmt.Errorf(
					"create index %s on table %s failed: %w",
					indexName,
					fullTableName,
					err,
				)
			}
			// Index already exists, log and continue.
			log.InfofContext(
				ctx,
				"index %s already exists on table %s, skipping",
				indexName,
				fullTableName,
			)
		} else {
			log.InfofContext(
				ctx,
				"created index: %s on table %s",
				indexName,
				fullTableName,
			)
		}
	}

	log.InfoContext(
		ctx,
		"mysql session database schema initialized successfully",
	)
	return nil
}

// isDuplicateKeyError checks if the error is a MySQL duplicate key error.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == sqldb.MySQLErrDuplicateKeyName ||
			mysqlErr.Number == sqldb.MySQLErrDuplicateEntry
	}

	return false
}
