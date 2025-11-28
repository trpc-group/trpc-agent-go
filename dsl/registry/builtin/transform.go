//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package builtin

import (
	"context"
	"reflect"
	"strings"

	dslcel "trpc.group/trpc-go/trpc-agent-go/dsl/cel"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Register the Transform component so that it can be referenced from DSL
	// graphs as "builtin.transform".
	registry.MustRegister(&TransformComponent{})
}

// TransformComponent is a data reshaping component. It evaluates an expression
// into a new structured object and writes it into state as a regular output
// field so downstream nodes can consume it.
//
// The expression is a CEL expression evaluated in an environment that
// exposes:
//   - state: graph.State (global graph state)
//   - input: JSON-like object for future extensibility (currently nil for
//            builtin.transform).
type TransformComponent struct{}

// Metadata describes the Transform component.
func (c *TransformComponent) Metadata() registry.ComponentMetadata {
	mapStringAnyType := reflect.TypeOf(map[string]any{})

	return registry.ComponentMetadata{
		Name:        "builtin.transform",
		DisplayName: "Transform",
		Description: "Reshape structured data into a new object based on an expression",
		Category:    "Data",
		Version:     "1.0.0",

		Inputs: []registry.ParameterSchema{},

		Outputs: []registry.ParameterSchema{
			{
				Name:        "result",
				DisplayName: "Transform Result",
				Description: "Structured object produced by this Transform node",
				Type:        "map[string]any",
				TypeID:      "transform.output",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "output_schema",
				DisplayName: "Output Schema",
				Description: "JSON Schema for the transformed output object (for tooling / UI)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
			{
				Name:        "expr",
				DisplayName: "Transform Expression",
				Description: "Expression that evaluates to the transformed object. The expression is written in CEL and can reference state.* variables.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},
	}
}

// Execute evaluates the configured expression into a structured object and
// returns it under the "result" key in the state. If no expression is
// configured, the state is left unchanged.
func (c *TransformComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	rawExpr := config.Get("expr")
	if rawExpr == nil {
		// No transform configured; leave state unchanged.
		return graph.State{}, nil
	}

	exprMap, ok := rawExpr.(map[string]any)
	if !ok {
		// Invalid expr type; ignore at runtime to be tolerant.
		return graph.State{}, nil
	}

	exprStr, _ := exprMap["expression"].(string)
	if strings.TrimSpace(exprStr) == "" {
		// Empty expression; leave state unchanged.
		return graph.State{}, nil
	}

	// Evaluate the expression using CEL with the current graph.State bound
	// to the "state" variable. For builtin.transform we do not currently
	// provide a structured "input" object, so it is nil.
	value, err := dslcel.Eval(exprStr, state, nil)
	if err != nil {
		// Be tolerant on errors: skip the transform but do not fail the
		// entire node execution.
		return graph.State{}, nil
	}

	return graph.State{
		"result": value,
	}, nil
}
