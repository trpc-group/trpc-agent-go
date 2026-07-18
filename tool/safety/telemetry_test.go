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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpanAttributesReturnsOnlyReservedFields(t *testing.T) {
	attributes := SpanAttributes(Report{
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelHigh,
		RuleID:    "NETWORK_DOMAIN_DENIED",
		Backend:   BackendHostExec,
		Command:   "api_key=must-not-appear",
	})
	require.Len(t, attributes, 4)
	got := make(map[string]string, len(attributes))
	for _, item := range attributes {
		got[string(item.Key)] = item.Value.AsString()
	}
	require.Equal(t, map[string]string{
		"tool.safety.decision":   "deny",
		"tool.safety.risk_level": "high",
		"tool.safety.rule_id":    "NETWORK_DOMAIN_DENIED",
		"tool.safety.backend":    "hostexec",
	}, got)
}
