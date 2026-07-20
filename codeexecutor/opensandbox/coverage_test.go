//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// --- WithEnvVars / WithMetadata / WithHeaders / WithEndpointHostRewrite
// nil branches. The non-nil copy branches are already covered by the
// existing "copies the caller's map" tests; these exercise the nil
// early-return so the option does not allocate an empty map.

func TestWithEnvVars_NilClearsValue(t *testing.T) {
	c := &CodeExecutor{envVars: map[string]string{"pre": "set"}}
	WithEnvVars(nil)(c)
	assert.Nil(t, c.envVars, "WithEnvVars(nil) should clear envVars to nil")
}

func TestWithMetadata_NilClearsValue(t *testing.T) {
	c := &CodeExecutor{metadata: map[string]string{"pre": "set"}}
	WithMetadata(nil)(c)
	assert.Nil(t, c.metadata, "WithMetadata(nil) should clear metadata to nil")
}

func TestWithHeaders_NilClearsValue(t *testing.T) {
	c := &CodeExecutor{headers: map[string]string{"pre": "set"}}
	WithHeaders(nil)(c)
	assert.Nil(t, c.headers, "WithHeaders(nil) should clear headers to nil")
}

func TestWithEndpointHostRewrite_NilClearsValue(t *testing.T) {
	c := &CodeExecutor{endpointHostRewrite: map[string]string{"pre": "set"}}
	WithEndpointHostRewrite(nil)(c)
	assert.Nil(t, c.endpointHostRewrite, "WithEndpointHostRewrite(nil) should clear to nil")
}

// --- ensureRemoteDir error branch (CreateDirectory failure)

func TestEnsureRemoteDir_CreateDirectoryFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.createDirShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-ensure-dir-err", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Trigger ensureRemoteDir via PutFiles with a nested path; the
	// buildPutFileEntry calls ensureRemoteDir which now fails.
	err = exec.rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "sub/dir/file.txt",
		Content: []byte("data"),
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create directory")
}

// --- uploadEntriesBatched error branch: UploadFiles failure

func TestUploadEntriesBatched_UploadFilesFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.uploadShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-upload-fail", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "file.txt",
		Content: []byte("data"),
	}})
	require.Error(t, err)
}

// --- visit error branch: CreateDirectory failure in a subdir

func TestVisit_CreateDirectoryFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.createDirShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-visit-createdir", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// PutDirectory walks the host tree; the first subdir visit calls
	// CreateDirectory, which now fails.
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	require.NoError(t, os.Mkdir(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "f.txt"), []byte("x"), 0o644))

	err = exec.rt.PutDirectory(context.Background(), ws, tmpDir, "staged")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create directory")
}

// --- visit skips non-regular files (shouldUploadFile=false branch)

func TestVisit_SkipsNonRegularFile(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-skip-nonregular", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Create a host directory containing a symlink. visit should
	// skip the symlink (shouldUploadFile returns false for
	// non-regular files) and only upload the regular file.
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("x"), 0o644))
	if err := os.Symlink("real.txt", filepath.Join(tmpDir, "link.txt")); err != nil {
		t.Skipf("symlink not permitted in this environment: %v", err)
	}

	err = exec.rt.PutDirectory(context.Background(), ws, tmpDir, "staged")
	require.NoError(t, err)
	// Verify only real.txt was uploaded (mock records by filename).
	m.mu.Lock()
	_, hasReal := m.files["real.txt"]
	_, hasLink := m.files["link.txt"]
	m.mu.Unlock()
	assert.True(t, hasReal, "regular file should be uploaded")
	assert.False(t, hasLink, "symlink should be skipped")
}

// --- removeSymlinksBatch: pathUnder failure (no runBash needed)

func TestRemoveSymlinksBatch_PathEscapesWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Path escapes wsBase → pathUnder check fails before runBash.
	err := exec.rt.removeSymlinksBatch(
		context.Background(),
		[]string{"/etc/passwd"},
		"/tmp/run/ws-base",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// --- removeSymlinksBatch: runBash failure

func TestRemoveSymlinksBatch_RunBashFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// commandShouldFail makes every /command return 500, including
	// the mkdir inside CreateWorkspace. So we cannot CreateWorkspace
	// first. Instead call removeSymlinksBatch directly: it calls
	// runBash, which calls sandbox(). sandbox() requires the runtime
	// to be initialized. Use ensureRuntime to force init without
	// going through CreateWorkspace's mkdir.
	m.commandShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	rt := exec.ensureRuntime()
	err := rt.removeSymlinksBatch(
		context.Background(),
		[]string{"/tmp/run/ws-rm-fail/file"},
		"/tmp/run/ws-rm-fail",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "batch remove symlinks")
}

// --- prepareStdinRedirect: UploadFiles failure

func TestPrepareStdinRedirect_UploadFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.uploadShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-stdin-upload", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Trigger prepareStdinRedirect via RunProgram with Stdin set.
	_, err = exec.rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:   "cat",
		Stdin: "hello",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload stdin")
}

// --- ExecuteInline: PutFiles failure surfaces as non-zero exit

func TestExecuteInline_PutFilesFails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.uploadShouldFail = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// CreateWorkspace's mkdir uses runBash (not upload), so it
	// succeeds; the PutFiles inside ExecuteInline then fails.
	res, err := exec.ExecuteInline(context.Background(), "exec-putfiles-fail",
		[]codeexecutor.CodeBlock{{Language: "python", Code: "print('hi')"}},
		0,
	)
	// ExecuteInline never returns a Go error for block failures
	// (it aggregates into RunResult.ExitCode). Verify the error
	// surfaces in stderr.
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode, "PutFiles failure should surface as non-zero exit")
	assert.NotEmpty(t, res.Stderr, "PutFiles failure should surface in stderr")
}

// --- ExecuteInline: RunProgram timeout surfaces as TimedOut

func TestExecuteInline_RunProgramError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// runError makes RunProgram (but not infra) return a timeout-style
	// 500 error. isTimeoutErr returns true, so RunProgram returns
	// res.TimedOut=true with err==nil. ExecuteInline aggregates this
	// into RunResult.TimedOut.
	m.setRunError(context.DeadlineExceeded)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-runprogram-err",
		[]codeexecutor.CodeBlock{{Language: "python", Code: "print('hi')"}},
		0,
	)
	require.NoError(t, err)
	assert.True(t, res.TimedOut, "RunProgram timeout should aggregate to TimedOut")
}

// --- resolveSandboxPaths: fallback to per-path resolve

func TestResolveSandboxPaths_FallbackToPerPathResolve(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.readlinkBatchMalformed = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "ws-resolve-fallback", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed two files under ws.Path so resolveSandboxPaths has
	// multiple results to resolve. readlinkBatchMalformed returns
	// one fewer line than input, triggering the fallback.
	results := []fileSearchResult{
		{path: ws.Path + "/file1.txt", size: 10},
		{path: ws.Path + "/file2.txt", size: 20},
	}
	resolved, err := exec.rt.resolveSandboxPaths(context.Background(), results, ws.Path)
	require.NoError(t, err)
	// Fallback resolves each path individually; both should survive
	// pathUnder and be returned.
	assert.Len(t, resolved, 2)
}

// --- runBash sub-millisecond timeout rejection

func TestRunBash_SubMillisecondTimeoutRejected(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	_, err := exec.rt.runBash(context.Background(), "echo hi", 500*time.Microsecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below the 1ms API granularity")
}

// --- runBash incomplete stream (exec == nil)

func TestRunBash_ExecNilFailsClosed(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// noComplete omits execution_complete; forceInfraExit makes
	// infra commands (like the one runBash issues) also skip it.
	m.noComplete = true
	m.forceInfraExit = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	_, err := exec.rt.runBash(context.Background(), "echo hi", 0)
	require.Error(t, err)
}
