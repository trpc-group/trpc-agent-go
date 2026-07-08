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
	// killShouldFail, when true, makes DELETE /v1/sandboxes/{id} return
	// 500 so the SDK's Kill call fails. Used to test Close error path.
	killShouldFail bool
	// endpointOverride, when non-empty, is returned as the execd
	// endpoint instead of the mock server's own host:port. Used to test
	// EndpointHostRewrite by returning an unresolvable hostname that
	// the rewrite must map back to a reachable address.
	endpointOverride string
	// endpointProxyParams captures the use_server_proxy query parameter
	// values from GET /v1/sandboxes/{id}/endpoints/{port} requests.
	// Used to verify WithUseServerProxy wires through to the SDK.
	endpointProxyParams []string
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

// lastEndpointProxyParam returns the use_server_proxy query parameter
// value from the most recent GET /v1/sandboxes/{id}/endpoints/{port}
// request, or "" if none has been captured.
func (m *mockOpenSandboxServer) lastEndpointProxyParam() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.endpointProxyParams) == 0 {
		return ""
	}
	return m.endpointProxyParams[len(m.endpointProxyParams)-1]
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
		// Capture the use_server_proxy query parameter so tests can
		// verify WithUseServerProxy wired through to the SDK.
		m.mu.Lock()
		m.endpointProxyParams = append(m.endpointProxyParams, r.URL.Query().Get("use_server_proxy"))
		override := m.endpointOverride
		m.mu.Unlock()
		// Return the mock server's own URL as the execd endpoint,
		// unless an override is configured (for rewrite tests).
		endpoint := ""
		if override != "" {
			endpoint = override
		} else {
			u, _ := url.Parse(m.server.URL)
			endpoint = u.Host // host:port
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"endpoint":%q}`, endpoint)
		return
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/sandboxes/"):
		m.mu.Lock()
		m.killCalls++
		shouldFail := m.killShouldFail
		m.mu.Unlock()
		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
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
		// pathUnder passes. Entries may use "name:type" syntax to
		// set the Type field (e.g. "subdir:dir" to simulate a
		// directory result).
		entries := make([]string, 0, len(m.searchResults))
		for _, spec := range m.searchResults {
			name := spec
			fileType := ""
			if idx := strings.Index(spec, ":"); idx >= 0 {
				name = spec[:idx]
				fileType = spec[idx+1:]
			}
			p := filepath.ToSlash(filepath.Join(dir, name))
			if fileType != "" {
				entries = append(entries, fmt.Sprintf(`{"path":%q,"size":12,"type":%q}`, p, fileType))
			} else {
				entries = append(entries, fmt.Sprintf(`{"path":%q,"size":12}`, p))
			}
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

// TestWorkspace_RunProgram_TimeoutExceedsBudgetReturnsError verifies
// that RunProgram returns an error when spec.Timeout exceeds
// requestTimeout - requestTimeoutBuffer, instead of silently clamping.
// Default executor has executionTimeout=30s, so NewWithContext clamps
// requestTimeout to 40s; maxRun = 30s.
func TestWorkspace_RunProgram_TimeoutExceedsBudgetReturnsError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Record the command count after CreateWorkspace; RunProgram should
	// not add any new /command calls when it rejects the timeout.
	cmdCountBefore := len(m.commands)

	// spec.Timeout=60s exceeds maxRun=30s (requestTimeout 40s - buffer 10s).
	// RunProgram must return an error, not silently clamp.
	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"ok"},
		Timeout: 60 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the request timeout budget")
	assert.Contains(t, err.Error(), "WithRequestTimeout")
	// No /command should have been issued by RunProgram.
	assert.Equal(t, cmdCountBefore, len(m.commands),
		"no /command request should be sent when spec.Timeout exceeds budget")
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

// TestWorkspace_RunProgram_CwdEscapesWorkspace verifies that a
// spec.Cwd that resolves outside ws.Path is rejected before the
// command is sent to the sandbox. Without this check a direct
// RunProgram caller could run anywhere inside the sandbox.
func TestWorkspace_RunProgram_CwdEscapesWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"ok"},
		Cwd:     "../../etc",
		Timeout: 5 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
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

// TestExecuteInline_PerSession_NoAutoCleanup verifies that in
// WorkspacePersistencePerSession mode, ExecuteInline does NOT call
// Cleanup — the caller owns the workspace lifecycle so files written
// during one turn remain visible to later turns in the same session.
func TestExecuteInline_PerSession_NoAutoCleanup(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
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

	cmdCountBefore := len(m.commands)

	_, err = exec.ExecuteInline(context.Background(), "persist-inline", []codeexecutor.CodeBlock{
		{Language: "bash", Code: "echo ok"},
	}, 5*time.Second)
	require.NoError(t, err)

	// PerSession mode must NOT issue `rm -rf` (Cleanup) — files written
	// this turn should survive for the next turn.
	for _, cmd := range m.commands[cmdCountBefore:] {
		assert.NotContains(t, cmd, "rm -rf",
			"PerSession mode should NOT auto-cleanup after ExecuteInline")
	}
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

// mockFileInfo implements os.FileInfo for testing shouldUploadFile
// without touching the real filesystem, so tests are cross-platform
// and don't depend on symlink support (which needs admin/developer
// mode on Windows).
type mockFileInfo struct {
	mode os.FileMode
}

func (m mockFileInfo) Name() string       { return "test" }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) IsDir() bool        { return m.mode.IsDir() }
func (m mockFileInfo) Sys() any           { return nil }

// TestShouldUploadFile verifies that shouldUploadFile accepts regular
// files and rejects symlinks, devices, sockets, named pipes, and other
// non-regular entries. Complements TestWorkspace_PutDirectory_SkipsSymlinks
// by covering file types that are hard to create portably on real FS.
func TestShouldUploadFile(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{"regular file 0644", 0o644, true},
		{"regular file 0755", 0o755, true},
		{"regular file 0600", 0o600, true},
		{"symlink", os.ModeSymlink | 0o777, false},
		{"named pipe", os.ModeNamedPipe | 0o644, false},
		{"character device", os.ModeDevice | os.ModeCharDevice | 0o644, false},
		{"block device", os.ModeDevice | 0o644, false},
		{"socket", os.ModeSocket | 0o644, false},
		{"irregular", os.ModeIrregular | 0o644, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := mockFileInfo{mode: tt.mode}
			assert.Equal(t, tt.want, shouldUploadFile(info),
				"mode %s should upload=%v", tt.mode, tt.want)
		})
	}
}

// TestWorkspace_PutDirectory_SkipsSymlinks is the end-to-end regression
// test requested by WineChord: a symlink inside the staged directory
// pointing to a file outside hostPath must NOT cause the outside file
// to be uploaded into the sandbox.
//
// This test requires symlink support, which on Windows needs admin or
// developer mode. When symlink creation fails the test is skipped —
// Linux/macOS CI covers the regression path.
func TestWorkspace_PutDirectory_SkipsSymlinks(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// hostPath contains a regular file and a symlink pointing OUTSIDE
	// hostPath to a file with different content.
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("inside"), 0o644))

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside-content"), 0o644))

	linkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("symlink creation failed (Windows needs admin/developer mode): %v", err)
	}
	// Some Windows configs report success without creating a real symlink.
	fi, err := os.Lstat(linkPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Skipf("symlink not supported on this platform (lstat err: %v, mode: %v)", err, fi)
	}

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutDirectory(context.Background(), ws, tmpDir, "subdir")
	require.NoError(t, err)

	// Only real.txt should be uploaded; link.txt (symlink) must be skipped.
	// The SDK may upload an internal "metadata" file alongside user
	// files, so we check presence/absence of specific keys rather than
	// asserting an exact file count.
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.files, "real.txt", "regular file should be uploaded")
	assert.NotContains(t, m.files, "link.txt", "symlink should be skipped")
	assert.NotContains(t, m.files, "outside.txt", "outside file must NOT be uploaded via symlink")
}

// TestWorkspace_ListFilesByGlob_SkipsDirectories verifies that
// listFilesByGlob skips entries with Type "dir" returned by
// SearchFiles, only collecting regular files.
func TestWorkspace_ListFilesByGlob_SkipsDirectories(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// searchResults uses "name:type" syntax; "subdir:dir" simulates a
	// directory entry, "real.txt" is a regular file (no type).
	m.setSearchResults([]string{"subdir:dir", "real.txt"})

	out, err := exec.rt.listFilesByGlob(context.Background(), ws.Path, []string{"*"})
	require.NoError(t, err)
	require.Len(t, out, 1, "only the regular file should be collected")
	assert.Contains(t, out[0], "real.txt")
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

// TestWorkspace_NilSandbox_ErrorPaths verifies that workspaceRuntime
// methods return the "sandbox not initialized" error when the
// underlying sandbox is nil. This covers the sandbox() error branches
// in CreateWorkspace, Cleanup, PutFiles, RunProgram, Collect, runBash,
// listFilesByGlob, and ExecuteInline.
func TestWorkspace_NilSandbox_ErrorPaths(t *testing.T) {
	rt := &workspaceRuntime{ce: &CodeExecutor{}}
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}

	_, err := rt.CreateWorkspace(ctx, "exec-1", codeexecutor.WorkspacePolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not initialized")

	assert.Error(t, rt.Cleanup(ctx, ws))

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{Path: "a.txt", Content: []byte("x")}})
	assert.Error(t, err)

	_, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "echo", Timeout: 5 * time.Second})
	assert.Error(t, err)

	_, err = rt.Collect(ctx, ws, []string{"*.txt"})
	assert.Error(t, err)

	_, err = rt.runBash(ctx, "ls", 0)
	assert.Error(t, err)

	_, err = rt.listFilesByGlob(ctx, ws.Path, []string{"*.txt"})
	assert.Error(t, err)

	// ExecuteInline should also fail when CreateWorkspace fails.
	_, err = rt.ExecuteInline(ctx, "exec-1", []codeexecutor.CodeBlock{
		{Language: "python", Code: "print(1)"},
	}, 5*time.Second)
	assert.Error(t, err)
}

// TestWorkspace_PutFiles_EmptySlice verifies PutFiles returns nil for
// an empty file slice without touching the sandbox.
func TestWorkspace_PutFiles_EmptySlice(t *testing.T) {
	rt := &workspaceRuntime{ce: &CodeExecutor{}}
	err := rt.PutFiles(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"}, nil)
	assert.NoError(t, err)
}

// TestWorkspace_PutFiles_InvalidPath_DotAndRoot verifies PutFiles
// rejects paths that clean to ".", "/", or "".
func TestWorkspace_PutFiles_InvalidPath_DotAndRoot(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	for _, p := range []string{".", "/", ""} {
		err := exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
			{Path: p, Content: []byte("x"), Mode: 0o644},
		})
		require.Error(t, err, "path %q should be rejected", p)
		assert.Contains(t, err.Error(), "invalid file path")
	}
}

// TestWorkspace_Collect_EmptyPatterns verifies Collect returns nil for
// empty patterns without touching the sandbox.
func TestWorkspace_Collect_EmptyPatterns(t *testing.T) {
	rt := &workspaceRuntime{ce: &CodeExecutor{}}
	files, err := rt.Collect(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"}, nil)
	assert.NoError(t, err)
	assert.Nil(t, files)
}

// TestWorkspace_ListFilesByGlob_EmptyPatterns verifies listFilesByGlob
// returns nil for empty patterns.
func TestWorkspace_ListFilesByGlob_EmptyPatterns(t *testing.T) {
	rt := &workspaceRuntime{ce: &CodeExecutor{}}
	out, err := rt.listFilesByGlob(context.Background(), "/tmp/ws", nil)
	assert.NoError(t, err)
	assert.Nil(t, out)
}

// TestWorkspace_CreateWorkspace_PerSession_EmptyExecID verifies that
// PerSession mode rejects an empty execID.
func TestWorkspace_CreateWorkspace_PerSession_EmptyExecID(t *testing.T) {
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

	_, err = exec.CreateWorkspace(context.Background(), "", codeexecutor.WorkspacePolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execID must not be empty")
}

// TestWorkspace_PutDirectory_NilSandbox verifies PutDirectory returns
// the sandbox-not-initialized error after passing filesystem checks.
func TestWorkspace_PutDirectory_NilSandbox(t *testing.T) {
	rt := &workspaceRuntime{ce: &CodeExecutor{}}
	tmpDir := t.TempDir()
	err := rt.PutDirectory(context.Background(), codeexecutor.Workspace{Path: "/tmp/ws"}, tmpDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not initialized")
}

// TestIsTimeoutErr_Nil verifies isTimeoutErr returns false for nil.
func TestIsTimeoutErr_Nil(t *testing.T) {
	assert.False(t, isTimeoutErr(nil))
}

// TestWorkspace_Close_KillError verifies Close returns the error when
// Kill fails.
func TestWorkspace_Close_KillError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.killShouldFail = true
	exec := newTestExecutor(t, m)

	err := exec.Close()
	require.Error(t, err)
	assert.Equal(t, 1, m.killCalls)
}

// TestWorkspace_ReadFile_ServerIgnoresRange is a regression test for P1:
// when the download endpoint ignores the Range header and returns far
// more than the limit, io.LimitReader caps the read at limit+1 bytes so
// the agent process does not buffer the entire response.
func TestWorkspace_ReadFile_ServerIgnoresRange(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-range", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed a file much larger than maxReadSizeBytes. The mock's
	// handleDownloadFile ignores the Range header and returns the
	// full content.
	expectedPath := filepath.ToSlash(filepath.Join(ws.Path, "output.txt"))
	bigData := make([]byte, maxReadSizeBytes*3) // 3x the cap
	m.setDownloadData(expectedPath, bigData)

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, files[0].Truncated, "file exceeding maxReadSizeBytes should be truncated")
	assert.Equal(t, maxReadSizeBytes, len(files[0].Content), "content should be capped at maxReadSizeBytes")
}

// TestWorkspace_CollectUploadEntries_StreamsFiles is a regression test
// for P2-2: files should be opened as io.Reader (streamed via
// *os.File), not materialized into memory via os.ReadFile.
func TestWorkspace_CollectUploadEntries_StreamsFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644))

	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-stream", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	sb, err := exec.rt.sandbox()
	require.NoError(t, err)

	entries, cleanup, err := exec.rt.collectUploadEntries(context.Background(), sb, dir, ws.Path)
	require.NoError(t, err)
	defer cleanup()

	require.Len(t, entries, 1)
	_, ok := entries[0].File.(*os.File)
	assert.True(t, ok, "entry should stream via *os.File, not buffer via *bytes.Reader")
}
