//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTool implements the Tool interface for testing
type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Declaration() *Declaration {
	return &Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

// mockToolSet implements the ToolSet interface for testing
type mockToolSet struct {
	name  string
	tools []Tool
}

func (m *mockToolSet) Tools(ctx context.Context) []Tool {
	return m.tools
}

func (m *mockToolSet) Close() error {
	return nil
}

func (m *mockToolSet) Name() string {
	return m.name
}

func TestFilterTools_Include(t *testing.T) {
	// Create mock tools
	tools := []Tool{
		&mockTool{name: "echo", description: "Echo tool"},
		&mockTool{name: "add", description: "Add tool"},
		&mockTool{name: "multiply", description: "Multiply tool"},
		&mockTool{name: "divide", description: "Divide tool"},
	}

	// Create original toolset
	original := &mockToolSet{
		name:  "test-toolset",
		tools: tools,
	}

	t.Run("filter by specific names", func(t *testing.T) {
		// Filter tools by names
		filtered := FilterTools(original, Include("echo", "multiply"))

		// Get filtered tools
		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Verify results
		require.Len(t, result, 2)
		assert.Equal(t, "echo", result[0].Declaration().Name)
		assert.Equal(t, "multiply", result[1].Declaration().Name)
		assert.Equal(t, "test-toolset", filtered.Name())
	})

	t.Run("filter by empty names", func(t *testing.T) {
		// Filter with empty names list
		filtered := FilterTools(original, Include())

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should return empty result
		require.Len(t, result, 0)
	})

	t.Run("filter by non-existent names", func(t *testing.T) {
		// Filter by names that don't exist
		filtered := FilterTools(original, Include("nonexistent", "tool"))

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should return empty result
		require.Len(t, result, 0)
	})
}

func TestFilterTools_Exclude(t *testing.T) {
	// Create mock tools
	tools := []Tool{
		&mockTool{name: "echo", description: "Echo tool"},
		&mockTool{name: "add", description: "Add tool"},
		&mockTool{name: "multiply", description: "Multiply tool"},
		&mockTool{name: "divide", description: "Divide tool"},
	}

	// Create original toolset
	original := &mockToolSet{
		name:  "test-toolset",
		tools: tools,
	}

	t.Run("exclude specific names", func(t *testing.T) {
		// Exclude specific tools
		filtered := FilterTools(original, Exclude("multiply", "divide"))

		// Get filtered tools
		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Verify results - should have echo and add only
		require.Len(t, result, 2)
		assert.Equal(t, "echo", result[0].Declaration().Name)
		assert.Equal(t, "add", result[1].Declaration().Name)
		assert.Equal(t, "test-toolset", filtered.Name())
	})

	t.Run("exclude by empty names", func(t *testing.T) {
		// Exclude with empty names list - should return all tools
		filtered := FilterTools(original, Exclude())

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should return all tools
		require.Len(t, result, 4)
		assert.Equal(t, "echo", result[0].Declaration().Name)
		assert.Equal(t, "add", result[1].Declaration().Name)
		assert.Equal(t, "multiply", result[2].Declaration().Name)
		assert.Equal(t, "divide", result[3].Declaration().Name)
	})

	t.Run("exclude all tools", func(t *testing.T) {
		// Exclude all tools
		filtered := FilterTools(original, Exclude("echo", "add", "multiply", "divide"))

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should return empty result
		require.Len(t, result, 0)
	})

	t.Run("exclude non-existent names", func(t *testing.T) {
		// Exclude names that don't exist
		filtered := FilterTools(original, Exclude("nonexistent", "tool"))

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should return all tools since none match
		require.Len(t, result, 4)
	})
}

func TestFilterTools_CustomFilter(t *testing.T) {
	// Create mock tools
	tools := []Tool{
		&mockTool{name: "tool1", description: "Tool 1"},
		&mockTool{name: "tool2", description: "Tool 2"},
		&mockTool{name: "admin_tool", description: "Admin tool"},
		&mockTool{name: "test_tool", description: "Test tool"},
	}

	// Create original toolset
	original := &mockToolSet{
		name:  "test-toolset",
		tools: tools,
	}

	t.Run("filter with custom function", func(t *testing.T) {
		// Custom filter: exclude admin tools
		customFilter := func(name string) bool {
			return name != "admin_tool"
		}

		filtered := FilterTools(original, customFilter)

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Verify results
		require.Len(t, result, 3)
		assert.Equal(t, "tool1", result[0].Declaration().Name)
		assert.Equal(t, "tool2", result[1].Declaration().Name)
		assert.Equal(t, "test_tool", result[2].Declaration().Name)
	})

	t.Run("filter with complex custom function", func(t *testing.T) {
		// Custom filter: only tools with names longer than 6 characters
		customFilter := func(name string) bool {
			return len(name) > 6
		}

		filtered := FilterTools(original, customFilter)

		ctx := context.Background()
		result := filtered.Tools(ctx)

		// Should include admin_tool (10) and test_tool (9), exclude tool1 (5) and tool2 (5)
		require.Len(t, result, 2)
		names := make(map[string]bool)
		for _, tool := range result {
			names[tool.Declaration().Name] = true
		}
		assert.True(t, names["admin_tool"])
		assert.True(t, names["test_tool"])
	})
}

func TestFilteredToolSet_Interface(t *testing.T) {
	// Create mock tools
	tools := []Tool{
		&mockTool{name: "tool1", description: "Tool 1"},
		&mockTool{name: "tool2", description: "Tool 2"},
	}

	// Create original toolset
	original := &mockToolSet{
		name:  "test-toolset",
		tools: tools,
	}

	t.Run("implements ToolSet interface", func(t *testing.T) {
		filtered := FilterTools(original, Include("tool1"))

		// Verify it implements ToolSet interface
		var _ ToolSet = filtered

		// Test interface methods
		ctx := context.Background()
		result := filtered.Tools(ctx)
		require.Len(t, result, 1)
		assert.Equal(t, "tool1", result[0].Declaration().Name)

		assert.Equal(t, "test-toolset", filtered.Name())
		assert.NoError(t, filtered.Close())
	})

	t.Run("close delegates to original", func(t *testing.T) {
		// Create a toolset that returns an error on close
		errorToolSet := &mockToolSet{
			name:  "error-toolset",
			tools: tools,
		}

		filtered := FilterTools(errorToolSet, Include("tool1"))
		err := filtered.Close()
		assert.NoError(t, err)
	})
}

func TestFilterTools_Chaining(t *testing.T) {
	// Create mock tools
	tools := []Tool{
		&mockTool{name: "calc_add", description: "Add calculator"},
		&mockTool{name: "calc_multiply", description: "Multiply calculator"},
		&mockTool{name: "text_echo", description: "Echo text"},
		&mockTool{name: "text_reverse", description: "Reverse text"},
	}

	// Create original toolset
	original := &mockToolSet{
		name:  "test-toolset",
		tools: tools,
	}

	t.Run("chain multiple filters", func(t *testing.T) {
		// First filter by calc_ pattern
		firstFilter := FilterTools(original, Include("calc_add", "calc_multiply"))

		// Then filter by specific name
		finalFilter := FilterTools(firstFilter, Include("calc_multiply"))

		ctx := context.Background()
		result := finalFilter.Tools(ctx)

		// Should only have calc_multiply
		require.Len(t, result, 1)
		assert.Equal(t, "calc_multiply", result[0].Declaration().Name)
		assert.Equal(t, "test-toolset", finalFilter.Name())
	})
}
