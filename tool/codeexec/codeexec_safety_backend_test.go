//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexec

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

// TestExecuteCodeTool_DeclaresCodeExecBackend asserts that the code
// execution tool declares its safety backend as codeexec via the
// safety.BackendProvider interface, and that a permission check driven
// through the real tool records backend=codeexec in the audit log.
//
// Without the interface, inferBackend falls back to a name heuristic:
// the default tool name "execute_code" contains "execute", which the
// heuristic maps to BackendCodeExec by coincidence. Declaring the
// backend explicitly removes the dependency on that substring match,
// which is the same fragile path that misroutes exec_command.
func TestExecuteCodeTool_DeclaresCodeExecBackend(t *testing.T) {
	tl := NewTool(&mockCodeExecutor{}).(*executeCodeTool)
	require.NotNil(t, tl)

	// Compile-time and runtime interface check.
	var _ safety.BackendProvider = tl
	require.Equal(t, safety.BackendCodeExec, tl.SafetyBackend())

	// End-to-end: the backend recorded in the audit log must be
	// codeexec, sourced from the declared backend rather than from name
	// matching.
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

	// Use the real code_blocks argument shape (the tool's declared
	// schema requires code_blocks; a top-level code field is not part
	// of the contract).  The block content is irrelevant to this test,
	// which only asserts the recorded backend.
	args, err := json.Marshal(map[string]any{
		"code_blocks": []map[string]any{
			{"language": "python", "code": "print('hi')"},
		},
	})
	require.NoError(t, err)

	req := &tool.PermissionRequest{
		Tool:      tl,
		ToolName:  "execute_code",
		Arguments: args,
	}
	_, err = policy.CheckToolPermission(context.Background(), req)
	require.NoError(t, err)

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	var event safety.AuditEvent
	require.NoError(t, json.Unmarshal(data, &event), "audit log: %s", data)
	require.Equal(t, string(safety.BackendCodeExec), event.Backend,
		"execute_code must route to the codeexec backend, not %q",
		event.Backend)
}
