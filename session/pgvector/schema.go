//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

// SQL templates for table creation.
// These are identical to session/postgres.
const (
	sqlCreateSessionStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			state JSONB DEFAULT NULL,
			created_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionTrackEventsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			track VARCHAR(255) NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateSessionSummariesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			filter_key VARCHAR(255) NOT NULL DEFAULT '',
			summary JSONB DEFAULT NULL,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateAppStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`

	sqlCreateUserStatesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGSERIAL PRIMARY KEY,
			app_name VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			value TEXT DEFAULT NULL,
			created_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL
				DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NULL,
			deleted_at TIMESTAMP DEFAULT NULL
		)`
)

// Index creation SQL templates.
const (
	sqlCreateSessionStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, session_id)
		WHERE deleted_at IS NULL`

	sqlCreateSessionStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`

	sqlCreateSessionEventsIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(
			app_name, user_id, session_id, created_at
		)`

	sqlCreateSessionEventsExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`

	sqlCreateSessionTracksIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(
			app_name, user_id, session_id,
			track, created_at
		)`

	sqlCreateSessionTracksExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`

	sqlCreateSessionSummariesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(
			app_name, user_id, session_id, filter_key
		)
		WHERE deleted_at IS NULL`

	sqlCreateSessionSummariesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`

	sqlCreateAppStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, key)
		WHERE deleted_at IS NULL`

	sqlCreateAppStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`

	sqlCreateUserStatesUniqueIndex = `
		CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(app_name, user_id, key)
		WHERE deleted_at IS NULL`

	sqlCreateUserStatesExpiresIndex = `
		CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
		ON {{TABLE_NAME}}(expires_at)
		WHERE expires_at IS NOT NULL`
)

// tableDefinition defines a table with its SQL template.
type tableDefinition struct {
	name     string
	template string
}

// indexDefinition defines an index with its table, suffix
// and SQL template.
type indexDefinition struct {
	table    string
	suffix   string
	template string
}

// tableDefs lists all tables to create.
var tableDefs = []tableDefinition{
	{sqldb.TableNameSessionStates,
		sqlCreateSessionStatesTable},
	{sqldb.TableNameSessionEvents,
		sqlCreateSessionEventsTable},
	{sqldb.TableNameSessionTrackEvents,
		sqlCreateSessionTrackEventsTable},
	{sqldb.TableNameSessionSummaries,
		sqlCreateSessionSummariesTable},
	{sqldb.TableNameAppStates,
		sqlCreateAppStatesTable},
	{sqldb.TableNameUserStates,
		sqlCreateUserStatesTable},
}

// indexDefs lists all indexes to create.
var indexDefs = []indexDefinition{
	{sqldb.TableNameSessionStates,
		sqldb.IndexSuffixUniqueActive,
		sqlCreateSessionStatesUniqueIndex},
	{sqldb.TableNameSessionSummaries,
		sqldb.IndexSuffixUniqueActive,
		sqlCreateSessionSummariesUniqueIndex},
	{sqldb.TableNameAppStates,
		sqldb.IndexSuffixUniqueActive,
		sqlCreateAppStatesUniqueIndex},
	{sqldb.TableNameUserStates,
		sqldb.IndexSuffixUniqueActive,
		sqlCreateUserStatesUniqueIndex},
	{sqldb.TableNameSessionEvents,
		sqldb.IndexSuffixLookup,
		sqlCreateSessionEventsIndex},
	{sqldb.TableNameSessionTrackEvents,
		sqldb.IndexSuffixLookup,
		sqlCreateSessionTracksIndex},
	{sqldb.TableNameSessionStates,
		sqldb.IndexSuffixExpires,
		sqlCreateSessionStatesExpiresIndex},
	{sqldb.TableNameSessionEvents,
		sqldb.IndexSuffixExpires,
		sqlCreateSessionEventsExpiresIndex},
	{sqldb.TableNameSessionTrackEvents,
		sqldb.IndexSuffixExpires,
		sqlCreateSessionTracksExpiresIndex},
	{sqldb.TableNameSessionSummaries,
		sqldb.IndexSuffixExpires,
		sqlCreateSessionSummariesExpiresIndex},
	{sqldb.TableNameAppStates,
		sqldb.IndexSuffixExpires,
		sqlCreateAppStatesExpiresIndex},
	{sqldb.TableNameUserStates,
		sqldb.IndexSuffixExpires,
		sqlCreateUserStatesExpiresIndex},
}

// buildCreateTableSQL builds CREATE TABLE SQL with
// table prefix.
func buildCreateTableSQL(
	schema, prefix, tableName, template string,
) string {
	fullName := sqldb.BuildTableNameWithSchema(
		schema, prefix, tableName,
	)
	return strings.ReplaceAll(
		template, "{{TABLE_NAME}}", fullName,
	)
}

// buildCreateIndexSQL builds CREATE INDEX SQL with
// table and index names.
func buildCreateIndexSQL(
	schema, prefix, tableName, suffix, template string,
) string {
	fullName := sqldb.BuildTableNameWithSchema(
		schema, prefix, tableName,
	)
	indexName := sqldb.BuildIndexNameWithSchema(
		schema, prefix, tableName, suffix,
	)
	sql := strings.ReplaceAll(
		template, "{{TABLE_NAME}}", fullName,
	)
	return strings.ReplaceAll(
		sql, "{{INDEX_NAME}}", indexName,
	)
}
