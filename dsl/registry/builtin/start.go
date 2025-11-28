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

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Register the Start component so that it can be referenced from DSL
	// graphs as "builtin.start". This component is primarily a
	// compile-time / UX concept and is typically not turned into a real
	// graph node by the DSL compiler.
	registry.MustRegister(&StartComponent{})
}

// StartComponent represents the logical graph start node used by the DSL.
// It is mainly used for:
//   - Providing a concrete "Start" node in the visual editor
//   - Acting as the graph entry point in the engine DSL
//
// The DSL compiler treats builtin.start specially: it uses the outgoing edge
// from this node to determine the real graph entry point and usually does not
// create a corresponding executable node in the StateGraph.
type StartComponent struct{}

// Metadata describes the Start component. For now it does not expose any
// configurable inputs/outputs/config schema; Start is purely structural.
// Future iterations may extend ConfigSchema to support explicit state
// variable declarations.
func (c *StartComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.start",
		DisplayName: "Start",
		Description: "Logical start node for DSL graphs (compile-time only)",
		Category:    "Control",
		Version:     "1.0.0",

		Inputs:       []registry.ParameterSchema{},
		Outputs:      []registry.ParameterSchema{},
		ConfigSchema: []registry.ParameterSchema{},
	}
}

// Execute implements the Component interface. In practice this method is not
// used when compiling DSL graphs, because builtin.start is handled
// specially by the compiler and does not become a real executable node.
// It is provided only for completeness and for potential non-DSL usage.
func (c *StartComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// No-op: Start does not modify state.
	return graph.State{}, nil
}
