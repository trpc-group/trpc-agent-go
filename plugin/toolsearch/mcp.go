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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// mcpSource holds runtime listing state for an MCP-backed toolbox.
type mcpSource struct {
	serverName string
	toolSet    tool.ToolSet
	// Fingerprint by renamed tool name; used to forget embeddings only on
	// declaration change/removal.
	fingerprints map[string]string
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
		mcp: &mcpSource{
			serverName:   name,
			toolSet:      box.ToolSet,
			fingerprints: make(map[string]string),
		},
	}
	p.toolboxByName[name] = idx
	p.toolboxes = append(p.toolboxes, idx)
}

// ensureMCPListed syncs one MCP namespace to the latest listing:
// add/update current tools and prune missing ones. An empty listing is treated
// as a transient failure and skipped so a temporarily unreachable MCP server
// does not wipe the previously indexed tools; a server that truly wants to
// publish an empty directory should surface that through its own signaling
// rather than an ambiguous empty slice.
// Listing I/O runs outside lock; index updates run under lock; vector-store
// forget runs after unlock.
func (p *Plugin) ensureMCPListed(ctx context.Context, box *toolboxIndex) {
	if !box.isMCP() {
		return
	}

	tools := box.mcp.toolSet.Tools(ctx)
	if len(tools) == 0 {
		// Empty listing = transient failure; keep the last-known snapshot so a
		// down server does not wipe previously-visible tools.
		return
	}

	// Build the fresh set of renamed tools outside the lock (cheap, no I/O).
	type freshEntry struct {
		tool tool.Tool
		fp   string
	}
	fresh := make(map[string]freshEntry, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		renamed := newRenamedTool(t, box.mcp.serverName)
		decl := renamed.Declaration()
		fresh[decl.Name] = freshEntry{tool: renamed, fp: declarationFingerprint(decl)}
	}

	p.mu.Lock()

	// Prune tools that were previously indexed for this namespace but are no
	// longer returned by the server. Collisions from other namespaces never
	// entered box.toolNames, so this only touches tools we actually own.
	var pruned []string
	for name := range box.toolNames {
		if _, keep := fresh[name]; keep {
			continue
		}
		p.unindexTool(name)
		delete(box.toolNames, name)
		delete(box.mcp.fingerprints, name)
		pruned = append(pruned, name)
	}

	// Add or refresh every tool in the fresh listing. Refreshing rebinds
	// toolsByName/metaByName to the latest wrapper so a server-side schema or
	// implementation update immediately supersedes the stale one. Only tools
	// whose declaration fingerprint actually changed are collected for
	// forgetting so unchanged entries keep their embedding across listings.
	var (
		added, refreshed int
		changed          []string
	)
	for name, entry := range fresh {
		if existingNS, dup := p.namespaceByTool[name]; dup && existingNS != box.name {
			log.Errorf("[%s] MCP tool %q collides with namespace %q; kept %q",
				p.name, name, box.name, existingNS)
			continue
		}
		if oldFP, existed := box.mcp.fingerprints[name]; existed {
			if oldFP != entry.fp {
				refreshed++
				changed = append(changed, name)
			}
			// unchanged: do not touch the embedding index.
		} else {
			added++
			// Newly added tools do not need forgetting: they were never indexed.
		}
		p.indexTool(entry.tool)
		p.deferredNames[name] = struct{}{}
		p.namespaceByTool[name] = box.name
		box.toolNames[name] = struct{}{}
		box.mcp.fingerprints[name] = entry.fp
	}

	// Build the forget list while still holding the lock, and snapshot the
	// indexed count for logging because it is racy to read after unlock.
	// box.mcp.serverName and p.semanticIndex are set once at registration and
	// never mutated afterwards, so they are safe to read post-unlock.
	var forget []string
	if p.semanticIndex != nil {
		forget = append(append(forget, changed...), pruned...)
	}
	indexedCount := len(box.toolNames)

	// Explicit Unlock (not defer): vector-store I/O below must run outside the
	// write lock so a slow/failing store never stalls a concurrent listing or
	// catalog render.
	p.mu.Unlock()

	if p.semanticIndex != nil && len(forget) > 0 {
		p.semanticIndex.forget(ctx, forget)
	}

	log.Infof("[%s] listed MCP server %q: %d tools (added=%d refreshed=%d pruned=%d)",
		p.name, box.mcp.serverName, indexedCount, added, refreshed, len(pruned))
}

// declarationFingerprint hashes a declaration for change detection across
// listings, so unchanged tools can keep cached embeddings. On marshal failure
// it falls back to hashing the tool name so the return value always has the
// same shape (hex-encoded sha256) and cannot accidentally equal a valid hash
// from the happy path.
func declarationFingerprint(decl *tool.Declaration) string {
	if decl == nil {
		return ""
	}
	data, err := json.Marshal(decl)
	if err != nil {
		data = []byte("name:" + decl.Name)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
	if i <= 0 || i+2 >= len(rest) {
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
