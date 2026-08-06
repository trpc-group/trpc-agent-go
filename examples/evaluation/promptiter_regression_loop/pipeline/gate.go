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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
)

// DeltaClass describes how a single eval case moved between the baseline and the candidate.
type DeltaClass string

const (
	// DeltaUnchanged means neither pass status nor score changed.
	DeltaUnchanged DeltaClass = "unchanged"
	// DeltaNewPass means the case went from failing to passing (an improvement).
	DeltaNewPass DeltaClass = "new_pass"
	// DeltaNewFail means the case went from passing to failing (a regression).
	DeltaNewFail DeltaClass = "new_fail"
	// DeltaScoreUp means the pass status is unchanged but the score rose.
	DeltaScoreUp DeltaClass = "score_up"
	// DeltaScoreDown means the pass status is unchanged but the score fell.
	DeltaScoreDown DeltaClass = "score_down"
)

// CaseDelta is the per-case comparison between a baseline and candidate evaluation.
type CaseDelta struct {
	EvalCaseID      string     `json:"evalCaseId"`
	BaselinePassed  bool       `json:"baselinePassed"`
	CandidatePassed bool       `json:"candidatePassed"`
	BaselineScore   float64    `json:"baselineScore"`
	CandidateScore  float64    `json:"candidateScore"`
	ScoreDelta      float64    `json:"scoreDelta"`
	Class           DeltaClass `json:"class"`
}

// DiffResults compares two evaluation results case-by-case, matched by eval case ID. Cases present
// in the baseline drive the output order; a case missing from the candidate is treated as scoring
// zero and failing (so a dropped case reads as a regression rather than silently vanishing).
func DiffResults(baseline, candidate *evaluation.EvaluationResult) []CaseDelta {
	baseAttr := AttributeResult(baseline)
	candByID := indexAttributions(AttributeResult(candidate))
	deltas := make([]CaseDelta, 0, len(baseAttr))
	for _, base := range baseAttr {
		cand, ok := candByID[base.EvalCaseID]
		if !ok {
			cand = CaseAttribution{EvalCaseID: base.EvalCaseID, Passed: false, Score: 0}
		}
		deltas = append(deltas, classifyDelta(base, cand))
	}
	return deltas
}

func indexAttributions(attrs []CaseAttribution) map[string]CaseAttribution {
	byID := make(map[string]CaseAttribution, len(attrs))
	for _, a := range attrs {
		byID[a.EvalCaseID] = a
	}
	return byID
}

// classifyDelta assigns a DeltaClass. Pass-status transitions take priority over score movement,
// since a pass→fail (or fail→pass) is the decision-relevant event; score up/down applies only when
// the pass status is unchanged.
func classifyDelta(base, cand CaseAttribution) CaseDelta {
	delta := CaseDelta{
		EvalCaseID:      base.EvalCaseID,
		BaselinePassed:  base.Passed,
		CandidatePassed: cand.Passed,
		BaselineScore:   base.Score,
		CandidateScore:  cand.Score,
		ScoreDelta:      cand.Score - base.Score,
	}
	switch {
	case !base.Passed && cand.Passed:
		delta.Class = DeltaNewPass
	case base.Passed && !cand.Passed:
		delta.Class = DeltaNewFail
	case delta.ScoreDelta > 0:
		delta.Class = DeltaScoreUp
	case delta.ScoreDelta < 0:
		delta.Class = DeltaScoreDown
	default:
		delta.Class = DeltaUnchanged
	}
	return delta
}

// meanScore returns the mean case score of an evaluation result, 0 for an empty result.
func meanScore(result *evaluation.EvaluationResult) float64 {
	attrs := AttributeResult(result)
	if len(attrs) == 0 {
		return 0
	}
	var sum float64
	for _, a := range attrs {
		sum += a.Score
	}
	return sum / float64(len(attrs))
}

// meansFromDeltas computes the baseline and candidate mean scores over the matched delta set, so
// the aggregate shares the exact case set (and dropped-case handling) of the per-case deltas.
func meansFromDeltas(deltas []CaseDelta) (baseMean, candMean float64) {
	if len(deltas) == 0 {
		return 0, 0
	}
	var baseSum, candSum float64
	for _, d := range deltas {
		baseSum += d.BaselineScore
		candSum += d.CandidateScore
	}
	n := float64(len(deltas))
	return baseSum / n, candSum / n
}

// GatePolicy configures the multi-criterion acceptance gate. Each field maps to one criterion the
// issue enumerates: validation improvement, no new hard fail, key cases protected, and a budget.
type GatePolicy struct {
	// MinValidationGain is the minimum validation mean-score gain (candidate − baseline) required
	// to accept. A candidate that does not clear this bar is rejected as not-worth-it.
	MinValidationGain float64
	// KeyCaseIDs are business-critical validation cases that must not regress pass→fail. This is a
	// stricter, explicitly-named subset of the no-new-hard-fail criterion.
	KeyCaseIDs []string
	// MaxCandidateModelCalls caps the candidate model invocations the run may spend. Zero disables
	// the budget criterion (it then passes trivially). Non-zero enables cost/call-count budgeting.
	MaxCandidateModelCalls int
}

// GateObservations carries the measured run facts the budget criterion needs. They are observed
// during the run (not derivable from the evaluation results alone) and passed into the gate.
type GateObservations struct {
	// CandidateModelCalls is the number of candidate model invocations spent during the run.
	CandidateModelCalls int
}

// GateCriterion is one pass/fail check within the gate, retained for the audit report.
type GateCriterion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// GateDecision is the outcome of applying the gate to a candidate on the validation set.
type GateDecision struct {
	Accepted bool `json:"accepted"`
	// Criteria are the individual checks in evaluation order.
	Criteria []GateCriterion `json:"criteria"`
	// Reasons summarizes, in human terms, why the candidate was accepted or rejected.
	Reasons []string `json:"reasons"`
	// Aggregates surfaced for the report.
	BaselineValidationMean  float64 `json:"baselineValidationMean"`
	CandidateValidationMean float64 `json:"candidateValidationMean"`
	ValidationGain          float64 `json:"validationGain"`
	// Regressions lists validation case IDs that went pass→fail.
	Regressions []string `json:"regressions,omitempty"`
	// KeyRegressions is the subset of Regressions that are configured key cases.
	KeyRegressions []string `json:"keyRegressions,omitempty"`
	// ValidationDeltas is the full per-case validation diff (for the report).
	ValidationDeltas []CaseDelta `json:"validationDeltas"`
}

// ApplyGate applies the acceptance gate to a candidate's validation result versus the baseline's.
// A candidate is accepted only when every enabled criterion passes:
//   - has_validation_evidence: at least one validation case was evaluated (fails closed otherwise)
//   - validation_improves:  validation mean gain ≥ MinValidationGain
//   - no_new_hard_fail:      no validation case regressed pass→fail
//   - key_cases_protected:   every configured key case exists in the candidate results AND passes
//   - within_budget:         candidate model calls ≤ MaxCandidateModelCalls (skipped when 0)
//
// no_new_hard_fail is the overfitting veto: a candidate that raises the aggregate while breaking a
// previously-passing case is rejected even though its mean went up. key_cases_protected is stricter
// and independent: it requires the named cases to be present and passing in the candidate, so it can
// veto a candidate that no_new_hard_fail would accept (e.g. a key case that failed at baseline and
// still fails is not a regression, but still violates key protection).
func ApplyGate(policy GatePolicy, baselineValidation, candidateValidation *evaluation.EvaluationResult, obs GateObservations) GateDecision {
	deltas := DiffResults(baselineValidation, candidateValidation)
	// Derive the means from the same matched case set as deltas so the aggregate always agrees with
	// the per-case numbers: a case dropped from the candidate is counted as 0 here (as DiffResults
	// scores it), rather than silently excluded from the denominator.
	baseMean, candMean := meansFromDeltas(deltas)
	gain := candMean - baseMean

	byID := make(map[string]CaseDelta, len(deltas))
	for _, d := range deltas {
		byID[d.EvalCaseID] = d
	}

	keyCases := make(map[string]bool, len(policy.KeyCaseIDs))
	for _, id := range policy.KeyCaseIDs {
		keyCases[id] = true
	}
	var regressions, keyRegressions []string
	for _, d := range deltas {
		if d.Class == DeltaNewFail {
			regressions = append(regressions, d.EvalCaseID)
			if keyCases[d.EvalCaseID] {
				keyRegressions = append(keyRegressions, d.EvalCaseID)
			}
		}
	}
	// key_cases_protected is enforced independently of no_new_hard_fail: each configured key ID must
	// be present in the candidate results (a misspelled/absent ID fails, rather than silently reading
	// as "retained pass") and must pass there.
	var keyMissing, keyFailing []string
	for _, id := range policy.KeyCaseIDs {
		d, ok := byID[id]
		if !ok {
			keyMissing = append(keyMissing, id)
			continue
		}
		if !d.CandidatePassed {
			keyFailing = append(keyFailing, id)
		}
	}

	hasEvidence := len(deltas) > 0
	improves := gain >= policy.MinValidationGain
	noHardFail := len(regressions) == 0
	keyProtected := len(keyMissing) == 0 && len(keyFailing) == 0
	withinBudget := policy.MaxCandidateModelCalls == 0 || obs.CandidateModelCalls <= policy.MaxCandidateModelCalls

	decision := GateDecision{
		Accepted:                hasEvidence && improves && noHardFail && keyProtected && withinBudget,
		BaselineValidationMean:  baseMean,
		CandidateValidationMean: candMean,
		ValidationGain:          gain,
		Regressions:             regressions,
		KeyRegressions:          keyRegressions,
		ValidationDeltas:        deltas,
	}
	decision.Criteria = []GateCriterion{
		{
			Name:   "has_validation_evidence",
			Passed: hasEvidence,
			Detail: evidenceDetail(len(deltas)),
		},
		{
			Name:   "validation_improves",
			Passed: improves,
			Detail: fmt.Sprintf("validation mean %.3f → %.3f (gain %+.3f, required ≥ %.3f)", baseMean, candMean, gain, policy.MinValidationGain),
		},
		{
			Name:   "no_new_hard_fail",
			Passed: noHardFail,
			Detail: hardFailDetail(regressions),
		},
		{
			Name:   "key_cases_protected",
			Passed: keyProtected,
			Detail: keyProtectedDetail(policy.KeyCaseIDs, keyMissing, keyFailing),
		},
		{
			Name:   "within_budget",
			Passed: withinBudget,
			Detail: budgetDetail(policy.MaxCandidateModelCalls, obs.CandidateModelCalls),
		},
	}
	decision.Reasons = buildReasons(decision)
	return decision
}

func evidenceDetail(n int) string {
	if n == 0 {
		return "no validation cases evaluated — gate fails closed"
	}
	return fmt.Sprintf("%d validation case(s) evaluated", n)
}

func hardFailDetail(regressions []string) string {
	if len(regressions) == 0 {
		return "no validation case regressed (pass→fail)"
	}
	return fmt.Sprintf("%d case(s) regressed pass→fail: %v", len(regressions), regressions)
}

func keyProtectedDetail(keyCaseIDs, keyMissing, keyFailing []string) string {
	if len(keyCaseIDs) == 0 {
		return "no key cases configured"
	}
	if len(keyMissing) == 0 && len(keyFailing) == 0 {
		return fmt.Sprintf("key case(s) %v all present and passing in candidate", keyCaseIDs)
	}
	parts := make([]string, 0, 2)
	if len(keyMissing) > 0 {
		parts = append(parts, fmt.Sprintf("not found in candidate results: %v", keyMissing))
	}
	if len(keyFailing) > 0 {
		parts = append(parts, fmt.Sprintf("not passing in candidate: %v", keyFailing))
	}
	return "KEY case issue — " + strings.Join(parts, "; ")
}

func budgetDetail(maxCalls, calls int) string {
	if maxCalls == 0 {
		return fmt.Sprintf("no budget configured (candidate model calls: %d)", calls)
	}
	return fmt.Sprintf("candidate model calls %d (budget ≤ %d)", calls, maxCalls)
}

// buildReasons renders the human-facing accept/reject rationale from the decision.
func buildReasons(d GateDecision) []string {
	if d.Accepted {
		return []string{fmt.Sprintf("accepted: validation improved %+.3f with no regressions", d.ValidationGain)}
	}
	reasons := make([]string, 0, len(d.Criteria))
	if len(d.KeyRegressions) > 0 {
		reasons = append(reasons, fmt.Sprintf("rejected: KEY validation case(s) %v regressed pass→fail (overfitting)", d.KeyRegressions))
	} else if len(d.Regressions) > 0 {
		reasons = append(reasons, fmt.Sprintf("rejected: validation case(s) %v regressed pass→fail", d.Regressions))
	}
	for _, c := range d.Criteria {
		switch c.Name {
		case "has_validation_evidence":
			if !c.Passed {
				reasons = append(reasons, "rejected: no validation evidence — gate fails closed")
			}
		case "validation_improves":
			if !c.Passed {
				reasons = append(reasons, fmt.Sprintf("rejected: %s", c.Detail))
			}
		case "key_cases_protected":
			// Report the key-case violation only when it is not already covered by the pass→fail
			// overfitting reason above (i.e. a missing key ID or a key case that was already failing).
			if !c.Passed && len(d.KeyRegressions) == 0 {
				reasons = append(reasons, fmt.Sprintf("rejected: %s", c.Detail))
			}
		case "within_budget":
			if !c.Passed {
				reasons = append(reasons, fmt.Sprintf("rejected: %s", c.Detail))
			}
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "rejected: acceptance criteria not met")
	}
	return reasons
}
