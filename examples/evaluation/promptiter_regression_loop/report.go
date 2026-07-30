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
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// BaselineEvaluation records every scored case from the baseline run (train + validation),
// satisfying step 1 of the regression methodology: per-case metric score, pass/fail,
// failure reason, and trace / tool trajectory.
type BaselineEvaluation struct {
	EvalSets []BaselineEvalSetRecord `json:"evalSets"`
}

// BaselineEvalSetRecord aggregates one evaluation set (train or validation).
type BaselineEvalSetRecord struct {
	Role      string               `json:"role"` // "validation" (baseline) or "train" (round-1 baseline)
	EvalSetID string               `json:"evalSetId"`
	CaseCount int                  `json:"caseCount"`
	Passed    int                  `json:"passed"`
	Failed    int                  `json:"failed"`
	Cases     []BaselineCaseRecord `json:"cases"`
}

// BaselineCaseRecord is the per-case baseline record.
type BaselineCaseRecord struct {
	EvalSetID  string            `json:"evalSetId"`
	EvalCaseID string            `json:"evalCaseId"`
	SessionID  string            `json:"sessionId"`
	Passed     bool              `json:"passed"`
	Metrics    []BaselineMetric  `json:"metrics"`
	Trace      BaselineTraceInfo `json:"trace"`
}

// BaselineMetric is one metric result for a case.
type BaselineMetric struct {
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	Status string  `json:"status"` // "pass" | "fail"
	Reason string  `json:"reason"`
}

// BaselineTraceInfo summarizes the execution trace / tool trajectory of a case.
type BaselineTraceInfo struct {
	HasTrace   bool     `json:"hasTrace"`
	NodeCount  int      `json:"nodeCount"`
	ToolCalls  int      `json:"toolCalls"`
	StepErrors []string `json:"stepErrors"`
}

// CaseDelta captures the score change of a single eval case between baseline
// and candidate.
type CaseDelta struct {
	EvalSetID       string  `json:"evalSetId"`
	EvalCaseID      string  `json:"evalCaseId"`
	BaselineScore   float64 `json:"baselineScore"`
	CandidateScore  float64 `json:"candidateScore"`
	Delta           float64 `json:"delta"`
	Trend           string  `json:"trend"` // up | down | flat (score direction)
	BaselinePassed  bool    `json:"baselinePassed"`
	CandidatePassed bool    `json:"candidatePassed"`
	// Transition classifies the case-level outcome change:
	//   new_pass   : baseline failed -> candidate passed (新增通过)
	//   new_fail   : baseline passed -> candidate failed (新增失败)
	//   score_up   : same pass/fail status but score increased (分数提升)
	//   score_down : same pass/fail status but score decreased (分数下降)
	//   flat       : no change (持平)
	Transition string `json:"transition"`
}

// StageTiming records the wall-clock duration of each pipeline phase so the
// audit report shows where time is spent (satisfying the "记录耗时" requirement
// from issue #2003).
type StageTiming struct {
	EngineMs      int64 `json:"engineMs"`
	AttributionMs int64 `json:"attributionMs"`
	GateMs        int64 `json:"gateMs"`
	ReportMs      int64 `json:"reportMs"`
}

// RoundRecord audits one optimization round: the candidate prompt the
// optimizer produced plus the train/validation scores of that round.
type RoundRecord struct {
	Round           int     `json:"round"`
	TrainScore      float64 `json:"trainScore"`
	ValidationScore float64 `json:"validationScore"`
	Accepted        bool    `json:"accepted"`
	Reason          string  `json:"reason,omitempty"`
	CandidatePrompt string  `json:"candidatePrompt,omitempty"`
}

// RegressionReport is the audit artifact written by the regression loop.
type RegressionReport struct {
	GeneratedAt        string              `json:"generatedAt"`
	DurationMS         int64               `json:"durationMs"`
	StageTiming        *StageTiming        `json:"stageTiming,omitempty"`
	AppName            string              `json:"appName"`
	Model              string              `json:"model"`
	Rounds             int                 `json:"rounds"`
	RoundRecords       []RoundRecord       `json:"roundRecords,omitempty"`
	BaselineScore      float64             `json:"baselineScore"`
	CandidateScore     float64             `json:"candidateScore"`
	ScoreDelta         float64             `json:"scoreDelta"`
	Accepted           bool                `json:"accepted"`
	GateReason         string              `json:"gateReason"`
	GateRejectedBy     string              `json:"gateRejectedBy,omitempty"`
	Attribution        *AttributionResult  `json:"attribution"`
	Narrative          string              `json:"narrative,omitempty"`
	LLMCalls           int                 `json:"llmCalls"`
	LLMErrors          int                 `json:"llmErrors"`
	PerCaseDelta       []CaseDelta         `json:"perCaseDelta"`
	BaselineEvaluation *BaselineEvaluation `json:"baselineEvaluation,omitempty"`
	CandidatePrompt    string              `json:"candidatePrompt"`
	Cost               *CostReport         `json:"cost,omitempty"`
	Config             ConfigSnapshot      `json:"config"`
}

// buildReport assembles the audit report from a PromptIter run result plus the
// attribution, gate decision, and natural-language narrative computed by the loop.
func buildReport(
	cfg regressionConfig,
	result *promptiterengine.RunResult,
	attrib *AttributionResult,
	gate *GateDecision,
	cost *CostReport,
	duration time.Duration,
	stageTiming *StageTiming,
	narrative string,
	llmCalls int,
	llmErrors int,
) *RegressionReport {
	baseline := result.BaselineValidation
	// Use the same candidate the gate compared against: the validation result of
	// the final round. Falling back to only an engine-accepted round would hide a
	// regressed candidate's real score and per-case deltas from the audit report,
	// making the gate decision unexplainable (e.g. delta=0 but "regressed").
	candidate := baseline
	if n := len(result.Rounds); n > 0 && result.Rounds[n-1].Validation != nil {
		candidate = result.Rounds[n-1].Validation
	}
	bs := overallScore(baseline)
	cs := overallScore(candidate)
	return &RegressionReport{
		GeneratedAt:        time.Now().Format(time.RFC3339),
		DurationMS:         duration.Milliseconds(),
		StageTiming:        stageTiming,
		AppName:            appName,
		Model:              cfg.CandidateModelName,
		Rounds:             len(result.Rounds),
		RoundRecords:       buildRoundRecords(result),
		BaselineScore:      bs,
		CandidateScore:     cs,
		ScoreDelta:         cs - bs,
		Accepted:           gate.Accepted,
		GateReason:         gate.Reason,
		GateRejectedBy:     gate.RejectedBy,
		Attribution:        attrib,
		Narrative:          narrative,
		LLMCalls:           llmCalls,
		LLMErrors:          llmErrors,
		PerCaseDelta:       perCaseDeltas(baseline, candidate),
		BaselineEvaluation: buildBaselineEvaluation(result),
		CandidatePrompt:    candidateInstructionText(result),
		Cost:               cost,
		Config:             snapshotConfig(cfg),
	}
}

// buildRoundRecords audits every optimization round: per-round candidate
// prompt, train/validation scores, and the engine's acceptance decision.
func buildRoundRecords(result *promptiterengine.RunResult) []RoundRecord {
	var out []RoundRecord
	for _, rd := range result.Rounds {
		rr := RoundRecord{Round: rd.Round}
		if rd.Train != nil {
			rr.TrainScore = overallScore(rd.Train)
		}
		if rd.Validation != nil {
			rr.ValidationScore = overallScore(rd.Validation)
		}
		if rd.Acceptance != nil {
			rr.Accepted = rd.Acceptance.Accepted
			rr.Reason = rd.Acceptance.Reason
		}
		rr.CandidatePrompt = profileInstructionText(rd.OutputProfile)
		out = append(out, rr)
	}
	return out
}

// buildBaselineEvaluation records the per-case baseline detail for both sets:
//   - validation: the accepted baseline validation result (scored once at run start)
//   - train: the round-1 train result, which is evaluated on the baseline profile
//     before any optimization, so it represents the train-set baseline.
func buildBaselineEvaluation(result *promptiterengine.RunResult) *BaselineEvaluation {
	be := &BaselineEvaluation{}
	if result.BaselineValidation != nil {
		be.EvalSets = append(be.EvalSets, summarizeEvalSet("validation", result.BaselineValidation))
	}
	if len(result.Rounds) > 0 && result.Rounds[0].Train != nil {
		be.EvalSets = append(be.EvalSets, summarizeEvalSet("train", result.Rounds[0].Train))
	}
	if len(be.EvalSets) == 0 {
		return nil
	}
	return be
}

func summarizeEvalSet(role string, res *promptiterengine.EvaluationResult) BaselineEvalSetRecord {
	rec := BaselineEvalSetRecord{Role: role}
	for _, es := range res.EvalSets {
		rec.EvalSetID = es.EvalSetID
		for _, c := range es.Cases {
			cc := BaselineCaseRecord{
				EvalSetID:  es.EvalSetID,
				EvalCaseID: c.EvalCaseID,
				SessionID:  c.SessionID,
				Trace:      summarizeTrace(c.Trace),
			}
			passed := true
			for _, m := range c.Metrics {
				ok := m.Score >= 1.0
				if !ok {
					passed = false
				}
				status := "pass"
				reason := m.Reason
				if !ok {
					status = "fail"
					if strings.TrimSpace(reason) == "" {
						reason = explainCategory(classifyMetric(m, c.Trace), m)
					}
				}
				cc.Metrics = append(cc.Metrics, BaselineMetric{
					Name:   m.MetricName,
					Score:  m.Score,
					Status: status,
					Reason: reason,
				})
			}
			cc.Passed = passed
			if passed {
				rec.Passed++
			} else {
				rec.Failed++
			}
			rec.CaseCount++
			rec.Cases = append(rec.Cases, cc)
		}
	}
	return rec
}

func summarizeTrace(t *atrace.Trace) BaselineTraceInfo {
	info := BaselineTraceInfo{}
	if t == nil {
		return info
	}
	info.HasTrace = true
	info.NodeCount = len(t.Steps)
	for _, s := range t.Steps {
		if s.NodeType == "tool" {
			info.ToolCalls++
		}
		if s.Error != "" {
			info.StepErrors = append(info.StepErrors, s.Error)
		}
	}
	return info
}

// writeReport writes the audit artifacts under outputDir:
//   - optimization_report.json / .md  (the structured + human-readable audit)
//   - candidate_prompt.txt            (the optimized candidate prompt)
//   - baseline_eval_result.json       (raw baseline validation eval result)
//   - candidate_eval_result.json      (raw candidate validation eval result)
func writeReport(outputDir string, report *RegressionReport, result *promptiterengine.RunResult) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return fmt.Errorf("write report json: %w", err)
	}
	mdPath := filepath.Join(outputDir, "optimization_report.md")
	if err := os.WriteFile(mdPath, []byte(reportToMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write report md: %w", err)
	}
	if report.CandidatePrompt != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "candidate_prompt.txt"), []byte(report.CandidatePrompt), 0o644); err != nil {
			return fmt.Errorf("write candidate prompt: %w", err)
		}
	}
	if result != nil {
		if result.BaselineValidation != nil {
			if b, err := json.MarshalIndent(result.BaselineValidation, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(outputDir, "baseline_eval_result.json"), b, 0o644)
			}
		}
		if n := len(result.Rounds); n > 0 && result.Rounds[n-1].Validation != nil {
			if b, err := json.MarshalIndent(result.Rounds[n-1].Validation, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(outputDir, "candidate_eval_result.json"), b, 0o644)
			}
		}
	}
	return nil
}

func reportToMarkdown(r *RegressionReport) string {
	var b strings.Builder
	b.WriteString("# PromptIter 回归优化报告\n\n")
	fmt.Fprintf(&b, "- 应用: %s\n", r.AppName)
	fmt.Fprintf(&b, "- 模型: %s\n", r.Model)
	fmt.Fprintf(&b, "- 优化轮次: %d\n", r.Rounds)
	fmt.Fprintf(&b, "- 耗时: %d 毫秒\n", r.DurationMS)
	if st := r.StageTiming; st != nil {
		b.WriteString("- 分阶段耗时:\n")
		fmt.Fprintf(&b, "  - 引擎运行: %d 毫秒\n", st.EngineMs)
		fmt.Fprintf(&b, "  - 失败归因: %d 毫秒\n", st.AttributionMs)
		fmt.Fprintf(&b, "  - 门禁判断: %d 毫秒\n", st.GateMs)
		fmt.Fprintf(&b, "  - 报告生成: %d 毫秒\n", st.ReportMs)
	}
	fmt.Fprintf(&b, "- 基线分数: %.4f\n", r.BaselineScore)
	fmt.Fprintf(&b, "- 候选分数: %.4f\n", r.CandidateScore)
	fmt.Fprintf(&b, "- 分数变化: %+.4f\n", r.ScoreDelta)
	fmt.Fprintf(&b, "- 是否接受: %v\n", r.Accepted)
	fmt.Fprintf(&b, "- 门禁理由: %s\n", r.GateReason)
	if r.GateRejectedBy != "" {
		fmt.Fprintf(&b, "- 拒绝原因: %s\n", r.GateRejectedBy)
	}
	b.WriteString("\n## 基线失败归因\n\n")
	if r.Attribution == nil || len(r.Attribution.Failures) == 0 {
		b.WriteString("无失败需要归因。\n")
	} else {
		fmt.Fprintf(&b, "- 归因方法: %s\n", r.Attribution.Method)
		fmt.Fprintf(&b, "- 总 case 数: %d，失败 case 数: %d（共 %d 条失败指标，即每个 case 平均 %.1f 条）\n", r.Attribution.TotalCases, r.Attribution.FailedCases, len(r.Attribution.Failures), float64(len(r.Attribution.Failures))/float64(r.Attribution.FailedCases))
		b.WriteString("- 按类别分布：")
		for cat, n := range r.Attribution.ByCategory {
			fmt.Fprintf(&b, "%s=%d ", cat, n)
		}
		b.WriteString("\n")
		if ins := r.Attribution.Insights; ins != nil {
			fmt.Fprintf(&b, "- 洞察（%s）：%s\n", ins.Method, ins.Summary)
			if ins.SuggestedFix != "" {
				fmt.Fprintf(&b, "- 修复建议：%s\n", ins.SuggestedFix)
			}
			if len(ins.Patterns) > 0 {
				b.WriteString("- 失败模式：\n")
				for _, p := range ins.Patterns {
					fmt.Fprintf(&b, "  - %s: %d 个 (%.0f%%)", p.Category, p.Count, p.Ratio*100)
					if p.Example != "" {
						fmt.Fprintf(&b, "（例如：%s）", oneLine(p.Example))
					}
					b.WriteString("\n")
				}
			}
		}
		// Clusters: de-duplicated, actionable groups of similar failures.
		if len(r.Attribution.Clusters) > 0 {
			b.WriteString("- 聚类（去重）：\n")
			for _, c := range r.Attribution.Clusters {
				fmt.Fprintf(&b, "  - %s × %d：%s", c.Category, c.Count, oneLine(c.Reason))
				if len(c.CaseIDs) > 0 {
					fmt.Fprintf(&b, "  [%s]", strings.Join(c.CaseIDs, ", "))
				}
				b.WriteString("\n")
			}
		}
	}
	if r.Narrative != "" {
		b.WriteString("\n## 自然语言总结\n\n")
		b.WriteString(r.Narrative)
		b.WriteString("\n")
	}
	b.WriteString("\n## LLM 增强可观测性\n\n")
	fmt.Fprintf(&b, "- LLM 调用次数：%d（批量归因 + 合并报告各计一次）\n", r.LLMCalls)
	fmt.Fprintf(&b, "- LLM 错误次数：%d（任何错误均回退确定性规则）\n", r.LLMErrors)
	if r.LLMCalls > 0 {
		b.WriteString("  说明：失败归因以规则为主，LLM 调用为可选增强，永远不会影响接受/拒绝门禁。\n")
	} else {
		b.WriteString("  说明：全流程以确定性规则运行（规则归因），未发起任何 LLM 调用。\n")
	}
	b.WriteString("\n## 基线评测（逐 case）\n\n")
	if r.BaselineEvaluation == nil {
		b.WriteString("未记录基线评测。\n")
	} else {
		for _, s := range r.BaselineEvaluation.EvalSets {
			fmt.Fprintf(&b, "- [%s] %s：%d 个 case，%d 通过，%d 失败\n",
				s.Role, s.EvalSetID, s.CaseCount, s.Passed, s.Failed)
		}
		b.WriteString("  （完整逐 case 指标分数 / 通过-失败 / 理由 / trace 见 optimization_report.json）\n")
	}
	if len(r.RoundRecords) > 0 {
		b.WriteString("\n## 优化轮次\n\n")
		b.WriteString("| 轮次 | 训练集 | 验证集 | 引擎是否接受 | 理由 |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, rr := range r.RoundRecords {
			fmt.Fprintf(&b, "| %d | %.4f | %.4f | %v | %s |\n",
				rr.Round, rr.TrainScore, rr.ValidationScore, rr.Accepted, rr.Reason)
		}
		b.WriteString("\n（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）\n")
	}
	b.WriteString("\n## 逐 case 分数变化\n\n")
	b.WriteString("| 评测集 | Case | 基线 | 候选 | 基线通过 | 候选通过 | 变化 | 趋势 | 变化类别 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	trendCn := map[string]string{"up": "上升", "down": "下降", "flat": "持平"}
	transitionCn := map[string]string{
		"new_pass":   "新增通过",
		"new_fail":   "新增失败",
		"score_up":   "分数提升",
		"score_down": "分数下降",
		"flat":       "持平",
	}
	for _, d := range r.PerCaseDelta {
		trend := trendCn[d.Trend]
		if trend == "" {
			trend = d.Trend
		}
		tc := transitionCn[d.Transition]
		if tc == "" {
			tc = d.Transition
		}
		fmt.Fprintf(&b, "| %s | %s | %.4f | %.4f | %v | %v | %+.4f | %s | %s |\n",
			d.EvalSetID, d.EvalCaseID, d.BaselineScore, d.CandidateScore,
			d.BaselinePassed, d.CandidatePassed, d.Delta, trend, tc)
	}
	b.WriteString("\n## 成本\n\n")
	if r.Cost == nil {
		b.WriteString("未记录成本。\n")
	} else {
		fmt.Fprintf(&b, "- 评测单元：%d（单元成本 %.4f）\n", r.Cost.EvalUnits, r.Cost.UnitEvalCost)
		fmt.Fprintf(&b, "- 工作流单元：%d（单元成本 %.4f）\n", r.Cost.WorkerUnits, r.Cost.UnitWorkerCost)
		fmt.Fprintf(&b, "- 估算总成本：%.4f\n", r.Cost.Total)
		budget := r.Config.CostBudget
		if budget <= 0 {
			b.WriteString("- 预算：无限制\n")
		} else {
			fmt.Fprintf(&b, "- 预算：%.4f\n", budget)
		}
	}
	b.WriteString("\n## 配置\n\n")
	c := r.Config
	fmt.Fprintf(&b, "- 应用：%s（候选 agent：%s）\n", c.AppName, c.CandidateAgentName)
	fmt.Fprintf(&b, "- Prompt 类型：%s\n", c.PromptType)
	if len(c.TargetSurfaces) > 0 {
		b.WriteString("- 目标面：")
		for _, s := range c.TargetSurfaces {
			fmt.Fprintf(&b, "%s ", s)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- 训练评测集：%v\n", c.TrainEvalSetIDs)
	fmt.Fprintf(&b, "- 验证评测集：%v\n", c.ValidationEvalSetIDs)
	fmt.Fprintf(&b, "- 指标文件：%s\n", c.MetricFileID)
	fmt.Fprintf(&b, "- 模型：candidate=%s judge=%s worker=%s\n", c.CandidateModelName, c.JudgeModelName, c.WorkerModelName)
	fmt.Fprintf(&b, "- minScoreGain=%.4f targetScore=%.4f maxRounds=%d\n", c.MinScoreGain, c.TargetScore, c.MaxRounds)
	fmt.Fprintf(&b, "- 随机种子：%d（确定性 fake runner 不受影响；用于真实模型复现）\n", c.Seed)
	if len(c.KeyCaseIDs) > 0 {
		fmt.Fprintf(&b, "- 关键 case：%v\n", c.KeyCaseIDs)
	}
	fmt.Fprintf(&b, "- Fake 模式：%v（场景=%s）\n", c.Fake, c.FakeScenario)
	b.WriteString("\n## 候选 Prompt\n\n```text\n")
	b.WriteString(r.CandidatePrompt)
	b.WriteString("\n```\n")
	return b.String()
}

// candidateInstructionText extracts the candidate instruction that was actually
// gated, i.e. the last round's output profile. This is the exact prompt the
// accept/reject decision was made against, so it is populated even when the
// engine rejected every round (in which case AcceptedProfile is nil and would
// otherwise yield an empty prompt). Falls back to the accepted profile when a
// round profile is unavailable.
func candidateInstructionText(result *promptiterengine.RunResult) string {
	if result == nil {
		return ""
	}
	if n := len(result.Rounds); n > 0 {
		if t := profileInstructionText(result.Rounds[n-1].OutputProfile); t != "" {
			return t
		}
	}
	return profileInstructionText(result.AcceptedProfile)
}

// profileInstructionText returns the first text override in a profile.
func profileInstructionText(p *promptiter.Profile) string {
	if p == nil {
		return ""
	}
	for _, override := range p.Overrides {
		if override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

// printSummary logs the headline numbers of the regression loop.
func printSummary(r *RegressionReport) {
	log.Printf("regression loop finished: baseline=%.4f candidate=%.4f delta=%+.4f accepted=%v",
		r.BaselineScore, r.CandidateScore, r.ScoreDelta, r.Accepted)
	log.Printf("gate reason: %s", r.GateReason)
}
