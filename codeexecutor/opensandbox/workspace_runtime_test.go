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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// mockOpenSandboxServer simulates the OpenSandbox lifecycle + execd
// HTTP API surface for testing. It handles:
//   - POST /v1/sandboxes (create)
//   - GET  /v1/sandboxes/{id} (status poll)
//   - GET  /v1/sandboxes/{id}/endpoints/{port} (resolve execd endpoint)
//   - POST /v1/sandboxes/{id}/pause
//   - DELETE /v1/sandboxes/{id} (kill)
//   - execd: POST /command, POST /directories, POST /files (upload),
//     GET /files/download, GET /files/search, GET /ping
type mockOpenSandboxServer struct {
	t        *testing.T
	server   *httptest.Server
	endpoint string // execd endpoint URL returned to the SDK

	mu              sync.Mutex
	commands        []string          // captured /command request bodies
	commandTimeouts []int64           // captured /command timeout field (ms)
	files           map[string][]byte // simulated sandbox filesystem
	dirsCreated     []string
	killCalls       int
	pauseCalls      int
	// exitCode controls the exit_code in execution_complete events.
	// nil means no exit_code field (test the nil → -1 fallback).
	exitCode *int
	stdout   string
	stderr   string
	// noComplete, when true, omits the execution_complete event so
	// Execution.ExitCode stays nil (tests the -1 fallback).
	noComplete bool
	// runError, when non-nil, makes /command return a 500 error with
	// "timeout" in the code field so isTimeoutErr returns true. Used to
	// test RunProgram timeout handling.
	runError error
	// forceInfraExit, when true, forces infrastructure commands (mkdir,
	// rm, chmod — those without `&& cd `) to use the configured exitCode
	// instead of forcing 0. Used to test runBash error paths.
	forceInfraExit bool
	// searchResults, when non-empty, overrides the default single-file
	// search response with the configured file names (joined under the
	// searched dir). Used to test Collect multi-file behaviour.
	searchResults []string
}

func newMockServer(t *testing.T) *mockOpenSandboxServer {
	t.Helper()
	m := &mockOpenSandboxServer{
		t:     t,
		files: map[string][]byte{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	// The execd endpoint points back at the same mock server; the SDK
	// will use this URL for all execd calls.
	m.endpoint = m.server.URL
	return m
}

func (m *mockOpenSandboxServer) close() { m.server.Close() }

func (m *mockOpenSandboxServer) setExitCode(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exitCode = &code
}

func (m *mockOpenSandboxServer) setStdout(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stdout = s
}

func (m *mockOpenSandboxServer) setStderr(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stderr = s
}

func (m *mockOpenSandboxServer) setRunError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runError = err
}

func (m *mockOpenSandboxServer) setForceInfraExit(b bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forceInfraExit = b
}

func (m *mockOpenSandboxServer) setSearchResults(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchResults = paths
}

func (m *mockOpenSandboxServer) setDownloadData(path string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = data
}

func (m *mockOpenSandboxServer) lastCommand() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commands) == 0 {
		return ""
	}
	return m.commands[len(m.commands)-1]
}

// lastCommandTimeoutMs returns the timeout (ms) of the most recent
// /command request, or 0 if none has been captured.
func (m *mockOpenSandboxServer) lastCommandTimeoutMs() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commandTimeouts) == 0 {
		return 0
	}
	return m.commandTimeouts[len(m.commandTimeouts)-1]
}

func (m *mockOpenSandboxServer) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Lifecycle routes (prefixed with /v1). SandboxInfo.Status is a
	// SandboxStatus struct with a "state" field (not a bare string),
	// and CreatedAt is a required time.Time field.
	const sandboxInfo = `{"id":"sbx-mock","status":{"state":"Running"},"createdAt":"2026-01-01T00:00:00Z"}`
	switch {
	case r.Method == http.MethodPost && path == "/v1/sandboxes":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sandboxInfo)
		return
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/sandboxes/") && !strings.Contains(path, "/endpoints/"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sandboxInfo)
		return
	case r.Method == http.MethodGet && strings.Contains(path, "/endpoints/"):
		// Return the mock server's own URL as the execd endpoint.
		u, _ := url.Parse(m.server.URL)
		endpoint := u.Host // host:port
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"endpoint":%q}`, endpoint)
		return
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/sandboxes/"):
		m.mu.Lock()
		m.killCalls++
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/pause"):
		m.mu.Lock()
		m.pauseCalls++
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Execd routes (no /v1 prefix). The SDK resolves the endpoint to
	// the mock server's host:port and uses the same http.Client, so
	// these requests arrive at the same server.
	switch {
	case r.Method == http.MethodGet && path == "/ping":
		w.WriteHeader(http.StatusOK)
		return
	case r.Method == http.MethodPost && path == "/command":
		m.handleCommand(w, r)
		return
	case r.Method == http.MethodPost && path == "/directories":
		m.handleCreateDirectory(w, r)
		return
	case r.Method == http.MethodPost && (path == "/files" || path == "/files/upload"):
		m.handleUploadFiles(w, r)
		return
	case r.Method == http.MethodGet && path == "/files/download":
		m.handleDownloadFile(w, r)
		return
	case r.Method == http.MethodGet && path == "/files/search":
		m.handleSearchFiles(w, r)
		return
	}

	// Unknown route — return 404 so the test fails loudly.
	m.t.Logf("mock server: unhandled %s %s", r.Method, path)
	w.WriteHeader(http.StatusNotFound)
}

func (m *mockOpenSandboxServer) handleCommand(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Command string `json:"command"`
		Timeout int64  `json:"timeout"`
	}
	_ = json.Unmarshal(body, &req)
	m.mu.Lock()
	m.commands = append(m.commands, req.Command)
	m.commandTimeouts = append(m.commandTimeouts, req.Timeout)
	exitCode := m.exitCode
	stdout := m.stdout
	stderr := m.stderr
	noComplete := m.noComplete
	runErr := m.runError
	forceInfraExit := m.forceInfraExit
	m.mu.Unlock()

	// runError makes /command return a 500 with "timeout" in the code
	// field so the SDK produces an APIError whose Error() contains
	// "timeout" — exercising isTimeoutErr in RunProgram.
	if runErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"code":"timeout","message":%q}`, runErr.Error())
		return
	}

	// runBash calls (CreateWorkspace mkdir, Cleanup rm, StageDirectory
	// chmod) and RunProgram calls are both wrapped in `bash -c '...'`.
	// Infrastructure commands should always succeed; only apply the
	// configured non-zero exit code to RunProgram commands, which
	// contain `&& cd ` (from `mkdir -p ... && cd ... && ...`).
	// forceInfraExit bypasses this guard to test runBash error paths.
	isRunProgram := strings.Contains(req.Command, "&& cd ")
	if !isRunProgram && exitCode != nil && *exitCode != 0 && !forceInfraExit {
		zero := 0
		exitCode = &zero
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	// init event
	fmt.Fprintf(w, `{"type":"init","text":"exec-1"}`)
	fmt.Fprint(w, "\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	if stdout != "" {
		fmt.Fprintf(w, `{"type":"stdout","text":%q}`, stdout)
		fmt.Fprint(w, "\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	if stderr != "" {
		fmt.Fprintf(w, `{"type":"stderr","text":%q}`, stderr)
		fmt.Fprint(w, "\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	if !noComplete {
		// Non-zero exit code must be delivered via an "error" event
		// because the SDK's execution_complete handler does not parse
		// the exit_code field; it only defaults to 0 when no error
		// occurred. The error event's evalue is parsed as the exit
		// code by the SDK's error handler.
		if exitCode != nil && *exitCode != 0 {
			fmt.Fprintf(w, `{"type":"error","ename":"ExitError","evalue":"%d"}`, *exitCode)
			fmt.Fprint(w, "\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, `{"type":"execution_complete","execution_time":10}`)
		fmt.Fprint(w, "\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (m *mockOpenSandboxServer) handleCreateDirectory(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var dirs map[string]map[string]int
	_ = json.Unmarshal(body, &dirs)
	m.mu.Lock()
	for p := range dirs {
		m.dirsCreated = append(m.dirsCreated, p)
	}
	m.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (m *mockOpenSandboxServer) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	// Multipart upload: parse the form and record file paths. We don't
	// fully simulate the filesystem; we just track that files were
	// received.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	for _, headers := range r.MultipartForm.File {
		for _, h := range headers {
			if h.Filename != "" {
				m.files[h.Filename] = []byte{}
			}
		}
	}
	m.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (m *mockOpenSandboxServer) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	m.mu.Lock()
	data, ok := m.files[p]
	m.mu.Unlock()
	if !ok {
		// Return some default content so Collect tests work even
		// without an explicit upload.
		data = []byte("mock-content")
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (m *mockOpenSandboxServer) handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	pattern := r.URL.Query().Get("pattern")
	_ = pattern
	// Return fake files under the searched directory.
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if len(m.searchResults) > 0 {
		// Return configured results joined under the searched dir so
		// pathUnder passes.
		entries := make([]string, 0, len(m.searchResults))
		for _, name := range m.searchResults {
			p := filepath.ToSlash(filepath.Join(dir, name))
			entries = append(entries, fmt.Sprintf(`{"path":%q,"size":12}`, p))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
		return
	}
	// Default: return one file so basic Collect tests work.
	fakePath := filepath.ToSlash(filepath.Join(dir, "output.txt"))
	fmt.Fprintf(w, `[{"path":%q,"size":12}]`, fakePath)
}

// newTestExecutor creates a CodeExecutor backed by the mock server.
func newTestExecutor(t *testing.T, m *mockOpenSandboxServer) *CodeExecutor {
	t.Helper()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err, "New failed against mock server")
	return exec
}

func TestWorkspace_CreateWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	// sanitize("exec-1") keeps the hyphen, so the path uses "ws_exec-1_".
	assert.Contains(t, ws.Path, "/tmp/run/ws_exec-1_")
	// The mock should have received a mkdir -p command.
	assert.NotEmpty(t, m.lastCommand())
	assert.Contains(t, m.lastCommand(), "mkdir -p")
}

func TestWorkspace_RunProgram(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("hello world")
	code := 0
	m.setExitCode(code)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	res, err := exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"hello"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", res.Stdout)
	assert.Equal(t, 0, res.ExitCode)
}

// TestWorkspace_RunProgram_TimeoutClampedToRequestBudget verifies that
// RunProgram clamps spec.Timeout to requestTimeout - requestTimeoutBuffer
// so the HTTP client cannot kill a streaming /command call before the
// per-command timeout fires. Default executor has executionTimeout=30s,
// so NewWithContext clamps requestTimeout to 40s; maxRun = 30s.
func TestWorkspace_RunProgram_TimeoutClampedToRequestBudget(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// spec.Timeout=60s exceeds maxRun=30s (requestTimeout 40s - buffer 10s).
	// RunProgram must clamp to 30s; mock should receive 30000ms.
	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"ok"},
		Timeout: 60 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(30000), m.lastCommandTimeoutMs(),
		"spec.Timeout 60s should be clamped to 30s (30000ms) when requestTimeout=40s")
}

// TestWorkspace_RunProgram_TimeoutWithinBudget verifies that
// RunProgram does NOT clamp spec.Timeout when it fits within
// requestTimeout - requestTimeoutBuffer.
func TestWorkspace_RunProgram_TimeoutWithinBudget(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// spec.Timeout=20s fits within maxRun=30s; no clamping.
	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"ok"},
		Timeout: 20 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(20000), m.lastCommandTimeoutMs(),
		"spec.Timeout 20s should not be clamped when within budget")
}

func TestWorkspace_RunProgram_CleanEnv(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:      "env",
		Env:      map[string]string{"FOO": "bar"},
		CleanEnv: true,
		Timeout:  5 * time.Second,
	})
	require.NoError(t, err)
	// The command sent to the mock should contain `env -i` and the
	// injected FOO= variable. The exact quoting depends on the
	// bash -c wrapping (single-quote escaping), so we check for
	// the env -i prefix and the FOO= assignment separately.
	cmd := m.lastCommand()
	assert.Contains(t, cmd, "env -i")
	assert.Contains(t, cmd, "FOO=")
}

func TestWorkspace_RunProgram_NilExitCode(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// Omit execution_complete entirely so Execution.ExitCode stays nil
	// (the SDK only sets ExitCode=0 when execution_complete is received
	// with no exit_code and no error). RunProgram should fall back to -1.
	m.noComplete = true
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	res, err := exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "false",
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, -1, res.ExitCode)
}

func TestWorkspace_PutFiles(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "a.txt", Content: []byte("aaa"), Mode: 0o644},
		{Path: "b.txt", Content: []byte("bbb"), Mode: 0o644},
	})
	require.NoError(t, err)
}

func TestWorkspace_PutDirectory(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Create a temp dir with files.
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "x.txt"), []byte("xx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "y.txt"), []byte("yy"), 0o644))

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutDirectory(context.Background(), ws, tmpDir, "subdir")
	require.NoError(t, err)
}

func TestWorkspace_StageDirectory_ReadOnly(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "z.txt"), []byte("zz"), 0o644))

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.StageDirectory(context.Background(), ws, tmpDir, "staged", codeexecutor.StageOptions{ReadOnly: true})
	require.NoError(t, err)
	// The last command should be `chmod -R a-w <dest>`.
	assert.Contains(t, m.lastCommand(), "chmod -R a-w")
}

func TestWorkspace_Collect(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Contains(t, files[0].Name, "output.txt")
	assert.Equal(t, "mock-content", files[0].Content)
}

func TestWorkspace_ExecuteInline(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("42")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-1", []codeexecutor.CodeBlock{
		{Language: "python", Code: "print(42)"},
	}, 10*time.Second)
	require.NoError(t, err)
	assert.Contains(t, res.Stdout, "42")
}

func TestWorkspace_Cleanup(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.Cleanup(context.Background(), ws)
	require.NoError(t, err)
	assert.Contains(t, m.lastCommand(), "rm -rf")
}

func TestWorkspace_StageInputs_NotImplemented(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{From: "host:///x"}})
	assert.ErrorIs(t, err, errNotImplementedV1)
}

func TestWorkspace_CollectOutputs_NotImplemented(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{Globs: []string{"*.txt"}})
	assert.ErrorIs(t, err, errNotImplementedV1)
}

func TestWorkspace_Close_KillsOwnedSandbox(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)

	require.NoError(t, exec.Close())
	assert.Equal(t, 1, m.killCalls, "Close should kill the owned sandbox")
}

// --- Coverage-focused tests below ---

func TestWorkspace_RunProgram_Timeout(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// CreateWorkspace must succeed first (it uses /command for mkdir);
	// only afterwards inject the run error so RunProgram sees it.
	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	m.setRunError(fmt.Errorf("command timeout exceeded"))

	res, err := exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "sleep",
		Args:    []string{"100"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err, "timeout should be surfaced via RunResult, not error")
	assert.True(t, res.TimedOut, "RunResult.TimedOut should be true on timeout")
}

func TestWorkspace_CreateWorkspace_RunBashError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// Force infrastructure commands (mkdir) to honour the configured
	// non-zero exit code so runBash returns an error.
	nonZero := 1
	m.setExitCode(nonZero)
	m.setForceInfraExit(true)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	_, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.Error(t, err, "CreateWorkspace should fail when runBash exits non-zero")
	assert.Contains(t, err.Error(), "bash exit")
}

func TestExecuteInline_MultipleBlocks(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("block-out")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Multiple blocks should aggregate stdout from each. An unsupported
	// language block should be recorded in stderr without aborting.
	res, err := exec.ExecuteInline(context.Background(), "exec-multi", []codeexecutor.CodeBlock{
		{Language: "python", Code: "print('a')"},
		{Language: "ruby", Code: "puts 'b'"},
		{Language: "bash", Code: "echo c"},
	}, 10*time.Second)
	require.NoError(t, err)
	// stdout from python + bash blocks (ruby is skipped).
	assert.Contains(t, res.Stdout, "block-out")
	// stderr should record the unsupported language error.
	assert.Contains(t, res.Stderr, "unsupported language")
}

func TestWorkspace_PutDirectory_WithSubdirs(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Create a temp dir with a subdirectory and files at both levels
	// so PutDirectory exercises the parent-directory creation branch.
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	require.NoError(t, os.Mkdir(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "top.txt"), []byte("top"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested"), 0o644))

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutDirectory(context.Background(), ws, tmpDir, "project")
	require.NoError(t, err)
}

func TestWorkspace_PutDirectory_PathEscape(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	tmpDir := t.TempDir()
	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// A destination that escapes the workspace should be rejected.
	err = exec.PutDirectory(context.Background(), ws, tmpDir, "../../../etc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

func TestWorkspace_Collect_MultipleFiles(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Configure search to return multiple files. The ".metadata.tmp"
	// entry is a root-level metadata temp file and should be skipped
	// by IsRootMetadataTempPath.
	m.setSearchResults([]string{"a.txt", "b.txt", ".metadata.tmp"})

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	// Two real files; ".metadata.tmp" should be filtered out.
	require.Len(t, files, 2)
	names := []string{files[0].Name, files[1].Name}
	assert.Contains(t, names, "a.txt")
	assert.Contains(t, names, "b.txt")
	for _, f := range files {
		assert.Equal(t, "mock-content", f.Content)
		assert.False(t, f.Truncated)
	}
}

func TestWorkspace_Collect_Truncation(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-trunc", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Compute the exact path the mock's handleSearchFiles will return
	// and seed handleDownloadFile with data exceeding maxReadSizeBytes
	// so readFile truncates and Collect reports Truncated == true.
	expectedPath := filepath.ToSlash(filepath.Join(ws.Path, "output.txt"))
	m.setDownloadData(expectedPath, make([]byte, maxReadSizeBytes+1))

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, files[0].Truncated, "file exceeding maxReadSizeBytes should be truncated")
	assert.Equal(t, int64(maxReadSizeBytes+1), files[0].SizeBytes)
}

func TestWorkspace_RunProgram_WithStdin(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "cat",
		Stdin:   "hello",
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)

	// The command sent to the mock should pipe stdin via base64 -d.
	cmd := m.lastCommand()
	assert.Contains(t, cmd, "base64 -d", "stdin should be piped through base64 -d")
	assert.Contains(t, cmd, b64encode("hello"), "command should contain base64-encoded stdin")
}

func TestWorkspace_CreateWorkspace_Persist(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithWorkspacePersistence(WorkspacePersistencePerSession),
	)
	require.NoError(t, err)
	defer exec.Close()

	// PerSession mode: same execID → same deterministic workspace path.
	ws1, err := exec.CreateWorkspace(context.Background(), "persist-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	ws2, err := exec.CreateWorkspace(context.Background(), "persist-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	assert.Equal(t, ws1.Path, ws2.Path, "PerSession mode should reuse the same workspace path")

	// Empty execID in PerSession mode should be rejected.
	_, err = exec.CreateWorkspace(context.Background(), "", codeexecutor.WorkspacePolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execID must not be empty")
}

func TestWorkspace_PutFiles_InvalidPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// A file path that escapes the workspace should be rejected.
	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "../../etc/passwd", Content: []byte("x"), Mode: 0o644},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

func TestWorkspace_PutDirectory_EmptyHostPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutDirectory(context.Background(), ws, "", "subdir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostPath is empty")
}

func TestWorkspace_PutDirectory_NotDir(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// A file (not a directory) should be rejected.
	tmpFile := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0o644))

	err = exec.PutDirectory(context.Background(), ws, tmpFile, "subdir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestWorkspace_Cleanup_EmptyPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// An empty workspace path should be a no-op (no /command call).
	err := exec.Cleanup(context.Background(), codeexecutor.Workspace{Path: ""})
	require.NoError(t, err)
}

func TestWorkspace_Cleanup_RunError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Make the /command endpoint return an HTTP error so runBash
	// receives a non-nil error with a non-nil exec (covering the
	// `if exec != nil` branch in runBash).
	m.setRunError(fmt.Errorf("command timeout"))

	err = exec.Cleanup(context.Background(), ws)
	require.Error(t, err)
}
