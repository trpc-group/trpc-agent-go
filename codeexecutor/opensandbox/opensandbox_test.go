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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
	WithUseServerProxy(true)(c)
	hostRewrites := map[string]string{"host.docker.internal": "localhost"}
	WithEndpointHostRewrite(hostRewrites)(c)

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
	assert.True(t, c.useServerProxy, "WithUseServerProxy(true) should set useServerProxy")
	assert.Equal(t, hostRewrites, c.endpointHostRewrite, "WithEndpointHostRewrite should set endpointHostRewrite")
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
	got, err := envToken(base, spec, false)
	require.NoError(t, err)
	assert.Contains(t, got, "env ")
	assert.Contains(t, got, "WORKSPACE_DIR='/ws'")
	assert.Contains(t, got, "FOO='bar'")
	assert.True(t, endsWithSpace(got))

	// Clean: "env -i PATH='...' WORKSPACE_DIR='/ws' FOO='bar' "
	gotClean, err := envToken(base, spec, true)
	require.NoError(t, err)
	assert.Contains(t, gotClean, "env -i ")
	assert.Contains(t, gotClean, "PATH=")
	assert.Contains(t, gotClean, "WORKSPACE_DIR='/ws'")
	assert.Contains(t, gotClean, "FOO='bar'")

	// Clean with PATH in spec: minimalCleanPATH should NOT be injected.
	specWithPath := map[string]string{"PATH": "/custom/bin"}
	gotCleanPath, err := envToken(base, specWithPath, true)
	require.NoError(t, err)
	assert.Contains(t, gotCleanPath, "env -i ")
	assert.Contains(t, gotCleanPath, "PATH='/custom/bin'")
	assert.NotContains(t, gotCleanPath, minimalCleanPATH)

	// Non-clean with no entries: empty string.
	gotEmpty, err := envToken(nil, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "", gotEmpty)
}

// TestEnvToken_RejectsInvalidVarName verifies that envToken rejects
// environment variable names containing shell metacharacters, preventing
// command injection through the env-assignment token.
func TestEnvToken_RejectsInvalidVarName(t *testing.T) {
	invalidNames := []string{
		"A;touch /tmp/pwned",
		"X=$(touch /tmp/pwned)",
		"X Y",
		"X=Y",
		"X\nPATH",
		"",
		"1ABC",
	}
	for _, name := range invalidNames {
		_, err := envToken(nil, map[string]string{name: "v"}, false)
		assert.Error(t, err, "expected error for invalid env name %q", name)
		assert.Contains(t, err.Error(), "invalid environment variable name")
	}
	// Also test invalid base key.
	_, err := envToken(map[string]string{"BAD;KEY": "v"}, nil, false)
	assert.Error(t, err)
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

// TestNew_WithHTTPClient_DoesNotMutateCallerClient is a regression
// test for WineChord's review comment: the SDK's WithTimeout option
// writes httpClient.Timeout in place when the client has a custom
// Transport. Without cloning, the caller's shared client would have
// its Timeout changed, affecting unrelated auth/proxy/mesh traffic
// reusing the same client. NewWithContext must pass a clone to the
// SDK so the caller's Timeout is preserved.
func TestNew_WithHTTPClient_DoesNotMutateCallerClient(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	originalTimeout := 5 * time.Second
	hc := &http.Client{
		Timeout:   originalTimeout,
		Transport: http.DefaultTransport, // non-nil Transport triggers the SDK's in-place write
	}
	_, err = New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithHTTPClient(hc),
	)
	require.NoError(t, err)

	// The caller's client must retain its original Timeout; the SDK
	// must have written its clamped timeout to a clone, not the
	// original.
	assert.Equal(t, originalTimeout, hc.Timeout,
		"caller's HTTP client Timeout must not be mutated by NewWithContext")
}

// TestNew_WithOutputPatterns_DoesNotAliasCallerSlice verifies that
// WithOutputPatterns copies the caller's slice so subsequent
// modifications to the original slice do not change executor behaviour.
// Without the copy, mutating patterns after construction would silently
// change which files Collect harvests, and concurrent mutation would
// be a data race.
func TestNew_WithOutputPatterns_DoesNotAliasCallerSlice(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	patterns := []string{"*.txt", "*.csv"}
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithOutputPatterns(patterns),
	)
	require.NoError(t, err)
	defer exec.Close()

	// Mutate the original slice after construction.
	patterns[0] = "**/*"
	patterns[1] = "*.secret"

	// The executor's patterns must be unaffected.
	assert.Equal(t, []string{"*.txt", "*.csv"}, exec.outputPatterns,
		"WithOutputPatterns must copy the caller's slice")
}

// TestNew_WithSandboxTimeout_RejectsSubSecond verifies that a sandbox
// timeout between 0 and 1 second is rejected with a clear error rather
// than silently truncated to 0 (which the server may interpret as
// immediate expiry or no timeout).
func TestNew_WithSandboxTimeout_RejectsSubSecond(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	_, err = New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithSandboxTimeout(500*time.Millisecond),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be at least 1s")

	// The mock should not have received a create call — the error
	// fires before CreateSandbox.
	assert.Equal(t, 0, m.createCalls,
		"sub-second timeout must be rejected before CreateSandbox")
}

// TestNew_UseServerProxy_PassedToSDK verifies that WithUseServerProxy(true)
// causes the SDK to send use_server_proxy=true in the GET
// /v1/sandboxes/{id}/endpoints/{port} query string. The SDK calls
// GetEndpoint during CreateSandbox (resolveExecd), so by the time New()
// returns the mock has captured the parameter.
func TestNew_UseServerProxy_PassedToSDK(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithUseServerProxy(true),
	)
	require.NoError(t, err, "New should succeed against mock")
	defer exec.Close()

	assert.Equal(t, "true", m.lastEndpointProxyParam(),
		"WithUseServerProxy(true) should cause use_server_proxy=true query param")
}

// TestNew_UseServerProxy_DefaultFalse verifies that without
// WithUseServerProxy, the SDK sends use_server_proxy=false (the default).
func TestNew_UseServerProxy_DefaultFalse(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)
	defer exec.Close()

	assert.Equal(t, "false", m.lastEndpointProxyParam(),
		"Default should be use_server_proxy=false")
}

// TestNew_EndpointHostRewrite_RewritesUnresolvableHost verifies that
// WithEndpointHostRewrite rewrites the endpoint URL returned by the
// server. The mock returns an unresolvable hostname
// ("nonexistent.invalid:<port>"); without the rewrite, New() would fail
// because WaitUntilReady cannot ping the execd endpoint. With the rewrite
// mapping "nonexistent.invalid" → the mock's actual hostname, New()
// succeeds and ExecuteCode works.
func TestNew_EndpointHostRewrite_RewritesUnresolvableHost(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	// Make the mock return an unresolvable hostname with the mock's port.
	m.endpointOverride = "nonexistent.invalid:" + u.Port()

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithEndpointHostRewrite(map[string]string{
			"nonexistent.invalid": u.Hostname(),
		}),
	)
	require.NoError(t, err, "New should succeed when endpoint rewrite maps to a reachable host")
	defer exec.Close()

	// ExecuteCode exercises the full execd path (resolveExecd → /command),
	// proving the rewritten endpoint is actually used.
	m.setStdout("42")
	zero := 0
	m.setExitCode(zero)
	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-rewrite",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(42)"},
		},
	})
	require.NoError(t, err, "ExecuteCode should succeed with rewritten endpoint")
	assert.Contains(t, res.Output, "42")
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
	// and does not advertise SupportsCleanEnv (execd env merge).
	eng := exec.Engine()
	require.NotNil(t, eng)
	caps := eng.Describe()
	assert.False(t, caps.SupportsCleanEnv, "OpenSandbox must not advertise CleanEnv: execd merges os.Environ")
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

// TestNew_RequestTimeout_ZeroResolvesToDefault verifies that
// WithRequestTimeout(0) resolves to the SDK default and the clamp
// logic fires, so the RunProgram budget check is not bypassed.
func TestNew_RequestTimeout_ZeroResolvesToDefault(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithRequestTimeout(0),
	)
	require.NoError(t, err)
	defer exec.Close()

	// WithRequestTimeout(0) should resolve to osb.DefaultRequestTimeout,
	// then be clamped up to executionTimeout + buffer (30s + 10s = 40s).
	want := 30*time.Second + requestTimeoutBuffer
	assert.Equal(t, want, exec.requestTimeout,
		"WithRequestTimeout(0) should resolve to SDK default and be clamped")
}

// TestAppendStderr_EdgeCases verifies appendStderr handles empty and
// multi-line input.
func TestAppendStderr_EdgeCases(t *testing.T) {
	var out cappedOutputBuffer
	appendStderr(&out, "")
	assert.Equal(t, "", out.String())

	var out2 cappedOutputBuffer
	appendStderr(&out2, "line1\nline2\n")
	assert.Equal(t, "[stderr] line1\n[stderr] line2\n", out2.String())
}

// TestAppendError_Nil verifies appendError skips nil errors.
func TestAppendError_Nil(t *testing.T) {
	var out cappedOutputBuffer
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

// TestExecuteCode_PerSession_NoAutoCleanup is a regression test for
// P2-1: in PerSession mode, ExecuteCode should NOT auto-cleanup the
// workspace so files remain visible across turns.
func TestExecuteCode_PerSession_NoAutoCleanup(t *testing.T) {
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

	_, err = exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-session",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo ok"},
		},
	})
	require.NoError(t, err)

	// No "rm -rf" command should have been issued in PerSession mode.
	for _, cmd := range m.commands {
		assert.NotContains(t, cmd, "rm -rf",
			"PerSession mode should not auto-cleanup")
	}
}

// TestExecuteCode_PerTurn_AutoCleanup verifies that in the default
// PerTurn mode, ExecuteCode DOES call Cleanup after execution.
func TestExecuteCode_PerTurn_AutoCleanup(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	zero := 0
	m.setExitCode(zero)
	exec := newTestExecutor(t, m)
	defer exec.Close()

	cmdCountBefore := len(m.commands)

	_, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "perturn-cleanup-test",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo ok"},
		},
	})
	require.NoError(t, err)

	// In PerTurn mode ExecuteCode must issue `rm -rf` (Cleanup).
	foundCleanup := false
	for _, cmd := range m.commands[cmdCountBefore:] {
		if strings.Contains(cmd, "rm -rf") {
			foundCleanup = true
			break
		}
	}
	assert.True(t, foundCleanup,
		"PerTurn mode should auto-cleanup the workspace")
}

// TestWithImage_EmptyStringKeepsDefault verifies that WithImage("")
// does not clear the SDK default image. Without this guard an explicit
// empty string would overwrite the default and trigger an SDK
// "missing image" error at sandbox creation time.
func TestWithImage_EmptyStringKeepsDefault(t *testing.T) {
	c := &CodeExecutor{image: osb.CodeInterpreterImage}
	WithImage("")(c)
	assert.Equal(t, osb.CodeInterpreterImage, c.image,
		"WithImage(\"\") should not clear the default image")
}

// TestWithEntrypoint_EmptyKeepsDefault verifies that
// WithEntrypoint(nil) and WithEntrypoint([]string{}) do not clear the
// SDK default entrypoint. Without this guard an explicit empty value
// would fall through to `tail -f /dev/null` instead of the code
// interpreter entrypoint.
func TestWithEntrypoint_EmptyKeepsDefault(t *testing.T) {
	c := &CodeExecutor{entrypoint: osb.CodeInterpreterEntrypoint}
	WithEntrypoint(nil)(c)
	assert.Equal(t, osb.CodeInterpreterEntrypoint, c.entrypoint,
		"WithEntrypoint(nil) should not clear the default entrypoint")

	WithEntrypoint([]string{})(c)
	assert.Equal(t, osb.CodeInterpreterEntrypoint, c.entrypoint,
		"WithEntrypoint([]string{}) should not clear the default entrypoint")
}

// --- FU-7: WithSandboxRunBase path validation ---

// TestValidateRunBase_RejectsRelativePath verifies that a non-absolute
// runBase is rejected. Relative paths break the pathUnder precondition
// (which expects absolute POSIX paths) and could allow workspace
// creation in unintended locations.
func TestValidateRunBase_RejectsRelativePath(t *testing.T) {
	err := validateRunBase("tmp/run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an absolute path")
}

// TestValidateRunBase_RejectsRoot verifies that "/" is rejected as a
// runBase. If "/" were allowed, validateWorkspace's "path must not
// equal runBase" check would reject every workspace (since no path
// can be under "/" without being... under "/"), and more critically,
// a workspace path of "/tmp" would pass pathUnder but Cleanup rm -rf
// on "/" would be catastrophic if the path check were ever bypassed.
func TestValidateRunBase_RejectsRoot(t *testing.T) {
	err := validateRunBase("/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must not be`)
}

// TestValidateRunBase_RejectsDotDotEscape verifies that a runBase
// containing ".." components is rejected. Without this,
// WithSandboxRunBase("/tmp/run/../../etc") would be path.Cleaned to
// "/etc", allowing workspace creation under arbitrary directories.
func TestValidateRunBase_RejectsDotDotEscape(t *testing.T) {
	err := validateRunBase("/tmp/run/../../etc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `".." escape`)
}

// TestValidateRunBase_AcceptsValidPaths verifies that normal absolute
// paths and the empty string (use default) are accepted.
func TestValidateRunBase_AcceptsValidPaths(t *testing.T) {
	assert.NoError(t, validateRunBase(""))
	assert.NoError(t, validateRunBase("/tmp/run"))
	assert.NoError(t, validateRunBase("/tmp/run/"))
	assert.NoError(t, validateRunBase("/home/user/workspaces"))
}

// TestNew_WithSandboxRunBase_RejectsInvalid verifies that
// NewWithContext returns an error when WithSandboxRunBase is given an
// invalid path, rather than silently creating a workspace runtime
// with an escape-prone base. The error must fire BEFORE
// CreateSandbox/ConnectSandbox is called, otherwise the caller cannot
// obtain the CodeExecutor to Close() it and the sandbox leaks until
// the server-side timeout fires.
func TestNew_WithSandboxRunBase_RejectsInvalid(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)
	host := u.Host

	_, err = NewWithContext(context.Background(),
		WithAPIKey("test"),
		WithDomain(host),
		WithProtocol("http"),
		WithSandboxRunBase("/tmp/run/../../etc"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `".." escape`)

	// The sandbox create endpoint must NOT have been called —
	// validateRunBase must fire before CreateSandbox/ConnectSandbox
	// so an invalid config does not leak a remote sandbox.
	m.mu.Lock()
	createCalls := m.createCalls
	m.mu.Unlock()
	assert.Equal(t, 0, createCalls,
		"validateRunBase must reject before CreateSandbox is called")
}

// TestExecuteCode_PerSession_EmptyExecID_ReturnsError verifies that without ExecutionID and without session in context
// in PerSession mode, an empty ExecutionID is rejected with a clear
// error rather than generating a random fallback (which would defeat
// workspace persistence and leak workspaces). The error must fire
// before CreateWorkspace is called.
func TestExecuteCode_PerSession_EmptyExecID_ReturnsError(t *testing.T) {
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

	createCallsBefore := m.createCalls
	cmdsBefore := len(m.commands)

	_, err = exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo ok"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
	// No workspace creation should have occurred — the empty-execID
	// check fires before CreateWorkspace.
	assert.Equal(t, createCallsBefore, m.createCalls,
		"no additional sandbox creation should occur when execID is empty")
	assert.Equal(t, cmdsBefore, len(m.commands),
		"no workspace creation commands should be sent when execID is empty")
}

// TestExecuteCode_TimedOut_SurfacesTimeout verifies that when a
// RunProgram command times out, ExecuteCode surfaces the timeout via
// the result output (not as a returned error) with a "[timeout:"
// marker so callers can distinguish timeouts from successes.
func TestExecuteCode_TimedOut_SurfacesTimeout(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setRunError(fmt.Errorf("command timeout exceeded"))
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-timeout-surface",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo ok"},
		},
	})
	require.NoError(t, err, "timeout should be surfaced via result, not error")
	assert.Contains(t, res.Output, "[timeout:",
		"output should contain a timeout marker")
}

// TestExecuteCode_NonZeroExit_StderrNotDuplicated verifies that when
// a command exits non-zero with stderr, the stderr text is not
// duplicated in the output. The "[exit N]" line must be a bare
// status marker, not followed by the stderr text.
func TestExecuteCode_NonZeroExit_StderrNotDuplicated(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	m.setStdout("ok")
	m.setExitCode(1)
	m.setStderr("permission denied")
	exec := newTestExecutor(t, m)
	defer exec.Close()

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "exec-stderr-dup",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo ok"},
		},
	})
	require.NoError(t, err)
	// "permission denied" should appear exactly once — in the
	// [stderr] line, not duplicated after [exit 1].
	assert.Equal(t, 1, strings.Count(res.Output, "permission denied"),
		"stderr text should appear exactly once in output")
	assert.Contains(t, res.Output, "[exit 1]")
	assert.NotContains(t, res.Output, "[exit 1] permission denied",
		"exit status line should not be followed by stderr text")
}

// TestNew_WithEnvVars_RejectsInvalidVarName verifies that
// NewWithContext validates sandbox-level env var names for contract
// consistency with envToken's validation of spec.Env.
func TestNew_WithEnvVars_RejectsInvalidVarName(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	_, err = New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithEnvVars(map[string]string{"BAD;KEY": "v"}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid environment variable name")
	assert.Contains(t, err.Error(), "WithEnvVars")
	// Sandbox must not be created when validation fails.
	assert.Equal(t, 0, m.createCalls,
		"invalid env var name must be rejected before CreateSandbox")
}

// TestNew_WithHeaders_DoesNotAliasCallerMap verifies that WithHeaders
// copies the caller's map so subsequent mutations to the original map
// do not affect the executor. Without the copy, mutating headers after
// construction would silently change which HTTP headers are sent, and
// concurrent mutation would be a data race.
func TestNew_WithHeaders_DoesNotAliasCallerMap(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	headers := map[string]string{"X-Test": "1"}
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithHeaders(headers),
	)
	require.NoError(t, err)
	defer exec.Close()

	// Mutate the original map after construction.
	headers["X-Test"] = "mutated"
	delete(headers, "X-Test")
	headers["X-Evil"] = "yes"

	// The executor's headers must be unaffected.
	assert.Equal(t, map[string]string{"X-Test": "1"}, exec.headers,
		"WithHeaders must copy the caller's map")
}

// TestNew_WithEndpointHostRewrite_DoesNotAliasCallerMap verifies that
// WithEndpointHostRewrite copies the caller's map so subsequent
// mutations to the original map do not affect the executor.
func TestNew_WithEndpointHostRewrite_DoesNotAliasCallerMap(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	u, err := url.Parse(m.server.URL)
	require.NoError(t, err)

	rewrites := map[string]string{"host.docker.internal": "localhost"}
	exec, err := New(
		WithDomain(u.Host),
		WithProtocol("http"),
		WithAPIKey("test-key"),
		WithEndpointHostRewrite(rewrites),
	)
	require.NoError(t, err)
	defer exec.Close()

	// Mutate the original map after construction.
	rewrites["host.docker.internal"] = "evil.example.com"
	rewrites["attacker.com"] = "localhost"

	// The executor's rewrites must be unaffected.
	assert.Equal(t, map[string]string{"host.docker.internal": "localhost"},
		exec.endpointHostRewrite,
		"WithEndpointHostRewrite must copy the caller's map")
}

// --- Rememorio 2026-07-17 follow-ups ---

// TestExecuteCode_PerSession_DerivesExecIDFromSession verifies that when
// ExecutionID is empty but ctx carries an invocation session, PerSession
// mode reuses a stable workspace key (app/user/session) across turns.
func TestExecuteCode_PerSession_DerivesExecIDFromSession(t *testing.T) {
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

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			AppName: "app-a",
			UserID:  "user-1",
			ID:      "sess-1",
		},
	})
	blocks := []codeexecutor.CodeBlock{{Language: "bash", Code: "echo ok"}}

	_, err = exec.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		ExecutionID: "",
		CodeBlocks:  blocks,
	})
	require.NoError(t, err)

	// Same session again must target the same workspace path (stable hash).
	// Capture mkdir paths from commands.
	m.mu.Lock()
	cmds1 := append([]string(nil), m.commands...)
	m.mu.Unlock()

	_, err = exec.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		ExecutionID: "",
		CodeBlocks:  blocks,
	})
	require.NoError(t, err)

	m.mu.Lock()
	cmds2 := append([]string(nil), m.commands...)
	m.mu.Unlock()

	wantKey := executionIDFromContext(ctx)
	require.Equal(t, "app-a/user-1/sess-1", wantKey)
	h := stableWorkspaceHash(wantKey)
	wsMarker := "ws_" + h

	found1, found2 := false, false
	for _, c := range cmds1 {
		if strings.Contains(c, wsMarker) {
			found1 = true
			break
		}
	}
	for _, c := range cmds2 {
		if strings.Contains(c, wsMarker) {
			found2 = true
			break
		}
	}
	assert.True(t, found1, "first turn should use session-derived workspace")
	assert.True(t, found2, "second turn should reuse same session workspace")
}

// TestExecuteCode_PerSession_CrossSessionIsolation verifies different
// sessions get different workspace paths.
func TestExecuteCode_PerSession_CrossSessionIsolation(t *testing.T) {
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

	blocks := []codeexecutor.CodeBlock{{Language: "bash", Code: "echo ok"}}
	ctxA := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{AppName: "app", UserID: "u", ID: "a"},
	})
	ctxB := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{AppName: "app", UserID: "u", ID: "b"},
	})

	_, err = exec.ExecuteCode(ctxA, codeexecutor.CodeExecutionInput{CodeBlocks: blocks})
	require.NoError(t, err)
	_, err = exec.ExecuteCode(ctxB, codeexecutor.CodeExecutionInput{CodeBlocks: blocks})
	require.NoError(t, err)

	ha := "ws_" + stableWorkspaceHash(executionIDFromContext(ctxA))
	hb := "ws_" + stableWorkspaceHash(executionIDFromContext(ctxB))
	require.NotEqual(t, ha, hb)

	m.mu.Lock()
	cmds := append([]string(nil), m.commands...)
	m.mu.Unlock()
	sawA, sawB := false, false
	for _, c := range cmds {
		if strings.Contains(c, ha) {
			sawA = true
		}
		if strings.Contains(c, hb) {
			sawB = true
		}
	}
	assert.True(t, sawA && sawB, "each session must have its own workspace path")
}

// TestEngine_DoesNotAdvertiseSupportsCleanEnv locks the execd reality:
// outer shell inherits os.Environ, so CleanEnv is not a security boundary.
func TestEngine_DoesNotAdvertiseSupportsCleanEnv(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()
	assert.False(t, exec.Engine().Describe().SupportsCleanEnv)
}
