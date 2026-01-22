//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestBasic(t *testing.T) {
	// Basic test to ensure package compiles.
	var _ memory.Service = (*Service)(nil)
	t.Log("ClickHouse memory service interface implemented")
}

// mockMemoryExtractor is a mock implementation of extractor.MemoryExtractor.
type mockMemoryExtractor struct{}

func (m *mockMemoryExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (m *mockMemoryExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (m *mockMemoryExtractor) SetPrompt(prompt string) {}

func (m *mockMemoryExtractor) SetModel(m2 model.Model) {}

func (m *mockMemoryExtractor) Metadata() map[string]any {
	return nil
}

var _ storage.Client = (*mockClickHouseClient)(nil)

// mockClickHouseClient is a mock implementation of storage.Client for testing.
type mockClickHouseClient struct {
	execFunc           func(ctx context.Context, query string, args ...any) error
	queryFunc          func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	queryRowFunc       func(ctx context.Context, dest []any, query string, args ...any) error
	batchInsertFunc    func(ctx context.Context, query string, fn storage.BatchFn,
		opts ...driver.PrepareBatchOption) error
	asyncInsertFunc    func(ctx context.Context, query string, wait bool, args ...any) error
	closeFunc          func() error
	queryToStructFunc  func(ctx context.Context, dest any, query string, args ...any) error
	queryToStructsFunc func(ctx context.Context, dest any, query string, args ...any) error
}

func (m *mockClickHouseClient) Exec(ctx context.Context, query string, args ...any) error {
	if m.execFunc != nil {
		return m.execFunc(ctx, query, args...)
	}
	return nil
}

func (m *mockClickHouseClient) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockClickHouseClient) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	if m.queryRowFunc != nil {
		return m.queryRowFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockClickHouseClient) QueryToStruct(ctx context.Context, dest any, query string, args ...any) error {
	if m.queryToStructFunc != nil {
		return m.queryToStructFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockClickHouseClient) QueryToStructs(ctx context.Context, dest any, query string, args ...any) error {
	if m.queryToStructsFunc != nil {
		return m.queryToStructsFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockClickHouseClient) BatchInsert(ctx context.Context, query string,
	fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
	if m.batchInsertFunc != nil {
		return m.batchInsertFunc(ctx, query, fn, opts...)
	}
	return nil
}

func (m *mockClickHouseClient) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	if m.asyncInsertFunc != nil {
		return m.asyncInsertFunc(ctx, query, wait, args...)
	}
	return nil
}

func (m *mockClickHouseClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// mockRows is a mock implementation of driver.Rows for testing.
type mockRows struct {
	data     [][]any
	current  int
	scanFunc func(dest ...any) error
}

func (m *mockRows) Next() bool {
	if m.current < len(m.data) {
		m.current++
		return true
	}
	return false
}

func (m *mockRows) Scan(dest ...any) error {
	if m.scanFunc != nil {
		return m.scanFunc(dest...)
	}
	if m.current > 0 && m.current <= len(m.data) {
		row := m.data[m.current-1]
		for i, d := range dest {
			if i < len(row) {
				switch v := d.(type) {
				case *string:
					*v = row[i].(string)
				case *uint64:
					*v = row[i].(uint64)
				case *time.Time:
					*v = row[i].(time.Time)
				}
			}
		}
	}
	return nil
}

func (m *mockRows) Close() error {
	return nil
}

func (m *mockRows) Err() error {
	return nil
}

func (m *mockRows) Columns() []string {
	return nil
}

func (m *mockRows) ColumnTypes() []driver.ColumnType {
	return nil
}

func (m *mockRows) Totals(dest ...any) error {
	return nil
}

func (m *mockRows) ScanStruct(dest any) error {
	return nil
}

func TestService_AddMemory_Success(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}
	memoryStr := "User prefers dark mode"
	topics := []string{"preferences"}

	mockClient := &mockClickHouseClient{
		execFunc: func(ctx context.Context, query string, args ...any) error {
			assert.Contains(t, query, "INSERT INTO")
			assert.Contains(t, query, "memories")
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName: "memories",
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.AddMemory(ctx, userKey, memoryStr, topics)
	require.NoError(t, err)
}

func TestService_AddMemory_WithMemoryLimit(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}
	memoryStr := "User prefers dark mode"
	topics := []string{"preferences"}

	mockClient := &mockClickHouseClient{
		queryRowFunc: func(ctx context.Context, dest []any, query string, args ...any) error {
			// Return count = 10 (at limit).
			if len(dest) > 0 {
				if countPtr, ok := dest[0].(*uint64); ok {
					*countPtr = 10
				}
			}
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:   "memories",
			memoryLimit: 10,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.AddMemory(ctx, userKey, memoryStr, topics)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_UpdateMemory_Success(t *testing.T) {
	ctx := context.Background()
	memoryKey := memory.Key{
		AppName:  "test-app",
		UserID:   "user-123",
		MemoryID: "mem-456",
	}
	memoryStr := "Updated memory content"
	topics := []string{"updated"}

	now := time.Now()
	existingEntry := &memory.Entry{
		ID:      memoryKey.MemoryID,
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
		Memory: &memory.Memory{
			Memory:      "Old content",
			Topics:      []string{"old"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	existingData, _ := json.Marshal(existingEntry)

	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{string(existingData), now},
				},
			}, nil
		},
		execFunc: func(ctx context.Context, query string, args ...any) error {
			assert.Contains(t, query, "INSERT INTO")
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName: "memories",
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.UpdateMemory(ctx, memoryKey, memoryStr, topics)
	require.NoError(t, err)
}

func TestService_DeleteMemory_HardDelete(t *testing.T) {
	ctx := context.Background()
	memoryKey := memory.Key{
		AppName:  "test-app",
		UserID:   "user-123",
		MemoryID: "mem-456",
	}

	mockClient := &mockClickHouseClient{
		execFunc: func(ctx context.Context, query string, args ...any) error {
			assert.Contains(t, query, "ALTER TABLE")
			assert.Contains(t, query, "DELETE")
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:  "memories",
			softDelete: false,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.DeleteMemory(ctx, memoryKey)
	require.NoError(t, err)
}

func TestService_ReadMemories_Success(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem-456",
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
		Memory: &memory.Memory{
			Memory:      "Test memory",
			Topics:      []string{"test"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryData, _ := json.Marshal(entry)

	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{string(entryData)},
				},
			}, nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName: "memories",
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "Test memory", entries[0].Memory.Memory)
}

func TestService_SearchMemories_Success(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem-456",
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
		Memory: &memory.Memory{
			Memory:      "User prefers dark mode",
			Topics:      []string{"preferences"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryData, _ := json.Marshal(entry)

	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{string(entryData)},
				},
			}, nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName: "memories",
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	entries, err := svc.SearchMemories(ctx, userKey, "dark mode")
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "User prefers dark mode", entries[0].Memory.Memory)
}

func TestService_Tools(t *testing.T) {
	svc := &Service{
		precomputedTools: []tool.Tool{
			&mockTool{name: memory.SearchToolName},
		},
	}

	tools := svc.Tools()
	assert.Len(t, tools, 1)
	assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)
}

func TestService_Close(t *testing.T) {
	closeCalled := false
	mockClient := &mockClickHouseClient{
		closeFunc: func() error {
			closeCalled = true
			return nil
		},
	}

	svc := &Service{
		chClient: mockClient,
	}

	err := svc.Close()
	require.NoError(t, err)
	assert.True(t, closeCalled)
}

// mockTool is a mock implementation of tool.Tool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: fmt.Sprintf("Mock tool: %s", m.name),
	}
}

// TestServiceOpts_Clone tests the clone method of ServiceOpts.
func TestServiceOpts_Clone(t *testing.T) {
	original := ServiceOpts{
		dsn:          "clickhouse://localhost:9000/default",
		tableName:    "custom_table",
		memoryLimit:  100,
		softDelete:   true,
		toolCreators: map[string]memory.ToolCreator{memory.AddToolName: func() tool.Tool { return nil }},
		enabledTools: map[string]bool{memory.AddToolName: true},
	}

	cloned := original.clone()

	// Verify values are copied.
	assert.Equal(t, original.dsn, cloned.dsn)
	assert.Equal(t, original.tableName, cloned.tableName)
	assert.Equal(t, original.memoryLimit, cloned.memoryLimit)
	assert.Equal(t, original.softDelete, cloned.softDelete)

	// Verify maps are deep copied.
	cloned.toolCreators[memory.SearchToolName] = func() tool.Tool { return nil }
	cloned.enabledTools[memory.SearchToolName] = true
	assert.NotContains(t, original.toolCreators, memory.SearchToolName)
	assert.NotContains(t, original.enabledTools, memory.SearchToolName)
}

// TestWithClickHouseDSN tests the DSN option.
func TestWithClickHouseDSN(t *testing.T) {
	opts := ServiceOpts{}
	dsn := "clickhouse://user:pass@localhost:9000/testdb"
	WithClickHouseDSN(dsn)(&opts)
	assert.Equal(t, dsn, opts.dsn)
}

// TestWithClickHouseInstance tests the instance name option.
func TestWithClickHouseInstance(t *testing.T) {
	opts := ServiceOpts{}
	WithClickHouseInstance("my-instance")(&opts)
	assert.Equal(t, "my-instance", opts.instanceName)
}

// TestWithTableName tests the table name option.
func TestWithTableName(t *testing.T) {
	opts := ServiceOpts{}
	WithTableName("custom_memories")(&opts)
	assert.Equal(t, "custom_memories", opts.tableName)
}

// TestWithTableName_Invalid tests that invalid table name panics.
func TestWithTableName_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid table name")
	}()
	opts := ServiceOpts{}
	WithTableName("invalid-table-name")(&opts)
}

// TestWithSoftDelete tests the soft delete option.
func TestWithSoftDelete(t *testing.T) {
	opts := ServiceOpts{}
	WithSoftDelete(true)(&opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(&opts)
	assert.False(t, opts.softDelete)
}

// TestWithMemoryLimit tests the memory limit option.
func TestWithMemoryLimit(t *testing.T) {
	opts := ServiceOpts{}
	WithMemoryLimit(50)(&opts)
	assert.Equal(t, 50, opts.memoryLimit)
}

// TestWithCustomTool tests the custom tool option.
func TestWithCustomTool(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	creator := func() tool.Tool { return &mockTool{name: memory.AddToolName} }
	WithCustomTool(memory.AddToolName, creator)(&opts)

	assert.Contains(t, opts.toolCreators, memory.AddToolName)
	assert.True(t, opts.enabledTools[memory.AddToolName])

	// Test with invalid tool name (should do nothing).
	WithCustomTool("invalid_tool", creator)(&opts)
	assert.NotContains(t, opts.toolCreators, "invalid_tool")

	// Test with nil creator (should do nothing).
	WithCustomTool(memory.SearchToolName, nil)(&opts)
	assert.NotContains(t, opts.toolCreators, memory.SearchToolName)
}

// TestWithToolEnabled tests the tool enabled option.
func TestWithToolEnabled(t *testing.T) {
	opts := ServiceOpts{}

	WithToolEnabled(memory.AddToolName, true)(&opts)
	assert.True(t, opts.enabledTools[memory.AddToolName])
	assert.True(t, opts.userExplicitlySet[memory.AddToolName])

	WithToolEnabled(memory.AddToolName, false)(&opts)
	assert.False(t, opts.enabledTools[memory.AddToolName])

	// Test with invalid tool name (should do nothing).
	WithToolEnabled("invalid_tool", true)(&opts)
	assert.NotContains(t, opts.enabledTools, "invalid_tool")
}

// TestWithSkipDBInit tests the skip DB init option.
func TestWithSkipDBInit(t *testing.T) {
	opts := ServiceOpts{}
	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)

	WithSkipDBInit(false)(&opts)
	assert.False(t, opts.skipDBInit)
}

// TestWithTablePrefix tests the table prefix option.
func TestWithTablePrefix(t *testing.T) {
	opts := ServiceOpts{}
	WithTablePrefix("trpc")(&opts)
	assert.Equal(t, "trpc", opts.tablePrefix)

	// Empty prefix should clear.
	WithTablePrefix("")(&opts)
	assert.Equal(t, "", opts.tablePrefix)
}

// TestWithTablePrefix_Invalid tests that invalid prefix panics.
func TestWithTablePrefix_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid prefix")
	}()
	opts := ServiceOpts{}
	WithTablePrefix("invalid;prefix")(&opts)
}

// TestWithExtractor tests the extractor option.
func TestWithExtractor(t *testing.T) {
	opts := ServiceOpts{}
	extractor := &mockMemoryExtractor{}
	WithExtractor(extractor)(&opts)
	assert.Equal(t, extractor, opts.extractor)
}

// TestWithAsyncMemoryNum tests the async memory num option.
func TestWithAsyncMemoryNum(t *testing.T) {
	opts := ServiceOpts{}
	WithAsyncMemoryNum(5)(&opts)
	assert.Equal(t, 5, opts.asyncMemoryNum)

	// Test with invalid value (should use default).
	WithAsyncMemoryNum(0)(&opts)
	assert.Equal(t, 1, opts.asyncMemoryNum)

	WithAsyncMemoryNum(-1)(&opts)
	assert.Equal(t, 1, opts.asyncMemoryNum)
}

// TestWithMemoryQueueSize tests the memory queue size option.
func TestWithMemoryQueueSize(t *testing.T) {
	opts := ServiceOpts{}
	WithMemoryQueueSize(20)(&opts)
	assert.Equal(t, 20, opts.memoryQueueSize)

	// Test with invalid value (should use default).
	WithMemoryQueueSize(0)(&opts)
	assert.Equal(t, 10, opts.memoryQueueSize)
}

// TestWithMemoryJobTimeout tests the memory job timeout option.
func TestWithMemoryJobTimeout(t *testing.T) {
	opts := ServiceOpts{}
	timeout := 60 * time.Second
	WithMemoryJobTimeout(timeout)(&opts)
	assert.Equal(t, timeout, opts.memoryJobTimeout)
}

// TestWithExtraOptions tests the extra options.
func TestWithExtraOptions(t *testing.T) {
	opts := ServiceOpts{}
	WithExtraOptions("opt1", "opt2")(&opts)
	assert.Len(t, opts.extraOptions, 2)
}

// TestBuildFullTableName tests the table name building logic.
func TestBuildFullTableName(t *testing.T) {
	tests := []struct {
		prefix    string
		tableName string
		expected  string
	}{
		{"", "memories", "memories"},
		{"trpc", "memories", "trpc_memories"},
		{"trpc_", "memories", "trpc_memories"},
		{"app", "sessions", "app_sessions"},
	}

	for _, tc := range tests {
		result := buildFullTableName(tc.prefix, tc.tableName)
		assert.Equal(t, tc.expected, result,
			"prefix=%q, tableName=%q", tc.prefix, tc.tableName)
	}
}

// TestService_AddMemory_InvalidUserKey tests validation of user key.
func TestService_AddMemory_InvalidUserKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	// Empty AppName.
	err := svc.AddMemory(ctx, memory.UserKey{}, "test", nil)
	require.Error(t, err)

	// Empty UserID.
	err = svc.AddMemory(ctx, memory.UserKey{AppName: "app"}, "test", nil)
	require.Error(t, err)
}

// TestService_UpdateMemory_InvalidMemoryKey tests validation of memory key.
func TestService_UpdateMemory_InvalidMemoryKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	tests := []struct {
		name string
		key  memory.Key
	}{
		{"empty app name", memory.Key{}},
		{"empty user ID", memory.Key{AppName: "app"}},
		{"empty memory ID", memory.Key{AppName: "app", UserID: "user"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpdateMemory(ctx, tc.key, "test", nil)
			require.Error(t, err)
		})
	}
}

// TestService_UpdateMemory_NotFound tests update when memory doesn't exist.
func TestService_UpdateMemory_NotFound(t *testing.T) {
	ctx := context.Background()
	memoryKey := memory.Key{
		AppName:  "test-app",
		UserID:   "user-123",
		MemoryID: "non-existent",
	}

	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{data: [][]any{}}, nil
		},
	}

	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.UpdateMemory(ctx, memoryKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestService_DeleteMemory_InvalidMemoryKey tests validation of memory key.
func TestService_DeleteMemory_InvalidMemoryKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	err := svc.DeleteMemory(ctx, memory.Key{})
	require.Error(t, err)
}

// TestService_DeleteMemory_SoftDelete tests soft delete behavior.
func TestService_DeleteMemory_SoftDelete(t *testing.T) {
	ctx := context.Background()
	memoryKey := memory.Key{
		AppName:  "test-app",
		UserID:   "user-123",
		MemoryID: "mem-456",
	}

	now := time.Now()
	entry := &memory.Entry{
		ID:        memoryKey.MemoryID,
		AppName:   memoryKey.AppName,
		UserID:    memoryKey.UserID,
		Memory:    &memory.Memory{Memory: "test"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryData, _ := json.Marshal(entry)

	execCalled := false
	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{string(entryData), now},
				},
			}, nil
		},
		execFunc: func(ctx context.Context, query string, args ...any) error {
			execCalled = true
			assert.Contains(t, query, "INSERT INTO")
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:  "memories",
			softDelete: true,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.DeleteMemory(ctx, memoryKey)
	require.NoError(t, err)
	assert.True(t, execCalled)
}

// TestService_ClearMemories_InvalidUserKey tests validation of user key.
func TestService_ClearMemories_InvalidUserKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	err := svc.ClearMemories(ctx, memory.UserKey{})
	require.Error(t, err)
}

// TestService_ClearMemories_HardDelete tests hard delete behavior.
func TestService_ClearMemories_HardDelete(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	execCalled := false
	mockClient := &mockClickHouseClient{
		execFunc: func(ctx context.Context, query string, args ...any) error {
			execCalled = true
			assert.Contains(t, query, "ALTER TABLE")
			assert.Contains(t, query, "DELETE")
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:  "memories",
			softDelete: false,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)
	assert.True(t, execCalled)
}

// TestService_ClearMemories_SoftDelete tests soft delete behavior.
func TestService_ClearMemories_SoftDelete(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	now := time.Now()
	entry := &memory.Entry{
		ID:        "mem-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "test"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryData, _ := json.Marshal(entry)

	batchInsertCalled := false
	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{"mem-1", string(entryData), now},
				},
			}, nil
		},
		batchInsertFunc: func(ctx context.Context, query string, fn storage.BatchFn,
			opts ...driver.PrepareBatchOption) error {
			batchInsertCalled = true
			return nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:  "memories",
			softDelete: true,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)
	assert.True(t, batchInsertCalled)
}

// TestService_ReadMemories_InvalidUserKey tests validation of user key.
func TestService_ReadMemories_InvalidUserKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	_, err := svc.ReadMemories(ctx, memory.UserKey{}, 10)
	require.Error(t, err)
}

// TestService_ReadMemories_WithSoftDelete tests read with soft delete filter.
func TestService_ReadMemories_WithSoftDelete(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	queryCaptured := ""
	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			queryCaptured = query
			return &mockRows{data: [][]any{}}, nil
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:  "memories",
			softDelete: true,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	_, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Contains(t, queryCaptured, "deleted_at IS NULL")
}

// TestService_SearchMemories_InvalidUserKey tests validation of user key.
func TestService_SearchMemories_InvalidUserKey(t *testing.T) {
	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}
	ctx := context.Background()

	_, err := svc.SearchMemories(ctx, memory.UserKey{}, "query")
	require.Error(t, err)
}

// TestService_SearchMemories_NoMatch tests search with no matching results.
func TestService_SearchMemories_NoMatch(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	now := time.Now()
	entry := &memory.Entry{
		ID:      "mem-456",
		AppName: userKey.AppName,
		UserID:  userKey.UserID,
		Memory: &memory.Memory{
			Memory:      "User likes cats",
			Topics:      []string{"pets"},
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	entryData, _ := json.Marshal(entry)

	mockClient := &mockClickHouseClient{
		queryFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &mockRows{
				data: [][]any{
					{string(entryData)},
				},
			}, nil
		},
	}

	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	// Search for something not in the memory.
	entries, err := svc.SearchMemories(ctx, userKey, "dogs")
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

// TestService_AddMemory_CountQueryError tests error handling for count query.
func TestService_AddMemory_CountQueryError(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mockClient := &mockClickHouseClient{
		queryRowFunc: func(ctx context.Context, dest []any, query string, args ...any) error {
			return errors.New("database error")
		},
	}

	svc := &Service{
		opts: ServiceOpts{
			tableName:   "memories",
			memoryLimit: 10,
		},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check memory count failed")
}

// TestService_AddMemory_InsertError tests error handling for insert.
func TestService_AddMemory_InsertError(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	mockClient := &mockClickHouseClient{
		execFunc: func(ctx context.Context, query string, args ...any) error {
			return errors.New("insert error")
		},
	}

	svc := &Service{
		opts:        ServiceOpts{tableName: "memories"},
		chClient:    mockClient,
		tableName:   "memories",
		cachedTools: make(map[string]tool.Tool),
	}

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory entry failed")
}

// TestService_Close_WithAutoMemoryWorker tests close with auto memory worker.
func TestService_Close_WithAutoMemoryWorker(t *testing.T) {
	closeCalled := false
	mockClient := &mockClickHouseClient{
		closeFunc: func() error {
			closeCalled = true
			return nil
		},
	}

	svc := &Service{
		chClient: mockClient,
		// autoMemoryWorker is nil, which is fine.
	}

	err := svc.Close()
	require.NoError(t, err)
	assert.True(t, closeCalled)
}

// TestService_Close_NilClient tests close with nil client.
func TestService_Close_NilClient(t *testing.T) {
	svc := &Service{
		chClient: nil,
	}

	err := svc.Close()
	require.NoError(t, err)
}

// TestBuildCreateTableSQL tests the SQL generation for table creation.
func TestBuildCreateTableSQL(t *testing.T) {
	sql := buildCreateTableSQL("test_memories")
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS test_memories")
	assert.Contains(t, sql, "memory_id")
	assert.Contains(t, sql, "app_name")
	assert.Contains(t, sql, "user_id")
	assert.Contains(t, sql, "memory_data")
	assert.Contains(t, sql, "ReplacingMergeTree")
}
