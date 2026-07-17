//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// reportFixture builds a small synthetic result covering both gate outcomes.
func reportFixture(t *testing.T, accepted bool) (Options, *Result) {
	t.Helper()
	config := &Config{
		AppName:      "app",
		EvalSets:     EvalSetsConfig{Train: "train", Validation: "validation"},
		PromptSource: "baseline_prompt.txt",
		TargetSurfaces: []TargetSurface{
			{Node: "candidate", Type: "instruction"},
		},
		Seed: 7,
	}
	config.applyDefaults()
	require.NoError(t, config.Validate())

	prompt := "优化后的指令"
	candidate := Candidate{
		Round:           1,
		ValidationScore: 0.83,
		TrainScore:      0.67,
		TrainScoreKnown: true,
		ModelCalls:      9,
		WallClock:       6 * time.Millisecond,
		Deltas: []CaseDelta{
			{EvalSetID: "validation", EvalCaseID: "val_gen", Kind: DeltaNewPass, CandidateScore: 1, CandidatePass: true},
			{
				EvalSetID: "validation", EvalCaseID: "val_protected", Kind: DeltaNewFail,
				BaselinePass: true, BaselineScore: 1, CandidateScore: 0.5, ScoreDelta: -0.5,
				CandidateAttribution: &CaseAttribution{
					EvalSetID: "validation", EvalCaseID: "val_protected",
					RootCauses: []FailureCause{{Category: CauseFinalResponseMismatch, Metric: "final_response_avg_score", Evidence: "text mismatch"}},
					Chain:      []FailureCause{{Category: CauseFinalResponseMismatch, Metric: "final_response_avg_score", Evidence: "text mismatch"}},
				},
			},
		},
		TrainDeltas: []CaseDelta{
			{EvalSetID: "train", EvalCaseID: "train_fix", Kind: DeltaNewPass, CandidateScore: 1, CandidatePass: true},
		},
		Profile: &promptiter.Profile{
			Overrides: []promptiter.SurfaceOverride{
				{SurfaceID: "candidate#instruction", Value: astructure.SurfaceValue{Text: &prompt}},
			},
		},
	}
	gate := &GateDecision{
		Accepted:       accepted,
		SelectedRound:  1,
		Recommendation: RecommendationAcceptPendingCanary,
		Summary:        "接受第 1 轮候选",
		Rules: []RuleOutcome{
			{Name: "min_validation_score_gain", Passed: true, Observed: "+0.1667", Threshold: ">= 0.02", Reason: "ok"},
		},
		Selection: []CandidateOutcome{
			{Round: 1, ValidationScore: 0.83, GatePassed: accepted, Selected: accepted},
		},
	}
	if !accepted {
		gate.SelectedRound = 0
		gate.Recommendation = RecommendationReject
		gate.Summary = "拒绝全部候选。训练集 +0.1700 但验证集 case val_protected 由 pass 转 fail，判定为过拟合"
		gate.Rules = append(gate.Rules, RuleOutcome{
			Name: "protected_cases", Passed: false, Observed: "1", Threshold: "== 0",
			Reason: "关键 case 退化: val_protected",
		})
	}
	result := &Result{
		Status:  StatusAccepted,
		RunID:   "run-report-test",
		Gate:    gate,
		Message: gate.Summary,
		BaselineTrain: []CaseSnapshot{
			{EvalSetID: "train", EvalCaseID: "train_fix", Pass: false, Score: 0.5,
				Metrics: []MetricSnapshot{{Name: "final_response_avg_score", Score: 0.5, Status: status.EvalStatusFailed, Reason: "text mismatch"}}},
		},
		BaselineValidation: []CaseSnapshot{
			{EvalSetID: "validation", EvalCaseID: "val_gen", Pass: false, Score: 0},
			{EvalSetID: "validation", EvalCaseID: "val_protected", Pass: true, Score: 1},
		},
		BaselineTrainScore:      0.5,
		BaselineValidationScore: 0.6667,
		BaselineAttributions: []CaseAttribution{
			{
				EvalSetID: "train", EvalCaseID: "train_fix",
				RootCauses: []FailureCause{{Category: CauseToolCallError, Metric: "tool_trajectory_avg_score", Evidence: "expected tool(s) not called: query_order"}},
				Chain: []FailureCause{
					{Category: CauseToolCallError, Metric: "tool_trajectory_avg_score", Evidence: "expected tool(s) not called: query_order"},
					{Category: CauseFinalResponseMismatch, Metric: "final_response_avg_score", Evidence: "多行\n证据\n文本", DerivedFrom: CauseToolCallError},
				},
			},
		},
		Candidates:          []Candidate{candidate},
		CandidatePrompt:     prompt,
		CandidatePromptPath: "output/candidate_prompt.txt",
		Cost: CostSummary{
			Scopes: map[string]ScopeCost{"candidate": {RunCalls: 18, ModelCalls: 26, PromptTokens: 7000, CompletionTokens: 500}},
			Total:  ScopeCost{RunCalls: 18, ModelCalls: 26, PromptTokens: 7000, CompletionTokens: 500},
		},
		StageDurations: map[string]time.Duration{"s1_baseline_train": 7 * time.Millisecond},
		StartedAt:      time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		FinishedAt:     time.Date(2026, 7, 5, 12, 0, 1, 0, time.UTC),
	}
	if !accepted {
		result.Status = StatusRejected
		result.CandidatePrompt = ""
		result.CandidatePromptPath = ""
	}
	opts := Options{
		Config:     config,
		DataDir:    "./data",
		OutputDir:  t.TempDir(),
		Mode:       ModeFake,
		Components: Components{ModelInfo: map[string]string{"candidate": "fake"}},
	}
	return opts, result
}

// TestReportJSONKeyFields locks the report schema fields required by the
// acceptance criteria: baseline score, candidate score, per-case delta, gate
// decision, and accept/reject reasons.
func TestReportJSONKeyFields(t *testing.T) {
	opts, result := reportFixture(t, true)
	jsonPath, markdownPath, err := WriteReports(opts, result)
	require.NoError(t, err)
	assert.FileExists(t, markdownPath)

	content, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(content, &decoded))

	// Top-level schema.
	for _, key := range []string{
		"runId", "mode", "seed", "startedAt", "finishedAt", "config",
		"baseline", "candidate", "attribution", "delta", "gate", "rounds", "cost", "nextSteps",
	} {
		assert.Contains(t, decoded, key, key)
	}

	baseline := decoded["baseline"].(map[string]any)
	train := baseline["train"].(map[string]any)
	assert.InDelta(t, 0.5, train["score"].(float64), 1e-9)
	validation := baseline["validation"].(map[string]any)
	assert.InDelta(t, 0.6667, validation["score"].(float64), 1e-9)

	candidate := decoded["candidate"].(map[string]any)
	assert.InDelta(t, 0.83, candidate["validationScore"].(float64), 1e-9)
	assert.Equal(t, "优化后的指令", candidate["prompt"])
	assert.Equal(t, true, candidate["accepted"])

	delta := decoded["delta"].(map[string]any)
	validationDeltas := delta["validation"].([]any)
	require.Len(t, validationDeltas, 2)
	first := validationDeltas[0].(map[string]any)
	assert.Equal(t, "val_gen", first["evalCaseId"])
	assert.Equal(t, string(DeltaNewPass), first["kind"])
	summary := delta["summary"].(map[string]any)
	assert.EqualValues(t, 1, summary["newPass"])
	assert.EqualValues(t, 1, summary["newFail"])

	gate := decoded["gate"].(map[string]any)
	assert.Equal(t, true, gate["accepted"])
	assert.Equal(t, RecommendationAcceptPendingCanary, gate["recommendation"])
	rules := gate["rules"].([]any)
	require.NotEmpty(t, rules)
	rule := rules[0].(map[string]any)
	for _, key := range []string{"name", "passed", "observed", "threshold", "reason"} {
		assert.Contains(t, rule, key)
	}

	attribution := decoded["attribution"].(map[string]any)
	baselineCounts := attribution["baselineCounts"].(map[string]any)
	assert.EqualValues(t, 1, baselineCounts[string(CauseToolCallError)])

	cost := decoded["cost"].(map[string]any)
	total := cost["total"].(map[string]any)
	assert.EqualValues(t, 26, total["modelCalls"])
}

// TestReportMarkdownAcceptPath asserts the accept wording and next steps.
func TestReportMarkdownAcceptPath(t *testing.T) {
	opts, result := reportFixture(t, true)
	report := BuildReport(opts, result)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)

	assert.Contains(t, markdown, "**接受**（accept_pending_canary）")
	assert.Contains(t, markdown, "baseline 0.6667 → 候选 0.8300")
	assert.Contains(t, markdown, "| val_protected | new_fail |")
	assert.Contains(t, markdown, "| min_validation_score_gain |")
	assert.Contains(t, markdown, "canary")
	assert.Contains(t, markdown, "evalset/recorder")
	assert.Contains(t, markdown, "-write-back")
	assert.Contains(t, markdown, "优化后的指令")
	// Train delta section renders when known.
	assert.Contains(t, markdown, "逐 case delta（train）")
	assert.Contains(t, markdown, "| train_fix | new_pass |")
	// Multi-line evidence is flattened into one list line.
	assert.Contains(t, markdown, "多行 证据 文本")
	assert.NotContains(t, markdown, "多行\n证据")
}

// TestReportMarkdownRejectPath asserts the reject wording, overfitting call
// out, and the rejected next steps.
func TestReportMarkdownRejectPath(t *testing.T) {
	opts, result := reportFixture(t, false)
	report := BuildReport(opts, result)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)

	assert.Contains(t, markdown, "**拒绝**")
	assert.Contains(t, markdown, "判定为过拟合")
	assert.Contains(t, markdown, "val_protected")
	assert.Contains(t, markdown, "**未通过**")
	assert.Contains(t, markdown, "baseline prompt 保持不变")
	assert.NotContains(t, markdown, "accept_pending_canary")
	// The rejected candidate is still fully reported for auditability.
	assert.Contains(t, markdown, "候选 prompt 全文")
}

// TestReportMarkdownEscapesAdversarialContent locks the report against
// Markdown injection from external data: a model-generated prompt containing
// backtick fences stays inside a longer dynamic fence, and eval-data values
// (case IDs, evidence) containing pipes, newlines, links, or raw HTML are
// escaped so the table/list structure and the rendered page stay intact.
func TestReportMarkdownEscapesAdversarialContent(t *testing.T) {
	opts, result := reportFixture(t, true)
	hostilePrompt := "第一行\n```\n# 注入标题\n<script>alert(1)</script>\n````\n结束"
	result.Candidates[0].Profile.Overrides[0].Value.Text = &hostilePrompt
	result.Candidates[0].Deltas[1].EvalCaseID = "case|注入<img src=x>"
	result.BaselineAttributions[0].EvalCaseID = "train|注入[link](http://evil)"
	result.BaselineAttributions[0].Chain[1].Evidence = "多行\n`证据`|<b>加粗</b>"

	report := BuildReport(opts, result)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)

	// The prompt fence is longer than any backtick run inside the prompt (the
	// prompt's longest run is 4, so the fence must be 5), keeping the injected
	// fence closers and Markdown inside the code block.
	assert.Contains(t, markdown, "`````\n"+hostilePrompt+"\n`````")
	// Table cells: the pipe and the raw HTML tag are escaped.
	assert.Contains(t, markdown, `case\|注入\<img src=x\>`)
	assert.NotContains(t, markdown, "<img src=x>")
	// List items: pipes and link syntax are escaped.
	assert.Contains(t, markdown, `train\|注入\[link\](http://evil)`)
	assert.NotContains(t, markdown, "[link](http://evil)")
	// Evidence: newlines flatten, backticks / pipes / HTML are escaped.
	assert.Contains(t, markdown, "多行 \\`证据\\`\\|\\<b\\>加粗\\</b\\>")
	assert.NotContains(t, markdown, "<b>")
}

// TestReportWithoutCandidates covers a run that produced no candidates.
func TestReportWithoutCandidates(t *testing.T) {
	opts, result := reportFixture(t, false)
	result.Candidates = nil
	result.Gate.Selection = nil
	report := BuildReport(opts, result)
	require.Nil(t, report.Candidate)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	assert.Contains(t, markdown, "**拒绝**")
	assert.NotContains(t, markdown, "候选 prompt 全文")

	jsonPath, _, err := WriteReports(opts, result)
	require.NoError(t, err)
	var decoded map[string]any
	content, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &decoded))
	_, hasCandidate := decoded["candidate"]
	assert.False(t, hasCandidate)
}

// TestReportFilesLandInOutputDir verifies the canonical file names.
func TestReportFilesLandInOutputDir(t *testing.T) {
	opts, result := reportFixture(t, true)
	jsonPath, markdownPath, err := WriteReports(opts, result)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(opts.OutputDir, "optimization_report.json"), jsonPath)
	assert.Equal(t, filepath.Join(opts.OutputDir, "optimization_report.md"), markdownPath)
}
