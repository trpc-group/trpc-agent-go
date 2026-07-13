//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package delta computes deterministic case- and metric-level evaluation changes.
package delta

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// Engine compares snapshots with a configurable floating-point tolerance.
type Engine struct {
	Epsilon float64
}

// New creates a delta engine.
func New(epsilon float64) *Engine {
	if math.IsNaN(epsilon) || math.IsInf(epsilon, 0) || epsilon < 0 {
		epsilon = 0
	}
	return &Engine{Epsilon: epsilon}
}

// Compare returns stable deltas aligned by case ID and metric name.
func (e *Engine) Compare(
	baseline *regression.EvaluationSnapshot,
	candidate *regression.EvaluationSnapshot,
	policies map[string]regression.MetricPolicy,
) (*regression.DeltaReport, error) {
	if err := validateSnapshots(baseline, candidate); err != nil {
		return nil, err
	}
	baselineCases, err := indexCases(baseline.Cases, baseline.EvalSetID)
	if err != nil {
		return nil, err
	}
	candidateCases, err := indexCases(candidate.Cases, candidate.EvalSetID)
	if err != nil {
		return nil, err
	}
	report := &regression.DeltaReport{
		BaselineScore:  baseline.OverallScore,
		CandidateScore: candidate.OverallScore,
		Complete:       baseline.Complete && candidate.Complete,
	}
	totals := comparisonTotals{complete: true}
	byEvalSet := make(map[string]comparisonTotals)
	for _, key := range unionKeys(baselineCases, candidateCases) {
		caseDelta, caseTotals, err := e.compareCase(
			key, baselineCases, candidateCases, policies,
		)
		if err != nil {
			return nil, err
		}
		report.Cases = append(report.Cases, caseDelta)
		totals.add(caseTotals)
		setTotals := byEvalSet[caseDelta.EvalSetID]
		setTotals.add(caseTotals)
		byEvalSet[caseDelta.EvalSetID] = setTotals
		countCaseChange(report, caseDelta.Kind)
	}
	report.Complete = report.Complete && totals.complete
	report.NewHardFailures = totals.newHardFailures
	report.CriticalRegressions = totals.criticalRegressions
	weightedSetDelta := 0.0
	weightedSetCount := 0
	for _, evalSetID := range sortedKeys(byEvalSet) {
		setTotals := byEvalSet[evalSetID]
		if setTotals.totalWeight == 0 {
			continue
		}
		weightedSetDelta += setTotals.weightedDelta / setTotals.totalWeight
		weightedSetCount++
	}
	if weightedSetCount == 0 {
		report.WeightedScoreDelta = candidate.OverallScore - baseline.OverallScore
	} else {
		report.WeightedScoreDelta = weightedSetDelta / float64(weightedSetCount)
	}
	return report, nil
}

type comparisonTotals struct {
	weightedDelta       float64
	totalWeight         float64
	newHardFailures     int
	criticalRegressions int
	complete            bool
}

func (t *comparisonTotals) add(other comparisonTotals) {
	t.weightedDelta += other.weightedDelta
	t.totalWeight += other.totalWeight
	t.newHardFailures += other.newHardFailures
	t.criticalRegressions += other.criticalRegressions
	t.complete = t.complete && other.complete
}

func (e *Engine) compareCase(
	key string,
	baselineCases map[string]regression.CaseResult,
	candidateCases map[string]regression.CaseResult,
	policies map[string]regression.MetricPolicy,
) (regression.CaseDelta, comparisonTotals, error) {
	baselineCase, baselineOK := baselineCases[key]
	candidateCase, candidateOK := candidateCases[key]
	if !baselineOK || !candidateOK {
		totals := comparisonTotals{complete: false}
		if baselineOK && !candidateOK && baselineCase.Critical {
			totals.criticalRegressions = 1
		}
		return missingCaseDelta(baselineCase, baselineOK, candidateCase, candidateOK), totals, nil
	}
	if baselineCase.EvalSetID != candidateCase.EvalSetID {
		return regression.CaseDelta{}, comparisonTotals{}, fmt.Errorf(
			"case %q moved from eval set %q to %q",
			baselineCase.CaseID, baselineCase.EvalSetID, candidateCase.EvalSetID,
		)
	}
	caseDelta := regression.CaseDelta{
		EvalSetID:       baselineCase.EvalSetID,
		CaseID:          baselineCase.CaseID,
		Critical:        baselineCase.Critical || candidateCase.Critical,
		BaselinePassed:  baselineCase.Passed,
		CandidatePassed: candidateCase.Passed,
	}
	baselineMetrics, err := indexMetrics(baselineCase.Metrics)
	if err != nil {
		return regression.CaseDelta{}, comparisonTotals{}, fmt.Errorf("baseline case %q: %w", baselineCase.CaseID, err)
	}
	candidateMetrics, err := indexMetrics(candidateCase.Metrics)
	if err != nil {
		return regression.CaseDelta{}, comparisonTotals{}, fmt.Errorf("candidate case %q: %w", candidateCase.CaseID, err)
	}
	metrics, totals, err := e.compareMetrics(
		caseDelta.CaseID, caseDelta.Critical, baselineMetrics, candidateMetrics, policies,
	)
	if err != nil {
		return regression.CaseDelta{}, comparisonTotals{}, err
	}
	caseDelta.Metrics = metrics
	caseDelta.Kind = classifyCase(caseDelta)
	return caseDelta, totals, nil
}

func missingCaseDelta(
	baseline regression.CaseResult,
	baselineExists bool,
	candidate regression.CaseResult,
	candidateExists bool,
) regression.CaseDelta {
	kind := regression.ChangeMissing
	if candidateExists {
		kind = regression.ChangeExtra
		return regression.CaseDelta{
			EvalSetID: candidate.EvalSetID, CaseID: candidate.CaseID,
			Kind: kind, Critical: candidate.Critical,
		}
	}
	if baselineExists {
		return regression.CaseDelta{
			EvalSetID: baseline.EvalSetID, CaseID: baseline.CaseID,
			Kind: kind, Critical: baseline.Critical,
		}
	}
	return regression.CaseDelta{Kind: kind}
}

func (e *Engine) compareMetrics(
	caseID string,
	critical bool,
	baselineMetrics map[string]regression.MetricResult,
	candidateMetrics map[string]regression.MetricResult,
	policies map[string]regression.MetricPolicy,
) ([]regression.MetricDelta, comparisonTotals, error) {
	metrics := make([]regression.MetricDelta, 0, len(baselineMetrics)+len(candidateMetrics))
	totals := comparisonTotals{complete: true}
	criticalRegressed := false
	for _, name := range unionKeys(baselineMetrics, candidateMetrics) {
		metricDelta, contribution, err := e.compareMetric(
			caseID, name, baselineMetrics, candidateMetrics, policies,
		)
		if err != nil {
			return nil, comparisonTotals{}, err
		}
		metrics = append(metrics, metricDelta)
		totals.weightedDelta += contribution.weightedDelta
		totals.totalWeight += contribution.weight
		totals.newHardFailures += contribution.newHardFailure
		totals.complete = totals.complete && contribution.complete
		criticalRegressed = criticalRegressed || critical && contribution.regressed
	}
	if criticalRegressed {
		totals.criticalRegressions = 1
	}
	return metrics, totals, nil
}

type metricContribution struct {
	weightedDelta  float64
	weight         float64
	newHardFailure int
	regressed      bool
	complete       bool
}

func (e *Engine) compareMetric(
	caseID string,
	name string,
	baselineMetrics map[string]regression.MetricResult,
	candidateMetrics map[string]regression.MetricResult,
	policies map[string]regression.MetricPolicy,
) (regression.MetricDelta, metricContribution, error) {
	baselineMetric, baselineOK := baselineMetrics[name]
	candidateMetric, candidateOK := candidateMetrics[name]
	if !baselineOK || !candidateOK {
		kind := regression.ChangeMissing
		if candidateOK {
			kind = regression.ChangeExtra
		}
		return regression.MetricDelta{MetricName: name, Kind: kind}, metricContribution{complete: false}, nil
	}
	if !finite(baselineMetric.Score) || !finite(candidateMetric.Score) {
		return regression.MetricDelta{}, metricContribution{}, fmt.Errorf(
			"case %q metric %q scores must be finite", caseID, name,
		)
	}
	policy, exists := policies[name]
	scoreDelta := candidateMetric.Score - baselineMetric.Score
	kind := classify(baselineMetric.Passed, candidateMetric.Passed, scoreDelta, e.Epsilon)
	if !exists {
		return regression.MetricDelta{
				MetricName:      name,
				Kind:            kind,
				BaselineScore:   baselineMetric.Score,
				CandidateScore:  candidateMetric.Score,
				BaselinePassed:  baselineMetric.Passed,
				CandidatePassed: candidateMetric.Passed,
			}, metricContribution{
				regressed: kind == regression.ChangeNewFail || scoreDelta < -e.Epsilon,
				complete:  false,
			}, nil
	}
	newHardFailure := 0
	if policy.HardFail && baselineMetric.Passed && !candidateMetric.Passed {
		newHardFailure = 1
	}
	return regression.MetricDelta{
			MetricName:      name,
			Kind:            kind,
			BaselineScore:   baselineMetric.Score,
			CandidateScore:  candidateMetric.Score,
			BaselinePassed:  baselineMetric.Passed,
			CandidatePassed: candidateMetric.Passed,
			HardFail:        policy.HardFail,
		}, metricContribution{
			weightedDelta:  scoreDelta * policy.Weight,
			weight:         policy.Weight,
			newHardFailure: newHardFailure,
			regressed:      kind == regression.ChangeNewFail || scoreDelta < -e.Epsilon,
			complete:       true,
		}, nil
}

func validateSnapshots(
	baseline *regression.EvaluationSnapshot,
	candidate *regression.EvaluationSnapshot,
) error {
	if baseline == nil || candidate == nil {
		return errors.New("baseline and candidate snapshots are required")
	}
	if baseline.EvalSetID == "" || candidate.EvalSetID == "" || baseline.EvalSetID != candidate.EvalSetID {
		return fmt.Errorf("eval set mismatch: %q != %q", baseline.EvalSetID, candidate.EvalSetID)
	}
	if !finite(baseline.OverallScore) || !finite(candidate.OverallScore) {
		return errors.New("overall scores must be finite")
	}
	return nil
}

func countCaseChange(report *regression.DeltaReport, kind regression.ChangeKind) {
	switch kind {
	case regression.ChangeNewPass:
		report.NewPasses++
	case regression.ChangeNewFail:
		report.NewFailures++
	}
}

func classify(baselinePassed, candidatePassed bool, scoreDelta, epsilon float64) regression.ChangeKind {
	switch {
	case !baselinePassed && candidatePassed:
		return regression.ChangeNewPass
	case baselinePassed && !candidatePassed:
		return regression.ChangeNewFail
	case scoreDelta > epsilon:
		return regression.ChangeImproved
	case scoreDelta < -epsilon:
		return regression.ChangeRegressed
	default:
		return regression.ChangeUnchanged
	}
}

func classifyCase(delta regression.CaseDelta) regression.ChangeKind {
	if !delta.BaselinePassed && delta.CandidatePassed {
		return regression.ChangeNewPass
	}
	if delta.BaselinePassed && !delta.CandidatePassed {
		return regression.ChangeNewFail
	}
	kind := regression.ChangeUnchanged
	for _, metric := range delta.Metrics {
		switch metric.Kind {
		case regression.ChangeMissing, regression.ChangeExtra:
			return metric.Kind
		case regression.ChangeNewFail, regression.ChangeRegressed:
			kind = regression.ChangeRegressed
		case regression.ChangeNewPass, regression.ChangeImproved:
			if kind == regression.ChangeUnchanged {
				kind = regression.ChangeImproved
			}
		}
	}
	return kind
}

func indexCases(
	values []regression.CaseResult,
	defaultEvalSetID string,
) (map[string]regression.CaseResult, error) {
	result := make(map[string]regression.CaseResult, len(values))
	for _, value := range values {
		if value.CaseID == "" {
			return nil, errors.New("case id is empty")
		}
		if value.EvalSetID == "" {
			if defaultEvalSetID == "" || strings.Contains(defaultEvalSetID, ",") {
				return nil, fmt.Errorf("case %q eval set id is empty", value.CaseID)
			}
			value.EvalSetID = defaultEvalSetID
		}
		key := caseKey(value.EvalSetID, value.CaseID)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate case id %q in eval set %q", value.CaseID, value.EvalSetID)
		}
		result[key] = value
	}
	return result, nil
}

func caseKey(evalSetID, caseID string) string {
	return evalSetID + "\x00" + caseID
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func indexMetrics(values []regression.MetricResult) (map[string]regression.MetricResult, error) {
	result := make(map[string]regression.MetricResult, len(values))
	for _, value := range values {
		if value.Name == "" {
			return nil, errors.New("metric name is empty")
		}
		if _, exists := result[value.Name]; exists {
			return nil, fmt.Errorf("duplicate metric %q", value.Name)
		}
		result[value.Name] = value
	}
	return result, nil
}

func unionKeys[T any](left, right map[string]T) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		set[key] = struct{}{}
	}
	for key := range right {
		set[key] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
