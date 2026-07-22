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
	"runtime"
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
	candidateAttributions := input.CandidateAttributions
	if candidateAttributions == nil && hasFinalCandidate {
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
		CandidateSurfaces:                  CandidateSurfaces(input.PromptIterRun),
	}
	return report
}

// ReportInput carries report-generation inputs.
type ReportInput struct {
	Ctx                   context.Context
	Config                Config
	StartedAt             time.Time
	FinishedAt            time.Time
	BaselineTrain         *promptiterengine.EvaluationResult
	BaselineValidation    *promptiterengine.EvaluationResult
	CandidateValidation   *promptiterengine.EvaluationResult
	PromptIterRun         *promptiterengine.RunResult
	Attributions          []CaseAttribution
	CandidateAttributions []CaseAttribution
	AttributionJudge      AttributionJudge
	Metrics               []MetricDefinition
	Cost                  CostSummary
	Latency               Duration
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
	override, err := candidateTextOverride(run)
	if err != nil {
		return ""
	}
	return override.Text
}

func CandidateTextPrompt(run *promptiterengine.RunResult) (string, error) {
	override, err := candidateTextOverride(run)
	if err != nil {
		return "", err
	}
	return override.Text, nil
}

type textOverride struct {
	SurfaceID string
	Text      string
}

func candidateTextOverride(run *promptiterengine.RunResult) (textOverride, error) {
	if run == nil {
		return textOverride{}, nil
	}
	for i := len(run.Rounds) - 1; i >= 0; i-- {
		override, err := profileTextOverride(run.Rounds[i].OutputProfile)
		if err != nil {
			return textOverride{}, err
		}
		if override.Text != "" {
			return override, nil
		}
	}
	return profileTextOverride(run.AcceptedProfile)
}

func profilePromptText(profile *promptiter.Profile) (string, error) {
	override, err := profileTextOverride(profile)
	if err != nil {
		return "", err
	}
	return override.Text, nil
}

func profileTextOverride(profile *promptiter.Profile) (textOverride, error) {
	if profile == nil {
		return textOverride{}, nil
	}
	var found textOverride
	for _, surfaceOverride := range profile.Overrides {
		if surfaceOverride.Value.Text != nil && strings.TrimSpace(*surfaceOverride.Value.Text) != "" {
			if found.Text != "" {
				return textOverride{}, fmt.Errorf("candidate profile has multiple text overrides; provide profile-aware validation")
			}
			found = textOverride{
				SurfaceID: strings.TrimSpace(surfaceOverride.SurfaceID),
				Text:      *surfaceOverride.Value.Text,
			}
			continue
		}
		if hasNonTextProfileValue(surfaceOverride.Value) {
			return textOverride{}, fmt.Errorf("candidate profile contains non-text override for %q; provide profile-aware validation", surfaceOverride.SurfaceID)
		}
	}
	return found, nil
}

func hasNonTextProfileValue(value astructure.SurfaceValue) bool {
	return len(value.Skills) > 0 || len(value.Tools) > 0 || len(value.FewShot) > 0 || value.Model != nil
}

// WriteReports writes JSON and Markdown reports.
func WriteReports(report OptimizationReport, jsonPath, markdownPath string) error {
	jsonResolved, err := normalizedReportPath(jsonPath)
	if err != nil {
		return fmt.Errorf("resolve JSON report path: %w", err)
	}
	markdownResolved, err := normalizedReportPath(markdownPath)
	if err != nil {
		return fmt.Errorf("resolve markdown report path: %w", err)
	}
	if jsonResolved == markdownResolved {
		return fmt.Errorf("JSON and markdown report paths must differ: %s", jsonResolved)
	}
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

func normalizedReportPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean, nil
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

	if len(report.CandidateSurfaces) > 0 {
		fmt.Fprintf(&b, "## Candidate Surfaces\n\n")
		renderPatchTable(&b, report.CandidateSurfaces)
	}

	if hasRoundPatches(report.Rounds) {
		fmt.Fprintf(&b, "## Round Patches\n\n")
		renderRoundPatches(&b, report.Rounds)
	}

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
		fence := markdownFence(report.CandidatePrompt)
		fmt.Fprintf(&b, "## %s\n\n%stext\n%s\n%s\n", title, fence, report.CandidatePrompt, fence)
	}
	return b.String()
}

func markdownFence(text string) string {
	longest := 0
	current := 0
	for _, r := range text {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	if longest < 3 {
		longest = 3
	}
	return strings.Repeat("`", longest+1)
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

func hasRoundPatches(rounds []RoundAudit) bool {
	for _, round := range rounds {
		if len(round.Patches) > 0 {
			return true
		}
	}
	return false
}

func renderRoundPatches(b *bytes.Buffer, rounds []RoundAudit) {
	for _, round := range rounds {
		if len(round.Patches) == 0 {
			continue
		}
		fmt.Fprintf(b, "### Round %d\n\n", round.Round)
		renderPatchTable(b, round.Patches)
	}
}

func renderPatchTable(b *bytes.Buffer, patches []PatchAudit) {
	fmt.Fprintf(b, "| surface | value | reason |\n")
	fmt.Fprintf(b, "| --- | --- | --- |\n")
	for _, patch := range patches {
		fmt.Fprintf(b, "| %s | %s | %s |\n",
			markdownCell(patch.SurfaceID),
			markdownCell(patchValueSummary(patch.Value)),
			markdownCell(patch.Reason),
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
			Value:     patchValueAudit(patch.Value),
		}
		out = append(out, audit)
	}
	return out
}

// CandidateSurfaces returns the final candidate profile as structured audit
// rows so non-text surfaces are visible in both JSON and Markdown reports.
func CandidateSurfaces(run *promptiterengine.RunResult) []PatchAudit {
	profile := finalCandidateProfile(run)
	if profile == nil || len(profile.Overrides) == 0 {
		return nil
	}
	out := make([]PatchAudit, 0, len(profile.Overrides))
	for _, override := range profile.Overrides {
		out = append(out, PatchAudit{
			SurfaceID: override.SurfaceID,
			Value:     patchValueAudit(override.Value),
		})
	}
	return out
}

func patchValueAudit(value astructure.SurfaceValue) *PatchValueAudit {
	audit := &PatchValueAudit{}
	if value.Text != nil {
		text := *value.Text
		audit.Text = &text
	}
	if value.PromptSyntax != nil {
		audit.PromptSyntax = string(*value.PromptSyntax)
	}
	if len(value.FewShot) > 0 {
		audit.FewShot = make([]PatchFewShotAudit, 0, len(value.FewShot))
		for _, example := range value.FewShot {
			audit.FewShot = append(audit.FewShot, patchFewShotAudit(example))
		}
	}
	if value.Model != nil {
		audit.Model = &PatchModelAudit{
			Provider: value.Model.Provider,
			Name:     value.Model.Name,
			Variant:  value.Model.Variant,
			BaseURL:  value.Model.BaseURL,
		}
	}
	if len(value.Tools) > 0 {
		audit.Tools = make([]PatchToolAudit, 0, len(value.Tools))
		for _, tool := range value.Tools {
			audit.Tools = append(audit.Tools, PatchToolAudit{
				ID:          tool.ID,
				Description: tool.Description,
			})
		}
	}
	if len(value.Skills) > 0 {
		audit.Skills = make([]PatchSkillAudit, 0, len(value.Skills))
		for _, skill := range value.Skills {
			audit.Skills = append(audit.Skills, PatchSkillAudit{
				ID:          skill.ID,
				Description: skill.Description,
			})
		}
	}
	if audit.Text == nil && audit.PromptSyntax == "" && len(audit.FewShot) == 0 && audit.Model == nil && len(audit.Tools) == 0 && len(audit.Skills) == 0 {
		return nil
	}
	return audit
}

func patchFewShotAudit(example astructure.FewShotExample) PatchFewShotAudit {
	audit := PatchFewShotAudit{
		Messages: make([]PatchFewShotMessageAudit, 0, len(example.Messages)),
	}
	for _, message := range example.Messages {
		audit.Messages = append(audit.Messages, PatchFewShotMessageAudit{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	return audit
}

func patchValueSummary(value *PatchValueAudit) string {
	if value == nil {
		return ""
	}
	if value.Text != nil && strings.TrimSpace(*value.Text) != "" {
		return *value.Text
	}
	if len(value.Tools) > 0 {
		items := make([]string, 0, len(value.Tools))
		for _, tool := range value.Tools {
			item := strings.TrimSpace(tool.ID)
			if strings.TrimSpace(tool.Description) != "" {
				if item != "" {
					item += ": "
				}
				item += strings.TrimSpace(tool.Description)
			}
			items = append(items, item)
		}
		return "tool " + strings.Join(items, "; ")
	}
	if len(value.Skills) > 0 {
		items := make([]string, 0, len(value.Skills))
		for _, skill := range value.Skills {
			item := strings.TrimSpace(skill.ID)
			if strings.TrimSpace(skill.Description) != "" {
				if item != "" {
					item += ": "
				}
				item += strings.TrimSpace(skill.Description)
			}
			items = append(items, item)
		}
		return "skill " + strings.Join(items, "; ")
	}
	if len(value.FewShot) > 0 {
		messages := make([]string, 0, len(value.FewShot))
		for _, example := range value.FewShot {
			parts := make([]string, 0, len(example.Messages))
			for _, message := range example.Messages {
				parts = append(parts, strings.TrimSpace(message.Role)+": "+strings.TrimSpace(message.Content))
			}
			messages = append(messages, strings.Join(parts, " | "))
		}
		return "few-shot " + strings.Join(messages, "; ")
	}
	if value.Model != nil {
		parts := []string{value.Model.Provider, value.Model.Name, value.Model.Variant, value.Model.BaseURL}
		clean := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				clean = append(clean, strings.TrimSpace(part))
			}
		}
		return "model " + strings.Join(clean, " / ")
	}
	if strings.TrimSpace(value.PromptSyntax) != "" {
		return "prompt syntax " + value.PromptSyntax
	}
	return ""
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
