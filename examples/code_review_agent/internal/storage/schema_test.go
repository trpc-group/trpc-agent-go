//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package storage

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaInit_CreatesTables(t *testing.T) {
	f, err := os.CreateTemp("", "cr-schema-test-*.db")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	db, err := sql.Open("sqlite", f.Name())
	require.NoError(t, err)
	defer db.Close()

	// Execute schema.
	_, err = db.Exec(schemaSQL)
	require.NoError(t, err)

	// Verify all tables exist.
	var tables []string
	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}

	assert.Contains(t, tables, "cr_tasks")
	assert.Contains(t, tables, "cr_findings")
	assert.Contains(t, tables, "cr_sandbox_runs")
	assert.Contains(t, tables, "cr_permission_decisions")
	assert.Contains(t, tables, "cr_reports")
}

func TestSchemaInit_Idempotent(t *testing.T) {
	f, err := os.CreateTemp("", "cr-schema-idem-*.db")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	db, err := sql.Open("sqlite", f.Name())
	require.NoError(t, err)
	defer db.Close()

	// Run schema twice.
	_, err = db.Exec(schemaSQL)
	require.NoError(t, err)
	_, err = db.Exec(schemaSQL)
	require.NoError(t, err, "schema should be idempotent")
}

func TestSchemaInit_CreatesIndexes(t *testing.T) {
	f, err := os.CreateTemp("", "cr-schema-idx-*.db")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	db, err := sql.Open("sqlite", f.Name())
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(schemaSQL)
	require.NoError(t, err)

	var indexes []string
	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='index' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		if name != "sqlite_autoindex_cr_tasks_1" {
			indexes = append(indexes, name)
		}
	}

	assert.Contains(t, indexes, "idx_cr_findings_task")
	assert.Contains(t, indexes, "idx_cr_findings_dedup")
	assert.Contains(t, indexes, "idx_cr_sandbox_runs_task")
	assert.Contains(t, indexes, "idx_cr_permission_decisions_task")
}
