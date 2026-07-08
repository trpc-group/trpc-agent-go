//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// fakeToolSet is a tool.ToolSet whose Tools() result and list-call count are
// controllable, so tests can exercise lazy listing, caching, and retry.
type fakeToolSet struct {
	name     string
	tools    []tool.Tool
	calls    int32 // number of Tools() invocations
	emptyFor int32 // return no tools for the first N calls (simulates a down server)
}

func (f *fakeToolSet) Tools(context.Context) []tool.Tool {
	n := atomic.AddInt32(&f.calls, 1)
	if n <= f.emptyFor {
		return nil
	}
	return f.tools
}

func (f *fakeToolSet) Close() error { return nil }
func (f *fakeToolSet) Name() string { return f.name }

func TestMCPToolbox_ListAndRename(t *testing.T) {
	ts := &fakeToolSet{
		name: "weather",
		tools: []tool.Tool{
			newTestTool("get_forecast", "get the weather forecast"),
			newTestTool("get_alerts", "get severe weather alerts"),
		},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", Description: "weather data", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// No listing happens at construction.
	assert.Equal(t, int32(0), atomic.LoadInt32(&ts.calls))

	// A search lists the server and renames its tools.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.ElementsMatch(t,
		[]string{"mcp__weather__get_forecast", "mcp__weather__get_alerts"},
		toolNames(res.Tools),
	)
	assert.Equal(t, int32(1), atomic.LoadInt32(&ts.calls))

	// Tools are listed on every request — no caching.
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.Greater(t, atomic.LoadInt32(&ts.calls), int32(1), "tools must be re-listed each call")
}

func TestMCPToolbox_SearchByQueryAndLoad(t *testing.T) {
	ts := &fakeToolSet{
		name: "billing",
		tools: []tool.Tool{
			newTestTool("create_invoice", "create a billing invoice"),
			newTestTool("refund_payment", "refund a payment"),
		},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", Description: "invoices", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "billing", Queries: []string{"invoice"}})
	assert.Equal(t, []string{"mcp__billing__create_invoice"}, toolNames(res.Tools))

	// The renamed tool is callable: beforeTool must let it pass once loaded, and
	// its schema must be injected on the next model request.
	bt, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__billing__create_invoice"})
	require.NoError(t, err)
	assert.Nil(t, bt, "loaded MCP tool must pass through")

	req := &model.Request{}
	_, err = p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, ok := req.Tools["mcp__billing__create_invoice"]
	assert.True(t, ok, "loaded MCP tool schema must be injected")
}

func TestMCPToolbox_BlocksUnloadedTool(t *testing.T) {
	ts := &fakeToolSet{
		name:  "billing",
		tools: []tool.Tool{newTestTool("create_invoice", "x")},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__billing__create_invoice"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.CustomResult.(string), "not loaded yet")
}

func TestMCPToolbox_RetriesAfterEmptyListing(t *testing.T) {
	ts := &fakeToolSet{
		name:     "weather",
		tools:    []tool.Tool{newTestTool("get_forecast", "forecast")},
		emptyFor: 1, // first listing fails (server down), second succeeds
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// First search sees a down server: no tools, namespace not yet registered.
	raw, err := p.searchTools(ctx, toolSearchInput{Namespace: "weather"})
	require.NoError(t, err)
	assert.NotContains(t, raw, "get_forecast")

	// Second search retries and now lists the tool.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.Equal(t, []string{"mcp__weather__get_forecast"}, toolNames(res.Tools))
}

func TestMCPToolbox_RenamedToolCallRoutesToOriginal(t *testing.T) {
	var gotArgs string
	original := function.NewFunctionTool(
		func(ctx context.Context, in struct {
			V string `json:"v"`
		}) (string, error) {
			gotArgs = in.V
			return "ok", nil
		},
		function.WithName("echo"),
		function.WithDescription("echo input"),
	)
	renamed := newRenamedTool(original, "tools")
	assert.Equal(t, "mcp__tools__echo", renamed.Declaration().Name)

	callable, ok := renamed.(tool.CallableTool)
	require.True(t, ok, "renamed callable tool must implement CallableTool")
	out, err := callable.Call(context.Background(), []byte(`{"v":"hello"}`))
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, "hello", gotArgs)
}

func TestMCPToolbox_CatalogRendersWithToolNames(t *testing.T) {
	ts := &fakeToolSet{
		name:  "weather",
		tools: []tool.Tool{newTestTool("get_forecast", "forecast")},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", Description: "weather data", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Catalog must include the namespace, its description, and its tools
	// since MCP servers are listed before rendering.
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
	assert.Contains(t, req.Messages[0].Content, "weather")
	assert.Contains(t, req.Messages[0].Content, "weather data")
	assert.Contains(t, req.Messages[0].Content, "mcp__weather__get_forecast")
	assert.Greater(t, atomic.LoadInt32(&ts.calls), int32(0), "catalog rendering must list the server")
}

func TestMCPToolbox_LoadedToolSchemaInjectedAfterRestart(t *testing.T) {
	// Simulate a fresh process: the loaded set is restored from session state but
	// the server has not been listed in this Plugin instance yet.
	ts := &fakeToolSet{
		name:  "billing",
		tools: []tool.Tool{newTestTool("create_invoice", "create an invoice")},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, inv := ctxWithInvocation()
	p.saveDiscoveredTools(ctx, inv, []string{"mcp__billing__create_invoice"})

	req := &model.Request{}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, ok := req.Tools["mcp__billing__create_invoice"]
	assert.True(t, ok, "loaded MCP tool schema must be injected after a restart-style reload")
}

func TestMCPToolbox_ConcurrentSearchesAreRaceFree(t *testing.T) {
	ts := &fakeToolSet{
		name: "weather",
		tools: []tool.Tool{
			newTestTool("get_forecast", "forecast"),
			newTestTool("get_alerts", "alerts"),
		},
	}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", Description: "weather data", ToolSet: ts},
	}))

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, _ := ctxWithInvocation()
			_, err := p.searchTools(ctx, toolSearchInput{Namespace: "weather"})
			assert.NoError(t, err)
			req := &model.Request{
				Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
			}
			_, err = p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	// Each goroutine lists the server in searchTools and beforeModel — verify
	// no races occur (the test is run with -race in CI) and the server is
	// listed at least once.
	assert.Greater(t, atomic.LoadInt32(&ts.calls), int32(0))
}

func TestParseMCPName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantServer string
		wantTool   string
		wantOK     bool
	}{
		{"standard MCP name", "mcp__server__tool", "server", "tool", true},
		{"server with underscores", "mcp__my_server__my_tool", "my_server", "my_tool", true},
		{"tool with underscores", "mcp__server__my_tool_v2", "server", "my_tool_v2", true},
		{"no mcp prefix", "server__tool", "", "", false},
		{"only mcp prefix", "mcp__", "", "", false},
		{"mcp with no tool", "mcp__server__", "server", "", true},
		{"mcp with single underscore", "mcp_server_tool", "", "", false},
		{"empty string", "", "", "", false},
		{"mcp with empty server", "mcp____tool", "", "", false},
		{"just mcp prefix no separator", "mcp__servertool", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, ok := parseMCPName(tt.input)
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantTool, tool)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestIsMCP(t *testing.T) {
	// nil box
	var nilBox *toolboxIndex
	assert.False(t, nilBox.isMCP())

	// box without mcp source
	nonMCP := &toolboxIndex{name: "test"}
	assert.False(t, nonMCP.isMCP())

	// box with mcp source
	mcpBox := &toolboxIndex{name: "test", mcp: &mcpSource{serverName: "srv"}}
	assert.True(t, mcpBox.isMCP())
}

func TestRegisterMCPToolbox_EdgeCases(t *testing.T) {
	// Empty server name should be skipped.
	p1 := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "", Description: "desc", ToolSet: &fakeToolSet{name: "empty"}},
	}))
	assert.Len(t, p1.toolboxes, 0, "empty server name MCP toolbox should be skipped")

	// Nil ToolSet should be skipped.
	p2 := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "valid", Description: "desc", ToolSet: nil},
	}))
	assert.Len(t, p2.toolboxes, 0, "nil ToolSet MCP toolbox should be skipped")

	// Collision should be skipped (two MCP toolboxes with same ServerName).
	p3 := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "collision", ToolSet: &fakeToolSet{name: "c1", tools: []tool.Tool{newTestTool("t1", "d1")}}},
		{ServerName: "collision", ToolSet: &fakeToolSet{name: "c2", tools: []tool.Tool{newTestTool("t2", "d2")}}},
	}))
	assert.Len(t, p3.toolboxes, 1, "colliding MCP namespace should keep only the first")
	assert.Equal(t, "collision", p3.toolboxes[0].name)

	// MCP toolbox collision with static toolbox.
	p4 := NewPlugin(nil,
		WithToolboxes([]Toolbox{{Name: "shared", Tools: []tool.Tool{newTestTool("t1", "d1")}}}),
		WithMCPToolboxes([]MCPToolbox{{ServerName: "shared", ToolSet: &fakeToolSet{name: "mcp"}}}),
	)
	assert.Len(t, p4.toolboxes, 1, "MCP namespace colliding with static toolbox should be skipped")
}

func TestRenamedTool_Original(t *testing.T) {
	orig := newTestTool("echo", "echo input")
	renamed := newRenamedTool(orig, "tools")
	assert.Equal(t, orig, renamed.(*renamedTool).Original())
}

func TestRenamedTool_SkipSummarization(t *testing.T) {
	// Default: not implemented → false.
	orig := newTestTool("echo", "echo input")
	renamed := newRenamedTool(orig, "tools")
	assert.False(t, renamed.(*renamedTool).SkipSummarization())
}

func TestRenamedTool_ToolMetadata(t *testing.T) {
	orig := newTestTool("echo", "echo input")
	renamed := newRenamedTool(orig, "tools")
	meta := renamed.(*renamedTool).ToolMetadata()
	assert.NotNil(t, meta)
}

func TestRenamedTool_NonCallable(t *testing.T) {
	// Create a tool that is NOT CallableTool.
	nonCallable := &fakeNonCallableTool{name: "nc", desc: "not callable"}
	renamed := newRenamedTool(nonCallable, "server")
	callable, ok := renamed.(tool.CallableTool)
	require.True(t, ok)
	_, err := callable.Call(context.Background(), []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not callable")
}

// fakeNonCallableTool is a tool.Tool that does not implement CallableTool.
type fakeNonCallableTool struct {
	name, desc string
}

func (f *fakeNonCallableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name, Description: f.desc}
}

func TestStreamableRenamedTool_StreamableCall(t *testing.T) {
	// A simple streamable tool.
	streamable := &fakeStreamableTool{
		name: "stream",
		desc: "test streamable",
	}
	renamed := newRenamedTool(streamable, "server")
	st, ok := renamed.(tool.StreamableTool)
	require.True(t, ok, "renamed streamable tool should implement StreamableTool")
	_, err := st.StreamableCall(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	// fakeStreamableTool returns nil reader; just verify no error and no panic.
}

// fakeStreamableTool implements tool.Tool, tool.CallableTool, and tool.StreamableTool.
type fakeStreamableTool struct {
	name, desc string
}

func (f *fakeStreamableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name, Description: f.desc}
}

func (f *fakeStreamableTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return "ok", nil
}

func (f *fakeStreamableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	return nil, nil
}

func (f *fakeStreamableTool) StreamInner() bool { return true }

func TestNewRenamedTool_StreamInnerFalse(t *testing.T) {
	// A tool that implements StreamableTool but StreamInner returns false.
	noStream := &fakeNoStreamTool{name: "nostream", desc: "no stream inner"}
	renamed := newRenamedTool(noStream, "server")
	_, isStreamable := renamed.(tool.StreamableTool)
	assert.False(t, isStreamable, "when StreamInner returns false, should not be streamableRenamedTool")
	_, isCallable := renamed.(tool.CallableTool)
	assert.True(t, isCallable)
}

type fakeNoStreamTool struct {
	name, desc string
}

func (f *fakeNoStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name, Description: f.desc}
}

func (f *fakeNoStreamTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return "ok", nil
}

func (f *fakeNoStreamTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	return nil, nil
}

func (f *fakeNoStreamTool) StreamInner() bool { return false }

func TestMaterializeNamespace_BlankAndNonMCP(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "static", Tools: []tool.Tool{newTestTool("t1", "d1")}},
	}))
	ctx, _ := ctxWithInvocation()

	// Blank namespace → no-op.
	p.materializeNamespace(ctx, "")
	// Non-MCP namespace → no-op.
	p.materializeNamespace(ctx, "static")
	// Unknown namespace → no-op.
	p.materializeNamespace(ctx, "unknown")
}

func TestMaterializeByToolNames_Mixed(t *testing.T) {
	ts := &fakeToolSet{name: "billing", tools: []tool.Tool{newTestTool("create_invoice", "desc")}}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// Only MCP-prefixed names trigger listing; non-MCP names are skipped.
	p.materializeByToolNames(ctx, []string{"mcp__billing__create_invoice", "plain_tool", "", "mcp__billing__create_invoice"})
	assert.Greater(t, atomic.LoadInt32(&ts.calls), int32(0))
}

func TestMaterializeAllMCP_MixedBoxes(t *testing.T) {
	ts1 := &fakeToolSet{name: "weather", tools: []tool.Tool{newTestTool("forecast", "desc")}}
	ts2 := &fakeToolSet{name: "billing", tools: []tool.Tool{newTestTool("invoice", "desc")}}
	p := NewPlugin(nil,
		WithToolboxes([]Toolbox{{Name: "static", Tools: []tool.Tool{newTestTool("t1", "d1")}}}),
		WithMCPToolboxes([]MCPToolbox{
			{ServerName: "weather", ToolSet: ts1},
			{ServerName: "billing", ToolSet: ts2},
		}),
	)
	ctx, _ := ctxWithInvocation()
	p.materializeAllMCP(ctx)
	// Both MCP servers should be listed.
	assert.Greater(t, atomic.LoadInt32(&ts1.calls), int32(0))
	assert.Greater(t, atomic.LoadInt32(&ts2.calls), int32(0))
}

func TestParseToolName_MCPAndCamelCase(t *testing.T) {
	cases := map[string][]string{
		"":                       {},
		"simple":                 {"simple"},
		"CamelCase":              {"camel", "case"},
		"snake_case":             {"snake", "case"},
		"mcp__server__action":    {"mcp", "server", "action"},
		"mcp__my_server__do_sth": {"mcp", "my", "server", "do", "sth"},
	}
	for name, want := range cases {
		assert.Equal(t, want, parseToolName(name).Parts, name)
	}
}
