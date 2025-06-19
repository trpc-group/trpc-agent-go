package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// Helper function to create Arguments from any struct.
func toArguments(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return json.RawMessage(b)
}

func TestFunctionTool_Run_Success(t *testing.T) {
	type Args struct {
		A int `json:"A"`
		B int `json:"B"`
	}
	fn := func(args Args) int {
		return args.A + args.B
	}
	tool := NewFunctionTool[Args](fn, FunctionToolConfig{
		Name:        "SumFunction",
		Description: "Calculates the sum of two integers.",
	})

	input := Args{A: 2, B: 3}
	args := toArguments(t, input)

	result, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum, ok := result.(int)
	if !ok {
		t.Fatalf("expected int result, got %T", result)
	}
	if sum != 5 {
		t.Errorf("expected 5, got %d", sum)
	}
}
