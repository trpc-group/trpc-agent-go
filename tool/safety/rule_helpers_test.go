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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestDenyToolNames_ExtraRule(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard(safety.WithExtraRules(safety.DenyToolNames("web_search")))
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "web_search",
		Arguments: []byte(`{"q":"weather"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.Contains(t, dec.Reason, "deny_tool_name")
}

func TestAskToolNames_ExtraRule(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard(safety.WithExtraRules(safety.AskToolNames("read_file")))
	raw, err := json.Marshal(map[string]any{"path": "/tmp/readme.txt"})
	require.NoError(t, err)
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "read_file",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, dec.Action)
	require.Contains(t, dec.Reason, "ask_tool_name")
}

func TestDenyCommandSubstrings_ExtraRule(t *testing.T) {
	t.Parallel()
	g := safety.NewGuard(safety.WithExtraRules(
		safety.DenyCommandSubstrings("terraform apply"),
	))
	raw, err := json.Marshal(map[string]any{"command": "terraform apply -auto-approve"})
	require.NoError(t, err)
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.Contains(t, dec.Reason, "deny_command_substring")
}

func TestCompose_FirstNonAllowWins(t *testing.T) {
	t.Parallel()
	// Guard would allow echo; site rule denies a substring.
	guard := safety.NewGuard(safety.WithExtraRules(
		safety.DenyCommandSubstrings("echo secret"),
	))
	policy := safety.Compose(guard)
	raw, err := json.Marshal(map[string]any{"command": "echo secret"})
	require.NoError(t, err)
	dec, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: raw,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
}
