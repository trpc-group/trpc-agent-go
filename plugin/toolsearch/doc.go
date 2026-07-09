//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsearch provides a Runner plugin that keeps a large set of tools
// out of the model's context until they are needed.
//
// Instead of advertising every tool in each model request, the plugin defers
// tools and exposes a single tool_search function plus a catalog rendered into
// the system prompt. The model calls tool_search to load the tools it needs;
// loaded tools become callable for the rest of the conversation and survive
// across turns via session state.
//
// # Motivation
//
// Tool schemas are expensive: each tool consumes prompt tokens on every request
// and dilutes the model's attention. When an agent has dozens or hundreds of
// tools, advertising all of them hurts both cost and accuracy. Deferring tools
// behind a search step keeps requests small while leaving the full toolset
// reachable on demand.
//
// # Concepts
//
//   - Preset tools are passed to NewPlugin and stay available as usual; they are
//     searchable but never deferred.
//   - Deferred tools are registered via WithDeferredTools or WithToolboxes. They
//     are not advertised to the model until loaded through tool_search.
//   - A Toolbox groups semantically-related deferred tools under a namespace.
//     Keyword searches are scoped to a namespace, which prevents same-named
//     tools from different domains colliding once the catalog grows large.
//   - An MCP toolbox (WithMCPToolboxes) registers a live MCP server as a
//     namespace whose tools are listed on every model request, so the catalog
//     always reflects the server's current tool set. Each listed tool is
//     renamed to mcp__<server>__<tool> so names from different servers
//     never collide.
//
// # Catalog rendering
//
// Place the placeholder {deferred_tools_section} in a system instruction. On
// each model request the plugin replaces it with a catalog of the registered
// toolboxes. MCP toolboxes are materialized (their servers contacted) before
// the catalog is rendered, so their tool names appear inline alongside static
// toolboxes. Tools registered via WithDeferredTools (no namespace) render as a
// header-less block; when they are the only registration, a legacy
// <available-deferred-tools> block is emitted.
//
// # Usage
//
//	runner := runner.NewRunner(
//	    "app",
//	    myAgent,
//	    runner.WithPlugins(toolsearch.NewPlugin(
//	        presetTools,
//	        toolsearch.WithToolboxes([]toolsearch.Toolbox{{
//	            Name:        "billing",
//	            Description: "invoices, payments and refunds",
//	            Tools:       billingTools,
//	        }}),
//	    )),
//	)
//
// The agent's system instruction should contain the {deferred_tools_section}
// placeholder so the catalog is injected for the model to browse.
//
// # Semantic (embedding) search
//
// By default the tool_search "queries" path ranks deferred tools with built-in
// keyword text matching. Passing WithToolKnowledge switches it to embedding-
// based semantic search: each deferred tool's name, description, and parameters
// are embedded into a vector store, and a keyword query is ranked by vector
// similarity instead of literal term overlap. Exact tool_names loads and
// namespace-only listings still use the deterministic index path.
//
//	toolKnowledge, err := toolsearch.NewToolKnowledge(
//	    openaiembedder.New(openaiembedder.WithModel(openaiembedder.ModelTextEmbedding3Small)),
//	    toolsearch.WithVectorStore(vectorinmemory.New()), // optional; defaults to in-memory
//	)
//	plugin := toolsearch.NewPlugin(presetTools,
//	    toolsearch.WithToolKnowledge(toolKnowledge),
//	    toolsearch.WithMaxTools(3), // cap schema-loaded results
//	    toolsearch.WithFailOpen(),  // on embedding failure, fall back to keyword search
//	    toolsearch.WithToolboxes(boxes),
//	)
//
// Embedding token usage per model turn is accumulated on the context and can be
// read back with ToolSearchUsageFromContext.
//
// # Invocation modes (native vs. indirect tool calls)
//
// Once tool_search loads a deferred tool, how the model actually invokes it is
// controlled by WithInvocationMode:
//
//   - toolsearch.NativeToolCalls (default): each loaded deferred tool is
//     advertised to the model as its own function tool and the model calls it
//     directly by name using the backend's native function-calling protocol.
//
//   - toolsearch.DispatchToolCalls: the deferred toolset is collapsed into
//     exactly two function tools:
//
//   - tool_search — discover and load deferred tools. In this mode each
//     result also carries the tool's input_schema, since the tool is never
//     advertised as an individual function.
//
//   - call_tool — invoke a loaded tool by its exact name with a params
//     object matching that schema.
//
// DispatchToolCalls keeps the advertised tool count constant (two) no matter
// how many deferred tools are loaded, which some backends handle better than a
// growing tool list. call_tool enforces the same permission and loaded-set
// guards as a direct deferred-tool call.
//
//	plugin := toolsearch.NewPlugin(presetTools,
//	    toolsearch.WithToolboxes(boxes),
//	    toolsearch.WithInvocationMode(toolsearch.DispatchToolCalls),
//	)
package toolsearch
