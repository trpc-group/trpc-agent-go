package cel

import (
	"fmt"
	"reflect"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var defaultEnv *celgo.Env

func init() {
	// Create a CEL environment with dynamic variables:
	//   - state: graph.State (map[string]any)
	//   - input: arbitrary JSON-like object (map/struct), typically the
	//            upstream node's structured output or node-local context.
	//   - nodes: view of per-node structured outputs (mirrors
	//            state["node_structured"]), allowing expressions such as
	//            nodes.classifier.output_parsed.classification.
	env, err := celgo.NewEnv(
		celgo.Variable("state", celgo.DynType),
		celgo.Variable("input", celgo.DynType),
		celgo.Variable("nodes", celgo.DynType),
		// has_tool_calls([state]) -> bool
		celgo.Function("has_tool_calls",
			celgo.Overload("has_tool_calls_no_args",
				[]*celgo.Type{}, celgo.BoolType,
				celgo.FunctionBinding(func(args ...ref.Val) ref.Val {
					// The actual state binding is injected in Eval/EvalBool via
					// the evaluation activation; however cel-go function bindings
					// do not receive the activation. To keep things simple and
					// avoid depending on internal APIs, we expose the canonical
					// form has_tool_calls(state) and implement only that. The
					// empty-args overload is left as a stub so expressions can
					// still be parsed, but it always returns false.
					return types.Bool(false)
				}),
			),
			celgo.Overload("has_tool_calls_state",
				[]*celgo.Type{celgo.DynType}, celgo.BoolType,
				celgo.FunctionBinding(func(args ...ref.Val) ref.Val {
					if len(args) != 1 {
						return types.Bool(false)
					}
					raw := args[0].Value()
					state, ok := raw.(map[string]any)
					if !ok {
						return types.Bool(false)
					}
					msgsAny, ok := state[graph.StateKeyMessages]
					if !ok || msgsAny == nil {
						return types.Bool(false)
					}
					msgs, ok := msgsAny.([]model.Message)
					if !ok || len(msgs) == 0 {
						return types.Bool(false)
					}
					last := msgs[len(msgs)-1]
					if len(last.ToolCalls) > 0 {
						return types.Bool(true)
					}
					return types.Bool(false)
				}),
			),
		),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create CEL environment: %v", err))
	}
	defaultEnv = env
}

// EvalBool evaluates a CEL expression that is expected to produce a boolean
// result. The "state" and "input" variables are available inside the
// expression and are typically bound to graph.State and a JSON-like object,
// respectively.
func EvalBool(expr string, state any, input any) (bool, error) {
	val, err := Eval(expr, state, input)
	if err != nil {
		return false, err
	}
	b, ok := val.(bool)
	if ok {
		return b, nil
	}
	// Handle CEL bool wrapper.
	if rv, ok := val.(ref.Val); ok {
		if bVal, ok := rv.(types.Bool); ok {
			return bool(bVal), nil
		}
	}
	return false, fmt.Errorf("cel: expression %q did not evaluate to bool (got %T)", expr, val)
}

// Eval evaluates a CEL expression and returns the resulting Go value. It
// supports primitive types, maps, lists, and other JSON-like structures.
func Eval(expr string, state any, input any) (any, error) {
	if expr == "" {
		return nil, fmt.Errorf("cel: expression is empty")
	}

	// Rewrite convenience calls has_tool_calls() to has_tool_calls(state)
	// so that DSL authors can omit the explicit state argument while the
	// underlying CEL function only needs to support the canonical form.
	// This is a simple textual rewrite and intentionally conservative.
	if expr == "has_tool_calls()" {
		expr = "has_tool_calls(state)"
	}

	ast, issues := defaultEnv.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel parse error: %w", issues.Err())
	}

	ast, issues = defaultEnv.Check(ast)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel type-check error: %w", issues.Err())
	}

	prg, err := defaultEnv.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel program build error: %w", err)
	}

	// Best-effort extraction of nodes view from state for convenience.
	nodes := any(nil)
	if m, ok := state.(map[string]any); ok {
		if ns, ok := m["node_structured"]; ok {
			nodes = ns
		}
	}

	out, _, err := prg.Eval(map[string]any{
		"state": state,
		"input": input,
		"nodes": nodes,
	})
	if err != nil {
		return nil, fmt.Errorf("cel eval error: %w", err)
	}

	return normalizeCELValue(out), nil
}

// normalizeCELValue converts CEL evaluation results into JSON-friendly Go
// values. It recursively:
//   - unwraps ref.Val into underlying Go values
//   - converts map keys to strings
//   - normalizes nested maps/slices.
func normalizeCELValue(v any) any {
	// Unwrap CEL values.
	if rv, ok := v.(ref.Val); ok {
		return normalizeCELValue(rv.Value())
	}

	if v == nil {
		return nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key().Interface()
			val := iter.Value().Interface()
			keyStr := fmt.Sprint(normalizeCELValue(k))
			out[keyStr] = normalizeCELValue(val)
		}
		return out
	case reflect.Slice, reflect.Array:
		n := rv.Len()
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = normalizeCELValue(rv.Index(i).Interface())
		}
		return out
	default:
		return v
	}
}
