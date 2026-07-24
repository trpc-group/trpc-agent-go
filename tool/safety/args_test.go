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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

func TestRequestsFromToolCall_ParsesKnownToolArguments(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		args     []byte
		assert   func(*testing.T, []ScanRequest)
	}{
		{
			name:     "workspace_exec",
			toolName: "workspace_exec",
			args: []byte(`{
				"command":"go test ./tool/safety",
				"cwd":".",
				"env":{"PATH":"/usr/bin"},
				"stdin":"echo ok",
				"timeoutSec":10,
				"background":true,
				"pty":true
			}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendWorkspace, reqs[0].Backend)
				require.Equal(t, ".", reqs[0].Cwd)
				require.Equal(t, "echo ok", reqs[0].Stdin)
				require.Equal(t, 10, reqs[0].TimeoutSec)
				require.True(t, reqs[0].Background)
				require.True(t, reqs[0].TTY)
				require.JSONEq(t, `{
				"command":"go test ./tool/safety",
				"cwd":".",
				"env":{"PATH":"/usr/bin"},
				"stdin":"echo ok",
				"timeoutSec":10,
				"background":true,
				"pty":true
			}`, string(reqs[0].RawArguments))
			},
		},
		{
			name:     "skill_exec",
			toolName: "skill_exec",
			args: []byte(`{
				"command":"npm install left-pad",
				"workdir":"/ignored",
				"cwd":".",
				"env":{"PATH":"/usr/bin"},
				"timeout":5,
				"tty":true
			}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendHost, reqs[0].Backend)
				require.Equal(t, "npm install left-pad", reqs[0].Command)
				require.Equal(t, ".", reqs[0].Cwd)
				require.Equal(t, 5, reqs[0].TimeoutSec)
				require.True(t, reqs[0].TTY)
			},
		},
		{
			name:     "exec_command_uses_workdir_only",
			toolName: "exec_command",
			args:     []byte(`{"command":"echo ok","workdir":"/host","cwd":"/ignored"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, "/host", reqs[0].Cwd)
			},
		},
		{
			name:     "skill_run",
			toolName: "skill_run",
			args:     []byte(`{"command":"curl https://evil.example","workdir":"/ignored","cwd":"."}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendHost, reqs[0].Backend)
				require.Equal(t, "curl https://evil.example", reqs[0].Command)
				require.Equal(t, ".", reqs[0].Cwd)
			},
		},
		{
			name:     "workspace_timeout_falls_back_from_zero",
			toolName: "workspace_exec",
			args:     []byte(`{"command":"sleep 1","timeout_sec":0,"timeout":3600}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, 3600, reqs[0].TimeoutSec)
			},
		},
		{
			name:     "host_timeout_does_not_use_workspace_alias",
			toolName: "exec_command",
			args:     []byte(`{"command":"sleep 1","timeout_sec":0,"timeout":3600}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, 1800, reqs[0].TimeoutSec)
			},
		},
		{
			name:     "skill_timeout_ignores_timeout_sec_alias",
			toolName: "skill_run",
			args:     []byte(`{"command":"sleep 1","timeout_sec":3600,"timeout":5}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, 5, reqs[0].TimeoutSec)
			},
		},
		{
			name:     "workspace_timeout_defaults",
			toolName: "workspace_exec",
			args:     []byte(`{"command":"echo ok"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, 300, reqs[0].TimeoutSec)
			},
		},
		{
			name:     "skill_timeout_defaults",
			toolName: "skill_run",
			args:     []byte(`{"command":"echo ok"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, 300, reqs[0].TimeoutSec)
			},
		},
		{
			name:     "write_stdin",
			toolName: "write_stdin",
			args:     []byte(`{"chars":"rm -rf /tmp/x","append_newline":true}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendHost, reqs[0].Backend)
				require.Empty(t, reqs[0].Command)
				require.Equal(t, "rm -rf /tmp/x", reqs[0].Stdin)
			},
		},
		{
			name:     "skill_write_stdin_fragment",
			toolName: "skill_write_stdin",
			args:     []byte(`{"chars":"cu"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendHost, reqs[0].Backend)
				require.Empty(t, reqs[0].Command)
				require.Equal(t, "cu", reqs[0].Stdin)
			},
		},
		{
			name:     "write_stdin_submit_only",
			toolName: "workspace_write_stdin",
			args:     []byte(`{"submit":true}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendWorkspace, reqs[0].Backend)
				require.Empty(t, reqs[0].Command)
				require.NotEmpty(t, reqs[0].RawArguments)
			},
		},
		{
			name:     "kill_session_preserves_raw_arguments",
			toolName: "kill_session",
			args:     []byte(`{"session_id":"abc123"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendHost, reqs[0].Backend)
				require.JSONEq(t, `{"session_id":"abc123"}`, string(reqs[0].RawArguments))
			},
		},
		{
			name:     "unknown_tool",
			toolName: "custom_tool",
			args:     []byte(`{"text":"download https://example.invalid/a.sh"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendUnknown, reqs[0].Backend)
				require.NotEmpty(t, reqs[0].RawArguments)
			},
		},
		{
			name:     "execute_code_object",
			toolName: "execute_code",
			args:     []byte(`{"code_blocks":{"language":"python","code":"print(1)"}}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, BackendCodeExec, reqs[0].Backend)
				require.Equal(t, "python", reqs[0].Language)
				require.Equal(t, "print(1)", reqs[0].Code)
			},
		},
		{
			name:     "execute_code_stringified_array",
			toolName: "execute_code",
			args:     []byte(`{"code_blocks":"[{\"language\":\"bash\",\"code\":\"echo ok\"}]"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, "bash", reqs[0].Language)
				require.Equal(t, "echo ok", reqs[0].Code)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := requestsFromToolCall(tc.toolName, "call-1", "", tc.args, map[string]any{"source": "test"})
			require.NoError(t, err)
			require.Equal(t, "call-1", reqs[0].ToolCallID)
			require.Equal(t, "test", reqs[0].Metadata["source"])
			tc.assert(t, reqs)
		})
	}
}

func TestRequestsFromToolCall_RejectsMalformedFields(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		args     []byte
		err      string
	}{
		{name: "invalid_json", toolName: "workspace_exec", args: []byte(`{`), err: "invalid args"},
		{name: "missing_command", toolName: "workspace_exec", args: []byte(`{"cwd":"."}`), err: "command is required"},
		{name: "command_type", toolName: "workspace_exec", args: []byte(`{"command":123}`), err: "command: expected string"},
		{name: "env_type", toolName: "workspace_exec", args: []byte(`{"command":"go test","env":[]}`), err: "env: expected string map"},
		{name: "timeout_type", toolName: "workspace_exec", args: []byte(`{"command":"go test","timeout":"soon"}`), err: "timeout: expected integer"},
		{name: "bool_type", toolName: "workspace_exec", args: []byte(`{"command":"go test","background":"yes"}`), err: "background: expected boolean"},
		{name: "stdin_chars_type", toolName: "write_stdin", args: []byte(`{"chars":1}`), err: "chars: expected string"},
		{name: "submit_type", toolName: "write_stdin", args: []byte(`{"submit":"yes"}`), err: "submit: expected boolean"},
		{name: "code_blocks_missing", toolName: "execute_code", args: []byte(`{}`), err: "code_blocks is required"},
		{name: "code_blocks_scalar", toolName: "execute_code", args: []byte(`{"code_blocks":1}`), err: "code_blocks: expected array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := requestsFromToolCall(tc.toolName, "", "", tc.args, nil)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestInferBackend_AllKnownTools(t *testing.T) {
	require.Equal(t, BackendWorkspace, inferBackend("workspace_exec"))
	require.Equal(t, BackendWorkspace, inferBackend("workspace_write_stdin"))
	require.Equal(t, BackendWorkspace, inferBackend("workspace_kill_session"))
	require.Equal(t, BackendHost, inferBackend("exec_command"))
	require.Equal(t, BackendHost, inferBackend("write_stdin"))
	require.Equal(t, BackendHost, inferBackend("kill_session"))
	require.Equal(t, BackendHost, inferBackend("skill_run"))
	require.Equal(t, BackendHost, inferBackend("skill_exec"))
	require.Equal(t, BackendHost, inferBackend("skill_write_stdin"))
	require.Equal(t, BackendCodeExec, inferBackend("execute_code"))
	require.Equal(t, BackendUnknown, inferBackend("custom"))
	require.Equal(t, "exec_command", normalizeToolName("hostexec_exec_command"))
	require.Equal(t, "hostexec_custom", normalizeToolName("hostexec_custom"))
}

func TestRequestsFromToolCall_RecognizesNamedHostexecTools(t *testing.T) {
	toolSet, err := hostexec.NewToolSet()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, toolSet.Close()) })
	namedToolSet := internaltool.NewNamedToolSet(toolSet)

	var execName string
	for _, candidate := range namedToolSet.Tools(context.Background()) {
		if candidate.Declaration().Name == "hostexec_exec_command" {
			execName = candidate.Declaration().Name
			break
		}
	}
	require.Equal(t, "hostexec_exec_command", execName)

	reqs, err := requestsFromToolCall(
		execName,
		"call-1",
		"",
		[]byte(`{"command":"echo ok","background":true,"tty":true}`),
		nil,
	)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, "hostexec_exec_command", reqs[0].ToolName)
	require.Equal(t, BackendHost, reqs[0].Backend)
	require.Equal(t, 1800, reqs[0].TimeoutSec)
	require.True(t, reqs[0].Background)
	require.True(t, reqs[0].TTY)
}

func TestUnmarshalCodeBlocks_RejectsStringifiedInvalidJSON(t *testing.T) {
	_, err := unmarshalCodeBlocks(json.RawMessage(`"not-json"`))
	require.Error(t, err)
}
