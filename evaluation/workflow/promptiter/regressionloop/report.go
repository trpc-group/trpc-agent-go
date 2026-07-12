//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// BuildReport assembles the auditable optimization report.
func BuildReport(input ReportInput) OptimizationReport {
	ctx := input.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	acceptedValidation, acceptedRound, hasEngineAccepted := AcceptedValidation(input.PromptIterRun)
	candidateValidation, candidateRound, hasFinalCandidate := FinalCandidateValidation(input.PromptIterRun)
	if input.CandidateValidation != nil {
		candidateValidation = input.CandidateValidation
		hasFinalCandidate = true
	}
	if candidateValidation == nil {
		candidateValidation = acceptedValidation
		candidateRound = acceptedRound
	}
	delta := ComputeDelta(input.BaselineValidation, candidateValidation, input.Config.Gate.CriticalCaseIDs)
	engineAccepted := hasEngineAccepted && (!hasFinalCandidate || acceptedRound == candidateRound)
	gate := EvaluateGate(input.Config.Gate, engineAccepted, delta, input.Cost, input.Latency)
	attributionHints := AttributionHints(input.Config, input.Metrics)
	baselineAttributions := append([]CaseAttribution(nil), input.Attributions...)
	var candidateAttributions []CaseAttribution
	if hasFinalCandidate {
		candidateAttributions = AttributeFailuresWithOptions(ctx, candidateValidation, AttributionOptions{
			Hints:   attributionHints,
			Metrics: input.Metrics,
			Judge:   input.AttributionJudge,
		})
	}
	attributions := append([]CaseAttribution(nil), baselineAttributions...)
	attributions = append(attributions, candidateAttributions...)
	report := OptimizationReport{
		Metadata: RunMetadata{
			AppName:          input.Config.AppName,
			StartedAt:        input.StartedAt,
			FinishedAt:       input.FinishedAt,
			Duration:         Duration{Duration: input.FinishedAt.Sub(input.StartedAt)},
			Seed:             input.Config.Seed,
			PromptSource:     input.Config.PromptSource,
			MetricsPath:      input.Config.MetricsPath,
			MetricNames:      metricNames(input.Metrics),
			TrainEvalSetID:   input.Config.TrainEvalSetID,
			ValidationSetID:  input.Config.ValidationEvalSetID,
			Scenario:         input.Config.Scenario,
			TargetSurfaces:   append([]string(nil), input.Config.TargetSurfaceIDs...),
			ModelConfig:      cloneStringMap(input.Config.ModelConfig),
			FakeConfig:       cloneStringMap(input.Config.FakeConfig),
			AttributionHints: cloneAttributionHints(attributionHints),
		},
		BaselineTrain:                      evaluationReportFromResult(input.BaselineTrain),
		BaselineValidation:                 evaluationReportFromResult(input.BaselineValidation),
		AcceptedValidation:                 evaluationReportFromResult(acceptedValidation),
		CandidateValidation:                evaluationReportFromResult(candidateValidation),
		Rounds:                             BuildRoundAudit(input.PromptIterRun, input.BaselineValidation, input.Config.Gate.CriticalCaseIDs),
		Delta:                              delta,
		GateDecision:                       gate,
		BaselineFailureAttributions:        baselineAttributions,
		BaselineFailureAttributionSummary:  SummarizeAttributions(baselineAttributions),
		CandidateFailureAttributions:       candidateAttributions,
		CandidateFailureAttributionSummary: SummarizeAttributions(candidateAttributions),
		FailureAttributions:                attributions,
		FailureAttributionSummary:          SummarizeAttributions(attributions),
		Cost:                               input.Cost,
		Latency:                            input.Latency,
		CandidatePrompt:                    CandidatePrompt(input.PromptIterRun),
	}
	return report
}

// ReportInput carries report-generation inputs.
type ReportInput struct {
	Ctx                 context.Context
	Config              Config
	StartedAt           time.Time
	FinishedAt          time.Time
	BaselineTrain       *promptiterengine.EvaluationResult
	BaselineValidation  *promptiterengine.EvaluationResult
	CandidateValidation *promptiterengine.EvaluationResult
	PromptIterRun       *promptiterengine.RunResult
	Attributions        []CaseAttribution
	AttributionJudge    AttributionJudge
	Metrics             []MetricDefinition
	Cost                CostSummary
	Latency             Duration
}

// BuildRoundAudit creates compact per-round audit rows.
func BuildRoundAudit(
	run *promptiterengine.RunResult,
	baselineValidation *promptiterengine.EvaluationResult,
	criticalCaseIDs []string,
) []RoundAudit {
	if run == nil {
		return nil
	}
	rounds := make([]RoundAudit, 0, len(run.Rounds))
	for _, round := range run.Rounds {
		audit := RoundAudit{
			Round:           round.Round,
			TrainScore:      scoreOf(round.Train),
			ValidationScore: scoreOf(round.Validation),
			Patches:         patchesAudit(round.Patches),
			Validation:      evaluationReportFromResult(round.Validation),
		}
		if round.Acceptance != nil {
			audit.Accepted = round.Acceptance.Accepted
			audit.Reason = round.Acceptance.Reason
		}
		delta := ComputeDelta(baselineValidation, round.Validation, criticalCaseIDs)
		audit.Delta = &delta
		rounds = append(rounds, audit)
	}
	return rounds
}

// CandidatePrompt returns the latest single text override. It returns an empty
// string for multi-surface or non-text profiles that cannot be represented by
// one prompt string.
func CandidatePrompt(run *promptiterengine.RunResult) string {
	text, err := CandidateTextPrompt(run)
	if err != nil {
		return ""
	}
	return text
}

func CandidateTextPrompt(run *promptiterengine.RunResult) (string, error) {
	if run == nil {
		return "", nil
	}
	for i := len(run.Rounds) - 1; i >= 0; i-- {
		text, err := profilePromptText(run.Rounds[i].OutputProfile)
		if err != nil {
			return "", err
		}
		if text != "" {
			return text, nil
		}
	}
	return profilePromptText(run.AcceptedProfile)
}

func profilePromptText(profile *promptiter.Profile) (string, error) {
	if profile == nil {
		return "", nil
	}
	var text string
	for _, override := range profile.Overrides {
		if override.Value.Text != nil && strings.TrimSpace(*override.Value.Text) != "" {
			if text != "" {
				return "", fmt.Errorf("candidate profile has multiple text overrides; provide profile-aware validation")
			}
			text = *override.Value.Text
			continue
		}
		if hasNonTextProfileValue(override.Value) {
			return "", fmt.Errorf("candidate profile contains non-text override for %q; provide profile-aware validation", override.SurfaceID)
		}
	}
	return text, nil
}

func hasNonTextProfileValue(value astructure.SurfaceValue) bool {
	return len(value.Skills) > 0 || len(value.Tools) > 0 || len(value.FewShot) > 0 || value.Model != nil
}

// WriteReports writes JSON and Markdown reports.
func WriteReports(report OptimizationReport, jsonPath, markdownPath string) error {
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return fmt.Errorf("create JSON report dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil {
		return fmt.Errorf("create markdown report dir: %w", err)
	}
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal optimization report: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(jsonBytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := os.WriteFile(markdownPath, []byte(RenderMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}
	return nil
}

// RenderMarkdown renders a human-readable report.
func RenderMarkdown(report OptimizationReport) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Optimization Report\n\n")
	fmt.Fprintf(&b, "- App: `%s`\n", report.Metadata.AppName)
	fmt.Fprintf(&b, "- Decision: `%t`\n", report.GateDecision.Accepted)
	fmt.Fprintf(&b, "- Baseline validation score: `%.3f`\n", reportScoreOf(report.BaselineValidation))
	fmt.Fprintf(&b, "- Accepted validation score: `%.3f`\n", reportScoreOf(report.AcceptedValidation))
	fmt.Fprintf(&b, "- Candidate validation score: `%.3f`\n", reportScoreOf(report.CandidateValidation))
	fmt.Fprintf(&b, "- Validation delta: `%+.3f`\n", report.Delta.OverallScoreDelta)
	fmt.Fprintf(&b, "- Cost: `%d` model calls", report.Cost.ModelCalls)
	if report.Cost.Estimated {
		fmt.Fprintf(&b, " (estimated)")
	}
	if report.Cost.Tokens > 0 {
		fmt.Fprintf(&b, ", `%d` tokens", report.Cost.Tokens)
	}
	if report.Cost.Amount > 0 || report.Cost.AmountMeasured {
		if strings.TrimSpace(report.Cost.Currency) == "" {
			fmt.Fprintf(&b, ", `%.4f`", report.Cost.Amount)
		} else {
			fmt.Fprintf(&b, ", `%.4f %s`", report.Cost.Amount, report.Cost.Currency)
		}
	}
	if report.Cost.Source != "" {
		fmt.Fprintf(&b, ", source `%s`", report.Cost.Source)
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "- Latency: `%s`\n\n", report.Latency.Duration)
	fmt.Fprintf(&b, "> `Accepted validation` is the last profile accepted by PromptIter. ")
	fmt.Fprintf(&b, "`Candidate validation` is the final audited candidate, even when PromptIter or the outer gate rejects it.\n\n")

	fmt.Fprintf(&b, "## Gate Decision\n\n")
	for _, reason := range report.GateDecision.Reasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Delta Summary\n\n")
	fmt.Fprintf(&b, "| newly passed | newly failed | score up | score down | unchanged |\n")
	fmt.Fprintf(&b, "| ---: | ---: | ---: | ---: | ---: |\n")
	fmt.Fprintf(&b, "| %d | %d | %d | %d | %d |\n\n",
		report.Delta.Summary.NewlyPassed,
		report.Delta.Summary.NewlyFailed,
		report.Delta.Summary.ScoreUp,
		report.Delta.Summary.ScoreDown,
		report.Delta.Summary.Unchanged,
	)

	fmt.Fprintf(&b, "## Case Delta\n\n")
	fmt.Fprintf(&b, "| eval set | case | metric | baseline | candidate | delta | kind |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | ---: | ---: | ---: | --- |\n")
	for _, item := range report.Delta.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %.3f | %.3f | %+.3f | %s |\n",
			item.EvalSetID,
			item.EvalCaseID,
			item.MetricName,
			item.BaselineScore,
			item.CandidateScore,
			item.ScoreDelta,
			item.Kind,
		)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Failure Attribution\n\n")
	fmt.Fprintf(&b, "Baseline failures: `%d`; candidate failures: `%d`; combined: `%d`.\n\n",
		report.BaselineFailureAttributionSummary.Total,
		report.CandidateFailureAttributionSummary.Total,
		report.FailureAttributionSummary.Total,
	)
	if report.BaselineFailureAttributionSummary.Total > 0 {
		fmt.Fprintf(&b, "### Baseline\n\n")
		renderAttributionSummary(&b, report.BaselineFailureAttributionSummary)
	}
	if report.CandidateFailureAttributionSummary.Total > 0 {
		fmt.Fprintf(&b, "### Candidate\n\n")
		renderAttributionSummary(&b, report.CandidateFailureAttributionSummary)
	}
	fmt.Fprintf(&b, "### Combined\n\n")
	renderAttributionSummary(&b, report.FailureAttributionSummary)
	renderAttributionDetails(&b, report.FailureAttributions)
	if report.CandidatePrompt != "" {
		title := "Accepted Prompt"
		if !report.GateDecision.Accepted {
			title = "Candidate Prompt Rejected By Gate"
		}
		fmt.Fprintf(&b, "## %s\n\n```text\n%s\n```\n", title, report.CandidatePrompt)
	}
	return b.String()
}

func renderAttributionSummary(b *bytes.Buffer, summary AttributionSummary) {
	fmt.Fprintf(b, "| category | count | secondary |\n")
	fmt.Fprintf(b, "| --- | ---: | ---: |\n")
	for _, category := range sortedCategories(summary.ByCategory) {
		fmt.Fprintf(b, "| %s | %d | %d |\n", category, summary.ByCategory[category], summary.BySecondaryCategory[category])
	}
	fmt.Fprintf(b, "\n")
}

func renderAttributionDetails(b *bytes.Buffer, attributions []CaseAttribution) {
	if len(attributions) == 0 {
		return
	}
	fmt.Fprintf(b, "### Failure Details\n\n")
	fmt.Fprintf(b, "| eval set | case | metric | category | secondary | reason | evidence |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, attr := range attributions {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			markdownCell(attr.EvalSetID),
			markdownCell(attr.EvalCaseID),
			markdownCell(attr.MetricName),
			markdownCell(string(attr.Category)),
			markdownCell(categoryList(attr.SecondaryCategories)),
			markdownCell(attr.Reason),
			markdownCell(strings.Join(attr.Evidence, "; ")),
		)
	}
	fmt.Fprintf(b, "\n")
}

func categoryList(categories []FailureCategory) string {
	if len(categories) == 0 {
		return ""
	}
	values := make([]string, 0, len(categories))
	for _, category := range categories {
		values = append(values, string(category))
	}
	return strings.Join(values, ",")
}

func markdownCell(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, "|", `\|`)
	return truncateMarkdownCell(text, 180)
}

func truncateMarkdownCell(text string, limit int) string {
	if limit <= 3 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-3]) + "..."
}

func patchesAudit(patches *promptiter.PatchSet) []PatchAudit {
	if patches == nil {
		return nil
	}
	out := make([]PatchAudit, 0, len(patches.Patches))
	for _, patch := range patches.Patches {
		audit := PatchAudit{
			SurfaceID: patch.SurfaceID,
			Reason:    patch.Reason,
		}
		if patch.Value.Text != nil {
			audit.Value = *patch.Value.Text
		}
		out = append(out, audit)
	}
	return out
}

func evaluationReportFromResult(result *promptiterengine.EvaluationResult) *EvaluationReport {
	if result == nil {
		return nil
	}
	out := &EvaluationReport{
		OverallScore: result.OverallScore,
		EvalSets:     make([]EvalSetReport, 0, len(result.EvalSets)),
	}
	for _, evalSet := range result.EvalSets {
		setReport := EvalSetReport{
			EvalSetID:    evalSet.EvalSetID,
			OverallScore: evalSet.OverallScore,
			Cases:        make([]CaseReport, 0, len(evalSet.Cases)),
		}
		for _, evalCase := range evalSet.Cases {
			caseReport := CaseReport{
				EvalSetID:  evalCase.EvalSetID,
				EvalCaseID: evalCase.EvalCaseID,
				SessionID:  evalCase.SessionID,
				Trace:      traceReportFromTrace(evalCase.Trace),
				Metrics:    make([]MetricReport, 0, len(evalCase.Metrics)),
			}
			for _, metric := range evalCase.Metrics {
				caseReport.Metrics = append(caseReport.Metrics, MetricReport{
					MetricName: metric.MetricName,
					Score:      metric.Score,
					Status:     string(metric.Status),
					Reason:     metric.Reason,
				})
			}
			setReport.Cases = append(setReport.Cases, caseReport)
		}
		out.EvalSets = append(out.EvalSets, setReport)
	}
	return out
}

func traceReportFromTrace(trace *atrace.Trace) *TraceReport {
	if trace == nil {
		return nil
	}
	out := &TraceReport{
		RootAgentName:    trace.RootAgentName,
		RootInvocationID: trace.RootInvocationID,
		SessionID:        trace.SessionID,
		Status:           string(trace.Status),
		Steps:            make([]StepReport, 0, len(trace.Steps)),
	}
	for _, step := range trace.Steps {
		out.Steps = append(out.Steps, StepReport{
			StepID:             step.StepID,
			InvocationID:       step.InvocationID,
			ParentInvocationID: step.ParentInvocationID,
			AgentName:          step.AgentName,
			Branch:             step.Branch,
			NodeID:             step.NodeID,
			PredecessorStepIDs: append([]string(nil), step.PredecessorStepIDs...),
			AppliedSurfaceIDs:  append([]string(nil), step.AppliedSurfaceIDs...),
			Input:              snapshotReportFromSnapshot(step.Input),
			Output:             snapshotReportFromSnapshot(step.Output),
			Error:              step.Error,
		})
	}
	return out
}

func snapshotReportFromSnapshot(snapshot *atrace.Snapshot) *SnapshotReport {
	if snapshot == nil {
		return nil
	}
	return &SnapshotReport{Text: snapshot.Text}
}

func reportScoreOf(result *EvaluationReport) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}

func sortedCategories(counts map[FailureCategory]int) []FailureCategory {
	categories := make([]FailureCategory, 0, len(counts))
	for category := range counts {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool {
		return categories[i] < categories[j]
	})
	return categories
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneAttributionHints(values map[string]FailureCategory) map[string]FailureCategory {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]FailureCategory, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
