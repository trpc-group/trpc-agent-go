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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testAdapter struct {
	input ScanInput
	err   error
}

func (adapter *testAdapter) Adapt(
	_ context.Context,
	_ AdaptRequest,
	_ Binding,
) (ScanInput, error) {
	return adapter.input, adapter.err
}

func TestValidateBindings_ValidMatrix(t *testing.T) {
	custom := &testAdapter{}
	tests := []struct {
		name    string
		binding Binding
	}{
		{name: "workspace exec", binding: BindWorkspaceExec("workspace_exec")},
		{name: "workspace session", binding: BindWorkspaceSession("workspace_write_stdin")},
		{name: "host exec", binding: BindHostExec("exec_command", ".")},
		{name: "host session", binding: BindHostSession("write_stdin")},
		{name: "code generic", binding: BindCodeExec("execute_code", BackendCodeExec)},
		{name: "code local", binding: BindCodeExec("execute_local", BackendLocal)},
		{name: "code container", binding: BindCodeExec("execute_container", BackendContainer)},
		{name: "code e2b", binding: BindRemoteCodeExec("execute_e2b", ProviderE2B)},
		{name: "custom mcp", binding: BindCustom("mcp.exec", BackendMCP, custom)},
		{name: "custom skill", binding: BindCustom("skill.exec", BackendSkill, custom)},
		{name: "custom", binding: BindCustom("custom.exec", BackendCustom, custom)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, validateBinding(test.binding))
		})
	}

	compiled, err := validateBindings([]Binding{
		BindWorkspaceExec("prefix.workspace_exec"),
		BindHostSession("prefix.write_stdin"),
	})
	require.NoError(t, err)
	require.Contains(t, compiled, "prefix.workspace_exec")
	require.Contains(t, compiled, "prefix.write_stdin")
}

func TestValidateBindings_Invalid(t *testing.T) {
	var typedNil *testAdapter
	validAdapter := &testAdapter{}
	tests := []struct {
		name    string
		binding Binding
	}{
		{
			name: "empty name",
			binding: Binding{Kind: ExecutionKindCustom, Backend: BackendCustom,
				Adapter: validAdapter},
		},
		{
			name: "untrimmed name",
			binding: Binding{ToolName: " tool", Kind: ExecutionKindCustom,
				Backend: BackendCustom, Adapter: validAdapter},
		},
		{
			name: "nil adapter",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindCustom,
				Backend: BackendCustom},
		},
		{
			name: "typed nil adapter",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindCustom,
				Backend: BackendCustom, Adapter: typedNil},
		},
		{
			name: "unknown kind",
			binding: Binding{ToolName: "tool", Kind: ExecutionKind("future"),
				Backend: BackendCustom, Adapter: validAdapter},
		},
		{
			name: "workspace backend mismatch",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindWorkspaceExec,
				Backend: BackendHostExec, Adapter: validAdapter},
		},
		{
			name: "host backend mismatch",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindHostSession,
				Backend: BackendWorkspaceExec, Adapter: validAdapter},
		},
		{
			name: "code backend mismatch",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindCodeExec,
				Backend: BackendMCP, Adapter: validAdapter},
		},
		{
			name:    "remote provider missing",
			binding: BindRemoteCodeExec("tool", ""),
		},
		{
			name:    "remote provider invalid",
			binding: BindRemoteCodeExec("tool", "E2B..Cloud"),
		},
		{
			name: "provider on local backend",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindCodeExec,
				Backend: BackendLocal, Provider: ProviderE2B, Adapter: validAdapter},
		},
		{
			name: "custom backend mismatch",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindCustom,
				Backend: BackendLocal, Adapter: validAdapter},
		},
		{
			name: "invalid built-in adapter configuration",
			binding: Binding{ToolName: "tool", Kind: ExecutionKindHostExec,
				Backend: BackendHostExec,
				Adapter: hostExecAdapter{baseDirInvalid: true}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateBinding(test.binding))
		})
	}

	_, err := validateBindings([]Binding{
		BindWorkspaceExec("same"),
		BindHostExec("same", "."),
	})
	require.ErrorContains(t, err, "duplicate")
}

func TestWorkspaceExecAdapter_NormalizesWireContract(t *testing.T) {
	binding := BindWorkspaceExec("named.workspace_exec")
	arguments := []byte(`{
		"command":"go test ./...",
		"cwd":"work/module",
		"env":{"SAFE":"value"},
		"stdin":"input",
		"yield-time_ms":0,
		"yieldMs":999,
		"background":true,
		"timeout":7,
		"timeout_sec":0,
		"timeoutSec":99,
		"tty":false,
		"pty":true
	}`)
	input, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{
		ToolName:   binding.ToolName,
		ToolCallID: "call-1",
		Arguments:  arguments,
		Metadata: tool.ToolMetadata{
			OpenWorld: true,
		},
	}, binding)
	require.NoError(t, err)
	require.Equal(t, "named.workspace_exec", input.ToolName)
	require.Equal(t, "call-1", input.ToolCallID)
	require.Equal(t, OperationExecute, input.Operation)
	require.Equal(t, "go test ./...", input.Command)
	require.Equal(t, "work/module", input.WorkingDir)
	require.Equal(t, map[string]string{"SAFE": "value"}, input.Env)
	require.Equal(t, "input", input.InitialStdin)
	require.Equal(t, 7*time.Second, input.Timeout)
	require.Zero(t, input.Yield)
	require.False(t, input.PTY)
	require.True(t, input.Background)
	require.True(t, input.Interactive)
	require.True(t, input.Metadata.OpenWorld)

	for i := range arguments {
		arguments[i] = 'x'
	}
	require.Equal(t, "go test ./...", input.Command)
	require.Equal(t, "value", input.Env["SAFE"])
}

func TestWorkspaceExecAdapter_DefaultsAndAliases(t *testing.T) {
	binding := BindWorkspaceExec("workspace_exec")
	tests := []struct {
		name    string
		args    string
		timeout time.Duration
		pty     bool
		yield   time.Duration
	}{
		{
			name: "defaults", args: `{"command":"date"}`,
			timeout: defaultWorkspaceTimeout,
		},
		{
			name: "canonical timeout", args: `{"command":"date","timeout_sec":2}`,
			timeout: 2 * time.Second,
		},
		{
			name: "old timeout and pty", args: `{"command":"date","timeoutSec":3,"pty":true}`,
			timeout: 3 * time.Second, pty: true,
			yield: time.Duration(1000) * time.Millisecond,
		},
		{
			name: "interactive yield", args: `{"command":"date","yieldMs":25}`,
			timeout: defaultWorkspaceTimeout, yield: 25 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := requireAdapt(t, binding, test.args)
			require.Equal(t, test.timeout, input.Timeout)
			require.Equal(t, test.pty, input.PTY)
			require.Equal(t, test.yield, input.Yield)
			require.Equal(t, ".", input.WorkingDir)
		})
	}
}

func TestAdapters_SaturateOversizedDurations(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("duration overflow requires a 64-bit int")
	}
	const maxDuration = time.Duration(1<<63 - 1)
	maxInt := int(^uint(0) >> 1)

	workspace := requireAdapt(t, BindWorkspaceExec("workspace_exec"), fmt.Sprintf(
		`{"command":"date","timeout_sec":%d,"yield-time_ms":%d}`,
		maxInt, maxInt,
	))
	require.Equal(t, maxDuration, workspace.Timeout)
	require.Equal(t, maxDuration, workspace.Yield)

	host := requireAdapt(t, BindHostExec("exec_command", "."), fmt.Sprintf(
		`{"command":"date","timeout_sec":%d,"yield-time_ms":%d}`,
		maxInt, maxInt,
	))
	require.Equal(t, maxDuration, host.Timeout)
	require.Equal(t, maxDuration, host.Yield)

	session := requireAdapt(t, BindHostSession("write_stdin"), fmt.Sprintf(
		`{"session_id":"session-1","yield-time_ms":%d}`,
		maxInt,
	))
	require.Equal(t, maxDuration, session.Yield)
}

func TestWorkspaceExecAdapter_RejectsUnsafeCWDAndInvalidInput(t *testing.T) {
	binding := BindWorkspaceExec("workspace_exec")
	tests := []string{
		`null`,
		`{"command":"date","cwd":"../outside"}`,
		`{"command":""}`,
		`{"command":null}`,
		`{"command":"date","timeout_sec":"secret-value"}`,
		`{"command":"secret-value",`,
		`{"command":"date"}{"command":"secret-value"}`,
		`["date"]`,
	}
	for _, arguments := range tests {
		_, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{
			ToolName:  binding.ToolName,
			Arguments: []byte(arguments),
		}, binding)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret-value")
	}
}

func TestWorkspaceExecAdapter_MatchesEncodingJSONCompatibility(t *testing.T) {
	binding := BindWorkspaceExec("workspace_exec")
	input := requireAdapt(t, binding, `{
		"Command":"first","command":"second","unknown":true,
		"timeout_sec":null,"env":{"SAFE":"one","SAFE":"two"}
	}`)
	require.Equal(t, "second", input.Command)
	require.Equal(t, defaultWorkspaceTimeout, input.Timeout)
	require.Equal(t, map[string]string{"SAFE": "two"}, input.Env)
}

func TestWorkspaceSessionAdapter_AliasesAndOperations(t *testing.T) {
	binding := BindWorkspaceSession("workspace_write_stdin")
	poll := requireAdapt(t, binding, `{
		"session_id":"  ","sessionId":" session-1 ",
		"chars":"","append_newline":false,"submit":true,
		"yield-time_ms":0,"yieldMs":900
	}`)
	require.Equal(t, "session-1", poll.SessionID)
	require.Equal(t, OperationSessionPoll, poll.Operation)
	require.False(t, poll.Submit)
	require.Zero(t, poll.Yield)
	require.True(t, poll.Interactive)

	input := requireAdapt(t, binding, `{"session_id":"session-2","chars":"whoami"}`)
	require.Equal(t, OperationSessionInput, input.Operation)
	require.Equal(t, "whoami", input.SessionInput)
	require.Equal(t, defaultSessionWriteYield, input.Yield)

	submit := requireAdapt(t, binding, `{"session_id":"session-3","submit":true}`)
	require.Equal(t, OperationSessionInput, submit.Operation)
	require.True(t, submit.Submit)
}

func TestHostExecAdapter_PathDefaultsAndAliases(t *testing.T) {
	base := t.TempDir()
	binding := BindHostExec("named.exec_command", base)
	input := requireAdapt(t, binding, `{
		"command":"go test ./...","workdir":"sub","env":{"SAFE":"ok"},
		"yield-time_ms":0,"yieldMs":999,"background":true,
		"timeout_sec":0,"timeoutSec":999,"tty":false,"pty":true
	}`)
	require.Equal(t, filepath.Join(base, "sub"), input.WorkingDir)
	require.Zero(t, input.Yield)
	require.Equal(t, defaultHostTimeout, input.Timeout)
	require.False(t, input.PTY)
	require.True(t, input.Background)
	require.True(t, input.Interactive)
	require.Equal(t, map[string]string{"SAFE": "ok"}, input.Env)

	defaults := requireAdapt(t, binding, `{"command":"date"}`)
	require.Equal(t, base, defaults.WorkingDir)
	require.Equal(t, defaultHostYield, defaults.Yield)
	require.Equal(t, defaultHostTimeout, defaults.Timeout)

	aliases := requireAdapt(t, binding, `{
		"command":"date","yieldMs":25,"timeoutSec":4,"pty":true
	}`)
	require.Equal(t, 25*time.Millisecond, aliases.Yield)
	require.Equal(t, 4*time.Second, aliases.Timeout)
	require.True(t, aliases.PTY)

	absolute := filepath.Join(t.TempDir(), "absolute")
	absInput := requireAdapt(t, binding, fmt.Sprintf(
		`{"command":"date","workdir":%q}`, absolute,
	))
	require.Equal(t, absolute, absInput.WorkingDir)
}

func TestHostExecAdapter_DefaultBaseAndHome(t *testing.T) {
	binding := BindHostExec("exec_command", "")
	input := requireAdapt(t, binding, `{"command":"date"}`)
	require.Empty(t, input.WorkingDir)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	homeInput := requireAdapt(t, binding, `{"command":"date","workdir":"~"}`)
	require.Equal(t, home, homeInput.WorkingDir)
}

func TestHostSessionAdapter(t *testing.T) {
	binding := BindHostSession("write_stdin")
	poll := requireAdapt(t, binding, `{"sessionId":"host-1"}`)
	require.Equal(t, "host-1", poll.SessionID)
	require.Equal(t, OperationSessionPoll, poll.Operation)
	require.Equal(t, defaultSessionWriteYield, poll.Yield)

	input := requireAdapt(t, binding, `{
		"session_id":"host-2","chars":"yes","append_newline":true,"yieldMs":3
	}`)
	require.Equal(t, OperationSessionInput, input.Operation)
	require.True(t, input.Submit)
	require.Equal(t, 3*time.Millisecond, input.Yield)
}

func TestCodeExecAdapter_WireFormsAndOrder(t *testing.T) {
	binding := BindCodeExec("execute_code", BackendContainer)
	tests := []struct {
		name string
		args string
		want []CodeBlockInput
	}{
		{
			name: "array",
			args: `{"execution_id":"exec-1","code_blocks":[` +
				`{"language":"python","code":"print(1)"},` +
				`{"language":"go","code":"package main"}]}`,
			want: []CodeBlockInput{
				{Language: "python", Code: "print(1)"},
				{Language: "go", Code: "package main"},
			},
		},
		{
			name: "single object",
			args: `{"execution_id":"exec-1","code_blocks":` +
				`{"language":"python","code":"print(1)"}}`,
			want: []CodeBlockInput{{Language: "python", Code: "print(1)"}},
		},
		{
			name: "one JSON string layer",
			args: `{"execution_id":"exec-1","code_blocks":` +
				`"[{\"language\":\"python\",\"code\":\"print(1)\"}]"}`,
			want: []CodeBlockInput{{Language: "python", Code: "print(1)"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := requireAdapt(t, binding, test.args)
			require.Equal(t, "exec-1", input.ExecutionID)
			require.Equal(t, OperationCodeExecute, input.Operation)
			require.Equal(t, BackendContainer, input.Backend)
			require.Equal(t, test.want, input.CodeBlocks)
			require.Zero(t, input.Timeout)
			require.Zero(t, input.MaxOutputSize)
		})
	}
}

func TestRemoteCodeExecAdapterPreservesProvider(t *testing.T) {
	binding := BindRemoteCodeExec("execute_e2b", ProviderE2B)
	require.NoError(t, validateBinding(binding))

	input := requireAdapt(t, binding,
		`{"code_blocks":{"language":"python","code":"print(1)"}}`)
	require.Equal(t, BackendRemoteSandbox, input.Backend)
	require.Equal(t, ProviderE2B, input.Provider)
}

func TestCodeExecAdapter_RejectsInvalidFormsWithoutEcho(t *testing.T) {
	binding := BindCodeExec("execute_code", BackendLocal)
	tests := []string{
		`{}`,
		`{"code_blocks":null}`,
		`{"code_blocks":[]}`,
		`{"code_blocks":42}`,
		`{"code_blocks":"secret-value"}`,
		`{"code_blocks":"\"[{\\\"language\\\":\\\"python\\\",\\\"code\\\":\\\"secret-value\\\"}]\""}`,
		`{"code_blocks":{"language":"","code":"secret-value"}}`,
		`{"code_blocks":[invalid]}`,
	}
	for _, arguments := range tests {
		_, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{
			ToolName:  binding.ToolName,
			Arguments: []byte(arguments),
		}, binding)
		require.Error(t, err, arguments)
		require.NotContains(t, err.Error(), "secret-value")
	}
}

func TestCodeExecAdapter_MatchesEncodingJSONCompatibility(t *testing.T) {
	binding := BindCodeExec("execute_code", BackendLocal)
	input := requireAdapt(t, binding, `{
		"unknown":true,
		"code_blocks":{"Language":"python","extra":true}
	}`)
	require.Equal(t, []CodeBlockInput{{Language: "python"}}, input.CodeBlocks)
}

func TestAdapters_ContextAndBindingChecks(t *testing.T) {
	binding := BindWorkspaceExec("workspace_exec")
	request := AdaptRequest{
		ToolName:  binding.ToolName,
		Arguments: []byte(`{"command":"date"}`),
	}
	_, err := binding.Adapter.Adapt(nil, request, binding)
	require.ErrorContains(t, err, "nil adapter context")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = binding.Adapter.Adapt(canceled, request, binding)
	require.ErrorIs(t, err, context.Canceled)

	request.ToolName = "other"
	_, err = binding.Adapter.Adapt(context.Background(), request, binding)
	require.ErrorContains(t, err, "tool name do not match")

	request.ToolName = binding.ToolName
	binding.Kind = ExecutionKindHostExec
	binding.Backend = BackendHostExec
	_, err = binding.Adapter.Adapt(context.Background(), request, binding)
	require.ErrorContains(t, err, "kind do not match")
}

func TestBuiltinAdaptersRejectMalformedRequests(t *testing.T) {
	for _, test := range []struct {
		binding Binding
		valid   string
	}{
		{BindWorkspaceExec("workspace_exec"), `{"command":"date"}`},
		{BindWorkspaceSession("workspace_session"), `{"session_id":"id"}`},
		{BindHostExec("exec_command", ""), `{"command":"date"}`},
		{BindHostSession("write_stdin"), `{"session_id":"id"}`},
		{BindCodeExec("execute_code", BackendLocal), `{"code_blocks":[{"language":"python","code":"print(1)"}]}`},
	} {
		request := AdaptRequest{
			ToolName:  test.binding.ToolName,
			Arguments: []byte(test.valid),
		}
		_, err := test.binding.Adapter.Adapt(nil, request, test.binding)
		require.ErrorContains(t, err, "nil adapter context")

		request.ToolName = "other"
		_, err = test.binding.Adapter.Adapt(
			context.Background(), request, test.binding,
		)
		require.ErrorContains(t, err, "tool name do not match")

		request.ToolName = test.binding.ToolName
		for _, arguments := range []string{`{}`, `{"unterminated"`} {
			request.Arguments = []byte(arguments)
			_, err = test.binding.Adapter.Adapt(
				context.Background(), request, test.binding,
			)
			require.Error(t, err)
		}
	}
}

func TestAdaptersNormalizeYieldAliasesAndHomeWorkdir(t *testing.T) {
	workspace := BindWorkspaceExec("workspace_exec")
	for _, arguments := range []string{
		`{"command":"date","background":true,"yield-time_ms":25}`,
		`{"command":"date","background":true,"yieldMs":25}`,
	} {
		input := requireAdapt(t, workspace, arguments)
		require.Equal(t, 25*time.Millisecond, input.Yield)
		require.True(t, input.Interactive)
	}

	host := BindHostExec("exec_command", "")
	input := requireAdapt(t, host, `{"command":"date","workdir":"~/repo"}`)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "repo"), input.WorkingDir)
}

func TestBindCustom_PreservesExplicitAdapter(t *testing.T) {
	want := ScanInput{
		Command:    "custom",
		Args:       []string{"one", "two"},
		Env:        map[string]string{"SAFE": "value"},
		CodeBlocks: []CodeBlockInput{{Language: "custom", Code: "run"}},
	}
	adapter := &testAdapter{input: want}
	binding := BindCustom("skill.run", BackendSkill, adapter)
	require.Same(t, adapter, binding.Adapter)
	require.NoError(t, validateBinding(binding))
	got, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{}, binding)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDecodeArguments_RejectsInvalidJSONWithoutPanic(t *testing.T) {
	malformed := []string{
		``, `[]`, `true`, `{"command":"date"} trailing`,
	}
	for _, arguments := range malformed {
		t.Run(strings.ReplaceAll(arguments, "/", "_"), func(t *testing.T) {
			require.NotPanics(t, func() {
				var input workspaceExecInput
				err := decodeArguments([]byte(arguments), &input)
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-value")
			})
		})
	}
}

func requireAdapt(t *testing.T, binding Binding, arguments string) ScanInput {
	t.Helper()
	input, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{
		ToolName:  binding.ToolName,
		Arguments: []byte(arguments),
	}, binding)
	require.NoError(t, err)
	return input
}

func TestCodeExecAdapter_DefensiveCodeBlockCopy(t *testing.T) {
	blocks := []CodeBlockInput{{Language: "python", Code: "print(1)"}}
	encoded, err := json.Marshal(map[string]any{"code_blocks": blocks})
	require.NoError(t, err)
	binding := BindCodeExec("execute_code", BackendCodeExec)
	input, err := binding.Adapter.Adapt(context.Background(), AdaptRequest{
		ToolName:  binding.ToolName,
		Arguments: encoded,
	}, binding)
	require.NoError(t, err)
	blocks[0].Code = "changed"
	for i := range encoded {
		encoded[i] = 'x'
	}
	require.Equal(t, "print(1)", input.CodeBlocks[0].Code)
}
