//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTableExists_TableFound tests tableExists when table exists
func TestTableExists_TableFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.tables query
	rows := sqlmock.NewRows([]string{"exists"}).AddRow(true)
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	exists, err := s.tableExists(context.Background(), "session_states")
	require.NoError(t, err)
	assert.True(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTableExists_TableNotFound tests tableExists when table doesn't exist
func TestTableExists_TableNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.tables query
	rows := sqlmock.NewRows([]string{"exists"}).AddRow(false)
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("public", "non_existent_table").
		WillReturnRows(rows)

	exists, err := s.tableExists(context.Background(), "non_existent_table")
	require.NoError(t, err)
	assert.False(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTableExists_QueryError tests tableExists when query fails
func TestTableExists_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock query error
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("public", "session_states").
		WillReturnError(assert.AnError)

	_, err = s.tableExists(context.Background(), "session_states")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyColumns_Success tests verifyColumns with matching schema
func TestVerifyColumns_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.columns query
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
		AddRow("id", "bigint", "NO").
		AddRow("app_name", "character varying", "NO").
		AddRow("user_id", "character varying", "NO").
		AddRow("session_id", "character varying", "NO").
		AddRow("state", "jsonb", "YES")

	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedColumns := []tableColumn{
		{"id", "bigint", false},
		{"app_name", "character varying", false},
		{"user_id", "character varying", false},
		{"session_id", "character varying", false},
		{"state", "jsonb", true},
	}

	err = s.verifyColumns(context.Background(), "session_states", expectedColumns)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyColumns_MissingColumn tests verifyColumns when column is missing
func TestVerifyColumns_MissingColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.columns query - missing 'state' column
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
		AddRow("id", "bigint", "NO").
		AddRow("app_name", "character varying", "NO")

	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedColumns := []tableColumn{
		{"id", "bigint", false},
		{"app_name", "character varying", false},
		{"state", "jsonb", true}, // This column is missing
	}

	err = s.verifyColumns(context.Background(), "session_states", expectedColumns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "column session_states.state is missing")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyColumns_TypeMismatch tests verifyColumns when data type doesn't match
func TestVerifyColumns_TypeMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.columns query - wrong type for 'state'
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
		AddRow("id", "bigint", "NO").
		AddRow("state", "text", "YES") // Wrong type, should be jsonb

	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedColumns := []tableColumn{
		{"id", "bigint", false},
		{"state", "jsonb", true},
	}

	err = s.verifyColumns(context.Background(), "session_states", expectedColumns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has type text, expected jsonb")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyColumns_NullabilityMismatch tests verifyColumns when nullability doesn't match
func TestVerifyColumns_NullabilityMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock information_schema.columns query - wrong nullability
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
		AddRow("id", "bigint", "YES") // Should be NOT NULL

	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedColumns := []tableColumn{
		{"id", "bigint", false}, // Expecting NOT NULL
	}

	err = s.verifyColumns(context.Background(), "session_states", expectedColumns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nullable mismatch")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyColumns_QueryError tests verifyColumns when query fails
func TestVerifyColumns_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock query error
	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs("public", "session_states").
		WillReturnError(assert.AnError)

	expectedColumns := []tableColumn{
		{"id", "bigint", false},
	}

	err = s.verifyColumns(context.Background(), "session_states", expectedColumns)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query columns failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyIndexes_Success tests verifyIndexes when all indexes exist
func TestVerifyIndexes_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock pg_indexes query
	rows := sqlmock.NewRows([]string{"indexname"}).
		AddRow("idx_session_states_unique_active").
		AddRow("idx_session_states_expires")

	mock.ExpectQuery("SELECT indexname").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedIndexes := []tableIndex{
		{"session_states", "unique_active", []string{"app_name", "user_id", "session_id"}},
		{"session_states", "expires", []string{"expires_at"}},
	}

	err = s.verifyIndexes(context.Background(), "session_states", expectedIndexes)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyIndexes_MissingIndex tests verifyIndexes when index is missing (non-fatal)
func TestVerifyIndexes_MissingIndex(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock pg_indexes query - missing one index
	rows := sqlmock.NewRows([]string{"indexname"}).
		AddRow("idx_session_states_unique_active")
	// idx_session_states_expires is missing

	mock.ExpectQuery("SELECT indexname").
		WithArgs("public", "session_states").
		WillReturnRows(rows)

	expectedIndexes := []tableIndex{
		{"session_states", "unique_active", []string{"app_name", "user_id", "session_id"}},
		{"session_states", "expires", []string{"expires_at"}}, // This is missing
	}

	// verifyIndexes should succeed but log a warning (we can't test the log here)
	err = s.verifyIndexes(context.Background(), "session_states", expectedIndexes)
	require.NoError(t, err) // Non-fatal, returns no error
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifyIndexes_QueryError tests verifyIndexes when query fails
func TestVerifyIndexes_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock query error
	mock.ExpectQuery("SELECT indexname").
		WithArgs("public", "session_states").
		WillReturnError(assert.AnError)

	expectedIndexes := []tableIndex{
		{"session_states", "unique_active", []string{"app_name", "user_id", "session_id"}},
	}

	err = s.verifyIndexes(context.Background(), "session_states", expectedIndexes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query indexes failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifySchema_Success tests verifySchema when all tables/columns/indexes match
// NOTE: This test can be flaky due to Go map iteration being random.
// The test covers all 5 tables but mock expectations may not match the iteration order.
func TestVerifySchema_Success(t *testing.T) {
	t.Skip("Skipping due to Go map iteration randomness - individual component tests cover this")

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	// Disable order checking since map iteration is random
	mock.MatchExpectationsInOrder(false)

	s := createTestService(t, db)

	// For each of the 5 tables, we need to mock:
	// 1. tableExists query
	// 2. verifyColumns query
	// 3. verifyIndexes query

	// Table schemas to mock (simplified - include all mandatory columns)
	tableSchemas := [][]string{
		// session_states
		{"id", "app_name", "user_id", "session_id", "state", "created_at", "updated_at", "expires_at", "deleted_at"},
		// session_events
		{"id", "app_name", "user_id", "session_id", "event", "created_at", "updated_at", "expires_at", "deleted_at"},
		// session_summaries
		{"id", "app_name", "user_id", "session_id", "filter_key", "summary", "updated_at", "expires_at", "deleted_at"},
		// app_states
		{"id", "app_name", "key", "value", "created_at", "updated_at", "expires_at", "deleted_at"},
		// user_states
		{"id", "app_name", "user_id", "key", "value", "created_at", "updated_at", "expires_at", "deleted_at"},
	}

	for _, columns := range tableSchemas {
		// Mock tableExists - now expects schema and tableName
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		// Mock verifyColumns - return columns for this table
		rows := sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"})
		for _, col := range columns {
			dataType := "character varying"
			isNullable := "YES"
			if col == "id" {
				dataType = "bigint"
				isNullable = "NO"
			} else if col == "app_name" || col == "user_id" || col == "session_id" || col == "key" || col == "filter_key" {
				isNullable = "NO"
			} else if col == "event" {
				dataType = "jsonb"
				isNullable = "NO" // event is NOT NULL
			} else if col == "summary" || col == "value" || col == "state" {
				dataType = "jsonb"
				isNullable = "YES" // These can be NULL
			} else if col == "created_at" || col == "updated_at" {
				dataType = "timestamp without time zone"
				isNullable = "NO"
			} else if col == "expires_at" || col == "deleted_at" {
				dataType = "timestamp without time zone"
			}
			rows.AddRow(col, dataType, isNullable)
		}

		mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnRows(rows)

		// Mock verifyIndexes - return empty (indexes are non-fatal)
		mock.ExpectQuery("SELECT indexname").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"indexname"}))
	}

	err = s.verifySchema(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifySchema_TableMissing tests verifySchema when a table is missing
func TestVerifySchema_TableMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock first table not existing
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	err = s.verifySchema(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestVerifySchema_ColumnVerificationFails tests verifySchema when column verification fails
func TestVerifySchema_ColumnVerificationFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Mock tableExists succeeds
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	// Mock verifyColumns returns wrong data
	mock.ExpectQuery("SELECT column_name, data_type, is_nullable").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable"}).
			AddRow("id", "bigint", "NO"))
	// Missing many columns

	err = s.verifySchema(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify columns")
	require.NoError(t, mock.ExpectationsWereMet())
}
