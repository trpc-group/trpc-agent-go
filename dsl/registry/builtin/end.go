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
	"encoding/json"
	"reflect"
	"strings"

	dslcel "trpc.group/trpc-go/trpc-agent-go/dsl/cel"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func init() {
	registry.MustRegister(&EndComponent{})
}

// EndComponent is a simple component that marks the logical end of a graph
// branch. It does not modify state and can be used alongside the virtual
// graph.End node to provide a concrete "End" node in the DSL/UX.
type EndComponent struct{}

func (c *EndComponent) Metadata() registry.ComponentMetadata {
	mapStringAnyType := reflect.TypeOf(map[string]any{})

	return registry.ComponentMetadata{
		Name:        "builtin.end",
		DisplayName: "End",
		Description: "Marks the end of a graph branch and can optionally set a structured final output",
		Category:    "Control",
		Version:     "1.0.0",

		Inputs: []registry.ParameterSchema{},

		Outputs: []registry.ParameterSchema{
			{
				Name:        "end_structured_output",
				DisplayName: "End Structured Output",
				Description: "Structured graph output object set by this End node",
				Type:        "map[string]any",
				TypeID:      "end.structured_output",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "output_schema",
				DisplayName: "Output Schema",
				Description: "JSON Schema for the structured final output (for tooling / UI)",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
			{
				Name:        "expr",
				DisplayName: "Structured Output Expression",
				Description: "Expression that evaluates to the structured final output object. The expression is written in CEL and can reference state.* variables.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    false,
			},
		},
	}
}

func (c *EndComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	rawExpr := config.Get("expr")
	if rawExpr == nil {
		// No structured output configured; leave state unchanged.
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
	// to the "state" variable. For builtin.end we do not currently provide a
	// structured "input" object, so it is nil.
	log.Infof("builtin.end: evaluating CEL expr=%q", exprStr)
	value, err := dslcel.Eval(exprStr, state, nil)
	if err != nil {
		// Be tolerant on errors: skip structured output but do not fail the
		// entire node execution, while emitting a debug log so that issues
		// can be diagnosed during development.
		log.Warnf("builtin.end: CEL evaluation failed for expr=%q: %v", exprStr, err)
		return graph.State{}, nil
	}

	// Try to record this structured output in the per-node cache as well,
	// so that downstream logic can access it via node_structured[<nodeID>].
	var nodeID string
	if nodeIDData, exists := state[graph.StateKeyCurrentNodeID]; exists {
		if id, ok := nodeIDData.(string); ok {
			nodeID = id
		}
	}

	stateDelta := graph.State{
		"end_structured_output": value,
	}

	log.Infof("builtin.end: stateDelta keys after CEL eval: %v", func() []string {
		keys := make([]string, 0, len(stateDelta))
		for k := range stateDelta {
			keys = append(keys, k)
		}
		return keys
	}())

	if nodeID != "" {
		stateDelta["node_structured"] = map[string]any{
			nodeID: map[string]any{
				// For consistency with LLMAgent, we expose the End node's
				// structured result under the per-node "output_parsed" key.
				"output_parsed": value,
			},
		}
	}

	// Override last_response with a JSON string representation of the
	// structured final output so that existing runners / UIs that only look
	// at last_response can still see the End node's result, without relying
	// on any particular field name in the object.
	if b, err := json.Marshal(value); err == nil && strings.TrimSpace(string(b)) != "" {
		stateDelta[graph.StateKeyLastResponse] = string(b)
	}

	return stateDelta, nil
}

// Note: renderStructuredTemplate and the HTTP/template-based expression
// handling have been removed in favor of a CEL-based implementation.
