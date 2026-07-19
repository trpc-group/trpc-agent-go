//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Migration support for the SQLite store.
//
// Borrowed from competitor PR #2243, which adds a schema_migrations
// table so the store can track which schema versions have been applied
// and apply pending migrations idempotently. This keeps the store
// forward-compatible: future schema changes can be added as new
// migration entries without touching Init.
//
// The current implementation has a single migration (v1) that records
// the initial schema. Init applies the schema via schema.sql and then
// records v1 in schema_migrations. Migrate is idempotent: calling it
// on an up-to-date database is a no-op.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CurrentSchemaVersion is the schema version applied by Init. Migrate
// applies every migration up to and including this version. Bump this
// constant when adding a new migration entry to migrations().
const CurrentSchemaVersion = "v1"

// Migration is a single schema change. Version is the unique identifier
// (e.g. "v1"); SQL is the idempotent SQL to apply (typically
// "CREATE TABLE IF NOT EXISTS ...").
type Migration struct {
	Version string
	SQL     string
}

// migrations returns the ordered list of schema migrations. Each entry
// is applied inside its own transaction by Migrate. The list is
// append-only: new migrations are added at the end with an incremented
// version. Editing or removing an existing migration breaks
// idempotency and must not be done.
//
// The v1 migration is empty: the initial schema is applied by Init via
// the embedded schema.sql (which includes CREATE TABLE IF NOT EXISTS
// for every table, including schema_migrations itself). The v1 row is
// still recorded in schema_migrations so the version is queryable and
// future migrations can build on it.
func migrations() []Migration {
	return []Migration{
		{Version: "v1", SQL: ""}, // initial schema applied via schema.sql
	}
}

// Migrate applies every pending migration up to CurrentSchemaVersion.
// It is safe to call multiple times: migrations already recorded in
// schema_migrations are skipped. Each migration runs in its own
// transaction so a failure leaves the database at the last successful
// version rather than half-migrated.
//
// Migrate returns the number of migrations applied (0 if the database
// was already up to date) and an error if any migration failed.
func (s *sqliteStore) Migrate(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, errors.New("store: not initialised")
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range migrations() {
		if applied[m.Version] {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return count, fmt.Errorf("store: migrate %s: %w", m.Version, err)
		}
		count++
	}
	return count, nil
}

// SchemaVersion returns the highest migration version recorded in
// schema_migrations, or "" if no migration has been applied yet (e.g.
// on a fresh database that has not been migrated). Callers can compare
// the result with CurrentSchemaVersion to decide whether Migrate
// needs to run.
func (s *sqliteStore) SchemaVersion(ctx context.Context) (string, error) {
	if s.db == nil {
		return "", errors.New("store: not initialised")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;`)
	var v string
	err := row.Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: query schema version: %w", err)
	}
	return v, nil
}

// appliedVersions returns the set of migration versions recorded in
// schema_migrations. A fresh database (no migrations applied) returns
// an empty non-nil map.
func (s *sqliteStore) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, fmt.Errorf("store: query schema_migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate schema_migrations: %w", err)
	}
	return out, nil
}

// applyMigration runs a single migration inside its own transaction.
// The migration's SQL is executed first; if it succeeds, the version
// is recorded in schema_migrations with the current UTC timestamp.
// Empty SQL (e.g. the v1 placeholder) is allowed — only the version
// row is recorded.
func (s *sqliteStore) applyMigration(ctx context.Context, m Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if m.SQL != "" {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("exec migration sql: %w", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?);`,
		m.Version, now); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	committed = true
	return nil
}
