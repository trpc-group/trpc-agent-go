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
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

func TestCreateTables_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}

	// Mock table creation (5 tables)
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Test createTables with no prefix
	err = createTables(context.Background(), client, "", "")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTables_WithPrefix(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	prefix := "myapp_"

	// Mock table creation with prefix
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS myapp_").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Test createTables with prefix
	err = createTables(context.Background(), client, "", prefix)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateIndexes_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}

	// Mock index creation (10 indexes)
	for i := 0; i < 10; i++ {
		mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Test createIndexes
	err = createIndexes(context.Background(), client, "", "")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildCreateTableSQL(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		table    string
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			table:    "session_states",
			expected: "session_states",
		},
		{
			name:     "with prefix",
			prefix:   "app1_",
			table:    "session_states",
			expected: "app1_session_states",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := "CREATE TABLE {{TABLE_NAME}} (id INT)"
			result := buildCreateTableSQL("", tt.prefix, tt.table, template)
			assert.Contains(t, result, tt.expected)
		})
	}
}

func TestBuildIndexName(t *testing.T) {
	tests := []struct {
		name      string
		schema    string
		prefix    string
		tableName string
		suffix    string
		expected  string
	}{
		{
			name:      "no schema, no prefix",
			schema:    "",
			prefix:    "",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_session_states_unique_active",
		},
		{
			name:      "with prefix only",
			schema:    "",
			prefix:    "app1_",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_app1_session_states_unique_active",
		},
		{
			name:      "with schema only",
			schema:    "new",
			prefix:    "",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_new_session_states_unique_active",
		},
		{
			name:      "with both schema and prefix",
			schema:    "new",
			prefix:    "app1_",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_new_app1_session_states_unique_active",
		},
		{
			name:      "different index suffix",
			schema:    "new",
			prefix:    "app1_",
			tableName: "session_states",
			suffix:    "expires",
			expected:  "idx_new_app1_session_states_expires",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildIndexName(tt.schema, tt.prefix, tt.tableName, tt.suffix)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildIndexSQL(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		table    string
		suffix   string
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			table:    "session_states",
			suffix:   "test",
			expected: "idx_session_states_test",
		},
		{
			name:     "with prefix",
			prefix:   "app1_",
			table:    "session_states",
			suffix:   "test",
			expected: "idx_app1_session_states_test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := "CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}} (id)"
			result := buildIndexSQL("", tt.prefix, tt.table, tt.suffix, template)
			assert.Contains(t, result, tt.expected)
		})
	}
}

func TestInitDBOptions(t *testing.T) {
	config := &InitDBConfig{}

	// Test all options
	WithInitDBHost("testhost")(config)
	assert.Equal(t, "testhost", config.host)

	WithInitDBPort(3306)(config)
	assert.Equal(t, 3306, config.port)

	WithInitDBUser("testuser")(config)
	assert.Equal(t, "testuser", config.user)

	WithInitDBPassword("testpass")(config)
	assert.Equal(t, "testpass", config.password)

	WithInitDBDatabase("testdb")(config)
	assert.Equal(t, "testdb", config.database)

	WithInitDBSSLMode("require")(config)
	assert.Equal(t, "require", config.sslMode)

	WithInitDBTablePrefix("test_")(config)
	assert.Equal(t, "test_", config.tablePrefix)

	WithInitDBInstanceName("my-instance")(config)
	assert.Equal(t, "my-instance", config.instanceName)

	WithInitDBExtraOptions("opt1", "opt2")(config)
	assert.Len(t, config.extraOptions, 2)
	assert.Equal(t, "opt1", config.extraOptions[0])
	assert.Equal(t, "opt2", config.extraOptions[1])
}

// TestInitDB_Success tests the InitDB function with all tables and indexes created successfully
func TestInitDB_Success(t *testing.T) {
	// Save and restore original builder
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	// Set custom builder that returns our mock client
	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mockClient, nil
	})

	// Mock all CREATE operations (5 tables + 10 indexes = 15 total)
	for i := 0; i < 15; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = InitDB(context.Background(),
		WithInitDBHost("localhost"),
		WithInitDBPort(5432),
		WithInitDBDatabase("testdb"),
		WithInitDBUser("testuser"),
		WithInitDBPassword("testpass"),
	)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInitDB_WithTablePrefix tests InitDB with table prefix
func TestInitDB_WithTablePrefix(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mockClient, nil
	})

	// Mock all CREATE operations (5 tables + ~10 indexes)
	for i := 0; i < 15; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = InitDB(context.Background(),
		WithInitDBHost("localhost"),
		WithInitDBPort(5432),
		WithInitDBDatabase("testdb"),
		WithInitDBTablePrefix("test_"),
	)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInitDB_CreateTablesFails tests InitDB when creating tables fails
func TestInitDB_CreateTablesFails(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mockClient, nil
	})

	// Mock first table creation fails
	mock.ExpectExec("CREATE").WillReturnError(assert.AnError)

	err = InitDB(context.Background(),
		WithInitDBHost("localhost"),
		WithInitDBPort(5432),
		WithInitDBDatabase("testdb"),
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create tables")
}

// TestInitDB_CreateIndexesFails tests InitDB when creating indexes fails
func TestInitDB_CreateIndexesFails(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mockClient, nil
	})

	// Mock create tables succeed (5 tables)
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock first index creation fails
	mock.ExpectExec("CREATE").WillReturnError(assert.AnError)

	err = InitDB(context.Background(),
		WithInitDBHost("localhost"),
		WithInitDBPort(5432),
		WithInitDBDatabase("testdb"),
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create indexes")
}

// TestInitDB_WithInstanceName tests InitDB with instance name
func TestInitDB_WithInstanceName(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	// Register instance
	storage.RegisterPostgresInstance("test-instance",
		storage.WithClientConnString("postgres://localhost:5432/testdb"),
	)

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return mockClient, nil
	})

	// Mock all CREATE operations (5 tables + ~10 indexes)
	for i := 0; i < 15; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = InitDB(context.Background(),
		WithInitDBInstanceName("test-instance"),
	)

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInitDB_ClientBuilderFails tests that InitDB fails when client builder returns error
func TestInitDB_ClientBuilderFails(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	defer func() { storage.SetClientBuilder(oldBuilder) }()

	storage.SetClientBuilder(func(ctx context.Context, opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, assert.AnError
	})

	err := InitDB(context.Background(),
		WithInitDBHost("localhost"),
		WithInitDBPort(5432),
		WithInitDBDatabase("testdb"),
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "create postgres client from connection settings failed")
}

// TestCreateTables tests the createTables function
func TestCreateTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	// Mock create tables
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = createTables(context.Background(), mockClient, "", "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateTables_WithPrefixMock tests createTables with table prefix
func TestCreateTables_WithPrefixMock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	// Mock create tables with prefix
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS myapp_").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = createTables(context.Background(), mockClient, "", "myapp_")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateIndexes tests the createIndexes function
func TestCreateIndexes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	// Mock create indexes (~10 indexes)
	for i := 0; i < 10; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = createIndexes(context.Background(), mockClient, "", "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateIndexes_WithPrefix tests createIndexes with table prefix
func TestCreateIndexes_WithPrefix(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mockClient := &mockPostgresClient{db: db}

	// Mock create indexes with prefix (~10 indexes)
	for i := 0; i < 10; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = createIndexes(context.Background(), mockClient, "", "myapp_")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildCreateTableSQL_NoPrefix tests SQL template replacement without prefix
func TestBuildCreateTableSQL_NoPrefix(t *testing.T) {
	sql := buildCreateTableSQL("", "", "session_states", sqlCreateSessionStatesTable)
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS session_states")
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
}

// TestBuildCreateTableSQL_WithPrefixMock tests SQL template replacement with prefix
func TestBuildCreateTableSQL_WithPrefixMock(t *testing.T) {
	sql := buildCreateTableSQL("", "test_", "session_states", sqlCreateSessionStatesTable)
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS test_session_states")
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
}

// TestBuildIndexSQL_Mock tests index SQL template replacement
func TestBuildIndexSQL_Mock(t *testing.T) {
	sql := buildIndexSQL("", "", "test_table", "test_suffix", "CREATE UNIQUE INDEX IF NOT EXISTS {{INDEX_NAME}} ON {{TABLE_NAME}}(id)")
	assert.Contains(t, sql, "ON test_table(id)")
	assert.Contains(t, sql, "idx_test_table_test_suffix")
	assert.NotContains(t, sql, "{{TABLE_NAME}}")
	assert.NotContains(t, sql, "{{INDEX_NAME}}")
}
