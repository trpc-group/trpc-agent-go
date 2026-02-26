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

func TestOverrideToolSetName_NoOpWhenEmpty(t *testing.T) {
	t.Parallel()

	base := &fakeToolSet{name: "base"}
	out := overrideToolSetName(base, "")
	require.Equal(t, base, out)
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
