//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// GateConfig controls the accept/reject decision for a candidate prompt.
type GateConfig struct {
	// MinValidationGain requires the candidate validation score to improve over
	// the baseline by at least this amount to be accepted.
	MinValidationGain float64
	// AllowRegression, when false, rejects the candidate if its validation
	// score is below the baseline. This is the key "overfitting" guard: a
	// candidate that only improves the training set but regresses on the
	// validation set must be rejected.
	AllowRegression bool
	// MaxNewHardFails caps how many new hard-fail cases the candidate may
	// introduce relative to the baseline.
	MaxNewHardFails int
	// KeyCaseIDs are eval case IDs that must never regress. A drop on any of
	// these forces rejection.
	KeyCaseIDs []string
	// CostBudget, when > 0, rejects the candidate if the estimated run cost
	// exceeds it. 0 means the budget is unlimited.
	CostBudget float64
	// CostUsed is the estimated cost of the run, populated by the loop before
	// calling decideAcceptance.
	CostUsed float64
}

// GateDecision is the outcome of decideAcceptance.
type GateDecision struct {
	Accepted   bool    `json:"accepted"`
	ScoreDelta float64 `json:"scoreDelta"`
	Reason     string  `json:"reason"`
	RejectedBy string  `json:"rejectedBy,omitempty"`
}

// decideAcceptance compares the baseline and candidate validation results and
// returns an accept/reject decision. It is deterministic and does not call a
// model, so it can be unit tested without an API key.
func decideAcceptance(
	baseline, candidate *promptiterengine.EvaluationResult,
	cfg GateConfig,
) *GateDecision {
	bs := overallScore(baseline)
	cs := overallScore(candidate)
	delta := cs - bs
	d := &GateDecision{ScoreDelta: delta, Accepted: true}

	if !cfg.AllowRegression && cs < bs-1e-9 {
		d.Accepted = false
		d.RejectedBy = "validation_regression"
		d.Reason = "candidate validation score regressed below baseline"
		return d
	}
	if cfg.MinValidationGain > 0 && delta < cfg.MinValidationGain {
		d.Accepted = false
		d.RejectedBy = "insufficient_gain"
		d.Reason = "validation score gain below minimum requirement"
		return d
	}
	// Key cases must not regress.
	baseByCase := caseScoreMap(baseline)
	candByCase := caseScoreMap(candidate)
	for _, kc := range cfg.KeyCaseIDs {
		b, ok := baseByCase[kc]
		if !ok {
			continue
		}
		if c, ok2 := candByCase[kc]; ok2 && c < b-1e-9 {
			d.Accepted = false
			d.RejectedBy = "key_case_regression"
			d.Reason = "key case " + kc + " regressed on candidate"
			return d
		}
	}
	// Reject when too many new hard-fail cases are introduced.
	newHard := 0
	for id := range hardFailCaseIDs(candidate) {
		if !hardFailCaseIDs(baseline)[id] {
			newHard++
		}
	}
	if newHard > cfg.MaxNewHardFails {
		d.Accepted = false
		d.RejectedBy = "new_hard_fails"
		d.Reason = "candidate introduced new hard-fail cases"
		return d
	}
	// Cost budget guard: reject when the estimated run cost exceeds the budget.
	if cfg.CostBudget > 0 && cfg.CostUsed > cfg.CostBudget {
		d.Accepted = false
		d.RejectedBy = "cost_budget_exceeded"
		d.Reason = "estimated run cost exceeded budget"
		return d
	}
	if d.Reason == "" {
		d.Reason = "accepted: validation gain met, no regression or new hard fails"
	}
	return d
}

// overallScore returns the mean of the per-eval-set scores.
func overallScore(r *promptiterengine.EvaluationResult) float64 {
	if r == nil {
		return 0
	}
	var sum float64
	var n int
	for _, es := range r.EvalSets {
		sum += es.OverallScore
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// caseScore returns the mean metric score of a single eval case.
func caseScore(c promptiterengine.CaseResult) float64 {
	if len(c.Metrics) == 0 {
		return 0
	}
	var sum float64
	for _, m := range c.Metrics {
		sum += m.Score
	}
	return sum / float64(len(c.Metrics))
}

// caseScoreMap returns a map from eval case id to its mean metric score.
func caseScoreMap(r *promptiterengine.EvaluationResult) map[string]float64 {
	m := map[string]float64{}
	if r == nil {
		return m
	}
	for _, es := range r.EvalSets {
		for _, c := range es.Cases {
			m[c.EvalCaseID] = caseScore(c)
		}
	}
	return m
}

// hardFailCaseIDs returns the set of eval case ids that contain at least one
// failed metric.
func hardFailCaseIDs(r *promptiterengine.EvaluationResult) map[string]bool {
	m := map[string]bool{}
	if r == nil {
		return m
	}
	for _, es := range r.EvalSets {
		for _, c := range es.Cases {
			for _, mt := range c.Metrics {
				if isFailedMetric(mt) {
					m[c.EvalCaseID] = true
					break
				}
			}
		}
	}
	return m
}

// perCaseDeltas computes the per-case change from baseline to candidate,
// distinguishing newly-passed / newly-failed cases from pure score moves.
func perCaseDeltas(baseline, candidate *promptiterengine.EvaluationResult) []CaseDelta {
	evalSetOf := map[string]string{}
	baseMap := caseScoreMapWithSet(baseline, evalSetOf)
	candMap := caseScoreMapWithSet(candidate, evalSetOf)
	basePass := casePassedMap(baseline)
	candPass := casePassedMap(candidate)
	ids := map[string]struct{}{}
	for id := range baseMap {
		ids[id] = struct{}{}
	}
	for id := range candMap {
		ids[id] = struct{}{}
	}
	deltas := make([]CaseDelta, 0, len(ids))
	for id := range ids {
		b := baseMap[id]
		c := candMap[id]
		delta := c - b
		bp := basePass[id]
		cp := candPass[id]
		trend := "flat"
		if delta > 1e-9 {
			trend = "up"
		} else if delta < -1e-9 {
			trend = "down"
		}
		deltas = append(deltas, CaseDelta{
			EvalSetID:       evalSetOf[id],
			EvalCaseID:      id,
			BaselineScore:   b,
			CandidateScore:  c,
			Delta:           delta,
			Trend:           trend,
			BaselinePassed:  bp,
			CandidatePassed: cp,
			Transition:      transitionOf(bp, cp, trend),
		})
	}
	return deltas
}

// transitionOf classifies a case's outcome change. A pass/fail status flip takes
// precedence (new_pass / new_fail); otherwise the score direction is reported
// (score_up / score_down / flat).
func transitionOf(baselinePassed, candidatePassed bool, trend string) string {
	switch {
	case !baselinePassed && candidatePassed:
		return "new_pass"
	case baselinePassed && !candidatePassed:
		return "new_fail"
	case trend == "up":
		return "score_up"
	case trend == "down":
		return "score_down"
	default:
		return "flat"
	}
}

// casePassed reports whether a case passed (every metric scored >= the pass
// threshold).
func casePassed(c promptiterengine.CaseResult) bool {
	if len(c.Metrics) == 0 {
		return false
	}
	for _, m := range c.Metrics {
		if isFailedMetric(m) {
			return false
		}
	}
	return true
}

// casePassedMap returns a map from eval case id to its pass/fail status.
func casePassedMap(r *promptiterengine.EvaluationResult) map[string]bool {
	m := map[string]bool{}
	if r == nil {
		return m
	}
	for _, es := range r.EvalSets {
		for _, c := range es.Cases {
			m[c.EvalCaseID] = casePassed(c)
		}
	}
	return m
}

func caseScoreMapWithSet(r *promptiterengine.EvaluationResult, evalSetOf map[string]string) map[string]float64 {
	m := map[string]float64{}
	if r == nil {
		return m
	}
	for _, es := range r.EvalSets {
		for _, c := range es.Cases {
			m[c.EvalCaseID] = caseScore(c)
			if _, ok := evalSetOf[c.EvalCaseID]; !ok {
				evalSetOf[c.EvalCaseID] = es.EvalSetID
			}
		}
	}
	return m
}
