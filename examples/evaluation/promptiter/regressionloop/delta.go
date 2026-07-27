//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type deltaKind string

const (
	deltaNewPass          deltaKind = "new_pass"
	deltaNewFailure       deltaKind = "new_failure"
	deltaImproved         deltaKind = "improved"
	deltaRegressed        deltaKind = "regressed"
	deltaUnchangedPass    deltaKind = "unchanged_pass"
	deltaUnchangedFailure deltaKind = "unchanged_failure"
)

type metricDelta struct {
	Key             resultKey         `json:"key"`
	Kind            deltaKind         `json:"kind"`
	BaselineStatus  status.EvalStatus `json:"baselineStatus"`
	CandidateStatus status.EvalStatus `json:"candidateStatus"`
	BaselineScore   float64           `json:"baselineScore"`
	CandidateScore  float64           `json:"candidateScore"`
	ScoreDelta      float64           `json:"scoreDelta"`
}

type snapshotDelta struct {
	BaselineScore  float64       `json:"baselineScore"`
	CandidateScore float64       `json:"candidateScore"`
	ScoreDelta     float64       `json:"scoreDelta"`
	Metrics        []metricDelta `json:"metrics"`
}

func compareSnapshots(baseline, candidate evaluationSnapshot) (snapshotDelta, error) {
	baselineIndex, err := checkedSnapshotIndex(baseline)
	if err != nil {
		return snapshotDelta{}, fmt.Errorf("baseline evidence shape: %w", err)
	}
	candidateIndex, err := checkedSnapshotIndex(candidate)
	if err != nil {
		return snapshotDelta{}, fmt.Errorf("candidate evidence shape: %w", err)
	}
	if baseline.EvalSetID != candidate.EvalSetID || len(baselineIndex) != len(candidateIndex) {
		return snapshotDelta{}, errors.New("evidence shape differs")
	}

	keys := make([]resultKey, 0, len(baselineIndex))
	for key := range baselineIndex {
		if _, ok := candidateIndex[key]; !ok {
			return snapshotDelta{}, fmt.Errorf("evidence shape missing candidate key %+v", key)
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].EvalCaseID != keys[j].EvalCaseID {
			return keys[i].EvalCaseID < keys[j].EvalCaseID
		}
		return keys[i].MetricName < keys[j].MetricName
	})

	result := snapshotDelta{
		BaselineScore:  baseline.Score,
		CandidateScore: candidate.Score,
		ScoreDelta:     candidate.Score - baseline.Score,
		Metrics:        make([]metricDelta, 0, len(keys)),
	}
	for _, key := range keys {
		before := baselineIndex[key]
		after := candidateIndex[key]
		result.Metrics = append(result.Metrics, metricDelta{
			Key:             key,
			Kind:            classifyDelta(before, after),
			BaselineStatus:  before.Status,
			CandidateStatus: after.Status,
			BaselineScore:   before.Score,
			CandidateScore:  after.Score,
			ScoreDelta:      after.Score - before.Score,
		})
	}
	return result, nil
}

func checkedSnapshotIndex(snapshot evaluationSnapshot) (map[resultKey]metricResult, error) {
	if snapshot.EvalSetID == "" {
		return nil, errors.New("evaluation set ID is empty")
	}
	if snapshot.Status != status.EvalStatusPassed && snapshot.Status != status.EvalStatusFailed {
		return nil, fmt.Errorf("evaluation status is %s", snapshot.Status)
	}
	result := make(map[resultKey]metricResult)
	seenCases := make(map[string]struct{}, len(snapshot.Cases))
	for _, evalCase := range snapshot.Cases {
		if evalCase.EvalSetID != snapshot.EvalSetID || evalCase.EvalCaseID == "" {
			return nil, errors.New("case identity is invalid")
		}
		if _, ok := seenCases[evalCase.EvalCaseID]; ok {
			return nil, fmt.Errorf("duplicate case %q", evalCase.EvalCaseID)
		}
		if evalCase.Status != status.EvalStatusPassed && evalCase.Status != status.EvalStatusFailed {
			return nil, fmt.Errorf("case %q status is %s", evalCase.EvalCaseID, evalCase.Status)
		}
		seenCases[evalCase.EvalCaseID] = struct{}{}
		allPassed := true
		for _, metric := range evalCase.Metrics {
			key := resultKey{EvalSetID: evalCase.EvalSetID, EvalCaseID: evalCase.EvalCaseID, MetricName: metric.Name}
			if metric.Name == "" {
				return nil, errors.New("metric name is empty")
			}
			if _, ok := result[key]; ok {
				return nil, fmt.Errorf("duplicate metric key %+v", key)
			}
			if metric.Status != status.EvalStatusPassed && metric.Status != status.EvalStatusFailed {
				return nil, fmt.Errorf("metric %q status is %s", metric.Name, metric.Status)
			}
			if metric.Status != status.EvalStatusPassed {
				allPassed = false
			}
			result[key] = metric
		}
		if (evalCase.Status == status.EvalStatusPassed) != allPassed {
			return nil, fmt.Errorf("case %q status is inconsistent with its metrics", evalCase.EvalCaseID)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("metric evidence is empty")
	}
	return result, nil
}

func classifyDelta(before, after metricResult) deltaKind {
	if before.Status == status.EvalStatusFailed && after.Status == status.EvalStatusPassed {
		return deltaNewPass
	}
	if before.Status == status.EvalStatusPassed && after.Status == status.EvalStatusFailed {
		return deltaNewFailure
	}
	if after.Score > before.Score && !almostEqual(after.Score, before.Score) {
		return deltaImproved
	}
	if after.Score < before.Score && !almostEqual(after.Score, before.Score) {
		return deltaRegressed
	}
	if after.Status == status.EvalStatusPassed {
		return deltaUnchangedPass
	}
	return deltaUnchangedFailure
}

func almostEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-12
}
