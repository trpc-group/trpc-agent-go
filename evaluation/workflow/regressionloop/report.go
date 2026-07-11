//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// OptimizationReport is the top-level audit report for the regression loop.
type OptimizationReport struct {
	// Timestamp records when the report was generated.
	Timestamp time.Time `json:"timestamp"`

	// Baseline is the baseline evaluation summary.
	Baseline *RoundSummary `json:"baseline"`

	// Candidate is the final candidate evaluation summary.
	Candidate *RoundSummary `json:"candidate,omitempty"`

	// Rounds contains per-round audit data.
	Rounds []RoundAudit `json:"rounds"`

	// GateDecision is the final accept/reject decision.
	GateDecision *GateDecision `json:"gate_decision"`

	// FailureAttributionSummary groups failures by category.
	FailureAttributionSummary []AttributionSummary `json:"failure_attribution_summary,omitempty"`

	// CaseDeltas shows per-case score changes.
	CaseDeltas []CaseDelta `json:"case_deltas,omitempty"`

	// CostSummary records token usage and cost.
	CostSummary CostSummary `json:"cost_summary"`

	// Config records the pipeline configuration.
	Config PipelineConfig `json:"config"`
}

// RoundSummary is a compact summary of one evaluation round.
type RoundSummary struct {
	OverallScore float64 `json:"overall_score"`
	TotalCases   int     `json:"total_cases"`
	PassCount    int     `json:"pass_count"`
	FailCount    int     `json:"fail_count"`
}

// RoundAudit records detailed audit information for one optimization round.
type RoundAudit struct {
	Round               int                  `json:"round"`
	TrainScore          float64              `json:"train_score"`
	ValidationScore     float64              `json:"validation_score"`
	Accepted            bool                 `json:"accepted"`
	AcceptanceReason    string               `json:"acceptance_reason"`
	FailureAttributions []AttributionSummary `json:"failure_attributions,omitempty"`
	PatchesApplied      []PatchAudit         `json:"patches_applied,omitempty"`
	OverfittingDetected bool                 `json:"overfitting_detected"`
}

// PatchAudit records one applied patch in the audit.
type PatchAudit struct {
	SurfaceID string `json:"surface_id"`
	OldValue  string `json:"old_value,omitempty"`
	NewValue  string `json:"new_value"`
	Reason    string `json:"reason"`
}

// CostSummary records cost and latency information.
type CostSummary struct {
	TotalTokens    int     `json:"total_tokens"`
	EstimatedCost  float64 `json:"estimated_cost"`
	TotalLatencyMs int64   `json:"total_latency_ms"`
	RoundsRun      int     `json:"rounds_run"`
}

// PipelineConfig records the pipeline configuration for audit purposes.
type PipelineConfig struct {
	GateConfig    GateConfig `json:"gate_config"`
	MaxRounds     int        `json:"max_rounds"`
	TrainEvalSets []string   `json:"train_eval_sets"`
	ValEvalSets   []string   `json:"validation_eval_sets"`
	ModelConfig   string     `json:"model_config,omitempty"`
	Seed          int64      `json:"seed,omitempty"`
}

// BuildOptimizationReport constructs an OptimizationReport from pipeline state.
func BuildOptimizationReport(
	cfg PipelineConfig,
	baselineSummary *RoundSummary,
	candidateSummary *RoundSummary,
	baselineRun EvalRunSummary,
	candidateRun EvalRunSummary,
	trainBaseline, trainCandidate *EvalRunSummary,
	attributions []FailureAttribution,
	rounds []RoundAudit,
	costSummary CostSummary,
) *OptimizationReport {
	gateCfg := cfg.GateConfig
	if gateCfg.MinScoreGain == 0 && !gateCfg.NoNewHardFailures &&
		gateCfg.OverfitThreshold == 0 {
		gateCfg = DefaultGateConfig()
	}

	gate := EvaluateGate(gateCfg, baselineRun, candidateRun, trainBaseline, trainCandidate)
	deltas := ComputeCaseDeltas(baselineRun, candidateRun)

	report := &OptimizationReport{
		Timestamp:                 time.Now().UTC(),
		Baseline:                  baselineSummary,
		Candidate:                 candidateSummary,
		Rounds:                    rounds,
		GateDecision:              gate,
		FailureAttributionSummary: SummarizeAttributions(attributions),
		CaseDeltas:                deltas,
		CostSummary:               costSummary,
		Config:                    cfg,
	}
	return report
}

// WriteJSONReport writes the report as a formatted JSON file.
func WriteJSONReport(path string, report *OptimizationReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdownReport writes a human-readable Markdown audit report.
func WriteMarkdownReport(path string, report *OptimizationReport) error {
	var b strings.Builder

	b.WriteString("# Optimization Audit Report\n\n")
	b.WriteString(fmt.Sprintf("**Generated**: %s\n\n", report.Timestamp.Format(time.RFC3339)))

	// Baseline vs candidate summary.
	b.WriteString("## Score Summary\n\n")
	b.WriteString("| | Overall Score | Pass | Fail |\n")
	b.WriteString("|---|---|---|---|\n")
	if report.Baseline != nil {
		b.WriteString(fmt.Sprintf("| **Baseline** | %.4f | %d | %d |\n",
			report.Baseline.OverallScore, report.Baseline.PassCount, report.Baseline.FailCount))
	}
	if report.Candidate != nil {
		b.WriteString(fmt.Sprintf("| **Candidate** | %.4f | %d | %d |\n",
			report.Candidate.OverallScore, report.Candidate.PassCount, report.Candidate.FailCount))
	}
	b.WriteString("\n")

	// Gate decision.
	b.WriteString("## Gate Decision\n\n")
	if report.GateDecision != nil {
		if report.GateDecision.Accepted {
			b.WriteString(fmt.Sprintf("**Result: ACCEPTED** — %s\n\n", report.GateDecision.Summary))
		} else {
			b.WriteString(fmt.Sprintf("**Result: REJECTED** — %s\n\n", report.GateDecision.Summary))
		}
		if report.GateDecision.OverfittingDetected {
			b.WriteString("> ⚠️ **Overfitting detected**: training score improved but validation score decreased.\n\n")
		}
		b.WriteString("### Rules\n\n")
		for _, r := range report.GateDecision.Reasons {
			icon := "✅"
			if !r.Passed {
				icon = "❌"
			}
			b.WriteString(fmt.Sprintf("- %s **%s**: %s\n", icon, r.Rule, r.Detail))
		}
		b.WriteString("\n")
	}

	// Failure attributions.
	if len(report.FailureAttributionSummary) > 0 {
		b.WriteString("## Failure Attribution\n\n")
		b.WriteString("| Category | Count | Cases |\n")
		b.WriteString("|---|---|---|\n")
		for _, a := range report.FailureAttributionSummary {
			b.WriteString(fmt.Sprintf("| %s | %d | %s |\n",
				a.Category, a.Count, strings.Join(a.Cases, ", ")))
		}
		b.WriteString("\n")
	}

	// Per-case deltas.
	if len(report.CaseDeltas) > 0 {
		b.WriteString("## Per-Case Deltas\n\n")
		b.WriteString("| Case | Baseline | Candidate | Delta | Status |\n")
		b.WriteString("|---|---|---|---|---|\n")
		// Sort by score delta ascending (biggest regressions first).
		sorted := make([]CaseDelta, len(report.CaseDeltas))
		copy(sorted, report.CaseDeltas)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].ScoreDelta < sorted[j].ScoreDelta
		})
		for _, d := range sorted {
			status := ""
			if d.IsNewFailure {
				status = "🔴 NEW FAILURE"
			} else if d.IsNewPass {
				status = "🟢 NEW PASS"
			} else if d.IsRegression {
				status = "🟠 REGRESSION"
			} else if d.IsImprovement {
				status = "🔵 IMPROVED"
			}
			b.WriteString(fmt.Sprintf("| %s | %.4f | %.4f | %+.4f | %s |\n",
				d.EvalCaseID, d.BaselineScore, d.CandidateScore, d.ScoreDelta, status))
		}
		b.WriteString("\n")
	}

	// Round history.
	if len(report.Rounds) > 0 {
		b.WriteString("## Round History\n\n")
		for _, r := range report.Rounds {
			status := "rejected"
			if r.Accepted {
				status = "accepted"
			}
			b.WriteString(fmt.Sprintf("### Round %d (%s)\n\n", r.Round, status))
			b.WriteString(fmt.Sprintf("- Train score: %.4f\n", r.TrainScore))
			b.WriteString(fmt.Sprintf("- Validation score: %.4f\n", r.ValidationScore))
			if r.OverfittingDetected {
				b.WriteString("- ⚠️ Overfitting detected\n")
			}
			if len(r.PatchesApplied) > 0 {
				b.WriteString("- Patches applied:\n")
				for _, p := range r.PatchesApplied {
					b.WriteString(fmt.Sprintf("  - `%s`: %s\n", p.SurfaceID, p.Reason))
				}
			}
			b.WriteString("\n")
		}
	}

	// Cost summary.
	b.WriteString("## Cost & Latency\n\n")
	b.WriteString(fmt.Sprintf("- Total tokens: %d\n", report.CostSummary.TotalTokens))
	b.WriteString(fmt.Sprintf("- Estimated cost: $%.4f\n", report.CostSummary.EstimatedCost))
	b.WriteString(fmt.Sprintf("- Total latency: %d ms\n", report.CostSummary.TotalLatencyMs))
	b.WriteString(fmt.Sprintf("- Rounds run: %d\n", report.CostSummary.RoundsRun))
	b.WriteString("\n")

	// Recommendation.
	b.WriteString("## Recommendation\n\n")
	if report.GateDecision != nil && report.GateDecision.Accepted {
		b.WriteString("The candidate prompt **should be accepted**. ")
		b.WriteString("It improves overall quality without introducing regressions or overfitting.\n")
	} else {
		b.WriteString("The candidate prompt **should NOT be accepted**. ")
		if report.GateDecision != nil && report.GateDecision.OverfittingDetected {
			b.WriteString("Overfitting was detected: training performance improved but validation degraded.\n")
		} else {
			b.WriteString("See the gate decision rules above for specific failure reasons.\n")
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0644)
}
