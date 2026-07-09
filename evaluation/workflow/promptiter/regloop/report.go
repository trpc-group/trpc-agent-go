//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Options configures how one run result is turned into a report.
type Options struct {
	// App names the optimization target for the report header.
	App string
	// Mode records how the run was executed (e.g. "fake" or "live").
	Mode string
	// Gate is the release policy applied to the candidate.
	Gate ReleaseGate
	// Config echoes run configuration into the report for auditing.
	Config map[string]any
}

// Analyze turns one PromptIter engine RunResult into a full optimization report:
// baseline / candidate scores, per-case delta, failure attribution, release gate,
// and a cost estimate. It is pure and needs no model or API key.
func Analyze(result *engine.RunResult, opts Options) (*Report, error) {
	if result == nil {
		return nil, errors.New("run result is nil")
	}
	baseline := phaseScore(result.BaselineValidation)
	candidateValidation, acceptedRound := acceptedValidation(result)
	candidate := phaseScore(candidateValidation)

	delta := ComputeDelta(result.BaselineValidation, candidateValidation)

	attribution := Attribute(result.BaselineValidation)
	attribution.BySeverity = severityCounts(result.Rounds)

	totalGain := candidate.OverallScore - baseline.OverallScore
	gate := opts.Gate.Evaluate(acceptedRound > 0, totalGain, len(result.Rounds), delta)

	return &Report{
		App:      opts.App,
		Mode:     opts.Mode,
		Status:   string(result.Status),
		Baseline: baseline,
		Candidate: CandidateScore{
			OverallScore:    candidate.OverallScore,
			ProfileAccepted: acceptedRound > 0,
			AcceptedRound:   acceptedRound,
		},
		Delta:              delta,
		FailureAttribution: attribution,
		Gate:               gate,
		Cost:               costReport(result),
		Rounds:             roundReports(result),
		Config:             opts.Config,
	}, nil
}

// costReport estimates cost from observable counts; the engine result has no
// token accounting, so teacher calls are derived from rounds x evaluated cases.
func costReport(result *engine.RunResult) CostReport {
	rounds := len(result.Rounds)
	teacherCalls := caseCount(result.BaselineValidation)
	for _, round := range result.Rounds {
		teacherCalls += caseCount(round.Train) + caseCount(round.Validation)
	}
	return CostReport{
		Rounds:       rounds,
		TeacherCalls: teacherCalls,
		Estimated:    true,
		Note:         "teacher calls derived from evaluated cases across baseline and rounds",
	}
}

func caseCount(result *engine.EvaluationResult) int {
	if result == nil {
		return 0
	}
	total := 0
	for _, set := range result.EvalSets {
		total += len(set.Cases)
	}
	return total
}

func roundReports(result *engine.RunResult) []RoundReport {
	reports := make([]RoundReport, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		report := RoundReport{Round: round.Round}
		if round.Validation != nil {
			report.ValidationScore = round.Validation.OverallScore
		}
		if round.Acceptance != nil {
			report.Accepted = round.Acceptance.Accepted
			report.ScoreDelta = round.Acceptance.ScoreDelta
			report.Reason = round.Acceptance.Reason
		}
		reports = append(reports, report)
	}
	return reports
}

// JSON renders the report as indented optimization_report.json bytes.
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Markdown renders a human-readable optimization_report.md.
func (r *Report) Markdown() string {
	var b strings.Builder
	verdict := "REJECTED — candidate not worth accepting"
	if r.Gate.Released {
		verdict = "RELEASED — candidate improves quality and passes the gate"
	}
	fmt.Fprintf(&b, "# Optimization Report: %s\n\n", r.App)
	fmt.Fprintf(&b, "**Verdict: %s**\n\n", verdict)
	fmt.Fprintf(&b, "- mode: `%s`\n- status: `%s`\n\n", r.Mode, r.Status)

	fmt.Fprintf(&b, "## Score\n\n")
	fmt.Fprintf(&b, "| phase | overall score |\n|---|---|\n")
	fmt.Fprintf(&b, "| baseline | %.3f |\n", r.Baseline.OverallScore)
	fmt.Fprintf(&b, "| candidate | %.3f |\n", r.Candidate.OverallScore)
	fmt.Fprintf(&b, "| gain | %+.3f |\n\n", r.Candidate.OverallScore-r.Baseline.OverallScore)

	fmt.Fprintf(&b, "## Delta\n\n")
	s := r.Delta.Summary
	fmt.Fprintf(&b, "- newly passed: **%d**\n- newly failed: **%d**\n- score up: %d\n- score down: %d\n- unchanged: %d\n\n",
		s.NewlyPassed, s.NewlyFailed, s.ScoreUp, s.ScoreDown, s.Unchanged)

	fmt.Fprintf(&b, "## Failure Attribution (baseline)\n\n")
	if len(r.FailureAttribution.Baseline) == 0 {
		fmt.Fprintf(&b, "- no baseline failures\n\n")
	} else {
		for _, category := range sortedCategoryKeys(r.FailureAttribution.Baseline) {
			fmt.Fprintf(&b, "- %s: %d\n", category, r.FailureAttribution.Baseline[FailureCategory(category)])
		}
		b.WriteString("\n")
	}
	if len(r.FailureAttribution.BySeverity) > 0 {
		fmt.Fprintf(&b, "Terminal-loss severity (training signal): ")
		parts := make([]string, 0, len(r.FailureAttribution.BySeverity))
		for _, sev := range sortedStringKeys(r.FailureAttribution.BySeverity) {
			parts = append(parts, fmt.Sprintf("%s=%d", sev, r.FailureAttribution.BySeverity[sev]))
		}
		fmt.Fprintf(&b, "%s\n\n", strings.Join(parts, ", "))
	}

	fmt.Fprintf(&b, "## Release Gate\n\n")
	fmt.Fprintf(&b, "- released: **%t**\n", r.Gate.Released)
	for _, reason := range r.Gate.Reasons {
		fmt.Fprintf(&b, "  - %s\n", reason)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Cost (estimated)\n\n")
	fmt.Fprintf(&b, "- rounds: %d\n- teacher calls: %d\n- note: %s\n",
		r.Cost.Rounds, r.Cost.TeacherCalls, r.Cost.Note)
	return b.String()
}

// WriteFiles writes optimization_report.json and optimization_report.md to dir.
func WriteFiles(dir string, r *Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	payload, err := r.JSON()
	if err != nil {
		return fmt.Errorf("marshal report json: %w", err)
	}
	jsonPath := filepath.Join(dir, "optimization_report.json")
	if err := os.WriteFile(jsonPath, payload, 0o644); err != nil {
		return fmt.Errorf("write report json: %w", err)
	}
	mdPath := filepath.Join(dir, "optimization_report.md")
	if err := os.WriteFile(mdPath, []byte(r.Markdown()), 0o644); err != nil {
		return fmt.Errorf("write report md: %w", err)
	}
	return nil
}

func sortedCategoryKeys(m map[FailureCategory]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
