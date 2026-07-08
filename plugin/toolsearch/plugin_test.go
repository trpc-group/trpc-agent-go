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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// newTestTool builds a no-op function tool with the given name and description.
func newTestTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in struct{}) (string, error) { return "", nil },
		function.WithName(name),
		function.WithDescription(desc),
	)
}

// newEchoTool builds a function tool that echoes its "value" input, used to
// verify call_tool forwards params to the underlying tool.
func newEchoTool(name string) tool.Tool {
	type echoIn struct {
		Value string `json:"value"`
	}
	return function.NewFunctionTool(
		func(ctx context.Context, in echoIn) (string, error) { return "echo:" + in.Value, nil },
		function.WithName(name),
		function.WithDescription("echo the value input"),
	)
}

// callSearch invokes the tool_search entry point and decodes the result.
func callSearch(t *testing.T, ctx context.Context, p *Plugin, in toolSearchInput) searchResult {
	t.Helper()
	raw, err := p.searchTools(ctx, in)
	require.NoError(t, err)
	var res searchResult
	require.NoError(t, json.Unmarshal([]byte(raw), &res))
	return res
}

// searchResult mirrors the JSON produced by formatSearchResult.
type searchResult struct {
	Status               string        `json:"status"`
	Tools                []toolSummary `json:"tools"`
	AdditionalCandidates []string      `json:"additional_candidates"`
}

func toolNames(tools []toolSummary) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// ctxWithInvocation returns a context carrying an invocation with a session but
// no SessionService (state persists in memory only).
func ctxWithInvocation() (context.Context, *agent.Invocation) {
	inv := &agent.Invocation{
		Session: &session.Session{
			AppName: "app",
			UserID:  "user",
			ID:      "sess",
			State:   session.StateMap{},
		},
	}
	return agent.NewInvocationContext(context.Background(), inv), inv
}

func TestPlugin_NameAndInterface(t *testing.T) {
	var _ plugin.Plugin = (*Plugin)(nil)

	p := NewPlugin(nil)
	assert.Equal(t, PluginName, p.Name())

	p2 := NewPlugin(nil, WithName("custom"))
	assert.Equal(t, "custom", p2.Name())
}

func TestSelectToolsByName_CrossNamespaceCaseInsensitiveDedup(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("create_invoice", "make an invoice")}},
		{Name: "media", Tools: []tool.Tool{newTestTool("create_image", "render an image")}},
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{
		ToolNames: []string{"CREATE_INVOICE", " create_image ", "create_invoice", "unknown"},
	})
	assert.ElementsMatch(t, []string{"create_invoice", "create_image"}, toolNames(res.Tools))
}

func TestSearchByQueries_ScopedToNamespace(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{
			newTestTool("create_invoice", "create a billing invoice"),
			newTestTool("refund_payment", "refund a payment"),
		}},
		{Name: "media", Description: "images", Tools: []tool.Tool{
			newTestTool("create_image", "create an invoice-like graphic"),
		}},
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "billing", Queries: []string{"invoice"}})
	assert.Equal(t, []string{"create_invoice"}, toolNames(res.Tools))
	// media's create_image must not leak into the billing-scoped search.
	assert.NotContains(t, toolNames(res.Tools), "create_image")
}

func TestSearchByQueries_RequiredTermGating(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "ops", Tools: []tool.Tool{
			newTestTool("export_invoice", "export an invoice to pdf"),
			newTestTool("export_report", "export a report"),
		}},
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "ops", Queries: []string{"+invoice export"}})
	assert.Equal(t, []string{"export_invoice"}, toolNames(res.Tools))
}

func TestSearch_MissingNamespaceFallsBackToGlobalSearch(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices and payments", Tools: []tool.Tool{
			newTestTool("create_invoice", "create a billing invoice"),
			newTestTool("refund_payment", "refund a payment"),
		}},
		{Name: "media", Description: "image assets", Tools: []tool.Tool{
			newTestTool("create_image", "create an image asset"),
		}},
		{Name: "ops", Description: "operational reports", Tools: []tool.Tool{
			newTestTool("export_report", "export an operational report"),
		}},
	}))
	ctx, _ := ctxWithInvocation()

	// (a) With no namespace and no _default tools, search must fall back to a
	//     global sweep across every toolbox and still surface the billing tool.
	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"invoice"}})
	names := toolNames(res.Tools)
	assert.Contains(t, names, "create_invoice",
		"cross-toolbox search should reach into the billing box even without a namespace hint")
	assert.NotContains(t, names, "create_image",
		"tools whose descriptions do not match the query must not appear")
	assert.NotContains(t, names, "export_report")

	// (b) A single query can legitimately hit multiple toolboxes at once —
	//     "create" matches both billing.create_invoice and media.create_image.
	res = callSearch(t, ctx, p, toolSearchInput{Queries: []string{"create"}})
	names = toolNames(res.Tools)
	assert.Contains(t, names, "create_invoice")
	assert.Contains(t, names, "create_image")

	// (c) Passing a namespace still constrains the search — global fallback
	//     must not weaken the explicit-namespace guarantee.
	res = callSearch(t, ctx, p, toolSearchInput{Namespace: "media", Queries: []string{"create"}})
	assert.Equal(t, []string{"create_image"}, toolNames(res.Tools))
}

func TestSearch_UnknownNamespaceSuggests(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	raw, err := p.searchTools(ctx, toolSearchInput{Namespace: "biling", Queries: []string{"invoice"}})
	require.NoError(t, err)
	assert.Contains(t, raw, "unknown_namespace")
	assert.Contains(t, raw, "billing")
}

func TestSearch_NamespaceWithoutQueryListsTools(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{
			newTestTool("a_tool", "x"), newTestTool("b_tool", "y"),
		}},
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "billing"})
	assert.ElementsMatch(t, []string{"a_tool", "b_tool"}, toolNames(res.Tools))
}

func TestSearch_DeferredToolsNoNamespaceRequired(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{
		newTestTool("send_email", "send an email message"),
	}))
	ctx, _ := ctxWithInvocation()

	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"email"}})
	assert.Equal(t, []string{"send_email"}, toolNames(res.Tools))
	// _default namespace is never exposed to the model — no namespace field is
	// emitted at all in the current schema; ensure the tool is present and
	// nothing else leaks in.
	assert.Len(t, res.Tools, 1)
}

func TestSearch_MissingNamespaceSpansDefaultAndToolboxes(t *testing.T) {
	// WithDeferredTools (internal default namespace) coexists with named
	// toolboxes. An empty namespace query must reach into BOTH sources when
	// the query provides a domain signal that matches the toolbox description.
	p := NewPlugin(nil,
		WithDeferredTools([]tool.Tool{
			newTestTool("send_email", "send an email message"),
		}),
		WithToolboxes([]Toolbox{
			{Name: "billing", Description: "invoice and payment tools", Tools: []tool.Tool{
				newTestTool("create_invoice", "create a billing invoice"),
			}},
		}),
	)
	ctx, _ := ctxWithInvocation()

	// (a) Query hits a tool in the default (deferred) block.
	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"email"}})
	assert.Equal(t, []string{"send_email"}, toolNames(res.Tools))

	// (b) A domain-hint query reaches into a named toolbox even though no
	//     namespace was passed. The default block is always kept in scope,
	//     so the billing tool is guaranteed to appear alongside it.
	res = callSearch(t, ctx, p, toolSearchInput{Queries: []string{"invoice"}})
	assert.Contains(t, toolNames(res.Tools), "create_invoice",
		"description-biased search must pull the matching toolbox into scope")
}

func TestSearch_AlreadyLoaded(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("send_email", "send email")}))
	ctx, _ := ctxWithInvocation()

	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"send_email"}})
	res := callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"send_email"}})
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "send_email", res.Tools[0].Name)
	assert.True(t, res.Tools[0].AlreadyLoaded)
}

func TestBeforeModel_InjectsSearchToolAndCatalog(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "Tools:\n" + Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	_, ok := req.Tools[toolSearchToolName]
	assert.True(t, ok, "tool_search must be injected")
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
	assert.Contains(t, req.Messages[0].Content, "billing")
	// Deferred tool schema must NOT be injected before it is loaded.
	_, loaded := req.Tools["create_invoice"]
	assert.False(t, loaded)
}

func TestBeforeModel_InjectsLoadedToolSchema(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"create_invoice"}})

	req := &model.Request{}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, ok := req.Tools["create_invoice"]
	assert.True(t, ok, "loaded deferred tool schema must be injected")
}

func TestBeforeModel_LegacyDeferredRendering(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("send_email", "x")}))
	ctx, _ := ctxWithInvocation()

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Contains(t, req.Messages[0].Content, "<available-deferred-tools>")
	assert.Contains(t, req.Messages[0].Content, "send_email")
}

func TestBeforeTool_BlocksUnloadedDeferredTool(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "create_invoice"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.CustomResult)
	assert.Contains(t, res.CustomResult.(string), "not loaded yet")
}

func TestBeforeTool_AllowsLoadedDeferredTool(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"create_invoice"}})
	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "create_invoice"})
	require.NoError(t, err)
	assert.Nil(t, res, "loaded deferred tool must pass through")
}

func TestBeforeTool_IgnoresPresetTool(t *testing.T) {
	p := NewPlugin([]tool.Tool{newTestTool("preset_tool", "x")})
	ctx, _ := ctxWithInvocation()

	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "preset_tool"})
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestPermissionFilter_HidesAndBlocks(t *testing.T) {
	filter := func(ctx context.Context, names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, n := range names {
			out[n] = n == "allowed_tool"
		}
		return out
	}
	p := NewPlugin(nil,
		WithToolboxes([]Toolbox{{Name: "ns", Tools: []tool.Tool{
			newTestTool("allowed_tool", "ok"),
			newTestTool("denied_tool", "no"),
		}}}),
		WithToolPermissionFilter(filter),
	)
	ctx, _ := ctxWithInvocation()

	// Search must drop the denied tool.
	res := callSearch(t, ctx, p, toolSearchInput{Namespace: "ns"})
	assert.Equal(t, []string{"allowed_tool"}, toolNames(res.Tools))

	// Calling the denied tool must be blocked with a permission message.
	br, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "denied_tool"})
	require.NoError(t, err)
	require.NotNil(t, br)
	assert.Contains(t, br.CustomResult.(string), "permission")
}

func TestSessionState_PersistsAcrossLoad(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{
		newTestTool("send_email", "x"), newTestTool("create_doc", "y"),
	}))
	ctx, _ := ctxWithInvocation()

	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"send_email"}})
	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"create_doc"}})

	req := &model.Request{}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	_, ok := req.Tools["send_email"]
	assert.True(t, ok, "send_email should be injected after loading")
	_, ok = req.Tools["create_doc"]
	assert.True(t, ok, "create_doc should be injected after loading")
}

func TestDuplicateNamespaceRegistrationKeepsFirstOwner(t *testing.T) {
	shared := newTestTool("shared_tool", "x")
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "first", Tools: []tool.Tool{shared}},
		{Name: "second", Tools: []tool.Tool{shared}},
	}))
	ctx, _ := ctxWithInvocation()

	first := callSearch(t, ctx, p, toolSearchInput{Namespace: "first"})
	assert.Contains(t, toolNames(first.Tools), "shared_tool",
		"shared_tool should be discoverable under the first namespace")

	second := callSearch(t, ctx, p, toolSearchInput{Namespace: "second"})
	assert.NotContains(t, toolNames(second.Tools), "shared_tool",
		"shared_tool should not be discoverable under the second namespace")
}

func TestParseToolName(t *testing.T) {
	cases := map[string][]string{
		"create_invoice":      {"create", "invoice"},
		"createInvoice":       {"create", "invoice"},
		"mcp__server__action": {"mcp", "server", "action"},
	}
	for name, want := range cases {
		assert.Equal(t, want, parseToolName(name).Parts, name)
	}
}

func TestRegister_NilCases(t *testing.T) {
	// Nil Plugin receiver.
	var p *Plugin
	p.Register(nil)
	// should not panic

	// Valid Plugin with nil registry.
	p2 := NewPlugin(nil)
	p2.Register(nil)
	// should not panic
}

func TestAfterTool(t *testing.T) {
	p := NewPlugin(nil)
	ctx, _ := ctxWithInvocation()
	res, err := p.afterTool(ctx, &tool.AfterToolArgs{})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestHasRegisteredToolboxes(t *testing.T) {
	p := NewPlugin(nil)
	assert.False(t, p.hasRegisteredToolboxes(), "empty plugin has no toolboxes")

	p2 := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("t1", "d1")}},
	}))
	assert.True(t, p2.hasRegisteredToolboxes())

	p3 := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "mcp1", ToolSet: &fakeToolSet{name: "mcp1"}},
	}))
	assert.True(t, p3.hasRegisteredToolboxes())
}

func TestIsDefaultOnly(t *testing.T) {
	// WithDeferredTools alone → only _default toolbox.
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}))
	assert.True(t, p.isDefaultOnly())

	// With toolboxes → not default only.
	p2 := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("t1", "d1")}},
	}))
	assert.False(t, p2.isDefaultOnly())

	// Both → not default only.
	p3 := NewPlugin(nil,
		WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}),
		WithToolboxes([]Toolbox{{Name: "billing", Tools: []tool.Tool{newTestTool("t2", "d2")}}}),
	)
	assert.False(t, p3.isDefaultOnly())
}

func TestAllDeferredPermissions_NoFilter(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}))
	ctx, _ := ctxWithInvocation()
	assert.Nil(t, p.allDeferredPermissions(ctx), "nil filter returns nil")
}

func TestFilterAllowed_NilAllowed(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}))
	names := []string{"t1", "t2"}
	result := p.filterAllowed(names, nil)
	assert.Equal(t, names, result, "nil allowed returns original names")
}

func TestBeforeModel_NilArgs(t *testing.T) {
	p := NewPlugin(nil)
	ctx, _ := ctxWithInvocation()
	res, err := p.beforeModel(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBeforeModel_NilRequest(t *testing.T) {
	p := NewPlugin(nil)
	ctx, _ := ctxWithInvocation()
	res, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: nil})
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBeforeModel_NoInvocation(t *testing.T) {
	p := NewPlugin(nil)
	ctx := context.Background() // no invocation
	res, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBeforeModel_CatalogInDescription(t *testing.T) {
	p := NewPlugin(nil, WithCatalogInDescription(true), WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	ctx, _ := ctxWithInvocation()

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "Tools:\n" + Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	_, ok := req.Tools[toolSearchToolName]
	assert.True(t, ok, "tool_search must be injected")
	// In catalogInDescription mode, placeholder should be stripped, not replaced with catalog.
	assert.NotContains(t, req.Messages[0].Content, "<toolbox-catalog>")
	assert.NotContains(t, req.Messages[0].Content, Placeholder)
	// The catalog lives in the tool_search description.
	found := req.Tools[toolSearchToolName]
	assert.Contains(t, found.Declaration().Description, "<toolbox-catalog>")
}

func TestBeforeModel_WithKnowledge(t *testing.T) {
	emb := &fakeEmbedder{vectors: map[string][]float64{}}
	k, err := NewToolKnowledge(emb, WithVectorStore(inmemory.New()))
	require.NoError(t, err)
	p := NewPlugin(nil, WithToolKnowledge(k))
	ctx, _ := ctxWithInvocation()

	res, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	require.NotNil(t, res)
	// Context should be updated with usage accumulator.
	require.NotNil(t, res.Context)
	usage, ok := ToolSearchUsageFromContext(res.Context)
	assert.True(t, ok, "usage accumulator should be retrievable from context")
	assert.NotNil(t, usage, "usage snapshot should not be nil")
}

func TestBeforeTool_NilArgs(t *testing.T) {
	p := NewPlugin(nil)
	ctx, _ := ctxWithInvocation()
	res, err := p.beforeTool(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBeforeTool_EmptyToolName(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}))
	ctx, _ := ctxWithInvocation()
	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: ""})
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBeforeTool_NoInvocation(t *testing.T) {
	p := NewPlugin(nil, WithDeferredTools([]tool.Tool{newTestTool("t1", "d1")}))
	ctx := context.Background()
	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "t1"})
	require.NoError(t, err)
	assert.Nil(t, res, "no invocation → let it through")
}

func TestBeforeTool_MCPToolBypassesPreSearchCheck(t *testing.T) {
	ts := &fakeToolSet{name: "billing", tools: []tool.Tool{newTestTool("create_invoice", "x")}}
	p := NewPlugin(nil, WithMCPToolboxes([]MCPToolbox{
		{ServerName: "billing", ToolSet: ts},
	}))
	ctx, _ := ctxWithInvocation()

	// Calling an MCP tool before search materializes the server and then fails
	// with "not loaded yet" (since it was not loaded via tool_search).
	res, err := p.beforeTool(ctx, &tool.BeforeToolArgs{ToolName: "mcp__billing__create_invoice"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.CustomResult.(string), "not loaded yet")
}

func TestReplaceDeferredToolsPlaceholder_NoPlaceholderAppend(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "System prompt without placeholder."}},
	}
	p.replaceDeferredToolsPlaceholder(&model.BeforeModelArgs{Request: req}, nil)
	// Placeholder not found → catalog appended to the first system message.
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
}

func TestReplaceDeferredToolsPlaceholder_NoSystemMessage(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Hello"}},
	}
	p.replaceDeferredToolsPlaceholder(&model.BeforeModelArgs{Request: req}, nil)
	// No system message → one is prepended.
	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
}

func TestReplaceDeferredToolsPlaceholder_EmptyCatalog(t *testing.T) {
	// No toolboxes → empty catalog → nothing appended.
	p := NewPlugin(nil)
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "System prompt."}},
	}
	p.replaceDeferredToolsPlaceholder(&model.BeforeModelArgs{Request: req}, nil)
	// Should remain unchanged (no catalog, no placeholder to strip).
	assert.Equal(t, "System prompt.", req.Messages[0].Content)
}

func TestReplaceDeferredToolsPlaceholder_CatalogInDescriptionStripsPlaceholder(t *testing.T) {
	p := NewPlugin(nil, WithCatalogInDescription(true))
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "Tools:\n" + Placeholder}},
	}
	p.replaceDeferredToolsPlaceholder(&model.BeforeModelArgs{Request: req}, nil)
	assert.NotContains(t, req.Messages[0].Content, Placeholder, "placeholder stripped in catalogInDescription mode")
}

func TestSearchToolWithCatalog(t *testing.T) {
	p := NewPlugin(nil, WithCatalogInDescription(true), WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
	}))
	wrapped := p.searchToolWithCatalog(nil)
	desc := wrapped.Declaration().Description
	assert.Contains(t, desc, "tool_search")
	assert.Contains(t, desc, "<toolbox-catalog>")

	// Call should delegate to the base tool.
	ctx, _ := ctxWithInvocation()
	callable, ok := wrapped.(tool.CallableTool)
	require.True(t, ok)
	raw, err := callable.Call(ctx, []byte(`{"tool_names":["create_invoice"]}`))
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

func TestSearchToolWithDynamicDesc_NilBaseDeclaration(t *testing.T) {
	// A wrapper with a base whose Declaration returns nil.
	wrapper := &searchToolWithDynamicDesc{
		base:        nilTool{},
		description: "test desc",
	}
	decl := wrapper.Declaration()
	assert.Equal(t, toolSearchToolName, decl.Name)
	assert.Equal(t, "test desc", decl.Description)
}

// nilTool is a tool.Tool whose Declaration() returns nil.
type nilTool struct{}

func (nilTool) Declaration() *tool.Declaration { return nil }

func TestSearchToolWithDynamicDesc_Call_NonCallable(t *testing.T) {
	nonCallable := &fakeNonCallableTool{name: "nc", desc: "not callable"}
	wrapper := &searchToolWithDynamicDesc{
		base:        nonCallable,
		description: "test",
	}
	_, err := wrapper.Call(context.Background(), []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not implement CallableTool")
}

func TestCallToolFn_EmptyToolName(t *testing.T) {
	p := NewPlugin(nil, WithEnableCallTool(true))
	ctx, _ := ctxWithInvocation()
	res, err := p.callToolFn(ctx, callToolInput{ToolName: ""})
	require.NoError(t, err)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", m["status"])
}

func TestCallToolFn_WhitespaceOnlyToolName(t *testing.T) {
	p := NewPlugin(nil, WithEnableCallTool(true))
	ctx, _ := ctxWithInvocation()
	res, err := p.callToolFn(ctx, callToolInput{ToolName: "   "})
	require.NoError(t, err)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", m["status"])
}

func TestCallToolFn_NoInvocation(t *testing.T) {
	p := NewPlugin(nil, WithEnableCallTool(true), WithDeferredTools([]tool.Tool{newEchoTool("echo")}))
	ctx := context.Background()
	// Call load to mark as loaded.
	p.saveDiscoveredTools(ctx, &agent.Invocation{Session: &session.Session{
		AppName: "app", UserID: "user", ID: "sess", State: session.StateMap{},
	}}, []string{"echo"})
	res, err := p.callToolFn(ctx, callToolInput{ToolName: "echo", Params: map[string]any{"value": "hi"}})
	require.NoError(t, err)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", m["status"])
	assert.Contains(t, m["message"], "not loaded yet")
}

func TestCallToolFn_PresetToolIsCallable(t *testing.T) {
	p := NewPlugin([]tool.Tool{newEchoTool("echo")}, WithEnableCallTool(true))
	ctx, _ := ctxWithInvocation()
	res, err := p.callToolFn(ctx, callToolInput{ToolName: "echo", Params: map[string]any{"value": "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "echo:hi", res)
}

func TestCallToolFn_CaseInsensitive(t *testing.T) {
	p := NewPlugin([]tool.Tool{newEchoTool("echo")}, WithEnableCallTool(true))
	ctx, _ := ctxWithInvocation()
	res, err := p.callToolFn(ctx, callToolInput{ToolName: "ECHO", Params: map[string]any{"value": "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "echo:hi", res)
}

func TestRenderToolboxCatalog_PermissionFiltering(t *testing.T) {
	filter := func(ctx context.Context, names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, n := range names {
			out[n] = n == "allowed_tool"
		}
		return out
	}
	p := NewPlugin(nil,
		WithToolPermissionFilter(filter),
		WithToolboxes([]Toolbox{{
			Name:        "ns",
			Description: "test ns",
			Tools:       []tool.Tool{newTestTool("allowed_tool", "ok"), newTestTool("denied_tool", "no")},
		}}),
	)
	// Pre-compute allowed map to fix race condition.
	ctx, _ := ctxWithInvocation()
	allowed := p.allDeferredPermissions(ctx)
	catalog := p.renderToolboxCatalog(allowed)
	assert.Contains(t, catalog, "allowed_tool")
	assert.NotContains(t, catalog, "denied_tool")
}

func TestRenderToolboxCatalog_LegacyWithPermission(t *testing.T) {
	filter := func(ctx context.Context, names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, n := range names {
			out[n] = n == "visible"
		}
		return out
	}
	p := NewPlugin(nil,
		WithToolPermissionFilter(filter),
		WithDeferredTools([]tool.Tool{
			newTestTool("visible", "ok"), newTestTool("hidden", "no"),
		}),
	)
	ctx, _ := ctxWithInvocation()
	allowed := p.allDeferredPermissions(ctx)
	catalog := p.renderToolboxCatalog(allowed)
	assert.Contains(t, catalog, "visible")
	assert.NotContains(t, catalog, "hidden")
}

func TestRenderToolboxCatalog_EmptyAfterFiltering(t *testing.T) {
	filter := func(ctx context.Context, names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, n := range names {
			out[n] = false
		}
		return out
	}
	p := NewPlugin(nil,
		WithToolPermissionFilter(filter),
		WithToolboxes([]Toolbox{{
			Name: "ns", Description: "desc", Tools: []tool.Tool{newTestTool("t1", "d1")},
		}}),
	)
	ctx, _ := ctxWithInvocation()
	allowed := p.allDeferredPermissions(ctx)
	catalog := p.renderToolboxCatalog(allowed)
	assert.Empty(t, catalog, "everything filtered → empty catalog")
}

func TestBeforeModel_AppendToSystemMessage(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "n", Description: "d", Tools: []tool.Tool{newTestTool("t1", "d1")}},
	}))
	ctx, _ := ctxWithInvocation()

	// System message without placeholder → catalog appended.
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: "Hello"}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Contains(t, req.Messages[0].Content, "Hello")
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
}

func TestBeforeModel_PrependSystemMessage(t *testing.T) {
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "n", Description: "d", Tools: []tool.Tool{newTestTool("t1", "d1")}},
	}))
	ctx, _ := ctxWithInvocation()

	// No system message → prepend one.
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "<toolbox-catalog>")
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
}

func TestBeforeModel_CatalogWithPermissionFilter(t *testing.T) {
	filter := func(ctx context.Context, names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, n := range names {
			out[n] = n == "allowed"
		}
		return out
	}
	p := NewPlugin(nil, WithToolPermissionFilter(filter), WithDeferredTools([]tool.Tool{
		newTestTool("allowed", "ok"), newTestTool("denied", "no"),
	}))
	ctx, _ := ctxWithInvocation()

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleSystem, Content: Placeholder}},
	}
	_, err := p.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Contains(t, req.Messages[0].Content, "allowed")
	assert.NotContains(t, req.Messages[0].Content, "denied")
}
