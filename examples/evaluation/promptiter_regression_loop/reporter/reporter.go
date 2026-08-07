//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reporter handles generating structured JSON and human-readable Markdown audit reports.
package reporter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/attribution"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/gates"
)

// RoundSummary holds the results for one optimization round.
type RoundSummary struct {
	RoundIndex    int                         `json:"round_index"`
	TargetSurface string                      `json:"target_surface"`
	ProposedPatch string                      `json:"proposed_patch"`
	TrainScore    float64                     `json:"train_score"`
	ValScore      float64                     `json:"val_score"`
	GateDecision  gates.GateDecision          `json:"gate_decision"`
	Attributions  []attribution.FailureDetail `json:"attributions"`
}

// AuditReport is the full optimization report structure.
type AuditReport struct {
	Timestamp           string         `json:"timestamp"`
	Mode                string         `json:"mode"`
	BaselineTrainScore  float64        `json:"baseline_train_score"`
	BaselineValScore    float64        `json:"baseline_val_score"`
	BestValScore        float64        `json:"best_val_score"`
	OverallAccepted     bool           `json:"overall_accepted"`
	TotalCostUSD        float64        `json:"total_cost_usd"`
	TotalDurationSec    float64        `json:"total_duration_sec"`
	Rounds              []RoundSummary `json:"rounds"`
	FinalRecommendation string         `json:"final_recommendation"`
}

// GenerateReports outputs optimization_report.json and optimization_report.md in outputDir.
func GenerateReports(outputDir string, report AuditReport) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// 1. JSON Report
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON report: %w", err)
	}
	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("failed to write JSON report: %w", err)
	}

	// 2. Markdown Report
	var sb strings.Builder
	sb.WriteString("# Optimization & Regression Audit Report\n\n")
	sb.WriteString(fmt.Sprintf("**Execution Mode**: `%s`  \n", report.Mode))
	sb.WriteString(fmt.Sprintf("**Overall Status**: %s  \n", formatStatus(report.OverallAccepted)))
	sb.WriteString(fmt.Sprintf("**Baseline Val Score**: `%.4f`  \n", report.BaselineValScore))
	sb.WriteString(fmt.Sprintf("**Best Candidate Val Score**: `%.4f` (Delta: `%+.4f`)  \n", report.BestValScore, report.BestValScore-report.BaselineValScore))
	sb.WriteString(fmt.Sprintf("**Total Cost**: `$%.4f` | **Total Duration**: `%.2fs`  \n\n", report.TotalCostUSD, report.TotalDurationSec))

	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", report.FinalRecommendation))

	sb.WriteString("## Round History & Gate Decisions\n\n")
	sb.WriteString("| Round | Target Surface | Val Score | Score Delta | Gate Decision | Reason |\n")
	sb.WriteString("|-------|----------------|-----------|-------------|---------------|--------|\n")
	for _, r := range report.Rounds {
		acceptedStr := "❌ REJECTED"
		if r.GateDecision.Accepted {
			acceptedStr = "✅ ACCEPTED"
		}
		sb.WriteString(fmt.Sprintf("| %d | `%s` | `%.4f` | `%+.4f` | %s | %s |\n",
			r.RoundIndex, r.TargetSurface, r.ValScore, r.GateDecision.ValScoreDelta, acceptedStr, r.GateDecision.Reason))
	}
	sb.WriteString("\n")

	sb.WriteString("## Failure Attribution Summary\n\n")
	sb.WriteString("| Round | Case ID | Category | Severity | Explanation |\n")
	sb.WriteString("|-------|---------|----------|----------|-------------|\n")
	for _, r := range report.Rounds {
		for _, attr := range r.Attributions {
			if attr.Category != attribution.None {
				sb.WriteString(fmt.Sprintf("| %d | `%s` | `%s` | `%s` | %s |\n",
					r.RoundIndex, attr.CaseID, attr.Category, attr.Severity, attr.Explanation))
			}
		}
	}
	sb.WriteString("\n")

	mdPath := filepath.Join(outputDir, "optimization_report.md")
	if err := os.WriteFile(mdPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write Markdown report: %w", err)
	}

	return nil
}

func formatStatus(accepted bool) string {
	if accepted {
		return "🟢 **PROMOTED (Accepted)**"
	}
	return "🔴 **REJECTED (No Promoted Candidate)**"
}
