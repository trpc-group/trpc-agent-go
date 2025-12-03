//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tooltrajectory

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// defaultToolTrajectoryStrategy is used when no user strategy is supplied.
var defaultToolTrajectoryStrategy = &ToolTrajectoryStrategy{
	Name:      &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	Arguments: &json.JSONCriterion{MatchStrategy: json.JSONMatchStrategyExact},
	Response:  &json.JSONCriterion{MatchStrategy: json.JSONMatchStrategyExact},
}

// options configures ToolTrajectoryCriterion.
type options struct {
	// defaultStrategy sets the fallback strategy when no tool-specific strategy is defined.
	defaultStrategy *ToolTrajectoryStrategy
	// toolStrategy configures per-tool strategies keyed by tool name.
	toolStrategy map[string]*ToolTrajectoryStrategy
	// orderInsensitive toggles order-agnostic comparison for args and responses.
	orderInsensitive bool
	// compare allows overriding comparison logic entirely.
	compare func(actual, expected *evalset.Invocation) (bool, error)
}

// newOptions applies provided options for ToolTrajectoryCriterion.
func newOptions(opt ...Option) *options {
	opts := &options{
		defaultStrategy:  defaultToolTrajectoryStrategy,
		toolStrategy:     nil,
		orderInsensitive: false,
		compare:          nil,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures ToolTrajectoryCriterion.
type Option func(*options)

// WithDefault sets the default tool trajectory strategy.
func WithDefault(defaultStrategy *ToolTrajectoryStrategy) Option {
	return func(o *options) {
		o.defaultStrategy = defaultStrategy
	}
}

// WithTool sets the per-tool strategies keyed by tool name.
func WithTool(tool map[string]*ToolTrajectoryStrategy) Option {
	return func(o *options) {
		o.toolStrategy = tool
	}
}

// WithOrderInsensitive sets the order-agnostic comparison for tool calls and responses.
func WithOrderInsensitive(orderInsensitive bool) Option {
	return func(o *options) {
		o.orderInsensitive = orderInsensitive
	}
}

// WithCompare sets the tool trajectory comparison logic.
func WithCompare(compare func(actual, expected *evalset.Invocation) (bool, error)) Option {
	return func(o *options) {
		o.compare = compare
	}
}
