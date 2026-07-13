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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

var phase4Pending = []string{}

type ReportContext struct {
	Mode             string
	Seed             int64
	TargetSurfaceIDs []string
	PromptPath       string
	PromptSHA256     string
	ConfigPath       string
	ConfigSHA256     string
	ModelConfig      *ModelConfigSummary
	PromptIterConfig *PromptIterConfigSummary
	FinalGate        finalGateConfig
	LatencyMs        int64
	ModelCallCount   int
}

type OptimizationReport struct {
	Phase            string                   `json:"phase"`
	Mode             string                   `json:"mode"`
	Seed             int64                    `json:"seed"`
	SingleRound      bool                     `json:"singleRound"`
	TargetSurfaceIDs []string                 `json:"targetSurfaceIds"`
	PromptPath       string                   `json:"promptPath"`
	PromptSHA256     string                   `json:"promptSha256"`
	ConfigPath       string                   `json:"configPath,omitempty"`
	ConfigSHA256     string                   `json:"configSha256,omitempty"`
	ModelConfig      *ModelConfigSummary      `json:"modelConfig,omitempty"`
	PromptIterConfig *PromptIterConfigSummary `json:"promptIterConfig,omitempty"`
	Baseline         ReportCandidate          `json:"baseline"`
	Candidate        ReportCandidate          `json:"candidate"`
	Rounds           []ReportRound            `json:"rounds"`
	Delta            *ValidationDelta         `json:"delta"`
	Gate             *GateReport              `json:"gate"`
	Attribution      *ReportAttribution       `json:"attribution"`
	TraceSmoke       TraceSmokeReport         `json:"traceSmoke"`
	Cost             CostSummary              `json:"cost"`
	LatencyMs        int64                    `json:"latencyMs"`
	ModelCallCount   int                      `json:"modelCallCount"`
	Pending          []string                 `json:"pending"`
}

type TraceSmokeReport struct {
	Enabled                   bool                `json:"enabled"`
	EvalSetID                 string              `json:"evalSetId"`
	OptimizationSkipped       bool                `json:"optimizationSkipped"`
	OptimizationSkippedReason string              `json:"optimizationSkippedReason"`
	Evaluation                *EvaluationSummary  `json:"evaluation"`
	Attribution               *FailureAttribution `json:"attribution"`
}

type ReportCandidate struct {
	Train           *EvaluationSummary `json:"train"`
	Validation      *EvaluationSummary `json:"validation"`
	AcceptedProfile *ProfileSummary    `json:"acceptedProfile,omitempty"`
}

type ReportRound struct {
	Round            int                `json:"round"`
	Accepted         bool               `json:"accepted"`
	AcceptanceReason string             `json:"acceptanceReason,omitempty"`
	ScoreDelta       float64            `json:"scoreDelta"`
	Train            *EvaluationSummary `json:"train"`
	Validation       *EvaluationSummary `json:"validation"`
	OutputProfile    *ProfileSummary    `json:"outputProfile,omitempty"`
	Patches          []PatchSummary     `json:"patches"`
}

type PatchSummary struct {
	SurfaceID string              `json:"surfaceId"`
	Reason    string              `json:"reason"`
	Value     SurfaceValueSummary `json:"value"`
}

type ProfileSummary struct {
	StructureID string                   `json:"structureId,omitempty"`
	Overrides   []SurfaceOverrideSummary `json:"overrides"`
}

type SurfaceOverrideSummary struct {
	SurfaceID string              `json:"surfaceId"`
	Value     SurfaceValueSummary `json:"value"`
}

type SurfaceValueSummary struct {
	Text  *string            `json:"text,omitempty"`
	Tools []ToolValueSummary `json:"tools,omitempty"`
}

type ToolValueSummary struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type ModelConfigSummary struct {
	Name          string  `json:"name"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"maxTokens"`
	Stream        bool    `json:"stream"`
	Deterministic bool    `json:"deterministic"`
}

type PromptIterConfigSummary struct {
	MaxRounds                  int      `json:"maxRounds"`
	MinScoreGain               float64  `json:"minScoreGain"`
	TargetScore                *float64 `json:"targetScore,omitempty"`
	MaxRoundsWithoutAcceptance int      `json:"maxRoundsWithoutAcceptance"`
}

type ReportAttribution struct {
	BaselineTrain       *FailureAttribution `json:"baselineTrain"`
	BaselineValidation  *FailureAttribution `json:"baselineValidation"`
	CandidateValidation *FailureAttribution `json:"candidateValidation"`
}

type EvaluationSummary struct {
	OverallScore float64          `json:"overallScore"`
	EvalSets     []EvalSetSummary `json:"evalSets"`
}

type EvalSetSummary struct {
	EvalSetID    string        `json:"evalSetId"`
	OverallScore float64       `json:"overallScore"`
	Cases        []CaseSummary `json:"cases"`
}

type CaseSummary struct {
	EvalCaseID string          `json:"evalCaseId"`
	Metrics    []MetricSummary `json:"metrics"`
}

type MetricSummary struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

func newOptimizationReport(
	result *promptiterengine.RunResult,
	candidateTrain *EvaluationSummary,
	ctx ReportContext,
) (*OptimizationReport, error) {
	if result == nil {
		return nil, errors.New("run result is nil")
	}
	if len(result.Rounds) == 0 {
		return nil, errors.New("run result has no rounds")
	}
	rounds := make([]ReportRound, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		rounds = append(rounds, reportRound(round))
	}
	firstRound := result.Rounds[0]
	baselineTrain := evaluationSummary(firstRound.Train)
	candidateValidationResult, accepted, err := finalCandidateValidation(result)
	if err != nil {
		return nil, err
	}
	effectiveCandidateTrain := candidateTrain
	if accepted {
		if effectiveCandidateTrain == nil {
			return nil, errors.New("candidate train regression is required when a round was accepted")
		}
	} else {
		effectiveCandidateTrain = baselineTrain
	}
	baselineValidation := evaluationSummary(result.BaselineValidation)
	candidateValidation := evaluationSummary(candidateValidationResult)
	delta, err := buildValidationDelta(baselineValidation, candidateValidation)
	if err != nil {
		return nil, err
	}
	baselineTrainAttribution, err := buildFailureAttribution(firstRound.Train)
	if err != nil {
		return nil, fmt.Errorf("build baseline train attribution: %w", err)
	}
	baselineValidationAttribution, err := buildFailureAttribution(result.BaselineValidation)
	if err != nil {
		return nil, fmt.Errorf("build baseline validation attribution: %w", err)
	}
	candidateValidationAttribution, err := buildFailureAttribution(candidateValidationResult)
	if err != nil {
		return nil, fmt.Errorf("build candidate validation attribution: %w", err)
	}
	gate, err := buildGateReport(baselineValidation, candidateValidation, delta, ctx.FinalGate, ctx.LatencyMs, ctx.ModelCallCount, ctx.Mode)
	if err != nil {
		return nil, err
	}
	return &OptimizationReport{
		Phase:            phaseVersion,
		Mode:             ctx.Mode,
		Seed:             ctx.Seed,
		SingleRound:      len(result.Rounds) == 1,
		TargetSurfaceIDs: append([]string(nil), ctx.TargetSurfaceIDs...),
		PromptPath:       ctx.PromptPath,
		PromptSHA256:     ctx.PromptSHA256,
		ConfigPath:       ctx.ConfigPath,
		ConfigSHA256:     ctx.ConfigSHA256,
		ModelConfig:      ctx.ModelConfig,
		PromptIterConfig: ctx.PromptIterConfig,
		Baseline: ReportCandidate{
			// See buildRunRequest: result.Rounds[0].Train is the baseline train
			// evaluation while InitialProfile remains nil for this example run.
			Train:      baselineTrain,
			Validation: baselineValidation,
		},
		Candidate: ReportCandidate{
			Train:           effectiveCandidateTrain,
			Validation:      candidateValidation,
			AcceptedProfile: profileSummary(result.AcceptedProfile),
		},
		Rounds: rounds,
		Delta:  delta,
		Gate:   gate,
		Attribution: &ReportAttribution{
			BaselineTrain:       baselineTrainAttribution,
			BaselineValidation:  baselineValidationAttribution,
			CandidateValidation: candidateValidationAttribution,
		},
		Cost:           CostSummary{TotalUSD: 0},
		LatencyMs:      ctx.LatencyMs,
		ModelCallCount: ctx.ModelCallCount,
		Pending:        append([]string{}, phase4Pending...),
	}, nil
}

func newTraceSmokeOptimizationReport(
	evaluation *EvaluationSummary,
	attribution *FailureAttribution,
	ctx ReportContext,
) *OptimizationReport {
	return &OptimizationReport{
		Phase:            phaseVersion,
		Mode:             ctx.Mode,
		Seed:             ctx.Seed,
		SingleRound:      false,
		TargetSurfaceIDs: []string{},
		ModelConfig:      ctx.ModelConfig,
		Baseline:         ReportCandidate{},
		Candidate:        ReportCandidate{},
		Rounds:           []ReportRound{},
		TraceSmoke: TraceSmokeReport{
			Enabled:                   true,
			EvalSetID:                 traceSmokeEvalSetID,
			OptimizationSkipped:       true,
			OptimizationSkippedReason: traceSmokeSkipReason,
			Evaluation:                evaluation,
			Attribution:               attribution,
		},
		Cost:           CostSummary{TotalUSD: 0},
		LatencyMs:      ctx.LatencyMs,
		ModelCallCount: ctx.ModelCallCount,
		Pending:        append([]string{}, phase4Pending...),
	}
}

func finalCandidateValidation(result *promptiterengine.RunResult) (*promptiterengine.EvaluationResult, bool, error) {
	if result == nil {
		return nil, false, errors.New("run result is nil")
	}
	if result.BaselineValidation == nil {
		return nil, false, errors.New("baseline validation is nil")
	}
	round, ok := lastAcceptedRound(result)
	if !ok {
		return result.BaselineValidation, false, nil
	}
	if round.Validation == nil {
		return nil, true, fmt.Errorf("accepted round %d validation is nil", round.Round)
	}
	return round.Validation, true, nil
}

func lastAcceptedRound(result *promptiterengine.RunResult) (*promptiterengine.RoundResult, bool) {
	if result == nil {
		return nil, false
	}
	acceptedIndex := -1
	for i := range result.Rounds {
		if result.Rounds[i].Acceptance != nil && result.Rounds[i].Acceptance.Accepted {
			acceptedIndex = i
		}
	}
	if acceptedIndex < 0 {
		return nil, false
	}
	return &result.Rounds[acceptedIndex], true
}

func reportRound(round promptiterengine.RoundResult) ReportRound {
	out := ReportRound{
		Round:         round.Round,
		Train:         evaluationSummary(round.Train),
		Validation:    evaluationSummary(round.Validation),
		OutputProfile: profileSummary(round.OutputProfile),
	}
	if round.Acceptance != nil {
		out.Accepted = round.Acceptance.Accepted
		out.AcceptanceReason = round.Acceptance.Reason
		out.ScoreDelta = round.Acceptance.ScoreDelta
	}
	if round.Patches != nil {
		out.Patches = make([]PatchSummary, 0, len(round.Patches.Patches))
		for _, patch := range round.Patches.Patches {
			out.Patches = append(out.Patches, PatchSummary{
				SurfaceID: patch.SurfaceID,
				Reason:    patch.Reason,
				Value:     surfaceValueSummary(patch.Value),
			})
		}
	}
	return out
}

func profileSummary(profile *promptiter.Profile) *ProfileSummary {
	if profile == nil {
		return nil
	}
	out := &ProfileSummary{
		StructureID: profile.StructureID,
		Overrides:   make([]SurfaceOverrideSummary, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		out.Overrides = append(out.Overrides, SurfaceOverrideSummary{
			SurfaceID: override.SurfaceID,
			Value:     surfaceValueSummary(override.Value),
		})
	}
	return out
}

func surfaceValueSummary(value astructure.SurfaceValue) SurfaceValueSummary {
	out := SurfaceValueSummary{
		Text:  value.Text,
		Tools: make([]ToolValueSummary, 0, len(value.Tools)),
	}
	for _, tool := range value.Tools {
		out.Tools = append(out.Tools, ToolValueSummary{
			ID:          tool.ID,
			Description: tool.Description,
		})
	}
	return out
}

func fakeModelConfigSummary() *ModelConfigSummary {
	return &ModelConfigSummary{
		Name:          fakeModelName,
		Temperature:   fakeModelTemperature,
		MaxTokens:     fakeModelMaxTokens,
		Stream:        fakeModelStream,
		Deterministic: true,
	}
}

func promptIterConfigSummary(cfg promptIterFileConfig) *PromptIterConfigSummary {
	var targetScore *float64
	if configured := cfg.targetScore(); configured != nil {
		value := *configured
		targetScore = &value
	}
	return &PromptIterConfigSummary{
		MaxRounds:                  cfg.maxRounds(),
		MinScoreGain:               cfg.minScoreGain(),
		TargetScore:                targetScore,
		MaxRoundsWithoutAcceptance: cfg.maxRoundsWithoutAcceptance(),
	}
}

func evaluationSummary(result *promptiterengine.EvaluationResult) *EvaluationSummary {
	if result == nil {
		return nil
	}
	out := &EvaluationSummary{
		OverallScore: result.OverallScore,
		EvalSets:     make([]EvalSetSummary, 0, len(result.EvalSets)),
	}
	for _, evalSet := range result.EvalSets {
		evalSetSummary := EvalSetSummary{
			EvalSetID:    evalSet.EvalSetID,
			OverallScore: evalSet.OverallScore,
			Cases:        make([]CaseSummary, 0, len(evalSet.Cases)),
		}
		for _, evalCase := range evalSet.Cases {
			caseSummary := CaseSummary{
				EvalCaseID: evalCase.EvalCaseID,
				Metrics:    make([]MetricSummary, 0, len(evalCase.Metrics)),
			}
			for _, metric := range evalCase.Metrics {
				caseSummary.Metrics = append(caseSummary.Metrics, MetricSummary{
					MetricName: metric.MetricName,
					Score:      metric.Score,
					Status:     string(metric.Status),
					Reason:     metric.Reason,
				})
			}
			evalSetSummary.Cases = append(evalSetSummary.Cases, caseSummary)
		}
		out.EvalSets = append(out.EvalSets, evalSetSummary)
	}
	return out
}

func writeOptimizationReport(outputDir string, report *OptimizationReport) (string, string, error) {
	if report == nil {
		return "", "", errors.New("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create output dir %s: %w", outputDir, err)
	}
	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	markdownPath := filepath.Join(outputDir, "optimization_report.md")
	jsonContent, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal report json: %w", err)
	}
	jsonContent = append(jsonContent, '\n')
	if err := os.WriteFile(jsonPath, jsonContent, 0o644); err != nil {
		return "", "", fmt.Errorf("write report json: %w", err)
	}
	if err := os.WriteFile(markdownPath, renderMarkdownReport(report), 0o644); err != nil {
		return "", "", fmt.Errorf("write report markdown: %w", err)
	}
	return jsonPath, markdownPath, nil
}

func renderMarkdownReport(report *OptimizationReport) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Phase 4 v2 PromptIter Regression Loop\n\n")
	fmt.Fprintf(&buf, "Mode: `%s`\n\n", report.Mode)
	renderAuditSummaryMarkdown(&buf, report)
	if report.TraceSmoke.Enabled {
		renderTraceSmokeMarkdown(&buf, report)
		return finalizedMarkdown(&buf)
	}
	fmt.Fprintf(&buf, "Single round: `%t`\n\n", report.SingleRound)
	if len(report.TargetSurfaceIDs) > 0 {
		fmt.Fprintf(&buf, "Target surface: `%s`\n\n", report.TargetSurfaceIDs[0])
	}
	fmt.Fprintf(&buf, "Baseline validation overall score: `%.4f`\n\n", scoreOf(report.Baseline.Validation))
	fmt.Fprintf(&buf, "Candidate validation overall score: `%.4f`\n\n", scoreOf(report.Candidate.Validation))
	fmt.Fprintf(&buf, "Candidate train overall score: `%.4f`\n\n", scoreOf(report.Candidate.Train))
	renderProfileSummaryMarkdown(&buf, "PromptIter Accepted Profile", report.Candidate.AcceptedProfile)
	if report.Gate != nil {
		fmt.Fprintf(&buf, "PromptIter acceptance determines whether a candidate becomes the working profile inside the optimization loop; it is not release approval. Release approval is determined exclusively by the final gate.\n\n")
		fmt.Fprintf(&buf, "Final release gate decision: `%s`\n\n", report.Gate.Decision)
		fmt.Fprintf(&buf, "Validation gain: `%.4f`\n\n", report.Gate.ValidationGain)
		renderFinalReleaseOutcomeMarkdown(&buf, report.Gate)
	}
	if report.Delta != nil {
		fmt.Fprintf(&buf, "## Validation Delta\n\n")
		fmt.Fprintf(&buf, "- New pass: `%d`\n", report.Delta.Summary.NewPass)
		fmt.Fprintf(&buf, "- New fail: `%d`\n", report.Delta.Summary.NewFail)
		fmt.Fprintf(&buf, "- Improved: `%d`\n", report.Delta.Summary.Improved)
		fmt.Fprintf(&buf, "- Regressed: `%d`\n", report.Delta.Summary.Regressed)
		fmt.Fprintf(&buf, "- Unchanged pass: `%d`\n", report.Delta.Summary.UnchangedPass)
		fmt.Fprintf(&buf, "- Unchanged fail: `%d`\n\n", report.Delta.Summary.UnchangedFail)
	}
	for _, round := range report.Rounds {
		fmt.Fprintf(&buf, "## Round %d\n\n", round.Round)
		fmt.Fprintf(&buf, "- Accepted by PromptIter: `%t`\n", round.Accepted)
		fmt.Fprintf(&buf, "- Score delta: `%.4f`\n", round.ScoreDelta)
		fmt.Fprintf(&buf, "- PromptIter acceptance reason: %s\n", round.AcceptanceReason)
		for _, patch := range round.Patches {
			fmt.Fprintf(&buf, "- Patch `%s`: %s\n", patch.SurfaceID, surfaceValueMarkdown(patch.Value))
		}
		renderProfileSummaryMarkdown(&buf, "Round Output Profile", round.OutputProfile)
	}
	if report.Gate != nil {
		fmt.Fprintf(&buf, "\n## Final Gate\n\n")
		for _, reason := range report.Gate.Reasons {
			fmt.Fprintf(&buf, "- %s\n", reason)
		}
	}
	renderReportAttributionMarkdown(&buf, report.Attribution)
	if len(report.Pending) > 0 {
		fmt.Fprintf(&buf, "\n## Pending\n\n")
		for _, item := range report.Pending {
			fmt.Fprintf(&buf, "- `%s`\n", item)
		}
	}
	return finalizedMarkdown(&buf)
}

func renderFinalReleaseOutcomeMarkdown(buf *bytes.Buffer, gate *GateReport) {
	if gate.Decision == gateDecisionAccept {
		fmt.Fprintf(buf, "Final release outcome: approved by the final gate.\n\n")
		return
	}
	if len(gate.CriticalRegressions) > 0 {
		quotedCases := make([]string, 0, len(gate.CriticalRegressions))
		for _, caseID := range gate.CriticalRegressions {
			quotedCases = append(quotedCases, fmt.Sprintf("`%s`", caseID))
		}
		fmt.Fprintf(buf, "Final release outcome: rejected by the final gate because critical validation regression cases were detected: %s.\n\n", strings.Join(quotedCases, ", "))
		return
	}
	fmt.Fprintf(buf, "Final release outcome: rejected by the final gate; see the Final Gate reasons below.\n\n")
}

func finalizedMarkdown(buf *bytes.Buffer) []byte {
	return []byte(strings.TrimRight(buf.String(), "\n") + "\n")
}

func renderAuditSummaryMarkdown(buf *bytes.Buffer, report *OptimizationReport) {
	fmt.Fprintf(buf, "## Audit Configuration\n\n")
	fmt.Fprintf(buf, "- Deterministic seed: `%d`\n", report.Seed)
	if report.ConfigPath != "" {
		fmt.Fprintf(buf, "- PromptIter config: `%s`\n", report.ConfigPath)
		fmt.Fprintf(buf, "- PromptIter config SHA-256: `%s`\n", report.ConfigSHA256)
	}
	if report.ModelConfig != nil {
		fmt.Fprintf(buf, "- Model: `%s` (deterministic=`%t`, temperature=`%.1f`, max tokens=`%d`, stream=`%t`)\n",
			report.ModelConfig.Name,
			report.ModelConfig.Deterministic,
			report.ModelConfig.Temperature,
			report.ModelConfig.MaxTokens,
			report.ModelConfig.Stream,
		)
	}
	if report.PromptIterConfig != nil {
		targetScore := "disabled"
		if report.PromptIterConfig.TargetScore != nil {
			targetScore = fmt.Sprintf("%.4f", *report.PromptIterConfig.TargetScore)
		}
		fmt.Fprintf(buf, "- PromptIter: max rounds=`%d`, min score gain=`%.4f`, target score=`%s`, max rounds without acceptance=`%d`\n",
			report.PromptIterConfig.MaxRounds,
			report.PromptIterConfig.MinScoreGain,
			targetScore,
			report.PromptIterConfig.MaxRoundsWithoutAcceptance,
		)
	}
	fmt.Fprintf(buf, "\n")
}

func renderTraceSmokeMarkdown(buf *bytes.Buffer, report *OptimizationReport) {
	fmt.Fprintf(buf, "## Trace Smoke\n\n")
	fmt.Fprintf(buf, "- Eval set: `%s`\n", report.TraceSmoke.EvalSetID)
	fmt.Fprintf(buf, "- Optimization skipped: `%t`\n", report.TraceSmoke.OptimizationSkipped)
	fmt.Fprintf(buf, "- Reason: %s\n", report.TraceSmoke.OptimizationSkippedReason)
	fmt.Fprintf(buf, "- Evaluation overall score: `%.4f`\n", scoreOf(report.TraceSmoke.Evaluation))
	fmt.Fprintf(buf, "- Model calls: `%d`\n", report.ModelCallCount)
	fmt.Fprintf(buf, "- Latency: `%dms`\n", report.LatencyMs)
	renderFailureAttributionMarkdown(buf, report.TraceSmoke.Attribution)
}

func renderProfileSummaryMarkdown(buf *bytes.Buffer, title string, profile *ProfileSummary) {
	if profile == nil {
		return
	}
	fmt.Fprintf(buf, "\n### %s\n\n", title)
	if len(profile.Overrides) == 0 {
		fmt.Fprintf(buf, "- No overrides\n\n")
		return
	}
	for _, override := range profile.Overrides {
		fmt.Fprintf(buf, "- `%s`: %s\n", override.SurfaceID, surfaceValueMarkdown(override.Value))
	}
	fmt.Fprintf(buf, "\n")
}

func surfaceValueMarkdown(value SurfaceValueSummary) string {
	if len(value.Tools) > 0 {
		parts := make([]string, 0, len(value.Tools))
		for _, tool := range value.Tools {
			parts = append(parts, fmt.Sprintf("tool `%s` description = %q", tool.ID, tool.Description))
		}
		return strings.Join(parts, "; ")
	}
	if value.Text != nil {
		return fmt.Sprintf("text = %q", *value.Text)
	}
	return "empty value"
}

func renderReportAttributionMarkdown(buf *bytes.Buffer, attribution *ReportAttribution) {
	if attribution == nil {
		return
	}
	fmt.Fprintf(buf, "\n## Failure Attribution\n\n")
	renderFailureAttributionSection(buf, "Baseline train", attribution.BaselineTrain)
	renderFailureAttributionSection(buf, "Baseline validation", attribution.BaselineValidation)
	renderFailureAttributionSection(buf, "Candidate validation", attribution.CandidateValidation)
}

func renderFailureAttributionMarkdown(buf *bytes.Buffer, attribution *FailureAttribution) {
	if attribution == nil {
		return
	}
	fmt.Fprintf(buf, "\n## Failure Attribution\n\n")
	renderFailureAttributionSection(buf, "", attribution)
}

func renderFailureAttributionSection(buf *bytes.Buffer, title string, attribution *FailureAttribution) {
	if attribution == nil {
		return
	}
	if title != "" {
		fmt.Fprintf(buf, "### %s\n\n", title)
	}
	fmt.Fprintf(buf, "- Tool not called: `%d`\n", attribution.Summary.ToolNotCalled)
	fmt.Fprintf(buf, "- Wrong tool name: `%d`\n", attribution.Summary.WrongToolName)
	fmt.Fprintf(buf, "- Tool arguments mismatch: `%d`\n", attribution.Summary.ToolArgumentsMismatch)
	fmt.Fprintf(buf, "- Route error: `%d`\n", attribution.Summary.RouteError)
	fmt.Fprintf(buf, "- Format error: `%d`\n", attribution.Summary.FormatError)
	fmt.Fprintf(buf, "- Knowledge insufficient: `%d`\n", attribution.Summary.KnowledgeInsufficient)
	fmt.Fprintf(buf, "- Final response mismatch: `%d`\n", attribution.Summary.FinalResponseMismatch)
	fmt.Fprintf(buf, "- Metric failure: `%d`\n\n", attribution.Summary.MetricFailure)
	for _, failedCase := range attribution.PerFailedCase {
		fmt.Fprintf(buf, "- `%s`: `%s`\n", failedCase.EvalCaseID, failedCase.Category)
	}
	if title != "" {
		fmt.Fprintf(buf, "\n")
	}
}

func scoreOf(summary *EvaluationSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.OverallScore
}
