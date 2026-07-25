//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCovercore_DecodeRequestEmptyArguments covers the zero-argument fast
// path.
func TestCovercore_DecodeRequestEmptyArguments(t *testing.T) {
	in, err := decodeRequest("workspace_exec", nil, newProfileRegistry())
	require.NoError(t, err)
	require.Equal(t, "workspace_exec", in.ToolName)
	require.Empty(t, in.Command)
}

// TestCovercore_DecodeRequestInvalidJSON covers the known-tool malformed
// JSON error path.
func TestCovercore_DecodeRequestInvalidJSON(t *testing.T) {
	_, err := decodeRequest("workspace_exec", []byte(`{not json`), newProfileRegistry())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid arguments")
}

// TestCovercore_DecodeRequestMissingRequiredField covers the required
// command-field error.
func TestCovercore_DecodeRequestMissingRequiredField(t *testing.T) {
	_, err := decodeRequest("workspace_exec", []byte(`{"cwd":"/tmp"}`), newProfileRegistry())
	require.Error(t, err)
	require.Contains(t, err.Error(), `"command" is required`)
}

// TestCovercore_DecodeRequestWrongCodeBlocksType covers the required
// code-block decode error path.
func TestCovercore_DecodeRequestWrongCodeBlocksType(t *testing.T) {
	_, err := decodeRequest("execute_code", []byte(`{"code_blocks":42}`), newProfileRegistry())
	require.Error(t, err)
	require.Contains(t, err.Error(), "code_blocks")
}

// TestCovercore_DecodeRequestEnvError covers the optional-field error
// propagation for a malformed env map.
func TestCovercore_DecodeRequestEnvError(t *testing.T) {
	_, err := decodeRequest("workspace_exec",
		[]byte(`{"command":"ls","env":"not-an-object"}`), newProfileRegistry())
	require.Error(t, err)
	require.Contains(t, err.Error(), "env")
}

// TestCovercore_DecodeUnknownTool covers the unknown-tool decode shapes.
func TestCovercore_DecodeUnknownTool(t *testing.T) {
	reg := newProfileRegistry()

	// Malformed JSON from an unknown tool errors out.
	_, err := decodeRequest("mystery_tool", []byte(`{broken`), reg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed arguments")

	// No command surface: the input passes through unchanged.
	in, err := decodeRequest("mystery_tool", []byte(`{"query":"hello"}`), reg)
	require.NoError(t, err)
	require.Empty(t, in.Command)

	// Command-shaped unknown tool: the command is captured with an
	// unknown backend.
	in, err = decodeRequest("mystery_tool", []byte(`{"command":"ls"}`), reg)
	require.NoError(t, err)
	require.Equal(t, BackendUnknown, in.Backend)
	require.Equal(t, "ls", in.Command)

	// A present but unsupported command shape requires review.
	_, err = decodeRequest("mystery_tool", []byte(`{"command":42}`), reg)
	require.ErrorContains(t, err, "must be a string")

	_, err = decodeRequest("mystery_tool", []byte(`{"command":""}`), reg)
	require.ErrorContains(t, err, "must not be empty")

	in, err = decodeRequest("mystery_tool", []byte(`{"script":"rm -rf /"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "rm -rf /", in.Command)

	in, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"code":"print(1)","language":"python"}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{
		Language: "python",
		Code:     "print(1)",
	}}, in.CodeBlocks)

	in, err = decodeRequest(
		"verify_otp",
		[]byte(`{"code":"123456"}`),
		reg,
	)
	require.NoError(t, err)
	require.Empty(t, in.CodeBlocks)

	in, err = decodeRequest("mystery_tool", []byte(`{"argv":["rm","-rf","/"]}`), reg)
	require.NoError(t, err)
	require.Equal(t, []string{"rm", "-rf", "/"}, in.Args)

	_, err = decodeRequest("mystery_tool", []byte(`{"argv":["rm",42]}`), reg)
	require.ErrorContains(t, err, "item 1")

	_, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"command":"echo safe","script":"rm -rf /"}`),
		reg,
	)
	require.ErrorContains(t, err, "multiple execution fields")

	in, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"command":"ls","shell":true}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "ls", in.Command)

	in, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"command":"ls","code":null}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "ls", in.Command)

	in, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"query":"hello","shell":true}`),
		reg,
	)
	require.NoError(t, err)
	require.Empty(t, in.Command)

	_, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"command":"echo ok","argv":"rm -rf /"}`),
		reg,
	)
	require.ErrorContains(t, err, "argv")

	_, err = decodeRequest(
		"mystery_tool",
		[]byte(`{"code":"print(1)","language":42}`),
		reg,
	)
	require.ErrorContains(t, err, "language")
}

// TestCovercore_DecodeSessionFields covers the session-tool argument
// shapes.
func TestCovercore_DecodeSessionFields(t *testing.T) {
	reg := newProfileRegistry()

	in, err := decodeRequest("write_stdin",
		[]byte(`{"session_id":"s1","chars":"ls -la\n"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "s1", in.SessionID)
	require.Equal(t, "ls -la\n", in.SessionInput)

	in, err = decodeRequest("workspace_write_stdin",
		[]byte(`{"sessionId":"s2","chars":"pwd"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "s2", in.SessionID)
	require.Equal(t, "pwd", in.SessionInput)

	in, err = decodeRequest("write_stdin",
		[]byte(`{"session_id":" ","sessionId":" s3 ","chars":"pwd"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "s3", in.SessionID)

	in, err = decodeRequest("workspace_exec",
		[]byte(`{"command":"python","stdin":"print(1)"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "print(1)", in.SessionInput)

	in, err = decodeRequest("kill_session", []byte(`{"session_id":"s3"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "s3", in.SessionID)
	require.Empty(t, in.SessionInput)

	in, err = decodeRequest("workspace_kill_session", []byte(`{"sessionId":"s4"}`), reg)
	require.NoError(t, err)
	require.Equal(t, "s4", in.SessionID)
}

// TestCovercore_DecodeOptionalFields covers cwd, env, timeout, background,
// and PTY decoding.
func TestCovercore_DecodeOptionalFields(t *testing.T) {
	reg := newProfileRegistry()

	in, err := decodeRequest("workspace_exec", []byte(`{
		"command": "ls",
		"cwd": "/tmp",
		"env": {"PATH": "/usr/bin", "DEBUG": "1"},
		"timeout": 12,
		"background": true,
		"pty": true
	}`), reg)
	require.NoError(t, err)
	require.Equal(t, "/tmp", in.Cwd)
	require.Equal(t, map[string]string{"PATH": "/usr/bin", "DEBUG": "1"}, in.Env)
	require.Equal(t, 12*time.Second, in.Timeout)
	require.True(t, in.Background)
	require.True(t, in.PTY)

	// A non-string cwd is ignored.
	in, err = decodeRequest("workspace_exec",
		[]byte(`{"command":"ls","cwd":42}`), reg)
	require.NoError(t, err)
	require.Empty(t, in.Cwd)

	// A non-bool background flag is ignored.
	in, err = decodeRequest("workspace_exec",
		[]byte(`{"command":"ls","background":"yes"}`), reg)
	require.NoError(t, err)
	require.False(t, in.Background)
}

func TestCovercore_DefaultProfileTimeoutParity(t *testing.T) {
	reg := newProfileRegistry()
	workspace, ok := reg.lookup("workspace_exec")
	require.True(t, ok)
	require.Equal(t,
		[]string{"timeout_sec", "timeoutSec", "timeout"},
		workspace.TimeoutFields,
	)
	host, ok := reg.lookup("exec_command")
	require.True(t, ok)
	require.Equal(t, 30*time.Minute, host.DefaultTimeout)
	code, ok := reg.lookup("execute_code")
	require.True(t, ok)
	require.Zero(t, code.DefaultTimeout)
	skillRun, ok := reg.lookup("skill_run")
	require.True(t, ok)
	require.Equal(t, "command", skillRun.CommandField)
	require.Equal(t, "stdin", skillRun.SessionInputField)
	skillExec, ok := reg.lookup("skill_exec")
	require.True(t, ok)
	require.False(t, skillExec.CreatesSession)
	require.Equal(t, "stdin", skillExec.SessionInputField)
	skillWrite, ok := reg.lookup("skill_write_stdin")
	require.True(t, ok)
	require.Equal(t, []string{"session_id"}, skillWrite.SessionIDFields)
	require.Equal(t, "chars", skillWrite.SessionInputField)
	require.Equal(t, []string{"submit"}, skillWrite.SessionSubmitFields)
	skillKill, ok := reg.lookup("skill_kill_session")
	require.True(t, ok)
	require.True(t, skillKill.TerminatesSession)

	in, err := decodeRequest(
		"workspace_exec",
		[]byte(`{"command":"ls","timeout":10,"timeout_sec":3600}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, time.Hour, in.Timeout)

	in, err = decodeRequest(
		"workspace_exec",
		[]byte(`{"command":"ls","timeout_sec":0,"timeout":3600}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, time.Hour, in.Timeout)

	in, err = decodeRequest(
		"workspace_exec",
		[]byte(`{"command":"ls","timeout_sec":0,"timeoutSec":120,"timeout":10}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, in.Timeout)
}

func TestCovercore_DecodeSkillSessionFields(t *testing.T) {
	reg := newProfileRegistry()

	in, err := decodeRequest(
		"skill_exec",
		[]byte(`{
			"skill":"demo",
			"command":"python",
			"stdin":"print(1)",
			"timeout":10
		}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "python", in.Command)
	require.Equal(t, "print(1)", in.SessionInput)
	require.False(t, in.sessionCreates)

	in, err = decodeRequest(
		"skill_write_stdin",
		[]byte(`{"session_id":"skill-1","chars":"print(2)","submit":true}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "skill-1", in.SessionID)
	require.Equal(t, "print(2)", in.SessionInput)
	require.True(t, in.sessionSubmit)
	require.True(t, in.sessionWrites)

	in, err = decodeRequest(
		"skill_kill_session",
		[]byte(`{"session_id":"skill-1"}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "skill-1", in.SessionID)
	require.True(t, in.sessionTerminates)

	in, err = decodeRequest(
		"skill_run",
		[]byte(`{
			"skill":"demo",
			"command":"python",
			"stdin":"print(3)",
			"timeout":10
		}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, "python", in.Command)
	require.Equal(t, "print(3)", in.SessionInput)
	require.Equal(t, 10*time.Second, in.Timeout)

	in, err = decodeRequest(
		"workspace_exec",
		[]byte(`{"command":"ls","timeoutSec":120,"timeout":10}`),
		reg,
	)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, in.Timeout)
}

// TestCovercore_RequiredString covers missing and mistyped fields.
func TestCovercore_RequiredString(t *testing.T) {
	_, err := requiredString(map[string]any{}, "command")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")

	_, err = requiredString(map[string]any{"command": 42}, "command")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a string")

	s, err := requiredString(map[string]any{"command": "ls"}, "command")
	require.NoError(t, err)
	require.Equal(t, "ls", s)
}

// TestCovercore_RawInt covers every accepted numeric shape.
func TestCovercore_RawInt(t *testing.T) {
	_, ok := rawInt(map[string]any{}, "k")
	require.False(t, ok)

	n, ok := rawInt(map[string]any{"k": float64(7.9)}, "k")
	require.True(t, ok)
	require.Equal(t, 7, n)

	n, ok = rawInt(map[string]any{"k": 5}, "k")
	require.True(t, ok)
	require.Equal(t, 5, n)

	n, ok = rawInt(map[string]any{"k": int64(9)}, "k")
	require.True(t, ok)
	require.Equal(t, 9, n)

	n, ok = rawInt(map[string]any{"k": "12"}, "k")
	require.True(t, ok)
	require.Equal(t, 12, n)

	_, ok = rawInt(map[string]any{"k": "abc"}, "k")
	require.False(t, ok)

	_, ok = rawInt(map[string]any{"k": true}, "k")
	require.False(t, ok)
}

// TestCovercore_DecodeEnvMap covers the absent, malformed, and non-string
// value branches.
func TestCovercore_DecodeEnvMap(t *testing.T) {
	env, err := decodeEnvMap(map[string]any{}, "env")
	require.NoError(t, err)
	require.Nil(t, env)

	_, err = decodeEnvMap(map[string]any{"env": "nope"}, "env")
	require.Error(t, err)
	require.Contains(t, err.Error(), "object of string values")

	_, err = decodeEnvMap(map[string]any{"env": map[string]any{"A": 1}}, "env")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a string")

	env, err = decodeEnvMap(map[string]any{"env": map[string]any{"A": "1"}}, "env")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"A": "1"}, env)
}

// TestCovercore_DecodeCodeBlocks covers the array, object, string, and
// error shapes.
func TestCovercore_DecodeCodeBlocks(t *testing.T) {
	_, err := decodeCodeBlocks(map[string]any{}, "code_blocks")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")

	blocks, err := decodeCodeBlocks(map[string]any{
		"code_blocks": []any{
			map[string]any{"language": "python", "code": "print(1)"},
			map[string]any{"language": "bash", "code": "ls"},
		},
	}, "code_blocks")
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	require.Equal(t, "python", blocks[0].Language)

	// An array item of the wrong type is rejected by typed decoding.
	_, err = decodeCodeBlocks(map[string]any{
		"code_blocks": []any{"just a string"},
	}, "code_blocks")
	require.Error(t, err)

	// A single object is wrapped into a one-element slice.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": map[string]any{"language": "go", "code": "package main"},
	}, "code_blocks")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "go", blocks[0].Language)

	// Missing fields match codeexec's typed JSON decoding and retain
	// their zero values.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": map[string]any{"language": "go"},
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "go"}}, blocks)

	// Present fields with incompatible types are rejected.
	_, err = decodeCodeBlocks(map[string]any{
		"code_blocks": map[string]any{"language": 42, "code": "package main"},
	}, "code_blocks")
	require.Error(t, err)

	// An explicit null matches codeexec and decodes to no blocks.
	blocks, err = decodeCodeBlocks(map[string]any{"code_blocks": nil}, "code_blocks")
	require.NoError(t, err)
	require.Nil(t, blocks)

	// A plain non-JSON string is rejected: the codeexec tool treats a
	// string value as double-encoded JSON, so an unparsable string is a
	// decode error rather than a bash block.
	_, err = decodeCodeBlocks(map[string]any{
		"code_blocks": "echo hi",
	}, "code_blocks")
	require.Error(t, err)
	require.Contains(t, err.Error(), "double-encoded")

	// A blank string is rejected.
	_, err = decodeCodeBlocks(map[string]any{"code_blocks": "   "}, "code_blocks")
	require.Error(t, err)

	// A wrong scalar type is rejected.
	_, err = decodeCodeBlocks(map[string]any{"code_blocks": 42}, "code_blocks")
	require.Error(t, err)
	require.Contains(t, err.Error(), "array, an object, or a double-encoded JSON string")
}

// TestCovercore_DecodeCodeBlocksDoubleEncoded mirrors the codeexec tool's
// unmarshalCodeBlocks handling of double-encoded JSON string payloads.
func TestCovercore_DecodeCodeBlocksDoubleEncoded(t *testing.T) {
	// A JSON-encoded array of blocks is unwrapped into typed blocks, so a
	// Python payload is analyzed as Python instead of shell text.
	blocks, err := decodeCodeBlocks(map[string]any{
		"code_blocks": `[{"language":"python","code":"print(1)"},{"language":"bash","code":"ls"}]`,
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{
		{Language: "python", Code: "print(1)"},
		{Language: "bash", Code: "ls"},
	}, blocks)

	// A JSON-encoded single object is wrapped into a one-element slice.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": `{"language":"python","code":"print(2)"}`,
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "python", Code: "print(2)"}}, blocks)

	// A dangerous payload keeps its code verbatim so the scanner can
	// analyze the real command instead of the JSON wrapper.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": `[{"language":"python","code":"import os; os.system('rm -rf /')"}]`,
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, "python", blocks[0].Language)
	require.Equal(t, "import os; os.system('rm -rf /')", blocks[0].Code)

	// A double-encoded scalar of the wrong shape is rejected.
	_, err = decodeCodeBlocks(map[string]any{"code_blocks": `42`}, "code_blocks")
	require.Error(t, err)

	// Missing fields in a double-encoded block retain their zero values,
	// matching codeexec's typed JSON decoding.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": `[{"language":"python"}]`,
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "python"}}, blocks)
}

// TestCovercore_DecodeOptionalFieldsTimeoutBounds is the X7 regression:
// timeout values that would overflow int64 when converted to
// time.Duration must be a decode error instead of wrapping negative and
// bypassing resource.timeout_exceeded.
func TestCovercore_DecodeOptionalFieldsTimeoutBounds(t *testing.T) {
	reg := newProfileRegistry()
	maxSeconds := int64(math.MaxInt64) / int64(time.Second)

	// The maximum representable timeout is accepted.
	in, err := decodeRequest("workspace_exec",
		[]byte(fmt.Sprintf(`{"command":"ls","timeout":%d}`, maxSeconds)), reg)
	require.NoError(t, err)
	require.Equal(t, time.Duration(maxSeconds)*time.Second, in.Timeout)

	// One past the maximum overflows and must be rejected.
	_, err = decodeRequest("workspace_exec",
		[]byte(fmt.Sprintf(`{"command":"ls","timeout":%d}`, maxSeconds+1)), reg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")

	// A negative timeout is rejected.
	_, err = decodeRequest("workspace_exec",
		[]byte(`{"command":"ls","timeout":-1}`), reg)
	require.Error(t, err)

	// Zero remains valid.
	in, err = decodeRequest("workspace_exec",
		[]byte(`{"command":"ls","timeout":0}`), reg)
	require.NoError(t, err)
	require.Zero(t, in.Timeout)
}
