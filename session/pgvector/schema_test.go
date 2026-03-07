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
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- Tests for buildCreateTableSQL ---

func TestBuildCreateTableSQL(t *testing.T) {
	sql := buildCreateTableSQL(
		"", "", "test_table",
		"CREATE TABLE IF NOT EXISTS "+
			"{{TABLE_NAME}} (id INT)",
	)
	assert.Contains(t, sql, "test_table")
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
}

func TestBuildCreateTableSQL_WithSchemaPrefix(
	t *testing.T,
) {
	sql := buildCreateTableSQL(
		"myschema", "pfx_", "test_table",
		"CREATE TABLE IF NOT EXISTS "+
			"{{TABLE_NAME}} (id INT)",
	)
	assert.Contains(t, sql, "myschema")
	assert.Contains(t, sql, "pfx_")
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
}

// --- Tests for buildCreateIndexSQL ---

func TestBuildCreateIndexSQL(t *testing.T) {
	sql := buildCreateIndexSQL(
		"", "", "test_table", "idx_suffix",
		"CREATE INDEX IF NOT EXISTS {{INDEX_NAME}} "+
			"ON {{TABLE_NAME}}(col)",
	)
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
	assert.NotContains(t, sql, "{{INDEX_NAME}}")
	assert.Contains(t, sql, "test_table")
}

func TestBuildCreateIndexSQL_WithSchemaPrefix(
	t *testing.T,
) {
	sql := buildCreateIndexSQL(
		"s", "p_", "tbl", "suffix",
		"CREATE INDEX IF NOT EXISTS {{INDEX_NAME}} "+
			"ON {{TABLE_NAME}}(col)",
	)
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
	assert.NotContains(t, sql, "{{INDEX_NAME}}")
}

// --- Tests for tableDefs and indexDefs ---

func TestTableDefs_Count(t *testing.T) {
	// Should have 6 tables.
	const expectedTableCount = 6
	assert.Len(t, tableDefs, expectedTableCount)
}

func TestIndexDefs_Count(t *testing.T) {
	// Should have 12 indexes.
	const expectedIndexCount = 12
	assert.Len(t, indexDefs, expectedIndexCount)
}

func TestTableDefs_NamesNotEmpty(t *testing.T) {
	for _, td := range tableDefs {
		assert.NotEmpty(t, td.name)
		assert.NotEmpty(t, td.template)
		assert.Contains(t, td.template,
			"{{TABLE_NAME}}")
	}
}

func TestIndexDefs_FieldsNotEmpty(t *testing.T) {
	for _, id := range indexDefs {
		assert.NotEmpty(t, id.table)
		assert.NotEmpty(t, id.suffix)
		assert.NotEmpty(t, id.template)
	}
}
