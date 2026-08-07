//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Decision is the normalized permission action produced by the Guard. It
// mirrors tool.PermissionAction so callers can map directly.
type Decision string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny skips execution and returns a denial to the model.
	DecisionDeny Decision = "deny"
	// DecisionAsk skips execution and asks a human to approve.
	DecisionAsk Decision = "ask"
)

// toPermissionDecision maps the Guard's Decision to the framework's
// tool.PermissionDecision, attaching the aggregated reason.
func toPermissionDecision(decision Decision, report ScanReport) tool.PermissionDecision {
	switch decision {
	case DecisionDeny:
		return tool.DenyPermission(reportReason(report))
	case DecisionAsk:
		return tool.AskPermission(reportReason(report))
	default:
		return tool.AllowPermission()
	}
}

// reportReason builds a single human-readable reason from the findings that
// drove a non-allow decision. It is the text returned to the model so the
// model can correct the call on the next turn.
func reportReason(report ScanReport) string {
	if len(report.Findings) == 0 {
		return "tool call blocked by safety policy"
	}
	// Findings are already sorted by descending risk; the leading one is
	// the most actionable explanation for the model.
	top := report.Findings[0]
	reason := "tool call blocked by safety policy: " + top.Detail
	if len(report.Findings) > 1 {
		reason += " (plus additional findings)"
	}
	return reason
}

// Guard is a tool.PermissionPolicy that statically scans tool calls before
// execution and returns allow / deny / ask based on a SafetyPolicy.
//
// A zero-value Guard (or a Guard built from a zero SafetyPolicy) allows
// every call, preserving backward compatibility. Construct a Guard with
// NewGuard to enable scanning, audit and telemetry.
//
// The Guard is safe for concurrent use: CheckToolPermission may be called
// from many goroutines. The configured AuditWriter must also be safe for
// concurrent use (AuditWriter is).
type Guard struct {
	policy SafetyPolicy
	audit  *AuditWriter
	now    func() time.Time
	mu     sync.Mutex
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithAuditWriter attaches a JSON-lines audit sink. Every non-no-op scan
// emits one AuditEvent. nil (the default) disables auditing.
func WithAuditWriter(w *AuditWriter) GuardOption {
	return func(g *Guard) {
		g.audit = w
	}
}

// WithClock injects the clock used to stamp scan reports. Defaults to
// time.Now. Intended for tests.
func WithClock(now func() time.Time) GuardOption {
	return func(g *Guard) {
		if now != nil {
			g.now = now
		}
	}
}

// NewGuard returns a Guard that enforces policy. The policy is normalized
// with defaults via SafetyPolicy.withDefaults before use.
func NewGuard(policy SafetyPolicy, opts ...GuardOption) *Guard {
	g := &Guard{
		policy: policy.withDefaults(),
		now:    time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// Policy returns the active policy (with defaults applied).
func (g *Guard) Policy() SafetyPolicy { return g.policy }

// CheckToolPermission implements tool.PermissionPolicy. It scans the tool
// call, records the audit event and OpenTelemetry span attributes, and
// returns the permission decision. It never returns an error: a scan
// failure is recorded as a parse_error finding and routed through the
// configured OnParseError action, so the runner still receives a
// well-formed decision.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if req == nil {
		return tool.AllowPermission(), nil
	}
	sc := g.extractScanContext(req.ToolName, req.Arguments)
	report := g.scan(sc)

	g.emitAudit(report)
	recordSpan(ctx, report)

	return toPermissionDecision(Decision(report.Decision), report), nil
}

// Scan is the non-mutating entry point used by hosts that want the
// structured report without routing through PermissionPolicy. It does not
// write an audit event or span attributes; callers that want auditing
// should use CheckToolPermission.
func (g *Guard) Scan(ctx context.Context, toolName string, args []byte) ScanReport {
	_ = ctx
	sc := g.extractScanContext(toolName, args)
	return g.scan(sc)
}

// emitAudit writes one AuditEvent to the configured sink. Failures are
// swallowed: an audit-write failure must never change the permission
// decision. The write is serialized by the AuditWriter's own mutex.
func (g *Guard) emitAudit(report ScanReport) {
	if g.audit == nil || !g.policy.Active() {
		return
	}
	_ = g.audit.Write(AuditEvent{
		Timestamp:     report.Timestamp,
		ToolName:      report.ToolName,
		ToolCallID:    report.ToolCallID,
		Decision:      report.Decision,
		RiskLevel:     string(report.RiskLevel),
		Command:       report.Command,
		HostExec:      report.HostExec,
		PolicyVersion: report.PolicyVersion,
		Findings:      report.Findings,
	})
}
