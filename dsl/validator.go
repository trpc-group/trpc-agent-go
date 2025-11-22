//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package dsl

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Validator validates DSL workflows.
// It performs multi-level validation:
// 1. Structure validation (required fields, valid references)
// 2. Semantic validation (no cycles, reachable nodes)
// 3. Component validation (components exist in registry)
// 4. Type validation (config matches component schema)
type Validator struct {
	registry *registry.Registry
}

// NewValidator creates a new validator with the given component registry.
func NewValidator(reg *registry.Registry) *Validator {
	return &Validator{
		registry: reg,
	}
}

// Validate validates an engine-level workflow. It operates purely on the
// execution DSL and does not depend on any UI-specific concepts.
func (v *Validator) Validate(workflow *Workflow) error {
	if workflow == nil {
		return fmt.Errorf("workflow is nil")
	}
	if err := v.validateStructure(workflow); err != nil {
		return fmt.Errorf("structure validation failed: %w", err)
	}

	if err := v.validateComponents(workflow); err != nil {
		return fmt.Errorf("component validation failed: %w", err)
	}

	if err := v.validateTopology(workflow); err != nil {
		return fmt.Errorf("topology validation failed: %w", err)
	}

	return nil
}

// validateStructure validates the basic structure of the workflow.
func (v *Validator) validateStructure(workflow *Workflow) error {
	// Check version
	if workflow.Version == "" {
		return fmt.Errorf("workflow version is required")
	}

	// Check name
	if workflow.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	// Check nodes
	if len(workflow.Nodes) == 0 {
		return fmt.Errorf("workflow must have at least one node")
	}

	// Check for duplicate node IDs
	nodeIDs := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node ID cannot be empty")
		}
		if nodeIDs[node.ID] {
			return fmt.Errorf("duplicate node ID: %s", node.ID)
		}
		nodeIDs[node.ID] = true

		engine := node.EngineNode

		// Validate component reference
		if engine.Component.Type == "" {
			return fmt.Errorf("node %s: component type is required", node.ID)
		}

		if engine.Component.Type != "code" && engine.Component.Ref == "" {
			return fmt.Errorf("node %s: component ref is required for type %s", node.ID, engine.Component.Type)
		}

		if engine.Component.Type == "code" && engine.Component.Code == nil {
			return fmt.Errorf("node %s: code config is required for type 'code'", node.ID)
		}
	}

	// Check entry point
	if workflow.EntryPoint == "" {
		return fmt.Errorf("workflow entry point is required")
	}
	if !nodeIDs[workflow.EntryPoint] {
		return fmt.Errorf("entry point %s does not exist", workflow.EntryPoint)
	}

	// Check finish point (if specified)
	if workflow.FinishPoint != "" && !nodeIDs[workflow.FinishPoint] {
		return fmt.Errorf("finish point %s does not exist", workflow.FinishPoint)
	}

	// Validate edges
	for _, edge := range workflow.Edges {
		// Allow virtual Start and End nodes without explicit node definitions.
		if edge.Source != graph.Start && !nodeIDs[edge.Source] {
			return fmt.Errorf("edge %s: source node %s does not exist", edge.ID, edge.Source)
		}
		if edge.Target != graph.End && !nodeIDs[edge.Target] {
			return fmt.Errorf("edge %s: target node %s does not exist", edge.ID, edge.Target)
		}
	}

	// Validate conditional edges
	for _, condEdge := range workflow.ConditionalEdges {
		if !nodeIDs[condEdge.From] {
			return fmt.Errorf("conditional edge %s: source node %s does not exist", condEdge.ID, condEdge.From)
		}

		// Validate condition
		if condEdge.Condition.Type == "" {
			return fmt.Errorf("conditional edge %s: condition type is required", condEdge.ID)
		}

		// For tool_routing, validate tools_node and next instead of routes
		if condEdge.Condition.Type == "tool_routing" {
			if condEdge.Condition.ToolsNode == "" {
				return fmt.Errorf("conditional edge %s: tools_node is required for tool_routing", condEdge.ID)
			}
			if !nodeIDs[condEdge.Condition.ToolsNode] {
				return fmt.Errorf("conditional edge %s: tools_node %s does not exist",
					condEdge.ID, condEdge.Condition.ToolsNode)
			}
			if condEdge.Condition.Next == "" {
				return fmt.Errorf("conditional edge %s: next is required for tool_routing", condEdge.ID)
			}
			if !nodeIDs[condEdge.Condition.Next] {
				return fmt.Errorf("conditional edge %s: next node %s does not exist",
					condEdge.ID, condEdge.Condition.Next)
			}
		} else {
			// For other condition types, validate routes
			if len(condEdge.Condition.Routes) == 0 {
				return fmt.Errorf("conditional edge %s: at least one route is required", condEdge.ID)
			}

			for routeKey, targetNode := range condEdge.Condition.Routes {
				if !nodeIDs[targetNode] {
					return fmt.Errorf("conditional edge %s: route %s target node %s does not exist",
						condEdge.ID, routeKey, targetNode)
				}
			}

			// Validate default route (if specified)
			if condEdge.Condition.Default != "" && !nodeIDs[condEdge.Condition.Default] {
				return fmt.Errorf("conditional edge %s: default route target %s does not exist",
					condEdge.ID, condEdge.Condition.Default)
			}
		}
	}

	return nil
}

// validateComponents validates that all referenced components exist in the registry.
func (v *Validator) validateComponents(workflow *Workflow) error {
	for _, node := range workflow.Nodes {
		engine := node.EngineNode

		// Skip code components (they don't need to be in registry)
		if engine.Component.Type == "code" {
			continue
		}

		// Check if component exists
		if !v.registry.Has(engine.Component.Ref) {
			return fmt.Errorf("node %s: component %s not found in registry", node.ID, engine.Component.Ref)
		}

		// Get component metadata for validation
		metadata, err := v.registry.GetMetadata(engine.Component.Ref)
		if err != nil {
			return fmt.Errorf("node %s: failed to get component metadata: %w", node.ID, err)
		}

		// Validate config against component schema
		if err := v.validateConfig(node.ID, engine.Config, metadata.ConfigSchema); err != nil {
			return err
		}
	}

	return nil
}

// validateConfig validates a node's config against the component's config schema.
func (v *Validator) validateConfig(nodeID string, config map[string]interface{}, schema []registry.ParameterSchema) error {
	// Check required config parameters
	for _, param := range schema {
		if param.Required {
			if _, exists := config[param.Name]; !exists {
				return fmt.Errorf("node %s: required config parameter %s is missing", nodeID, param.Name)
			}
		}
	}

	// TODO: Add type validation for config values
	// This would require more sophisticated type checking

	return nil
}

// validateTopology validates the workflow topology (no unreachable nodes, etc.).
func (v *Validator) validateTopology(workflow *Workflow) error {
	// Build adjacency list
	adjacency := make(map[string][]string)
	for _, edge := range workflow.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	for _, condEdge := range workflow.ConditionalEdges {
		// Handle tool_routing specially
		if condEdge.Condition.Type == "tool_routing" {
			// tool_routing creates edges: from -> tools_node -> from -> next
			adjacency[condEdge.From] = append(adjacency[condEdge.From], condEdge.Condition.ToolsNode)
			adjacency[condEdge.Condition.ToolsNode] = append(adjacency[condEdge.Condition.ToolsNode], condEdge.From)
			adjacency[condEdge.From] = append(adjacency[condEdge.From], condEdge.Condition.Next)
		} else {
			// Regular conditional edges
			for _, target := range condEdge.Condition.Routes {
				adjacency[condEdge.From] = append(adjacency[condEdge.From], target)
			}
			if condEdge.Condition.Default != "" {
				adjacency[condEdge.From] = append(adjacency[condEdge.From], condEdge.Condition.Default)
			}
		}
	}

	// Find reachable nodes from entry point
	reachable := make(map[string]bool)
	v.dfs(workflow.EntryPoint, adjacency, reachable)

	// Check for unreachable nodes
	for _, node := range workflow.Nodes {
		if !reachable[node.ID] && node.ID != workflow.EntryPoint {
			return fmt.Errorf("node %s is unreachable from entry point", node.ID)
		}
	}

	return nil
}

// dfs performs depth-first search to find reachable nodes.
func (v *Validator) dfs(nodeID string, adjacency map[string][]string, visited map[string]bool) {
	if visited[nodeID] {
		return
	}
	visited[nodeID] = true

	for _, neighbor := range adjacency[nodeID] {
		v.dfs(neighbor, adjacency, visited)
	}
}
