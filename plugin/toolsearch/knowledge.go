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
	"fmt"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// This file backs the tool_search "queries" path with embedding-based semantic
// search, enabled via WithToolKnowledge. See doc.go for the user-facing
// overview. Exact tool_names loads and namespace-only listings keep the
// deterministic index path in search.go.

// --- Embedding token usage tracking ---

// usageAccumulator collects embedding token usage across the tool_search calls
// of a single model turn. Tool calls may run concurrently (parallel tools), so
// updates are guarded by mu.
type usageAccumulator struct {
	mu    sync.Mutex
	usage model.Usage
}

// usageContextKey is the context key for the per-turn usageAccumulator.
type usageContextKey struct{}

func (a *usageAccumulator) add(promptTokens, totalTokens int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage.PromptTokens += promptTokens
	a.usage.TotalTokens += totalTokens
}

// withUsageAccumulator seeds an empty usage accumulator onto ctx so tool_search
// calls this turn can fold their embedding usage into it.
func withUsageAccumulator(ctx context.Context) context.Context {
	return context.WithValue(ctx, usageContextKey{}, &usageAccumulator{})
}

// ToolSearchUsageFromContext returns a snapshot of the embedding token usage
// accumulated by tool_search this turn. ok is true only when WithToolKnowledge
// is configured (which seeds the accumulator at BeforeModel time).
func ToolSearchUsageFromContext(ctx context.Context) (*model.Usage, bool) {
	acc, ok := ctx.Value(usageContextKey{}).(*usageAccumulator)
	if !ok {
		return nil, false
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	snapshot := acc.usage
	return &snapshot, true
}

// recordUsage folds u into the accumulator seeded on ctx at BeforeModel time.
// It is a no-op when no accumulator is present.
func (p *Plugin) recordUsage(ctx context.Context, u *model.Usage) {
	if u == nil {
		return
	}
	if acc, ok := ctx.Value(usageContextKey{}).(*usageAccumulator); ok {
		acc.add(u.PromptTokens, u.TotalTokens)
	}
}

// addEmbedderUsage folds an embedder's usage map into usage. Token counts may be
// int, int64, or float64 depending on the backend, so all are accepted.
func addEmbedderUsage(usage *model.Usage, m map[string]any) {
	usage.PromptTokens += usageTokens(m["prompt_tokens"])
	usage.TotalTokens += usageTokens(m["total_tokens"])
}

func usageTokens(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// --- ToolKnowledge: embedding index over deferred tools ---

// ToolKnowledge stores deferred tools and their embeddings in a vector store,
// enabling semantic keyword search from tool_search. Build one with
// NewToolKnowledge and pass it to New via WithToolKnowledge.
type ToolKnowledge struct {
	store    vectorstore.VectorStore
	embedder embedder.Embedder

	// indexed maps a tool name to the fingerprint of its stored embedding.
	// Keeping the fingerprint (not just a presence bit) lets upsert detect
	// snapshots that raced a concurrent prune+forget or a fingerprint bump.
	mu      sync.Mutex
	indexed map[string]string
}

// candidateTool pairs a tool with the fingerprint observed under the
// snapshot lock. upsert compares it against indexed and the verifier before
// (re)embedding.
type candidateTool struct {
	tool        tool.Tool
	fingerprint string
}

// candidateVerifier reports whether name@fp is still authoritative in the
// Plugin index; a false return tells upsert to skip publishing so a stale
// snapshot cannot revive a forgotten vector.
type candidateVerifier func(name, fp string) bool

// ToolKnowledgeOption configures a ToolKnowledge.
type ToolKnowledgeOption func(*ToolKnowledge)

// WithVectorStore sets the vector store for the ToolKnowledge (default: inmemory).
func WithVectorStore(s vectorstore.VectorStore) ToolKnowledgeOption {
	return func(k *ToolKnowledge) {
		if s != nil {
			k.store = s
		}
	}
}

// NewToolKnowledge creates a ToolKnowledge backed by embedder e.
func NewToolKnowledge(e embedder.Embedder, opts ...ToolKnowledgeOption) (*ToolKnowledge, error) {
	if e == nil {
		return nil, fmt.Errorf("tool knowledge: embedder is nil")
	}
	k := &ToolKnowledge{
		store:    inmemory.New(),
		embedder: e,
		indexed:  make(map[string]string),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(k)
		}
	}
	return k, nil
}

// upsert (re)embeds tools whose fingerprint differs from the indexed one,
// folding embedding token usage into usage. Matching fingerprints are a
// no-op. When verify is non-nil it is consulted before publishing so a
// stale snapshot cannot resurrect a vector a concurrent forget just dropped.
// On a fingerprint change the old document is deleted before Add to keep the
// store free of duplicates across backends with differing Add semantics.
func (k *ToolKnowledge) upsert(
	ctx context.Context,
	tools map[string]candidateTool,
	verify candidateVerifier,
	usage *model.Usage,
) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for name, ct := range tools {
		if ct.tool == nil {
			continue
		}
		cur, existed := k.indexed[name]
		if existed && cur == ct.fingerprint {
			continue
		}
		if verify != nil && !verify(name, ct.fingerprint) {
			log.DebugfContext(ctx, "skip stale embed for tool %s", name)
			continue
		}
		embedding, u, err := k.embedder.GetEmbeddingWithUsage(ctx, toolToText(ct.tool))
		if err != nil {
			return err
		}
		addEmbedderUsage(usage, u)
		if existed {
			if derr := k.store.Delete(ctx, name); derr != nil {
				log.DebugfContext(ctx, "drop stale embedded tool %s: %v", name, derr)
			}
			log.DebugfContext(ctx, "refresh embedded tool %s", name)
		} else {
			log.DebugfContext(ctx, "add embedded tool %s", name)
		}
		if err := k.store.Add(ctx, &document.Document{ID: name}, embedding); err != nil {
			return err
		}
		k.indexed[name] = ct.fingerprint
	}
	return nil
}

// forget drops the given tool names from the embedding index so their next
// upsert re-embeds them, and removes their documents from the store so a
// removed tool no longer surfaces in semantic search results. Missing entries
// (never indexed, or already removed by a concurrent call) are ignored so
// callers can pass a superset without pre-checking.
func (k *ToolKnowledge) forget(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, name := range names {
		if _, ok := k.indexed[name]; !ok {
			continue
		}
		delete(k.indexed, name)
		if err := k.store.Delete(ctx, name); err != nil {
			// Delete errors are typically "not found" for a store that lost
			// state (e.g. a fresh in-memory store on restart); log and move on
			// so a stale entry cannot block the refresh.
			log.DebugfContext(ctx, "forget embedded tool %s: %v", name, err)
		}
	}
}

// searchNames embeds query and returns candidate tool names ordered by
// descending vector similarity, scoped to candidateIDs. Token usage is folded
// into usage.
func (k *ToolKnowledge) searchNames(
	ctx context.Context,
	query string,
	candidateIDs []string,
	limit int,
	usage *model.Usage,
) ([]string, error) {
	embedding, u, err := k.embedder.GetEmbeddingWithUsage(ctx, query)
	if err != nil {
		return nil, err
	}
	addEmbedderUsage(usage, u)

	results, err := k.store.Search(ctx, &vectorstore.SearchQuery{
		Vector:     embedding,
		SearchMode: vectorstore.SearchModeVector,
		Limit:      limit,
		Filter:     &vectorstore.SearchFilter{IDs: candidateIDs},
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(results.Results))
	for _, r := range results.Results {
		if r != nil && r.Document != nil {
			names = append(names, r.Document.ID)
		}
	}
	return names, nil
}

// toolToText renders a tool's name, description, and parameters into the text
// embedded for similarity search.
func toolToText(t tool.Tool) string {
	if t == nil {
		return ""
	}
	decl := t.Declaration()
	if decl == nil {
		return ""
	}
	parts := []string{
		fmt.Sprintf("Tool: %s", decl.Name),
		fmt.Sprintf("Description: %s", decl.Description),
	}
	if decl.InputSchema != nil && len(decl.InputSchema.Properties) > 0 {
		keys := make([]string, 0, len(decl.InputSchema.Properties))
		for name := range decl.InputSchema.Properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		descs := make([]string, 0, len(keys))
		for _, name := range keys {
			info := decl.InputSchema.Properties[name]
			if info == nil {
				continue
			}
			paramType := strings.TrimSpace(info.Type)
			if paramType == "" {
				if info.Items != nil {
					paramType = "array"
				} else if len(info.Properties) > 0 {
					paramType = "object"
				}
			}
			descs = append(descs, fmt.Sprintf("%s (%s): %s", name, paramType, strings.TrimSpace(info.Description)))
		}
		if len(descs) > 0 {
			parts = append(parts, "Parameters: "+strings.Join(descs, ", "))
		}
	}
	return strings.Join(parts, "\n")
}

// --- Embedding search path wired into tool_search ---

// searchToolsByEmbedding ranks deferred tools by semantic similarity to the
// queries. A non-empty namespace scopes the candidate set to that toolbox; an
// empty namespace searches every deferred tool. permission-denied tools are
// dropped from the candidate set before embeddings are computed so their
// name/description is never sent to a remote embedder. Each query is searched
// independently and merged by best (smallest) rank per tool so OR-combined
// queries behave like the keyword path. The top maxResults load with schemas;
// the remainder are returned as name-only overflow.
//
// It returns an errPayload (mirroring resolveSelection) on an unknown namespace,
// or an error only on embedding/store failures.
func (p *Plugin) searchToolsByEmbedding(
	ctx context.Context,
	req searchRequest,
	allAllowed map[string]bool,
) (selected, overflow []string, errPayload string, err error) {
	candidateTools, errPayload := p.embeddingCandidates(req.namespace, allAllowed)
	if errPayload != "" || len(candidateTools) == 0 {
		return nil, nil, errPayload, nil
	}

	// Record accumulated embedding usage even if we return early on error.
	usage := &model.Usage{}
	defer p.recordUsage(ctx, usage)

	if err := p.knowledge.upsert(ctx, candidateTools, p.verifyCandidate, usage); err != nil {
		return nil, nil, "", fmt.Errorf("tool search: embedding tools: %w", err)
	}

	candidateIDs := make([]string, 0, len(candidateTools))
	for name := range candidateTools {
		candidateIDs = append(candidateIDs, name)
	}

	// Fetch the full candidate pool per query (limit=len) so the merge, not a
	// per-query cap, decides the final cut.
	bestRank := make(map[string]int, len(candidateIDs))
	for _, q := range req.queries {
		if q = strings.TrimSpace(q); q == "" {
			continue
		}
		names, serr := p.knowledge.searchNames(ctx, q, candidateIDs, len(candidateIDs), usage)
		if serr != nil {
			return nil, nil, "", fmt.Errorf("tool search: semantic search: %w", serr)
		}
		for rank, name := range names {
			if cur, ok := bestRank[name]; !ok || rank < cur {
				bestRank[name] = rank
			}
		}
	}
	if len(bestRank) == 0 {
		return nil, nil, "", nil
	}

	ranked := make([]string, 0, len(bestRank))
	for name := range bestRank {
		ranked = append(ranked, name)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if bestRank[ranked[i]] != bestRank[ranked[j]] {
			return bestRank[ranked[i]] < bestRank[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})

	selected, overflow = splitByCap(ranked, req.maxResults)
	return selected, overflow, "", nil
}

// embeddingCandidates resolves candidate tools for an embedding search under
// a single read lock: the namespace's tools when namespace is set (with the
// same unknown-namespace guard as resolveSelection), otherwise every deferred
// tool. Permission-denied tools are excluded so their name/description is
// never sent to a remote embedder. Each entry carries the fingerprint
// observed under the lock; upsert uses it via verifyCandidate to detect
// snapshots that raced a concurrent refresh.
func (p *Plugin) embeddingCandidates(namespace string, allAllowed map[string]bool) (map[string]candidateTool, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if errPayload := p.validateNamespace(namespace); errPayload != "" {
		return nil, errPayload
	}

	names := p.deferredNames
	if namespace != "" {
		box, ok := p.toolboxByName[namespace]
		if !ok {
			return nil, ""
		}
		names = box.toolNames
	}

	tools := make(map[string]candidateTool, len(names))
	for name := range names {
		if allAllowed != nil && !allAllowed[name] {
			continue
		}
		t, ok := p.toolBox[name]
		if !ok {
			continue
		}
		tools[name] = candidateTool{
			tool:        t,
			fingerprint: p.currentFingerprintLocked(name, t),
		}
	}
	return tools, ""
}

// currentFingerprintLocked returns the fingerprint of name in the Plugin
// index while p.mu is held. MCP toolboxes reuse box.mcp.fingerprints (the
// same digest ensureMCPListed compares to decide forgets); static toolboxes
// hash the current declaration on the fly.
func (p *Plugin) currentFingerprintLocked(name string, t tool.Tool) string {
	if ns, ok := p.namespaceByTool[name]; ok {
		if box := p.toolboxByName[ns]; box != nil && box.mcp != nil {
			if fp, ok := box.mcp.fingerprints[name]; ok {
				return fp
			}
		}
	}
	if t == nil {
		return ""
	}
	return declarationFingerprint(t.Declaration())
}

// verifyCandidate reports whether name@fp is still an authoritative deferred
// tool. It closes the TOCTOU window between embeddingCandidates and upsert:
// a concurrent ensureMCPListed that pruned name or bumped its fingerprint
// makes this return false, so upsert skips publishing the stale vector.
func (p *Plugin) verifyCandidate(name, fp string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.toolBox[name]
	if !ok {
		return false
	}
	if _, isDeferred := p.deferredNames[name]; !isDeferred {
		return false
	}
	return p.currentFingerprintLocked(name, t) == fp
}
