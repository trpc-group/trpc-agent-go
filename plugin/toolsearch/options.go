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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// PluginName is the default name used when registering the plugin.
	PluginName = "tool_search"

	// toolSearchToolName is the name of the function tool injected for the model.
	toolSearchToolName = "tool_search"

	// callToolToolName is the name of the function tool that invokes a deferred
	// tool loaded through tool_search. It is only injected when EnableCallTool is
	// set, in which case the model interacts with the toolset through exactly two
	// tools: tool_search (discover + load) and call_tool (invoke).
	callToolToolName = "call_tool"

	// discoveredToolsStateKey is the session-state key holding the names of
	// deferred tools loaded so far in the conversation.
	discoveredToolsStateKey = "tool_search:discovered_tools"

	// Placeholder is replaced in system instructions with the toolbox catalog
	// (or the legacy flat tool list when no toolboxes are registered).
	Placeholder = "{deferred_tools_section}"

	// defaultNamespace is the internal sentinel for tools registered via
	// WithDeferredTools. It is never exposed to the model. Registration via
	// WithToolboxes rejects empty Name so the two never collide.
	defaultNamespace = ""

	// exactNameScore is the overwhelming score given when a whole query exactly
	// equals a tool name, keeping that tool first in multi-query merge sorting.
	exactNameScore = 1000

	// defaultMaxResults is the default cap on keyword-search results.
	defaultMaxResults = 5

	// maxMaxResults is the hard ceiling on keyword-search results a caller may
	// request via max_results. Matches beyond the effective cap are returned as
	// name-only additional_candidates rather than dropped.
	maxMaxResults = 10
)

// Toolbox groups a set of semantically-related deferred tools under a single
// namespace. Keyword searches must specify a namespace to scope the result
// set, which prevents same-named tools from a different domain being returned
// by mistake when the deferred-tool catalog grows large.
type Toolbox struct {
	Name        string      // namespace name (used in the catalog and tool_search arguments)
	Description string      // one-line domain summary, surfaced to the LLM in the catalog
	Tools       []tool.Tool // deferred tools that belong to this namespace
}

// ToolPermissionFilter reports, for a batch of tool names, which ones the
// current caller may use (true = allowed). It is invoked with the context of
// the active invocation, so implementations can key on the authenticated user.
type ToolPermissionFilter func(ctx context.Context, toolNames []string) map[string]bool

// Option configures the plugin.
type Option func(*options)

// options accumulates configuration before the plugin builds its index.
type options struct {
	name                 string
	maxResults           int
	defaultTools         []tool.Tool // tools from WithDeferredTools; registered into the internal default namespace
	toolboxes            []Toolbox
	mcpToolboxes         []MCPToolbox
	permissionFilter     ToolPermissionFilter
	catalogInDescription bool
	// enableCallTool, when true, collapses the deferred toolset into exactly two
	// tools exposed to the model: tool_search (discover + load, returning each
	// match's input schema) and call_tool (invoke a loaded tool by name with
	// params). Loaded deferred tools are no longer injected as individual
	// function tools.
	enableCallTool bool
	// toolKnowledge, when set via WithToolKnowledge, switches the tool_search
	// "queries" path from keyword matching to embedding-based semantic search.
	toolKnowledge *ToolKnowledge
	// failOpen, when true, makes an embedding-search failure fall back to the
	// built-in keyword matching instead of returning an error to the model.
	failOpen bool
}

func newOptions(opts ...Option) *options {
	o := &options{
		name:       PluginName,
		maxResults: defaultMaxResults,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// WithName sets the plugin name. The name must be unique within a Runner.
func WithName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.name = name
		}
	}
}

// WithMaxTools sets the default maximum number of keyword-search results.
func WithMaxTools(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.maxResults = n
		}
	}
}

// WithDeferredTools registers a set of deferred tools that do not belong to any
// business namespace. They are collected under the internal _default namespace.
//
// It can coexist with WithToolboxes (rendered as a header-less block at the top
// of the catalog, searchable without specifying a namespace). When used alone,
// the plugin falls back to the legacy <available-deferred-tools> rendering for
// backwards compatibility.
func WithDeferredTools(tools []tool.Tool) Option {
	return func(o *options) {
		if len(tools) == 0 {
			return
		}
		o.defaultTools = append(o.defaultTools, tools...)
	}
}

// WithToolboxes registers deferred tools grouped by namespace. The keyword-
// search path requires callers to pass a namespace argument and only scores
// tools that belong to it, so registering via toolboxes is the strongest guard
// against same-named tools from different domains colliding in search results.
//
// Toolboxes are stored in registration order, which determines the order the
// catalog renders into the system prompt.
func WithToolboxes(boxes []Toolbox) Option {
	return func(o *options) {
		o.toolboxes = append(o.toolboxes, boxes...)
	}
}

// WithToolPermissionFilter sets a permission filter for deferred tools. When
// set, tools the caller may not use are dropped from the catalog, from search
// results, and are blocked at call time. Preset (non-deferred) tools are never
// permission-controlled.
func WithToolPermissionFilter(fn ToolPermissionFilter) Option {
	return func(o *options) {
		o.permissionFilter = fn
	}
}

// WithCatalogInDescription controls where the deferred-tool catalog is
// surfaced. When true, the catalog is embedded into the tool_search tool's
// description on every model turn, and NOT injected into the system prompt
// (any {deferred_tools_section} placeholder is still stripped so it never
// leaks). When false (default), the catalog is injected into the system
// prompt at the {deferred_tools_section} placeholder, matching legacy
// behavior.
//
// Use this option to keep system prompts stable and let the catalog live
// alongside the tool that consumes it, which some model backends prefer.
func WithCatalogInDescription(enabled bool) Option {
	return func(o *options) {
		o.catalogInDescription = enabled
	}
}

// WithEnableCallTool collapses the deferred toolset behind exactly two tools:
//
//   - tool_search — discover and load deferred tools. In this mode each loaded
//     tool's input schema is returned inline in the search result, since the
//     tool itself is never advertised as an individual function to the model.
//   - call_tool — invoke a previously loaded tool by its exact name, passing the
//     parameters that match the schema tool_search returned.
//
// When disabled (default), loaded deferred tools are injected as individual
// function tools and the model calls them directly by name.
//
// This mode keeps the model's advertised tool count constant (two) regardless
// of how many deferred tools are loaded, which some backends handle better than
// a growing tool list. tool_search's description is adjusted to steer the model
// toward call_tool instead of a direct call.
func WithEnableCallTool(enabled bool) Option {
	return func(o *options) {
		o.enableCallTool = enabled
	}
}

// WithToolKnowledge enables embedding-based semantic search for the tool_search
// "queries" path. When set, keyword queries the model sends to tool_search are
// ranked by vector similarity against the deferred tools' embedded
// name/description/parameters (see NewToolKnowledge), instead of the built-in
// keyword text matching. Exact tool_names loads and namespace-only listings are
// unaffected.
func WithToolKnowledge(k *ToolKnowledge) Option {
	return func(o *options) { o.toolKnowledge = k }
}

// WithFailOpen makes an embedding-search failure "fail open": instead of
// returning an error to the model, tool_search falls back to the built-in
// keyword matching so tools stay reachable. It has no effect unless
// WithToolKnowledge is also set.
func WithFailOpen() Option {
	return func(o *options) { o.failOpen = true }
}

// trimNonEmpty trims whitespace from each entry and drops the empties.
func trimNonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// toStringSet converts a string slice to a set.
func toStringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}
