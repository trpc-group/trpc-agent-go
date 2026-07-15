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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
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
		ModelCalls: 5,
		Tokens:     123,
		Amount:     0.25,
		Currency:   "USD",
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
