//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
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

	// A non-string command field is not a command surface.
	in, err = decodeRequest("mystery_tool", []byte(`{"command":42}`), reg)
	require.NoError(t, err)
	require.Empty(t, in.Command)
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

// TestCovercore_PeekCommand covers the direct peekCommand paths.
func TestCovercore_PeekCommand(t *testing.T) {
	_, err := peekCommand([]byte(`{broken`))
	require.Error(t, err)

	cmd, err := peekCommand([]byte(`{"command":"ls -la"}`))
	require.NoError(t, err)
	require.Equal(t, "ls -la", cmd)

	cmd, err = peekCommand([]byte(`{"other":1}`))
	require.NoError(t, err)
	require.Empty(t, cmd)
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

	// An array item of the wrong type errors with the index.
	_, err = decodeCodeBlocks(map[string]any{
		"code_blocks": []any{"just a string"},
	}, "code_blocks")
	require.Error(t, err)
	require.Contains(t, err.Error(), "[0]")

	// A single object is wrapped into a one-element slice.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": map[string]any{"language": "go", "code": "package main"},
	}, "code_blocks")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "go", blocks[0].Language)

	// A single object with a bad shape errors without an index.
	_, err = decodeCodeBlocks(map[string]any{
		"code_blocks": map[string]any{"language": "go"},
	}, "code_blocks")
	require.Error(t, err)

	// A plain string becomes a bash block.
	blocks, err = decodeCodeBlocks(map[string]any{
		"code_blocks": "echo hi",
	}, "code_blocks")
	require.NoError(t, err)
	require.Equal(t, []CodeBlock{{Language: "bash", Code: "echo hi"}}, blocks)

	// A blank string is rejected.
	_, err = decodeCodeBlocks(map[string]any{"code_blocks": "   "}, "code_blocks")
	require.Error(t, err)

	// A wrong scalar type is rejected.
	_, err = decodeCodeBlocks(map[string]any{"code_blocks": 42}, "code_blocks")
	require.Error(t, err)
	require.Contains(t, err.Error(), "array, object, or string")
}

// TestCovercore_DecodeOneBlock covers the per-block validation errors.
func TestCovercore_DecodeOneBlock(t *testing.T) {
	_, err := decodeOneBlock("not a map")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be object")

	_, err = decodeOneBlock(map[string]any{"code": "print(1)"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "language is required")

	_, err = decodeOneBlock(map[string]any{"language": "python"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "code is required")

	b, err := decodeOneBlock(map[string]any{"language": "python", "code": "print(1)"})
	require.NoError(t, err)
	require.Equal(t, CodeBlock{Language: "python", Code: "print(1)"}, b)
}
