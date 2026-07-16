//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractWorkspaceExec verifies extracting a workspace_exec request.
func TestExtractWorkspaceExec(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command":    "go test ./...",
		"cwd":        "/workspace",
		"env":        map[string]string{"GOPATH": "/go"},
		"timeout":    30,
		"background": false,
	})

	req, err := extractWorkspaceExec("workspace_exec", args)
	require.NoError(t, err)

	assert.Equal(t, "go test ./...", req.Command)
	assert.Equal(t, "/workspace", req.WorkDir)
	assert.Equal(t, map[string]string{"GOPATH": "/go"}, req.Env)
	assert.Equal(t, 30, req.Timeout)
	assert.False(t, req.Background)
	assert.Equal(t, "workspaceexec", req.Backend)
}

// TestExtractWorkspaceExec_PTYYAndTTY verifies that TTY and PTY are handled.
func TestExtractWorkspaceExec_PTYYAndTTY(t *testing.T) {
	tests := []struct {
		name      string
		jsonArgs  string
		expectPTY bool
	}{
		{
			name:      "pty true",
			jsonArgs:  `{"command":"echo hello","pty":true}`,
			expectPTY: true,
		},
		{
			name:      "tty true",
			jsonArgs:  `{"command":"echo hello","tty":true}`,
			expectPTY: true,
		},
		{
			name:      "neither",
			jsonArgs:  `{"command":"echo hello"}`,
			expectPTY: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := extractWorkspaceExec("workspace_exec", []byte(tt.jsonArgs))
			require.NoError(t, err)
			assert.Equal(t, tt.expectPTY, req.PTY)
		})
	}
}

// TestExtractWorkspaceExec_TimeoutSec verifies timeout_sec field handling.
func TestExtractWorkspaceExec_TimeoutSec(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command":     "go test ./...",
		"timeout":     10,
		"timeout_sec": 60,
	})

	req, err := extractWorkspaceExec("workspace_exec", args)
	require.NoError(t, err)

	assert.Equal(t, 60, req.Timeout, "timeout_sec should take precedence when larger")
}

// TestExtractHostExec verifies extracting a hostexec request.
func TestExtractHostExec(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command":     "sudo apt update",
		"workdir":     "/home/user",
		"env":         map[string]string{"HOME": "/home/user"},
		"timeout_sec": 30,
		"background":  false,
	})

	req, err := extractHostExec("exec_command", args)
	require.NoError(t, err)

	assert.Equal(t, "sudo apt update", req.Command)
	assert.Equal(t, "/home/user", req.WorkDir)
	assert.Equal(t, map[string]string{"HOME": "/home/user"}, req.Env)
	assert.Equal(t, 30, req.Timeout)
	assert.False(t, req.Background)
	assert.Equal(t, "hostexec", req.Backend)
}

// TestExtractHostExec_PTYYAndTTY verifies that TTY and PTY are handled for hostexec.
func TestExtractHostExec_PTYYAndTTY(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command": "echo hello",
		"pty":     true,
	})

	req, err := extractHostExec("exec_command", args)
	require.NoError(t, err)
	assert.True(t, req.PTY)
}

// TestExtractCodeExec verifies extracting a codeexec request.
func TestExtractCodeExec(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"code_blocks": []map[string]string{
			{"language": "python", "code": "print('hello')"},
			{"language": "go", "code": "fmt.Println(\"world\")"},
		},
		"execution_id": "test-exec-123",
	})

	req, err := extractCodeExec("execute_code", args)
	require.NoError(t, err)

	assert.Equal(t, []string{"print('hello')", "fmt.Println(\"world\")"}, req.CodeBlocks)
	assert.Equal(t, "codeexec", req.Backend)
	assert.Empty(t, req.Command)
}

// TestExtractRequest_UnknownTool verifies that unknown tools get generic extraction.
func TestExtractRequest_UnknownTool(t *testing.T) {
	extractors := map[string]Extractor{}
	req, err := extractRequest("unknown_tool", []byte(`{"some":"args"}`), extractors)
	require.NoError(t, err)

	assert.Equal(t, `{"some":"args"}`, req.Command)
	assert.Equal(t, "unknown", req.Backend)
}

// TestExtractRequest_KnownTool verifies that known tools use their extractor.
func TestExtractRequest_KnownTool(t *testing.T) {
	extractors := map[string]Extractor{
		"workspace_exec": extractWorkspaceExec,
	}

	args, _ := json.Marshal(map[string]any{
		"command": "go test ./...",
	})

	req, err := extractRequest("workspace_exec", args, extractors)
	require.NoError(t, err)

	assert.Equal(t, "go test ./...", req.Command)
	assert.Equal(t, "workspaceexec", req.Backend)
}

// TestExtractWorkspaceExec_InvalidJSON verifies that invalid JSON returns an error.
func TestExtractWorkspaceExec_InvalidJSON(t *testing.T) {
	_, err := extractWorkspaceExec("workspace_exec", []byte("not json"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_exec")
}

// TestExtractHostExec_InvalidJSON verifies that invalid JSON returns an error.
func TestExtractHostExec_InvalidJSON(t *testing.T) {
	_, err := extractHostExec("exec_command", []byte("not json"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exec_command")
}

// TestExtractCodeExec_InvalidJSON verifies that invalid JSON returns an error.
func TestExtractCodeExec_InvalidJSON(t *testing.T) {
	_, err := extractCodeExec("execute_code", []byte("not json"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execute_code")
}

// TestExecRequest_ToScanInput verifies the ToScanInput conversion.
func TestExecRequest_ToScanInput(t *testing.T) {
	req := ExecRequest{
		Command:    "go test ./...",
		CodeBlocks: []string{"func main() {}"},
		Args:       []string{"-v"},
		WorkDir:    "/workspace",
		Env:        map[string]string{"GOPATH": "/go"},
		Backend:    "workspaceexec",
	}

	scanInput := req.ToScanInput("my_tool")

	assert.Equal(t, "go test ./...", scanInput.Command)
	assert.Equal(t, []string{"func main() {}"}, scanInput.CodeBlocks)
	assert.Equal(t, []string{"-v"}, scanInput.Args)
	assert.Equal(t, "/workspace", scanInput.WorkDir)
	assert.Equal(t, map[string]string{"GOPATH": "/go"}, scanInput.Env)
	assert.Equal(t, "my_tool", scanInput.ToolName)
	assert.Equal(t, "workspaceexec", scanInput.Backend)
}

// TestRegisterExtractor verifies that custom extractors can be registered.
func TestRegisterExtractor(t *testing.T) {
	extractors := map[string]Extractor{}
	customExt := func(toolName string, args []byte) (ExecRequest, error) {
		return ExecRequest{Command: "custom", Backend: "custom_backend"}, nil
	}

	RegisterExtractor(extractors, "custom_tool", customExt)

	assert.Contains(t, extractors, "custom_tool")
	req, err := extractors["custom_tool"]("custom_tool", nil)
	require.NoError(t, err)
	assert.Equal(t, "custom", req.Command)
}

// TestExtractWorkspaceExec_EmptyCommand verifies handling of empty command.
func TestExtractWorkspaceExec_EmptyCommand(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command": "",
	})

	req, err := extractWorkspaceExec("workspace_exec", args)
	require.NoError(t, err)
	assert.Equal(t, "", req.Command)
}

// TestExtractCodeExec_EmptyCodeBlocks verifies handling of empty code blocks.
func TestExtractCodeExec_EmptyCodeBlocks(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"code_blocks": []map[string]string{},
	})

	req, err := extractCodeExec("execute_code", args)
	require.NoError(t, err)
	assert.Empty(t, req.CodeBlocks)
}
