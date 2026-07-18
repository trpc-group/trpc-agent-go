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
	"encoding/json"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type outputSnapshot struct {
	serialized []byte
	sensitive  string
}

type outputViolation struct {
	ruleID         string
	riskLevel      RiskLevel
	decision       Decision
	evidence       string
	recommendation string
}

func (wrapper *executionWrapper) call(
	ctx context.Context,
	arguments []byte,
) (any, error) {
	report, err := wrapper.precheck(ctx, arguments)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(normalizeContext(ctx), wrapper.guard.policy.maxTimeout)
	defer cancel()
	result, err := wrapper.callable.Call(runCtx, arguments)
	if err != nil {
		return nil, wrapper.inspectToolError(ctx, report, err)
	}
	snapshot, err := snapshotOutput(result)
	if err != nil {
		return nil, wrapper.rejectOutput(ctx, report, outputViolation{
			ruleID:         "TOOL_OUTPUT_UNINSPECTABLE",
			riskLevel:      RiskLevelHigh,
			decision:       DecisionNeedsHumanReview,
			evidence:       "tool output could not be serialized for safety checks",
			recommendation: "return a JSON-serializable result and review the tool",
		})
	}
	if err := wrapper.inspectOutput(ctx, report, snapshot); err != nil {
		return nil, err
	}
	return result, nil
}

func (wrapper *executionWrapper) precheck(
	ctx context.Context,
	arguments []byte,
) (Report, error) {
	toolCallID, _ := tool.ToolCallIDFromContext(normalizeContext(ctx))
	report, err := wrapper.guard.scanRequest(ctx, AdaptRequest{
		ToolName:   wrapper.binding.ToolName,
		ToolCallID: toolCallID,
		Arguments:  append([]byte(nil), arguments...),
		Metadata:   tool.MetadataOf(wrapper.semantic),
	}, wrapper.binding)
	if err != nil {
		return report, errors.Join(
			newExecutionError(report, auditPhasePrecheck),
			err,
		)
	}
	if report.Decision != DecisionAllow {
		return report, newExecutionError(report, auditPhasePrecheck)
	}
	return report, nil
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func snapshotOutput(result any) (outputSnapshot, error) {
	serialized, err := json.Marshal(result)
	if err != nil {
		return outputSnapshot{}, err
	}
	return outputSnapshot{
		serialized: serialized,
		sensitive:  string(serialized),
	}, nil
}

func (wrapper *executionWrapper) inspectOutput(
	ctx context.Context,
	precheck Report,
	snapshot outputSnapshot,
) error {
	if int64(len(snapshot.serialized)) > wrapper.guard.policy.maxOutputBytes {
		return wrapper.rejectOutput(ctx, precheck, outputViolation{
			ruleID:         "RESOURCE_OUTPUT_LIMIT_EXCEEDED",
			riskLevel:      RiskLevelHigh,
			decision:       DecisionDeny,
			evidence:       "tool output exceeds the configured byte limit",
			recommendation: "reduce output size or use a bounded result format",
		})
	}
	if hasSensitiveText(snapshot.sensitive) {
		return wrapper.rejectOutput(ctx, precheck, outputViolation{
			ruleID:         "SECRET_IN_TOOL_OUTPUT",
			riskLevel:      RiskLevelHigh,
			decision:       DecisionDeny,
			evidence:       "sensitive material detected in tool output",
			recommendation: "remove secrets and return only redacted output",
		})
	}
	return nil
}

func (wrapper *executionWrapper) inspectToolError(
	ctx context.Context,
	precheck Report,
	toolErr error,
) error {
	if !hasSensitiveText(toolErr.Error()) {
		return toolErr
	}
	return wrapper.rejectOutput(ctx, precheck, outputViolation{
		ruleID:         "SECRET_IN_TOOL_OUTPUT",
		riskLevel:      RiskLevelHigh,
		decision:       DecisionDeny,
		evidence:       "sensitive material detected in tool error",
		recommendation: "remove secrets from tool errors and logs",
	})
}

func (wrapper *executionWrapper) rejectOutput(
	ctx context.Context,
	precheck Report,
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
		ToolName:       precheck.ToolName,
		Command:        precheck.Command,
		Backend:        precheck.Backend,
		Blocked:        false,
		DurationMS:     time.Duration(0).Milliseconds(),
		PolicyVersion:  wrapper.guard.policy.versionString(),
		Findings:       []Finding{finding},
	}
	report, auditErr := wrapper.guard.finalizeReport(ctx, report, auditPhasePostcheck)
	safetyErr := newExecutionError(report, auditPhasePostcheck)
	if auditErr != nil {
		return errors.Join(safetyErr, auditErr)
	}
	return safetyErr
}
