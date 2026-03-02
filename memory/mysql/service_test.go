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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	_ "github.com/go-sql-driver/mysql"
)

// mockClientBuilder creates a mock MySQL client for testing.
func mockClientBuilder(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
	// For unit tests, we can use an in-memory SQLite database or mock
	// Here we just return an error to demonstrate the pattern
	return nil, sql.ErrConnDone
}

// setupMockDB creates a mock database and sqlmock for testing.
func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return mockDB, mock
}

// setupMockService creates a service with a mock database.
func setupMockService(_ *testing.T, db *sql.DB) *Service {
	return &Service{
		opts: ServiceOpts{
			memoryLimit:  100,
			toolCreators: make(map[string]memory.ToolCreator),
			enabledTools: make(map[string]struct{}),
			tableName:    "memories",
			softDelete:   true,
		},
		db:          storage.WrapSQLDB(db),
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
}

// mockTool is a mock implementation of tool.Tool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: "mock tool for testing",
	}
}

// TestNewService_WithDSN tests creating a new service with DSN configuration.
// It verifies that the service creation fails when using a mock builder that returns an error.
func TestNewService_WithDSN(t *testing.T) {
	// Set mock builder
	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(mockClientBuilder)
	defer storage.SetClientBuilder(originalBuilder)

	_, err := NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)
	require.Error(t, err, "expected error with mock builder")
}

// TestNewService_WithInstance tests creating a new service with a registered MySQL instance.
// It verifies that the service creation fails when the instance uses a mock builder that returns an error.
func TestNewService_WithInstance(t *testing.T) {
	// Set mock builder
	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(mockClientBuilder)
	defer storage.SetClientBuilder(originalBuilder)

	// Register instance
	storage.RegisterMySQLInstance("test-instance",
		storage.WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)

	_, err := NewService(
		WithMySQLInstance("test-instance"),
	)
	require.Error(t, err, "expected error with mock builder")
}

// TestNewService_InstanceNotFound tests that service creation fails when referencing a non-existent instance.
func TestNewService_InstanceNotFound(t *testing.T) {
	_, err := NewService(
		WithMySQLInstance("non-existent-instance"),
	)
	require.Error(t, err, "expected error for non-existent instance")
	assert.Contains(t, err.Error(), "not found")
}

// TestNewService_InvalidTableName tests that service creation panics with an invalid table name.
// Table names with dashes are not allowed in MySQL.
func TestNewService_InvalidTableName(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
		WithTableName("invalid-table-name"),
	)
}

// TestNewService_AutoCreateTable tests that the service automatically creates the table.
func TestNewService_AutoCreateTable(t *testing.T) {
	mockDB, mock := setupMockDB(t)
	defer mockDB.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(mockDB), nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Expect table creation (always happens now).
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	service, err := NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNewService_DSNPriority tests that DSN has priority over instanceName when both are provided.
func TestNewService_DSNPriority(t *testing.T) {
	mockDB, mock := setupMockDB(t)
	defer mockDB.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(mockDB), nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Register instance
	storage.RegisterMySQLInstance("test-instance",
		storage.WithClientBuilderDSN("wrong-dsn"),
	)

	// Expect table creation
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	// Both DSN and instanceName provided, DSN should be used
	service, err := NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
		WithMySQLInstance("test-instance"),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNewService_WithSkipDBInit tests that skipDBInit option skips database initialization.
func TestNewService_WithSkipDBInit(t *testing.T) {
	mockDB, mock := setupMockDB(t)
	defer mockDB.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(mockDB), nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// No table creation expected because skipDBInit is true
	service, err := NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNewService_WithExtractor tests that the auto memory worker is
// initialized when an extractor implementing EnabledToolsConfigurer
// is provided.
func TestNewService_WithExtractor(t *testing.T) {
	mockDB, _ := setupMockDB(t)
	defer mockDB.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(mockDB), nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	ext := &mockExtractor{}
	service, err := NewService(
		WithMySQLClientDSN(
			"user:password@tcp(localhost:3306)/testdb",
		),
		WithSkipDBInit(true),
		WithExtractor(ext),
	)
	require.NoError(t, err)
	require.NotNil(t, service)
	defer service.Close()

	assert.NotNil(t, service.autoMemoryWorker)
}

// TestNewService_InitDBError tests that service creation fails when initDB fails.
func TestNewService_InitDBError(t *testing.T) {
	mockDB, mock := setupMockDB(t)
	defer mockDB.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(mockDB), nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table creation failure
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnError(errors.New("create table failed"))

	service, err := NewService(
		WithMySQLClientDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)
	require.Error(t, err)
	assert.Nil(t, service)
	assert.Contains(t, err.Error(), "init database failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestServiceOpts tests all service option setters to ensure they correctly modify the ServiceOpts struct.
func TestServiceOpts(t *testing.T) {
	opts := ServiceOpts{}

	WithMySQLClientDSN("test-dsn")(&opts)
	assert.Equal(t, "test-dsn", opts.dsn)

	WithMySQLInstance("test-instance")(&opts)
	assert.Equal(t, "test-instance", opts.instanceName)

	WithMemoryLimit(100)(&opts)
	assert.Equal(t, 100, opts.memoryLimit)

	WithTableName("custom_memories")(&opts)
	assert.Equal(t, "custom_memories", opts.tableName)
}

// TestWithCustomTool tests registering custom tool creators.
// It verifies that valid tool names are registered and invalid ones are ignored.
func TestWithCustomTool(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]struct{}),
	}

	customCreator := func() tool.Tool {
		return &mockTool{name: memory.AddToolName}
	}

	WithCustomTool(memory.AddToolName, customCreator)(&opts)
	assert.Contains(t, opts.toolCreators, memory.AddToolName)
	_, hasAdd := opts.enabledTools[memory.AddToolName]
	assert.True(t, hasAdd)

	// Test with invalid tool name (should do nothing).
	WithCustomTool("invalid_tool_name", customCreator)(&opts)
	assert.NotContains(t, opts.toolCreators, "invalid_tool_name")

	// Test with nil creator (should do nothing).
	WithCustomTool(memory.SearchToolName, nil)(&opts)
	assert.NotContains(t, opts.toolCreators, memory.SearchToolName)
	_, hasSearch := opts.enabledTools[memory.SearchToolName]
	assert.False(t, hasSearch)
}

// TestWithToolEnabled tests enabling and disabling tools.
// It verifies that valid tool names can be toggled and invalid ones are ignored.
func TestWithToolEnabled(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]struct{}),
	}

	WithToolEnabled(memory.AddToolName, true)(&opts)
	_, hasAdd := opts.enabledTools[memory.AddToolName]
	assert.True(t, hasAdd)

	WithToolEnabled(memory.AddToolName, false)(&opts)
	_, hasAdd = opts.enabledTools[memory.AddToolName]
	assert.False(t, hasAdd)

	// Test with invalid tool name (should do nothing).
	WithToolEnabled("invalid_tool_name", true)(&opts)
	assert.NotContains(t, opts.enabledTools, "invalid_tool_name")
}

func TestWithExtraOptions(t *testing.T) {
	opts := ServiceOpts{}

	WithExtraOptions("opt1")(&opts)
	assert.Len(t, opts.extraOptions, 1)

	WithExtraOptions("opt2", "opt3")(&opts)
	assert.Len(t, opts.extraOptions, 3)
}

// TestWithSkipDBInit tests that skipDBInit option is correctly set.
func TestWithSkipDBInit(t *testing.T) {
	opts := ServiceOpts{}
	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)

	WithSkipDBInit(false)(&opts)
	assert.False(t, opts.skipDBInit)
}

// TestValidateTableName tests table name validation logic.
// It ensures that only valid MySQL table names are accepted and malicious inputs are rejected.
func TestValidateTableName(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		wantErr   bool
	}{
		{"valid table name", "memories", false},
		{"valid with underscore", "user_memories", false},
		{"valid with numbers", "memories_v2", false},
		{"valid starting with underscore", "_memories", false},
		{"empty table name", "", true},
		{"table name with dash", "user-memories", true},
		{"table name with space", "user memories", true},
		{"SQL injection attempt", "memories; DROP TABLE users;--", true},
		{"starting with number", "123memories", true},
		{"too long", "a123456789012345678901234567890123456789012345678901234567890123456789", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTableName(tt.tableName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestAddMemory_InvalidUserKey tests that AddMemory rejects invalid user keys.
// Both AppName and UserID are required fields.
func TestAddMemory_InvalidUserKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	t.Run("empty app name", func(t *testing.T) {
		err := s.AddMemory(ctx, memory.UserKey{}, "test", nil)
		require.Error(t, err)
	})

	t.Run("empty user ID", func(t *testing.T) {
		err := s.AddMemory(ctx, memory.UserKey{AppName: "app"}, "test", nil)
		require.Error(t, err)
	})
}

// TestAddMemory_MemoryLimitExceeded tests that AddMemory enforces the memory limit.
// When the limit is reached, no new memories can be added.
func TestAddMemory_MemoryLimitExceeded(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.memoryLimit = 10

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))

	err := s.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_Success tests successful memory addition.
// It verifies that a memory is correctly inserted into the database.
func TestAddMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	// Mock count query.
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	// Mock insert.
	mock.ExpectExec("INSERT INTO").
		WithArgs("app", "user", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_CountQueryError tests error handling when the count query fails.
func TestAddMemory_CountQueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnError(errors.New("database error"))

	err := s.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check memory count failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_InsertError tests error handling when the insert operation fails.
func TestAddMemory_InsertError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	mock.ExpectExec("INSERT INTO").
		WillReturnError(errors.New("insert error"))

	err := s.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_NoLimit tests that when memory limit is 0, no limit is enforced.
func TestAddMemory_NoLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.memoryLimit = 0

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectExec("INSERT INTO").
		WithArgs("app", "user", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_Idempotent tests that AddMemory is idempotent - calling it multiple times
// with the same memory content should not fail.
func TestAddMemory_Idempotent(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	memoryStr := "test memory"
	topics := []string{"topic1"}

	// Mock count query for first call.
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	// Mock insert for first call.
	mock.ExpectExec("INSERT INTO.*ON DUPLICATE KEY UPDATE").
		WithArgs("app", "user", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock count query for second call.
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Mock upsert for second call (should update existing).
	mock.ExpectExec("INSERT INTO.*ON DUPLICATE KEY UPDATE").
		WithArgs("app", "user", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// First call should succeed.
	err := s.AddMemory(ctx, userKey, memoryStr, topics)
	require.NoError(t, err)

	// Second call with same content should also succeed (idempotent).
	err = s.AddMemory(ctx, userKey, memoryStr, topics)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddMemory_SoftDeleteFalse tests AddMemory when softDelete is false.
func TestAddMemory_SoftDeleteFalse(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false
	s.opts.memoryLimit = 10

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	// Count query without deleted_at filter
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	mock.ExpectExec("INSERT INTO").
		WithArgs("app", "user", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.AddMemory(ctx, userKey, "test memory", nil)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_InvalidMemoryKey tests that UpdateMemory rejects invalid memory keys.
// All three fields (AppName, UserID, MemoryID) are required.
func TestUpdateMemory_InvalidMemoryKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	tests := []struct {
		name string
		key  memory.Key
	}{
		{"empty app name", memory.Key{}},
		{"empty user ID", memory.Key{AppName: "app"}},
		{"empty memory ID", memory.Key{AppName: "app", UserID: "user"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateMemory(ctx, tt.key, "test", nil)
			require.Error(t, err)
		})
	}
}

// TestUpdateMemory_MemoryNotFound tests that UpdateMemory returns an error when the memory doesn't exist.
func TestUpdateMemory_MemoryNotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user", "mem123").
		WillReturnError(sql.ErrNoRows)

	err := s.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_Success tests successful memory update.
// It verifies that an existing memory is correctly updated in the database.
func TestUpdateMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	// Create a valid memory entry JSON.
	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "old memory",
			Topics:      []string{"old"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user", "mem123").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	mock.ExpectExec("UPDATE").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "app", "user", "mem123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.UpdateMemory(ctx, memKey, "updated memory", []string{"new"})
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_SoftDeleteFalse tests UpdateMemory when softDelete is false.
func TestUpdateMemory_SoftDeleteFalse(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	now := time.Now()
	existingEntry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "old memory",
			Topics:      []string{"old"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	existingJSON, _ := json.Marshal(existingEntry)

	// Query should not include deleted_at filter
	mock.ExpectQuery("SELECT memory_data FROM.*WHERE app_name").
		WithArgs("app", "user", "mem123").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(existingJSON))

	mock.ExpectExec("UPDATE.*SET memory_data").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "app", "user", "mem123").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateMemory(ctx, memKey, "new memory", []string{"new"})
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_SelectError tests error handling when the select query fails.
func TestUpdateMemory_SelectError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user", "mem123").
		WillReturnError(errors.New("select error"))

	err := s.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_UnmarshalError tests error handling when unmarshaling fails.
func TestUpdateMemory_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user", "mem123").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow([]byte("invalid json")))

	err := s.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateMemory_UpdateError tests error handling when the update operation fails.
func TestUpdateMemory_UpdateError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "old memory",
			Topics:      []string{"old"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user", "mem123").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	mock.ExpectExec("UPDATE").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "app", "user", "mem123").
		WillReturnError(errors.New("update error"))

	err := s.UpdateMemory(ctx, memKey, "updated memory", []string{"new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteMemory_InvalidMemoryKey tests that DeleteMemory rejects invalid memory keys.
func TestDeleteMemory_InvalidMemoryKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	err := s.DeleteMemory(ctx, memory.Key{})
	require.Error(t, err)
}

// TestDeleteMemory_Success tests successful memory deletion (soft delete).
func TestDeleteMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectExec("UPDATE.*SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "app", "user", "mem123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteMemory(ctx, memKey)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteMemory_Error tests error handling when the delete operation fails.
func TestDeleteMemory_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectExec("UPDATE.*SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "app", "user", "mem123").
		WillReturnError(errors.New("delete error"))

	err := s.DeleteMemory(ctx, memKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteMemory_HardDelete tests hard delete when softDelete is false.
func TestDeleteMemory_HardDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false

	ctx := context.Background()
	memKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "mem123"}

	mock.ExpectExec("DELETE FROM").
		WithArgs("app", "user", "mem123").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.DeleteMemory(ctx, memKey)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClearMemories_InvalidUserKey tests that ClearMemories rejects invalid user keys.
func TestClearMemories_InvalidUserKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	err := s.ClearMemories(ctx, memory.UserKey{})
	require.Error(t, err)
}

// TestClearMemories_Success tests successful clearing of all memories for a user (soft delete).
func TestClearMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectExec("UPDATE.*SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "app", "user").
		WillReturnResult(sqlmock.NewResult(0, 5))

	err := s.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClearMemories_Error tests error handling when the clear operation fails.
func TestClearMemories_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectExec("UPDATE.*SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "app", "user").
		WillReturnError(errors.New("clear error"))

	err := s.ClearMemories(ctx, userKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear memories failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClearMemories_HardDelete tests hard delete when softDelete is false.
func TestClearMemories_HardDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectExec("DELETE FROM").
		WithArgs("app", "user").
		WillReturnResult(sqlmock.NewResult(5, 5))

	err := s.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_InvalidUserKey tests that ReadMemories rejects invalid user keys.
func TestReadMemories_InvalidUserKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	_, err := s.ReadMemories(ctx, memory.UserKey{}, 10)
	require.Error(t, err)
}

// TestReadMemories_Success tests successful reading of memories.
// It verifies that memories are correctly retrieved and deserialized from the database.
func TestReadMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	entries, err := s.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "test memory", entries[0].Memory.Memory)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_WithLimit tests that the limit parameter is correctly applied to the query.
func TestReadMemories_WithLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data.*LIMIT 5").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}))

	_, err := s.ReadMemories(ctx, userKey, 5)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_NoLimit tests that when limit is 0, all memories are returned without a LIMIT clause.
func TestReadMemories_NoLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data FROM memories WHERE app_name = \\? AND user_id = \\? AND deleted_at IS NULL ORDER BY updated_at DESC, created_at DESC$").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}))

	_, err := s.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_SoftDeleteFalse tests ReadMemories when softDelete is false.
func TestReadMemories_SoftDeleteFalse(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	// Query should not include deleted_at filter
	mock.ExpectQuery("SELECT memory_data FROM.*WHERE app_name").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	entries, err := s.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_QueryError tests error handling when the query fails.
func TestReadMemories_QueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnError(errors.New("query error"))

	_, err := s.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list memories failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_ScanError tests error handling when scanning rows fails.
func TestReadMemories_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	rows := sqlmock.NewRows([]string{"memory_data"}).
		AddRow(nil).
		RowError(0, errors.New("scan error"))

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(rows)

	_, err := s.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReadMemories_UnmarshalError tests error handling when unmarshaling fails.
func TestReadMemories_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow([]byte("invalid json")))

	_, err := s.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_InvalidUserKey tests that SearchMemories rejects invalid user keys.
func TestSearchMemories_InvalidUserKey(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	_, err := s.SearchMemories(ctx, memory.UserKey{}, "query")
	require.Error(t, err)
}

// TestSearchMemories_Success tests successful memory search.
// It verifies that memories matching the query are correctly retrieved.
func TestSearchMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "test memory with query",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	entries, err := s.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.NotNil(t, entries)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_QueryError tests error handling when the search query fails.
func TestSearchMemories_QueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnError(errors.New("search error"))

	_, err := s.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search memories failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_ScanError tests error handling when scanning rows fails.
func TestSearchMemories_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	rows := sqlmock.NewRows([]string{"memory_data"}).
		AddRow(nil).
		RowError(0, errors.New("scan error"))

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(rows)

	_, err := s.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_UnmarshalError tests error handling when unmarshaling fails.
func TestSearchMemories_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow([]byte("invalid json")))

	_, err := s.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestService_Tools tests the Tools method.
// It verifies that tools are correctly pre-computed and returned consistently.
func TestService_Tools(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	mockTool1 := &mockTool{name: "tool1"}
	mockTool2 := &mockTool{name: "tool2"}

	s := &Service{
		opts: ServiceOpts{
			toolCreators: map[string]memory.ToolCreator{
				"tool1": func() tool.Tool { return mockTool1 },
				"tool2": func() tool.Tool { return mockTool2 },
			},
			enabledTools: map[string]struct{}{
				"tool1": {},
				"tool2": {},
			},
		},
		db:          storage.WrapSQLDB(db),
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	// Pre-compute tools list as NewService would do.
	s.precomputedTools = imemory.BuildToolsList(
		s.opts.extractor,
		s.opts.toolCreators,
		s.opts.enabledTools,
		s.cachedTools,
	)

	tools := s.Tools()
	assert.Len(t, tools, 2)

	// Verify tools are cached.
	assert.Len(t, s.cachedTools, 2)

	// Verify Tools() returns the same pre-computed list.
	tools2 := s.Tools()
	assert.Len(t, tools2, 2)
	assert.Equal(t, tools[0], tools2[0])
}

// TestService_Close tests the Close method.
// It verifies that the database connection is properly closed.
func TestService_Close(t *testing.T) {
	t.Run("with nil db", func(t *testing.T) {
		s := &Service{db: nil}
		err := s.Close()
		assert.NoError(t, err)
	})

	t.Run("with mock db", func(t *testing.T) {
		db, mock := setupMockDB(t)
		s := &Service{db: storage.WrapSQLDB(db)}

		mock.ExpectClose()
		err := s.Close()
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestInitDB_Success tests successful table initialization.
func TestInitDB_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.initDB(ctx)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInitDB_Error tests error handling when table creation fails.
func TestInitDB_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnError(errors.New("create table error"))

	err := s.initDB(ctx)
	require.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_SortingWithEqualUpdatedAt tests sorting when multiple memories have the same updated_at
func TestSearchMemories_SortingWithEqualUpdatedAt(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	// Create entries with same updated_at but different created_at
	entry1 := &memory.Entry{
		ID:      "mem1",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "memory 1 with query",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: earlier,
		UpdatedAt: now,
	}
	entry2 := &memory.Entry{
		ID:      "mem2",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "memory 2 with query",
			Topics:      []string{"topic2"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	entry1JSON, _ := json.Marshal(entry1)
	entry2JSON, _ := json.Marshal(entry2)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow(entry1JSON).
			AddRow(entry2JSON))

	entries, err := s.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	// Should be sorted by created_at desc when updated_at is equal
	assert.Equal(t, "mem2", entries[0].ID)
	assert.Equal(t, "mem1", entries[1].ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_SortingWithDifferentUpdatedAt tests sorting when memories have different updated_at
func TestSearchMemories_SortingWithDifferentUpdatedAt(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	// Create entries with different updated_at
	entry1 := &memory.Entry{
		ID:      "mem1",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "memory 1 with query",
			Topics:      []string{"topic1"},
			LastUpdated: &earlier,
		},
		CreatedAt: now,
		UpdatedAt: earlier,
	}
	entry2 := &memory.Entry{
		ID:      "mem2",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "memory 2 with query",
			Topics:      []string{"topic2"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	entry1JSON, _ := json.Marshal(entry1)
	entry2JSON, _ := json.Marshal(entry2)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).
			AddRow(entry1JSON).
			AddRow(entry2JSON))

	entries, err := s.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	// Should be sorted by updated_at desc
	assert.Equal(t, "mem2", entries[0].ID)
	assert.Equal(t, "mem1", entries[1].ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_NoMatches tests search with no matching memories
func TestSearchMemories_NoMatches(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem1",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "memory without match",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	entryJSON, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	entries, err := s.SearchMemories(ctx, userKey, "nonexistent")
	require.NoError(t, err)
	assert.Len(t, entries, 0)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSearchMemories_SoftDeleteFalse tests SearchMemories when softDelete is false.
func TestSearchMemories_SoftDeleteFalse(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)
	s.opts.softDelete = false

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem123",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "test memory with query",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryJSON, _ := json.Marshal(entry)

	// Query should not include deleted_at filter
	mock.ExpectQuery("SELECT memory_data FROM.*WHERE app_name").
		WithArgs("app", "user").
		WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryJSON))

	entries, err := s.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// mockExtractor is a mock implementation of extractor.MemoryExtractor.
type mockExtractor struct {
	extractCalled bool
}

func (m *mockExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	m.extractCalled = true
	return nil, nil
}

func (m *mockExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (m *mockExtractor) SetPrompt(prompt string) {}

func (m *mockExtractor) SetModel(mdl model.Model) {}

func (m *mockExtractor) SetEnabledTools(enabled map[string]struct{}) {}

func (m *mockExtractor) Metadata() map[string]any {
	return map[string]any{}
}

func TestWithExtractor(t *testing.T) {
	ext := &mockExtractor{}
	opts := defaultOptions.clone()
	WithExtractor(ext)(&opts)
	assert.Equal(t, ext, opts.extractor)
}

func TestWithAsyncMemoryNum(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithAsyncMemoryNum(5)(&opts)
		assert.Equal(t, 5, opts.asyncMemoryNum)
	})

	t.Run("invalid value uses default", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithAsyncMemoryNum(0)(&opts)
		assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	})
}

func TestWithMemoryQueueSize(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithMemoryQueueSize(200)(&opts)
		assert.Equal(t, 200, opts.memoryQueueSize)
	})

	t.Run("invalid value uses default", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithMemoryQueueSize(0)(&opts)
		assert.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)
	})
}

func TestWithMemoryJobTimeout(t *testing.T) {
	opts := defaultOptions.clone()
	WithMemoryJobTimeout(time.Minute)(&opts)
	assert.Equal(t, time.Minute, opts.memoryJobTimeout)
}

func TestEnqueueAutoMemoryJob_NoWorker(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	// Should return nil when no worker is configured.
	err := s.EnqueueAutoMemoryJob(ctx, sess)
	assert.NoError(t, err)
}

func TestClose_NoWorker(t *testing.T) {
	db, mock := setupMockDB(t)
	s := setupMockService(t, db)

	// Expect db.Close() to be called.
	mock.ExpectClose()

	err := s.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTools_AutoMemoryMode(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	// Enable auto memory mode.
	s.opts.extractor = &mockExtractor{}
	s.opts.toolCreators = imemory.AllToolCreators
	// Apply auto mode defaults (nil for userExplicitlySet since this is a test).
	imemory.ApplyAutoModeDefaults(s.opts.enabledTools, nil)
	// Re-compute tools list after changing opts to simulate auto memory mode.
	s.precomputedTools = imemory.BuildToolsList(
		s.opts.extractor,
		s.opts.toolCreators,
		s.opts.enabledTools,
		s.cachedTools,
	)

	tools := s.Tools()

	// In auto memory mode, Search is enabled by default.
	assert.Len(t, tools, 1, "Auto mode should return Search tool by default")
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.SearchToolName], "Search tool should be returned by default")

	// Enable Load tool explicitly.
	s.opts.enabledTools[memory.LoadToolName] = struct{}{}
	s.precomputedTools = imemory.BuildToolsList(
		s.opts.extractor,
		s.opts.toolCreators,
		s.opts.enabledTools,
		s.cachedTools,
	)

	tools = s.Tools()
	assert.Len(t, tools, 2, "Auto mode should return Search and Load tools when Load is enabled")
	toolNames = make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.SearchToolName], "Search tool should be returned")
	assert.True(t, toolNames[memory.LoadToolName], "Load tool should be returned when enabled")
	assert.False(t, toolNames[memory.AddToolName], "Add tool should not be exposed via Tools()")
	assert.False(t, toolNames[memory.ClearToolName], "Clear tool should not be exposed via Tools()")
}
