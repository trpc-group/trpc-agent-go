//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
)

// executeResponder is a hook for customizing the /execute NDJSON response.
// The supplied code is the raw bash/python snippet the SDK was asked to run.
type executeResponder func(code string) string

type mockE2BServer struct {
	t       *testing.T
	server  *httptest.Server
	respond executeResponder

	mu          sync.Mutex
	createCalls int
	execCalls   int
	killCalls   int
	lastCode    string
}

func newMockE2BServer(t *testing.T, respond executeResponder) *mockE2BServer {
	t.Helper()
	m := &mockE2BServer{t: t, respond: respond}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockE2BServer) close() { m.server.Close() }

func (m *mockE2BServer) client() *http.Client {
	u, err := url.Parse(m.server.URL)
	require.NoError(m.t, err)
	return &http.Client{
		Transport: &redirectTransport{host: u.Host, scheme: u.Scheme},
	}
}

func (m *mockE2BServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "POST" && r.URL.Path == "/sandboxes":
		m.mu.Lock()
		m.createCalls++
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxID":"sbx-mock","clientID":"c-mock","templateID":"code-interpreter-v1","envdPort":49999}`))
		return
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
		m.mu.Lock()
		m.killCalls++
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxID":"sbx-mock","clientID":"c-mock","templateID":"code-interpreter-v1","state":"running"}`))
		return
	case r.URL.Path == "/execute":
		m.mu.Lock()
		m.execCalls++
		m.mu.Unlock()
		// Extract `code` from body.
		var body struct {
			Code string `json:"code"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		m.mu.Lock()
		m.lastCode = body.Code
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if m.respond != nil {
			_, _ = w.Write([]byte(m.respond(body.Code)))
		}
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

type redirectTransport struct {
	host   string
	scheme string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.scheme
	req.URL.Host = t.host
	req.Host = t.host
	return http.DefaultTransport.RoundTrip(req)
}

func ndjsonLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func stdoutMsg(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "stdout", "text": text, "timestamp": 1,
	})
	return string(b)
}

func stderrMsg(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "stderr", "text": text, "timestamp": 2,
	})
	return string(b)
}

func errorMsg(name, value, traceback string) string {
	b, _ := json.Marshal(map[string]any{
		"type":      "error",
		"name":      name,
		"value":     value,
		"traceback": traceback,
	})
	return string(b)
}

func newMockedExecutor(
	t *testing.T, srv *mockE2BServer, opts ...Option,
) *CodeExecutor {
	t.Helper()
	t.Setenv("E2B_API_KEY", "test-key")
	allOpts := append([]Option{
		WithAPIKey("test-key"),
		WithDomain("e2b.test"),
		WithDebug(true),
		WithHTTPClient(srv.client()),
		WithRequestTimeout(5 * time.Second),
	}, opts...)
	c, err := NewWithContext(context.Background(), allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestNewWithContext_Success(t *testing.T) {
	srv := newMockE2BServer(t, func(string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	assert.Equal(t, "sbx-mock", c.SandboxID())
	assert.NotNil(t, c.Sandbox())
	assert.True(t, c.owned)
	assert.Equal(t, 1, srv.createCalls)
}

func TestNewWithContext_ConnectExisting(t *testing.T) {
	srv := newMockE2BServer(t, nil)
	defer srv.close()

	t.Setenv("E2B_API_KEY", "test-key")
	c, err := NewWithContext(context.Background(),
		WithAPIKey("test-key"),
		WithDomain("e2b.test"),
		WithDebug(true),
		WithHTTPClient(srv.client()),
		WithSandboxID("existing-id"),
	)
	require.NoError(t, err)
	defer c.Close()

	assert.Equal(t, "sbx-mock", c.SandboxID())
	assert.False(t, c.owned, "should not own an externally-provided sandbox")
}

func TestNew_EnvKey(t *testing.T) {
	srv := newMockE2BServer(t, nil)
	defer srv.close()

	t.Setenv("E2B_API_KEY", "env-key")
	c, err := New(
		WithDomain("e2b.test"),
		WithDebug(true),
		WithHTTPClient(srv.client()),
	)
	require.NoError(t, err)
	defer c.Close()
	assert.Equal(t, "sbx-mock", c.SandboxID())
}

func TestCloseOwnedSandbox(t *testing.T) {
	srv := newMockE2BServer(t, nil)
	defer srv.close()
	c := newMockedExecutor(t, srv)

	require.NoError(t, c.Close())
	assert.Equal(t, 1, srv.killCalls)
	require.NoError(t, c.Close())
}

func TestExecuteCode_StdoutStderrAndResult(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		return ndjsonLines(
			stdoutMsg("hello\n"),
			stderrMsg("warn\n"),
			`{"type":"result","text":"42","html":"<p>42</p>","is_main_result":true}`,
		)
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "x",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print('hi')"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "hello")
	assert.Contains(t, res.Output, "[stderr] warn")
	assert.Contains(t, res.Output, "42")
	assert.Contains(t, res.Output, "<p>42</p>")
}

func TestExecuteCode_ErrorEventAppended(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		return ndjsonLines(
			stdoutMsg("pre\n"),
			errorMsg("NameError", "x is not defined", "tb line 1"),
		)
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "python", Code: "x"}},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "[error] NameError: x is not defined")
	assert.Contains(t, res.Output, "tb line 1")
}

func TestExecuteCode_MultipleBlocks(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		return ndjsonLines(stdoutMsg("ok\n"))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(1)"},
			{Language: "bash", Code: "echo 2"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, srv.execCalls)
	assert.Equal(t, "ok\nok\n", res.Output)
}

func TestCreateAndCleanupWorkspace(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		// CreateWorkspace invokes a mkdir script; respond with nothing.
		return ""
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)
	ctx := context.Background()

	ws, err := c.CreateWorkspace(ctx, "exec/abc 1", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	assert.Equal(t, "exec/abc 1", ws.ID)
	assert.Contains(t, ws.Path, defaultSandboxRunBase+"/ws_exec_abc_1_")

	prev := srv.execCalls
	require.NoError(t, c.Cleanup(ctx, codeexecutor.Workspace{Path: ""}))
	assert.Equal(t, prev, srv.execCalls)

	require.NoError(t, c.Cleanup(ctx, ws))
}

func TestPutFilesAndPutDirectory(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)
	ctx := context.Background()

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	prev := srv.execCalls
	require.NoError(t, c.PutFiles(ctx, ws, nil))
	assert.Equal(t, prev, srv.execCalls)

	err := c.PutFiles(ctx, ws, []codeexecutor.PutFile{
		{Path: "a/b.txt", Content: []byte("hi"), Mode: 0o644},
	})
	require.NoError(t, err)
	assert.Greater(t, srv.execCalls, prev)

	err = c.PutDirectory(ctx, ws, "", "to")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostPath")

	err = c.PutDirectory(ctx, ws, "/does/not/exist/"+t.Name(), "")
	require.Error(t, err)

	dir := t.TempDir()
	require.NoError(t, writeTempFile(dir+"/a.txt", "content"))
	require.NoError(t, c.PutDirectory(ctx, ws, dir, "sub"))
}

func TestStageDirectory_ReadOnly(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	dir := t.TempDir()
	require.NoError(t, writeTempFile(dir+"/f.txt", "x"))

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	require.NoError(t, c.StageDirectory(context.Background(), ws, dir, "",
		codeexecutor.StageOptions{}))
	require.NoError(t, c.StageDirectory(context.Background(), ws, dir, "d",
		codeexecutor.StageOptions{ReadOnly: true}))
}

func TestRunProgram_FramedOutput(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		stdout := strings.Join([]string{
			sentinelStdoutBegin,
			"hello",
			sentinelStdoutEnd,
			sentinelExitPrefix + "0",
		}, "\n") + "\n"
		stderr := strings.Join([]string{
			sentinelStderrBegin,
			sentinelStderrEnd,
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(stdout), stderrMsg(stderr))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	res, err := c.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "echo",
		Args:    []string{"hi"},
		Env:     map[string]string{"FOO": "bar"},
		Cwd:     "work",
		Stdin:   "input data",
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello", res.Stdout)
	assert.Equal(t, "", res.Stderr)
	assert.Equal(t, 0, res.ExitCode)
	assert.False(t, res.TimedOut)
}

func TestRunProgram_BashErrorSurfaced(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		// Emit an error event — runBashStreaming should translate this
		// into an error return value.
		return ndjsonLines(errorMsg("ShellError", "boom", ""))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	_, err := c.RunProgram(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.RunProgramSpec{Cmd: "false"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bash error")
}

func TestCollect_ReadsFiles(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n" +
					"/tmp/ws/out/a.txt\n/tmp/ws/out/b.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=5",
			"__E2B_B64_BEGIN__",
			"aGVsbG8=", // "hello"
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	files, err := c.Collect(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		[]string{"out/*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "out/a.txt", files[0].Name)
	assert.Equal(t, "hello", files[0].Content)
	assert.Equal(t, int64(5), files[0].SizeBytes)

	// Empty pattern list should short-circuit.
	out, err := c.Collect(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestCollect_FiltersPathsOutsideWorkspace(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n" +
					"/tmp/ws/out/safe.txt\n" +
					"/etc/passwd\n" +
					"/tmp/wsmalicious/x.txt\n" +
					"/tmp/ws/../escaped.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=4",
			"__E2B_B64_BEGIN__",
			"c2FmZQ==", // "safe"
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	files, err := c.Collect(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		[]string{"**/*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1, "only the path under ws.Path should survive")
	assert.Equal(t, "out/safe.txt", files[0].Name)
}

func TestCollectOutputs_FiltersPathsOutsideWorkspace(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n" +
					"/etc/passwd\n" +
					"/tmp/ws/out/ok.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=2",
			"__E2B_B64_BEGIN__",
			"b2s=", // "ok"
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	mf, err := c.CollectOutputs(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.OutputSpec{Globs: []string{"**/*.txt"}, Inline: true})
	require.NoError(t, err)
	require.Len(t, mf.Files, 1)
	assert.Equal(t, "out/ok.txt", mf.Files[0].Name)
}

func TestCollect_UsesResolvedBaseWhenWsPathIsSymlink(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/var/real/ws\n" +
					"/var/real/ws/out/a.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=1",
			"__E2B_B64_BEGIN__",
			"YQ==", // "a"
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	files, err := c.Collect(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}, // different link
		[]string{"out/*.txt"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.NotEmpty(t, files[0].Content)
}

func TestPathUnder(t *testing.T) {
	cases := []struct {
		name, path, base string
		want             bool
	}{
		{"exact match", "/ws", "/ws", true},
		{"nested", "/ws/a/b", "/ws", true},
		{"trailing slash on base", "/ws/a", "/ws/", true},
		{"sibling prefix", "/wsmalicious/x", "/ws", false},
		{"parent dir", "/", "/ws", false},
		{"empty base", "/ws/a", "", false},
		{"empty path", "", "/ws", false},
		{"different root", "/etc/passwd", "/ws", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pathUnder(tc.path, tc.base))
		})
	}
}

func TestCollectOutputs_InlinePersistsBytes(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n/tmp/ws/out/f.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=2",
			"__E2B_B64_BEGIN__",
			"aGk=", // "hi"
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	mf, err := c.CollectOutputs(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.OutputSpec{Globs: []string{"out/*"}, Inline: true})
	require.NoError(t, err)
	require.Len(t, mf.Files, 1)
	assert.Equal(t, "out/f.txt", mf.Files[0].Name)
	assert.Equal(t, "hi", mf.Files[0].Content)
}

func TestStageInputs_UnsupportedScheme(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	err := c.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "ftp://invalid"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported input")
}

func TestStageInputs_WorkspaceScheme(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	err := c.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "workspace://data/x.txt", Mode: "link"}})
	require.NoError(t, err)
}

func TestStageInputs_SandboxNotInitialized(t *testing.T) {
	c := &CodeExecutor{}
	err := c.StageInputs(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		[]codeexecutor.InputSpec{{From: "workspace://x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not initialized")
}

func TestExecuteInline_Python(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		if strings.Contains(code, sentinelStdoutBegin) {
			stdout := strings.Join([]string{
				sentinelStdoutBegin,
				"inline-ok",
				sentinelStdoutEnd,
				sentinelExitPrefix + "0",
			}, "\n") + "\n"
			return ndjsonLines(stdoutMsg(stdout))
		}
		return ""
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.ExecuteInline(context.Background(), "e1",
		[]codeexecutor.CodeBlock{{Language: "python", Code: "print('x')"}},
		2*time.Second)
	require.NoError(t, err)
	assert.Contains(t, res.Stdout, "inline-ok")
}

func TestExecuteInline_UnsupportedBlockLang(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.ExecuteInline(context.Background(), "e1",
		[]codeexecutor.CodeBlock{{Language: "ruby", Code: "puts 1"}},
		time.Second)
	require.NoError(t, err)
	assert.Contains(t, res.Stderr, "unsupported language")
}

func TestEngineExposure(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	eng := c.Engine()
	require.NotNil(t, eng)
	assert.NotNil(t, eng.Manager())
	assert.NotNil(t, eng.FS())
	assert.NotNil(t, eng.Runner())
}

func TestPickLanguageTrimsAndLowers(t *testing.T) {
	assert.Equal(t, ci.LanguagePython, pickLanguage("  Python  ", ""))
	assert.Equal(t, ci.LanguageBash, pickLanguage("Shell", ""))
	assert.Equal(t, ci.RunCodeLanguage("Rust"), pickLanguage("Rust", ""))
}

func TestAppendStderr_MultipleLinesNoTrailingNL(t *testing.T) {
	var b strings.Builder
	appendStderr(&b, "a\nb")
	assert.Equal(t, "[stderr] a\n[stderr] b", b.String())
}

func TestExtractFromResult_Multiple(t *testing.T) {
	r := &ci.Result{
		Text:  "main",
		PNG:   "not-base64!!!",
		JPEG:  base64Encode("jpeg-data"),
		PDF:   base64Encode("pdf-data"),
		LaTeX: "\\latex",
	}
	idx := 10
	files, text := extractFromResult(r, 7, &idx)
	assert.Equal(t, "main", text)
	require.Len(t, files, 4)
	names := map[string]codeexecutor.File{}
	for _, f := range files {
		names[f.Name] = f
	}
	assert.Contains(t, names, "result_7_10.png")
	assert.Contains(t, names, "result_7_11.jpeg")
	assert.Contains(t, names, "result_7_12.pdf")
	assert.Contains(t, names, "result_7_13.tex")
	assert.Equal(t, "not-base64!!!", names["result_7_10.png"].Content)
	assert.Equal(t, "\\latex", names["result_7_13.tex"].Content)
	assert.Equal(t, 14, idx)
}

func TestInputBase_ArtifactScheme(t *testing.T) {
	assert.Equal(t, "thing", inputBase("artifact://thing@2"))
	assert.Equal(t, "c", inputBase("artifact://a/b/c"))
	assert.Equal(t, "x", inputBase("artifact:///x"))
}

func TestExtractBetween_Edge(t *testing.T) {
	assert.Equal(t, "", extractBetween("BEGINEND", "BEGIN", "END"))
	assert.Equal(t, "hello", extractBetween("BEGIN\nhelloEND", "BEGIN", "END"))
}

func TestParseFramedOutput_NegativeExit(t *testing.T) {
	stdout := strings.Join([]string{
		sentinelStdoutBegin,
		"x",
		sentinelStdoutEnd,
		sentinelExitPrefix + "not-a-number",
	}, "\n") + "\n"
	_, _, exit := parseFramedOutput(stdout, "")
	assert.Equal(t, 0, exit)
}

func TestTarGzFromFiles_InvalidPath(t *testing.T) {
	_, err := tarGzFromFiles([]codeexecutor.PutFile{{Path: "", Content: []byte("x")}})
	require.Error(t, err)

	_, err = tarGzFromFiles([]codeexecutor.PutFile{{Path: "/", Content: []byte("x")}})
	require.Error(t, err)
}

func TestTarGzFromDir_MissingRoot(t *testing.T) {
	_, err := tarGzFromDir("/definitely/not/there/" + t.Name())
	require.Error(t, err)
}

func TestIsTimeoutErr_MixedCase(t *testing.T) {
	assert.True(t, isTimeoutErr(fmt.Errorf("Request TIMEOUT occurred")))
	assert.True(t, isTimeoutErr(fmt.Errorf("context deadline: timeout")))
	assert.False(t, isTimeoutErr(fmt.Errorf("misc")))
}

func TestPinnedArtifactVersion(t *testing.T) {
	v1, v2 := 1, 2
	md := codeexecutor.WorkspaceMetadata{
		Inputs: []codeexecutor.InputRecord{
			{From: "artifact://name@1", To: "work/inputs/name",
				Resolved: "name", Version: &v1},
			{From: "artifact://name", To: "work/inputs/name",
				Resolved: "name", Version: &v2},
		},
	}
	got := pinnedArtifactVersion(md, "name", "work/inputs/name")
	require.NotNil(t, got)
	assert.Equal(t, 2, *got, "should pick the most recent matching record")

	assert.Nil(t, pinnedArtifactVersion(codeexecutor.WorkspaceMetadata{},
		"name", "work/inputs/name"))

	assert.Nil(t, pinnedArtifactVersion(md, "", "x"))
	assert.Nil(t, pinnedArtifactVersion(md, "name", ""))

	assert.Nil(t, pinnedArtifactVersion(md, "name", "other"))

	v3 := 3
	md2 := codeexecutor.WorkspaceMetadata{
		Inputs: []codeexecutor.InputRecord{
			{From: "artifact://other@1", To: "t", Version: &v3},
		},
	}
	assert.Nil(t, pinnedArtifactVersion(md2, "name", "t"))
}

func TestSanitize_Unicode(t *testing.T) {
	assert.Equal(t, "_abc_",
		sanitize("中abc中"),
		"non-ASCII is replaced by underscore")
	assert.Equal(t, "", sanitize(""))
}

func TestShellQuote_SpecialChars(t *testing.T) {
	assert.Equal(t, "'a$b'", shellQuote("a$b"))
	assert.Equal(t, "'a\"b'", shellQuote(`a"b`))
	assert.Equal(t, `'a'\''b'\''c'`, shellQuote("a'b'c"))
}

func TestWorkspaceMethodsSandboxNotInitialized(t *testing.T) {
	c := &CodeExecutor{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}

	_, err := c.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "echo"})
	require.Error(t, err)

	_, err = c.Collect(ctx, ws, []string{"**"})
	require.Error(t, err)

	_, err = c.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{Globs: []string{"**"}})
	require.Error(t, err)

	err = c.StageDirectory(ctx, ws, "/doesnotexist-"+t.Name(), "", codeexecutor.StageOptions{})
	require.Error(t, err)

	_, err = c.ExecuteInline(ctx, "e1",
		[]codeexecutor.CodeBlock{{Language: "python", Code: "x"}}, time.Second)
	require.Error(t, err)
}

func TestStageInputs_SkillScheme(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	err := c.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "skill://pkg/data.txt", Mode: "copy"}})
	require.NoError(t, err)
}

func TestStageInputs_HostScheme(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string { return "" })
	defer srv.close()
	c := newMockedExecutor(t, srv)

	dir := t.TempDir()
	require.NoError(t, writeTempFile(dir+"/a.txt", "abc"))

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	err := c.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "host://" + dir}})
	require.NoError(t, err)
}

func TestLoadWorkspaceMetadata_ParsesJSON(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if strings.Contains(code, "metadata.json") {
			// readFile for metadata.json
			body := strings.Join([]string{
				"__E2B_SIZE__=50",
				"__E2B_B64_BEGIN__",
				base64.StdEncoding.EncodeToString([]byte(
					`{"version":2,"skills":{"sk1":{"name":"sk1"}}}`)),
				"__E2B_B64_END__",
			}, "\n") + "\n"
			return ndjsonLines(stdoutMsg(body))
		}
		return ""
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}
	err := c.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{From: "workspace://data.txt"}})
	require.NoError(t, err)
}

func TestCollectOutputs_LimitsAndNameTemplate(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n/tmp/ws/out/big.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=100",
			"__E2B_B64_BEGIN__",
			base64.StdEncoding.EncodeToString([]byte("0123456789")),
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	mf, err := c.CollectOutputs(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.OutputSpec{
			Globs:         []string{"out/*"},
			MaxFileBytes:  10,
			MaxTotalBytes: 20,
			Inline:        true,
		})
	require.NoError(t, err)
	require.Len(t, mf.Files, 1)
	assert.True(t, mf.Files[0].Truncated)
	assert.True(t, mf.LimitsHit)
}

func TestCollectOutputs_MaxFilesLimit(t *testing.T) {
	calls := 0
	srv := newMockE2BServer(t, func(code string) string {
		calls++
		if calls == 1 {
			return ndjsonLines(stdoutMsg(
				"__E2B_BASE__=/tmp/ws\n" +
					"/tmp/ws/out/a.txt\n/tmp/ws/out/b.txt\n"))
		}
		body := strings.Join([]string{
			"__E2B_SIZE__=1",
			"__E2B_B64_BEGIN__",
			base64.StdEncoding.EncodeToString([]byte("x")),
			"__E2B_B64_END__",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(body))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	mf, err := c.CollectOutputs(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.OutputSpec{
			Globs:    []string{"out/*"},
			MaxFiles: 1,
			Inline:   true,
		})
	require.NoError(t, err)
	assert.Len(t, mf.Files, 1)
	assert.True(t, mf.LimitsHit)
}

func TestRunProgram_DefaultTimeout(t *testing.T) {
	srv := newMockE2BServer(t, func(code string) string {
		stdout := strings.Join([]string{
			sentinelStdoutBegin,
			"done",
			sentinelStdoutEnd,
			sentinelExitPrefix + "0",
		}, "\n") + "\n"
		return ndjsonLines(stdoutMsg(stdout))
	})
	defer srv.close()
	c := newMockedExecutor(t, srv)

	res, err := c.RunProgram(context.Background(),
		codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"},
		codeexecutor.RunProgramSpec{Cmd: "true"})
	require.NoError(t, err)
	assert.Equal(t, "done", res.Stdout)
}

func TestExecuteCode_SandboxExecutionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"sandboxID":"sbx","clientID":"c","templateID":"t","envdPort":49999}`))
		case r.URL.Path == "/execute":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	t.Setenv("E2B_API_KEY", "k")
	c, err := NewWithContext(context.Background(),
		WithAPIKey("k"), WithDomain("e2b.test"), WithDebug(true),
		WithHTTPClient(&http.Client{
			Transport: &redirectTransport{host: u.Host, scheme: u.Scheme},
		}))
	require.NoError(t, err)
	defer c.Close()

	_, err = c.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "python", Code: "x"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execute block 0")
}

func writeTempFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
