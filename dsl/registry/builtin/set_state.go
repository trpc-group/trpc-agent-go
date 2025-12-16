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

	dslcel "trpc.group/trpc-go/trpc-agent-go/dsl/internal/cel"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Register the SetState component so that it can be referenced from DSL
	// graphs as "builtin.set_state".
	registry.MustRegister(&SetStateComponent{})
}

// SetStateComponent assigns values to existing graph state variables.
// It evaluates a list of CEL expressions and writes the results into the
// corresponding state fields. Variable declaration (type/default) is handled
// at the graph/start level; this component is purely an assignment node.
//
// Each assignment.expr is a CEL expression string evaluated with the
// following variables available:
//   - state: graph.State (global graph state)
//   - input: JSON-like object representing the node input (currently unused
//     for builtin.set_state and left as nil).
type SetStateComponent struct{}

// setStateAssignmentConfig describes a single assignment in config.
// It is intentionally internal-only; the external DSL shape is plain JSON.
type setStateAssignmentConfig struct {
	Field string
	Expr  map[string]any
}

// Metadata describes the SetState component.
func (c *SetStateComponent) Metadata() registry.ComponentMetadata {
	assignmentsType := reflect.TypeOf([]map[string]any{})

	return registry.ComponentMetadata{
		Name:        "builtin.set_state",
		DisplayName: "Set State",
		Description: "Assign values to graph state variables based on expressions",
		Category:    "Data",
		Version:     "1.0.0",

		Inputs:  []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "assignments",
				DisplayName: "Assignments",
				Description: "List of {field, expr} objects describing state updates",
				Type:        "[]map[string]any",
				TypeID:      "array",
				Kind:        "array",
				GoType:      assignmentsType,
				Required:    false,
			},
		},
	}
}

// Execute evaluates all configured assignments and returns a state delta
// containing the updated fields. If no assignments are configured, the state
// is left unchanged.
func (c *SetStateComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	raw := config.Get("assignments")
	if raw == nil {
		return graph.State{}, nil
	}

	stateDelta := graph.State{}

	applyAssignment := func(assignMap map[string]any) {
		if assignMap == nil {
			return
		}

		field, _ := assignMap["field"].(string)
		if field == "" {
			// For compatibility with potential "name" field naming.
			field, _ = assignMap["name"].(string)
		}
		if strings.TrimSpace(field) == "" {
			return
		}

		rawExpr, ok := assignMap["expr"].(map[string]any)
		if !ok {
			return
		}

		exprStr, _ := rawExpr["expression"].(string)
		if strings.TrimSpace(exprStr) == "" {
			return
		}

		// Evaluate the expression using CEL with the current graph.State
		// bound to the "state" variable. For builtin.set_state we do not
		// currently provide a structured "input" object, so it is nil.
		value, err := dslcel.Eval(exprStr, state, nil)
		if err != nil {
			// Be conservative on errors: skip this assignment but do not
			// fail the entire node execution.
			return
		}

		stateDelta[field] = value
	}

	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			assignMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			applyAssignment(assignMap)
		}
	case []map[string]any:
		for _, assignMap := range list {
			applyAssignment(assignMap)
		}
	default:
		// Be tolerant of mis-typed configs.
		return graph.State{}, nil
	}

	return stateDelta, nil
}
