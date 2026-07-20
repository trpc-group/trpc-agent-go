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
	"reflect"
	"time"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	ruleOutputUninspectable = "TOOL_OUTPUT_UNINSPECTABLE"
	ruleOutputLimit         = "RESOURCE_OUTPUT_LIMIT_EXCEEDED"
	ruleOutputSecret        = "SECRET_IN_TOOL_OUTPUT"
	ruleExecutionTimeout    = "RESOURCE_TIMEOUT_EXCEEDED"
	rulePostcheckFailed     = "SAFETY_POSTCHECK_FAILED"
)

// BlockedResult is a redacted, successful transport result. It deliberately
// does not implement error or any result-error marker, so retry runners treat a
// post-execution safety block as terminal.
type BlockedResult struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Backend        Backend   `json:"backend"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
}

type outputGuard struct {
	guard    *Guard
	inner    tool.Tool
	semantic tool.Tool
	binding  Binding
	callable tool.CallableTool
}

type outputViolation struct {
	ruleID         string
	riskLevel      RiskLevel
	decision       Decision
	evidence       string
	recommendation string
	redacted       bool
}

// WrapOutputGuard inspects non-streaming tool output after execution. Use it
// together with NewPermissionPolicy for complete pre- and post-execution
// protection.
func WrapOutputGuard(
	guard *Guard,
	inner tool.Tool,
	binding Binding,
) (tool.Tool, error) {
	wrapper, err := newOutputGuard(guard, inner, binding)
	if err != nil {
		return nil, err
	}
	return wrapper, nil
}

func newOutputGuard(
	guard *Guard,
	inner tool.Tool,
	binding Binding,
) (*outputGuard, error) {
	if err := validateOutputGuard(guard, inner, binding); err != nil {
		return nil, err
	}
	semantic := itool.ResolveSemantic(inner)
	if _, ok := semantic.(tool.StreamableTool); ok {
		return nil, errors.New("tool safety: streaming tools are unsupported")
	}
	if hasStateDeltaCapability(semantic) {
		return nil, errors.New("tool safety: state-delta tools are unsupported")
	}
	callable, ok := inner.(tool.CallableTool)
	if !ok {
		return nil, errors.New("tool safety: wrapped tool must be callable")
	}
	return &outputGuard{
		guard: guard, inner: inner, semantic: semantic,
		binding: binding, callable: callable,
	}, nil
}

func hasStateDeltaCapability(tl tool.Tool) bool {
	typeOfTool := reflect.TypeOf(tl)
	for _, method := range []string{"StateDelta", "StateDeltaForInvocation"} {
		if _, ok := typeOfTool.MethodByName(method); ok {
			return true
		}
	}
	return false
}

func validateOutputGuard(guard *Guard, inner tool.Tool, binding Binding) error {
	if err := validateExecutionGuard(guard); err != nil {
		return err
	}
	if isNilTool(inner) || inner.Declaration() == nil {
		return errors.New("tool safety: wrapped tool requires a declaration")
	}
	if err := validateBinding(binding); err != nil {
		return err
	}
	if inner.Declaration().Name != binding.ToolName {
		return errors.New("tool safety: binding name must match wrapped tool")
	}
	return nil
}

func isNilTool(tl tool.Tool) bool {
	return isNilInterface(tl)
}

// Declaration delegates to the model-visible wrapped tool.
func (wrapper *outputGuard) Declaration() *tool.Declaration {
	return wrapper.inner.Declaration()
}

// ToolMetadata delegates to the semantic wrapped tool.
func (wrapper *outputGuard) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(wrapper.semantic)
}

// CheckPermission preserves the semantic tool's own permission checks.
func (wrapper *outputGuard) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	checker, ok := wrapper.semantic.(tool.PermissionChecker)
	if !ok {
		return tool.AllowPermission(), nil
	}
	return checker.CheckPermission(ctx, req)
}

// LongRunning delegates the semantic tool's long-running preference.
func (wrapper *outputGuard) LongRunning() bool {
	runner, ok := wrapper.semantic.(interface{ LongRunning() bool })
	return ok && runner.LongRunning()
}

// Call delegates once, then withholds unsafe output without returning an error.
func (wrapper *outputGuard) Call(
	ctx context.Context,
	arguments []byte,
) (result any, err error) {
	parentCtx := normalizeContext(ctx)
	started := time.Now()
	defer wrapper.recoverPostcheck(parentCtx, started, &result, &err)
	runCtx, cancel := context.WithTimeout(parentCtx, wrapper.guard.policy.maxTimeout)
	defer cancel()
	result, err = wrapper.callable.Call(runCtx, arguments)
	violation, blocked := wrapper.callViolation(runCtx, parentCtx, result, err)
	if blocked {
		return wrapper.blockedResult(parentCtx, started, violation), nil
	}
	return result, err
}

func (wrapper *outputGuard) recoverPostcheck(
	ctx context.Context,
	started time.Time,
	result *any,
	err *error,
) {
	if recover() == nil {
		return
	}
	violation := outputViolation{
		ruleID: rulePostcheckFailed, riskLevel: RiskLevelHigh,
		decision:       DecisionDeny,
		evidence:       "output safety inspection stopped after an internal failure",
		recommendation: "review the output guard and configured auditor",
		redacted:       true,
	}
	*result = wrapper.blockedResult(ctx, started, violation)
	*err = nil
}

func (wrapper *outputGuard) callViolation(
	runCtx context.Context,
	parentCtx context.Context,
	result any,
	callErr error,
) (outputViolation, bool) {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && parentCtx.Err() == nil {
		return timeoutViolation(), true
	}
	if result != nil {
		if violation, blocked := wrapper.resultViolation(result); blocked {
			return violation, true
		}
	}
	if callErr != nil {
		return wrapper.errorViolation(callErr)
	}
	return outputViolation{}, false
}

func (wrapper *outputGuard) errorViolation(callErr error) (outputViolation, bool) {
	message := callErr.Error()
	if int64(len(message)) > wrapper.guard.policy.maxOutputBytes {
		violation := outputLimitViolation()
		violation.evidence = "tool error exceeds the configured byte limit"
		return violation, true
	}
	if hasSensitiveText(message) {
		return secretErrorViolation(), true
	}
	return outputViolation{}, false
}

func (wrapper *outputGuard) resultViolation(result any) (outputViolation, bool) {
	serialized, err := json.Marshal(result)
	if err != nil {
		return uninspectableViolation(), true
	}
	if int64(len(serialized)) > wrapper.guard.policy.maxOutputBytes {
		return outputLimitViolation(), true
	}
	if hasSensitiveText(string(serialized)) {
		return secretOutputViolation(), true
	}
	return outputViolation{}, false
}

func (wrapper *outputGuard) blockedResult(
	ctx context.Context,
	started time.Time,
	violation outputViolation,
) BlockedResult {
	report := wrapper.violationReport(started, violation)
	report, auditErr := wrapper.finalizePostcheckSafely(ctx, report)
	if auditErr != nil {
		report = auditFailureReport(report, true)
	}
	return blockedResultFromReport(report)
}

func (wrapper *outputGuard) finalizePostcheckSafely(
	ctx context.Context,
	report Report,
) (finalized Report, err error) {
	defer func() {
		if recover() != nil {
			finalized = auditFailureReport(report, true)
			err = errors.New("tool safety: auditor panicked")
		}
	}()
	return wrapper.guard.finalizeReport(ctx, report, auditPhasePostcheck)
}

func (wrapper *outputGuard) violationReport(
	started time.Time,
	violation outputViolation,
) Report {
	finding := newFinding(
		violation.ruleID, violation.riskLevel, violation.decision,
		violation.evidence, violation.recommendation,
	)
	return Report{
		Decision: finding.Decision, RiskLevel: finding.RiskLevel,
		RuleID: finding.RuleID, Evidence: finding.Evidence,
		Recommendation: finding.Recommendation,
		ToolName:       wrapper.binding.ToolName, Backend: wrapper.binding.Backend,
		Blocked:  true,
		Redacted: violation.redacted, DurationMS: time.Since(started).Milliseconds(),
		PolicyVersion: wrapper.guard.policy.versionString(), Findings: []Finding{finding},
	}
}

func blockedResultFromReport(report Report) BlockedResult {
	return BlockedResult{
		Decision: report.Decision, RiskLevel: report.RiskLevel,
		RuleID: report.RuleID, Recommendation: report.Recommendation,
		ToolName: report.ToolName, Backend: report.Backend,
		Blocked: true, Redacted: report.Redacted,
	}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func timeoutViolation() outputViolation {
	return outputViolation{
		ruleID: ruleExecutionTimeout, riskLevel: RiskLevelHigh,
		decision: DecisionDeny, evidence: "tool exceeded the configured runtime limit",
		recommendation: "reduce work or increase the bounded policy timeout",
	}
}

func uninspectableViolation() outputViolation {
	return outputViolation{
		ruleID: ruleOutputUninspectable, riskLevel: RiskLevelHigh,
		decision:       DecisionNeedsHumanReview,
		evidence:       "tool output could not be serialized for safety checks",
		recommendation: "return a JSON-serializable result and review the tool",
	}
}

func outputLimitViolation() outputViolation {
	return outputViolation{
		ruleID: ruleOutputLimit, riskLevel: RiskLevelHigh,
		decision:       DecisionDeny,
		evidence:       "tool output exceeds the configured byte limit",
		recommendation: "reduce output size or use a bounded result format",
	}
}

func secretOutputViolation() outputViolation {
	return outputViolation{
		ruleID: ruleOutputSecret, riskLevel: RiskLevelHigh,
		decision:       DecisionDeny,
		evidence:       "sensitive material detected in tool output",
		recommendation: "remove secrets and return only redacted output",
		redacted:       true,
	}
}

func secretErrorViolation() outputViolation {
	violation := secretOutputViolation()
	violation.evidence = "sensitive material detected in tool error"
	violation.recommendation = "remove secrets from tool errors and logs"
	return violation
}
