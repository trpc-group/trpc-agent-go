//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"math"
	"strings"
)

type snapshotCaseKey struct {
	evalSetID string
	caseID    string
}

// CalculateDelta compares two provenance-compatible snapshots. Metric deltas
// remain raw after-before values; ScoreDelta is an oriented quality gain.
func CalculateDelta(
	comparison string,
	before *EvaluationSnapshot,
	after *EvaluationSnapshot,
	policy GatePolicy,
) (DeltaSummary, error) {
	comparison = strings.TrimSpace(comparison)
	if comparison == "" {
		return DeltaSummary{}, errorsf("comparison is empty")
	}
	epsilon, err := comparisonEpsilon(policy.Epsilon)
	if err != nil {
		return DeltaSummary{}, err
	}
	if err := validateDeltaPolicy(policy); err != nil {
		return DeltaSummary{}, err
	}
	beforeCases, err := validateComparisonSnapshot("before", before, policy)
	if err != nil {
		return DeltaSummary{}, err
	}
	afterCases, err := validateComparisonSnapshot("after", after, policy)
	if err != nil {
		return DeltaSummary{}, err
	}
	if err := validateCompatibleSnapshots(before, after); err != nil {
		return DeltaSummary{}, err
	}

	primaryDirection := policy.MetricDirections[policy.PrimaryMetric]
	rawOverallDelta := after.OverallScore - before.OverallScore
	summary := DeltaSummary{
		Comparison:         comparison,
		BeforeProfileHash:  before.Provenance.ProfileHash,
		AfterProfileHash:   after.Provenance.ProfileHash,
		BeforeOverallScore: before.OverallScore,
		AfterOverallScore:  after.OverallScore,
		ScoreDelta:         orientedDelta(rawOverallDelta, primaryDirection),
		Cases:              make([]CaseDelta, 0, len(before.Inventory.CaseIDs)),
	}
	for _, caseID := range before.Inventory.CaseIDs {
		key := snapshotCaseKey{
			evalSetID: before.Provenance.EvalSetID,
			caseID:    caseID,
		}
		beforeCase := beforeCases[key]
		afterCase := afterCases[key]
		if beforeCase.Critical != afterCase.Critical {
			return DeltaSummary{}, errorsf(
				"case %s/%s critical classification changed between snapshots",
				key.evalSetID,
				key.caseID,
			)
		}
		if beforeCase.HardFailure != afterCase.HardFailure {
			return DeltaSummary{}, errorsf(
				"case %s/%s hard-failure classification changed between snapshots",
				key.evalSetID,
				key.caseID,
			)
		}
		if beforeCase.ExpectNoTools != afterCase.ExpectNoTools {
			return DeltaSummary{}, errorsf(
				"case %s/%s explicit no-tool expectation changed between snapshots",
				key.evalSetID,
				key.caseID,
			)
		}
		caseDelta, err := calculateCaseDelta(
			beforeCase,
			afterCase,
			before.Inventory.MetricNames,
			policy,
			epsilon,
		)
		if err != nil {
			return DeltaSummary{}, fmt.Errorf(
				"calculate case %s/%s delta: %w",
				key.evalSetID,
				key.caseID,
				err,
			)
		}
		incrementDeltaCount(&summary, caseDelta.PrimaryKind)
		summary.Cases = append(summary.Cases, caseDelta)
	}
	return summary, nil
}

func validateDeltaPolicy(policy GatePolicy) error {
	if strings.TrimSpace(policy.PrimaryMetric) == "" {
		return errorsf("primary metric is empty")
	}
	if len(policy.MetricDirections) == 0 {
		return errorsf("metric directions are empty")
	}
	direction, ok := policy.MetricDirections[policy.PrimaryMetric]
	if !ok {
		return errorsf(
			"primary metric %q has no configured direction",
			policy.PrimaryMetric,
		)
	}
	if !isValidDirection(direction) {
		return errorsf(
			"primary metric %q has invalid direction %q",
			policy.PrimaryMetric,
			direction,
		)
	}
	for metricName, configuredDirection := range policy.MetricDirections {
		if strings.TrimSpace(metricName) == "" {
			return errorsf("metric direction has an empty metric name")
		}
		if !isValidDirection(configuredDirection) {
			return errorsf(
				"metric %q has invalid direction %q",
				metricName,
				configuredDirection,
			)
		}
	}
	return nil
}

//nolint:gocyclo // Inventory and provenance invariants stay linear for precise failures.
func validateComparisonSnapshot(
	label string,
	snapshot *EvaluationSnapshot,
	policy GatePolicy,
) (map[snapshotCaseKey]CaseResult, error) {
	if snapshot == nil {
		return nil, errorsf("%s snapshot is nil", label)
	}
	if snapshot.Status != EvaluationCompleted {
		return nil, errorsf(
			"%s snapshot status %q is not completed",
			label,
			snapshot.Status,
		)
	}
	if !isFinite(snapshot.OverallScore) {
		return nil, errorsf("%s overall score must be finite", label)
	}
	provenance := snapshot.Provenance
	requiredProvenance := []struct {
		name  string
		value string
	}{
		{name: "run id", value: provenance.RunID},
		{name: "profile hash", value: provenance.ProfileHash},
		{name: "eval set id", value: provenance.EvalSetID},
		{name: "eval set hash", value: provenance.EvalSetHash},
		{name: "metrics hash", value: provenance.MetricsHash},
		{name: "split", value: provenance.Split},
		{name: "evaluator config hash", value: provenance.EvaluatorConfigHash},
		{name: "metric policy hash", value: provenance.MetricPolicyHash},
	}
	for _, field := range requiredProvenance {
		if strings.TrimSpace(field.value) == "" {
			return nil, errorsf(
				"%s provenance %s is empty",
				label,
				field.name,
			)
		}
	}
	if err := validateUniqueStrings(
		label+" expected case inventory",
		snapshot.Inventory.CaseIDs,
	); err != nil {
		return nil, err
	}
	if err := validateUniqueStrings(
		label+" expected metric inventory",
		snapshot.Inventory.MetricNames,
	); err != nil {
		return nil, err
	}
	if !containsExactlyOnce(
		snapshot.Inventory.MetricNames,
		policy.PrimaryMetric,
	) {
		return nil, errorsf(
			"%s expected metric inventory does not uniquely contain primary metric %q",
			label,
			policy.PrimaryMetric,
		)
	}
	for _, metricName := range snapshot.Inventory.MetricNames {
		if _, ok := policy.MetricDirections[metricName]; !ok {
			return nil, errorsf(
				"%s metric %q has no configured direction",
				label,
				metricName,
			)
		}
	}
	for i, item := range snapshot.Cases {
		if item.EvalSetID != provenance.EvalSetID {
			return nil, errorsf(
				"%s case at index %d has eval set %q, expected %q",
				label,
				i,
				item.EvalSetID,
				provenance.EvalSetID,
			)
		}
		if strings.TrimSpace(item.CaseID) == "" {
			return nil, errorsf("%s case at index %d has an empty id", label, i)
		}
		if item.ExpectNoTools && len(item.ExpectedTools) > 0 {
			return nil, errorsf(
				"%s case %s/%s has both an explicit no-tool expectation and expected tool calls",
				label,
				item.EvalSetID,
				item.CaseID,
			)
		}
	}
	for _, expectedCaseID := range snapshot.Inventory.CaseIDs {
		matchCount := 0
		for _, item := range snapshot.Cases {
			if item.EvalSetID == provenance.EvalSetID &&
				item.CaseID == expectedCaseID {
				matchCount++
			}
		}
		switch {
		case matchCount == 0:
			return nil, errorsf(
				"%s snapshot is missing expected case %s/%s",
				label,
				provenance.EvalSetID,
				expectedCaseID,
			)
		case matchCount > 1:
			return nil, errorsf(
				"%s snapshot contains duplicate expected case %s/%s",
				label,
				provenance.EvalSetID,
				expectedCaseID,
			)
		}
	}
	if len(snapshot.Cases) != len(snapshot.Inventory.CaseIDs) {
		return nil, errorsf(
			"%s case inventory mismatch: expected %d cases, got %d",
			label,
			len(snapshot.Inventory.CaseIDs),
			len(snapshot.Cases),
		)
	}
	indexed := make(map[snapshotCaseKey]CaseResult, len(snapshot.Cases))
	passed := 0
	failed := 0
	for _, item := range snapshot.Cases {
		key := snapshotCaseKey{evalSetID: item.EvalSetID, caseID: item.CaseID}
		if !isComparableResultStatus(item.Status) {
			return nil, errorsf(
				"%s case %s/%s has non-comparable status %q",
				label,
				key.evalSetID,
				key.caseID,
				item.Status,
			)
		}
		if !statusMatchesPassed(item.Status, item.Passed) {
			return nil, errorsf(
				"%s case %s/%s status and passed flag disagree",
				label,
				key.evalSetID,
				key.caseID,
			)
		}
		if item.PrimaryMetric != policy.PrimaryMetric {
			return nil, errorsf(
				"%s case %s/%s primary metric %q does not match policy %q",
				label,
				key.evalSetID,
				key.caseID,
				item.PrimaryMetric,
				policy.PrimaryMetric,
			)
		}
		if err := validateCaseMetrics(
			label,
			item,
			snapshot.Inventory.MetricNames,
			policy,
		); err != nil {
			return nil, err
		}
		if item.Passed {
			passed++
		} else {
			failed++
		}
		indexed[key] = item
	}
	if passed != snapshot.Passed || failed != snapshot.Failed {
		return nil, errorsf(
			"%s pass/fail counts mismatch: recorded %d/%d, computed %d/%d",
			label,
			snapshot.Passed,
			snapshot.Failed,
			passed,
			failed,
		)
	}
	return indexed, nil
}

func validateCaseMetrics(
	label string,
	item CaseResult,
	metricInventory []string,
	policy GatePolicy,
) error {
	if len(item.Metrics) != len(metricInventory) {
		return errorsf(
			"%s case %s/%s metric inventory mismatch: expected %d metrics, got %d",
			label,
			item.EvalSetID,
			item.CaseID,
			len(metricInventory),
			len(item.Metrics),
		)
	}
	seen := make(map[string]struct{}, len(item.Metrics))
	for _, metric := range item.Metrics {
		if strings.TrimSpace(metric.MetricName) == "" {
			return errorsf(
				"%s case %s/%s has an empty metric name",
				label,
				item.EvalSetID,
				item.CaseID,
			)
		}
		if _, ok := seen[metric.MetricName]; ok {
			return errorsf(
				"%s case %s/%s has duplicate metric %q",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
			)
		}
		seen[metric.MetricName] = struct{}{}
		if !containsExactlyOnce(metricInventory, metric.MetricName) {
			return errorsf(
				"%s case %s/%s metric %q is not uniquely present in expected inventory",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
			)
		}
		if !isFinite(metric.Score) || !isFinite(metric.Threshold) {
			return errorsf(
				"%s case %s/%s metric %q score and threshold must be finite",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
			)
		}
		if !isComparableResultStatus(metric.Status) {
			return errorsf(
				"%s case %s/%s metric %q has non-comparable status %q",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
				metric.Status,
			)
		}
		if !statusMatchesPassed(metric.Status, metric.Passed) {
			return errorsf(
				"%s case %s/%s metric %q status and passed flag disagree",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
			)
		}
		direction := policy.MetricDirections[metric.MetricName]
		if !isValidDirection(metric.Direction) || metric.Direction != direction {
			return errorsf(
				"%s case %s/%s metric %q direction %q does not match policy %q",
				label,
				item.EvalSetID,
				item.CaseID,
				metric.MetricName,
				metric.Direction,
				direction,
			)
		}
		for _, rubric := range metric.RubricScores {
			if !isFinite(rubric.Score) {
				return errorsf(
					"%s case %s/%s metric %q rubric %q score must be finite",
					label,
					item.EvalSetID,
					item.CaseID,
					metric.MetricName,
					rubric.ID,
				)
			}
		}
	}
	return nil
}

func validateUniqueStrings(label string, values []string) error {
	if len(values) == 0 {
		return errorsf("%s is empty", label)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return errorsf("%s contains an empty value", label)
		}
		if _, exists := seen[value]; exists {
			return errorsf("%s contains duplicate value %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateCompatibleSnapshots(
	before *EvaluationSnapshot,
	after *EvaluationSnapshot,
) error {
	beforeProvenance := before.Provenance
	afterProvenance := after.Provenance
	comparisons := []struct {
		name   string
		before string
		after  string
	}{
		{name: "eval set id",
			before: beforeProvenance.EvalSetID,
			after:  afterProvenance.EvalSetID,
		},
		{name: "eval set hash",
			before: beforeProvenance.EvalSetHash,
			after:  afterProvenance.EvalSetHash,
		},
		{name: "metrics hash",
			before: beforeProvenance.MetricsHash,
			after:  afterProvenance.MetricsHash,
		},
		{name: "split",
			before: beforeProvenance.Split,
			after:  afterProvenance.Split,
		},
		{name: "evaluator config hash",
			before: beforeProvenance.EvaluatorConfigHash,
			after:  afterProvenance.EvaluatorConfigHash,
		},
		{name: "metric policy hash",
			before: beforeProvenance.MetricPolicyHash,
			after:  afterProvenance.MetricPolicyHash,
		},
	}
	for _, comparison := range comparisons {
		if comparison.before != comparison.after {
			return errorsf(
				"snapshot provenance mismatch for %s: before %q, after %q",
				comparison.name,
				comparison.before,
				comparison.after,
			)
		}
	}
	if beforeProvenance.Seed != afterProvenance.Seed {
		return errorsf(
			"snapshot provenance mismatch for seed: before %d, after %d",
			beforeProvenance.Seed,
			afterProvenance.Seed,
		)
	}
	if !sameStringSet(
		before.Inventory.CaseIDs,
		after.Inventory.CaseIDs,
	) {
		return errorsf("snapshot case inventories differ")
	}
	if !sameStringSet(
		before.Inventory.MetricNames,
		after.Inventory.MetricNames,
	) {
		return errorsf("snapshot metric inventories differ")
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func calculateCaseDelta(
	before CaseResult,
	after CaseResult,
	metricNames []string,
	policy GatePolicy,
	epsilon float64,
) (CaseDelta, error) {
	beforeMetrics := make(map[string]MetricResult, len(before.Metrics))
	afterMetrics := make(map[string]MetricResult, len(after.Metrics))
	for _, metric := range before.Metrics {
		beforeMetrics[metric.MetricName] = metric
	}
	for _, metric := range after.Metrics {
		afterMetrics[metric.MetricName] = metric
	}
	result := CaseDelta{
		EvalSetID:    before.EvalSetID,
		CaseID:       before.CaseID,
		BeforeStatus: before.Status,
		AfterStatus:  after.Status,
		BeforePassed: before.Passed,
		AfterPassed:  after.Passed,
		HardFailure:  before.HardFailure,
		Critical:     before.Critical,
		Metrics:      make([]MetricDelta, 0, len(metricNames)),
	}
	for _, metricName := range metricNames {
		beforeMetric := beforeMetrics[metricName]
		afterMetric := afterMetrics[metricName]
		if math.Abs(beforeMetric.Threshold-afterMetric.Threshold) > epsilon {
			return CaseDelta{}, errorsf(
				"metric %q threshold changed from %.17g to %.17g",
				metricName,
				beforeMetric.Threshold,
				afterMetric.Threshold,
			)
		}
		if beforeMetric.Direction != afterMetric.Direction {
			return CaseDelta{}, errorsf(
				"metric %q direction changed from %q to %q",
				metricName,
				beforeMetric.Direction,
				afterMetric.Direction,
			)
		}
		result.Metrics = append(result.Metrics, MetricDelta{
			MetricName:   metricName,
			BeforeScore:  beforeMetric.Score,
			AfterScore:   afterMetric.Score,
			Delta:        afterMetric.Score - beforeMetric.Score,
			Direction:    beforeMetric.Direction,
			BeforeStatus: beforeMetric.Status,
			AfterStatus:  afterMetric.Status,
		})
	}
	switch {
	case !before.Passed && after.Passed:
		result.PrimaryKind = ChangeNewlyPassing
		result.Reason = "case changed from failed to passed"
	case before.Passed && !after.Passed:
		result.PrimaryKind = ChangeNewlyFailing
		result.Reason = "case changed from passed to failed"
	default:
		primaryBefore := beforeMetrics[policy.PrimaryMetric]
		primaryAfter := afterMetrics[policy.PrimaryMetric]
		gain := orientedDelta(
			primaryAfter.Score-primaryBefore.Score,
			primaryBefore.Direction,
		)
		switch {
		case gain > epsilon:
			result.PrimaryKind = ChangeImproved
			result.Reason = fmt.Sprintf(
				"primary metric %q improved by %.6g",
				policy.PrimaryMetric,
				gain,
			)
		case gain < -epsilon:
			result.PrimaryKind = ChangeRegressed
			result.Reason = fmt.Sprintf(
				"primary metric %q regressed by %.6g",
				policy.PrimaryMetric,
				-gain,
			)
		default:
			result.PrimaryKind = ChangeUnchanged
			result.Reason = fmt.Sprintf(
				"primary metric %q changed within epsilon %.6g",
				policy.PrimaryMetric,
				epsilon,
			)
		}
	}
	if len(result.Metrics) > 0 {
		primary := result.Metrics[0]
		for _, metric := range result.Metrics {
			if metric.MetricName == policy.PrimaryMetric {
				primary = metric
				break
			}
		}
		result.Evidence = appendEvidence(
			result.Evidence,
			makeEvidence(
				"delta."+policy.PrimaryMetric,
				"metric_delta",
				fmt.Sprintf(
					"before %.17g; after %.17g; raw delta %.17g; direction %s",
					primary.BeforeScore,
					primary.AfterScore,
					primary.Delta,
					primary.Direction,
				),
			),
		)
	}
	return result, nil
}

func incrementDeltaCount(summary *DeltaSummary, kind ChangeKind) {
	switch kind {
	case ChangeNewlyPassing:
		summary.NewlyPassing++
	case ChangeNewlyFailing:
		summary.NewlyFailing++
	case ChangeImproved:
		summary.Improved++
	case ChangeRegressed:
		summary.Regressed++
	case ChangeUnchanged:
		summary.Unchanged++
	}
}
