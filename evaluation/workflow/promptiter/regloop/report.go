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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Options configures how one run result is turned into a report.
type Options struct {
	// AppName names the optimization target for the report header. It is only
	// used when the RunResult does not carry its own AppName (direct engine runs);
	// a manager-backed result's AppName is authoritative.
	AppName string
	// Mode records how the run was executed (e.g. "fake" or "live").
	Mode string
	// Gate is the release policy applied to the candidate.
	Gate ReleaseGate
	// Config echoes run configuration into the report for auditing. Values under
	// sensitive-looking keys (api keys, tokens, authorization headers, ...) are
	// redacted before serialization; do not rely on this map to carry secrets.
	Config map[string]any
	// Cost carries runtime cost facts the pure package cannot observe.
	Cost CostInput
	// ExpectedMetrics lists the metric names every case in both compared phases
	// must carry. It catches metrics that were silently skipped (e.g. a name not
	// matching any registered evaluator) in both phases, which the key-set
	// comparison alone cannot see. Empty disables the shape check.
	ExpectedMetrics []string
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
	attribution.TrainingTerminalLossesBySeverity = severityCounts(result.Rounds)

	totalGain := candidate.OverallScore - baseline.OverallScore
	gate := opts.Gate.evaluate(gateInput{
		ProfileAccepted: acceptedRound > 0,
		TotalGain:       totalGain,
		Rounds:          len(result.Rounds),
		ModelCalls:      totalModelCalls(opts.Cost.ModelCalls),
		// A nil ModelCalls map means the caller did not instrument calls, so the
		// count is unknown (not a real zero) and a call budget must fail closed.
		ModelCallsKnown: opts.Cost.ModelCalls != nil,
		Delta:           delta,
	})
	// Fail closed: a release can only be trusted when the run finished
	// successfully and both phases carry comparable per-case data. A still-running
	// or failed run may retain an accepted round, and a slimmed RunResult that
	// omits evaluation cases would hide regressions and release on aggregate gain
	// alone — both must be rejected rather than released.
	gate = applyReleasePreconditions(gate, result, candidateValidation, acceptedRound, opts.ExpectedMetrics)

	// Project the accepted candidate's surfaces only when a round was actually
	// accepted; the engine keeps the initial profile as AcceptedProfile when every
	// round is rejected, and rendering those baseline overrides under "Accepted
	// candidate" would misattribute them.
	var acceptedSurfaces []SurfaceProjection
	if acceptedRound > 0 {
		acceptedSurfaces = candidateSurfaces(result.AcceptedProfile)
	}

	// The manager populates RunResult.AppName; when present it is the
	// authoritative identity, and the option only labels direct engine runs
	// where that field is empty.
	app := result.AppName
	if app == "" {
		app = opts.AppName
	}

	return &Report{
		App:      app,
		Mode:     opts.Mode,
		Status:   string(result.Status),
		Baseline: baseline,
		Candidate: CandidateScore{
			OverallScore:    candidate.OverallScore,
			ProfileAccepted: acceptedRound > 0,
			AcceptedRound:   acceptedRound,
			Surfaces:        acceptedSurfaces,
		},
		Delta:              delta,
		FailureAttribution: attribution,
		Gate:               gate,
		Cost:               costReport(result, opts.Cost),
		Rounds:             roundReports(result),
		Config:             sanitizeConfig(opts.Config),
	}, nil
}

// sensitiveKeyFragments flags config keys whose values must never be
// serialized into the audit report.
var sensitiveKeyFragments = []string{
	"apikey", "api_key", "secret", "token", "password", "credential",
	"authorization", "bearer",
}

// isSensitiveKey reports whether a config key looks like it carries a secret.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// sanitizeConfig deep-copies an audit config, replacing every value stored
// under a sensitive-looking key with a redaction marker. The report is designed
// to be persisted (and often committed), so unlike the typed surface
// projection, an arbitrary caller-provided map must be scrubbed before it is
// serialized.
func sanitizeConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	return sanitizeMap(config)
}

func sanitizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for key, value := range m {
		if isSensitiveKey(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = sanitizeValue(value)
	}
	return out
}

func sanitizeValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return sanitizeMap(v)
	case map[string]string:
		out := make(map[string]string, len(v))
		for key, s := range v {
			if isSensitiveKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = s
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeValue(item)
		}
		return out
	default:
		return value
	}
}

// candidateSurfaces projects a profile down to a stable audit view: every
// override is kept, rendered as a (type, value) pair per SurfaceValue variant,
// so a non-text optimization (few-shot, model, tools, skills) cannot be
// released while absent from the audit.
func candidateSurfaces(profile *promptiter.Profile) []SurfaceProjection {
	if profile == nil {
		return nil
	}
	surfaces := make([]SurfaceProjection, 0, len(profile.Overrides))
	for _, override := range profile.Overrides {
		kind, value := projectSurfaceValue(override.Value)
		surfaces = append(surfaces, SurfaceProjection{SurfaceID: override.SurfaceID, Type: kind, Value: value})
	}
	if len(surfaces) == 0 {
		return nil
	}
	return surfaces
}

// projectSurfaceValue renders one SurfaceValue variant as a stable
// (type, value) pair. Model projections keep identity fields only; credentials
// (API key, base URL, headers) must never reach the audit report.
func projectSurfaceValue(v astructure.SurfaceValue) (string, string) {
	switch {
	case v.Text != nil:
		return "text", *v.Text
	case len(v.FewShot) > 0:
		return "fewShot", compactJSON(v.FewShot)
	case v.Model != nil:
		return "model", compactJSON(struct {
			Provider string `json:",omitempty"`
			Name     string `json:",omitempty"`
			Variant  string `json:",omitempty"`
		}{v.Model.Provider, v.Model.Name, v.Model.Variant})
	case len(v.Tools) > 0:
		return "tools", compactJSON(v.Tools)
	case len(v.Skills) > 0:
		return "skills", compactJSON(v.Skills)
	case v.PromptSyntax != nil:
		return "promptSyntax", string(*v.PromptSyntax)
	default:
		return "empty", ""
	}
}

// compactJSON renders a value as one-line JSON; a marshal failure is made
// visible in the report instead of dropping the surface.
func compactJSON(v any) string {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("unserializable: %v", err)
	}
	return string(payload)
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
// successfully, both phases must carry comparable per-case data with terminal
// metric evidence, and an accepted round must come with the actual accepted
// profile artifact.
func applyReleasePreconditions(gate GateResult, result *engine.RunResult, candidate *engine.EvaluationResult, acceptedRound int, expectedMetrics []string) GateResult {
	if result.Status != engine.RunStatusSucceeded {
		gate.Released = false
		gate.Reasons = append(gate.Reasons, fmt.Sprintf("run did not complete successfully (status %q)", result.Status))
		return gate
	}
	// A release publishes the accepted profile; a profile-slimmed result (e.g.
	// stored with OmitProfiles) keeps the acceptance decision but drops the
	// artifact, so there is nothing to publish and the gate must fail closed.
	if acceptedRound > 0 && result.AcceptedProfile == nil {
		gate.Released = false
		gate.Reasons = append(gate.Reasons, "accepted profile artifact unavailable (profile-slimmed result); nothing to publish")
	}
	if !hasComparableCases(result.BaselineValidation) || !hasComparableCases(candidate) {
		gate.Released = false
		gate.Reasons = append(gate.Reasons, "per-case results unavailable; cannot verify regressions")
	}
	phases := []struct {
		name   string
		result *engine.EvaluationResult
	}{{"baseline", result.BaselineValidation}, {"candidate", candidate}}
	for _, phase := range phases {
		if issue := metricEvidenceIssue(phase.name, phase.result, expectedMetrics); issue != "" {
			gate.Released = false
			gate.Reasons = append(gate.Reasons, issue)
		}
	}
	return gate
}

// metricEvidenceIssue reports the first incomplete-evidence problem in one
// phase, or "" when every case carries a terminal (passed/failed) result for
// every metric — including every expected metric name. A metric retained as
// not_evaluated is excluded from aggregate scoring, and a name that matches no
// registered evaluator is silently omitted from both phases; either way part of
// the validation never ran and an aggregate gain cannot be trusted.
func metricEvidenceIssue(phase string, result *engine.EvaluationResult, expectedMetrics []string) string {
	if result == nil {
		return ""
	}
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			present := make(map[string]bool, len(evalCase.Metrics))
			for _, m := range evalCase.Metrics {
				present[m.MetricName] = true
				if m.Status != status.EvalStatusPassed && m.Status != status.EvalStatusFailed {
					return fmt.Sprintf("%s metric %q on case %q has non-terminal status %q; evidence incomplete",
						phase, m.MetricName, evalCase.EvalCaseID, m.Status)
				}
			}
			for _, name := range expectedMetrics {
				if !present[name] {
					return fmt.Sprintf("%s case %q is missing expected metric %q; evidence incomplete",
						phase, evalCase.EvalCaseID, name)
				}
			}
		}
	}
	return ""
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
		Rounds:         rounds,
		EvaluatedCases: evaluatedCases,
		DurationMs:     cost.DurationMs,
		ModelCalls:     cost.ModelCalls,
		// A nil map means the caller did not instrument calls; the audit must not
		// present that as a measured zero-call run.
		ModelCallsKnown: cost.ModelCalls != nil,
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
	// Each round's delta is measured against the last accepted validation at the
	// round's start; the first round compares against baseline. A rejected round
	// does NOT advance this baseline, so the next round still compares against the
	// last version actually accepted.
	baseline := result.BaselineValidation
	for _, round := range result.Rounds {
		report := RoundReport{
			Round:          round.Round,
			OutputSurfaces: candidateSurfaces(round.OutputProfile),
			Validation:     phaseScore(round.Validation),
			Delta:          ComputeDelta(baseline, round.Validation),
		}
		if round.Validation != nil {
			report.ValidationScore = round.Validation.OverallScore
		}
		if round.Acceptance != nil {
			report.Accepted = round.Acceptance.Accepted
			report.ScoreDelta = round.Acceptance.ScoreDelta
			report.Reason = round.Acceptance.Reason
			if round.Acceptance.Accepted && round.Validation != nil {
				baseline = round.Validation
			}
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
	if len(r.FailureAttribution.TrainingTerminalLossesBySeverity) > 0 {
		fmt.Fprintf(&b, "Terminal-loss severity (training signal, accumulated across rounds): ")
		parts := make([]string, 0, len(r.FailureAttribution.TrainingTerminalLossesBySeverity))
		for _, sev := range sortedStringKeys(r.FailureAttribution.TrainingTerminalLossesBySeverity) {
			parts = append(parts, fmt.Sprintf("%s=%d", sev, r.FailureAttribution.TrainingTerminalLossesBySeverity[sev]))
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
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", s.SurfaceID, s.Type, s.Value)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Cost (estimated)\n\n")
	fmt.Fprintf(&b, "- rounds: %d\n- evaluated cases: %d\n- duration: %d ms\n",
		r.Cost.Rounds, r.Cost.EvaluatedCases, r.Cost.DurationMs)
	if r.Cost.ModelCallsKnown {
		fmt.Fprintf(&b, "- model calls: %d\n", r.Cost.TotalModelCalls)
		for _, role := range sortedStringKeys(r.Cost.ModelCalls) {
			fmt.Fprintf(&b, "  - %s: %d\n", role, r.Cost.ModelCalls[role])
		}
	} else {
		fmt.Fprintf(&b, "- model calls: unavailable (not instrumented)\n")
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
