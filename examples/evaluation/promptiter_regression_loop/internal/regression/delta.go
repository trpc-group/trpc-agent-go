// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const scoreEpsilon = 1e-9

type caseKey struct {
	evalSetID string
	caseID    string
}

// Compare requires identical case and metric coverage before classifying
// changes. Missing validation evidence is never treated as an unchanged score.
func Compare(baseline, candidate *EvaluationResult) (*DeltaSummary, error) {
	baselineCases, err := indexCases("baseline", baseline)
	if err != nil {
		return nil, err
	}
	candidateCases, err := indexCases("candidate", candidate)
	if err != nil {
		return nil, err
	}
	if err := sameCaseKeys(baselineCases, candidateCases); err != nil {
		return nil, err
	}
	keys := make([]caseKey, 0, len(baselineCases))
	for key := range baselineCases {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].evalSetID != keys[j].evalSetID {
			return keys[i].evalSetID < keys[j].evalSetID
		}
		return keys[i].caseID < keys[j].caseID
	})
	result := &DeltaSummary{
		ScoreDelta: candidate.OverallScore - baseline.OverallScore,
		Counts:     make(map[DeltaKind]int),
		Cases:      make([]CaseDelta, 0, len(keys)),
	}
	if !finite(result.ScoreDelta) {
		return nil, errors.New("evaluation score delta is not finite")
	}
	for _, key := range keys {
		item, err := compareCase(baselineCases[key], candidateCases[key])
		if err != nil {
			return nil, fmt.Errorf("compare case %q in eval set %q: %w", key.caseID, key.evalSetID, err)
		}
		result.Cases = append(result.Cases, item)
		result.Counts[item.Kind]++
	}
	return result, nil
}

func indexCases(label string, result *EvaluationResult) (map[caseKey]CaseResult, error) {
	if result == nil {
		return nil, fmt.Errorf("%s evaluation is nil", label)
	}
	if len(result.Cases) == 0 {
		return nil, fmt.Errorf("%s evaluation has no cases", label)
	}
	index := make(map[caseKey]CaseResult, len(result.Cases))
	for _, evalCase := range result.Cases {
		if strings.TrimSpace(evalCase.EvalSetID) == "" || strings.TrimSpace(evalCase.CaseID) == "" {
			return nil, fmt.Errorf("%s evaluation contains an empty case identity", label)
		}
		if !finite(evalCase.Score) {
			return nil, fmt.Errorf("%s case %q score is not finite", label, evalCase.CaseID)
		}
		key := caseKey{evalSetID: evalCase.EvalSetID, caseID: evalCase.CaseID}
		if _, ok := index[key]; ok {
			return nil, fmt.Errorf("%s evaluation contains duplicate case %q", label, evalCase.CaseID)
		}
		index[key] = evalCase
	}
	return index, nil
}

func sameCaseKeys(left, right map[caseKey]CaseResult) error {
	if len(left) != len(right) {
		return fmt.Errorf("case sets differ: baseline=%d candidate=%d", len(left), len(right))
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return fmt.Errorf("candidate is missing case %q in eval set %q", key.caseID, key.evalSetID)
		}
	}
	return nil
}

func compareCase(baseline, candidate CaseResult) (CaseDelta, error) {
	baselineMetrics, err := indexMetrics("baseline", baseline.Metrics)
	if err != nil {
		return CaseDelta{}, err
	}
	candidateMetrics, err := indexMetrics("candidate", candidate.Metrics)
	if err != nil {
		return CaseDelta{}, err
	}
	if err := sameMetricKeys(baselineMetrics, candidateMetrics); err != nil {
		return CaseDelta{}, err
	}
	names := make([]string, 0, len(baselineMetrics))
	for name := range baselineMetrics {
		names = append(names, name)
	}
	sort.Strings(names)
	item := CaseDelta{
		EvalSetID:       baseline.EvalSetID,
		CaseID:          baseline.CaseID,
		BaselineScore:   baseline.Score,
		CandidateScore:  candidate.Score,
		ScoreDelta:      candidate.Score - baseline.Score,
		BaselinePassed:  baseline.Passed,
		CandidatePassed: candidate.Passed,
		Kind:            classifyDelta(baseline.Score, baseline.Passed, candidate.Score, candidate.Passed),
		Metrics:         make([]MetricDelta, 0, len(names)),
	}
	for _, name := range names {
		left, right := baselineMetrics[name], candidateMetrics[name]
		if math.Abs(left.Threshold-right.Threshold) > scoreEpsilon {
			return CaseDelta{}, fmt.Errorf("metric %q threshold changed from %.4f to %.4f",
				name, left.Threshold, right.Threshold)
		}
		metricDelta := MetricDelta{
			Name:            name,
			BaselineScore:   left.Score,
			CandidateScore:  right.Score,
			ScoreDelta:      right.Score - left.Score,
			BaselineStatus:  left.Status,
			CandidateStatus: right.Status,
			Kind: classifyDelta(
				left.Score, left.Status == status.EvalStatusPassed,
				right.Score, right.Status == status.EvalStatusPassed,
			),
		}
		item.Metrics = append(item.Metrics, metricDelta)
	}
	return item, nil
}

func indexMetrics(label string, metrics []MetricResult) (map[string]MetricResult, error) {
	if len(metrics) == 0 {
		return nil, fmt.Errorf("%s case has no metrics", label)
	}
	index := make(map[string]MetricResult, len(metrics))
	for _, metricResult := range metrics {
		name := strings.TrimSpace(metricResult.Name)
		if name == "" {
			return nil, fmt.Errorf("%s case has an empty metric name", label)
		}
		if _, ok := index[name]; ok {
			return nil, fmt.Errorf("%s case has duplicate metric %q", label, name)
		}
		if !finite(metricResult.Score) || !finite(metricResult.Threshold) {
			return nil, fmt.Errorf("%s metric %q has a non-finite value", label, name)
		}
		index[name] = metricResult
	}
	return index, nil
}

func sameMetricKeys(left, right map[string]MetricResult) error {
	if len(left) != len(right) {
		return fmt.Errorf("metric sets differ: baseline=%d candidate=%d", len(left), len(right))
	}
	for name := range left {
		if _, ok := right[name]; !ok {
			return fmt.Errorf("candidate is missing metric %q", name)
		}
	}
	return nil
}

func classifyDelta(baselineScore float64, baselinePassed bool, candidateScore float64, candidatePassed bool) DeltaKind {
	switch {
	case !baselinePassed && candidatePassed:
		return DeltaNewPass
	case baselinePassed && !candidatePassed:
		return DeltaNewFail
	case candidateScore > baselineScore+scoreEpsilon:
		return DeltaImproved
	case candidateScore+scoreEpsilon < baselineScore:
		return DeltaDeclined
	default:
		return DeltaUnchanged
	}
}
