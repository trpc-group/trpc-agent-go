//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
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

func TestGetUserMemKey(t *testing.T) {
	tests := []struct {
		name     string
		userKey  memory.UserKey
		expected string
	}{
		{
			name: "normal user key",
			userKey: memory.UserKey{
				AppName: "test-app",
				UserID:  "test-user",
			},
			expected: "mem:{test-app}:test-user",
		},
		{
			name: "user key with special characters",
			userKey: memory.UserKey{
				AppName: "my-app-123",
				UserID:  "user_456",
			},
			expected: "mem:{my-app-123}:user_456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := getUserMemKey(tt.userKey)
			assert.Equal(t, tt.expected, key)
		})
	}
}

func TestServiceOpts_Defaults(t *testing.T) {
	opts := ServiceOpts{}

	// Test default values.
	assert.Equal(t, 0, opts.memoryLimit, "Expected default memoryLimit to be 0")
	assert.Empty(t, opts.url, "Expected default url to be empty")
	assert.Empty(t, opts.instanceName, "Expected default instanceName to be empty")
	// Note: toolCreators and enabledTools are nil by default in the zero value.
	// They get initialized when NewService is called.
}

func TestServiceOpts_WithMemoryLimit(t *testing.T) {
	opts := ServiceOpts{}
	limit := 500

	WithMemoryLimit(limit)(&opts)

	assert.Equal(t, limit, opts.memoryLimit)
}

func TestServiceOpts_WithRedisClientURL(t *testing.T) {
	opts := ServiceOpts{}
	url := "redis://localhost:6379"

	WithRedisClientURL(url)(&opts)

	assert.Equal(t, url, opts.url)
}

func TestServiceOpts_WithRedisInstance(t *testing.T) {
	opts := ServiceOpts{}
	instanceName := "test-instance"

	WithRedisInstance(instanceName)(&opts)

	assert.Equal(t, instanceName, opts.instanceName)
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

func TestServiceOpts_WithCustomTool_NilCreator(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	// Test WithCustomTool with nil creator.
	WithCustomTool(memory.AddToolName, nil)(&opts)

	assert.Nil(t, opts.toolCreators[memory.AddToolName], "Expected nil creator not to be set")
	assert.False(t, opts.enabledTools[memory.AddToolName], "Expected tool with nil creator not to be enabled")
}

func TestServiceOpts_CombinedOptions(t *testing.T) {
	opts := ServiceOpts{}

	// Apply multiple options.
	WithRedisClientURL("redis://localhost:6379")(&opts)
	WithMemoryLimit(1000)(&opts)
	WithRedisInstance("backup-instance")(&opts)

	// Verify all options are set correctly.
	assert.Equal(t, "redis://localhost:6379", opts.url)
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

// --- End-to-end tests with miniredis ---

func setupTestRedis(t testing.TB) (string, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	cleanup := func() { mr.Close() }
	return "redis://" + mr.Addr(), cleanup
}

func newTestService(t *testing.T) (*Service, func()) {
	url, cleanup := setupTestRedis(t)
	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)
	return svc, func() {
		// No Close method for memory Service; just cleanup redis.
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
	url, cleanup := setupTestRedis(t)
	defer cleanup()
	svc, err := NewService(WithRedisClientURL(url), WithMemoryLimit(1))
	require.NoError(t, err)
	ctx := context.Background()
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
	_, err := NewService(WithRedisInstance("non-existent-instance"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis instance")
}

func TestNewService_ConnectionFailure(t *testing.T) {
	// Test with invalid Redis address that will fail on ping.
	// Use a non-routable IP address to ensure connection failure.
	invalidURL := "redis://255.255.255.255:6379"
	_, err := NewService(WithRedisClientURL(invalidURL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection test failed")
}

func TestNewService_ConnectionFailure_WrongHost(t *testing.T) {
	// Test with wrong hostname that will fail DNS lookup.
	wrongHostURL := "redis://wrong-host-that-does-not-exist:6379"
	_, err := NewService(WithRedisClientURL(wrongHostURL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection test failed")
}

func TestNewService_ConnectionSuccess(t *testing.T) {
	// Test with valid Redis connection (using miniredis).
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)
	assert.NotNil(t, svc)

	// Verify the service works by calling a method.
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.NotNil(t, entries)
}

func TestNewService_InstanceName_ConnectionFailure(t *testing.T) {
	// Register an instance with invalid Redis address that will fail on ping.
	storage.RegisterRedisInstance("test-instance-failure", storage.WithClientBuilderURL("redis://255.255.255.255:6379"))

	// Test with instance name that has invalid Redis address.
	_, err := NewService(WithRedisInstance("test-instance-failure"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection test failed")
}

func TestNewService_InstanceName_ConnectionSuccess(t *testing.T) {
	// Isolate global state to avoid affecting other tests.
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	instanceName := "test-instance-success"
	storage.RegisterRedisInstance(instanceName, storage.WithClientBuilderURL(url))

	// Test with instance name that has valid Redis connection.
	svc, err := NewService(WithRedisInstance(instanceName))
	require.NoError(t, err)
	assert.NotNil(t, svc)

	// Verify the service works by calling a method.
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.NotNil(t, entries)
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

func TestService_AddMemory_RedisError(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(url), WithMemoryLimit(2))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Add memories up to the limit
	require.NoError(t, svc.AddMemory(ctx, userKey, "memory1", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "memory2", nil))

	// This should fail due to limit
	err = svc.AddMemory(ctx, userKey, "memory3", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_SearchMemories_RedisNil(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Search for non-existent user should return empty list, not error
	userKey := memory.UserKey{AppName: "test-app", UserID: "non-existent"}
	results, err := svc.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestService_ReadMemories_RedisNil(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Read for non-existent user should return empty list, not error
	userKey := memory.UserKey{AppName: "test-app", UserID: "non-existent"}
	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestService_Tools_Caching(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	// First call should create tools
	tools1 := svc.Tools()
	require.NotEmpty(t, tools1)

	// Second call should return cached tools
	tools2 := svc.Tools()
	require.NotEmpty(t, tools2)

	// Should be the same length
	assert.Equal(t, len(tools1), len(tools2))
}

func TestService_Tools_DisabledTools(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	// Disable a tool
	svc, err := NewService(
		WithRedisClientURL(url),
		WithToolEnabled(memory.SearchToolName, false),
	)
	require.NoError(t, err)

	tools := svc.Tools()

	// Search tool should not be in the list
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			assert.NotEqual(t, memory.SearchToolName, decl.Name)
		}
	}
}

func TestService_UpdateMemory_UnmarshalError(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Add a memory
	require.NoError(t, svc.AddMemory(ctx, userKey, "original", nil))

	// Get the memory ID
	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Manually corrupt the data in Redis to trigger unmarshal error
	key := getUserMemKey(userKey)
	svc.redisClient.HSet(ctx, key, entries[0].ID, []byte("invalid json"))

	// Try to update - should get unmarshal error
	memKey := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: entries[0].ID}
	err = svc.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestService_ReadMemories_UnmarshalError(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Manually add corrupted data to Redis
	key := getUserMemKey(userKey)
	svc.redisClient.HSet(ctx, key, "corrupt-id", []byte("invalid json"))

	// Try to read - should get unmarshal error
	_, err = svc.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestService_SearchMemories_UnmarshalError(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Manually add corrupted data to Redis
	key := getUserMemKey(userKey)
	svc.redisClient.HSet(ctx, key, "corrupt-id", []byte("invalid json"))

	// Try to search - should get unmarshal error
	_, err = svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestService_Tools_PanicWithNilCreator(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test that DeleteToolName and ClearToolName can be enabled.
	// They should have creators now, so no panic should occur.
	svc, err := NewService(
		WithRedisClientURL(url),
		WithToolEnabled(memory.DeleteToolName, true),
		WithToolEnabled(memory.ClearToolName, true),
	)
	require.NoError(t, err)

	tools := svc.Tools()

	// DeleteToolName and ClearToolName should be in the tools list.
	foundDelete := false
	foundClear := false
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			if decl.Name == memory.DeleteToolName {
				foundDelete = true
			}
			if decl.Name == memory.ClearToolName {
				foundClear = true
			}
		}
	}
	assert.True(t, foundDelete, "DeleteToolName should be in tools")
	assert.True(t, foundClear, "ClearToolName should be in tools")
}

func TestService_Tools_DefaultToolsOnly(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with default settings.
	svc, err := NewService(WithRedisClientURL(url))
	require.NoError(t, err)

	tools := svc.Tools()

	// Collect tool names.
	names := make(map[string]bool)
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			names[decl.Name] = true
		}
	}

	// Default enabled tools: add, update, search, load.
	assert.True(t, names[memory.AddToolName], "AddToolName should be enabled by default")
	assert.True(t, names[memory.UpdateToolName], "UpdateToolName should be enabled by default")
	assert.True(t, names[memory.SearchToolName], "SearchToolName should be enabled by default")
	assert.True(t, names[memory.LoadToolName], "LoadToolName should be enabled by default")

	// Delete and Clear should NOT be enabled by default.
	assert.False(t, names[memory.DeleteToolName], "DeleteToolName should NOT be enabled by default")
	assert.False(t, names[memory.ClearToolName], "ClearToolName should NOT be enabled by default")
}

func TestUserScenario(t *testing.T) {
	url, cleanup := setupTestRedis(t)
	defer cleanup()

	// User's exact code from the screenshot that was causing panic.
	memoryService, err := NewService(
		WithRedisClientURL(url),
		WithToolEnabled(memory.LoadToolName, true),
		WithToolEnabled(memory.UpdateToolName, true),
		WithToolEnabled(memory.SearchToolName, true),
		WithToolEnabled(memory.AddToolName, true),
		WithToolEnabled(memory.DeleteToolName, true),
		WithToolEnabled(memory.ClearToolName, true),
	)
	require.NoError(t, err)

	// This should NOT panic - this was the original issue.
	tools := memoryService.Tools()
	require.NotEmpty(t, tools)

	// Verify all enabled tools are present.
	names := make(map[string]bool)
	for _, tool := range tools {
		if decl := tool.Declaration(); decl != nil {
			names[decl.Name] = true
		}
	}

	assert.True(t, names[memory.LoadToolName], "LoadToolName should be enabled")
	assert.True(t, names[memory.UpdateToolName], "UpdateToolName should be enabled")
	assert.True(t, names[memory.SearchToolName], "SearchToolName should be enabled")
	assert.True(t, names[memory.AddToolName], "AddToolName should be enabled")
	assert.True(t, names[memory.DeleteToolName], "DeleteToolName should be enabled")
	assert.True(t, names[memory.ClearToolName], "ClearToolName should be enabled")

	// Verify tools work correctly.
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}

	// Test add.
	err = memoryService.AddMemory(ctx, userKey, "test memory", []string{"test"})
	require.NoError(t, err)

	// Test read.
	entries, err := memoryService.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Test delete.
	err = memoryService.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	})
	require.NoError(t, err)

	// Test clear.
	err = memoryService.ClearMemories(ctx, userKey)
	require.NoError(t, err)
}
