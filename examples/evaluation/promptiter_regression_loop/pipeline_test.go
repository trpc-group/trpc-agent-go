//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	expectedCaseCount       = 3
	expectedRoundModelCalls = 10
	maximumJSONReportLines  = 1000
	testScoreTolerance      = 1e-9
)

func TestRunPipelineProducesExpectedRegressionMatrix(t *testing.T) {
	cfg, err := loadConfig(defaultConfigPath)
	require.NoError(t, err)
	cfg.OutputDir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	report, err := runPipeline(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, regression.RunStatusCompleted, report.Run.Status)
	assert.Empty(t, report.Run.Error)
	assert.Len(t, report.Run.ConfigSHA256, 64)
	assert.Equal(t, fakeEngineVersion, report.Run.FakeEngine)
	require.Len(t, report.Rounds, cfg.MaxAttempts)
	assert.InDelta(t, 2.0/3.0, report.BaselineTrain.OverallScore, testScoreTolerance)
	assert.InDelta(t, 5.0/6.0, report.BaselineValidation.OverallScore, testScoreTolerance)
	assert.Equal(t, 1, report.BaselineValidation.Usage.ToolCalls)
	var deliveryCase *regression.CaseResult
	for index := range report.BaselineValidation.Cases {
		if report.BaselineValidation.Cases[index].CaseID == "validation_delivery_trace" {
			deliveryCase = &report.BaselineValidation.Cases[index]
			break
		}
	}
	require.NotNil(t, deliveryCase)
	require.Len(t, deliveryCase.Metrics, 2)
	require.Len(t, deliveryCase.Trace.Steps, 1)
	assert.Equal(t, "lookup_shipment", deliveryCase.Trace.Steps[0].NodeID)
	assert.Equal(t, "tool", deliveryCase.Trace.Steps[0].NodeType)
	assertRound(t, report.Rounds[0], roundExpectation{
		attempt: 1, trainScore: 5.0 / 6.0, validationScore: 1.0, accepted: true,
	})
	assertRound(t, report.Rounds[1], roundExpectation{
		attempt: 2, trainScore: 5.0 / 6.0, validationScore: 1.0, accepted: false,
	})
	assertRound(t, report.Rounds[2], roundExpectation{
		attempt: 3, trainScore: 1.0, validationScore: 5.0 / 6.0, accepted: false,
	})
	assert.True(t, report.ShouldWriteBack)
	require.NotNil(t, report.WritebackProfile)
	assert.Equal(t, candidateOneInstruction, report.WritebackProfile.Text)
	assert.True(t, report.Decision.Accepted)
	assert.Equal(t, candidateOneInstruction, report.Candidate.Text)
	assert.Contains(t, report.Rounds[2].RegressionGateDecision.Reasons,
		"critical validation case regressed: validation_account_security")
}

func TestRunPipelineReportsAreSerializable(t *testing.T) {
	cfg, err := loadConfig(defaultConfigPath)
	require.NoError(t, err)
	cfg.OutputDir = t.TempDir()
	report, err := runPipeline(context.Background(), cfg)
	require.NoError(t, err)
	require.NoError(t, regression.WriteReports(cfg.OutputDir, report))
	for _, name := range []string{"optimization_report.json", "optimization_report.md"} {
		info, statErr := os.Stat(filepath.Join(cfg.OutputDir, name))
		require.NoError(t, statErr)
		assert.Positive(t, info.Size())
	}
	jsonData, err := os.ReadFile(filepath.Join(cfg.OutputDir, "optimization_report.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(jsonData), `"inputEvaluation"`)
	assert.NotContains(t, string(jsonData), `"acceptedDelta"`)
	assert.NotContains(t, string(jsonData), `"engineDecision"`)
	assert.NotContains(t, string(jsonData), `"stageCalls"`)
	assert.NotContains(t, string(jsonData), fakeResponseIDPrefix)
	assert.Contains(t, string(jsonData), `"durationNanos"`)
	assert.NotContains(t, string(jsonData), `"duration":`)
	assert.LessOrEqual(t, bytes.Count(jsonData, []byte{'\n'})+1, maximumJSONReportLines)
}

func TestApplyRoundAuditsNotEvaluatedCandidate(t *testing.T) {
	baselineTrain := engineEvaluationResult("train", "train_case", status.EvalStatusPassed)
	baselineValidation := engineEvaluationResult("validation", "case", status.EvalStatusPassed)
	candidateValidation := engineEvaluationResult("validation", "case", status.EvalStatusNotEvaluated)
	candidateTrain, err := regression.NormalizeEvaluation(baselineTrain)
	require.NoError(t, err)
	profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "candidate#instruction", Value: promptValue(profileCandidateOne),
	}}}
	round := promptiterengine.RoundResult{
		Train: baselineTrain, Validation: candidateValidation, OutputProfile: profile,
		Acceptance: &promptiterengine.AcceptanceDecision{Accepted: false, Reason: "not evaluated"},
	}
	execution := &roundExecution{
		attempt: 1, result: &promptiterengine.RunResult{
			BaselineValidation: baselineValidation, Rounds: []promptiterengine.RoundResult{round},
		},
		candidateTrain: candidateTrain,
	}
	cfg := validTestConfig()
	state := &pipelineState{
		originalValidation: mustNormalizeEvaluation(t, baselineValidation),
		acceptedValidation: mustNormalizeEvaluation(t, baselineValidation),
		baselinePrompt:     regression.PromptRecord{SurfaceID: cfg.TargetSurfaceID, Text: profileBaseline},
		acceptedPrompt:     regression.PromptRecord{SurfaceID: cfg.TargetSurfaceID, Text: profileBaseline},
		catalog:            regression.AttributionCatalog{},
	}
	state.report, err = regression.NewReport(regression.RunMetadata{}, candidateTrain,
		state.originalValidation, regression.AttributionResult{})
	require.NoError(t, err)

	require.NoError(t, applyRound(cfg, state, execution))
	require.Len(t, state.report.Rounds, 1)
	audited := state.report.Rounds[0]
	assert.False(t, audited.RegressionGateDecision.Accepted)
	assert.Contains(t, audited.RegressionGateDecision.Reasons,
		"candidate validation metric is not evaluated: case/quality")
	require.Len(t, audited.Attribution.Items, 1)
	assert.Equal(t, "case", audited.Attribution.Items[0].CaseID)
}

func TestConfiguredEntryPointRunsAndHandlesErrors(t *testing.T) {
	cfg, err := loadConfig(defaultConfigPath)
	require.NoError(t, err)
	cfg.OutputDir = t.TempDir()
	require.NoError(t, executeConfig(cfg))
	_, err = os.Stat(filepath.Join(cfg.OutputDir, "optimization_report.json"))
	require.NoError(t, err)
	assert.Error(t, executeConfig(nil))
	assert.Error(t, runConfigured(filepath.Join(t.TempDir(), "missing.json")))
}

func TestRunParsesFlagsAndReturnsConfigError(t *testing.T) {
	originalArgs := os.Args
	originalFlags := flag.CommandLine
	t.Cleanup(func() {
		os.Args = originalArgs
		flag.CommandLine = originalFlags
	})
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"promptiter-regression-loop", "-config", filepath.Join(t.TempDir(), "missing.json")}
	assert.Error(t, run())
}

func TestConfigValidationAndDuration(t *testing.T) {
	var duration durationValue
	require.NoError(t, json.Unmarshal([]byte(`"3s"`), &duration))
	assert.Equal(t, 3*time.Second, time.Duration(duration))
	assert.Error(t, json.Unmarshal([]byte(`"bad"`), &duration))
	assert.Error(t, json.Unmarshal([]byte(`1`), &duration))
	assert.Error(t, validateConfig(nil))
	cfg := validTestConfig()
	cfg.MaxAttempts = 0
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.CandidatePrompts = nil
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.CandidatePrompts[0] = " "
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.AppName = ""
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.AppName = " \t"
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.TargetSurfaceID = ""
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.Timeout = 0
	assert.Error(t, validateConfig(cfg))
	cfg = validTestConfig()
	cfg.MaxAttempts = 2
	assert.Error(t, validateConfig(cfg))
}

func TestLoadConfigAndPromptErrors(t *testing.T) {
	assertErrorCases(t, []func() error{
		func() error { _, err := loadConfig(""); return err },
		func() error { _, err := loadConfig(filepath.Join(t.TempDir(), "missing.json")); return err },
		func() error { _, err := loadBaselinePrompt(filepath.Join(t.TempDir(), "missing.txt")); return err },
	})
	emptyPath := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(emptyPath, []byte(" \n"), 0o600))
	_, err := loadBaselinePrompt(emptyPath)
	assert.Error(t, err)
	invalidConfig := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalidConfig, []byte("not-json"), 0o600))
	_, err = loadConfig(invalidConfig)
	assert.Error(t, err)
	unknownConfig := filepath.Join(t.TempDir(), "unknown.json")
	require.NoError(t, os.WriteFile(unknownConfig, []byte(`{"unexpected":true}`), 0o600))
	_, err = loadConfig(unknownConfig)
	assert.ErrorContains(t, err, "unknown field")
	trailingConfig := filepath.Join(t.TempDir(), "trailing.json")
	require.NoError(t, os.WriteFile(trailingConfig, []byte(`{} {}`), 0o600))
	_, err = loadConfig(trailingConfig)
	assert.ErrorContains(t, err, "multiple JSON values")
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "promptiter.json")
	data := []byte(`{
		"appName":"app","trainEvalSetID":"train","validationEvalSetID":"validation",
		"maxAttempts":1,"targetSurfaceID":"candidate#instruction",
		"candidatePrompts":["prompt"],"outputDir":"output"
	}`)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, defaultTimeout, time.Duration(cfg.Timeout))
	assert.Equal(t, filepath.ToSlash(filepath.Clean(configPath)), cfg.ConfigPath)
	assert.Equal(t, filepath.Dir(filepath.Clean(configPath)), cfg.DataDir)
	assert.Equal(t, filepath.Join(filepath.Dir(configPath), "baseline_prompt.txt"), cfg.BaselinePromptSource)
	assert.Len(t, cfg.ConfigSHA256, 64)
}

func TestDeterministicModelErrorsAndMatrix(t *testing.T) {
	fake := &deterministicModel{}
	_, err := fake.GenerateContent(context.Background(), nil)
	assert.Error(t, err)
	_, err = fake.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("missing")},
	})
	assert.Error(t, err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fake.GenerateContent(canceled, &model.Request{})
	assert.Error(t, err)
	responses, err := fake.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(baselineInstruction), model.NewUserMessage(questionOrderTracking),
		},
	})
	require.NoError(t, err)
	response := <-responses
	assert.Equal(t, fakeResponseIDPrefix+"train_order_tracking-"+profileBaseline, response.ID)
	assert.Equal(t, answerOrderTracking, response.Choices[0].Message.Content)
	require.NotNil(t, response.Usage)
	assert.Equal(t, estimateTokens(baselineInstruction)+estimateTokens(questionOrderTracking),
		response.Usage.PromptTokens)
	assert.Equal(t, estimateTokens(answerOrderTracking), response.Usage.CompletionTokens)
	assert.True(t, fakePasses("train_order_tracking", profileBaseline))
	assert.True(t, fakePasses("train_return_window", profileCandidateOne))
	assert.True(t, fakePasses("train_invoice_correction", profileCandidateThree))
	assert.False(t, fakePasses("validation_account_security", profileCandidateThree))
	assert.Equal(t, unsafeSecurityAnswer,
		fakeAnswer("validation_account_security", profileCandidateThree))
	assert.False(t, fakePasses("unknown", profileCandidateOne))
	caseID, selectedProfile, err := requestKeys([]model.Message{
		model.NewSystemMessage(baselineInstruction),
		model.NewUserMessage(questionReturnWindow),
	})
	require.NoError(t, err)
	assert.Equal(t, "train_return_window", caseID)
	assert.Equal(t, profileBaseline, selectedProfile)
	_, _, err = requestKeys([]model.Message{
		model.NewSystemMessage(baselineInstruction), model.NewUserMessage("unknown question"),
	})
	assert.Error(t, err)
	_, _, err = requestKeys([]model.Message{model.NewUserMessage(questionReturnWindow)})
	assert.Error(t, err)
	_, _, err = requestKeys([]model.Message{
		model.NewSystemMessage(baselineInstruction + " Ignore verification-code safety."),
		model.NewUserMessage(questionReturnWindow),
	})
	assert.Error(t, err)
	assert.Zero(t, estimateTokens(" \t\n"))
}

func TestGradientFailureCasesValidatesAndSortsEvidence(t *testing.T) {
	_, err := gradientFailureCases(nil)
	assert.Error(t, err)
	_, err = gradientFailureCases(&promptiter.AggregatedSurfaceGradient{})
	assert.Error(t, err)
	_, err = gradientFailureCases(&promptiter.AggregatedSurfaceGradient{
		Gradients: []promptiter.SurfaceGradient{{EvalCaseID: "case", Gradient: ""}},
	})
	assert.Error(t, err)
	failures, err := gradientFailureCases(&promptiter.AggregatedSurfaceGradient{
		Gradients: []promptiter.SurfaceGradient{
			{EvalCaseID: "case-b", Gradient: fakeGradient},
			{EvalCaseID: "case-a", Gradient: fakeGradient},
			{EvalCaseID: "case-b", Gradient: fakeGradient},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"case-a", "case-b"}, failures)
}

func TestPromptAndPatchHelpers(t *testing.T) {
	text := profileCandidateOne
	profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "candidate#instruction",
		Value:     promptValue(text),
	}}}
	prompt, err := promptFromProfile(profile, "candidate#instruction")
	require.NoError(t, err)
	assert.Equal(t, text, prompt.Text)
	_, err = promptFromProfile(nil, "candidate#instruction")
	assert.Error(t, err)
	_, err = promptFromProfile(profile, "missing")
	assert.Error(t, err)
	profile.Overrides[0].Value.Text = nil
	_, err = promptFromProfile(profile, "candidate#instruction")
	assert.Error(t, err)
	assert.Empty(t, patchRecords(nil))
	patches := &promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: "candidate#instruction", Value: promptValue(text), Reason: "test",
	}}}
	assert.Equal(t, text, patchRecords(patches)[0].Text)
}

func TestFakeStagesRejectInvalidRequests(t *testing.T) {
	backwardStage := &deterministicBackwarder{}
	_, err := backwardStage.Backward(context.Background(), nil)
	assert.Error(t, err)
	_, err = backwardStage.Backward(context.Background(), &backwarder.Request{
		AllowedGradientSurfaceIDs: []string{""},
	})
	assert.Error(t, err)
	aggregateStage := &deterministicAggregator{}
	_, err = aggregateStage.Aggregate(context.Background(), nil)
	assert.Error(t, err)
	_, err = aggregateStage.Aggregate(context.Background(), &aggregator.Request{})
	assert.Error(t, err)
	optimizeStage := &deterministicOptimizer{}
	_, err = optimizeStage.Optimize(context.Background(), nil)
	assert.Error(t, err)
	_, err = optimizeStage.Optimize(context.Background(), &optimizer.Request{
		Surface:  &astructure.Surface{Type: astructure.SurfaceTypeTool},
		Gradient: &promptiter.AggregatedSurfaceGradient{},
	})
	assert.Error(t, err)
}

func TestCanceledFakeStages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&deterministicBackwarder{}).Backward(ctx, &backwarder.Request{})
	assert.Error(t, err)
	_, err = (&deterministicAggregator{}).Aggregate(ctx, &aggregator.Request{})
	assert.Error(t, err)
	_, err = (&deterministicOptimizer{}).Optimize(ctx, &optimizer.Request{})
	assert.Error(t, err)
}

func TestPipelineErrorHelpers(t *testing.T) {
	_, err := runPipeline(nil, validTestConfig())
	assert.ErrorContains(t, err, "context is nil")
	assert.NoError(t, (*promptIterRuntime)(nil).Close())
	_, err = (*promptIterRuntime)(nil).engineForAttempt(context.Background(), "prompt", 1)
	assert.Error(t, err)
	missing := filepath.Join(t.TempDir(), "metrics.json")
	_, err = loadAttributionCatalog(missing)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(missing, []byte("not-json"), 0o600))
	_, err = loadAttributionCatalog(missing)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(missing, []byte("[]"), 0o600))
	_, err = loadAttributionCatalog(missing)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(missing, []byte("[null]"), 0o600))
	_, err = loadAttributionCatalog(missing)
	assert.ErrorContains(t, err, "is nil")
	_, err = compileProfileOptions(nil, &promptiter.Profile{})
	assert.Error(t, err)
}

func TestCompileProfileOptionsAppliesInstructionPatch(t *testing.T) {
	snapshot := testInstructionSnapshot("baseline")
	compiled, err := compileProfileOptions(snapshot, nil)
	require.NoError(t, err)
	require.Len(t, compiled, 1)
	assert.True(t, agent.NewRunOptions(compiled...).ExecutionTraceEnabled)

	compiled, err = compileProfileOptions(snapshot, &promptiter.Profile{
		StructureID: snapshot.StructureID,
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: snapshot.Surfaces[0].SurfaceID,
			Value:     promptValue("candidate"),
		}},
	})
	require.NoError(t, err)
	require.Len(t, compiled, 2)
	assert.NotNil(t, agent.NewRunOptions(compiled...).CustomAgentConfigs)

	compiled, err = compileProfileOptions(snapshot, &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: snapshot.Surfaces[0].SurfaceID, Value: promptValue("baseline"),
	}}})
	require.NoError(t, err)
	assert.Len(t, compiled, 1)
}

func TestCompileProfileOptionsRejectsInvalidStructureAndProfile(t *testing.T) {
	snapshot := testInstructionSnapshot("baseline")
	duplicate := *snapshot
	duplicate.Surfaces = append(append([]astructure.Surface(nil), snapshot.Surfaces...), snapshot.Surfaces[0])
	tests := []struct {
		name     string
		snapshot *astructure.Snapshot
		profile  *promptiter.Profile
	}{
		{name: "nil snapshot", profile: &promptiter.Profile{}},
		{name: "empty structure id", snapshot: &astructure.Snapshot{}, profile: &promptiter.Profile{}},
		{name: "duplicate surface", snapshot: &duplicate, profile: &promptiter.Profile{}},
		{name: "structure mismatch", snapshot: snapshot, profile: &promptiter.Profile{StructureID: "other"}},
		{name: "empty override", snapshot: snapshot, profile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{}}}},
		{name: "unknown override", snapshot: snapshot, profile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "unknown"}}}},
		{name: "duplicate override", snapshot: snapshot, profile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
			{SurfaceID: snapshot.Surfaces[0].SurfaceID, Value: promptValue("one")},
			{SurfaceID: snapshot.Surfaces[0].SurfaceID, Value: promptValue("two")},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileProfileOptions(test.snapshot, test.profile)
			assert.Error(t, err)
		})
	}
}

func TestCompileProfileOptionsRejectsInvalidInstructionValues(t *testing.T) {
	snapshot := testInstructionSnapshot("baseline")
	syntax := astructure.PromptSyntaxSingleBrace
	values := []astructure.SurfaceValue{
		{},
		{Text: stringPointer("candidate"), PromptSyntax: &syntax},
		{Text: stringPointer("candidate"), FewShot: []astructure.FewShotExample{{}}},
		{Text: stringPointer("candidate"), Model: &astructure.ModelRef{Name: "model"}},
		{Text: stringPointer("candidate"), Tools: []astructure.ToolRef{{ID: "tool"}}},
		{Text: stringPointer("candidate"), Skills: []astructure.SkillRef{{ID: "skill"}}},
	}
	for index, value := range values {
		_, err := compileProfileOptions(snapshot, &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: snapshot.Surfaces[0].SurfaceID, Value: value,
		}}})
		assert.Error(t, err, "invalid value %d", index)
	}

	nonInstruction := testInstructionSnapshot("baseline")
	nonInstruction.Surfaces[0].Type = astructure.SurfaceTypeGlobalInstruction
	_, err := compileProfileOptions(nonInstruction, &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: nonInstruction.Surfaces[0].SurfaceID, Value: promptValue("candidate"),
	}}})
	assert.Error(t, err)
}

func testInstructionSnapshot(text string) *astructure.Snapshot {
	return &astructure.Snapshot{
		StructureID: "structure", EntryNodeID: candidateAgentName,
		Nodes: []astructure.Node{{NodeID: candidateAgentName, Kind: astructure.NodeKindLLM}},
		Surfaces: []astructure.Surface{{
			SurfaceID: astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction),
			NodeID:    candidateAgentName, Type: astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{Text: stringPointer(text)},
		}},
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestBuildRoundReportRejectsIncompleteArtifacts(t *testing.T) {
	state := &pipelineState{}
	execution := &roundExecution{}
	_, err := buildRoundReport(state, execution, normalizedRound{})
	assert.Error(t, err)
	valid, err := regression.NormalizeEvaluation(
		engineEvaluationResult("validation", "case", status.EvalStatusPassed),
	)
	require.NoError(t, err)
	artifacts := normalizedRound{inputTrain: valid, baselineValidation: valid}
	_, err = buildRoundReport(state, execution, artifacts)
	assert.Error(t, err)
}

func TestFailPipelineFinalizesReport(t *testing.T) {
	baseline := mustNormalizeEvaluation(t,
		engineEvaluationResult("validation", "case", status.EvalStatusPassed))
	report, err := regression.NewReport(regression.RunMetadata{}, baseline, baseline,
		regression.AttributionResult{})
	require.NoError(t, err)
	prompt := regression.PromptRecord{SurfaceID: "candidate#instruction", Text: profileBaseline}
	state := &pipelineState{
		report: report, originalValidation: baseline,
		acceptedValidation: baseline, baselinePrompt: prompt, acceptedPrompt: prompt,
	}
	result, resultErr := failPipeline(state, time.Now().Add(-time.Second),
		context.DeadlineExceeded)
	require.ErrorIs(t, resultErr, context.DeadlineExceeded)
	assert.Empty(t, result.Rounds)
	assert.Positive(t, result.Run.Duration)
	assert.Equal(t, regression.RunStatusFailed, result.Run.Status)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Run.Error)
	assert.False(t, result.ShouldWriteBack)
}

func TestFailedPipelineDisablesPreviouslyAcceptedWriteback(t *testing.T) {
	baseline := mustNormalizeEvaluation(t,
		engineEvaluationResult("validation", "case", status.EvalStatusPassed))
	report, err := regression.NewReport(regression.RunMetadata{}, baseline, baseline,
		regression.AttributionResult{})
	require.NoError(t, err)
	report.Candidate = &regression.PromptRecord{SurfaceID: "candidate#instruction", Text: candidateOneInstruction}
	report.Decision = regression.GateDecision{Accepted: true, Reasons: []string{"accepted earlier"}}
	state := &pipelineState{
		report: report, originalValidation: baseline, acceptedValidation: baseline,
		baselinePrompt: regression.PromptRecord{SurfaceID: "candidate#instruction", Text: baselineInstruction},
		acceptedPrompt: regression.PromptRecord{SurfaceID: "candidate#instruction", Text: candidateOneInstruction},
	}

	result, resultErr := failPipeline(state, time.Now().Add(-time.Second), context.DeadlineExceeded)
	require.ErrorIs(t, resultErr, context.DeadlineExceeded)
	assert.Equal(t, regression.RunStatusFailed, result.Run.Status)
	assert.False(t, result.ShouldWriteBack)
	assert.Nil(t, result.WritebackProfile)
	assert.False(t, result.Decision.Accepted)
	assert.Contains(t, result.Decision.Reasons, "pipeline failed; writeback disabled")
}

func TestCleanupFailureDisablesWriteback(t *testing.T) {
	baseline := mustNormalizeEvaluation(t,
		engineEvaluationResult("validation", "case", status.EvalStatusPassed))
	report, err := regression.NewReport(regression.RunMetadata{}, baseline, baseline,
		regression.AttributionResult{})
	require.NoError(t, err)
	require.NoError(t, regression.SetWriteback(report,
		regression.PromptRecord{SurfaceID: "instruction", Text: baselineInstruction},
		regression.PromptRecord{SurfaceID: "instruction", Text: candidateOneInstruction},
	))
	require.True(t, report.ShouldWriteBack)

	markReportFailed(report, errors.New("close failed"))
	assert.Equal(t, regression.RunStatusFailed, report.Run.Status)
	assert.Contains(t, report.Run.Error, "close failed")
	assert.False(t, report.ShouldWriteBack)
	assert.Nil(t, report.WritebackProfile)
	assert.False(t, report.Decision.Accepted)
}

type roundExpectation struct {
	attempt         int
	trainScore      float64
	validationScore float64
	accepted        bool
}

func assertRound(t *testing.T, round regression.RoundReport, expected roundExpectation) {
	t.Helper()
	assert.Equal(t, expected.attempt, round.Attempt)
	assert.InDelta(t, expected.trainScore, round.Train.OverallScore, testScoreTolerance)
	assert.InDelta(t, expected.validationScore, round.Validation.OverallScore, testScoreTolerance)
	assert.Equal(t, expected.accepted, round.RegressionGateDecision.Accepted)
	assert.Len(t, round.Validation.Cases, expectedCaseCount)
	assert.Positive(t, round.Usage.TotalTokens)
	assert.Equal(t, expectedRoundModelCalls, round.Usage.ModelCalls)
	require.NotNil(t, round.Delta)
	assert.Len(t, round.Delta.Cases, expectedCaseCount)
}

func validTestConfig() *config {
	return &config{
		AppName: "app", TrainEvalSetID: "train", ValidationEvalSetID: "validation",
		Timeout: durationValue(time.Second), MaxAttempts: 1,
		TargetSurfaceID: "candidate#instruction", CandidatePrompts: []string{"prompt"},
		OutputDir: "output", BaselinePromptSource: "baseline.txt",
	}
}

func assertErrorCases(t *testing.T, cases []func() error) {
	t.Helper()
	for _, testCase := range cases {
		assert.Error(t, testCase())
	}
}

func promptValue(text string) astructure.SurfaceValue {
	return astructure.SurfaceValue{Text: &text}
}

func engineEvaluationResult(
	evalSetID string,
	caseID string,
	evalStatus status.EvalStatus,
) *promptiterengine.EvaluationResult {
	score := 0.0
	if evalStatus == status.EvalStatusPassed {
		score = 1
	}
	return &promptiterengine.EvaluationResult{
		OverallScore: score,
		EvalSets: []promptiterengine.EvalSetResult{{
			EvalSetID: evalSetID, OverallScore: score,
			Cases: []promptiterengine.CaseResult{{
				EvalSetID: evalSetID, EvalCaseID: caseID,
				Metrics: []promptiterengine.MetricResult{{
					MetricName: "quality", Score: score, Status: evalStatus,
				}},
				Trace: &atrace.Trace{Status: atrace.TraceStatusCompleted},
			}},
		}},
	}
}

func mustNormalizeEvaluation(
	t *testing.T,
	input *promptiterengine.EvaluationResult,
) *regression.EvaluationResult {
	t.Helper()
	result, err := regression.NormalizeEvaluation(input)
	require.NoError(t, err)
	return result
}
