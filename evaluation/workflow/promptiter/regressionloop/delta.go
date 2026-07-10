//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

const (
	scoreEpsilon = 1e-9
	statusAbsent = "absent"
)

type metricKey struct {
	evalSetID  string
	evalCaseID string
	metricName string
}

// ComputeDelta compares validation metrics by eval set, case, and metric name.
func ComputeDelta(
	baseline *promptiterengine.EvaluationResult,
	candidate *promptiterengine.EvaluationResult,
	criticalCaseIDs []string,
) DeltaReport {
	baselineIndex := indexMetrics(baseline)
	candidateIndex := indexMetrics(candidate)
	critical := setOf(criticalCaseIDs)
	keys := unionMetricKeys(baselineIndex, candidateIndex)
	report := DeltaReport{
		OverallScoreDelta: scoreOf(candidate) - scoreOf(baseline),
		Cases:             make([]CaseDelta, 0, len(keys)),
	}
	for _, key := range keys {
		base, hasBase := baselineIndex[key]
		cand, hasCand := candidateIndex[key]
		if !hasBase {
			delta := CaseDelta{
				EvalSetID:       key.evalSetID,
				EvalCaseID:      key.evalCaseID,
				MetricName:      key.metricName,
				BaselineStatus:  statusAbsent,
				CandidateScore:  cand.Score,
				CandidateStatus: string(cand.Status),
				ScoreDelta:      cand.Score,
				Kind:            addedMetricKind(cand),
				Critical:        critical[key.evalCaseID],
			}
			addDeltaSummary(&report.Summary, delta.Kind)
			report.Cases = append(report.Cases, delta)
			continue
		}
		delta := CaseDelta{
			EvalSetID:      key.evalSetID,
			EvalCaseID:     key.evalCaseID,
			MetricName:     key.metricName,
			BaselineScore:  base.Score,
			BaselineStatus: string(base.Status),
			Critical:       critical[key.evalCaseID],
		}
		if hasCand {
			delta.CandidateScore = cand.Score
			delta.CandidateStatus = string(cand.Status)
			delta.ScoreDelta = cand.Score - base.Score
			delta.Kind = classifyDelta(base, cand)
		} else {
			delta.CandidateStatus = statusAbsent
			delta.ScoreDelta = -base.Score
			delta.Kind = droppedMetricKind(base)
		}
		addDeltaSummary(&report.Summary, delta.Kind)
		report.Cases = append(report.Cases, delta)
	}
	return report
}

// AcceptedValidation returns the last PromptIter-accepted validation result.
func AcceptedValidation(result *promptiterengine.RunResult) (*promptiterengine.EvaluationResult, int, bool) {
	if result == nil {
		return nil, 0, false
	}
	var accepted *promptiterengine.EvaluationResult
	acceptedRound := 0
	for _, round := range result.Rounds {
		if round.Acceptance == nil || !round.Acceptance.Accepted || round.Validation == nil {
			continue
		}
		accepted = round.Validation
		acceptedRound = round.Round
	}
	if accepted != nil {
		return accepted, acceptedRound, true
	}
	return result.BaselineValidation, 0, false
}

// FinalCandidateValidation returns the latest round validation result, even
// when PromptIter did not accept that candidate.
func FinalCandidateValidation(result *promptiterengine.RunResult) (*promptiterengine.EvaluationResult, int, bool) {
	if result == nil {
		return nil, 0, false
	}
	for i := len(result.Rounds) - 1; i >= 0; i-- {
		round := result.Rounds[i]
		if round.Validation != nil {
			return round.Validation, round.Round, true
		}
	}
	return nil, 0, false
}

func classifyDelta(base, cand promptiterengine.MetricResult) DeltaKind {
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

func addedMetricKind(cand promptiterengine.MetricResult) DeltaKind {
	switch {
	case cand.Status != status.EvalStatusPassed:
		return DeltaNewlyFailed
	case cand.Score > scoreEpsilon:
		return DeltaScoreUp
	default:
		return DeltaUnchanged
	}
}

func droppedMetricKind(base promptiterengine.MetricResult) DeltaKind {
	switch {
	case base.Status == status.EvalStatusPassed:
		return DeltaNewlyFailed
	case base.Score > scoreEpsilon:
		return DeltaScoreDown
	default:
		return DeltaUnchanged
	}
}

func addDeltaSummary(summary *DeltaSummary, kind DeltaKind) {
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

func indexMetrics(result *promptiterengine.EvaluationResult) map[metricKey]promptiterengine.MetricResult {
	index := make(map[metricKey]promptiterengine.MetricResult)
	if result == nil {
		return index
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			for _, metric := range evalCase.Metrics {
				index[metricKey{
					evalSetID:  evalSet.EvalSetID,
					evalCaseID: evalCase.EvalCaseID,
					metricName: metric.MetricName,
				}] = metric
			}
		}
	}
	return index
}

func unionMetricKeys(
	baseline map[metricKey]promptiterengine.MetricResult,
	candidate map[metricKey]promptiterengine.MetricResult,
) []metricKey {
	seen := make(map[metricKey]struct{}, len(baseline)+len(candidate))
	for key := range baseline {
		seen[key] = struct{}{}
	}
	for key := range candidate {
		seen[key] = struct{}{}
	}
	keys := make([]metricKey, 0, len(seen))
	for key := range seen {
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

func scoreOf(result *promptiterengine.EvaluationResult) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}

func setOf(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
