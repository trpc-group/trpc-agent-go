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
			enabledTools: make(map[string]bool),
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
		enabledTools: make(map[string]bool),
	}

	customCreator := func() tool.Tool {
		return &mockTool{name: memory.AddToolName}
	}

	WithCustomTool(memory.AddToolName, customCreator)(&opts)
	assert.Contains(t, opts.toolCreators, memory.AddToolName)
	assert.True(t, opts.enabledTools[memory.AddToolName])

	// Test with invalid tool name (should do nothing).
	WithCustomTool("invalid_tool_name", customCreator)(&opts)
	assert.NotContains(t, opts.toolCreators, "invalid_tool_name")
}

// TestWithToolEnabled tests enabling and disabling tools.
// It verifies that valid tool names can be toggled and invalid ones are ignored.
func TestWithToolEnabled(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	WithToolEnabled(memory.AddToolName, true)(&opts)
	assert.True(t, opts.enabledTools[memory.AddToolName])

	WithToolEnabled(memory.AddToolName, false)(&opts)
	assert.False(t, opts.enabledTools[memory.AddToolName])

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

// TestGenerateMemoryID tests the memory ID generation logic.
// It verifies that IDs are deterministic for identical content and unique for different content.
func TestGenerateMemoryID(t *testing.T) {
	now := time.Now()

	t.Run("same content generates same ID", func(t *testing.T) {
		mem1 := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1", "topic2"},
			LastUpdated: &now,
		}
		mem2 := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1", "topic2"},
			LastUpdated: &now,
		}

		id1 := generateMemoryID(mem1)
		id2 := generateMemoryID(mem2)
		assert.Equal(t, id1, id2)
	})

	t.Run("different content generates different ID", func(t *testing.T) {
		mem1 := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		}
		mem2 := &memory.Memory{
			Memory:      "different memory",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		}

		id1 := generateMemoryID(mem1)
		id2 := generateMemoryID(mem2)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("without topics", func(t *testing.T) {
		mem := &memory.Memory{
			Memory:      "test memory",
			Topics:      nil,
			LastUpdated: &now,
		}

		id := generateMemoryID(mem)
		assert.NotEmpty(t, id)
		assert.Len(t, id, 64)
	})

	t.Run("empty topics", func(t *testing.T) {
		mem := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{},
			LastUpdated: &now,
		}

		id := generateMemoryID(mem)
		assert.NotEmpty(t, id)
		assert.Len(t, id, 64)
	})

	t.Run("different topics generate different IDs", func(t *testing.T) {
		mem1 := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic1"},
			LastUpdated: &now,
		}
		mem2 := &memory.Memory{
			Memory:      "test memory",
			Topics:      []string{"topic2"},
			LastUpdated: &now,
		}

		id1 := generateMemoryID(mem1)
		id2 := generateMemoryID(mem2)
		assert.NotEqual(t, id1, id2)
	})
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
// It verifies that tools are correctly created, cached, and filtered based on enabled status.
func TestService_Tools(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	s := &Service{
		opts: ServiceOpts{
			toolCreators: make(map[string]memory.ToolCreator),
			enabledTools: make(map[string]bool),
		},
		db:          storage.WrapSQLDB(db),
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	mockTool1 := &mockTool{name: "tool1"}
	mockTool2 := &mockTool{name: "tool2"}

	s.opts.toolCreators["tool1"] = func() tool.Tool { return mockTool1 }
	s.opts.toolCreators["tool2"] = func() tool.Tool { return mockTool2 }
	s.opts.enabledTools["tool1"] = true
	s.opts.enabledTools["tool2"] = true

	tools := s.Tools()
	assert.Len(t, tools, 2)

	assert.Len(t, s.cachedTools, 2)

	tools2 := s.Tools()
	assert.Len(t, tools2, 2)
	assert.Equal(t, tools[0], tools2[0])

	s.opts.enabledTools["tool2"] = false
	s.cachedTools = make(map[string]tool.Tool)
	tools3 := s.Tools()
	assert.Len(t, tools3, 1)
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

// TestInitTable_Success tests successful table initialization.
func TestInitTable_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.initTable(ctx)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInitTable_Error tests error handling when table creation fails.
func TestInitTable_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	s := setupMockService(t, db)

	ctx := context.Background()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnError(errors.New("create table error"))

	err := s.initTable(ctx)
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
