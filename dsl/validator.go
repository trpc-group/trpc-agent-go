// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package dsl

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Validator validates DSL graphs.
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

// Validate validates an engine-level graph. It operates purely on the
// execution DSL and does not depend on any UI-specific concepts.
func (v *Validator) Validate(graphDef *Graph) error {
	if graphDef == nil {
		return fmt.Errorf("graph is nil")
	}
	if err := v.validateStructure(graphDef); err != nil {
		return fmt.Errorf("structure validation failed: %w", err)
	}

	if err := v.validateStateVariables(graphDef); err != nil {
		return fmt.Errorf("state variables validation failed: %w", err)
	}

	if err := v.validateComponents(graphDef); err != nil {
		return fmt.Errorf("component validation failed: %w", err)
	}

	if err := v.validateTopology(graphDef); err != nil {
		return fmt.Errorf("topology validation failed: %w", err)
	}

	return nil
}

// validateStructure validates the basic structure of the graph.
func (v *Validator) validateStructure(graphDef *Graph) error {
	// Check version
	if graphDef.Version == "" {
		return fmt.Errorf("graph version is required")
	}

	// Check name
	if graphDef.Name == "" {
		return fmt.Errorf("graph name is required")
	}

	// Check nodes
	if len(graphDef.Nodes) == 0 {
		return fmt.Errorf("graph must have at least one node")
	}

	// Check for duplicate node IDs and component references. Track builtin.start
	// node (if present) so we can enforce related invariants.
	nodeIDs := make(map[string]bool)
	startNodeID := ""
	for _, node := range graphDef.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node ID cannot be empty")
		}
		if nodeIDs[node.ID] {
			return fmt.Errorf("duplicate node ID: %s", node.ID)
		}
		nodeIDs[node.ID] = true

		engine := node.EngineNode

		// Validate node type reference.
		if engine.NodeType == "" {
			return fmt.Errorf("node %s: node_type is required", node.ID)
		}

		// Track builtin.start node. We only allow at most one such node.
		if engine.NodeType == "builtin.start" {
			if startNodeID != "" {
				return fmt.Errorf("multiple builtin.start nodes are not allowed (found %s and %s)", startNodeID, node.ID)
			}
			startNodeID = node.ID
		}
	}

	// Check start node ID
	if graphDef.StartNodeID == "" {
		return fmt.Errorf("graph start_node_id is required")
	}
	if !nodeIDs[graphDef.StartNodeID] {
		return fmt.Errorf("start_node_id %s does not exist", graphDef.StartNodeID)
	}

	// If a builtin.start node is present, the graph start_node_id must be
	// that node. The actual executable entry point will be derived from its
	// outgoing edge by the compiler.
	if startNodeID != "" && graphDef.StartNodeID != startNodeID {
		return fmt.Errorf("graph start_node_id must be builtin.start node %s when present (got %s)", startNodeID, graphDef.StartNodeID)
	}

	// Validate edges
	startOutCount := 0
	for _, edge := range graphDef.Edges {
		// Allow virtual Start and End nodes without explicit node definitions.
		if edge.Source != graph.Start && !nodeIDs[edge.Source] {
			return fmt.Errorf("edge %s: source node %s does not exist", edge.ID, edge.Source)
		}
		if edge.Target != graph.End && !nodeIDs[edge.Target] {
			return fmt.Errorf("edge %s: target node %s does not exist", edge.ID, edge.Target)
		}

		// Additional constraints for builtin.start (if present):
		if startNodeID != "" {
			if edge.Target == startNodeID {
				return fmt.Errorf("edge %s: builtin.start node %s cannot be the target of an edge", edge.ID, startNodeID)
			}
			if edge.Source == startNodeID {
				startOutCount++
			}
		}
	}

	if startNodeID != "" {
		if startOutCount == 0 {
			return fmt.Errorf("builtin.start node %s must have exactly one outgoing edge (found none)", startNodeID)
		}
		if startOutCount > 1 {
			return fmt.Errorf("builtin.start node %s must have exactly one outgoing edge (found %d)", startNodeID, startOutCount)
		}
	}

	// Validate conditional edges
	for _, condEdge := range graphDef.ConditionalEdges {
		if !nodeIDs[condEdge.From] {
			return fmt.Errorf("conditional edge %s: source node %s does not exist", condEdge.ID, condEdge.From)
		}

		// Validate condition: ensure at least one case and each target exists.
		if len(condEdge.Condition.Cases) == 0 {
			return fmt.Errorf("conditional edge %s: condition requires at least one case", condEdge.ID)
		}
		for idx, kase := range condEdge.Condition.Cases {
			if strings.TrimSpace(kase.Predicate.Expression) == "" {
				return fmt.Errorf("conditional edge %s: case %d predicate.expression is required", condEdge.ID, idx)
			}
			if kase.Target == "" {
				return fmt.Errorf("conditional edge %s: case %d target is empty", condEdge.ID, idx)
			}
			if !nodeIDs[kase.Target] {
				return fmt.Errorf("conditional edge %s: case %d target node %s does not exist",
					condEdge.ID, idx, kase.Target)
			}
		}
		if condEdge.Condition.Default != "" && !nodeIDs[condEdge.Condition.Default] {
			return fmt.Errorf("conditional edge %s: default route target %s does not exist",
				condEdge.ID, condEdge.Condition.Default)
		}
	}

	return nil
}

// validateStateVariables validates graph-level state variable declarations
// and ensures builtin.set_state assignments only target declared variables
// when declarations are present.
func (v *Validator) validateStateVariables(graphDef *Graph) error {
	declared := make(map[string]StateVariable)
	for idx, sv := range graphDef.StateVariables {
		name := strings.TrimSpace(sv.Name)
		if name == "" {
			return fmt.Errorf("state_variables[%d]: name is required", idx)
		}
		if _, exists := declared[name]; exists {
			return fmt.Errorf("state_variables[%d]: duplicate state variable name %q", idx, name)
		}
		declared[name] = sv
	}

	// If no state variables are declared, we do not enforce assignments.
	if len(declared) == 0 {
		return nil
	}

	// Validate builtin.set_state assignments.
	for _, node := range graphDef.Nodes {
		engine := node.EngineNode
		if engine.NodeType != "builtin.set_state" {
			continue
		}

		rawAssignments, ok := engine.Config["assignments"]
		if !ok || rawAssignments == nil {
			continue
		}

		assignSlice, ok := rawAssignments.([]any)
		if !ok {
			continue
		}

		for i, item := range assignSlice {
			assignMap, ok := item.(map[string]any)
			if !ok {
				continue
			}

			field, _ := assignMap["field"].(string)
			if strings.TrimSpace(field) == "" {
				// For compatibility with potential "name" field naming.
				field, _ = assignMap["name"].(string)
			}

			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}

			if _, exists := declared[field]; !exists {
				return fmt.Errorf("node %s: assignments[%d] field %q is not declared in state_variables", node.ID, i, field)
			}
		}
	}

	return nil
}

// validateComponents validates that all referenced components exist in the registry.
func (v *Validator) validateComponents(graphDef *Graph) error {
	for _, node := range graphDef.Nodes {
		engine := node.EngineNode

		// Check if component exists
		if !v.registry.Has(engine.NodeType) {
			return fmt.Errorf("node %s: component %s not found in registry", node.ID, engine.NodeType)
		}

		// Get component metadata for validation
		metadata, err := v.registry.GetMetadata(engine.NodeType)
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

// validateTopology validates the graph topology (no unreachable nodes, etc.).
func (v *Validator) validateTopology(graphDef *Graph) error {
	// Build adjacency list
	adjacency := make(map[string][]string)
	for _, edge := range graphDef.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	for _, condEdge := range graphDef.ConditionalEdges {
		for _, kase := range condEdge.Condition.Cases {
			adjacency[condEdge.From] = append(adjacency[condEdge.From], kase.Target)
		}
		if condEdge.Condition.Default != "" {
			adjacency[condEdge.From] = append(adjacency[condEdge.From], condEdge.Condition.Default)
		}
	}

	// Find reachable nodes from start node
	reachable := make(map[string]bool)
	v.dfs(graphDef.StartNodeID, adjacency, reachable)

	// Check for unreachable nodes
	for _, node := range graphDef.Nodes {
		if !reachable[node.ID] && node.ID != graphDef.StartNodeID {
			return fmt.Errorf("node %s is unreachable from start_node_id", node.ID)
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
