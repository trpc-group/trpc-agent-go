package tool

import (
	"context"
	"fmt"
	"testing"
)

type mockTool struct {
	BaseTool
	executeFunc func(ctx context.Context, args map[string]interface{}) (*Result, error)
}

func (t *mockTool) Execute(ctx context.Context, args map[string]interface{}) (*Result, error) {
	if t.executeFunc != nil {
		return t.executeFunc(ctx, args)
	}
	return nil, fmt.Errorf("mock execution")
}

func newMockTool(name, description string) *mockTool {
	return &mockTool{
		BaseTool: *NewBaseTool(name, description, nil),
	}
}

func TestBaseTool_Name(t *testing.T) {
	tool := NewBaseTool("test-tool", "A test tool", nil)
	if tool.Name() != "test-tool" {
		t.Errorf("Expected name to be 'test-tool', got '%s'", tool.Name())
	}
}

func TestBaseTool_Description(t *testing.T) {
	tool := NewBaseTool("test-tool", "A test tool", nil)
	if tool.Description() != "A test tool" {
		t.Errorf("Expected description to be 'A test tool', got '%s'", tool.Description())
	}
}

func TestBaseTool_Parameters(t *testing.T) {
	params := map[string]interface{}{
		"foo": "bar",
	}
	tool := NewBaseTool("test-tool", "A test tool", params)
	if tool.Parameters()["foo"] != "bar" {
		t.Errorf("Expected parameter 'foo' to be 'bar', got '%v'", tool.Parameters()["foo"])
	}
}

func TestBaseTool_Execute(t *testing.T) {
	tool := NewBaseTool("test-tool", "A test tool", nil)
	_, err := tool.Execute(context.Background(), nil)
	if err == nil {
		t.Error("Expected error from BaseTool.Execute, got nil")
	}
}

func TestNewResult(t *testing.T) {
	result := NewResult("test")
	if result.Output != "test" {
		t.Errorf("Expected output to be 'test', got '%v'", result.Output)
	}
	if result.ContentType != "text/plain" {
		t.Errorf("Expected content type to be 'text/plain', got '%s'", result.ContentType)
	}
	if result.Metadata == nil {
		t.Error("Expected metadata to be initialized")
	}
}

func TestNewJSONResult(t *testing.T) {
	result := NewJSONResult(map[string]string{"key": "value"})
	if result.ContentType != "application/json" {
		t.Errorf("Expected content type to be 'application/json', got '%s'", result.ContentType)
	}
}

// stringerType implements fmt.Stringer for testing
type stringerType struct{}

func (s stringerType) String() string {
	return "stringer"
}

func TestResult_String(t *testing.T) {
	// Test with string output
	result := NewResult("test")
	if result.String() != "test" {
		t.Errorf("Expected string to be 'test', got '%s'", result.String())
	}

	// Test with bytes output
	result = NewResult([]byte("test"))
	if result.String() != "test" {
		t.Errorf("Expected string to be 'test', got '%s'", result.String())
	}

	// Test with stringer interface
	result = NewResult(stringerType{})
	if result.String() != "stringer" {
		t.Errorf("Expected string to be 'stringer', got '%s'", result.String())
	}

	// Test with other type
	result = NewResult(map[string]string{"key": "value"})
	if result.String() != `{"key":"value"}` {
		t.Errorf("Expected JSON string, got '%s'", result.String())
	}
}

func TestToolSet_Add(t *testing.T) {
	ts := NewToolSet()
	tool := newMockTool("test-tool", "A test tool")

	err := ts.Add(tool)
	if err != nil {
		t.Errorf("Unexpected error adding tool: %v", err)
	}

	// Test adding a tool with empty name
	emptyTool := newMockTool("", "Empty name")
	err = ts.Add(emptyTool)
	if err == nil {
		t.Error("Expected error adding tool with empty name, got nil")
	}

	// Test adding a duplicate tool
	dupTool := newMockTool("test-tool", "Duplicate name")
	err = ts.Add(dupTool)
	if err == nil {
		t.Error("Expected error adding tool with duplicate name, got nil")
	}
}

func TestToolSet_Get(t *testing.T) {
	ts := NewToolSet()
	tool := newMockTool("test-tool", "A test tool")
	_ = ts.Add(tool)

	// Test getting an existing tool
	gotTool, exists := ts.Get("test-tool")
	if !exists {
		t.Error("Expected tool to exist")
	}
	if gotTool.Name() != "test-tool" {
		t.Errorf("Expected tool name to be 'test-tool', got '%s'", gotTool.Name())
	}

	// Test getting a non-existent tool
	_, exists = ts.Get("non-existent")
	if exists {
		t.Error("Expected tool to not exist")
	}
}

func TestToolSet_Remove(t *testing.T) {
	ts := NewToolSet()
	tool := newMockTool("test-tool", "A test tool")
	_ = ts.Add(tool)

	// Remove the tool
	ts.Remove("test-tool")

	// Verify it's gone
	_, exists := ts.Get("test-tool")
	if exists {
		t.Error("Expected tool to be removed")
	}

	// Removing a non-existent tool should not error
	ts.Remove("non-existent")
}

func TestToolSet_List(t *testing.T) {
	ts := NewToolSet()
	tool1 := newMockTool("tool1", "Tool 1")
	tool2 := newMockTool("tool2", "Tool 2")
	_ = ts.Add(tool1)
	_ = ts.Add(tool2)

	tools := ts.List()
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}

	// Check that both tools are in the list
	var foundTool1, foundTool2 bool
	for _, tool := range tools {
		if tool.Name() == "tool1" {
			foundTool1 = true
		}
		if tool.Name() == "tool2" {
			foundTool2 = true
		}
	}
	if !foundTool1 {
		t.Error("Expected to find tool1 in the list")
	}
	if !foundTool2 {
		t.Error("Expected to find tool2 in the list")
	}
}

func TestToolSet_Names(t *testing.T) {
	ts := NewToolSet()
	tool1 := newMockTool("tool1", "Tool 1")
	tool2 := newMockTool("tool2", "Tool 2")
	_ = ts.Add(tool1)
	_ = ts.Add(tool2)

	names := ts.Names()
	if len(names) != 2 {
		t.Errorf("Expected 2 names, got %d", len(names))
	}

	// Check that both names are in the list
	var foundName1, foundName2 bool
	for _, name := range names {
		if name == "tool1" {
			foundName1 = true
		}
		if name == "tool2" {
			foundName2 = true
		}
	}
	if !foundName1 {
		t.Error("Expected to find tool1 in the names")
	}
	if !foundName2 {
		t.Error("Expected to find tool2 in the names")
	}
}

func TestToolSet_Size(t *testing.T) {
	ts := NewToolSet()
	if ts.Size() != 0 {
		t.Errorf("Expected size to be 0, got %d", ts.Size())
	}

	tool := newMockTool("test-tool", "A test tool")
	_ = ts.Add(tool)
	if ts.Size() != 1 {
		t.Errorf("Expected size to be 1, got %d", ts.Size())
	}

	ts.Remove("test-tool")
	if ts.Size() != 0 {
		t.Errorf("Expected size to be 0 after removal, got %d", ts.Size())
	}
} 