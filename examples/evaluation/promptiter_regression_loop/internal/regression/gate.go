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
	case policy.MaxValidationTokens < 0:
		return errors.New("maximum validation tokens is negative")
	case policy.MaxValidationModelCalls < 0:
		return errors.New("maximum validation model calls is negative")
	case policy.MaxValidationToolCalls < 0:
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
	if _, err := indexCases("accepted baseline", input.AcceptedBaseline); err != nil {
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

func appendCandidateIntegrityReasons(candidate *EvaluationResult, decision *GateDecision) {
	if candidate.OverallStatus != status.EvalStatusPassed && candidate.OverallStatus != status.EvalStatusFailed {
		decision.Reasons = append(decision.Reasons,
			fmt.Sprintf("candidate overall evaluation status is %s", candidate.OverallStatus))
	}
	for _, evalCase := range candidate.Cases {
		if evalCase.Trace.Status != "completed" {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("candidate trace for %s is %s", evalCase.CaseID, evalCase.Trace.Status))
		}
		if evalCase.ErrorMessage != "" {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("candidate case %s failed execution", evalCase.CaseID))
		}
		for _, metricResult := range evalCase.Metrics {
			if metricResult.Status == status.EvalStatusNotEvaluated || metricResult.Status == status.EvalStatusUnknown {
				decision.Reasons = append(decision.Reasons,
					fmt.Sprintf("candidate metric %s/%s is %s",
						evalCase.CaseID, metricResult.Name, metricResult.Status))
			}
		}
	}
}

func appendBudgetReasons(policy GatePolicy, candidate *EvaluationResult, decision *GateDecision) {
	budgetEnabled := policy.MaxValidationTokens > 0 || policy.MaxValidationModelCalls > 0 ||
		policy.MaxValidationToolCalls > 0
	if budgetEnabled && !candidate.Usage.Measured {
		decision.Reasons = append(decision.Reasons, "candidate validation usage is not measured")
		return
	}
	usage := candidate.Usage
	if policy.MaxValidationTokens > 0 && usage.TotalTokens > policy.MaxValidationTokens {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation tokens %d exceed budget %d", usage.TotalTokens, policy.MaxValidationTokens))
	}
	if policy.MaxValidationModelCalls > 0 && usage.ModelCalls > policy.MaxValidationModelCalls {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation model calls %d exceed budget %d", usage.ModelCalls, policy.MaxValidationModelCalls))
	}
	if policy.MaxValidationToolCalls > 0 && usage.ToolCalls > policy.MaxValidationToolCalls {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation tool calls %d exceed budget %d", usage.ToolCalls, policy.MaxValidationToolCalls))
	}
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
