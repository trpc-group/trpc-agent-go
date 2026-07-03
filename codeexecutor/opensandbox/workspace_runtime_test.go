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

	mu          sync.Mutex
	commands    []string          // captured /command request bodies
	files       map[string][]byte // simulated sandbox filesystem
	dirsCreated []string
	killCalls   int
	pauseCalls  int
	// exitCode controls the exit_code in execution_complete events.
	// nil means no exit_code field (test the nil → -1 fallback).
	exitCode *int
	stdout   string
	stderr   string
	// noComplete, when true, omits the execution_complete event so
	// Execution.ExitCode stays nil (tests the -1 fallback).
	noComplete bool
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

func (m *mockOpenSandboxServer) lastCommand() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commands) == 0 {
		return ""
	}
	return m.commands[len(m.commands)-1]
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
	}
	_ = json.Unmarshal(body, &req)
	m.mu.Lock()
	m.commands = append(m.commands, req.Command)
	exitCode := m.exitCode
	stdout := m.stdout
	stderr := m.stderr
	noComplete := m.noComplete
	m.mu.Unlock()

	// runBash calls (CreateWorkspace mkdir, Cleanup rm, StageDirectory
	// chmod) wrap their script as `bash -c '...'`. These infrastructure
	// commands should always succeed; only apply the configured non-zero
	// exit code to RunProgram commands (which start with `mkdir -p ... &&
	// cd ...`).
	if strings.HasPrefix(req.Command, "bash -c ") && exitCode != nil && *exitCode != 0 {
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
		// because v1.0.2 SDK's execution_complete handler ignores the
		// exit_code field and only defaults to 0 when no error occurred.
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
	// Return one fake file under the searched directory.
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	// Return a file path that's under the searched dir so pathUnder
	// passes.
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
	// injected FOO='bar'.
	cmd := m.lastCommand()
	assert.Contains(t, cmd, "env -i")
	assert.Contains(t, cmd, "FOO='bar'")
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
