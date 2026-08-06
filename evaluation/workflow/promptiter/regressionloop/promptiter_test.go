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
	assert.Nil(t, CandidatesFromPromptIterResult(nil, "agent#instruction"))

	prompt := "candidate prompt"
	wrongPrompt := "wrong prompt"
	emptyPrompt := ""
	result := &promptiterengine.RunResult{
		Rounds: []promptiterengine.RoundResult{
			{
				Round: 1,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
					{
						SurfaceID: "agent#model",
						Value:     structure.SurfaceValue{Text: &wrongPrompt},
					},
					{
						SurfaceID: "agent#instruction",
						Value:     structure.SurfaceValue{Text: &prompt},
					},
				}},
				Acceptance: &promptiterengine.AcceptanceDecision{Reason: "accepted by promptiter"},
			},
			{
				Round:         2,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "agent#model"}}},
			},
			{
				Round: 3,
			},
			{
				Round: 4,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
					SurfaceID: "agent#instruction",
				}}},
			},
			{
				Round: 5,
				OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
					SurfaceID: "agent#instruction",
					Value:     structure.SurfaceValue{Text: &emptyPrompt},
				}}},
				Acceptance: &promptiterengine.AcceptanceDecision{Reason: "intentionally empty"},
			},
		},
	}

	candidates := CandidatesFromPromptIterResult(result, "agent#instruction")
	require.Len(t, candidates, 2)
	assert.Equal(t, Candidate{Round: 1, Prompt: prompt, Reason: "accepted by promptiter"}, candidates[0])
	assert.Equal(t, Candidate{Round: 5, Prompt: "", Reason: "intentionally empty"}, candidates[1])
}
