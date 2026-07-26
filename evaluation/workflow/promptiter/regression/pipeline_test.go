//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPipelineUsesNativeEngineWithoutHeldoutLeakageAndPropagatesEvidenceHints(t *testing.T) {
	ctx := context.Background()
	structure := pipelineTestStructure()
	nativeService := &pipelineNativeService{surfaceID: pipelineTestSurfaceID}
	nativeEvaluator, evalSetManager, metricManager := pipelineNativeEvaluator(
		t,
		ctx,
		nativeService,
	)
	t.Cleanup(func() {
		require.NoError(t, nativeEvaluator.Close())
	})
	backward := &pipelineBackwarder{}
	nativeEngine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithStructure(structure),
		promptiterengine.WithAgentEvaluator(nativeEvaluator),
		promptiterengine.WithBackwarder(backward),
		promptiterengine.WithAggregator(pipelineAggregator{}),
		promptiterengine.WithOptimizer(pipelineOptimizer{}),
	)
	require.NoError(t, err)
	_ = evalSetManager
	_ = metricManager

	outer := &pipelineSnapshotEvaluator{}
	pipeline, err := New(nativeEngine, outer)
	require.NoError(t, err)
	config := pipelineRunConfig()
	config.InitialProfile.StructureID = ""
	bindPipelineRunConfig(&config)
	report, err := pipeline.Run(ctx, &config)
	require.NoError(t, err)
	require.Equal(t, PipelineSucceeded, report.Status)
	require.Equal(t, StopMaxRounds, report.StopReason)
	require.Len(t, report.Candidates, 1)
	candidate := report.Candidates[0]
	require.Equal(t, DecisionRejected, candidate.SearchDecision.Status)
	require.Equal(t, DecisionAccepted, candidate.ReleaseDecision.Status)
	require.Contains(t, candidate.SearchDecision.Reasons[0], "does not satisfy")
	require.Contains(t, candidate.SearchDecision.Reasons[1], "0.700000")
	require.Equal(t, report.InitialProfile.Hash, report.SearchProfile.Hash)
	require.Equal(t, candidate.Profile.Hash, report.ReleasedProfile.Hash)
	require.Equal(t, pipelineTestStructure().StructureID, report.InitialProfile.StructureID)
	require.Equal(t, report.InitialProfile.StructureID, report.InitialProfile.Profile.StructureID)
	initialHash, err := ProfileFingerprint(report.InitialProfile.Profile)
	require.NoError(t, err)
	require.Equal(t, initialHash, report.InitialProfile.Hash)
	require.Empty(t, config.InitialProfile.StructureID, "pipeline must not mutate caller profile")
	require.Equal(t, candidate.Validation.Provenance.RunID, candidate.Profile.EvaluationRunID)
	require.Equal(t, candidate.Validation.Provenance.RunID, report.ReleasedProfile.EvaluationRunID)
	require.True(t, candidate.Transition.ReleaseUpdated)
	require.False(t, candidate.Transition.SearchUpdated)

	nativeService.mu.Lock()
	inferenceRequests := append(
		[]service.InferenceRequest(nil),
		nativeService.inferenceRequests...,
	)
	nativeService.mu.Unlock()
	require.Len(t, inferenceRequests, 3)
	for _, request := range inferenceRequests {
		require.Equal(t, "train", request.EvalSetID)
		require.NotContains(t, request.EvalCaseIDs, "heldout-a")
	}
	require.Equal(t, []string{"train-a"}, inferenceRequests[0].EvalCaseIDs)
	require.Equal(t, []string{"train-a"}, inferenceRequests[1].EvalCaseIDs)
	require.Equal(t, []string{"train-a"}, inferenceRequests[2].EvalCaseIDs)

	backward.mu.Lock()
	backwardRequests := append([]*backwarder.Request(nil), backward.requests...)
	backward.mu.Unlock()
	require.NotEmpty(t, backwardRequests)
	hintFound := false
	for _, request := range backwardRequests {
		for _, incoming := range request.Incoming {
			if strings.Contains(incoming.Gradient, "outer evidence") &&
				strings.Contains(incoming.Gradient, "trace[outer-step]") {
				hintFound = true
			}
		}
	}
	require.True(t, hintFound, "outer attribution evidence must causally reach native backward input")

	stages := make([]string, 0, len(candidate.Resources.Entries))
	for _, entry := range candidate.Resources.Entries {
		stages = append(stages, entry.Stage)
	}
	require.Contains(t, stages, "promptiter_baseline_validation")
	require.Contains(t, stages, "promptiter_round_train_evaluation")
	require.Contains(t, stages, "promptiter_round_backward")
	require.NotContains(t, stages, "promptiter")
}

func TestPipelineStopsNativePromptIterAtObservedBudgetBoundary(t *testing.T) {
	ctx := context.Background()
	structure := pipelineTestStructure()
	meter := NewUsageMeter()
	nativeService := &pipelineNativeService{
		surfaceID: pipelineTestSurfaceID,
		meter:     meter,
	}
	nativeEvaluator, _, _ := pipelineNativeEvaluator(t, ctx, nativeService)
	t.Cleanup(func() {
		require.NoError(t, nativeEvaluator.Close())
	})
	nativeEngine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithStructure(structure),
		promptiterengine.WithAgentEvaluator(nativeEvaluator),
		promptiterengine.WithBackwarder(&pipelineBackwarder{}),
		promptiterengine.WithAggregator(pipelineAggregator{}),
		promptiterengine.WithOptimizer(pipelineOptimizer{}),
	)
	require.NoError(t, err)
	pipeline, err := New(
		nativeEngine,
		&pipelineSnapshotEvaluator{},
		WithResourceMeter(meter),
	)
	require.NoError(t, err)
	config := pipelineRunConfig()
	config.Gate.MaxCumulativeModelCalls = 3
	bindPipelineRunConfig(&config)

	report, err := pipeline.Run(ctx, &config)
	require.NoError(t, err)
	require.Equal(t, PipelineBudgetStopped, report.Status)
	require.Equal(t, StopBudgetExhausted, report.StopReason)
	require.Len(t, report.Candidates, 1)
	candidate := report.Candidates[0]
	require.Equal(t, EvaluationNotEvaluable, candidate.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.SearchDecision.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
	require.Equal(t, "budget_stopped", candidate.PromptIterStatus)
	require.False(t, candidate.Transition.SearchUpdated)
	require.False(t, candidate.Transition.ReleaseUpdated)
	require.Equal(t, int64(3), report.Resources.Cumulative.ModelCalls.Value)

	nativeService.mu.Lock()
	inferenceCalls := nativeService.inferenceCalls
	nativeService.mu.Unlock()
	require.Equal(t, 1, inferenceCalls, "observer must stop PromptIter before later native stages")
}

func TestPipelinePropagatesContextTerminationWithFinalizedReport(t *testing.T) {
	terminations := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	stages := []string{
		"baseline_train",
		"baseline_validation",
		"promptiter_engine",
		"candidate_train",
		"candidate_validation",
	}
	for _, termination := range terminations {
		for _, stage := range stages {
			t.Run(termination.name+"/"+stage, func(t *testing.T) {
				engine := &pipelineStaticEngine{
					structure: pipelineTestStructure(),
					result:    pipelineCandidateRunResult(),
				}
				if stage == "promptiter_engine" {
					engine.err = fmt.Errorf("native engine stopped: %w", termination.err)
				}
				evaluator := snapshotEvaluatorFunc(func(
					_ context.Context,
					request SnapshotRequest,
				) (*EvaluationSnapshot, error) {
					candidate := strings.Contains(
						pipelineProfileText(request.Profile),
						"candidate",
					)
					shouldStop := false
					switch stage {
					case "baseline_train":
						shouldStop = strings.Contains(
							request.EvaluationRunID,
							"/baseline_train",
						)
					case "baseline_validation":
						shouldStop = strings.Contains(
							request.EvaluationRunID,
							"/baseline_validation",
						)
					case "candidate_train":
						shouldStop = candidate && request.Split == "train"
					case "candidate_validation":
						shouldStop = candidate &&
							request.Split == "heldout_validation"
					}
					if shouldStop {
						snapshot := pipelineSnapshot(request, 0, false, false)
						snapshot.Status = EvaluationRunFailed
						snapshot.Error = termination.err.Error()
						return snapshot, fmt.Errorf(
							"%s stopped: %w",
							stage,
							termination.err,
						)
					}
					score := 0.2
					passed := false
					if request.Split == "heldout_validation" {
						score = 0.4
					}
					if candidate {
						score = 0.9
						passed = true
					}
					return pipelineSnapshot(request, score, passed, false), nil
				})
				pipeline, err := New(engine, evaluator)
				require.NoError(t, err)
				config := pipelineRunConfig()

				report, err := pipeline.Run(context.Background(), &config)
				require.ErrorIs(t, err, termination.err)
				require.NotNil(t, report)
				require.Equal(t, PipelineRunFailed, report.Status)
				require.Equal(t, StopNecessaryRunFailed, report.StopReason)
				require.Equal(t, report.InitialProfile.Hash, report.SearchProfile.Hash)
				require.Equal(t, report.InitialProfile.Hash, report.ReleasedProfile.Hash)
				require.NotEmpty(t, report.FinalDecision.Reasons)
				require.NotEmpty(t, report.Errors)
				if stage == "promptiter_engine" {
					require.Len(t, report.Candidates, 1)
					require.Equal(
						t,
						string(promptiterengine.RunStatusCanceled),
						report.Candidates[0].PromptIterStatus,
					)
				}
				_, renderErr := RenderJSON(report)
				require.NoError(t, renderErr)
				_, renderErr = RenderMarkdown(report)
				require.NoError(t, renderErr)
			})
		}
	}
}

func TestPipelinePropagatesPromptIterObserverContextTermination(t *testing.T) {
	for _, termination := range []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(termination.name, func(t *testing.T) {
			ctx := context.Background()
			nativeEvaluator, _, _ := pipelineNativeEvaluator(
				t,
				ctx,
				&pipelineNativeService{surfaceID: pipelineTestSurfaceID},
			)
			t.Cleanup(func() {
				require.NoError(t, nativeEvaluator.Close())
			})
			nativeEngine, err := promptiterengine.New(
				ctx,
				promptiterengine.WithStructure(pipelineTestStructure()),
				promptiterengine.WithAgentEvaluator(nativeEvaluator),
				promptiterengine.WithBackwarder(&pipelineBackwarder{}),
				promptiterengine.WithAggregator(pipelineAggregator{}),
				promptiterengine.WithOptimizer(pipelineOptimizer{}),
			)
			require.NoError(t, err)
			pipeline, err := New(
				nativeEngine,
				&pipelineSnapshotEvaluator{},
				WithEngineObserver(func(
					context.Context,
					*promptiterengine.Event,
				) error {
					return fmt.Errorf("observer stopped: %w", termination.err)
				}),
			)
			require.NoError(t, err)
			config := pipelineRunConfig()

			report, err := pipeline.Run(ctx, &config)
			require.ErrorIs(t, err, termination.err)
			require.NotNil(t, report)
			require.Equal(t, PipelineRunFailed, report.Status)
			require.Len(t, report.Candidates, 1)
			require.Equal(
				t,
				string(promptiterengine.RunStatusCanceled),
				report.Candidates[0].PromptIterStatus,
			)
			_, renderErr := RenderJSON(report)
			require.NoError(t, renderErr)
			_, renderErr = RenderMarkdown(report)
			require.NoError(t, renderErr)
		})
	}
}

func TestContextTerminationErrorUsesCanceledContextWithoutOperationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, contextTerminationError(ctx, nil), context.Canceled)
	providerErr := errors.New("provider unavailable")
	joined := contextTerminationError(ctx, providerErr)
	require.ErrorIs(t, joined, context.Canceled)
	require.ErrorIs(t, joined, providerErr)
	require.NoError(t, contextTerminationError(context.Background(), nil))
	require.NoError(t, contextTerminationError(context.Background(), providerErr))
}

func TestPipelineAuditsCanceledContextWhenEvaluatorOmitsError(t *testing.T) {
	for _, status := range []EvaluationStatus{
		EvaluationCompleted,
		EvaluationRunFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			evaluator := snapshotEvaluatorFunc(func(
				_ context.Context,
				request SnapshotRequest,
			) (*EvaluationSnapshot, error) {
				snapshot := pipelineSnapshot(request, 0, false, false)
				snapshot.Status = status
				if status == EvaluationRunFailed {
					snapshot.Error = "evaluation stopped"
				}
				return snapshot, nil
			})
			pipeline, err := New(
				&pipelineStaticEngine{structure: pipelineTestStructure()},
				evaluator,
			)
			require.NoError(t, err)
			config := pipelineRunConfig()

			report, err := pipeline.Run(ctx, &config)
			require.ErrorIs(t, err, context.Canceled)
			require.NotNil(t, report)
			require.Contains(t, strings.Join(report.Errors, "\n"), context.Canceled.Error())
		})
	}
}

func TestPipelinePreservesNotEvaluableCandidateAndUpdatesNeitherProfile(t *testing.T) {
	structure := pipelineTestStructure()
	config := pipelineRunConfig()
	candidateProfile := pipelineProfile("candidate prompt")
	staticEngine := &pipelineStaticEngine{
		structure: structure,
		result: &promptiterengine.RunResult{
			Status:             promptiterengine.RunStatusSucceeded,
			CurrentRound:       1,
			BaselineValidation: &promptiterengine.EvaluationResult{},
			AcceptedProfile:    candidateProfile,
			Rounds: []promptiterengine.RoundResult{{
				Round:        1,
				InputProfile: pipelineProfile("initial prompt"),
				Train:        &promptiterengine.EvaluationResult{},
				Patches: &promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
					SurfaceID: pipelineTestSurfaceID,
					Value:     astructure.SurfaceValue{Text: stringPointer("candidate prompt")},
					Reason:    "candidate",
				}}},
				OutputProfile: candidateProfile,
				Validation:    &promptiterengine.EvaluationResult{},
				Acceptance: &promptiterengine.AcceptanceDecision{
					Accepted:   true,
					ScoreDelta: 0.5,
					Reason:     "native acceptance",
				},
			}},
		},
	}
	outer := &pipelineSnapshotEvaluator{failCandidateTrain: true}
	pipeline, err := New(staticEngine, outer)
	require.NoError(t, err)
	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, PipelineRunFailed, report.Status)
	require.Equal(t, StopNecessaryRunFailed, report.StopReason)
	require.Len(t, report.Candidates, 1)
	candidate := report.Candidates[0]
	require.Equal(t, EvaluationNotEvaluable, candidate.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.SearchDecision.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
	require.False(t, candidate.Transition.SearchUpdated)
	require.False(t, candidate.Transition.ReleaseUpdated)
	require.Equal(t, report.InitialProfile.Hash, report.SearchProfile.Hash)
	require.Equal(t, report.InitialProfile.Hash, report.ReleasedProfile.Hash)
	require.NotNil(t, candidate.Train)
	require.Nil(t, candidate.Validation)

	outer.mu.Lock()
	requests := append([]SnapshotRequest(nil), outer.requests...)
	outer.mu.Unlock()
	require.Len(t, requests, 3, "held-out evaluation must not run after candidate train fails")
}

func TestPipelineFailsClosedOnUnknownConfiguredBudgetBeforeSearch(t *testing.T) {
	config := pipelineRunConfig()
	config.Gate.MaxCumulativeModelCalls = 2
	bindPipelineRunConfig(&config)
	staticEngine := &pipelineStaticEngine{structure: pipelineTestStructure()}
	outer := &pipelineSnapshotEvaluator{unknownCalls: true}
	pipeline, err := New(staticEngine, outer)
	require.NoError(t, err)
	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, PipelineBudgetStopped, report.Status)
	require.Equal(t, StopBudgetExhausted, report.StopReason)
	require.Equal(t, DecisionNotEvaluable, report.FinalDecision.Status)
	require.Contains(t, report.FinalDecision.Reasons[0], "unavailable")
	require.Zero(t, staticEngine.runCalls)
}

func TestPipelineRunConfigBypassRejectsHeldoutInputLeakageAndInvalidPolicy(t *testing.T) {
	staticEngine := &pipelineStaticEngine{structure: pipelineTestStructure()}
	pipeline, err := New(staticEngine, &pipelineSnapshotEvaluator{})
	require.NoError(t, err)

	t.Run("normalized input collision", func(t *testing.T) {
		config := pipelineRunConfig()
		config.Validation.NormalizedInputHashes["heldout-a"] = "normalized-train"
		_, err := pipeline.Run(context.Background(), &config)
		require.ErrorContains(t, err, "held-out leakage")
		require.Zero(t, staticEngine.describeCalls)
	})
	t.Run("non-finite search policy", func(t *testing.T) {
		config := pipelineRunConfig()
		config.PromptIter.SearchMinScoreGain = math.NaN()
		_, err := pipeline.Run(context.Background(), &config)
		require.ErrorContains(t, err, "finite")
		require.Zero(t, staticEngine.describeCalls)
	})
}

func TestValidateRunConfigRejectsPublicBypasses(t *testing.T) {
	valid := pipelineRunConfig()
	require.NoError(t, validateRunConfig(&valid))

	tests := []struct {
		name    string
		mutate  func(*RunConfig)
		message string
	}{
		{
			name: "critical case duplicate",
			mutate: func(config *RunConfig) {
				config.CriticalCaseIDs = []string{"heldout-a", "heldout-a"}
			},
			message: "duplicate",
		},
		{
			name: "hard failure case is not held out",
			mutate: func(config *RunConfig) {
				config.HardFailureCaseIDs = []string{"train-a"}
			},
			message: "held-out validation",
		},
		{
			name: "extra metric direction",
			mutate: func(config *RunConfig) {
				config.Gate.MetricDirections["forged"] = ScoreHigherIsBetter
			},
			message: "metric direction inventory",
		},
		{
			name: "extra normalized hash key",
			mutate: func(config *RunConfig) {
				config.Train.NormalizedInputHashes["forged"] = "forged-hash"
			},
			message: "normalized input hash inventory",
		},
		{
			name: "duplicate normalized hash within split",
			mutate: func(config *RunConfig) {
				config.Train.CaseIDs = append(config.Train.CaseIDs, "train-b")
				config.Train.NormalizedInputHashes["train-b"] = "normalized-train"
			},
			message: "duplicate normalized input hashes",
		},
		{
			name: "train input hash mismatch",
			mutate: func(config *RunConfig) {
				config.InputHashes["trainEvalSet"] = "forged-hash"
			},
			message: "does not match train dataset hash",
		},
		{
			name: "missing required input hash",
			mutate: func(config *RunConfig) {
				delete(config.InputHashes, "baselinePrompt")
			},
			message: "input hash inventory",
		},
		{
			name: "zero generated at",
			mutate: func(config *RunConfig) {
				config.GeneratedAt = time.Time{}
			},
			message: "generated at is required",
		},
		{
			name: "runtime changed after binding",
			mutate: func(config *RunConfig) {
				config.Runtime.Engine = "forged-runtime"
			},
			message: "runtime fingerprint",
		},
		{
			name: "run id changed after binding",
			mutate: func(config *RunConfig) {
				config.RunID = "forged-run"
			},
			message: "input and runtime fingerprint",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := pipelineRunConfig()
			test.mutate(&config)
			require.ErrorContains(t, validateRunConfig(&config), test.message)
		})
	}
}

func TestValidateSnapshotResponseRejectsForgedEvaluatorData(t *testing.T) {
	config := pipelineRunConfig()
	profileHash, err := ProfileFingerprint(config.InitialProfile)
	require.NoError(t, err)
	request := SnapshotRequest{
		EvaluationRunID:     config.RunID + "/probe",
		Profile:             config.InitialProfile,
		ExpectedProfileHash: profileHash,
		Dataset: DatasetSpec{
			EvalSetID:   config.Train.EvalSetID,
			EvalSetHash: config.Train.EvalSetHash,
			MetricsHash: config.Train.MetricsHash,
			CaseIDs:     []string{"train-a", "train-b"},
			MetricNames: append([]string(nil), config.Train.MetricNames...),
		},
		Split:               "train",
		Seed:                config.Seed,
		EvaluatorConfigHash: config.EvaluatorConfigHash,
		MetricPolicyHash:    config.MetricPolicyHash,
		PrimaryMetric:       config.Gate.PrimaryMetric,
		MetricDirections:    config.Gate.MetricDirections,
		EvidenceLimit:       config.EvidenceLimit,
	}
	require.NoError(t, validateSnapshotResponse(
		request,
		pipelineSnapshot(request, 0.2, false, false),
	))

	tests := []struct {
		name    string
		mutate  func(*EvaluationSnapshot)
		message string
	}{
		{
			name: "wrong run binding",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Provenance.RunID = "forged-run"
			},
			message: "provenance does not match",
		},
		{
			name: "reordered inventory",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Inventory.CaseIDs[0], snapshot.Inventory.CaseIDs[1] =
					snapshot.Inventory.CaseIDs[1], snapshot.Inventory.CaseIDs[0]
			},
			message: "does not exactly match",
		},
		{
			name: "forged aggregate",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.OverallScore = 0.9
			},
			message: "does not match primary metric mean",
		},
		{
			name: "forged counts",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Passed = 1
				snapshot.Failed = 1
			},
			message: "pass/fail counts",
		},
		{
			name: "missing failure attribution",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Attributions = snapshot.Attributions[1:]
			},
			message: "failure attributions",
		},
		{
			name: "attribution wrong profile",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Attributions[0].ProfileHash = "forged-profile"
			},
			message: "does not match request provenance",
		},
		{
			name: "completed operational error",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Cases[0].Error = "provider 429"
			},
			message: "operational error",
		},
		{
			name: "case status is not comparable",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Cases[0].Status = "not_evaluated"
			},
			message: "case \"train-a\" status \"not_evaluated\" is not comparable",
		},
		{
			name: "case status disagrees with passed",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Cases[0].Status = "passed"
			},
			message: "disagrees with passed=false",
		},
		{
			name: "metric status is not comparable",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Cases[0].Metrics[0].Status = "not_evaluated"
			},
			message: "metric \"quality\" status \"not_evaluated\" is not comparable",
		},
		{
			name: "metric status disagrees with passed",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Cases[0].Metrics[0].Status = "passed"
			},
			message: "metric \"quality\" status \"passed\" disagrees with passed=false",
		},
		{
			name: "negative resource value",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Resources.ModelCalls.Value = -1
			},
			message: "model calls must be non-negative",
		},
		{
			name: "unavailable resource has value",
			mutate: func(snapshot *EvaluationSnapshot) {
				snapshot.Resources.ModelCalls = Count{Value: 1}
			},
			message: "unavailable model calls has a value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := pipelineSnapshot(request, 0.2, false, false)
			test.mutate(snapshot)
			require.ErrorContains(t, validateSnapshotResponse(request, snapshot), test.message)
		})
	}
}

func TestValidateRunConfigRejectsSourceSemanticMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RunConfig)
	}{
		{
			name: "baseline prompt",
			mutate: func(config *RunConfig) {
				config.InitialProfile = pipelineProfile("forged prompt")
			},
		},
		{
			name: "promptiter policy",
			mutate: func(config *RunConfig) {
				config.PromptIter.MaxOuterRounds++
			},
		},
		{
			name: "release case policy",
			mutate: func(config *RunConfig) {
				config.HardFailureCaseIDs = []string{"heldout-a"}
			},
		},
		{
			name: "evidence limit",
			mutate: func(config *RunConfig) {
				config.EvidenceLimit++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := pipelineRunConfig()
			runID := config.RunID
			test.mutate(&config)
			require.Equal(t, runID, config.RunID)
			require.ErrorContains(t, validateRunConfig(&config), "source configuration")
		})
	}
}

func TestPipelineRejectsUnboundSnapshotBeforeSearch(t *testing.T) {
	config := pipelineRunConfig()
	staticEngine := &pipelineStaticEngine{structure: pipelineTestStructure()}
	outer := &pipelineSnapshotEvaluator{mutate: func(
		request SnapshotRequest,
		snapshot *EvaluationSnapshot,
	) {
		if strings.Contains(request.EvaluationRunID, "baseline_train") {
			snapshot.Provenance.RunID = "forged-run"
		}
	}}
	pipeline, err := New(staticEngine, outer)
	require.NoError(t, err)
	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, PipelineRunFailed, report.Status)
	require.Equal(t, StopNecessaryRunFailed, report.StopReason)
	require.Equal(t, EvaluationNotEvaluable, report.BaselineTrain.Status)
	require.Zero(t, staticEngine.runCalls)
	require.Contains(t, strings.Join(report.Errors, "\n"), "provenance does not match")
}

func TestPipelineStopsBetweenBaselineSplitsWhenBudgetIsExhausted(t *testing.T) {
	staticEngine := &pipelineStaticEngine{structure: pipelineTestStructure()}
	outer := &pipelineSnapshotEvaluator{}
	pipeline, err := New(staticEngine, outer)
	require.NoError(t, err)
	config := pipelineRunConfig()
	config.Gate.MaxCumulativeModelCalls = 1
	bindPipelineRunConfig(&config)

	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, PipelineBudgetStopped, report.Status)
	require.Equal(t, StopBudgetExhausted, report.StopReason)
	require.Equal(t, DecisionNotEvaluable, report.FinalDecision.Status)
	require.NotNil(t, report.BaselineTrain)
	require.Nil(t, report.BaselineValidation)
	require.Zero(t, staticEngine.runCalls)
	outer.mu.Lock()
	requestCount := len(outer.requests)
	outer.mu.Unlock()
	require.Equal(t, 1, requestCount)
	require.Equal(t, int64(1), report.Resources.Cumulative.ModelCalls.Value)
	_, err = RenderJSON(report)
	require.NoError(t, err)
}

func TestPipelineStopsBeforeNextEvaluationWhenBudgetIsExhausted(t *testing.T) {
	tests := []struct {
		name             string
		budget           int64
		wantRequests     int
		wantTrainPresent bool
	}{
		{
			name:         "after promptiter",
			budget:       3,
			wantRequests: 2,
		},
		{
			name:             "after candidate train",
			budget:           4,
			wantRequests:     3,
			wantTrainPresent: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meter := NewUsageMeter()
			staticEngine := &pipelineStaticEngine{
				structure: pipelineTestStructure(),
				result:    pipelineCandidateRunResult(),
				onRun: func() {
					meter.Record(ResourceUsage{
						ModelCalls: Count{Available: true, Value: 1},
					})
				},
			}
			outer := &pipelineSnapshotEvaluator{}
			pipeline, err := New(
				staticEngine,
				outer,
				WithResourceMeter(meter),
			)
			require.NoError(t, err)
			config := pipelineRunConfig()
			config.Gate.MaxCumulativeModelCalls = test.budget
			bindPipelineRunConfig(&config)
			report, err := pipeline.Run(context.Background(), &config)
			require.NoError(t, err)
			require.Equal(t, PipelineBudgetStopped, report.Status)
			require.Equal(t, StopBudgetExhausted, report.StopReason)
			_, err = RenderJSON(report)
			require.NoError(t, err)
			require.Len(t, report.Candidates, 1)
			candidate := report.Candidates[0]
			require.Equal(t, EvaluationNotEvaluable, candidate.Status)
			require.Equal(t, DecisionNotEvaluable, candidate.SearchDecision.Status)
			require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
			require.False(t, candidate.Transition.SearchUpdated)
			require.False(t, candidate.Transition.ReleaseUpdated)
			if test.wantTrainPresent {
				require.NotNil(t, candidate.Train)
			} else {
				require.Nil(t, candidate.Train)
			}
			require.Nil(t, candidate.Validation)
			outer.mu.Lock()
			requestCount := len(outer.requests)
			outer.mu.Unlock()
			require.Equal(t, test.wantRequests, requestCount)
		})
	}
}

func TestPipelineMarksRepeatedUnevaluatedCandidateNotEvaluable(t *testing.T) {
	result := pipelineCandidateRunResult()
	result.Rounds[0].OutputProfile = pipelineProfile("initial prompt")
	result.Rounds[0].Patches.Patches[0].Value.Text = stringPointer("initial prompt")
	result.AcceptedProfile = pipelineProfile("initial prompt")
	staticEngine := &pipelineStaticEngine{
		structure: pipelineTestStructure(),
		result:    result,
	}
	outer := &pipelineSnapshotEvaluator{}
	pipeline, err := New(staticEngine, outer)
	require.NoError(t, err)

	config := pipelineRunConfig()
	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, StopRepeatedFingerprint, report.StopReason)
	require.Len(t, report.Candidates, 1)
	candidate := report.Candidates[0]
	require.Equal(t, EvaluationNotEvaluable, candidate.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.SearchDecision.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
	require.Nil(t, candidate.Train)
	require.Nil(t, candidate.Validation)
	require.False(t, candidate.Transition.SearchUpdated)
	require.False(t, candidate.Transition.ReleaseUpdated)
	_, err = RenderJSON(report)
	require.NoError(t, err)
}

func TestPipelineRejectsForgedPromptIterLineageAndPatches(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*promptiterengine.RunResult)
		wantError string
	}{
		{
			name: "input profile",
			mutate: func(result *promptiterengine.RunResult) {
				result.Rounds[0].InputProfile = pipelineProfile("forged parent")
			},
			wantError: "round input",
		},
		{
			name: "non-target patch",
			mutate: func(result *promptiterengine.RunResult) {
				result.Rounds[0].Patches.Patches[0].SurfaceID = "other#instruction"
			},
			wantError: "outside configured target",
		},
		{
			name: "patch reason",
			mutate: func(result *promptiterengine.RunResult) {
				result.Rounds[0].Patches.Patches[0].Reason = ""
			},
			wantError: "reason is empty",
		},
		{
			name: "output not derived from patches",
			mutate: func(result *promptiterengine.RunResult) {
				result.Rounds[0].OutputProfile = pipelineProfile("forged output")
			},
			wantError: "round output",
		},
		{
			name: "accepted profile",
			mutate: func(result *promptiterengine.RunResult) {
				result.AcceptedProfile = pipelineProfile("forged accepted")
			},
			wantError: "accepted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := pipelineCandidateRunResult()
			test.mutate(result)
			staticEngine := &pipelineStaticEngine{
				structure: pipelineTestStructure(),
				result:    result,
			}
			outer := &pipelineSnapshotEvaluator{}
			pipeline, err := New(staticEngine, outer)
			require.NoError(t, err)
			config := pipelineRunConfig()

			report, err := pipeline.Run(context.Background(), &config)
			require.NoError(t, err)
			require.Equal(t, PipelineRunFailed, report.Status)
			require.Equal(t, StopNecessaryRunFailed, report.StopReason)
			require.Len(t, report.Candidates, 1)
			candidate := report.Candidates[0]
			require.Equal(t, DecisionNotEvaluable, candidate.SearchDecision.Status)
			require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
			require.NotEmpty(t, candidate.PromptIterRunID)
			require.Contains(t, strings.Join(report.Errors, "\n"), test.wantError)
			outer.mu.Lock()
			requestCount := len(outer.requests)
			outer.mu.Unlock()
			require.Equal(t, 2, requestCount)
		})
	}
}

func TestPipelineKeepsCompletedCandidateWhenReleaseBudgetIsNotEvaluable(t *testing.T) {
	meter := NewUsageMeter()
	staticEngine := &pipelineStaticEngine{
		structure: pipelineTestStructure(),
		result:    pipelineCandidateRunResult(),
	}
	outer := &pipelineSnapshotEvaluator{unknownCandidateValidation: true}
	pipeline, err := New(staticEngine, outer, WithResourceMeter(meter))
	require.NoError(t, err)
	config := pipelineRunConfig()
	config.Gate.MaxCumulativeModelCalls = 100
	bindPipelineRunConfig(&config)
	report, err := pipeline.Run(context.Background(), &config)
	require.NoError(t, err)
	require.Equal(t, PipelineBudgetStopped, report.Status)
	require.Equal(t, StopBudgetExhausted, report.StopReason)
	_, err = RenderJSON(report)
	require.NoError(t, err)
	require.Len(t, report.Candidates, 1)
	candidate := report.Candidates[0]
	require.Equal(t, EvaluationCompleted, candidate.Status)
	require.Equal(t, DecisionNotEvaluable, candidate.ReleaseDecision.Status)
	require.False(t, candidate.Transition.SearchUpdated)
	require.False(t, candidate.Transition.ReleaseUpdated)
	require.Equal(t, report.InitialProfile.Hash, report.SearchProfile.Hash)
	require.Equal(t, report.InitialProfile.Hash, report.ReleasedProfile.Hash)
	require.Equal(t, candidate.Validation.Provenance.RunID, candidate.Profile.EvaluationRunID)
}

func TestProfileEvaluatorRetainsNativeEvidenceAndVerifiesInventory(t *testing.T) {
	ctx := context.Background()
	appName := "native-profile-test"
	evalSetID := "heldout-native"
	evalSetManager := evalsetinmemory.New()
	_, err := evalSetManager.Create(ctx, appName, evalSetID)
	require.NoError(t, err)
	require.NoError(t, evalSetManager.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID: "case-a",
		Conversation: []*evalset.Invocation{
			{
				InvocationID: "expected-1",
				Tools: []*evalset.Tool{{
					Name:      "lookup",
					Arguments: map[string]any{"id": "A-17"},
				}},
			},
			{
				InvocationID: "expected-2",
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: `{"status":"ok"}`,
				},
				Tools: []*evalset.Tool{{
					Name:      "verify",
					Arguments: map[string]any{"id": "A-17"},
				}},
			},
		},
		Rubrics: []*evalset.EvalCaseRubric{
			{
				Type:    "expected_route",
				Content: &evalset.EvalCaseRubricContent{Text: "support"},
			},
			{
				Type:    "expected_fact",
				Content: &evalset.EvalCaseRubricContent{Text: "7 days"},
			},
			{
				Type:    "structured_output",
				Content: &evalset.EvalCaseRubricContent{Text: "strict JSON"},
			},
		},
	}))
	metricManager := metricinmemory.New()
	require.NoError(t, metricManager.Add(ctx, appName, evalSetID, &metric.EvalMetric{
		MetricName: "quality",
		Threshold:  0.5,
	}))
	native := &pipelineNativeResultEvaluator{result: nativeEvidenceResult(appName, evalSetID)}
	evaluator, err := NewProfileEvaluator(ProfileEvaluatorConfig{
		AppName:        appName,
		AgentEvaluator: native,
		EvalSetManager: evalSetManager,
		MetricManager:  metricManager,
		Structure:      pipelineTestStructure(),
	})
	require.NoError(t, err)
	profile := pipelineProfile("candidate prompt")
	profileHash, err := ProfileFingerprint(profile)
	require.NoError(t, err)
	request := SnapshotRequest{
		EvaluationRunID:     "native-run",
		Profile:             profile,
		ExpectedProfileHash: profileHash,
		Dataset: DatasetSpec{
			EvalSetID:             evalSetID,
			EvalSetHash:           "evalset-hash",
			MetricsHash:           "metrics-hash",
			CaseIDs:               []string{"case-a"},
			MetricNames:           []string{"quality"},
			NormalizedInputHashes: map[string]string{"case-a": "input-hash"},
		},
		Split:               "heldout_validation",
		Seed:                2003,
		EvaluatorConfigHash: "evaluator-hash",
		MetricPolicyHash:    "policy-hash",
		PrimaryMetric:       "quality",
		MetricDirections:    map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
		CriticalCaseIDs:     []string{"case-a"},
		HardFailureCaseIDs:  []string{"case-a"},
		EvidenceLimit:       4,
	}
	snapshot, err := evaluator.Evaluate(ctx, request)
	require.NoError(t, err)
	require.Equal(t, EvaluationCompleted, snapshot.Status)
	require.Equal(t, profileHash, snapshot.Provenance.ProfileHash)
	require.Equal(t, 0.2, snapshot.OverallScore)
	require.Len(t, snapshot.Cases, 1)
	evalCase := snapshot.Cases[0]
	require.Equal(t, `{"status":"bad"}`, evalCase.FinalResponse)
	require.Equal(t, `{"status":"ok"}`, evalCase.ExpectedResponse)
	require.Equal(t, `{"status":"bad"}`, evalCase.StructuredOutput)
	require.Equal(t, "support", evalCase.Route)
	require.Equal(t, "support", evalCase.ExpectedRoute)
	require.Equal(t, []string{"7 days"}, evalCase.ExpectedFacts)
	require.True(t, evalCase.ExpectStructured)
	require.True(t, evalCase.Critical)
	require.True(t, evalCase.HardFailure)
	require.Len(t, evalCase.ToolTrajectory, 2)
	require.Equal(t, []int{1, 2}, []int{
		evalCase.ToolTrajectory[0].Sequence,
		evalCase.ToolTrajectory[1].Sequence,
	})
	require.Len(t, evalCase.ExpectedTools, 2)
	require.Len(t, evalCase.Trace, 1)
	require.Len(t, evalCase.Metrics[0].RubricScores, 1)
	require.Len(t, snapshot.Attributions, 1)
	require.Equal(t, FailureWrongArguments, snapshot.Attributions[0].PrimaryCategory)
	require.False(t, snapshot.Resources.ModelCalls.Available)
	require.True(t, snapshot.Resources.LatencyMS.Available)

	limitedRequest := request
	limitedRequest.EvaluationRunID = "native-run-limited"
	limitedRequest.EvidenceLimit = 1
	limited, err := evaluator.Evaluate(ctx, limitedRequest)
	require.NoError(t, err)
	require.Equal(t, EvaluationCompleted, limited.Status)
	require.Len(t, limited.Cases[0].ToolTrajectory, 1)
	require.Len(t, limited.Cases[0].ExpectedTools, 1)
	require.Len(t, limited.Cases[0].Trace, 1)
	require.LessOrEqual(t, len(limited.Attributions[0].Evidence), 1)

	badRequest := request
	badRequest.Dataset.CaseIDs = []string{"case-a", "missing"}
	badRequest.Dataset.NormalizedInputHashes = map[string]string{
		"case-a":  "input-hash",
		"missing": "missing-hash",
	}
	failed, err := evaluator.Evaluate(ctx, badRequest)
	require.ErrorContains(t, err, "missing")
	require.Equal(t, EvaluationNotEvaluable, failed.Status)
	require.Equal(t, 2, native.calls, "source inventory mismatch must fail before native execution")
}

func TestProfileEvaluatorTreatsNativeErrorMessagesAsRunFailuresAndRecovers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*evaluation.EvaluationResult)
	}{
		{
			name: "case result error",
			mutate: func(result *evaluation.EvaluationResult) {
				result.EvalCases[0].EvalCaseResults[0].ErrorMessage = "provider 429"
			},
		},
		{
			name: "inference error",
			mutate: func(result *evaluation.EvaluationResult) {
				result.EvalCases[0].RunDetails[0].Inference.ErrorMessage = "provider 429"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			appName := "native-" + strings.ReplaceAll(test.name, " ", "-")
			evalSetID := "heldout-native"
			evalSetManager := evalsetinmemory.New()
			_, err := evalSetManager.Create(ctx, appName, evalSetID)
			require.NoError(t, err)
			require.NoError(t, evalSetManager.AddCase(
				ctx,
				appName,
				evalSetID,
				&evalset.EvalCase{
					EvalID: "case-a",
					Conversation: []*evalset.Invocation{{
						InvocationID: "expected",
						FinalResponse: &model.Message{
							Role:    model.RoleAssistant,
							Content: "expected",
						},
					}},
				},
			))
			metricManager := metricinmemory.New()
			require.NoError(t, metricManager.Add(ctx, appName, evalSetID, &metric.EvalMetric{
				MetricName: "quality",
				Threshold:  0.5,
			}))
			failedResult := nativeEvidenceResult(appName, evalSetID)
			test.mutate(failedResult)
			native := &pipelineNativeResultEvaluator{results: []*evaluation.EvaluationResult{
				failedResult,
				nativeEvidenceResult(appName, evalSetID),
			}}
			evaluator, err := NewProfileEvaluator(ProfileEvaluatorConfig{
				AppName:        appName,
				AgentEvaluator: native,
				EvalSetManager: evalSetManager,
				MetricManager:  metricManager,
				Structure:      pipelineTestStructure(),
			})
			require.NoError(t, err)
			profile := pipelineProfile("candidate prompt")
			profileHash, err := ProfileFingerprint(profile)
			require.NoError(t, err)
			request := SnapshotRequest{
				EvaluationRunID:     "native-operational-run",
				Profile:             profile,
				ExpectedProfileHash: profileHash,
				Dataset: DatasetSpec{
					EvalSetID:             evalSetID,
					EvalSetHash:           "evalset-hash",
					MetricsHash:           "metrics-hash",
					CaseIDs:               []string{"case-a"},
					MetricNames:           []string{"quality"},
					NormalizedInputHashes: map[string]string{"case-a": "input-hash"},
				},
				Split:               "heldout_validation",
				Seed:                2003,
				EvaluatorConfigHash: "evaluator-hash",
				MetricPolicyHash:    "policy-hash",
				PrimaryMetric:       "quality",
				MetricDirections:    map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
				EvidenceLimit:       4,
			}
			failed, err := evaluator.Evaluate(ctx, request)
			require.ErrorContains(t, err, "provider 429")
			require.Equal(t, EvaluationRunFailed, failed.Status)
			require.Empty(t, failed.Cases)
			require.Empty(t, failed.Attributions)

			request.EvaluationRunID = "native-recovery-run"
			recovered, err := evaluator.Evaluate(ctx, request)
			require.NoError(t, err)
			require.Equal(t, EvaluationCompleted, recovered.Status)
			require.Empty(t, recovered.Error)
			require.Len(t, recovered.Cases, 1)
			require.Len(t, recovered.Attributions, 1)
			require.Equal(t, 2, native.calls)
		})
	}
}

type pipelineSnapshotEvaluator struct {
	mu                         sync.Mutex
	requests                   []SnapshotRequest
	failCandidateTrain         bool
	unknownCalls               bool
	unknownCandidateValidation bool
	mutate                     func(SnapshotRequest, *EvaluationSnapshot)
}

func (e *pipelineSnapshotEvaluator) Evaluate(
	_ context.Context,
	request SnapshotRequest,
) (*EvaluationSnapshot, error) {
	e.mu.Lock()
	e.requests = append(e.requests, request)
	e.mu.Unlock()
	candidate := strings.Contains(pipelineProfileText(request.Profile), "candidate")
	unknownCalls := e.unknownCalls ||
		e.unknownCandidateValidation && candidate && request.Split == "heldout_validation"
	if candidate && request.Split == "train" && e.failCandidateTrain {
		snapshot := pipelineSnapshot(request, 0, false, unknownCalls)
		snapshot.Status = EvaluationRunFailed
		snapshot.Error = "candidate train failed"
		if e.mutate != nil {
			e.mutate(request, snapshot)
		}
		return snapshot, errors.New("candidate train failed")
	}
	score := 0.2
	passed := false
	if request.Split == "heldout_validation" {
		score = 0.4
	}
	if candidate {
		score = 0.9
		passed = true
	}
	snapshot := pipelineSnapshot(request, score, passed, unknownCalls)
	if e.mutate != nil {
		e.mutate(request, snapshot)
	}
	return snapshot, nil
}

func pipelineSnapshot(
	request SnapshotRequest,
	score float64,
	passed bool,
	unknownCalls bool,
) *EvaluationSnapshot {
	statusValue := "failed"
	passedCount := 0
	failedCount := len(request.Dataset.CaseIDs)
	if passed {
		statusValue = "passed"
		passedCount = len(request.Dataset.CaseIDs)
		failedCount = 0
	}
	modelCalls := Count{Available: !unknownCalls}
	if modelCalls.Available {
		modelCalls.Value = 1
	}
	resources := ResourceUsage{
		ModelCalls:   modelCalls,
		InputTokens:  Count{Available: true},
		OutputTokens: Count{Available: true},
		LatencyMS:    Count{Available: true, Value: 1},
	}
	snapshot := &EvaluationSnapshot{
		Status: EvaluationCompleted,
		Provenance: EvaluationProvenance{
			RunID:               request.EvaluationRunID,
			ProfileHash:         request.ExpectedProfileHash,
			EvalSetID:           request.Dataset.EvalSetID,
			EvalSetHash:         request.Dataset.EvalSetHash,
			MetricsHash:         request.Dataset.MetricsHash,
			Split:               request.Split,
			Seed:                request.Seed,
			EvaluatorConfigHash: request.EvaluatorConfigHash,
			MetricPolicyHash:    request.MetricPolicyHash,
		},
		Inventory: ExpectedInventory{
			CaseIDs:     append([]string(nil), request.Dataset.CaseIDs...),
			MetricNames: append([]string(nil), request.Dataset.MetricNames...),
		},
		OverallScore: score,
		Passed:       passedCount,
		Failed:       failedCount,
		Resources:    resources,
		LatencyMS:    1,
	}
	for _, caseID := range request.Dataset.CaseIDs {
		evalCase := CaseResult{
			EvalSetID:     request.Dataset.EvalSetID,
			CaseID:        caseID,
			Status:        statusValue,
			Passed:        passed,
			PrimaryMetric: request.PrimaryMetric,
			FinalResponse: statusValue,
			Metrics: []MetricResult{{
				MetricName: request.PrimaryMetric,
				Score:      score,
				Status:     statusValue,
				Passed:     passed,
				Threshold:  0.5,
				Direction:  request.MetricDirections[request.PrimaryMetric],
				Reason:     "outer quality result",
			}},
			Trace: []TraceStep{{StepID: "outer-step", Output: statusValue}},
		}
		snapshot.Cases = append(snapshot.Cases, evalCase)
		if !passed {
			snapshot.Attributions = append(snapshot.Attributions, FailureAttribution{
				EvalSetID:       request.Dataset.EvalSetID,
				EvalCaseID:      caseID,
				MetricName:      request.PrimaryMetric,
				PrimaryCategory: FailureResponseMismatch,
				Reason:          "outer evidence",
				Evidence: []EvidenceReference{{
					ID:      "outer-step",
					Kind:    "trace",
					Summary: "outer trace fragment",
				}},
				Severity:            FailureSeverityP1,
				Confidence:          1,
				EvidenceSufficiency: EvidenceSufficient,
				EvaluationRunID:     request.EvaluationRunID,
				ProfileHash:         request.ExpectedProfileHash,
			})
		}
	}
	return snapshot
}

type pipelineStaticEngine struct {
	structure     *astructure.Snapshot
	result        *promptiterengine.RunResult
	err           error
	onRun         func()
	describeCalls int
	runCalls      int
}

func (e *pipelineStaticEngine) Describe(context.Context) (*astructure.Snapshot, error) {
	e.describeCalls++
	return e.structure, nil
}

func (e *pipelineStaticEngine) Run(
	context.Context,
	*promptiterengine.RunRequest,
	...promptiterengine.Option,
) (*promptiterengine.RunResult, error) {
	e.runCalls++
	if e.onRun != nil {
		e.onRun()
	}
	return e.result, e.err
}

func pipelineCandidateRunResult() *promptiterengine.RunResult {
	return &promptiterengine.RunResult{
		Status:             promptiterengine.RunStatusSucceeded,
		CurrentRound:       1,
		BaselineValidation: &promptiterengine.EvaluationResult{},
		AcceptedProfile:    pipelineProfile("candidate prompt"),
		Rounds: []promptiterengine.RoundResult{{
			Round:        1,
			InputProfile: pipelineProfile("initial prompt"),
			Train:        &promptiterengine.EvaluationResult{},
			Patches: &promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
				SurfaceID: pipelineTestSurfaceID,
				Value:     astructure.SurfaceValue{Text: stringPointer("candidate prompt")},
				Reason:    "candidate",
			}}},
			OutputProfile: pipelineProfile("candidate prompt"),
			Validation:    &promptiterengine.EvaluationResult{},
			Acceptance: &promptiterengine.AcceptanceDecision{
				Accepted:   true,
				ScoreDelta: 0.5,
				Reason:     "native acceptance",
			},
		}},
	}
}

type pipelineNativeService struct {
	mu                sync.Mutex
	surfaceID         string
	meter             *UsageMeter
	inferenceCalls    int
	inferenceRequests []service.InferenceRequest
	outcomes          map[string]pipelineNativeOutcome
}

type pipelineNativeOutcome struct {
	score  float64
	status status.EvalStatus
}

func (s *pipelineNativeService) Inference(
	_ context.Context,
	request *service.InferenceRequest,
	_ ...service.Option,
) ([]*service.InferenceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inferenceCalls++
	if s.meter != nil {
		s.meter.Record(ResourceUsage{
			ModelCalls:   Count{Available: true, Value: 1},
			InputTokens:  Count{Available: true},
			OutputTokens: Count{Available: true},
			LatencyMS:    Count{Available: true},
		})
	}
	call := s.inferenceCalls
	copied := *request
	copied.EvalCaseIDs = append([]string(nil), request.EvalCaseIDs...)
	s.inferenceRequests = append(s.inferenceRequests, copied)
	if s.outcomes == nil {
		s.outcomes = make(map[string]pipelineNativeOutcome)
	}
	caseIDs := append([]string(nil), request.EvalCaseIDs...)
	if len(caseIDs) == 0 {
		caseIDs = []string{"train-a"}
	}
	score := 0.2
	if call == 3 {
		score = 0.1
	}
	results := make([]*service.InferenceResult, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		invocationID := fmt.Sprintf("call-%d-%s", call, caseID)
		s.outcomes[invocationID] = pipelineNativeOutcome{
			score:  score,
			status: status.EvalStatusFailed,
		}
		results = append(results, &service.InferenceResult{
			AppName:    request.AppName,
			EvalSetID:  request.EvalSetID,
			EvalCaseID: caseID,
			SessionID:  "session-" + invocationID,
			UserID:     "user",
			Status:     status.EvalStatusPassed,
			Inferences: []*evalset.Invocation{{
				InvocationID: invocationID,
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: fmt.Sprintf("response-%d", call),
				},
			}},
			ExecutionTraces: []*atrace.Trace{{
				RootAgentName:    "target",
				RootInvocationID: invocationID,
				SessionID:        "session-" + invocationID,
				Status:           atrace.TraceStatusCompleted,
				Steps: []atrace.Step{{
					StepID:            "step-" + invocationID,
					InvocationID:      invocationID,
					NodeID:            pipelineTestNodeID,
					AppliedSurfaceIDs: []string{s.surfaceID},
					Input:             &atrace.Snapshot{Text: "input"},
					Output:            &atrace.Snapshot{Text: "output"},
				}},
			}},
		})
	}
	return results, nil
}

func (s *pipelineNativeService) Evaluate(
	_ context.Context,
	request *service.EvaluateRequest,
	_ ...service.Option,
) (*service.EvalSetRunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]*evalresult.EvalCaseResult, 0, len(request.InferenceResults))
	for _, inference := range request.InferenceResults {
		invocation := inference.Inferences[0]
		outcome := s.outcomes[invocation.InvocationID]
		results = append(results, &evalresult.EvalCaseResult{
			EvalSetID:       request.EvalSetID,
			EvalID:          inference.EvalCaseID,
			FinalEvalStatus: outcome.status,
			SessionID:       inference.SessionID,
			UserID:          inference.UserID,
			OverallEvalMetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "quality",
				Score:      outcome.score,
				EvalStatus: outcome.status,
				Threshold:  0.5,
				Details: &evalresult.EvalMetricResultDetails{
					Reason: "native loss",
					Score:  outcome.score,
				},
			}},
			EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
				ActualInvocation: invocation,
				ExpectedInvocation: &evalset.Invocation{
					InvocationID: "expected",
					FinalResponse: &model.Message{
						Role:    model.RoleAssistant,
						Content: "expected",
					},
				},
			}},
		})
	}
	return &service.EvalSetRunResult{
		AppName:         request.AppName,
		EvalSetID:       request.EvalSetID,
		EvalCaseResults: results,
	}, nil
}

func (*pipelineNativeService) Close() error {
	return nil
}

type pipelineBackwarder struct {
	mu       sync.Mutex
	requests []*backwarder.Request
}

func (b *pipelineBackwarder) Backward(
	_ context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	b.mu.Lock()
	b.requests = append(b.requests, request)
	b.mu.Unlock()
	if len(request.AllowedGradientSurfaceIDs) == 0 {
		return nil, errors.New("no allowed gradient surfaces")
	}
	severity := promptiter.LossSeverityP2
	if len(request.Incoming) > 0 && request.Incoming[0].Severity != "" {
		severity = request.Incoming[0].Severity
	}
	return &backwarder.Result{
		Gradients: []promptiter.SurfaceGradient{{
			EvalSetID:  request.EvalSetID,
			EvalCaseID: request.EvalCaseID,
			StepID:     request.StepID,
			SurfaceID:  request.AllowedGradientSurfaceIDs[0],
			Severity:   severity,
			Gradient:   "improve prompt",
		}},
	}, nil
}

type pipelineAggregator struct{}

func (pipelineAggregator) Aggregate(
	_ context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	return &aggregator.Result{Gradient: &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID,
		NodeID:    request.NodeID,
		Type:      request.Type,
		Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
	}}, nil
}

type pipelineOptimizer struct{}

func (pipelineOptimizer) Optimize(
	_ context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     astructure.SurfaceValue{Text: stringPointer("candidate prompt")},
		Reason:    "native optimizer patch",
	}}, nil
}

type pipelineRunner struct{}

func (pipelineRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event)
	close(events)
	return events, nil
}

func (pipelineRunner) Close() error {
	return nil
}

type pipelineNativeResultEvaluator struct {
	result  *evaluation.EvaluationResult
	results []*evaluation.EvaluationResult
	calls   int
}

func (e *pipelineNativeResultEvaluator) Evaluate(
	context.Context,
	string,
	...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	e.calls++
	if len(e.results) > 0 {
		index := min(e.calls-1, len(e.results)-1)
		return e.results[index], nil
	}
	return e.result, nil
}

func (*pipelineNativeResultEvaluator) Close() error {
	return nil
}

func pipelineNativeEvaluator(
	t *testing.T,
	ctx context.Context,
	evalService service.Service,
) (evaluation.AgentEvaluator, evalset.Manager, metric.Manager) {
	t.Helper()
	evalSetManager := evalsetinmemory.New()
	_, err := evalSetManager.Create(ctx, "pipeline-native", "train")
	require.NoError(t, err)
	require.NoError(t, evalSetManager.AddCase(ctx, "pipeline-native", "train", &evalset.EvalCase{
		EvalID: "train-a",
	}))
	metricManager := metricinmemory.New()
	require.NoError(t, metricManager.Add(ctx, "pipeline-native", "train", &metric.EvalMetric{
		MetricName: "quality",
		Threshold:  0.5,
	}))
	evaluator, err := evaluation.New(
		"pipeline-native",
		pipelineRunner{},
		evaluation.WithEvaluationService(evalService),
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
	)
	require.NoError(t, err)
	return evaluator, evalSetManager, metricManager
}

func nativeEvidenceResult(appName, evalSetID string) *evaluation.EvaluationResult {
	actualOne := &evalset.Invocation{
		InvocationID: "actual-1",
		Tools: []*evalset.Tool{{
			Name:      "lookup",
			Arguments: map[string]any{"id": "A-17"},
		}},
	}
	actualTwo := &evalset.Invocation{
		InvocationID: "actual-2",
		FinalResponse: &model.Message{
			Role:    model.RoleAssistant,
			Content: `{"status":"bad"}`,
		},
		Tools: []*evalset.Tool{{
			Name:      "verify",
			Arguments: map[string]any{"id": "A-71"},
		}},
	}
	metricResult := &evalresult.EvalMetricResult{
		MetricName: "quality",
		Score:      0.2,
		EvalStatus: status.EvalStatusFailed,
		Threshold:  0.5,
		Details: &evalresult.EvalMetricResultDetails{
			Reason: "wrong arguments",
			Score:  0.2,
			RubricScores: []*evalresult.RubricScore{{
				ID:     "arguments",
				Reason: "id mismatch",
				Score:  0,
			}},
		},
	}
	return &evaluation.EvaluationResult{
		AppName:       appName,
		EvalSetID:     evalSetID,
		OverallStatus: status.EvalStatusFailed,
		ExecutionTime: time.Millisecond,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "case-a",
			OverallStatus: status.EvalStatusFailed,
			MetricResults: []*evalresult.EvalMetricResult{metricResult},
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalSetID:                evalSetID,
				EvalID:                   "case-a",
				RunID:                    1,
				FinalEvalStatus:          status.EvalStatusFailed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{metricResult},
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				RunID: 1,
				Inference: &evaluation.EvaluationInferenceDetails{
					SessionID:  "session",
					UserID:     "user",
					Status:     status.EvalStatusPassed,
					Inferences: []*evalset.Invocation{actualOne, actualTwo},
					ExecutionTraces: []*atrace.Trace{{
						RootAgentName:    "target",
						RootInvocationID: "actual-2",
						SessionID:        "session",
						Status:           atrace.TraceStatusCompleted,
						Steps: []atrace.Step{{
							StepID:            "step-a",
							InvocationID:      "actual-2",
							NodeID:            pipelineTestNodeID,
							Branch:            "router:support",
							AppliedSurfaceIDs: []string{pipelineTestSurfaceID},
							Input:             &atrace.Snapshot{Text: "input"},
							Output:            &atrace.Snapshot{Text: "output"},
						}},
					}},
				},
			}},
		}},
	}
}

const (
	pipelineTestNodeID    = "node"
	pipelineTestSurfaceID = "node#instruction"
)

func pipelineTestStructure() *astructure.Snapshot {
	return &astructure.Snapshot{
		StructureID: "pipeline-structure",
		EntryNodeID: pipelineTestNodeID,
		Nodes: []astructure.Node{{
			NodeID: pipelineTestNodeID,
			Kind:   astructure.NodeKindLLM,
			Name:   "target",
		}},
		Surfaces: []astructure.Surface{{
			SurfaceID: pipelineTestSurfaceID,
			NodeID:    pipelineTestNodeID,
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: stringPointer("structure prompt")},
		}},
	}
}

func pipelineProfile(value string) *promptiter.Profile {
	return &promptiter.Profile{
		StructureID: "pipeline-structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: pipelineTestSurfaceID,
			Value:     astructure.SurfaceValue{Text: stringPointer(value)},
		}},
	}
}

func pipelineProfileText(profile *promptiter.Profile) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == pipelineTestSurfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

func pipelineRunConfig() RunConfig {
	config := RunConfig{
		ReportID:       "pipeline-report",
		GeneratedAt:    time.Unix(0, 0).UTC(),
		Seed:           2003,
		InitialProfile: pipelineProfile("initial prompt"),
		Train: DatasetSpec{
			EvalSetID:             "train",
			EvalSetHash:           "train-hash",
			MetricsHash:           "metrics-hash",
			CaseIDs:               []string{"train-a"},
			MetricNames:           []string{"quality"},
			NormalizedInputHashes: map[string]string{"train-a": "normalized-train"},
		},
		Validation: DatasetSpec{
			EvalSetID:             "heldout",
			EvalSetHash:           "heldout-hash",
			MetricsHash:           "metrics-hash",
			CaseIDs:               []string{"heldout-a"},
			MetricNames:           []string{"quality"},
			NormalizedInputHashes: map[string]string{"heldout-a": "normalized-heldout"},
		},
		PromptIter: PromptIterPolicy{
			MaxOuterRounds:             1,
			SearchMinScoreGain:         0.1,
			InternalValidationStrategy: internalValidationTrainCaseIDs,
			InternalValidationCaseIDs:  []string{"train-a"},
			TargetSurfaceIDs:           []string{pipelineTestSurfaceID},
		},
		Gate: GatePolicy{
			PrimaryMetric:         "quality",
			MetricDirections:      map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
			Epsilon:               DefaultEpsilon,
			MinValidationGain:     0.1,
			NoNewHardFailures:     true,
			NoCriticalRegressions: true,
		},
		Output: OutputConfig{
			JSON:     "optimization_report.json",
			Markdown: "optimization_report.md",
		},
		InputHashes: map[string]string{
			"trainEvalSet":      "train-hash",
			"validationEvalSet": "heldout-hash",
			"metrics":           "metrics-hash",
			"baselinePrompt":    "baseline-hash",
			"promptIterConfig":  "promptiter-config-hash",
			"regressionConfig":  "regression-config-hash",
		},
		Runtime: RuntimeConfig{
			Engine: "deterministic-test",
			Seed:   2003,
		},
		EvidenceLimit: 4,
	}
	bindPipelineRunConfig(&config)
	return config
}

func bindPipelineRunConfig(config *RunConfig) {
	if config.executionNonce == "" {
		config.executionNonce = "pipeline-test-execution"
	}
	gateJSON, err := json.Marshal(config.Gate)
	if err != nil {
		panic(err)
	}
	config.MetricPolicyHash = hashStrings(
		"native-metric-policy-v1",
		config.InputHashes["metrics"],
		string(gateJSON),
	)
	if err := BindRuntime(config, config.Runtime); err != nil {
		panic(err)
	}
	if err := sealSourceConfig(config); err != nil {
		panic(err)
	}
}

func stringPointer(value string) *string {
	return &value
}
