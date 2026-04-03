//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

const (
	testMemoryAppName = "openclaw"
	testMemoryUserID  = "wecom:dm:test-user"
	testMemoryName    = "Sample User"
	testMemoryOldText = "Original Name"
	testMemoryNewText = "Updated Name"
)

func TestMemoryFileToolCallback_SaveFileWritesScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName: memoryToolSaveFileFS,
		Arguments: []byte(
			`{"file_name":"MEMORY.md","contents":"- User name: ` +
				testMemoryName +
				`\n","overwrite":false}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memorySaveFileResponse)
	require.True(t, ok)
	require.Equal(t, "Successfully saved: MEMORY.md", rsp.Message)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "# Memory")
	require.Contains(t, string(raw), "- User name: "+testMemoryName)

	_, err = os.Stat(filepath.Join(stateDir, memoryToolFileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMemoryFileToolCallback_ReadFilePrefersScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("scoped memory\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(stateDir, memoryToolFileName),
		[]byte("root memory\n"),
		0o600,
	))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  memoryToolReadFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memoryReadFileResponse)
	require.True(t, ok)
	require.Contains(t, rsp.Contents, "scoped memory")
	require.NotContains(t, rsp.Contents, "root memory")
}

func TestMemoryFileToolCallback_DoesNotInterceptGenericReadFile(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  "read_file",
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMemoryFileToolCallback_ReplaceContentUsesScopedMemory(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("Hello "+testMemoryOldText+"\n"), 0o600))
	rootPath := filepath.Join(stateDir, memoryToolFileName)
	require.NoError(t, os.WriteFile(rootPath, []byte("Hello Root\n"), 0o600))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName: memoryToolReplaceContentFS,
		Arguments: []byte(
			`{"file_name":"MEMORY.md","old_string":"` +
				testMemoryOldText +
				`","new_string":"` +
				testMemoryNewText +
				`"}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memoryReplaceContentResponse)
	require.True(t, ok)
	require.Equal(t, "Successfully replaced 1 of 1 in 'MEMORY.md'", rsp.Message)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), testMemoryNewText)

	rootRaw, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	require.Contains(t, string(rootRaw), "Hello Root")
}

func TestMemoryFileToolCallback_SaveFilePreservesOverwriteGuard(
	t *testing.T,
) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	ctx := newTestMemoryToolContext()
	callback := newMemoryFileToolCallback(store, stateDir)

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("# Memory\n\n- Existing fact\n"), 0o600))

	result, err := callback(ctx, &tool.BeforeToolArgs{
		ToolName:  memoryToolSaveFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md","contents":"replacement text","overwrite":false}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	rsp, ok := result.CustomResult.(memorySaveFileResponse)
	require.True(t, ok)
	require.Equal(
		t,
		"Error: file exists and overwrite=false: MEMORY.md",
		rsp.Message,
	)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- Existing fact\n", string(raw))
}

func TestRegisterMemoryFileToolCallback(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	registerMemoryFileToolCallback(nil, store, stateDir)

	callbacks := tool.NewCallbacks()
	registerMemoryFileToolCallback(callbacks, nil, stateDir)
	require.Empty(t, callbacks.BeforeTool)

	registerMemoryFileToolCallback(callbacks, store, stateDir)
	require.Len(t, callbacks.BeforeTool, 1)

	result, err := callbacks.RunBeforeTool(newTestMemoryToolContext(), &tool.BeforeToolArgs{
		ToolName:  memoryToolReadFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestMemoryFileToolCallback_NoopsWhenUnavailable(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	args := &tool.BeforeToolArgs{
		ToolName:  memoryToolReadFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	}

	callback := newMemoryFileToolCallback(nil, stateDir)
	result, err := callback(newTestMemoryToolContext(), args)
	require.NoError(t, err)
	require.Nil(t, result)

	callback = newMemoryFileToolCallback(store, stateDir)
	result, err = callback(newTestMemoryToolContext(), nil)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = callback(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, result)

	noSessionCtx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(),
	)
	result, err = callback(noSessionCtx, args)
	require.NoError(t, err)
	require.Nil(t, result)

	blankAppCtx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(agent.WithInvocationSession(
			session.NewSession(" ", testMemoryUserID, "memory-tool-session"),
		)),
	)
	result, err = callback(blankAppCtx, args)
	require.NoError(t, err)
	require.Nil(t, result)

	blankUserCtx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(agent.WithInvocationSession(
			session.NewSession(testMemoryAppName, " ", "memory-tool-session"),
		)),
	)
	result, err = callback(blankUserCtx, args)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMemoryFileToolCallback_TargetResolutionError(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "memory-root")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o600))

	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	callback := newMemoryFileToolCallback(store, stateDir)
	result, err := callback(newTestMemoryToolContext(), &tool.BeforeToolArgs{
		ToolName:  memoryToolReadFileFS,
		Arguments: []byte(`{"file_name":"MEMORY.md"}`),
	})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestHandleMemoryReadFileTool_Branches(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	target := memoryToolTarget{
		Path: filepath.Join(t.TempDir(), memoryToolFileName),
	}

	result, err := handleMemoryReadFileTool(target, baseDir, []byte(`{`))
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"notes.md"}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"MEMORY.md"}`),
	)
	require.NoError(t, err)
	rsp := requireMemoryReadFileResponse(t, result)
	require.Contains(t, rsp.Message, "Error: cannot read file")

	require.NoError(t, os.WriteFile(target.Path, []byte(""), 0o600))
	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"./memory.md"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReadFileResponse(t, result)
	require.Equal(t, "Successfully read ./memory.md, but file is empty", rsp.Message)

	require.NoError(t, os.WriteFile(
		target.Path,
		[]byte("line1\nline2\nline3"),
		0o600,
	))
	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"MEMORY.md","start_line":2,"num_lines":2}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReadFileResponse(t, result)
	require.Equal(t, "line2\nline3", rsp.Contents)
	require.Equal(
		t,
		"Successfully read MEMORY.md, start line: 2, end line: 3, total lines: 3",
		rsp.Message,
	)

	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"MEMORY.md","start_line":0}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReadFileResponse(t, result)
	require.Equal(t, "Error: start line must be > 0: 0", rsp.Message)

	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"MEMORY.md","num_lines":0}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReadFileResponse(t, result)
	require.Equal(t, "Error: number of lines must be > 0: 0", rsp.Message)

	result, err = handleMemoryReadFileTool(
		target,
		baseDir,
		[]byte(`{"file_name":"MEMORY.md","start_line":5}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReadFileResponse(t, result)
	require.Equal(
		t,
		"Error: start line is out of range, start line: 5, total lines: 3",
		rsp.Message,
	)
}

func TestHandleMemorySaveFileTool_Branches(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	target := newTestMemoryToolTarget(t, store)

	result, err := handleMemorySaveFileTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = handleMemorySaveFileTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"notes.md","contents":"ignored","overwrite":true}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	require.NoError(t, os.WriteFile(
		target.Path,
		[]byte("# Memory\n\n- Existing fact\n"),
		0o600,
	))
	result, err = handleMemorySaveFileTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","contents":"- Existing fact","overwrite":false}`),
	)
	require.NoError(t, err)
	rsp := requireMemorySaveFileResponse(t, result)
	require.Equal(t, "Successfully saved: MEMORY.md", rsp.Message)

	raw, err := os.ReadFile(target.Path)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- Existing fact\n", string(raw))

	result, err = handleMemorySaveFileTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","contents":"","overwrite":false}`),
	)
	require.NoError(t, err)
	rsp = requireMemorySaveFileResponse(t, result)
	require.Equal(
		t,
		"Error: file exists and overwrite=false: MEMORY.md",
		rsp.Message,
	)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = handleMemorySaveFileTool(
		canceledCtx,
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","contents":"replacement","overwrite":true}`),
	)
	require.NoError(t, err)
	rsp = requireMemorySaveFileResponse(t, result)
	require.Equal(t, "Error: context canceled", rsp.Message)

	result, err = handleMemorySaveFileTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","contents":"replacement","overwrite":true}`),
	)
	require.NoError(t, err)
	rsp = requireMemorySaveFileResponse(t, result)
	require.Equal(t, "Successfully saved: MEMORY.md", rsp.Message)

	raw, err = os.ReadFile(target.Path)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(raw))
}

func TestHandleMemoryReplaceContentTool_Branches(t *testing.T) {
	t.Parallel()

	stateDir, store := newTestMemoryFileStore(t)
	target := newTestMemoryToolTarget(t, store)

	result, err := handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"notes.md","old_string":"a","new_string":"b"}`),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	result, err = handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","old_string":"","new_string":"b"}`),
	)
	require.NoError(t, err)
	rsp := requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "Error: old_string cannot be empty", rsp.Message)

	result, err = handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","old_string":"same","new_string":"same"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "old_string equals new_string; no changes made", rsp.Message)

	require.NoError(t, os.WriteFile(
		target.Path,
		[]byte("alpha beta alpha"),
		0o600,
	))
	result, err = handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","old_string":"missing","new_string":"beta"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "'missing' not found in 'MEMORY.md'", rsp.Message)

	result, err = handleMemoryReplaceContentTool(
		context.Background(),
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","old_string":"alpha","new_string":"gamma","num_replacements":-1}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(
		t,
		"Successfully replaced 2 of 2 in 'MEMORY.md'",
		rsp.Message,
	)

	raw, err := os.ReadFile(target.Path)
	require.NoError(t, err)
	require.Equal(t, "gamma beta gamma", string(raw))

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = handleMemoryReplaceContentTool(
		canceledCtx,
		store,
		stateDir,
		target,
		[]byte(`{"file_name":"MEMORY.md","old_string":"gamma","new_string":"delta"}`),
	)
	require.NoError(t, err)
	rsp = requireMemoryReplaceContentResponse(t, result)
	require.Equal(t, "Error: context canceled", rsp.Message)
}

func TestMemoryFileHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, memoryToolReadFileFS, normalizeMemoryToolName("  fs_read_file  "))
	require.Equal(t, memoryToolSaveFileFS, normalizeMemoryToolName(memoryToolSaveFileFS))
	require.Equal(
		t,
		memoryToolReplaceContentFS,
		normalizeMemoryToolName(memoryToolReplaceContentFS),
	)
	require.Empty(t, normalizeMemoryToolName("read_file"))

	require.True(t, isMemoryFileAlias("./MEMORY.md"))
	require.True(t, isMemoryFileAlias("././memory.md"))
	require.False(t, isMemoryFileAlias("notes/MEMORY.md"))

	next, err := nextMemorySaveContents("current", "replacement", true)
	require.NoError(t, err)
	require.Equal(t, "replacement", next)

	_, err = nextMemorySaveContents("current", " ", false)
	require.ErrorIs(t, err, errMemorySaveFileExists)

	next, err = nextMemorySaveContents(
		"# Memory\n\n- Existing fact\n",
		"- Existing fact",
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- Existing fact\n", next)

	_, err = nextMemorySaveContents("current", "full replacement text", false)
	require.ErrorIs(t, err, errMemorySaveFileExists)

	next, err = nextMemorySaveContents("", "* Favorite editor: Cursor", false)
	require.NoError(t, err)
	require.Equal(t, "* Favorite editor: Cursor\n", next)

	next, err = nextMemorySaveContents(
		"# Memory\n\n- Existing fact\n",
		"- New fact",
		false,
	)
	require.NoError(t, err)
	require.Equal(t, "# Memory\n\n- Existing fact\n\n- New fact\n", next)

	require.False(t, looksLikeMemoryAppendSnippet("not a bullet"))
	require.True(t, looksLikeMemoryAppendSnippet("- First fact\n\n* Second fact"))
	require.NotNil(t, memoryToolResult("custom"))
}

func TestSliceMemoryTextByLines(t *testing.T) {
	t.Parallel()

	chunk, start, end, total, empty, err := sliceMemoryTextByLines("", nil, nil)
	require.NoError(t, err)
	require.Empty(t, chunk)
	require.Zero(t, start)
	require.Zero(t, end)
	require.Zero(t, total)
	require.True(t, empty)

	chunk, start, end, total, empty, err = sliceMemoryTextByLines(
		"one\ntwo\nthree",
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\nthree", chunk)
	require.Equal(t, 1, start)
	require.Equal(t, 3, end)
	require.Equal(t, 3, total)
	require.False(t, empty)

	startLine := 2
	numLines := 1
	chunk, start, end, total, empty, err = sliceMemoryTextByLines(
		"one\ntwo\nthree",
		&startLine,
		&numLines,
	)
	require.NoError(t, err)
	require.Equal(t, "two", chunk)
	require.Equal(t, 2, start)
	require.Equal(t, 2, end)
	require.Equal(t, 3, total)
	require.False(t, empty)

	startLine = 2
	numLines = 5
	chunk, start, end, total, empty, err = sliceMemoryTextByLines(
		"one\ntwo\nthree",
		&startLine,
		&numLines,
	)
	require.NoError(t, err)
	require.Equal(t, "two\nthree", chunk)
	require.Equal(t, 2, start)
	require.Equal(t, 3, end)
	require.Equal(t, 3, total)
	require.False(t, empty)

	startLine = 0
	_, _, _, _, _, err = sliceMemoryTextByLines("one\ntwo", &startLine, nil)
	require.EqualError(t, err, "start line must be > 0: 0")

	startLine = 1
	numLines = 0
	_, _, _, _, _, err = sliceMemoryTextByLines("one\ntwo", &startLine, &numLines)
	require.EqualError(t, err, "number of lines must be > 0: 0")

	startLine = 3
	_, _, _, total, empty, err = sliceMemoryTextByLines("one\ntwo", &startLine, nil)
	require.EqualError(
		t,
		err,
		"start line is out of range, start line: 3, total lines: 2",
	)
	require.Equal(t, 2, total)
	require.False(t, empty)
}

func newTestMemoryFileStore(t *testing.T) (string, *memoryfile.Store) {
	t.Helper()

	stateDir := t.TempDir()
	root, err := memoryfile.DefaultRoot(stateDir)
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	return stateDir, store
}

func newTestMemoryToolTarget(
	t *testing.T,
	store *memoryfile.Store,
) memoryToolTarget {
	t.Helper()

	path, err := store.EnsureMemory(
		context.Background(),
		testMemoryAppName,
		testMemoryUserID,
	)
	require.NoError(t, err)
	return memoryToolTarget{
		AppName: testMemoryAppName,
		UserID:  testMemoryUserID,
		Path:    path,
	}
}

func newTestMemoryToolContext() context.Context {
	inv := agent.NewInvocation(agent.WithInvocationSession(
		session.NewSession(
			testMemoryAppName,
			testMemoryUserID,
			"memory-tool-session",
		),
	))
	return agent.NewInvocationContext(context.Background(), inv)
}

func requireMemoryReadFileResponse(
	t *testing.T,
	result *tool.BeforeToolResult,
) memoryReadFileResponse {
	t.Helper()

	require.NotNil(t, result)
	rsp, ok := result.CustomResult.(memoryReadFileResponse)
	require.True(t, ok)
	return rsp
}

func requireMemorySaveFileResponse(
	t *testing.T,
	result *tool.BeforeToolResult,
) memorySaveFileResponse {
	t.Helper()

	require.NotNil(t, result)
	rsp, ok := result.CustomResult.(memorySaveFileResponse)
	require.True(t, ok)
	return rsp
}

func requireMemoryReplaceContentResponse(
	t *testing.T,
	result *tool.BeforeToolResult,
) memoryReplaceContentResponse {
	t.Helper()

	require.NotNil(t, result)
	rsp, ok := result.CustomResult.(memoryReplaceContentResponse)
	require.True(t, ok)
	return rsp
}
