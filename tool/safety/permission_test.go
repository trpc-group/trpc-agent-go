//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicy_MapsNeedsHumanReviewToAsk(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "custom",
				Backend:        BackendUnknown,
				Decision:       DecisionNeedsHumanReview,
				RiskLevel:      RiskHigh,
				RuleID:         "unknown.requires_review",
				Recommendation: "review unknown tool",
				Blocked:        true,
			}, nil
		}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom",
		Arguments: []byte(`{"text":"download"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "decision=needs_human_review")
	require.Equal(t, DecisionNeedsHumanReview, observed.Decision)
}

func TestPermissionPolicy_AuditBestEffortKeepsDenyDecision(t *testing.T) {
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "command.dangerous_delete",
				Recommendation: "avoid deletion",
				Blocked:        true,
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /tmp/x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestPermissionPolicy_AuditStrictFailsAllowDecision(t *testing.T) {
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionAllow,
				RiskLevel:      RiskLow,
				Recommendation: "safe",
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureModeStrict),
	)
	_, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.ErrorContains(t, err, "disk full")
}

func TestPermissionPolicy_CodeExecInvalidArgumentsAsk(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		MustDefaultScanner(Policy{}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":"not-json"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Equal(t, DecisionAsk, observed.Decision)
	require.Equal(t, "tool.arguments_invalid", observed.RuleID)
}

func TestPermissionPolicy_AuditStrictKeepsBlockedDecision(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "command.dangerous_delete",
				Recommendation: "avoid deletion",
				Blocked:        true,
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureModeStrict),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /tmp/x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, "disk full", observed.AuditError)
}

func TestJSONLAuditWriter_WritesRedactedEvent(t *testing.T) {
	var buf bytes.Buffer
	writer := NewJSONLAuditWriter(&buf)
	err := writer.WriteAuditEvent(context.Background(), AuditEvent{
		ToolName:         "workspace_exec",
		Backend:          BackendWorkspace,
		Decision:         DecisionDeny,
		PermissionAction: "deny",
		RiskLevel:        RiskCritical,
		RuleID:           "path.secret_file",
		Blocked:          true,
		Redacted:         true,
	})
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"tool_name":"workspace_exec"`)
	require.Contains(t, buf.String(), `"redacted":true`)
}
