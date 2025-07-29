package memory

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CustomMemoryAddTool is a custom implementation for testing.
type CustomMemoryAddTool struct {
	*MemoryAddTool
	customBehavior string
}

// NewCustomMemoryAddTool creates a new custom memory add tool.
func NewCustomMemoryAddTool(memoryService memory.Service, appName string, userID string, customBehavior string) *CustomMemoryAddTool {
	return &CustomMemoryAddTool{
		MemoryAddTool:  NewMemoryAddTool(memoryService, appName, userID),
		customBehavior: customBehavior,
	}
}

// Declaration returns the tool declaration with custom description.
func (c *CustomMemoryAddTool) Declaration() *tool.Declaration {
	decl := c.MemoryAddTool.Declaration()
	decl.Description = "CUSTOM: " + decl.Description + " (" + c.customBehavior + ")"
	return decl
}

func TestNewMemoryTools_Default(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test default behavior (all tools).
	tools := NewMemoryTools(service, appName, userID)

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify tool types.
	_, ok := tools[0].(*MemoryAddTool)
	if !ok {
		t.Fatal("Expected MemoryAddTool")
	}

	_, ok = tools[1].(*MemoryUpdateTool)
	if !ok {
		t.Fatal("Expected MemoryUpdateTool")
	}

	_, ok = tools[2].(*MemoryDeleteTool)
	if !ok {
		t.Fatal("Expected MemoryDeleteTool")
	}

	_, ok = tools[3].(*MemoryClearTool)
	if !ok {
		t.Fatal("Expected MemoryClearTool")
	}

	_, ok = tools[4].(*MemorySearchTool)
	if !ok {
		t.Fatal("Expected MemorySearchTool")
	}

	_, ok = tools[5].(*MemoryLoadTool)
	if !ok {
		t.Fatal("Expected MemoryLoadTool")
	}
}

func TestNewMemoryTools_CustomTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test with custom add tool.
	customAddTool := NewCustomMemoryAddTool(service, appName, userID, "enhanced-logging")
	tools := NewMemoryTools(service, appName, userID, WithAddTool(customAddTool))

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom tool is used.
	customTool, ok := tools[0].(*CustomMemoryAddTool)
	if !ok {
		t.Fatal("Expected CustomMemoryAddTool")
	}

	if customTool.customBehavior != "enhanced-logging" {
		t.Fatalf("Expected custom behavior 'enhanced-logging', got %s", customTool.customBehavior)
	}

	// Verify other tools are still default.
	_, ok = tools[1].(*MemoryUpdateTool)
	if !ok {
		t.Fatal("Expected MemoryUpdateTool")
	}
}

func TestNewMemoryTools_MultipleCustomTools(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test multiple custom tools.
	customAddTool := NewCustomMemoryAddTool(service, appName, userID, "enhanced-logging")
	customSearchTool := NewCustomMemoryAddTool(service, appName, userID, "analytics")
	tools := NewMemoryTools(
		service, appName, userID,
		WithAddTool(customAddTool),
		WithSearchTool(customSearchTool),
	)

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom add tool is used.
	customTool, ok := tools[0].(*CustomMemoryAddTool)
	if !ok {
		t.Fatal("Expected CustomMemoryAddTool")
	}

	if customTool.customBehavior != "enhanced-logging" {
		t.Fatalf("Expected custom behavior 'enhanced-logging', got %s", customTool.customBehavior)
	}

	// Verify custom search tool is used.
	customSearchToolResult, ok := tools[4].(*CustomMemoryAddTool)
	if !ok {
		t.Fatal("Expected CustomMemoryAddTool for search")
	}

	if customSearchToolResult.customBehavior != "analytics" {
		t.Fatalf("Expected custom behavior 'analytics', got %s", customSearchToolResult.customBehavior)
	}

	// Verify other tools are still default.
	_, ok = tools[1].(*MemoryUpdateTool)
	if !ok {
		t.Fatal("Expected MemoryUpdateTool")
	}
}

func TestNewMemoryToolsOptions(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test direct options usage.
	options := NewMemoryToolsOptions(service, appName, userID)
	customAddTool := NewCustomMemoryAddTool(service, appName, userID, "direct-usage")
	options.AddTool = customAddTool

	tools := options.BuildTools()

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom tool is used.
	customTool, ok := tools[0].(*CustomMemoryAddTool)
	if !ok {
		t.Fatal("Expected CustomMemoryAddTool")
	}

	if customTool.customBehavior != "direct-usage" {
		t.Fatalf("Expected custom behavior 'direct-usage', got %s", customTool.customBehavior)
	}

	// Verify other tools are default.
	_, ok = tools[1].(*MemoryUpdateTool)
	if !ok {
		t.Fatal("Expected MemoryUpdateTool")
	}
}
