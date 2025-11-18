//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"context"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Register the While component so that it can be referenced from DSL
	// graphs as "builtin.while". This component is primarily a structural
	// / compile-time concept: the DSL compiler expands it into conditional
	// back-edges in the underlying StateGraph rather than creating a real
	// executable node.
	registry.MustRegister(&WhileComponent{})
}

// WhileComponent represents a logical "while" control node in the DSL.
// Its semantics are:
//   - body_entry: ID of the first node inside the loop body
//   - body_exit:  ID of the node where the loop condition is evaluated
//   - condition:  structured condition configuration evaluated after each
//                 execution of body_exit; when true, the loop continues by
//                 routing back to body_entry; otherwise it exits to the
//                 node connected after the builtin.while node.
//
// The actual looping behavior is implemented by the DSL compiler, which
// rewrites edges so that predecessors of the While node route to body_entry,
// and adds a conditional edge from body_exit that either routes back to
// body_entry (continue) or to the post-while node (break).
type WhileComponent struct{}

// Metadata describes the While component. It intentionally does not declare
// any Inputs/Outputs because it does not introduce new state fields; it
// operates purely via control flow and the shared graph state.
func (c *WhileComponent) Metadata() registry.ComponentMetadata {
	mapStringAnyType := reflect.TypeOf(map[string]any{})

	return registry.ComponentMetadata{
		Name:        "builtin.while",
		DisplayName: "While",
		Description: "Loop while a condition is true over the shared graph state. The loop body is defined as a nested subgraph.",
		Category:    "Control",
		Version:     "1.0.0",

		Inputs:  []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "body",
				DisplayName: "Loop Body",
				Description: "Nested subgraph representing the loop body (fields: nodes, edges, start_node_id, exit_node_id).",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    true,
			},
			{
				Name:        "condition",
				DisplayName: "Loop Condition",
				Description: "Structured builtin condition (CaseCondition) evaluated after each body_exit",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      mapStringAnyType,
				Required:    true,
			},
		},
	}
}

// Execute implements the Component interface. In practice this method is not
// used when compiling DSL graphs, because builtin.while is expanded by the
// compiler into conditional edges and does not become a real executable node.
// It is provided only for completeness and potential non-DSL usage.
func (c *WhileComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// No-op: While does not modify state directly at runtime.
	return graph.State{}, nil
}
