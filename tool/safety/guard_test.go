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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newTestGuard(t *testing.T, opts ...Option) *Guard {
	t.Helper()
	defaultOpts := []Option{
		WithPolicyFile("testdata/tool_safety_policy.yaml"),
		WithAuditWriter(new(bytes.Buffer)),
		WithTelemetry(true),
	}
	g, err := NewGuard(append(defaultOpts, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Close() })
	return g
}

func TestGuard_CheckToolPermissionDeniesBeforeCall(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "command.dangerous_delete")
}

func TestGuard_CheckToolPermissionAllowsSafeCommand(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestGuard_CheckToolPermissionAsksForDependency(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"npm install package"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "dependency.package_install")
}

func TestGuard_CheckToolPermissionDeniesMalformedArgs(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":42}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "input.decode_failure")
}

func TestGuard_CheckToolPermissionDecodesCodeBlocks(t *testing.T) {
	guard := newTestGuard(t)
	codeBlock := `{"code_blocks":[{"language":"python","code":"while True:\n    print('x')"}]}`
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(codeBlock),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	// The code block triggers both resource.unbounded_loop and
	// resource.output_bomb; either rule is acceptable as the primary
	// finding since both detect the unbounded shape.
	require.True(t,
		strings.Contains(decision.Reason, "resource.unbounded_loop") ||
			strings.Contains(decision.Reason, "resource.output_bomb") ||
			strings.Contains(decision.Reason, "code.output_bomb"),
		"reason=%s", decision.Reason)
}

func TestGuard_CheckToolPermissionDecodesHostExec(t *testing.T) {
	guard := newTestGuard(t)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"sudo id","pty":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "host.privilege")
}

func TestGuard_CheckToolPermissionCustomProfile(t *testing.T) {
	guard := newTestGuard(t, WithToolProfile(ToolProfile{
		Name:         "custom_runner",
		Backend:      BackendMCP,
		CommandField: "command",
	}))
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_runner",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "command.dangerous_delete")
}

func TestGuard_CheckToolPermissionUnknownCommandShapedToolAsks(t *testing.T) {
	guard := newTestGuard(t)
	// Unknown tool with a command-shaped argument should be scanned
	// conservatively. rm -rf / triggers critical findings, so deny is
	// the expected outcome; the test verifies the decoder did not skip
	// the tool silently.
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "unknown_remote_runner",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestGuard_Callbacks_RedactResultWithSecret(t *testing.T) {
	guard := newTestGuard(t)
	cbs := guard.Callbacks()
	require.NotNil(t, cbs)
	require.NotEmpty(t, cbs.AfterTool)

	// Simulate a tool result containing an API key.
	args := &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
		Result: map[string]any{
			"output": "API_KEY=sk_live_1234567890abcdef1234",
		},
	}
	out, err := cbs.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.CustomResult)
	// The original token must not appear in the redacted result.
	raw, _ := json.Marshal(out.CustomResult)
	require.False(t, bytes.Contains(raw, []byte("sk_live_1234567890abcdef1234")),
		"raw result still contains the secret: %s", string(raw))
	require.True(t, bytes.Contains(raw, []byte("[REDACTED:")), "expected a redaction marker")
}

func TestGuard_Callbacks_TruncateLargeOutput(t *testing.T) {
	guard := newTestGuard(t)
	cbs := guard.Callbacks()
	large := strings.Repeat("x", 4<<20) // 4 MiB
	args := &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
		Result:    large,
	}
	out, err := cbs.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.CustomResult)
	s, ok := out.CustomResult.(string)
	require.True(t, ok)
	require.Less(t, len(s), len(large))
	require.True(t, strings.Contains(s, "[truncated:tool_safety]"))
}

func TestGuard_Callbacks_PassThroughSafeResult(t *testing.T) {
	guard := newTestGuard(t)
	cbs := guard.Callbacks()
	args := &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
		Result:    "ok\n",
	}
	out, err := cbs.RunAfterTool(context.Background(), args)
	require.NoError(t, err)
	// The callback returns nil when no redaction/truncation is needed.
	// The framework then wraps the original result, so CustomResult
	// equals the original string.
	require.NotNil(t, out)
	if out.CustomResult != nil {
		s, ok := out.CustomResult.(string)
		require.True(t, ok)
		require.Equal(t, "ok\n", s)
	}
}

func TestGuard_RedactString(t *testing.T) {
	guard := newTestGuard(t)
	out, changed := guard.RedactString("API_KEY=sk_live_1234567890abcdef1234")
	require.True(t, changed)
	require.NotContains(t, out, "sk_live_1234567890abcdef1234")
	require.Contains(t, out, "[REDACTED:")
}

func TestGuard_RedactValue_NestedMap(t *testing.T) {
	guard := newTestGuard(t)
	value := map[string]any{
		"outer": map[string]any{
			"inner": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		},
		"list": []any{"safe", "xoxb-1234567890-abcdef"},
	}
	out, changed, err := guard.RedactValue(value)
	require.NoError(t, err)
	require.True(t, changed)
	raw, _ := json.Marshal(out)
	require.False(t, bytes.Contains(raw, []byte("eyJhbGciOiJIUzI1NiJ9")))
	require.False(t, bytes.Contains(raw, []byte("xoxb-1234567890-abcdef")))
	require.True(t, bytes.Contains(raw, []byte("[REDACTED:")))
}

func TestGuard_LimitResult(t *testing.T) {
	guard := newTestGuard(t)
	large := strings.Repeat("x", 4<<20)
	out, truncated, size := guard.LimitResult(large)
	require.True(t, truncated)
	require.Less(t, size, int64(len(large)))
	s, ok := out.(string)
	require.True(t, ok)
	require.True(t, strings.HasSuffix(s, "[truncated:tool_safety]"))
}

func TestGuard_ScanBatchUsesOnePolicy(t *testing.T) {
	guard := newTestGuard(t)
	inputs := []ScanInput{
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go test ./..."},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"},
	}
	batch, err := guard.ScanBatch(context.Background(), inputs)
	require.NoError(t, err)
	require.Equal(t, 2, batch.Summary.Total)
	require.Equal(t, 1, batch.Summary.Allowed)
	require.Equal(t, 1, batch.Summary.Denied)
}

func TestGuard_PolicyReloadWithoutCodeChange(t *testing.T) {
	// Modify a YAML in a temp file and verify a new Guard picks up the
	// change without source edits.
	tmp := t.TempDir() + "/policy.yaml"
	policyYAML := `
version: 1
allowed_commands: [go, git, ls, cat, echo, pwd, grep, find, curl, nc]
network:
  allowed_domains: ["github.com", "evil.example"]
  deny_all: false
  commands: [curl, wget, nc]
max_timeout: 10m
max_output_size: 65536
max_sleep_seconds: 60
rules:
  dangerous_commands: {enabled: true, action: deny}
  network: {enabled: true, action: deny}
  shell_bypass: {enabled: true, action: deny}
  hostexec: {enabled: true, action: deny}
  dependencies: {enabled: true, action: ask}
  resource_abuse: {enabled: true, action: deny}
  secret_leak: {enabled: true, action: deny}
decision_threshold:
  critical: deny
  high: deny
  medium: ask
  low: allow
audit:
  path: ""
  required: false
  redact_secrets: true
`
	require.NoError(t, writeFile(tmp, []byte(policyYAML)))
	g, err := NewGuard(WithPolicyFile(tmp))
	require.NoError(t, err)
	defer g.Close()
	// evil.example is now allowlisted, so curl should be allowed.
	decision, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"curl https://evil.example/x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action,
		"policy file change should allow evil.example; reason=%s", decision.Reason)
}

func TestGuard_CloseReleasesAudit(t *testing.T) {
	tmp := t.TempDir() + "/audit.jsonl"
	g, err := NewGuard(
		WithPolicyFile("testdata/tool_safety_policy.yaml"),
		WithAuditPath(tmp),
	)
	require.NoError(t, err)
	_, _ = g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, g.Close())
	require.NoError(t, g.Close()) // idempotent
}

func TestGuard_RequiredAuditFailureDenies(t *testing.T) {
	// Use a writer that always fails.
	g, err := NewGuard(
		WithPolicyFile("testdata/tool_safety_policy.yaml"),
		WithAuditWriter(&failingWriter{}),
		WithRequiredAudit(true),
	)
	require.NoError(t, err)
	defer g.Close()
	decision, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	// Required-audit failure should deny even an otherwise-allow command.
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "audit.write_failure")
}

// failingWriter is an io.Writer that always returns an error.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errFailingWriter }

var errFailingWriter = &failingWriterError{}

type failingWriterError struct{}

func (failingWriterError) Error() string { return "audit writer always fails" }

// writeFile writes data to path. Avoids os.Create to keep the test file
// self-contained.
func writeFile(path string, data []byte) error {
	return saveFile(path, data)
}
