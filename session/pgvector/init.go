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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/log"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// initDB initializes the database schema including pgvector
// extension, tables, indexes, and HNSW vector index.
func (s *Service) initDB(ctx context.Context) {
	if err := enablePgvectorExtension(
		ctx, s.pgClient,
	); err != nil {
		panic(fmt.Sprintf(
			"enable pgvector extension failed: %v", err,
		))
	}
	if err := createTables(
		ctx, s.pgClient, s.opts.schema, s.opts.tablePrefix,
	); err != nil {
		panic(fmt.Sprintf(
			"create tables failed: %v", err,
		))
	}
	if err := createIndexes(
		ctx, s.pgClient,
		s.opts.schema, s.opts.tablePrefix,
	); err != nil {
		panic(fmt.Sprintf(
			"create indexes failed: %v", err,
		))
	}
	if err := s.addVectorColumns(ctx); err != nil {
		panic(fmt.Sprintf(
			"add vector columns failed: %v", err,
		))
	}
	if err := s.createHNSWIndex(ctx); err != nil {
		// HNSW index creation may fail if pgvector is not
		// installed; log a warning instead of panic.
		log.WarnfContext(ctx,
			"pgvector session: create HNSW index "+
				"failed (non-fatal): %v", err,
		)
	}
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

// addVectorColumns adds content_text, role, and embedding
// columns to session_events if they do not already exist.
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
