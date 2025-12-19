package codegen

import (
	"fmt"
	"strconv"
	"strings"
)

// This file implements "CEL-lite -> native Go" compilation for codegen.
//
// The generated Go code is intended to be readable: it does not embed a CEL
// evaluator. Instead, a small, explicit subset used by DSL examples is
// translated into ordinary Go expressions with inline type assertions.
//
// Design principles:
//   - Direct map/slice access via type assertions (e.g., state["x"].(map[string]any)["y"])
//   - Inline ensure* wrappers for safe type conversion when needed
//   - Silent zero-value on missing fields (not errors)
//   - Codegen-time error for unsupported syntax

type goKind int

const (
	goKindAny goKind = iota
	goKindString
	goKindNumber
	goKindBool
	goKindMap
)

func (k goKind) String() string {
	switch k {
	case goKindAny:
		return "any"
	case goKindString:
		return "string"
	case goKindNumber:
		return "number"
	case goKindBool:
		return "bool"
	case goKindMap:
		return "map"
	default:
		return "unknown"
	}
}

type goExpr struct {
	code string
	kind goKind
}

func parseCELLiteExpr(expr string) (celExpr, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("CEL expression is empty")
	}

	toks, err := lexCELLite(expr)
	if err != nil {
		return nil, err
	}
	p := &celLiteParser{src: expr, toks: toks}
	ast, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != celTokEOF {
		return nil, p.errf(p.peek(), "unexpected token %q", p.peek().text)
	}
	return ast, nil
}

// compileCELLiteToGoValue compiles a CEL-lite expression into a native Go value
// expression string.
func compileCELLiteToGoValue(expr string) (string, error) {
	ast, err := parseCELLiteExpr(expr)
	if err != nil {
		return "", err
	}
	c := &celNativeCompiler{}
	out, err := c.compile(ast)
	if err != nil {
		return "", err
	}
	return out.code, nil
}

// compileCELLiteToGoPredicate compiles a CEL-lite expression into a Go boolean
// expression string.
func compileCELLiteToGoPredicate(expr string) (string, error) {
	ast, err := parseCELLiteExpr(expr)
	if err != nil {
		return "", err
	}
	c := &celNativeCompiler{}
	out, err := c.compile(ast)
	if err != nil {
		return "", err
	}
	if out.kind != goKindBool {
		return "", fmt.Errorf("CEL-lite: predicate must evaluate to bool (got %s)", out.kind)
	}
	return out.code, nil
}

// extractStringEqualityPredicate recognizes expressions like:
//
//	input.output_parsed.foo == "bar"
//
// and returns (root, steps, literal, ok).
func extractStringEqualityPredicate(expr string) (root string, steps []celPathStep, lit string, ok bool, err error) {
	ast, err := parseCELLiteExpr(expr)
	if err != nil {
		return "", nil, "", false, err
	}
	bin, okBin := ast.(*celBinary)
	if !okBin || strings.TrimSpace(bin.op) != "==" {
		return "", nil, "", false, nil
	}

	if r, ok := bin.right.(*celStringLit); ok {
		root, steps, ok := flattenCELLitePath(bin.left)
		if !ok {
			return "", nil, "", false, nil
		}
		return root, steps, r.val, true, nil
	}
	if l, ok := bin.left.(*celStringLit); ok {
		root, steps, ok := flattenCELLitePath(bin.right)
		if !ok {
			return "", nil, "", false, nil
		}
		return root, steps, l.val, true, nil
	}

	return "", nil, "", false, nil
}

type celNativeCompiler struct{}

func (c *celNativeCompiler) compile(e celExpr) (goExpr, error) {
	// Fast path: a pure path expression.
	if root, steps, ok := flattenCELLitePath(e); ok {
		return compileNativePath(root, steps)
	}

	switch x := e.(type) {
	case *celIdent:
		// Note: "state", "input", "nodes" as standalone identifiers are handled by
		// flattenCELLitePath fast path above, so they won't reach here.
		return goExpr{}, fmt.Errorf("CEL-lite: unsupported identifier %q", x.name)
	case *celStringLit:
		return goExpr{code: strconv.Quote(x.val), kind: goKindString}, nil
	case *celNumberLit:
		return goExpr{code: formatFloat(x.val), kind: goKindNumber}, nil
	case *celBoolLit:
		if x.val {
			return goExpr{code: "true", kind: goKindBool}, nil
		}
		return goExpr{code: "false", kind: goKindBool}, nil
	case *celNullLit:
		return goExpr{code: "nil", kind: goKindAny}, nil
	case *celMapLit:
		parts := make([]string, 0, len(x.entries))
		for _, ent := range x.entries {
			v, err := c.compile(ent.value)
			if err != nil {
				return goExpr{}, err
			}
			parts = append(parts, fmt.Sprintf("%s: %s", strconv.Quote(ent.key), v.code))
		}
		return goExpr{code: fmt.Sprintf("map[string]any{%s}", strings.Join(parts, ", ")), kind: goKindMap}, nil
	case *celBinary:
		left, err := c.compile(x.left)
		if err != nil {
			return goExpr{}, err
		}
		right, err := c.compile(x.right)
		if err != nil {
			return goExpr{}, err
		}
		switch x.op {
		case "||":
			return goExpr{code: fmt.Sprintf("(%s || %s)", ensureBool(left), ensureBool(right)), kind: goKindBool}, nil
		case "+":
			// CEL-lite supports + for string concat and numeric addition.
			// Heuristic: if either side is a string, treat as concat.
			if left.kind == goKindString || right.kind == goKindString {
				return goExpr{code: fmt.Sprintf("(%s + %s)", ensureString(left), ensureString(right)), kind: goKindString}, nil
			}
			return goExpr{code: fmt.Sprintf("(%s + %s)", ensureNumber(left), ensureNumber(right)), kind: goKindNumber}, nil
		case "==", "!=", "<", "<=", ">", ">=":
			return compileComparison(x.op, left, right)
		default:
			return goExpr{}, fmt.Errorf("CEL-lite: unsupported operator %q", x.op)
		}
	case *celCall:
		switch x.name {
		case "string":
			if len(x.args) != 1 {
				return goExpr{}, fmt.Errorf("CEL-lite: string() expects 1 argument")
			}
			arg, err := c.compile(x.args[0])
			if err != nil {
				return goExpr{}, err
			}
			return goExpr{code: fmt.Sprintf("fmt.Sprint(%s)", arg.code), kind: goKindString}, nil
		case "has_tool_calls":
			// has_tool_calls() is not supported in codegen because the generated code
			// does not include the hasToolCalls helper function.
			return goExpr{}, fmt.Errorf("CEL-lite: has_tool_calls() is not supported in codegen")
		default:
			return goExpr{}, fmt.Errorf("CEL-lite: unsupported function %q", x.name)
		}
	default:
		return goExpr{}, fmt.Errorf("CEL-lite: unsupported expression type %T", e)
	}
}

func compileNativePath(root string, steps []celPathStep) (goExpr, error) {
	// Normalize state.node_structured.* to nodes.* to match DSL examples.
	if root == "state" && len(steps) > 0 && !steps[0].isIndex && steps[0].key == "node_structured" {
		root = "nodes"
		steps = steps[1:]
	}

	switch root {
	case "state":
		if len(steps) == 0 {
			return goExpr{code: "state", kind: goKindAny}, nil
		}
		// state.<field> - single level access uses direct map access for well-known keys
		if len(steps) == 1 && !steps[0].isIndex {
			key := steps[0].key
			switch key {
			case "user_input":
				return goExpr{code: "state[graph.StateKeyUserInput]", kind: goKindAny}, nil
			case "last_response":
				return goExpr{code: "state[graph.StateKeyLastResponse]", kind: goKindAny}, nil
			case "messages":
				return goExpr{code: "state[graph.StateKeyMessages]", kind: goKindAny}, nil
			default:
				return goExpr{code: fmt.Sprintf("state[%s]", strconv.Quote(key)), kind: goKindAny}, nil
			}
		}
		// state.<field>.<subfield>... - generate direct type assertion chain
		return compileDirectAccess("state", steps)

	case "input":
		if len(steps) == 0 {
			return goExpr{code: "parsedOutput", kind: goKindAny}, nil
		}
		// input.output_parsed.xxx -> parsedOutput["xxx"]
		// input.output_raw -> state["{nodeID}_output"] (handled by template)
		if len(steps) >= 1 && !steps[0].isIndex {
			if steps[0].key == "output_parsed" {
				// input.output_parsed.xxx -> parsedOutput["xxx"]
				if len(steps) == 1 {
					return goExpr{code: "parsedOutput", kind: goKindMap}, nil
				}
				// input.output_parsed.field -> parsedOutput["field"]
				remainingSteps := steps[1:]
				return compileDirectAccess("parsedOutput", remainingSteps)
			}
			if steps[0].key == "output_raw" {
				return goExpr{code: "rawOutput", kind: goKindString}, nil
			}
		}
		// Fallback for other input paths
		return compileDirectAccess("parsedOutput", steps)

	case "nodes":
		if len(steps) == 0 {
			return goExpr{code: "state", kind: goKindMap}, nil
		}
		// nodes.<node_id>.output_parsed.xxx -> state["{node_id}_parsed"].(map[string]any)["xxx"]
		if len(steps) >= 2 && !steps[0].isIndex {
			nodeID := steps[0].key
			if len(steps) >= 2 && steps[1].key == "output_parsed" {
				if len(steps) == 2 {
					return goExpr{code: fmt.Sprintf("state[%q]", nodeID+"_parsed"), kind: goKindMap}, nil
				}
				// nodes.{nodeID}.output_parsed.field
				code := fmt.Sprintf("state[%q].(map[string]any)", nodeID+"_parsed")
				remainingSteps := steps[2:]
				return compileDirectAccess(code, remainingSteps)
			}
			if len(steps) >= 2 && steps[1].key == "output_raw" {
				return goExpr{code: fmt.Sprintf("state[%q]", nodeID+"_output"), kind: goKindString}, nil
			}
		}
		// Fallback
		return compileDirectAccess("state", steps)

	default:
		return goExpr{}, fmt.Errorf("CEL-lite: unsupported root %q", root)
	}
}

// compileDirectAccess generates direct type assertion chain for map access.
// e.g., root["key1"].(map[string]any)["key2"].(map[string]any)["key3"]
//
// Note: The generated code assumes data comes from JSON unmarshal, which produces
// map[string]any for objects and []any for arrays. If the actual runtime data
// has different types (e.g., []string), the type assertion will panic.
func compileDirectAccess(rootVar string, steps []celPathStep) (goExpr, error) {
	if len(steps) == 0 {
		return goExpr{code: rootVar, kind: goKindAny}, nil
	}

	// Validate: negative indexes are not supported in Go slices.
	for _, step := range steps {
		if step.isIndex && step.index < 0 {
			return goExpr{}, fmt.Errorf("CEL-lite: negative index %d is not supported", step.index)
		}
	}

	code := rootVar
	for i, step := range steps {
		if step.isIndex {
			// Array index access - need type assertion for non-first steps
			if i > 0 {
				code = fmt.Sprintf("%s.([]any)[%d]", code, step.index)
			} else {
				code = fmt.Sprintf("%s[%d]", code, step.index)
			}
		} else {
			// Map key access - need type assertion for all non-first steps
			// because map[string]any returns any, which cannot be indexed directly
			if i > 0 {
				code = fmt.Sprintf("%s.(map[string]any)[%s]", code, strconv.Quote(step.key))
			} else {
				code = fmt.Sprintf("%s[%s]", code, strconv.Quote(step.key))
			}
		}
	}

	return goExpr{code: code, kind: goKindAny}, nil
}

func ensureString(e goExpr) string {
	switch e.kind {
	case goKindString:
		return e.code
	case goKindAny:
		// Generate direct type assertion instead of helper function
		return fmt.Sprintf("func() string { s, _ := %s.(string); return s }()", e.code)
	default:
		return fmt.Sprintf("fmt.Sprint(%s)", e.code)
	}
}

func ensureNumber(e goExpr) string {
	if e.kind == goKindNumber {
		return e.code
	}
	// Generate inline type switch for numeric conversion
	return fmt.Sprintf("func() float64 { switch x := %s.(type) { case float64: return x; case int: return float64(x); case int64: return float64(x); default: return 0 } }()", e.code)
}

func ensureBool(e goExpr) string {
	switch e.kind {
	case goKindBool:
		return e.code
	default:
		// Generate direct type assertion instead of helper function
		return fmt.Sprintf("func() bool { b, _ := %s.(bool); return b }()", e.code)
	}
}

func compileComparison(op string, left, right goExpr) (goExpr, error) {
	// Prefer string comparison when either side is a string.
	if left.kind == goKindString || right.kind == goKindString {
		return goExpr{code: fmt.Sprintf("(%s %s %s)", ensureString(left), op, ensureString(right)), kind: goKindBool}, nil
	}

	// Bool comparisons only for == and !=.
	if left.kind == goKindBool || right.kind == goKindBool {
		if op != "==" && op != "!=" {
			return goExpr{}, fmt.Errorf("CEL-lite: unsupported operator %q for bool", op)
		}
		return goExpr{code: fmt.Sprintf("(%s %s %s)", ensureBool(left), op, ensureBool(right)), kind: goKindBool}, nil
	}

	// Numeric comparisons.
	return goExpr{code: fmt.Sprintf("(%s %s %s)", ensureNumber(left), op, ensureNumber(right)), kind: goKindBool}, nil
}
