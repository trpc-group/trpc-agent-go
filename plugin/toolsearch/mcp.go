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
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mcpNamePrefix is the leading marker for renamed MCP tools. The full layout is
// mcp__<server>__<tool>, matching the Claude Code naming convention so the same
// names round-trip through transcripts and tool catalogs.
const mcpNamePrefix = "mcp__"

// MCPToolbox registers one MCP server as a deferred namespace.
//
// Unlike a static Toolbox, its tools are not known at construction time: the
// ToolSet is listed on every model request (via beforeModel) to reflect the
// server's current tool set in the catalog. Every listed tool is renamed to
// mcp__<ServerName>__<tool> so names from different servers never collide.
type MCPToolbox struct {
	// ServerName is the MCP server name. It doubles as the search namespace and
	// the mcp__<ServerName>__ prefix applied to every tool the server exposes.
	ServerName string
	// Description is a one-line domain summary surfaced to the LLM in the catalog.
	Description string
	// ToolSet is the live MCP tool set. Its Tools(ctx) method is invoked on
	// every model request so the catalog always reflects real-time tools.
	ToolSet tool.ToolSet
}

// mcpSource holds the real-time listing state for an MCP-backed toolbox.
type mcpSource struct {
	serverName string
	toolSet    tool.ToolSet
}

// WithMCPToolboxes registers one or more MCP servers as deferred namespaces.
// The servers are listed on every model request so the catalog always reflects
// the server's current tools.
func WithMCPToolboxes(boxes []MCPToolbox) Option {
	return func(o *options) {
		o.mcpToolboxes = append(o.mcpToolboxes, boxes...)
	}
}

// isMCP reports whether the toolbox is backed by an MCP server.
func (b *toolboxIndex) isMCP() bool { return b != nil && b.mcp != nil }

// registerMCPToolbox records an MCP server as a namespace. Its tools are
// listed on every model request via materializeAllMCP so the catalog reflects
// the server's current tool set.
func (p *Plugin) registerMCPToolbox(box MCPToolbox) {
	name := strings.TrimSpace(box.ServerName)
	if name == "" {
		log.Errorf("[%s] skipping MCP toolbox with empty server name (description=%q)",
			p.name, box.Description)
		return
	}
	if box.ToolSet == nil {
		log.Errorf("[%s] skipping MCP toolbox %q with nil ToolSet", p.name, name)
		return
	}
	if _, exists := p.toolboxByName[name]; exists {
		log.Errorf("[%s] MCP toolbox %q collides with an existing namespace; skipped", p.name, name)
		return
	}
	idx := &toolboxIndex{
		name:        name,
		description: box.Description,
		toolNames:   make(map[string]struct{}),
		mcp:         &mcpSource{serverName: name, toolSet: box.ToolSet},
	}
	p.toolboxByName[name] = idx
	p.toolboxes = append(p.toolboxes, idx)
}

// ensureMCPListed lists an MCP toolbox's ToolSet and indexes its renamed tools.
// It is a no-op for non-MCP toolboxes. Each call fetches Tools(ctx) so the
// catalog always reflects the server's current tools.
//
// Tools(ctx) is invoked without holding the lock — it may perform network I/O —
// and the result is committed under the write lock.
func (p *Plugin) ensureMCPListed(ctx context.Context, box *toolboxIndex) {
	if !box.isMCP() {
		return
	}

	tools := box.mcp.toolSet.Tools(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(tools) == 0 {
		return
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		renamed := newRenamedTool(t, box.mcp.serverName)
		name := renamed.Declaration().Name
		if existingNS, dup := p.namespaceByTool[name]; dup {
			if existingNS != box.name {
				log.Errorf("[%s] MCP tool %q collides with namespace %q; kept %q",
					p.name, name, box.name, existingNS)
			}
			continue
		}
		p.indexTool(renamed)
		p.deferredNames[name] = struct{}{}
		p.namespaceByTool[name] = box.name
		box.toolNames[name] = struct{}{}
	}
	log.Infof("[%s] listed MCP server %q: %d tools", p.name, box.mcp.serverName, len(box.toolNames))
}

// materializeNamespace lists the MCP server backing a namespace, if any, so its
// tools are indexed before a namespace-scoped search or listing runs. A blank or
// non-MCP namespace is a no-op.
func (p *Plugin) materializeNamespace(ctx context.Context, namespace string) {
	if namespace == "" {
		return
	}
	p.mu.RLock()
	box := p.toolboxByName[namespace]
	p.mu.RUnlock()
	if box.isMCP() {
		p.ensureMCPListed(ctx, box)
	}
}

// materializeByToolNames lists the MCP servers that own any of the given
// mcp__server__tool names, so an exact-name load or a deferred-tool check sees
// the freshly-indexed tools. Names without the MCP prefix, or whose server is
// not a registered MCP namespace, are skipped.
func (p *Plugin) materializeByToolNames(ctx context.Context, names []string) {
	seen := make(map[string]struct{})
	for _, name := range names {
		server, _, ok := parseMCPName(strings.TrimSpace(name))
		if !ok {
			continue
		}
		if _, dup := seen[server]; dup {
			continue
		}
		seen[server] = struct{}{}
		p.materializeNamespace(ctx, server)
	}
}

// materializeAllMCP lists every MCP toolbox. It is called before catalog
// rendering so all MCP tools appear in the catalog on every model request.
func (p *Plugin) materializeAllMCP(ctx context.Context) {
	p.mu.RLock()
	boxes := make([]*toolboxIndex, 0, len(p.toolboxes))
	for _, box := range p.toolboxes {
		if box.isMCP() {
			boxes = append(boxes, box)
		}
	}
	p.mu.RUnlock()
	for _, box := range boxes {
		p.ensureMCPListed(ctx, box)
	}
}

// parseMCPName splits a renamed MCP tool name (mcp__server__tool) into its
// server and tool parts. ok is false for names that are not MCP-prefixed or are
// missing either part.
func parseMCPName(name string) (server, toolName string, ok bool) {
	if !strings.HasPrefix(name, mcpNamePrefix) {
		return "", "", false
	}
	rest := name[len(mcpNamePrefix):]
	i := strings.Index(rest, "__")
	if i <= 0 || i+2 > len(rest) {
		return "", "", false
	}
	return rest[:i], rest[i+2:], true
}

// renamedTool wraps a tool with an mcp__<server>__<name> name while delegating
// Declaration metadata and calls to the original. It mirrors the framework's
// NamedTool but applies the MCP naming convention rather than a "name_" prefix.
type renamedTool struct {
	original tool.Tool
	name     string
}

// streamableRenamedTool is the StreamableTool-capable variant, used only when
// the underlying tool genuinely streams, so non-streaming tools are never routed
// through the streaming execution path.
type streamableRenamedTool struct {
	renamedTool
	streamable tool.StreamableTool
}

// newRenamedTool wraps original under the mcp__<server>__<name> naming scheme.
// The returned tool implements CallableTool, and additionally StreamableTool
// when the underlying tool truly streams.
func newRenamedTool(original tool.Tool, server string) tool.Tool {
	base := renamedTool{
		original: original,
		name:     mcpNamePrefix + server + "__" + original.Declaration().Name,
	}
	// Honor the StreamInner() opt-out: a tool may implement StreamableTool but
	// explicitly ask not to be treated as streaming.
	if st, ok := original.(tool.StreamableTool); ok {
		if pref, ok := original.(interface{ StreamInner() bool }); !ok || pref.StreamInner() {
			return &streamableRenamedTool{renamedTool: base, streamable: st}
		}
	}
	return &base
}

// Declaration returns the original metadata with the renamed name.
func (t *renamedTool) Declaration() *tool.Declaration {
	decl := t.original.Declaration()
	return &tool.Declaration{
		Name:         t.name,
		Description:  decl.Description,
		InputSchema:  decl.InputSchema,
		OutputSchema: decl.OutputSchema,
	}
}

// Original returns the wrapped tool so framework unwrapping can reach it.
func (t *renamedTool) Original() tool.Tool { return t.original }

// Call delegates to the original tool.
func (t *renamedTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if callable, ok := t.original.(tool.CallableTool); ok {
		return callable.Call(ctx, jsonArgs)
	}
	return nil, errors.New("tool is not callable")
}

// SkipSummarization delegates to the original tool when supported.
func (t *renamedTool) SkipSummarization() bool {
	if s, ok := t.original.(interface{ SkipSummarization() bool }); ok {
		return s.SkipSummarization()
	}
	return false
}

// ToolMetadata delegates to the original tool.
func (t *renamedTool) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(t.original)
}

// StreamableCall delegates to the original streamable tool.
func (t *streamableRenamedTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}
