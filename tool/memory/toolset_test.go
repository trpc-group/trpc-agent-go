//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	memorypkg "trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestNewMemoryToolSet(t *testing.T) {
	service := newMockMemoryService()
	toolSet := NewMemoryToolSet(service)

	require.NotNil(t, toolSet, "Expected non-nil tool set")

	// Test that tools are lazily initialized.
	assert.Nil(t, toolSet.tools, "Expected tools to be nil before initialization")

	// Test that tools are created when requested.
	tools := toolSet.Tools(context.Background())
	assert.Len(t, tools, 6, "Expected 6 tools, got %d", len(tools))

	// Verify tool names.
	expectedNames := []string{
		memorypkg.AddToolName,
		memorypkg.UpdateToolName,
		memorypkg.DeleteToolName,
		memorypkg.ClearToolName,
		memorypkg.SearchToolName,
		memorypkg.LoadToolName,
	}

	for _, expectedName := range expectedNames {
		found := false
		for _, tool := range tools {
			if tool.Declaration().Name == expectedName {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected tool %s not found", expectedName)
	}
}

func TestMemoryToolSet_LazyInitialization(t *testing.T) {
	service := newMockMemoryService()
	toolSet := NewMemoryToolSet(service)

	// First call should initialize tools.
	tools1 := toolSet.Tools(context.Background())
	assert.Len(t, tools1, 6, "Expected 6 tools, got %d", len(tools1))

	// Second call should return the same tools (no re-initialization).
	tools2 := toolSet.Tools(context.Background())
	assert.Len(t, tools2, 6, "Expected 6 tools, got %d", len(tools2))

	// Verify that the same tool instances are returned.
	assert.Equal(t, &tools1[0], &tools2[0], "Expected same tool instances to be returned")
}

func TestMemoryToolSet_Close(t *testing.T) {
	service := newMockMemoryService()
	toolSet := NewMemoryToolSet(service)

	// Initialize tools.
	tools := toolSet.Tools(context.Background())
	assert.Len(t, tools, 6, "Expected 6 tools, got %d", len(tools))

	// Close the tool set.
	err := toolSet.Close()
	require.NoError(t, err, "Failed to close tool set")

	// Verify that tools are cleared.
	assert.Nil(t, toolSet.tools, "Expected tools to be nil after close")
	assert.False(t, toolSet.initialized, "Expected initialized flag to be false after close")
}
