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
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestNewHTTPWebFetchTools_RequiresAllowlist(t *testing.T) {
	t.Parallel()

	_, err := newHTTPWebFetchTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires allowed_domains")
}

func TestNewHTTPWebFetchTools_AllowAllSucceeds(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
allow_all_domains: true
timeout: 200ms
main_content_only: true
max_content_length: 123
max_total_content_length: 456
`)
	tools, err := newHTTPWebFetchTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.NotEmpty(t, tools[0].Declaration().Name)
	require.Contains(
		t,
		tools[0].Declaration().Description,
		"Search-result pages are blocked",
	)
	require.Contains(
		t,
		tools[0].Declaration().Description,
		"challenge pages are reported as blocked",
	)
}

func TestNewHTTPWebFetchTools_SearchPageAndBlockedPageOptOut(
	t *testing.T,
) {
	t.Parallel()

	cfg := yamlNode(t, `
allow_all_domains: true
allow_search_result_pages: true
detect_blocked_pages: false
`)
	tools, err := newHTTPWebFetchTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.NotContains(
		t,
		tools[0].Declaration().Description,
		"Search-result pages are blocked",
	)
	require.NotContains(
		t,
		tools[0].Declaration().Description,
		"challenge pages are reported as blocked",
	)
}

func TestNewDuckDuckGoTools_Succeeds(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GAIA benchmark", r.URL.Query().Get("q"))
			require.Equal(t, "ua", r.Header.Get("User-Agent"))
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fgaia">GAIA benchmark</a>
  <a class="result__snippet">HTML backend result.</a>
</body></html>`))
		},
	))
	defer server.Close()

	cfg := yamlNode(t, strings.Join([]string{
		`base_url: "` + server.URL + `"`,
		`backend: "html"`,
		`user_agent: "ua"`,
		`timeout: 100ms`,
		"",
	}, "\n"))
	tools, err := newDuckDuckGoTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.NotEmpty(t, tools[0].Declaration().Name)
	require.Contains(t, tools[0].Declaration().Description, "html search")

	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)
	raw, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"GAIA benchmark"}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), `"summary":"Found 1 html results`)
	require.Contains(t, string(data), `"url":"https://example.com/gaia"`)
}

func TestNewDuckDuckGoTools_BlockedResultURLPatterns(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a" href="https://x.io/t">Trace mirror</a>
  <a class="result__snippet">Benchmark trace mirror.</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fsource">Source page</a>
  <a class="result__snippet">Primary source.</a>
</body></html>`))
		},
	))
	defer server.Close()

	cfg := yamlNode(t, strings.Join([]string{
		`base_url: "` + server.URL + `"`,
		`backend: "html"`,
		`blocked_result_url_patterns:`,
		`  - "x.io/t"`,
		"",
	}, "\n"))
	tools, err := newDuckDuckGoTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)

	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)
	raw, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"example benchmark"}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), "filtered 1 result")
	require.Contains(t, string(data), "https://example.com/source")
	require.NotContains(t, string(data), "x.io/t")
}

func TestNewDuckDuckGoTools_InvalidBackend(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, "backend: unknown\n")
	_, err := newDuckDuckGoTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "backend must be api, html, or lite")
}

func TestNewImageInspectTools_RequiresFileScope(t *testing.T) {
	t.Parallel()

	_, err := newImageInspectTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires allowed_dirs")
}

func TestNewImageInspectTools_InspectImage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.White)
		}
	}
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())

	cfg := yamlNode(t, strings.Join([]string{
		`allowed_dirs:`,
		`  - "` + dir + `"`,
		`timeout: "100ms"`,
		"",
	}, "\n"))
	tools, err := newImageInspectTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "image_inspect", tools[0].Declaration().Name)

	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)
	raw, err := callable.Call(
		context.Background(),
		[]byte(`{"path":`+strconv.Quote(path)+`,"ocr":false}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), `"format":"png"`)
	require.Contains(t, string(data), `"width":12`)
	require.Contains(t, string(data), `"height":8`)
}

func TestNewBrowserTools_Succeeds(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
default_profile: "openclaw"
evaluate_enabled: false
profiles:
  - name: "openclaw"
    transport: "stdio"
    command: "npx"
    args: ["--yes", "@playwright/mcp@latest"]
    timeout: "5m"
`)
	tools, err := newBrowserTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "browser", tools[0].Declaration().Name)
}

func TestNewBrowserTools_ServerBackedProfileSucceeds(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:9223"
profiles:
  - name: "openclaw"
`)
	tools, err := newBrowserTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "browser", tools[0].Declaration().Name)
}

func TestNewBrowserTools_ErrorPaths(t *testing.T) {
	t.Parallel()

	_, err := newBrowserTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{
			Config: yamlNode(t, "unknown_field: true\n"),
		},
	)
	require.Error(t, err)

	_, err = newBrowserTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{
			Config: yamlNode(t, `
profiles:
  - name: "openclaw"
    transport: "bad"
`),
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported transport")
}

func TestValidateMCPConnection_StdioRequiresCommand(t *testing.T) {
	t.Parallel()

	err := validateMCPConnection(mcpConn(mcpTransportStdio, "", "", nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires command")
}

func TestValidateMCPConnection_SSERequiresURL(t *testing.T) {
	t.Parallel()

	err := validateMCPConnection(mcpConn(mcpTransportSSE, "", "", nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires server_url")
}

func TestValidateMCPConnection_UnsupportedTransport(t *testing.T) {
	t.Parallel()

	err := validateMCPConnection(mcpConn("bad", "", "", nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mcp transport")
}

func TestBuildMCPToolFilter_Include(t *testing.T) {
	t.Parallel()

	filter, err := buildMCPToolFilter(&mcpFilterConfig{
		Mode:  "include",
		Names: []string{"a"},
	})
	require.NoError(t, err)
	require.NotNil(t, filter)

	tools := []tool.Tool{
		stubTool{name: "a"},
		stubTool{name: "b"},
	}
	filtered := tool.FilterTools(context.Background(), tools, filter)
	require.Len(t, filtered, 1)
	require.Equal(t, "a", filtered[0].Declaration().Name)
}

func TestBuildMCPToolFilter_Exclude(t *testing.T) {
	t.Parallel()

	filter, err := buildMCPToolFilter(&mcpFilterConfig{
		Mode:  "exclude",
		Names: []string{"a"},
	})
	require.NoError(t, err)
	require.NotNil(t, filter)

	tools := []tool.Tool{
		stubTool{name: "a"},
		stubTool{name: "b"},
	}
	filtered := tool.FilterTools(context.Background(), tools, filter)
	require.Len(t, filtered, 1)
	require.Equal(t, "b", filtered[0].Declaration().Name)
}

func TestBuildMCPToolFilter_NilConfig(t *testing.T) {
	t.Parallel()

	filter, err := buildMCPToolFilter(nil)
	require.NoError(t, err)
	require.Nil(t, filter)
}

func TestBuildMCPToolFilter_DefaultsToInclude(t *testing.T) {
	t.Parallel()

	filter, err := buildMCPToolFilter(&mcpFilterConfig{
		Mode:  "",
		Names: []string{" a ", " ", "b"},
	})
	require.NoError(t, err)
	require.NotNil(t, filter)

	tools := []tool.Tool{
		stubTool{name: "a"},
		stubTool{name: "x"},
	}
	filtered := tool.FilterTools(context.Background(), tools, filter)
	require.Len(t, filtered, 1)
	require.Equal(t, "a", filtered[0].Declaration().Name)
}

func TestBuildMCPToolFilter_UnsupportedModeFails(t *testing.T) {
	t.Parallel()

	_, err := buildMCPToolFilter(&mcpFilterConfig{
		Mode:  "nope",
		Names: []string{"a"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported mcp tool_filter.mode")
}

func TestOpenAPILoader_RequiresExactlyOneSource(t *testing.T) {
	t.Parallel()

	_, err := openAPILoader(openAPISpecConfig{}, false)
	require.Error(t, err)

	_, err = openAPILoader(openAPISpecConfig{
		File: "a.yaml",
		URL:  "https://example.invalid",
	}, false)
	require.Error(t, err)
}

func TestNewFileToolSet_DefaultReadOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"base_dir: "+dir+"\n",
	), &node))

	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "fs", Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)

	names := toolNames(ts.Tools(context.Background()))
	require.Contains(t, names, "read_file")
	require.NotContains(t, names, "save_file")
	require.NotContains(t, names, "replace_content")
}

func TestNewFileToolSet_ReadWriteDefaultsWhenNotReadOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cfg := yamlNode(t, "base_dir: "+dir+"\nread_only: false\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)

	names := toolNames(ts.Tools(context.Background()))
	require.Contains(t, names, "save_file")
	require.Contains(t, names, "replace_content")
}

func TestNewFileToolSet_EnableSaveOverridesDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cfg := yamlNode(
		t,
		"base_dir: "+dir+"\nread_only: false\nenable_save: false\n",
	)
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)

	names := toolNames(ts.Tools(context.Background()))
	require.NotContains(t, names, "save_file")
}

func TestNewFileToolSet_EnableSaveWorks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"base_dir: "+dir+"\n"+"enable_save: true\n",
	), &node))

	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "fs", Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)

	names := toolNames(ts.Tools(context.Background()))
	require.Contains(t, names, "save_file")
}

func TestNewFileToolSet_EnableReadCanDisable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cfg := yamlNode(t, "base_dir: "+dir+"\nenable_read: false\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)

	names := toolNames(ts.Tools(context.Background()))
	require.NotContains(t, names, "read_file")
}

func TestNewFileToolSet_RuntimeReadDirsDefault(t *testing.T) {
	dir := t.TempDir()
	tmpFile := filepath.Join(t.TempDir(), "derived.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("derived"), 0o644))

	cfg := yamlNode(t, "base_dir: "+dir+"\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{StateDir: t.TempDir()},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	readFile := findCallableTool(t, ts.Tools(context.Background()), "read_file")

	raw, err := readFile.Call(
		context.Background(),
		[]byte(`{"file_name":`+strconv.Quote(tmpFile)+`}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), `"contents":"derived"`)
}

func TestNewFileToolSet_RuntimeReadDirsRelativeStateDir(t *testing.T) {
	cwd := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldwd))
	})

	baseDir := filepath.Join(cwd, "base")
	require.NoError(t, os.MkdirAll(baseDir, 0o755))
	scratchFile := filepath.Join(
		cwd,
		"state",
		"workspaces",
		"scratch",
		"out",
		"derived.txt",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(scratchFile), 0o755))
	require.NoError(t, os.WriteFile(scratchFile, []byte("derived"), 0o644))

	cfg := yamlNode(t, "base_dir: "+baseDir+"\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{StateDir: "state"},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	readFile := findCallableTool(t, ts.Tools(context.Background()), "read_file")

	raw, err := readFile.Call(
		context.Background(),
		[]byte(`{"file_name":`+strconv.Quote(scratchFile)+`}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), `"contents":"derived"`)
}

func TestNewFileToolSet_RuntimeReadDirsAllowBrowserArtifacts(
	t *testing.T,
) {
	oldWorkdir, err := os.Getwd()
	require.NoError(t, err)
	workdir, err := os.MkdirTemp(oldWorkdir, ".test-browser-artifacts-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(workdir))
	})
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWorkdir))
	})

	artifactDir := filepath.Join(workdir, browserArtifactDirName)
	require.NoDirExists(t, artifactDir)

	cfg := yamlNode(t, "base_dir: "+t.TempDir()+"\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{StateDir: t.TempDir()},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	require.DirExists(t, artifactDir)
	readFile := findCallableTool(t, ts.Tools(context.Background()), "read_file")

	artifactFile := filepath.Join(artifactDir, "page.yml")
	require.NoError(t, os.WriteFile(
		artifactFile,
		[]byte("title: Example\n"),
		0o644,
	))
	raw, err := readFile.Call(
		context.Background(),
		[]byte(`{"file_name":`+strconv.Quote(artifactFile)+`}`),
	)
	require.NoError(t, err)
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.Contains(t, string(data), `"contents":"title: Example`)
}

func TestNewFileToolSet_RuntimeReadDirsRejectSymlinkedBrowserArtifacts(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows")
	}

	oldWorkdir, err := os.Getwd()
	require.NoError(t, err)
	workdir, err := os.MkdirTemp(oldWorkdir, ".test-browser-artifacts-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(workdir))
	})
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWorkdir))
	})

	outsideDir := filepath.Join(workdir, "outside")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))
	artifactDir := filepath.Join(workdir, browserArtifactDirName)
	require.NoError(t, os.Symlink(outsideDir, artifactDir))

	cfg := yamlNode(t, "base_dir: "+t.TempDir()+"\n")
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{StateDir: t.TempDir()},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	readFile := findCallableTool(t, ts.Tools(context.Background()), "read_file")

	artifactFile := filepath.Join(outsideDir, "page.yml")
	require.NoError(t, os.WriteFile(
		artifactFile,
		[]byte("title: Symlink\n"),
		0o644,
	))
	_, err = readFile.Call(
		context.Background(),
		[]byte(`{"file_name":`+strconv.Quote(artifactFile)+`}`),
	)
	require.Error(t, err)
	require.Contains(
		t,
		err.Error(),
		"outside base_directory and configured read-only roots",
	)
}

func TestBrowserArtifactReadRootWithErrorPaths(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name    string
		lstat   browserArtifactLstatFunc
		mkdir   browserArtifactMkdirAllFunc
		wantDir string
		wantOK  bool
	}{
		{
			name: "lstat error",
			lstat: func(string) (os.FileInfo, error) {
				return nil, errBoom
			},
			mkdir: func(string, os.FileMode) error {
				t.Fatal("mkdir should not be called")
				return nil
			},
		},
		{
			name: "mkdir error",
			lstat: func(string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			mkdir: func(string, os.FileMode) error {
				return errBoom
			},
		},
		{
			name: "post-create lstat error",
			lstat: lstatSequence(
				nil,
				os.ErrNotExist,
				nil,
				errBoom,
			),
			mkdir: func(string, os.FileMode) error {
				return nil
			},
		},
		{
			name: "post-create symlink",
			lstat: lstatSequence(
				nil,
				os.ErrNotExist,
				browserArtifactFileInfo{
					mode: os.ModeSymlink,
				},
				nil,
			),
			mkdir: func(string, os.FileMode) error {
				return nil
			},
		},
		{
			name: "post-create safe directory",
			lstat: lstatSequence(
				nil,
				os.ErrNotExist,
				browserArtifactFileInfo{dir: true},
				nil,
			),
			mkdir: func(string, os.FileMode) error {
				return nil
			},
			wantDir: filepath.Join("cwd", browserArtifactDirName),
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotDir, gotOK := browserArtifactReadRootWith(
				"cwd",
				tt.lstat,
				tt.mkdir,
			)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantDir, gotDir)
		})
	}
}

func TestNewFileToolSet_RuntimeReadDirsCanDisable(t *testing.T) {
	dir := t.TempDir()
	tmpFile := filepath.Join(t.TempDir(), "derived.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("derived"), 0o644))

	cfg := yamlNode(
		t,
		"base_dir: "+dir+"\nruntime_read_dirs: false\n",
	)
	ts, err := newFileToolSet(
		registry.ToolSetProviderDeps{StateDir: t.TempDir()},
		registry.PluginSpec{Name: "fs", Config: cfg},
	)
	require.NoError(t, err)
	readFile := findCallableTool(t, ts.Tools(context.Background()), "read_file")

	_, err = readFile.Call(
		context.Background(),
		[]byte(`{"file_name":`+strconv.Quote(tmpFile)+`}`),
	)
	require.Error(t, err)
	require.Contains(
		t,
		err.Error(),
		"outside base_directory and configured read-only roots",
	)
}

func TestDefaultFileReadOnlyDirsIncludesPlatformTmp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardcoded /tmp root is Unix-only")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	roots := defaultFileReadOnlyDirs("")
	require.Contains(t, roots, tmp)
	require.Contains(t, roots, "/tmp")
}

func TestDefaultFileReadOnlyDirsAbsolutizesRelativeStateDir(t *testing.T) {
	stateRoot := filepath.Join(".", "state")
	wantScratch, err := filepath.Abs(
		filepath.Join(stateRoot, "workspaces", "scratch"),
	)
	require.NoError(t, err)

	roots := defaultFileReadOnlyDirs(stateRoot)
	require.Contains(t, roots, wantScratch)
}

func lstatSequence(
	firstInfo os.FileInfo,
	firstErr error,
	secondInfo os.FileInfo,
	secondErr error,
) browserArtifactLstatFunc {
	var calls int
	return func(string) (os.FileInfo, error) {
		calls++
		if calls == 1 {
			return firstInfo, firstErr
		}
		return secondInfo, secondErr
	}
}

type browserArtifactFileInfo struct {
	mode os.FileMode
	dir  bool
}

func (i browserArtifactFileInfo) Name() string {
	return browserArtifactDirName
}

func (i browserArtifactFileInfo) Size() int64 {
	return 0
}

func (i browserArtifactFileInfo) Mode() os.FileMode {
	return i.mode
}

func (i browserArtifactFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (i browserArtifactFileInfo) IsDir() bool {
	return i.dir
}

func (i browserArtifactFileInfo) Sys() any {
	return nil
}

func TestAbsPathOrOriginalFallbacks(t *testing.T) {
	require.Equal(t, "  ", absPathOrOriginal("  "))

	oldwd, err := os.Getwd()
	require.NoError(t, err)
	tmp := t.TempDir()
	deletedWD := filepath.Join(tmp, "deleted")
	require.NoError(t, os.MkdirAll(deletedWD, 0o755))
	require.NoError(t, os.Chdir(deletedWD))
	require.NoError(t, os.RemoveAll(deletedWD))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldwd))
	})

	require.Equal(t, "relative-state", absPathOrOriginal("relative-state"))
}

func TestOverrideToolSetName_NoOpWhenEmpty(t *testing.T) {
	t.Parallel()

	base := &fakeToolSet{name: "base"}
	out := overrideToolSetName(base, "")
	require.Equal(t, base, out)
}

func TestOverrideToolSetName_NilToolSet(t *testing.T) {
	t.Parallel()

	require.Nil(t, overrideToolSetName(nil, "x"))
}

func TestOverrideToolSetName_ChangesName(t *testing.T) {
	t.Parallel()

	base := &fakeToolSet{
		name:  "base",
		tools: []tool.Tool{stubTool{name: "a"}},
	}
	out := overrideToolSetName(base, "new")
	require.NotNil(t, out)
	require.Equal(t, "new", out.Name())
	require.Len(t, out.Tools(context.Background()), 1)
}

func TestNewMCPToolSet_UsesSpecName(t *testing.T) {
	t.Parallel()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
transport: stdio
command: echo
args: ["hello"]
`), &node))

	ts, err := newMCPToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "demo", Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "demo", ts.Name())
}

func TestNewMCPToolSet_ReconnectDefaults(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
transport: stdio
command: echo
args: ["hello"]
reconnect:
  enabled: true
  max_attempts: 0
`)
	ts, err := newMCPToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "demo", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
}

func TestNewOpenAPIToolSet_MissingSpecFails(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `allow_external_refs: false`)
	_, err := newOpenAPIToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "api", Config: cfg},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires config.spec")
}

func TestNewOpenAPIToolSet_InlineSpecWorks(t *testing.T) {
	t.Parallel()

	const specBody = `
openapi: 3.0.0
info:
  title: Demo API
  version: "1.0"
paths:
  /hello:
    get:
      operationId: getHello
      responses:
        "200":
          description: OK
`

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
spec:
  inline: |
`+indentYAML(specBody, 4)+`
`), &node))

	ts, err := newOpenAPIToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "api", Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "api", ts.Name())

	tools := ts.Tools(context.Background())
	require.NotEmpty(t, tools)
}

func TestNewOpenAPIToolSet_FileSpecWorks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.yaml")
	require.NoError(t, os.WriteFile(
		specPath,
		[]byte(`openapi: 3.0.0
info:
  title: Demo API
  version: "1.0"
paths:
  /hello:
    get:
      operationId: getHello
      responses:
        "200":
          description: OK`),
		0o600,
	))

	cfg := yamlNode(
		t,
		"spec:\n  file: \""+specPath+"\"\n",
	)
	ts, err := newOpenAPIToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "api", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func TestNewOpenAPIToolSet_URLSpecWorks(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, `openapi: 3.0.0
info:
  title: Demo API
  version: "1.0"
paths:
  /hello:
    get:
      operationId: getHello
      responses:
        "200":
          description: OK`)
	}))
	t.Cleanup(srv.Close)

	cfg := yamlNode(
		t,
		"spec:\n  url: \""+srv.URL+"\"\n"+"timeout: 1s\n",
	)
	ts, err := newOpenAPIToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "api", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func TestNewGoogleToolSet_EnvFallbackAndNameOverride(t *testing.T) {
	t.Setenv(envGoogleAPIKey, "k")
	t.Setenv(envGoogleEngineID, "e")

	cfg := yamlNode(t, `
api_key: ""
engine_id: ""
base_url: "https://example.invalid"
size: 3
offset: 2
lang: "en"
timeout: 200ms
`)
	ts, err := newGoogleToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "g", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "g", ts.Name())
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func TestNewWikipediaToolSet_NameOverride(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
language: "en"
max_results: 2
timeout: 100ms
`)
	ts, err := newWikipediaToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "wiki", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "wiki", ts.Name())
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func TestNewArxivToolSet_NameOverride(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
base_url: "https://example.invalid"
page_size: 3
delay_seconds: 100ms
num_retries: 2
`)
	ts, err := newArxivToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "ax", Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "ax", ts.Name())
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func TestNewEmailToolSet_NameOverride(t *testing.T) {
	t.Parallel()

	ts, err := newEmailToolSet(
		registry.ToolSetProviderDeps{},
		registry.PluginSpec{Name: "mail"},
	)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, "mail", ts.Name())
	require.NotEmpty(t, ts.Tools(context.Background()))
}

func mcpConn(
	transport, urlStr, command string,
	args []string,
) mcp.ConnectionConfig {
	return mcp.ConnectionConfig{
		Transport: transport,
		ServerURL: urlStr,
		Command:   command,
		Args:      args,
	}
}

type fakeToolSet struct {
	name  string
	tools []tool.Tool
}

func (f *fakeToolSet) Tools(ctx context.Context) []tool.Tool {
	return f.tools
}

func (f *fakeToolSet) Close() error { return nil }

func (f *fakeToolSet) Name() string { return f.name }

func toolNames(tools []tool.Tool) map[string]struct{} {
	out := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		out[t.Declaration().Name] = struct{}{}
	}
	return out
}

func findCallableTool(
	t *testing.T,
	tools []tool.Tool,
	name string,
) tool.CallableTool {
	t.Helper()
	for _, tl := range tools {
		if tl.Declaration().Name != name {
			continue
		}
		callable, ok := tl.(tool.CallableTool)
		require.True(t, ok)
		return callable
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func indentYAML(body string, spaces int) string {
	pad := make([]byte, spaces)
	for i := range pad {
		pad[i] = ' '
	}
	prefix := string(pad)

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
