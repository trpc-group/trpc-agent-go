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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// GateInput contains both the original baseline and the last released
// baseline. Comparing against both prevents a sequence of individually small
// regressions from drifting past the original protected behavior.
type GateInput struct {
	OriginalBaseline *EvaluationResult
	AcceptedBaseline *EvaluationResult
	Candidate        *EvaluationResult
}

// ValidateGatePolicy rejects values that would silently disable a release rule.
func ValidateGatePolicy(policy GatePolicy) error {
	switch {
	case !finite(policy.MinValidationScoreGain):
		return errors.New("minimum validation score gain is not finite")
	case policy.MinValidationScoreGain < 0:
		return errors.New("minimum validation score gain is negative")
	case !finite(policy.MaxCriticalScoreDrop):
		return errors.New("maximum critical score drop is not finite")
	case policy.MaxCriticalScoreDrop < 0:
		return errors.New("maximum critical score drop is negative")
	case negativeBudget(policy.MaxValidationTokens):
		return errors.New("maximum validation tokens is negative")
	case negativeBudget(policy.MaxValidationModelCalls):
		return errors.New("maximum validation model calls is negative")
	case negativeBudget(policy.MaxValidationToolCalls):
		return errors.New("maximum validation tool calls is negative")
	}
	seen := make(map[string]struct{}, len(policy.CriticalCaseIDs))
	for _, caseID := range policy.CriticalCaseIDs {
		caseID = strings.TrimSpace(caseID)
		if caseID == "" {
			return errors.New("critical case id is empty")
		}
		if _, ok := seen[caseID]; ok {
			return fmt.Errorf("duplicate critical case id %q", caseID)
		}
		seen[caseID] = struct{}{}
	}
	return nil
}

// Decide applies the configured release gate. Malformed or incomplete
// candidate evidence is represented as a rejection so the audit can still be
// written; malformed baseline or policy input returns an error.
func Decide(policy GatePolicy, input GateInput) (*GateDecision, error) {
	if err := ValidateGatePolicy(policy); err != nil {
		return nil, err
	}
	originalCases, err := indexCases("original baseline", input.OriginalBaseline)
	if err != nil {
		return nil, err
	}
	if err := validateBaselineEvidence("original baseline", input.OriginalBaseline); err != nil {
		return nil, err
	}
	if _, err := indexCases("accepted baseline", input.AcceptedBaseline); err != nil {
		return nil, err
	}
	if err := validateBaselineEvidence("accepted baseline", input.AcceptedBaseline); err != nil {
		return nil, err
	}
	if err := validateCriticalCaseScope(policy.CriticalCaseIDs, originalCases); err != nil {
		return nil, err
	}
	decision := &GateDecision{Reasons: []string{}, NewFailures: []string{}, CriticalRegressions: []string{}}
	if input.Candidate == nil {
		decision.Reasons = append(decision.Reasons, "candidate validation result is missing")
		return finalizeDecision(decision), nil
	}
	decision.ScoreDelta = input.Candidate.OverallScore - input.AcceptedBaseline.OverallScore
	if !finite(decision.ScoreDelta) {
		decision.Reasons = append(decision.Reasons, "candidate validation score delta is not finite")
		return finalizeDecision(decision), nil
	}
	acceptedDelta, err := Compare(input.AcceptedBaseline, input.Candidate)
	if err != nil {
		decision.Reasons = append(decision.Reasons, "candidate validation is incomplete: "+err.Error())
		appendCandidateIntegrityReasons(input.Candidate, decision)
		appendBudgetReasons(policy, input.Candidate, decision)
		return finalizeDecision(decision), nil
	}
	originalDelta, err := Compare(input.OriginalBaseline, input.Candidate)
	if err != nil {
		decision.Reasons = append(decision.Reasons, "candidate cannot be compared with original baseline: "+err.Error())
		appendCandidateIntegrityReasons(input.Candidate, decision)
		appendBudgetReasons(policy, input.Candidate, decision)
		return finalizeDecision(decision), nil
	}
	if decision.ScoreDelta+scoreEpsilon < policy.MinValidationScoreGain {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation score gain %.4f is below required %.4f",
			decision.ScoreDelta, policy.MinValidationScoreGain))
	}
	decision.NewFailures = collectNewFailures(acceptedDelta, originalDelta)
	if policy.RejectNewFailures && len(decision.NewFailures) > 0 {
		decision.Reasons = append(decision.Reasons,
			"candidate introduces validation failures: "+strings.Join(decision.NewFailures, ", "))
	}
	decision.CriticalRegressions = collectCriticalRegressions(
		policy.CriticalCaseIDs, policy.MaxCriticalScoreDrop, acceptedDelta, originalDelta,
	)
	if policy.RejectCriticalRegressions && len(decision.CriticalRegressions) > 0 {
		decision.Reasons = append(decision.Reasons,
			"critical validation cases regressed: "+strings.Join(decision.CriticalRegressions, ", "))
	}
	appendCandidateIntegrityReasons(input.Candidate, decision)
	appendBudgetReasons(policy, input.Candidate, decision)
	return finalizeDecision(decision), nil
}

func validateCriticalCaseScope(ids []string, baseline map[caseKey]CaseResult) error {
	for _, id := range ids {
		found := false
		for key := range baseline {
			if key.caseID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("critical case %q is absent from original validation", id)
		}
	}
	return nil
}

func collectNewFailures(deltas ...*DeltaSummary) []string {
	set := make(map[string]struct{})
	for _, delta := range deltas {
		if delta == nil {
			continue
		}
		for _, evalCase := range delta.Cases {
			if evalCase.Kind == DeltaNewFail {
				set[evalCase.CaseID] = struct{}{}
			}
			for _, metricResult := range evalCase.Metrics {
				if metricResult.Kind == DeltaNewFail {
					set[evalCase.CaseID+"/"+metricResult.Name] = struct{}{}
				}
			}
		}
	}
	return sortedSet(set)
}

func collectCriticalRegressions(ids []string, maxDrop float64, deltas ...*DeltaSummary) []string {
	critical := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		critical[id] = struct{}{}
	}
	set := make(map[string]struct{})
	for _, delta := range deltas {
		if delta == nil {
			continue
		}
		for _, evalCase := range delta.Cases {
			if _, ok := critical[evalCase.CaseID]; !ok {
				continue
			}
			if evalCase.Kind == DeltaNewFail || evalCase.ScoreDelta+maxDrop < -scoreEpsilon {
				set[evalCase.CaseID] = struct{}{}
				continue
			}
			for _, metricResult := range evalCase.Metrics {
				if metricResult.Kind == DeltaNewFail || metricResult.ScoreDelta+maxDrop < -scoreEpsilon {
					set[evalCase.CaseID+"/"+metricResult.Name] = struct{}{}
				}
			}
		}
	}
	return sortedSet(set)
}

func validateBaselineEvidence(name string, baseline *EvaluationResult) error {
	reasons := evaluationIntegrityReasons(name, baseline)
	if len(reasons) == 0 {
		return nil
	}
	return errors.New(strings.Join(reasons, "; "))
}

func appendCandidateIntegrityReasons(candidate *EvaluationResult, decision *GateDecision) {
	decision.Reasons = append(decision.Reasons, evaluationIntegrityReasons("candidate", candidate)...)
}

func evaluationIntegrityReasons(name string, result *EvaluationResult) []string {
	resultReasons := make([]string, 0)
	if result.OverallStatus != status.EvalStatusPassed && result.OverallStatus != status.EvalStatusFailed {
		resultReasons = append(resultReasons,
			fmt.Sprintf("%s overall evaluation status is %s", name, result.OverallStatus))
	}
	for _, evalCase := range result.Cases {
		if evalCase.Trace.Status != "completed" {
			resultReasons = append(resultReasons,
				fmt.Sprintf("%s trace for %s is %s", name, evalCase.CaseID, evalCase.Trace.Status))
		}
		if evalCase.ErrorMessage != "" {
			resultReasons = append(resultReasons,
				fmt.Sprintf("%s case %s failed execution", name, evalCase.CaseID))
		}
		for _, metricResult := range evalCase.Metrics {
			if metricResult.Status == status.EvalStatusNotEvaluated || metricResult.Status == status.EvalStatusUnknown {
				resultReasons = append(resultReasons,
					fmt.Sprintf("%s metric %s/%s is %s",
						name, evalCase.CaseID, metricResult.Name, metricResult.Status))
			}
		}
	}
	return resultReasons
}

func appendBudgetReasons(policy GatePolicy, candidate *EvaluationResult, decision *GateDecision) {
	budgetEnabled := policy.MaxValidationTokens != nil || policy.MaxValidationModelCalls != nil ||
		policy.MaxValidationToolCalls != nil
	if budgetEnabled && !candidate.Usage.Measured {
		decision.Reasons = append(decision.Reasons, "candidate validation usage is not measured")
		return
	}
	usage := candidate.Usage
	if policy.MaxValidationTokens != nil && usage.TotalTokens > *policy.MaxValidationTokens {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation tokens %d exceed budget %d", usage.TotalTokens, *policy.MaxValidationTokens))
	}
	if policy.MaxValidationModelCalls != nil && usage.ModelCalls > *policy.MaxValidationModelCalls {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation model calls %d exceed budget %d", usage.ModelCalls, *policy.MaxValidationModelCalls))
	}
	if policy.MaxValidationToolCalls != nil && usage.ToolCalls > *policy.MaxValidationToolCalls {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation tool calls %d exceed budget %d", usage.ToolCalls, *policy.MaxValidationToolCalls))
	}
}

func negativeBudget(value *int) bool {
	return value != nil && *value < 0
}

func finalizeDecision(decision *GateDecision) *GateDecision {
	sort.Strings(decision.Reasons)
	decision.Reasons = compactStrings(decision.Reasons)
	decision.Accepted = len(decision.Reasons) == 0
	if decision.Accepted {
		decision.Reasons = []string{"candidate satisfies every configured release gate"}
	}
	return decision
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
