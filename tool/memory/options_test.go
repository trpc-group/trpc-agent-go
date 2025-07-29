package memory

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CustomAddTool is a custom implementation for testing.
type CustomAddTool struct {
	*AddTool
	customBehavior string
}

// NewCustomAddTool creates a new custom memory add tool.
func NewCustomAddTool(memoryService memory.Service, appName string, userID string, customBehavior string) *CustomAddTool {
	return &CustomAddTool{
		AddTool:        NewAddTool(memoryService, appName, userID),
		customBehavior: customBehavior,
	}
}

// Declaration returns the tool declaration with custom description.
func (c *CustomAddTool) Declaration() *tool.Declaration {
	decl := c.AddTool.Declaration()
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
	_, ok := tools[0].(*AddTool)
	if !ok {
		t.Fatal("Expected AddTool")
	}

	_, ok = tools[1].(*UpdateTool)
	if !ok {
		t.Fatal("Expected UpdateTool")
	}

	_, ok = tools[2].(*DeleteTool)
	if !ok {
		t.Fatal("Expected DeleteTool")
	}

	_, ok = tools[3].(*ClearTool)
	if !ok {
		t.Fatal("Expected ClearTool")
	}

	_, ok = tools[4].(*SearchTool)
	if !ok {
		t.Fatal("Expected SearchTool")
	}

	_, ok = tools[5].(*LoadTool)
	if !ok {
		t.Fatal("Expected LoadTool")
	}
}

func TestNewMemoryTools_CustomTool(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test with custom add tool.
	customAddTool := NewCustomAddTool(service, appName, userID, "enhanced-logging")
	tools := NewMemoryTools(service, appName, userID, WithAddTool(customAddTool))

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom tool is used.
	customTool, ok := tools[0].(*CustomAddTool)
	if !ok {
		t.Fatal("Expected CustomAddTool")
	}

	if customTool.customBehavior != "enhanced-logging" {
		t.Fatalf("Expected custom behavior 'enhanced-logging', got %s", customTool.customBehavior)
	}

	// Verify other tools are still default.
	_, ok = tools[1].(*UpdateTool)
	if !ok {
		t.Fatal("Expected UpdateTool")
	}
}

func TestNewMemoryTools_MultipleCustomTools(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test multiple custom tools.
	customAddTool := NewCustomAddTool(service, appName, userID, "enhanced-logging")
	customSearchTool := NewCustomAddTool(service, appName, userID, "analytics")
	tools := NewMemoryTools(
		service, appName, userID,
		WithAddTool(customAddTool),
		WithSearchTool(customSearchTool),
	)

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom add tool is used.
	customTool, ok := tools[0].(*CustomAddTool)
	if !ok {
		t.Fatal("Expected CustomAddTool")
	}

	if customTool.customBehavior != "enhanced-logging" {
		t.Fatalf("Expected custom behavior 'enhanced-logging', got %s", customTool.customBehavior)
	}

	// Verify custom search tool is used.
	customSearchToolResult, ok := tools[4].(*CustomAddTool)
	if !ok {
		t.Fatal("Expected CustomAddTool for search")
	}

	if customSearchToolResult.customBehavior != "analytics" {
		t.Fatalf("Expected custom behavior 'analytics', got %s", customSearchToolResult.customBehavior)
	}

	// Verify other tools are still default.
	_, ok = tools[1].(*UpdateTool)
	if !ok {
		t.Fatal("Expected UpdateTool")
	}
}

func TestNewMemoryToolsOptions(t *testing.T) {
	service := inmemory.NewMemoryService()
	appName := "test-app"
	userID := "test-user"

	// Test direct options usage.
	options := NewMemoryToolsOptions(service, appName, userID)
	customAddTool := NewCustomAddTool(service, appName, userID, "direct-usage")
	options.AddTool = customAddTool

	tools := options.BuildTools()

	if len(tools) != 6 {
		t.Fatalf("Expected 6 tools, got %d", len(tools))
	}

	// Verify custom tool is used.
	customTool, ok := tools[0].(*CustomAddTool)
	if !ok {
		t.Fatal("Expected CustomAddTool")
	}

	if customTool.customBehavior != "direct-usage" {
		t.Fatalf("Expected custom behavior 'direct-usage', got %s", customTool.customBehavior)
	}

	// Verify other tools are default.
	_, ok = tools[1].(*UpdateTool)
	if !ok {
		t.Fatal("Expected UpdateTool")
	}
}
