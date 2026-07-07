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
	ctx, inv := ctxWithInvocation()

	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"send_email"}})
	callSearch(t, ctx, p, toolSearchInput{ToolNames: []string{"create_doc"}})

	loaded := p.loadDiscoveredTools(ctx, inv)
	assert.ElementsMatch(t, []string{"send_email", "create_doc"}, loaded)
}

func TestDuplicateNamespaceRegistrationKeepsFirstOwner(t *testing.T) {
	shared := newTestTool("shared_tool", "x")
	p := NewPlugin(nil, WithToolboxes([]Toolbox{
		{Name: "first", Tools: []tool.Tool{shared}},
		{Name: "second", Tools: []tool.Tool{shared}},
	}))
	assert.Equal(t, "first", p.namespaceByTool["shared_tool"])
	_, inFirst := p.toolboxByName["first"].toolNames["shared_tool"]
	_, inSecond := p.toolboxByName["second"].toolNames["shared_tool"]
	assert.True(t, inFirst)
	assert.False(t, inSecond)
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
