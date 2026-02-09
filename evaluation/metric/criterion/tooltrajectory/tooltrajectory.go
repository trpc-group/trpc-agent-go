//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tooltrajectory defines tool trajectory comparison criteria.
package tooltrajectory

import (
	"fmt"

	"github.com/hashicorp/go-multierror"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory/internal/kuhn"
)

// New creates a ToolTrajectoryCriterion with the provided options.
func New(opt ...Option) *ToolTrajectoryCriterion {
	opts := newOptions(opt...)
	return &ToolTrajectoryCriterion{
		DefaultStrategy: opts.defaultStrategy,
		ToolStrategy:    opts.toolStrategy,
		OrderSensitive:  opts.orderSensitive,
		SubsetMatching:  opts.subsetMatching,
		Compare:         opts.compare,
	}
}

// ToolTrajectoryCriterion provides comparison rules for tool call and response sequences.
type ToolTrajectoryCriterion struct {
	// DefaultStrategy applies when no tool-specific strategy is provided.
	DefaultStrategy *ToolTrajectoryStrategy `json:"defaultStrategy,omitempty"`
	// ToolStrategy holds per-tool strategies keyed by tool name.
	ToolStrategy map[string]*ToolTrajectoryStrategy `json:"toolStrategy,omitempty"`
	// OrderSensitive requires tools to match in sequence when true; when false, matching is order-agnostic.
	OrderSensitive bool `json:"orderSensitive,omitempty"`
	// SubsetMatching allows expected tool list to be a subset of actual list.
	SubsetMatching bool `json:"subsetMatching,omitempty"`
	// Compare allows custom comparison override.
	Compare func(actual, expected *evalset.Invocation) (bool, error) `json:"-"`
}

// ToolTrajectoryStrategy defines comparison strategies for a single tool.
type ToolTrajectoryStrategy struct {
	Name      *text.TextCriterion          `json:"name,omitempty"`      // Name compares tool names.
	Arguments *criterionjson.JSONCriterion `json:"arguments,omitempty"` // Arguments compares tool call arguments.
	Result    *criterionjson.JSONCriterion `json:"result,omitempty"`    // Result compares tool call results.
}

// Match compares actual and expected invocations according to tool trajectory rules.
func (t *ToolTrajectoryCriterion) Match(actual, expected *evalset.Invocation) (bool, error) {
	if t.Compare != nil {
		return t.Compare(actual, expected)
	}
	if actual == nil || expected == nil {
		return false, fmt.Errorf("actual or expected invocation is nil")
	}
	if len(actual.Tools) == 0 && len(expected.Tools) == 0 {
		return true, nil
	}
	if err := t.validateToolCounts(actual, expected); err != nil {
		return false, fmt.Errorf("validate tool counts: %w", err)
	}
	var err error
	if t.OrderSensitive {
		err = t.orderedMatch(actual.Tools, expected.Tools)
	} else {
		err = t.unorderedMatch(actual.Tools, expected.Tools)
	}
	if err != nil {
		return false, fmt.Errorf("match tools: %w", err)
	}
	return true, nil
}

// validateToolCounts validates the tool counts of actual and expected invocations.
func (t *ToolTrajectoryCriterion) validateToolCounts(actual, expected *evalset.Invocation) error {
	numActualTools := len(actual.Tools)
	numExpectedTools := len(expected.Tools)
	if t.SubsetMatching {
		if numActualTools < numExpectedTools {
			return fmt.Errorf("number of tool calls mismatch: actual(%d) < expected(%d)",
				numActualTools, numExpectedTools)
		}
		return nil
	}
	if numActualTools != numExpectedTools {
		return fmt.Errorf("number of tool calls mismatch: actual(%d) != expected(%d)", numActualTools, numExpectedTools)
	}
	return nil
}

// orderedMatch matches actual and expected tool calls in order.
func (t *ToolTrajectoryCriterion) orderedMatch(actual, expected []*evalset.Tool) error {
	actualIdx := -1
	for expectedIdx := range len(expected) {
		if actualIdx == len(actual)-1 {
			return fmt.Errorf("tool id %s with name %s mismatch", expected[expectedIdx].ID, expected[expectedIdx].Name)
		}
		var err error
		for actualIdx+1 < len(actual) {
			actualIdx++
			if err = t.matchTool(actual[actualIdx], expected[expectedIdx]); err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("tool id %s with name %s mismatch: %w",
				expected[expectedIdx].ID, expected[expectedIdx].Name, err)
		}
	}
	return nil
}

// unorderedMatch matches actual and expected tool calls in no order.
func (t *ToolTrajectoryCriterion) unorderedMatch(actual, expected []*evalset.Tool) error {
	leftSize := len(expected)
	rightSize := len(actual)
	matcher := kuhn.New(leftSize, rightSize)
	for i := range leftSize {
		for j := range rightSize {
			if t.matchTool(actual[j], expected[i]) == nil {
				matcher.AddEdge(i, j)
			}
		}
	}
	unmatchedLeft, err := matcher.FullLeftMatch()
	if err == nil {
		return nil
	}
	for _, left := range unmatchedLeft {
		err = multierror.Append(err, fmt.Errorf("tool id %s with name %s mismatch",
			expected[left].ID, expected[left].Name))
	}
	return err
}

// matchTool matches a single tool call and response.
func (t *ToolTrajectoryCriterion) matchTool(actualTool, expectedTool *evalset.Tool) error {
	if actualTool == nil || expectedTool == nil {
		return fmt.Errorf("actual or expected tool is nil")
	}
	strategy := t.getStrategy(actualTool, expectedTool)
	ok, err := strategy.Match(actualTool, expectedTool)
	if err != nil {
		return fmt.Errorf("actual tool %s named %s mismatch with expected tool %s named %s: %w",
			actualTool.ID, actualTool.Name, expectedTool.ID, expectedTool.Name, err)
	}
	if !ok {
		return fmt.Errorf("actual tool %s named %s mismatch with expected tool %s named %s",
			actualTool.ID, actualTool.Name, expectedTool.ID, expectedTool.Name)
	}
	return nil
}

// getStrategy picks the comparison strategy for a specific tool pair.
func (t *ToolTrajectoryCriterion) getStrategy(actualTool, expectedTool *evalset.Tool) *ToolTrajectoryStrategy {
	if t.ToolStrategy != nil {
		strategy, ok := t.ToolStrategy[actualTool.Name]
		if ok {
			return strategy
		}
		strategy, ok = t.ToolStrategy[expectedTool.Name]
		if ok {
			return strategy
		}
	}
	if t.DefaultStrategy != nil {
		return t.DefaultStrategy
	}
	return defaultToolTrajectoryStrategy
}

// Match compares a single tool call against the expected tool call using the configured strategies.
func (t *ToolTrajectoryStrategy) Match(actual, expected *evalset.Tool) (bool, error) {
	if t.Name != nil {
		ok, err := t.Name.Match(actual.Name, expected.Name)
		if err != nil {
			return false, fmt.Errorf("name mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("name mismatch")
		}
	}
	if t.Arguments != nil {
		ok, err := t.Arguments.Match(actual.Arguments, expected.Arguments)
		if err != nil {
			return false, fmt.Errorf("arguments mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("arguments mismatch")
		}
	}
	if t.Result != nil {
		ok, err := t.Result.Match(actual.Result, expected.Result)
		if err != nil {
			return false, fmt.Errorf("result mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("result mismatch")
		}
	}
	return true, nil
}
