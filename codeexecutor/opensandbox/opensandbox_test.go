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
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestOptions(t *testing.T) {
	c := &CodeExecutor{}

	WithAPIKey("key-1")(c)
	WithDomain("example.com")(c)
	WithProtocol("https")(c)
	WithImage("my-image:1")(c)
	WithEntrypoint([]string{"sh", "-c", "sleep 1"})(c)
	WithResourceLimits(osb.ResourceLimits{"cpu": "500m"})(c)
	WithSandboxTimeout(2 * time.Minute)(c)
	WithRequestTimeout(15 * time.Second)(c)
	WithExecutionTimeout(42 * time.Second)(c)
	WithEnvVars(map[string]string{"A": "1"})(c)
	WithMetadata(map[string]string{"m": "v"})(c)
	hc := &http.Client{}
	WithHTTPClient(hc)(c)
	WithHeaders(map[string]string{"X-Test": "1"})(c)
	WithSandboxID("sbx-123")(c)
	WithSandboxRunBase("/tmp/sandbox-run")(c)
	WithWorkspacePersistence(WorkspacePersistencePerSession)(c)
	WithOutputPatterns([]string{"*.log"})(c)

	assert.Equal(t, "key-1", c.apiKey)
	assert.Equal(t, "example.com", c.domain)
	assert.Equal(t, "https", c.protocol)
	assert.Equal(t, "my-image:1", c.image)
	assert.Equal(t, []string{"sh", "-c", "sleep 1"}, c.entrypoint)
	assert.Equal(t, osb.ResourceLimits{"cpu": "500m"}, c.resourceLimits)
	assert.Equal(t, 2*time.Minute, c.sandboxTimeout)
	assert.Equal(t, 15*time.Second, c.requestTimeout)
	assert.Equal(t, 42*time.Second, c.executionTimeout)
	assert.Equal(t, map[string]string{"A": "1"}, c.envVars)
	assert.Equal(t, map[string]string{"m": "v"}, c.metadata)
	assert.Same(t, hc, c.httpClient)
	assert.Equal(t, map[string]string{"X-Test": "1"}, c.headers)
	assert.Equal(t, "sbx-123", c.sandboxID)
	assert.Equal(t, "/tmp/sandbox-run", c.sandboxRunBase)
	assert.Equal(t, WorkspacePersistencePerSession, c.workspacePersistence)
	assert.Equal(t, []string{"*.log"}, c.outputPatterns)
}

func TestCodeBlockDelimiter(t *testing.T) {
	c := &CodeExecutor{}
	d := c.CodeBlockDelimiter()
	assert.Equal(t, "```", d.Start)
	assert.Equal(t, "```", d.End)
}

func TestSandboxIDWithoutSandbox(t *testing.T) {
	c := &CodeExecutor{}
	assert.Empty(t, c.SandboxID())
	assert.Nil(t, c.Sandbox())
}

func TestDefaultOutputPatterns(t *testing.T) {
	// defaultOutputPatterns should cover common image and document
	// types so users get useful OutputFiles without configuration.
	expected := []string{
		"*.png", "*.jpg", "*.jpeg", "*.gif", "*.svg",
		"*.csv", "*.json", "*.txt", "*.html", "*.pdf",
	}
	assert.Equal(t, expected, defaultOutputPatterns)
}

func TestEnvToken(t *testing.T) {
	// envToken is the CleanEnv primitive.
	base := map[string]string{"WORKSPACE_DIR": "/ws"}
	spec := map[string]string{"FOO": "bar"}

	// Non-clean: "env WORKSPACE_DIR='/ws' FOO='bar' "
	got := envToken(base, spec, false)
	assert.Contains(t, got, "env ")
	assert.Contains(t, got, "WORKSPACE_DIR='/ws'")
	assert.Contains(t, got, "FOO='bar'")
	assert.True(t, endsWithSpace(got))

	// Clean: "env -i PATH='...' WORKSPACE_DIR='/ws' FOO='bar' "
	gotClean := envToken(base, spec, true)
	assert.Contains(t, gotClean, "env -i ")
	assert.Contains(t, gotClean, "PATH=")
	assert.Contains(t, gotClean, "WORKSPACE_DIR='/ws'")
	assert.Contains(t, gotClean, "FOO='bar'")

	// Clean with PATH in spec: minimalCleanPATH should NOT be injected.
	specWithPath := map[string]string{"PATH": "/custom/bin"}
	gotCleanPath := envToken(base, specWithPath, true)
	assert.Contains(t, gotCleanPath, "env -i ")
	assert.Contains(t, gotCleanPath, "PATH='/custom/bin'")
	assert.NotContains(t, gotCleanPath, minimalCleanPATH)

	// Non-clean with no entries: empty string.
	assert.Equal(t, "", envToken(nil, nil, false))
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, "''", shellQuote(""))
	assert.Equal(t, "'hello'", shellQuote("hello"))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
}

func TestSanitize(t *testing.T) {
	assert.Equal(t, "abc_123", sanitize("abc 123"))
	assert.Equal(t, "a_b_c", sanitize("a/b/c"))
	assert.Equal(t, "ABC-xyz", sanitize("ABC-xyz"))
}

func TestStableWorkspaceHash(t *testing.T) {
	// Same input → same hash.
	h1 := stableWorkspaceHash("exec-1")
	h2 := stableWorkspaceHash("exec-1")
	assert.Equal(t, h1, h2)
	// Different input → different hash.
	h3 := stableWorkspaceHash("exec-2")
	assert.NotEqual(t, h1, h3)
	// Hash is 16 hex chars (8 bytes → 16 hex chars).
	assert.Equal(t, 16, len(h1))
}

func TestPathUnder(t *testing.T) {
	assert.True(t, pathUnder("/ws/a", "/ws"))
	assert.True(t, pathUnder("/ws", "/ws"))
	assert.False(t, pathUnder("/wsescape", "/ws"))
	assert.False(t, pathUnder("", "/ws"))
	assert.False(t, pathUnder("/ws", ""))
}

func TestHasPathKey(t *testing.T) {
	assert.True(t, hasPathKey(map[string]string{"PATH": "/x"}, nil))
	assert.True(t, hasPathKey(nil, map[string]string{"PATH": "/x"}))
	assert.False(t, hasPathKey(map[string]string{"HOME": "/x"}, nil))
	assert.False(t, hasPathKey(nil, map[string]string{"HOME": "/x"}))
}

func TestSortedEnvKeys(t *testing.T) {
	m := map[string]string{"b": "2", "a": "1", "c": "3"}
	assert.Equal(t, []string{"a", "b", "c"}, sortedEnvKeys(m))
}

func TestB64Encode(t *testing.T) {
	// b64encode must produce standard base64 so `base64 -d` in the
	// sandbox can decode it.
	got := b64encode("hello")
	assert.Equal(t, "aGVsbG8=", got)
}

// endsWithSpace reports whether s ends with a space byte.
func endsWithSpace(s string) bool {
	return len(s) > 0 && s[len(s)-1] == ' '
}

// --- Task 5: constructor and CodeExecutor behavior tests ---
//
// These tests reuse the mock server helpers defined in
// workspace_runtime_test.go (newMockServer, newTestExecutor, etc.)
// since both test files live in the same package.

func TestNew_CreatePath_DefaultImage(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Default image and entrypoint come from the SDK constants.
	assert.Equal(t, osb.CodeInterpreterImage, exec.image)
	assert.Equal(t, osb.CodeInterpreterEntrypoint, exec.entrypoint)
	assert.True(t, exec.owned, "New without WithSandboxID should own the sandbox")
	assert.Equal(t, "sbx-mock", exec.SandboxID())
	assert.NotNil(t, exec.Sandbox())
}

func TestNew_ConnectPath_WithSandboxID(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithSandboxID("sbx-preexisting"),
	)
	require.NoError(t, err, "New connect path should succeed against mock")
	defer exec.Close()

	assert.False(t, exec.owned, "New with WithSandboxID should NOT own the sandbox")
	assert.Equal(t, "sbx-preexisting", exec.SandboxID())
}

func TestNew_WithHTTPClient_RoutesThroughCustomClient(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	hc := &http.Client{Timeout: 5 * time.Second}
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithHTTPClient(hc),
	)
	require.NoError(t, err)
	defer exec.Close()

	// The custom client must be stored on the executor and used by the
	// SDK for all requests. We verify it is wired through; a nil
	// transport would mean the SDK fell back to its default client.
	assert.Same(t, hc, exec.httpClient)
}

func TestExecuteCode_Python(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("42")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-py",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(42)"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "42")
}

func TestExecuteCode_Bash(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("hello-bash")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-sh",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo hello-bash"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "hello-bash")
}

func TestExecuteCode_UnsupportedLanguage_AggregatesAndContinues(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// First block uses an unsupported language; second block is valid
	// python. The executor should record an error for the first block
	// and continue executing the second.
	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-mixed",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "ruby", Code: "puts 'hi'"},
			{Language: "python", Code: "print('ok')"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "unsupported language", "first block error should be aggregated")
	assert.Contains(t, res.Output, "ok", "second block should still execute")
}

func TestExecuteCode_NonZeroExitCode_AggregatesAndContinues(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	// First block exits non-zero; second block succeeds.
	m.setStdout("after")
	nonZero := 1
	m.setExitCode(nonZero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-err",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "import sys; sys.exit(1)"},
			{Language: "python", Code: "print('after')"},
		},
	})
	require.NoError(t, err, "ExecuteCode should not return error for non-zero exit; it aggregates")
	assert.Contains(t, res.Output, "[exit 1]", "non-zero exit should be recorded")
	assert.Contains(t, res.Output, "after", "execution should continue after non-zero exit")
}

func TestExecuteCode_OutputFileCollection(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("done")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-files",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print('done')"},
		},
	})
	require.NoError(t, err)
	// The mock's handleSearchFiles always returns one file named
	// "output.txt" under the searched dir, and handleDownloadFile
	// returns "mock-content". Since defaultOutputPatterns includes
	// "*.txt", we should collect at least one file.
	require.NotEmpty(t, res.OutputFiles, "output files should be collected")
	assert.Contains(t, res.OutputFiles[0].Name, "output.txt")
}

func TestClose_NonOwned_DoesNotKill(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithSandboxID("sbx-preexisting"),
	)
	require.NoError(t, err)
	require.False(t, exec.owned)

	require.NoError(t, exec.Close())
	assert.Equal(t, 0, m.killCalls, "Close should NOT kill a non-owned sandbox")
}

func TestExecuteCode_EmptyExecutionID_GeneratesOne(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Empty ExecutionID should be auto-generated; the call should
	// succeed without requiring the caller to provide an ID.
	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print('ok')"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "ok")
}

func TestEngine(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// Engine exposes the sandbox-backed runtime; verify it is non-nil
	// and advertises SupportsCleanEnv.
	eng := exec.Engine()
	require.NotNil(t, eng)
	caps := eng.Describe()
	assert.True(t, caps.SupportsCleanEnv, "OpenSandbox engine should support CleanEnv")
	assert.NotNil(t, eng.Manager())
	assert.NotNil(t, eng.FS())
	assert.NotNil(t, eng.Runner())
}

func TestExecuteCode_StderrAggregation(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok\n")
	m.setStderr("warn")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	// ExecuteCode should aggregate stderr into the output with a
	// "[stderr]" prefix so users can distinguish it from stdout.
	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-stderr",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print('ok')"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "ok")
	assert.Contains(t, res.Output, "[stderr] warn", "stderr should be prefixed and aggregated into output")
}

// TestNew_RequestTimeout_ClampedToExecutionTimeout verifies that
// NewWithContext raises requestTimeout so it can cover the streaming
// /command endpoint used by RunProgram. The SDK applies
// ConnectionConfig.RequestTimeout to the HTTP client for ALL requests,
// including streaming ones; if requestTimeout < executionTimeout the
// HTTP client would kill a RunProgram call before the per-command
// execution timeout fires.
func TestNew_RequestTimeout_ClampedToExecutionTimeout(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	// requestTimeout (5s) < executionTimeout (60s) + buffer (10s) =>
	// NewWithContext must clamp requestTimeout up to 70s.
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithRequestTimeout(5*time.Second),
		WithExecutionTimeout(60*time.Second),
	)
	require.NoError(t, err)
	defer exec.Close()

	want := 60*time.Second + requestTimeoutBuffer
	assert.Equal(t, want, exec.requestTimeout,
		"requestTimeout must be clamped to executionTimeout + buffer to cover streaming /command")
}

// TestNew_RequestTimeout_PreservedWhenLargeEnough verifies that
// NewWithContext does NOT lower requestTimeout when it is already
// large enough to cover the execution timeout.
func TestNew_RequestTimeout_PreservedWhenLargeEnough(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithRequestTimeout(120*time.Second),
		WithExecutionTimeout(30*time.Second),
	)
	require.NoError(t, err)
	defer exec.Close()

	assert.Equal(t, 120*time.Second, exec.requestTimeout,
		"requestTimeout should be preserved when already >= executionTimeout + buffer")
}

// TestNew_RequestTimeout_DefaultClamped verifies that the default
// requestTimeout (SDK DefaultRequestTimeout = 30s) gets clamped above
// the default executionTimeout (30s) so streaming /command calls are
// not killed by the HTTP client at the same 30s boundary.
func TestNew_RequestTimeout_DefaultClamped(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	want := 30*time.Second + requestTimeoutBuffer
	assert.Equal(t, want, exec.requestTimeout,
		"default requestTimeout should be clamped to default executionTimeout + buffer")
}

// TestAppendStderr_EdgeCases verifies appendStderr handles empty and
// multi-line input.
func TestAppendStderr_EdgeCases(t *testing.T) {
	var out strings.Builder
	appendStderr(&out, "")
	assert.Equal(t, "", out.String())

	var out2 strings.Builder
	appendStderr(&out2, "line1\nline2\n")
	assert.Equal(t, "[stderr] line1\n[stderr] line2\n", out2.String())
}

// TestAppendError_Nil verifies appendError skips nil errors.
func TestAppendError_Nil(t *testing.T) {
	var out strings.Builder
	appendError(&out, nil)
	assert.Equal(t, "", out.String())
}

// TestEnsureRuntime_LazyInit verifies ensureRuntime creates the runtime
// on first call and reuses it on subsequent calls.
func TestEnsureRuntime_LazyInit(t *testing.T) {
	c := &CodeExecutor{}
	rt := c.ensureRuntime()
	require.NotNil(t, rt)
	rt2 := c.ensureRuntime()
	assert.Same(t, rt, rt2)
}

// TestExecuteCode_NilSandbox verifies ExecuteCode returns an error when
// the sandbox is not initialized.
func TestExecuteCode_NilSandbox(t *testing.T) {
	c := &CodeExecutor{}
	_, err := c.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-1",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(1)"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not initialized")
}

// TestNew_ExecutionTimeoutZero_ClampsToDefault verifies that
// NewWithContext uses defaultRunTimeout as the floor when
// executionTimeout is set to 0.
func TestNew_ExecutionTimeoutZero_ClampsToDefault(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithExecutionTimeout(0),
	)
	require.NoError(t, err)
	defer exec.Close()
	want := defaultRunTimeout + requestTimeoutBuffer
	assert.Equal(t, want, exec.requestTimeout)
}

// TestNew_ConnectError verifies NewWithContext returns an error when
// ConnectSandbox fails (server unreachable).
func TestNew_ConnectError(t *testing.T) {
	_, err := New(
		WithDomain("127.0.0.1:1"),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithSandboxID("sbx-nonexistent"),
		WithRequestTimeout(1*time.Second),
	)
	assert.Error(t, err)
}
