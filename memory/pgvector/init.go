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
	"context"
	"database/sql"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// SQL templates for table creation (PostgreSQL with pgvector syntax).
const (
	sqlCreateExtension    = "CREATE EXTENSION IF NOT EXISTS vector"
	sqlCheckDDLPrivilege  = "SELECT has_schema_privilege($1, 'CREATE')"
	sqlCreateTablePattern = "CREATE TABLE IF NOT EXISTS %s (" +
		"memory_id TEXT PRIMARY KEY," +
		"app_name TEXT NOT NULL," +
		"user_id TEXT NOT NULL," +
		"memory_content TEXT NOT NULL," +
		"topics TEXT[]," +
		"embedding vector(%d)," +
		"memory_kind TEXT NOT NULL DEFAULT 'fact'," +
		"event_time TIMESTAMP NULL," +
		"participants TEXT[]," +
		"location TEXT NULL," +
		"created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"deleted_at TIMESTAMP NULL DEFAULT NULL" +
		")"

	sqlCreateAppUserIndexPattern      = "CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id)"
	sqlCreateUpdatedAtIndexPattern    = "CREATE INDEX IF NOT EXISTS %s ON %s(updated_at DESC)"
	sqlCreateDeletedAtIndexPattern    = "CREATE INDEX IF NOT EXISTS %s ON %s(deleted_at)"
	sqlCreateEventTimeIndexPattern    = "CREATE INDEX IF NOT EXISTS %s ON %s(event_time DESC) WHERE event_time IS NOT NULL"
	sqlCreateKindIndexPattern         = "CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, memory_kind)"
	sqlCreateParticipantsIndexPattern = "CREATE INDEX IF NOT EXISTS %s ON %s USING gin(participants) WHERE participants IS NOT NULL"

	sqlAddSearchVectorColumn         = "ALTER TABLE %s ADD COLUMN IF NOT EXISTS search_vector tsvector"
	sqlCreateSearchVectorIndex       = "CREATE INDEX IF NOT EXISTS %s ON %s USING gin(search_vector)"
	sqlBackfillSearchVector          = "UPDATE %s SET search_vector = to_tsvector('english', coalesce(memory_content, '')) WHERE search_vector IS NULL"
	sqlCreateSearchVectorTriggerFunc = `CREATE OR REPLACE FUNCTION %s_search_vector_update() RETURNS trigger AS $$
BEGIN
  NEW.search_vector := to_tsvector('english', coalesce(NEW.memory_content, ''));
  RETURN NEW;
END
$$ LANGUAGE plpgsql`
	sqlAttachSearchVectorTrigger = "DROP TRIGGER IF EXISTS tsvector_update ON %s; " +
		"CREATE TRIGGER tsvector_update BEFORE INSERT OR UPDATE ON %s " +
		"FOR EACH ROW EXECUTE FUNCTION %s_search_vector_update()"

	sqlCreateHNSWIndexPattern = "CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw " +
		"(embedding vector_cosine_ops) WITH (m = %d, ef_construction = %d)"
)

// buildFullTableName builds the full table name with optional schema prefix.
func buildFullTableName(schema, tableName string) string {
	return sqldb.BuildTableNameWithSchema(schema, "", tableName)
}

// buildIndexName builds the index name from table name and suffix.
func buildIndexName(tableName, suffix string) string {
	return sqldb.BuildIndexName("", tableName, suffix)
}

// buildCreateTableSQL builds the CREATE TABLE SQL.
func buildCreateTableSQL(schema, tableName string, dimension int) string {
	fullTableName := buildFullTableName(schema, tableName)
	return fmt.Sprintf(sqlCreateTablePattern, fullTableName, dimension)
}

// buildCreateIndexSQL builds the CREATE INDEX SQL.
func buildCreateIndexSQL(schema, tableName, indexSuffix, template string) string {
	fullTableName := buildFullTableName(schema, tableName)
	indexName := buildIndexName(tableName, indexSuffix)
	return fmt.Sprintf(template, indexName, fullTableName)
}

// buildCreateHNSWIndexSQL builds the CREATE HNSW INDEX SQL.
func buildCreateHNSWIndexSQL(schema, tableName string, params *HNSWIndexParams) string {
	fullTableName := buildFullTableName(schema, tableName)
	indexName := buildIndexName(tableName, "embedding_hnsw")

	m := defaultHNSWM
	efConstruction := defaultHNSWEfConstruction
	if params != nil {
		if params.M > 0 {
			m = params.M
		}
		if params.EfConstruction > 0 {
			efConstruction = params.EfConstruction
		}
	}

	return fmt.Sprintf(sqlCreateHNSWIndexPattern, indexName, fullTableName, m,
		efConstruction)
}

// checkDDLPrivilege checks if the current user has DDL (CREATE) privilege.
func (s *Service) checkDDLPrivilege(ctx context.Context) (bool, error) {
	schemaName := s.opts.schema
	if schemaName == "" {
		schemaName = "public"
	}

	var hasPrivilege bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&hasPrivilege)
		}
		// If no rows returned, hasPrivilege remains false (default).
		return nil
	}, sqlCheckDDLPrivilege, schemaName)

	if err != nil {
		return false, fmt.Errorf("check DDL privilege on schema %s failed: %w",
			schemaName, err)
	}
	return hasPrivilege, nil
}

// initDB initializes the database schema.
func (s *Service) initDB(ctx context.Context) error {
	log.InfoContext(ctx, "initializing pgvector memory database schema...")

	// Enable pgvector extension.
	if _, err := s.db.ExecContext(ctx, sqlCreateExtension); err != nil {
		return fmt.Errorf("enable pgvector extension failed: %w", err)
	}

	// Check DDL privilege before proceeding.
	hasDDLPrivilege, err := s.checkDDLPrivilege(ctx)
	if err != nil {
		return err
	}
	if !hasDDLPrivilege {
		log.WarnContext(ctx, "skipping DDL operations: no CREATE privilege on schema")
		return nil
	}

	// Use base table name from opts (before schema prefix is applied).
	baseTableName := s.opts.tableName
	fullTableName := buildFullTableName(s.opts.schema, baseTableName)

	// Create table.
	tableSQL := buildCreateTableSQL(s.opts.schema, baseTableName, s.opts.indexDimension)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s failed: %w", fullTableName, err)
	}
	log.InfofContext(ctx, "created table: %s", fullTableName)

	// Migrate existing tables: add episodic columns if they don't exist.
	// This is safe to run on both new and existing tables because
	// ADD COLUMN IF NOT EXISTS is a no-op when the column already exists.
	episodicColumns := []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS memory_kind TEXT NOT NULL DEFAULT 'fact'", fullTableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS event_time TIMESTAMP NULL", fullTableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS participants TEXT[]", fullTableName),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS location TEXT NULL", fullTableName),
	}
	for _, ddl := range episodicColumns {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("add episodic column on table %s failed: %w", fullTableName, err)
		}
	}

	// Index suffix constants.
	const (
		indexSuffixAppUser      = "app_user"
		indexSuffixUpdatedAt    = "updated_at"
		indexSuffixDeletedAt    = "deleted_at"
		indexSuffixEventTime    = "event_time"
		indexSuffixKind         = "kind"
		indexSuffixParticipants = "participants"
		indexSuffixSearchVector = "search_vector"
	)

	// Create regular indexes.
	indexes := []struct {
		suffix   string
		template string
	}{
		{indexSuffixAppUser, sqlCreateAppUserIndexPattern},
		{indexSuffixUpdatedAt, sqlCreateUpdatedAtIndexPattern},
		{indexSuffixDeletedAt, sqlCreateDeletedAtIndexPattern},
		{indexSuffixEventTime, sqlCreateEventTimeIndexPattern},
		{indexSuffixKind, sqlCreateKindIndexPattern},
		{indexSuffixParticipants, sqlCreateParticipantsIndexPattern},
	}

	for _, idx := range indexes {
		indexSQL := buildCreateIndexSQL(s.opts.schema, baseTableName, idx.suffix,
			idx.template)
		if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index %s on table %s failed: %w",
				idx.suffix, fullTableName, err)
		}
		log.InfofContext(ctx, "created index: %s on table %s", idx.suffix,
			fullTableName)
	}

	// Create HNSW vector index.
	hnswSQL := buildCreateHNSWIndexSQL(s.opts.schema, baseTableName, s.opts.hnswParams)
	if _, err := s.db.ExecContext(ctx, hnswSQL); err != nil {
		return fmt.Errorf("create HNSW index on table %s failed: %w", fullTableName,
			err)
	}
	log.InfofContext(ctx, "created HNSW index on table %s", fullTableName)

	// Add search_vector column for full-text search (hybrid search support).
	addTSVCol := fmt.Sprintf(sqlAddSearchVectorColumn, fullTableName)
	if _, err := s.db.ExecContext(ctx, addTSVCol); err != nil {
		return fmt.Errorf("add search_vector column on table %s failed: %w",
			fullTableName, err)
	}

	// Create trigger function to auto-populate search_vector on insert/update.
	triggerFuncSQL := fmt.Sprintf(sqlCreateSearchVectorTriggerFunc, baseTableName)
	if _, err := s.db.ExecContext(ctx, triggerFuncSQL); err != nil {
		return fmt.Errorf("create tsvector trigger function for %s failed: %w",
			fullTableName, err)
	}

	// Attach trigger to table.
	triggerSQL := fmt.Sprintf(sqlAttachSearchVectorTrigger,
		fullTableName, fullTableName, baseTableName)
	if _, err := s.db.ExecContext(ctx, triggerSQL); err != nil {
		return fmt.Errorf("attach tsvector trigger on %s failed: %w",
			fullTableName, err)
	}

	// Create GIN index on search_vector for fast full-text search.
	tsvIndexSQL := buildCreateIndexSQL(s.opts.schema, baseTableName,
		indexSuffixSearchVector, sqlCreateSearchVectorIndex)
	if _, err := s.db.ExecContext(ctx, tsvIndexSQL); err != nil {
		return fmt.Errorf("create search_vector GIN index on %s failed: %w",
			fullTableName, err)
	}
	log.InfofContext(ctx, "created search_vector GIN index on table %s", fullTableName)

	// Backfill search_vector for existing rows that lack it.
	backfillSQL := fmt.Sprintf(sqlBackfillSearchVector, fullTableName)
	if _, bfErr := s.db.ExecContext(ctx, backfillSQL); bfErr != nil {
		log.WarnfContext(ctx, "backfill search_vector on %s (non-fatal): %v",
			fullTableName, bfErr)
	}

	log.InfoContext(ctx, "pgvector memory database schema initialized successfully")
	return nil
}
