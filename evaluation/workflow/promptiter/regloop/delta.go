//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

const (
	scoreEpsilon = 1e-9
	// statusAbsent marks a baseline metric that is missing from the candidate.
	statusAbsent = "absent"
)

// metricKey uniquely identifies one metric measurement across evaluation phases.
type metricKey struct {
	evalSetID  string
	evalCaseID string
	metricName string
}

// phaseScore projects one engine evaluation result into the report schema.
func phaseScore(result *engine.EvaluationResult) PhaseScore {
	if result == nil {
		return PhaseScore{EvalSets: []EvalSetScore{}}
	}
	sets := make([]EvalSetScore, 0, len(result.EvalSets))
	for _, set := range result.EvalSets {
		cases := make([]CaseScore, 0, len(set.Cases))
		for _, evalCase := range set.Cases {
			metrics := make([]MetricScore, 0, len(evalCase.Metrics))
			for _, m := range evalCase.Metrics {
				metrics = append(metrics, MetricScore{
					MetricName: m.MetricName,
					Score:      m.Score,
					Status:     string(m.Status),
					Reason:     m.Reason,
				})
			}
			cases = append(cases, CaseScore{EvalCaseID: evalCase.EvalCaseID, Metrics: metrics})
		}
		sets = append(sets, EvalSetScore{
			EvalSetID:    set.EvalSetID,
			OverallScore: set.OverallScore,
			Cases:        cases,
		})
	}
	return PhaseScore{OverallScore: result.OverallScore, EvalSets: sets}
}

// acceptedValidation returns the validation result to compare against baseline
// and the round it was accepted in. It uses the last accepted round; when no
// round is accepted the engine keeps the baseline as the accepted profile, so
// the reporting candidate is the baseline itself (no improvement, no release).
// It deliberately does NOT fall back to a rejected round's validation, which
// would let the report present and release a candidate the engine rejected.
func acceptedValidation(result *engine.RunResult) (*engine.EvaluationResult, int) {
	if result == nil {
		return nil, 0
	}
	var accepted *engine.EvaluationResult
	acceptedRound := 0
	for _, round := range result.Rounds {
		if round.Acceptance != nil && round.Acceptance.Accepted && round.Validation != nil {
			accepted = round.Validation
			acceptedRound = round.Round
		}
	}
	if accepted != nil {
		return accepted, acceptedRound
	}
	return result.BaselineValidation, 0
}

// ComputeDelta pairs baseline and candidate metric measurements by
// (evalSetID, evalCaseID, metricName) and classifies each movement. It iterates
// the union of baseline and candidate keys so a metric that existed at baseline
// but is absent from the candidate is reported as a regression rather than
// silently dropped. New candidate-only metrics have no baseline to compare and
// are skipped.
func ComputeDelta(baseline, candidate *engine.EvaluationResult) DeltaReport {
	baselineIndex := indexMetrics(baseline)
	candidateIndex := indexMetrics(candidate)
	deltas := make([]CaseDelta, 0)
	summary := DeltaSummary{}
	for _, key := range unionKeys(baselineIndex, candidateIndex) {
		base, hasBase := baselineIndex[key]
		cand, hasCand := candidateIndex[key]
		var d CaseDelta
		switch {
		case hasBase && hasCand:
			d = CaseDelta{
				EvalSetID:       key.evalSetID,
				EvalCaseID:      key.evalCaseID,
				MetricName:      key.metricName,
				BaselineScore:   base.Score,
				CandidateScore:  cand.Score,
				BaselineStatus:  string(base.Status),
				CandidateStatus: string(cand.Status),
				Kind:            classifyDelta(base, cand),
			}
		case hasBase:
			d = CaseDelta{
				EvalSetID:       key.evalSetID,
				EvalCaseID:      key.evalCaseID,
				MetricName:      key.metricName,
				BaselineScore:   base.Score,
				CandidateScore:  0,
				BaselineStatus:  string(base.Status),
				CandidateStatus: statusAbsent,
				Kind:            droppedKind(base),
			}
		default:
			continue
		}
		addToSummary(&summary, d.Kind)
		deltas = append(deltas, d)
	}
	return DeltaReport{CaseDeltas: deltas, Summary: summary}
}

// droppedKind classifies a baseline metric that disappeared from the candidate.
// A previously passing metric becomes a new failure; a previously failing metric
// that carried a positive partial score is a score drop; a metric that was
// already at zero has nothing to lose and stays unchanged (avoids reporting a
// 0 -> 0 "score down").
func droppedKind(base engine.MetricResult) DeltaKind {
	switch {
	case base.Status == status.EvalStatusPassed:
		return DeltaNewlyFailed
	case base.Score > scoreEpsilon:
		return DeltaScoreDown
	default:
		return DeltaUnchanged
	}
}

func addToSummary(summary *DeltaSummary, kind DeltaKind) {
	switch kind {
	case DeltaNewlyPassed:
		summary.NewlyPassed++
	case DeltaNewlyFailed:
		summary.NewlyFailed++
	case DeltaScoreUp:
		summary.ScoreUp++
	case DeltaScoreDown:
		summary.ScoreDown++
	default:
		summary.Unchanged++
	}
}

// unionKeys returns the sorted union of metric keys across both indexes.
func unionKeys(baseline, candidate map[metricKey]engine.MetricResult) []metricKey {
	set := make(map[metricKey]struct{}, len(baseline)+len(candidate))
	for key := range baseline {
		set[key] = struct{}{}
	}
	for key := range candidate {
		set[key] = struct{}{}
	}
	keys := make([]metricKey, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].evalSetID != keys[j].evalSetID {
			return keys[i].evalSetID < keys[j].evalSetID
		}
		if keys[i].evalCaseID != keys[j].evalCaseID {
			return keys[i].evalCaseID < keys[j].evalCaseID
		}
		return keys[i].metricName < keys[j].metricName
	})
	return keys
}

func classifyDelta(base, cand engine.MetricResult) DeltaKind {
	basePassed := base.Status == status.EvalStatusPassed
	candPassed := cand.Status == status.EvalStatusPassed
	switch {
	case !basePassed && candPassed:
		return DeltaNewlyPassed
	case basePassed && !candPassed:
		return DeltaNewlyFailed
	case cand.Score > base.Score+scoreEpsilon:
		return DeltaScoreUp
	case cand.Score < base.Score-scoreEpsilon:
		return DeltaScoreDown
	default:
		return DeltaUnchanged
	}
}

func indexMetrics(result *engine.EvaluationResult) map[metricKey]engine.MetricResult {
	index := map[metricKey]engine.MetricResult{}
	forEachMetric(result, func(key metricKey, m engine.MetricResult) {
		index[key] = m
	})
	return index
}

func forEachMetric(result *engine.EvaluationResult, fn func(metricKey, engine.MetricResult)) {
	if result == nil {
		return
	}
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			for _, m := range evalCase.Metrics {
				fn(metricKey{
					evalSetID:  set.EvalSetID,
					evalCaseID: evalCase.EvalCaseID,
					metricName: m.MetricName,
				}, m)
			}
		}
	}
}
