//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	workflowpromptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestSlimRunResultReturnsOriginalForEmptyInputs(t *testing.T) {
	result := &engine.RunResult{ID: "run-1"}
	assert.Same(t, result, slimRunResult(result, engine.RunResultSlimming{}))
	assert.Nil(t, slimRunResult(nil, engine.RunResultSlimming{OmitStructure: true}))
	slimmed := slimRunResult(&engine.RunResult{ID: "run-2"}, engine.RunResultSlimming{
		OmitStructure:       true,
		OmitEvaluationCases: true,
	})
	require.NotNil(t, slimmed)
	assert.Equal(t, "run-2", slimmed.ID)
	assert.Nil(t, slimmed.Structure)
	assert.Nil(t, slimmed.BaselineValidation)
	assert.Nil(t, slimmed.Rounds)
}

func TestSlimRunResultOmitsConfiguredFields(t *testing.T) {
	result := &engine.RunResult{
		ID:           "run-1",
		Status:       engine.RunStatusSucceeded,
		CurrentRound: 1,
		Structure:    &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
		BaselineValidation: &engine.EvaluationResult{
			OverallScore: 0.5,
			EvalSets: []engine.EvalSetResult{
				{
					EvalSetID:    "validation",
					OverallScore: 0.5,
					Cases:        []engine.CaseResult{{EvalSetID: "validation", EvalCaseID: "case_1"}},
				},
			},
		},
		AcceptedProfile: &workflowpromptiter.Profile{
			StructureID: "structure_1",
			Overrides: []workflowpromptiter.SurfaceOverride{
				{SurfaceID: "node_1#instruction", Value: astructure.SurfaceValue{Text: stringPtrForSlimmingTest("accepted")}},
			},
		},
		Rounds: []engine.RoundResult{
			{
				Round:        1,
				InputProfile: &workflowpromptiter.Profile{StructureID: "structure_1"},
				Train: &engine.EvaluationResult{
					OverallScore: 0.4,
					EvalSets: []engine.EvalSetResult{
						{
							EvalSetID:    "train",
							OverallScore: 0.4,
							Cases:        []engine.CaseResult{{EvalSetID: "train", EvalCaseID: "case_1"}},
						},
					},
				},
				Losses:      []workflowpromptiter.CaseLoss{{EvalSetID: "train", EvalCaseID: "case_1"}},
				Backward:    &engine.BackwardResult{Cases: []engine.CaseBackwardResult{{EvalSetID: "train", EvalCaseID: "case_1"}}},
				Aggregation: &engine.AggregationResult{Surfaces: []workflowpromptiter.AggregatedSurfaceGradient{{SurfaceID: "node_1#instruction", NodeID: "node_1"}}},
				Patches: &workflowpromptiter.PatchSet{
					Patches: []workflowpromptiter.SurfacePatch{
						{SurfaceID: "node_1#instruction", Value: astructure.SurfaceValue{Text: stringPtrForSlimmingTest("candidate")}},
					},
				},
				OutputProfile: &workflowpromptiter.Profile{StructureID: "structure_1"},
				Validation:    &engine.EvaluationResult{OverallScore: 0.7},
				Acceptance:    &engine.AcceptanceDecision{Accepted: true},
				Stop:          &engine.StopDecision{ShouldStop: true},
			},
		},
	}
	slimmed := slimRunResult(result, engine.RunResultSlimming{
		OmitStructure:       true,
		OmitEvaluationCases: true,
		OmitBackward:        true,
		OmitAggregation:     true,
		OmitPatches:         true,
		OmitProfiles:        true,
		OmitLosses:          true,
	})
	require.NotSame(t, result, slimmed)
	assert.Nil(t, slimmed.Structure)
	assert.Nil(t, slimmed.AcceptedProfile)
	require.NotNil(t, slimmed.BaselineValidation)
	require.Len(t, slimmed.BaselineValidation.EvalSets, 1)
	assert.Empty(t, slimmed.BaselineValidation.EvalSets[0].Cases)
	require.Len(t, slimmed.Rounds, 1)
	round := slimmed.Rounds[0]
	assert.Nil(t, round.InputProfile)
	assert.Nil(t, round.OutputProfile)
	assert.Nil(t, round.Backward)
	assert.Nil(t, round.Aggregation)
	assert.Nil(t, round.Patches)
	assert.Empty(t, round.Losses)
	require.NotNil(t, round.Train)
	require.Len(t, round.Train.EvalSets, 1)
	assert.Empty(t, round.Train.EvalSets[0].Cases)
	require.NotNil(t, round.Acceptance)
	require.NotNil(t, round.Stop)
	require.NotNil(t, result.Structure)
	require.Len(t, result.BaselineValidation.EvalSets[0].Cases, 1)
	require.NotNil(t, result.Rounds[0].Backward)
}

func TestRunResponseHandlesNilServer(t *testing.T) {
	run := &engine.RunResult{ID: "run-1"}
	var server *Server
	resp := server.runResponse(run)
	require.NotNil(t, resp)
	assert.Same(t, run, resp.Result)
}

func stringPtrForSlimmingTest(value string) *string {
	return &value
}
