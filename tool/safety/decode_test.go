//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
		{name: "direct array", args: []byte(`{"code_blocks":[{"language":"python","code":"print(1)"},{"language":"bash","code":"printf hi"}]}`), want: wantMany},
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
			Tool:      runTool,
			Arguments: []byte(`{"skill":"demo","command":"go test","cwd":"scripts","env":{"LANG":"C"}}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Equal(t, "go test", req.Command)
		require.Equal(t, "scripts", req.Cwd)
		require.Equal(t, map[string]string{"LANG": "C"}, req.Env)
		require.Equal(t, 300, req.TimeoutSeconds)
	})

	t.Run("skill_exec", func(t *testing.T) {
		req, ok, err := requestFromPermissionRequest(&tool.PermissionRequest{
			Tool:      execTool,
			Arguments: []byte(`{"skill":"demo","command":"python app.py","timeout":19,"tty":true}`),
		})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, BackendWorkspaceExec, req.Backend)
		require.Equal(t, "python app.py", req.Command)
		require.Equal(t, 19, req.TimeoutSeconds)
		require.True(t, req.TTY)
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
