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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
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
			name:     "workspace_exec_accepts_case_variant_fields",
			toolName: "workspace_exec",
			args:     []byte(`{"COMMAND":"echo ok","CWD":".","TIMEOUTSEC":10,"TTY":true}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, "echo ok", reqs[0].Command)
				require.Equal(t, ".", reqs[0].Cwd)
				require.Equal(t, 10, reqs[0].TimeoutSec)
				require.True(t, reqs[0].TTY)
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
			name:     "skill_output_collection_paths",
			toolName: "skill_run",
			args:     []byte(`{"command":"true","output_files":[".env"],"outputs":{"globs":["out/*.txt"]},"inputs":[{"from":"host:///etc/passwd","to":"inputs/passwd","mode":"copy"}]}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.ElementsMatch(t, []string{".env", "out/*.txt"}, reqs[0].CollectionPaths)
				require.ElementsMatch(t, []string{"host:///etc/passwd", "inputs/passwd"}, reqs[0].InputPaths)
			},
		},
		{
			name:     "skill_output_collection_legacy_keys",
			toolName: "skill_run",
			args:     []byte(`{"command":"true","outputs":{"Globs":[".env"],"MaxTotalBytes":33554432,"Inline":true}}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, []string{".env"}, reqs[0].CollectionPaths)
				require.Equal(t, int64(32*1024*1024), reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "skill_implicit_output_collection",
			toolName: "skill_run",
			args:     []byte(`{"command":"true"}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, []string{skillDefaultOutputGlob}, reqs[0].CollectionPaths)
				require.Equal(t, int64(skillDefaultOutputTotalBytes), reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "empty_output_files_use_implicit_collection",
			toolName: "skill_exec",
			args:     []byte(`{"command":"true","output_files":[]}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, []string{skillDefaultOutputGlob}, reqs[0].CollectionPaths)
				require.Equal(t, int64(skillDefaultOutputTotalBytes), reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "explicit_empty_outputs_do_not_collect",
			toolName: "skill_run",
			args:     []byte(`{"command":"true","outputs":{}}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Empty(t, reqs[0].CollectionPaths)
				require.Zero(t, reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "skill_editor_text_and_output_limits",
			toolName: "skill_run",
			args: []byte(`{
				"command":"true",
				"editor_text":"password=hunter2",
				"outputs":{
					"globs":["out/*.txt"],
					"max_file_bytes":33554432,
					"max_total_bytes":134217728
				}
			}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, "password=hunter2", reqs[0].EditorText)
				require.Equal(t, int64(128*1024*1024), reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "skill_output_collector_defaults",
			toolName: "skill_exec",
			args:     []byte(`{"command":"true","outputs":{"globs":["out/*.txt"]}}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, int64(skillDefaultOutputTotalBytes), reqs[0].RequestedOutputBytes)
			},
		},
		{
			name:     "skill_explicit_output_total_overrides_default",
			toolName: "skill_exec",
			args:     []byte(`{"command":"true","outputs":{"globs":["out/*.txt"],"max_total_bytes":33554432}}`),
			assert: func(t *testing.T, reqs []ScanRequest) {
				require.Len(t, reqs, 1)
				require.Equal(t, int64(32*1024*1024), reqs[0].RequestedOutputBytes)
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
				require.True(t, reqs[0].sessionSubmit)
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
		{name: "case_variant_duplicate", toolName: "workspace_exec", args: []byte(`{"command":"echo ok","COMMAND":"rm -rf /"}`), err: "duplicate case-insensitive field"},
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

func TestRequestsFromPermissionRequest_UsesRenamedToolSchemas(t *testing.T) {
	hostBase := t.TempDir()
	hostSet, err := hostexec.NewToolSet(
		hostexec.WithName("local"),
		hostexec.WithBaseDir(hostBase),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, hostSet.Close()) })
	namedHostSet := internaltool.NewNamedToolSet(hostSet)
	var hostTool tool.Tool
	for _, candidate := range namedHostSet.Tools(context.Background()) {
		if candidate.Declaration().InputSchema.Properties["workdir"] != nil {
			hostTool = candidate
			break
		}
	}
	require.NotNil(t, hostTool)
	hostReq := &tool.PermissionRequest{
		Tool:      hostTool,
		ToolName:  hostTool.Declaration().Name,
		Arguments: []byte(`{"command":"cat passwd"}`),
	}
	hostScanReqs, err := requestsFromPermissionRequest(hostReq, defaultBackendResolver(hostReq), nil)
	require.NoError(t, err)
	require.Len(t, hostScanReqs, 1)
	require.Equal(t, BackendHost, hostScanReqs[0].Backend)
	require.Equal(t, filepath.Clean(hostBase), filepath.Clean(hostScanReqs[0].Cwd))
	require.True(t, hostScanReqs[0].cwdResolved)
	report, err := MustDefaultScanner(Policy{
		DeniedPaths: []string{filepath.Join(hostBase, "passwd")},
	}).Scan(context.Background(), hostScanReqs[0])
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "path.sensitive_credentials", report.RuleID)

	codeTool := codeexec.NewTool(nil, codeexec.WithName("run_code"))
	codeReq := &tool.PermissionRequest{
		Tool:      codeTool,
		ToolName:  "run_code",
		Arguments: []byte(`{"code_blocks":[{"language":"python","code":"while True: pass"}]}`),
	}
	codeScanReqs, err := requestsFromPermissionRequest(codeReq, defaultBackendResolver(codeReq), nil)
	require.NoError(t, err)
	require.Len(t, codeScanReqs, 1)
	require.Equal(t, BackendCodeExec, codeScanReqs[0].Backend)
	require.Equal(t, "python", codeScanReqs[0].Language)
}

func TestRequestsFromPermissionRequest_DoesNotInferCustomParserFromSchema(t *testing.T) {
	custom := &testPermissionTool{decl: &tool.Declaration{
		Name: "custom_exec",
		InputSchema: &tool.Schema{Properties: map[string]*tool.Schema{
			"command": {Type: "string"},
			"cwd":     {Type: "string"},
		}},
	}}
	reqs, err := requestsFromPermissionRequest(&tool.PermissionRequest{
		Tool:      custom,
		ToolName:  "custom_exec",
		Arguments: []byte(`{"command":"echo ok","cwd":".","url":"https://evil.example"}`),
	}, BackendUnknown, nil)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, BackendUnknown, reqs[0].Backend)
	require.Empty(t, reqs[0].Command)
	require.NotEmpty(t, reqs[0].RawArguments)
}

func TestRequestsFromPermissionRequest_DoesNotInferConcreteToolFromBuiltinName(t *testing.T) {
	for _, toolName := range []string{"exec_command", "workspace_exec"} {
		t.Run(toolName, func(t *testing.T) {
			custom := &testPermissionTool{decl: &tool.Declaration{Name: toolName}}
			req := &tool.PermissionRequest{
				Tool:      custom,
				ToolName:  toolName,
				Arguments: []byte(`{"command":"echo ok","url":"https://evil.example"}`),
			}
			reqs, err := requestsFromPermissionRequest(
				req,
				defaultBackendResolver(req),
				nil,
			)
			require.NoError(t, err)
			require.Len(t, reqs, 1)
			require.Equal(t, BackendUnknown, reqs[0].Backend)
			require.Empty(t, reqs[0].Command)
			require.JSONEq(t, string(req.Arguments), string(reqs[0].RawArguments))

			report, err := MustDefaultScanner(Policy{
				NetworkAllowlist: []string{"allowed.example"},
			}).Scan(context.Background(), reqs[0])
			require.NoError(t, err)
			require.Equal(t, DecisionNeedsHumanReview, report.Decision)
			require.Equal(t, "unknown.requires_review", report.RuleID)
		})
	}
}

func TestRequestsFromPermissionRequest_UsesTypedSafetyParserContract(t *testing.T) {
	parserTool := &testSafetyParserTool{
		decl: &tool.Declaration{Name: "custom_workspace_exec"},
		kind: tool.SafetyParserKindWorkspaceExec,
	}
	req := &tool.PermissionRequest{
		Tool:      parserTool,
		ToolName:  parserTool.Declaration().Name,
		Arguments: []byte(`{"command":"echo ok","cwd":"."}`),
	}
	reqs, err := requestsFromPermissionRequest(req, defaultBackendResolver(req), nil)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, BackendWorkspace, reqs[0].Backend)
	require.Equal(t, "echo ok", reqs[0].Command)
	require.Equal(t, workspaceExecDefaultTimeoutSec, reqs[0].TimeoutSec)
}

func TestRequestsFromToolCall_SkillOutputLimitAffectsDecision(t *testing.T) {
	reqs, err := requestsFromToolCall(
		"skill_run",
		"call-output",
		BackendHost,
		[]byte(`{"command":"true","outputs":{"globs":["out/*.txt"],"max_file_bytes":33554432,"max_total_bytes":134217728}}`),
		nil,
	)
	require.NoError(t, err)
	require.Len(t, reqs, 1)

	for _, tc := range []struct {
		name     string
		maxBytes int64
		decision Decision
		ruleID   string
	}{
		{name: "policy below requested limit", maxBytes: 1 << 20, decision: DecisionAsk, ruleID: "resource.output_limit"},
		{name: "policy above requested limit", maxBytes: 256 << 20, decision: DecisionAllow, ruleID: "evaluation.none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := MustDefaultScanner(Policy{MaxOutputBytes: tc.maxBytes}).Scan(
				context.Background(), reqs[0],
			)
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.ruleID, report.RuleID)
		})
	}
}

type testPermissionTool struct {
	decl *tool.Declaration
}

func (t *testPermissionTool) Declaration() *tool.Declaration { return t.decl }

type testSafetyParserTool struct {
	decl *tool.Declaration
	kind tool.SafetyParserKind
}

func (t *testSafetyParserTool) Declaration() *tool.Declaration { return t.decl }

func (t *testSafetyParserTool) SafetyParserKind() tool.SafetyParserKind {
	return t.kind
}

var _ tool.SafetyParserKindProvider = (*testSafetyParserTool)(nil)

func TestUnmarshalCodeBlocks_RejectsStringifiedInvalidJSON(t *testing.T) {
	_, err := unmarshalCodeBlocks(json.RawMessage(`"not-json"`))
	require.Error(t, err)
}
