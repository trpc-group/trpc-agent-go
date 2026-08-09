//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestExecCommandTool_DeclaresHostExecBackend is the regression test for
// the routing bug where the model-visible exec_command tool fell through
// to the WorkspaceExec rule set because its name lacks the substring
// "host".  The fix is for the tool to implement safety.BackendProvider
// and return safety.BackendHostExec; inferBackend then trusts the
// declared backend instead of guessing from the name.
//
// This test drives the real execCommandTool through the safety
// PermissionPolicy and asserts that the resulting audit event records
// backend=hostexec (not workspaceexec).  It fails on the current
// implementation because execCommandTool does not yet implement
// BackendProvider, so inferBackend falls back to the name heuristic and
// "exec_command" matches neither "host" nor "code", defaulting to
// workspaceexec.
func TestExecCommandTool_DeclaresHostExecBackend(t *testing.T) {
	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	// We need the concrete *execCommandTool as the PermissionRequest.Tool
	// value so safety.BackendProvider can be detected on it.
	rawTool, ok := execTool.(*execCommandTool)
	require.True(t, ok, "exec tool must be *execCommandTool")

	// Sanity: the tool must implement safety.BackendProvider at all.
	var _ safety.BackendProvider = rawTool
	require.Equal(t, safety.BackendHostExec, rawTool.SafetyBackend())

	// End-to-end: drive the policy and observe the backend via the
	// audit log, the externally observable seam for the chosen backend.
	tmpDir := t.TempDir()
	auditPath := filepath.Join(tmpDir, "audit.jsonl")
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	require.NoError(t, err)
	auditLogger, err := safety.NewAuditLogger(
		auditPath, safety.DefaultPolicy().SensitivePatterns,
	)
	require.NoError(t, err)
	defer auditLogger.Close() //nolint:errcheck

	policy := safety.NewPermissionPolicyFromScanner(scanner, auditLogger)

	args, err := json.Marshal(map[string]any{"command": "ls -la"})
	require.NoError(t, err)

	req := &tool.PermissionRequest{
		Tool:      rawTool,
		ToolName:  "exec_command",
		Arguments: args,
	}
	dec, err := policy.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)
	// hostexec has RequireHumanReview=true by default, so a safe command
	// yields ask (or at worst deny).  The action itself is not what we
	// are asserting here; we assert the backend recorded in the audit.
	_ = dec

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	var event safety.AuditEvent
	require.NoError(t, json.Unmarshal(data, &event), "audit log: %s", data)
	require.Equal(t, string(safety.BackendHostExec), event.Backend,
		"exec_command must route to the hostexec backend, not %q", event.Backend)
}
