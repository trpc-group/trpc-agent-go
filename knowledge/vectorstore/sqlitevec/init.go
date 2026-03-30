//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlitevec

import (
	"context"
	"fmt"
	"strings"
)

const (
	sqlCheckVecVersion = `SELECT vec_version();`
)

// initDB checks sqlite-vec availability and creates the schema.
func (s *Store) initDB(ctx context.Context) error {
	// Check that sqlite-vec extension is available.
	if _, err := s.db.ExecContext(ctx, sqlCheckVecVersion); err != nil {
		return fmt.Errorf("sqlite-vec is not available: %w", err)
	}

	// Create the main vec0 table.
	vecSQL := s.buildCreateVecTableSQL()
	if _, err := s.db.ExecContext(ctx, vecSQL); err != nil {
		return fmt.Errorf("create vec0 table %s: %w", s.opts.tableName, err)
	}

	// Create the metadata index table and its indices.
	metaStatements := s.buildCreateMetadataTableSQL()
	for _, stmt := range metaStatements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create metadata table %s: %w", s.opts.metadataTableName, err)
		}
	}

	return nil
}

// buildCreateVecTableSQL returns the CREATE VIRTUAL TABLE statement for the
// vec0 main table.
func (s *Store) buildCreateVecTableSQL() string {
	sql := `CREATE VIRTUAL TABLE IF NOT EXISTS {{VEC_TABLE_NAME}} USING vec0(
  id text primary key,
  embedding float[{{DIMENSION}}] distance_metric=cosine,
  created_at integer,
  updated_at integer,
  +name text,
  +content text,
  +metadata text
);`
	sql = strings.ReplaceAll(sql, "{{VEC_TABLE_NAME}}", s.opts.tableName)
	sql = strings.ReplaceAll(sql, "{{DIMENSION}}", fmt.Sprintf("%d", s.opts.indexDimension))
	return sql
}

// buildCreateMetadataTableSQL returns the SQL statements for creating the
// metadata index table and its indices.
func (s *Store) buildCreateMetadataTableSQL() []string {
	metaTable := s.opts.metadataTableName

	createTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  doc_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value_ordinal INTEGER NOT NULL DEFAULT 0,
  value_type TEXT NOT NULL,
  value_text TEXT,
  value_num REAL,
  value_bool INTEGER,
  value_json TEXT,
  PRIMARY KEY (doc_id, key, value_ordinal)
);`, metaTable)

	idxKeyText := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s__key__value_text ON %s(key, value_text);`,
		metaTable, metaTable,
	)
	idxKeyNum := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s__key__value_num ON %s(key, value_num);`,
		metaTable, metaTable,
	)
	idxKeyBool := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s__key__value_bool ON %s(key, value_bool);`,
		metaTable, metaTable,
	)
	idxDocID := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s__doc_id ON %s(doc_id);`,
		metaTable, metaTable,
	)
	idxDocIDKey := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s__doc_id__key ON %s(doc_id, key);`,
		metaTable, metaTable,
	)

	return []string{createTable, idxKeyText, idxKeyNum, idxKeyBool, idxDocID, idxDocIDKey}
}
