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

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	registry.MustRegister(&PassthroughComponent{})
}

// PassthroughComponent is a simple component that passes state through unchanged.
// Useful for start/end nodes or debugging.
type PassthroughComponent struct{}

func (c *PassthroughComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.passthrough",
		DisplayName: "Passthrough",
		Description: "Pass state through unchanged",
		Category:    "Utility",
		Version:     "1.0.0",

		Inputs: []registry.ParameterSchema{
			{
				Name:        "*",
				DisplayName: "Any Input",
				Description: "Accepts any state fields",
				Type:        "any",
				GoType:      reflect.TypeOf((*interface{})(nil)).Elem(),
				Required:    false,
			},
		},

		Outputs: []registry.ParameterSchema{
			{
				Name:        "*",
				DisplayName: "Any Output",
				Description: "Outputs the same state fields",
				Type:        "any",
				GoType:      reflect.TypeOf((*interface{})(nil)).Elem(),
			},
		},

		ConfigSchema: []registry.ParameterSchema{},
	}
}

func (c *PassthroughComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Return empty state (no changes)
	return graph.State{}, nil
}
