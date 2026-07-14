//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// ArtifactReferences links report sections to persisted audit artifacts.
type ArtifactReferences struct {
	InputProfile         string `json:"inputProfile,omitempty"`
	CandidateProfile     string `json:"candidateProfile,omitempty"`
	TrainEvaluation      string `json:"trainEvaluation,omitempty"`
	ValidationEvaluation string `json:"validationEvaluation,omitempty"`
	Delta                string `json:"delta,omitempty"`
	Gate                 string `json:"gate,omitempty"`
}

// MetricSummary is one metric in an audit-friendly shape.
type MetricSummary struct {
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	Status string  `json:"status"`
	Reason string  `json:"reason,omitempty"`
}

// ToolEvidence preserves actual and expected tool trajectories for audit.
type ToolEvidence struct {
	Actual   [][]*evalset.Tool `json:"actual,omitempty"`
	Expected [][]*evalset.Tool `json:"expected,omitempty"`
}

// TraceStep is the stable subset of an execution trace step.
type TraceStep struct {
	AgentName         string   `json:"agentName"`
	NodeID            string   `json:"nodeId"`
	AppliedSurfaceIDs []string `json:"appliedSurfaceIds"`
	Error             string   `json:"error,omitempty"`
}

// CaseSummary contains scores, attribution, trace, and tool evidence for one case.
type CaseSummary struct {
	CaseID         string          `json:"caseId"`
	Passed         bool            `json:"passed"`
	Score          float64         `json:"score"`
	Metrics        []MetricSummary `json:"metrics"`
	FailureReasons []FailureReason `json:"failureReasons"`
	Trace          []TraceStep     `json:"trace"`
	Tools          ToolEvidence    `json:"toolTrajectory"`
}

// EvaluationSnapshot is a stable report representation of one real evaluation.
type EvaluationSnapshot struct {
	Score     float64          `json:"score"`
	PerCase   []CaseSummary    `json:"perCase"`
	Resources ResourceSnapshot `json:"resources"`
}

// BaselineSnapshot contains the initial train and validation measurements.
type BaselineSnapshot struct {
	Train      EvaluationSnapshot `json:"train"`
	Validation EvaluationSnapshot `json:"validation"`
	Artifacts  ArtifactReferences `json:"artifacts"`
}

// DeltaBundle keeps initial, search-input, and last-released comparisons separate.
type DeltaBundle struct {
	AgainstInitial      Delta `json:"againstInitial"`
	AgainstRoundInput   Delta `json:"againstRoundInput"`
	AgainstLastReleased Delta `json:"againstLastReleased"`
}

// RoundFailureAttributionStats contains candidate failure counts for one round.
type RoundFailureAttributionStats struct {
	Round               int            `json:"round"`
	CandidateTrain      map[string]int `json:"candidateTrain"`
	CandidateValidation map[string]int `json:"candidateValidation"`
}

// FailureAttributionStats aggregates failure categories by evaluation phase.
type FailureAttributionStats struct {
	BaselineTrain      map[string]int                 `json:"baselineTrain"`
	BaselineValidation map[string]int                 `json:"baselineValidation"`
	Rounds             []RoundFailureAttributionStats `json:"rounds"`
}

// RoundReport records PromptIter acceptance separately from the release Gate.
type RoundReport struct {
	Round              int                          `json:"round"`
	PromptIterAccepted bool                         `json:"promptIterAccepted"`
	PromptIterReasons  []string                     `json:"promptIterReasons,omitempty"`
	Train              EvaluationSnapshot           `json:"train"`
	Validation         EvaluationSnapshot           `json:"validation"`
	Delta              DeltaBundle                  `json:"delta"`
	Resources          EvaluationResourceComparison `json:"resources"`
	ReleaseGate        GateDecision                 `json:"releaseGate"`
	Usage              Usage                        `json:"usage"`
	EstimatedCost      EstimatedCost                `json:"estimatedCost"`
	LatencySeconds     float64                      `json:"latencySeconds"`
	Artifacts          ArtifactReferences           `json:"artifacts"`
}

// WriteBackDecision makes recommendation and performed side effects explicit.
type WriteBackDecision struct {
	RecommendedForWriteBack bool   `json:"recommendedForWriteBack"`
	Performed               bool   `json:"performed"`
	AcceptedProfileRef      string `json:"acceptedProfileRef"`
}

// Report is the complete machine-readable optimization audit document.
type Report struct {
	Version                 int                     `json:"version"`
	Seed                    int64                   `json:"seed"`
	ModelConfig             ModelConfig             `json:"modelConfig"`
	TargetSurfaceIDs        []string                `json:"targetSurfaceIds"`
	Timing                  Timing                  `json:"timing"`
	Usage                   Usage                   `json:"usage"`
	LatencySeconds          float64                 `json:"latencySeconds"`
	EstimatedCost           EstimatedCost           `json:"estimatedCost"`
	Baseline                BaselineSnapshot        `json:"baseline"`
	Rounds                  []RoundReport           `json:"rounds"`
	FailureAttributionStats FailureAttributionStats `json:"failureAttributionStats"`
	WriteBack               WriteBackDecision       `json:"writeBack"`
}

// SummarizeEvaluation converts a real engine result and its attributions into a stable snapshot.
func SummarizeEvaluation(result *engine.EvaluationResult, reasons []CaseAttribution) EvaluationSnapshot {
	summary := EvaluationSnapshot{PerCase: []CaseSummary{}}
	if result == nil {
		return summary
	}
	summary.Score = result.OverallScore
	reasonIndex := make(map[string][]FailureReason, len(reasons))
	for _, item := range reasons {
		reasonIndex[item.CaseID] = item.Reasons
	}
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			evaluated := 0
			item := CaseSummary{
				CaseID: evalCase.EvalCaseID, Passed: len(evalCase.Metrics) > 0, Metrics: []MetricSummary{},
				FailureReasons: reasonIndex[evalCase.EvalCaseID], Trace: summarizeTrace(evalCase.Trace),
				Tools: summarizeTools(evalCase),
			}
			for _, metric := range evalCase.Metrics {
				if metric.Status != status.EvalStatusPassed {
					item.Passed = false
				}
				if metric.Status != status.EvalStatusNotEvaluated {
					item.Score += metric.Score
					evaluated++
				}
				item.Metrics = append(item.Metrics, MetricSummary{Name: metric.MetricName, Score: metric.Score, Status: string(metric.Status), Reason: metric.Reason})
			}
			if evaluated > 0 {
				item.Score /= float64(evaluated)
			}
			if item.FailureReasons == nil {
				item.FailureReasons = []FailureReason{}
			}
			sort.Slice(item.Metrics, func(i, j int) bool { return item.Metrics[i].Name < item.Metrics[j].Name })
			summary.PerCase = append(summary.PerCase, item)
		}
	}
	sort.Slice(summary.PerCase, func(i, j int) bool { return summary.PerCase[i].CaseID < summary.PerCase[j].CaseID })
	return summary
}

func summarizeTrace(trace *atrace.Trace) []TraceStep {
	out := []TraceStep{}
	if trace == nil {
		return out
	}
	for _, step := range trace.Steps {
		out = append(out, TraceStep{AgentName: step.AgentName, NodeID: step.NodeID, AppliedSurfaceIDs: append([]string(nil), step.AppliedSurfaceIDs...), Error: step.Error})
	}
	return out
}

func summarizeTools(evalCase engine.CaseResult) ToolEvidence {
	evidence := ToolEvidence{}
	for _, invocation := range evalCase.ActualInvocations {
		if invocation != nil {
			evidence.Actual = append(evidence.Actual, invocation.Tools)
		}
	}
	for _, invocation := range evalCase.ExpectedInvocations {
		if invocation != nil {
			evidence.Expected = append(evidence.Expected, invocation.Tools)
		}
	}
	return evidence
}

// JSON renders the report as indented JSON with a trailing newline.
func JSON(value *Report) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal optimization report: %w", err)
	}
	return append(payload, '\n'), nil
}

// Markdown renders the human-readable optimization audit report.
func Markdown(value *Report) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, "# PromptIter Regression Report")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Mode: `%s` (`%s`)\n\nSeed: `%d`\n\nDuration: `%.3fs`\n\n", value.ModelConfig.Mode, value.ModelConfig.Name, value.Seed, value.Timing.DurationSeconds)
	fmt.Fprintln(&out, "## Baseline")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Train score: **%.4f**\n\nValidation score: **%.4f**\n\n", value.Baseline.Train.Score, value.Baseline.Validation.Score)
	fmt.Fprintln(&out, "## Candidate Decisions")
	for _, round := range value.Rounds {
		decision := "REJECT"
		if round.ReleaseGate.Accepted {
			decision = "ACCEPT"
		}
		fmt.Fprintf(&out, "\n### Round %d: %s\n\n", round.Round, decision)
		fmt.Fprintf(&out, "PromptIter accepted: `%t`; release accepted: `%t`.\n\nTrain: `%.4f`; validation: `%.4f`.\n\n", round.PromptIterAccepted, round.ReleaseGate.Accepted, round.Train.Score, round.Validation.Score)
		fmt.Fprintf(&out, "Reasons: %s\n\n", strings.Join(round.ReleaseGate.Reasons, ", "))
		fmt.Fprintf(&out, "Validation score delta vs initial: `%+.4f`; vs search input: `%+.4f`; vs last released: `%+.4f`.\n\n", round.Delta.AgainstInitial.ScoreDelta, round.Delta.AgainstRoundInput.ScoreDelta, round.Delta.AgainstLastReleased.ScoreDelta)
		fmt.Fprintln(&out, "Validation comparison against last released profile:")
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "| Case | Baseline | Candidate | Delta | Transition |")
		fmt.Fprintln(&out, "|---|---:|---:|---:|---|")
		for _, item := range round.Delta.AgainstLastReleased.PerCase {
			fmt.Fprintf(&out, "| %s | %.4f | %.4f | %+.4f | %s |\n", item.CaseID, item.BaselineScore, item.CandidateScore, item.ScoreDelta, item.Transition)
		}
		fmt.Fprintln(&out, "\nResource comparison against last released profile:")
		fmt.Fprintln(&out, "\n| Set | Side | Model calls | Tool calls | Case runs | Latency | Cost |")
		fmt.Fprintln(&out, "|---|---|---:|---:|---:|---:|---:|")
		writeResourceComparison(&out, "Train", round.Resources.Train)
		writeResourceComparison(&out, "Validation", round.Resources.Validation)
	}
	fmt.Fprintln(&out, "\n## Failure Attribution Summary")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| Evaluation | Category | Count |")
	fmt.Fprintln(&out, "|---|---|---:|")
	writeAttributionStats(&out, "Baseline train", value.FailureAttributionStats.BaselineTrain)
	writeAttributionStats(&out, "Baseline validation", value.FailureAttributionStats.BaselineValidation)
	for _, round := range value.FailureAttributionStats.Rounds {
		writeAttributionStats(&out, fmt.Sprintf("Round %d train", round.Round), round.CandidateTrain)
		writeAttributionStats(&out, fmt.Sprintf("Round %d validation", round.Round), round.CandidateValidation)
	}
	fmt.Fprintln(&out, "\n## Write-Back Decision")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Recommended: **%t**\n\nPerformed: **%t**\n\nAccepted profile: `%s`\n", value.WriteBack.RecommendedForWriteBack, value.WriteBack.Performed, value.WriteBack.AcceptedProfileRef)
	fmt.Fprintln(&out, "\n## Usage and Cost")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Evaluation case runs: `%d`; model calls: `%d`; tool calls: `%d`; retries: `%d`.\n\nDeterministic evaluation latency: `%.4fs`.\n\nEstimated cost: `%.4f %s` (%s).\n", value.Usage.EvaluationCaseRuns, value.Usage.ModelCalls, value.Usage.ToolCalls, value.Usage.Retries, value.LatencySeconds, value.EstimatedCost.Amount, value.EstimatedCost.Currency, value.EstimatedCost.Source)
	return out.Bytes()
}

func failureStats(snapshot EvaluationSnapshot) map[string]int {
	stats := make(map[string]int)
	for _, evalCase := range snapshot.PerCase {
		seen := make(map[string]struct{})
		for _, reason := range evalCase.FailureReasons {
			if reason.Code == "" {
				continue
			}
			seen[reason.Code] = struct{}{}
		}
		for code := range seen {
			stats[code]++
		}
	}
	return stats
}

func buildFailureAttributionStats(baseline BaselineSnapshot, rounds []RoundReport) FailureAttributionStats {
	result := FailureAttributionStats{
		BaselineTrain: failureStats(baseline.Train), BaselineValidation: failureStats(baseline.Validation),
		Rounds: make([]RoundFailureAttributionStats, 0, len(rounds)),
	}
	for _, round := range rounds {
		result.Rounds = append(result.Rounds, RoundFailureAttributionStats{
			Round: round.Round, CandidateTrain: failureStats(round.Train), CandidateValidation: failureStats(round.Validation),
		})
	}
	return result
}

func writeAttributionStats(out *bytes.Buffer, label string, stats map[string]int) {
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Fprintf(out, "| %s | none | 0 |\n", label)
		return
	}
	for _, key := range keys {
		fmt.Fprintf(out, "| %s | %s | %d |\n", label, key, stats[key])
	}
}

func writeResourceComparison(out *bytes.Buffer, label string, comparison ResourceComparison) {
	writeResourceSnapshotRow(out, label, "Last released", comparison.LastReleased)
	writeResourceSnapshotRow(out, label, "Candidate", comparison.Candidate)
	delta := comparison.Delta
	fmt.Fprintf(out, "| %s | Delta | %+d | %+d | %+d | %+.4fs | %+.6f |\n", label, delta.ModelCalls, delta.ToolCalls, delta.EvaluationCaseRuns, delta.LatencySeconds, delta.EstimatedCostAmount)
}

func writeResourceSnapshotRow(out *bytes.Buffer, label, side string, snapshot ResourceSnapshot) {
	fmt.Fprintf(out, "| %s | %s | %d | %d | %d | %.4fs | %.6f |\n", label, side, snapshot.Usage.ModelCalls, snapshot.Usage.ToolCalls, snapshot.Usage.EvaluationCaseRuns, snapshot.LatencySeconds, snapshot.EstimatedCost.Amount)
}
