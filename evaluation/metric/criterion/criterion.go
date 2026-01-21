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

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// Criterion encapsulates multiple evaluation criteria for comprehensive model behavior assessment.
type Criterion struct {
	// ToolTrajectory configures checks for tool call and response sequences.
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion `json:"toolTrajectory,omitempty"`
	// FinalResponse configures checks for the agent final response content.
	FinalResponse *finalresponse.FinalResponseCriterion `json:"finalResponse,omitempty"`
	// LLMJudge configures the LLM-based judge criterion.
	LLMJudge *llm.LLMCriterion `json:"llmJudge,omitempty"`
}

// New creates a Criterion with the provided options.
func New(opt ...Option) *Criterion {
	opts := newOptions(opt...)
	return &Criterion{
		ToolTrajectory: opts.toolTrajectory,
		FinalResponse:  opts.finalResponse,
		LLMJudge:       opts.llmJudge,
	}
}
