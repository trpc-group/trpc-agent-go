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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type provenanceTamperingEvaluator struct {
	base   *LocalEvaluator
	mutate func(*EvaluationSummary)
}

func (e provenanceTamperingEvaluator) Evaluate(
	ctx context.Context,
	set *RegressionEvalSet,
	variantID string,
	prompt string,
) (*EvaluationSummary, error) {
	summary, err := e.base.Evaluate(ctx, set, variantID, prompt)
	if err == nil && e.mutate != nil {
		e.mutate(summary)
	}
	return summary, err
}

func (e provenanceTamperingEvaluator) RuntimeMode() RuntimeMode {
	return e.base.RuntimeMode()
}

func TestDeterministicPromptIterBuildsProfileAndPatchSet(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	config.Candidates = config.Candidates[:1]
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)

	baselinePrompt := "Answer with grounded facts."
	proposal, err := optimizer.Propose(context.Background(), OptimizeRequest{
		Round:          1,
		BaselinePrompt: baselinePrompt,
		Train: &EvaluationSummary{
			EvalSetID: "train",
			Cases: []CaseResult{
				{
					CaseID: "train-fix",
					FailureAttributions: []Attribution{
						{Category: FailureFinalResponseMismatch},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, proposal)

	expectedProposal := buildProposedPrompt(
		baselinePrompt,
		config.Candidates[0],
		[]FailureCategory{FailureFinalResponseMismatch},
	)
	assert.Equal(t, expectedProposal, proposal.Prompt)
	assert.Contains(t, proposal.Reason, string(FailureFinalResponseMismatch))

	pipeline := &Pipeline{config: config}
	candidate, err := pipeline.materializeCandidate(proposal, 1, baselinePrompt)
	require.NoError(t, err)
	expectedPrompt := expectedProposal + "\n\n[[trpc-promptiter-candidate:accepted;seed:424242]]"
	expectedSurfaceID := structure.SurfaceID("candidate", structure.SurfaceTypeInstruction)
	assert.Equal(t, "accepted", candidate.ID)
	assert.Equal(t, 1, candidate.Round)
	assert.Equal(t, expectedPrompt, candidate.Prompt)
	assert.Equal(t, HashText(expectedPrompt), candidate.PromptHash)
	assert.Equal(t, expectedSurfaceID, candidate.SurfaceID)

	var profile *promptiter.Profile = candidate.Profile
	require.NotNil(t, profile)
	assert.Equal(t, "structure-test", profile.StructureID)
	require.Len(t, profile.Overrides, 1)
	assert.Equal(t, expectedSurfaceID, profile.Overrides[0].SurfaceID)
	require.NotNil(t, profile.Overrides[0].Value.Text)
	assert.Equal(t, expectedPrompt, *profile.Overrides[0].Value.Text)

	var patchSet *promptiter.PatchSet = candidate.PatchSet
	require.NotNil(t, patchSet)
	require.Len(t, patchSet.Patches, 1)
	patch := patchSet.Patches[0]
	assert.Equal(t, expectedSurfaceID, patch.SurfaceID)
	require.NotNil(t, patch.Value.Text)
	assert.Equal(t, expectedPrompt, *patch.Value.Text)
	assert.Equal(t, candidate.Reason, patch.Reason)
}

func TestPipelineRejectsForgedCandidatePromptHash(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	config.Candidates = config.Candidates[:1]
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	proposal, err := optimizer.Propose(context.Background(), OptimizeRequest{
		Round:          1,
		BaselinePrompt: "baseline",
		Train: &EvaluationSummary{Cases: []CaseResult{{
			CaseID:              "failed",
			FailureAttributions: []Attribution{{Category: FailureFinalResponseMismatch}},
		}}},
	})
	require.NoError(t, err)
	pipeline := &Pipeline{config: config}
	candidate, err := pipeline.materializeCandidate(proposal, 1, "baseline")
	require.NoError(t, err)
	candidate.PromptHash = "forged"
	err = pipeline.validateCandidate(candidate, 1, map[string]struct{}{})
	require.ErrorContains(t, err, "does not match computed hash")
}

func TestNewPipelineRejectsEvaluatorRuntimeModeMismatch(t *testing.T) {
	config := newReportPipelineTestConfig()
	base, err := NewLocalEvaluator(
		[]MetricConfig{{MetricName: metricFinalResponse, Threshold: 1, Weight: 1}},
		"baseline",
		RuntimeModeTrace,
	)
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	_, err = NewPipeline(config, base, optimizer, time.Now)
	require.ErrorContains(t, err, "does not match config mode")
}

func TestPipelineIndependentlyRejectsTamperedPromptBinding(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	base, err := NewLocalEvaluator([]MetricConfig{{MetricName: metricFinalResponse, Threshold: 1, Weight: 1}}, "baseline")
	require.NoError(t, err)
	tampering := provenanceTamperingEvaluator{
		base: base,
		mutate: func(summary *EvaluationSummary) {
			if summary.VariantID == "baseline" {
				return
			}
			for index := range summary.Cases {
				summary.Cases[index].ResponsePromptSHA256 = strings.Repeat("0", 64)
			}
		},
	}
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline, err := NewPipeline(config, tampering, optimizer, time.Now)
	require.NoError(t, err)
	_, err = pipeline.Run(
		context.Background(),
		"Answer with grounded facts.",
		newReportPipelineTrainSet(),
		newReportPipelineValidationSet(),
	)
	require.ErrorContains(t, err, "does not match evaluated prompt")
}

func TestPipelineRequiresEveryCandidateValidationCaseToRerun(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	base, err := NewLocalEvaluator([]MetricConfig{{MetricName: metricFinalResponse, Threshold: 1, Weight: 1}}, "baseline")
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline, err := NewPipeline(config, base, optimizer, time.Now)
	require.NoError(t, err)
	validation := newReportPipelineValidationSet()
	delete(validation.Cases[0].FakeResponses, "accepted")
	_, err = pipeline.Run(
		context.Background(),
		"Answer with grounded facts.",
		newReportPipelineTrainSet(),
		validation,
	)
	require.ErrorContains(t, err, "candidate validation must rerun every case")
	require.ErrorContains(t, err, "validation-fix")
}

func TestPipelineRejectsCandidateWithExtraSurfaceFields(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	config.Candidates = config.Candidates[:1]
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	proposal, err := optimizer.Propose(context.Background(), OptimizeRequest{
		Round:          1,
		BaselinePrompt: "baseline",
		Train:          &EvaluationSummary{Cases: []CaseResult{{CaseID: "case"}}},
	})
	require.NoError(t, err)
	pipeline := &Pipeline{config: config}
	candidate, err := pipeline.materializeCandidate(proposal, 1, "baseline")
	require.NoError(t, err)
	syntax := structure.PromptSyntaxSingleBrace
	candidate.Profile.Overrides[0].Value.PromptSyntax = &syntax
	candidate.PatchSet.Patches[0].Value.PromptSyntax = &syntax

	err = pipeline.validateCandidate(candidate, 1, map[string]struct{}{})
	require.ErrorContains(t, err, "profile override does not match candidate prompt")
}

func TestPipelineRejectsWhitespaceOnlySemanticPromptChange(t *testing.T) {
	config := newReportPipelineTestConfig()
	config.MaxRounds = 1
	config.Candidates = config.Candidates[:1]
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
		HardFail:   true,
	}}, config.FakeEngine.FallbackVariant)
	require.NoError(t, err)
	optimizer := optimizerFunc(func(_ context.Context, request OptimizeRequest) (*PromptProposal, error) {
		return &PromptProposal{
			Prompt: strings.TrimSpace(request.BaselinePrompt),
			Reason: "whitespace-only semantic change",
		}, nil
	})
	pipeline, err := NewPipeline(config, evaluator, optimizer, time.Now)
	require.NoError(t, err)
	baselinePrompt := "  Answer with grounded facts. \n"
	train := newReportPipelineTrainSet()
	validation := newReportPipelineValidationSet()
	for index := range train.Cases {
		delete(train.Cases[index].FakeResponses, "overfit")
		output := train.Cases[index].FakeResponses["accepted"]
		output.PromptSemanticSHA256 = HashText(strings.TrimSpace(baselinePrompt))
		train.Cases[index].FakeResponses["accepted"] = output
	}
	for index := range validation.Cases {
		delete(validation.Cases[index].FakeResponses, "overfit")
		output := validation.Cases[index].FakeResponses["accepted"]
		output.PromptSemanticSHA256 = HashText(strings.TrimSpace(baselinePrompt))
		validation.Cases[index].FakeResponses["accepted"] = output
	}
	report, err := pipeline.Run(
		context.Background(),
		baselinePrompt,
		train,
		validation,
	)
	require.NoError(t, err)
	assert.False(t, report.GateDecision.Accepted)
	assert.False(t, gateCheckByName(t, report.GateDecision, "prompt_changed").Passed)
	assert.Equal(t, HashText(baselinePrompt), report.BaselinePrompt.SHA256)
	assert.Equal(t, HashText(strings.TrimSpace(baselinePrompt)), report.BaselinePrompt.SemanticSHA256)
}

func TestPipelineRejectsEmptyValidationSet(t *testing.T) {
	config := newReportPipelineTestConfig()
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, config.FakeEngine.FallbackVariant)
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline, err := NewPipeline(config, evaluator, optimizer, time.Now)
	require.NoError(t, err)
	_, err = pipeline.Run(
		context.Background(),
		"baseline",
		newReportPipelineTrainSet(),
		&RegressionEvalSet{EvalSet: evalset.EvalSet{EvalSetID: "empty-validation"}},
	)
	require.ErrorContains(t, err, "evalCases are empty")
}

func TestPipelineRejectsTrainValidationCaseOverlap(t *testing.T) {
	config := newReportPipelineTestConfig()
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, config.FakeEngine.FallbackVariant)
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline, err := NewPipeline(config, evaluator, optimizer, time.Now)
	require.NoError(t, err)
	train := newReportPipelineTrainSet()
	validation := newReportPipelineValidationSet()
	validation.Cases[0].EvalID = train.Cases[0].EvalID
	_, err = pipeline.Run(context.Background(), "Answer with grounded facts.", train, validation)
	require.ErrorContains(t, err, "appears in both train and validation")
}

func TestPipelineDoesNotMutateInputEvalSets(t *testing.T) {
	config := newReportPipelineTestConfig()
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
		HardFail:   true,
	}}, config.FakeEngine.FallbackVariant)
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)
	pipeline, err := NewPipeline(config, evaluator, optimizer, time.Now)
	require.NoError(t, err)
	train := newReportPipelineTrainSet()
	validation := newReportPipelineValidationSet()
	train.PassThreshold = nil
	validation.PassThreshold = nil
	_, err = pipeline.Run(context.Background(), "Answer with grounded facts.", train, validation)
	require.NoError(t, err)
	assert.Nil(t, train.PassThreshold)
	assert.Nil(t, validation.PassThreshold)
}

func TestPipelineSelectsAcceptedCandidateAndRejectsOverfit(t *testing.T) {
	report := runReportPipelineTest(t)

	assert.Equal(t, int64(424242), report.Seed)
	assert.Equal(t, "accepted", report.CandidatePrompt.ID)
	assert.True(t, report.GateDecision.Accepted)
	require.Len(t, report.Rounds, 2)
	assert.True(t, report.Rounds[0].GateDecision.Accepted)
	assert.False(t, report.Rounds[1].GateDecision.Accepted)

	acceptedRound := report.Rounds[0]
	overfitRound := report.Rounds[1]
	assert.Greater(t, acceptedRound.Delta.Train.ScoreDelta, 0.0)
	assert.Greater(t, acceptedRound.Delta.Validation.ScoreDelta, 0.0)
	assert.Greater(t, overfitRound.Delta.Train.ScoreDelta, 0.0)
	assert.Less(t, overfitRound.Delta.Validation.ScoreDelta, 0.0)
	assert.Equal(t, 1, overfitRound.Delta.Validation.NewFailures)
	assert.Equal(t, 1, overfitRound.Delta.Validation.NewHardFails)
	assert.False(t, gateCheckByName(t, overfitRound.GateDecision, "min_validation_score_gain").Passed)
	assert.False(t, gateCheckByName(t, overfitRound.GateDecision, "no_new_hard_failures").Passed)
	assert.False(t, gateCheckByName(t, overfitRound.GateDecision, "critical_cases_non_regression").Passed)

	assert.Equal(t, report.Rounds[0].Candidate, report.CandidatePrompt)
	assert.Equal(t, report.Rounds[0].Evaluation, report.Candidate)
	assert.Equal(t, report.Rounds[0].Delta, report.Delta)
	assert.Equal(t, 2, report.FailureAttributionStats[FailureFinalResponseMismatch])
}

func TestReportArtifactsContainRequiredFieldsAndUseAtomicWrites(t *testing.T) {
	report := runReportPipelineTest(t)
	assert.Equal(t, ReportSchemaVersion, report.SchemaVersion)

	jsonData, err := MarshalReportJSON(report)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(jsonData, &fields))
	for _, field := range []string{
		"schemaVersion",
		"runId",
		"mode",
		"seed",
		"startedAt",
		"completedAt",
		"wallTimeMs",
		"modelConfig",
		"baselinePrompt",
		"baseline",
		"candidatePrompt",
		"candidate",
		"delta",
		"gateDecision",
		"failureAttributionStats",
		"costLatencySummary",
		"rounds",
	} {
		assert.Contains(t, fields, field)
	}
	assert.Contains(t, string(jsonData), `"accepted": true`)
	assert.Contains(t, string(jsonData), `"seed": 424242`)
	assert.Contains(t, string(jsonData), `"newHardFails": 1`)

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	for _, required := range []string{
		"# PromptIter 优化回归报告",
		"接受候选 `accepted`",
		"模式 / 随机种子",
		"424242",
		"## 分数摘要",
		"## Train 逐 case 回归",
		"## Validation 逐 case 回归",
		"## 接受门禁",
		"## Baseline 失败归因统计",
		"## 成本与时延",
		"## 优化轮次审计",
		"轮次决策理由",
		"candidate introduced 1 hard failures",
		"ACCEPT",
		"REJECT",
	} {
		assert.Contains(t, markdown, required)
	}

	outputDir := t.TempDir()
	jsonPath := filepath.Join(outputDir, JSONReportName)
	markdownPath := filepath.Join(outputDir, MarkdownReportName)
	require.NoError(t, os.WriteFile(jsonPath, []byte("stale-json"), 0o600))
	require.NoError(t, os.WriteFile(markdownPath, []byte("stale-markdown"), 0o600))
	require.NoError(t, WriteReports(report, outputDir))

	writtenJSON, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	assert.Equal(t, jsonData, writtenJSON)
	writtenMarkdown, err := os.ReadFile(markdownPath)
	require.NoError(t, err)
	assert.Equal(t, markdown, string(writtenMarkdown))
	assertFileMode(t, jsonPath, 0o644)
	assertFileMode(t, markdownPath, 0o644)
	assertNoAtomicReportTemps(t, outputDir)
	assert.Equal(t, 12, report.CostLatencySummary.TotalRun.ModelCalls)

	pairFailureDir := t.TempDir()
	pairJSONPath := filepath.Join(pairFailureDir, JSONReportName)
	require.NoError(t, os.WriteFile(pairJSONPath, []byte("old-json"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(pairFailureDir, MarkdownReportName), 0o755))
	require.Error(t, WriteReports(report, pairFailureDir))
	unchangedJSON, err := os.ReadFile(pairJSONPath)
	require.NoError(t, err)
	assert.Equal(t, "old-json", string(unchangedJSON))
	assertNoAtomicReportTemps(t, pairFailureDir)

	badTarget := filepath.Join(outputDir, "existing-directory")
	require.NoError(t, os.Mkdir(badTarget, 0o755))
	assert.Error(t, writeAtomic(badTarget, []byte("cannot replace a directory")))
	assertNoAtomicReportTemps(t, outputDir)
}

func runReportPipelineTest(t *testing.T) *Report {
	t.Helper()
	config := newReportPipelineTestConfig()
	evaluator, err := NewLocalEvaluator([]MetricConfig{
		{
			MetricName: metricFinalResponse,
			Threshold:  1,
			Weight:     1,
			HardFail:   true,
		},
	}, config.FakeEngine.FallbackVariant)
	require.NoError(t, err)
	optimizer, err := NewDeterministicPromptIter(config)
	require.NoError(t, err)

	startedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	completedAt := startedAt.Add(2500 * time.Millisecond)
	timestamps := []time.Time{startedAt, completedAt}
	clockCalls := 0
	clock := func() time.Time {
		if clockCalls >= len(timestamps) {
			return timestamps[len(timestamps)-1]
		}
		value := timestamps[clockCalls]
		clockCalls++
		return value
	}
	pipeline, err := NewPipeline(config, evaluator, optimizer, clock)
	require.NoError(t, err)

	report, err := pipeline.Run(
		context.Background(),
		"Answer with grounded facts.",
		newReportPipelineTrainSet(),
		newReportPipelineValidationSet(),
	)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 2, clockCalls)
	assert.Equal(t, startedAt, report.StartedAt)
	assert.Equal(t, completedAt, report.CompletedAt)
	assert.Equal(t, int64(2500), report.WallTimeMS)
	assert.Equal(t, int64(424242), report.Seed)
	assert.Equal(t, "promptiter-regression-424242-"+HashText("Answer with grounded facts.")[:12], report.RunID)
	return report
}

func newReportPipelineTestConfig() Config {
	zero := 0
	return Config{
		SchemaVersion: ConfigSchemaVersion,
		Mode:          "fake",
		Seed:          424242,
		MaxRounds:     2,
		Surface: SurfaceConfig{
			StructureID: "structure-test",
			NodeID:      "candidate",
			Type:        string(structure.SurfaceTypeInstruction),
		},
		Candidates: []CandidateConfig{
			{
				ID:                "accepted",
				AppendPrompt:      "Include the required grounded answer.",
				Reason:            "repair known response failures",
				AddressCategories: []FailureCategory{FailureFinalResponseMismatch},
			},
			{
				ID:                "overfit",
				AppendPrompt:      "Memorize the training answers even when validation conflicts.",
				Reason:            "maximize training score",
				AddressCategories: []FailureCategory{FailureFinalResponseMismatch},
			},
		},
		Gate: GatePolicy{
			MinValidationScoreGain: 0.1,
			RejectNewHardFails:     true,
			MaxNewFailures:         &zero,
			CriticalCaseIDs:        []string{"validation-critical"},
			MaxCriticalScoreDrop:   0,
		},
		FakeEngine: FakeEngineConfig{
			Name:            "deterministic-test-engine",
			Version:         "1",
			FallbackVariant: "baseline",
		},
	}
}

func newReportPipelineTrainSet() *RegressionEvalSet {
	return &RegressionEvalSet{
		EvalSet:       evalset.EvalSet{EvalSetID: "train"},
		PassThreshold: testScore(1),
		Cases: []RegressionEvalCase{
			newReportPipelineCase(
				"train-fix",
				false,
				"train answer",
				map[string]string{
					"baseline": "wrong baseline answer",
					"accepted": "train answer",
					"overfit":  "train answer",
				},
			),
			newReportPipelineCase(
				"train-stable",
				false,
				"stable train answer",
				map[string]string{
					"baseline": "stable train answer",
					"accepted": "stable train answer",
					"overfit":  "stable train answer",
				},
			),
		},
	}
}

func newReportPipelineValidationSet() *RegressionEvalSet {
	return &RegressionEvalSet{
		EvalSet:       evalset.EvalSet{EvalSetID: "validation"},
		PassThreshold: testScore(1),
		Cases: []RegressionEvalCase{
			newReportPipelineCase(
				"validation-fix",
				false,
				"validation answer",
				map[string]string{
					"baseline": "wrong validation answer",
					"accepted": "validation answer",
					"overfit":  "overfit validation miss",
				},
			),
			newReportPipelineCase(
				"validation-critical",
				true,
				"critical validation answer",
				map[string]string{
					"baseline": "critical validation answer",
					"accepted": "critical validation answer",
					"overfit":  "training-only hallucination",
				},
			),
		},
	}
}

func newReportPipelineCase(
	caseID string,
	critical bool,
	expected string,
	responses map[string]string,
) RegressionEvalCase {
	finalResponse := model.NewAssistantMessage(expected)
	fakeResponses := make(map[string]FakeOutput, len(responses))
	for variant, response := range responses {
		output := FakeOutput{
			Response: response,
			Trace: []TraceStep{
				{
					StepID:  caseID + "-step",
					Kind:    "llm",
					Name:    "candidate",
					Status:  "completed",
					Message: response,
				},
			},
			Usage: Usage{
				ModelCalls:   1,
				InputTokens:  10,
				OutputTokens: 5,
				CostUSD:      0.01,
				LatencyMS:    10,
			},
		}
		if variant == "baseline" {
			output.PromptSemanticSHA256 = HashText("Answer with grounded facts.")
		} else {
			output.PromptSemanticSHA256 = reportPipelinePromptSemanticHash(variant)
		}
		fakeResponses[variant] = output
	}
	return RegressionEvalCase{
		EvalCase: evalset.EvalCase{
			EvalID: caseID,
			Conversation: []*evalset.Invocation{
				{
					InvocationID:  caseID + "-invocation",
					FinalResponse: &finalResponse,
				},
			},
		},
		Critical:      critical,
		FakeResponses: fakeResponses,
	}
}

func reportPipelinePromptSemanticHash(variant string) string {
	config := newReportPipelineTestConfig()
	for _, candidate := range config.Candidates {
		if candidate.ID != variant {
			continue
		}
		proposedPrompt := buildProposedPrompt(
			"Answer with grounded facts.",
			candidate,
			[]FailureCategory{FailureFinalResponseMismatch},
		)
		prompt := proposedPrompt + "\n\n" +
			"[[trpc-promptiter-candidate:" + candidate.ID + ";seed:424242]]"
		return HashText(semanticPromptContent(prompt))
	}
	return ""
}

func gateCheckByName(t *testing.T, decision GateDecision, name string) GateCheck {
	t.Helper()
	for _, check := range decision.Checks {
		if check.Name == name {
			return check
		}
	}
	require.FailNowf(t, "missing gate check", "gate check %q was not found", name)
	return GateCheck{}
}

func assertFileMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, expected, info.Mode().Perm())
}

type optimizerFunc func(context.Context, OptimizeRequest) (*PromptProposal, error)

func (function optimizerFunc) Propose(ctx context.Context, request OptimizeRequest) (*PromptProposal, error) {
	return function(ctx, request)
}

func assertNoAtomicReportTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".optimization-report-"), entry.Name())
	}
}
