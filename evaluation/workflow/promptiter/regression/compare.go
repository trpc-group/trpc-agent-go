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
	"sort"
)

const scoreDeltaEpsilon = 1e-9

// DeltaKind describes how a candidate changed a result.
type DeltaKind string

const (
	DeltaNewlyPassed DeltaKind = "newly_passed"
	DeltaNewlyFailed DeltaKind = "newly_failed"
	DeltaImproved    DeltaKind = "improved"
	DeltaRegressed   DeltaKind = "regressed"
	DeltaUnchanged   DeltaKind = "unchanged"
)

// MetricDelta compares one metric.
type MetricDelta struct {
	Name               string    `json:"name"`
	Kind               DeltaKind `json:"kind"`
	BaselineScore      float64   `json:"baseline_score"`
	CandidateScore     float64   `json:"candidate_score"`
	ScoreDelta         float64   `json:"score_delta"`
	BaselineEvaluated  bool      `json:"baseline_evaluated"`
	CandidateEvaluated bool      `json:"candidate_evaluated"`
	BaselinePassed     bool      `json:"baseline_passed"`
	CandidatePassed    bool      `json:"candidate_passed"`
}

// CaseDelta compares one evaluation case.
type CaseDelta struct {
	ID             string        `json:"id"`
	Kind           DeltaKind     `json:"kind"`
	BaselineScore  float64       `json:"baseline_score"`
	CandidateScore float64       `json:"candidate_score"`
	ScoreDelta     float64       `json:"score_delta"`
	Metrics        []MetricDelta `json:"metrics"`
}

// DatasetDelta compares baseline and candidate results for one eval set.
type DatasetDelta struct {
	EvalSetID      string      `json:"eval_set_id"`
	Kind           DeltaKind   `json:"kind"`
	BaselineScore  float64     `json:"baseline_score"`
	CandidateScore float64     `json:"candidate_score"`
	ScoreDelta     float64     `json:"score_delta"`
	Cases          []CaseDelta `json:"cases"`
}

// Compare returns deterministic per-dataset, per-case, and per-metric deltas.
func Compare(baseline, candidate *EvalSummary) (*DatasetDelta, error) {
	if baseline == nil || candidate == nil {
		return nil, errors.New("baseline and candidate summaries must not be nil")
	}
	if baseline.EvalSetID != candidate.EvalSetID {
		return nil, fmt.Errorf("eval set mismatch: %q and %q", baseline.EvalSetID, candidate.EvalSetID)
	}
	kind, delta, err := classifyDelta(baseline.Score, candidate.Score, baseline.Passed, candidate.Passed)
	if err != nil {
		return nil, err
	}
	result := &DatasetDelta{
		EvalSetID: baseline.EvalSetID, Kind: kind, BaselineScore: baseline.Score,
		CandidateScore: candidate.Score, ScoreDelta: delta,
	}
	baselineCases, err := caseIndex(baseline.Cases)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	candidateCases, err := caseIndex(candidate.Cases)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	if err := sameKeys("case", baselineCases, candidateCases); err != nil {
		return nil, err
	}
	ids := sortedKeys(baselineCases)
	for _, id := range ids {
		item, err := compareCase(baselineCases[id], candidateCases[id])
		if err != nil {
			return nil, fmt.Errorf("compare case %q: %w", id, err)
		}
		result.Cases = append(result.Cases, item)
	}
	return result, nil
}

func compareCase(baseline, candidate CaseSummary) (CaseDelta, error) {
	kind, delta, err := classifyDelta(baseline.Score, candidate.Score, baseline.Passed, candidate.Passed)
	if err != nil {
		return CaseDelta{}, err
	}
	result := CaseDelta{
		ID: baseline.ID, Kind: kind, BaselineScore: baseline.Score,
		CandidateScore: candidate.Score, ScoreDelta: delta,
	}
	baselineMetrics, err := metricIndex(baseline.Metrics)
	if err != nil {
		return CaseDelta{}, fmt.Errorf("baseline: %w", err)
	}
	candidateMetrics, err := metricIndex(candidate.Metrics)
	if err != nil {
		return CaseDelta{}, fmt.Errorf("candidate: %w", err)
	}
	if err := sameKeys("metric", baselineMetrics, candidateMetrics); err != nil {
		return CaseDelta{}, err
	}
	for _, name := range sortedKeys(baselineMetrics) {
		before, after := baselineMetrics[name], candidateMetrics[name]
		kind, scoreDelta, err := classifyDelta(before.Score, after.Score, before.Passed, after.Passed)
		if err != nil {
			return CaseDelta{}, fmt.Errorf("metric %q: %w", name, err)
		}
		if !before.Evaluated && !after.Evaluated {
			kind = DeltaUnchanged
		}
		result.Metrics = append(result.Metrics, MetricDelta{
			Name: name, Kind: kind, BaselineScore: before.Score, CandidateScore: after.Score,
			ScoreDelta: scoreDelta, BaselineEvaluated: before.Evaluated, CandidateEvaluated: after.Evaluated,
			BaselinePassed: before.Passed, CandidatePassed: after.Passed,
		})
	}
	return result, nil
}

func classifyDelta(baseline, candidate float64, baselinePassed, candidatePassed bool) (DeltaKind, float64, error) {
	if !finite(baseline) || !finite(candidate) || !finite(candidate-baseline) {
		return "", 0, errors.New("scores must be finite")
	}
	delta := candidate - baseline
	switch {
	case !baselinePassed && candidatePassed:
		return DeltaNewlyPassed, delta, nil
	case baselinePassed && !candidatePassed:
		return DeltaNewlyFailed, delta, nil
	case delta > scoreDeltaEpsilon:
		return DeltaImproved, delta, nil
	case delta < -scoreDeltaEpsilon:
		return DeltaRegressed, delta, nil
	default:
		return DeltaUnchanged, delta, nil
	}
}

func caseIndex(items []CaseSummary) (map[string]CaseSummary, error) {
	result := make(map[string]CaseSummary, len(items))
	for _, item := range items {
		if item.ID == "" {
			return nil, errors.New("case has no id")
		}
		if _, ok := result[item.ID]; ok {
			return nil, fmt.Errorf("duplicate case %q", item.ID)
		}
		result[item.ID] = item
	}
	return result, nil
}

func metricIndex(items []MetricSummary) (map[string]MetricSummary, error) {
	result := make(map[string]MetricSummary, len(items))
	for _, item := range items {
		if item.Name == "" {
			return nil, errors.New("metric has no name")
		}
		if _, ok := result[item.Name]; ok {
			return nil, fmt.Errorf("duplicate metric %q", item.Name)
		}
		result[item.Name] = item
	}
	return result, nil
}

func sameKeys[T any](kind string, left, right map[string]T) error {
	leftKeys, rightKeys := sortedKeys(left), sortedKeys(right)
	if fmt.Sprint(leftKeys) != fmt.Sprint(rightKeys) {
		return fmt.Errorf("%s sets differ: %v and %v", kind, leftKeys, rightKeys)
	}
	return nil
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
