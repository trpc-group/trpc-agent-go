package memory

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockMemoryService is a simple mock implementation for testing.
type mockMemoryService struct {
	tools []tool.Tool
}

func (m *mockMemoryService) AddMemory(ctx context.Context, userKey UserKey, memory string, topics []string) error {
	return nil
}

func (m *mockMemoryService) UpdateMemory(ctx context.Context, memoryKey Key, memory string, topics []string) error {
	return nil
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey Key) error {
	return nil
}

func (m *mockMemoryService) ClearMemories(ctx context.Context, userKey UserKey) error {
	return nil
}

func (m *mockMemoryService) ReadMemories(ctx context.Context, userKey UserKey, limit int) ([]*Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey UserKey, query string) ([]*Entry, error) {
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
				&mockTool{name: AddToolName},
				&mockTool{name: SearchToolName},
				&mockTool{name: LoadToolName},
				&mockTool{name: UpdateToolName},
				&mockTool{name: DeleteToolName},
				&mockTool{name: ClearToolName},
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
				&mockTool{name: AddToolName},
				&mockTool{name: SearchToolName},
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
					case SearchToolName:
						if !strings.Contains(instruction, "memory_search") {
							t.Error("Instruction should contain search guidance")
						}
					case LoadToolName:
						if !strings.Contains(instruction, "memory_load") {
							t.Error("Instruction should contain load guidance")
						}
					case UpdateToolName:
						if !strings.Contains(instruction, "memory_update") {
							t.Error("Instruction should contain update guidance")
						}
					case DeleteToolName:
						if !strings.Contains(instruction, "memory_delete") {
							t.Error("Instruction should contain delete guidance")
						}
					case ClearToolName:
						if !strings.Contains(instruction, "memory_clear") {
							t.Error("Instruction should contain clear guidance")
						}
					}
				}
			}
		})
	}
}
