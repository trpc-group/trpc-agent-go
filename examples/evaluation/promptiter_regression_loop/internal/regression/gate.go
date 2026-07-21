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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const (
	reasonScoreGain       = "validation score gain is below threshold"
	reasonNewFailure      = "candidate introduces a new validation failure"
	reasonCriticalDrop    = "critical validation case regressed"
	reasonTokenBudget     = "candidate exceeds validation token budget"
	reasonModelCallBudget = "candidate exceeds validation model-call budget"
	reasonToolCallBudget  = "candidate exceeds validation tool-call budget"
	reasonNotEvaluated    = "candidate validation metric is not evaluated"
)

// Decide applies all configured regression gates.
func Decide(config GateConfig, input GateInput) (*GateDecision, error) {
	if err := ValidateGateConfig(config); err != nil {
		return nil, err
	}
	originalCases, err := indexCases("original baseline", input.OriginalBaseline)
	if err != nil {
		return nil, err
	}
	if _, err := indexCases("accepted baseline", input.AcceptedBaseline); err != nil {
		return nil, err
	}
	if err := validateCriticalCases(config.CriticalCaseIDs, originalCases); err != nil {
		return nil, err
	}
	decision := &GateDecision{Reasons: make([]string, 0)}
	acceptedDelta, err := Compare(input.AcceptedBaseline, input.Candidate)
	if err != nil {
		decision.Reasons = append(decision.Reasons, "candidate data is invalid: "+err.Error())
		appendBudgetReasons(config, input.Candidate, decision)
		return finishDecision(decision), nil
	}
	decision.ScoreDelta = acceptedDelta.ScoreDelta
	if decision.ScoreDelta+scoreEpsilon < config.MinValidationScoreGain {
		decision.Reasons = append(decision.Reasons, reasonScoreGain)
	}
	originalDelta, err := Compare(input.OriginalBaseline, input.Candidate)
	if err != nil {
		decision.Reasons = append(decision.Reasons, "candidate baseline comparison failed: "+err.Error())
		appendBudgetReasons(config, input.Candidate, decision)
		return finishDecision(decision), nil
	}
	appendRegressionReasons(config, acceptedDelta, decision)
	appendRegressionReasons(config, originalDelta, decision)
	appendCandidateStatusReasons(input.Candidate, decision)
	appendTraceReasons(input.Candidate, decision)
	appendBudgetReasons(config, input.Candidate, decision)
	return finishDecision(decision), nil
}

func validateCriticalCases(ids []string, cases map[caseKey]CaseResult) error {
	for _, id := range ids {
		found := false
		for key := range cases {
			if key.caseID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("critical case %q is not in original baseline", id)
		}
	}
	return nil
}

func appendCandidateStatusReasons(candidate *EvaluationResult, decision *GateDecision) {
	if candidate == nil {
		return
	}
	for _, evalCase := range candidate.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Status != status.EvalStatusNotEvaluated {
				continue
			}
			decision.Reasons = append(decision.Reasons,
				reasonNotEvaluated+": "+evalCase.CaseID+"/"+metric.Name)
		}
	}
}

// ValidateGateConfig rejects unsafe or ambiguous gate configuration.
func ValidateGateConfig(config GateConfig) error {
	switch {
	case !finite(config.MinValidationScoreGain):
		return errors.New("minimum validation score gain is not finite")
	case config.MinValidationScoreGain < 0:
		return errors.New("minimum validation score gain is negative")
	case !finite(config.MaxCriticalScoreDrop):
		return errors.New("maximum critical score drop is not finite")
	case config.MaxCriticalScoreDrop < 0:
		return errors.New("maximum critical score drop is negative")
	case config.MaxValidationTokens < 0:
		return errors.New("maximum validation tokens is negative")
	case config.MaxValidationModelCalls < 0:
		return errors.New("maximum validation model calls is negative")
	case config.MaxValidationToolCalls < 0:
		return errors.New("maximum validation tool calls is negative")
	default:
		return validateCriticalIDs(config.CriticalCaseIDs)
	}
}

func validateCriticalIDs(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return errors.New("critical case id is empty")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate critical case id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func appendRegressionReasons(config GateConfig, delta *DeltaSummary, decision *GateDecision) {
	critical := make(map[string]struct{}, len(config.CriticalCaseIDs))
	for _, id := range config.CriticalCaseIDs {
		critical[id] = struct{}{}
	}
	for _, item := range delta.Cases {
		if config.RejectNewFailures {
			appendNewFailureReasons(item, decision)
		}
		if _, ok := critical[item.CaseID]; !ok {
			continue
		}
		if criticalRegressed(item, config.MaxCriticalScoreDrop) {
			decision.Reasons = append(decision.Reasons, reasonCriticalDrop+": "+item.CaseID)
		}
	}
}

func appendNewFailureReasons(item CaseDelta, decision *GateDecision) {
	if item.Kind == DeltaNewFail {
		decision.Reasons = append(decision.Reasons, reasonNewFailure+": "+item.CaseID)
		return
	}
	for _, metric := range item.Metrics {
		if metric.Kind != DeltaNewFail {
			continue
		}
		decision.Reasons = append(decision.Reasons,
			reasonNewFailure+": "+item.CaseID+"/"+metric.Name)
	}
}

func criticalRegressed(item CaseDelta, maxDrop float64) bool {
	if item.ScoreDelta+maxDrop < -scoreEpsilon {
		return true
	}
	for _, metric := range item.Metrics {
		if metric.ScoreDelta+maxDrop < -scoreEpsilon {
			return true
		}
	}
	return false
}

func appendTraceReasons(candidate *EvaluationResult, decision *GateDecision) {
	if candidate == nil {
		return
	}
	for _, item := range candidate.Cases {
		if item.Trace.Status == "failed" || item.Trace.Status == "incomplete" {
			decision.Reasons = append(decision.Reasons,
				"candidate trace is "+item.Trace.Status+": "+item.CaseID)
		}
	}
}

func appendBudgetReasons(config GateConfig, candidate *EvaluationResult, decision *GateDecision) {
	if candidate == nil {
		return
	}
	usage := candidate.Usage
	if config.MaxValidationTokens > 0 && usage.TotalTokens > config.MaxValidationTokens {
		decision.Reasons = append(decision.Reasons, reasonTokenBudget)
	}
	if config.MaxValidationModelCalls > 0 && usage.ModelCalls > config.MaxValidationModelCalls {
		decision.Reasons = append(decision.Reasons, reasonModelCallBudget)
	}
	if config.MaxValidationToolCalls > 0 && usage.ToolCalls > config.MaxValidationToolCalls {
		decision.Reasons = append(decision.Reasons, reasonToolCallBudget)
	}
}

func finishDecision(decision *GateDecision) *GateDecision {
	sort.Strings(decision.Reasons)
	decision.Reasons = compactStrings(decision.Reasons)
	decision.Accepted = len(decision.Reasons) == 0
	if decision.Accepted {
		decision.Reasons = []string{"candidate satisfies all regression gates"}
	}
	return decision
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
