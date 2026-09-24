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
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
	skilltool "trpc.group/trpc-go/trpc-agent-go/tool/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

func TestDecodeWorkspaceExec(t *testing.T) {
	execTool := workspaceexec.NewExecTool(nil)

	t.Run("all execution fields", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool:     execTool,
			ToolName: "workspace_exec",
			Arguments: []byte(`{
				"command":"go test ./...","cwd":"sub","env":{"GOFLAGS":"-race"},
				"timeout":17,"background":true,"tty":true
			}`),
			Metadata: tool.ToolMetadata{OpenWorld: true},
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, Request{
			ToolName:       "workspace_exec",
			Backend:        BackendWorkspaceExec,
			Command:        "go test ./...",
			Cwd:            "sub",
			Env:            map[string]string{"GOFLAGS": "-race"},
			TimeoutSeconds: 17,
			Background:     true,
			TTY:            true,
			Metadata:       tool.ToolMetadata{OpenWorld: true},
		}, req.Request)
	})

	t.Run("case folded execution field", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: execTool, Arguments: []byte(`{"Command":"go test"}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "go test", req.Command)
		require.Empty(t, req.additionalArgs)
	})

	for _, tc := range []struct {
		name string
		args string
		want int
	}{
		{name: "timeout", args: `{"command":"go test","timeout":11}`, want: 11},
		{name: "timeout_sec", args: `{"command":"go test","timeout_sec":12}`, want: 12},
		{name: "timeoutSec", args: `{"command":"go test","timeoutSec":13}`, want: 13},
		{name: "timeout_sec wins over timeout", args: `{"command":"go test","timeout":11,"timeout_sec":12}`, want: 12},
		{name: "timeout_sec wins over timeoutSec", args: `{"command":"go test","timeout_sec":12,"timeoutSec":13}`, want: 12},
		{name: "omitted defaults to five minutes", args: `{"command":"go test"}`, want: 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: execTool, Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, req.TimeoutSeconds)
		})
	}

	for _, tc := range []struct {
		name string
		args string
		want bool
	}{
		{name: "tty", args: `{"command":"go test","tty":true}`, want: true},
		{name: "pty", args: `{"command":"go test","pty":true}`, want: true},
		{name: "tty false wins over pty true", args: `{"command":"go test","tty":false,"pty":true}`, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: execTool, Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, req.TTY)
		})
	}

	_, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: execTool, Arguments: []byte(`{"command":12}`),
	})
	require.Error(t, err)
	require.False(t, ok)
}

func TestDecodeWorkspaceExecScansSensitiveStdin(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz123456"
	execTool := workspaceexec.NewExecTool(nil)

	decoded, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: execTool,
		Arguments: mustJSON(t, map[string]any{
			"command": "cat",
			"stdin":   secret,
		}),
	})
	require.NoError(t, err)
	require.True(t, ok)

	report := scanDecodedPermissionRequest(mustGuard(t), decoded)
	require.Equal(t, DecisionNeedsHumanReview, report.Decision)
	require.Equal(t, "sensitive.secret", report.RuleID)
	serialized, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), secret)
}

func TestDecodeWorkspaceExecReviewsExplicitSessionYield(t *testing.T) {
	execTool := workspaceexec.NewExecTool(nil)
	decoded, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: execTool,
		Arguments: []byte(
			`{"command":"go test ./...","yield_time_ms":250}`,
		),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, decoded.needsHumanReview)
	report := scanDecodedPermissionRequest(mustGuard(t), decoded)
	require.Equal(t, DecisionNeedsHumanReview, report.Decision)
	require.Equal(t, "workspace.session", report.RuleID)
}

func TestDecodeWorkspaceExecReviewsNegativeSessionYield(t *testing.T) {
	execTool := workspaceexec.NewExecTool(nil)
	var raw workspaceExecutionArguments
	require.NoError(t, json.Unmarshal(
		[]byte(`{"command":"go test ./...","yield_time_ms":-1}`), &raw,
	))
	require.NotNil(t, raw.YieldTimeMS)
	require.Equal(t, -1, *raw.YieldTimeMS)
	decoded, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: execTool,
		Arguments: []byte(
			`{"command":"go test ./...","yield_time_ms":-1}`,
		),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, decoded.needsHumanReview)
	report := scanDecodedPermissionRequest(mustGuard(t), decoded)
	require.Equal(t, DecisionNeedsHumanReview, report.Decision)
	require.Equal(t, "workspace.session", report.RuleID)
}

func TestDecodeWorkspaceExecBlocksPrivateKeyStdin(t *testing.T) {
	const privateKey = "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"
	execTool := workspaceexec.NewExecTool(nil)

	decoded, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: execTool,
		Arguments: mustJSON(t, map[string]any{
			"command": "cat",
			"stdin":   privateKey,
		}),
	})
	require.NoError(t, err)
	require.True(t, ok)

	report := scanDecodedPermissionRequest(mustGuard(t), decoded)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "sensitive.private_key", report.RuleID)
	require.True(t, report.Blocked)
}

func TestDecodeHostExec(t *testing.T) {
	set, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, set.Close()) })
	execTool := findTool(t, set.Tools(context.Background()), "exec_command")

	t.Run("all supported execution fields", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: execTool, ToolName: "hostexec_exec_command",
			Arguments: []byte(`{
				"command":"printf hi","workdir":"sub","env":{"LANG":"C"},
				"timeoutSec":21,"background":true,"pty":true
			}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendHostExec, req.Backend)
		require.Equal(t, "printf hi", req.Command)
		require.Empty(t, req.Args, "hostexec does not accept a separate args field")
		require.Equal(t, "sub", req.Cwd)
		require.Equal(t, map[string]string{"LANG": "C"}, req.Env)
		require.Equal(t, 21, req.TimeoutSeconds)
		require.True(t, req.Background)
		require.True(t, req.TTY)
	})

	for _, tc := range []struct {
		name string
		args string
		want int
	}{
		{name: "timeout_sec", args: `{"command":"true","timeout_sec":22}`, want: 22},
		{name: "timeoutSec", args: `{"command":"true","timeoutSec":23}`, want: 23},
		{name: "timeout_sec wins over timeoutSec", args: `{"command":"true","timeout_sec":22,"timeoutSec":23}`, want: 22},
		{name: "omitted defaults to thirty minutes", args: `{"command":"true"}`, want: 1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: execTool, Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, req.TimeoutSeconds)
		})
	}

	for _, tc := range []struct {
		name string
		args string
		want bool
	}{
		{name: "tty", args: `{"command":"true","tty":true}`, want: true},
		{name: "pty", args: `{"command":"true","pty":true}`, want: true},
		{name: "tty false wins over pty true", args: `{"command":"true","tty":false,"pty":true}`, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: execTool, Arguments: []byte(tc.args),
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, req.TTY)
		})
	}
}

func TestDecodeHostExecReviewsExplicitSessionYield(t *testing.T) {
	set, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, set.Close()) })
	execTool := findTool(t, set.Tools(context.Background()), "exec_command")

	for _, arguments := range []string{
		`{"command":"sleep 10","timeout_sec":60,"yield_time_ms":1}`,
		`{"command":"sleep 10","timeout_sec":60,"` +
			"yield" + "_time_ms" + `":-1}`,
		`{"command":"sleep 10","timeout_sec":60,"yieldMs":1}`,
	} {
		decoded, ok, decodeErr := requestFromPermissionRequest(
			&tool.PermissionRequest{Tool: execTool, Arguments: []byte(arguments)},
		)
		require.NoError(t, decodeErr)
		require.True(t, ok)
		report := scanDecodedPermissionRequest(mustGuard(t), decoded)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "host.session", report.RuleID)
	}
}

func TestDecodeHostExecReviewsDefaultButNotZeroYield(t *testing.T) {
	set, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, set.Close()) })
	execTool := findTool(t, set.Tools(context.Background()), "exec_command")

	for _, tc := range []struct {
		name      string
		arguments string
		decision  Decision
		ruleID    string
	}{
		{"default yield", `{"command":"true","timeout_sec":60}`,
			DecisionNeedsHumanReview, "host.session"},
		{"zero yield", `{"command":"true","timeout_sec":60,"` +
			"yield" + "_time_ms" + `":0}`, DecisionAllow, "safety.no_findings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, ok, decodeErr := requestFromPermissionRequest(
				&tool.PermissionRequest{Tool: execTool, Arguments: []byte(tc.arguments)},
			)
			require.NoError(t, decodeErr)
			require.True(t, ok)
			report := scanDecodedPermissionRequest(mustGuard(t), decoded)
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.ruleID, report.RuleID)
		})
	}
}

func TestDecodeCanonicalSessionWrites(t *testing.T) {
	hostSet, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, hostSet.Close()) })
	hostWrite := findTool(t, hostSet.Tools(context.Background()), "write_stdin")
	workspaceWrite := workspaceexec.NewWriteStdinTool(
		workspaceexec.NewExecTool(nil),
	)

	for _, tc := range []struct {
		name    string
		tool    tool.Tool
		args    string
		backend Backend
		review  bool
	}{
		{"host poll", hostWrite, `{"session_id":"host-session"}`, BackendHostExec, false},
		{"host chars", hostWrite, `{"session_id":"host-session","chars":"session-fragment"}`, BackendHostExec, true},
		{"host append newline", hostWrite, `{"session_id":"host-session","append_newline":true}`, BackendHostExec, true},
		{"host submit alias", hostWrite, `{"sessionId":"host-session","submit":true}`, BackendHostExec, true},
		{"workspace poll", workspaceWrite, `{"session_id":"workspace-session","chars":"","submit":false}`, BackendWorkspaceExec, false},
		{"workspace chars", workspaceWrite, `{"session_id":"workspace-session","chars":"session-fragment"}`, BackendWorkspaceExec, true},
		{"workspace append newline", workspaceWrite, `{"session_id":"workspace-session","append_newline":true}`, BackendWorkspaceExec, true},
		{"workspace submit alias", workspaceWrite, `{"sessionId":"workspace-session","submit":true}`, BackendWorkspaceExec, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, scan, decodeErr := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: tc.tool, Arguments: []byte(tc.args),
			})
			require.NoError(t, decodeErr)
			require.True(t, scan)
			require.Equal(t, tc.backend, decoded.Backend)
			require.Equal(t, tc.review, decoded.needsHumanReview)
			require.Empty(t, decoded.Command, "session input is not a shell command")
			require.Empty(t, decoded.RawArguments, "session input must not be retained")
		})
	}

	namedSet := internaltool.NewNamedToolSet(&decodeToolSet{
		name: "pref", tools: []tool.Tool{hostWrite, workspaceWrite},
	})
	for _, named := range namedSet.Tools(context.Background()) {
		decoded, scan, decodeErr := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: named, ToolName: named.Declaration().Name,
			Arguments: []byte(`{"session_id":"session","chars":"continue"}`),
		})
		require.NoError(t, decodeErr)
		require.True(t, scan)
		require.True(t, decoded.needsHumanReview, named.Declaration().Name)
	}
}

func TestDecodeSessionWriteDurationAndBooleanPrecedence(t *testing.T) {
	hostSet, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, hostSet.Close()) })
	hostWrite := findTool(t, hostSet.Tools(context.Background()), "write_stdin")
	workspaceWrite := workspaceexec.NewWriteStdinTool(
		workspaceexec.NewExecTool(nil),
	)
	const (
		ownerYieldField  = "yield" + "_time_ms"
		mixedYieldField  = "yield" + "-time_ms"
		dashedYieldField = "yield" + "-time-ms"
	)

	for _, owner := range []struct {
		name    string
		tool    tool.Tool
		backend Backend
	}{
		{"host", hostWrite, BackendHostExec},
		{"workspace", workspaceWrite, BackendWorkspaceExec},
	} {
		for _, tc := range []struct {
			name        string
			args        string
			wantSeconds int
			wantReview  bool
		}{
			{"default poll", `{"session_id":"session"}`, 1, false},
			{"owner spelling rounds up", sessionDurationArguments(ownerYieldField, "1001"), 2, false},
			{"unsupported mixed spelling is ignored", sessionDurationArguments(mixedYieldField, "1001"), 1, false},
			{"unsupported dashed spelling is ignored", sessionDurationArguments(dashedYieldField, "1001"), 1, false},
			{"duration alias", `{"session_id":"session","yieldMs":2001}`, 3, false},
			{"owner zero wins alias", sessionDurationAndAliasArguments(ownerYieldField, "0", "600001"), 0, false},
			{"null owner selects alias", sessionDurationAndAliasArguments(ownerYieldField, "null", "600001"), 601, false},
			{"null alias does not replace owner", sessionDurationAndAliasArguments(ownerYieldField, "0", "null"), 0, false},
			{"unsupported mixed cannot hide alias", sessionDurationAndAliasArguments(mixedYieldField, "0", "600001"), 601, false},
			{"unsupported dashed cannot hide alias", sessionDurationAndAliasArguments(dashedYieldField, "0", "600001"), 601, false},
			{"owner negative uses default", sessionDurationAndAliasArguments(ownerYieldField, "-1", "600001"), 1, false},
			{"owner long wins zero alias", sessionDurationAndAliasArguments(ownerYieldField, "600001", "0"), 601, false},
			{"primary false wins", `{"session_id":"session","append_newline":false,"submit":true}`, 1, false},
			{"primary true wins", `{"session_id":"session","append_newline":true,"submit":false}`, 1, true},
			{"submit alias", `{"session_id":"session","submit":true}`, 1, true},
		} {
			t.Run(owner.name+" "+tc.name, func(t *testing.T) {
				decoded, scan, decodeErr := requestFromPermissionRequest(
					&tool.PermissionRequest{Tool: owner.tool, Arguments: []byte(tc.args)},
				)
				require.NoError(t, decodeErr)
				require.True(t, scan)
				require.Equal(t, owner.backend, decoded.Backend)
				require.Equal(t, tc.wantSeconds, decoded.TimeoutSeconds)
				require.Equal(t, tc.wantReview, decoded.needsHumanReview)
			})
		}
	}

	_, scan, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: hostWrite,
		Arguments: []byte(sessionDurationArguments(
			ownerYieldField,
			"9223372036854775808",
		)),
	})
	require.Error(t, err)
	require.False(t, scan)

	for _, tc := range []struct {
		name  string
		alias string
	}{
		{"overflow", "9223372036854775808"},
		{"string", `"1000"`},
		{"float", "1.5"},
		{"object", `{}`},
	} {
		t.Run("unselected alias "+tc.name, func(t *testing.T) {
			_, invalidScan, invalidErr := requestFromPermissionRequest(
				&tool.PermissionRequest{
					Tool: hostWrite,
					Arguments: []byte(sessionDurationAndAliasArguments(
						ownerYieldField,
						"0",
						tc.alias,
					)),
				},
			)
			require.Error(t, invalidErr)
			require.False(t, invalidScan)
		})
	}
}

func sessionDurationArguments(field, value string) string {
	return `{"session_id":"session","` + field + `":` + value + `}`
}

func sessionDurationAndAliasArguments(field, value, alias string) string {
	return `{"session_id":"session","` + field + `":` + value +
		`,"yieldMs":` + alias + `}`
}

func TestDecodeCodeExec(t *testing.T) {
	execTool := codeexec.NewTool(nil)
	wantMany := []codeexecutor.CodeBlock{
		{Language: "python", Code: "print(1)"},
		{Language: "bash", Code: "printf hi"},
	}
	encodedMany, err := json.Marshal(wantMany)
	require.NoError(t, err)
	encodedOne, err := json.Marshal(wantMany[0])
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		args []byte
		want []codeexecutor.CodeBlock
	}{
		{name: "direct array", args: []byte(`{"execution_id":"run-1","code_blocks":[{"language":"python","code":"print(1)"},{"language":"bash","code":"printf hi"}]}`), want: wantMany},
		{name: "single object", args: []byte(`{"code_blocks":{"language":"python","code":"print(1)"}}`), want: wantMany[:1]},
		{name: "encoded array", args: mustJSON(t, map[string]any{"code_blocks": string(encodedMany)}), want: wantMany},
		{name: "encoded object", args: mustJSON(t, map[string]any{"code_blocks": string(encodedOne)}), want: wantMany[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: execTool, ToolName: "execute_code", Arguments: tc.args,
			})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, BackendCodeExec, req.Backend)
			require.Equal(t, tc.want, req.CodeBlocks)
			require.Empty(t, req.additionalArgs)
		})
	}

	for _, args := range [][]byte{
		[]byte(`{"code_blocks":"not json"}`),
		[]byte(`{"code_blocks":17}`),
		[]byte(`{"code_blocks":[}`),
		[]byte(`{}`),
	} {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: execTool, Arguments: args,
		})
		require.Error(t, err)
		require.False(t, ok)
		require.Empty(t, req)
	}
}

func TestDecodeSkillExecution(t *testing.T) {
	runTool := skilltool.NewRunTool(nil, nil)
	execTool := skilltool.NewExecTool(runTool)
	writeTool := skilltool.NewWriteStdinTool(execTool)

	t.Run("skill_run", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: runTool,
			Arguments: []byte(`{"skill":"demo","command":"go test","cwd":"scripts",` +
				`"env":{"LANG":"C"},"save_as_artifacts":true,` +
				`"omit_inline_content":true,"artifact_prefix":"reports"}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Equal(t, "go test", req.Command)
		require.Equal(t, "scripts", req.Cwd)
		require.Equal(t, map[string]string{"LANG": "C"}, req.Env)
		require.Equal(t, 300, req.TimeoutSeconds)
		require.Empty(t, req.additionalArgs)
	})

	t.Run("skill input source", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: runTool,
			Arguments: []byte(`{"skill":"demo","command":"cat inputs/key",` +
				`"inputs":[{"from":"host:///root/.ssh/id_rsa",` +
				`"to":"inputs/key"}]}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		report := scanDecodedPermissionRequest(mustGuard(t), req)
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, "sensitive.path", report.RuleID)
	})

	for _, tc := range []struct {
		name      string
		arguments string
	}{
		{"skill stdin", `{"skill":"demo","command":"go test",` +
			`"stdin":"token=sk-secret-value"}`},
		{"skill editor text", `{"skill":"demo","command":"go test",` +
			`"editor_text":"password=hunter2"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: runTool, Arguments: []byte(tc.arguments),
			})
			require.NoError(t, err)
			require.True(t, ok)
			report := scanDecodedPermissionRequest(mustGuard(t), req)
			require.Equal(t, DecisionNeedsHumanReview, report.Decision)
			require.Equal(t, "sensitive.secret", report.RuleID)
			require.True(t, report.Redacted)
			encoded, marshalErr := json.Marshal(report)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(encoded), "sk-secret-value")
			require.NotContains(t, string(encoded), "hunter2")
		})
	}

	t.Run("skill_exec", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: execTool,
			Arguments: []byte(`{"skill":"demo","command":"python app.py",` +
				`"timeout":19,"tty":true,"poll_lines":25}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Equal(t, "python app.py", req.Command)
		require.Equal(t, 19, req.TimeoutSeconds)
		require.True(t, req.TTY)
		require.Empty(t, req.additionalArgs)
		report := scanDecodedPermissionRequest(mustGuard(t), req)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "skill.tty", report.RuleID)
	})

	t.Run("skill_exec without tty", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: execTool,
			Arguments: []byte(
				`{"skill":"demo","command":"python app.py","tty":false}`,
			),
		})
		require.NoError(t, err)
		require.True(t, ok)
		report := scanDecodedPermissionRequest(mustGuard(t), req)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "skill.session", report.RuleID)
	})

	t.Run("empty write stdin poll", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool:      writeTool,
			Arguments: []byte(`{"session_id":"session-1","chars":"","submit":false}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Empty(t, req.Command)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.False(t, req.needsHumanReview)
		require.Equal(t, DecisionAllow, scanDecodedPermissionRequest(mustGuard(t), req).Decision)
	})

	t.Run("submitted newline is session input", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool:      writeTool,
			Arguments: []byte(`{"session_id":"session-1","submit":true}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.True(t, req.needsHumanReview)
		require.Empty(t, req.Command)
		require.Equal(t, DecisionNeedsHumanReview,
			scanDecodedPermissionRequest(mustGuard(t), req).Decision)
	})

	t.Run("non-empty write stdin requires review", func(t *testing.T) {
		const fragment = "session-fragment-needle $()"
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: writeTool,
			Arguments: mustJSON(t, map[string]any{
				"session_id": "session-1", "chars": fragment,
			}),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Empty(t, req.Command, "session text is not a shell command")
		require.Empty(t, req.RawArguments, "session text is not retained as raw arguments")
		require.True(t, req.needsHumanReview)

		policy := DefaultPolicy()
		policy.AllowedCommands = []string{"echo"}
		guard, guardErr := NewGuard(policy)
		require.NoError(t, guardErr)
		report := scanDecodedPermissionRequest(guard, req)
		require.Equal(t, DecisionNeedsHumanReview, report.Decision)
		require.Equal(t, "session.interactive_input", report.RuleID)
		serialized, marshalErr := json.Marshal(report)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(serialized), fragment)
		require.NotContains(t, strings.Join(report.Evidence, " "), fragment)
	})

	t.Run("named prefix cannot bypass write review", func(t *testing.T) {
		set := internaltool.NewNamedToolSet(&decodeToolSet{
			name: "pref", tools: []tool.Tool{writeTool},
		})
		named := set.Tools(context.Background())[0]
		require.Equal(t, "pref_skill_write_stdin", named.Declaration().Name)

		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool: named, ToolName: named.Declaration().Name,
			Arguments: []byte(`{"session_id":"session-1","chars":"continue"}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.True(t, req.needsHumanReview)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Equal(t, DecisionNeedsHumanReview,
			scanDecodedPermissionRequest(mustGuard(t), req).Decision)
	})
}

func TestDecodeUsesSemanticToolBeforeModelDeclaration(t *testing.T) {
	base := workspaceexec.NewExecTool(nil)
	set := internaltool.NewNamedToolSet(&decodeToolSet{name: "pref", tools: []tool.Tool{base}})
	named := set.Tools(context.Background())[0]
	require.Equal(t, "pref_workspace_exec", named.Declaration().Name)
	require.Same(t, base, internaltool.ResolveSemantic(named))

	req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool:     named,
		ToolName: "pref_workspace_exec",
		Declaration: &tool.Declaration{
			Name: "exec_command",
		},
		Arguments: []byte(`{"command":"go test"}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "pref_workspace_exec", req.ToolName)
	require.Equal(t, BackendWorkspaceExec, req.Backend)
}

func TestDecodeFallsBackToDeclarationWithoutSemanticTool(t *testing.T) {
	req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		ToolName: "pref_workspace_exec",
		Declaration: &tool.Declaration{
			Name: "workspace_exec",
		},
		Arguments: []byte(`{"command":"go test"}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, BackendWorkspaceExec, req.Backend)

	req, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"go test"}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, BackendHostExec, req.Backend)
}

func TestDecodeUnknownOpenWorldRequest(t *testing.T) {
	raw := []byte(`{"outer":{"encoded":"{\"command\":\"rm -rf /\"}"},"url":"https://example.test"}`)
	req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool:      declarationOnlyTool("remote_action", "payload"),
		ToolName:  "remote_action",
		Arguments: raw,
		Metadata:  tool.ToolMetadata{OpenWorld: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, BackendUnknown, req.Backend)
	require.JSONEq(t, string(raw), string(req.RawArguments))
	require.Equal(t, DecisionDeny,
		scanDecodedPermissionRequest(mustGuard(t), req).Decision)

	_, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool:      declarationOnlyTool("remote_action", "payload"),
		Arguments: []byte(`{"command":"unterminated}`),
	})
	require.Error(t, err)
	require.False(t, ok)
}

func TestDecodeBuiltInNameCollisionScansAdditionalArguments(t *testing.T) {
	decoded, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: declarationOnlyTool("workspace_exec", "command", "url"),
		Arguments: []byte(
			`{"command":"go test ./...","url":"https://evil.example/payload"}`,
		),
		Metadata: tool.ToolMetadata{OpenWorld: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, decoded.RawArguments)

	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	report := scanDecodedPermissionRequest(guard, decoded)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "network.destination", report.RuleID)

	decoded, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: declarationOnlyTool("workspace_exec", "command", "payload"),
		Arguments: []byte(
			`{"command":"go test ./...","Payload":"run an external action"}`,
		),
		Metadata: tool.ToolMetadata{OpenWorld: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	report = scanDecodedPermissionRequest(mustGuard(t), decoded)
	require.Equal(t, DecisionNeedsHumanReview, report.Decision)
	require.Equal(t, "arguments.additional_fields", report.RuleID)

	openTool := declarationOnlyTool("workspace_exec", "command")
	openTool.Declaration().InputSchema.AdditionalProperties = true
	decoded, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: openTool,
		Arguments: []byte(
			`{"command":"go test ./...","url":"https://evil.example/payload"}`,
		),
		Metadata: tool.ToolMetadata{OpenWorld: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	report = scanDecodedPermissionRequest(guard, decoded)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "network.destination", report.RuleID)

	schemaTool := declarationOnlyTool("workspace_exec", "command")
	schemaTool.Declaration().InputSchema.AdditionalProperties = &tool.Schema{
		Type: "string",
	}
	decoded, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: schemaTool,
		Arguments: []byte(
			`{"command":"go test ./...","url":"https://evil.example/payload"}`,
		),
		Metadata: tool.ToolMetadata{OpenWorld: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	report = scanDecodedPermissionRequest(guard, decoded)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "network.destination", report.RuleID)
}

func TestDecodeNilAndClosedWorldRequests(t *testing.T) {
	req, ok, err := requestFromPermissionRequest(nil)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, req)

	calculator := closedCalculatorTool()
	req, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool:      calculator,
		Arguments: []byte(`{"left":1,"right":2}`),
		Metadata:  tool.ToolMetadata{ReadOnly: true},
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, req)

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "extra execution property", raw: `{"left":1,"right":2,"command":"rm -rf /"}`},
		{name: "missing required property", raw: `{"left":1}`},
		{name: "mismatched property type", raw: `{"left":"one","right":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, scan, decodeErr := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: calculator, Arguments: []byte(tc.raw),
				Metadata: tool.ToolMetadata{ReadOnly: true},
			})
			require.NoError(t, decodeErr)
			require.True(t, scan)
			require.Equal(t, BackendUnknown, decoded.Backend)
			if tc.name == "extra execution property" {
				require.Equal(t, DecisionDeny,
					scanDecodedPermissionRequest(mustGuard(t), decoded).Decision)
			}
		})
	}

	req, ok, err = requestFromPermissionRequest(&tool.PermissionRequest{
		Tool:      declarationOnlyTool("read_payload", "payload"),
		Arguments: []byte(`{"payload":{"command":"rm -rf /"}}`),
		Metadata:  tool.ToolMetadata{ReadOnly: true},
	})
	require.NoError(t, err)
	require.True(t, ok, "an open-ended payload is not demonstrably closed-world")
	require.Equal(t, BackendUnknown, req.Backend)
}

func TestDecodeClosedWorldCompositeValues(t *testing.T) {
	reader := &decodeDeclarationTool{declaration: &tool.Declaration{
		Name: "typed_reader",
		InputSchema: &tool.Schema{
			Type: "object",
			Required: []string{
				"ids", "count", "ratio", "enabled", "note", "label",
			},
			AdditionalProperties: false,
			Properties: map[string]*tool.Schema{
				"ids": {
					Type:  "array",
					Items: &tool.Schema{Type: "integer"},
				},
				"count":   {Type: "integer"},
				"ratio":   {Type: "number"},
				"enabled": {Type: "boolean"},
				"note":    {Type: "null"},
				"label":   {Type: "string"},
			},
		},
	}}
	metadata := tool.ToolMetadata{ReadOnly: true}

	decoded, scan, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: reader,
		Arguments: []byte(
			`{"ids":[1,2],"count":3,"ratio":1.5,` +
				`"enabled":true,"note":null,"label":"ok"}`,
		),
		Metadata: metadata,
	})
	require.NoError(t, err)
	require.False(t, scan)
	require.Empty(t, decoded)

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "array type mismatch",
			raw:  `{"ids":1,"count":3,"ratio":1.5,"enabled":true,"note":null,"label":"ok"}`,
		},
		{
			name: "array element mismatch",
			raw:  `{"ids":[1.5],"count":3,"ratio":1.5,"enabled":true,"note":null,"label":"ok"}`,
		},
		{
			name: "integer type mismatch",
			raw:  `{"ids":[1],"count":"3","ratio":1.5,"enabled":true,"note":null,"label":"ok"}`,
		},
		{
			name: "number type mismatch",
			raw:  `{"ids":[1],"count":3,"ratio":"1.5","enabled":true,"note":null,"label":"ok"}`,
		},
		{
			name: "boolean type mismatch",
			raw:  `{"ids":[1],"count":3,"ratio":1.5,"enabled":"true","note":null,"label":"ok"}`,
		},
		{
			name: "null type mismatch",
			raw:  `{"ids":[1],"count":3,"ratio":1.5,"enabled":true,"note":"value","label":"ok"}`,
		},
		{
			name: "string type mismatch",
			raw:  `{"ids":[1],"count":3,"ratio":1.5,"enabled":true,"note":null,"label":1}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, scan, err := requestFromPermissionRequest(&tool.PermissionRequest{
				Tool: reader, Arguments: []byte(tc.raw), Metadata: metadata,
			})
			require.NoError(t, err)
			require.True(t, scan)
			require.Equal(t, BackendUnknown, decoded.Backend)
		})
	}
}

func TestDecodeAdversarialClosedWorldSchemasFailSafe(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		cyclic := &tool.Schema{
			Type:                 "object",
			AdditionalProperties: false,
		}
		cyclic.Properties = map[string]*tool.Schema{"child": cyclic}

		requireClosedSchemaScans(t, "cyclic_reader", cyclic)
	})

	t.Run("excessive depth", func(t *testing.T) {
		root := &tool.Schema{
			Type:                 "object",
			AdditionalProperties: false,
		}
		current := root
		for i := 0; i < 101; i++ {
			child := &tool.Schema{
				Type:                 "object",
				AdditionalProperties: false,
			}
			current.Properties = map[string]*tool.Schema{"child": child}
			current = child
		}

		requireClosedSchemaScans(t, "deep_reader", root)
	})
}

func requireClosedSchemaScans(t *testing.T, name string, schema *tool.Schema) {
	t.Helper()
	_, scan, err := requestFromPermissionRequest(&tool.PermissionRequest{
		Tool: &decodeDeclarationTool{declaration: &tool.Declaration{
			Name:        name,
			InputSchema: schema,
		}},
		Arguments: []byte(`{}`),
		Metadata:  tool.ToolMetadata{ReadOnly: true},
	})
	require.NoError(t, err)
	require.True(t, scan, "adversarial schema must not bypass safety scanning")
}

func closedCalculatorTool() tool.Tool {
	return &decodeDeclarationTool{declaration: &tool.Declaration{
		Name: "calculator",
		InputSchema: &tool.Schema{
			Type:                 "object",
			Required:             []string{"left", "right"},
			AdditionalProperties: false,
			Properties: map[string]*tool.Schema{
				"left":  {Type: "number"},
				"right": {Type: "number"},
			},
		},
	}}
}

type decodeDeclarationTool struct {
	declaration *tool.Declaration
}

func declarationOnlyTool(name string, properties ...string) tool.Tool {
	props := make(map[string]*tool.Schema, len(properties))
	for _, property := range properties {
		props[property] = &tool.Schema{}
	}
	return &decodeDeclarationTool{declaration: &tool.Declaration{
		Name: name,
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: props,
		},
	}}
}

func (t *decodeDeclarationTool) Declaration() *tool.Declaration {
	return t.declaration
}

type decodeToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *decodeToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s *decodeToolSet) Close() error                      { return nil }
func (s *decodeToolSet) Name() string                      { return s.name }

func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, candidate := range tools {
		if candidate.Declaration().Name == name {
			return candidate
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func mustGuard(t *testing.T) *Guard {
	t.Helper()
	guard, err := NewGuard(Policy{})
	require.NoError(t, err)
	return guard
}
