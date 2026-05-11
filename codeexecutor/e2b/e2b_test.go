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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
)

func TestOptions(t *testing.T) {
	c := &CodeExecutor{}

	WithAPIKey("key-1")(c)
	WithAccessToken("tok")(c)
	WithDomain("example.com")(c)
	WithDebug(true)(c)
	WithTemplate("tmpl-x")(c)
	WithSandboxTimeout(2 * time.Minute)(c)
	WithRequestTimeout(15 * time.Second)(c)
	WithExecutionTimeout(42 * time.Second)(c)
	WithEnvVars(map[string]string{"A": "1"})(c)
	WithMetadata(map[string]string{"m": "v"})(c)
	hc := &http.Client{}
	WithHTTPClient(hc)(c)
	WithHeaders(map[string]string{"X-Test": "1"})(c)
	WithSandboxID("sbx-123")(c)
	WithLanguage(ci.LanguageJavaScript)(c)
	WithSandboxRunBase("/tmp/sandbox-run")(c)

	assert.Equal(t, "key-1", c.apiKey)
	assert.Equal(t, "tok", c.accessToken)
	assert.Equal(t, "example.com", c.domain)
	assert.True(t, c.debug)
	assert.Equal(t, "tmpl-x", c.template)
	assert.Equal(t, 2*time.Minute, c.sandboxTimeout)
	assert.Equal(t, 15*time.Second, c.requestTimeout)
	assert.Equal(t, 42*time.Second, c.executionTimeout)
	assert.Equal(t, map[string]string{"A": "1"}, c.envVars)
	assert.Equal(t, map[string]string{"m": "v"}, c.metadata)
	assert.Same(t, hc, c.httpClient)
	assert.Equal(t, map[string]string{"X-Test": "1"}, c.headers)
	assert.Equal(t, "sbx-123", c.sandboxID)
	assert.Equal(t, ci.LanguageJavaScript, c.defaultLanguage)
	assert.Equal(t, "/tmp/sandbox-run", c.sandboxRunBase)
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

func TestExecuteCodeSandboxNotInitialized(t *testing.T) {
	c := &CodeExecutor{}
	_, err := c.ExecuteCode(context.Background(),
		codeexecutor.CodeExecutionInput{ExecutionID: "x"})
	assert.Error(t, err)
}

func TestPickLanguage(t *testing.T) {
	cases := []struct {
		in   string
		def  ci.RunCodeLanguage
		want ci.RunCodeLanguage
	}{
		{"", ci.LanguagePython, ci.LanguagePython},
		{"auto", ci.LanguageBash, ci.LanguageBash},
		{"python", "", ci.LanguagePython},
		{"py", "", ci.LanguagePython},
		{"python3", "", ci.LanguagePython},
		{"javascript", "", ci.LanguageJavaScript},
		{"JS", "", ci.LanguageJavaScript},
		{"node", "", ci.LanguageJavaScript},
		{"nodejs", "", ci.LanguageJavaScript},
		{"typescript", "", ci.LanguageTypeScript},
		{"ts", "", ci.LanguageTypeScript},
		{"bash", "", ci.LanguageBash},
		{"sh", "", ci.LanguageBash},
		{"shell", "", ci.LanguageBash},
		{"r", "", ci.LanguageR},
		{"java", "", ci.LanguageJava},
		{"rust", "", ci.RunCodeLanguage("rust")},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := pickLanguage(tc.in, tc.def)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAppendStderr(t *testing.T) {
	var b strings.Builder
	appendStderr(&b, "")
	assert.Empty(t, b.String())

	b.Reset()
	appendStderr(&b, "hello\n")
	assert.Equal(t, "[stderr] hello\n", b.String())

	b.Reset()
	appendStderr(&b, "line1\nline2\n")
	assert.Equal(t, "[stderr] line1\n[stderr] line2\n", b.String())

	b.Reset()
	appendStderr(&b, "no-newline")
	assert.Equal(t, "[stderr] no-newline", b.String())
}

func TestExtractFromResult(t *testing.T) {
	// text + html + markdown go into text output.
	r := &ci.Result{
		Text:     "hello",
		HTML:     "<p>hi</p>",
		Markdown: "# m",
	}
	idx := 0
	files, text := extractFromResult(r, 0, &idx)
	assert.Empty(t, files)
	assert.Contains(t, text, "hello")
	assert.Contains(t, text, "<p>hi</p>")
	assert.Contains(t, text, "# m")

	// PNG encoded as base64 becomes a binary file.
	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	b64 := base64.StdEncoding.EncodeToString(raw)
	r2 := &ci.Result{PNG: b64}
	idx2 := 0
	files2, text2 := extractFromResult(r2, 3, &idx2)
	assert.Empty(t, text2)
	require.Len(t, files2, 1)
	assert.Equal(t, "result_3_0.png", files2[0].Name)
	assert.Equal(t, "image/png", files2[0].MIMEType)
	assert.Equal(t, string(raw), files2[0].Content)
	assert.Equal(t, int64(len(raw)), files2[0].SizeBytes)

	// SVG is returned as-is (not base64-decoded).
	r3 := &ci.Result{SVG: "<svg></svg>"}
	idx3 := 0
	files3, _ := extractFromResult(r3, 1, &idx3)
	require.Len(t, files3, 1)
	assert.Equal(t, "result_1_0.svg", files3[0].Name)
	assert.Equal(t, "image/svg+xml", files3[0].MIMEType)
	assert.Equal(t, "<svg></svg>", files3[0].Content)

	// nil result is safe.
	idx4 := 0
	f4, t4 := extractFromResult(nil, 0, &idx4)
	assert.Nil(t, f4)
	assert.Empty(t, t4)
}

func TestEnsureRuntime(t *testing.T) {
	c := &CodeExecutor{}
	rt := c.ensureRuntime()
	require.NotNil(t, rt)
	// Second call returns the same runtime instance.
	assert.Same(t, rt, c.ensureRuntime())
	// Default sandbox run base is used when not overridden.
	assert.Equal(t, defaultSandboxRunBase, rt.cfg.runBase)

	c2 := &CodeExecutor{sandboxRunBase: "/custom/run"}
	rt2 := c2.ensureRuntime()
	assert.Equal(t, "/custom/run", rt2.cfg.runBase)
}

// TestWorkspaceMethodsRequireSandbox verifies that workspace methods return
// errors when the sandbox is not initialized.
func TestWorkspaceMethodsRequireSandbox(t *testing.T) {
	ce := &CodeExecutor{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: "/tmp/ws"}

	_, err := ce.CreateWorkspace(ctx, "x", codeexecutor.WorkspacePolicy{})
	assert.Error(t, err, "CreateWorkspace should fail without sandbox")

	err = ce.Cleanup(ctx, ws)
	assert.Error(t, err)

	err = ce.PutFiles(ctx, ws,
		[]codeexecutor.PutFile{{Path: "a", Content: []byte("x")}})
	assert.Error(t, err)
}

func TestCloseWithoutSandbox(t *testing.T) {
	c := &CodeExecutor{}
	assert.NoError(t, c.Close())
}

// TestNewMissingAPIKey verifies New surfaces authentication errors when no
// API key is available. The test ensures E2B_API_KEY is unset for the run.
func TestNewMissingAPIKey(t *testing.T) {
	oldKey := os.Getenv("E2B_API_KEY")
	os.Unsetenv("E2B_API_KEY")
	defer os.Setenv("E2B_API_KEY", oldKey)

	_, err := NewWithContext(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox")
}

func TestSanitize(t *testing.T) {
	assert.Equal(t, "abc_123", sanitize("abc 123"))
	assert.Equal(t, "ab-c_D_e", sanitize("ab-c/D\\e"))
	assert.Equal(t, "______", sanitize("!@#$%^"))
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, "''", shellQuote(""))
	assert.Equal(t, "'abc'", shellQuote("abc"))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
	assert.Equal(t, "'a b c'", shellQuote("a b c"))
}

func TestInputBase(t *testing.T) {
	assert.Equal(t, "file.txt",
		inputBase("host:///tmp/data/file.txt"))
	assert.Equal(t, "model",
		inputBase("workspace://deep/path/model"))
	assert.Equal(t, "standalone", inputBase("standalone"))
}

func TestExtractBetween(t *testing.T) {
	s := "prefix\nBEGIN\nhello world\nEND\nsuffix\n"
	got := extractBetween(s, "BEGIN", "END")
	assert.Equal(t, "hello world", got)

	assert.Equal(t, "", extractBetween("nothing", "BEGIN", "END"))

	s2 := "BEGIN\nonly-begin"
	assert.Equal(t, "only-begin", extractBetween(s2, "BEGIN", "END"))
}

func TestParseFramedOutput(t *testing.T) {
	rawStdout := strings.Join([]string{
		"__E2B_STDOUT_BEGIN__",
		"hello",
		"__E2B_STDOUT_END__",
		"__E2B_EXITCODE__=7",
		"",
	}, "\n")
	rawStderr := strings.Join([]string{
		"__E2B_STDERR_BEGIN__",
		"bad",
		"__E2B_STDERR_END__",
		"",
	}, "\n")
	stdout, stderr, exit := parseFramedOutput(rawStdout, rawStderr)
	assert.Equal(t, "hello", stdout)
	assert.Equal(t, "bad", stderr)
	assert.Equal(t, 7, exit)

	stdout, stderr, exit = parseFramedOutput("", "")
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, exit)
}

func TestBuildRunWrapperContainsSentinels(t *testing.T) {
	script := buildRunWrapper("echo hi")
	for _, sub := range []string{
		sentinelStdoutBegin,
		sentinelStdoutEnd,
		sentinelExitPrefix,
		sentinelStderrBegin,
		sentinelStderrEnd,
		"echo hi",
	} {
		assert.Contains(t, script, sub)
	}
}

func TestTarGzFromFilesRoundTrip(t *testing.T) {
	files := []codeexecutor.PutFile{
		{Path: "dir/a.txt", Content: []byte("A"), Mode: 0o644},
		{Path: "dir/sub/b.bin", Content: []byte{0x00, 0x01, 0x02}, Mode: 0o600},
		{Path: "c.txt", Content: []byte("c")},
	}
	data, err := tarGzFromFiles(files)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	names, contents := decodeTarGz(t, data)
	assert.Contains(t, names, "dir/a.txt")
	assert.Contains(t, names, "dir/sub/b.bin")
	assert.Contains(t, names, "c.txt")
	assert.Equal(t, "A", contents["dir/a.txt"])
	assert.Equal(t, string([]byte{0x00, 0x01, 0x02}),
		contents["dir/sub/b.bin"])
	assert.Equal(t, "c", contents["c.txt"])
}

func TestTarGzFromDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t,
		os.WriteFile(dir+"/hello.txt", []byte("hello"), 0o644))
	require.NoError(t, os.MkdirAll(dir+"/sub", 0o755))
	require.NoError(t,
		os.WriteFile(dir+"/sub/x.bin", []byte{1, 2, 3}, 0o644))

	data, err := tarGzFromDir(dir)
	require.NoError(t, err)
	names, contents := decodeTarGz(t, data)
	assert.Contains(t, names, "hello.txt")
	assert.Contains(t, names, "sub/x.bin")
	assert.Equal(t, "hello", contents["hello.txt"])
	assert.Equal(t, string([]byte{1, 2, 3}), contents["sub/x.bin"])
}

func TestIsTimeoutErr(t *testing.T) {
	assert.False(t, isTimeoutErr(nil))
	assert.True(t, isTimeoutErr(assertErr("request Timeout")))
	assert.True(t, isTimeoutErr(assertErr("execution timeout")))
	assert.False(t, isTimeoutErr(assertErr("other error")))
}

type strError string

func (e strError) Error() string { return string(e) }

func assertErr(s string) error { return strError(s) }

func decodeTarGz(t *testing.T, data []byte) (
	[]string, map[string]string,
) {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gr.Close()

	var names []string
	content := map[string]string{}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		names = append(names, hdr.Name)
		buf, err := io.ReadAll(tr)
		require.NoError(t, err)
		content[hdr.Name] = string(buf)
	}
	return names, content
}
