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
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// New creates a ToolTrajectoryCriterion with the provided options.
func New(opt ...Option) *ToolTrajectoryCriterion {
	opts := newOptions(opt...)
	return &ToolTrajectoryCriterion{
		DefaultStrategy:  opts.defaultStrategy,
		ToolStrategy:     opts.toolStrategy,
		OrderInsensitive: opts.orderInsensitive,
		Compare:          opts.compare,
	}
}

// ToolTrajectoryCriterion provides comparison rules for tool call and response sequences.
type ToolTrajectoryCriterion struct {
	// DefaultStrategy applies when no tool-specific strategy is provided.
	DefaultStrategy *ToolTrajectoryStrategy `json:"defaultStrategy,omitempty"`
	// ToolStrategy holds per-tool strategies keyed by tool name.
	ToolStrategy map[string]*ToolTrajectoryStrategy `json:"toolStrategy,omitempty"`
	// OrderInsensitive toggles comparison order for args and responses.
	OrderInsensitive bool `json:"orderInsensitive,omitempty"`
	// Compare allows custom comparison override.
	Compare func(actual, expected *evalset.Invocation) (bool, error) `json:"-"`
}

// ToolTrajectoryStrategy defines comparison strategies for a single tool.
type ToolTrajectoryStrategy struct {
	Name      *text.TextCriterion          `json:"name,omitempty"`      // Name compares tool names.
	Arguments *criterionjson.JSONCriterion `json:"arguments,omitempty"` // Arguments compares tool call arguments.
	Response  *criterionjson.JSONCriterion `json:"response,omitempty"`  // Response compares tool call responses.
}

// Match compares actual and expected invocations according to tool trajectory rules.
func (t *ToolTrajectoryCriterion) Match(actual, expected *evalset.Invocation) (bool, error) {
	if t.Compare != nil {
		return t.Compare(actual, expected)
	}
	if actual == nil || expected == nil {
		return false, fmt.Errorf("actual or expected invocation is nil")
	}
	if actual.IntermediateData == nil || expected.IntermediateData == nil {
		return false, fmt.Errorf("actual or expected intermediate data is nil")
	}
	// Ensure one-to-one mapping between tool calls and responses on actual invocation.
	if len(actual.IntermediateData.ToolCalls) != len(actual.IntermediateData.ToolResponses) {
		return false, fmt.Errorf("tool calls and tool responses count mismatch: %d != %d",
			len(actual.IntermediateData.ToolCalls), len(actual.IntermediateData.ToolResponses))
	}
	// Ensure one-to-one mapping between tool calls and responses on expected invocation.
	if len(expected.IntermediateData.ToolCalls) != len(expected.IntermediateData.ToolResponses) {
		return false, fmt.Errorf("tool calls and tool responses count mismatch: %d != %d",
			len(expected.IntermediateData.ToolCalls), len(expected.IntermediateData.ToolResponses))
	}
	// Ensure the same number of tool calls before detailed comparison.
	if len(actual.IntermediateData.ToolCalls) != len(expected.IntermediateData.ToolCalls) {
		return false, fmt.Errorf("tool calls count mismatch: %d != %d",
			len(actual.IntermediateData.ToolCalls), len(expected.IntermediateData.ToolCalls))
	}
	if len(actual.IntermediateData.ToolCalls) == 0 {
		return true, nil
	}
	actualTools, err := getToolComparers(
		actual.IntermediateData.ToolCalls,
		actual.IntermediateData.ToolResponses,
		t.OrderInsensitive,
	)
	if err != nil {
		return false, fmt.Errorf("get actual tools: %w", err)
	}
	expectedTools, err := getToolComparers(
		expected.IntermediateData.ToolCalls,
		expected.IntermediateData.ToolResponses,
		t.OrderInsensitive,
	)
	if err != nil {
		return false, fmt.Errorf("get expected tools: %w", err)
	}
	if t.OrderInsensitive {
		sort.Slice(actualTools, func(i, j int) bool {
			return actualTools[i].lessThan(actualTools[j])
		})
		sort.Slice(expectedTools, func(i, j int) bool {
			return expectedTools[i].lessThan(expectedTools[j])
		})
	}
	for i := range len(actualTools) {
		strategy := getStrategy(t, actualTools[i], expectedTools[i])
		ok, err := strategy.match(actualTools[i], expectedTools[i])
		if err != nil {
			return false, fmt.Errorf("tool %s mismatch: %w", actualTools[i].name, err)
		}
		if !ok {
			return false, fmt.Errorf("tool %s mismatch", actualTools[i].name)
		}
	}
	return true, nil
}

// Match validates a single tool call pair using configured criteria.
func (t *ToolTrajectoryStrategy) match(actual, expected *toolComparer) (bool, error) {
	if t.Name != nil {
		ok, err := t.Name.Match(actual.name, expected.name)
		if err != nil {
			return false, fmt.Errorf("name mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("name mismatch")
		}
	}
	if t.Arguments != nil {
		ok, err := t.Arguments.Match(actual.args, expected.args)
		if err != nil {
			return false, fmt.Errorf("arguments mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("arguments mismatch")
		}
	}
	if t.Response != nil {
		ok, err := t.Response.Match(actual.response, expected.response)
		if err != nil {
			return false, fmt.Errorf("response mismatch: %w", err)
		}
		if !ok {
			return false, fmt.Errorf("response mismatch")
		}
	}
	return true, nil
}

// toolComparer normalizes tool call and response data for comparison.
type toolComparer struct {
	name          string         // name holds the tool name.
	args          map[string]any // args holds parsed tool arguments.
	response      map[string]any // response holds parsed tool response payload.
	argsOrder     string         // argsOrder caches JSON for order-insensitive compare.
	responseOrder string         // responseOrder caches JSON for order-insensitive compare.
}

// lessThan provides deterministic ordering when order-insensitive compares require sorting.
func (t *toolComparer) lessThan(other *toolComparer) bool {
	if t.name != other.name {
		return t.name < other.name
	}
	if t.argsOrder != other.argsOrder {
		return t.argsOrder < other.argsOrder
	}
	if t.responseOrder != other.responseOrder {
		return t.responseOrder < other.responseOrder
	}
	return false
}

// getToolComparers aligns tool calls with their responses and builds toolComparer.
func getToolComparers(toolCalls []*model.ToolCall, toolResponses []*model.Message,
	orderInsensitive bool) ([]*toolComparer, error) {
	// toolCallIDs ensures every tool use can be matched by ID.
	// Map from tool call id to index.
	toolCallIDs := make(map[string]int)
	for i := range len(toolCalls) {
		if toolCalls[i] == nil {
			return nil, fmt.Errorf("tool use is nil")
		}
		if toolCalls[i].ID == "" {
			return nil, fmt.Errorf("tool use id is empty")
		}
		if _, ok := toolCallIDs[toolCalls[i].ID]; ok {
			return nil, fmt.Errorf("tool use id %s is duplicated", toolCalls[i].ID)
		}
		toolCallIDs[toolCalls[i].ID] = i
	}
	// toolResponseIDs ensures every tool response can be matched by ID.
	// Map from tool response id to index.
	toolResponseIDs := make(map[string]int)
	for i := range len(toolResponses) {
		if toolResponses[i] == nil {
			return nil, fmt.Errorf("tool response is nil")
		}
		if toolResponses[i].ToolID == "" {
			return nil, fmt.Errorf("tool response id is empty")
		}
		if _, ok := toolResponseIDs[toolResponses[i].ToolID]; ok {
			return nil, fmt.Errorf("tool response id %s is duplicated", toolResponses[i].ToolID)
		}
		toolResponseIDs[toolResponses[i].ToolID] = i
	}
	for toolID := range toolCallIDs {
		if _, ok := toolResponseIDs[toolID]; !ok {
			return nil, fmt.Errorf("tool id %s is missing response", toolID)
		}
	}
	toolComparers := make([]*toolComparer, 0, len(toolCalls))
	for i := range len(toolCalls) {
		toolComparer, err := getToolComparer(
			toolCalls[i],
			toolResponses[toolResponseIDs[toolCalls[i].ID]],
			orderInsensitive,
		)
		if err != nil {
			return nil, fmt.Errorf("get tool comparer: %w", err)
		}
		toolComparers = append(toolComparers, toolComparer)
	}
	return toolComparers, nil
}

// getToolComparer pairs a tool call with its response and precomputes ordering hints.
func getToolComparer(toolCall *model.ToolCall, toolResponse *model.Message,
	orderInsensitive bool) (*toolComparer, error) {
	if toolCall == nil || toolResponse == nil {
		return nil, errors.New("tool call or tool response is nil")
	}
	tool := &toolComparer{
		name: toolCall.Function.Name,
	}
	if len(toolCall.Function.Arguments) > 0 {
		if err := json.Unmarshal(toolCall.Function.Arguments, &tool.args); err != nil {
			return nil, fmt.Errorf("unmarshal arguments: %w", err)
		}
	}
	if toolResponse.Content != "" {
		if err := json.Unmarshal([]byte(toolResponse.Content), &tool.response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
	}
	if orderInsensitive {
		args, err := json.Marshal(tool.args)
		if err != nil {
			return nil, fmt.Errorf("marshal arguments: %w", err)
		}
		response, err := json.Marshal(tool.response)
		if err != nil {
			return nil, fmt.Errorf("marshal response: %w", err)
		}
		tool.argsOrder = string(args)
		tool.responseOrder = string(response)
	}
	return tool, nil
}

// getStrategy picks the comparison strategy for a specific tool pair.
func getStrategy(t *ToolTrajectoryCriterion, actualTool,
	expectedTool *toolComparer) *ToolTrajectoryStrategy {
	if t.ToolStrategy != nil {
		strategy, ok := t.ToolStrategy[actualTool.name]
		if ok {
			return strategy
		}
		strategy, ok = t.ToolStrategy[expectedTool.name]
		if ok {
			return strategy
		}
	}
	if t.DefaultStrategy != nil {
		return t.DefaultStrategy
	}
	return defaultToolTrajectoryStrategy
}
