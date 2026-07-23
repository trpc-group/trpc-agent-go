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
)

const scoreEpsilon = 1e-9

func scorePasses(score, threshold float64) bool {
	return score+scoreEpsilon >= threshold
}

// ComputeDelta builds a stable, fail-closed case and metric comparison. Missing
// or extra candidate cases are recorded as coverage issues instead of ignored.
func ComputeDelta(baseline, candidate *EvaluationSummary) (*DeltaSummary, error) {
	if baseline == nil || candidate == nil {
		return nil, errors.New("baseline and candidate summaries are required")
	}
	if baseline.EvalSetID != "" && candidate.EvalSetID != "" && baseline.EvalSetID != candidate.EvalSetID {
		return nil, fmt.Errorf("eval set mismatch: baseline %q, candidate %q", baseline.EvalSetID, candidate.EvalSetID)
	}
	if !validUnitScore(baseline.OverallScore) || !validUnitScore(candidate.OverallScore) {
		return nil, errors.New("overall scores must be finite and in [0,1]")
	}
	if math.Abs(baseline.PassThreshold-candidate.PassThreshold) > scoreEpsilon {
		return nil, fmt.Errorf(
			"pass threshold mismatch: baseline %.9f, candidate %.9f",
			baseline.PassThreshold,
			candidate.PassThreshold,
		)
	}
	if err := validateDeltaSummaryInput(baseline); err != nil {
		return nil, fmt.Errorf("baseline summary: %w", err)
	}
	if err := validateDeltaSummaryInput(candidate); err != nil {
		return nil, fmt.Errorf("candidate summary: %w", err)
	}
	baselineIndex, err := indexCases(baseline.Cases)
	if err != nil {
		return nil, fmt.Errorf("index baseline cases: %w", err)
	}
	candidateIndex, err := indexCases(candidate.Cases)
	if err != nil {
		return nil, fmt.Errorf("index candidate cases: %w", err)
	}
	ids := make([]string, 0, len(baselineIndex)+len(candidateIndex))
	seen := make(map[string]struct{}, len(baselineIndex)+len(candidateIndex))
	for id := range baselineIndex {
		ids = append(ids, id)
		seen[id] = struct{}{}
	}
	for id := range candidateIndex {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	delta := &DeltaSummary{
		BaselineScore:  baseline.OverallScore,
		CandidateScore: candidate.OverallScore,
		ScoreDelta:     candidate.OverallScore - baseline.OverallScore,
		Complete:       true,
		Cases:          make([]CaseDelta, 0, len(ids)),
	}
	for _, id := range ids {
		baselineCase, baselineOK := baselineIndex[id]
		candidateCase, candidateOK := candidateIndex[id]
		caseDelta := CaseDelta{
			CaseID:           id,
			BaselinePresent:  baselineOK,
			CandidatePresent: candidateOK,
		}
		switch {
		case baselineOK && !candidateOK:
			if err := validateCaseScores(baselineCase, baseline.PassThreshold); err != nil {
				return nil, fmt.Errorf("case %q baseline: %w", id, err)
			}
			delta.Complete = false
			delta.CoverageIssues = append(delta.CoverageIssues, "candidate is missing case "+id)
			caseDelta.BaselineScore = baselineCase.Score
			caseDelta.CandidateScore = 0
			caseDelta.ScoreDelta = -baselineCase.Score
			caseDelta.BaselinePassed = baselineCase.Passed
			caseDelta.CandidatePassed = false
			caseDelta.BaselineHardFail = baselineCase.HardFail
			caseDelta.CandidateHardFail = true
			caseDelta.Critical = baselineCase.Critical
			caseDelta.Outcome = DeltaMissingCandidate
			caseDelta.BecameFailed = baselineCase.Passed
			caseDelta.ScoreRegressed = baselineCase.Score > scoreEpsilon
			caseDelta.NewHardFail = !baselineCase.HardFail
		case !baselineOK && candidateOK:
			if err := validateCaseScores(candidateCase, candidate.PassThreshold); err != nil {
				return nil, fmt.Errorf("case %q candidate: %w", id, err)
			}
			delta.Complete = false
			delta.CoverageIssues = append(delta.CoverageIssues, "candidate contains unexpected case "+id)
			caseDelta.CandidateScore = candidateCase.Score
			caseDelta.ScoreDelta = candidateCase.Score
			caseDelta.CandidatePassed = candidateCase.Passed
			caseDelta.CandidateHardFail = candidateCase.HardFail
			caseDelta.Critical = candidateCase.Critical
			caseDelta.Outcome = DeltaUnexpectedCase
		case baselineOK && candidateOK:
			if err := validateCaseScores(baselineCase, baseline.PassThreshold); err != nil {
				return nil, fmt.Errorf("case %q baseline: %w", id, err)
			}
			if err := validateCaseScores(candidateCase, candidate.PassThreshold); err != nil {
				return nil, fmt.Errorf("case %q candidate: %w", id, err)
			}
			caseDelta = compareCases(baselineCase, candidateCase)
			if baselineCase.Critical != candidateCase.Critical {
				delta.Complete = false
				delta.CoverageIssues = append(delta.CoverageIssues, "case "+id+" critical flag changed")
			}
			metricDeltas, complete, issues, err := compareMetrics(id, baselineCase, candidateCase)
			if err != nil {
				return nil, err
			}
			caseDelta.MetricDeltas = metricDeltas
			if !complete {
				delta.Complete = false
				delta.CoverageIssues = append(delta.CoverageIssues, issues...)
			}
		}
		if caseDelta.BecamePassed {
			delta.NewPasses++
		}
		if caseDelta.BecameFailed || caseDelta.Outcome == DeltaMissingCandidate {
			delta.NewFailures++
		}
		if caseDelta.ScoreImproved {
			delta.ScoreImprovements++
		}
		if caseDelta.ScoreRegressed {
			delta.ScoreRegressions++
		}
		if caseDelta.NewHardFail {
			delta.NewHardFails++
		}
		delta.Cases = append(delta.Cases, caseDelta)
	}
	return delta, nil
}

func validateDeltaSummaryInput(summary *EvaluationSummary) error {
	if !validUnitScore(summary.PassThreshold) {
		return errors.New("pass threshold must be finite and in [0,1]")
	}
	if len(summary.Cases) == 0 {
		return errors.New("evaluation has no cases")
	}
	total := 0.0
	for _, evalCase := range summary.Cases {
		total += evalCase.Score
	}
	macroAverage := total / float64(len(summary.Cases))
	if math.Abs(macroAverage-summary.OverallScore) > 1e-6 {
		return fmt.Errorf("overall score %.9f does not match case macro average %.9f", summary.OverallScore, macroAverage)
	}
	return nil
}

func validateCaseScores(evalCase CaseResult, passThreshold float64) error {
	if !validUnitScore(evalCase.Score) {
		return errors.New("score must be finite and in [0,1]")
	}
	if len(evalCase.MetricResults) == 0 {
		return errors.New("case has no metric results")
	}
	metrics, err := indexMetrics(evalCase.MetricResults)
	if err != nil {
		return err
	}
	totalWeight := 0.0
	weightedScore := 0.0
	derivedHardFail := evalCase.Error != ""
	for name, metric := range metrics {
		if err := validateMetricResult(metric, evalCase.Error != ""); err != nil {
			return fmt.Errorf("metric %q: %w", name, err)
		}
		if evalCase.Error != "" && metric.Score != 0 {
			return fmt.Errorf("metric %q score must be zero after an execution error", name)
		}
		totalWeight += metric.Weight
		weightedScore += metric.Score * metric.Weight
		if metric.HardFail && !metric.Passed {
			derivedHardFail = true
		}
	}
	if totalWeight <= 0 || !finiteScore(totalWeight) {
		return errors.New("metric total weight must be finite and positive")
	}
	derivedScore := weightedScore / totalWeight
	if evalCase.Error != "" && evalCase.Score != 0 {
		return errors.New("case score must be zero after an execution error")
	}
	if math.Abs(derivedScore-evalCase.Score) > 1e-6 {
		return fmt.Errorf("score %.9f does not match weighted metric score %.9f", evalCase.Score, derivedScore)
	}
	if evalCase.HardFail != derivedHardFail {
		return errors.New("hard-fail status is inconsistent with execution and metrics")
	}
	expectedPassed := scorePasses(evalCase.Score, passThreshold) && !evalCase.HardFail
	if evalCase.Passed != expectedPassed {
		return fmt.Errorf(
			"pass status %t does not match score %.9f, threshold %.9f, and hard-fail status %t",
			evalCase.Passed,
			evalCase.Score,
			passThreshold,
			evalCase.HardFail,
		)
	}
	return nil
}

func compareCases(baseline, candidate CaseResult) CaseDelta {
	scoreDelta := candidate.Score - baseline.Score
	delta := CaseDelta{
		CaseID:            baseline.CaseID,
		BaselinePresent:   true,
		CandidatePresent:  true,
		BaselineScore:     baseline.Score,
		CandidateScore:    candidate.Score,
		ScoreDelta:        scoreDelta,
		BaselinePassed:    baseline.Passed,
		CandidatePassed:   candidate.Passed,
		BaselineHardFail:  baseline.HardFail,
		CandidateHardFail: candidate.HardFail,
		Critical:          baseline.Critical || candidate.Critical,
		BecamePassed:      !baseline.Passed && candidate.Passed,
		BecameFailed:      baseline.Passed && !candidate.Passed,
		ScoreImproved:     scoreDelta > scoreEpsilon,
		ScoreRegressed:    scoreDelta < -scoreEpsilon,
		NewHardFail:       !baseline.HardFail && candidate.HardFail,
	}
	switch {
	case delta.BecamePassed:
		delta.Outcome = DeltaNewPass
	case delta.BecameFailed:
		delta.Outcome = DeltaNewFailure
	case delta.ScoreImproved:
		delta.Outcome = DeltaImproved
	case delta.ScoreRegressed:
		delta.Outcome = DeltaRegressed
	case baseline.Passed && candidate.Passed:
		delta.Outcome = DeltaUnchangedPass
	default:
		delta.Outcome = DeltaUnchangedFailure
	}
	return delta
}

func compareMetrics(
	caseID string,
	baselineCase CaseResult,
	candidateCase CaseResult,
) ([]MetricDelta, bool, []string, error) {
	baseline, err := indexMetrics(baselineCase.MetricResults)
	if err != nil {
		return nil, false, nil, fmt.Errorf("case %q baseline metrics: %w", caseID, err)
	}
	candidate, err := indexMetrics(candidateCase.MetricResults)
	if err != nil {
		return nil, false, nil, fmt.Errorf("case %q candidate metrics: %w", caseID, err)
	}
	names := make([]string, 0, len(baseline)+len(candidate))
	seen := make(map[string]struct{}, len(baseline)+len(candidate))
	for name := range baseline {
		names = append(names, name)
		seen[name] = struct{}{}
	}
	for name := range candidate {
		if _, ok := seen[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	complete := true
	issues := make([]string, 0)
	deltas := make([]MetricDelta, 0, len(names))
	for _, name := range names {
		baselineMetric, baselineOK := baseline[name]
		candidateMetric, candidateOK := candidate[name]
		if !baselineOK || !candidateOK {
			complete = false
			issues = append(issues, fmt.Sprintf("case %s metric %s is missing from one side", caseID, name))
		}
		if baselineOK {
			if err := validateMetricResult(baselineMetric, baselineCase.Error != ""); err != nil {
				return nil, false, nil, fmt.Errorf("case %q baseline metric %q: %w", caseID, name, err)
			}
		}
		if candidateOK {
			if err := validateMetricResult(candidateMetric, candidateCase.Error != ""); err != nil {
				return nil, false, nil, fmt.Errorf("case %q candidate metric %q: %w", caseID, name, err)
			}
		}
		if baselineOK && candidateOK &&
			(math.Abs(baselineMetric.Threshold-candidateMetric.Threshold) > scoreEpsilon ||
				math.Abs(baselineMetric.Weight-candidateMetric.Weight) > scoreEpsilon ||
				baselineMetric.HardFail != candidateMetric.HardFail) {
			complete = false
			issues = append(issues, fmt.Sprintf("case %s metric %s configuration changed", caseID, name))
		}
		deltas = append(deltas, MetricDelta{
			MetricName:     name,
			BaselineScore:  baselineMetric.Score,
			CandidateScore: candidateMetric.Score,
			ScoreDelta:     candidateMetric.Score - baselineMetric.Score,
		})
	}
	return deltas, complete, issues, nil
}

func validateMetricResult(metric MetricResult, executionFailed bool) error {
	if !validUnitScore(metric.Score) {
		return errors.New("score must be finite and in [0,1]")
	}
	if !validUnitScore(metric.Threshold) {
		return errors.New("threshold must be finite and in [0,1]")
	}
	if !finiteScore(metric.Weight) || metric.Weight <= 0 {
		return errors.New("weight must be finite and positive")
	}
	expectedPassed := !executionFailed && scorePasses(metric.Score, metric.Threshold)
	if metric.Passed != expectedPassed {
		return errors.New("pass status does not match score and threshold")
	}
	return nil
}

func indexCases(cases []CaseResult) (map[string]CaseResult, error) {
	index := make(map[string]CaseResult, len(cases))
	for _, evalCase := range cases {
		if evalCase.CaseID == "" {
			return nil, errors.New("case id is empty")
		}
		if _, ok := index[evalCase.CaseID]; ok {
			return nil, fmt.Errorf("duplicate case id %q", evalCase.CaseID)
		}
		index[evalCase.CaseID] = evalCase
	}
	return index, nil
}

func indexMetrics(metrics []MetricResult) (map[string]MetricResult, error) {
	index := make(map[string]MetricResult, len(metrics))
	for _, metric := range metrics {
		if metric.MetricName == "" {
			return nil, errors.New("metric name is empty")
		}
		if _, ok := index[metric.MetricName]; ok {
			return nil, fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		index[metric.MetricName] = metric
	}
	return index, nil
}

func finiteScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validUnitScore(value float64) bool {
	return finiteScore(value) && value >= 0 && value <= 1
}
