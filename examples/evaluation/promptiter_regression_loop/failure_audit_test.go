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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingGenerator struct{}

func (failingGenerator) Generate(context.Context, string, string) (generationResult, error) {
	return generationResult{Usage: generationUsage{Calls: 2}}, errors.New("upstream timeout")
}

func TestEvaluationAuditsModelFailureInsteadOfAborting(t *testing.T) {
	set := evalSetFile{EvalSetID: "failure-set", EvalCases: []caseSpec{{
		EvalID: "failure-case",
		Conversation: []invocationSpec{{
			InvocationID:  "failure-case-1",
			UserContent:   messageSpec{Role: "user", Content: "hello"},
			FinalResponse: messageSpec{Role: "assistant", Content: "hello"},
		}},
	}}}
	result, err := evaluatePrompt(context.Background(), set, "prompt", 1, failingGenerator{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Runs, 1)
	run := result[0].Runs[0]
	assert.False(t, run.Passed)
	assert.Equal(t, 2, run.Usage.Calls)
	assert.Equal(t, FailureCategoryEnvironment, run.Attribution.Category)
	assert.Contains(t, run.Error, "upstream timeout")
	assert.NotEmpty(t, run.Trace)
}
