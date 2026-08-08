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
	"io/fs"
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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
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
// tests can mutate the baseline files without touching the repository. It
// walks the tree by hand because os.CopyFS needs Go 1.23 while the module
// still declares Go 1.21.
func copyTestData(t *testing.T) string {
	t.Helper()
	return copyTestDataInto(t, t.TempDir())
}

// copyTestDataInto clones the committed data dir into an explicit directory,
// letting a test choose a path whose name exercises quoting.
func copyTestDataInto(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	source := os.DirFS(testDataDir)
	require.NoError(t, fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}))
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

	// Accepted rerun without write-back: the engine's own round profile cannot
	// carry the tool patch (the override is baked into the agent), yet the
	// deployable candidate_profile.json must still publish the effective
	// profile including the inherited override.
	noWriteBackDir := t.TempDir()
	noWriteBack := runExamplePipeline(t, config, inputs, dataDir, noWriteBackDir, false)
	require.Equal(t, StatusAccepted, noWriteBack.Status)
	require.NotContains(t, selectedRoundProfileJSON(t, noWriteBack), improved,
		"the accepted round profile must not itself carry the tool patch")
	candidateContent, err := os.ReadFile(filepath.Join(noWriteBackDir, "candidate_profile.json"))
	require.NoError(t, err)
	assert.Contains(t, string(candidateContent), improved,
		"candidate_profile.json must carry the inherited tool override even without write-back")

	second := runExamplePipeline(t, config, inputs, dataDir, outputDir, true)
	require.Equal(t, StatusAccepted, second.Status)
	assert.InDelta(t, 5.0/6.0, second.BaselineValidationScore, 1e-9,
		"rerun baseline must behave as the previously accepted candidate")
	// The scenario is only meaningful if the second accepted round profile
	// itself lacks the tool patch: the override is baked into the agent, so
	// only the merge can carry it forward.
	require.NotContains(t, selectedRoundProfileJSON(t, second), improved,
		"the accepted round profile must not itself carry the tool patch")
	candidateContent, err = os.ReadFile(filepath.Join(outputDir, "candidate_profile.json"))
	require.NoError(t, err)
	assert.Contains(t, string(candidateContent), improved,
		"candidate_profile.json must publish the effective profile")

	// The second write-back keeps the inherited tool override.
	profileContent, err = os.ReadFile(inputs.baselineProfilePath)
	require.NoError(t, err)
	assert.Contains(t, string(profileContent), improved,
		"consecutive write-backs must not drop the inherited tool override")
	_, reloaded := loadInputsAt(t, dataDir)
	assert.Equal(t, improved, reloaded.baselineToolDescriptions[ToolQueryOrder])
	assert.Contains(t, reloaded.baselinePrompt, OptimizedMarker)
}

// TestToolOnlyAcceptanceClearsStalePromptArtifact locks the stable prompt
// path against a stale artifact: an instruction-changing acceptance publishes
// candidate_prompt.txt, and a later tool-only acceptance into the same output
// dir keeps the baseline instruction in force, so that prompt file must go —
// otherwise consumers of the stable path deploy text the latest accepted run
// never evaluated.
func TestToolOnlyAcceptanceClearsStalePromptArtifact(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	promptPath := filepath.Join(outputDir, "candidate_prompt.txt")
	relax := func(config *Config) {
		config.Gate.ProtectedCases = nil
		config.Gate.MaxRegressedCases = 1
		config.Gate.MaxNewHardFails = 1
	}

	// First run: instruction-only optimization, accepted and written back, so
	// the output dir holds a candidate prompt and the baseline carries the
	// marker instruction.
	config, inputs := loadInputsAt(t, dataDir)
	relax(config)
	config.TargetSurfaces = []TargetSurface{{Node: "candidate", Type: "instruction"}}
	inputs, err := resolveInputs(dataDir, config)
	require.NoError(t, err)
	first := runExamplePipeline(t, config, inputs, dataDir, outputDir, true)
	require.Equal(t, StatusAccepted, first.Status)
	require.FileExists(t, promptPath)
	stalePrompt, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	require.Contains(t, string(stalePrompt), OptimizedMarker)

	// Second run over the written-back baseline, optimizing the tool surface
	// only — the shape a round takes when the instruction collects no gradient.
	// The initial instruction override then equals the agent's instruction and
	// the engine normalizes it away, so the accepted profile carries only the
	// tool override and the baseline prompt stays in force.
	config, inputs = loadInputsAt(t, dataDir)
	relax(config)
	zeroGain := 0.0
	config.Engine.MinScoreGain = &zeroGain
	config.Gate.MinValidationScoreGain = 0
	toolSurfaceID, err := TargetSurface{Node: "candidate", Type: "tool", Name: ToolQueryOrder}.ID()
	require.NoError(t, err)
	require.Contains(t, inputs.targetSurfaceIDs, toolSurfaceID)
	inputs.targetSurfaceIDs = []string{toolSurfaceID}
	second := runExamplePipeline(t, config, inputs, dataDir, outputDir, false)
	require.Equal(t, StatusAccepted, second.Status)
	require.Empty(t, second.CandidatePromptPath,
		"the accepted profile must carry no instruction override for this scenario")

	assert.NoFileExists(t, promptPath,
		"a tool-only acceptance must clear the prompt artifact of the earlier acceptance")
	profileContent, err := os.ReadFile(filepath.Join(outputDir, "candidate_profile.json"))
	require.NoError(t, err)
	assert.Contains(t, string(profileContent), ImprovedToolDescriptions[ToolQueryOrder],
		"the tool-only acceptance still publishes its effective profile")
	publishedProfile := &promptiter.Profile{}
	require.NoError(t, json.Unmarshal(profileContent, publishedProfile))
	instruction := ""
	for _, override := range publishedProfile.Overrides {
		if override.SurfaceID == "candidate#instruction" && override.Value.Text != nil {
			instruction = *override.Value.Text
		}
	}
	assert.Equal(t, inputs.baselinePrompt, instruction,
		"the effective profile keeps the baseline instruction in force")
}

// TestResolveInputsRejectsUnknownProtectedCase locks the fail-closed rule: a
// mistyped protected case ID would never match any validation delta, silently
// disabling the protection, so input resolution must reject it.
func TestResolveInputsRejectsUnknownProtectedCase(t *testing.T) {
	config, err := LoadConfig(filepath.Join(testDataDir, dataAppName, "promptiter.json"))
	require.NoError(t, err)
	config.Gate.ProtectedCases = []string{"val_protectd"}
	_, err = resolveInputs(testDataDir, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "val_protectd")
	assert.Contains(t, err.Error(), "not present in validation eval set")
}

// TestConfigRejectsDuplicateProtectedCases: a duplicated protected case ID is
// a config mistake that validation surfaces immediately.
func TestConfigRejectsDuplicateProtectedCases(t *testing.T) {
	config, err := LoadConfig(filepath.Join(testDataDir, dataAppName, "promptiter.json"))
	require.NoError(t, err)
	config.Gate.ProtectedCases = append(config.Gate.ProtectedCases, config.Gate.ProtectedCases[0])
	err = config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicated")
}

// TestPipelineRejectsUnknownMetricHint locks the fail-closed rule for
// attribution hints: a hint keyed by a metric name that does not exist would
// be silently unused, and the metric it was meant to classify would fall
// back to final_response_mismatch — a mistyped route_error hint could then
// slip a hard failure past a nonzero regression allowance.
func TestPipelineRejectsUnknownMetricHint(t *testing.T) {
	config, inputs := loadExampleInputs(t)
	config.Attribution.MetricCategoryHints = map[string]string{
		"tool_trajectory_avg_scor": string(CauseRouteError),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := runPipeline(ctx, Options{
		Config:    config,
		Inputs:    inputs,
		DataDir:   testDataDir,
		OutputDir: t.TempDir(),
		Mode:      ModeFake,
		Components: Components{
			CandidateAgent: NewAgent(NewModel(""), inputs.baselinePrompt, inputs.baselineToolDescriptions),
			Backwarder:     NewBackwarder(),
			Aggregator:     NewAggregator(),
			Optimizer:      NewOptimizer(),
		},
		Logger: log.New(os.Stderr, "[test] ", 0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool_trajectory_avg_scor")
	assert.Contains(t, err.Error(), "metricCategoryHints")
}

// TestPublishFailureKeepsPreviousReportsAndDeployableState locks the
// one-transaction publication contract: when a later run fails on its last
// staged write, the previous run's reports AND its deployable candidate
// artifacts must all survive untouched — consumers of the stable paths can
// never observe reports that disagree with the candidate state next to them.
func TestPublishFailureKeepsPreviousReportsAndDeployableState(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	relax := func(config *Config) {
		config.Gate.ProtectedCases = nil
		config.Gate.MaxRegressedCases = 1
		config.Gate.MaxNewHardFails = 1
	}

	// First run: accepted without write-back, publishing reports plus the
	// candidate artifacts.
	config, inputs := loadInputsAt(t, dataDir)
	relax(config)
	first := runExamplePipeline(t, config, inputs, dataDir, outputDir, false)
	require.Equal(t, StatusAccepted, first.Status)
	stablePaths := []string{
		filepath.Join(outputDir, "optimization_report.json"),
		filepath.Join(outputDir, "optimization_report.md"),
		filepath.Join(outputDir, "candidate_prompt.txt"),
		filepath.Join(outputDir, "candidate_profile.json"),
	}
	previous := make(map[string]string, len(stablePaths))
	for _, path := range stablePaths {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		previous[path] = string(content)
	}
	originalPrompt, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)

	// Second run with write-back: a directory squatting on the write-back
	// profile path makes the *last* staged write fail after the reports and
	// candidate artifacts were already replaced, forcing a full rollback.
	config, inputs = loadInputsAt(t, dataDir)
	relax(config)
	require.NoError(t, os.MkdirAll(inputs.baselineProfilePath, 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err = runPipeline(ctx, Options{
		Config:    config,
		Inputs:    inputs,
		DataDir:   dataDir,
		OutputDir: outputDir,
		Mode:      ModeFake,
		WriteBack: true,
		Components: Components{
			CandidateAgent: NewAgent(NewModel(""), inputs.baselinePrompt, inputs.baselineToolDescriptions),
			Backwarder:     NewBackwarder(),
			Aggregator:     NewAggregator(),
			Optimizer:      NewOptimizer(),
		},
		Logger: log.New(os.Stderr, "[test] ", 0),
	})
	require.Error(t, err)

	for _, path := range stablePaths {
		content, err := os.ReadFile(path)
		require.NoError(t, err, path)
		assert.Equal(t, previous[path], string(content),
			"%s must roll back to the previous run's content", path)
	}
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, string(originalPrompt), string(promptAfter),
		"the write-back must not survive a failed publication")
}

// TestRejectingRerunRemovesStaleCandidateArtifacts: the stable candidate
// paths of a previously accepted run are cleared by a later rejecting run in
// the same publication unit as its rejection reports, so consumers can never
// deploy a stale candidate against the latest gate decision.
func TestRejectingRerunRemovesStaleCandidateArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	config, inputs := loadExampleInputs(t)
	config.Gate.ProtectedCases = nil
	config.Gate.MaxRegressedCases = 1
	config.Gate.MaxNewHardFails = 1
	accepted := runExamplePipeline(t, config, inputs, testDataDir, outputDir, false)
	require.Equal(t, StatusAccepted, accepted.Status)
	require.FileExists(t, filepath.Join(outputDir, "candidate_prompt.txt"))
	require.FileExists(t, filepath.Join(outputDir, "candidate_profile.json"))

	// The committed strict preset rejects the same candidate.
	strictConfig, strictInputs := loadExampleInputs(t)
	rejected := runExamplePipeline(t, strictConfig, strictInputs, testDataDir, outputDir, false)
	require.Equal(t, StatusRejected, rejected.Status)
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_prompt.txt"))
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_profile.json"))
	markdown, err := os.ReadFile(rejected.ReportMarkdownPath)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "**拒绝**")
}

// TestAcceptedRunWithReportFailurePublishesNothing locks the one-transaction
// publication: when the gate accepts a candidate but publishing the report
// files fails, neither the deployable candidate artifacts nor the write-back
// baseline may be left behind.
func TestAcceptedRunWithReportFailurePublishesNothing(t *testing.T) {
	dataDir := copyTestData(t)
	config, inputs := loadInputsAt(t, dataDir)
	config.Gate.ProtectedCases = nil
	config.Gate.MaxRegressedCases = 1
	config.Gate.MaxNewHardFails = 1
	outputDir := t.TempDir()
	// A directory squatting on the JSON report path makes S6 fail after the
	// gate has already accepted the candidate.
	require.NoError(t, os.MkdirAll(filepath.Join(outputDir, "optimization_report.json"), 0o755))
	originalPrompt, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := runPipeline(ctx, Options{
		Config:    config,
		Inputs:    inputs,
		DataDir:   dataDir,
		OutputDir: outputDir,
		Mode:      ModeFake,
		WriteBack: true,
		Components: Components{
			CandidateAgent: NewAgent(NewModel(""), inputs.baselinePrompt, inputs.baselineToolDescriptions),
			Backwarder:     NewBackwarder(),
			Aggregator:     NewAggregator(),
			Optimizer:      NewOptimizer(),
		},
		Logger: log.New(os.Stderr, "[test] ", 0),
	})
	require.Error(t, err)
	require.Nil(t, result)
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_prompt.txt"))
	assert.NoFileExists(t, filepath.Join(outputDir, "candidate_profile.json"))
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, string(originalPrompt), string(promptAfter),
		"write-back must not mutate the baseline prompt when reports fail")
	assert.NoFileExists(t, inputs.baselineProfilePath)
}

// stagingFixture builds the minimal config/inputs/result trio needed to stage
// the artifacts of one accepted candidate whose instruction override is the
// given text.
func stagingFixture(t *testing.T, instruction string) (Options, *resolvedInputs, *Result, *GateDecision) {
	t.Helper()
	baselineDir := t.TempDir()
	promptSourcePath := filepath.Join(baselineDir, "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptSourcePath, []byte("基线指令\n"), 0o644))
	config := &Config{
		AppName:        dataAppName,
		EvalSets:       EvalSetsConfig{Train: "train", Validation: "validation"},
		PromptSource:   promptSourcePath,
		TargetSurfaces: []TargetSurface{{Node: "candidate", Type: "instruction"}},
	}
	config.applyDefaults()
	require.NoError(t, config.Validate())
	inputs := &resolvedInputs{
		promptSourcePath:    promptSourcePath,
		baselinePrompt:      "基线指令",
		baselineProfilePath: filepath.Join(baselineDir, baselineProfileFileName),
	}
	result := &Result{Candidates: []Candidate{{
		Round: 1,
		Profile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
			{SurfaceID: "candidate#instruction", Value: astructureTextValue(instruction)},
		}},
	}}}
	opts := Options{Config: config, OutputDir: t.TempDir(), WriteBack: true}
	return opts, inputs, result, &GateDecision{Accepted: true, SelectedRound: 1}
}

// TestStagingRejectsWhitespaceOnlyInstruction locks the fail-closed rule for
// degenerate instruction overrides: whitespace survives the optimizer
// sanitizer, but publishing it (and writing it back) would leave the next run
// rejecting its own baseline prompt as empty while -promote rejects the same
// profile. Nothing may be published for such a candidate.
func TestStagingRejectsWhitespaceOnlyInstruction(t *testing.T) {
	opts, inputs, result, decision := stagingFixture(t, "  \n\t ")

	_, err := stageCandidateArtifacts(opts, inputs, result, decision)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace only")
	assert.NoFileExists(t, filepath.Join(opts.OutputDir, candidatePromptFileName))
	assert.NoFileExists(t, filepath.Join(opts.OutputDir, candidateProfileFileName))
	assert.NoFileExists(t, inputs.baselineProfilePath)
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, "基线指令\n", string(promptAfter))
}

// TestStagingCanonicalizesInstructionWhitespace: surrounding whitespace is
// trimmed once, so the prompt artifact, the effective profile, and the
// write-back baseline all carry the identical canonical text — the form
// resolveInputs loads and -promote compares.
func TestStagingCanonicalizesInstructionWhitespace(t *testing.T) {
	opts, inputs, result, decision := stagingFixture(t, "\n\n  优化后的指令  \n\n")

	staged, err := stageCandidateArtifacts(opts, inputs, result, decision)
	require.NoError(t, err)
	require.NoError(t, publishFiles(staged))
	assert.Equal(t, "优化后的指令", result.CandidatePrompt)

	for _, path := range []string{
		filepath.Join(opts.OutputDir, candidatePromptFileName),
		inputs.promptSourcePath,
	} {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "优化后的指令\n", string(content), path)
	}
	profileContent, err := os.ReadFile(inputs.baselineProfilePath)
	require.NoError(t, err)
	profile := &promptiter.Profile{}
	require.NoError(t, json.Unmarshal(profileContent, profile))
	require.Len(t, profile.Overrides, 1)
	require.NotNil(t, profile.Overrides[0].Value.Text)
	assert.Equal(t, "优化后的指令", *profile.Overrides[0].Value.Text)

	// The canonical artifacts promote without tripping the consistency check.
	promotion, err := promoteCandidate(opts.Config, opts.OutputDir)
	require.NoError(t, err)
	assert.True(t, promotion.PromptPromoted)
}

// TestPublishFilesSerializesAcrossProcesses locks the inter-process contract:
// a publication holds a lock on every directory it touches, so a second
// publication (another run, or -promote) cannot interleave its own snapshot,
// writes, and rollback with the first and leave a mixed state such as a
// candidate prompt without the profile it was published with.
func TestPublishFilesSerializesAcrossProcesses(t *testing.T) {
	outputDir := t.TempDir()
	promptPath := filepath.Join(outputDir, candidatePromptFileName)
	profilePath := filepath.Join(outputDir, candidateProfileFileName)

	// Hold the lock the way a concurrent process would.
	release, err := lockPublishDirs([]stagedFile{{path: promptPath}})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outputDir, publishLockFileName))

	published := make(chan error, 1)
	go func() {
		published <- publishFiles([]stagedFile{
			{path: promptPath, content: []byte("prompt\n")},
			{path: profilePath, content: []byte("{}\n")},
		})
	}()
	select {
	case err := <-published:
		t.Fatalf("publication must wait for the lock holder, got %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	assert.NoFileExists(t, promptPath, "a blocked publication must not have written anything")

	release()
	select {
	case err := <-published:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("publication did not proceed after the lock was released")
	}
	assert.FileExists(t, promptPath)
	assert.FileExists(t, profilePath)
	// The lock is released with the publication, leaving no file behind.
	assert.NoFileExists(t, filepath.Join(outputDir, publishLockFileName))
}

// TestPublishFilesReportsStuckLockHolder: a lock left behind by a killed run
// fails the publication with an actionable message instead of hanging or
// publishing over the other process.
func TestPublishFilesReportsStuckLockHolder(t *testing.T) {
	previous := publishLockTimeout
	publishLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { publishLockTimeout = previous })

	outputDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(outputDir, publishLockFileName), []byte("pid=424242 since=earlier\n"), 0o644))
	err := publishFiles([]stagedFile{
		{path: filepath.Join(outputDir, candidateProfileFileName), content: []byte("{}\n")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pid=424242")
	assert.Contains(t, err.Error(), "delete the lock file")
	assert.NoFileExists(t, filepath.Join(outputDir, candidateProfileFileName))
}

// TestPublishFilesLocksEveryDirectoryOnce: a publication spanning the output
// dir and the baseline dir locks both, and takes one lock per directory even
// when several files share it.
func TestPublishFilesLocksEveryDirectoryOnce(t *testing.T) {
	outputDir := t.TempDir()
	baselineDir := t.TempDir()
	release, err := lockPublishDirs([]stagedFile{
		{path: filepath.Join(outputDir, candidatePromptFileName)},
		{path: filepath.Join(outputDir, candidateProfileFileName)},
		{path: filepath.Join(baselineDir, baselineProfileFileName)},
	})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outputDir, publishLockFileName))
	assert.FileExists(t, filepath.Join(baselineDir, publishLockFileName))
	release()
	assert.NoFileExists(t, filepath.Join(outputDir, publishLockFileName))
	assert.NoFileExists(t, filepath.Join(baselineDir, publishLockFileName))
}

// selectedRoundProfileJSON serializes the gate-selected round's raw engine
// profile — the round-relative override set before the pipeline merges it
// with the inherited baseline profile for publication.
func selectedRoundProfileJSON(t *testing.T, result *Result) string {
	t.Helper()
	require.NotNil(t, result.Gate)
	for _, candidate := range result.Candidates {
		if candidate.Round == result.Gate.SelectedRound {
			content, err := json.Marshal(candidate.Profile)
			require.NoError(t, err)
			return string(content)
		}
	}
	t.Fatalf("selected round %d has no candidate", result.Gate.SelectedRound)
	return ""
}
