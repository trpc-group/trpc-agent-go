//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestExecTool_DeclaresWorkspaceExecBackend asserts that ExecTool
// declares its safety backend as workspaceexec via the
// safety.BackendProvider interface, and that a permission check driven
// through the real ExecTool records backend=workspaceexec in the audit
// log.
//
// Without the interface, inferBackend falls back to a name heuristic:
// the tool name "workspace_exec" contains neither "host" nor "code",
// so it defaults to workspaceexec by chance — but relying on that
// coincidence is fragile and is the same code path that misroutes
// exec_command to workspaceexec. Declaring the backend explicitly
// removes the dependency on name matching.
func TestExecTool_DeclaresWorkspaceExecBackend(t *testing.T) {
	tl := NewExecTool(localexec.New())

	// Compile-time and runtime interface check.
	var _ safety.BackendProvider = tl
	require.Equal(t, safety.BackendWorkspaceExec, tl.SafetyBackend())

	// End-to-end: the backend recorded in the audit log must be
	// workspaceexec, sourced from the declared backend rather than from
	// name matching.
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

	args, err := json.Marshal(map[string]any{"command": "echo hello"})
	require.NoError(t, err)

	req := &tool.PermissionRequest{
		Tool:      tl,
		ToolName:  "workspace_exec",
		Arguments: args,
	}
	_, err = policy.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	var event safety.AuditEvent
	require.NoError(t, json.Unmarshal(data, &event), "audit log: %s", data)
	require.Equal(t, string(safety.BackendWorkspaceExec), event.Backend,
		"workspace_exec must route to the workspaceexec backend, not %q",
		event.Backend)
}
