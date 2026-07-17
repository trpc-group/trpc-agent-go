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
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	dataAppName = "promptiter-regression-app"
	testDataDir = "./data"
)

// loadInputsAt loads the pipeline config and resolved inputs from dataDir,
// including any baseline profile a previous write-back left there.
func loadInputsAt(t *testing.T, dataDir string) (*Config, *resolvedInputs) {
	t.Helper()
	config, err := LoadConfig(filepath.Join(dataDir, dataAppName, "promptiter.json"))
	require.NoError(t, err)
	inputs, err := resolveInputs(dataDir, config)
	require.NoError(t, err)
	return config, inputs
}

func loadExampleInputs(t *testing.T) (*Config, *resolvedInputs) {
	t.Helper()
	config, inputs := loadInputsAt(t, testDataDir)
	require.NotContains(t, inputs.baselinePrompt, OptimizedMarker,
		"baseline prompt must not already contain the optimization marker")
	return config, inputs
}

// copyTestData clones the committed data dir into a temp dir so write-back
// tests can mutate the baseline files without touching the repository.
func copyTestData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(testDataDir)))
	return dir
}

func runExamplePipeline(t *testing.T, config *Config, inputs *resolvedInputs, dataDir, outputDir string, writeBack bool) *Result {
	t.Helper()
	// Bound the run so a deadlock or cancellation regression fails fast
	// instead of stalling until the go test global timeout.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := runPipeline(ctx, Options{
		Config:    config,
		Inputs:    inputs,
		DataDir:   dataDir,
		OutputDir: outputDir,
		Mode:      ModeFake,
		WriteBack: writeBack,
		Components: Components{
			// Mirror main.go: restored tool-description overrides are baked
			// into the candidate agent.
			CandidateAgent: NewAgent(NewModel(""), inputs.baselinePrompt, inputs.baselineToolDescriptions),
			Backwarder:     NewBackwarder(),
			Aggregator:     NewAggregator(),
			Optimizer:      NewOptimizer(),
		},
		Logger: log.New(os.Stderr, "[test] ", 0),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// TestPipelineRunFakeMode drives the whole loop end to end over the shipped
// data with the strict gate preset: the engine's inner score gate accepts the
// round-1 candidate, and the outer gate rejects it as overfitting.
func TestPipelineRunFakeMode(t *testing.T) {
	config, inputs := loadExampleInputs(t)
	outputDir := t.TempDir()
	started := time.Now()
	result := runExamplePipeline(t, config, inputs, testDataDir, outputDir, false)
	// Acceptance criterion 5 requires the full fake pipeline under 3 minutes;
	// assert a much tighter bound.
	assert.Less(t, time.Since(started), time.Minute)
	assert.Equal(t, StatusRejected, result.Status)

	// Acceptance criterion 4: every failed case carries at least one
	// explainable root cause with evidence.
	failedBaseline := 0
	for _, snapshot := range append(result.BaselineTrain, result.BaselineValidation...) {
		if !snapshot.Pass {
			failedBaseline++
		}
	}
	require.Len(t, result.BaselineAttributions, failedBaseline)
	for _, attribution := range result.BaselineAttributions {
		require.NotEmpty(t, attribution.RootCauses, attribution.EvalCaseID)
		for _, cause := range attribution.RootCauses {
			assert.NotEmpty(t, cause.Evidence, attribution.EvalCaseID)
		}
	}

	// Baseline snapshots cover all seven cases with per-metric outcomes.
	assert.Len(t, result.BaselineTrain, 4)
	assert.Len(t, result.BaselineValidation, 3)
	for _, snapshot := range result.BaselineTrain {
		assert.Len(t, snapshot.Metrics, 2, snapshot.EvalCaseID)
	}

	// Designed aggregate scores: baseline validation 4/6, round-1 candidate
	// 5/6 accepted by the engine's inner score gate (the overfit candidate the
	// outer gate must later reject).
	require.NotNil(t, result.Run)
	assert.InDelta(t, 4.0/6.0, result.Run.BaselineValidation.OverallScore, 1e-9)
	require.NotEmpty(t, result.Run.Rounds)
	round1 := result.Run.Rounds[0]
	require.NotNil(t, round1.Validation)
	assert.InDelta(t, 5.0/6.0, round1.Validation.OverallScore, 1e-9)
	require.NotNil(t, round1.Acceptance)
	assert.True(t, round1.Acceptance.Accepted)

	// S2 attribution: baseline failures carry causal chains; train_02's root
	// cause is the wrong tool call.
	require.NotEmpty(t, result.BaselineAttributions)
	attributionByCase := make(map[string]CaseAttribution)
	for _, attribution := range result.BaselineAttributions {
		attributionByCase[attribution.EvalCaseID] = attribution
	}
	train02, ok := attributionByCase["train_02_wrong_tool_choice"]
	require.True(t, ok)
	require.NotEmpty(t, train02.RootCauses)
	assert.Equal(t, CauseToolCallError, train02.RootCauses[0].Category)
	assert.Contains(t, train02.RootCauses[0].Evidence, "query_order")
	assert.Contains(t, train02.RootCauses[0].Evidence, "query_logistics")

	// train_04 picks the right tool with the wrong argument: the trajectory
	// diff must classify it as tool_argument_error (not tool_call_error) with
	// the expected/actual argument values in evidence, and the final response
	// mismatch folded under it as a derived symptom.
	train04, ok := attributionByCase["train_04_wrong_tool_argument"]
	require.True(t, ok)
	require.Len(t, train04.RootCauses, 1)
	assert.Equal(t, CauseToolArgumentError, train04.RootCauses[0].Category)
	assert.Contains(t, train04.RootCauses[0].Evidence, "ORD-1007")
	assert.Contains(t, train04.RootCauses[0].Evidence, "ORD-1070")
	require.Len(t, train04.Chain, 2)
	assert.Equal(t, CauseFinalResponseMismatch, train04.Chain[1].Category)
	assert.Equal(t, CauseToolArgumentError, train04.Chain[1].DerivedFrom)

	// S4/S5: the round-1 candidate raises the aggregate but flips the
	// protected case; the outer gate must reject it as overfitting.
	require.NotNil(t, result.Gate)
	assert.False(t, result.Gate.Accepted)
	assert.Equal(t, RecommendationReject, result.Gate.Recommendation)
	assert.Contains(t, result.Gate.Summary, "过拟合")
	assert.Contains(t, result.Gate.Summary, "val_02_protected_format")
	require.NotEmpty(t, result.Candidates)
	deltaByCase := make(map[string]CaseDelta)
	for _, delta := range result.Candidates[0].Deltas {
		deltaByCase[delta.EvalCaseID] = delta
	}
	assert.Equal(t, DeltaNewPass, deltaByCase["val_01_generalize_tool_and_format"].Kind)
	assert.Equal(t, DeltaNewFail, deltaByCase["val_02_protected_format"].Kind)
	assert.Equal(t, DeltaUnchanged, deltaByCase["val_03_stable_pass"].Kind)
	assert.Empty(t, result.CandidatePrompt, "rejected run must not emit a candidate prompt")
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_prompt.txt"))
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_profile.json"))

	// Cost accounting is populated and every stage has a duration.
	assert.Positive(t, result.Cost.Total.RunCalls)
	assert.Positive(t, result.Cost.Total.ModelCalls)
	assert.Positive(t, result.Cost.Total.PromptTokens)
	for _, stage := range []string{"s1_baseline_train", "s2_attribution", "s3_optimization", "s4_delta", "s5_gate"} {
		assert.Contains(t, result.StageDurations, stage)
	}

	// Reports: both formats generated with the reject verdict.
	assert.Equal(t, filepath.Join(outputDir, "optimization_report.json"), result.ReportJSONPath)
	markdown, err := os.ReadFile(result.ReportMarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "**拒绝**")
	assert.Contains(t, string(markdown), "判定为过拟合")

	// Audit trail: run meta, baseline artifacts, attribution, gate decision,
	// and per-round event files.
	auditDir := filepath.Join(outputDir, "audit", result.RunID)
	for _, path := range []string{
		filepath.Join(outputDir, "optimization_report.json"),
		filepath.Join(outputDir, "optimization_report.md"),
		filepath.Join(auditDir, "run_meta.json"),
		filepath.Join(auditDir, "baseline_train.json"),
		filepath.Join(auditDir, "baseline_train_attribution.json"),
		filepath.Join(auditDir, "baseline_validation.json"),
		filepath.Join(auditDir, "baseline_validation_attribution.json"),
		filepath.Join(auditDir, "candidates.json"),
		filepath.Join(auditDir, "gate_decision.json"),
		filepath.Join(auditDir, "round_1", "round_patch_set.json"),
		filepath.Join(auditDir, "round_1", "round_validation.json"),
		filepath.Join(auditDir, "round_1", "cost.json"),
	} {
		assert.FileExists(t, path)
	}
	var meta RunMeta
	content, err := os.ReadFile(filepath.Join(auditDir, "run_meta.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &meta))
	assert.Equal(t, config.Seed, meta.Seed)
	assert.Equal(t, result.RunID, meta.RunID)
	assert.Contains(t, meta.TargetSurfaceIDs, "candidate#instruction")
}

// TestTraceModeEvalSetRunsWithoutInference locks the trace-mode path: cases
// with evalMode "trace" are scored from the recorded actualConversation with
// zero model inference, and the recorded tool trajectory feeds the same
// attribution rule engine — the deterministic no-API-key route for evaluating
// recorded (e.g. hidden or canary) samples.
func TestTraceModeEvalSetRunsWithoutInference(t *testing.T) {
	ctx := context.Background()
	tracker := NewCostTracker()
	candidateRunner := tracker.Wrap(
		"candidate",
		runner.NewRunner(dataAppName, NewAgent(NewModel(""), "任意指令，trace 模式下不应被调用", nil)),
	)
	t.Cleanup(func() { candidateRunner.Close() })
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(testDataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(testDataDir),
		metric.WithLocator(&SharedMetricLocator{}),
	)
	agentEvaluator, err := evaluation.New(
		dataAppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
	)
	require.NoError(t, err)
	t.Cleanup(func() { agentEvaluator.Close() })
	result, err := agentEvaluator.Evaluate(ctx, "trace", evaluation.WithRunDetailsEnabled(true))
	require.NoError(t, err)

	// Trace mode must not touch the candidate model at all.
	assert.Zero(t, tracker.Snapshot().Total.RunCalls)
	assert.Zero(t, tracker.Snapshot().Total.ModelCalls)

	snapshots := SnapshotsFromEvaluationResult(result)
	require.Len(t, snapshots, 2)
	snapshotByCase := make(map[string]CaseSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		snapshotByCase[snapshot.EvalCaseID] = snapshot
	}
	assert.True(t, snapshotByCase["trace_01_recorded_pass"].Pass)
	failing := snapshotByCase["trace_02_recorded_wrong_argument"]
	assert.False(t, failing.Pass)

	// The recorded trajectory carries through run details, so attribution
	// splits the wrong argument from a wrong tool choice without any model.
	metrics := make([]*metric.EvalMetric, 0, 2)
	for _, name := range []string{"final_response_avg_score", "tool_trajectory_avg_score"} {
		evalMetric, err := metricManager.Get(ctx, dataAppName, "trace", name)
		require.NoError(t, err)
		metrics = append(metrics, evalMetric)
	}
	traceSet, err := evalSetManager.Get(ctx, dataAppName, "trace")
	require.NoError(t, err)
	var expected []*evalset.Invocation
	for _, evalCase := range traceSet.EvalCases {
		if evalCase != nil && evalCase.EvalID == failing.EvalCaseID {
			expected = evalCase.Conversation
		}
	}
	require.NotEmpty(t, expected)
	attribution := NewAttributor(metrics, nil).Attribute(failing, expected)
	require.NotNil(t, attribution)
	require.Len(t, attribution.RootCauses, 1)
	assert.Equal(t, CauseToolArgumentError, attribution.RootCauses[0].Category)
	assert.Contains(t, attribution.RootCauses[0].Evidence, "ORD-1007")
	assert.Contains(t, attribution.RootCauses[0].Evidence, "ORD-1070")
	require.Len(t, attribution.Chain, 2)
	assert.Equal(t, CauseToolArgumentError, attribution.Chain[1].DerivedFrom)
}

// TestPipelineRunRelaxedGateAccepts reruns the loop with the relaxed gate
// preset (protected case unprotected, one regression tolerated): the same
// candidate is accepted and the optimized prompt is emitted.
func TestPipelineRunRelaxedGateAccepts(t *testing.T) {
	config, inputs := loadExampleInputs(t)
	config.Gate.ProtectedCases = nil
	config.Gate.MaxRegressedCases = 1
	config.Gate.MaxNewHardFails = 1
	outputDir := t.TempDir()
	result := runExamplePipeline(t, config, inputs, testDataDir, outputDir, false)

	assert.Equal(t, StatusAccepted, result.Status)
	require.NotNil(t, result.Gate)
	assert.True(t, result.Gate.Accepted)
	assert.Equal(t, RecommendationAcceptPendingCanary, result.Gate.Recommendation)
	// Rounds 1 and 2 carry the identical optimized profile (the scripted
	// optimizer is idempotent), so selection may legitimately pick either;
	// the accepted prompt is what matters.
	assert.Positive(t, result.Gate.SelectedRound)

	// The accepted candidate prompt is persisted and carries the marker; the
	// full profile (including the tool-description override) is persisted too.
	require.NotEmpty(t, result.CandidatePrompt)
	assert.Contains(t, result.CandidatePrompt, OptimizedMarker)
	promptContent, err := os.ReadFile(filepath.Join(outputDir, "candidate_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(promptContent), OptimizedMarker)
	assert.Contains(t, string(promptContent), inputs.baselinePrompt,
		"optimizer appends constraints without discarding the baseline")
	profileContent, err := os.ReadFile(filepath.Join(outputDir, "candidate_profile.json"))
	require.NoError(t, err)
	assert.Contains(t, string(profileContent), "candidate#instruction")

	// The accept-path report carries the canary recommendation.
	markdown, err := os.ReadFile(result.ReportMarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "**接受**（accept_pending_canary）")
	assert.Contains(t, string(markdown), "canary")
}

// TestConsecutiveWriteBacksKeepToolOverride locks write-back fidelity across
// runs. The first acceptance persists the merged effective profile
// (instruction + improved tool description) to baseline_profile.json; the
// rerun restores it, baking the tool description into the agent, so its own
// accepted candidate no longer carries the tool patch. The second write-back
// must merge onto the restored baseline instead of overwriting it, or the
// inherited tool override would be silently dropped for every later run.
func TestConsecutiveWriteBacksKeepToolOverride(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	improved := ImprovedToolDescriptions[ToolQueryOrder]
	relax := func(config *Config) {
		config.Gate.ProtectedCases = nil
		config.Gate.MaxRegressedCases = 1
		config.Gate.MaxNewHardFails = 1
	}

	// First write-back: the optimized candidate (instruction + tool patch)
	// is accepted and becomes the on-disk baseline.
	config, inputs := loadInputsAt(t, dataDir)
	relax(config)
	first := runExamplePipeline(t, config, inputs, dataDir, outputDir, true)
	require.Equal(t, StatusAccepted, first.Status)
	profileContent, err := os.ReadFile(inputs.baselineProfilePath)
	require.NoError(t, err)
	require.Contains(t, string(profileContent), improved,
		"first write-back must persist the accepted tool override")

	// Rerun over the written-back baseline: the tool description and marker
	// instruction are restored, so the baseline already behaves as the
	// previously accepted candidate.
	config, inputs = loadInputsAt(t, dataDir)
	require.Equal(t, improved, inputs.baselineToolDescriptions[ToolQueryOrder])
	require.Contains(t, inputs.baselinePrompt, OptimizedMarker)
	relax(config)
	// Narrow the second run to the instruction surface — the documented shape
	// of a later instruction-only optimization — so its accepted profile
	// cannot carry the tool patch at all.
	config.TargetSurfaces = []TargetSurface{{Node: "candidate", Type: "instruction"}}
	inputs, err = resolveInputs(dataDir, config)
	require.NoError(t, err)
	// Zero-gain thresholds let the idempotent zero-delta candidate through.
	zeroGain := 0.0
	config.Engine.MinScoreGain = &zeroGain
	config.Gate.MinValidationScoreGain = 0
	second := runExamplePipeline(t, config, inputs, dataDir, outputDir, true)
	require.Equal(t, StatusAccepted, second.Status)
	assert.InDelta(t, 5.0/6.0, second.BaselineValidationScore, 1e-9,
		"rerun baseline must behave as the previously accepted candidate")
	// The scenario is only meaningful if the second accepted profile itself
	// lacks the tool patch: the override is baked into the agent, so only
	// the merge can carry it forward.
	candidateContent, err := os.ReadFile(filepath.Join(outputDir, "candidate_profile.json"))
	require.NoError(t, err)
	require.NotContains(t, string(candidateContent), improved,
		"second accepted profile must not itself carry the tool patch")

	// The second write-back keeps the inherited tool override.
	profileContent, err = os.ReadFile(inputs.baselineProfilePath)
	require.NoError(t, err)
	assert.Contains(t, string(profileContent), improved,
		"consecutive write-backs must not drop the inherited tool override")
	_, reloaded := loadInputsAt(t, dataDir)
	assert.Equal(t, improved, reloaded.baselineToolDescriptions[ToolQueryOrder])
	assert.Contains(t, reloaded.baselinePrompt, OptimizedMarker)
}
