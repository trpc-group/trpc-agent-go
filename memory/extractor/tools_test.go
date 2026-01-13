//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestBackgroundTools(t *testing.T) {
	// All four background tools should be included.
	require.Len(t, backgroundTools, 4)
	assert.Contains(t, backgroundTools, memory.AddToolName)
	assert.Contains(t, backgroundTools, memory.UpdateToolName)
	assert.Contains(t, backgroundTools, memory.DeleteToolName)
	assert.Contains(t, backgroundTools, memory.ClearToolName)

	// Verify each tool has a valid declaration.
	for name, tool := range backgroundTools {
		decl := tool.Declaration()
		require.NotNil(t, decl)
		assert.Equal(t, name, decl.Name)
	}
}

func TestDeclarationOnlyTool(t *testing.T) {
	for _, tool := range backgroundTools {
		decl := tool.Declaration()
		assert.NotNil(t, decl)
		assert.NotEmpty(t, decl.Name)
		assert.NotEmpty(t, decl.Description)
	}
}

func TestParseToolCallArgs_Add(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected *Operation
	}{
		{
			name: "valid add with topics",
			args: map[string]any{
				"memory": "User likes coffee.",
				"topics": []any{"preferences", "food"},
			},
			expected: &Operation{
				Type:   OperationAdd,
				Memory: "User likes coffee.",
				Topics: []string{"preferences", "food"},
			},
		},
		{
			name: "valid add without topics",
			args: map[string]any{
				"memory": "User likes tea.",
			},
			expected: &Operation{
				Type:   OperationAdd,
				Memory: "User likes tea.",
				Topics: []string{},
			},
		},
		{
			name: "empty memory",
			args: map[string]any{
				"memory": "",
			},
			expected: nil,
		},
		{
			name:     "missing memory",
			args:     map[string]any{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := parseToolCallArgs(memory.AddToolName, tt.args)
			assert.Equal(t, tt.expected, op)
		})
	}
}

func TestParseToolCallArgs_Update(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected *Operation
	}{
		{
			name: "valid update",
			args: map[string]any{
				"memory_id": "mem-123",
				"memory":    "User now prefers tea.",
				"topics":    []any{"preferences"},
			},
			expected: &Operation{
				Type:     OperationUpdate,
				MemoryID: "mem-123",
				Memory:   "User now prefers tea.",
				Topics:   []string{"preferences"},
			},
		},
		{
			name: "missing memory_id",
			args: map[string]any{
				"memory": "User now prefers tea.",
			},
			expected: nil,
		},
		{
			name: "missing memory",
			args: map[string]any{
				"memory_id": "mem-123",
			},
			expected: nil,
		},
		{
			name: "empty memory_id",
			args: map[string]any{
				"memory_id": "",
				"memory":    "test",
			},
			expected: nil,
		},
		{
			name: "empty memory",
			args: map[string]any{
				"memory_id": "mem-123",
				"memory":    "",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := parseToolCallArgs(memory.UpdateToolName, tt.args)
			assert.Equal(t, tt.expected, op)
		})
	}
}

func TestParseToolCallArgs_Delete(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected *Operation
	}{
		{
			name: "valid delete",
			args: map[string]any{
				"memory_id": "mem-456",
			},
			expected: &Operation{
				Type:     OperationDelete,
				MemoryID: "mem-456",
			},
		},
		{
			name:     "missing memory_id",
			args:     map[string]any{},
			expected: nil,
		},
		{
			name: "empty memory_id",
			args: map[string]any{
				"memory_id": "",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := parseToolCallArgs(memory.DeleteToolName, tt.args)
			assert.Equal(t, tt.expected, op)
		})
	}
}

func TestParseToolCallArgs_Clear(t *testing.T) {
	// Clear tool takes no arguments.
	op := parseToolCallArgs(memory.ClearToolName, map[string]any{})
	require.NotNil(t, op)
	assert.Equal(t, OperationClear, op.Type)
}

func TestParseToolCallArgs_UnknownTool(t *testing.T) {
	args := map[string]any{
		"memory": "test",
	}
	op := parseToolCallArgs("unknown_tool", args)
	assert.Nil(t, op)
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "empty slice",
			input:    []any{},
			expected: []string{},
		},
		{
			name:     "string slice",
			input:    []any{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "mixed types",
			input:    []any{"a", 123, "b", true},
			expected: []string{"a", "b"},
		},
		{
			name:     "non-slice input",
			input:    "not a slice",
			expected: []string{},
		},
		{
			name:     "int slice",
			input:    []any{1, 2, 3},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toStringSlice(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
