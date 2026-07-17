//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package report renders machine-readable and human-reviewable optimization reports.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// JSON renders the complete audit record without recomputing decisions.
func JSON(result *regression.RunResult) ([]byte, error) {
	sanitized, err := regression.SanitizeRunResult(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sanitized)
}

// Markdown renders the same recorded evidence for human review.
func Markdown(result *regression.RunResult) ([]byte, error) {
	sanitized, err := regression.SanitizeRunResult(result)
	if err != nil {
		return nil, err
	}
	result = sanitized
	if result.Spec == nil {
		return nil, errors.New("run result spec is nil")
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Prompt optimization report: %s\n\n", result.RunID)
	fmt.Fprintf(&builder, "- Status: `%s`\n", result.Status)
	fmt.Fprintf(&builder, "- Decision: `%s`\n", result.Decision)
	builder.WriteString("- Release authority: `Regression release gate`\n")
	fmt.Fprintf(&builder, "- Selected candidate: `%s`\n", result.SelectedCandidateID)
	fmt.Fprintf(&builder, "- Input fingerprint: `%s`\n", result.Spec.InputFingerprint)
	writeRandomSeed(&builder, result.Spec.Runtime)
	fmt.Fprintf(&builder, "- Audit runs: `%d`\n", result.Spec.Runtime.NumRuns)
	fmt.Fprintf(&builder, "- Deterministic runtime: `%t`\n", result.Spec.Runtime.Deterministic)
	fmt.Fprintf(&builder, "- Started: `%s`\n", formatHumanTime(result.StartedAt))
	fmt.Fprintf(&builder, "- Finished: `%s`\n", formatHumanTime(result.EndedAt))
	fmt.Fprintf(&builder, "- Duration: `%s`\n\n", formatDuration(result.StartedAt, result.EndedAt))

	writePromptIterConfiguration(&builder, result.PromptIter)

	if len(result.Spec.Metadata) > 0 {
		builder.WriteString("## Runtime metadata\n\n")
		keys := make([]string, 0, len(result.Spec.Metadata))
		for key := range result.Spec.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&builder, "- %s: `%s`\n",
				escapeInlineText(key), escapeInlineCode(result.Spec.Metadata[key]))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Baseline\n\n")
	builder.WriteString("| Set | Score | Complete | Cases |\n|---|---:|---:|---:|\n")
	writeSnapshotRow(&builder, "train", result.BaselineTrain)
	writeSnapshotRow(&builder, "validation", result.BaselineValidation)
	builder.WriteString("\n### Baseline prompt\n\n")
	writeCodeBlock(&builder, profileText(result.BaselineProfile, result.Spec.TargetSurfaceID))
	writeProgressSummary(&builder, result)

	builder.WriteString("## Failure attribution\n\n")
	if len(result.Attributions) == 0 {
		builder.WriteString("No failed training cases.\n\n")
	} else {
		writeAttributionCounts(&builder, result.AttributionCounts)
		builder.WriteString("| Phase | Candidate | Set | Case | Category | Reason |\n")
		builder.WriteString("|---|---|---|---|---|---|\n")
		for _, attribution := range result.Attributions {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s | %s |\n",
				escapeCell(string(attribution.Phase)),
				escapeCell(attribution.CandidateID),
				escapeCell(attribution.EvalSetID),
				escapeCell(attribution.CaseID),
				escapeCell(string(attribution.Category)),
				escapeCell(attribution.Reason),
			)
		}
		builder.WriteString("\n")
	}

	candidates := append([]regression.CandidateResult(nil), result.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Candidate.Round != candidates[j].Candidate.Round {
			return candidates[i].Candidate.Round < candidates[j].Candidate.Round
		}
		return candidates[i].Candidate.ID < candidates[j].Candidate.ID
	})
	for _, candidate := range candidates {
		fmt.Fprintf(&builder, "## Candidate: %s\n\n", candidate.Candidate.ID)
		fmt.Fprintf(&builder, "PromptIter accepted: `%t`", candidate.PromptIterAccepted)
		if candidate.PromptIterReason != "" {
			fmt.Fprintf(&builder, " — %s", escapeCell(candidate.PromptIterReason))
		}
		builder.WriteString("\n\n")
		fmt.Fprintf(&builder, "Effective profile change: `%t`\n\n", candidate.ProfileChanged)
		if candidate.PromptIterShouldStop && candidate.PromptIterStopReason != "" {
			fmt.Fprintf(&builder, "PromptIter stop: `%s`\n\n",
				escapeInlineCode(candidate.PromptIterStopReason))
		} else {
			builder.WriteString("PromptIter action: `continue optimization`\n\n")
		}
		writeCodeBlock(&builder, profileText(candidate.Candidate.Profile, result.Spec.TargetSurfaceID))
		builder.WriteString("### Resources\n\n")
		builder.WriteString("| Scope | Calls | Tokens | Estimated cost | Cost known | PromptIter latency | Complete |\n")
		builder.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
		writeUsageRow(&builder, "round", candidate.RoundUsage)
		writeUsageRow(&builder, "cumulative", candidate.CumulativeUsage)
		builder.WriteString("\n")
		if candidate.TrainDelta == nil {
			builder.WriteString("Candidate training evidence is unavailable because this output profile ended the run before a later round could evaluate it as input.\n\n")
		}
		builder.WriteString("| Set | Baseline | Candidate | Weighted delta | New passes | New failures |\n")
		builder.WriteString("|---|---:|---:|---:|---:|---:|\n")
		if candidate.TrainDelta != nil {
			writeDeltaRow(&builder, "train", candidate.TrainDelta)
		}
		writeDeltaRow(&builder, "validation", candidate.ValidationDelta)
		builder.WriteString("\n### Validation case delta\n\n")
		builder.WriteString("| Set | Case | Change | Baseline pass | Candidate pass | Critical |\n")
		builder.WriteString("|---|---|---|---:|---:|---:|\n")
		if candidate.ValidationDelta != nil {
			for _, caseDelta := range candidate.ValidationDelta.Cases {
				fmt.Fprintf(&builder, "| %s | %s | %s | %t | %t | %t |\n",
					escapeCell(caseDelta.EvalSetID), escapeCell(caseDelta.CaseID), caseDelta.Kind,
					caseDelta.BaselinePassed, caseDelta.CandidatePassed, caseDelta.Critical,
				)
			}
		}
		builder.WriteString("\n### Gate\n\n")
		if candidate.Gate == nil {
			builder.WriteString("Decision evidence is missing.\n\n")
			continue
		}
		fmt.Fprintf(&builder, "Decision: `%s`\n\n", candidate.Gate.Decision)
		if len(candidate.Gate.Warnings) > 0 {
			builder.WriteString("Warnings:\n\n")
			for _, warning := range candidate.Gate.Warnings {
				fmt.Fprintf(&builder, "- %s\n", escapeCell(warning))
			}
			builder.WriteString("\n")
		}
		builder.WriteString("| Rule | Pass | Observed | Threshold | Reason |\n")
		builder.WriteString("|---|---:|---|---|---|\n")
		for _, rule := range candidate.Gate.Rules {
			fmt.Fprintf(&builder, "| %s | %t | %s | %s | %s |\n",
				escapeCell(rule.Rule), rule.Passed,
				escapeCell(formatReportValue(rule.Observed)),
				escapeCell(formatReportValue(rule.Threshold)),
				escapeCell(rule.Reason),
			)
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Usage\n\n")
	fmt.Fprintf(&builder, "Calls: %d; tokens: %d; estimated cost: %.6f (known: %t); PromptIter latency: %s; complete: %t; telemetry source: `%s`; pricing source: `%s`.\n",
		result.Usage.Calls,
		result.Usage.TotalTokens,
		result.Usage.EstimatedCost,
		result.Usage.CostKnown,
		result.Usage.PromptIterLatency,
		result.Usage.Complete,
		escapeCell(result.Usage.TelemetrySource),
		escapeCell(result.Usage.PricingSource),
	)
	return []byte(builder.String()), nil
}

func writeUsageRow(builder *strings.Builder, scope string, usage regression.UsageSummary) {
	fmt.Fprintf(builder, "| %s | %d | %d | %.6f | %t | %s | %t |\n",
		scope, usage.Calls, usage.TotalTokens, usage.EstimatedCost,
		usage.CostKnown, usage.PromptIterLatency, usage.Complete,
	)
}

func writeProgressSummary(builder *strings.Builder, result *regression.RunResult) {
	builder.WriteString("## Optimization progress\n\n")
	builder.WriteString("| Round | Validation score | Gain vs baseline | Profile changed | PromptIter action | Release gate |\n")
	builder.WriteString("|---:|---:|---:|---:|---|---|\n")
	baselineScore := 0.0
	if result.BaselineValidation != nil {
		baselineScore = result.BaselineValidation.OverallScore
	}
	fmt.Fprintf(builder, "| 0 | %s | 0 | n/a | baseline | n/a |\n", formatScore(baselineScore))
	candidates := append([]regression.CandidateResult(nil), result.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Candidate.Round < candidates[j].Candidate.Round
	})
	for _, candidate := range candidates {
		score := 0.0
		gain := 0.0
		if candidate.ValidationDelta != nil {
			score = candidate.ValidationDelta.CandidateScore
			gain = candidate.ValidationDelta.CandidateScore - candidate.ValidationDelta.BaselineScore
		}
		action := "continue optimization"
		if candidate.PromptIterShouldStop {
			action = candidate.PromptIterStopReason
		}
		gateDecision := "missing"
		if candidate.Gate != nil {
			gateDecision = string(candidate.Gate.Decision)
		}
		fmt.Fprintf(builder, "| %d | %s | %s | %t | %s | %s |\n",
			candidate.Candidate.Round,
			formatScore(score),
			formatScore(gain),
			candidate.ProfileChanged,
			escapeCell(action),
			escapeCell(gateDecision),
		)
	}
	builder.WriteString("\n")
}

func writePromptIterConfiguration(
	builder *strings.Builder,
	configuration *regression.PromptIterConfiguration,
) {
	builder.WriteString("## PromptIter execution\n\n")
	if configuration == nil {
		builder.WriteString("Effective PromptIter configuration is unavailable.\n\n")
		return
	}
	fmt.Fprintf(builder, "- Evaluation runs: `%d`\n", configuration.NumRuns)
	fmt.Fprintf(builder, "- Trace usage covers all Evaluation calls: `%t`\n",
		configuration.TraceUsageCoversAllCalls)
	fmt.Fprintf(builder, "- Hard round limit: `%d`\n", configuration.MaxRounds)
	fmt.Fprintf(builder, "- Acceptance minimum score gain: `%s`\n", formatScore(configuration.MinScoreGain))
	if configuration.MaxRoundsWithoutAcceptance <= 0 {
		builder.WriteString("- Stop after consecutive unaccepted rounds: `disabled`\n")
	} else {
		fmt.Fprintf(builder, "- Stop after consecutive unaccepted rounds: `%d`\n", configuration.MaxRoundsWithoutAcceptance)
	}
	if configuration.TargetScore == nil {
		builder.WriteString("- Early-stop target score: `disabled`\n")
	} else {
		fmt.Fprintf(builder, "- Early-stop target score: `%s`\n", formatScore(*configuration.TargetScore))
	}
	fmt.Fprintf(builder, "- Target surfaces: `%s`\n",
		escapeInlineCode(strings.Join(configuration.TargetSurfaceIDs, ", ")))
	fmt.Fprintf(builder,
		"- Evaluation parallelism: cases=`%d`, inference=`%t`, evaluation=`%t`\n",
		configuration.EvalCaseParallelism,
		configuration.EvalCaseParallelInferenceEnabled,
		configuration.EvalCaseParallelEvaluationEnabled,
	)
	fmt.Fprintf(builder,
		"- Stage parallelism: backward=`%t/%d`, aggregation=`%t/%d`, optimizer=`%t/%d`\n\n",
		configuration.BackwardCaseParallelismEnabled,
		configuration.BackwardCaseParallelism,
		configuration.AggregationSurfaceParallelismEnabled,
		configuration.AggregationSurfaceParallelism,
		configuration.OptimizerSurfaceParallelismEnabled,
		configuration.OptimizerSurfaceParallelism,
	)
}

func writeRandomSeed(builder *strings.Builder, runtime regression.RuntimePolicy) {
	if runtime.SeedApplied {
		fmt.Fprintf(builder, "- Random seed: `%d` (applied)\n", runtime.Seed)
		return
	}
	fmt.Fprintf(builder, "- Random seed: `not applied` (configured value: `%d`)\n", runtime.Seed)
}

func formatHumanTime(value time.Time) string {
	if value.IsZero() {
		return "not recorded"
	}
	return value.UTC().Format("2006-01-02 15:04:05.000 UTC")
}

func formatDuration(started, ended time.Time) string {
	if started.IsZero() || ended.IsZero() || ended.Before(started) {
		return "not recorded"
	}
	duration := ended.Sub(started)
	if duration < time.Second {
		return fmt.Sprintf("%.3f ms", float64(duration)/float64(time.Millisecond))
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.3f s", duration.Seconds())
	}
	return duration.Round(time.Millisecond).String()
}

func formatScore(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", value), "0"), ".")
}

func formatReportValue(value any) string {
	switch typed := value.(type) {
	case float64:
		return formatScore(typed)
	case float32:
		return formatScore(float64(typed))
	case time.Duration:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func writeAttributionCounts(
	builder *strings.Builder,
	counts map[regression.FailureCategory]int,
) {
	categories := make([]string, 0, len(counts))
	for category := range counts {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	builder.WriteString("| Category | Count |\n|---|---:|\n")
	for _, category := range categories {
		fmt.Fprintf(builder, "| %s | %d |\n", escapeCell(category), counts[regression.FailureCategory(category)])
	}
	builder.WriteString("\n")
}

func writeSnapshotRow(builder *strings.Builder, name string, snapshot *regression.EvaluationSnapshot) {
	if snapshot == nil {
		fmt.Fprintf(builder, "| %s | n/a | false | 0 |\n", name)
		return
	}
	fmt.Fprintf(builder, "| %s | %.6f | %t | %d |\n", name, snapshot.OverallScore, snapshot.Complete, len(snapshot.Cases))
}

func writeDeltaRow(builder *strings.Builder, name string, delta *regression.DeltaReport) {
	if delta == nil {
		fmt.Fprintf(builder, "| %s | n/a | n/a | n/a | 0 | 0 |\n", name)
		return
	}
	fmt.Fprintf(builder, "| %s | %.6f | %.6f | %.6f | %d | %d |\n",
		name, delta.BaselineScore, delta.CandidateScore, delta.WeightedScoreDelta,
		delta.NewPasses, delta.NewFailures,
	)
}

func profileText(profile *promptiter.Profile, targetSurfaceID string) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID != targetSurfaceID {
			continue
		}
		if override.Value.Text != nil {
			return strings.TrimSpace(*override.Value.Text)
		}
		if override.Value.PromptSyntax == nil && len(override.Value.FewShot) == 0 &&
			override.Value.Model == nil && len(override.Value.Tools) == 0 &&
			len(override.Value.Skills) == 0 {
			return ""
		}
		encoded, err := json.MarshalIndent(override.Value, "", "  ")
		if err != nil {
			return fmt.Sprint(override.Value)
		}
		return strings.TrimSpace(string(encoded))
	}
	return ""
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

func escapeInlineText(value string) string {
	return escapeCell(strings.ReplaceAll(value, "`", "\\`"))
}

func escapeInlineCode(value string) string {
	value = strings.ReplaceAll(value, "`", "\\`")
	return escapeCell(value)
}

func writeCodeBlock(builder *strings.Builder, value string) {
	fence := strings.Repeat("`", max(3, longestBacktickRun(value)+1))
	fmt.Fprintf(builder, "%stext\n%s\n%s\n\n", fence, value, fence)
}

func longestBacktickRun(value string) int {
	longest := 0
	current := 0
	for _, currentRune := range value {
		if currentRune == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}
