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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestCandidateFromGradientsUsesObservedEvidenceOnly(t *testing.T) {
	candidate := candidateFromGradients("baseline", []promptiter.SurfaceGradient{{
		Gradient: "required directive ROUTE_EXPLICITLY missing",
	}})
	assert.Contains(t, candidate, "ROUTE_EXPLICITLY")
	assert.NotContains(t, candidate, "VALIDATE_TOOL_ARGUMENTS")
	assert.NotContains(t, candidate, "PRESERVE_SAFETY_CONSTRAINTS")
}

func TestPromptIterCandidateIsDerivedFromTrainingFailures(t *testing.T) {
	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)
	candidate, audit, err := runDeterministicPromptIter(context.Background(), cfg)
	require.NoError(t, err)
	for _, spec := range cfg.Train.EvalCases {
		if spec.RequiredDirective != "" {
			assert.Contains(t, candidate, spec.RequiredDirective)
		}
	}
	assert.NotEqual(t, strings.TrimSpace(cfg.Prompt), candidate)
	require.NotEmpty(t, audit.Rounds)
	assert.Equal(t, candidate, audit.Rounds[0].CandidatePrompt)
}
