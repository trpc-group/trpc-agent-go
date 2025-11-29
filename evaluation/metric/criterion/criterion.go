//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package criterion provides configurable evaluation criteria.
package criterion

import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"

// Criterion encapsulates multiple evaluation criteria for comprehensive model behavior assessment.
type Criterion struct {
	// ToolTrajectory configures checks for tool call and response sequences.
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion `json:"toolTrajectory,omitempty"`
}

// New creates a Criterion with the provided options.
func New(opt ...Option) *Criterion {
	opts := newOptions(opt...)
	return &Criterion{
		ToolTrajectory: opts.ToolTrajectory,
	}
}
