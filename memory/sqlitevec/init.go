//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlitevec

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const (
	defaultTableName = "memories"
	schemaBackupName = "__schema_backup"

	sqlCheckVecVersion = `SELECT vec_version();`

	sqlCreateMemoriesTable = `
CREATE VIRTUAL TABLE IF NOT EXISTS {{TABLE_NAME}} USING vec0(
  memory_id text primary key,
  embedding float[{{DIMENSION}}] distance_metric=cosine,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  +memory_content text,
  +topics text,
  +memory_kind text,
  +event_time integer,
  +participants text,
  +location text
);`

	sqlCreateSchemaBackupTable = `
CREATE TABLE %s (
  memory_id text,
  embedding blob,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  memory_content text,
  topics text,
  memory_kind text,
  event_time integer,
  participants text,
  location text
);`
)

var requiredSchemaColumns = []string{
	"memory_id",
	"embedding",
	"app_name",
	"user_id",
	"created_at",
	"updated_at",
	"deleted_at",
	"memory_content",
	"topics",
	"memory_kind",
	"event_time",
	"participants",
	"location",
}

var legacySchemaColumns = []string{
	"memory_id",
	"embedding",
	"app_name",
	"user_id",
	"created_at",
	"updated_at",
	"deleted_at",
	"memory_content",
	"topics",
}

func (s *Service) initDB(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, sqlCheckVecVersion); err != nil {
		return fmt.Errorf("sqlite-vec is not available: %w", err)
	}

	if err := s.recoverSchemaBackup(ctx); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(
		ctx,
		s.createMemoriesTableSQL(),
	); err != nil {
		return fmt.Errorf("create table %s: %w", s.tableName, err)
	}

	missing, found, err := s.schemaMissingColumns(ctx, s.tableName)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	if err := s.migrateLegacySchema(ctx, found); err != nil {
		return err
	}

	return s.ensureSchemaColumns(ctx)
}

func (s *Service) ensureSchemaColumns(ctx context.Context) error {
	missing, _, err := s.schemaMissingColumns(ctx, s.tableName)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	return s.outdatedSchemaError(missing)
}

func (s *Service) recoverSchemaBackup(ctx context.Context) error {
	backupTable := s.schemaBackupTableName()
	backupExists, err := s.tableExists(ctx, backupTable)
	if err != nil || !backupExists {
		return err
	}

	tableExists, err := s.tableExists(ctx, s.tableName)
	if err != nil {
		return err
	}
	if tableExists {
		if err := s.dropTable(ctx, s.tableName); err != nil {
			return fmt.Errorf("drop table %s: %w", s.tableName, err)
		}
	}

	if _, err := s.db.ExecContext(
		ctx,
		s.createMemoriesTableSQL(),
	); err != nil {
		return fmt.Errorf("recreate table %s: %w", s.tableName, err)
	}
	if err := s.restoreSchemaBackup(ctx, backupTable); err != nil {
		return err
	}
	return s.dropTable(ctx, backupTable)
}

func (s *Service) migrateLegacySchema(
	ctx context.Context,
	found map[string]struct{},
) error {
	if !hasSchemaColumns(found, legacySchemaColumns) {
		missing := missingColumns(found, requiredSchemaColumns)
		return s.outdatedSchemaError(missing)
	}

	backupTable := s.schemaBackupTableName()
	if err := s.createSchemaBackup(ctx, backupTable, found); err != nil {
		return err
	}
	if err := s.dropTable(ctx, s.tableName); err != nil {
		return fmt.Errorf("drop table %s: %w", s.tableName, err)
	}
	if _, err := s.db.ExecContext(
		ctx,
		s.createMemoriesTableSQL(),
	); err != nil {
		return fmt.Errorf("recreate table %s: %w", s.tableName, err)
	}
	if err := s.restoreSchemaBackup(ctx, backupTable); err != nil {
		return err
	}
	return s.dropTable(ctx, backupTable)
}

func (s *Service) createSchemaBackup(
	ctx context.Context,
	backupTable string,
	found map[string]struct{},
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema backup %s: %w", backupTable, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(sqlCreateSchemaBackupTable, backupTable),
	); err != nil {
		return fmt.Errorf("create schema backup %s: %w", backupTable, err)
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind,
event_time, participants, location
)
SELECT
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, %s, %s, %s, %s
FROM %s`,
		backupTable,
		optionalColumnExpr(found, "memory_kind"),
		optionalColumnExpr(found, "event_time"),
		optionalColumnExpr(found, "participants"),
		optionalColumnExpr(found, "location"),
		s.tableName,
	)
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("copy legacy rows to %s: %w", backupTable, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema backup %s: %w", backupTable, err)
	}
	return nil
}

func (s *Service) restoreSchemaBackup(
	ctx context.Context,
	backupTable string,
) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (
memory_id, embedding, app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind,
event_time, participants, location
)
SELECT
memory_id, vec_f32(embedding), app_name, user_id,
created_at, updated_at, deleted_at,
memory_content, topics, memory_kind,
event_time, participants, location
FROM %s`,
		s.tableName,
		backupTable,
	)
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("restore rows from %s: %w", backupTable, err)
	}
	return nil
}

func (s *Service) tableExists(
	ctx context.Context,
	tableName string,
) (bool, error) {
	const query = `SELECT COUNT(*) FROM sqlite_master
WHERE type IN ('table', 'view') AND name = ?`

	var count int
	if err := s.db.QueryRowContext(ctx, query, tableName).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect table %s: %w", tableName, err)
	}
	return count > 0, nil
}

func (s *Service) schemaMissingColumns(
	ctx context.Context,
	tableName string,
) ([]string, map[string]struct{}, error) {
	const pragma = `PRAGMA table_info(%s);`
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(pragma, tableName))
	if err != nil {
		return nil, nil, fmt.Errorf(
			"inspect table %s schema: %w",
			tableName,
			err,
		)
	}
	defer rows.Close()

	found := make(map[string]struct{}, len(requiredSchemaColumns))

	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(
			&cid,
			&name,
			&typ,
			&notNull,
			&dfltValue,
			&pk,
		); err != nil {
			return nil, nil, fmt.Errorf(
				"scan table %s schema: %w",
				tableName,
				err,
			)
		}
		found[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf(
			"iterate table %s schema: %w",
			tableName,
			err,
		)
	}
	return missingColumns(found, requiredSchemaColumns), found, nil
}

func (s *Service) createMemoriesTableSQL() string {
	tableSQL := strings.ReplaceAll(
		sqlCreateMemoriesTable,
		"{{TABLE_NAME}}",
		s.tableName,
	)
	return strings.ReplaceAll(
		tableSQL,
		"{{DIMENSION}}",
		fmt.Sprintf("%d", s.opts.indexDimension),
	)
}

func (s *Service) schemaBackupTableName() string {
	return s.tableName + schemaBackupName
}

func (s *Service) outdatedSchemaError(
	missing []string,
) error {
	slices.Sort(missing)
	return fmt.Errorf(
		"sqlitevec table %s has outdated schema; recreate it to add "+
			"columns: %s",
		s.tableName,
		strings.Join(missing, ", "),
	)
}

func (s *Service) dropTable(
	ctx context.Context,
	tableName string,
) error {
	if _, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName),
	); err != nil {
		return fmt.Errorf("drop table %s: %w", tableName, err)
	}
	return nil
}

func hasSchemaColumns(
	found map[string]struct{},
	columns []string,
) bool {
	for _, column := range columns {
		if _, ok := found[column]; !ok {
			return false
		}
	}
	return true
}

func missingColumns(
	found map[string]struct{},
	required []string,
) []string {
	missing := make([]string, 0)
	for _, column := range required {
		if _, ok := found[column]; !ok {
			missing = append(missing, column)
		}
	}
	slices.Sort(missing)
	return missing
}

func optionalColumnExpr(
	found map[string]struct{},
	column string,
) string {
	if _, ok := found[column]; ok {
		return column
	}
	return "NULL"
}
