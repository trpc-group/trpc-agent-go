//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package condition

import (
	"context"
	"fmt"
	"log"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Evaluate evaluates a builtin condition against the given state.
// It returns true if the condition is satisfied, false otherwise.
func Evaluate(ctx context.Context, state graph.State, cond *BuiltinCondition) (bool, error) {
	if cond == nil {
		return false, fmt.Errorf("builtin condition is nil")
	}

	if len(cond.Conditions) == 0 {
		return false, fmt.Errorf("no conditions specified")
	}

	// Default to "and" if not specified
	logicalOp := cond.LogicalOperator
	if logicalOp == "" {
		logicalOp = LogicalAnd
	}

	// Validate logical operator
	if logicalOp != LogicalAnd && logicalOp != LogicalOr {
		return false, fmt.Errorf("invalid logical operator: %s (must be 'and' or 'or')", logicalOp)
	}

	results := make([]bool, 0, len(cond.Conditions))

	for i, rule := range cond.Conditions {
		result, err := evaluateRule(state, rule)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate condition %d: %w", i, err)
		}

		results = append(results, result)

		// Short-circuit evaluation
		if logicalOp == LogicalAnd && !result {
			// If any condition is false in AND mode, return false immediately
			return false, nil
		}
		if logicalOp == LogicalOr && result {
			// If any condition is true in OR mode, return true immediately
			return true, nil
		}
	}

	// Final result
	if logicalOp == LogicalAnd {
		final := allTrue(results)
		log.Printf("[COND] logical_op=AND results=%v final=%v", results, final)
		return final, nil
	}
	final := anyTrue(results)
	log.Printf("[COND] logical_op=OR results=%v final=%v", results, final)
	return final, nil
}

// evaluateRule evaluates a single condition rule.
func evaluateRule(state graph.State, rule ConditionRule) (bool, error) {
	// Get the actual value from state
	actualValue, err := getValueFromState(state, rule.Variable)
	if err != nil {
		return false, fmt.Errorf("failed to get variable %s: %w", rule.Variable, err)
	}

	// Evaluate the operator
	result, err := evaluateOperator(rule.Operator, actualValue, rule.Value)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate operator %s: %w", rule.Operator, err)
	}

	log.Printf("[COND] rule variable=%q operator=%q actual=%T(%v) expected=%T(%v) -> %v",
		rule.Variable, rule.Operator, actualValue, actualValue, rule.Value, rule.Value, result)

	return result, nil
}

// getValueFromState retrieves a value from state using a variable path.
// Supports paths like:
//   - "state.score" -> state["score"]
//   - "state.user.name" -> state["user"]["name"]
//   - "score" -> state["score"] (assumes "state." prefix)
func getValueFromState(state graph.State, variablePath string) (interface{}, error) {
	if variablePath == "" {
		return nil, fmt.Errorf("variable path is empty")
	}

	// Remove "state." prefix if present
	path := strings.TrimPrefix(variablePath, "state.")

	// For simple paths (no dots), directly access the state
	if !strings.Contains(path, ".") {
		value, exists := state[path]
		if !exists {
			// Return nil if field doesn't exist (not an error, allows null checks)
			return nil, nil
		}
		return value, nil
	}

	// Split path by "." for nested access
	parts := strings.Split(path, ".")

	// First, get the top-level field from state
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid path: %s", path)
	}

	topLevelValue, exists := state[parts[0]]
	if !exists {
		// Return nil if field doesn't exist (not an error, allows null checks)
		return nil, nil
	}

	// If there's only one part, return the value directly
	if len(parts) == 1 {
		return topLevelValue, nil
	}

	// Navigate through nested fields
	var current any = topLevelValue
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			continue
		}

		// Try to access as map
		currentMap, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot access field %s: parent is not a map (type: %T)", part, current)
		}

		value, exists := currentMap[part]
		if !exists {
			// Return nil if field doesn't exist (not an error, allows null checks)
			return nil, nil
		}

		current = value
	}

	return current, nil
}

// allTrue returns true if all values in the slice are true.
func allTrue(values []bool) bool {
	for _, v := range values {
		if !v {
			return false
		}
	}
	return true
}

// anyTrue returns true if any value in the slice is true.
func anyTrue(values []bool) bool {
	for _, v := range values {
		if v {
			return true
		}
	}
	return false
}
