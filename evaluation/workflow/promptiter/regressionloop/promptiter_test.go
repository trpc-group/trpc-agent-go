//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestCandidatesFromPromptIterResult(t *testing.T) {
	assert.Nil(t, CandidatesFromPromptIterResult(nil))

	prompt := "candidate prompt"
	result := &promptiterengine.RunResult{
		Rounds: []promptiterengine.RoundResult{
			{
				Round: 1,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
					SurfaceID: "agent#instruction",
					Value:     structure.SurfaceValue{Text: &prompt},
				}}},
				Acceptance: &promptiterengine.AcceptanceDecision{Reason: "accepted by promptiter"},
			},
			{
				Round:         2,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "agent#model"}}},
			},
			{
				Round: 3,
			},
		},
	}

	candidates := CandidatesFromPromptIterResult(result)
	require.Len(t, candidates, 3)
	assert.Equal(t, Candidate{Round: 1, Prompt: prompt, Reason: "accepted by promptiter"}, candidates[0])
	assert.Equal(t, Candidate{Round: 2}, candidates[1])
	assert.Equal(t, Candidate{Round: 3}, candidates[2])
}
