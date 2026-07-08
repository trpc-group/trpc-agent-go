//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

const (
	candidateSuccess     = "SUCCESS_PROMPT: call tools only when needed, preserve final answer format, and verify numeric facts."
	candidateIneffective = "INEFFECTIVE_PROMPT: be helpful and concise."
	candidateOverfit     = "OVERFIT_PROMPT: optimize train cases aggressively even if validation formatting changes."
)

type fakeOptimizer struct{}

func (fakeOptimizer) Candidates(ctx context.Context, req regressionloop.OptimizationRequest) ([]regressionloop.Candidate, error) {
	return []regressionloop.Candidate{
		{Round: 1, Prompt: candidateSuccess, Reason: "fix tool and format failures without changing validation-critical behavior"},
		{Round: 2, Prompt: candidateIneffective, Reason: "generic rewrite with no material validation gain"},
		{Round: 3, Prompt: candidateOverfit, Reason: "train-focused prompt that causes validation regression"},
	}, nil
}
