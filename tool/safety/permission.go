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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NewPermissionPolicy creates a run-level gate for explicitly bound tools.
func NewPermissionPolicy(
	guard *Guard,
	bindings ...Binding,
) (tool.PermissionPolicy, error) {
	if err := validateExecutionGuard(guard); err != nil {
		return nil, err
	}
	validated, err := validateBindings(bindings)
	if err != nil {
		return nil, err
	}
	if len(validated) == 0 {
		return nil, errors.New("tool safety: permission policy requires bindings")
	}
	return &permissionPolicy{
		guard:    guard,
		bindings: validated,
	}, nil
}

type permissionPolicy struct {
	guard    *Guard
	bindings map[string]Binding
}

func (policy *permissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if req == nil {
		return tool.DenyPermission("tool safety: permission request is nil"), nil
	}
	binding, ok := policy.bindings[req.ToolName]
	if !ok {
		return tool.AllowPermission(), nil
	}
	report, err := policy.guard.scanRequest(ctx, AdaptRequest{
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Arguments:  append([]byte(nil), req.Arguments...),
		Metadata:   req.Metadata,
	}, binding)
	decision := permissionDecision(report)
	if err != nil {
		return decision, err
	}
	return decision, nil
}

func validateExecutionGuard(guard *Guard) error {
	if guard == nil {
		return errors.New("tool safety: nil guard")
	}
	if isNilAuditor(guard.auditor) {
		return errors.New("tool safety: execution guard requires an auditor")
	}
	return nil
}

func permissionDecision(report Report) tool.PermissionDecision {
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionDeny:
		return tool.DenyPermission(reportReason(report))
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(reportReason(report))
	default:
		return tool.DenyPermission("SAFETY_SCAN_FAILED: invalid safety decision")
	}
}

func reportReason(report Report) string {
	reason := fmt.Sprintf(
		"%s: %s; %s",
		report.RuleID,
		report.Evidence,
		report.Recommendation,
	)
	reason, _ = redactText(reason)
	return bounded(reason, maxEvidenceBytes)
}

func (guard *Guard) scanRequest(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (Report, error) {
	input, err := adaptSafely(ctx, req, binding)
	if err != nil {
		return guard.scanUnparsableRequest(ctx, req, binding)
	}
	report, err := guard.scan(ctx, input)
	if err != nil {
		return report, err
	}
	return guard.finalizeReport(ctx, report, auditPhasePrecheck)
}

func adaptSafely(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (input ScanInput, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("tool safety: input adapter failed")
		}
	}()
	return binding.Adapter.Adapt(ctx, req, binding)
}

func (guard *Guard) scanUnparsableRequest(
	ctx context.Context,
	req AdaptRequest,
	binding Binding,
) (Report, error) {
	input := ScanInput{
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Kind:       binding.Kind,
		Backend:    binding.Backend,
		Operation:  operationForKind(binding.Kind),
		Script:     "<unparsable-arguments>",
		Language:   "arguments",
		Metadata:   req.Metadata,
	}
	findings := []Finding{newFinding(
		"TOOL_INPUT_UNPARSABLE",
		RiskLevelHigh,
		DecisionNeedsHumanReview,
		"tool arguments could not be normalized",
		"review the arguments and execution binding",
	)}
	findings = append(findings, guard.definiteRawFindings(ctx, req, input)...)
	report := buildReport(guard.policy, input, scanOutcome{findings: findings})
	return guard.finalizeReport(ctx, report, auditPhasePrecheck)
}

func operationForKind(kind ExecutionKind) Operation {
	switch kind {
	case ExecutionKindCodeExec:
		return OperationCodeExecute
	case ExecutionKindWorkspaceSession, ExecutionKindHostSession:
		return OperationSessionInput
	default:
		return OperationExecute
	}
}

func (guard *Guard) definiteRawFindings(
	ctx context.Context,
	req AdaptRequest,
	base ScanInput,
) []Finding {
	findings := filterDefiniteFindings(rawCommandFindings(
		string(req.Arguments),
		"arguments",
	))
	raw := base
	raw.Command = string(req.Arguments)
	raw.Script = ""
	report, err := guard.scan(ctx, raw)
	if err != nil {
		return findings
	}
	return appendUniqueFindings(findings, filterDefiniteFindings(report.Findings))
}

func filterDefiniteFindings(candidates []Finding) []Finding {
	findings := make([]Finding, 0, len(candidates))
	for _, finding := range candidates {
		if definiteRawRule(finding.RuleID) {
			findings = append(findings, finding)
		}
	}
	return findings
}

func appendUniqueFindings(existing, candidates []Finding) []Finding {
	seen := make(map[string]struct{}, len(existing))
	for _, finding := range existing {
		seen[finding.RuleID] = struct{}{}
	}
	for _, finding := range candidates {
		if _, ok := seen[finding.RuleID]; ok {
			continue
		}
		existing = append(existing, finding)
		seen[finding.RuleID] = struct{}{}
	}
	return existing
}

func definiteRawRule(ruleID string) bool {
	switch ruleID {
	case "CMD_DANGEROUS_DELETE", "CMD_SYSTEM_OVERWRITE",
		"CMD_PRIVILEGE_ESCALATION", "PATH_SSH_CREDENTIAL",
		"PATH_ENV_FILE", "PATH_CREDENTIAL_FILE",
		"NETWORK_DOMAIN_DENIED", "NETWORK_IP_LITERAL",
		"RESOURCE_FORK_BOMB", "SECRET_PRIVATE_KEY",
		"SECRET_CLOUD_CREDENTIAL", "SECRET_TOKEN",
		"SECRET_PASSWORD", "SECRET_API_KEY":
		return true
	default:
		return false
	}
}
