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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
}

func TestNewDuckDuckGoTools_Succeeds(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, `
base_url: "https://example.invalid"
user_agent: "ua"
timeout: 100ms
`)
	tools, err := newDuckDuckGoTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.NotEmpty(t, tools[0].Declaration().Name)
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
