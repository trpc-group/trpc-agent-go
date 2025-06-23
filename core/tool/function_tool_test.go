package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFunctionTool_Run_Success(t *testing.T) {
	type inputArgs struct {
		A int `json:"A"`
		B int `json:"B"`
	}
	type outputArgs struct {
		Result int `json:"result"`
	}
	fn := func(args inputArgs) <-chan outputArgs {
		output := make(chan outputArgs, 1)
		go func() {
			defer close(output)
			// Simulate some processing
			output <- outputArgs{Result: args.A + args.B}
		}()
		return output
	}
	tool := NewFunctionTool(fn, FunctionToolConfig{
		Name:        "SumFunction",
		Description: "Calculates the sum of two integers.",
	})

	input := inputArgs{A: 2, B: 3}
	args := toArguments(t, input)

	resultCh, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result any
	for res := range resultCh {
		result = res
	}
	sum, ok := result.(outputArgs)
	if !ok {
		t.Fatalf("expected int result, got %T", result)
	}
	if sum.Result != 5 {
		t.Errorf("expected 5, got %d", sum)
	}
}

// Helper function to create Arguments from any struct.
func toArguments(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return json.RawMessage(b)
}
