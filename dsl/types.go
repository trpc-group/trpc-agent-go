//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package dsl provides the DSL (Domain-Specific Language) for defining graphs.
// It allows users to define graphs in JSON format that can be compiled into
// executable trpc-agent-go StateGraphs.
package dsl

// Graph represents a complete graph definition in the engine DSL.
// It only contains fields that are required for execution by the graph
// engine and intentionally avoids any UI-specific concepts such as
// positions or visual layout information.
type Graph struct {
	// Version is the DSL version (e.g., "1.0")
	Version string `json:"version"`

	// Name is the graph name
	Name string `json:"name"`

	// Description describes what this graph does
	Description string `json:"description,omitempty"`

	// Nodes are the component instances in this graph
	Nodes []Node `json:"nodes"`

	// Edges define the connections between nodes
	Edges []Edge `json:"edges"`

	// ConditionalEdges define conditional routing between nodes
	ConditionalEdges []ConditionalEdge `json:"conditional_edges,omitempty"`

	// StateVariables declares graph-level state variables that can be read
	// and written by nodes (for example via builtin.set_state). This allows
	// the graph author to define the shape and reducer behavior of global
	// state independently of any particular component.
	StateVariables []StateVariable `json:"state_variables,omitempty"`

	// StartNodeID is the ID of the visual start node (usually the builtin.start
	// node). The actual executable entry point is derived from this node's
	// outgoing edge during compilation.
	StartNodeID string `json:"start_node_id"`

	// Metadata contains additional graph-level metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// StateVariable describes a graph-level state variable. It is the single
// source of truth for the variable's existence and coarse-grained type; nodes
// such as builtin.set_state only assign to already-declared variables.
type StateVariable struct {
	// Name is the state field name (e.g., "greeting", "counter").
	Name string `json:"name"`

	// Kind is a coarse-grained classification for editor usage and schema
	// inference: "string", "number", "boolean", "object", "array", "opaque".
	// When omitted, it is treated as "opaque".
	Kind string `json:"kind,omitempty"`

	// JSONSchema optionally provides a JSON Schema when this variable
	// represents a structured object.
	JSONSchema map[string]any `json:"json_schema,omitempty"`

	// Description explains what this state variable is for.
	Description string `json:"description,omitempty"`

	// Default is the default value if the state field is not present when
	// the graph starts. When omitted, no default is applied.
	Default any `json:"default,omitempty"`

	// Reducer specifies the reducer function name for this state field.
	// When empty, the framework's DefaultReducer is used.
	Reducer string `json:"reducer,omitempty"`
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
	// Label is the human-readable label for this node instance
	Label string `json:"label,omitempty"`

	// NodeType specifies which component to use (e.g., "builtin.llmagent").
	NodeType string `json:"node_type"`

	// NodeVersion is an optional component version (mirrors ComponentRef.Version).
	NodeVersion string `json:"node_version,omitempty"`

	// Config contains component-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`

	// Inputs defines the input parameters for this node (optional, overrides component metadata)
	Inputs []NodeIO `json:"inputs,omitempty"`

	// Outputs defines the output parameters for this node (optional, overrides component metadata)
	Outputs []NodeIO `json:"outputs,omitempty"`

	// Description describes what this node does
	Description string `json:"description,omitempty"`
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

// Condition defines the routing logic for conditional edges. It is expressed
// as an ordered list of CEL-based cases plus an optional default target.
// Each case's Predicate is evaluated in order; the first case that returns
// true wins and its Target node ID is selected. If no case matches and
// Default is non-empty, Default is used. Otherwise, evaluation fails.
type Condition struct {
	// Cases describes ordered cases. The first matching case wins.
	Cases []Case `json:"cases,omitempty"`

	// Default is the default route if no case matches.
	Default string `json:"default,omitempty"`
}

	// Case represents a single case branch in a conditional edge.
// Cases are evaluated in order; the first matching case's Target is chosen.
type Case struct {
	// Name is an optional human-readable label for the case (UI/meta).
	Name string `json:"name,omitempty"`

	// Predicate is the CEL expression evaluated for this case. When it
	// evaluates to true, the case's Target is chosen. The expression is
	// evaluated with access to:
	//   - state: graph.State
	//   - input: JSON-like view of the upstream node output (e.g.,
	//            node_structured[from].output_parsed for builtin routes).
	Predicate Expression `json:"predicate"`

	// Target is the node ID to route to when this case matches.
	Target string `json:"target"`
}

// Expression represents a CEL expression used in the engine DSL. It matches
// the common OpenAI shape { "expression": "...", "format": "cel" } and is
// evaluated in an environment that typically exposes "state" and "input"
// variables.
type Expression struct {
	Expression string `json:"expression"`
	Format     string `json:"format,omitempty"`
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
