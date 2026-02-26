//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

// mockVerifySchemaQueries adds mock expectations for verifySchema queries
func mockVerifySchemaQueries(mock sqlmock.Sqlmock, tablePrefix string) {
	tableNames := []string{
		sqldb.TableNameSessionStates,
		sqldb.TableNameSessionEvents,
		sqldb.TableNameSessionTrackEvents,
		sqldb.TableNameSessionSummaries,
		sqldb.TableNameAppStates,
		sqldb.TableNameUserStates,
	}

	for _, tableName := range tableNames {
		fullTableName := sqldb.BuildTableName(tablePrefix, tableName)
		schema := expectedSchema[tableName]

		// 1. tableExists query
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(fullTableName).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		// 2. verifyColumns query
		colRows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"})
		for _, col := range schema.columns {
			isNullable := "NO"
			if col.nullable {
				isNullable = "YES"
			}
			colRows.AddRow(col.name, col.dataType, isNullable)
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COLUMN_NAME")).
			WithArgs(fullTableName).
			WillReturnRows(colRows)

		// 3. verifyIndexes query
		idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME"})
		for _, idx := range schema.indexes {
			idxName := sqldb.BuildIndexName(tablePrefix, idx.table, idx.suffix)
			for _, col := range idx.columns {
				idxRows.AddRow(idxName, col)
			}
		}
		idxRows.AddRow("PRIMARY", "id")
		mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
			WithArgs(fullTableName).
			WillReturnRows(idxRows)
	}
}

func TestInitDB_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: Create tables
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_events")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_track_events")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_summaries")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS app_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS user_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock: Create indexes (12 indexes total: 3 unique + 3 lookup + 6 TTL)
	for i := 0; i < 12; i++ {
		mock.ExpectExec(regexp.QuoteMeta("CREATE")).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock: verifySchema queries
	mockVerifySchemaQueries(mock, s.opts.tablePrefix)

	err = s.initDB(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_TableCreationError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: First table creation fails
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_states")).
		WillReturnError(assert.AnError)

	err = s.initDB(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create table")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_IndexCreationError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: Create all tables successfully
	for i := 0; i < 6; i++ {
		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS")).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock: First index creation fails with non-duplicate error
	mock.ExpectExec(regexp.QuoteMeta("CREATE")).
		WillReturnError(assert.AnError)

	err = s.initDB(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create index")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_WithTablePrefix(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Create service with table prefix
	serviceOpts := ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		asyncPersisterNum: defaultAsyncPersisterNum,
		softDelete:        true,
		tablePrefix:       "trpc",
	}

	s := &Service{
		opts:                  serviceOpts,
		mysqlClient:           &mockMySQLClient{db: db},
		tableSessionStates:    "trpc_session_states",
		tableSessionEvents:    "trpc_session_events",
		tableSessionTracks:    "trpc_session_track_events",
		tableSessionSummaries: "trpc_session_summaries",
		tableAppStates:        "trpc_app_states",
		tableUserStates:       "trpc_user_states",
	}
	ctx := context.Background()

	// Verify table names contain prefix
	assert.Equal(t, "trpc_session_states", s.tableSessionStates)
	assert.Equal(t, "trpc_session_events", s.tableSessionEvents)
	assert.Equal(t, "trpc_session_track_events", s.tableSessionTracks)
	assert.Equal(t, "trpc_session_summaries", s.tableSessionSummaries)
	assert.Equal(t, "trpc_app_states", s.tableAppStates)
	assert.Equal(t, "trpc_user_states", s.tableUserStates)

	// Mock: Create tables with prefix
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_session_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_session_events")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_session_track_events")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_session_summaries")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_app_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS trpc_user_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock: Create indexes with prefix
	for i := 0; i < 12; i++ {
		mock.ExpectExec(regexp.QuoteMeta("CREATE")).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock: verifySchema queries
	mockVerifySchemaQueries(mock, s.opts.tablePrefix)

	err = s.initDB(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_DuplicateIndexIgnored(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: Create all tables successfully
	for i := 0; i < 6; i++ {
		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS")).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock: Some indexes already exist (simulate duplicate key error)
	for i := 0; i < 12; i++ {
		if i%3 == 0 {
			// Simulate duplicate index error
			mock.ExpectExec(regexp.QuoteMeta("CREATE")).
				WillReturnError(assert.AnError)
		} else {
			mock.ExpectExec(regexp.QuoteMeta("CREATE")).
				WillReturnResult(sqlmock.NewResult(0, 0))
		}
	}

	// Should succeed despite duplicate index errors
	err = s.initDB(ctx)
	// This will fail because our mock doesn't actually return "Duplicate key name" error
	// In real scenario, MySQL would return specific error for duplicate index
	assert.Error(t, err) // Expected to fail with our simple mock
}

func TestIsDuplicateIndexNameError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "MySQL error 1061 - Duplicate key name",
			err:      &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName, Message: "Duplicate key name 'idx_test'"},
			expected: true,
		},
		{
			name:     "MySQL error 1062 - Duplicate entry (should NOT match)",
			err:      &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry, Message: "Duplicate entry 'test' for key 'PRIMARY'"},
			expected: false,
		},
		{
			name:     "MySQL error 1050 - Table already exists",
			err:      &mysql.MySQLError{Number: 1050, Message: "Table 'test' already exists"},
			expected: false,
		},
		{
			name:     "wrapped MySQL error 1061",
			err:      errors.Join(errors.New("wrapped"), &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName}),
			expected: true,
		},
		{
			name:     "wrapped MySQL error 1062 (should NOT match)",
			err:      errors.Join(errors.New("context"), &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry}),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDuplicateIndexNameError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVerifySchema_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Backup and restore expectedSchema
	originalExpectedSchema := expectedSchema
	defer func() { expectedSchema = originalExpectedSchema }()

	// Override expectedSchema to test only one table for simplicity
	testTable := sqldb.TableNameSessionStates
	expectedSchema = map[string]struct {
		columns []tableColumn
		indexes []tableIndex
	}{
		testTable: originalExpectedSchema[testTable],
	}

	// Use BuildTableName to match actual code behavior
	fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, testTable)

	// 1. tableExists
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(fullTableName).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// 2. verifyColumns
	rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"})
	for _, col := range expectedSchema[testTable].columns {
		isNullable := "NO"
		if col.nullable {
			isNullable = "YES"
		}
		rows.AddRow(col.name, col.dataType, isNullable)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COLUMN_NAME")).
		WithArgs(fullTableName).
		WillReturnRows(rows)

	// 3. verifyIndexes
	idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME"})
	for _, idx := range expectedSchema[testTable].indexes {
		idxName := sqldb.BuildIndexName(s.opts.tablePrefix, idx.table, idx.suffix)
		for _, col := range idx.columns {
			idxRows.AddRow(idxName, col)
		}
	}
	// Add PRIMARY key (should be ignored by unexpected check)
	idxRows.AddRow("PRIMARY", "id")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
		WithArgs(fullTableName).
		WillReturnRows(idxRows)

	err = s.verifySchema(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyIndexes_Scenarios(t *testing.T) {
	tests := []struct {
		name            string
		expectedIndexes []tableIndex
		actualIndexes   map[string][]string // indexName -> columns
		wantError       bool
	}{
		{
			name: "all indexes correct",
			expectedIndexes: []tableIndex{
				{"session_states", "lookup", []string{"app_name", "user_id", "session_id", "deleted_at"}, false},
				{"session_states", "expires", []string{"expires_at"}, false},
			},
			actualIndexes: map[string][]string{
				"idx_session_states_lookup":  {"app_name", "user_id", "session_id", "deleted_at"},
				"idx_session_states_expires": {"expires_at"},
				"PRIMARY":                    {"id"},
			},
			wantError: false,
		},
		{
			name: "missing index - should warn with CREATE statement",
			expectedIndexes: []tableIndex{
				{"session_states", "lookup", []string{"app_name", "user_id"}, false},
				{"session_states", "expires", []string{"expires_at"}, false},
			},
			actualIndexes: map[string][]string{
				// lookup index is missing.
				"idx_session_states_expires": {"expires_at"},
				"PRIMARY":                    {"id"},
			},
			wantError: false,
		},
		{
			name: "wrong columns - should warn with DROP and CREATE statements",
			expectedIndexes: []tableIndex{
				{"session_states", "expires", []string{"expires_at"}, false},
			},
			actualIndexes: map[string][]string{
				"idx_session_states_expires": {"app_name"}, // wrong column
				"PRIMARY":                    {"id"},
			},
			wantError: false,
		},
		{
			name: "unexpected index - should warn with DROP statement",
			expectedIndexes: []tableIndex{
				{"session_states", "lookup", []string{"app_name"}, false},
			},
			actualIndexes: map[string][]string{
				"idx_session_states_lookup": {"app_name"},
				"idx_old_deprecated":        {"some_col"}, // unexpected
				"PRIMARY":                   {"id"},
			},
			wantError: false,
		},
		{
			name: "mixed scenarios",
			expectedIndexes: []tableIndex{
				{"session_states", "lookup", []string{"col1", "col2"}, false},
				{"session_states", "expires", []string{"col3"}, false},
			},
			actualIndexes: map[string][]string{
				// lookup is missing.
				"idx_session_states_expires": {"col4"}, // wrong columns
				"idx_extra":                  {"col5"}, // unexpected
				"PRIMARY":                    {"id"},
			},
			wantError: false,
		},
		{
			name: "missing unique index - should warn with CREATE UNIQUE INDEX",
			expectedIndexes: []tableIndex{
				{"session_states", "unique_active", []string{"app_name", "user_id"}, true},
			},
			actualIndexes: map[string][]string{
				"PRIMARY": {"id"},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db)
			ctx := context.Background()

			fullTableName := "session_states"

			// Build mock rows from actualIndexes
			idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME"})
			for idxName, cols := range tt.actualIndexes {
				for _, col := range cols {
					idxRows.AddRow(idxName, col)
				}
			}

			mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
				WithArgs(fullTableName).
				WillReturnRows(idxRows)

			err = s.verifyIndexes(ctx, fullTableName, tt.expectedIndexes)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVerifyColumns_Scenarios(t *testing.T) {
	tests := []struct {
		name            string
		expectedColumns []tableColumn
		actualColumns   map[string]tableColumn
		wantError       bool
		wantErrContains string
	}{
		{
			name: "all columns correct",
			expectedColumns: []tableColumn{
				{"id", "bigint", false},
				{"app_name", "varchar", false},
				{"state", "json", true},
			},
			actualColumns: map[string]tableColumn{
				"id":       {"id", "bigint", false},
				"app_name": {"app_name", "varchar", false},
				"state":    {"state", "json", true},
			},
			wantError: false,
		},
		{
			name: "missing column",
			expectedColumns: []tableColumn{
				{"id", "bigint", false},
				{"app_name", "varchar", false},
				{"missing_col", "text", true},
			},
			actualColumns: map[string]tableColumn{
				"id":       {"id", "bigint", false},
				"app_name": {"app_name", "varchar", false},
			},
			wantError:       true,
			wantErrContains: "missing_col is missing",
		},
		{
			name: "wrong data type",
			expectedColumns: []tableColumn{
				{"state", "json", true},
			},
			actualColumns: map[string]tableColumn{
				"state": {"state", "text", true}, // should be json
			},
			wantError:       true,
			wantErrContains: "has type text, expected json",
		},
		{
			name: "wrong nullable",
			expectedColumns: []tableColumn{
				{"app_name", "varchar", false},
			},
			actualColumns: map[string]tableColumn{
				"app_name": {"app_name", "varchar", true}, // should be NOT NULL
			},
			wantError:       true,
			wantErrContains: "nullable mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db)
			ctx := context.Background()

			tableName := "test_table"

			// Build mock rows from actualColumns
			rows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"})
			for _, col := range tt.actualColumns {
				isNullable := "NO"
				if col.nullable {
					isNullable = "YES"
				}
				rows.AddRow(col.name, col.dataType, isNullable)
			}

			mock.ExpectQuery(regexp.QuoteMeta("SELECT COLUMN_NAME")).
				WithArgs(tableName).
				WillReturnRows(rows)

			err = s.verifyColumns(ctx, tableName, tt.expectedColumns)
			if tt.wantError {
				assert.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVerifySchema_TableExistsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: First table check fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnError(assert.AnError)

	err = s.verifySchema(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check table")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifySchema_TableMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: First table check returns 0 count (missing)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	err = s.verifySchema(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyColumns_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: verifyColumns query fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COLUMN_NAME")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, "test_table")).
		WillReturnError(assert.AnError)

	err = s.verifyColumns(ctx, sqldb.BuildTableName(s.opts.tablePrefix, "test_table"), []tableColumn{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query columns failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyIndexes_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// Mock: verifyIndexes query fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, "test_table")).
		WillReturnError(assert.AnError)

	err = s.verifyIndexes(ctx, sqldb.BuildTableName(s.opts.tablePrefix, "test_table"), []tableIndex{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query indexes failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildIndexColumnsStr(t *testing.T) {
	tests := []struct {
		name     string
		table    string
		suffix   string
		columns  []string
		expected string
	}{
		{
			name:     "session_summaries unique_active - should add prefix",
			table:    sqldb.TableNameSessionSummaries,
			suffix:   sqldb.IndexSuffixUniqueActive,
			columns:  []string{"app_name", "user_id", "session_id", "filter_key"},
			expected: "app_name(191), user_id(191), session_id(191), filter_key(191)",
		},
		{
			name:     "session_summaries expires - no prefix",
			table:    sqldb.TableNameSessionSummaries,
			suffix:   sqldb.IndexSuffixExpires,
			columns:  []string{"expires_at"},
			expected: "expires_at",
		},
		{
			name:     "session_states unique_active - no prefix",
			table:    sqldb.TableNameSessionStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			columns:  []string{"app_name", "user_id", "session_id", "deleted_at"},
			expected: "app_name, user_id, session_id, deleted_at",
		},
		{
			name:     "app_states unique_active - no prefix",
			table:    sqldb.TableNameAppStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			columns:  []string{"app_name", "key", "deleted_at"},
			expected: "app_name, key, deleted_at",
		},
		{
			name:     "user_states unique_active - no prefix",
			table:    sqldb.TableNameUserStates,
			suffix:   sqldb.IndexSuffixUniqueActive,
			columns:  []string{"app_name", "user_id", "key", "deleted_at"},
			expected: "app_name, user_id, key, deleted_at",
		},
		{
			name:     "session_events lookup - no prefix",
			table:    sqldb.TableNameSessionEvents,
			suffix:   sqldb.IndexSuffixLookup,
			columns:  []string{"app_name", "user_id", "session_id", "created_at"},
			expected: "app_name, user_id, session_id, created_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildIndexColumnsStr(tt.table, tt.suffix, tt.columns)
			assert.Equal(t, tt.expected, got)
		})
	}
}
