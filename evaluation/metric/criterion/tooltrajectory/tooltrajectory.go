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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/maptext"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
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
	Compare func(actual, expected *evalset.Invocation) error `json:"-"`
}

// ToolTrajectoryStrategy defines comparison strategies for a single tool.
type ToolTrajectoryStrategy struct {
	Name      *text.TextCriterion       `json:"name,omitempty"`      // Name compares tool names.
	Arguments *maptext.MapTextCriterion `json:"arguments,omitempty"` // Arguments compares tool call arguments.
	Response  *maptext.MapTextCriterion `json:"response,omitempty"`  // Response compares tool call responses.
}
