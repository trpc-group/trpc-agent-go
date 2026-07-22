//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

func TestPipelineFeedsTrainAttributionIntoLossHints(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			BaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
			}),
		},
	}
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{
						id:     "train_case",
						metric: "tool_trajectory",
						score:  0,
						status: status.EvalStatusFailed,
						reason: "wrong tool route: used general_support instead of billing",
					},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: iterator,
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}
	_, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, iterator.request)
	require.NotNil(t, iterator.request.InitialProfile)
	require.Len(t, iterator.request.InitialProfile.Overrides, 1)
	assert.Equal(t, "agent#instruction", iterator.request.InitialProfile.Overrides[0].SurfaceID)
	require.NotNil(t, iterator.request.InitialProfile.Overrides[0].Value.Text)
	assert.Equal(t, "baseline prompt", *iterator.request.InitialProfile.Overrides[0].Value.Text)
	require.Len(t, iterator.request.Train, 1)
	require.Len(t, iterator.request.Train[0].LossHints, 1)
	hint := iterator.request.Train[0].LossHints[0]
	assert.Equal(t, "train_case", hint.EvalCaseID)
	assert.Equal(t, "tool_trajectory", hint.MetricName)
	assert.Contains(t, hint.Reason, "failure_category=tool_call_error")
	assert.Contains(t, hint.Reason, "wrong tool route")
}

func TestPipelineDoesNotSendSyntheticInferenceLossHintToPromptIterEngine(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	failedTrace := promptIterTestTrace(atrace.TraceStatusFailed)
	passedTrace := promptIterTestTrace(atrace.TraceStatusCompleted)
	engineEvaluator := &sequenceAgentEvaluator{results: []*evaluation.EvaluationResult{
		genericEvalResult("validation", "validation_case", "final_response", 1, status.EvalStatusPassed, "ok", passedTrace),
		genericEvalResult("train", "train_case", "final_response", 0, status.EvalStatusFailed, "ordinary metric failed", failedTrace),
		genericEvalResult("validation", "validation_case", "final_response", 1, status.EvalStatusPassed, "ok", passedTrace),
	}}
	realEngine, err := promptiterengine.New(
		context.Background(),
		promptiterengine.WithStructure(promptIterTestStructure()),
		promptiterengine.WithAgentEvaluator(engineEvaluator),
		promptiterengine.WithBackwarder(noopBackwarder{}),
		promptiterengine.WithAggregator(noopAggregator{}),
		promptiterengine.WithOptimizer(noopOptimizer{}),
	)
	require.NoError(t, err)
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: &promptiterengine.EvaluationResult{
					EvalSets: []promptiterengine.EvalSetResult{{
						EvalSetID: "train",
						Cases: []promptiterengine.CaseResult{{
							EvalSetID:  "train",
							EvalCaseID: "train_case",
							Trace:      failedTrace,
							Metrics: []promptiterengine.MetricResult{{
								MetricName: "final_response",
								Score:      0,
								Status:     status.EvalStatusFailed,
								Reason:     "ordinary metric failed",
							}},
						}},
					}},
				},
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseCandidateValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: EnginePromptIterator{Engine: realEngine},
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}

	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Report.BaselineFailureAttributions, 2)
	assert.Equal(t, 3, engineEvaluator.index)
}

func TestPipelineUsesCostProviderWithEstimatedModelCallFallback(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			BaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
			Rounds: []promptiterengine.RoundResult{
				{Round: 1},
			},
		},
	}
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: iterator,
		CostProvider:   staticCostProvider{summary: CostSummary{Tokens: 123, Amount: 0.25, Currency: "USD"}},
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false, MaxCost: 1},
	}
	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 5, result.Report.Cost.ModelCalls)
	assert.Equal(t, 123, result.Report.Cost.Tokens)
	assert.Equal(t, 0.25, result.Report.Cost.Amount)
	assert.Equal(t, CostSourceProvider, result.Report.Cost.Source)
	assert.True(t, result.Report.Cost.Estimated)
	assert.True(t, result.Report.GateDecision.Accepted)

	pipeline.CostProvider = staticCostProvider{summary: CostSummary{
		ModelCalls:         5,
		ModelCallsMeasured: true,
		Tokens:             123,
		Amount:             0.25,
		Currency:           "USD",
	}}
	cfg.Gate.MaxModelCalls = 5
	result, err = pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, result.Report.Cost.Estimated)
	assert.True(t, result.Report.GateDecision.Accepted)

	pipeline.CostProvider = staticCostProvider{summary: CostSummary{Tokens: 123}}
	cfg.Gate.MaxModelCalls = 0
	result, err = pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, result.Report.GateDecision.Accepted)
	assert.Contains(t, result.Report.GateDecision.Reasons, "cost amount unavailable; configure CostProvider to enforce maxCost")

	pipeline.CostProvider = staticCostProvider{summary: CostSummary{ModelCallsMeasured: true}}
	cfg.Gate.MaxCost = 0
	result, err = pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Report.Cost.ModelCalls)
	assert.False(t, result.Report.Cost.Estimated)
}

func TestPipelineRejectsEstimatedPositiveProviderModelCalls(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			BaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
			Rounds: []promptiterengine.RoundResult{
				{Round: 1},
			},
		},
	}
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: iterator,
		CostProvider: staticCostProvider{summary: CostSummary{
			ModelCalls: 5,
			Estimated:  true,
		}},
		Clock: &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false, MaxModelCalls: 10},
	}
	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 5, result.Report.Cost.ModelCalls)
	assert.Equal(t, CostSourceProvider, result.Report.Cost.Source)
	assert.True(t, result.Report.Cost.Estimated)
	assert.False(t, result.Report.Cost.ModelCallsMeasured)
	assert.False(t, result.Report.GateDecision.Accepted)
	assert.Contains(t, result.Report.GateDecision.Reasons, "model call count unavailable; configure CostProvider to enforce maxModelCalls")
}

func TestPipelineCountsCandidateAttributionBeforeCostSnapshots(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	judge := &countingJudge{}
	clock := &judgeAwareClock{
		startedAt:  time.Unix(1, 0),
		finishedAt: time.Unix(4, 0),
		judge:      judge,
	}
	costProvider := &judgeAwareCostProvider{judge: judge}
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
						{
							SurfaceID: "agent#instruction",
							Value:     astructure.SurfaceValue{Text: ptrString("candidate prompt")},
						},
					}},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true},
				},
			},
		},
	}
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseCandidateValidation: evalResult("validation", []caseSpec{
					{id: "validation_case", metric: "metric", score: 0, status: status.EvalStatusFailed, reason: "needs judge"},
				}),
			},
		},
		PromptIterator:   iterator,
		CostProvider:     costProvider,
		AttributionJudge: judge,
		Clock:            clock,
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}
	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, judge.calls)
	assert.Equal(t, 101, result.Report.Cost.ModelCalls)
	assert.False(t, result.Report.Cost.Estimated)
	assert.Equal(t, time.Second*3, result.Report.Latency.Duration)
	assert.Equal(t, 1, result.Report.CandidateFailureAttributionSummary.Total)
}

func TestPipelineRendersStructuredToolPatchInReports(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	description := "candidate tool description"
	toolPatch := astructure.SurfaceValue{Tools: []astructure.ToolRef{{ID: "lookup", Description: description}}}
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
						{SurfaceID: "agent#tool.lookup", Value: toolPatch},
					}},
					Patches: &promptiter.PatchSet{
						Patches: []promptiter.SurfacePatch{
							{SurfaceID: "agent#tool.lookup", Value: toolPatch, Reason: "tool patch"},
						},
					},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true},
				},
			},
		},
	}
	pipeline := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain:       evalResult("train", []caseSpec{{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed}}),
				PhaseBaselineValidation:  evalResult("validation", []caseSpec{{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed}}),
				PhaseCandidateValidation: evalResult("validation", []caseSpec{{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed}}),
			},
		},
		PromptIterator: iterator,
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#tool.lookup"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}
	result, err := pipeline.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Report.CandidateSurfaces, 1)
	assert.Equal(t, "agent#tool.lookup", result.Report.CandidateSurfaces[0].SurfaceID)
	require.NotNil(t, result.Report.CandidateSurfaces[0].Value)
	require.Len(t, result.Report.CandidateSurfaces[0].Value.Tools, 1)
	assert.Equal(t, description, result.Report.CandidateSurfaces[0].Value.Tools[0].Description)
	require.Len(t, result.Report.Rounds, 1)
	require.Len(t, result.Report.Rounds[0].Patches, 1)
	require.NotNil(t, result.Report.Rounds[0].Patches[0].Value)
	require.Len(t, result.Report.Rounds[0].Patches[0].Value.Tools, 1)
	assert.Equal(t, description, result.Report.Rounds[0].Patches[0].Value.Tools[0].Description)

	jsonBytes, err := os.ReadFile(result.JSONPath)
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), description)
	assert.Contains(t, string(jsonBytes), `"candidateSurfaces"`)
	assert.Contains(t, string(jsonBytes), `"tools"`)

	mdBytes, err := os.ReadFile(result.MarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(mdBytes), description)
	assert.Contains(t, string(mdBytes), "Candidate Surfaces")
	assert.Contains(t, string(mdBytes), "Round Patches")
}

func TestPipelineRejectsFinalCandidateProfileTargetMismatch(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
						{
							SurfaceID: "agent#tool.lookup",
							Value: astructure.SurfaceValue{
								Tools: []astructure.ToolRef{{ID: "lookup", Description: "candidate tool description"}},
							},
						},
					}},
				},
			},
		},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	_, err := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: iterator,
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "does not match configured target surface")
}

func TestPipelineRejectsCandidateTextWhenTargetSurfaceIdentityIsAmbiguous(t *testing.T) {
	tests := []struct {
		name             string
		targetSurfaceIDs []string
		wantErr          string
	}{
		{
			name:             "multiple configured targets",
			targetSurfaceIDs: []string{"agent#instruction", "router#instruction"},
			wantErr:          "requires exactly one target surface id",
		},
		{
			name:             "single target mismatch",
			targetSurfaceIDs: []string{"router#instruction"},
			wantErr:          "does not match configured target surface",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			promptPath := filepath.Join(dir, "prompt.txt")
			metricsPath := filepath.Join(dir, "metrics.json")
			require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
			require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
			candidatePrompt := "candidate prompt"
			iterator := &capturingPromptIterator{
				result: &promptiterengine.RunResult{
					Rounds: []promptiterengine.RoundResult{
						{
							Round: 1,
							OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
								{
									SurfaceID: "agent#instruction",
									Value:     astructure.SurfaceValue{Text: &candidatePrompt},
								},
							}},
						},
					},
				},
			}
			cfg := Config{
				AppName:             "app",
				PromptSource:        promptPath,
				MetricsPath:         metricsPath,
				TrainEvalSetID:      "train",
				ValidationEvalSetID: "validation",
				OutputJSON:          filepath.Join(dir, "optimization_report.json"),
				OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
				TargetSurfaceIDs:    tt.targetSurfaceIDs,
				PromptIter:          PromptIterConfig{MaxRounds: 1},
			}
			evaluator := &scriptedEvaluator{
				results: map[Phase]*promptiterengine.EvaluationResult{
					PhaseBaselineTrain: evalResult("train", []caseSpec{
						{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
					}),
					PhaseBaselineValidation: evalResult("validation", []caseSpec{
						{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
					}),
				},
			}
			_, err := Pipeline{
				Evaluator:      evaluator,
				PromptIterator: iterator,
				Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
			}.Run(context.Background(), cfg)
			assert.ErrorContains(t, err, tt.wantErr)
			if len(tt.targetSurfaceIDs) == 1 {
				require.Len(t, evaluator.requests, 2)
			} else {
				assert.Empty(t, evaluator.requests)
			}
		})
	}
}

func TestPipelineRerunsFinalCandidateValidation(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	candidatePrompt := "candidate prompt"
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			BaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 0, status: status.EvalStatusFailed},
			}),
			Rounds: []promptiterengine.RoundResult{
				{
					Round:      1,
					Validation: evalResult("validation", []caseSpec{{id: "validation_case", metric: "metric", score: 0, status: status.EvalStatusFailed}}),
					OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
						{
							SurfaceID: "agent#instruction",
							Value:     astructure.SurfaceValue{Text: &candidatePrompt},
						},
					}},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true},
				},
			},
		},
	}
	evaluator := &scriptedEvaluator{
		results: map[Phase]*promptiterengine.EvaluationResult{
			PhaseBaselineTrain: evalResult("train", []caseSpec{
				{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
			PhaseBaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 0, status: status.EvalStatusFailed},
			}),
			PhaseCandidateValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
		},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}
	result, err := Pipeline{
		Evaluator:      evaluator,
		PromptIterator: iterator,
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, evaluator.requests, 3)
	assert.Equal(t, PhaseCandidateValidation, evaluator.requests[2].Phase)
	assert.Equal(t, candidatePrompt, evaluator.requests[2].Prompt)
	require.NotNil(t, result.Report.CandidateValidation)
	assert.Equal(t, 1.0, result.Report.CandidateValidation.OverallScore)
	assert.Equal(t, 1, result.Report.Delta.Summary.NewlyPassed)
	assert.Equal(t, 6, result.Report.Cost.ModelCalls)
}

func TestPipelineRerunsFinalToolCandidateValidation(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline tool description"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{
		result: &promptiterengine.RunResult{
			Status: promptiterengine.RunStatusSucceeded,
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					OutputProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
						{
							SurfaceID: "agent#tool.lookup",
							Value: astructure.SurfaceValue{
								Tools: []astructure.ToolRef{{ID: "lookup", Description: "candidate tool description"}},
							},
						},
					}},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true},
				},
			},
		},
	}
	evaluator := &scriptedEvaluator{
		results: map[Phase]*promptiterengine.EvaluationResult{
			PhaseBaselineTrain: evalResult("train", []caseSpec{
				{id: "train_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
			PhaseBaselineValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 0, status: status.EvalStatusFailed},
			}),
			PhaseCandidateValidation: evalResult("validation", []caseSpec{
				{id: "validation_case", metric: "metric", score: 1, status: status.EvalStatusPassed},
			}),
		},
	}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "optimization_report.json"),
		OutputMarkdown:      filepath.Join(dir, "optimization_report.md"),
		TargetSurfaceIDs:    []string{"agent#tool.lookup"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
		Gate:                GateConfig{RequireEngineAccepted: false},
	}
	result, err := Pipeline{
		Evaluator:      evaluator,
		PromptIterator: iterator,
		Clock:          &sequenceClock{times: []time.Time{time.Unix(1, 0), time.Unix(2, 0)}},
	}.Run(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, evaluator.requests, 3)
	require.NotNil(t, evaluator.requests[2].Profile)
	assert.Equal(t, "agent#tool.lookup", evaluator.requests[2].Profile.Overrides[0].SurfaceID)
	assert.Empty(t, evaluator.requests[2].Prompt)
	require.NotNil(t, result.Report.CandidateValidation)
	assert.Equal(t, 1.0, result.Report.CandidateValidation.OverallScore)
	assert.Equal(t, 1, result.Report.Delta.Summary.NewlyPassed)
}

func TestPipelineRejectsMissingCollaboratorsAndMetricsPath(t *testing.T) {
	cfg := Config{
		AppName:             "app",
		PromptSource:        "prompt.txt",
		MetricsPath:         "metrics.json",
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          "report.json",
		OutputMarkdown:      "report.md",
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	_, err := Pipeline{}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "evaluator is nil")

	_, err = Pipeline{Evaluator: &scriptedEvaluator{}}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "prompt iterator is nil")

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("prompt"), 0o644))
	cfg.PromptSource = promptPath
	cfg.MetricsPath = filepath.Join(dir, "missing-metrics.json")
	cfg.OutputJSON = filepath.Join(dir, "report.json")
	cfg.OutputMarkdown = filepath.Join(dir, "report.md")
	_, err = Pipeline{
		Evaluator:      &scriptedEvaluator{},
		PromptIterator: &capturingPromptIterator{},
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "read metrics path")
}

func TestPipelinePropagatesPromptProfileAndReportErrors(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	cfg := Config{
		AppName:             "app",
		PromptSource:        filepath.Join(dir, "missing-prompt.txt"),
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "report.json"),
		OutputMarkdown:      filepath.Join(dir, "report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	pipeline := Pipeline{Evaluator: &scriptedEvaluator{}, PromptIterator: &capturingPromptIterator{}}
	_, err := pipeline.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "read prompt source")

	promptPath := filepath.Join(dir, "prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("prompt"), 0o644))
	cfg.PromptSource = promptPath
	cfg.TargetSurfaceIDs = []string{"agent#model"}
	pipeline.Evaluator = &scriptedEvaluator{
		results: map[Phase]*promptiterengine.EvaluationResult{
			PhaseBaselineTrain:      evalResult("train", []caseSpec{{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed}}),
			PhaseBaselineValidation: evalResult("validation", []caseSpec{{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed}}),
		},
	}
	_, err = pipeline.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "supports only instruction, global_instruction, or tool")

	cfg.TargetSurfaceIDs = []string{"agent#instruction"}
	cfg.OutputJSON = filepath.Join(dir, "json-dir")
	require.NoError(t, os.Mkdir(cfg.OutputJSON, 0o755))
	_, err = pipeline.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "write JSON report")
}

func TestPipelineDocumentsSkillTargetRequiresCustomPromptIteratorProfilePath(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	iterator := &capturingPromptIterator{}
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "report.json"),
		OutputMarkdown:      filepath.Join(dir, "report.md"),
		TargetSurfaceIDs:    []string{"agent#skill.refund_policy"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	_, err := Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{
					{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
				}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{
					{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed},
				}),
			},
		},
		PromptIterator: iterator,
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "supports only instruction, global_instruction, or tool")
	assert.Nil(t, iterator.request)
}

func TestPipelinePropagatesEvaluatorAndIteratorErrors(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	metricsPath := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(promptPath, []byte("prompt"), 0o644))
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"metrics":[]}`), 0o644))
	cfg := Config{
		AppName:             "app",
		PromptSource:        promptPath,
		MetricsPath:         metricsPath,
		TrainEvalSetID:      "train",
		ValidationEvalSetID: "validation",
		OutputJSON:          filepath.Join(dir, "report.json"),
		OutputMarkdown:      filepath.Join(dir, "report.md"),
		TargetSurfaceIDs:    []string{"agent#instruction"},
		PromptIter:          PromptIterConfig{MaxRounds: 1},
	}
	_, err := Pipeline{
		Evaluator:      &scriptedEvaluator{err: errors.New("eval failed")},
		PromptIterator: &capturingPromptIterator{},
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "evaluate baseline train")

	_, err = Pipeline{
		Evaluator: &phaseErrorEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain: evalResult("train", []caseSpec{{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed}}),
			},
			errPhase: PhaseBaselineValidation,
			err:      errors.New("validation failed"),
		},
		PromptIterator: &capturingPromptIterator{},
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "evaluate baseline validation")

	_, err = Pipeline{
		Evaluator: &scriptedEvaluator{
			results: map[Phase]*promptiterengine.EvaluationResult{
				PhaseBaselineTrain:      evalResult("train", []caseSpec{{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed}}),
				PhaseBaselineValidation: evalResult("validation", []caseSpec{{id: "case", metric: "m", score: 1, status: status.EvalStatusPassed}}),
			},
		},
		PromptIterator: &capturingPromptIterator{err: errors.New("iter failed")},
	}.Run(context.Background(), cfg)
	assert.ErrorContains(t, err, "run promptiter")
}

func TestPipelineReturnsConfigValidationErrors(t *testing.T) {
	_, err := Pipeline{Evaluator: &scriptedEvaluator{}, PromptIterator: &capturingPromptIterator{}}.
		Run(context.Background(), Config{})
	assert.ErrorContains(t, err, "app name is empty")
}

func TestEstimateCostHandlesNilRun(t *testing.T) {
	cost := estimateCost(nil)
	assert.Equal(t, 2, cost.ModelCalls)
	assert.True(t, cost.Estimated)
	assert.Equal(t, CostSourceModelCallEstimate, cost.Source)

	cost = estimateCost(nil, true)
	assert.Equal(t, 3, cost.ModelCalls)
}

type scriptedEvaluator struct {
	results  map[Phase]*promptiterengine.EvaluationResult
	requests []EvaluationRequest
	err      error
}

func (e *scriptedEvaluator) Evaluate(_ context.Context, request EvaluationRequest) (*promptiterengine.EvaluationResult, error) {
	e.requests = append(e.requests, request)
	if e.err != nil {
		return nil, e.err
	}
	return e.results[request.Phase], nil
}

type phaseErrorEvaluator struct {
	results  map[Phase]*promptiterengine.EvaluationResult
	errPhase Phase
	err      error
}

func (e *phaseErrorEvaluator) Evaluate(_ context.Context, request EvaluationRequest) (*promptiterengine.EvaluationResult, error) {
	if request.Phase == e.errPhase {
		return nil, e.err
	}
	return e.results[request.Phase], nil
}

type capturingPromptIterator struct {
	request *promptiterengine.RunRequest
	result  *promptiterengine.RunResult
	err     error
}

func (i *capturingPromptIterator) Run(
	_ context.Context,
	request *promptiterengine.RunRequest,
) (*promptiterengine.RunResult, error) {
	i.request = request
	if i.err != nil {
		return nil, i.err
	}
	return i.result, nil
}

type sequenceAgentEvaluator struct {
	results []*evaluation.EvaluationResult
	index   int
}

func (e *sequenceAgentEvaluator) Evaluate(
	_ context.Context,
	_ string,
	_ ...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	if e.index >= len(e.results) {
		return nil, errors.New("unexpected evaluation call")
	}
	result := e.results[e.index]
	e.index++
	return result, nil
}

func (e *sequenceAgentEvaluator) Close() error {
	return nil
}

type noopBackwarder struct{}

func (noopBackwarder) Backward(_ context.Context, _ *backwarder.Request) (*backwarder.Result, error) {
	return &backwarder.Result{}, nil
}

type noopAggregator struct{}

func (noopAggregator) Aggregate(_ context.Context, _ *aggregator.Request) (*aggregator.Result, error) {
	return &aggregator.Result{}, nil
}

type noopOptimizer struct{}

func (noopOptimizer) Optimize(_ context.Context, _ *optimizer.Request) (*optimizer.Result, error) {
	return &optimizer.Result{}, nil
}

func promptIterTestStructure() *astructure.Snapshot {
	text := "baseline prompt"
	return &astructure.Snapshot{
		StructureID: "structure",
		EntryNodeID: "agent",
		Nodes: []astructure.Node{
			{NodeID: "agent", Kind: astructure.NodeKindLLM, Name: "agent"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "agent#instruction",
				NodeID:    "agent",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &text},
			},
		},
	}
}

func promptIterTestTrace(traceStatus atrace.TraceStatus) *atrace.Trace {
	return &atrace.Trace{
		Status:    traceStatus,
		SessionID: "session",
		Steps: []atrace.Step{
			{
				StepID:            "step",
				NodeID:            "agent",
				AppliedSurfaceIDs: []string{"agent#instruction"},
				Output:            &atrace.Snapshot{Text: "answer"},
			},
		},
	}
}

func genericEvalResult(
	evalSetID string,
	evalCaseID string,
	metricName string,
	score float64,
	metricStatus status.EvalStatus,
	reason string,
	trace *atrace.Trace,
) *evaluation.EvaluationResult {
	return &evaluation.EvaluationResult{
		AppName:   "app",
		EvalSetID: evalSetID,
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID: evalCaseID,
				EvalCaseResults: []*evalresult.EvalCaseResult{
					{
						RunID: 1,
						OverallEvalMetricResults: []*evalresult.EvalMetricResult{
							{
								MetricName: metricName,
								Score:      score,
								EvalStatus: metricStatus,
								Threshold:  1,
								Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
							},
						},
					},
				},
				MetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: metricName,
						Score:      score,
						EvalStatus: metricStatus,
						Threshold:  1,
						Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
					},
				},
				RunDetails: []*evaluation.EvaluationCaseRunDetails{
					{
						RunID: 1,
						Inference: &evaluation.EvaluationInferenceDetails{
							SessionID:       "session",
							Status:          metricStatus,
							ExecutionTraces: []*atrace.Trace{trace},
						},
					},
				},
			},
		},
	}
}

type staticCostProvider struct {
	summary CostSummary
}

func (p staticCostProvider) CostSummary() CostSummary {
	return p.summary
}

type sequenceClock struct {
	times []time.Time
	index int
}

func (c *sequenceClock) Now() time.Time {
	if c.index >= len(c.times) {
		return c.times[len(c.times)-1]
	}
	current := c.times[c.index]
	c.index++
	return current
}

func ptrString(value string) *string {
	return &value
}

type countingJudge struct {
	calls int
}

func (j *countingJudge) ClassifyFailure(_ context.Context, _ AttributionJudgeRequest) (AttributionJudgeResult, error) {
	j.calls++
	return AttributionJudgeResult{
		Category: FailureRouteError,
		Reason:   "judge classified the failure",
	}, nil
}

type judgeAwareClock struct {
	startedAt  time.Time
	finishedAt time.Time
	judge      *countingJudge
	calls      int
}

func (c *judgeAwareClock) Now() time.Time {
	c.calls++
	if c.calls == 1 {
		return c.startedAt
	}
	if c.judge != nil && c.judge.calls > 0 {
		return c.finishedAt
	}
	return c.startedAt
}

type judgeAwareCostProvider struct {
	judge *countingJudge
}

func (p *judgeAwareCostProvider) CostSummary() CostSummary {
	calls := 100
	if p.judge != nil {
		calls += p.judge.calls
	}
	return CostSummary{
		ModelCalls:         calls,
		ModelCallsMeasured: true,
		Source:             CostSourceProvider,
	}
}
