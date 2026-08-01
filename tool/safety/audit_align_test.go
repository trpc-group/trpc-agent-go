//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestAuditEvent_AlignsWithOTelSuffixes(t *testing.T) {
	t.Parallel()
	mem := safety.NewMemoryAuditor()
	g := safety.NewGuard(safety.WithAuditor(mem))
	_, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-audit-1",
		Arguments:  []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	evs := mem.Events()
	require.NotEmpty(t, evs)
	ev := evs[len(evs)-1]
	require.Equal(t, "call-audit-1", ev.ToolCallID)
	require.Equal(t, safety.DecisionDeny, ev.Decision)
	require.True(t, ev.Blocked)
	require.NotEmpty(t, ev.RuleID)
	require.Equal(t, safety.BackendWorkspace, ev.Backend)

	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	// JSON keys must match OTel attribute suffixes after "tool.safety.".
	for _, key := range []string{
		`"decision"`, `"risk_level"`, `"rule_id"`, `"backend"`, `"blocked"`, `"tool_call_id"`,
	} {
		require.Contains(t, string(raw), key)
	}
	require.True(t, strings.HasSuffix(safety.AttrDecision, "decision"))
	require.True(t, strings.HasSuffix(safety.AttrRiskLevel, "risk_level"))
	require.True(t, strings.HasSuffix(safety.AttrRuleID, "rule_id"))
	require.True(t, strings.HasSuffix(safety.AttrBackend, "backend"))
	require.True(t, strings.HasSuffix(safety.AttrBlocked, "blocked"))
	require.True(t, strings.HasSuffix(safety.AttrToolCallID, "tool_call_id"))
}
