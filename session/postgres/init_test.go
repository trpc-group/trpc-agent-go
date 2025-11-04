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
	err = createTables(context.Background(), client, "")
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
	err = createTables(context.Background(), client, prefix)
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
	err = createIndexes(context.Background(), client, "")
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
			result := buildCreateTableSQL(tt.prefix, tt.table, template)
			assert.Contains(t, result, tt.expected)
		})
	}
}

func TestBuildIndexSQL(t *testing.T) {
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
			expected: "idx_session_states",
		},
		{
			name:     "with prefix",
			prefix:   "app1_",
			table:    "session_states",
			expected: "idx_app1_session_states",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := "CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}} (id)"
			result := buildIndexSQL(tt.prefix, tt.table, template)
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
