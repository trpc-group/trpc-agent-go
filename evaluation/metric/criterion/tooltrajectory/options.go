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
var (
	defaultJsonCriterion          = json.New()
	defaultToolTrajectoryStrategy = &ToolTrajectoryStrategy{
		Name:      &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		Arguments: defaultJsonCriterion,
		Result:    defaultJsonCriterion,
	}
)

// options configures ToolTrajectoryCriterion.
type options struct {
	// defaultStrategy sets the fallback strategy when no tool-specific strategy is defined.
	defaultStrategy *ToolTrajectoryStrategy
	// toolStrategy configures per-tool strategies keyed by tool name.
	toolStrategy map[string]*ToolTrajectoryStrategy
	// orderSensitive enforces ordered matching when true; when false, tools can match out of order.
	orderSensitive bool
	// subsetMatching allows expected tool list to be a subset of actual list.
	subsetMatching bool
	// compare allows overriding comparison logic entirely.
	compare func(actual, expected *evalset.Invocation) (bool, error)
}

// newOptions applies provided options for ToolTrajectoryCriterion.
func newOptions(opt ...Option) *options {
	opts := &options{
		defaultStrategy: defaultToolTrajectoryStrategy,
		toolStrategy:    nil,
		orderSensitive:  false,
		subsetMatching:  false,
		compare:         nil,
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

// WithOrderSensitive controls whether tool matching must follow sequence order.
func WithOrderSensitive(orderSensitive bool) Option {
	return func(o *options) {
		o.orderSensitive = orderSensitive
	}
}

// WithSubsetMatching allows expected tool list to be a subset of actual list.
func WithSubsetMatching(subsetMatching bool) Option {
	return func(o *options) {
		o.subsetMatching = subsetMatching
	}
}

// WithCompare sets the tool trajectory comparison logic.
func WithCompare(compare func(actual, expected *evalset.Invocation) (bool, error)) Option {
	return func(o *options) {
		o.compare = compare
	}
}
