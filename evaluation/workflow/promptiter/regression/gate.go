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
)

// GatePolicy configures the acceptance requirements for a prompt candidate.
type GatePolicy struct {
	MinValidationGain float64  `json:"min_validation_gain"`
	RejectNewHardFail bool     `json:"reject_new_hard_fail"`
	HardMetrics       []string `json:"hard_metrics,omitempty"`
	CriticalCases     []string `json:"critical_cases,omitempty"`
	MaxMetricDrop     float64  `json:"max_metric_drop"`
	// MaxModelCalls limits candidate acceptance by cumulative run calls. It
	// includes baseline and all attempted rounds; zero disables the check.
	MaxModelCalls int `json:"max_model_calls,omitempty"`
	// MaxTokens applies the same cumulative acceptance limit to tokens.
	MaxTokens int64 `json:"max_tokens,omitempty"`
}

// GateDecision records whether a candidate may replace the current prompt.
type GateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

// Gate evaluates validation quality and projected cumulative run cost.
func Gate(policy GatePolicy, validation *DatasetDelta, runCost Cost) (*GateDecision, error) {
	if validation == nil {
		return nil, errors.New("validation delta is nil")
	}
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	if runCost.ModelCalls < 0 || runCost.Tokens < 0 || runCost.LatencyMS < 0 {
		return nil, errors.New("candidate cost must not be negative")
	}
	decision := &GateDecision{}
	if validation.ScoreDelta+scoreDeltaEpsilon < policy.MinValidationGain {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation score gain %.6f is below required %.6f", validation.ScoreDelta, policy.MinValidationGain,
		))
	}
	hardMetrics := stringSet(policy.HardMetrics)
	criticalCases := stringSet(policy.CriticalCases)
	foundMetrics := make(map[string]bool)
	foundCases := make(map[string]bool)
	for _, evalCase := range validation.Cases {
		if _, ok := criticalCases[evalCase.ID]; ok {
			foundCases[evalCase.ID] = true
			if evalCase.Kind == DeltaNewlyFailed || evalCase.ScoreDelta < -scoreDeltaEpsilon {
				decision.Reasons = append(decision.Reasons, fmt.Sprintf("critical case %q regressed", evalCase.ID))
			}
		}
		for _, metric := range evalCase.Metrics {
			if metric.BaselineEvaluated && !metric.CandidateEvaluated {
				decision.Reasons = append(decision.Reasons, fmt.Sprintf(
					"metric %q in case %q is no longer evaluated", metric.Name, evalCase.ID,
				))
			}
			if _, ok := hardMetrics[metric.Name]; ok {
				foundMetrics[metric.Name] = true
				newHardFail := metric.Kind == DeltaNewlyFailed ||
					(!metric.BaselineEvaluated && metric.CandidateEvaluated && !metric.CandidatePassed)
				if policy.RejectNewHardFail && newHardFail {
					decision.Reasons = append(decision.Reasons, fmt.Sprintf("hard metric %q newly failed in case %q", metric.Name, evalCase.ID))
				}
			}
			if metric.ScoreDelta < -policy.MaxMetricDrop-scoreDeltaEpsilon {
				decision.Reasons = append(decision.Reasons, fmt.Sprintf(
					"metric %q in case %q dropped %.6f", metric.Name, evalCase.ID, -metric.ScoreDelta,
				))
			}
		}
	}
	if missing := missingNames(policy.HardMetrics, foundMetrics); len(missing) > 0 {
		return nil, fmt.Errorf("hard metrics not present in validation delta: %v", missing)
	}
	if missing := missingNames(policy.CriticalCases, foundCases); len(missing) > 0 {
		return nil, fmt.Errorf("critical cases not present in validation delta: %v", missing)
	}
	if policy.MaxModelCalls > 0 && runCost.ModelCalls > policy.MaxModelCalls {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("model calls %d exceed budget %d", runCost.ModelCalls, policy.MaxModelCalls))
	}
	if policy.MaxTokens > 0 && runCost.Tokens > policy.MaxTokens {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("tokens %d exceed budget %d", runCost.Tokens, policy.MaxTokens))
	}
	decision.Accepted = len(decision.Reasons) == 0
	return decision, nil
}

func validatePolicy(policy GatePolicy) error {
	if !finite(policy.MinValidationGain) || !finite(policy.MaxMetricDrop) {
		return errors.New("gate score thresholds must be finite")
	}
	if policy.MaxMetricDrop < 0 || policy.MaxModelCalls < 0 || policy.MaxTokens < 0 {
		return errors.New("gate budgets must not be negative")
	}
	for kind, names := range map[string][]string{"hard metric": policy.HardMetrics, "critical case": policy.CriticalCases} {
		seen := make(map[string]struct{})
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s name is empty", kind)
			}
			if _, ok := seen[name]; ok {
				return fmt.Errorf("duplicate %s %q", kind, name)
			}
			seen[name] = struct{}{}
		}
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func missingNames(configured []string, found map[string]bool) []string {
	var missing []string
	for _, name := range configured {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}
