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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
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
	// Cost carries runtime cost facts the pure package cannot observe.
	Cost CostInput
}

// CostInput carries caller-measured cost facts (wall-clock, model calls) that
// the pure package cannot derive from a RunResult.
type CostInput struct {
	// DurationMs is the measured wall-clock of the run in milliseconds.
	DurationMs int64
	// ModelCalls counts model invocations per role.
	ModelCalls map[string]int
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
	modelCalls := totalModelCalls(opts.Cost.ModelCalls)
	gate := opts.Gate.Evaluate(acceptedRound > 0, totalGain, len(result.Rounds), modelCalls, delta)
	// Fail closed: a release can only be trusted when the run finished
	// successfully and both phases carry comparable per-case data. A still-running
	// or failed run may retain an accepted round, and a slimmed RunResult that
	// omits evaluation cases would hide regressions and release on aggregate gain
	// alone — both must be rejected rather than released.
	gate = applyReleasePreconditions(gate, result, candidateValidation)

	return &Report{
		App:      opts.App,
		Mode:     opts.Mode,
		Status:   string(result.Status),
		Baseline: baseline,
		Candidate: CandidateScore{
			OverallScore:    candidate.OverallScore,
			ProfileAccepted: acceptedRound > 0,
			AcceptedRound:   acceptedRound,
			Surfaces:        candidateSurfaces(result.AcceptedProfile),
		},
		Delta:              delta,
		FailureAttribution: attribution,
		Gate:               gate,
		Cost:               costReport(result, opts.Cost),
		Rounds:             roundReports(result),
		Config:             opts.Config,
	}, nil
}

// candidateSurfaces projects the accepted profile down to a stable audit view:
// each overridden surface's ID and textual value only.
func candidateSurfaces(profile *promptiter.Profile) []SurfaceProjection {
	if profile == nil {
		return nil
	}
	surfaces := make([]SurfaceProjection, 0, len(profile.Overrides))
	for _, override := range profile.Overrides {
		if override.Value.Text != nil {
			surfaces = append(surfaces, SurfaceProjection{SurfaceID: override.SurfaceID, Value: *override.Value.Text})
		}
	}
	if len(surfaces) == 0 {
		return nil
	}
	return surfaces
}

// totalModelCalls sums per-role model call counts.
func totalModelCalls(calls map[string]int) int {
	total := 0
	for _, n := range calls {
		total += n
	}
	return total
}

// applyReleasePreconditions forces the gate closed when the result cannot
// support a trustworthy release decision: the run must have completed
// successfully, and both phases must carry comparable per-case data.
func applyReleasePreconditions(gate GateResult, result *engine.RunResult, candidate *engine.EvaluationResult) GateResult {
	if result.Status != engine.RunStatusSucceeded {
		gate.Released = false
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("run did not complete successfully (status %q)", result.Status))
		return gate
	}
	if !hasComparableCases(result.BaselineValidation) || !hasComparableCases(candidate) {
		gate.Released = false
		gate.Reasons = append(gate.Reasons, "per-case results unavailable; cannot verify regressions")
	}
	return gate
}

// hasComparableCases reports whether the result carries at least one case with
// metric data (a slimmed result that omits cases returns false).
func hasComparableCases(result *engine.EvaluationResult) bool {
	if result == nil {
		return false
	}
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			if len(evalCase.Metrics) > 0 {
				return true
			}
		}
	}
	return false
}

// costReport combines result-derived case counts with caller-measured runtime
// cost (wall-clock and per-role model calls). EvaluatedCases is a case count,
// distinct from model calls.
func costReport(result *engine.RunResult, cost CostInput) CostReport {
	rounds := len(result.Rounds)
	evaluatedCases := caseCount(result.BaselineValidation)
	for _, round := range result.Rounds {
		evaluatedCases += caseCount(round.Train) + caseCount(round.Validation)
	}
	return CostReport{
		Rounds:          rounds,
		EvaluatedCases:  evaluatedCases,
		DurationMs:      cost.DurationMs,
		ModelCalls:      cost.ModelCalls,
		TotalModelCalls: totalModelCalls(cost.ModelCalls),
		Estimated:       true,
		Note:            "evaluated cases is a case count; model calls are counted per role, distinct from cases",
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

	if len(r.Candidate.Surfaces) > 0 {
		fmt.Fprintf(&b, "## Accepted candidate\n\n")
		for _, s := range r.Candidate.Surfaces {
			fmt.Fprintf(&b, "- `%s`: %s\n", s.SurfaceID, s.Value)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Cost (estimated)\n\n")
	fmt.Fprintf(&b, "- rounds: %d\n- evaluated cases: %d\n- duration: %d ms\n- model calls: %d\n",
		r.Cost.Rounds, r.Cost.EvaluatedCases, r.Cost.DurationMs, r.Cost.TotalModelCalls)
	for _, role := range sortedStringKeys(r.Cost.ModelCalls) {
		fmt.Fprintf(&b, "  - %s: %d\n", role, r.Cost.ModelCalls[role])
	}
	fmt.Fprintf(&b, "- note: %s\n", r.Cost.Note)
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
