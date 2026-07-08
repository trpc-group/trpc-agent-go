// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionPolicy adapts Scanner to tool.PermissionPolicy.
type PermissionPolicy struct {
	scanner    *Scanner
	audit      AuditWriter
	failClosed bool
}

// PermissionOption configures a PermissionPolicy.
type PermissionOption func(*PermissionPolicy)

// WithAuditWriter records an event for each scan.
func WithAuditWriter(w AuditWriter) PermissionOption {
	return func(p *PermissionPolicy) { p.audit = w }
}

// WithAuditFailClosed configures audit write failures.
func WithAuditFailClosed(v bool) PermissionOption {
	return func(p *PermissionPolicy) { p.failClosed = v }
}

// NewPermissionPolicy creates a tool permission policy.
func NewPermissionPolicy(scanner *Scanner, opts ...PermissionOption) *PermissionPolicy {
	if scanner == nil {
		scanner = MustScanner(DefaultPolicy())
	}
	p := &PermissionPolicy{scanner: scanner, failClosed: true}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	execReq := RequestFromPermission(req)
	report, err := p.scanner.Scan(ctx, execReq)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	if p.audit != nil {
		if err := p.audit.WriteAuditEvent(ctx, auditEventFromReport(time.Now(), report)); err != nil {
			if p.failClosed || report.Blocked {
				return tool.PermissionDecision{}, err
			}
		}
	}
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission(), nil
	case DecisionDeny:
		return tool.DenyPermission(permissionReason(report)), nil
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(permissionReason(report)), nil
	default:
		return tool.AskPermission(permissionReason(report)), nil
	}
}

func permissionReason(report Report) string {
	return fmt.Sprintf(
		"tool safety %s: risk=%s rule=%s backend=%s recommendation=%s",
		report.Decision,
		report.RiskLevel,
		primaryRuleID(report.RuleIDs),
		report.Backend,
		report.Recommendation,
	)
}

var _ tool.PermissionPolicy = (*PermissionPolicy)(nil)
