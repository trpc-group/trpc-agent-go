//
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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const scoreEpsilon = 1e-9

type caseKey struct {
	evalSetID string
	caseID    string
}

type scoreAccumulator struct {
	total   float64
	metrics int
}

// Compare evaluates case and metric changes from baseline to candidate.
func Compare(baseline, candidate *EvaluationResult) (*DeltaSummary, error) {
	baselineIndex, err := indexCases("baseline", baseline)
	if err != nil {
		return nil, err
	}
	candidateIndex, err := indexCases("candidate", candidate)
	if err != nil {
		return nil, err
	}
	keys := sortedCaseKeys(baselineIndex)
	if err := validateCandidateCases(keys, candidateIndex); err != nil {
		return nil, err
	}
	result := &DeltaSummary{
		ScoreDelta: candidate.OverallScore - baseline.OverallScore,
		Counts:     make(map[DeltaKind]int),
		Cases:      make([]CaseDelta, 0, len(keys)),
	}
	for _, key := range keys {
		caseDelta, err := compareCase(baselineIndex[key], candidateIndex[key])
		if err != nil {
			return nil, fmt.Errorf("compare case %q: %w", key.caseID, err)
		}
		result.Counts[caseDelta.Kind]++
		result.Cases = append(result.Cases, *caseDelta)
	}
	return result, nil
}

// SummarizeDelta removes repeated per-case details from a secondary delta.
func SummarizeDelta(input *DeltaSummary) (*DeltaOverview, error) {
	if input == nil {
		return nil, errors.New("delta summary is nil")
	}
	counts := make(map[DeltaKind]int, len(input.Counts))
	for kind, count := range input.Counts {
		counts[kind] = count
	}
	return &DeltaOverview{ScoreDelta: input.ScoreDelta, Counts: counts}, nil
}

func indexCases(label string, result *EvaluationResult) (map[caseKey]CaseResult, error) {
	if result == nil {
		return nil, fmt.Errorf("%s evaluation is nil", label)
	}
	if !finite(result.OverallScore) {
		return nil, fmt.Errorf("%s overall score is not finite", label)
	}
	index := make(map[caseKey]CaseResult, len(result.Cases))
	for _, item := range result.Cases {
		if item.EvalSetID == "" || item.CaseID == "" {
			return nil, fmt.Errorf("%s case identity is empty", label)
		}
		key := caseKey{evalSetID: item.EvalSetID, caseID: item.CaseID}
		if _, ok := index[key]; ok {
			return nil, fmt.Errorf("%s duplicate case %q", label, item.CaseID)
		}
		if !finite(item.Score) {
			return nil, fmt.Errorf("%s case %q score is not finite", label, item.CaseID)
		}
		index[key] = item
	}
	if len(index) == 0 {
		return nil, fmt.Errorf("%s evaluation has no cases", label)
	}
	aggregated, err := evaluationScoreFromCases(result.Cases)
	if err != nil {
		return nil, fmt.Errorf("%s evaluation summary is invalid: %w", label, err)
	}
	if math.Abs(aggregated-result.OverallScore) > scoreEpsilon {
		return nil, fmt.Errorf("%s overall score does not match case metrics", label)
	}
	return index, nil
}

func evaluationScoreFromCases(cases []CaseResult) (float64, error) {
	sets := make(map[string]scoreAccumulator)
	for _, item := range cases {
		caseScore, passedCase, aggregate, err := caseSummary(item)
		if err != nil {
			return 0, fmt.Errorf("case %q: %w", item.CaseID, err)
		}
		if math.Abs(caseScore-item.Score) > scoreEpsilon {
			return 0, errors.New("score does not match metrics")
		}
		if passedCase != item.Passed {
			return 0, errors.New("pass state does not match metrics and trace")
		}
		set := sets[item.EvalSetID]
		set.total += aggregate.total
		set.metrics += aggregate.metrics
		sets[item.EvalSetID] = set
	}
	return averageEvalSetScores(sets)
}

func caseSummary(item CaseResult) (float64, bool, scoreAccumulator, error) {
	if len(item.Metrics) == 0 {
		return 0, false, scoreAccumulator{}, errors.New("metrics are empty")
	}
	aggregate := scoreAccumulator{}
	passedCase := !traceIsFailure(item.Trace.Status)
	for _, metric := range item.Metrics {
		if !finite(metric.Score) {
			return 0, false, scoreAccumulator{}, fmt.Errorf("metric %q score is not finite", metric.Name)
		}
		switch metric.Status {
		case status.EvalStatusPassed:
			aggregate.total += metric.Score
			aggregate.metrics++
		case status.EvalStatusFailed:
			passedCase = false
			aggregate.total += metric.Score
			aggregate.metrics++
		case status.EvalStatusNotEvaluated:
			passedCase = false
		default:
			return 0, false, scoreAccumulator{}, fmt.Errorf("metric %q has invalid status %q", metric.Name, metric.Status)
		}
	}
	return averageScore(aggregate), passedCase, aggregate, nil
}

func averageEvalSetScores(sets map[string]scoreAccumulator) (float64, error) {
	if len(sets) == 0 {
		return 0, errors.New("evaluation has no cases")
	}
	ids := make([]string, 0, len(sets))
	for id := range sets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := 0.0
	for _, id := range ids {
		total += averageScore(sets[id])
	}
	return total / float64(len(ids)), nil
}

func averageScore(value scoreAccumulator) float64 {
	if value.metrics == 0 {
		return 0
	}
	return value.total / float64(value.metrics)
}

func sortedCaseKeys(index map[caseKey]CaseResult) []caseKey {
	keys := make([]caseKey, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].evalSetID != keys[j].evalSetID {
			return keys[i].evalSetID < keys[j].evalSetID
		}
		return keys[i].caseID < keys[j].caseID
	})
	return keys
}

func validateCandidateCases(keys []caseKey, candidate map[caseKey]CaseResult) error {
	if len(keys) != len(candidate) {
		return errors.New("candidate case set does not match baseline")
	}
	for _, key := range keys {
		if _, ok := candidate[key]; !ok {
			return fmt.Errorf("candidate is missing case %q", key.caseID)
		}
	}
	return nil
}

func compareCase(baseline, candidate CaseResult) (*CaseDelta, error) {
	metricDeltas, err := compareMetrics(baseline.Metrics, candidate.Metrics)
	if err != nil {
		return nil, err
	}
	return &CaseDelta{
		EvalSetID:       baseline.EvalSetID,
		CaseID:          baseline.CaseID,
		BaselineScore:   baseline.Score,
		CandidateScore:  candidate.Score,
		ScoreDelta:      candidate.Score - baseline.Score,
		BaselinePassed:  baseline.Passed,
		CandidatePassed: candidate.Passed,
		Kind:            classifyDelta(baseline.Score, candidate.Score, baseline.Passed, candidate.Passed),
		Metrics:         metricDeltas,
	}, nil
}

func compareMetrics(baseline, candidate []MetricResult) ([]MetricDelta, error) {
	baselineIndex, err := indexMetrics("baseline", baseline)
	if err != nil {
		return nil, err
	}
	candidateIndex, err := indexMetrics("candidate", candidate)
	if err != nil {
		return nil, err
	}
	if len(baselineIndex) != len(candidateIndex) {
		return nil, errors.New("candidate metric set does not match baseline")
	}
	names := make([]string, 0, len(baselineIndex))
	for name := range baselineIndex {
		names = append(names, name)
	}
	sort.Strings(names)
	deltas := make([]MetricDelta, 0, len(names))
	for _, name := range names {
		candidateMetric, ok := candidateIndex[name]
		if !ok {
			return nil, fmt.Errorf("candidate is missing metric %q", name)
		}
		baselineMetric := baselineIndex[name]
		deltas = append(deltas, MetricDelta{
			Name:            name,
			BaselineScore:   baselineMetric.Score,
			CandidateScore:  candidateMetric.Score,
			ScoreDelta:      candidateMetric.Score - baselineMetric.Score,
			BaselineStatus:  baselineMetric.Status,
			CandidateStatus: candidateMetric.Status,
			Kind: classifyDelta(baselineMetric.Score, candidateMetric.Score,
				passed(baselineMetric.Status), passed(candidateMetric.Status)),
		})
	}
	return deltas, nil
}

func indexMetrics(
	label string,
	metrics []MetricResult,
) (map[string]MetricResult, error) {
	index := make(map[string]MetricResult, len(metrics))
	for _, item := range metrics {
		if item.Name == "" {
			return nil, fmt.Errorf("%s metric name is empty", label)
		}
		if _, ok := index[item.Name]; ok {
			return nil, fmt.Errorf("%s duplicate metric %q", label, item.Name)
		}
		if !finite(item.Score) {
			return nil, fmt.Errorf("%s metric %q score is not finite", label, item.Name)
		}
		switch item.Status {
		case status.EvalStatusPassed, status.EvalStatusFailed, status.EvalStatusNotEvaluated:
		default:
			return nil, fmt.Errorf("%s metric %q has invalid status %q", label, item.Name, item.Status)
		}
		index[item.Name] = item
	}
	if len(index) == 0 {
		return nil, fmt.Errorf("%s metrics are empty", label)
	}
	return index, nil
}

func classifyDelta(baselineScore, candidateScore float64, baselinePass, candidatePass bool) DeltaKind {
	switch {
	case !baselinePass && candidatePass:
		return DeltaNewPass
	case baselinePass && !candidatePass:
		return DeltaNewFail
	case candidateScore-baselineScore > scoreEpsilon:
		return DeltaImproved
	case baselineScore-candidateScore > scoreEpsilon:
		return DeltaDeclined
	default:
		return DeltaUnchanged
	}
}

func passed(value status.EvalStatus) bool {
	return value == status.EvalStatusPassed
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
