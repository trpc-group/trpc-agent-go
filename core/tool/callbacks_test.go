package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func TestNewToolCallbacks(t *testing.T) {
	callbacks := tool.NewToolCallbacks()
	if callbacks == nil {
		t.Fatal("Expected non-nil ToolCallbacks")
	}
	if len(callbacks.BeforeTool) != 0 {
		t.Errorf("Expected empty BeforeTool slice, got %d", len(callbacks.BeforeTool))
	}
	if len(callbacks.AfterTool) != 0 {
		t.Errorf("Expected empty AfterTool slice, got %d", len(callbacks.AfterTool))
	}
}

func TestAddBeforeTool(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		return nil, false, nil
	}

	callbacks.AddBeforeTool(callback)

	if len(callbacks.BeforeTool) != 1 {
		t.Errorf("Expected 1 BeforeTool callback, got %d", len(callbacks.BeforeTool))
	}
}

func TestAddAfterTool(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		return nil, false, nil
	}

	callbacks.AddAfterTool(callback)

	if len(callbacks.AfterTool) != 1 {
		t.Errorf("Expected 1 AfterTool callback, got %d", len(callbacks.AfterTool))
	}
}

func TestRunBeforeTool_Empty(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if skip {
		t.Error("Expected skip to be false")
	}
}

func TestRunBeforeTool_Skip(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		return nil, true, nil
	}

	callbacks.AddBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if !skip {
		t.Error("Expected skip to be true")
	}
}

func TestRunBeforeTool_CustomResult(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedResult := map[string]string{"result": "custom"}

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		return expectedResult, false, nil
	}

	callbacks.AddBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["result"] != "custom" {
		t.Errorf("Expected result 'custom', got %s", result["result"])
	}
	if skip {
		t.Error("Expected skip to be false")
	}
}

func TestRunBeforeTool_Error(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedErr := "callback error"

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		return nil, false, tool.NewError(expectedErr)
	}

	callbacks.AddBeforeTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	if err == nil {
		t.Fatal("Expected error")
	}
	if err.Error() != expectedErr {
		t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if skip {
		t.Error("Expected skip to be false")
	}
}

func TestRunBeforeTool_MultipleCallbacks(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callCount := 0

	callback1 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		callCount++
		return nil, false, nil
	}

	callback2 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		callCount++
		return map[string]string{"result": "from-second"}, false, nil
	}

	callbacks.AddBeforeTool(callback1)
	callbacks.AddBeforeTool(callback2)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, args)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 callback calls, got %d", callCount)
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["result"] != "from-second" {
		t.Errorf("Expected result 'from-second', got %s", result["result"])
	}
	if skip {
		t.Error("Expected skip to be false")
	}
}

func TestRunAfterTool_Empty(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	result := map[string]string{"original": "result"}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, result, nil)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if override {
		t.Error("Expected override to be false")
	}
}

func TestRunAfterTool_Override(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedResult := map[string]string{"result": "overridden"}

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		return expectedResult, true, nil
	}

	callbacks.AddAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["result"] != "overridden" {
		t.Errorf("Expected result 'overridden', got %s", result["result"])
	}
	if !override {
		t.Error("Expected override to be true")
	}
}

func TestRunAfterTool_NoOverride(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		return map[string]string{"result": "not-overridden"}, false, nil
	}

	callbacks.AddAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if override {
		t.Error("Expected override to be false")
	}
}

func TestRunAfterTool_WithError(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		if runErr != nil {
			return map[string]string{"error": "handled"}, true, nil
		}
		return nil, false, nil
	}

	callbacks.AddAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}
	runErr := tool.NewError("tool execution error")

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, runErr)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["error"] != "handled" {
		t.Errorf("Expected result 'handled', got %s", result["error"])
	}
	if !override {
		t.Error("Expected override to be true")
	}
}

func TestRunAfterTool_Error(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	expectedErr := "callback error"

	callback := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		return nil, false, tool.NewError(expectedErr)
	}

	callbacks.AddAfterTool(callback)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	if err == nil {
		t.Fatal("Expected error")
	}
	if err.Error() != expectedErr {
		t.Errorf("Expected error '%s', got '%s'", expectedErr, err.Error())
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if override {
		t.Error("Expected override to be false")
	}
}

func TestRunAfterTool_MultipleCallbacks(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	callCount := 0

	callback1 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		callCount++
		return nil, false, nil
	}

	callback2 := func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		callCount++
		return map[string]string{"result": "from-second"}, true, nil
	}

	callbacks.AddAfterTool(callback1)
	callbacks.AddAfterTool(callback2)

	declaration := &tool.Declaration{
		Name:        "test-tool",
		Description: "A test tool",
	}

	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "test-tool", declaration, args, originalResult, nil)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 callback calls, got %d", callCount)
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["result"] != "from-second" {
		t.Errorf("Expected result 'from-second', got %s", result["result"])
	}
	if !override {
		t.Error("Expected override to be true")
	}
}

// Mock tool for testing
type MockTool struct {
	name        string
	description string
}

func (m *MockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

func TestToolCallbacks_Integration(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	// Add before callback that logs and modifies args
	callbacks.AddBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte) (any, bool, error) {
		if toolName == "skip-tool" {
			return map[string]string{"skipped": "true"}, true, nil
		}

		// Modify args for certain tools
		if toolName == "modify-args" {
			var args map[string]interface{}
			if err := json.Unmarshal(jsonArgs, &args); err != nil {
				return nil, false, err
			}
			args["modified"] = true
			return args, false, nil
		}

		return nil, false, nil
	})

	// Add after callback that logs and modifies results
	callbacks.AddAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, bool, error) {
		if runErr != nil {
			return map[string]string{"error": "handled"}, true, nil
		}

		if toolName == "override-result" {
			return map[string]string{"overridden": "true"}, true, nil
		}

		return nil, false, nil
	})

	// Test skip functionality
	declaration := &tool.Declaration{Name: "skip-tool", Description: "A tool to skip"}
	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "skip-tool", declaration, args)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !skip {
		t.Error("Expected skip to be true")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	// Test error handling
	declaration = &tool.Declaration{Name: "error-tool", Description: "A tool with error"}
	args = []byte(`{"test": "value"}`)
	runErr := tool.NewError("execution error")

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "error-tool", declaration, args, nil, runErr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !override {
		t.Error("Expected override to be true")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	// Test override functionality
	declaration = &tool.Declaration{Name: "override-result", Description: "A tool to override"}
	args = []byte(`{"test": "value"}`)
	originalResult := map[string]string{"original": "result"}

	customResult, override, err = callbacks.RunAfterTool(context.Background(), "override-result", declaration, args, originalResult, nil)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !override {
		t.Error("Expected override to be true")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil customResult")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["overridden"] != "true" {
		t.Errorf("Expected 'overridden': 'true', got %v", result)
	}
}

func TestToolCallbacks_EdgeCases(t *testing.T) {
	callbacks := tool.NewToolCallbacks()

	// Test with nil declaration
	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "test-tool", nil, args)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if skip {
		t.Error("Expected skip to be false")
	}

	// Test with nil args
	declaration := &tool.Declaration{Name: "test-tool", Description: "A test tool"}

	customResult, skip, err = callbacks.RunBeforeTool(context.Background(), "test-tool", declaration, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if skip {
		t.Error("Expected skip to be false")
	}

	// Test with empty tool name
	customResult, skip, err = callbacks.RunBeforeTool(context.Background(), "", declaration, args)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if customResult != nil {
		t.Errorf("Expected nil customResult, got %v", customResult)
	}
	if skip {
		t.Error("Expected skip to be false")
	}
}
