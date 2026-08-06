//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

// DeltaOutcome describes how one validation case changed between baseline and candidate.
type DeltaOutcome string

const (
	// DeltaNewlyPassed means the case failed at baseline and passes on the candidate.
	DeltaNewlyPassed DeltaOutcome = "newly_passed"
	// DeltaNewlyFailed means the case passed at baseline and fails on the candidate.
	DeltaNewlyFailed DeltaOutcome = "newly_failed"
	// DeltaScoreImproved means the case passed in both and the candidate score is higher.
	DeltaScoreImproved DeltaOutcome = "score_improved"
	// DeltaScoreRegressed means the case passed in both and the candidate score is lower.
	DeltaScoreRegressed DeltaOutcome = "score_regressed"
	// DeltaUnchanged means the case outcome and score did not meaningfully change.
	DeltaUnchanged DeltaOutcome = "unchanged"
)

// CaseDelta compares one eval case between baseline and candidate runs.
type CaseDelta struct {
	EvalCaseID      string
	BaselinePassed  bool
	CandidatePassed bool
	BaselineScore   float64
	CandidateScore  float64
	Delta           float64
	Outcome         DeltaOutcome
}

// DeltaSummary aggregates the per-case deltas into counters.
type DeltaSummary struct {
	Total             int
	NewlyPassed       int
	NewlyFailed       int
	ScoreImproved     int
	ScoreRegressed    int
	Unchanged         int
	PassedAtCandidate int
}

// ComputeDeltas compares every baseline validation case with the same candidate
// validation case. Cases present in only one side are skipped with a note.
func ComputeDeltas(baseline, candidate []CaseScore) []CaseDelta {
	deltas := make([]CaseDelta, 0, len(candidate))
	for _, candidateCase := range candidate {
		baselineCase := findCase(baseline, candidateCase.EvalCaseID)
		baselineScore := 0.0
		baselinePassed := false
		if baselineCase != nil {
			baselineScore = baselineCase.Score
			baselinePassed = baselineCase.Passed
		}
		delta := candidateCase.Score - baselineScore
		deltas = append(deltas, CaseDelta{
			EvalCaseID:      candidateCase.EvalCaseID,
			BaselinePassed:  baselinePassed,
			CandidatePassed: candidateCase.Passed,
			BaselineScore:   baselineScore,
			CandidateScore:  candidateCase.Score,
			Delta:           delta,
			Outcome:         outcomeFor(baselinePassed, candidateCase.Passed, baselineScore, candidateCase.Score),
		})
	}
	return deltas
}

// SummarizeDeltas turns the per-case deltas into counters.
func SummarizeDeltas(deltas []CaseDelta) DeltaSummary {
	summary := DeltaSummary{Total: len(deltas)}
	for _, delta := range deltas {
		switch delta.Outcome {
		case DeltaNewlyPassed:
			summary.NewlyPassed++
		case DeltaNewlyFailed:
			summary.NewlyFailed++
		case DeltaScoreImproved:
			summary.ScoreImproved++
		case DeltaScoreRegressed:
			summary.ScoreRegressed++
		default:
			summary.Unchanged++
		}
		if delta.CandidatePassed {
			summary.PassedAtCandidate++
		}
	}
	return summary
}

// outcomeFor classifies one case comparison.
func outcomeFor(baselinePassed, candidatePassed bool, baselineScore, candidateScore float64) DeltaOutcome {
	switch {
	case !baselinePassed && candidatePassed:
		return DeltaNewlyPassed
	case baselinePassed && !candidatePassed:
		return DeltaNewlyFailed
	case baselinePassed && candidatePassed:
		switch {
		case candidateScore > baselineScore:
			return DeltaScoreImproved
		case candidateScore < baselineScore:
			return DeltaScoreRegressed
		default:
			return DeltaUnchanged
		}
	default:
		return DeltaUnchanged
	}
}

// findCase returns the case score for the given id, or nil.
func findCase(cases []CaseScore, evalCaseID string) *CaseScore {
	for i := range cases {
		if cases[i].EvalCaseID == evalCaseID {
			return &cases[i]
		}
	}
	return nil
}
