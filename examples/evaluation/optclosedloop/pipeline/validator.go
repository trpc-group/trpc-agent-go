//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

// ComputeCaseDelta compares baseline and candidate evaluation summaries and
// returns per-case deltas plus aggregate changes.
//
// The resulting deltas feed directly into the acceptance gate decision and are
// persisted as part of the round audit record.
func ComputeCaseDelta(baseline, candidate *EvalSummary) ([]CaseDelta, int, int, error) {
	if baseline == nil {
		return nil, 0, 0, nilErr("baseline summary is nil")
	}
	if candidate == nil {
		return nil, 0, 0, nilErr("candidate summary is nil")
	}
	byID := make(map[string]CaseEval, len(candidate.PerCase))
	for _, c := range candidate.PerCase {
		byID[c.EvalCaseID] = c
	}

	deltas := make([]CaseDelta, 0, len(baseline.PerCase))
	newHardFail := 0
	keyCaseDegrade := 0
	for _, b := range baseline.PerCase {
		c, ok := byID[b.EvalCaseID]
		d := CaseDelta{
			EvalCaseID:     b.EvalCaseID,
			BaselinePassed: b.OverallPassed,
			BaselineScore:  meanScore(b.Metrics),
			Labels:         labelsOfCase(b.EvalCaseID),
		}
		if !ok {
			// Case missing from candidate: treat as hard fail.
			d.CandidatePassed = false
			d.CandidateScore = 0
			d.ScoreDelta = -d.BaselineScore
			d.IsHardFailNew = b.OverallPassed
			if d.IsHardFailNew {
				newHardFail++
			}
			if isKeyCase(b.EvalCaseID) && d.ScoreDelta < 0 {
				d.IsKeyCaseDegrade = true
				keyCaseDegrade++
			}
			deltas = append(deltas, d)
			continue
		}
		d.CandidatePassed = c.OverallPassed
		d.CandidateScore = meanScore(c.Metrics)
		d.ScoreDelta = d.CandidateScore - d.BaselineScore
		d.IsHardFailNew = b.OverallPassed && !c.OverallPassed
		if d.IsHardFailNew {
			newHardFail++
		}
		if isKeyCase(b.EvalCaseID) && d.ScoreDelta < -0.0001 {
			d.IsKeyCaseDegrade = true
			keyCaseDegrade++
		}
		deltas = append(deltas, d)
	}
	return deltas, newHardFail, keyCaseDegrade, nil
}

// Key cases are marked by label "hardfail_guard" or explicit config.
func isKeyCase(caseID string) bool {
	oc, ok := caseOutcomes[caseID]
	if !ok {
		return false
	}
	for _, l := range oc.Labels {
		if l == "hardfail_guard" {
			return true
		}
	}
	return false
}

func labelsOfCase(caseID string) []string {
	if oc, ok := caseOutcomes[caseID]; ok {
		return append([]string(nil), oc.Labels...)
	}
	return nil
}

func meanScore(metrics []CaseMetric) float64 {
	if len(metrics) == 0 {
		return 0
	}
	s := 0.0
	for _, m := range metrics {
		s += m.Score
	}
	return s / float64(len(metrics))
}

type nilErr string

func (e nilErr) Error() string { return string(e) }
