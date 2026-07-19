//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"time"
)

func (wrapper *executionWrapper) inspectStateDelta(
	ctx context.Context,
	delta map[string][]byte,
) (map[string][]byte, error) {
	if len(delta) == 0 {
		return delta, nil
	}
	if violation, ok := wrapper.stateDeltaViolation(delta); ok {
		return nil, wrapper.recordStateDeltaViolation(ctx, violation)
	}
	return cloneStateDelta(delta), nil
}

func (wrapper *executionWrapper) stateDeltaViolation(
	delta map[string][]byte,
) (outputViolation, bool) {
	var total int64
	for key, value := range delta {
		total += int64(len(key)) + int64(len(value))
	}
	if total > wrapper.guard.policy.maxOutputBytes {
		return outputViolation{
			ruleID:         "STATE_DELTA_LIMIT_EXCEEDED",
			riskLevel:      RiskLevelHigh,
			decision:       DecisionDeny,
			evidence:       "state delta exceeded the configured output limit",
			recommendation: "store large artifacts externally and return a bounded reference",
		}, true
	}
	for key, value := range delta {
		pair := key + "=" + string(value)
		if sensitiveEnvKey(key) || hasSensitiveText(pair) ||
			hasSensitiveText(string(value)) {
			return outputViolation{
				ruleID:         "SECRET_IN_STATE_DELTA",
				riskLevel:      RiskLevelHigh,
				decision:       DecisionDeny,
				evidence:       "sensitive material detected in state delta or inline artifact",
				recommendation: "remove secrets and publish only redacted artifact metadata",
			}, true
		}
	}
	return outputViolation{}, false
}

func (wrapper *executionWrapper) recordStateDeltaViolation(
	ctx context.Context,
	violation outputViolation,
) error {
	finding := newFinding(
		violation.ruleID,
		violation.riskLevel,
		violation.decision,
		violation.evidence,
		violation.recommendation,
	)
	report := Report{
		Decision:       finding.Decision,
		RiskLevel:      finding.RiskLevel,
		RuleID:         finding.RuleID,
		Evidence:       finding.Evidence,
		Recommendation: finding.Recommendation,
		ToolName:       wrapper.binding.ToolName,
		Backend:        wrapper.binding.Backend,
		Provider:       wrapper.binding.Provider,
		Blocked:        false,
		Redacted:       violation.ruleID == "SECRET_IN_STATE_DELTA",
		DurationMS:     time.Duration(0).Milliseconds(),
		PolicyVersion:  wrapper.guard.policy.versionString(),
		Findings:       []Finding{finding},
	}
	report, auditErr := wrapper.guard.finalizeReport(
		normalizeContext(ctx), report, auditPhasePostcheck,
	)
	if auditErr != nil {
		return errors.Join(
			newExecutionError(report, auditPhasePostcheck),
			auditErr,
		)
	}
	return nil
}

func cloneStateDelta(delta map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(delta))
	for key, value := range delta {
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}
