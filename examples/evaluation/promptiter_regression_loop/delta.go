//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

const (
	// TransitionStayedPass means both baseline and candidate passed the case.
	TransitionStayedPass = "stayed_pass"
	// TransitionStayedFail means both baseline and candidate failed the case.
	TransitionStayedFail = "stayed_fail"
	// TransitionFixed means the candidate passed a case that baseline failed.
	TransitionFixed = "fixed"
	// TransitionRegressed means the candidate failed a case that baseline passed.
	TransitionRegressed = "regressed"
)

// ComputeDelta compares candidate validation cases against the baseline.
func ComputeDelta(baseline, candidate EvaluationRun) DeltaSummary {
	baselineByID := make(map[string]CaseResult, len(baseline.Cases))
	for _, evalCase := range baseline.Cases {
		baselineByID[evalCase.CaseID] = evalCase
	}
	delta := DeltaSummary{
		BaselineScore:  baseline.OverallScore,
		CandidateScore: candidate.OverallScore,
		ScoreDelta:     candidate.OverallScore - baseline.OverallScore,
		Cases:          make([]CaseDelta, 0, len(candidate.Cases)),
	}
	for _, candidateCase := range candidate.Cases {
		baselineCase := baselineByID[candidateCase.CaseID]
		caseDelta := CaseDelta{
			CaseID:          candidateCase.CaseID,
			Critical:        candidateCase.Critical || baselineCase.Critical,
			BaselineScore:   baselineCase.Score,
			CandidateScore:  candidateCase.Score,
			ScoreDelta:      candidateCase.Score - baselineCase.Score,
			BaselineStatus:  baselineCase.Status,
			CandidateStatus: candidateCase.Status,
			FailureReasons:  candidateCase.FailureReasons,
		}
		caseDelta.Transition = classifyTransition(baselineCase.Status, candidateCase.Status)
		switch {
		case caseDelta.Transition == TransitionFixed:
			delta.NewlyPassed++
		case caseDelta.Transition == TransitionRegressed:
			delta.NewlyFailed++
		}
		if caseDelta.ScoreDelta > 0 {
			delta.Improved++
		}
		if caseDelta.ScoreDelta < 0 {
			delta.Regressed++
			if caseDelta.Critical {
				delta.CriticalRegressed++
			}
		}
		delta.Cases = append(delta.Cases, caseDelta)
	}
	return delta
}

func classifyTransition(baseline, candidate status.EvalStatus) string {
	switch {
	case baseline == status.EvalStatusPassed && candidate == status.EvalStatusPassed:
		return TransitionStayedPass
	case baseline == status.EvalStatusPassed && candidate != status.EvalStatusPassed:
		return TransitionRegressed
	case baseline != status.EvalStatusPassed && candidate == status.EvalStatusPassed:
		return TransitionFixed
	default:
		return TransitionStayedFail
	}
}
