//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func adaptEvaluation(
	source *engine.EvaluationResult,
	profile *promptiter.Profile,
	critical map[string]struct{},
	audit AuditPolicy,
) (*EvaluationSnapshot, error) {
	if source == nil || profile == nil {
		return nil, errors.New("evaluation result and profile are required")
	}
	if math.IsNaN(source.OverallScore) || math.IsInf(source.OverallScore, 0) {
		return nil, errors.New("evaluation overall score must be finite")
	}
	profileHash, err := ProfileHash(profile)
	if err != nil {
		return nil, err
	}
	snapshot := &EvaluationSnapshot{
		ProfileHash:  profileHash,
		OverallScore: source.OverallScore,
		Complete:     true,
	}
	evalSetIDs := make([]string, 0, len(source.EvalSets))
	seenEvalSets := make(map[string]struct{}, len(source.EvalSets))
	seenCases := make(map[string]struct{})
	runScores := make(runMetricScores)
	for _, evalSet := range source.EvalSets {
		if strings.TrimSpace(evalSet.EvalSetID) == "" {
			return nil, errors.New("evaluation set id is empty")
		}
		if _, exists := seenEvalSets[evalSet.EvalSetID]; exists {
			return nil, fmt.Errorf("duplicate evaluation set id %q", evalSet.EvalSetID)
		}
		seenEvalSets[evalSet.EvalSetID] = struct{}{}
		evalSetIDs = append(evalSetIDs, evalSet.EvalSetID)
		for _, sourceCase := range evalSet.Cases {
			key := evalSet.EvalSetID + "\x00" + sourceCase.EvalCaseID
			if _, exists := seenCases[key]; exists {
				return nil, fmt.Errorf(
					"duplicate evaluation case id %q in eval set %q",
					sourceCase.EvalCaseID, evalSet.EvalSetID,
				)
			}
			seenCases[key] = struct{}{}
			caseResult, complete, err := adaptCase(
				sourceCase, evalSet.EvalSetID, critical, runScores, audit,
			)
			if err != nil {
				return nil, fmt.Errorf("adapt evaluation case %q: %w", sourceCase.EvalCaseID, err)
			}
			snapshot.Complete = snapshot.Complete && complete
			snapshot.Cases = append(snapshot.Cases, caseResult)
		}
	}
	sort.Strings(evalSetIDs)
	snapshot.EvalSetID = strings.Join(evalSetIDs, ",")
	sort.Slice(snapshot.Cases, func(i, j int) bool {
		if snapshot.Cases[i].EvalSetID != snapshot.Cases[j].EvalSetID {
			return snapshot.Cases[i].EvalSetID < snapshot.Cases[j].EvalSetID
		}
		return snapshot.Cases[i].CaseID < snapshot.Cases[j].CaseID
	})
	snapshot.ScoreStdDev = scoreStdDev(runScores)
	if len(snapshot.Cases) == 0 {
		snapshot.Complete = false
	}
	return snapshot, nil
}

func adaptCase(
	source engine.CaseResult,
	evalSetID string,
	critical map[string]struct{},
	runScores runMetricScores,
	audit AuditPolicy,
) (CaseResult, bool, error) {
	result := CaseResult{EvalSetID: evalSetID, CaseID: source.EvalCaseID, Passed: true}
	if strings.TrimSpace(source.EvalCaseID) == "" {
		return CaseResult{}, false, errors.New("evaluation case id is empty")
	}
	_, result.Critical = critical[source.EvalCaseID]
	complete := source.EvalCaseID != "" && len(source.Metrics) > 0
	aggregateMetrics := make(map[string]MetricResult, len(source.Metrics))
	for _, metric := range source.Metrics {
		if strings.TrimSpace(metric.MetricName) == "" {
			return CaseResult{}, false, errors.New("metric name is empty")
		}
		if _, exists := aggregateMetrics[metric.MetricName]; exists {
			return CaseResult{}, false, fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		if math.IsNaN(metric.Score) || math.IsInf(metric.Score, 0) ||
			math.IsNaN(metric.Threshold) || math.IsInf(metric.Threshold, 0) {
			return CaseResult{}, false, fmt.Errorf("metric %q score and threshold must be finite", metric.MetricName)
		}
		converted := MetricResult{
			Name:      metric.MetricName,
			Score:     metric.Score,
			Threshold: metric.Threshold,
			Passed:    metric.Status == status.EvalStatusPassed,
			Reason:    sanitizeContent(audit, metric.Reason),
		}
		if metric.Status != status.EvalStatusPassed && metric.Status != status.EvalStatusFailed {
			complete = false
		}
		if metric.Details != nil {
			seenRubrics := make(map[string]struct{}, len(metric.Details.RubricScores))
			for _, rubric := range metric.Details.RubricScores {
				if rubric == nil {
					complete = false
					continue
				}
				if strings.TrimSpace(rubric.ID) == "" {
					return CaseResult{}, false, fmt.Errorf("metric %q rubric id is empty", metric.MetricName)
				}
				if _, exists := seenRubrics[rubric.ID]; exists {
					return CaseResult{}, false, fmt.Errorf("metric %q has duplicate rubric %q", metric.MetricName, rubric.ID)
				}
				seenRubrics[rubric.ID] = struct{}{}
				if math.IsNaN(rubric.Score) || math.IsInf(rubric.Score, 0) {
					return CaseResult{}, false, fmt.Errorf("metric %q rubric %q score must be finite", metric.MetricName, rubric.ID)
				}
				converted.Rubrics = append(converted.Rubrics, RubricResult{
					ID: rubric.ID, Score: rubric.Score,
					Reason: sanitizeContent(audit, rubric.Reason),
				})
			}
		}
		sort.Slice(converted.Rubrics, func(i, j int) bool {
			return converted.Rubrics[i].ID < converted.Rubrics[j].ID
		})
		result.Metrics = append(result.Metrics, converted)
		aggregateMetrics[converted.Name] = converted
		if !converted.Passed {
			result.Passed = false
		}
	}
	sort.Slice(result.Metrics, func(i, j int) bool {
		return result.Metrics[i].Name < result.Metrics[j].Name
	})
	if len(source.RunDetails) != len(source.RunResults) {
		complete = false
	}
	detailIDs := make(map[int]struct{}, len(source.RunDetails))
	for _, run := range source.RunDetails {
		if run != nil {
			if run.RunID <= 0 {
				return CaseResult{}, false, fmt.Errorf("run detail id %d must be positive", run.RunID)
			}
			if _, exists := detailIDs[run.RunID]; exists {
				return CaseResult{}, false, fmt.Errorf("duplicate run detail id %d", run.RunID)
			}
			detailIDs[run.RunID] = struct{}{}
		}
		observation, input, ok := adaptObservation(run, audit)
		if !ok {
			complete = false
			continue
		}
		if audit.IncludeRawContent && result.Input == "" {
			result.Input = input
		}
		if observationHasExecutionError(observation) {
			result.Passed = false
		}
		result.Runs = append(result.Runs, observation)
	}
	if len(result.Runs) == 0 {
		complete = false
	}
	resultIDs := make(map[int]struct{}, len(source.RunResults))
	for _, run := range source.RunResults {
		if run == nil {
			complete = false
			continue
		}
		if run.RunID <= 0 {
			return CaseResult{}, false, fmt.Errorf("run result id %d must be positive", run.RunID)
		}
		if _, exists := resultIDs[run.RunID]; exists {
			return CaseResult{}, false, fmt.Errorf("duplicate run result id %d", run.RunID)
		}
		resultIDs[run.RunID] = struct{}{}
		runMetrics := make(map[string]struct{}, len(run.OverallEvalMetricResults))
		for _, metricResult := range run.OverallEvalMetricResults {
			if metricResult == nil {
				complete = false
				continue
			}
			if strings.TrimSpace(metricResult.MetricName) == "" {
				return CaseResult{}, false, fmt.Errorf("run %d metric name is empty", run.RunID)
			}
			if _, exists := runMetrics[metricResult.MetricName]; exists {
				return CaseResult{}, false, fmt.Errorf(
					"run %d has duplicate metric %q", run.RunID, metricResult.MetricName,
				)
			}
			runMetrics[metricResult.MetricName] = struct{}{}
			aggregateMetric, exists := aggregateMetrics[metricResult.MetricName]
			if !exists {
				complete = false
				continue
			}
			if math.IsNaN(metricResult.Score) || math.IsInf(metricResult.Score, 0) ||
				math.IsNaN(metricResult.Threshold) || math.IsInf(metricResult.Threshold, 0) {
				return CaseResult{}, false, fmt.Errorf(
					"run %d metric %q score and threshold must be finite",
					run.RunID, metricResult.MetricName,
				)
			}
			if metricResult.Threshold != aggregateMetric.Threshold {
				return CaseResult{}, false, fmt.Errorf(
					"run %d metric %q threshold does not match aggregate evidence",
					run.RunID, metricResult.MetricName,
				)
			}
			switch metricResult.EvalStatus {
			case status.EvalStatusPassed, status.EvalStatusFailed:
				runScores.add(run.RunID, evalSetID, metricResult.Score)
			case status.EvalStatusNotEvaluated:
				complete = false
			default:
				return CaseResult{}, false, fmt.Errorf(
					"run %d metric %q has invalid status %q",
					run.RunID, metricResult.MetricName, metricResult.EvalStatus,
				)
			}
		}
		for metricName := range aggregateMetrics {
			if _, exists := runMetrics[metricName]; !exists {
				complete = false
				break
			}
		}
	}
	if len(detailIDs) > 0 && len(resultIDs) > 0 {
		for runID := range detailIDs {
			if _, exists := resultIDs[runID]; !exists {
				return CaseResult{}, false, fmt.Errorf("run detail id %d has no matching result", runID)
			}
		}
		for runID := range resultIDs {
			if _, exists := detailIDs[runID]; !exists {
				return CaseResult{}, false, fmt.Errorf("run result id %d has no matching detail", runID)
			}
		}
	}
	return result, complete, nil
}

func adaptObservation(
	run *evaluation.EvaluationCaseRunDetails,
	audit AuditPolicy,
) (Observation, string, bool) {
	if run == nil || run.Inference == nil {
		return Observation{}, "", false
	}
	errorMessage := strings.TrimSpace(run.Inference.ErrorMessage)
	if errorMessage == "" && run.Inference.Status == status.EvalStatusFailed {
		errorMessage = "inference failed without an error message"
	}
	observation := Observation{
		RunID: run.RunID,
		Error: sanitizeContent(audit, errorMessage),
	}
	input := ""
	for _, invocation := range run.Inference.Inferences {
		if invocation == nil {
			continue
		}
		if input == "" && invocation.UserContent != nil {
			input = invocation.UserContent.Content
		}
		if audit.IncludeRawContent && invocation.FinalResponse != nil {
			observation.FinalResponse = sanitizeContent(audit, invocation.FinalResponse.Content)
		}
		for _, toolCall := range invocation.Tools {
			if toolCall == nil {
				continue
			}
			toolObservation := ToolObservation{
				Name:  toolCall.Name,
				Error: sanitizeContent(audit, toolResultError(toolCall.Result)),
			}
			if audit.IncludeRawContent {
				toolObservation.Arguments = sanitizeStructuredContent(
					audit, marshalAuditValue(toolCall.Arguments),
				)
				toolObservation.Result = sanitizeStructuredContent(
					audit, marshalAuditValue(toolCall.Result),
				)
			}
			observation.Tools = append(observation.Tools, toolObservation)
		}
	}
	for _, trace := range run.Inference.ExecutionTraces {
		if trace == nil {
			continue
		}
		for stepIndex, step := range trace.Steps {
			stepID := step.StepID
			if stepID == "" {
				stepID = fmt.Sprintf("step-%d", stepIndex+1)
			}
			converted := TraceStep{
				StepID:            stepID,
				NodeID:            step.NodeID,
				Branch:            step.Branch,
				AppliedSurfaceIDs: append([]string(nil), step.AppliedSurfaceIDs...),
				Error:             sanitizeContent(audit, step.Error),
			}
			if audit.IncludeRawContent {
				if step.Input != nil {
					converted.Input = sanitizeContent(audit, step.Input.Text)
				}
				if step.Output != nil {
					converted.Output = sanitizeContent(audit, step.Output.Text)
				}
			}
			observation.Trace = append(observation.Trace, converted)
			if step.Branch != "" {
				observation.Route = step.Branch
			}
		}
	}
	return observation, input, true
}

// toolResultError extracts an explicit failure message from the tool result
// captured by the evaluation service. evalset.Tool does not carry a separate
// error field, so only conventional structured error keys are treated as
// failures; ordinary string results remain successful tool output.
func toolResultError(result any) string {
	if err, ok := result.(error); ok && err != nil {
		return strings.TrimSpace(err.Error())
	}
	for _, values := range []map[string]any{
		asStringAnyMap(result),
		asStringStringMap(result),
	} {
		for _, key := range []string{"error", "errorMessage", "err"} {
			if value, ok := values[key]; ok && value != nil {
				if message := strings.TrimSpace(fmt.Sprint(value)); message != "" {
					return message
				}
			}
		}
	}
	return ""
}

func asStringAnyMap(value any) map[string]any {
	if values, ok := value.(map[string]any); ok {
		return values
	}
	return nil
}

func asStringStringMap(value any) map[string]any {
	values, ok := value.(map[string]string)
	if !ok {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func marshalAuditValue(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return unsupportedAuditValue(value)
	}
	return string(encoded)
}

type runMetricScores map[int]map[string][]float64

func (s runMetricScores) add(runID int, evalSetID string, score float64) {
	if s[runID] == nil {
		s[runID] = make(map[string][]float64)
	}
	s[runID][evalSetID] = append(s[runID][evalSetID], score)
}

func scoreStdDev(byRun runMetricScores) float64 {
	// PromptIter averages metrics within each eval set, then averages eval sets
	// equally. Reconstruct each repeated run with that same two-level
	// aggregation before calculating the sample standard deviation.
	ids := make([]int, 0, len(byRun))
	for id := range byRun {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	values := make([]float64, 0, len(ids))
	for _, id := range ids {
		byEvalSet := byRun[id]
		if len(byEvalSet) == 0 {
			continue
		}
		evalSetIDs := make([]string, 0, len(byEvalSet))
		for evalSetID := range byEvalSet {
			evalSetIDs = append(evalSetIDs, evalSetID)
		}
		sort.Strings(evalSetIDs)
		totalAcrossSets := 0.0
		setCount := 0
		for _, evalSetID := range evalSetIDs {
			scores := byEvalSet[evalSetID]
			if len(scores) == 0 {
				continue
			}
			total := 0.0
			for _, score := range scores {
				total += score
			}
			totalAcrossSets += total / float64(len(scores))
			setCount++
		}
		if setCount > 0 {
			values = append(values, totalAcrossSets/float64(setCount))
		}
	}
	if len(values) < 2 {
		return 0
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	return math.Sqrt(variance / float64(len(values)-1))
}

func markConfiguredMetricCoverage(
	snapshot *EvaluationSnapshot,
	policies map[string]MetricPolicy,
) {
	if snapshot == nil || len(policies) == 0 {
		return
	}
	for _, caseResult := range snapshot.Cases {
		observed := make(map[string]struct{}, len(caseResult.Metrics))
		for _, metric := range caseResult.Metrics {
			observed[metric.Name] = struct{}{}
		}
		for metricName := range policies {
			if _, exists := observed[metricName]; !exists {
				snapshot.Complete = false
				break
			}
		}
	}
}

func markExpectedRunCoverage(snapshot *EvaluationSnapshot, expectedRuns int) {
	if snapshot == nil || expectedRuns <= 0 {
		return
	}
	for _, caseResult := range snapshot.Cases {
		if len(caseResult.Runs) != expectedRuns {
			snapshot.Complete = false
			continue
		}
		seen := make(map[int]struct{}, len(caseResult.Runs))
		for _, run := range caseResult.Runs {
			seen[run.RunID] = struct{}{}
		}
		for runID := 1; runID <= expectedRuns; runID++ {
			if _, exists := seen[runID]; !exists {
				snapshot.Complete = false
				break
			}
		}
	}
}
