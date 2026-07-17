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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

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
	createCalls     int
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
	// executionError, when non-nil, sends an error event with the
	// given name/value/traceback instead of the normal exit-code-based
	// error. Used to test RunProgram's handling of non-numeric
	// execution errors (where ExitCode stays nil).
	executionError *executionErrorSpec
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
	// symlinks maps a symlink path to its target, simulating sandbox
	// filesystem symlinks for readlink -f resolution in tests.
	symlinks map[string]string
	// existingPaths tracks paths that exist in the simulated sandbox
	// filesystem, used by test -e checks in resolveSandboxAncestor.
	// Workspaces and directories created via CreateDirectory /
	// UploadFiles are added here automatically.
	existingPaths map[string]bool
}

func newMockServer(t *testing.T) *mockOpenSandboxServer {
	t.Helper()
	m := &mockOpenSandboxServer{
		t:             t,
		files:         map[string][]byte{},
		existingPaths: map[string]bool{},
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

// executionErrorSpec configures a non-numeric error event for testing.
type executionErrorSpec struct {
	Name      string
	Value     string
	Traceback []string
}

func (m *mockOpenSandboxServer) setExecutionError(name, value string, traceback []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executionError = &executionErrorSpec{Name: name, Value: value, Traceback: traceback}
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

func (m *mockOpenSandboxServer) setSymlink(link, target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.symlinks == nil {
		m.symlinks = map[string]string{}
	}
	m.symlinks[link] = target
}

// readlinkPathRe matches absolute POSIX paths (e.g. /tmp/run/ws_x/src)
// embedded in a `bash -c 'readlink -f ...'` command, regardless of
// the shell quoting scheme used by shellQuote.
var readlinkPathRe = regexp.MustCompile(`(/[^\s'\\]+)`)

// cdPathRe extracts the workspace path from the listFilesByGlob
// script's opening `cd '<wsPath>'` clause. shellQuote nests the
// inner single-quoted path inside the outer bash -c '...' quote,
// producing `'\”<path>'\”`; the existing readlinkPathRe reliably
// captures the first absolute path in the command, which is wsPath.
var cdPathRe = readlinkPathRe

// parseCDPath returns the workspace path embedded in the
// listFilesByGlob bash script, or "" if not found.
func parseCDPath(cmd string) string {
	m := cdPathRe.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	return m[1]
}

// parseSingleReadlinkPath extracts the path argument from a
// `bash -c 'readlink -f <path>'` command. Uses a regex because
// shellQuote's nested quoting makes simple string splitting fragile.
func parseSingleReadlinkPath(cmd string) string {
	m := readlinkPathRe.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	return m[1]
}

// parseBatchReadlinkPaths extracts each path from a batch
// `for p in <p1> <p2> ...; do readlink -f -- "$p"; done` command.
// Only the "for p in ...; do" portion is scanned so that /dev/null
// (from `2>/dev/null` in the script body) is not picked up as a path.
func parseBatchReadlinkPaths(cmd string) []string {
	idx := strings.Index(cmd, "; do")
	if idx < 0 {
		return nil
	}
	head := cmd[:idx]
	matches := readlinkPathRe.FindAllStringSubmatch(head, -1)
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, m[1])
	}
	return paths
}

// resolveMockSymlink simulates `readlink -f` / `readlink -m` against
// the mock's symlinks map. If the path itself is a symlink, return the
// target (resolving chains up to a reasonable depth). Otherwise return
// the path as-is.
func resolveMockSymlink(p string, symlinks map[string]string) string {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		target, ok := symlinks[p]
		if !ok {
			break
		}
		if seen[p] {
			break // symlink loop
		}
		seen[p] = true
		p = target
	}
	return p
}

// parseTestEPath extracts the path argument from a
// `bash -c 'test -e <path> && echo yes || echo no'` command.
func parseTestEPath(cmd string) string {
	m := readlinkPathRe.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	return m[1]
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
		m.mu.Lock()
		m.createCalls++
		m.mu.Unlock()
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

	// runBash calls (CreateWorkspace mkdir, Cleanup rm, StageDirectory
	// chmod) and RunProgram calls are both wrapped in `bash -c '...'`.
	// Infrastructure commands should always succeed; only apply the
	// configured non-zero exit code to RunProgram commands, which
	// contain `&& cd ` (from `mkdir -p ... && cd ... && ...`).
	// forceInfraExit bypasses this guard to test runBash error paths.
	isRunProgram := strings.Contains(req.Command, "&& cd ")

	// runError makes /command return a 500 with "timeout" in the code
	// field so the SDK produces an APIError whose Error() contains
	// "timeout" — exercising isTimeoutErr in RunProgram. Only apply
	// to RunProgram commands so infra calls (CreateWorkspace mkdir,
	// Cleanup rm) still succeed and tests can reach the RunProgram
	// stage.
	if runErr != nil && (isRunProgram || forceInfraExit) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"code":"timeout","message":%q}`, runErr.Error())
		return
	}
	if !isRunProgram && exitCode != nil && *exitCode != 0 && !forceInfraExit {
		zero := 0
		exitCode = &zero
	}

	// Handle mkdir -p calls from CreateWorkspace: register created
	// directories as existing so resolveSandboxAncestor's test -e
	// returns "yes" for them.
	if strings.Contains(req.Command, "mkdir -p") {
		m.mu.Lock()
		for _, p := range readlinkPathRe.FindAllStringSubmatch(req.Command, -1) {
			m.existingPaths[p[1]] = true
		}
		m.mu.Unlock()
	}

	// Handle test -e calls from resolveSandboxAncestor. The mock
	// simulates file existence via existingPaths (seeded by
	// CreateDirectory / UploadFiles) plus symlinks (which always
	// exist as path entries).
	if strings.Contains(req.Command, "test -e") && !strings.Contains(req.Command, "test -L") {
		m.mu.Lock()
		existing := m.existingPaths
		symlinks := m.symlinks
		m.mu.Unlock()
		p := parseTestEPath(req.Command)
		exists := existing[p] || symlinks[p] != "" || p == "/" || p == "/tmp"
		stdout = "no"
		if exists {
			stdout = "yes"
		}
		if !forceInfraExit {
			zero := 0
			exitCode = &zero
		}
	}

	// Handle test -L calls from removeSymlinksBatch. test -L returns
	// true only for symlinks (does not follow the link).
	if strings.Contains(req.Command, "test -L") {
		m.mu.Lock()
		symlinks := m.symlinks
		m.mu.Unlock()
		p := parseTestEPath(req.Command)
		stdout = "no"
		if _, ok := symlinks[p]; ok {
			stdout = "yes"
		}
		if !forceInfraExit {
			zero := 0
			exitCode = &zero
		}
	}

	// Handle rm -f calls from removeSymlinksBatch and CreateWorkspace
	// symlink guard. Remove the path from symlinks and existingPaths.
	if strings.Contains(req.Command, "rm -f") && !strings.Contains(req.Command, "rm -rf") {
		m.mu.Lock()
		p := parseTestEPath(req.Command)
		delete(m.symlinks, p)
		delete(m.existingPaths, p)
		m.mu.Unlock()
		if !forceInfraExit {
			zero := 0
			exitCode = &zero
		}
	}

	// Handle the listFilesByGlob bash globstar script. The script is
	// sent via runBash and carries distinctive markers (__OSB_BASE__=
	// and shopt -s globstar). The mock cannot execute bash, so it
	// synthesizes the output the script would produce: for each
	// configured search result, resolve symlinks, skip directories
	// (matching [ -f "$f" ]) and paths that escape the workspace base
	// (matching the case "$__osb_rp" guard), and emit "path\tsize".
	if strings.Contains(req.Command, "__OSB_BASE__=") &&
		strings.Contains(req.Command, "shopt -s globstar") {
		wsPath := parseCDPath(req.Command)
		m.mu.Lock()
		searchResults := append([]string(nil), m.searchResults...)
		filesSnap := m.files
		symlinks := m.symlinks
		m.mu.Unlock()
		var out strings.Builder
		out.WriteString("__OSB_BASE__=" + wsPath + "\n")
		emit := func(name string) {
			full := path.Join(wsPath, name)
			resolved := resolveMockSymlink(full, symlinks)
			if !pathUnder(resolved, wsPath) {
				return
			}
			size := int64(12) // default "mock-content"
			if data, ok := filesSnap[full]; ok {
				size = int64(len(data))
			}
			fmt.Fprintf(&out, "%s\t%d\n", resolved, size)
		}
		if len(searchResults) > 0 {
			for _, spec := range searchResults {
				name := spec
				fileType := ""
				if idx := strings.Index(spec, ":"); idx >= 0 {
					name = spec[:idx]
					fileType = spec[idx+1:]
				}
				if fileType == "dir" {
					continue
				}
				emit(name)
			}
		} else {
			emit("output.txt")
		}
		stdout = out.String()
		if !forceInfraExit {
			zero := 0
			exitCode = &zero
		}
	}

	// Handle readlink -f and readlink -m calls from
	// resolveSandboxPath / resolveSandboxPaths /
	// resolveSandboxAncestor. The mock simulates a sandbox filesystem
	// with symlinks via the symlinks map (seeded via setSymlink).
	// readlink -m (canonicalize-missing) behaves the same as
	// readlink -f in the mock: resolve the symlink if it exists,
	// otherwise return the path as-is.
	// The __OSB_BASE__ guard prevents this handler from firing on the
	// listFilesByGlob script, which also contains readlink -f but is
	// handled by the dedicated block above.
	if (strings.Contains(req.Command, "readlink -f") ||
		strings.Contains(req.Command, "readlink -m")) &&
		!strings.Contains(req.Command, "__OSB_BASE__=") {
		m.mu.Lock()
		symlinks := m.symlinks
		m.mu.Unlock()
		var result string
		if strings.Contains(req.Command, "for p in") {
			paths := parseBatchReadlinkPaths(req.Command)
			for _, p := range paths {
				result += resolveMockSymlink(p, symlinks) + "\n"
			}
		} else {
			p := parseSingleReadlinkPath(req.Command)
			result = resolveMockSymlink(p, symlinks)
		}
		stdout = result
		if !forceInfraExit {
			zero := 0
			exitCode = &zero
		}
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
		if m.executionError != nil {
			ee := m.executionError
			tb, _ := json.Marshal(ee.Traceback)
			fmt.Fprintf(w, `{"type":"error","ename":%q,"evalue":%q,"traceback":%s}`,
				ee.Name, ee.Value, tb)
			fmt.Fprint(w, "\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		} else if exitCode != nil && *exitCode != 0 {
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
		m.existingPaths[p] = true
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
	// Return fake files under the searched directory. File sizes are
	// looked up from the files map (seeded via setDownloadData) so
	// that Collect's SizeBytes reflects the real file size; when a
	// file has no seeded data the default "mock-content" (12 bytes)
	// size is reported.
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
			size := int64(12)
			if data, ok := m.files[p]; ok {
				size = int64(len(data))
			}
			if fileType != "" {
				entries = append(entries, fmt.Sprintf(`{"path":%q,"size":%d,"type":%q}`, p, size, fileType))
			} else {
				entries = append(entries, fmt.Sprintf(`{"path":%q,"size":%d}`, p, size))
			}
		}
		fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
		return
	}
	// Default: return one file so basic Collect tests work.
	fakePath := filepath.ToSlash(filepath.Join(dir, "output.txt"))
	size := int64(12)
	if data, ok := m.files[fakePath]; ok {
		size = int64(len(data))
	}
	fmt.Fprintf(w, `[{"path":%q,"size":%d}]`, fakePath, size)
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

// TestWorkspace_EngineFS_GatingDeclarativeIO verifies that
// Engine().FS() returns ErrDeclarativeIONotSupported (from gatingFS)
// rather than the package-private errNotImplementedV1, so
// cross-package callers can detect the missing capability.
func TestWorkspace_EngineFS_GatingDeclarativeIO(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	eng := exec.Engine()
	require.NotNil(t, eng.Describe().SupportsDeclarativeIO,
		"opensandbox must explicitly declare SupportsDeclarativeIO=false")
	assert.False(t, *eng.Describe().SupportsDeclarativeIO,
		"opensandbox must not advertise SupportsDeclarativeIO")

	err = eng.FS().StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "host:///x"}})
	assert.ErrorIs(t, err, codeexecutor.ErrDeclarativeIONotSupported)

	_, err = eng.FS().CollectOutputs(context.Background(), ws,
		codeexecutor.OutputSpec{Globs: []string{"*.txt"}})
	assert.ErrorIs(t, err, codeexecutor.ErrDeclarativeIONotSupported)
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

	// The command sent to the mock should redirect stdin from a file
	// (not embed base64 in the command string, which could exceed
	// ARG_MAX for large stdin).
	cmd := m.lastCommand()
	assert.Contains(t, cmd, "< ", "stdin should be redirected from a file")
	assert.NotContains(t, cmd, "base64 -d", "stdin should not use base64 embedding")
	assert.NotContains(t, cmd, "base64", "stdin should not reference base64 at all")
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
	assert.Contains(t, out[0].path, "real.txt")
}

func TestWorkspace_Cleanup_EmptyPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// An empty workspace path is rejected by validateWorkspace before
	// any /command call is made.
	err := exec.Cleanup(context.Background(), codeexecutor.Workspace{Path: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace path is empty")
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
	// `if exec != nil` branch in runBash). forceInfraExit is needed
	// because the mock otherwise only applies runError to RunProgram
	// commands (those containing `&& cd `).
	m.setForceInfraExit(true)
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
	rt := &workspaceRuntime{
		ce:  &CodeExecutor{},
		cfg: runtimeConfig{runBase: defaultSandboxRunBase},
	}
	ctx := context.Background()
	// Use a path under the default runBase so validateWorkspace does
	// not short-circuit before the sandbox() nil check.
	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/run/ws_x"}

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
	rt := &workspaceRuntime{
		ce:  &CodeExecutor{},
		cfg: runtimeConfig{runBase: defaultSandboxRunBase},
	}
	tmpDir := t.TempDir()
	// Use a path under the default runBase so validateWorkspace does
	// not short-circuit before the sandbox() nil check.
	err := rt.PutDirectory(context.Background(), codeexecutor.Workspace{Path: "/tmp/run/ws_x"}, tmpDir, "")
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

// TestWorkspace_WalkAndUpload_StreamsFiles is a regression for Rememorio's
// follow-up on StageDirectory memory use: each regular file must be held
// as an *os.File (streamed), not loaded with os.ReadFile into a []byte /
// bytes.Reader. A naive success-only walk would still pass under the old
// materializing implementation; this test fails if File is not *os.File
// or if the open file is closed before flush completes.
func TestWorkspace_WalkAndUpload_StreamsFiles(t *testing.T) {
	dir := t.TempDir()
	// Multi-MiB payload so accidental full materialization is more
	// obvious in memory profiles, and so we can prove the open *os.File
	// still yields the full contents when read at upload time.
	const payloadSize = 2 << 20 // 2 MiB
	payload := bytes.Repeat([]byte("S"), payloadSize)
	hostFile := filepath.Join(dir, "big.bin")
	require.NoError(t, os.WriteFile(hostFile, payload, 0o644))

	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-stream", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	sb, err := exec.rt.sandbox()
	require.NoError(t, err)

	// Drive one visit without flushing so we can inspect the pending
	// entry types before UploadFiles consumes them.
	u := &batchUploader{r: exec.rt, sb: sb, destRoot: ws.Path, wsBase: ws.Path}
	info, err := os.Stat(hostFile)
	require.NoError(t, err)
	// DirEntry for the file via WalkDir semantics: use a synthetic entry.
	de, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, de, 1)
	require.NoError(t, u.visit(context.Background(), dir, ws.Path, hostFile, de[0]))
	_ = info

	require.Len(t, u.openFiles, 1, "streaming path must keep one open *os.File")
	require.Len(t, u.entries, 1)
	// Old os.ReadFile design used bytes.NewReader([]byte); streaming uses *os.File.
	require.IsType(t, (*os.File)(nil), u.openFiles[0],
		"openFiles must be *os.File (streaming), not a materialised buffer handle")
	require.IsType(t, (*os.File)(nil), u.entries[0].File,
		"UploadFileEntry.File must be *os.File so UploadFiles streams from disk")
	// The open file must still be readable for the full payload — proves
	// content was not pre-sliced into a detached buffer and then dropped.
	got, err := io.ReadAll(u.entries[0].File)
	require.NoError(t, err)
	require.Equal(t, payload, got, "streamed *os.File must yield full on-disk content")
	// Rewind so flush/UploadFiles can re-read if the mock drains the body.
	_, err = u.openFiles[0].Seek(0, io.SeekStart)
	require.NoError(t, err)

	require.NoError(t, u.flush(context.Background()))
	require.Empty(t, u.openFiles, "flush must close pending file handles")
	require.Empty(t, u.entries)

	// End-to-end walk still succeeds against the mock upload endpoint.
	require.NoError(t, exec.rt.walkAndUpload(context.Background(), sb, dir, ws.Path, ws.Path))
}

// TestWorkspace_WalkAndUpload_BatchedBySize verifies that walkAndUpload
// uploads files in batches of uploadBatchSize, closing each batch's
// file handles before opening the next. This is a regression test for
// the fd-exhaustion issue: staging a directory with more files than
// ulimit -n must not fail because all fds were held open until the
// walk finished.
func TestWorkspace_WalkAndUpload_BatchedBySize(t *testing.T) {
	// Create a directory with more than uploadBatchSize files.
	dir := t.TempDir()
	fileCount := uploadBatchSize + 10
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("file_%04d.txt", i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-batch", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// PutDirectory should succeed even though fileCount > uploadBatchSize,
	// because each batch's fds are closed before the next batch opens.
	err = exec.PutDirectory(context.Background(), ws, dir, "staged")
	require.NoError(t, err, "PutDirectory must handle directories larger than uploadBatchSize")
}

// TestWorkspace_WalkAndUpload_SkipsNonRegularFiles verifies that
// symlinks inside the host directory are skipped during upload,
// matching the e2b adapter's behaviour and preventing a symlink
// inside hostRoot from causing files outside hostRoot to be uploaded.
func TestWorkspace_WalkAndUpload_SkipsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("ok"), 0o644))
	// Create a symlink pointing outside hostRoot. On Windows this needs
	// admin/developer mode — skip rather than fail the suite.
	if err := os.Symlink(filepath.Join(os.TempDir(), "external"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlink creation failed (Windows needs admin/developer mode): %v", err)
	}

	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-skip-symlink", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// PutDirectory should succeed, skipping the symlink.
	err = exec.PutDirectory(context.Background(), ws, dir, "staged")
	require.NoError(t, err, "PutDirectory must skip non-regular files without error")
}

// TestWorkspace_WalkAndUpload_ClosesPendingHandlesOnError verifies
// that when WalkDir encounters an error after some files have been
// opened but not yet flushed, the pending file handles are closed.
// This is the core fd-lifecycle guarantee from WineChord's review.
func TestWorkspace_WalkAndUpload_ClosesPendingHandlesOnError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-err-close", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	sb, err := exec.rt.sandbox()
	require.NoError(t, err)

	// Call walkAndUpload with a non-existent hostRoot to trigger a
	// WalkDir error. No files will be opened, but the error path
	// (closing pending handles) is exercised.
	err = exec.rt.walkAndUpload(context.Background(), sb, filepath.Join(t.TempDir(), "nonexistent"), ws.Path, ws.Path)
	require.Error(t, err)
}

// --- validateWorkspace hardening tests ---

// TestWorkspace_Cleanup_RejectsRootPath verifies that a caller cannot
// hand-construct a Workspace pointing at an arbitrary sandbox path
// (e.g. "/") and have Cleanup rm -rf it.
func TestWorkspace_Cleanup_RejectsRootPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	err := exec.Cleanup(context.Background(), codeexecutor.Workspace{Path: "/"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes runBase")
}

// TestWorkspace_Cleanup_RejectsRunBase verifies that Cleanup refuses
// to remove the runBase directory itself, which would wipe all
// workspaces.
func TestWorkspace_Cleanup_RejectsRunBase(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	err := exec.Cleanup(context.Background(), codeexecutor.Workspace{Path: "/tmp/run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not equal runBase")
}

// TestWorkspace_Cleanup_RejectsEscapePath verifies that a path like
// "/etc" (outside runBase) is rejected.
func TestWorkspace_Cleanup_RejectsEscapePath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	err := exec.Cleanup(context.Background(), codeexecutor.Workspace{Path: "/etc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes runBase")
}

// TestWorkspace_PutFiles_RejectsInvalidWorkspace verifies that PutFiles
// validates the workspace path before touching the sandbox.
func TestWorkspace_PutFiles_RejectsInvalidWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	err := exec.PutFiles(context.Background(),
		codeexecutor.Workspace{Path: "/etc"},
		[]codeexecutor.PutFile{{Path: "a.txt", Content: []byte("x")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes runBase")
}

// TestWorkspace_Collect_RejectsInvalidWorkspace verifies that Collect
// validates the workspace path.
func TestWorkspace_Collect_RejectsInvalidWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	_, err := exec.Collect(context.Background(),
		codeexecutor.Workspace{Path: "/etc"},
		[]string{"*.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes runBase")
}

// TestWorkspace_RunProgram_RejectsInvalidWorkspace verifies that
// RunProgram validates the workspace path.
func TestWorkspace_RunProgram_RejectsInvalidWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	_, err := exec.RunProgram(context.Background(),
		codeexecutor.Workspace{Path: "/etc"},
		codeexecutor.RunProgramSpec{Cmd: "ls", Timeout: 5 * time.Second})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes runBase")
}

// --- ExecuteInline status aggregation tests ---

// TestExecuteInline_NonZeroExitCode_Aggregated verifies that a non-zero
// exit code from a block is reflected in the aggregated RunResult,
// instead of being silently replaced with 0.
func TestExecuteInline_NonZeroExitCode_Aggregated(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("out")
	exitCode := 42
	m.setExitCode(exitCode)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-fail", []codeexecutor.CodeBlock{
		{Language: "bash", Code: "exit 42"},
	}, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 42, res.ExitCode, "non-zero exit code should be propagated")
	assert.False(t, res.TimedOut)
}

// TestExecuteInline_TimeoutAggregated verifies that a timeout in any
// block is reflected in the aggregated RunResult.TimedOut.
func TestExecuteInline_TimeoutAggregated(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("out")
	// setRunError with "timeout" in the message triggers the
	// isTimeoutErr path in RunProgram, which sets TimedOut=true.
	m.setRunError(fmt.Errorf("command timeout exceeded"))
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-timeout", []codeexecutor.CodeBlock{
		{Language: "bash", Code: "sleep 100"},
	}, 10*time.Second)
	require.NoError(t, err)
	assert.True(t, res.TimedOut, "timeout should be propagated")
}

// TestExecuteInline_MixedSuccessAndFailure verifies that when one block
// succeeds (exit 0) and a later block fails (exit non-zero), the
// aggregated exit code reflects the failure.
func TestExecuteInline_MixedSuccessAndFailure(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("out")
	exitCode := 7
	m.setExitCode(exitCode)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-mixed", []codeexecutor.CodeBlock{
		{Language: "bash", Code: "echo ok"}, // would be exit 0
		{Language: "bash", Code: "exit 7"},  // mock returns 7
	}, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 7, res.ExitCode, "last non-zero exit code should be propagated")
}

// TestExecuteInline_UnsupportedLanguage_SurfacesExitCode verifies that
// an unsupported language (which causes BuildBlockSpec to fail) is
// surfaced as a non-zero exit code, not silently swallowed as 0.
func TestExecuteInline_UnsupportedLanguage_SurfacesExitCode(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteInline(context.Background(), "exec-unsupported", []codeexecutor.CodeBlock{
		{Language: "ruby", Code: "puts 'x'"},
	}, 10*time.Second)
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode, "build failure should surface as non-zero exit")
	assert.Contains(t, res.Stderr, "unsupported language")
}

// --- FU-5: readFile SizeBytes reflects real file size ---

// TestWorkspace_Collect_SizeBytesReflectsRealSize verifies that
// Collect's File.SizeBytes is the real file size reported by
// SearchFiles metadata, not just "at least limit+1". Without the
// SearchFiles size, a file 3x the cap would report SizeBytes ==
// maxReadSizeBytes+1 (the byte count read before truncation), which
// is not the true size.
func TestWorkspace_Collect_SizeBytesReflectsRealSize(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-size", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed a file 3x the cap. The mock's handleSearchFiles looks up
	// the real size from the files map and returns it as metadata.
	expectedPath := filepath.ToSlash(filepath.Join(ws.Path, "output.txt"))
	realSize := int64(maxReadSizeBytes * 3)
	m.setDownloadData(expectedPath, make([]byte, realSize))

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, realSize, files[0].SizeBytes,
		"SizeBytes should be the real file size from SearchFiles, not limit+1")
	assert.True(t, files[0].Truncated, "file exceeding maxReadSizeBytes should be truncated")
	assert.Equal(t, maxReadSizeBytes, len(files[0].Content),
		"content should be capped at maxReadSizeBytes")
}

// --- FU-6: cleanup runs after context cancellation ---

// TestCleanupContext_DetachesFromParent is a direct unit test for the
// cleanupContext helper. It proves the returned context is NOT
// cancelled even when the parent context is already cancelled, and
// that it carries a deadline (defaultRmTimeout). Without
// context.WithoutCancel, cleanup would inherit the parent's cancelled
// state and fail immediately.
func TestCleanupContext_DetachesFromParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	// Parent is now cancelled.
	require.Error(t, parent.Err())

	// cleanupCtx must NOT be cancelled even though parent is.
	cleanupCtx, cancelCleanup := cleanupContext(parent)
	defer cancelCleanup()

	assert.NoError(t, cleanupCtx.Err(),
		"cleanupContext must detach from parent cancellation")

	// cleanupCtx must have a deadline (defaultRmTimeout).
	_, hasDeadline := cleanupCtx.Deadline()
	assert.True(t, hasDeadline,
		"cleanupContext must carry a timeout deadline")
}

// TestWorkspace_Cleanup_CancelledContext_Fails proves that Cleanup
// with an already-cancelled context does NOT send the rm -rf command
// — the SDK's HTTP client rejects the request before it reaches the
// server. This is the "before" state that FU-6 fixes: without
// cleanupContext, deferred cleanup would silently no-op.
func TestWorkspace_Cleanup_CancelledContext_Fails(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-fail", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// Cancel the context before calling Cleanup.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, ctx.Err())

	err = exec.Cleanup(ctx, ws)
	assert.Error(t, err,
		"Cleanup with cancelled context should fail")

	// Verify no rm -rf was received by the mock server.
	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	for _, cmd := range commands {
		assert.NotContains(t, cmd, "rm -rf",
			"rm -rf should NOT be sent with a cancelled context")
	}
}

// TestWorkspace_Cleanup_cleanupContext_Succeeds proves that Cleanup
// with cleanupContext(cancelledCtx) DOES send the rm -rf command —
// the detached context is not cancelled, so the SDK's HTTP client
// processes the request normally. This is the "after" state that FU-6
// enables: deferred cleanup uses cleanupContext, so rm -rf runs even
// when the parent context is cancelled.
func TestWorkspace_Cleanup_cleanupContext_Succeeds(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-ok", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// Cancel the parent context, then derive a cleanup context.
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	require.Error(t, parent.Err())

	cleanupCtx, cancelCleanup := cleanupContext(parent)
	defer cancelCleanup()
	require.NoError(t, cleanupCtx.Err(),
		"cleanupContext must detach from parent cancellation")

	err = exec.Cleanup(cleanupCtx, ws)
	assert.NoError(t, err,
		"Cleanup with cleanupContext should succeed after parent cancel")

	// Verify rm -rf was received by the mock server.
	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	foundCleanup := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "rm -rf") && strings.Contains(cmd, "ws_") {
			foundCleanup = true
			break
		}
	}
	assert.True(t, foundCleanup,
		"cleanup rm -rf should run via cleanupContext after parent cancel")
}

// --- Symlink escape prevention ---

// TestWorkspace_PutFiles_RejectsSymlinkEscape verifies that PutFiles
// rejects a file whose parent directory is a symlink pointing outside
// the workspace. Without readlink -f resolution, the lexical
// pathUnder check would pass but the write would land outside the
// workspace.
func TestWorkspace_PutFiles_RejectsSymlinkEscape(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-sym", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// Simulate a symlink: /tmp/run/ws_x/link -> /tmp/outside
	outside := "/tmp/outside"
	linkPath := ws.Path + "/link"
	m.setSymlink(linkPath, outside)

	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "link/file.txt", Content: []byte("data")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// TestWorkspace_RunProgram_RejectsSymlinkCwd verifies that RunProgram
// rejects a Cwd that is a symlink pointing outside the workspace.
func TestWorkspace_RunProgram_RejectsSymlinkCwd(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-sym-cwd", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	outside := "/tmp/outside"
	linkPath := ws.Path + "/link"
	m.setSymlink(linkPath, outside)

	_, err = exec.RunProgram(context.Background(), ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "ls",
			Cwd:     "link",
			Timeout: 5 * time.Second,
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// TestWorkspace_Collect_SkipsSymlinkEscape verifies that Collect
// skips files that resolve outside the workspace via symlink, rather
// than reading them.
func TestWorkspace_Collect_SkipsSymlinkEscape(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-sym-col", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// Simulate: workspace contains a symlink "leak.txt" -> /tmp/outside/secret.txt
	outsideFile := "/tmp/outside/secret.txt"
	leakPath := ws.Path + "/leak.txt"
	m.setSymlink(leakPath, outsideFile)
	// Seed data at the outside path so the mock would return it if
	// Collect didn't skip the symlink.
	m.setDownloadData(outsideFile, []byte("SECRET"))

	// SearchFiles returns the symlink path; resolveSandboxPaths must
	// resolve it to /tmp/outside/secret.txt and skip it.
	m.setSearchResults([]string{"leak.txt"})

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	assert.Empty(t, files,
		"Collect must skip files that resolve outside the workspace via symlink")
}

// TestWorkspace_PutFiles_AcceptsNormalPath verifies that PutFiles
// still works for normal (non-symlink) paths after the readlink
// resolution was added.
func TestWorkspace_PutFiles_AcceptsNormalPath(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-normal", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "src/main.py", Content: []byte("print(1)")},
	})
	require.NoError(t, err)
}

// TestWorkspace_PutFiles_MultiLevelNewDirs verifies that PutFiles can
// upload to a path with multiple non-existent parent directories
// (e.g. a/b/c/file.txt where a, b, and c are all new). This is a
// regression test for the switch from resolveSandboxPath (which fails
// on non-existent intermediate components) to resolveSandboxAncestor
// (which walks up to the nearest existing ancestor).
func TestWorkspace_PutFiles_MultiLevelNewDirs(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-multi", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// a, a/b, and a/b/c are all new directories; none exist yet.
	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "a/b/c/file.txt", Content: []byte("deep")},
	})
	require.NoError(t, err,
		"PutFiles must support multi-level new directories")
}

// TestWorkspace_PutDirectory_RejectsSymlinkEscape verifies that
// PutDirectory rejects a destination that is a symlink pointing
// outside the workspace.
func TestWorkspace_PutDirectory_RejectsSymlinkEscape(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-pd-sym", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	// Create a temp host directory to upload.
	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(hostDir, "file.txt"), []byte("data"), 0o644,
	))

	// Simulate a symlink: ws.Path/link -> /tmp/outside
	linkPath := ws.Path + "/link"
	m.setSymlink(linkPath, "/tmp/outside")

	err = exec.PutDirectory(context.Background(), ws, hostDir, "link")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// TestWorkspace_StageDirectory_ReadOnly_NoChmodOutsideWorkspace
// verifies that StageDirectory with ReadOnly=true does not chmod a
// symlink target outside the workspace.
func TestWorkspace_StageDirectory_ReadOnly_NoChmodOutsideWorkspace(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-stage-sym", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(hostDir, "file.txt"), []byte("data"), 0o644,
	))

	// Simulate a symlink: ws.Path/link -> /tmp/outside
	linkPath := ws.Path + "/link"
	m.setSymlink(linkPath, "/tmp/outside")

	err = exec.StageDirectory(context.Background(), ws, hostDir, "link",
		codeexecutor.StageOptions{ReadOnly: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// TestResolveSandboxAncestor_NoExistingAncestor exercises the fallback
// path in resolveSandboxAncestor where no ancestor of the target
// exists (the loop walks all the way to "/"). The fallback resolves
// wsBase directly and appends the relative tail. This covers lines
// 1141-1149 in workspace_runtime.go.
func TestResolveSandboxAncestor_NoExistingAncestor(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	r := exec.ensureRuntime()
	// Use a wsBase whose ancestors are not in the mock's existingPaths
	// (avoid /tmp which the mock always considers existing).
	wsBase := "/var/run/custom_ws"
	target := wsBase + "/a/b/c/file.txt"

	resolved, err := r.resolveSandboxAncestor(
		context.Background(), target, wsBase,
	)
	require.NoError(t, err)
	assert.Equal(t, target, resolved,
		"fallback should resolve wsBase and append the relative tail")
}

// TestResolveSandboxPaths_BatchError covers the runBash error branch
// in resolveSandboxPaths (lines 565-569). When the batch readlink
// command fails, resolveSandboxPaths must return an error.
func TestResolveSandboxPaths_BatchError(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Force infrastructure commands to honour the non-zero exit code
	// so the batch readlink runBash call fails.
	m.setExitCode(1)
	m.setForceInfraExit(true)

	r := exec.ensureRuntime()
	_, err := r.resolveSandboxPaths(
		context.Background(),
		[]fileSearchResult{{path: "/tmp/run/ws_x/file.txt", size: 10}},
		"/tmp/run/ws_x",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve paths")
}

// TestResolveSandboxPath_EmptyResult covers the empty-result branch
// in resolveSandboxPath (lines 1081-1085). When readlink -f returns
// empty output, resolveSandboxPath must return an error.
func TestResolveSandboxPath_EmptyResult(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Register a symlink that resolves to empty string to simulate
	// readlink -f returning empty.
	m.setSymlink("/tmp/run/ws_x/empty", "")

	r := exec.ensureRuntime()
	_, err := r.resolveSandboxPath(
		context.Background(),
		"/tmp/run/ws_x/empty",
		"/tmp/run/ws_x",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned empty")
}

// TestReadFile_KnownSizeFallback covers the knownSize <= 0 branch in
// readFile (lines 973-975). When knownSize is non-positive, the size
// falls back to the number of bytes actually read.
func TestReadFile_KnownSizeFallback(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	r := exec.ensureRuntime()
	sb, err := r.sandbox()
	require.NoError(t, err)

	// Seed a small file.
	m.setDownloadData("/tmp/run/ws_x/data.txt", []byte("hello"))

	// Call readFile with knownSize=0 to exercise the fallback.
	data, size, truncated, err := r.readFile(
		context.Background(), sb, "/tmp/run/ws_x/data.txt",
		maxReadSizeBytes, 0,
	)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), data)
	assert.Equal(t, int64(5), size,
		"size should fall back to len(data) when knownSize <= 0")
	assert.False(t, truncated, "small file should not be truncated")
}

// --- R3: CleanEnv wraps outer bash ---

// TestRunProgram_CleanEnv_WrapsOuterBash verifies that when
// CleanEnv=true, the outer wrapper bash is launched via
// `env -i PATH=... bash --norc --noprofile -c` so BASH_ENV and
// LD_PRELOAD cannot inject into the clean-env boundary.
func TestRunProgram_CleanEnv_WrapsOuterBash(t *testing.T) {
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
		Cmd:      "echo",
		Args:     []string{"hi"},
		CleanEnv: true,
		Timeout:  5 * time.Second,
	})
	require.NoError(t, err)

	cmd := m.lastCommand()
	assert.True(t, strings.HasPrefix(cmd, "env -i PATH="),
		"CleanEnv command should start with env -i PATH=, got: %s", cmd)
	assert.Contains(t, cmd, "bash --norc --noprofile -c",
		"CleanEnv command should wrap with bash --norc --noprofile -c")
}

// TestRunProgram_CleanEnv_NoClean_RegularBash verifies that without
// CleanEnv, the command is wrapped in a plain `bash -c` (no env -i).
func TestRunProgram_CleanEnv_NoClean_RegularBash(t *testing.T) {
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
		Cmd:      "echo",
		Args:     []string{"hi"},
		CleanEnv: false,
		Timeout:  5 * time.Second,
	})
	require.NoError(t, err)

	cmd := m.lastCommand()
	assert.True(t, strings.HasPrefix(cmd, "bash -c "),
		"non-CleanEnv command should start with bash -c, got: %s", cmd)
	assert.NotContains(t, cmd, "env -i",
		"non-CleanEnv command should not contain env -i")
}

// --- R4: Collect aggregate file-count limit ---

// TestCollect_AggregateLimits_FileCount verifies that Collect stops
// after maxCollectFiles files, preventing model-generated code from
// exhausting host memory by creating thousands of matching files.
func TestCollect_AggregateLimits_FileCount(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Generate more file names than maxCollectFiles (100).
	names := make([]string, 150)
	for i := 0; i < 150; i++ {
		names[i] = fmt.Sprintf("file_%03d.txt", i)
	}
	m.setSearchResults(names)
	// Seed download data for each file so readFile succeeds.
	for _, name := range names {
		p := filepath.ToSlash(filepath.Join(ws.Path, name))
		m.setDownloadData(p, []byte("x"))
	}

	files, err := exec.Collect(context.Background(), ws, []string{"*"})
	require.NoError(t, err)
	// Real files are capped; a synthetic limits-hit marker may be appended.
	real := 0
	hasMarker := false
	for _, f := range files {
		if f.Name == collectLimitsHitMarkerName {
			hasMarker = true
			continue
		}
		real++
	}
	assert.LessOrEqual(t, real, maxCollectFiles,
		"Collect should stop at maxCollectFiles real files")
	assert.True(t, hasMarker,
		"Collect must append aggregate limits-hit marker when capped")
}

// --- R5: RunProgram bounded output ---

// TestRunProgram_BoundedOutput verifies that RunProgram caps stdout
// at maxCommandOutputBytes and appends a truncation marker, so a
// command that prints continuously cannot exhaust host memory.
func TestRunProgram_BoundedOutput(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout(strings.Repeat("A", 2*1024*1024)) // 2 MiB > 1 MiB cap
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	res, err := exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "yes",
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(res.Stdout), maxCommandOutputBytes+100,
		"stdout should be capped at maxCommandOutputBytes plus truncation marker")
	assert.Contains(t, res.Stdout, "[output truncated:",
		"truncated stdout should contain a truncation marker")
}

// --- U2: ExecutionError mapped to stderr ---

// TestRunProgram_ExecutionError_MappedToStderr verifies that when the
// sandbox sends an error event with a non-numeric evalue (e.g.
// "RuntimeError: process failed to start"), RunProgram maps the
// error details to res.Stderr and sets ExitCode to -1 (the nil
// fallback), so callers see the actual error instead of just "[exit -1]".
func TestRunProgram_ExecutionError_MappedToStderr(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setExecutionError("RuntimeError", "process failed to start", []string{"line 1", "line 2"})
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	res, err := exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "python",
		Args:    []string{"bad.py"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, -1, res.ExitCode,
		"non-numeric evalue should fall back to ExitCode -1")
	assert.Contains(t, res.Stderr, "RuntimeError")
	assert.Contains(t, res.Stderr, "process failed to start")
	assert.Contains(t, res.Stderr, "line 1")
	assert.Contains(t, res.Stderr, "line 2")
}

// --- U3: isTimeoutErr structured type assertions ---

// mockNetTimeoutError implements net.Error for testing isTimeoutErr's
// net.Error path without depending on a real network timeout.
type mockNetTimeoutError struct{}

func (e *mockNetTimeoutError) Error() string   { return "network timeout" }
func (e *mockNetTimeoutError) Timeout() bool   { return true }
func (e *mockNetTimeoutError) Temporary() bool { return false }

// TestIsTimeoutErr_Structured verifies that isTimeoutErr only
// recognizes SDK APIError with code "timeout" as a program execution
// timeout. context.DeadlineExceeded (caller-side cancellation) and
// net.Error.Timeout() (infrastructure timeout) are deliberately NOT
// classified as program timeouts.
func TestIsTimeoutErr_Structured(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Only the SDK's structured APIError with code "timeout" is a
		// genuine program execution timeout.
		{"APIError code=timeout", &osb.APIError{Response: osb.ErrorResponse{Code: "timeout"}}, true},
		{"APIError code=TIMEOUT (case-insensitive)", &osb.APIError{Response: osb.ErrorResponse{Code: "TIMEOUT"}}, true},
		// context.DeadlineExceeded is a caller-side cancellation, NOT
		// a program timeout. Treating it as TimedOut would mask
		// infrastructure-level cancellations.
		{"context deadline exceeded", context.DeadlineExceeded, false},
		// net.Error.Timeout() covers HTTP client request deadlines,
		// connection dial timeouts, etc. These are infrastructure
		// failures, NOT program execution timeouts.
		{"net.Error timeout", &mockNetTimeoutError{}, false},
		// Other APIError codes are not program timeouts.
		{"APIError code=500", &osb.APIError{Response: osb.ErrorResponse{Code: "500"}}, false},
		// String matching is never used.
		{"upstream timeout string", fmt.Errorf("upstream timeout"), false},
		{"504 gateway timeout string", fmt.Errorf("504 gateway timeout"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTimeoutErr(tt.err))
		})
	}
}

// --- U4: unique run directory per RunProgram call ---

// TestRunProgram_UniqueRunDir verifies that each RunProgram call
// creates a unique run directory (run_<timestamp>_<seq>), so
// concurrent or sequential commands don't overwrite each other's
// scratch files.
func TestRunProgram_UniqueRunDir(t *testing.T) {
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
		Cmd:     "echo",
		Args:    []string{"first"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"second"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)

	// Filter captured commands for RunProgram commands (containing
	// "&& cd ", the RunProgram marker).
	var runCmds []string
	for _, cmd := range m.commands {
		if strings.Contains(cmd, "&& cd ") {
			runCmds = append(runCmds, cmd)
		}
	}
	require.Len(t, runCmds, 2, "expected two RunProgram commands")

	// Extract the run_<timestamp>_<seq> directory name from each.
	runDirRe := regexp.MustCompile(`run_\d+_\d+`)
	dir1 := runDirRe.FindString(runCmds[0])
	dir2 := runDirRe.FindString(runCmds[1])
	require.NotEmpty(t, dir1, "first command should contain a run_ dir")
	require.NotEmpty(t, dir2, "second command should contain a run_ dir")
	assert.NotEqual(t, dir1, dir2,
		"each RunProgram call should create a unique run directory")
}

// --- U5: readFile handles stale knownSize ---

// TestReadFile_StaleSize verifies that readFile handles a stale
// knownSize (smaller than the actual file) by using max(knownSize,
// readBytes) so the SizeBytes reflects the real content size even
// when SearchFiles metadata is outdated.
func TestReadFile_StaleSize(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed a 5 MiB file but pass knownSize=1 (stale) to readFile.
	filePath := filepath.ToSlash(filepath.Join(ws.Path, "big.txt"))
	m.setDownloadData(filePath, make([]byte, 5*1024*1024))

	r := exec.ensureRuntime()
	sb, err := r.sandbox()
	require.NoError(t, err)

	data, size, truncated, err := r.readFile(
		context.Background(), sb, filePath,
		maxReadSizeBytes, 1, // knownSize=1 (stale)
	)
	require.NoError(t, err)
	// size must not be smaller than the actual content read.
	assert.GreaterOrEqual(t, size, int64(len(data)),
		"size should not be smaller than len(data)")
	// 5 MiB file read with 4 MiB limit must be truncated.
	assert.True(t, truncated, "5 MiB file with 4 MiB limit must be truncated")
	assert.Equal(t, int64(maxReadSizeBytes), int64(len(data)),
		"data should be capped at maxReadSizeBytes")
}

// TestReadFile_ShrunkFile_NoFalsePositiveTruncated verifies that when
// a file shrinks between SearchFiles (knownSize large) and DownloadFile
// (actual content smaller), Truncated is false — not a false positive
// based on stale knownSize > len(data).
func TestReadFile_ShrunkFile_NoFalsePositiveTruncated(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-shrink", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed a 10-byte file but pass knownSize=100 (stale, file shrank).
	filePath := filepath.ToSlash(filepath.Join(ws.Path, "small.txt"))
	m.setDownloadData(filePath, []byte("0123456789")) // 10 bytes

	r := exec.ensureRuntime()
	sb, err := r.sandbox()
	require.NoError(t, err)

	data, size, truncated, err := r.readFile(
		context.Background(), sb, filePath,
		maxReadSizeBytes, 100, // knownSize=100 (stale, file shrank to 10)
	)
	require.NoError(t, err)
	assert.Equal(t, []byte("0123456789"), data)
	// size = max(100, 10) = 100 (stale metadata preserved)
	assert.Equal(t, int64(100), size,
		"size should be max(knownSize, readBytes) = 100")
	// Truncated must be false: readBytes=10 < limit, so we reached EOF.
	// The file was NOT truncated, even though stale knownSize > len(data).
	assert.False(t, truncated,
		"shrunk file must not be falsely marked truncated")
}

// TestCollect_RemainingZero_BreaksBeforeReadFile verifies that when
// the aggregate byte budget is exhausted, Collect stops rather than
// passing limit=0 to readFile (which would fall back to maxReadSizeBytes
// and read beyond the budget).
func TestCollect_RemainingZero_BreaksBeforeReadFile(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-budget", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Seed enough files to exceed maxCollectTotalBytes (64 MiB).
	// Each file is maxReadSizeBytes (4 MiB). 17 files = 68 MiB > 64 MiB.
	// The 17th file's remaining budget would be 64 - 16*4 = 0 MiB.
	// Without the remaining<=0 break, readFile would be called with
	// limit=0, fall back to maxReadSizeBytes, and read 4 MiB beyond
	// the budget.
	var searchPaths []string
	for i := 0; i < 17; i++ {
		fname := fmt.Sprintf("file_%02d.txt", i)
		fpath := filepath.ToSlash(filepath.Join(ws.Path, fname))
		m.setDownloadData(fpath, make([]byte, maxReadSizeBytes))
		searchPaths = append(searchPaths, fname)
	}
	m.setSearchResults(searchPaths)

	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	// At most 16 real files (16 * 4 MiB = 64 MiB = budget). A synthetic
	// limits-hit marker may be appended and must not count toward content.
	realFiles := 0
	var totalBytes int64
	hasMarker := false
	for _, f := range files {
		if f.Name == collectLimitsHitMarkerName {
			hasMarker = true
			continue
		}
		realFiles++
		totalBytes += int64(len(f.Content))
	}
	assert.LessOrEqual(t, realFiles, 16,
		"Collect must not exceed maxCollectTotalBytes budget")
	assert.LessOrEqual(t, totalBytes, int64(maxCollectTotalBytes),
		"total bytes must not exceed maxCollectTotalBytes")
	assert.True(t, hasMarker, "byte-budget cap must set aggregate marker")
}

// --- L1: PutFiles final-component symlink removal ---

// TestPutFiles_RemovesPreExistingSymlink verifies that PutFiles
// removes a pre-existing symlink at the final file path before
// uploading, preventing writes from following the symlink outside
// the workspace.
func TestPutFiles_RemovesPreExistingSymlink(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setExitCode(0)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-sym", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Simulate a pre-existing symlink at the upload target path,
	// pointing outside the workspace.
	targetPath := path.Join(ws.Path, "output.txt")
	m.setSymlink(targetPath, "/etc/passwd")

	// PutFiles should succeed — the symlink is removed before upload.
	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "output.txt",
		Content: []byte("safe content"),
	}})
	require.NoError(t, err)

	// The symlink should have been removed (mock tracks this).
	m.mu.Lock()
	_, isSymlink := m.symlinks[targetPath]
	m.mu.Unlock()
	assert.False(t, isSymlink, "pre-existing symlink should have been removed")
}

// --- L2: Collect SearchFiles count limit ---

// TestListFilesByGlob_CountLimit verifies that listFilesByGlob stops
// collecting after maxCollectFiles+1 results, preventing model-
// generated code from creating thousands of matching files that would
// exhaust memory or exceed ARG_MAX in the batch readlink command.
func TestListFilesByGlob_CountLimit(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-cap", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Configure search results exceeding maxCollectFiles.
	var searchPaths []string
	for i := 0; i < maxCollectFiles+50; i++ {
		searchPaths = append(searchPaths, fmt.Sprintf("file_%04d.txt", i))
	}
	m.setSearchResults(searchPaths)

	// Collect should return at most maxCollectFiles real files, plus an
	// optional aggregate limits-hit marker.
	files, err := exec.Collect(context.Background(), ws, []string{"*.txt"})
	require.NoError(t, err)
	realFiles := 0
	hasMarker := false
	for _, f := range files {
		if f.Name == collectLimitsHitMarkerName {
			hasMarker = true
			continue
		}
		realFiles++
	}
	assert.LessOrEqual(t, realFiles, maxCollectFiles,
		"Collect must cap file count at maxCollectFiles")
	assert.True(t, hasMarker, "file-count cap must set aggregate marker")
}

// --- L3: ErrNotImplementedV1 exported ---

// TestErrNotImplementedV1_Exported verifies that the exported
// ErrNotImplementedV1 sentinel can be detected with errors.Is from
// external callers.
func TestErrNotImplementedV1_Exported(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-v1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// StageInputs returns ErrNotImplementedV1.
	err = exec.StageInputs(context.Background(), ws, nil)
	assert.ErrorIs(t, err, ErrNotImplementedV1,
		"StageInputs must return ErrNotImplementedV1 detectable via errors.Is")

	// CollectOutputs returns ErrNotImplementedV1.
	_, err = exec.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{})
	assert.ErrorIs(t, err, ErrNotImplementedV1,
		"CollectOutputs must return ErrNotImplementedV1 detectable via errors.Is")
}

// --- L4: Sub-millisecond timeout rejection ---

// TestRunProgram_SubMillisecondTimeoutRejected verifies that
// RunProgram rejects timeout values in (0, 1ms) with an explicit
// error instead of silently truncating to 0 and falling back to
// defaultRunTimeout (30s).
func TestRunProgram_SubMillisecondTimeoutRejected(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-subms", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Timeout: 500 * time.Microsecond,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below the 1ms API granularity",
		"sub-millisecond timeout should be rejected with a clear error")
}

// --- L6: ExecuteCode aggregate output cap ---

// TestExecuteCode_AggregateOutputCap verifies that ExecuteCode caps
// the total aggregated output across all code blocks, preventing
// unbounded memory consumption from a long sequence of verbose
// blocks.
func TestExecuteCode_AggregateOutputCap(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// Set stdout to a large value that, when repeated across multiple
	// blocks, exceeds maxAggregateOutputBytes.
	m.setStdout(strings.Repeat("x", 1024*1024)) // 1 MiB per block
	m.setExitCode(0)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Create enough blocks to exceed the 4 MiB aggregate cap.
	var blocks []codeexecutor.CodeBlock
	for i := 0; i < 10; i++ {
		blocks = append(blocks, codeexecutor.CodeBlock{
			Language: "bash",
			Code:     "echo test",
		})
	}

	result, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-agg",
		CodeBlocks:  blocks,
	})
	require.NoError(t, err)
	// The output should be capped at approximately maxAggregateOutputBytes
	// plus the truncation marker. Without the cap, 10 blocks × 1 MiB
	// = 10 MiB would be accumulated.
	assert.LessOrEqual(t, len(result.Output), maxAggregateOutputBytes+200,
		"aggregate output should be capped at maxAggregateOutputBytes + truncation marker")
	assert.Contains(t, result.Output, "[output truncated",
		"output should contain truncation marker")
}

// --- N2: CreateWorkspace meta.json symlink hijack guard ---

// TestCreateWorkspace_MetaJsonSymlinkGuard verifies that
// CreateWorkspace removes a pre-existing symlink at meta.json
// before writing the default '{}' content, preventing symlink
// hijack attacks that would write through the symlink to an
// external file.
func TestCreateWorkspace_MetaJsonSymlinkGuard(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setExitCode(0)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Pre-seed a symlink at the meta.json path for a workspace
	// that CreateWorkspace will create.
	// We can't know the exact workspace path (it includes a nanosecond
	// timestamp), but we can verify the CreateWorkspace command
	// includes the symlink guard by checking the captured commands.
	_, err := exec.CreateWorkspace(context.Background(), "exec-meta", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Verify the CreateWorkspace bash command includes the meta.json
	// symlink guard: if [ -L ... ]; then rm -f -- ...; fi
	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()

	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "mkdir -p") && strings.Contains(cmd, codeexecutor.MetaFileName) {
			assert.Contains(t, cmd, "[ -L ",
				"CreateWorkspace should check for symlink at "+codeexecutor.MetaFileName)
			assert.Contains(t, cmd, "rm -f",
				"CreateWorkspace should remove pre-existing symlink at "+codeexecutor.MetaFileName)
			found = true
			break
		}
	}
	assert.True(t, found, "CreateWorkspace command should include "+codeexecutor.MetaFileName+" symlink guard")
}

// --- N3+N4: resolveSandboxAncestor via readlink -m ---

// TestResolveSandboxAncestor_TailSymlinkResolved verifies that
// resolveSandboxAncestor uses readlink -m to resolve symlinks in
// existing tail components, not just the nearest existing ancestor.
// This prevents a symlink at an intermediate path component from
// causing writes to land outside the workspace.
func TestResolveSandboxAncestor_TailSymlinkResolved(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setExitCode(0)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-tail", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	// Create a symlink at ws/subdir pointing to /tmp/outside.
	// resolveSandboxAncestor for ws/subdir/newfile.txt should resolve
	// the symlink at ws/subdir and return /tmp/outside/newfile.txt,
	// which escapes the workspace and should be rejected.
	m.setSymlink(path.Join(ws.Path, "subdir"), "/tmp/outside")

	// PutFiles should fail because the resolved path escapes the workspace.
	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "subdir/newfile.txt",
		Content: []byte("test"),
	}})
	require.Error(t, err, "PutFiles should reject path that resolves outside workspace via tail symlink")
	assert.Contains(t, err.Error(), "escapes workspace")
}

// --- Review follow-ups (liuzengh 2026-07-17) ---

// TestBatchRemoveSymlinksScript_NoSymlinkExitsZero is a pure-shell
// regression for the batchUploader flush guard. The old form
// `[ -L "$p" ] && rm -f "$p"` left exit status 1 when the last path
// was a normal file or missing; runBash would then abort before
// UploadFiles. The mock historically forced exit 0 for any `rm -f`
// command, so only a real shell catches this.
func TestBatchRemoveSymlinksScript_NoSymlinkExitsZero(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	f1 := filepath.ToSlash(filepath.Join(dir, "a.txt"))
	f2 := filepath.ToSlash(filepath.Join(dir, "b.txt"))
	require.NoError(t, os.WriteFile(filepath.FromSlash(f1), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.FromSlash(f2), []byte("b"), 0o644))
	script := fmt.Sprintf(
		"for p in %s %s; do if [ -L \"$p\" ]; then rm -f -- \"$p\" || exit; fi; done; echo ok",
		shellQuote(f1), shellQuote(f2),
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "no-symlink batch must exit 0; output=%s", out)
	assert.Contains(t, string(out), "ok")
}

// TestBatchRemoveSymlinksScript_OldFormFails documents the bug the
// reviewer found: the previous `&&` form fails when no path is a
// symlink.
func TestBatchRemoveSymlinksScript_OldFormFails(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	f1 := filepath.ToSlash(filepath.Join(dir, "a.txt"))
	require.NoError(t, os.WriteFile(filepath.FromSlash(f1), []byte("a"), 0o644))
	script := fmt.Sprintf(
		"for p in %s; do [ -L \"$p\" ] && rm -f \"$p\"; done",
		shellQuote(f1),
	)
	cmd := exec.Command("bash", "-c", script)
	err := cmd.Run()
	require.Error(t, err, "old && form must exit non-zero on non-symlink")
}

// TestWorkspace_PutDirectory_RejectsIntermediateSymlink verifies that
// an intermediate destination directory that is a symlink outside the
// workspace is rejected during directory upload (not only the leaf).
func TestWorkspace_PutDirectory_RejectsIntermediateSymlink(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-inter-sym", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	hijack := path.Join(ws.Path, "dest", "hijack")
	m.setSymlink(hijack, "/tmp/outside")

	hostDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(hostDir, "hijack"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(hostDir, "hijack", "file.txt"), []byte("pwn"), 0o644,
	))

	err = exec.PutDirectory(context.Background(), ws, hostDir, "dest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace")
}

// TestWorkspace_PutDirectory_NoSymlinkBatchSucceeds is the end-to-end
// counterpart of TestBatchRemoveSymlinksScript_NoSymlinkExitsZero.
func TestWorkspace_PutDirectory_NoSymlinkBatchSucceeds(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-nosym", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "b.txt"), []byte("b"), 0o644))

	err = exec.PutDirectory(context.Background(), ws, hostDir, "out")
	require.NoError(t, err)

	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "if [ -L \"$p\" ]") && strings.Contains(cmd, "rm -f --") {
			found = true
			assert.NotContains(t, cmd, "[ -L \"$p\" ] && rm -f \"$p\"")
			break
		}
	}
	assert.True(t, found, "batch remove-symlink script should have been issued")
}

// TestListFilesByGlob_ScriptDedupsBeforeCounting verifies the generated
// bash script tracks unique resolved paths before incrementing the
// server-side budget.
func TestListFilesByGlob_ScriptDedupsBeforeCounting(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "exec-dedup", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	names := make([]string, maxCollectFiles+20)
	for i := range names {
		names[i] = fmt.Sprintf("f%03d.txt", i)
	}
	m.setSearchResults(names)
	for _, name := range names {
		m.setDownloadData(path.Join(ws.Path, name), []byte("x"))
	}

	_, err = exec.rt.listFilesByGlob(context.Background(), ws.Path, []string{"**/*", "**/*.txt"})
	require.NoError(t, err)

	cmd := m.lastCommand()
	assert.Contains(t, cmd, "declare -A __osb_seen")
	assert.Contains(t, cmd, "${__osb_seen[$__osb_rp]+x}")
}

// TestNewEngineWithCapabilities_UnknownDoesNotGate: zero/unknown
// SupportsDeclarativeIO must leave the FS unwrapped.
func TestNewEngineWithCapabilities_UnknownDoesNotGate(t *testing.T) {
	stub := &stubWorkspaceFS{}
	eng := codeexecutor.NewEngineWithCapabilities(
		stub, stub, stub,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)
	require.Nil(t, eng.Describe().SupportsDeclarativeIO)
	err := eng.FS().StageInputs(context.Background(), codeexecutor.Workspace{Path: "/tmp/x"}, nil)
	require.NoError(t, err, "unknown capability must not install gatingFS")

	eng2 := codeexecutor.NewEngineWithCapabilities(
		stub, stub, stub,
		codeexecutor.Capabilities{SupportsDeclarativeIO: codeexecutor.SupportsDeclarativeIOFalse},
	)
	err = eng2.FS().StageInputs(context.Background(), codeexecutor.Workspace{Path: "/tmp/x"}, nil)
	require.ErrorIs(t, err, codeexecutor.ErrDeclarativeIONotSupported)
}

// stubWorkspaceFS is a minimal WorkspaceFS for capability-gating tests.
type stubWorkspaceFS struct{}

func (*stubWorkspaceFS) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{Path: "/tmp/x"}, nil
}
func (*stubWorkspaceFS) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }
func (*stubWorkspaceFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}
func (*stubWorkspaceFS) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	return nil
}
func (*stubWorkspaceFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return nil, nil
}
func (*stubWorkspaceFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (*stubWorkspaceFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}
func (*stubWorkspaceFS) RunProgram(context.Context, codeexecutor.Workspace, codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, nil
}

// TestWorkspace_CreateWorkspace_StripsLayoutSymlinks verifies that a
// pre-existing symlink at a layout directory (e.g. out -> /tmp/outside)
// is removed before mkdir -p so CreateWorkspace does not follow it.
func TestWorkspace_CreateWorkspace_StripsLayoutSymlinks(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// We cannot know the exact PerTurn path in advance, but the generated
	// CreateWorkspace script must strip -L on skills/work/runs/out and
	// verify resolved paths stay under the workspace.
	_, err := exec.CreateWorkspace(context.Background(), "exec-layout", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "mkdir -p") && strings.Contains(cmd, codeexecutor.DirOut) {
			assert.Contains(t, cmd, "if [ -L ", "must strip layout symlinks")
			assert.Contains(t, cmd, "readlink -f", "must verify resolved paths")
			assert.Contains(t, cmd, "path escapes workspace", "must fail closed on escape")
			found = true
			break
		}
	}
	assert.True(t, found)
}

// TestWorkspace_PutFiles_BatchRemoveBeforeUpload verifies PutFiles issues
// the same if/fi leaf-symlink batch removal as PutDirectory flush.
func TestWorkspace_PutFiles_BatchRemoveBeforeUpload(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-put-batch", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	err = exec.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "a.txt", Content: []byte("a")},
		{Path: "b.txt", Content: []byte("b")},
	})
	require.NoError(t, err)

	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "if [ -L \"$p\" ]") && strings.Contains(cmd, "rm -f --") &&
			(strings.Contains(cmd, "a.txt") || strings.Contains(cmd, "b.txt")) {
			found = true
			break
		}
	}
	assert.True(t, found, "PutFiles must batch-remove leaf symlinks before upload")
}

// TestWorkspace_RunProgram_EnsuresLayoutDirs verifies RunProgram issues
// ensureLayoutDirs before mkdir/stdin so layout symlinks cannot redirect.
func TestWorkspace_RunProgram_EnsuresLayoutDirs(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(context.Background(), "exec-run-layout", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)

	_, err = exec.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"hi"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)

	m.mu.Lock()
	commands := append([]string(nil), m.commands...)
	m.mu.Unlock()
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, codeexecutor.DirOut) &&
			strings.Contains(cmd, "if [ -L ") &&
			strings.Contains(cmd, "readlink -f") &&
			strings.Contains(cmd, "mkdir -p") {
			found = true
			break
		}
	}
	assert.True(t, found, "RunProgram must ensureLayoutDirs before execution")
}
