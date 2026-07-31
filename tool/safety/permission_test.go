//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicyInfersExecutionBackends(t *testing.T) {
	scanner := newTestScanner(t)
	tests := []struct {
		name    string
		request *tool.PermissionRequest
		action  tool.PermissionAction
		reason  string
	}{
		{
			name: "workspace command",
			request: permissionRequest(
				"workspace_exec",
				map[string]*tool.Schema{
					"command": {Type: "string"},
					"cwd":     {Type: "string"},
				},
				`{"command":"rm -rf /","cwd":"."}`,
			),
			action: tool.PermissionActionDeny,
			reason: string(RuleDangerousDelete),
		},
		{
			name: "host pty",
			request: permissionRequest(
				"hostexec_exec_command",
				map[string]*tool.Schema{
					"command": {Type: "string"},
					"workdir": {Type: "string"},
				},
				`{"command":"go test ./...","workdir":".","tty":true,"timeout_sec":60}`,
			),
			action: tool.PermissionActionAsk,
			reason: string(RuleHostSession),
		},
		{
			name: "renamed codeexec",
			request: permissionRequest(
				"run_snippet",
				map[string]*tool.Schema{
					"code_blocks": {Type: "array"},
				},
				`{"code_blocks":[{"language":"ruby","code":"puts 1"}]}`,
			),
			action: tool.PermissionActionAsk,
			reason: string(RuleUnknownLanguage),
		},
		{
			name: "malformed arguments",
			request: permissionRequest(
				"workspace_exec",
				map[string]*tool.Schema{"command": {Type: "string"}},
				`{"command":`,
			),
			action: tool.PermissionActionDeny,
			reason: string(RuleInvalidInput),
		},
		{
			name: "opaque destructive tool",
			request: &tool.PermissionRequest{
				ToolName:  "delete_record",
				Arguments: []byte(`{"id":"1"}`),
				Metadata:  tool.ToolMetadata{Destructive: true},
			},
			action: tool.PermissionActionAsk,
			reason: string(RuleToolMetadata),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := scanner.CheckToolPermission(
				context.Background(),
				test.request,
			)
			require.NoError(t, err)
			require.Equal(t, test.action, decision.Action)
			require.Contains(t, decision.Reason, test.reason)
		})
	}
}

func TestPermissionPolicyUsesCustomToolProfile(t *testing.T) {
	policy := testPolicy()
	policy.ToolProfiles = map[string]ToolProfile{
		"mcp_run": {
			Backend:               BackendHost,
			CommandField:          "request.script",
			WorkingDirectoryField: "request.directory",
			TimeoutSecondsField:   "request.timeout",
		},
	}
	scanner, err := NewScanner(policy)
	require.NoError(t, err)
	decision, err := scanner.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "mcp_run",
			Arguments: []byte(
				`{"request":{"script":"curl https://evil.example","directory":".","timeout":30}}`,
			),
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, string(RuleNetworkEgress))
}

func TestPermissionPolicyScansOpenWorldMCPArguments(t *testing.T) {
	decision, err := newTestScanner(t).CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  "fetch_document",
			Arguments: []byte(`{"url":"https://evil.example/document"}`),
			Metadata:  tool.ToolMetadata{OpenWorld: true},
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, string(RuleNetworkEgress))
}

func TestPermissionPolicyRejectsSecretInOpaqueArguments(t *testing.T) {
	decision, err := newTestScanner(t).CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "remote_action",
			Arguments: []byte(
				`{"authentication":{"token":"sk-1234567890abcdef1234"}}`,
			),
			Metadata: tool.ToolMetadata{OpenWorld: true},
		},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, string(RuleSecretExposure))
	require.NotContains(t, decision.Reason, "1234567890abcdef1234")
}

func TestPermissionPolicyReviewsInteractiveSessionWrites(t *testing.T) {
	scanner, err := NewScanner(Policy{})
	require.NoError(t, err)
	properties := map[string]*tool.Schema{
		"session_id":     {Type: "string"},
		"chars":          {Type: "string"},
		"submit":         {Type: "boolean"},
		"append_newline": {Type: "boolean"},
	}
	tests := []struct {
		name      string
		arguments string
		action    tool.PermissionAction
	}{
		{name: "poll", arguments: `{"session_id":"s","chars":""}`, action: tool.PermissionActionAllow},
		{name: "first split fragment", arguments: `{"session_id":"s","chars":"cu"}`, action: tool.PermissionActionAsk},
		{name: "second split fragment", arguments: `{"session_id":"s","chars":"rl https://evil.example\n"}`, action: tool.PermissionActionAsk},
		{name: "submit", arguments: `{"session_id":"s","submit":true}`, action: tool.PermissionActionAsk},
		{name: "append newline", arguments: `{"session_id":"s","append_newline":true}`, action: tool.PermissionActionAsk},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := scanner.CheckToolPermission(
				context.Background(),
				permissionRequest("workspace_write_stdin", properties, test.arguments),
			)
			require.NoError(t, err)
			require.Equal(t, test.action, decision.Action)
			if test.action == tool.PermissionActionAsk {
				require.Contains(t, decision.Reason, string(RuleInteractiveInput))
			}
		})
	}
}

func TestPermissionPolicyInfersInteractiveSessionBackends(t *testing.T) {
	scanner, err := NewScanner(Policy{})
	require.NoError(t, err)
	properties := map[string]*tool.Schema{"chars": {Type: "string"}}
	for toolName, want := range map[string]Backend{
		"write_stdin":           BackendHost,
		"workspace_write_stdin": BackendWorkspace,
		"skill_write_stdin":     BackendCodeExecutor,
	} {
		t.Run(toolName, func(t *testing.T) {
			input := scanner.scanInputFromPermissionRequest(
				permissionRequest(toolName, properties, `{"chars":"x"}`),
			)
			require.Equal(t, want, input.Backend)
			require.True(t, input.sessionWrite)
			require.NotEmpty(t, input.initialFindings)
			require.Equal(t, RuleInteractiveInput, input.initialFindings[0].RuleID)
		})
	}
}

func TestPermissionPolicyRejectsOversizedArgumentsBeforeDecode(t *testing.T) {
	request := permissionRequest(
		"workspace_exec",
		map[string]*tool.Schema{"command": {Type: "string"}},
		strings.Repeat("x", maxScanInputBytes+1),
	)
	decision, err := newTestScanner(t).CheckToolPermission(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, string(RuleResourceAbuse))
}

func TestPermissionPolicyRejectsTrailingJSON(t *testing.T) {
	scanner := newTestScanner(t)
	for _, arguments := range []string{
		`{"command":"go test ./..."}{"command":"rm -rf /"}`,
		`{"code_blocks":"[{\"language\":\"go\",\"code\":\"package main\"}] []"}`,
	} {
		decision, err := scanner.CheckToolPermission(
			context.Background(),
			permissionRequest(
				"workspace_exec",
				map[string]*tool.Schema{
					"command":     {Type: "string"},
					"code_blocks": {Type: "string"},
				},
				arguments,
			),
		)
		require.NoError(t, err)
		require.Equal(t, tool.PermissionActionDeny, decision.Action)
		require.Contains(t, decision.Reason, string(RuleInvalidInput))
	}
}

func TestPermissionPolicyRejectsNilContext(t *testing.T) {
	decision, err := newTestScanner(t).CheckToolPermission(nil, &tool.PermissionRequest{})
	require.ErrorContains(t, err, "nil context")
	require.Empty(t, decision.Action)
}

func TestPermissionPolicyHonorsCanceledContextBeforeDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := newTestScanner(t).CheckToolPermission(
		ctx,
		permissionRequest(
			"workspace_exec",
			map[string]*tool.Schema{"command": {Type: "string"}},
			strings.Repeat("x", maxScanInputBytes),
		),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, decision.Action)
}

func TestNilScannerPermissionPolicyReturnsError(t *testing.T) {
	var scanner *Scanner
	decision, err := scanner.CheckToolPermission(context.Background(), &tool.PermissionRequest{})
	require.Error(t, err)
	require.Empty(t, decision.Action)
}

func permissionRequest(
	name string,
	properties map[string]*tool.Schema,
	arguments string,
) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		ToolName: name,
		Declaration: &tool.Declaration{
			Name: name,
			InputSchema: &tool.Schema{
				Type:       "object",
				Properties: properties,
			},
		},
		Arguments: []byte(arguments),
	}
}
