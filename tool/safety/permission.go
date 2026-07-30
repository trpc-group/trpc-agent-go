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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionPolicy adapts Scanner to tool.PermissionPolicy.
type PermissionPolicy struct {
	scanner *Scanner
}

// NewPermissionPolicy creates a tool permission policy backed by Scanner.
func NewPermissionPolicy(scanner *Scanner) *PermissionPolicy {
	if scanner == nil {
		scanner = NewScanner(DefaultPolicy())
	}
	return &PermissionPolicy{scanner: scanner}
}

// CheckToolPermission scans a pending tool call before execution.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if req == nil {
		report, err := p.scanner.scanArgumentFailure(ctx, "")
		if err != nil {
			return tool.DenyPermission(
				"safety scan failed: " + err.Error(),
			), nil
		}
		return tool.DenyPermission(reportReason(report)), nil
	}
	scanReq, err := ScanRequestFromArgs(req.ToolName, req.Arguments)
	if err != nil {
		report, auditErr := p.scanner.scanArgumentFailure(
			ctx,
			req.ToolName,
		)
		if auditErr != nil {
			return tool.DenyPermission(
				"safety scan failed: " + auditErr.Error(),
			), nil
		}
		return tool.DenyPermission(reportReason(report)), nil
	}
	scanReq.ToolCallID = req.ToolCallID
	scanReq.Metadata = req.Metadata
	report, err := p.scanner.Scan(ctx, scanReq)
	if err != nil {
		return tool.DenyPermission("safety scan failed: " + err.Error()), nil
	}
	reason := reportReason(report)
	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(reason), nil
	case DecisionAsk:
		return tool.AskPermission(reason), nil
	default:
		return tool.AllowPermission(), nil
	}
}

func reportReason(report ScanReport) string {
	return fmt.Sprintf(
		"safety guard %s (%s): %s",
		report.RuleID,
		report.RiskLevel,
		report.Recommendation,
	)
}

var _ tool.PermissionPolicy = (*PermissionPolicy)(nil)
