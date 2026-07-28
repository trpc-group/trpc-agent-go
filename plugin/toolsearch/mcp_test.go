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
	"time"

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
	mu       sync.Mutex
	tools    []tool.Tool
	calls    int32 // number of Tools() invocations
	emptyFor int32 // return no tools for the first N calls (simulates a down server)
}

func (f *fakeToolSet) Tools(context.Context) []tool.Tool {
	n := atomic.AddInt32(&f.calls, 1)
	if n <= f.emptyFor {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy so a concurrent setTools cannot mutate the slice a caller iterates.
	out := make([]tool.Tool, len(f.tools))
	copy(out, f.tools)
	return out
}

// setTools atomically replaces the tools returned by subsequent Tools() calls,
// simulating a live MCP server changing its exposed tool set between listings.
func (f *fakeToolSet) setTools(tools []tool.Tool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = tools
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__billing__create_invoice"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.CustomResult.(string), "not loaded yet")
}

// TestMCPToolbox_LaterListingSurfacesNewTools verifies the next non-empty
// listing is reflected immediately.
func TestMCPToolbox_LaterListingSurfacesNewTools(t *testing.T) {
	ts := &fakeToolSet{
		name:     "weather",
		tools:    []tool.Tool{newTestTool("get_forecast", "forecast")},
		emptyFor: 1, // first listing sees no tools, second lists get_forecast
	}
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// First search: server exposes no tools yet.
	raw, err := p.searchTools(ctx, toolSearchInput{Namespace: "weather"})
	require.NoError(t, err)
	assert.NotContains(t, raw, "get_forecast")

	// Second search: the new tool is picked up.
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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

// TestMCPToolbox_RefreshesChangedAndRemovedTools verifies each listing is
// treated as the fresh namespace snapshot: schema/description updates on an
// existing tool replace the previously indexed wrapper, and a tool dropped by
// the server between two listings is pruned from the catalog, invisible to
// tool_search, and no longer callable via beforeTool.
func TestMCPToolbox_RefreshesChangedAndRemovedTools(t *testing.T) {
	ts := &fakeToolSet{
		name: "weather",
		tools: []tool.Tool{
			newTestTool("get_forecast", "get the weather forecast"),
			newTestTool("get_alerts", "get severe weather alerts"),
		},
	}
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", Description: "weather data", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// First listing indexes the initial snapshot.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.ElementsMatch(t,
		[]string{"mcp__weather__get_forecast", "mcp__weather__get_alerts"},
		toolNames(res.Tools),
	)

	// Server swaps get_forecast's description (schema/impl update) and drops
	// get_alerts entirely. The next listing must reflect both.
	ts.setTools([]tool.Tool{
		newTestTool("get_forecast", "return the updated weather forecast"),
	})

	res = callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.Equal(t, []string{"mcp__weather__get_forecast"}, toolNames(res.Tools),
		"pruned tool must disappear from search results")

	// The refreshed wrapper carries the new description.
	got := p.toolsByName["mcp__weather__get_forecast"]
	require.NotNil(t, got)
	assert.Equal(t, "return the updated weather forecast", got.Declaration().Description)

	// The pruned tool is gone from every index.
	_, stillIndexed := p.toolsByName["mcp__weather__get_alerts"]
	assert.False(t, stillIndexed, "pruned tool must be removed from toolsByName")
	assert.False(t, p.isDeferred("mcp__weather__get_alerts"), "pruned tool must be un-deferred")
	_, stillInBox := p.toolboxByName["weather"].toolNames["mcp__weather__get_alerts"]
	assert.False(t, stillInBox, "pruned tool must leave the namespace membership set")

	// beforeTool must guard against a stale wrapper of the pruned tool: the
	// name still parses as mcp__weather__get_alerts and its namespace is a
	// registered MCP server, so the callback intercepts the call with the
	// "not loaded yet" guidance instead of letting a stale wrapper picked
	// up earlier this turn execute.
	bt, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__weather__get_alerts"})
	require.NoError(t, err)
	require.NotNil(t, bt, "pruned MCP tool must be intercepted, not passed through")
	assert.Contains(t, bt.CustomResult.(string), "not loaded yet")

	// The catalog rendered for the model reflects only the current snapshot.
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
	}
	_, err = p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Contains(t, req.Messages[0].Content, "mcp__weather__get_forecast")
	assert.NotContains(t, req.Messages[0].Content, "mcp__weather__get_alerts")
}

// TestMCPToolbox_EmptyListingPreservesSnapshot verifies that an empty listing
// is treated as a transient failure of the MCP server: the previously indexed
// tools stay visible/callable rather than being wiped, and a subsequent
// non-empty listing restores the normal reconciliation behaviour.
func TestMCPToolbox_EmptyListingPreservesSnapshot(t *testing.T) {
	ts := &fakeToolSet{
		name:  "weather",
		tools: []tool.Tool{newTestTool("get_forecast", "forecast")},
	}
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// First listing populates the index.
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	require.True(t, p.isDeferred("mcp__weather__get_forecast"))

	// Server temporarily reports an empty directory (transient failure). The
	// stale entry MUST stay indexed so a brief outage does not wipe the
	// namespace and force re-embedding on recovery.
	ts.setTools(nil)
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.True(t, p.isDeferred("mcp__weather__get_forecast"),
		"empty listing must be treated as transient and preserve the previous snapshot")

	_, stillIndexed := p.toolsByName["mcp__weather__get_forecast"]
	assert.True(t, stillIndexed, "empty listing must not evict tools from toolsByName")

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Contains(t, req.Messages[0].Content, "mcp__weather__get_forecast",
		"catalog must retain the last known snapshot across a transient empty listing")

	// A subsequent non-empty listing resumes normal reconciliation: replacing
	// the tool set now genuinely prunes the old entry.
	ts.setTools([]tool.Tool{newTestTool("get_alerts", "alerts")})
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather"})
	assert.False(t, p.isDeferred("mcp__weather__get_forecast"),
		"non-empty listing must resume normal prune behaviour")
	assert.True(t, p.isDeferred("mcp__weather__get_alerts"),
		"new tool from the recovered listing must be indexed")
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
		{"mcp with no tool", "mcp__server__", "", "", false},
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
	p1 := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "", Description: "desc", ToolSet: &fakeToolSet{name: "empty"}},
	}))
	assert.Len(t, p1.toolboxes, 0, "empty server name MCP toolbox should be skipped")

	// Nil ToolSet should be skipped.
	p2 := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "valid", Description: "desc", ToolSet: nil},
	}))
	assert.Len(t, p2.toolboxes, 0, "nil ToolSet MCP toolbox should be skipped")

	// Collision should be skipped (two MCP toolboxes with same ServerName).
	p3 := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "collision", ToolSet: &fakeToolSet{name: "c1", tools: []tool.Tool{newTestTool("t1", "d1")}}},
		{ServerName: "collision", ToolSet: &fakeToolSet{name: "c2", tools: []tool.Tool{newTestTool("t2", "d2")}}},
	}))
	assert.Len(t, p3.toolboxes, 1, "colliding MCP namespace should keep only the first")
	assert.Equal(t, "collision", p3.toolboxes[0].name)

	// MCP toolbox collision with static toolbox.
	p4 := New(nil,
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
	p := New(nil, WithToolboxes([]Toolbox{
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
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
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
	p := New(nil,
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

// TestMCPToolbox_LoadedToolPrunedMidTurnIsBlocked verifies stale MCP names
// are blocked even if a stale wrapper was injected earlier in the turn.
func TestMCPToolbox_LoadedToolPrunedMidTurnIsBlocked(t *testing.T) {
	ts := &fakeToolSet{
		name:  "billing",
		tools: []tool.Tool{newTestTool("create_invoice", "create an invoice")},
	}
	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, inv := ctxWithInvocation()

	// Turn N: model calls tool_search and loads mcp__billing__create_invoice.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "billing", Queries: []string{"invoice"}})
	require.Equal(t, []string{"mcp__billing__create_invoice"}, toolNames(res.Tools))

	// Turn N+1: beforeModel injects the loaded tool's schema into req.Tools.
	req := &model.Request{}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, injected := req.Tools["mcp__billing__create_invoice"]
	require.True(t, injected, "loaded MCP tool schema must be injected before the server prunes it")

	// Server prunes this tool but still lists something else, so the empty-
	// listing transient-failure guard does not kick in and the prune actually
	// takes effect.
	ts.setTools([]tool.Tool{newTestTool("list_invoices", "list existing invoices")})

	// A downstream materialize (e.g. another beforeModel or a materializeAllMCP
	// triggered by any subsequent read path) removes the tool from the index
	// while req.Tools still carries the earlier wrapper.
	p.materializeAllMCP(ctx)
	assert.False(t, p.isDeferred("mcp__billing__create_invoice"),
		"pruned tool must leave the deferred index")

	// The model then tries to call the stale name. beforeTool must block it:
	// isDeferred() is now false but isStaleMCPTool() catches an MCP-prefixed
	// name owned by a registered namespace whose current listing no longer
	// exposes it.
	bt, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__billing__create_invoice"})
	require.NoError(t, err)
	require.NotNil(t, bt, "stale MCP tool call must be intercepted")
	assert.Contains(t, bt.CustomResult.(string), "not loaded yet")

	// And a fresh beforeModel this turn must not re-inject the stale wrapper
	// (single materialize + single schema-injection pass keeps the request
	// consistent with the current snapshot).
	req = &model.Request{}
	_, err = p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, stillInjected := req.Tools["mcp__billing__create_invoice"]
	assert.False(t, stillInjected,
		"beforeModel must not inject a schema for a tool absent from the current snapshot")

	// Session state may still list the loaded name (persistence outlives a
	// listing), but the callback layer keeps the model from invoking it.
	assert.Contains(t, p.loadDiscoveredTools(ctx, inv), "mcp__billing__create_invoice")
}

// TestMCPToolbox_UnchangedToolKeepsEmbedding verifies unchanged declarations
// do not trigger re-embedding across repeated listings.
func TestMCPToolbox_UnchangedToolKeepsEmbedding(t *testing.T) {
	tool1 := newTestTool("get_forecast", "get the weather forecast")
	ts := &fakeToolSet{name: "weather", tools: []tool.Tool{tool1}}

	counter := &countingEmbedder{}
	k, err := NewSemanticToolIndex(counter)
	require.NoError(t, err)

	p := New(nil,
		WithSemanticToolIndex(k),
		WithMCPToolboxes([]MCPToolbox{
			{ServerName: "weather", ToolSet: ts},
		}),
	)
	ctx, _ := ctxWithInvocation()

	// First listing indexes the tool; first embedding search embeds it once.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
	require.Equal(t, []string{"mcp__weather__get_forecast"}, toolNames(res.Tools))
	docEmbeddingsAfterFirst := counter.docCount()
	require.Equal(t, 1, docEmbeddingsAfterFirst, "first listing must embed once")

	// Several unchanged re-listings: each one used to forget-and-re-embed
	// every fresh name, blowing away the cached embedding on every turn. The
	// fingerprint gate must recognize declarations are unchanged and leave
	// the store alone.
	for i := 0; i < 3; i++ {
		callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
	}
	assert.Equal(t, docEmbeddingsAfterFirst, counter.docCount(),
		"unchanged tool declarations must not trigger re-embedding")

	// Now genuinely change the declaration: the fingerprint diverges and
	// forget-then-upsert re-embeds exactly once.
	ts.setTools([]tool.Tool{newTestTool("get_forecast", "get the updated weather forecast")})
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
	assert.Equal(t, docEmbeddingsAfterFirst+1, counter.docCount(),
		"changed declaration must be re-embedded exactly once")
}

// countingEmbedder counts document embeddings for cache-invalidation tests.
type countingEmbedder struct {
	mu    sync.Mutex
	calls int
}

func (e *countingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	v, _, err := e.GetEmbeddingWithUsage(ctx, text)
	return v, err
}

func (e *countingEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	usage := map[string]any{"prompt_tokens": int64(1), "total_tokens": int64(1)}
	// Count only tool-document embeddings (prefixed with "Tool: ").
	if len(text) >= 6 && text[:6] == "Tool: " {
		e.mu.Lock()
		e.calls++
		e.mu.Unlock()
	}
	return []float64{1, 0, 0}, usage, nil
}

func (e *countingEmbedder) GetDimensions() int { return 3 }

func (e *countingEmbedder) docCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// TestRenderToolboxCatalog_MatchesInvocationMode verifies catalog guidance
// matches the active invocation mode.
func TestRenderToolboxCatalog_MatchesInvocationMode(t *testing.T) {
	tools := []tool.Tool{newTestTool("create_invoice", "make an invoice")}

	native := New(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: tools},
	}))
	nativeCatalog := native.renderToolboxCatalog(nil)
	assert.Contains(t, nativeCatalog, "call it directly",
		"native mode catalog must instruct direct calls")
	assert.NotContains(t, nativeCatalog, "`call_tool`",
		"native mode catalog must not mention call_tool")

	dispatch := New(nil,
		WithInvocationMode(DispatchToolCalls),
		WithToolboxes([]Toolbox{
			{Name: "billing", Description: "invoices", Tools: tools},
		}),
	)
	dispatchCatalog := dispatch.renderToolboxCatalog(nil)
	assert.Contains(t, dispatchCatalog, "`call_tool`",
		"dispatch mode catalog must instruct call_tool")
	assert.NotContains(t, dispatchCatalog, "call it directly",
		"dispatch mode catalog must not tell the model to call the tool directly")
	// The dispatch catalog must reinforce that tool_search returns the schema
	// needed to build call_tool params, matching toolSearchCallToolDescription.
	assert.Contains(t, dispatchCatalog, "input_schema")
}

// TestMCPToolbox_StaleSnapshotUpsertIsRejected pins the TOCTOU fix for the
// snapshot / prune|refresh / upsert race. In both cases Search A snapshots T
// before a concurrent refresh mutates the Plugin index; the pre-fix upsert
// would blindly (re-)publish T from the stale snapshot. verifyCandidate must
// reject the publish so the stale vector cannot resurrect a forgotten name
// or clobber a freshly-installed fingerprint.
func TestMCPToolbox_StaleSnapshotUpsertIsRejected(t *testing.T) {
	const renamed = "mcp__weather__get_forecast"

	tests := []struct {
		name string
		// nextTools is what the server lists after Search A's snapshot.
		nextTools func() []tool.Tool
		// runFreshSearch controls whether a follow-up search re-embeds the
		// new declaration before Search A's stale upsert runs (only makes
		// sense for the refresh case).
		runFreshSearch bool
		// assertFinal validates indexed[renamed] after the stale upsert.
		// staleFP is what Search A had captured before the refresh.
		assertFinal func(t *testing.T, k *SemanticToolIndex, staleFP string)
	}{
		{
			name: "pruned tool is not resurrected",
			nextTools: func() []tool.Tool {
				return []tool.Tool{newTestTool("list_alerts", "list severe weather alerts")}
			},
			assertFinal: func(t *testing.T, k *SemanticToolIndex, _ string) {
				k.mu.Lock()
				defer k.mu.Unlock()
				_, ok := k.indexed[renamed]
				assert.False(t, ok,
					"stale-snapshot upsert must not repopulate indexed[T] after forget")
			},
		},
		{
			name:           "refreshed fingerprint is not overwritten",
			runFreshSearch: true,
			nextTools: func() []tool.Tool {
				return []tool.Tool{newTestTool("get_forecast", "get the updated weather forecast")}
			},
			assertFinal: func(t *testing.T, k *SemanticToolIndex, staleFP string) {
				k.mu.Lock()
				defer k.mu.Unlock()
				finalFP := k.indexed[renamed]
				assert.NotEqual(t, staleFP, finalFP,
					"declaration change must bump the fingerprint tracked in indexed")
				assert.NotEmpty(t, finalFP,
					"fresh fingerprint must remain installed after stale upsert")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := &fakeToolSet{
				name:  "weather",
				tools: []tool.Tool{newTestTool("get_forecast", "get the weather forecast")},
			}
			counter := &countingEmbedder{}
			k, err := NewSemanticToolIndex(counter)
			require.NoError(t, err)

			p := New(nil,
				WithSemanticToolIndex(k),
				WithMCPToolboxes([]MCPToolbox{
					{ServerName: "weather", ToolSet: ts},
				}),
			)
			ctx, _ := ctxWithInvocation()

			// Prime: list + embed T once under its original declaration.
			callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
			require.Equal(t, 1, counter.docCount(), "prime step must embed exactly once")

			// Search A snapshots candidates before the refresh runs.
			snapshot, errPayload := p.embeddingCandidates("weather", nil)
			require.Empty(t, errPayload)
			require.Contains(t, snapshot, renamed)
			staleFP := snapshot[renamed].fingerprint

			// Refresh mutates the Plugin index (prune or fingerprint bump).
			ts.setTools(tc.nextTools())
			p.materializeAllMCP(ctx)

			// Optionally let a fresh search publish the new embedding so the
			// stale upsert has something concrete to try to clobber.
			if tc.runFreshSearch {
				callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
			}

			// Search A's upsert on the stale snapshot must not re-embed T.
			embedsBefore := counter.docCount()
			usage := &model.Usage{}
			require.NoError(t, k.upsert(ctx, snapshot, p.verifyCandidate, usage))
			assert.Equal(t, embedsBefore, counter.docCount(),
				"stale-snapshot upsert must not re-embed T")

			tc.assertFinal(t, k, staleFP)
		})
	}
}

// TestMCPToolbox_ConcurrentRefreshAndSearch stresses the interleaving of MCP
// refresh (prune + forget) and semantic search (snapshot + upsert). After
// the storm, every entry in k.indexed must still be an authoritative deferred
// tool with a matching fingerprint; any leak means a stale snapshot slipped
// past verifyCandidate.
func TestMCPToolbox_ConcurrentRefreshAndSearch(t *testing.T) {
	// Two declarations rotate so refresh flips between them; either is a
	// legal "current" state, and a third case prunes get_forecast entirely.
	toolA := newTestTool("get_forecast", "declaration A")
	toolB := newTestTool("get_forecast", "declaration B")
	ts := &fakeToolSet{name: "weather", tools: []tool.Tool{toolA}}

	counter := &countingEmbedder{}
	k, err := NewSemanticToolIndex(counter)
	require.NoError(t, err)

	p := New(nil,
		WithSemanticToolIndex(k),
		WithMCPToolboxes([]MCPToolbox{
			{ServerName: "weather", ToolSet: ts},
		}),
	)
	ctx, _ := ctxWithInvocation()

	// Prime so the first refresh takes the "fingerprint changed" branch too.
	callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})

	const iterations = 200
	var wg sync.WaitGroup

	// Refresher: alternate the server-side declaration and materialize.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			switch i % 3 {
			case 0:
				ts.setTools([]tool.Tool{toolA})
			case 1:
				ts.setTools([]tool.Tool{toolB})
			case 2:
				ts.setTools([]tool.Tool{newTestTool("list_alerts", "alerts")})
			}
			p.materializeAllMCP(ctx)
		}
	}()

	// Searcher: repeatedly run semantic search through candidates + upsert.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			callSearch(t, ctx, p, toolSearchInput{Namespace: "weather", Queries: []string{"forecast"}})
		}
	}()

	wg.Wait()

	// Invariant: every indexed entry must still be authoritative under the
	// current Plugin state. A violation means a stale snapshot slipped past.
	k.mu.Lock()
	snapshot := make(map[string]string, len(k.indexed))
	for name, fp := range k.indexed {
		snapshot[name] = fp
	}
	k.mu.Unlock()

	for name, fp := range snapshot {
		assert.True(t, p.verifyCandidate(name, fp),
			"indexed entry %q@%s must remain authoritative in the Plugin index",
			name, fp)
	}
}

// streamOnlyTool is a Tool that implements StreamableTool but not CallableTool,
// used to verify the dispatch-mode call_tool / rename-wrapper Call contract for
// tools that only speak the streaming interface.
type streamOnlyTool struct {
	name   string
	desc   string
	chunks []string
}

func (s *streamOnlyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: s.desc,
		InputSchema: &tool.Schema{Type: "object"},
	}
}

func (s *streamOnlyTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	stream := tool.NewStream(len(s.chunks) + 1)
	go func() {
		defer stream.Writer.Close()
		for _, c := range s.chunks {
			stream.Writer.Send(tool.StreamChunk{Content: c}, nil)
		}
	}()
	return stream.Reader, nil
}

// TestMCPRenamedTool_StreamOnlyCallAggregates verifies that Call on a renamed
// wrapper whose underlying tool is StreamableTool-only drains the stream and
// aggregates chunks in order — instead of returning "tool is not callable".
// StreamableTool is an independent framework interface, so a stream-only tool
// stays a first-class deferred tool.
func TestMCPRenamedTool_StreamOnlyCallAggregates(t *testing.T) {
	orig := &streamOnlyTool{name: "stream_it", desc: "d", chunks: []string{"a", "b", "c"}}
	renamed := newRenamedTool(orig, "svc")
	// Sanity: the wrapper must not skip streaming for a genuinely streaming tool.
	_, isStream := renamed.(tool.StreamableTool)
	require.True(t, isStream, "renamed wrapper must expose StreamableTool for a streaming original")

	callable, ok := renamed.(tool.CallableTool)
	require.True(t, ok, "renamed wrapper must always expose CallableTool")

	res, err := callable.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	envelope, ok := res.(map[string]any)
	require.True(t, ok, "aggregated stream must be map envelope, got %T", res)
	chunks, ok := envelope["chunks"].([]any)
	require.True(t, ok, "aggregated envelope must carry chunks slice, got %T", envelope["chunks"])
	assert.Equal(t, []any{"a", "b", "c"}, chunks, "chunk order must be preserved")
}

// TestPlugin_CallToolFn_StreamOnlyToolInDispatchMode verifies that in
// DispatchToolCalls mode, call_tool can invoke a deferred tool that only
// implements StreamableTool. Without stream support, dispatch mode would reject
// the tool as "not callable" — a regression from NativeToolCalls, where the
// framework routes streaming tools through their StreamableCall path.
func TestPlugin_CallToolFn_StreamOnlyToolInDispatchMode(t *testing.T) {
	streamTool := &streamOnlyTool{name: "stream_it", desc: "d", chunks: []string{"x", "y"}}
	p := New(nil,
		WithInvocationMode(DispatchToolCalls),
		WithDeferredTools([]tool.Tool{streamTool}),
	)
	ctx, inv := ctxWithInvocation()

	// Load the tool via tool_search so the loaded-set guard passes.
	p.appendDiscoveredTools(ctx, inv, []string{"stream_it"})

	res, err := p.callToolFn(ctx, callToolInput{ToolName: "stream_it", Params: map[string]any{}})
	require.NoError(t, err)
	envelope, ok := res.(map[string]any)
	require.True(t, ok, "call_tool must return aggregated envelope for stream-only tool, got %T", res)
	chunks, ok := envelope["chunks"].([]any)
	require.True(t, ok, "envelope must carry chunks slice")
	assert.Equal(t, []any{"x", "y"}, chunks, "stream order must be preserved through call_tool")
	// No error status should be reported for the stream-only path.
	if status, ok := envelope["status"].(string); ok {
		assert.NotEqual(t, "error", status, "stream-only tool must not be treated as not callable")
	}
}

// blockingToolSet is a fake ToolSet whose Tools() call is gated on a per-call
// release channel, so a test can control the interleaving of concurrent
// refreshes on the same MCP namespace.
type blockingToolSet struct {
	name    string
	mu      sync.Mutex
	queue   []releaseStep // FIFO of listings this ToolSet will serve
	entered chan struct{} // signals when a call entered Tools()
}

// releaseStep parameterizes a single Tools() call.
type releaseStep struct {
	release chan struct{} // caller unblocks Tools() by closing this
	tools   []tool.Tool   // what to return after unblocking
}

func (b *blockingToolSet) Name() string { return b.name }
func (b *blockingToolSet) Close() error { return nil }
func (b *blockingToolSet) Tools(context.Context) []tool.Tool {
	b.mu.Lock()
	if len(b.queue) == 0 {
		b.mu.Unlock()
		return nil
	}
	step := b.queue[0]
	b.queue = b.queue[1:]
	b.mu.Unlock()
	if b.entered != nil {
		b.entered <- struct{}{}
	}
	<-step.release
	// Copy so a concurrent test mutation cannot race iteration downstream.
	out := make([]tool.Tool, len(step.tools))
	copy(out, step.tools)
	return out
}

// TestMCPToolbox_SerializeRefreshesPerNamespace verifies that two concurrent
// refreshes on the same MCP namespace apply atomically and in the order the
// listMu is granted, so an older listing released later than a newer one can
// never overtake the newer commit. Without per-namespace serialization, the
// slower listing could acquire p.mu after the faster one and restore removed
// tools or overwrite a newer declaration.
//
// This test releases the FIRST listing first (natural FIFO), so its refresh
// commits first; the SECOND listing then commits and its snapshot must be the
// final visible state. If serialization were broken and the two applies
// interleaved, the final state could contain tools from both snapshots.
func TestMCPToolbox_SerializeRefreshesPerNamespace(t *testing.T) {
	oldTool := newTestTool("old_forecast", "old")
	newTool := newTestTool("new_forecast", "new")

	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	ts := &blockingToolSet{
		name:    "weather",
		entered: make(chan struct{}, 2),
		queue: []releaseStep{
			{release: firstRelease, tools: []tool.Tool{oldTool}},
			{release: secondRelease, tools: []tool.Tool{newTool}},
		},
	}

	p := New(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "weather", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.materializeAllMCP(ctx) }()

	// Wait for the first call to enter Tools() so the ordering is deterministic.
	select {
	case <-ts.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first Tools() never entered")
	}
	go func() { defer wg.Done(); p.materializeAllMCP(ctx) }()

	// Release both listings; the second refresh holds behind listMu until the
	// first fully applies, so the second snapshot is guaranteed to be the
	// final visible state.
	close(firstRelease)
	// Wait for the second call to enter Tools() as well before releasing it,
	// so both refreshes are truly in-flight.
	select {
	case <-ts.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second Tools() never entered")
	}
	close(secondRelease)
	wg.Wait()

	// Atomic apply invariant: the two snapshots must NOT mix. And because the
	// second refresh commits after the first (serialized), the final state
	// must be exactly the newer snapshot.
	p.mu.RLock()
	_, hasOld := p.deferredNames["mcp__weather__old_forecast"]
	_, hasNew := p.deferredNames["mcp__weather__new_forecast"]
	p.mu.RUnlock()
	assert.False(t, hasOld && hasNew, "concurrent MCP refreshes must not produce a mixed state")
	assert.True(t, hasNew, "final state must reflect the newer snapshot after serialized apply")
	assert.False(t, hasOld, "older snapshot must not persist after the newer one commits")
}
