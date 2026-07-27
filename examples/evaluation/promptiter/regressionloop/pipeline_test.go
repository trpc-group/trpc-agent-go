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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestPipelineNeverPassesHeldOutValidationToPromptIter(t *testing.T) {
	engine := &engineSpy{outputs: []*promptiter.Profile{profileWithPrompt("structure-1", "balanced")}}
	pipeline := testPipeline(engine, []float64{0.5, 0.5, 0.8})

	_, err := pipeline.run(context.Background())
	require.NoError(t, err)
	require.Len(t, engine.requests, 1)
	request := engine.requests[0]
	assert.Equal(t, "train", request.Train[0].EvalSetID)
	assert.Equal(t, "train", request.Validation[0].EvalSetID)
	assert.NotEqual(t, "validation", request.Validation[0].EvalSetID)
	assert.Equal(t, 1, request.MaxRounds)
}

func TestRejectedCandidateDoesNotAdvanceReleasedOrSearchProfile(t *testing.T) {
	engine := &engineSpy{outputs: []*promptiter.Profile{profileWithPrompt("structure-1", "overfit")}}
	pipeline := testPipeline(engine, []float64{0.5, 0.5, 0.2})

	result, err := pipeline.run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, result.InitialProfile, result.ReleasedProfile)
	assert.Equal(t, result.InitialProfile, result.SearchProfile)
	require.Len(t, result.Rounds, 1)
	assert.False(t, result.Rounds[0].Gate.Accepted)
}

func TestAcceptedCandidateAdvancesReleasedAndSearchProfile(t *testing.T) {
	candidate := profileWithPrompt("structure-1", "balanced")
	pipeline := testPipeline(&engineSpy{outputs: []*promptiter.Profile{candidate}}, []float64{0.5, 0.5, 0.8})

	result, err := pipeline.run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, candidate, result.ReleasedProfile)
	assert.Equal(t, candidate, result.SearchProfile)
}

func TestRejectedCandidateIsNotNextRoundInput(t *testing.T) {
	engine := &engineSpy{outputs: []*promptiter.Profile{
		profileWithPrompt("structure-1", "overfit"),
		profileWithPrompt("structure-1", "balanced"),
	}}
	pipeline := testPipeline(engine, []float64{0.5, 0.5, 0.2, 0.8})
	pipeline.cfg.MaxRounds = 2

	result, err := pipeline.run(context.Background())
	require.NoError(t, err)
	require.Len(t, engine.requests, 2)
	assert.Equal(t, result.InitialProfile, engine.requests[1].InitialProfile)
}

func TestCanceledRoundRemainsAuditedAndDoesNotAdvance(t *testing.T) {
	engine := &engineSpy{runErr: context.Canceled}
	pipeline := testPipeline(engine, []float64{0.5, 0.5})

	result, err := pipeline.run(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, result.Rounds, 1)
	assert.Equal(t, result.InitialProfile, result.ReleasedProfile)
	assert.Contains(t, failedCheckIDs(result.Rounds[0].Gate), "run_status")
}

type engineSpy struct {
	requests []*promptiterengine.RunRequest
	outputs  []*promptiter.Profile
	runErr   error
}

func (e *engineSpy) Describe(context.Context) (*astructure.Snapshot, error) {
	text := "baseline"
	return &astructure.Snapshot{
		StructureID: "structure-1", EntryNodeID: candidateAgentName,
		Nodes: []astructure.Node{{NodeID: candidateAgentName, Kind: astructure.NodeKindLLM}},
		Surfaces: []astructure.Surface{{
			SurfaceID: astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction),
			NodeID:    candidateAgentName, Type: astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{Text: &text},
		}},
	}, nil
}

func (e *engineSpy) Run(_ context.Context, request *promptiterengine.RunRequest, _ ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
	e.requests = append(e.requests, request)
	if e.runErr != nil {
		return nil, e.runErr
	}
	output := e.outputs[len(e.requests)-1]
	return &promptiterengine.RunResult{
		Status: promptiterengine.RunStatusSucceeded,
		Rounds: []promptiterengine.RoundResult{{Round: 1, OutputProfile: output}},
	}, nil
}

type evaluatorSequence struct {
	scores []float64
	calls  int
}

func (e *evaluatorSequence) Evaluate(_ context.Context, evalSetID string, _ ...evaluation.Option) (*evaluation.EvaluationResult, error) {
	score := e.scores[e.calls]
	e.calls++
	evalStatus := status.EvalStatusPassed
	if score < 0.5 {
		evalStatus = status.EvalStatusFailed
	}
	return &evaluation.EvaluationResult{
		EvalSetID: evalSetID, OverallStatus: evalStatus,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID: "case-1", OverallStatus: evalStatus,
			MetricResults: []*evalresult.EvalMetricResult{{MetricName: "quality", Score: score, EvalStatus: evalStatus}},
		}},
	}, nil
}

func (*evaluatorSequence) Close() error { return nil }

func testPipeline(engine promptiterengine.Engine, scores []float64) *pipeline {
	cfg := validConfig()
	cfg.MaxRounds = 1
	cfg.MinValidationGain = 0.1
	return &pipeline{
		cfg: cfg, engine: engine, evaluator: &evaluatorSequence{scores: scores}, ledger: newLedger(),
		trainCatalog:      &catalog{EvalSetID: "train", EvalCaseIDs: []string{"case-1"}, MetricNames: []string{"quality"}},
		validationCatalog: validationCatalog("case-1", "quality"),
		targetSurfaceID:   astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction),
	}
}

func profileWithPrompt(structureID, prompt string) *promptiter.Profile {
	return &promptiter.Profile{StructureID: structureID, Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction),
		Value:     astructure.SurfaceValue{Text: &prompt},
	}}}
}
