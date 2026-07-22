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
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCommandExtractorsPreserveExecutionContract(t *testing.T) {
	timeoutSec := 3
	timeoutSecOld := 2
	metadata := tool.ToolMetadata{OpenWorld: true, MaxResultSize: 8192}
	req := &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-workspace",
		Arguments: []byte(`{
			"command":"go test ./...",
			"cwd":"work/pkg",
			"stdin":"password=stdin-secret",
			"env":{"CI":"true"},
			"timeout":1,
			"timeoutSec":2,
			"timeout_sec":3,
			"background":true,
			"tty":false,
			"pty":true
		}`),
		Metadata: metadata,
	}

	got, handled, err := extractWorkspaceExec(req)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "call-workspace", got.ToolCallID)
	require.Equal(t, BackendWorkspace, got.Backend)
	require.Equal(t, "work/pkg", got.CWD)
	require.Equal(t, "password=stdin-secret", got.SessionInput)
	require.Equal(t, 3*time.Second, got.Timeout)
	// ToolMetadata is advisory and cannot prove executor-side byte enforcement.
	require.Zero(t, got.MaxOutputBytes)
	require.True(t, got.Background)
	require.False(t, got.TTY)
	require.Equal(t, metadata, got.Metadata)

	hostArgs, err := json.Marshal(commandArguments{
		Command:       "go test ./...",
		Workdir:       "host/pkg",
		TimeoutSecOld: &timeoutSecOld,
		PTY:           boolPointer(true),
	})
	require.NoError(t, err)
	host, handled, err := extractHostExec(&tool.PermissionRequest{
		ToolName: "exec_command", Arguments: hostArgs,
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, BackendHost, host.Backend)
	require.Equal(t, "host/pkg", host.CWD)
	require.Equal(t, time.Duration(timeoutSecOld)*time.Second, host.Timeout)
	require.True(t, host.TTY)

	fallbackArgs, err := json.Marshal(commandArguments{
		Command: "go test ./...", Timeout: 1, TimeoutSec: &timeoutSec,
	})
	require.NoError(t, err)
	fallback, handled, err := extractWorkspaceExec(&tool.PermissionRequest{
		ToolName: "workspace_exec", Arguments: fallbackArgs,
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, time.Duration(timeoutSec)*time.Second, fallback.Timeout)

	_, handled, err = extractWorkspaceExec(nil)
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractHostExec(&tool.PermissionRequest{ToolName: "workspace_exec"})
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractWorkspaceExec(&tool.PermissionRequest{
		ToolName: "workspace_exec", Arguments: []byte("{"),
	})
	require.Error(t, err)
	require.True(t, handled)
}

func TestSkillAndWriteStdinExtractors(t *testing.T) {
	metadata := tool.ToolMetadata{MaxResultSize: 4096}
	skill, handled, err := extractSkillRun(&tool.PermissionRequest{
		ToolName:   "skill_run",
		ToolCallID: "skill-call",
		Arguments: []byte(`{
			"command":"python -",
			"cwd":"skills/demo",
			"stdin":"import os; os.remove('.env')",
			"env":{"CI":"true"},
			"timeout":9
		}`),
		Metadata: metadata,
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, BackendSkill, skill.Backend)
	require.Equal(t, "import os; os.remove('.env')", skill.SessionInput)
	require.Equal(t, 9*time.Second, skill.Timeout)
	require.Equal(t, metadata, skill.Metadata)
	skillExec, handled, err := extractSkillExec(&tool.PermissionRequest{
		ToolName: "skill_exec",
		Arguments: []byte(`{
			"command":"go test ./...","stdin":"status","timeout":5,"tty":true
		}`),
		Metadata: metadata,
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, BackendSkill, skillExec.Backend)
	require.Equal(t, "status", skillExec.SessionInput)
	require.Equal(t, 5*time.Second, skillExec.Timeout)
	require.True(t, skillExec.TTY)

	_, handled, err = extractSkillRun(nil)
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractSkillRun(&tool.PermissionRequest{ToolName: "other"})
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractSkillRun(&tool.PermissionRequest{
		ToolName: "skill_run", Arguments: []byte("{"),
	})
	require.Error(t, err)
	require.True(t, handled)

	tests := []struct {
		name      string
		extractor ExtractorFunc
		toolName  string
		args      string
		backend   Backend
		wantInput string
	}{
		{
			name: "host append newline", extractor: extractHostWriteStdin,
			toolName: "write_stdin", args: `{"chars":"go test","append_newline":true}`,
			backend: BackendHost, wantInput: "go test\n",
		},
		{
			name: "workspace submit fallback", extractor: extractWorkspaceWriteStdin,
			toolName: "workspace_write_stdin", args: `{"chars":"go vet","submit":true}`,
			backend: BackendWorkspace, wantInput: "go vet\n",
		},
		{
			name: "skill input", extractor: extractSkillWriteStdin,
			toolName: "skill_write_stdin", args: `{"chars":"rm -rf /","submit":true}`,
			backend: BackendSkill, wantInput: "rm -rf /\n",
		},
		{
			name: "explicit false wins", extractor: extractHostWriteStdin,
			toolName: "write_stdin", args: `{"chars":"status","append_newline":false,"submit":true}`,
			backend: BackendHost, wantInput: "status",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, handled, err := test.extractor.Extract(&tool.PermissionRequest{
				ToolName: test.toolName, ToolCallID: "stdin-call",
				Arguments: []byte(test.args), Metadata: metadata,
			})
			require.NoError(t, err)
			require.True(t, handled)
			require.Equal(t, test.backend, got.Backend)
			require.Equal(t, test.wantInput, got.SessionInput)
			require.Zero(t, got.MaxOutputBytes)
		})
	}

	_, handled, err = extractHostWriteStdin(nil)
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractWorkspaceWriteStdin(&tool.PermissionRequest{ToolName: "write_stdin"})
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractHostWriteStdin(&tool.PermissionRequest{
		ToolName: "write_stdin", Arguments: []byte("{"),
	})
	require.Error(t, err)
	require.True(t, handled)
}

func TestDecodeCodeBlocksVariants(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      []CodeBlock
		wantError bool
	}{
		{name: "array", raw: `[{"language":"go","code":"package main"}]`,
			want: []CodeBlock{{Language: "go", Code: "package main"}}},
		{name: "object", raw: `{"language":"bash","code":"echo ok"}`,
			want: []CodeBlock{{Language: "bash", Code: "echo ok"}}},
		{name: "encoded array", raw: `"[{\"language\":\"python\",\"code\":\"print(1)\"}]"`,
			want: []CodeBlock{{Language: "python", Code: "print(1)"}}},
		{name: "missing", raw: "", wantError: true},
		{name: "null", raw: "null", wantError: true},
		{name: "invalid outer JSON", raw: "{", wantError: true},
		{name: "invalid encoded JSON", raw: `"not-json"`, wantError: true},
		{name: "invalid array element", raw: `[{"language":1}]`, wantError: true},
		{name: "invalid object field", raw: `{"code":1}`, wantError: true},
		{name: "unknown execution field",
			raw:       `{"language":"go","code":"package main","network_mode":"host"}`,
			wantError: true},
		{name: "scalar", raw: "42", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeCodeBlocks(json.RawMessage(test.raw))
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestExtractCodeHandlesOuterArguments(t *testing.T) {
	request := &tool.PermissionRequest{
		ToolName:   "execute_code",
		ToolCallID: "code-call",
		Arguments: []byte(
			`{"execution_id":"sandbox-7","code_blocks":{"language":"go","code":"package main"}}`,
		),
		Metadata: tool.ToolMetadata{MaxResultSize: 1024},
	}
	got, handled, err := extractCode(request)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, BackendCode, got.Backend)
	require.Equal(t, "sandbox-7", got.ExecutionID)
	require.Equal(t, []CodeBlock{{Language: "go", Code: "package main"}}, got.CodeBlocks)
	require.Zero(t, got.MaxOutputBytes)

	_, handled, err = extractCode(nil)
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractCode(&tool.PermissionRequest{ToolName: "other"})
	require.NoError(t, err)
	require.False(t, handled)
	_, handled, err = extractCode(&tool.PermissionRequest{
		ToolName: "execute_code", Arguments: []byte("{"),
	})
	require.Error(t, err)
	require.True(t, handled)
	_, handled, err = extractCode(&tool.PermissionRequest{
		ToolName: "execute_code", Arguments: []byte(`{}`),
	})
	require.Error(t, err)
	require.True(t, handled)
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
