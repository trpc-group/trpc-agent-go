//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Auditer persists per-round records and final JSON/Markdown reports.
type Auditer struct {
	outputDir string
}

// NewAuditer builds an auditer writing files under outputDir.
func NewAuditer(outputDir string) *Auditer {
	return &Auditer{outputDir: outputDir}
}

// WriteJSONReport writes optimization_report.json.
func (a *Auditer) WriteJSONReport(report *OptimizationReport) (string, error) {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir output dir: %w", err)
	}
	path := filepath.Join(a.outputDir, "optimization_report.json")
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report json: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("write report json: %w", err)
	}
	return path, nil
}

// WriteMarkdownReport writes optimization_report.md with human-friendly sections.
func (a *Auditer) WriteMarkdownReport(report *OptimizationReport) (string, error) {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir output dir: %w", err)
	}
	path := filepath.Join(a.outputDir, "optimization_report.md")
	md := renderMarkdown(report)
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("write report md: %w", err)
	}
	return path, nil
}

func renderMarkdown(r *OptimizationReport) string {
	var sb strings.Builder
	sb.WriteString("# Optimization Report\n\n")
	sb.WriteString(fmt.Sprintf("- **App**: `%s`\n", r.AppName))
	sb.WriteString(fmt.Sprintf("- **Mode**: `%s`  \n", r.Mode))
	sb.WriteString(fmt.Sprintf("- **Pipeline version**: `%s`  \n", r.PipelineVersion))
	sb.WriteString(fmt.Sprintf("- **Random seed**: `%d`  \n", r.RandomSeed))
	sb.WriteString(fmt.Sprintf("- **Window**: %s → %s  \n",
		r.StartedAt.Format(time.RFC3339),
		r.FinishedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("- **Duration**: %.2fs  \n", r.FinishedAt.Sub(r.StartedAt).Seconds()))
	sb.WriteString(fmt.Sprintf("- **Final accepted**: `%t`  \n", r.FinalAccepted))
	sb.WriteString(fmt.Sprintf("- **Best validation score**: `%.4f` (round %d)  \n",
		r.BestValidationScore, r.BestRound))
	sb.WriteString(fmt.Sprintf("- **Final validation score**: `%.4f`  \n\n", r.FinalValidationScore))

	sb.WriteString("## Gate configuration\n\n")
	sb.WriteString(fmt.Sprintf("- min_validation_score_gain: `%+.4f`\n", r.GateConfig.MinValidationScoreGain))
	sb.WriteString(fmt.Sprintf("- allow_new_hard_fail: `%t`\n", r.GateConfig.AllowNewHardFail))
	sb.WriteString(fmt.Sprintf("- key_case_ids: `%v`\n", r.GateConfig.KeyCaseIDs))
	sb.WriteString(fmt.Sprintf("- max_cost_budget_usd: `%.4f`\n\n", r.GateConfig.MaxCostBudget))

	sb.WriteString("## Baseline evaluation\n\n")
	sb.WriteString(renderEvalSummaryMd("Train (baseline)", r.BaselineTrain))
	sb.WriteString(renderEvalSummaryMd("Validation (baseline)", r.BaselineVal))

	sb.WriteString("## Optimization rounds\n\n")
	for i, round := range r.Rounds {
		sb.WriteString(fmt.Sprintf("### Round %d\n\n", round.Round))
		_ = i
		sb.WriteString(fmt.Sprintf("- timestamp: %s\n", round.Timestamp.Format(time.RFC3339)))
		sb.WriteString(fmt.Sprintf("- seed: %d\n", round.RandomSeed))
		if round.Candidate != nil {
			sb.WriteString(fmt.Sprintf("- candidate: `%s` (by `%s`)  \n",
				round.Candidate.CandidateID, round.Candidate.GeneratedBy))
			for s := range round.Candidate.Patches {
				sb.WriteString(fmt.Sprintf("  - patched surface `%s`  \n", s))
			}
			sb.WriteString(fmt.Sprintf("- rationale: %s\n\n", round.Candidate.Rationale))
		}
		if round.ValCandidate != nil {
			sb.WriteString(renderEvalSummaryMd("Validation (candidate)", round.ValCandidate))
		}
		if round.Acceptance != nil {
			verb := "ACCEPTED"
			if !round.Acceptance.Accepted {
				verb = "REJECTED"
			}
			sb.WriteString(fmt.Sprintf("#### Acceptance: **%s** (score_delta=%+.4f)\n\n",
				verb, round.Acceptance.ScoreDelta))
			for _, reason := range round.Acceptance.Reasons {
				sb.WriteString(fmt.Sprintf("- %s\n", reason))
			}
			sb.WriteString("\nPer-case deltas:\n\n")
			sb.WriteString("| Case | Baseline | Cand | Δ | NewHardFail | KeyDegrade |\n")
			sb.WriteString("|---|---|---|---|---|---|\n")
			for _, d := range round.Acceptance.PerCaseDelta {
				sb.WriteString(fmt.Sprintf("| %s | %.2f / %t | %.2f / %t | %+.2f | %t | %t |\n",
					d.EvalCaseID,
					d.BaselineScore, d.BaselinePassed,
					d.CandidateScore, d.CandidatePassed,
					d.ScoreDelta,
					d.IsHardFailNew, d.IsKeyCaseDegrade))
			}
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("- cost: tokens=%d, usd=$%.5f, wall=%.2fs\n\n",
			round.Cost.TotalTokens, round.Cost.EstimatedCostUSD,
			round.Cost.WallClockDuration.Seconds()))
	}

	sb.WriteString("## Final prompts\n\n")
	for k, v := range r.PromptsFinal {
		sb.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n\n", k, v))
	}

	sb.WriteString("## Notes\n\n")
	for _, n := range r.Notes {
		sb.WriteString(fmt.Sprintf("- %s\n", n))
	}
	if len(r.Notes) == 0 {
		sb.WriteString("- (no operator annotations)\n")
	}
	return sb.String()
}

func renderEvalSummaryMd(title string, s *EvalSummary) string {
	if s == nil {
		return fmt.Sprintf("#### %s: (no result)\n\n", title)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("#### %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- score: **%.4f**  \n", s.OverallScore))
	sb.WriteString(fmt.Sprintf("- cases: total=%d / pass=%d / fail=%d\n\n",
		s.TotalCases, s.PassedCases, s.FailedCases))
	sb.WriteString("| Case | Pass | Score | Reason |\n|---|---|---|---|\n")
	for _, c := range s.PerCase {
		score := 0.0
		reason := ""
		for _, m := range c.Metrics {
			score = m.Score
			if !m.Passed {
				reason = truncate(m.Reason, 80)
				break
			}
		}
		sb.WriteString(fmt.Sprintf("| %s | %t | %.2f | %s |\n",
			c.EvalCaseID, c.OverallPassed, score, reason))
	}
	sb.WriteString("\n")
	if len(s.Attribution) > 0 {
		sb.WriteString("Failure attribution:\n\n")
		sb.WriteString("| Case | Metric | Category | Reason |\n|---|---|---|---|\n")
		for _, a := range s.Attribution {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				a.EvalCaseID, a.MetricName, a.Category, truncate(a.Reason, 80)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
