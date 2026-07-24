//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestReportJSONAndMarkdownShareCanonicalIdentity(t *testing.T) {
	report := testReport(t)
	jsonData, err := RenderJSON(report)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(jsonData, &decoded))
	require.Equal(t, report.SchemaVersion, decoded.SchemaVersion)
	require.Equal(t, report.ReportID, decoded.ReportID)
	require.Equal(t, report.RunID, decoded.RunID)
	require.Equal(t, report.StopReason, decoded.StopReason)
	require.Len(t, decoded.Candidates, len(report.Candidates))

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	text := string(markdown)
	require.Contains(t, text, report.ReportID)
	require.Contains(t, text, report.RunID)
	require.Contains(t, text, string(report.StopReason))
	require.Contains(t, text, "Search: REJECTED")
	require.Contains(t, text, "Release: ACCEPTED")
	require.Contains(t, text, "vs_initial")
	require.Contains(t, text, report.ReleasedProfile.Prompt)
	require.Contains(t, text, "| quality | 0.800000 |")
	require.Contains(t, text, "Observed tools:")
	require.Contains(t, text, "Trace:")
}

func TestRenderMarkdownUsesDynamicFence(t *testing.T) {
	report := testReport(t)
	report.BaselineTrain.Cases[0].FinalResponse = "Explain this:\n```json\n{}\n```"
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "````")
}

func TestRedactAndBoundEvidence(t *testing.T) {
	value := `{"api_key":"secret","authorization":"Bearer private","query":"` + string(make([]byte, 256)) + `"}`
	got := redactAndBoundText(value, 96)
	require.NotContains(t, got, "secret")
	require.NotContains(t, got, "private")
	require.LessOrEqual(t, len(got), 99)
	require.Contains(t, got, "[REDACTED]")
}

func TestRenderJSONRedactsAndBoundsPersistedEvidence(t *testing.T) {
	report := testReport(t)
	report.BaselineTrain.Cases[0].FinalResponse =
		strings.Repeat("x", defaultMarkdownTextLimit+128) + " api_key=secret"
	report.BaselineTrain.Cases[0].ToolTrajectory[0].Arguments = map[string]any{
		"authorization": "Bearer private",
		"payload":       strings.Repeat("p", defaultMarkdownTextLimit+128),
	}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(data, &decoded))
	result := decoded.BaselineTrain.Cases[0]
	require.LessOrEqual(t, len(result.FinalResponse), defaultMarkdownTextLimit+3)
	arguments, ok := result.ToolTrajectory[0].Arguments.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", arguments["authorization"])
	require.LessOrEqual(t, len(arguments["payload"].(string)), defaultMarkdownTextLimit+3)
	require.NotContains(t, string(data), "private")
	require.NotContains(t, string(data), "secret")
	require.Equal(t, report.InitialProfile.Hash, decoded.InitialProfile.Hash)
}

func TestRenderRedactsCredentialContainersPluralsAndSuffixes(t *testing.T) {
	report := testReport(t)
	report.Runtime.Model = map[string]any{
		"apiKeys": map[string]any{
			"primary": "api-primary-secret",
			"backup":  "api-backup-secret",
		},
		"openai_api_key": "openai-secret",
		"credentials": map[string]any{
			"username": "credential-user-secret",
			"region":   "credential-region-secret",
		},
		"nested": map[string]any{
			"secrets": []any{"nested-secret-one", "nested-secret-two"},
			"tokens":  map[string]any{"access": "nested-token-secret"},
		},
		"github_token":        "github-secret",
		"serviceClientSecret": "client-secret",
		"maxTokens":           4096,
	}
	report.BaselineTrain.Cases[0].FinalResponse =
		`{"credentials":{"username":"response-user-secret","region":"response-region-secret"},"safe":"visible"}`
	rebindTestReportProvenance(t, report)

	jsonData, err := RenderJSON(report)
	require.NoError(t, err)
	markdownData, err := RenderMarkdown(report)
	require.NoError(t, err)
	for _, rendered := range []string{string(jsonData), string(markdownData)} {
		for _, secret := range []string{
			"api-primary-secret",
			"api-backup-secret",
			"openai-secret",
			"credential-user-secret",
			"credential-region-secret",
			"nested-secret-one",
			"nested-secret-two",
			"nested-token-secret",
			"github-secret",
			"client-secret",
			"response-user-secret",
			"response-region-secret",
		} {
			require.NotContains(t, rendered, secret)
		}
		require.Contains(t, rendered, "[REDACTED]")
		require.Contains(t, rendered, "visible")
	}

	var decoded Report
	require.NoError(t, json.Unmarshal(jsonData, &decoded))
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["apiKeys"])
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["openai_api_key"])
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["credentials"])
	require.Equal(t, float64(4096), decoded.Runtime.Model["maxTokens"])
	require.Contains(
		t,
		decoded.BaselineTrain.Cases[0].FinalResponse,
		`"credentials":"[REDACTED]"`,
	)
}

func TestRenderJSONBoundsEvidenceCollectionsAndNestedArguments(t *testing.T) {
	report := testReport(t)
	report.ResolvedConfig.EvidenceLimit = 2
	tool := report.BaselineTrain.Cases[0].ToolTrajectory[0]
	tool.Arguments = map[string]any{
		"a_items":  []any{"one", "two", "three", "four"},
		"b_nested": deeplyNestedValue(32),
		"c":        1,
		"d":        2,
		"e":        3,
	}
	report.BaselineTrain.Cases[0].ToolTrajectory = []ToolCall{tool, tool, tool, tool}
	report.BaselineTrain.Attributions = []FailureAttribution{{
		EvalCaseID: "train-case",
		MetricName: "quality",
		Evidence: []EvidenceReference{
			{ID: "one", Kind: "final_response", Summary: "one"},
			{ID: "two", Kind: "trace", Summary: "two"},
			{ID: "three", Kind: "tool", Summary: "three"},
		},
	}}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(data, &decoded))
	result := decoded.BaselineTrain.Cases[0]
	require.Len(t, result.ToolTrajectory, 2)
	require.Len(t, decoded.BaselineTrain.Attributions[0].Evidence, 2)
	arguments, ok := result.ToolTrajectory[0].Arguments.(map[string]any)
	require.True(t, ok)
	require.Len(t, arguments["a_items"], 2)
	require.Contains(t, arguments, "__report_truncated__")
	require.Contains(t, string(data), "[TRUNCATED: maximum nesting depth]")
}

func TestRenderJSONDoesNotTruncateCoreCollectionsAt1024(t *testing.T) {
	const collectionSize = 1025
	report := testReport(t)
	// A failed report can legitimately contain partial snapshots. Use one here
	// so this test isolates persistence loss from successful-run completeness.
	report.Status = PipelineRunFailed
	report.StopReason = StopNecessaryRunFailed
	report.Candidates = make([]CandidateReport, collectionSize)
	for i := range report.Candidates {
		report.Candidates[i] = CandidateReport{
			Round:  i + 1,
			ID:     fmt.Sprintf("candidate-%04d", i),
			Status: EvaluationNotEvaluable,
			SearchDecision: Decision{
				Status:  DecisionNotEvaluable,
				Reasons: []string{"not evaluated"},
			},
			ReleaseDecision: Decision{
				Status:  DecisionNotEvaluable,
				Reasons: []string{"not evaluated"},
			},
		}
	}
	report.BaselineTrain.Inventory.CaseIDs = make([]string, collectionSize)
	report.BaselineTrain.Inventory.MetricNames = make([]string, collectionSize)
	report.BaselineTrain.Cases = make([]CaseResult, collectionSize)
	for i := 0; i < collectionSize; i++ {
		report.BaselineTrain.Inventory.CaseIDs[i] = fmt.Sprintf("case-%04d", i)
		report.BaselineTrain.Inventory.MetricNames[i] = fmt.Sprintf("metric-%04d", i)
		report.BaselineTrain.Cases[i] = CaseResult{
			EvalSetID: "train-set",
			CaseID:    fmt.Sprintf("case-%04d", i),
			Metrics: []MetricResult{{
				MetricName: "quality",
			}},
		}
	}
	report.BaselineTrain.Cases[0].Metrics = make([]MetricResult, collectionSize)
	for i := range report.BaselineTrain.Cases[0].Metrics {
		report.BaselineTrain.Cases[0].Metrics[i].MetricName = fmt.Sprintf("metric-%04d", i)
	}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Candidates, collectionSize)
	require.Len(t, decoded.BaselineTrain.Cases, collectionSize)
	require.Len(t, decoded.BaselineTrain.Cases[0].Metrics, collectionSize)
	require.Len(t, decoded.BaselineTrain.Inventory.CaseIDs, collectionSize)
	require.Len(t, decoded.BaselineTrain.Inventory.MetricNames, collectionSize)
	require.NotContains(t, string(data), "__report_truncated__")
}

func TestRenderedProfileHashRetainsPreSanitizationEvaluationIdentity(t *testing.T) {
	report := testReport(t)
	profile := testProfileRecord(t, ProfileCandidate, "Use api_key=secret for this candidate")
	replaceTestCandidateProfile(t, report, profile)

	data, err := RenderJSON(report)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(data, &decoded))
	rendered := decoded.Candidates[0].Profile
	require.Equal(t, profile.Hash, rendered.Hash)
	require.NotContains(t, rendered.Prompt, "secret")
	sanitizedPayloadHash, err := ProfileFingerprint(rendered.Profile)
	require.NoError(t, err)
	require.NotEqual(t, rendered.Hash, sanitizedPayloadHash)

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "before report redaction and text bounding")
	require.Contains(t, string(markdown), "not hash-reconstructive")
}

func TestWriteArtifactsValidatesPathsAndPublishesCanonicalJSONLast(t *testing.T) {
	report := testReport(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "nested", "optimization_report.json")
	markdownPath := filepath.Join(dir, "nested", "optimization_report.md")
	require.NoError(t, WriteArtifacts(report, jsonPath, markdownPath))
	require.FileExists(t, jsonPath)
	require.FileExists(t, markdownPath)
	jsonData, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(jsonData, &decoded))
	require.Equal(t, report.ReportID, decoded.ReportID)
	markdown, err := os.ReadFile(markdownPath)
	require.NoError(t, err)
	require.Contains(t, string(markdown), report.ReportID)

	err = WriteArtifacts(report, jsonPath, jsonPath)
	require.ErrorContains(t, err, "different")
	require.ErrorContains(t, WriteArtifacts(nil, jsonPath, markdownPath), "report is nil")
}

func TestWriteArtifactsReturnsPublishFailure(t *testing.T) {
	report := testReport(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o600))
	err := WriteArtifacts(
		report,
		filepath.Join(dir, "optimization_report.json"),
		filepath.Join(blocker, "optimization_report.md"),
	)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(dir, "optimization_report.json"))
}

func TestRenderSuccessfulReportRequiresCanonicalBaselines(t *testing.T) {
	report := testReport(t)
	report.BaselineValidation = nil
	_, err := RenderJSON(report)
	require.ErrorContains(t, err, "baseline validation snapshot is nil")
}

func TestRenderSuccessfulReportRejectsIncompleteOrForgedAuditBindings(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Report)
		wantError string
	}{
		{
			name: "six input hashes required",
			mutate: func(report *Report) {
				delete(report.InputHashes, "baselinePrompt")
			},
			wantError: "input hash inventory",
		},
		{
			name: "runtime engine required",
			mutate: func(report *Report) {
				report.Runtime.Engine = ""
			},
			wantError: "runtime engine is empty",
		},
		{
			name: "runtime seed bound",
			mutate: func(report *Report) {
				report.Runtime.Seed++
			},
			wantError: "runtime seed",
		},
		{
			name: "resolved config valid",
			mutate: func(report *Report) {
				report.ResolvedConfig.EvidenceLimit = 0
			},
			wantError: "evidence limit",
		},
		{
			name: "baseline profile binding",
			mutate: func(report *Report) {
				report.BaselineValidation.Provenance.ProfileHash = "forged"
			},
			wantError: "profile hash",
		},
		{
			name: "displayed profile prompt binding",
			mutate: func(report *Report) {
				report.InitialProfile.Prompt = "forged"
			},
			wantError: "profile prompt",
		},
		{
			name: "snapshot run binding",
			mutate: func(report *Report) {
				report.BaselineTrain.Provenance.RunID = "forged"
			},
			wantError: "run id",
		},
		{
			name: "snapshot split binding",
			mutate: func(report *Report) {
				report.Candidates[0].Train.Provenance.Split = "heldout_validation"
			},
			wantError: "split",
		},
		{
			name: "snapshot eval set binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.EvalSetHash = "forged"
			},
			wantError: "eval set hash",
		},
		{
			name: "snapshot metrics binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.MetricsHash = "forged"
			},
			wantError: "metrics hash",
		},
		{
			name: "snapshot seed binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.Seed++
			},
			wantError: "seed",
		},
		{
			name: "snapshot evaluator binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.EvaluatorConfigHash = "forged"
			},
			wantError: "evaluator config hash",
		},
		{
			name: "snapshot metric policy binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.MetricPolicyHash = "forged"
			},
			wantError: "metric policy hash",
		},
		{
			name: "candidate search parent binding",
			mutate: func(report *Report) {
				report.Candidates[0].SearchParentHash = "forged"
			},
			wantError: "search parent",
		},
		{
			name: "PromptIter invocation required",
			mutate: func(report *Report) {
				report.Candidates[0].PromptIterRunID = ""
			},
			wantError: "PromptIter run id",
		},
		{
			name: "PromptIter status required",
			mutate: func(report *Report) {
				report.Candidates[0].PromptIterStatus = ""
			},
			wantError: "PromptIter status",
		},
		{
			name: "completed candidate patch required",
			mutate: func(report *Report) {
				report.Candidates[0].Patches = nil
			},
			wantError: "without a PromptIter patch",
		},
		{
			name: "candidate patch bound to profile",
			mutate: func(report *Report) {
				report.Candidates[0].Patches[0].Value = "forged"
			},
			wantError: "does not match the candidate profile",
		},
		{
			name: "delta comparison label",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsInitial.Comparison = "forged"
			},
			wantError: "comparison",
		},
		{
			name: "delta before profile hash",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsSearchParent.BeforeProfileHash = "forged"
			},
			wantError: "before profile hash",
		},
		{
			name: "delta aggregate validated",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsReleased.NewlyPassing = 0
			},
			wantError: "aggregate change counts",
		},
		{
			name: "accepted search transition",
			mutate: func(report *Report) {
				candidate := &report.Candidates[0]
				candidate.SearchDecision.Status = DecisionAccepted
				candidate.Transition.SearchUpdated = false
				candidate.Transition.SearchAfter = candidate.SearchParentHash
			},
			wantError: "search transition",
		},
		{
			name: "rejected release transition",
			mutate: func(report *Report) {
				candidate := &report.Candidates[0]
				candidate.ReleaseDecision.Status = DecisionRejected
			},
			wantError: "release transition",
		},
		{
			name: "candidate decision reasons required",
			mutate: func(report *Report) {
				report.Candidates[0].ReleaseDecision.Reasons = nil
			},
			wantError: "release decision reasons",
		},
		{
			name: "final decision reasons required",
			mutate: func(report *Report) {
				report.FinalDecision.Reasons = []string{""}
			},
			wantError: "final decision reasons",
		},
		{
			name: "negative resource entry",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "forged",
					Usage: ResourceUsage{
						ModelCalls: Count{Available: true, Value: -1},
					},
				}
				report.Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "model calls must be non-negative",
		},
		{
			name: "unavailable resource has value",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "forged",
					Usage: ResourceUsage{
						InputTokens: Count{Value: 1},
					},
				}
				report.Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "unavailable input tokens has a value",
		},
		{
			name: "global resource cumulative",
			mutate: func(report *Report) {
				report.Resources.Cumulative.ModelCalls = Count{
					Available: true,
					Value:     1,
				}
			},
			wantError: "cumulative resources do not equal",
		},
		{
			name: "candidate resource entry global subset",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "candidate-only",
					Round: 1,
					Usage: ResourceUsage{
						ModelCalls: Count{Available: true, Value: 1},
					},
				}
				report.Candidates[0].Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "absent from the global ledger",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := testReport(t)
			test.mutate(report)
			_, err := RenderJSON(report)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestRenderAllowsCompletedEvaluationWithNotEvaluableGateAndNoPointerUpdates(t *testing.T) {
	report := testReport(t)
	candidate := &report.Candidates[0]
	candidate.SearchDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	candidate.ReleaseDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	candidate.Transition = StateTransition{
		SearchBefore:   candidate.SearchParentHash,
		SearchAfter:    candidate.SearchParentHash,
		ReleasedBefore: candidate.ReleasedParentHash,
		ReleasedAfter:  candidate.ReleasedParentHash,
		Explanation:    "not-evaluable decision leaves both pointers unchanged",
	}
	released := report.InitialProfile
	released.Role = ProfileReleased
	report.ReleasedProfile = released
	report.FinalDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	_, err := RenderJSON(report)
	require.NoError(t, err)

	candidate.Transition.SearchUpdated = true
	_, err = RenderJSON(report)
	require.ErrorContains(t, err, "not-evaluable decision that updates a profile pointer")

	candidate.Transition.SearchUpdated = false
	candidate.Transition.ReleasedAfter = candidate.Profile.Hash
	_, err = RenderJSON(report)
	require.ErrorContains(t, err, "not-evaluable decision that updates a profile pointer")
}

func testReport(t *testing.T) *Report {
	t.Helper()
	const (
		seed          = int64(2003)
		trainSplit    = "train"
		heldoutSplit  = "heldout_validation"
		targetSurface = "agent#instruction"
	)
	initial := testProfileRecord(t, ProfileInitial, "baseline prompt")
	search := initial
	search.Role = ProfileSearch
	candidate := testProfileRecord(t, ProfileCandidate, "candidate prompt")
	released := candidate
	released.Role = ProfileReleased
	metricsHash := hashStrings("test-metrics")
	train := DatasetSpec{
		EvalSetID:   "train-set",
		EvalSetHash: hashStrings("train-set"),
		MetricsHash: metricsHash,
		CaseIDs:     []string{"train-case"},
		MetricNames: []string{"quality"},
		NormalizedInputHashes: map[string]string{
			"train-case": hashStrings("train-input"),
		},
	}
	validation := DatasetSpec{
		EvalSetID:   "validation-set",
		EvalSetHash: hashStrings("validation-set"),
		MetricsHash: metricsHash,
		CaseIDs:     []string{"validation-case"},
		MetricNames: []string{"quality"},
		NormalizedInputHashes: map[string]string{
			"validation-case": hashStrings("validation-input"),
		},
	}
	gate := GatePolicy{
		PrimaryMetric: "quality",
		MetricDirections: map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
		},
		Epsilon:               1e-9,
		MinValidationGain:     0.1,
		NoNewHardFailures:     true,
		NoCriticalRegressions: true,
	}
	runtime := RuntimeConfig{
		Engine:    "deterministic-fake",
		Seed:      seed,
		Model:     map[string]any{"name": "fake-model"},
		Evaluator: map[string]any{"name": "deterministic-evaluator"},
	}
	inputHashes := map[string]string{
		"trainEvalSet":      train.EvalSetHash,
		"validationEvalSet": validation.EvalSetHash,
		"metrics":           metricsHash,
		"baselinePrompt":    hashStrings("baseline-prompt"),
		"promptIterConfig":  hashStrings("promptiter-config"),
		"regressionConfig":  hashStrings("regression-config"),
	}
	metricPolicyHash := testMetricPolicyHash(t, inputHashes, gate)
	evaluatorConfigHash := testEvaluatorConfigHash(
		t,
		runtime,
		train,
		validation,
		metricPolicyHash,
	)
	baselineTrain := testReportSnapshot(
		initial.Hash,
		train,
		trainSplit,
		"run-2003/baseline_train",
		0.4,
		false,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	baselineValidation := testReportSnapshot(
		initial.Hash,
		validation,
		heldoutSplit,
		"run-2003/baseline_validation",
		0.4,
		false,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	candidateTrain := testReportSnapshot(
		candidate.Hash,
		train,
		trainSplit,
		"run-2003/candidate_train/1",
		0.8,
		true,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	candidateValidation := testReportSnapshot(
		candidate.Hash,
		validation,
		heldoutSplit,
		"run-2003/candidate_validation/1",
		0.8,
		true,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	initial.EvaluationRunID = baselineValidation.Provenance.RunID
	search.EvaluationRunID = baselineValidation.Provenance.RunID
	candidate.EvaluationRunID = candidateValidation.Provenance.RunID
	released.EvaluationRunID = candidateValidation.Provenance.RunID
	vsInitial, err := CalculateDelta("vs_initial", baselineValidation, candidateValidation, gate)
	require.NoError(t, err)
	vsSearchParent, err := CalculateDelta(
		"vs_search_parent",
		baselineValidation,
		candidateValidation,
		gate,
	)
	require.NoError(t, err)
	vsReleased, err := CalculateDelta(
		"vs_released",
		baselineValidation,
		candidateValidation,
		gate,
	)
	require.NoError(t, err)
	releaseScoreDelta := vsReleased.ScoreDelta
	resolved := ResolvedConfig{
		Seed:       seed,
		Train:      train,
		Validation: validation,
		PromptIter: PromptIterPolicy{
			MaxOuterRounds:             1,
			SearchMinScoreGain:         0.1,
			InternalValidationStrategy: internalValidationTrainAll,
			TargetSurfaceIDs:           []string{targetSurface},
		},
		Gate:          gate,
		Output:        OutputConfig{JSON: "optimization_report.json", Markdown: "optimization_report.md"},
		EvidenceLimit: 20,
	}
	return &Report{
		SchemaVersion:      SchemaVersion,
		ReportID:           "report-2003",
		RunID:              "run-2003",
		GeneratedAt:        time.Unix(0, 0).UTC(),
		Status:             PipelineSucceeded,
		StopReason:         StopMaxRounds,
		ResolvedConfig:     resolved,
		InputHashes:        inputHashes,
		InitialProfile:     initial,
		SearchProfile:      search,
		ReleasedProfile:    released,
		BaselineTrain:      baselineTrain,
		BaselineValidation: baselineValidation,
		Candidates: []CandidateReport{{
			Round:              1,
			ID:                 "candidate-1",
			Status:             EvaluationCompleted,
			SearchParentHash:   initial.Hash,
			ReleasedParentHash: initial.Hash,
			Profile:            &candidate,
			Patches: []PatchRecord{{
				SurfaceID: targetSurface,
				Value:     "candidate prompt",
				Reason:    "improve deterministic quality",
			}},
			OptimizationReason: "improve deterministic quality",
			PromptIterRunID:    "run-2003/promptiter/1",
			PromptIterStatus:   "succeeded",
			Train:              candidateTrain,
			Validation:         candidateValidation,
			Deltas: &DeltaSet{
				VsInitial:      vsInitial,
				VsSearchParent: vsSearchParent,
				VsReleased:     vsReleased,
			},
			SearchDecision: Decision{Status: DecisionRejected, Reasons: []string{"training objective did not improve"}},
			ReleaseDecision: Decision{
				Status:     DecisionAccepted,
				Reasons:    []string{"held-out release gates passed"},
				ScoreDelta: &releaseScoreDelta,
			},
			Transition: StateTransition{
				SearchBefore: initial.Hash, SearchAfter: initial.Hash,
				ReleasedBefore: initial.Hash, ReleasedAfter: candidate.Hash,
				ReleaseUpdated: true,
				Explanation:    "candidate advanced released profile only",
			},
		}},
		FinalDecision: Decision{Status: DecisionAccepted, Reasons: []string{"candidate-1 released"}},
		Runtime:       runtime,
	}
}

func testProfileRecord(t *testing.T, role ProfileRole, prompt string) ProfileRecord {
	t.Helper()
	value := prompt
	profile := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &value},
		}},
	}
	hash, err := ProfileFingerprint(profile)
	require.NoError(t, err)
	return ProfileRecord{
		Role:            role,
		Hash:            hash,
		StructureID:     "structure",
		TargetSurfaceID: "agent#instruction",
		Prompt:          prompt,
		Profile:         profile,
	}
}

func testReportSnapshot(
	profileHash string,
	dataset DatasetSpec,
	split string,
	runID string,
	score float64,
	passed bool,
	seed int64,
	evaluatorConfigHash string,
	metricPolicyHash string,
) *EvaluationSnapshot {
	status := "failed"
	if passed {
		status = "passed"
	}
	return &EvaluationSnapshot{
		Status: EvaluationCompleted,
		Provenance: EvaluationProvenance{
			RunID:               runID,
			ProfileHash:         profileHash,
			EvalSetID:           dataset.EvalSetID,
			EvalSetHash:         dataset.EvalSetHash,
			MetricsHash:         dataset.MetricsHash,
			Split:               split,
			Seed:                seed,
			EvaluatorConfigHash: evaluatorConfigHash,
			MetricPolicyHash:    metricPolicyHash,
		},
		Inventory: ExpectedInventory{
			CaseIDs:     append([]string(nil), dataset.CaseIDs...),
			MetricNames: append([]string(nil), dataset.MetricNames...),
		},
		OverallScore: score,
		Passed:       boolInt(passed),
		Failed:       boolInt(!passed),
		Cases: []CaseResult{{
			EvalSetID:        dataset.EvalSetID,
			CaseID:           dataset.CaseIDs[0],
			Status:           status,
			Passed:           passed,
			PrimaryMetric:    "quality",
			FinalResponse:    `{"answer":"observed"}`,
			ExpectedResponse: `{"answer":"expected"}`,
			ToolTrajectory: []ToolCall{{
				Sequence: 1,
				Name:     "lookup",
				Arguments: map[string]any{
					"query": "weather",
				},
				Result: map[string]any{"temperature": 25},
			}},
			Trace: []TraceStep{{
				StepID: "step-1",
				NodeID: "router",
				Branch: "lookup",
				Input:  "weather",
				Output: "25",
			}},
			Metrics: []MetricResult{{
				MetricName: "quality",
				Score:      score,
				Status:     status,
				Passed:     passed,
				Threshold:  0.7,
				Direction:  ScoreHigherIsBetter,
				Reason:     "deterministic rubric",
			}},
		}},
	}
}

func testMetricPolicyHash(
	t *testing.T,
	inputHashes map[string]string,
	gate GatePolicy,
) string {
	t.Helper()
	gateJSON, err := json.Marshal(gate)
	require.NoError(t, err)
	return hashStrings(
		"native-metric-policy-v1",
		inputHashes["metrics"],
		string(gateJSON),
	)
}

func testEvaluatorConfigHash(
	t *testing.T,
	runtime RuntimeConfig,
	train DatasetSpec,
	validation DatasetSpec,
	metricPolicyHash string,
) string {
	t.Helper()
	runtimeHash, err := RuntimeConfigFingerprint(runtime)
	require.NoError(t, err)
	return hashStrings(
		"runtime-bound-evaluator-v1",
		train.EvalSetHash,
		validation.EvalSetHash,
		metricPolicyHash,
		runtimeHash,
	)
}

func rebindTestReportProvenance(t *testing.T, report *Report) {
	t.Helper()
	metricPolicyHash := testMetricPolicyHash(
		t,
		report.InputHashes,
		report.ResolvedConfig.Gate,
	)
	evaluatorConfigHash := testEvaluatorConfigHash(
		t,
		report.Runtime,
		report.ResolvedConfig.Train,
		report.ResolvedConfig.Validation,
		metricPolicyHash,
	)
	for _, snapshot := range []*EvaluationSnapshot{
		report.BaselineTrain,
		report.BaselineValidation,
		report.Candidates[0].Train,
		report.Candidates[0].Validation,
	} {
		snapshot.Provenance.MetricPolicyHash = metricPolicyHash
		snapshot.Provenance.EvaluatorConfigHash = evaluatorConfigHash
	}
}

func replaceTestCandidateProfile(
	t *testing.T,
	report *Report,
	profile ProfileRecord,
) {
	t.Helper()
	candidate := &report.Candidates[0]
	profile.EvaluationRunID = candidate.Validation.Provenance.RunID
	candidate.Profile = &profile
	candidate.Patches[0].Value = profile.Prompt
	candidate.Train.Provenance.ProfileHash = profile.Hash
	candidate.Validation.Provenance.ProfileHash = profile.Hash
	vsInitial, err := CalculateDelta(
		"vs_initial",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	vsSearchParent, err := CalculateDelta(
		"vs_search_parent",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	vsReleased, err := CalculateDelta(
		"vs_released",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	candidate.Deltas = &DeltaSet{
		VsInitial:      vsInitial,
		VsSearchParent: vsSearchParent,
		VsReleased:     vsReleased,
	}
	candidate.ReleaseDecision.ScoreDelta = &candidate.Deltas.VsReleased.ScoreDelta
	candidate.Transition.ReleasedAfter = profile.Hash
	released := profile
	released.Role = ProfileReleased
	report.ReleasedProfile = released
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func deeplyNestedValue(depth int) any {
	value := any("leaf")
	for index := 0; index < depth; index++ {
		value = map[string]any{"next": value}
	}
	return value
}
