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
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
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
