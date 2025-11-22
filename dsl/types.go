//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package dsl provides the DSL (Domain-Specific Language) for defining workflows.
// It allows users to define workflows in JSON format that can be compiled into
// executable trpc-agent-go StateGraphs.
package dsl

// Workflow represents a complete workflow definition in the engine DSL.
// It only contains fields that are required for execution by the graph
// engine and intentionally avoids any UI-specific concepts such as
// positions or visual layout information.
type Workflow struct {
	// Version is the DSL version (e.g., "1.0")
	Version string `json:"version"`

	// Name is the workflow name
	Name string `json:"name"`

	// Description describes what this workflow does
	Description string `json:"description,omitempty"`

	// Nodes are the component instances in this workflow
	Nodes []Node `json:"nodes"`

	// Edges define the connections between nodes
	Edges []Edge `json:"edges"`

	// ConditionalEdges define conditional routing between nodes
	ConditionalEdges []ConditionalEdge `json:"conditional_edges,omitempty"`

	// EntryPoint is the ID of the starting node
	EntryPoint string `json:"entry_point"`

	// FinishPoint is the ID of the ending node (optional, defaults to END)
	FinishPoint string `json:"finish_point,omitempty"`

	// Metadata contains additional workflow-level metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Node represents an executable node in the engine DSL.
// It is a flat structure containing only execution semantics; UI concepts
// like position/labels live in the view-layer DSL (server/dsl).
type Node struct {
	// ID is the unique node identifier (e.g., "llm_node_1")
	ID string `json:"id"`

	// EngineNode fields are embedded so that JSON fields such as "component",
	// "config", "inputs", and "outputs" appear directly under the node.
	EngineNode `json:",inline"`
}

// EngineNode represents the engine-level node definition embedded under Node.Data.Engine.
// It closely mirrors the original Node structure before ReactFlow alignment.
type EngineNode struct {
	// Name is the display name for this node instance
	Name string `json:"name,omitempty"`

	// Component specifies which component to use
	Component ComponentRef `json:"component"`

	// Config contains component-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`

	// Inputs defines the input parameters for this node (optional, overrides component metadata)
	Inputs []NodeIO `json:"inputs,omitempty"`

	// Outputs defines the output parameters for this node (optional, overrides component metadata)
	Outputs []NodeIO `json:"outputs,omitempty"`

	// Description describes what this node does
	Description string `json:"description,omitempty"`
}

// ComponentRef references a registered component.
type ComponentRef struct {
	// Type is the component type: "builtin", "custom", or "code"
	Type string `json:"type"`

	// Ref is the component reference (e.g., "llm", "http_request")
	// For builtin/custom components, this is the component name in the registry
	// For code components, this is ignored
	Ref string `json:"ref,omitempty"`

	// Version is the component version (optional)
	Version string `json:"version,omitempty"`

	// Code is the code to execute (only for type="code")
	Code *CodeConfig `json:"code,omitempty"`
}

// CodeConfig defines inline code execution configuration.
type CodeConfig struct {
	// Language is the programming language (e.g., "python", "javascript")
	Language string `json:"language"`

	// Code is the actual code to execute
	Code string `json:"code"`

	// Inputs are the input variable names to extract from state
	Inputs []string `json:"inputs,omitempty"`

	// Outputs are the output variable names to write to state
	Outputs []string `json:"outputs,omitempty"`
}

// Edge represents a direct connection between two nodes.
// It aligns with ReactFlow's edge structure by using source/target.
type Edge struct {
	// ID is the unique edge identifier (optional, auto-generated if not provided)
	ID string `json:"id,omitempty"`

	// Source is the source node ID
	Source string `json:"source"`

	// Target is the target node ID
	Target string `json:"target"`

	// Label is the edge label for UI display
	Label string `json:"label,omitempty"`
}

// ConditionalEdge represents a conditional routing decision.
type ConditionalEdge struct {
	// ID is the unique conditional edge identifier
	ID string `json:"id,omitempty"`

	// From is the source node ID
	From string `json:"from"`

	// Condition specifies the routing logic
	Condition Condition `json:"condition"`

	// Label is the edge label for UI display
	Label string `json:"label,omitempty"`
}

// Condition defines the routing logic for conditional edges.
type Condition struct {
	// Type is the condition type: "expression", "function", "component", or "tool_routing"
	Type string `json:"type"`

	// Expression is a simple expression (e.g., "state.counter > 10")
	// Only used when Type="expression"
	Expression string `json:"expression,omitempty"`

	// Routes maps condition results to target node IDs
	// For example: {"true": "node_a", "false": "node_b"}
	Routes map[string]string `json:"routes"`

	// Default is the default route if no condition matches
	Default string `json:"default,omitempty"`

	// Function is a custom routing function reference
	// Only used when Type="function"
	Function string `json:"function,omitempty"`

	// ToolsNode is the tools node ID for tool routing
	// Only used when Type="tool_routing"
	ToolsNode string `json:"tools_node,omitempty"`

	// Next is the next node after tools execution
	// Only used when Type="tool_routing"
	Next string `json:"next,omitempty"`

	// Builtin is the structured condition configuration
	// Only used when Type="builtin"
	Builtin *BuiltinCondition `json:"builtin,omitempty"`
}

// BuiltinCondition represents a structured condition configuration.
// It contains multiple condition rules that are evaluated together using a logical operator.
type BuiltinCondition struct {
	// Conditions is the list of condition rules to evaluate
	Conditions []ConditionRule `json:"conditions"`

	// LogicalOperator specifies how to combine multiple conditions
	// Valid values: "and", "or"
	// Default: "and"
	LogicalOperator string `json:"logical_operator,omitempty"`
}

// ConditionRule represents a single condition rule.
// It compares a variable from state against a value using an operator.
type ConditionRule struct {
	// Variable is the path to the variable in state (e.g., "state.score", "state.category")
	Variable string `json:"variable"`

	// Operator is the comparison operator
	// Supported operators:
	//   String/Array: "contains", "not_contains", "starts_with", "ends_with",
	//                 "is", "is_not", "empty", "not_empty", "in", "not_in"
	//   Number: "==", "!=", ">", "<", ">=", "<="
	//   Null: "null", "not_null"
	Operator string `json:"operator"`

	// Value is the value to compare against
	// Can be string, number, boolean, or array
	Value interface{} `json:"value,omitempty"`
}

// NodeIO defines an input or output parameter for a node.
// It can override the component's default I/O schema and specify data sources/targets.
type NodeIO struct {
	// Name is the parameter name (e.g., "messages", "result")
	Name string `json:"name"`

	// Type is the parameter type string (historically Go type, e.g., "string",
	// "[]string", "model.Message"). New code should prefer TypeID/Kind for
	// frontend use.
	Type string `json:"type,omitempty"`

	// TypeID is the DSL-level type identifier exposed to frontends. For example:
	// "string", "number", "graph.messages", "llmagent.output_parsed".
	TypeID string `json:"type_id,omitempty"`

	// Kind is a coarse-grained classification for editor usage:
	// "string", "number", "boolean", "object", "array", "opaque".
	Kind string `json:"kind,omitempty"`

	// JSONSchema optionally provides a JSON Schema when this IO represents a
	// structured object. This mirrors ParameterSchema.JSONSchema and is mainly
	// intended for view-layer editors.
	JSONSchema map[string]any `json:"json_schema,omitempty"`

	// Required indicates if this parameter is required
	Required bool `json:"required,omitempty"`

	// Description explains what this parameter is for
	Description string `json:"description,omitempty"`

	// Source specifies where to read the input data from (only for inputs)
	Source *IOSource `json:"source,omitempty"`

	// Target specifies where to write the output data to (only for outputs)
	Target *IOTarget `json:"target,omitempty"`

	// Default is the default value if not provided
	Default interface{} `json:"default,omitempty"`

	// Reducer specifies the reducer function name for this output (only for outputs)
	// This is used when multiple parallel nodes write to the same state field
	Reducer string `json:"reducer,omitempty"`
}

// IOSource specifies where to read input data from.
type IOSource struct {
	// Type is the source type: "state", "node", "constant"
	Type string `json:"type"`

	// Field is the state field name (when Type="state")
	Field string `json:"field,omitempty"`

	// Node is the source node ID (when Type="node")
	Node string `json:"node,omitempty"`

	// Output is the output parameter name from the source node (when Type="node")
	Output string `json:"output,omitempty"`

	// Value is the constant value (when Type="constant")
	Value interface{} `json:"value,omitempty"`
}

// IOTarget specifies where to write output data to.
type IOTarget struct {
	// Type is the target type: "state", "output"
	Type string `json:"type"`

	// Field is the state field name (when Type="state")
	Field string `json:"field,omitempty"`
}
