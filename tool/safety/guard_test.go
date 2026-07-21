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
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestGuard_ImplementsPermissionPolicy verifies that Guard implements tool.PermissionPolicy.
func TestGuard_ImplementsPermissionPolicy(t *testing.T) {
	var _ tool.PermissionPolicy = (*Guard)(nil)
}

// TestGuard_CheckToolPermission_Allow verifies that a safe command is allowed.
func TestGuard_CheckToolPermission_Allow(t *testing.T) {
	guard, err := NewGuard(WithPolicy(PolicyFile{
		DefaultAction:    DecisionAllow,
		AllowedCommands:  []string{"go", "git", "echo"},
		DeniedCommands:   []string{"rm"},
		NetworkAllowlist: []string{"api.trusted.com"},
		MaxTimeoutSec:    300,
		MaxOutputBytes:   1048576,
		DeniedEnvVars:    []string{"LD_PRELOAD"},
	}))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "go test ./...",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestGuard_CheckToolPermission_Deny verifies that a dangerous command is denied.
func TestGuard_CheckToolPermission_Deny(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "rm -rf /",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.NotEmpty(t, decision.Reason)
}

// TestGuard_CheckToolPermission_Ask verifies that dependency install triggers ask.
func TestGuard_CheckToolPermission_Ask(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "pip install requests",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAsk, decision.Action)
}

// TestGuard_CheckToolPermission_FailClosed verifies that bad args result in deny (fail-closed).
func TestGuard_CheckToolPermission_FailClosed(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	// Invalid JSON for workspace_exec should cause extraction failure → deny.
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte("not valid json"),
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Contains(t, decision.Reason, "extraction failed")
}

// TestGuard_WithAuditWriter verifies that audit events are written when configured.
func TestGuard_WithAuditWriter(t *testing.T) {
	var buf bytes.Buffer
	guard, err := NewGuard(
		WithPolicy(DefaultPolicy()),
		WithAuditWriter(&buf),
	)
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "rm -rf /",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)

	// Verify that an audit event was written.
	assert.True(t, buf.Len() > 0, "audit writer should have data")

	// Verify it's valid JSONL.
	line := buf.Bytes()
	var event AuditEvent
	require.NoError(t, json.Unmarshal(line[:len(line)-1], &event)) // strip trailing newline
	assert.Equal(t, DecisionDeny, event.Decision)
	assert.Equal(t, "workspace_exec", event.ToolName)
	assert.True(t, event.Intercepted)
	assert.True(t, event.Redacted)
}

// TestGuard_WithReportSink verifies that reports are sent to the sink callback.
func TestGuard_WithReportSink(t *testing.T) {
	var receivedReport *Report
	guard, err := NewGuard(
		WithPolicy(DefaultPolicy()),
		WithReportSink(func(r Report) {
			receivedReport = &r
		}),
	)
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "rm -rf /",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)

	require.NotNil(t, receivedReport, "report should have been sent to sink")
	assert.Equal(t, DecisionDeny, receivedReport.Decision)
	assert.Equal(t, "workspace_exec", receivedReport.ToolName)
	assert.NotEmpty(t, receivedReport.Findings)
	assert.Equal(t, "1.0.0", receivedReport.Version)
	assert.NotNil(t, receivedReport.GeneratedAt)
}

// TestGuard_Close verifies that Close can be called without error.
func TestGuard_Close(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)

	require.NoError(t, guard.Close())
	// Calling Close twice should not panic.
	require.NoError(t, guard.Close())
}

// TestGuard_WithPolicyFile verifies loading policy from a file.
func TestGuard_WithPolicyFile(t *testing.T) {
	dir := t.TempDir()
	policyPath := dir + "/policy.yaml"

	yamlData := []byte(`
version: v1
default_action: allow
allowed_commands:
  - go
  - echo
network_allowlist:
  - api.trusted.com
max_timeout_sec: 120
`)
	require.NoError(t, writeTestFile(policyPath, yamlData))

	guard, err := NewGuard(WithPolicyFile(policyPath))
	require.NoError(t, err)
	defer guard.Close()

	// A safe command within allowed list should be allowed.
	args, _ := json.Marshal(map[string]any{
		"command": "go test ./...",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestGuard_WithPolicyFile_NotFound verifies that a missing policy file returns an error.
func TestGuard_WithPolicyFile_NotFound(t *testing.T) {
	guard, err := NewGuard(WithPolicyFile("/nonexistent/policy.yaml"))
	require.Error(t, err, "WithPolicyFile should return error for missing file")
	assert.Nil(t, guard)
}

// TestGuard_UnknownTool verifies that an unknown tool uses generic extraction.
func TestGuard_UnknownTool(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	// Unknown tool should still be scannable with generic extraction.
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "unknown_tool",
		Arguments: []byte(`{"command":"echo hello"}`),
	})
	require.NoError(t, err)
	// Generic extraction puts raw args as command; echo hello is safe.
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestGuard_HostExec verifies extracting and scanning a hostexec request.
func TestGuard_HostExec(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "sudo apt update",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

// TestGuard_CodeExec verifies extracting and scanning a codeexec request.
func TestGuard_CodeExec(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"code_blocks": []map[string]string{
			{"language": "python", "code": "print('hello')"},
		},
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestGuard_DeniesHostWriteStdinByDefault(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"session_id": "sess-1",
		"chars":      "rm -rf /",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "write_stdin",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestGuard_DeniesWorkspaceWriteStdinByDefault(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"session_id": "sess-1",
		"chars":      "rm -rf /",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_write_stdin",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestGuard_WorkspaceExecStdinIsScanned(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "python -",
		"stdin":   "import os\nos.system('rm -rf /')\n",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestGuard_WorkDirIsScanned(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(WithPolicy(policy))
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "cat id_rsa",
		"workdir": "~/.ssh",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("disk full")
}

func TestGuard_AuditWriterFailureFailsClosed(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultAction = DecisionAllow
	guard, err := NewGuard(
		WithPolicy(policy),
		WithAuditWriter(failingWriter{}),
	)
	require.NoError(t, err)
	defer guard.Close()

	args, _ := json.Marshal(map[string]any{
		"command": "echo hello",
	})
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	require.Error(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Contains(t, err.Error(), "audit write failed")
}

// writeTestFile is a helper to write test data to a file.
func writeTestFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
