package cel

import "testing"

// TestEvalBool_SimpleScalars verifies EvalBool on basic boolean expressions.
func TestEvalBool_SimpleScalars(t *testing.T) {
	state := map[string]any{"flag": true, "count": 3}

	tests := []struct {
		name   string
		expr   string
		input  any
		expect bool
	}{
		{
			name:   "state flag true",
			expr:   "state.flag",
			expect: true,
		},
		{
			name:   "comparison on state",
			expr:   "state.count > 2",
			expect: true,
		},
		{
			name:   "comparison using input",
			expr:   "input.value == 42",
			input:  map[string]any{"value": 42},
			expect: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvalBool(tt.expr, state, tt.input)
			if err != nil {
				t.Fatalf("EvalBool(%q) returned error: %v", tt.expr, err)
			}
			if got != tt.expect {
				t.Fatalf("EvalBool(%q) = %v, want %v", tt.expr, got, tt.expect)
			}
		})
	}
}

// TestEval_ObjectAndArrayResult ensures Eval returns JSON-friendly Go values.
func TestEval_ObjectAndArrayResult(t *testing.T) {
	state := map[string]any{
		"user": "alice",
		"age":  30,
		"tags": []string{"dev", "golang"},
	}

	expr := `{ "user": state.user, "age": state.age, "tags": state.tags }`
	val, err := Eval(expr, state, nil)
	if err != nil {
		t.Fatalf("Eval(%q) returned error: %v", expr, err)
	}

	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("Eval(%q) result type = %T, want map[string]any", expr, val)
	}

	if obj["user"] != "alice" {
		t.Fatalf("user = %v, want alice", obj["user"])
	}

	switch v := obj["age"].(type) {
	case int:
		if v != 30 {
			t.Fatalf("age(int) = %d, want 30", v)
		}
	case int64:
		if v != 30 {
			t.Fatalf("age(int64) = %d, want 30", v)
		}
	case float64:
		if v != 30 {
			t.Fatalf("age(float64) = %v, want 30", v)
		}
	default:
		t.Fatalf("age type = %T, want numeric", obj["age"])
	}

	tags, ok := obj["tags"].([]any)
	if !ok {
		t.Fatalf("tags type = %T, want []any", obj["tags"])
	}
	if len(tags) != 2 || tags[0] != "dev" || tags[1] != "golang" {
		t.Fatalf("tags = %#v, want [\"dev\", \"golang\"]", tags)
	}
}

// TestEval_NodesView verifies that the special "nodes" variable mirrors
// state["node_structured"] in CEL expressions.
func TestEval_NodesView(t *testing.T) {
	state := map[string]any{
		"node_structured": map[string]any{
			"classifier": map[string]any{
				"output_parsed": map[string]any{
					"classification": "http",
					"score":          0.9,
				},
			},
		},
	}

	expr := `nodes.classifier.output_parsed.classification == "http" && nodes.classifier.output_parsed.score > 0.5`
	ok, err := EvalBool(expr, state, nil)
	if err != nil {
		t.Fatalf("EvalBool(%q) returned error: %v", expr, err)
	}
	if !ok {
		t.Fatalf("EvalBool(%q) = false, want true", expr)
	}
}

// TestEval_ErrorCases ensures obvious error conditions surface as Go errors.
func TestEval_ErrorCases(t *testing.T) {
	if _, err := Eval("", nil, nil); err == nil {
		t.Fatalf("Eval(empty) did not return error")
	}

	// Parse error.
	if _, err := Eval("state.", map[string]any{}, nil); err == nil {
		t.Fatalf("Eval(parse error) did not return error")
	}

	// Type error: EvalBool on non-boolean result.
	if _, err := EvalBool("1 + 2", map[string]any{}, nil); err == nil {
		t.Fatalf("EvalBool(non-bool) did not return error")
	}
}
