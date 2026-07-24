//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// BackendResolver resolves the safety backend for a permission request.
type BackendResolver func(*tool.PermissionRequest) Backend

// PermissionPolicyOption configures the safety permission policy.
type PermissionPolicyOption func(*permissionPolicy)

type permissionPolicy struct {
	scanner          Scanner
	audit            AuditWriter
	auditDeniedPaths []string
	resolver         BackendResolver
	observer         func(context.Context, Report)
	auditMode        AuditFailureMode
	defaultBackend   Backend
}

// NewPermissionPolicy adapts a safety scanner to tool.PermissionPolicy.
func NewPermissionPolicy(
	scanner Scanner,
	opts ...PermissionPolicyOption,
) tool.PermissionPolicy {
	if scanner == nil {
		scanner = MustDefaultScanner(Policy{})
	}
	auditMode := AuditFailureModeBestEffort
	if defaultScanner, ok := scanner.(*DefaultScanner); ok {
		auditMode = defaultScanner.policy.AuditFailureMode
	}
	p := &permissionPolicy{
		scanner:          scanner,
		auditDeniedPaths: auditDeniedPathsForScanner(scanner),
		resolver:         defaultBackendResolver,
		auditMode:        auditMode,
		defaultBackend:   BackendUnknown,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// WithAuditWriter sets the audit writer.
func WithAuditWriter(w AuditWriter) PermissionPolicyOption {
	return func(p *permissionPolicy) {
		p.audit = w
	}
}

// WithBackendResolver sets a custom backend resolver.
func WithBackendResolver(r BackendResolver) PermissionPolicyOption {
	return func(p *permissionPolicy) {
		if r != nil {
			p.resolver = r
		}
	}
}

// WithReportObserver observes each report after scanning and audit attempts.
func WithReportObserver(fn func(context.Context, Report)) PermissionPolicyOption {
	return func(p *permissionPolicy) {
		p.observer = fn
	}
}

// WithAuditFailureMode sets the audit failure mode.
func WithAuditFailureMode(mode AuditFailureMode) PermissionPolicyOption {
	return func(p *permissionPolicy) {
		if mode == AuditFailureModeBestEffort || mode == AuditFailureModeStrict {
			p.auditMode = mode
		}
	}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *permissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p == nil || p.scanner == nil || req == nil {
		return tool.AllowPermission(), nil
	}
	metadata := metadataMap(req.Metadata)
	backend := p.defaultBackend
	if p.resolver != nil {
		backend = p.resolver(req)
	}
	scanReqs, err := requestsFromToolCall(
		req.ToolName,
		req.ToolCallID,
		backend,
		req.Arguments,
		metadata,
	)
	if err != nil {
		decision := DecisionDeny
		risk := RiskHigh
		recommendation := "fix tool arguments before execution"
		if req.ToolName == "execute_code" {
			decision = DecisionAsk
			risk = RiskMedium
			recommendation = "review malformed code execution arguments before retrying"
		}
		report := Report{
			ToolName:       req.ToolName,
			ToolCallID:     req.ToolCallID,
			Backend:        backend,
			Decision:       decision,
			RiskLevel:      risk,
			RuleID:         "tool.arguments_invalid",
			Evidence:       err.Error(),
			Recommendation: recommendation,
			Blocked:        true,
		}
		return p.finish(ctx, report)
	}
	var final Report
	for i, scanReq := range scanReqs {
		report, err := p.scanner.Scan(ctx, scanReq)
		if err != nil {
			failure := Report{
				ToolName:       scanReq.ToolName,
				ToolCallID:     scanReq.ToolCallID,
				Backend:        scanReq.Backend,
				Command:        scanReq.Command,
				Decision:       DecisionDeny,
				RiskLevel:      RiskHigh,
				RuleID:         "scanner.error",
				Evidence:       err.Error(),
				Recommendation: "fix scanner errors before tool execution",
				Blocked:        true,
			}
			if i == 0 || reportRank(failure) > reportRank(final) {
				final = failure
			}
			return p.finish(ctx, final)
		}
		if !report.Decision.Valid() {
			failure := invalidScannerDecisionReport(scanReq, report)
			if i > 0 && reportRank(final) > reportRank(failure) {
				return p.finish(ctx, final)
			}
			return p.finish(ctx, failure)
		}
		if i == 0 || reportRank(report) > reportRank(final) {
			final = report
		}
	}
	return p.finish(ctx, final)
}

func (p *permissionPolicy) finish(
	ctx context.Context,
	report Report,
) (tool.PermissionDecision, error) {
	report.Blocked = report.Decision != DecisionAllow
	auditErr := p.writeAudit(ctx, report)
	if auditErr != nil {
		report.AuditError = auditErr.Error()
	}
	if p.observer != nil {
		p.observer(ctx, report)
	}
	if auditErr != nil && p.auditMode == AuditFailureModeStrict &&
		report.Decision == DecisionAllow {
		return tool.PermissionDecision{}, auditErr
	}
	return permissionDecisionForReport(report, p.auditDeniedPaths), nil
}

func (p *permissionPolicy) writeAudit(
	ctx context.Context,
	report Report,
) error {
	if p.audit == nil {
		return nil
	}
	return p.audit.WriteAuditEvent(
		ctx,
		auditEventFromReport(report, p.auditDeniedPaths),
	)
}

func auditDeniedPathsForScanner(scanner Scanner) []string {
	if defaultScanner, ok := scanner.(*DefaultScanner); ok {
		return append([]string(nil), defaultScanner.policy.DeniedPaths...)
	}
	return append([]string(nil), DefaultPolicy().DeniedPaths...)
}

func invalidScannerDecisionReport(scanReq ScanRequest, report Report) Report {
	toolName := report.ToolName
	if toolName == "" {
		toolName = scanReq.ToolName
	}
	toolCallID := report.ToolCallID
	if toolCallID == "" {
		toolCallID = scanReq.ToolCallID
	}
	backend := report.Backend
	if !backend.Valid() {
		backend = scanReq.Backend
	}
	command := report.Command
	if command == "" {
		command = scanReq.Command
	}
	return Report{
		ToolName:       toolName,
		ToolCallID:     toolCallID,
		Backend:        backend,
		Command:        command,
		Decision:       DecisionDeny,
		RiskLevel:      RiskHigh,
		RuleID:         "scanner.invalid_decision",
		Evidence:       fmt.Sprintf("scanner returned invalid decision %q", report.Decision),
		Recommendation: "fix scanner decision before tool execution",
		Blocked:        true,
	}
}

func permissionDecisionForReport(
	report Report,
	deniedPaths []string,
) tool.PermissionDecision {
	reason := permissionReasonForDeniedPaths(report, deniedPaths)
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionDeny:
		return tool.DenyPermission(reason)
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(reason)
	default:
		return tool.DenyPermission(reason)
	}
}

// PermissionReason renders a short, redacted reason for non-allow decisions.
func PermissionReason(report Report) string {
	return permissionReasonForDeniedPaths(report, DefaultPolicy().DeniedPaths)
}

func permissionReasonForDeniedPaths(report Report, deniedPaths []string) string {
	if report.RuleID == "" && report.Decision == DecisionAllow {
		return ""
	}
	recommendation, _ := redactAuditRecommendation(report.Recommendation, deniedPaths)
	return fmt.Sprintf(
		"tool_safety: decision=%s; risk=%s; rule=%s; backend=%s; recommendation=%s",
		report.Decision,
		report.RiskLevel,
		report.RuleID,
		report.Backend,
		recommendation,
	)
}

func defaultBackendResolver(req *tool.PermissionRequest) Backend {
	if req == nil {
		return BackendUnknown
	}
	return inferBackend(normalizeToolName(req.ToolName))
}

func permissionAction(decision Decision) string {
	switch decision {
	case DecisionDeny:
		return string(tool.PermissionActionDeny)
	case DecisionAsk, DecisionNeedsHumanReview:
		return string(tool.PermissionActionAsk)
	default:
		return string(tool.PermissionActionAllow)
	}
}

func metadataMap(metadata tool.ToolMetadata) map[string]any {
	return map[string]any{
		"read_only":        metadata.ReadOnly,
		"destructive":      metadata.Destructive,
		"concurrency_safe": metadata.ConcurrencySafe,
		"search_or_read":   metadata.SearchOrRead,
		"open_world":       metadata.OpenWorld,
		"max_result_size":  metadata.MaxResultSize,
	}
}

func singleLine(s string) string {
	out := strings.NewReplacer("\n", " ", "\r", " ", ";", ",").Replace(s)
	out, _ = redactString(out)
	return out
}
