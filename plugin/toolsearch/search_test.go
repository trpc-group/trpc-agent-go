//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestFormatNamespaceError(t *testing.T) {
	p := New(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices"},
		{Name: "media", Description: "images"},
	}))
	p.mu.RLock()
	defer p.mu.RUnlock()

	raw := p.formatNamespaceError("unknown_namespace", "bad ns", "ghost")
	assert.Contains(t, raw, "unknown_namespace")
	assert.Contains(t, raw, "ghost")
	assert.Contains(t, raw, "billing")
	assert.Contains(t, raw, "media")
}

func TestSearchByQueries_PreservesCanonicalNameScope(t *testing.T) {
	p := New(nil, WithToolboxes([]Toolbox{
		{Name: "first", Tools: []tool.Tool{
			newTestTool("CREATE_INVOICE", ""),
			newTestTool("create_invoice_extra", "create_invoice"),
		}},
		{Name: "second", Tools: []tool.Tool{newTestTool("create_invoice", "")}},
	}))
	ctx, _ := ctxWithInvocation()
	global := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"CREATE_INVOICE"}})
	assert.Equal(t, []string{"create_invoice"}, toolNames(global.Tools))

	scoped := callSearch(t, ctx, p, toolSearchInput{
		Namespace: "first", Queries: []string{"create_invoice"}, MaxResults: 1,
	})
	assert.Equal(t, []string{"create_invoice_extra"}, toolNames(scoped.Tools))
	assert.Equal(t, []string{"CREATE_INVOICE"}, scoped.AdditionalCandidates)
}

func TestSearchByQueries_NamespaceBiasAndDescriptionEnrichment(t *testing.T) {
	p := New(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Tools: []tool.Tool{newTestTool("z_tool", "lookup")}},
		{Name: "other", Tools: []tool.Tool{newTestTool("a_tool", "lookup")}},
		{Name: "billing", Description: "invoice tools"},
		{Name: "empty", Description: "invoice tools", Tools: []tool.Tool{newTestTool("unrelated", "")}},
	}))
	ctx, _ := ctxWithInvocation()
	global := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"lookup invoice"}, MaxResults: 1})
	assert.Equal(t, []string{"z_tool"}, toolNames(global.Tools))
	assert.Equal(t, []string{"a_tool"}, global.AdditionalCandidates)

	// Namespace text biases existing hits but does not make unrelated tools
	// match merely because their namespace matches the query.
	namespaceOnly := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"billing invoice"}})
	assert.Empty(t, namespaceOnly.Tools)
}

func TestSearchByQueries_PreservesNameOrderingAndOverflow(t *testing.T) {
	p := New(nil, WithDeferredTools([]tool.Tool{
		newTestTool("z_two", "record"),
		newTestTool("bbb", "record"),
		newTestTool("aaa", "record"),
		newTestTool("a_two", "record"),
	}))
	ctx, _ := ctxWithInvocation()
	res := callSearch(t, ctx, p, toolSearchInput{Queries: []string{"record"}, MaxResults: 2})
	assert.Equal(t, []string{"aaa", "bbb"}, toolNames(res.Tools))
	assert.Equal(t, []string{"a_two", "z_two"}, res.AdditionalCandidates)
}

func TestSearchByQueries_ExactNameStillChecksPermissions(t *testing.T) {
	p := New(nil, WithDeferredTools([]tool.Tool{
		newTestTool("get_invoice", ""),
		newTestTool("get_invoice_extra", "get_invoice"),
	}))
	selected, overflow, errPayload := p.resolveSelection(searchRequest{
		queries: []string{"get_invoice"}, maxResults: 1,
	}, map[string]bool{"get_invoice_extra": true})
	assert.Empty(t, errPayload)
	assert.Equal(t, []string{"get_invoice_extra"}, selected)
	assert.Empty(t, overflow)
}

func TestSearchByQueries_MCPNamespaceDescriptionEnrichment(t *testing.T) {
	ts := &fakeToolSet{name: "billing", tools: []tool.Tool{newTestTool("read", "lookup")}}
	p := New(nil,
		WithDeferredTools([]tool.Tool{newTestTool("a_default", "lookup")}),
		WithMCPToolboxes([]MCPToolbox{{ServerName: "billing", ToolSet: ts}}),
	)
	ctx, _ := ctxWithInvocation()
	p.materializeAllMCP(ctx)
	p.mu.Lock()
	p.registerToolbox("billing", "invoice tools", nil)
	p.mu.Unlock()
	// Resolve directly so a new MCP listing cannot mask a stale cached document.
	selected, _, errPayload := p.resolveSelection(searchRequest{
		queries: []string{"lookup invoice"}, maxResults: 5,
	}, nil)
	assert.Empty(t, errPayload)
	assert.Equal(t, []string{"mcp__billing__read", "a_default"}, selected)
}

func TestSearchByQueries_ExactAndLexicalHitsAfterPermissionFiltering(t *testing.T) {
	p := New(nil, WithDeferredTools([]tool.Tool{
		newTestTool("m_target", ""),
		newTestTool("z_filler", ""),
		newTestTool("a_denied", "alpha beta"),
		newTestTool("b_alpha_beta", "alpha beta"),
	}))
	allowed := map[string]bool{"b_alpha_beta": true, "m_target": true, "z_filler": true}
	selected, overflow, errPayload := p.resolveSelection(searchRequest{
		queries:    []string{"M_TARGET", "alpha beta"},
		maxResults: 1,
	}, allowed)
	assert.Empty(t, errPayload)
	assert.Equal(t, []string{"m_target"}, selected)
	assert.Equal(t, []string{"b_alpha_beta"}, overflow)
}
