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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTelemetry_ProjectsAttributesOnActiveSpan(t *testing.T) {
	ctx, span := newRecordingSpan()
	report := ScanReport{
		Decision:    DecisionDeny,
		RiskLevel:   RiskCritical,
		Backend:     BackendWorkspaceExec,
		Findings:    []Finding{{RuleID: "command.dangerous_delete", RiskLevel: RiskCritical}},
		Intercepted: true,
		Redacted:    false,
	}
	telemetryProject(ctx, safetyAttributes(report))
	attrs := span.attributes()
	require.Equal(t, string(DecisionDeny), attrs[KeyToolSafetyDecision])
	require.Equal(t, string(RiskCritical), attrs[KeyToolSafetyRiskLevel])
	require.Equal(t, string(BackendWorkspaceExec), attrs[KeyToolSafetyBackend])
	require.Equal(t, true, attrs[KeyToolSafetyIntercepted])
	require.Equal(t, false, attrs[KeyToolSafetyRedacted])
	ruleIDs, ok := attrs[KeyToolSafetyRuleIDs].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"command.dangerous_delete"}, ruleIDs)
}

func TestTelemetry_NoopWhenNoSpan(t *testing.T) {
	require.NotPanics(t, func() {
		telemetryProject(context.Background(), []SpanAttribute{
			{Key: KeyToolSafetyDecision, Value: "deny"},
		})
	})
}

func TestTelemetry_ConstantsMatchSemconv(t *testing.T) {
	// The guard's key constants must match the canonical semconv keys
	// so consumers of either package see the same attribute names.
	require.Equal(t, "tool.safety.decision", KeyToolSafetyDecision)
	require.Equal(t, "tool.safety.risk_level", KeyToolSafetyRiskLevel)
	require.Equal(t, "tool.safety.rule_id", KeyToolSafetyRuleID)
	require.Equal(t, "tool.safety.rule_ids", KeyToolSafetyRuleIDs)
	require.Equal(t, "tool.safety.backend", KeyToolSafetyBackend)
	require.Equal(t, "tool.safety.intercepted", KeyToolSafetyIntercepted)
	require.Equal(t, "tool.safety.redacted", KeyToolSafetyRedacted)
}
