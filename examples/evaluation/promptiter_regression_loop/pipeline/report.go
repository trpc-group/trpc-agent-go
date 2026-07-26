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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
)

// CaseReport is a single case's attributed outcome as it appears in the audit report.
type CaseReport struct {
	EvalCaseID string          `json:"evalCaseId"`
	Passed     bool            `json:"passed"`
	Score      float64         `json:"score"`
	Category   FailureCategory `json:"failureCategory,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

// SetReport summarizes one eval set's evaluation: overall status, mean score, per-case detail, and
// the failure-attribution category counts.
type SetReport struct {
	EvalSetID        string          `json:"evalSetId"`
	OverallStatus    string          `json:"overallStatus"`
	MeanScore        float64         `json:"meanScore"`
	ExecutionTimeMs  int64           `json:"executionTimeMs"`
	Cases            []CaseReport    `json:"cases"`
	AttributionStats []CategoryCount `json:"attributionStats"`
}

// RoundSummary is a per-round view of the engine's internal optimization loop.
type RoundSummary struct {
	Round           int     `json:"round"`
	Instruction     string  `json:"instruction,omitempty"`
	TrainScore      float64 `json:"trainScore"`
	ValidationScore float64 `json:"validationScore"`
	Accepted        bool    `json:"accepted"`
	ScoreDelta      float64 `json:"scoreDelta"`
	Stopped         bool    `json:"stopped"`
	StopReason      string  `json:"stopReason,omitempty"`
}

// CostLatency summarizes the run's cost and latency for the audit report. In fake mode there is no
// monetary token cost, so candidate model-call count and evaluation latency stand in for it.
type CostLatency struct {
	TotalWallClockMs    int64  `json:"totalWallClockMs"`
	BaselineEvalMs      int64  `json:"baselineEvalMs"`
	CandidateEvalMs     int64  `json:"candidateEvalMs"`
	CandidateModelCalls int    `json:"candidateModelCalls"`
	Note                string `json:"note,omitempty"`
}

// Determinism records the reproducibility basis of the run. The fake source is fully deterministic
// (scripted model + deterministic collaborators, no RNG), so there is no seed to record.
type Determinism struct {
	Deterministic bool   `json:"deterministic"`
	Seed          *int   `json:"seed"`
	Note          string `json:"note,omitempty"`
}

// ConfigSnapshot captures the run configuration for audit reproducibility.
type ConfigSnapshot struct {
	ModelSource            string   `json:"modelSource"`
	CandidateModel         string   `json:"candidateModel,omitempty"`
	JudgeModel             string   `json:"judgeModel,omitempty"`
	WorkerModel            string   `json:"workerModel,omitempty"`
	MaxRounds              int      `json:"maxRounds"`
	MinScoreGain           float64  `json:"minScoreGain"`
	GateMinValidationGain  float64  `json:"gateMinValidationGain"`
	MaxCandidateModelCalls int      `json:"maxCandidateModelCalls"`
	TargetScore            float64  `json:"targetScore"`
	KeyCaseIDs             []string `json:"keyCaseIds,omitempty"`
	BaselinePromptFile     string   `json:"baselinePromptFile,omitempty"`
	FixtureFile            string   `json:"fixtureFile,omitempty"`
	DataDir                string   `json:"dataDir"`
}

// Report is the full audit artifact for one regression-loop run. It captures the baseline, the
// candidate, the per-case deltas, the engine's own acceptance, and the gate's final decision.
type Report struct {
	GeneratedAt          string `json:"generatedAt,omitempty"`
	App                  string `json:"app"`
	ModelSource          string `json:"modelSource"`
	TargetSurfaceID      string `json:"targetSurfaceId"`
	BaselineInstruction  string `json:"baselineInstruction"`
	CandidateInstruction string `json:"candidateInstruction"`
	// EngineAccepted is the PromptIter engine's own accept decision (mean-gain based).
	EngineAccepted bool `json:"engineAccepted"`
	// GateAccepted is this pipeline's multi-criterion gate decision (the authoritative one).
	GateAccepted bool `json:"gateAccepted"`
	// Decision is "accept" or "reject", mirroring GateAccepted.
	Decision string `json:"decision"`

	Config      ConfigSnapshot `json:"config"`
	Determinism Determinism    `json:"determinism"`
	CostLatency CostLatency    `json:"costLatency"`

	BaselineTrain       SetReport `json:"baselineTrain"`
	BaselineValidation  SetReport `json:"baselineValidation"`
	CandidateTrain      SetReport `json:"candidateTrain"`
	CandidateValidation SetReport `json:"candidateValidation"`

	TrainDeltas      []CaseDelta `json:"trainDeltas"`
	ValidationDeltas []CaseDelta `json:"validationDeltas"`

	Gate   GateDecision   `json:"gate"`
	Rounds []RoundSummary `json:"rounds"`
}

// ReportInput carries everything BuildReport needs. The four evaluation results are the baseline
// and candidate runs over the train and validation sets, all evaluated with run details enabled.
type ReportInput struct {
	GeneratedAt          string
	App                  string
	ModelSource          string
	TargetSurfaceID      string
	BaselineInstruction  string
	CandidateInstruction string
	EngineAccepted       bool

	Config      ConfigSnapshot
	Determinism Determinism
	CostLatency CostLatency

	BaselineTrain       *evaluation.EvaluationResult
	BaselineValidation  *evaluation.EvaluationResult
	CandidateTrain      *evaluation.EvaluationResult
	CandidateValidation *evaluation.EvaluationResult

	Gate   GateDecision
	Rounds []RoundSummary
}

// BuildReport assembles the audit report from the run's evaluation results and gate decision.
func BuildReport(in ReportInput) Report {
	decision := "reject"
	if in.Gate.Accepted {
		decision = "accept"
	}
	return Report{
		GeneratedAt:          in.GeneratedAt,
		App:                  in.App,
		ModelSource:          in.ModelSource,
		TargetSurfaceID:      in.TargetSurfaceID,
		BaselineInstruction:  in.BaselineInstruction,
		CandidateInstruction: in.CandidateInstruction,
		EngineAccepted:       in.EngineAccepted,
		GateAccepted:         in.Gate.Accepted,
		Decision:             decision,
		Config:               in.Config,
		Determinism:          in.Determinism,
		CostLatency:          in.CostLatency,
		BaselineTrain:        buildSetReport(in.BaselineTrain),
		BaselineValidation:   buildSetReport(in.BaselineValidation),
		CandidateTrain:       buildSetReport(in.CandidateTrain),
		CandidateValidation:  buildSetReport(in.CandidateValidation),
		TrainDeltas:          DiffResults(in.BaselineTrain, in.CandidateTrain),
		ValidationDeltas:     DiffResults(in.BaselineValidation, in.CandidateValidation),
		Gate:                 in.Gate,
		Rounds:               in.Rounds,
	}
}

func buildSetReport(result *evaluation.EvaluationResult) SetReport {
	if result == nil {
		return SetReport{AttributionStats: []CategoryCount{}}
	}
	attrs := AttributeResult(result)
	cases := make([]CaseReport, 0, len(attrs))
	for _, a := range attrs {
		cases = append(cases, CaseReport{
			EvalCaseID: a.EvalCaseID,
			Passed:     a.Passed,
			Score:      a.Score,
			Category:   a.Category,
			Reason:     a.Reason,
		})
	}
	return SetReport{
		EvalSetID:        result.EvalSetID,
		OverallStatus:    string(result.OverallStatus),
		MeanScore:        meanScore(result),
		ExecutionTimeMs:  result.ExecutionTime.Milliseconds(),
		Cases:            cases,
		AttributionStats: AttributionStats(result),
	}
}

// JSON renders the report as indented JSON.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Markdown renders the report as a human-readable Markdown audit document.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PromptIter Regression-Loop Optimization Report\n\n")
	if r.GeneratedAt != "" {
		fmt.Fprintf(&b, "- Generated at: %s\n", r.GeneratedAt)
	}
	fmt.Fprintf(&b, "- App: `%s`\n", r.App)
	fmt.Fprintf(&b, "- Model source: `%s`\n", r.ModelSource)
	fmt.Fprintf(&b, "- Target surface: `%s`\n\n", r.TargetSurfaceID)

	fmt.Fprintf(&b, "## Decision: %s\n\n", strings.ToUpper(r.Decision))
	fmt.Fprintf(&b, "- Engine accepted candidate: **%t**\n", r.EngineAccepted)
	fmt.Fprintf(&b, "- Acceptance gate accepted candidate: **%t**\n\n", r.GateAccepted)
	for _, reason := range r.Gate.Reasons {
		fmt.Fprintf(&b, "  - %s\n", mdCell(reason))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Cost & latency\n\n")
	fmt.Fprintf(&b, "- Total wall-clock: %d ms\n", r.CostLatency.TotalWallClockMs)
	fmt.Fprintf(&b, "- Baseline eval: %d ms · candidate eval: %d ms\n", r.CostLatency.BaselineEvalMs, r.CostLatency.CandidateEvalMs)
	fmt.Fprintf(&b, "- Candidate model calls: %d\n", r.CostLatency.CandidateModelCalls)
	if r.CostLatency.Note != "" {
		fmt.Fprintf(&b, "- Note: %s\n", r.CostLatency.Note)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Prompts\n\n")
	fmt.Fprintf(&b, "**Baseline instruction:**\n\n%s\n\n", fencedBlock(r.BaselineInstruction, "text"))
	fmt.Fprintf(&b, "**Candidate instruction:**\n\n%s\n\n", fencedBlock(r.CandidateInstruction, "text"))

	fmt.Fprintf(&b, "## Gate criteria (validation)\n\n")
	fmt.Fprintf(&b, "| Criterion | Passed | Detail |\n|---|---|---|\n")
	for _, c := range r.Gate.Criteria {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(c.Name), checkMark(c.Passed), mdCell(c.Detail))
	}
	b.WriteString("\n")

	writeDeltaTable(&b, "Validation per-case delta", r.ValidationDeltas)
	writeDeltaTable(&b, "Train per-case delta", r.TrainDeltas)

	writeSetSection(&b, "Baseline — validation", r.BaselineValidation)
	writeSetSection(&b, "Candidate — validation", r.CandidateValidation)

	fmt.Fprintf(&b, "## Engine optimization rounds\n\n")
	fmt.Fprintf(&b, "| Round | Train | Validation | Accepted | Δ | Stop | Reason |\n|---|---|---|---|---|---|---|\n")
	for _, rd := range r.Rounds {
		fmt.Fprintf(&b, "| %d | %.3f | %.3f | %t | %+.3f | %t | %s |\n",
			rd.Round, rd.TrainScore, rd.ValidationScore, rd.Accepted, rd.ScoreDelta, rd.Stopped, mdCell(rd.StopReason))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Per-round candidate prompt\n\n")
	for _, rd := range r.Rounds {
		fmt.Fprintf(&b, "**Round %d** (accepted=%t):\n\n%s\n\n", rd.Round, rd.Accepted, fencedBlock(rd.Instruction, "text"))
	}

	fmt.Fprintf(&b, "## Run configuration\n\n")
	fmt.Fprintf(&b, "- Model source: `%s`", r.Config.ModelSource)
	if r.Config.CandidateModel != "" {
		fmt.Fprintf(&b, " · candidate `%s` · judge `%s` · worker `%s`", r.Config.CandidateModel, r.Config.JudgeModel, r.Config.WorkerModel)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- Max rounds: %d · engine min-score-gain: %.3f · gate min-validation-gain: %.3f · target score: %.3f\n",
		r.Config.MaxRounds, r.Config.MinScoreGain, r.Config.GateMinValidationGain, r.Config.TargetScore)
	fmt.Fprintf(&b, "- Max candidate model calls (budget): %d (0 = disabled)\n", r.Config.MaxCandidateModelCalls)
	if len(r.Config.KeyCaseIDs) > 0 {
		fmt.Fprintf(&b, "- Key cases: %s\n", mdCell(fmt.Sprintf("%v", r.Config.KeyCaseIDs)))
	}
	if r.Config.BaselinePromptFile != "" {
		fmt.Fprintf(&b, "- Baseline prompt file: `%s`\n", r.Config.BaselinePromptFile)
	}
	if r.Config.FixtureFile != "" {
		fmt.Fprintf(&b, "- Fake fixture file: `%s`\n", r.Config.FixtureFile)
	}
	seed := "none (deterministic)"
	if r.Determinism.Seed != nil {
		seed = fmt.Sprintf("%d", *r.Determinism.Seed)
	}
	fmt.Fprintf(&b, "- Determinism: deterministic=%t, seed=%s", r.Determinism.Deterministic, seed)
	if r.Determinism.Note != "" {
		fmt.Fprintf(&b, " (%s)", r.Determinism.Note)
	}
	b.WriteString("\n\n")
	return b.String()
}

func writeDeltaTable(b *strings.Builder, title string, deltas []CaseDelta) {
	fmt.Fprintf(b, "## %s\n\n", title)
	fmt.Fprintf(b, "| Case | Class | Baseline | Candidate | Δ |\n|---|---|---|---|---|\n")
	for _, d := range deltas {
		fmt.Fprintf(b, "| %s | %s | %.3f | %.3f | %+.3f |\n",
			mdCell(d.EvalCaseID), mdCell(string(d.Class)), d.BaselineScore, d.CandidateScore, d.ScoreDelta)
	}
	b.WriteString("\n")
}

func writeSetSection(b *strings.Builder, title string, set SetReport) {
	fmt.Fprintf(b, "## %s (mean %.3f, %s)\n\n", title, set.MeanScore, set.OverallStatus)
	fmt.Fprintf(b, "| Case | Passed | Score | Failure category |\n|---|---|---|---|\n")
	for _, c := range set.Cases {
		fmt.Fprintf(b, "| %s | %s | %.3f | %s |\n", mdCell(c.EvalCaseID), checkMark(c.Passed), c.Score, mdCell(string(c.Category)))
	}
	b.WriteString("\n")
	if len(set.AttributionStats) > 0 {
		fmt.Fprintf(b, "Failure attribution: ")
		parts := make([]string, 0, len(set.AttributionStats))
		for _, s := range set.AttributionStats {
			parts = append(parts, fmt.Sprintf("%s=%d", s.Category, s.Count))
		}
		fmt.Fprintf(b, "%s\n\n", strings.Join(parts, ", "))
	}
}

func checkMark(passed bool) string {
	if passed {
		return "✅"
	}
	return "❌"
}

// mdCell escapes a dynamic string for safe inclusion in a GFM table cell. A literal '|' would start
// a new column and a newline would end the row and return to block context (allowing a forged
// heading or decision line), so both are neutralized. Case IDs and reasons originate from eval-set
// data and the -key-cases flag, so they are external and untrusted. This is the table-cell analogue
// of fencedBlock's code-fence hardening.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// fencedBlock renders content as a fenced code block labeled lang. It picks a backtick fence longer
// than the longest backtick run in content (per CommonMark), so model-derived content containing
// ``` cannot close the block early and forge subsequent Markdown.
func fencedBlock(content, lang string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	fence := strings.Repeat("`", n)
	return fmt.Sprintf("%s%s\n%s\n%s", fence, lang, content, fence)
}
