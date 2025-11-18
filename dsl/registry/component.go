//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package registry provides component registration and management for DSL graphs.
package registry

import (
	"context"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Component represents a reusable graph component that can be referenced in DSL.
// Components are the building blocks of graphs, providing specific functionality
// like LLM calls, HTTP requests, data transformations, etc.
type Component interface {
	// Metadata returns the component's metadata including name, description, and I/O schema
	Metadata() ComponentMetadata

	// Execute runs the component logic with the given configuration and state.
	// The return value can be:
	// - graph.State: Normal state update
	// - []*graph.Command: For dynamic fan-out (parallel execution)
	Execute(ctx context.Context, config ComponentConfig, state graph.State) (any, error)
}

// ComponentMetadata describes a component's interface and capabilities.
// This metadata is used for:
// 1. DSL validation (input/output schema validation)
// 2. State schema inference (automatic schema generation)
// 3. Optional higher-level tooling (e.g., HTTP layer, editors) via Meta.
type ComponentMetadata struct {
	// Name is the unique identifier for this component (e.g., "builtin.llm", "custom.processor")
	Name string `json:"name"`

	// DisplayName is the human-readable name shown in the UI
	DisplayName string `json:"display_name"`

	// Description explains what this component does
	Description string `json:"description"`

	// Category groups components logically (e.g., "LLM", "Data Processing", "Integration")
	Category string `json:"category"`

	// Version is the component version (e.g., "1.0.0")
	Version string `json:"version"`

	// Inputs defines the input parameters this component accepts
	Inputs []ParameterSchema `json:"inputs"`

	// Outputs defines the state fields this component produces
	Outputs []ParameterSchema `json:"outputs"`

	// ConfigSchema defines the configuration parameters for this component
	ConfigSchema []ParameterSchema `json:"config_schema"`

	// Meta holds optional, engine-agnostic metadata for higher layers.
	// Typical usages include UI hints (e.g., icon/color), category tags,
	// or other annotations that the engine itself does not interpret.
	Meta map[string]any `json:"meta,omitempty"`
}

// ParameterSchema defines the schema for an input, output, or config parameter.
type ParameterSchema struct {
	// Name is the parameter name (e.g., "messages", "temperature")
	Name string `json:"name"`

	// DisplayName is the human-readable name
	DisplayName string `json:"display_name"`

	// Description explains what this parameter is for
	Description string `json:"description"`

	// Type is the Go type name (e.g., "string", "int", "[]model.Message")
	Type string `json:"type"`

	// TypeID is the DSL-level type identifier exposed to frontends.
	// For simple scalars this often matches Type ("string", "number"), while
	// for richer types it can be a logical ID like "graph.messages" or
	// "llmagent.output_parsed".
	TypeID string `json:"type_id,omitempty"`

	// GoType is the actual Go reflect.Type (used for validation)
	GoType reflect.Type `json:"-"`

	// Required indicates if this parameter is required
	Required bool `json:"required"`

	// Default is the default value (if any)
	Default interface{} `json:"default,omitempty"`

	// Kind is a coarse-grained, frontend-friendly classification of the type.
	// Typical values: "string", "number", "boolean", "object", "array",
	// "opaque".
	Kind string `json:"kind,omitempty"`

	// Reducer specifies how this field should be merged in state updates
	// (e.g., "default", "append", "message")
	Reducer string `json:"reducer,omitempty"`

	// Enum lists allowed values (for dropdown UI)
	Enum []interface{} `json:"enum,omitempty"`

	// Placeholder is the placeholder text for UI input
	Placeholder string `json:"placeholder,omitempty"`

	// Validation contains additional validation rules
	Validation *ValidationRules `json:"validation,omitempty"`

	// JSONSchema optionally provides a JSON Schema for this field when it
	// represents a structured object. This is primarily intended for
	// editor/visualization use; the runtime still relies on GoType and
	// Reducer for execution semantics.
	JSONSchema map[string]any `json:"json_schema,omitempty"`
}

// ValidationRules defines validation constraints for a parameter.
type ValidationRules struct {
	// Min is the minimum value (for numbers) or length (for strings/arrays)
	Min *float64 `json:"min,omitempty"`

	// Max is the maximum value (for numbers) or length (for strings/arrays)
	Max *float64 `json:"max,omitempty"`

	// Pattern is a regex pattern for string validation
	Pattern string `json:"pattern,omitempty"`

	// Custom is a custom validation function
	Custom func(value interface{}) error `json:"-"`
}

// ComponentConfig holds the configuration for a component instance.
// This is the data from the DSL's "config" field.
type ComponentConfig map[string]interface{}

// GetString retrieves a string config value.
func (c ComponentConfig) GetString(key string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt retrieves an int config value.
func (c ComponentConfig) GetInt(key string) int {
	if v, ok := c[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case float64:
			return int(val)
		}
	}
	return 0
}

// GetFloat retrieves a float64 config value.
func (c ComponentConfig) GetFloat(key string) float64 {
	if v, ok := c[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0.0
}

// GetBool retrieves a bool config value.
func (c ComponentConfig) GetBool(key string) bool {
	if v, ok := c[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// Get retrieves a raw config value.
func (c ComponentConfig) Get(key string) interface{} {
	return c[key]
}
