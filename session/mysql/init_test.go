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

		// 3. verifyIndexes query (INDEX_NAME, COLUMN_NAME, NON_UNIQUE)
		idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME", "NON_UNIQUE"})
		for _, idx := range schema.indexes {
			idxName := sqldb.BuildIndexName(tablePrefix, idx.table, idx.suffix)
			nonUnique := 1
			if idx.unique {
				nonUnique = 0
			}
			for _, col := range idx.columns {
				idxRows.AddRow(idxName, col, nonUnique)
			}
		}
		idxRows.AddRow("PRIMARY", "id", 0)
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

	// initDB creates each (missing) table together with its indexes, then
	// verifies the schema.
	mockCreateMissingTables(mock, s.opts.tablePrefix)
	mockVerifySchemaQueries(mock, s.opts.tablePrefix)

	err = s.initDB(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_ExistingTablesSkipDDL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// All tables already exist: initDB must NOT issue any CREATE TABLE or
	// CREATE INDEX, only existence checks, then schema verification.
	for _, tableDef := range tableDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(fullTableName).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}
	mockVerifySchemaQueries(mock, s.opts.tablePrefix)

	err = s.initDB(ctx)
	assert.NoError(t, err)
	// No CREATE expectations were registered: if initDB had issued any DDL,
	// sqlmock would have failed on an unexpected Exec.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_TableExistsCheckError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// The first existence check itself fails (e.g. information_schema unreadable).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnError(assert.AnError)

	err = s.initDB(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check table")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_VerifySchemaError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// All tables already exist → initDB skips DDL and proceeds to verifySchema.
	for _, tableDef := range tableDefs {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}
	// verifySchema re-checks existence; report the first table as missing so
	// verification fails and initDB wraps the error.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	err = s.initDB(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "schema verification failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_TableCreationError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	// session_states does not exist; its CREATE TABLE then fails.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
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

	// session_states does not exist: create it, then its first index fails with
	// a non-duplicate error.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(sqldb.BuildTableName(s.opts.tablePrefix, sqldb.TableNameSessionStates)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS session_states")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE (UNIQUE )?INDEX").
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

	// Mock: create each (missing) prefixed table together with its indexes,
	// then verify the schema.
	mockCreateMissingTables(mock, s.opts.tablePrefix)
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

	// Every table is new; each CREATE INDEX reports "duplicate index name"
	// (1061), which initDB must tolerate so initialization still succeeds.
	dupErr := &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName, Message: "Duplicate key name"}
	indexCount := make(map[string]int)
	for _, idx := range indexDefs {
		indexCount[idx.table]++
	}
	for _, tableDef := range tableDefs {
		fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, tableDef.name)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(fullTableName).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		for i := 0; i < indexCount[tableDef.name]; i++ {
			mock.ExpectExec("CREATE (UNIQUE )?INDEX").
				WillReturnError(dupErr)
		}
	}

	mockVerifySchemaQueries(mock, s.opts.tablePrefix)

	// Duplicate index errors are ignored, so init succeeds.
	err = s.initDB(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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
	expectedSchema = map[string]tableSchema{
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
	idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME", "NON_UNIQUE"})
	for _, idx := range expectedSchema[testTable].indexes {
		idxName := sqldb.BuildIndexName(s.opts.tablePrefix, idx.table, idx.suffix)
		nonUnique := 1
		if idx.unique {
			nonUnique = 0
		}
		for _, col := range idx.columns {
			idxRows.AddRow(idxName, col, nonUnique)
		}
	}
	// Add PRIMARY key (should be ignored by unexpected check)
	idxRows.AddRow("PRIMARY", "id", 0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
		WithArgs(fullTableName).
		WillReturnRows(idxRows)

	err = s.verifySchema(ctx)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifySchema_MissingUniqueIndexFatal(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	originalExpectedSchema := expectedSchema
	defer func() { expectedSchema = originalExpectedSchema }()

	testTable := sqldb.TableNameSessionStates
	expectedSchema = map[string]tableSchema{
		testTable: originalExpectedSchema[testTable],
	}
	fullTableName := sqldb.BuildTableName(s.opts.tablePrefix, testTable)

	// Table exists.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(fullTableName).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// Columns match.
	colRows := sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"})
	for _, col := range expectedSchema[testTable].columns {
		isNullable := "NO"
		if col.nullable {
			isNullable = "YES"
		}
		colRows.AddRow(col.name, col.dataType, isNullable)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COLUMN_NAME")).
		WithArgs(fullTableName).
		WillReturnRows(colRows)
	// Indexes: omit the unique_active index so verification fails fatally.
	idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME", "NON_UNIQUE"})
	for _, idx := range expectedSchema[testTable].indexes {
		if idx.suffix == sqldb.IndexSuffixUniqueActive {
			continue
		}
		idxName := sqldb.BuildIndexName(s.opts.tablePrefix, idx.table, idx.suffix)
		for _, col := range idx.columns {
			idxRows.AddRow(idxName, col, 1)
		}
	}
	idxRows.AddRow("PRIMARY", "id", 0)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT INDEX_NAME")).
		WithArgs(fullTableName).
		WillReturnRows(idxRows)

	err = s.verifySchema(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "verify indexes")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyIndexes_Scenarios(t *testing.T) {
	tests := []struct {
		name            string
		expectedIndexes []tableIndex
		actualIndexes   map[string][]string // indexName -> columns
		actualNonUnique map[string]bool     // indexName -> is non-unique (NON_UNIQUE=1)
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
			name: "missing unique index - fatal (uniqueness not enforced)",
			expectedIndexes: []tableIndex{
				{"session_states", "unique_active", []string{"app_name", "user_id"}, true},
			},
			actualIndexes: map[string][]string{
				"PRIMARY": {"id"},
			},
			wantError: true,
		},
		{
			name: "unique index with wrong columns - fatal",
			expectedIndexes: []tableIndex{
				{"session_states", "unique_active", []string{"app_name", "user_id", "session_id", "deleted_at"}, true},
			},
			actualIndexes: map[string][]string{
				// Present but on the wrong columns: the unique-index check fails.
				"idx_session_states_unique_active": {"app_name", "user_id"},
				"PRIMARY":                          {"id"},
			},
			wantError: true,
		},
		{
			name: "missing non-unique index - warns, non-fatal",
			expectedIndexes: []tableIndex{
				{"session_states", "expires", []string{"expires_at"}, false},
			},
			actualIndexes: map[string][]string{
				"PRIMARY": {"id"},
			},
			wantError: false,
		},
		{
			name: "unique index present with right columns but NOT unique - fatal",
			expectedIndexes: []tableIndex{
				{"session_states", "unique_active", []string{"app_name", "user_id", "session_id", "deleted_at"}, true},
			},
			actualIndexes: map[string][]string{
				"idx_session_states_unique_active": {"app_name", "user_id", "session_id", "deleted_at"},
				"PRIMARY":                          {"id"},
			},
			actualNonUnique: map[string]bool{
				// Exists with the right columns, but is not a UNIQUE index.
				"idx_session_states_unique_active": true,
			},
			wantError: true,
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
			idxRows := sqlmock.NewRows([]string{"INDEX_NAME", "COLUMN_NAME", "NON_UNIQUE"})
			for idxName, cols := range tt.actualIndexes {
				nonUnique := 0
				if tt.actualNonUnique[idxName] {
					nonUnique = 1
				}
				for _, col := range cols {
					idxRows.AddRow(idxName, col, nonUnique)
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
			got := buildIndexColumnsStr(tt.table, tt.suffix, tt.columns, false)
			assert.Equal(t, tt.expected, got)
		})
	}
}
