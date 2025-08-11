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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockMemoryService is a simple mock implementation for testing.
type mockMemoryService struct {
	tools []tool.Tool
}

func (m *mockMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memory string, topics []string) error {
	return nil
}

func (m *mockMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, memory string, topics []string) error {
	return nil
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return nil
}

func (m *mockMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return nil
}

func (m *mockMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) Tools() []tool.Tool {
	return m.tools
}

// mockTool is a simple mock tool implementation.
type mockTool struct {
	name string
}

func (m *mockTool) Call(ctx context.Context, input []byte) ([]byte, error) {
	return nil, nil
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: m.name,
	}
}

func TestGenerateInstruction_Dynamic(t *testing.T) {
	tests := []struct {
		name     string
		tools    []tool.Tool
		expected []string
	}{
		{
			name: "all tools enabled",
			tools: []tool.Tool{
				&mockTool{name: memory.AddToolName},
				&mockTool{name: memory.SearchToolName},
				&mockTool{name: memory.LoadToolName},
				&mockTool{name: memory.UpdateToolName},
				&mockTool{name: memory.DeleteToolName},
				&mockTool{name: memory.ClearToolName},
			},
			expected: []string{
				"memory_add",
				"memory_search",
				"memory_load",
				"memory_update",
				"memory_delete",
				"memory_clear",
			},
		},
		{
			name: "only add and search",
			tools: []tool.Tool{
				&mockTool{name: memory.AddToolName},
				&mockTool{name: memory.SearchToolName},
			},
			expected: []string{
				"memory_add",
				"memory_search",
			},
		},
		{
			name:     "no tools enabled",
			tools:    []tool.Tool{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &mockMemoryService{tools: tt.tools}
			instruction := GenerateInstruction(mockService)

			// Check that the instruction contains the expected tool names.
			for _, expectedTool := range tt.expected {
				if !strings.Contains(instruction, expectedTool) {
					t.Errorf("Instruction should contain tool %s", expectedTool)
				}
			}

			// Check that the instruction contains tool-specific guidance.
			if len(tt.tools) > 0 {
				// Should contain the basic instruction.
				if !strings.Contains(instruction, "You have access to memory tools") {
					t.Error("Instruction should contain basic guidance")
				}

				// Check for specific tool guidance.
				for _, tool := range tt.tools {
					switch tool.Declaration().Name {
					case memory.SearchToolName:
						if !strings.Contains(instruction, "memory_search") {
							t.Error("Instruction should contain search guidance")
						}
					case memory.LoadToolName:
						if !strings.Contains(instruction, "memory_load") {
							t.Error("Instruction should contain load guidance")
						}
					case memory.UpdateToolName:
						if !strings.Contains(instruction, "memory_update") {
							t.Error("Instruction should contain update guidance")
						}
					case memory.DeleteToolName:
						if !strings.Contains(instruction, "memory_delete") {
							t.Error("Instruction should contain delete guidance")
						}
					case memory.ClearToolName:
						if !strings.Contains(instruction, "memory_clear") {
							t.Error("Instruction should contain clear guidance")
						}
					}
				}
			}
		})
	}
}

func TestIsValidToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"valid add tool", memory.AddToolName, true},
		{"valid update tool", memory.UpdateToolName, true},
		{"valid delete tool", memory.DeleteToolName, true},
		{"valid clear tool", memory.ClearToolName, true},
		{"valid search tool", memory.SearchToolName, true},
		{"valid load tool", memory.LoadToolName, true},
		{"invalid tool", "invalid_tool", false},
		{"empty tool", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidToolName(tt.toolName)
			if result != tt.expected {
				t.Errorf("IsValidToolName(%s) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}
