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
	// graphs as "builtin.start". The compiler now turns builtin.start into a
	// real executable node that acts as the entry point of the graph.
	registry.MustRegister(&StartComponent{})
}

// StartComponent represents the logical graph start node used by the DSL.
// It is mainly used for:
//   - Providing a concrete "Start" node in the visual editor
//   - Acting as the graph entry point in the engine DSL
//
// The DSL compiler previously treated builtin.start as a purely structural
// node. It now compiles builtin.start into a real StateGraph node, so that
// execution (and events) begin at the Start node before flowing to the first
// business node.
type StartComponent struct{}

// Metadata describes the Start component. For now it does not expose any
// configurable inputs/outputs/config schema; Start is purely structural.
// Future iterations may extend ConfigSchema to support explicit state
// variable declarations.
func (c *StartComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.start",
		DisplayName: "Start",
		Description: "Logical start node for DSL graphs (runtime no-op; marks the entry point)",
		Category:    "Control",
		Version:     "1.0.0",

		Inputs:       []registry.ParameterSchema{},
		Outputs:      []registry.ParameterSchema{},
		ConfigSchema: []registry.ParameterSchema{},
	}
}

// Execute implements the Component interface. builtin.start is a runtime
// no-op: it does not modify graph state and simply allows execution to
// proceed to the next node.
func (c *StartComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// No-op: Start does not modify state and returns nil so that the
	// executor leaves state unchanged and still performs static edge writes.
	return nil, nil
}
