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

func TestEnumValidHelpers(t *testing.T) {
	require.True(t, DecisionAllow.Valid())
	require.False(t, Decision("block").Valid())
	require.True(t, RiskCritical.Valid())
	require.False(t, RiskLevel("urgent").Valid())
	require.True(t, BackendSandbox.Valid())
	require.False(t, Backend("vm").Valid())
}

func TestReportToolSafetyAttributes(t *testing.T) {
	attrs := Report{
		Decision:  DecisionDeny,
		RiskLevel: RiskCritical,
		RuleID:    "command.dangerous_delete",
		Backend:   BackendHost,
		Blocked:   true,
		Redacted:  true,
	}.ToolSafetyAttributes()
	require.Len(t, attrs, 6)
	got := map[string]string{}
	for _, attr := range attrs {
		switch string(attr.Key) {
		case "tool.safety.blocked", "tool.safety.redacted":
			continue
		}
		got[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, "deny", got["tool.safety.decision"])
	require.Equal(t, "critical", got["tool.safety.risk_level"])
	require.Equal(t, "command.dangerous_delete", got["tool.safety.rule_id"])
	require.Equal(t, "host", got["tool.safety.backend"])
	for _, attr := range attrs {
		switch string(attr.Key) {
		case "tool.safety.blocked":
			require.True(t, attr.Value.AsBool())
		case "tool.safety.redacted":
			require.True(t, attr.Value.AsBool())
		}
	}
}

func TestScannerFunc(t *testing.T) {
	report, err := ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
		return Report{Decision: DecisionAllow}, nil
	}).Scan(context.Background(), ScanRequest{})
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
}
