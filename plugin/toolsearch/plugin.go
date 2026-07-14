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
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const toolSearchDescription = `Load deferred tools from <toolbox-catalog> so they become callable. After loading, call the tool directly — never search for it again.

- Known name → use "tool_names" (exact, cross-namespace).
- Unknown name → use "queries" plus "namespace" of the matching catalog domain; leave "namespace" empty only when no domain fits.`

// toolSearchCallToolDescription replaces toolSearchDescription when the
// invocation mode is DispatchToolCalls: loaded tools are invoked through
// call_tool rather than as individual function tools, and each search result
// carries the tool's input schema for building the call_tool "params".
const toolSearchCallToolDescription = `Load deferred tools from <toolbox-catalog>, then invoke them with call_tool — never search for the same tool again. Each result includes the tool's input_schema; use it to build call_tool "params".

- Known name → use "tool_names" (exact, cross-namespace).
- Unknown name → use "queries" plus "namespace" of the matching catalog domain; leave "namespace" empty only when no domain fits.`

// callToolDescription documents the call_tool function tool.
const callToolDescription = `Invoke a deferred tool that tool_search has already loaded. Pass its exact "tool_name" (as returned by tool_search) and a "params" object matching that tool's input_schema. If the tool is not loaded yet, call tool_search first.`

const unloadedToolErrorTemplate = `Tool %[1]q is not loaded yet. Call tool_search with tool_names=["%[1]s"] first, then retry this call.`

// discoveredLockShards bounds the sharded-mutex table used to serialize
// per-session appendDiscoveredTools. 128 keeps memory O(1) with negligible
// cross-session collision cost (each session still writes its own state key).
const discoveredLockShards = 128

// Plugin is a tool-search engine that defers tools behind a tool_search function
// and a system-prompt catalog, loading them on demand.
type Plugin struct {
	name string
	// mu guards the index maps; MCP toolboxes mutate them at runtime when a
	// server is listed lazily, and a single Plugin is shared across goroutines.
	mu               sync.RWMutex
	toolsByName      map[string]tool.Tool
	metaByName       map[string]*toolMetadata
	nameByLower      map[string]string
	deferredNames    map[string]struct{}
	toolboxes        []*toolboxIndex
	toolboxByName    map[string]*toolboxIndex
	namespaceByTool  map[string]string
	searchTool       tool.Tool
	callTool         tool.Tool
	maxResults       int
	permissionFilter ToolPermissionFilter
	// catalogInDescription, when true, embeds the deferred-tool catalog into
	// the tool_search tool's description each turn instead of injecting it
	// into the system prompt via {deferred_tools_section}.
	catalogInDescription bool
	// invocationMode selects how loaded deferred tools are invoked. Under
	// DispatchToolCalls the deferred toolset is collapsed behind two tools:
	// tool_search (discover + load, returning each match's input schema) and
	// call_tool (invoke a loaded tool by name). Loaded deferred tools are then
	// NOT injected as individual function tools. Under NativeToolCalls (the
	// default) each loaded deferred tool is advertised individually.
	invocationMode InvocationMode

	// semanticIndex, when non-nil, backs the tool_search "queries" path with
	// embedding-based semantic search over the deferred tools instead of the
	// built-in keyword text matching. It is configured via WithSemanticToolIndex.
	semanticIndex *SemanticToolIndex
	// embeddingFailOpen, when true, makes an embedding-search failure fall back
	// to the built-in keyword matching instead of surfacing the error.
	embeddingFailOpen bool

	// discoveredLocks serializes appendDiscoveredTools per session via a fixed
	// sharded mutex table (session key hashed into a shard), so concurrent
	// tool_search calls on cloned invocations sharing the same session don't
	// clobber each other and memory stays O(1) regardless of session churn.
	discoveredLocks [discoveredLockShards]sync.Mutex
}

// toolboxIndex holds catalog metadata and an O(1) membership set for a namespace.
// An MCP toolbox additionally carries an mcpSource for on-demand listing.
type toolboxIndex struct {
	name        string
	description string
	toolNames   map[string]struct{}
	mcp         *mcpSource
}

// compile-time check.
var _ plugin.Plugin = (*Plugin)(nil)

// New creates a tool-search plugin.
//
// Preset tools are always visible to the model (advertised via
// llmagent.WithTools) and resolvable by exact tool_names, but are excluded
// from keyword and embedding search candidate sets.
// Deferred tools are registered via WithDeferredTools or WithToolboxes and
// are the sole population for query and embedding search.
func New(presetTools []tool.Tool, opts ...Option) *Plugin {
	o := newOptions(opts...)
	p := &Plugin{
		name:                 o.name,
		toolsByName:          make(map[string]tool.Tool),
		metaByName:           make(map[string]*toolMetadata),
		nameByLower:          make(map[string]string),
		deferredNames:        make(map[string]struct{}),
		toolboxByName:        make(map[string]*toolboxIndex),
		namespaceByTool:      make(map[string]string),
		maxResults:           o.maxResults,
		permissionFilter:     o.permissionFilter,
		catalogInDescription: o.catalogInDescription,
		invocationMode:       o.invocationMode,
		semanticIndex:        o.semanticIndex,
		embeddingFailOpen:    o.embeddingFailOpen,
	}

	for _, t := range presetTools {
		// Skip a nil tool or one with a nil declaration: indexTool dereferences
		// the declaration, and a tool that cannot describe itself is unusable.
		if t == nil || t.Declaration() == nil {
			continue
		}
		p.indexTool(t)
	}
	// WithDeferredTools bypasses the empty-name guard below: the internal
	// default namespace deliberately uses "" as its sentinel, but user-supplied
	// WithToolboxes entries must not.
	if len(o.defaultTools) > 0 {
		p.registerToolbox(defaultNamespace, "", o.defaultTools)
	}
	for _, box := range o.toolboxes {
		name := strings.TrimSpace(box.Name)
		if name == "" {
			log.Errorf("[%s] skipping toolbox with empty name (description=%q tools=%d)",
				p.name, box.Description, len(box.Tools))
			continue
		}
		p.registerToolbox(name, box.Description, box.Tools)
	}
	for _, box := range o.mcpToolboxes {
		p.registerMCPToolbox(box)
	}

	p.searchTool = p.createSearchTool()
	if p.invocationMode == DispatchToolCalls {
		p.callTool = p.createCallTool()
	}
	log.Infof("[%s] registered %d deferred tools across %d toolboxes",
		p.name, len(p.deferredNames), len(p.toolboxes))
	return p
}

// Name implements plugin.Plugin.
func (p *Plugin) Name() string { return p.name }

// Register implements plugin.Plugin.
func (p *Plugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeModel(p.beforeModel)
	r.BeforeTool(p.beforeTool)
}

// indexTool registers a tool into the name-keyed maps (toolsByName,
// metaByName, nameByLower) and pre-computes its search metadata. It does not
// touch deferredNames or namespaceByTool, so preset tools indexed through
// this method alone stay out of the query and embedding candidate sets.
func (p *Plugin) indexTool(t tool.Tool) {
	decl := t.Declaration()
	name := decl.Name
	parsed := parseToolName(name)
	p.toolsByName[name] = t
	p.metaByName[name] = &toolMetadata{
		Parts:       parsed.Parts,
		Full:        parsed.Full,
		Description: decl.Description,
		descLower:   strings.ToLower(decl.Description),
		nameLower:   strings.ToLower(name),
	}
	p.nameByLower[strings.ToLower(name)] = name
}

// unindexTool is the inverse of indexTool: it removes a tool from every
// name-keyed catalog map so a subsequent listing/search no longer sees it.
// It is used when an MCP server drops a tool between two listings so the
// stale entry stops being surfaced or callable. The caller must hold p.mu
// (write).
func (p *Plugin) unindexTool(name string) {
	delete(p.toolsByName, name)
	delete(p.metaByName, name)
	delete(p.nameByLower, strings.ToLower(name))
	delete(p.deferredNames, name)
	delete(p.namespaceByTool, name)
}

// registerToolbox is the shared registration path for both WithToolboxes and
// WithDeferredTools. It indexes each tool, marks it deferred, and records its
// namespace membership exactly once. A tool registered into two namespaces logs
// an error and keeps the first owner so misconfiguration never breaks traffic.
func (p *Plugin) registerToolbox(name, description string, tools []tool.Tool) {
	box, ok := p.toolboxByName[name]
	if !ok {
		box = &toolboxIndex{
			name:        name,
			description: description,
			toolNames:   make(map[string]struct{}, len(tools)),
		}
		p.toolboxByName[name] = box
		p.toolboxes = append(p.toolboxes, box)
	} else if description != "" && box.description == "" {
		// Re-registering into the same namespace: enrich an empty description.
		box.description = description
	}

	for _, t := range tools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		toolName := t.Declaration().Name
		if existingNS, dup := p.namespaceByTool[toolName]; dup {
			if existingNS != name {
				log.Errorf("[%s] tool %q registered into multiple namespaces (%q kept, %q ignored)",
					p.name, toolName, existingNS, name)
			}
			continue
		}
		// A tool already indexed as a preset (present in toolsByName but with no
		// namespace) must stay a preset: always visible and never deferred. Pulling
		// it into the deferred population here would hide it behind tool_search,
		// contradicting the preset contract, so keep it as a preset and skip.
		if _, isPreset := p.toolsByName[toolName]; isPreset {
			log.Errorf("[%s] tool %q is a preset tool; ignoring its toolbox registration in %q",
				p.name, toolName, name)
			continue
		}
		p.indexTool(t)
		p.deferredNames[toolName] = struct{}{}
		p.namespaceByTool[toolName] = name
		box.toolNames[toolName] = struct{}{}
	}
}

// createSearchTool creates the tool_search function tool.
func (p *Plugin) createSearchTool() tool.Tool {
	return function.NewFunctionTool(
		p.searchTools,
		function.WithName(toolSearchToolName),
		function.WithDescription(p.baseSearchDescription()),
	)
}

// baseSearchDescription returns the tool_search description matching the
// current invocation mode: the call_tool-oriented variant when the mode is
// DispatchToolCalls, otherwise the direct-call variant.
func (p *Plugin) baseSearchDescription() string {
	if p.invocationMode == DispatchToolCalls {
		return toolSearchCallToolDescription
	}
	return toolSearchDescription
}

// createCallTool creates the call_tool function tool used to invoke deferred
// tools loaded through tool_search. It is only injected when the invocation
// mode is DispatchToolCalls.
func (p *Plugin) createCallTool() tool.Tool {
	return function.NewFunctionTool(
		p.callToolFn,
		function.WithName(callToolToolName),
		function.WithDescription(callToolDescription),
		function.WithInputSchema(&tool.Schema{
			Type:     "object",
			Required: []string{"tool_name", "params"},
			Properties: map[string]*tool.Schema{
				"tool_name": {
					Type:        "string",
					Description: "The exact name of the deferred tool to execute (as returned by tool_search).",
				},
				"params": {
					Type:                 "object",
					Description:          "Parameters for the target tool. Must match the tool's input schema (as returned by tool_search).",
					AdditionalProperties: true,
				},
			},
		}),
		// The return type is any (the underlying tool's result is passed through),
		// so an explicit output schema is required; otherwise the framework panics
		// generating a schema from the nil reflect type.
		function.WithOutputSchema(&tool.Schema{Type: "object", Description: "Result returned by the invoked tool."}),
	)
}

// callToolInput is the call_tool input.
type callToolInput struct {
	ToolName string         `json:"tool_name"`
	Params   map[string]any `json:"params"`
}

// callToolFn implements call_tool: it resolves tool_name to a loaded, permitted
// deferred tool and forwards params to its CallableTool implementation. It
// mirrors the guardrails beforeTool applies to direct deferred-tool calls
// (permission filtering + loaded-set check) so call_tool cannot be used to reach
// tools the model has not searched for or is not allowed to use.
func (p *Plugin) callToolFn(ctx context.Context, input callToolInput) (any, error) {
	name := strings.TrimSpace(input.ToolName)
	if name == "" {
		return map[string]any{"status": "error", "message": "tool_name is required."}, nil
	}

	// An MCP tool is indexed only after its server is listed; materialize it so a
	// renamed mcp__server__tool name resolves before the deferred/loaded checks.
	if server, _, ok := parseMCPName(name); ok {
		p.materializeNamespace(ctx, server)
	}

	// Resolve to the canonical (indexed) name. Preset tools are callable too, but
	// call_tool is intended for deferred tools; a non-deferred hit still works.
	p.mu.RLock()
	canonical, known := p.nameByLower[strings.ToLower(name)]
	var target tool.Tool
	if known {
		target = p.toolsByName[canonical]
	}
	_, isDeferred := p.deferredNames[canonical]
	p.mu.RUnlock()
	if !known || target == nil {
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Unknown tool %q. Use tool_search to find the correct tool name.", name),
		}, nil
	}

	// Permission check precedes the loaded check, matching beforeTool: never
	// reveal a "load this tool" hint for a tool the caller may not use.
	if isDeferred && p.permissionFilter != nil {
		if allowed := p.permissionFilter(ctx, []string{canonical}); !allowed[canonical] {
			log.WarnfContext(ctx, "[%s] call_tool blocked due to permission: %s", p.name, canonical)
			return map[string]any{
				"status":  "error",
				"message": fmt.Sprintf("You do not have permission to use the %q tool.", canonical),
			}, nil
		}
	}

	// Deferred tools must have been loaded via tool_search first.
	if isDeferred {
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return map[string]any{
				"status":  "error",
				"message": fmt.Sprintf(unloadedToolErrorTemplate, canonical),
			}, nil
		}
		loaded := false
		for _, n := range p.loadDiscoveredTools(ctx, inv) {
			if n == canonical {
				loaded = true
				break
			}
		}
		if !loaded {
			log.WarnfContext(ctx, "[%s] call_tool on unloaded deferred tool: %s", p.name, canonical)
			return map[string]any{
				"status":  "error",
				"message": fmt.Sprintf(unloadedToolErrorTemplate, canonical),
			}, nil
		}
	}

	callable, ok := target.(tool.CallableTool)
	if !ok {
		// Fall back to StreamableTool: it is an independent framework interface
		// (does not embed CallableTool), so a tool that only streams is still a
		// valid deferred tool. Aggregate the stream into a single envelope so
		// call_tool — which speaks the CallableTool contract — can return a
		// one-shot value. Order is preserved via the "chunks" slice.
		if streamable, isStream := target.(tool.StreamableTool); isStream {
			params := input.Params
			if params == nil {
				params = map[string]any{}
			}
			rawArgs, err := json.Marshal(params)
			if err != nil {
				return map[string]any{
					"status":  "error",
					"message": fmt.Sprintf("Failed to encode params: %v", err),
				}, nil
			}
			log.InfofContext(ctx, "[%s] call_tool aggregating stream from %s", p.name, canonical)
			result, err := aggregateStream(ctx, streamable, rawArgs)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Tool %q is not callable.", canonical),
		}, nil
	}

	params := input.Params
	if params == nil {
		params = map[string]any{}
	}
	rawArgs, err := json.Marshal(params)
	if err != nil {
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Failed to encode params: %v", err),
		}, nil
	}

	log.InfofContext(ctx, "[%s] call_tool invoking %s", p.name, canonical)
	return callable.Call(ctx, rawArgs)
}

// isDefaultOnly reports whether the only registered toolbox is the legacy
// _default namespace. Legacy callers (WithDeferredTools alone) keep the old
// "no namespace required" UX, so namespace validation is skipped. The caller
// must hold p.mu (read).
func (p *Plugin) isDefaultOnly() bool {
	return len(p.toolboxes) == 1 && p.toolboxes[0].name == defaultNamespace
}

// allDeferredPermissions returns permission results for all deferred tools in
// one batch call. It returns nil when no filter is configured (all allowed).
// The deferred-name snapshot is taken under the read lock; the (external)
// filter runs outside the lock so user code never blocks a concurrent listing.
func (p *Plugin) allDeferredPermissions(ctx context.Context) map[string]bool {
	if p.permissionFilter == nil {
		return nil
	}
	p.mu.RLock()
	names := make([]string, 0, len(p.deferredNames))
	for name := range p.deferredNames {
		names = append(names, name)
	}
	p.mu.RUnlock()
	return p.permissionFilter(ctx, names)
}

// filterAllowed drops deferred tools the caller is not permitted to use. Preset
// (non-deferred) tools are never permission-controlled. A nil allowed map means
// no filter is configured, so names is returned unchanged.
func (p *Plugin) filterAllowed(names []string, allowed map[string]bool) []string {
	if allowed == nil {
		return names
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, isDeferred := p.deferredNames[name]; !isDeferred || allowed[name] {
			out = append(out, name)
		}
	}
	return out
}

// isDeferred reports whether a tool name is a (currently indexed) deferred tool.
func (p *Plugin) isDeferred(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.deferredNames[name]
	return ok
}

// beforeModel injects the tool_search tool, the loaded deferred tools' schemas,
// and the rendered catalog before each model call.
func (p *Plugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}

	if args.Request.Tools == nil {
		args.Request.Tools = make(map[string]tool.Tool)
	}

	// Materialize all MCP servers once per turn before schema injection and
	// catalog rendering, so both observe one consistent snapshot.
	p.materializeAllMCP(ctx)

	// Inject schemas for the deferred tools loaded so far. In call_tool mode the
	// model invokes loaded tools through call_tool instead of as individual
	// function tools, so we skip this injection.
	discoveredTools := p.loadDiscoveredTools(ctx, inv)

	// Fetch all deferred-tool permissions once; reused for discovered filtering
	// and catalog rendering.
	allDeferredAllowed := p.allDeferredPermissions(ctx)
	discoveredTools = p.filterAllowed(discoveredTools, allDeferredAllowed)

	if p.invocationMode != DispatchToolCalls {
		p.mu.RLock()
		for _, toolName := range discoveredTools {
			// Inject only tools still present in the current snapshot.
			if _, alreadySet := args.Request.Tools[toolName]; alreadySet {
				continue
			}
			if t, exists := p.toolsByName[toolName]; exists {
				args.Request.Tools[toolName] = t
			}
		}
		p.mu.RUnlock()
	}

	// Always inject the tool_search function tool. When catalogInDescription
	// is enabled, wrap it with a per-turn description that carries the
	// (permission-filtered) toolbox catalog so the model sees the catalog
	// alongside the tool that consumes it.
	searchName := p.searchTool.Declaration().Name
	if _, exists := args.Request.Tools[searchName]; !exists {
		if p.catalogInDescription {
			args.Request.Tools[searchName] = p.searchToolWithCatalog(allDeferredAllowed)
		} else {
			args.Request.Tools[searchName] = p.searchTool
		}
	}

	// In DispatchToolCalls mode, also inject the call_tool function so the
	// model can invoke any loaded deferred tool through it.
	if p.invocationMode == DispatchToolCalls && p.callTool != nil {
		callName := p.callTool.Declaration().Name
		if _, exists := args.Request.Tools[callName]; !exists {
			args.Request.Tools[callName] = p.callTool
		}
	}

	// Always invoke: even when no toolbox is registered (or the catalog is
	// empty after permission filtering), we still need to strip any literal
	// {deferred_tools_section} placeholder from user-supplied system prompts,
	// otherwise the raw placeholder would leak to the model. When the catalog
	// lives in the tool description, the placeholder is stripped without
	// injecting any prompt content.
	p.replaceDeferredToolsPlaceholder(args, allDeferredAllowed)

	// When embedding search is enabled, seed a usage accumulator into the
	// context and hand it downstream. tool_search calls this turn fold their
	// embedding token usage into it, readable via ToolSearchUsageFromContext.
	if p.semanticIndex != nil {
		return &model.BeforeModelResult{Context: withUsageAccumulator(ctx)}, nil
	}
	return nil, nil
}

// beforeTool intercepts calls to deferred tools not yet loaded via tool_search.
func (p *Plugin) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil || args.ToolName == "" {
		return nil, nil
	}
	toolName := args.ToolName

	// An MCP tool is only indexed after its server is listed. The model may call
	// a renamed MCP tool (mcp__server__tool) before any search touched the
	// server, so materialize the owning server first; otherwise it would not be
	// recognized as deferred and the unloaded-tool guard would be skipped.
	// After materializing, also block stale MCP names — those that belong to a
	// registered MCP namespace but are absent from the current snapshot (server
	// pruned them on the latest listing).
	if server, _, ok := parseMCPName(toolName); ok {
		p.materializeNamespace(ctx, server)

		p.mu.RLock()
		box, known := p.toolboxByName[server]
		_, indexed := p.deferredNames[toolName]
		p.mu.RUnlock()

		if known && box.isMCP() && !indexed {
			log.WarnfContext(ctx, "[%s] blocked stale MCP tool call: %s", p.name, toolName)
			return &tool.BeforeToolResult{
				CustomResult: fmt.Sprintf(unloadedToolErrorTemplate, toolName),
			}, nil
		}
	}

	// Only intercept deferred tools.
	if !p.isDeferred(toolName) {
		return nil, nil
	}

	// Permission check precedes the discovery check: do not reveal a "load this
	// tool" hint for a tool the caller may not use, so the model cannot probe
	// for out-of-permission tools via tool_search and then call them directly.
	if p.permissionFilter != nil {
		if allowed := p.permissionFilter(ctx, []string{toolName}); !allowed[toolName] {
			log.WarnfContext(ctx, "[%s] blocked tool call due to permission: %s", p.name, toolName)
			return &tool.BeforeToolResult{
				CustomResult: fmt.Sprintf("You do not have permission to use the %q tool.", toolName),
			}, nil
		}
	}

	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil // cannot check state — let it through
	}
	for _, name := range p.loadDiscoveredTools(ctx, inv) {
		if name == toolName {
			return nil, nil // tool is loaded
		}
	}

	// Deferred but not loaded — return guidance. tool_names loads across
	// namespaces, so the message need not expose the internal namespace.
	log.WarnfContext(ctx, "[%s] blocked unloaded deferred tool call: %s", p.name, toolName)
	return &tool.BeforeToolResult{
		CustomResult: fmt.Sprintf(unloadedToolErrorTemplate, toolName),
	}, nil
}

// replaceDeferredToolsPlaceholder injects the rendered catalog into the request.
//
// Injection order:
//  1. Replace {deferred_tools_section} in a system message when present.
//  2. Otherwise, append to the FIRST system message (a second system message
//     would be misread as a user turn on backends without a system role).
//  3. Otherwise, prepend a new system message.
//
// When catalogInDescription is enabled, the catalog lives in the tool_search
// tool's description instead. In that mode we never append or prepend to the
// prompt; we only strip a literal {deferred_tools_section} placeholder so it
// does not leak to the model.
func (p *Plugin) replaceDeferredToolsPlaceholder(
	args *model.BeforeModelArgs,
	allDeferredAllowed map[string]bool,
) {
	var replacement string
	if !p.catalogInDescription {
		replacement = p.renderToolboxCatalog(allDeferredAllowed)
	}

	// First pass: locate a system message carrying the placeholder. Even when
	// the rendered catalog is empty (no toolbox / everything permission-filtered)
	// we still substitute so the literal {deferred_tools_section} token never
	// leaks to the model.
	firstSystem := -1
	for i, msg := range args.Request.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		if firstSystem < 0 {
			firstSystem = i
		}
		if strings.Contains(msg.Content, Placeholder) {
			args.Request.Messages[i].Content = strings.Replace(
				msg.Content, Placeholder, replacement, 1,
			)
			return
		}
	}

	// No placeholder found. Skip append/prepend when there is nothing to inject.
	if replacement == "" {
		return
	}

	if firstSystem >= 0 {
		args.Request.Messages[firstSystem].Content += "\n\n" + replacement
		return
	}

	args.Request.Messages = append(
		[]model.Message{model.NewSystemMessage(replacement)},
		args.Request.Messages...,
	)
}

// searchToolWithCatalog returns a per-turn wrapper around p.searchTool whose
// Declaration() reports a description that concatenates the base tool_search
// guidance with the currently-visible toolbox catalog. Call() delegates to the
// underlying tool so the search implementation is unchanged.
func (p *Plugin) searchToolWithCatalog(allDeferredAllowed map[string]bool) tool.Tool {
	catalog := p.renderToolboxCatalog(allDeferredAllowed)
	desc := p.baseSearchDescription()
	if catalog != "" {
		desc = desc + "\n\n" + catalog
	}
	return &searchToolWithDynamicDesc{base: p.searchTool, description: desc}
}

// searchToolWithDynamicDesc wraps the tool_search CallableTool but overrides
// its declared description with a value computed at BeforeModel time. It only
// forwards the CallableTool contract; tool_search does not stream.
type searchToolWithDynamicDesc struct {
	base        tool.Tool
	description string
}

// Declaration returns the base declaration with the description replaced.
func (t *searchToolWithDynamicDesc) Declaration() *tool.Declaration {
	baseDecl := t.base.Declaration()
	if baseDecl == nil {
		return &tool.Declaration{Name: toolSearchToolName, Description: t.description}
	}
	decl := *baseDecl
	decl.Description = t.description
	return &decl
}

// Call forwards the invocation to the underlying tool_search implementation.
func (t *searchToolWithDynamicDesc) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if callable, ok := t.base.(tool.CallableTool); ok {
		return callable.Call(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool_search base tool does not implement CallableTool")
}

// renderToolboxCatalog builds the catalog snippet injected at the placeholder.
// MCP toolboxes are materialized before this call so their tool names appear.
// Permission-filtered tools are dropped; empty toolboxes collapse silently.
func (p *Plugin) renderToolboxCatalog(allDeferredAllowed map[string]bool) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.toolboxes) == 0 {
		return ""
	}

	type rendered struct {
		name        string
		description string
		tools       []string
	}
	var defaultTools []string
	rows := make([]rendered, 0, len(p.toolboxes))
	for _, box := range p.toolboxes {
		desc := strings.TrimSpace(box.description)
		visible := make([]string, 0, len(box.toolNames))
		for name := range box.toolNames {
			if allDeferredAllowed != nil && !allDeferredAllowed[name] {
				continue
			}
			visible = append(visible, name)
		}
		if len(visible) == 0 {
			continue
		}
		sort.Strings(visible)
		if box.name == defaultNamespace {
			defaultTools = visible
			continue
		}
		rows = append(rows, rendered{name: box.name, description: desc, tools: visible})
	}
	if len(rows) == 0 && len(defaultTools) == 0 {
		return ""
	}

	var sb strings.Builder
	// Legacy compatibility: only the _default namespace (no business toolbox)
	// falls back to the flat <available-deferred-tools> block.
	if len(rows) == 0 && len(defaultTools) > 0 {
		sb.WriteString("<available-deferred-tools>\n")
		sb.WriteString(strings.Join(defaultTools, "\n"))
		sb.WriteString("\n</available-deferred-tools>")
		return sb.String()
	}

	sb.WriteString("<toolbox-catalog>\n")
	// Keep catalog invocation guidance consistent with the active mode.
	invocationHint := "call it directly."
	if p.invocationMode == DispatchToolCalls {
		invocationHint = "invoke it with `call_tool` (pass tool_name + params matching the input_schema returned by tool_search)."
	}
	sb.WriteString(
		"Deferred tools — NOT yet callable. Load one via `tool_search` first, then " + invocationHint + " " +
			"Format: `- <namespace> (<domain>) — <tools>`; \"(no namespace)\" tools need no namespace argument.")
	sb.WriteString("\n\n")
	if len(defaultTools) > 0 {
		// _default renders header-less at the top, signalling these tools can be
		// searched without specifying a namespace.
		sb.WriteString("- (no namespace) — ")
		sb.WriteString(strings.Join(defaultTools, ", "))
		sb.WriteString("\n")
	}
	for _, r := range rows {
		sb.WriteString("- ")
		sb.WriteString(r.name)
		if r.description != "" {
			sb.WriteString(" (")
			sb.WriteString(r.description)
			sb.WriteString(")")
		}
		sb.WriteString(" — ")
		sb.WriteString(strings.Join(r.tools, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("</toolbox-catalog>")
	return sb.String()
}

// --- Session state management ---

// loadDiscoveredTools reads the loaded deferred-tool names from session state.
// Returns nil on missing data or parse failure. Read goes through
// Session.GetState, so it is safe against concurrent SetState writers.
func (p *Plugin) loadDiscoveredTools(ctx context.Context, inv *agent.Invocation) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}
	raw, ok := inv.Session.GetState(discoveredToolsStateKey)
	if !ok || len(raw) == 0 {
		return nil
	}
	var toolNames []string
	if err := json.Unmarshal(raw, &toolNames); err != nil {
		log.WarnfContext(ctx, "[%s] failed to unmarshal discovered tools: %v", p.name, err)
		return nil
	}
	return toolNames
}

// discoveredToolsLock returns a sharded mutex for the invocation's session,
// selected by FNV-1a hashing the session key into discoveredLocks.
func (p *Plugin) discoveredToolsLock(inv *agent.Invocation) *sync.Mutex {
	if inv == nil || inv.Session == nil {
		return nil
	}
	h := fnv.New32a()
	// NUL separator avoids collisions from concatenated key fields.
	_, _ = h.Write([]byte(inv.Session.AppName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(inv.Session.UserID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(inv.Session.ID))
	return &p.discoveredLocks[h.Sum32()%discoveredLockShards]
}

// saveDiscoveredTools writes the loaded deferred-tool names back into session
// state and persists them through the SessionService. The in-memory write
// uses Session.SetState, which is safe under concurrent access.
func (p *Plugin) saveDiscoveredTools(ctx context.Context, inv *agent.Invocation, tools []string) {
	if inv == nil || inv.Session == nil {
		return
	}
	data, err := json.Marshal(tools)
	if err != nil {
		log.ErrorfContext(ctx, "[%s] failed to marshal discovered tools: %v", p.name, err)
		return
	}

	// Write into the in-memory state so later callbacks this turn see it.
	// SetState locks the session's stateMu and copies the payload internally.
	inv.Session.SetState(discoveredToolsStateKey, data)

	// Persist explicitly so the next turn does not lose the loaded set.
	if inv.SessionService == nil {
		log.WarnfContext(ctx, "[%s] SessionService is nil, discovered tools only in memory", p.name)
		return
	}
	key := session.Key{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
	if err := inv.SessionService.UpdateSessionState(ctx, key, session.StateMap{
		discoveredToolsStateKey: data,
	}); err != nil {
		log.WarnfContext(ctx, "[%s] UpdateSessionState failed: %v", p.name, err)
	}
}

// appendDiscoveredTools merges newTools into the loaded set and persists it.
// The load/merge/save cycle is serialized per session so concurrent
// tool_search calls (LLMAgent parallel tool calls share the underlying
// *session.Session via Invocation.Clone) do not overwrite each other.
func (p *Plugin) appendDiscoveredTools(ctx context.Context, inv *agent.Invocation, newTools []string) {
	if mu := p.discoveredToolsLock(inv); mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	toolSet := toStringSet(p.loadDiscoveredTools(ctx, inv))
	for _, t := range newTools {
		toolSet[t] = struct{}{}
	}
	merged := make([]string, 0, len(toolSet))
	for t := range toolSet {
		merged = append(merged, t)
	}
	sort.Strings(merged)
	p.saveDiscoveredTools(ctx, inv, merged)
}
