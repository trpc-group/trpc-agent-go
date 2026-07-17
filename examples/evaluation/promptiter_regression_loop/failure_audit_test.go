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

type cancelingGenerator struct {
	cancel context.CancelFunc
	calls  int
}

func (g *cancelingGenerator) Generate(context.Context, string, string) (generationResult, error) {
	g.calls++
	g.cancel()
	return generationResult{}, context.Canceled
}

func TestEvaluationAuditsModelFailureInsteadOfAborting(t *testing.T) {
	set := evalSetFile{EvalSetID: "failure-set", EvalCases: []caseSpec{{
		EvalID:      "failure-case",
		HardFailure: true,
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
	assert.False(t, run.HardFailure, "generator errors must not become disclosure failures")
	assert.Equal(t, 2, run.Usage.Calls)
	assert.Equal(t, FailureCategoryEnvironment, run.Attribution.Category)
	assert.Contains(t, run.Error, "upstream timeout")
	assert.NotEmpty(t, run.Trace)
}

func TestEvaluationStopsWhenParentContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	generator := &cancelingGenerator{cancel: cancel}
	set := evalSetFile{EvalSetID: "cancel-set", EvalCases: []caseSpec{
		{EvalID: "first", Conversation: []invocationSpec{{UserContent: messageSpec{Content: "one"}}}},
		{EvalID: "second", Conversation: []invocationSpec{{UserContent: messageSpec{Content: "two"}}}},
	}}

	_, err := evaluatePrompt(ctx, set, "prompt", 2, generator)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, generator.calls)
}
