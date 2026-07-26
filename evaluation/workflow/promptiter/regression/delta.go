//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package regression implements reusable PromptIter evaluation regression,
// release gating, failure attribution, reporting, and artifact persistence.
package regression

import (
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Transition describes a case-level quality and pass-state change.
type Transition string

// Transition values cover pass-state changes and score-only changes.
const (
	TransitionUnchangedPass Transition = "unchanged_pass"
	TransitionUnchangedFail Transition = "unchanged_fail"
	TransitionNewlyPassed   Transition = "newly_passed"
	TransitionNewlyFailed   Transition = "newly_failed"
	TransitionImproved      Transition = "improved"
	TransitionRegressed     Transition = "regressed"
)

// CaseDelta records one eval-set-qualified case's score and pass-state change.
type CaseDelta struct {
	EvalSetID      string     `json:"evalSetId"`
	CaseID         string     `json:"caseId"`
	BaselinePass   bool       `json:"baselinePass"`
	CandidatePass  bool       `json:"candidatePass"`
	BaselineScore  float64    `json:"baselineScore"`
	CandidateScore float64    `json:"candidateScore"`
	ScoreDelta     float64    `json:"scoreDelta"`
	Transition     Transition `json:"transition"`
}

// Delta records aggregate and per-case changes between two evaluations.
type Delta struct {
	ScoreDelta  float64     `json:"scoreDelta"`
	NewlyPassed []string    `json:"newlyPassed"`
	NewlyFailed []string    `json:"newlyFailed"`
	PerCase     []CaseDelta `json:"perCase"`
}

type caseState struct {
	present bool
	pass    bool
	score   float64
}

type caseKey struct {
	evalSetID string
	caseID    string
}

// Compare computes candidate changes against a baseline evaluation.
func Compare(baseline, candidate *engine.EvaluationResult) Delta {
	baselineCases := caseStates(baseline)
	candidateCases := caseStates(candidate)
	ids := make(map[caseKey]struct{}, len(baselineCases)+len(candidateCases))
	for id := range baselineCases {
		ids[id] = struct{}{}
	}
	for id := range candidateCases {
		ids[id] = struct{}{}
	}
	caseIDs := make([]caseKey, 0, len(ids))
	for id := range ids {
		caseIDs = append(caseIDs, id)
	}
	sort.Slice(caseIDs, func(i, j int) bool {
		if caseIDs[i].evalSetID != caseIDs[j].evalSetID {
			return caseIDs[i].evalSetID < caseIDs[j].evalSetID
		}
		return caseIDs[i].caseID < caseIDs[j].caseID
	})
	result := Delta{NewlyPassed: []string{}, NewlyFailed: []string{}, PerCase: make([]CaseDelta, 0, len(caseIDs))}
	if baseline != nil && candidate != nil {
		result.ScoreDelta = candidate.OverallScore - baseline.OverallScore
	}
	for _, id := range caseIDs {
		before := baselineCases[id]
		after := candidateCases[id]
		item := CaseDelta{
			EvalSetID: id.evalSetID, CaseID: id.caseID, BaselinePass: before.pass, CandidatePass: after.pass,
			BaselineScore: before.score, CandidateScore: after.score, ScoreDelta: after.score - before.score,
		}
		switch {
		case !before.pass && after.pass:
			item.Transition = TransitionNewlyPassed
			result.NewlyPassed = append(result.NewlyPassed, id.caseID)
		case before.pass && !after.pass:
			item.Transition = TransitionNewlyFailed
			result.NewlyFailed = append(result.NewlyFailed, id.caseID)
		case item.ScoreDelta > 0:
			item.Transition = TransitionImproved
		case item.ScoreDelta < 0:
			item.Transition = TransitionRegressed
		case before.pass:
			item.Transition = TransitionUnchangedPass
		default:
			item.Transition = TransitionUnchangedFail
		}
		result.PerCase = append(result.PerCase, item)
	}
	return result
}

func caseStates(result *engine.EvaluationResult) map[caseKey]caseState {
	states := make(map[caseKey]caseState)
	if result == nil {
		return states
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			state := caseState{present: true, pass: len(evalCase.Metrics) > 0}
			evaluated := 0
			for _, metric := range evalCase.Metrics {
				if metric.Status != status.EvalStatusPassed {
					state.pass = false
				}
				if metric.Status != status.EvalStatusNotEvaluated {
					state.score += metric.Score
					evaluated++
				}
			}
			if evaluated > 0 {
				state.score /= float64(evaluated)
			}
			evalSetID := evalCase.EvalSetID
			if evalSetID == "" {
				evalSetID = evalSet.EvalSetID
			}
			states[caseKey{evalSetID: evalSetID, caseID: evalCase.EvalCaseID}] = state
		}
	}
	return states
}
