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
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGenerateMemoryID(t *testing.T) {
	tests := []struct {
		name   string
		memory *memory.Memory
	}{
		{
			name: "memory with content only",
			memory: &memory.Memory{
				Memory: "test content",
			},
		},
		{
			name: "memory with content and topics",
			memory: &memory.Memory{
				Memory: "test content",
				Topics: []string{"topic1", "topic2"},
			},
		},
		{
			name: "memory with empty topics",
			memory: &memory.Memory{
				Memory: "test content",
				Topics: []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := generateMemoryID(tt.memory)
			assert.NotEmpty(t, id, "Generated memory ID should not be empty")
			// The ID is a hex encoding, so it should be even length.
			assert.Equal(t, 0, len(id)%2, "Generated memory ID should have even length")
		})
	}
}

func TestServiceOpts_Defaults(t *testing.T) {
	opts := ServiceOpts{}

	// Test default values.
	assert.Equal(t, 0, opts.memoryLimit, "Expected default memoryLimit to be 0")
	assert.Empty(t, opts.host, "Expected default host to be empty")
	assert.Empty(t, opts.instanceName, "Expected default instanceName to be empty")
	assert.False(t, opts.skipDBInit, "Expected default skipDBInit to be false")
	assert.Empty(t, opts.schema, "Expected default schema to be empty")
	// Note: toolCreators and enabledTools are nil by default in the zero value.
	// They get initialized when NewService is called.
}

func TestServiceOpts_WithMemoryLimit(t *testing.T) {
	opts := ServiceOpts{}
	limit := 500

	WithMemoryLimit(limit)(&opts)

	assert.Equal(t, limit, opts.memoryLimit)
}

func TestServiceOpts_WithHost(t *testing.T) {
	opts := ServiceOpts{}
	host := "localhost"

	WithHost(host)(&opts)

	assert.Equal(t, host, opts.host)
}

func TestServiceOpts_WithPort(t *testing.T) {
	opts := ServiceOpts{}
	port := 5432

	WithPort(port)(&opts)

	assert.Equal(t, port, opts.port)
}

func TestServiceOpts_WithUser(t *testing.T) {
	opts := ServiceOpts{}
	user := "testuser"

	WithUser(user)(&opts)

	assert.Equal(t, user, opts.user)
}

func TestServiceOpts_WithPassword(t *testing.T) {
	opts := ServiceOpts{}
	password := "testpass"

	WithPassword(password)(&opts)

	assert.Equal(t, password, opts.password)
}

func TestServiceOpts_WithDatabase(t *testing.T) {
	opts := ServiceOpts{}
	database := "testdb"

	WithDatabase(database)(&opts)

	assert.Equal(t, database, opts.database)
}

func TestServiceOpts_WithSSLMode(t *testing.T) {
	opts := ServiceOpts{}
	sslMode := "require"

	WithSSLMode(sslMode)(&opts)

	assert.Equal(t, sslMode, opts.sslMode)
}

func TestServiceOpts_WithSkipDBInit(t *testing.T) {
	opts := ServiceOpts{}

	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)

	WithSkipDBInit(false)(&opts)
	assert.False(t, opts.skipDBInit)
}

func TestServiceOpts_WithSchema(t *testing.T) {
	opts := ServiceOpts{}
	schema := "test_schema"

	WithSchema(schema)(&opts)

	assert.Equal(t, schema, opts.schema)
}

func TestServiceOpts_WithSchema_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid schema name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	opts := ServiceOpts{}
	WithSchema("invalid-schema-name")(&opts)
}

func TestServiceOpts_WithPostgresClientDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{
			name: "URL format",
			dsn:  "postgres://user:password@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "Key-Value format",
			dsn:  "host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithPostgresClientDSN(tt.dsn)(&opts)
			assert.Equal(t, tt.dsn, opts.dsn)
		})
	}
}

func TestServiceOpts_WithPostgresInstance(t *testing.T) {
	opts := ServiceOpts{}
	instanceName := "test-instance"

	WithPostgresInstance(instanceName)(&opts)

	assert.Equal(t, instanceName, opts.instanceName)
}

func TestServiceOpts_WithTableName(t *testing.T) {
	opts := ServiceOpts{}
	tableName := "custom_memories"

	WithTableName(tableName)(&opts)

	assert.Equal(t, tableName, opts.tableName)
}

func TestServiceOpts_WithTableName_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	opts := ServiceOpts{}
	WithTableName("invalid-table-name")(&opts)
}

func TestServiceOpts_WithCustomTool(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	toolName := memory.AddToolName
	creator := func() tool.Tool { return nil }

	WithCustomTool(toolName, creator)(&opts)

	assert.NotNil(t, opts.toolCreators[toolName], "Expected tool creator to be set")
	assert.True(t, opts.enabledTools[toolName], "Expected tool to be enabled")

	// Test with nil creator (should do nothing).
	WithCustomTool(memory.SearchToolName, nil)(&opts)

	assert.Nil(t, opts.toolCreators[memory.SearchToolName], "Expected nil creator not to be set")
	assert.False(t, opts.enabledTools[memory.SearchToolName], "Expected tool with nil creator not to be enabled")
}

func TestServiceOpts_WithToolEnabled(t *testing.T) {
	opts := ServiceOpts{
		enabledTools: make(map[string]bool),
	}

	toolName := memory.SearchToolName
	enabled := true

	WithToolEnabled(toolName, enabled)(&opts)

	assert.True(t, opts.enabledTools[toolName], "Expected tool to be enabled")

	// Test disabling.
	WithToolEnabled(toolName, false)(&opts)

	assert.False(t, opts.enabledTools[toolName], "Expected tool to be disabled")
}

func TestServiceOpts_InvalidToolName(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	invalidToolName := "invalid_tool"
	creator := func() tool.Tool { return nil }

	// Test WithCustomTool with invalid name.
	WithCustomTool(invalidToolName, creator)(&opts)

	assert.Nil(t, opts.toolCreators[invalidToolName], "Expected invalid tool creator not to be set")
	assert.False(t, opts.enabledTools[invalidToolName], "Expected invalid tool not to be enabled")

	// Test WithToolEnabled with invalid name.
	WithToolEnabled(invalidToolName, true)(&opts)

	assert.False(t, opts.enabledTools[invalidToolName], "Expected invalid tool not to be enabled")
}

func TestServiceOpts_CombinedOptions(t *testing.T) {
	opts := ServiceOpts{}

	// Apply multiple options.
	WithHost("localhost")(&opts)
	WithPort(5432)(&opts)
	WithUser("testuser")(&opts)
	WithPassword("testpass")(&opts)
	WithDatabase("testdb")(&opts)
	WithMemoryLimit(1000)(&opts)
	WithPostgresInstance("backup-instance")(&opts)

	// Verify all options are set correctly.
	assert.Equal(t, "localhost", opts.host)
	assert.Equal(t, 5432, opts.port)
	assert.Equal(t, "testuser", opts.user)
	assert.Equal(t, "testpass", opts.password)
	assert.Equal(t, "testdb", opts.database)
	assert.Equal(t, 1000, opts.memoryLimit)
	assert.Equal(t, "backup-instance", opts.instanceName)
}

func TestServiceOpts_ToolManagement(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	// Test enabling multiple tools.
	tools := []string{memory.AddToolName, memory.SearchToolName, memory.LoadToolName}
	for _, toolName := range tools {
		creator := func() tool.Tool { return nil }
		WithCustomTool(toolName, creator)(&opts)
	}

	// Verify all tools are enabled.
	for _, toolName := range tools {
		assert.True(t, opts.enabledTools[toolName], "Tool %s should be enabled", toolName)
		assert.NotNil(t, opts.toolCreators[toolName], "Tool creator for %s should be set", toolName)
	}

	// Test disabling a specific tool.
	WithToolEnabled(memory.SearchToolName, false)(&opts)
	assert.False(t, opts.enabledTools[memory.SearchToolName], "Search tool should be disabled")
}

func TestServiceOpts_EdgeCases(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	// Test with empty tool name.
	WithCustomTool("", func() tool.Tool { return nil })(&opts)
	assert.Empty(t, opts.toolCreators, "Empty tool name should not be added")

	// Test with very long tool name.
	longToolName := string(make([]byte, 1000))
	WithCustomTool(longToolName, func() tool.Tool { return nil })(&opts)
	assert.Empty(t, opts.toolCreators, "Very long tool name should not be added")

	// Test with zero memory limit.
	WithMemoryLimit(0)(&opts)
	assert.Equal(t, 0, opts.memoryLimit, "Zero memory limit should be allowed")

	// Test with negative memory limit.
	WithMemoryLimit(-100)(&opts)
	assert.Equal(t, -100, opts.memoryLimit, "Negative memory limit should be allowed")
}

func TestWithExtraOptions(t *testing.T) {
	opts := ServiceOpts{}
	opt1 := "option1"
	opt2 := "option2"

	WithExtraOptions(opt1, opt2)(&opts)

	assert.Len(t, opts.extraOptions, 2)
	assert.Equal(t, opt1, opts.extraOptions[0])
	assert.Equal(t, opt2, opts.extraOptions[1])

	// Test appending more options.
	opt3 := "option3"
	WithExtraOptions(opt3)(&opts)

	assert.Len(t, opts.extraOptions, 3)
	assert.Equal(t, opt3, opts.extraOptions[2])
}

func TestNewService_InstanceName(t *testing.T) {
	// Test with non-existent instance name.
	_, err := NewService(WithPostgresInstance("non-existent-instance"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres instance")
}

// --- End-to-end tests with testcontainers ---

func setupTestPostgres(t testing.TB) (string, func()) {
	t.Skip("Skipping integration tests - requires testcontainers setup")
	return "", func() {}
}

func newTestService(t *testing.T) (*Service, func()) {
	_, cleanup := setupTestPostgres(t)
	// For integration tests, we'll use the connection settings directly
	svc, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
	)
	require.NoError(t, err)
	return svc, func() {
		svc.Close()
		cleanup()
	}
}

func TestService_AddAndReadMemories(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	err := svc.AddMemory(ctx, userKey, "alpha", []string{"a"})
	require.NoError(t, err)
	// Sleep a tiny bit to ensure CreatedAt ordering differences.
	time.Sleep(1 * time.Millisecond)
	err = svc.AddMemory(ctx, userKey, "beta", []string{"b"})
	require.NoError(t, err)

	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Should be sorted by CreatedAt descending: latest first (beta then alpha).
	assert.Equal(t, "beta", entries[0].Memory.Memory)
	assert.Equal(t, "alpha", entries[1].Memory.Memory)
	// Basic fields.
	for _, e := range entries {
		assert.Equal(t, userKey.AppName, e.AppName)
		assert.Equal(t, userKey.UserID, e.UserID)
		assert.NotEmpty(t, e.ID)
		assert.False(t, e.CreatedAt.IsZero())
		assert.False(t, e.UpdatedAt.IsZero())
	}
}

func TestService_UpdateMemory(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Add then read to get ID.
	require.NoError(t, svc.AddMemory(ctx, userKey, "old", []string{"x"}))
	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	id := entries[0].ID

	// Update.
	memKey := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: id}
	require.NoError(t, svc.UpdateMemory(ctx, memKey, "new", []string{"y"}))

	entries, err = svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "new", entries[0].Memory.Memory)
	assert.Equal(t, []string{"y"}, entries[0].Memory.Topics)
}

func TestService_DeleteMemory(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "to-delete", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	memKey := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: entries[0].ID}
	require.NoError(t, svc.DeleteMemory(ctx, memKey))

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestService_ClearMemories(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "m1", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "m2", nil))

	require.NoError(t, svc.ClearMemories(ctx, userKey))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestService_SearchMemories(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "Alice likes coffee", []string{"profile"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "Bob plays tennis", []string{"sports"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "Coffee brewing tips", []string{"hobby"}))

	// Search by content.
	results, err := svc.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Search by topic.
	results, err = svc.SearchMemories(ctx, userKey, "sports")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Bob plays tennis", results[0].Memory.Memory)
}

func TestService_MemoryLimit(t *testing.T) {
	_, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()
	svc, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithMemoryLimit(1),
	)
	require.NoError(t, err)
	defer svc.Close()

	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	require.NoError(t, svc.AddMemory(ctx, userKey, "first", nil))
	err = svc.AddMemory(ctx, userKey, "second", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_Tools_DefaultEnabled(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	tools := svc.Tools()
	require.NotEmpty(t, tools)

	// Collect tool names and verify defaults include add/update/search/load.
	names := make(map[string]bool)
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			names[decl.Name] = true
		}
	}
	assert.True(t, names[memory.AddToolName])
	assert.True(t, names[memory.UpdateToolName])
	assert.True(t, names[memory.SearchToolName])
	assert.True(t, names[memory.LoadToolName])
	assert.False(t, names[memory.DeleteToolName])
	assert.False(t, names[memory.ClearToolName])
}

func TestService_InvalidKeys(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// AddMemory with empty app should fail.
	err := svc.AddMemory(ctx, memory.UserKey{AppName: "", UserID: "u"}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// UpdateMemory with empty memoryID should fail.
	err = svc.UpdateMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: ""}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrMemoryIDRequired, err)
}

func TestService_DeleteMemory_Errors(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with invalid key.
	err := svc.DeleteMemory(ctx, memory.Key{AppName: "", UserID: "u", MemoryID: "id"})
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty memory id.
	err = svc.DeleteMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: ""})
	require.Error(t, err)
	assert.Equal(t, memory.ErrMemoryIDRequired, err)

	// Test deleting non-existent memory (should not error).
	err = svc.DeleteMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: "non-existent"})
	require.NoError(t, err)
}

func TestService_ClearMemories_Errors(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with invalid key.
	err := svc.ClearMemories(ctx, memory.UserKey{AppName: "", UserID: "u"})
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	err = svc.ClearMemories(ctx, memory.UserKey{AppName: "a", UserID: ""})
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)

	// Test clearing non-existent user (should not error).
	err = svc.ClearMemories(ctx, memory.UserKey{AppName: "a", UserID: "non-existent"})
	require.NoError(t, err)
}

func TestService_ReadMemories_Errors(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with invalid key.
	_, err := svc.ReadMemories(ctx, memory.UserKey{AppName: "", UserID: "u"}, 10)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	_, err = svc.ReadMemories(ctx, memory.UserKey{AppName: "a", UserID: ""}, 10)
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)

	// Test reading non-existent user (should return empty list).
	entries, err := svc.ReadMemories(ctx, memory.UserKey{AppName: "a", UserID: "non-existent"}, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestService_SearchMemories_Errors(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with invalid key.
	_, err := svc.SearchMemories(ctx, memory.UserKey{AppName: "", UserID: "u"}, "query")
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	_, err = svc.SearchMemories(ctx, memory.UserKey{AppName: "a", UserID: ""}, "query")
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)

	// Test searching non-existent user (should return empty list).
	results, err := svc.SearchMemories(ctx, memory.UserKey{AppName: "a", UserID: "non-existent"}, "query")
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestService_UpdateMemory_Errors(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with invalid key.
	err := svc.UpdateMemory(ctx, memory.Key{AppName: "", UserID: "u", MemoryID: "id"}, "test", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test updating non-existent memory.
	err = svc.UpdateMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: "non-existent"}, "test", nil)
	require.Error(t, err)
}

func TestService_ReadMemoriesWithLimit(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Add multiple memories.
	for i := 0; i < 5; i++ {
		err := svc.AddMemory(ctx, userKey, fmt.Sprintf("memory %d", i), nil)
		require.NoError(t, err)
		time.Sleep(1 * time.Millisecond)
	}

	// Test with limit.
	entries, err := svc.ReadMemories(ctx, userKey, 3)
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	// Test without limit.
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 5)
}

func TestService_AddMemory_InvalidKey(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Test with empty user id.
	err := svc.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: ""}, "test", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_AddMemory_LimitError(t *testing.T) {
	_, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	svc, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithMemoryLimit(2),
	)
	require.NoError(t, err)
	defer svc.Close()

	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Add memories up to the limit.
	require.NoError(t, svc.AddMemory(ctx, userKey, "memory1", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "memory2", nil))

	// This should fail due to limit.
	err = svc.AddMemory(ctx, userKey, "memory3", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_Tools_Caching(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	// First call should create tools.
	tools1 := svc.Tools()
	require.NotEmpty(t, tools1)

	// Second call should return cached tools.
	tools2 := svc.Tools()
	require.NotEmpty(t, tools2)

	// Should be the same length.
	assert.Equal(t, len(tools1), len(tools2))
}

func TestService_Tools_DisabledTools(t *testing.T) {
	_, cleanup := setupTestPostgres(t)
	defer cleanup()

	// Disable a tool.
	svc, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithToolEnabled(memory.SearchToolName, false),
	)
	require.NoError(t, err)
	defer svc.Close()

	tools := svc.Tools()

	// Search tool should not be in the list.
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			assert.NotEqual(t, memory.SearchToolName, decl.Name)
		}
	}
}

// --- Unit tests with sqlmock ---

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return db, mock
}

// testClient wraps sql.DB to implement storage.Client interface
type testClient struct {
	db *sql.DB
}

func (c *testClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *testClient) Query(ctx context.Context, handler storage.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := handler(rows); err != nil {
		return err
	}
	return rows.Err()
}

func (c *testClient) Transaction(ctx context.Context, fn storage.TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *testClient) Close() error {
	return c.db.Close()
}

func setupMockService(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock, opts ...ServiceOpt) *Service {
	originalBuilder := storage.GetClientBuilder()

	// Create a test client that wraps sql.DB
	client := &testClient{db: db}

	// Set up builder to return our test client
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	// Check if skipDBInit is set in opts
	skipDBInit := false
	for _, opt := range opts {
		testOpts := &ServiceOpts{}
		opt(testOpts)
		if testOpts.skipDBInit {
			skipDBInit = true
			break
		}
	}

	if !skipDBInit {
		// Mock table creation
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
		// Mock index creation (3 indexes)
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Ensure host is set if not already set
	hasHost := false
	for _, opt := range opts {
		testOpts := &ServiceOpts{}
		opt(testOpts)
		if testOpts.host != "" {
			hasHost = true
			break
		}
	}
	if !hasHost {
		opts = append(opts, WithHost("localhost"))
	}

	svc, err := NewService(opts...)
	require.NoError(t, err)
	return svc
}

func TestServiceOpts_WithSoftDelete(t *testing.T) {
	opts := ServiceOpts{}

	WithSoftDelete(true)(&opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(&opts)
	assert.False(t, opts.softDelete)
}

func TestValidateTableName_Empty(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for empty table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "table name cannot be empty")
	}()

	opts := ServiceOpts{}
	WithTableName("")(&opts)
}

func TestValidateTableName_TooLong(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for too long table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "table name too long")
	}()

	opts := ServiceOpts{}
	longName := string(make([]byte, 64))
	WithTableName(longName)(&opts)
}

func TestNewService_WithHost(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table and index creation
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	service, err := NewService(WithHost("localhost"), WithPort(5432), WithDatabase("testdb"))
	require.NoError(t, err)
	assert.NotNil(t, service)

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}

func TestNewService_InitDBError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnError(fmt.Errorf("table creation failed"))

	_, err := NewService(WithHost("localhost"), WithPort(5432), WithDatabase("testdb"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")
}

func TestService_AddMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_WithLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_LimitExceeded(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_WithSoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT COUNT.*AND deleted_at IS NULL").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	entry := &memory.Entry{
		ID:      memoryKey.MemoryID,
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
		Memory:  &memory.Memory{Memory: "old content", Topics: []string{"old"}},
	}
	entryData, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryData))
	mock.ExpectExec("UPDATE.*SET memory_data").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	mock.ExpectQuery("SELECT memory_data").WillReturnError(sql.ErrNoRows)

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_WithSoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	entry := &memory.Entry{
		ID:      memoryKey.MemoryID,
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
		Memory:  &memory.Memory{Memory: "old content", Topics: []string{"old"}},
	}
	entryData, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data.*AND deleted_at IS NULL").WillReturnRows(sqlmock.NewRows([]string{"memory_data"}).AddRow(entryData))
	mock.ExpectExec("UPDATE.*AND deleted_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeleteMemory_HardDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSoftDelete(false), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.DeleteMemory(ctx, memoryKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeleteMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	mock.ExpectExec("UPDATE.*SET deleted_at.*AND deleted_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.DeleteMemory(ctx, memoryKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ClearMemories_HardDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSoftDelete(false), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 2))

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ClearMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectExec("UPDATE.*SET deleted_at.*AND deleted_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 2))

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	entry1 := &memory.Entry{
		ID:        "mem-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "content 1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	entry2 := &memory.Entry{
		ID:        "mem-2",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "content 2"},
		CreatedAt: time.Now().Add(time.Second),
		UpdatedAt: time.Now().Add(time.Second),
	}
	entry1Data, _ := json.Marshal(entry1)
	entry2Data, _ := json.Marshal(entry2)

	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).
			AddRow(entry2Data).
			AddRow(entry1Data),
	)

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_WithLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	entry := &memory.Entry{
		ID:        "mem-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "content 1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	entryData, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data.*LIMIT 1").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow(entryData),
	)

	entries, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_WithSoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT memory_data.*AND deleted_at IS NULL").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}),
	)

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 0)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	entry := &memory.Entry{
		ID:        "mem-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "coffee brewing tips", Topics: []string{"hobby"}},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	entryData, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow(entryData),
	)

	results, err := svc.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_WithSoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSoftDelete(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT memory_data.*AND deleted_at IS NULL").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}),
	)

	results, err := svc.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err)
	require.Len(t, results, 0)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_Tools(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	tools := svc.Tools()
	require.NotEmpty(t, tools)

	toolNames := make(map[string]bool)
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			toolNames[decl.Name] = true
		}
	}

	assert.True(t, toolNames[memory.AddToolName])
	assert.True(t, toolNames[memory.UpdateToolName])
	assert.True(t, toolNames[memory.SearchToolName])
	assert.True(t, toolNames[memory.LoadToolName])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_Close(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	mock.ExpectClose()

	err := svc.Close()
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeleteMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "", UserID: "u1", MemoryID: "mem-123"}

	err := svc.DeleteMemory(ctx, memoryKey)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ClearMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "", UserID: "u1"}

	err := svc.ClearMemories(ctx, userKey)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "", UserID: "u1"}

	_, err := svc.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "", UserID: "u1"}

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_ConnectionSettingsBuilderError(t *testing.T) {
	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, fmt.Errorf("connection failed")
	})
	defer storage.SetClientBuilder(originalBuilder)

	_, err := NewService(WithHost("localhost"), WithPort(5432), WithDatabase("testdb"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create postgres client failed")
}

func TestNewService_InstanceNameBuilderError(t *testing.T) {
	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, fmt.Errorf("connection failed")
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Register instance first
	storage.RegisterPostgresInstance("test-instance",
		storage.WithClientConnString("postgres://localhost:5432/testdb"),
	)

	_, err := NewService(WithPostgresInstance("test-instance"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create postgres client failed")
}

func TestNewService_ConnectionSettingsPriority(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	receivedConnString := ""
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		opts := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(opts)
		}
		receivedConnString = opts.ConnString
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	storage.RegisterPostgresInstance("test-instance",
		storage.WithClientConnString("postgres://localhost:5432/testdb"),
	)

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	service, err := NewService(
		WithHost("customhost"),
		WithPort(5433),
		WithDatabase("customdb"),
		WithPostgresInstance("test-instance"),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)
	assert.Contains(t, receivedConnString, "customhost", "direct connection settings should have priority over instanceName")
	assert.Contains(t, receivedConnString, "customdb", "direct connection settings should have priority over instanceName")

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}

func TestNewService_DSNPriority(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	receivedConnString := ""
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		opts := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(opts)
		}
		receivedConnString = opts.ConnString
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	dsn := "postgres://dsn-user:password@dsn-host:5432/dsndb?sslmode=disable"
	service, err := NewService(
		WithPostgresClientDSN(dsn),
		WithHost("other-host"),
		WithPort(5433),
		WithUser("other-user"),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, dsn, receivedConnString, "DSN should take priority over host settings")

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}

func TestService_AddMemory_CountQueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("database error"))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check memory count failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_InsertError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("insert failed"))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_SelectQueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	mock.ExpectQuery("SELECT memory_data").WillReturnError(fmt.Errorf("database error"))

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Return invalid JSON
	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow([]byte("invalid json")),
	)

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_UpdateError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	memoryKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	entry := &memory.Entry{
		ID:      memoryKey.MemoryID,
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
		Memory:  &memory.Memory{Memory: "old content", Topics: []string{"old"}},
	}
	entryData, _ := json.Marshal(entry)

	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow(entryData),
	)
	mock.ExpectExec("UPDATE.*SET memory_data").WillReturnError(fmt.Errorf("update failed"))

	err := svc.UpdateMemory(ctx, memoryKey, "new content", []string{"new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Return wrong number of columns to cause scan error
	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data", "extra_column"}).AddRow([]byte("{}"), "extra"),
	)

	_, err := svc.ReadMemories(ctx, userKey, 0)
	require.Error(t, err)
	// The error may be scan error or unmarshal error depending on implementation
	assert.Contains(t, err.Error(), "list memories failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Return invalid JSON
	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow([]byte("invalid json")),
	)

	_, err := svc.ReadMemories(ctx, userKey, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_QueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	mock.ExpectQuery("SELECT memory_data").WillReturnError(fmt.Errorf("database error"))

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search memories failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Return wrong number of columns to cause scan error
	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data", "extra_column"}).AddRow([]byte("{}"), "extra"),
	)

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	// The error may be scan error or unmarshal error depending on implementation
	assert.Contains(t, err.Error(), "search memories failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_UnmarshalError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Return invalid JSON
	mock.ExpectQuery("SELECT memory_data").WillReturnRows(
		sqlmock.NewRows([]string{"memory_data"}).AddRow([]byte("invalid json")),
	)

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal memory entry failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_Close_NilClient(t *testing.T) {
	svc := &Service{
		db: nil,
	}

	err := svc.Close()
	require.NoError(t, err)
}

func TestService_AddMemory_CountQueryNoRows(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// COUNT query returns no rows (should still work, count will be 0)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

// Test buildConnString function
func TestBuildConnString(t *testing.T) {
	tests := []struct {
		name     string
		opts     ServiceOpts
		expected string
	}{
		{
			name: "all fields set",
			opts: ServiceOpts{
				host:     "testhost",
				port:     5433,
				user:     "testuser",
				password: "testpass",
				database: "testdb",
				sslMode:  "require",
			},
			expected: "host=testhost port=5433 dbname=testdb sslmode=require user=testuser password=testpass",
		},
		{
			name: "default values",
			opts: ServiceOpts{
				host: "",
			},
			expected: "host=localhost port=5432 dbname=trpc-agent-go-pgmemory sslmode=disable",
		},
		{
			name: "without user and password",
			opts: ServiceOpts{
				host:     "testhost",
				port:     5432,
				database: "testdb",
				sslMode:  "disable",
			},
			expected: "host=testhost port=5432 dbname=testdb sslmode=disable",
		},
		{
			name: "with user only",
			opts: ServiceOpts{
				host:     "testhost",
				port:     5432,
				user:     "testuser",
				database: "testdb",
				sslMode:  "disable",
			},
			expected: "host=testhost port=5432 dbname=testdb sslmode=disable user=testuser",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildConnString(tt.opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test buildCreateTableSQL function
func TestBuildCreateTableSQL(t *testing.T) {
	tests := []struct {
		name      string
		schema    string
		tableName string
		expected  string
	}{
		{
			name:      "no schema",
			schema:    "",
			tableName: "memories",
			expected:  "memories",
		},
		{
			name:      "with schema",
			schema:    "test_schema",
			tableName: "memories",
			expected:  "test_schema.memories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCreateTableSQL(tt.schema, tt.tableName, sqlCreateMemoriesTable)
			assert.Contains(t, result, tt.expected)
			assert.Contains(t, result, "CREATE TABLE IF NOT EXISTS")
		})
	}
}

// Test buildCreateIndexSQL function
func TestBuildCreateIndexSQL(t *testing.T) {
	tests := []struct {
		name        string
		schema      string
		tableName   string
		indexSuffix string
		expected    string
	}{
		{
			name:        "no schema",
			schema:      "",
			tableName:   "memories",
			indexSuffix: "app_user",
			expected:    "memories",
		},
		{
			name:        "with schema",
			schema:      "test_schema",
			tableName:   "memories",
			indexSuffix: "app_user",
			expected:    "test_schema.memories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCreateIndexSQL(tt.schema, tt.tableName, tt.indexSuffix, sqlCreateMemoriesAppUserIndex)
			assert.Contains(t, result, tt.expected)
			assert.Contains(t, result, "CREATE INDEX IF NOT EXISTS")
		})
	}
}

// Test NewService with skipDBInit
func TestNewService_WithSkipDBInit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Should not expect any CREATE statements
	service, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}

// Test NewService with schema
func TestNewService_WithSchema(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table and index creation with schema
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS.*test_schema.memories").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	service, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithSchema("test_schema"),
	)
	require.NoError(t, err)
	assert.NotNil(t, service)
	assert.Contains(t, service.tableName, "test_schema")

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}

// Test initDB with index creation error
func TestInitDB_IndexCreationError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table creation success
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	// Mock first index creation error
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnError(fmt.Errorf("index creation failed"))

	_, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

// Test Service operations with schema
func TestService_WithSchema(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table and index creation
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS.*test_schema.memories").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	service, err := NewService(
		WithHost("localhost"),
		WithPort(5432),
		WithDatabase("testdb"),
		WithSchema("test_schema"),
		WithMemoryLimit(0), // Disable memory limit to avoid COUNT query
	)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Test AddMemory with schema-qualified table name
	mock.ExpectExec("INSERT INTO.*test_schema.memories").WillReturnResult(sqlmock.NewResult(0, 1))

	err = service.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

// Test generateMemoryID with different inputs
func TestGenerateMemoryID_DifferentContent(t *testing.T) {
	mem1 := &memory.Memory{Memory: "content1"}
	mem2 := &memory.Memory{Memory: "content2"}

	id1 := generateMemoryID(mem1)
	id2 := generateMemoryID(mem2)

	assert.NotEqual(t, id1, id2, "Different content should generate different IDs")
}

func TestGenerateMemoryID_DifferentTopics(t *testing.T) {
	mem1 := &memory.Memory{Memory: "content", Topics: []string{"topic1"}}
	mem2 := &memory.Memory{Memory: "content", Topics: []string{"topic2"}}

	id1 := generateMemoryID(mem1)
	id2 := generateMemoryID(mem2)

	assert.NotEqual(t, id1, id2, "Different topics should generate different IDs")
}

// TestNewService_FallbackToDefaultConnString tests the fallback branch when
// no DSN, host, or instanceName is provided.
func TestNewService_FallbackToDefaultConnString(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	receivedConnString := ""
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		opts := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(opts)
		}
		receivedConnString = opts.ConnString
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// Mock table and index creation
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	// Create service without DSN, host, or instanceName - should use default connection string
	service, err := NewService()
	require.NoError(t, err)
	assert.NotNil(t, service)

	// Verify that the default connection string was used
	assert.Contains(t, receivedConnString, "host=localhost", "Should use default host")
	assert.Contains(t, receivedConnString, "port=5432", "Should use default port")
	assert.Contains(t, receivedConnString, defaultDatabase, "Should use default database")
	assert.Contains(t, receivedConnString, "sslmode=disable", "Should use default sslmode")

	require.NoError(t, mock.ExpectationsWereMet())
	service.Close()
}
