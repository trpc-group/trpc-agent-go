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
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	registry.MustRegister(&FunctionComponent{})
}

// FunctionComponent is a generic function node component.
// It allows users to register custom business logic functions.
type FunctionComponent struct{}

// Metadata returns the component metadata.
func (c *FunctionComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.function",
		DisplayName: "Function Node",
		Description: "Generic function node for custom business logic",
		Category:    "Core",
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        "state",
				Type:        "graph.State",
				GoType:      reflect.TypeOf(map[string]any{}),
				Description: "Current graph state",
				Required:    true,
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "state",
				Type:        "graph.State",
				GoType:      reflect.TypeOf(map[string]any{}),
				Description: "Updated graph state",
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "function",
				DisplayName: "Function",
				Description: "Reference to the registered function (e.g., 'custom.preprocess_document')",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "custom.preprocess_document",
			},
		},
	}
}

// Execute executes the function component.
func (c *FunctionComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Get function reference from config
	functionRef := config.GetString("function")
	if functionRef == "" {
		return nil, fmt.Errorf("function reference not found in config")
	}

	// Look up the function in the registry
	// The function should be registered as a component
	component, exists := registry.DefaultRegistry.Get(functionRef)
	if !exists {
		return nil, fmt.Errorf("function '%s' not found in registry", functionRef)
	}

	// Execute the function component
	result, err := component.Execute(ctx, config, state)
	if err != nil {
		return nil, fmt.Errorf("error executing function '%s': %w", functionRef, err)
	}

	// Return the result (result is already graph.State)
	return result, nil
}
