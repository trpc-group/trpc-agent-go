//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package criterion

import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"

// options aggregates configurable parts of Criterion.
type options struct {
	// ToolTrajectory sets the default tool trajectory criterion.
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion
}

// newOptions creates a Options with the provided options.
func newOptions(opt ...Option) *options {
	opts := &options{
		ToolTrajectory: tooltrajectory.New(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures Criterion.
type Option func(*options)

// WithToolTrajectory sets the tool trajectory criterion.
func WithToolTrajectory(toolTrajectory *tooltrajectory.ToolTrajectoryCriterion) Option {
	return func(o *options) {
		o.ToolTrajectory = toolTrajectory
	}
}
