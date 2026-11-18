//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	permissionAuditStage = "preflight"

	permissionAttrDecision = "tool.safety.decision"
	permissionAttrRisk     = "tool.safety.risk_level"
	permissionAttrRule     = "tool.safety.rule_id"
	permissionAttrBackend  = "tool.safety.backend"
	permissionAttrBlocked  = "tool.safety.blocked"
)

// PermissionOption configures a PermissionPolicy. A nil option passed to
// NewPermissionPolicy is ignored.
type PermissionOption func(*PermissionPolicy)

// PermissionPolicy adapts a Guard to the framework's pre-execution permission
// boundary. Its zero value fails closed because it has no guard. Use
// NewPermissionPolicy to obtain the default best-effort audit behavior.
// Concurrent checks are safe when the configured AuditSink is safe for
// concurrent use.
type PermissionPolicy struct {
	guard            *Guard
	auditSink        AuditSink
	auditFailureMode AuditFailureMode
}

var _ tool.PermissionPolicy = (*PermissionPolicy)(nil)

// NewPermissionPolicy returns a pre-execution permission policy for guard.
// A nil guard is accepted for configuration assembly but every check then
// fails closed. Nil options are ignored. Audit writes are best effort by
// default. AuditRequired without a non-nil sink fails every check closed.
func NewPermissionPolicy(
	guard *Guard,
	opts ...PermissionOption,
) *PermissionPolicy {
	policy := &PermissionPolicy{
		guard:            guard,
		auditFailureMode: AuditBestEffort,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(policy)
		}
	}
	return policy
}

// WithAuditSink configures the sink that receives one secret-minimizing
// preflight event per check. A nil sink disables best-effort audit writes and
// fails checks configured with AuditRequired.
func WithAuditSink(sink AuditSink) PermissionOption {
	return func(policy *PermissionPolicy) {
		if policy != nil {
			policy.auditSink = sink
		}
	}
}

// WithAuditFailureMode configures how sink failures affect permission checks.
// AuditBestEffort preserves the scan decision. AuditRequired returns the sink
// error and prevents an allowed execution; already intercepted decisions stay
// intercepted. AuditRequired also requires a non-nil sink. An unsupported mode
// fails closed when the policy is checked.
func WithAuditFailureMode(mode AuditFailureMode) PermissionOption {
	return func(policy *PermissionPolicy) {
		if policy != nil {
			policy.auditFailureMode = mode
		}
	}
}

// CheckToolPermission decodes and scans the finalized framework request once,
// records at most one preflight audit event, and maps the result to the
// framework permission actions. A nil receiver, nil guard, nil request,
// malformed request, cancelled context, unsupported decision, or invalid
// audit configuration returns a deny decision and a lowercase error. A nil
// context is treated as context.Background.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil {
		return failClosedPermission(ctx, nil, "safety.policy_nil", errors.New("tool safety permission policy is nil"))
	}
	if err := ctx.Err(); err != nil {
		report := failClosedReport(req, "safety.context_cancelled")
		return p.completePermission(ctx, report, err)
	}
	if p.guard == nil {
		report := failClosedReport(req, "safety.guard_nil")
		return p.completePermission(ctx, report, errors.New("tool safety guard is nil"))
	}
	if req == nil {
		report := failClosedReport(nil, "safety.request_nil")
		return p.completePermission(ctx, report, errors.New("tool safety permission request is nil"))
	}
	if p.auditFailureMode == AuditRequired && p.auditSink == nil {
		report := failClosedReport(req, "safety.audit_required")
		return p.completePermission(ctx, report,
			errors.New("required tool safety audit sink is nil"))
	}

	decoded, scan, err := requestFromPermissionRequest(req)
	if err != nil {
		report := failClosedReport(req, "safety.decode_error")
		return p.completePermission(ctx, report, errors.New("tool safety permission request could not be decoded"))
	}

	var report Report
	if scan {
		report = scanDecodedPermissionRequest(p.guard, decoded)
	} else {
		report = nonExecutionReport(req)
	}
	if err := validatePermissionReport(report); err != nil {
		report = replaceWithFailure(report, "safety.invalid_decision")
		return p.completePermission(ctx, report, err)
	}
	if !validAuditFailureMode(p.auditFailureMode) {
		report = replaceWithFailure(report, "safety.audit_configuration")
		return p.completePermission(ctx, report, fmt.Errorf(
			"tool safety audit failure mode is invalid: %q", p.auditFailureMode,
		))
	}
	return p.completePermission(ctx, report, nil)
}

func (p *PermissionPolicy) completePermission(
	ctx context.Context,
	report Report,
	checkErr error,
) (tool.PermissionDecision, error) {
	decision := permissionDecision(report)
	if p.auditSink != nil {
		auditErr := p.auditSink.Record(ctx, auditEventFromReport(report))
		if auditErr != nil && p.auditFailureMode == AuditRequired {
			if decision.Action == tool.PermissionActionAllow {
				report = replaceWithFailure(report, "safety.audit_required")
				decision = permissionDecision(report)
			}
			setPermissionSpan(ctx, report)
			return decision, errors.Join(
				checkErr,
				fmt.Errorf("record required tool safety audit: %w", auditErr),
			)
		}
	}
	setPermissionSpan(ctx, report)
	return decision, checkErr
}

func failClosedPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
	ruleID string,
	err error,
) (tool.PermissionDecision, error) {
	report := failClosedReport(req, ruleID)
	setPermissionSpan(ctx, report)
	return permissionDecision(report), err
}

func failClosedReport(req *tool.PermissionRequest, ruleID string) Report {
	request := Request{Backend: BackendUnknown}
	if req != nil {
		request.ToolName = permissionToolName(req)
	}
	return replaceWithFailure(newReport(request), ruleID)
}

func nonExecutionReport(req *tool.PermissionRequest) Report {
	report := newReport(Request{
		ToolName: permissionToolName(req),
		Backend:  BackendUnknown,
	})
	report.RuleID = "safety.no_execution"
	report.Evidence = []string{"request has no executable input"}
	report.Recommendation = "request is permitted"
	report.SafeSummary = "non-execution request is permitted"
	return report
}

func replaceWithFailure(report Report, ruleID string) Report {
	report.Decision = DecisionDeny
	report.RiskLevel = RiskCritical
	report.RuleID = ruleID
	report.Evidence = []string{"permission policy failed closed"}
	report.Recommendation = "correct the safety policy configuration before execution"
	report.Blocked = true
	report.SafeSummary = "request was blocked by the safety policy"
	return report
}

func validatePermissionReport(report Report) error {
	if !validDecision(report.Decision) {
		return fmt.Errorf("tool safety returned unsupported decision %q", report.Decision)
	}
	for _, finding := range report.Findings {
		if !validDecision(finding.Decision) {
			return fmt.Errorf("tool safety returned unsupported decision %q", finding.Decision)
		}
	}
	return nil
}

func permissionDecision(report Report) tool.PermissionDecision {
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionDeny:
		return tool.DenyPermission(permissionReason("denied", report))
	case DecisionAsk:
		return tool.AskPermission(permissionReason("approval required", report))
	case DecisionNeedsHumanReview:
		return tool.AskPermission(permissionReason("human review required", report))
	default:
		return tool.DenyPermission("tool safety denied execution (safety.invalid_decision)")
	}
}

func permissionReason(action string, report Report) string {
	return fmt.Sprintf("tool safety %s (%s): %s", action, report.RuleID, report.Recommendation)
}

func validAuditFailureMode(mode AuditFailureMode) bool {
	return mode == AuditBestEffort || mode == AuditRequired
}

func auditEventFromReport(report Report) AuditEvent {
	return AuditEvent{
		SchemaVersion:  report.SchemaVersion,
		Timestamp:      time.Now().UTC(),
		ScanID:         report.ScanID,
		Stage:          permissionAuditStage,
		ToolName:       report.ToolName,
		Backend:        report.Backend,
		Decision:       report.Decision,
		RiskLevel:      report.RiskLevel,
		RuleID:         report.RuleID,
		DurationMillis: report.DurationMillis,
		Redacted:       report.Redacted,
		Intercepted:    report.Decision != DecisionAllow,
	}
}

func setPermissionSpan(ctx context.Context, report Report) {
	oteltrace.SpanFromContext(ctx).SetAttributes(
		attribute.String(permissionAttrDecision, string(report.Decision)),
		attribute.String(permissionAttrRisk, string(report.RiskLevel)),
		attribute.String(permissionAttrRule, report.RuleID),
		attribute.String(permissionAttrBackend, string(report.Backend)),
		attribute.Bool(permissionAttrBlocked, report.Decision != DecisionAllow),
	)
}
