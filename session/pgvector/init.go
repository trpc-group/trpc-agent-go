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
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// initDB initializes the database schema including pgvector
// extension, tables, indexes, and HNSW vector index.
func (s *Service) initDB(ctx context.Context) error {
	if err := enablePgvectorExtension(
		ctx, s.pgClient,
	); err != nil {
		return fmt.Errorf(
			"enable pgvector extension failed: %w", err,
		)
	}
	if err := createTables(
		ctx, s.pgClient, s.opts.schema, s.opts.tablePrefix,
	); err != nil {
		return fmt.Errorf(
			"create tables failed: %w", err,
		)
	}
	if err := createIndexes(
		ctx, s.pgClient,
		s.opts.schema, s.opts.tablePrefix,
	); err != nil {
		return fmt.Errorf(
			"create indexes failed: %w", err,
		)
	}
	if err := s.addVectorColumns(ctx); err != nil {
		return fmt.Errorf(
			"add vector columns failed: %w", err,
		)
	}
	if err := s.createTextSearchIndex(ctx); err != nil {
		return fmt.Errorf(
			"create text search index failed: %w", err,
		)
	}
	if err := s.createHNSWIndex(ctx); err != nil {
		// HNSW index creation may fail if pgvector is not
		// installed; log a warning instead of panic.
		log.WarnfContext(ctx,
			"pgvector session: create HNSW index "+
				"failed (non-fatal): %v", err,
		)
	}
	return nil
}

// enablePgvectorExtension enables the pgvector
// extension in PostgreSQL.
func enablePgvectorExtension(
	ctx context.Context,
	client storage.Client,
) error {
	_, err := client.ExecContext(ctx,
		"CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf(
			"enable pgvector extension: %w", err,
		)
	}
	return nil
}

// createTables creates all required session tables.
// Reuses the same table definitions as session/postgres.
func createTables(
	ctx context.Context,
	client storage.Client,
	schema, prefix string,
) error {
	for _, table := range tableDefs {
		tableSQL := buildCreateTableSQL(
			schema, prefix, table.name, table.template,
		)
		fullName := sqldb.BuildTableNameWithSchema(
			schema, prefix, table.name,
		)
		if _, err := client.ExecContext(
			ctx, tableSQL,
		); err != nil {
			return fmt.Errorf(
				"create table %s failed: %w",
				fullName, err,
			)
		}
	}
	return nil
}

// createIndexes creates all required table indexes.
func createIndexes(
	ctx context.Context,
	client storage.Client,
	schema, prefix string,
) error {
	for _, idx := range indexDefs {
		indexSQL := buildCreateIndexSQL(
			schema, prefix,
			idx.table, idx.suffix, idx.template,
		)
		fullName := sqldb.BuildTableNameWithSchema(
			schema, prefix, idx.table,
		)
		if _, err := client.ExecContext(
			ctx, indexSQL,
		); err != nil {
			return fmt.Errorf(
				"create index on %s failed: %w",
				fullName, err,
			)
		}
	}
	return nil
}

// addVectorColumns adds content_text, role, embedding, and
// search_vector columns to session_events if they do not
// already exist.
func (s *Service) addVectorColumns(
	ctx context.Context,
) error {
	alterStmts := []string{
		fmt.Sprintf(
			`ALTER TABLE %s `+
				`ADD COLUMN IF NOT EXISTS `+
				`content_text TEXT NOT NULL DEFAULT ''`,
			s.tableSessionEvents,
		),
		fmt.Sprintf(
			`ALTER TABLE %s `+
				`ADD COLUMN IF NOT EXISTS `+
				`role VARCHAR(32) NOT NULL DEFAULT ''`,
			s.tableSessionEvents,
		),
		fmt.Sprintf(
			`ALTER TABLE %s `+
				`ADD COLUMN IF NOT EXISTS `+
				`embedding vector(%d)`,
			s.tableSessionEvents,
			s.opts.indexDimension,
		),
		fmt.Sprintf(
			`ALTER TABLE %s `+
				`ADD COLUMN IF NOT EXISTS `+
				`search_vector tsvector GENERATED ALWAYS AS (`+
				`to_tsvector('english', content_text)`+
				`) STORED`,
			s.tableSessionEvents,
		),
	}
	for _, stmt := range alterStmts {
		if _, err := s.pgClient.ExecContext(
			ctx, stmt,
		); err != nil {
			return fmt.Errorf(
				"alter table failed: %w", err,
			)
		}
	}
	if err := s.validateEmbeddingColumnDimension(
		ctx,
	); err != nil {
		return err
	}
	return nil
}

func (s *Service) validateEmbeddingColumnDimension(
	ctx context.Context,
) error {
	tableName := sqldb.BuildTableName(
		s.opts.tablePrefix,
		sqldb.TableNameSessionEvents,
	)
	query := "SELECT format_type(a.atttypid, a.atttypmod) " +
		"FROM pg_catalog.pg_attribute a " +
		"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid " +
		"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace " +
		"WHERE c.relname = $1 " +
		"AND n.nspname = COALESCE(NULLIF($2, ''), current_schema()) " +
		"AND a.attname = 'embedding' " +
		"AND a.attnum > 0 " +
		"AND NOT a.attisdropped"
	var typeName string
	if err := s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			if !rows.Next() {
				return fmt.Errorf(
					"embedding column metadata not found",
				)
			}
			if err := rows.Scan(&typeName); err != nil {
				return fmt.Errorf(
					"scan embedding column metadata: %w",
					err,
				)
			}
			return nil
		},
		query,
		tableName,
		s.opts.schema,
	); err != nil {
		return fmt.Errorf(
			"query embedding column metadata: %w",
			err,
		)
	}
	dim, err := parseVectorColumnDimension(typeName)
	if err != nil {
		return fmt.Errorf(
			"parse embedding column type %q: %w",
			typeName,
			err,
		)
	}
	if dim != s.opts.indexDimension {
		return fmt.Errorf(
			"embedding column dimension mismatch: "+
				"existing=%d configured=%d",
			dim,
			s.opts.indexDimension,
		)
	}
	return nil
}

func parseVectorColumnDimension(typeName string) (int, error) {
	const (
		vectorTypePrefix = "vector("
		vectorTypeSuffix = ")"
	)
	trimmed := strings.TrimSpace(typeName)
	if !strings.HasPrefix(trimmed, vectorTypePrefix) ||
		!strings.HasSuffix(trimmed, vectorTypeSuffix) {
		return 0, fmt.Errorf(
			"unexpected vector column type: %q",
			trimmed,
		)
	}
	dimensionText := strings.TrimSuffix(
		strings.TrimPrefix(trimmed, vectorTypePrefix),
		vectorTypeSuffix,
	)
	dim, err := strconv.Atoi(dimensionText)
	if err != nil {
		return 0, fmt.Errorf(
			"parse vector dimension: %w",
			err,
		)
	}
	if dim <= 0 {
		return 0, fmt.Errorf(
			"invalid vector dimension: %d",
			dim,
		)
	}
	return dim, nil
}

// createTextSearchIndex creates a GIN index on the
// generated search_vector column for keyword search.
func (s *Service) createTextSearchIndex(
	ctx context.Context,
) error {
	indexName := sqldb.BuildIndexNameWithSchema(
		s.opts.schema, s.opts.tablePrefix,
		sqldb.TableNameSessionEvents, "search_vector_gin",
	)
	sql := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s `+
			`ON %s USING gin (search_vector)`,
		indexName,
		s.tableSessionEvents,
	)
	_, err := s.pgClient.ExecContext(ctx, sql)
	if err != nil {
		return fmt.Errorf(
			"create GIN index failed: %w", err,
		)
	}
	return nil
}

// createHNSWIndex creates an HNSW vector index on the
// embedding column for cosine similarity search.
func (s *Service) createHNSWIndex(
	ctx context.Context,
) error {
	indexName := sqldb.BuildIndexNameWithSchema(
		s.opts.schema, s.opts.tablePrefix,
		sqldb.TableNameSessionEvents, "embedding_hnsw",
	)
	sql := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s `+
			`ON %s USING hnsw (embedding vector_cosine_ops) `+
			`WITH (m = %d, ef_construction = %d)`,
		indexName,
		s.tableSessionEvents,
		s.opts.hnswM,
		s.opts.hnswEf,
	)
	_, err := s.pgClient.ExecContext(ctx, sql)
	if err != nil {
		return fmt.Errorf(
			"create HNSW index failed: %w", err,
		)
	}
	return nil
}
