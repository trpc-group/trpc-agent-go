//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package comparator provides interfaces for comparing invocations.
package comparator

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// Comparator defines the interface for comparing two invocations.
// Used by Pass@k evaluator to determine if two runs are consistent.
type Comparator interface {
	// IsConsistent checks if the actual invocation is consistent with the
	// expected invocation.
	IsConsistent(
		ctx context.Context,
		expected, actual *evalset.Invocation,
	) (bool, error)
}

// ConversationComparator defines the interface for comparing two conversations.
type ConversationComparator interface {
	// IsConversationConsistent checks if the actual conversation is consistent
	// with the expected conversation.
	IsConversationConsistent(
		ctx context.Context,
		expected, actual []*evalset.Invocation,
	) (bool, float64, error)
}

// ToolTrajectoryComparator compares tool call trajectories.
type ToolTrajectoryComparator struct {
	// StrictOrder requires tool calls to be in the same order.
	StrictOrder bool
	// StrictArgs requires tool call arguments to match exactly.
	StrictArgs bool
}

// NewToolTrajectoryComparator creates a new ToolTrajectoryComparator.
func NewToolTrajectoryComparator(strictOrder, strictArgs bool) *ToolTrajectoryComparator {
	return &ToolTrajectoryComparator{
		StrictOrder: strictOrder,
		StrictArgs:  strictArgs,
	}
}

// IsConsistent checks if tool trajectories are consistent.
func (c *ToolTrajectoryComparator) IsConsistent(
	ctx context.Context,
	expected, actual *evalset.Invocation,
) (bool, error) {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil, nil
	}
	expectedTools := expected.Tools
	actualTools := actual.Tools
	if len(expectedTools) != len(actualTools) {
		return false, nil
	}
	if c.StrictOrder {
		for i := range expectedTools {
			if !c.toolsMatch(expectedTools[i], actualTools[i]) {
				return false, nil
			}
		}
		return true, nil
	}
	// Non-strict order: check if all expected tools exist in actual.
	matched := make([]bool, len(actualTools))
	for _, exp := range expectedTools {
		found := false
		for j, act := range actualTools {
			if !matched[j] && c.toolsMatch(exp, act) {
				matched[j] = true
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func (c *ToolTrajectoryComparator) toolsMatch(
	expected, actual *evalset.Tool,
) bool {
	if expected.Name != actual.Name {
		return false
	}
	if !c.StrictArgs {
		return true
	}
	exp, ok1 := expected.Arguments.(map[string]any)
	act, ok2 := actual.Arguments.(map[string]any)
	if !ok1 || !ok2 {
		// If type assertion fails, treat nil/empty as equal.
		return expected.Arguments == nil && actual.Arguments == nil
	}
	return argsEqual(exp, act)
}

func argsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
