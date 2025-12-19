package codegen

import (
	"strings"
	"testing"
)

func TestCompileDirectAccess_NegativeIndex(t *testing.T) {
	steps := []celPathStep{
		{key: "items"},
		{isIndex: true, index: -1},
	}
	_, err := compileDirectAccess("state", steps)
	if err == nil {
		t.Fatal("expected error for negative index, got nil")
	}
	if !strings.Contains(err.Error(), "negative index") {
		t.Errorf("expected error to mention 'negative index', got: %v", err)
	}
}

func TestCompileDirectAccess_IndexThenField(t *testing.T) {
	// Test: arr[0].field should generate proper type assertion
	steps := []celPathStep{
		{key: "arr"},
		{isIndex: true, index: 0},
		{key: "field"},
	}
	expr, err := compileDirectAccess("state", steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After index access, field access should have type assertion
	if !strings.Contains(expr.code, ".(map[string]any)") {
		t.Errorf("expected type assertion after index, got: %s", expr.code)
	}
	// Should contain the field access
	if !strings.Contains(expr.code, `["field"]`) {
		t.Errorf("expected field access, got: %s", expr.code)
	}
}

func TestCompileDirectAccess_NestedFields(t *testing.T) {
	// Test: a.b.c should have type assertions for ALL non-first steps
	steps := []celPathStep{
		{key: "a"},
		{key: "b"},
		{key: "c"},
	}
	expr, err := compileDirectAccess("state", steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both "b" and "c" should have type assertions
	if !strings.Contains(expr.code, `.(map[string]any)["b"]`) {
		t.Errorf("expected type assertion for field b, got: %s", expr.code)
	}
	if !strings.Contains(expr.code, `.(map[string]any)["c"]`) {
		t.Errorf("expected type assertion for field c, got: %s", expr.code)
	}
	// Expected: state["a"].(map[string]any)["b"].(map[string]any)["c"]
	expected := `state["a"].(map[string]any)["b"].(map[string]any)["c"]`
	if expr.code != expected {
		t.Errorf("expected %s, got: %s", expected, expr.code)
	}
}

func TestCompileCELLiteToGoValue_HasToolCallsRejected(t *testing.T) {
	_, err := compileCELLiteToGoValue("has_tool_calls()")
	if err == nil {
		t.Fatal("expected error for has_tool_calls(), got nil")
	}
	if !strings.Contains(err.Error(), "has_tool_calls") {
		t.Errorf("expected error to mention 'has_tool_calls', got: %v", err)
	}
}
