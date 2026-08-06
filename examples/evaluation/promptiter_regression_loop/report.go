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
	"fmt"
	"os"
	"strings"
)

// OptimizationReport is the structured audit artifact emitted by the pipeline.
// It records the baseline, every optimization round, the gate decision, failure
// attribution, cost/latency and the final write-back recommendation so the whole
// loop is reproducible and reviewable.
type OptimizationReport struct {
	Pipeline       PipelineInfo     `json:"pipeline"`
	Input          InputInfo        `json:"input"`
	Baseline       BaselineInfo     `json:"baseline"`
	Optimization   OptimizationInfo `json:"optimization"`
	Gate           GateInfo         `json:"gate"`
	Attribution    AttributionInfo  `json:"attribution"`
	Cost           CostInfo         `json:"cost"`
	Recommendation string           `json:"recommendation"`
}

// PipelineInfo records run-level audit metadata.
type PipelineInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	RunID      string `json:"runId"`
	StartedAt  string `json:"startedAt"`
	DurationMs int64  `json:"durationMs"`
	RandomSeed int64  `json:"randomSeed"`
	Model      struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
	} `json:"model"`
}

// InputInfo records the pipeline inputs.
type InputInfo struct {
	TrainEvalSetID      string `json:"trainEvalSetId"`
	ValidationEvalSetID string `json:"validationEvalSetId"`
	MetricFileID        string `json:"metricFileId"`
	BaselinePromptFile  string `json:"baselinePromptFile"`
	TargetSurfaceID     string `json:"targetSurfaceId"`
	MaxRounds           int    `json:"maxRounds"`
	BaselinePrompt      string `json:"baselinePrompt"`
}

// BaselineInfo records the baseline evaluation on train and validation.
type BaselineInfo struct {
	TrainScore      float64      `json:"trainScore"`
	ValidationScore float64      `json:"validationScore"`
	TrainCases      []CaseReport `json:"trainCases,omitempty"`
	ValidationCases []CaseReport `json:"validationCases,omitempty"`
}

// CaseReport is the per-case audit record.
type CaseReport struct {
	EvalCaseID  string           `json:"evalCaseId"`
	Passed      bool             `json:"passed"`
	Score       float64          `json:"score"`
	Metrics     []MetricReport   `json:"metrics"`
	Attribution *CaseAttribution `json:"attribution,omitempty"`
}

// MetricReport is the per-metric audit record.
type MetricReport struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Passed     bool    `json:"passed"`
	Reason     string  `json:"reason,omitempty"`
}

// OptimizationInfo records every optimization round.
type OptimizationInfo struct {
	Rounds []RoundReport `json:"rounds"`
	// FinalAcceptedRound is the one-based round whose candidate was accepted; 0 means none.
	FinalAcceptedRound   int    `json:"finalAcceptedRound"`
	FinalCandidatePrompt string `json:"finalCandidatePrompt"`
}

// RoundReport records one optimization round's audit trail.
type RoundReport struct {
	Round           int             `json:"round"`
	CandidatePrompt string          `json:"candidatePrompt"`
	TrainScore      float64         `json:"trainScore"`
	ValidationScore float64         `json:"validationScore"`
	EngineAccepted  bool            `json:"engineAccepted"`
	EngineReason    string          `json:"engineReason,omitempty"`
	GateAccepted    bool            `json:"gateAccepted"`
	GateReason      string          `json:"gateReason,omitempty"`
	DeltaSummary    DeltaSummary    `json:"deltaSummary"`
	Deltas          []DeltaReport   `json:"deltas,omitempty"`
	Attribution     AttributionInfo `json:"attribution"`
	ModelCalls      int             `json:"modelCalls"`
}

// DeltaReport records one per-case delta in the audit trail.
type DeltaReport struct {
	EvalCaseID     string       `json:"evalCaseId"`
	BaselineScore  float64      `json:"baselineScore"`
	CandidateScore float64      `json:"candidateScore"`
	Delta          float64      `json:"delta"`
	Outcome        DeltaOutcome `json:"outcome"`
}

// GateInfo records the final gate decision and each check.
type GateInfo struct {
	Accepted bool        `json:"accepted"`
	Reason   string      `json:"reason"`
	Checks   []GateCheck `json:"checks"`
}

// AttributionInfo records the failure attribution distribution.
type AttributionInfo struct {
	Distribution map[FailureCategory]int `json:"distribution"`
	Count        int                     `json:"count"`
	Coverage     float64                 `json:"coverage"`
	Cases        []CaseAttribution       `json:"cases,omitempty"`
}

// CostInfo records cost and latency.
type CostInfo struct {
	ModelCalls     int   `json:"modelCalls"`
	TotalLatencyMs int64 `json:"totalLatencyMs"`
	MaxModelCalls  int   `json:"maxModelCalls"`
	MaxLatencyMs   int64 `json:"maxLatencyMs"`
	WithinBudget   bool  `json:"withinBudget"`
}

// BuildReport assembles the audit report from the pipeline result.
func BuildReport(pr *PipelineResult) *OptimizationReport {
	report := &OptimizationReport{
		Pipeline: PipelineInfo{
			Name:       "promptiter_regression_loop",
			Version:    "1.0.0",
			RunID:      pr.RunID,
			StartedAt:  pr.StartedAt,
			DurationMs: pr.DurationMs,
			RandomSeed: pr.Config.Model.Seed,
		},
		Input: InputInfo{
			TrainEvalSetID:      pr.Config.TrainEvalSetID,
			ValidationEvalSetID: pr.Config.ValidationEvalSetID,
			MetricFileID:        pr.Config.MetricFileID,
			BaselinePromptFile:  pr.Config.BaselinePromptFile,
			TargetSurfaceID:     pr.Config.TargetSurfaceID,
			MaxRounds:           pr.Config.MaxRounds,
			BaselinePrompt:      pr.BaselinePrompt,
		},
		Baseline: BaselineInfo{
			TrainScore:      scoreOf(pr.BaselineTrain),
			ValidationScore: scoreOf(pr.BaselineValidation),
			TrainCases:      buildCaseReports(pr.BaselineTrain),
			ValidationCases: buildCaseReports(pr.BaselineValidation),
		},
		Attribution: buildAttributionInfo(pr.BaselineAttribution),
		Cost: CostInfo{
			ModelCalls:     pr.ModelCalls,
			TotalLatencyMs: pr.LatencyMs,
			MaxModelCalls:  pr.Config.Gate.MaxModelCalls,
			MaxLatencyMs:   pr.Config.Gate.MaxLatencyMs,
			WithinBudget: pr.ModelCalls <= pr.Config.Gate.MaxModelCalls &&
				pr.LatencyMs <= pr.Config.Gate.MaxLatencyMs,
		},
		Recommendation: pr.Recommendation,
	}
	report.Pipeline.Model.Provider = pr.Config.Model.Provider
	report.Pipeline.Model.Name = pr.Config.Model.Name
	report.Optimization.Rounds = make([]RoundReport, 0, len(pr.Rounds))
	for _, round := range pr.Rounds {
		report.Optimization.Rounds = append(report.Optimization.Rounds, RoundReport{
			Round:           round.Round,
			CandidatePrompt: round.CandidatePrompt,
			TrainScore:      scoreOf(round.Train),
			ValidationScore: scoreOf(round.Validation),
			EngineAccepted:  round.EngineAccepted,
			EngineReason:    round.EngineReason,
			GateAccepted:    round.GateAccepted,
			GateReason:      round.GateReason,
			DeltaSummary:    SummarizeDeltas(round.Deltas),
			Deltas:          buildDeltaReports(round.Deltas),
			Attribution:     buildAttributionInfo(round.Attribution),
			ModelCalls:      round.ModelCalls,
		})
	}
	report.Optimization.FinalAcceptedRound = pr.FinalAcceptedRound
	report.Optimization.FinalCandidatePrompt = pr.FinalCandidatePrompt
	if pr.FinalGate != nil {
		report.Gate.Accepted = pr.FinalGate.Accepted
		report.Gate.Reason = pr.FinalGate.Reason
		report.Gate.Checks = append([]GateCheck(nil), pr.FinalGate.Checks...)
	}
	return report
}

// scoreOf safely extracts the overall score of a normalized eval result.
func scoreOf(result *EvalResult) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}

// buildCaseReports converts normalized cases into audit records with attribution.
func buildCaseReports(result *EvalResult) []CaseReport {
	if result == nil {
		return nil
	}
	reports := make([]CaseReport, 0, len(result.Cases))
	for _, caseScore := range result.Cases {
		report := CaseReport{
			EvalCaseID: caseScore.EvalCaseID,
			Passed:     caseScore.Passed,
			Score:      caseScore.Score,
			Metrics:    make([]MetricReport, 0, len(caseScore.Metrics)),
		}
		for _, metric := range caseScore.Metrics {
			report.Metrics = append(report.Metrics, MetricReport{
				MetricName: metric.MetricName,
				Score:      metric.Score,
				Passed:     metric.Passed,
				Reason:     metric.Reason,
			})
		}
		if attr := AttributeCase(caseScore); attr != nil {
			report.Attribution = attr
		}
		reports = append(reports, report)
	}
	return reports
}

// buildDeltaReports converts per-case deltas into audit records.
func buildDeltaReports(deltas []CaseDelta) []DeltaReport {
	reports := make([]DeltaReport, 0, len(deltas))
	for _, delta := range deltas {
		reports = append(reports, DeltaReport{
			EvalCaseID:     delta.EvalCaseID,
			BaselineScore:  delta.BaselineScore,
			CandidateScore: delta.CandidateScore,
			Delta:          delta.Delta,
			Outcome:        delta.Outcome,
		})
	}
	return reports
}

// buildAttributionInfo summarizes the attribution list. Coverage is the fraction
// of failed cases assigned to a concrete, explainable category (not "other").
func buildAttributionInfo(attributions []CaseAttribution) AttributionInfo {
	distribution := AttributionDistribution(attributions)
	info := AttributionInfo{
		Distribution: distribution,
		Count:        len(attributions),
		Cases:        attributions,
	}
	explained := 0
	for _, attr := range attributions {
		if attr.Category != CategoryOther {
			explained++
		}
	}
	if len(attributions) > 0 {
		info.Coverage = float64(explained) / float64(len(attributions))
	}
	return info
}

// WriteJSONReport serializes the report to JSON.
func WriteJSONReport(report *OptimizationReport, path string) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write report %q: %w", path, err)
	}
	return nil
}

// WriteMarkdownReport renders the report as human-readable markdown.
func WriteMarkdownReport(report *OptimizationReport, path string) error {
	var builder strings.Builder
	writeReportMarkdown(&builder, report)
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("write markdown report %q: %w", path, err)
	}
	return nil
}

// writeReportMarkdown renders the markdown body.
func writeReportMarkdown(builder *strings.Builder, report *OptimizationReport) {
	fmt.Fprintf(builder, "# Evaluation + Optimization 回归闭环报告\n\n")
	fmt.Fprintf(builder, "| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(builder, "| Run ID | `%s` |\n", report.Pipeline.RunID)
	fmt.Fprintf(builder, "| 开始时间 | %s |\n", report.Pipeline.StartedAt)
	fmt.Fprintf(builder, "| 耗时 | %d ms |\n", report.Pipeline.DurationMs)
	fmt.Fprintf(builder, "| 随机种子 | %d |\n", report.Pipeline.RandomSeed)
	fmt.Fprintf(builder, "| 模型 | %s (%s) |\n", report.Pipeline.Model.Name, report.Pipeline.Model.Provider)
	fmt.Fprintf(builder, "| 目标 surface | `%s` |\n", report.Input.TargetSurfaceID)

	fmt.Fprintf(builder, "\n## 1. 基线评测\n\n")
	fmt.Fprintf(builder, "| 数据集 | 总分 |\n|---|---|\n")
	fmt.Fprintf(builder, "| 训练集 | %.3f |\n", report.Baseline.TrainScore)
	fmt.Fprintf(builder, "| 验证集 | %.3f |\n", report.Baseline.ValidationScore)

	fmt.Fprintf(builder, "\n### 验证集逐 case 基线\n\n")
	fmt.Fprintf(builder, "| Case | 通过 | 分数 | 失败归因 |\n|---|---|---|---|\n")
	for _, caseReport := range report.Baseline.ValidationCases {
		attribution := "-"
		if caseReport.Attribution != nil {
			attribution = string(caseReport.Attribution.Category)
		}
		fmt.Fprintf(builder, "| `%s` | %v | %.3f | %s |\n", caseReport.EvalCaseID, caseReport.Passed, caseReport.Score, attribution)
	}

	fmt.Fprintf(builder, "\n## 2. 优化轮次\n\n")
	if len(report.Optimization.Rounds) == 0 {
		builder.WriteString("_没有执行任何优化轮次。_\n")
	} else {
		fmt.Fprintf(builder, "| 轮次 | 训练分 | 验证分 | Engine 接受 | Gate 接受 | 接受原因 |\n|---|---|---|---|---|---|\n")
		for _, round := range report.Optimization.Rounds {
			fmt.Fprintf(builder, "| %d | %.3f | %.3f | %v | %v | %s |\n",
				round.Round, round.TrainScore, round.ValidationScore,
				round.EngineAccepted, round.GateAccepted, round.GateReason)
		}
		fmt.Fprintf(builder, "\n### 逐 case delta(相对基线验证集)\n\n")
		for _, round := range report.Optimization.Rounds {
			fmt.Fprintf(builder, "**Round %d**(训练 %.3f → 验证 %.3f)\n\n", round.Round, round.TrainScore, round.ValidationScore)
			fmt.Fprintf(builder, "| Case | 基线分 | 候选分 | Δ | 结果 |\n|---|---|---|---|---|\n")
			for _, delta := range round.Deltas {
				fmt.Fprintf(builder, "| `%s` | %.3f | %.3f | %+.3f | %s |\n",
					delta.EvalCaseID, delta.BaselineScore, delta.CandidateScore, delta.Delta, delta.Outcome)
			}
			builder.WriteString("\n")
		}
	}

	fmt.Fprintf(builder, "\n## 3. 接受门禁(Gate)\n\n")
	fmt.Fprintf(builder, "**最终决策:%s**\n\n", acceptLabel(report.Gate.Accepted))
	fmt.Fprintf(builder, "%s\n\n", report.Gate.Reason)
	fmt.Fprintf(builder, "| 检查项 | 结果 | 详情 |\n|---|---|---|\n")
	for _, check := range report.Gate.Checks {
		fmt.Fprintf(builder, "| %s | %s | %s |\n", check.Name, passLabel(check.Passed), check.Detail)
	}

	fmt.Fprintf(builder, "\n## 4. 失败归因\n\n")
	if report.Attribution.Count == 0 {
		builder.WriteString("_没有失败 case。_\n")
	} else {
		fmt.Fprintf(builder, "共 %d 个失败 case,覆盖 %d 个类别:\n\n", report.Attribution.Count, len(report.Attribution.Distribution))
		fmt.Fprintf(builder, "| 类别 | 数量 |\n|---|---|\n")
		for _, entry := range sortedCategories(report.Attribution.Distribution) {
			fmt.Fprintf(builder, "| %s | %d |\n", entry.Category, entry.Count)
		}
	}

	fmt.Fprintf(builder, "\n## 5. 成本与预算\n\n")
	fmt.Fprintf(builder, "| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(builder, "| 模型调用次数 | %d / %d |\n", report.Cost.ModelCalls, report.Cost.MaxModelCalls)
	fmt.Fprintf(builder, "| 总耗时 | %d ms / %d ms |\n", report.Cost.TotalLatencyMs, report.Cost.MaxLatencyMs)
	fmt.Fprintf(builder, "| 预算内 | %v |\n", report.Cost.WithinBudget)

	fmt.Fprintf(builder, "\n## 6. 优化建议\n\n%s\n", report.Recommendation)
}

// acceptLabel renders accept/reject for display.
func acceptLabel(accepted bool) string {
	if accepted {
		return "接受候选"
	}
	return "拒绝候选"
}

// passLabel renders pass/fail for display.
func passLabel(passed bool) string {
	if passed {
		return "通过"
	}
	return "不通过"
}
