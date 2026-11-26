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
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	searchrank "trpc.group/trpc-go/trpc-agent-go/internal/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// toolMetadata caches the reusable ranking document and the original description
// surfaced in search results.
type toolMetadata struct {
	Description string
	document    searchrank.Document
}

// searchRequest is the normalized, pre-parsed view of toolSearchInput used by
// the internal routing layer. Grouping the fields keeps resolveSelection and
// searchToolsByEmbedding signatures short and lets callers compute the derived
// flags (isSelect / hasQuery) once at the entry point.
type searchRequest struct {
	namespace  string
	toolNames  []string
	queries    []string
	maxResults int
}

// isSelect reports whether the caller asked for exact tool names.
func (r searchRequest) isSelect() bool { return len(r.toolNames) > 0 }

// hasQuery reports whether the caller provided keyword queries.
func (r searchRequest) hasQuery() bool { return len(r.queries) > 0 }

// toolSearchInput is the argument schema for the tool_search function tool.
type toolSearchInput struct {
	ToolNames  []string `json:"tool_names,omitempty" jsonschema:"description=Exact, fully-qualified tool name(s), e.g. [\"create_image\",\"mcp__git__commit\"].  Do NOT pass partial names, descriptions, or keywords here — use queries for those. Prefer this over queries when the catalog shows a matching name."`
	Queries    []string `json:"queries,omitempty" jsonschema:"description=Keyword search terms; each element is ONE atomic concept (entity/domain noun), OR-combined across elements. Include both Chinese and English for a concept, e.g. [\"获取时间\",\"get time\"]. Prefix '+' to require a term. Distinctive nouns usually rank better than generic verbs like get/list/update."`
	Namespace  string   `json:"namespace,omitempty" jsonschema:"description=Catalog toolbox scoping a keyword search to one domain. Leave empty to search every toolbox."`
	MaxResults int      `json:"max_results,omitempty" jsonschema:"description=Cap on keyword-search results (default 5, max 10). Extra matches are returned as name-only additional_candidates."`
}

// searchTools is the entry point for the tool_search function tool.
// Argument routing lives in resolveSelection.
func (p *Plugin) searchTools(ctx context.Context, input toolSearchInput) (string, error) {
	maxResults := input.MaxResults
	// The maxMaxResults ceiling guards only the value the model supplies at
	// runtime; it is applied before the default fallback so a developer's
	// explicit WithMaxResults(n) is a deliberate choice and honored as-is.
	if maxResults > maxMaxResults {
		maxResults = maxMaxResults
	}
	if maxResults <= 0 {
		maxResults = p.maxResults
	}

	req := searchRequest{
		namespace:  strings.TrimSpace(input.Namespace),
		toolNames:  trimNonEmpty(input.ToolNames),
		queries:    trimNonEmpty(input.Queries),
		maxResults: maxResults,
	}

	// Materialize MCP servers before the index is read: exact-name load lists
	// the owning servers, a namespace search lists that server, otherwise all.
	switch {
	case req.isSelect():
		p.materializeByToolNames(ctx, req.toolNames)
	case req.namespace != "":
		p.materializeNamespace(ctx, req.namespace)
	default:
		p.materializeAllMCP(ctx)
	}

	// Fetch all deferred-tool permissions once for result filtering.
	allAllowed := p.allDeferredPermissions(ctx)

	// Capture the set already loaded before this call, used to flag
	// already_loaded in the result.
	inv, hasInv := agent.InvocationFromContext(ctx)
	var previouslyLoaded map[string]struct{}
	if hasInv && inv != nil {
		previouslyLoaded = toStringSet(p.loadDiscoveredTools(ctx, inv))
	}

	var (
		selectedTools, overflow []string
		errPayload              string
	)
	// Embedding path: when a SemanticToolIndex is configured
	// (WithSemanticToolIndex) and the model issued a keyword query, rank
	// deferred tools by semantic (vector) similarity instead of the built-in
	// keyword text matching. Exact tool_names loads and namespace-only
	// listings keep the deterministic index path.
	useEmbedding := p.semanticIndex != nil && !req.isSelect() && req.hasQuery()
	if useEmbedding {
		var err error
		selectedTools, overflow, errPayload, err = p.searchToolsByEmbedding(ctx, req, allAllowed)
		if err != nil {
			if !p.embeddingFailOpen {
				return "", err
			}
			// fail-open: fall back to keyword matching so tools stay reachable.
			log.WarnfContext(ctx, "[%s] embedding search failed, falling back to keyword search: %v", p.name, err)
			useEmbedding = false
		}
	}
	if !useEmbedding {
		// Resolve under a single read lock; overflow are matches beyond maxResults.
		// Permissions are enforced inside the selection phase so denied tools
		// never consume max_results slots ahead of allowed tools.
		selectedTools, overflow, errPayload = p.resolveSelection(req, allAllowed)
	}
	if errPayload != "" {
		return errPayload, nil
	}

	// Accumulate into session state.
	if hasInv && inv != nil {
		p.appendDiscoveredTools(ctx, inv, selectedTools)
		log.InfofContext(ctx, "[%s] tool_search namespace=%q queries=%s toolNames=%s found=%s",
			p.name, req.namespace, strings.Join(req.queries, "|"), strings.Join(req.toolNames, "|"), strings.Join(selectedTools, "|"))
	}
	return p.formatSearchResult(selectedTools, overflow, previouslyLoaded), nil
}

// resolveSelection routes the request under a single p.mu read lock:
//   - tool_names → cross-namespace exact load;
//   - namespace + queries → keyword search inside that toolbox;
//   - namespace only → list every tool in that toolbox (capped);
//   - queries only → global keyword search with per-namespace bias;
//   - unknown namespace → returns a JSON error payload with the catalog.
//
// Returns selected tools (loaded with full schemas), an overflow list surfaced
// as name-only additional_candidates, or errPayload on validation failure.
func (p *Plugin) resolveSelection(
	req searchRequest,
	allAllowed map[string]bool,
) (selected, overflow []string, errPayload string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// tool_names is name-anchored, so namespace validation is skipped.
	if !req.isSelect() {
		if errPayload := p.validateNamespace(req.namespace); errPayload != "" {
			return nil, nil, errPayload
		}
	}

	switch {
	case req.isSelect():
		// Exact-name loads have no overflow: every resolved name is loaded.
		return p.selectToolsByName(req.toolNames, allAllowed), nil, ""
	case req.namespace != "" && !req.hasQuery():
		sel, of := p.listNamespaceTools(req.namespace, req.maxResults, allAllowed)
		return sel, of, ""
	default:
		sel, of := p.searchToolsByQueries(req.namespace, req.queries, req.maxResults, allAllowed)
		return sel, of, ""
	}
}

// selectToolsByName resolves exact tool names to their canonical form, dropping
// blanks, unknown names, duplicates, and permission-denied deferred tools.
// Matching is case-insensitive across all namespaces. The caller must hold p.mu
// (read).
func (p *Plugin) selectToolsByName(names []string, allAllowed map[string]bool) []string {
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		canonical, ok := p.nameByLower[strings.ToLower(name)]
		if !ok {
			continue
		}
		if _, dup := seen[canonical]; dup {
			continue
		}
		// Permission check for deferred tools; preset tools pass through.
		if allAllowed != nil {
			if _, isDeferred := p.deferredNames[canonical]; isDeferred && !allAllowed[canonical] {
				continue
			}
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result
}

// listNamespaceTools returns every tool in the given namespace that the caller
// is permitted to use, sorted by name. The first maxResults are loaded; any
// beyond that are returned as overflow (name-only additional_candidates).
// Supports the "namespace=X, no query" scan mode. The caller must hold p.mu
// (read).
func (p *Plugin) listNamespaceTools(namespace string, maxResults int, allAllowed map[string]bool) (selected, overflow []string) {
	box, ok := p.toolboxByName[namespace]
	if !ok || box == nil {
		return nil, nil
	}
	names := make([]string, 0, len(box.toolNames))
	for name := range box.toolNames {
		if allAllowed != nil && !allAllowed[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return splitByCap(names, maxResults)
}

// splitByCap splits an ordered list into the first maxResults entries and the
// remainder. A non-positive maxResults keeps everything in selected.
func splitByCap(names []string, maxResults int) (selected, overflow []string) {
	if maxResults > 0 && len(names) > maxResults {
		return names[:maxResults], names[maxResults:]
	}
	return names, nil
}

// searchToolsByQueries scores each query independently within the candidate
// set, merges by best score per tool, sorts by (score desc, parts asc, name
// asc), then splits at maxResults: top entries load with schemas, the rest are
// returned as name-only additional_candidates.
//
// A non-empty namespace scopes scoring to that toolbox. An empty namespace
// searches every deferred tool and adds a per-tool namespace bias so a
// generic-verb tool in the wrong domain does not out-rank the right-domain
// tool at the same raw score. Denied tools are dropped from the candidate set
// before scoring so they never consume max_results slots. The caller must hold
// p.mu (read).
func (p *Plugin) searchToolsByQueries(namespace string, queries []string, maxResults int, allAllowed map[string]bool) (selected, overflow []string) {
	candidatesSet := p.deferredNames
	if namespace != "" {
		box := p.toolboxByName[namespace]
		if box == nil {
			return nil, nil
		}
		candidatesSet = box.toolNames
	}
	lookup := func(name string) *searchrank.Document {
		meta := p.metaByName[name]
		if meta == nil || (allAllowed != nil && !allAllowed[name]) {
			return nil
		}
		return &meta.document
	}
	ranked := searchrank.RankNames(queries, candidatesSet, p.nameByLower, lookup)
	return splitByCap(ranked, maxResults)
}

// catalogEntry is one namespace summary in error payloads.
type catalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// validateNamespace reports a JSON error payload when a non-empty namespace is
// not in the toolbox catalog, and "" when the namespace is valid or validation
// does not apply (no toolboxes, or the legacy _default-only setup). The caller
// must hold p.mu (read).
func (p *Plugin) validateNamespace(namespace string) string {
	if namespace == "" || len(p.toolboxes) == 0 || p.isDefaultOnly() {
		return ""
	}
	if _, ok := p.toolboxByName[namespace]; ok {
		return ""
	}
	return p.formatNamespaceError(
		"unknown_namespace",
		fmt.Sprintf("Namespace %q is not in the toolbox catalog.", namespace),
		namespace,
	)
}

// formatNamespaceError renders a structured error when the model passes an
// invalid or missing namespace, including the catalog so the model can retry.
func (p *Plugin) formatNamespaceError(status, message, badNamespace string) string {
	cat := make([]catalogEntry, 0, len(p.toolboxes))
	for _, box := range p.toolboxes {
		if box.name == defaultNamespace {
			continue
		}
		cat = append(cat, catalogEntry{Name: box.name, Description: box.description})
	}
	result := struct {
		Status    string         `json:"status"`
		Message   string         `json:"message"`
		Namespace string         `json:"namespace,omitempty"`
		Catalog   []catalogEntry `json:"catalog"`
	}{
		Status:    status,
		Message:   message,
		Namespace: badNamespace,
		Catalog:   cat,
	}
	data, _ := json.Marshal(result)
	return string(data)
}

// toolSummary describes a matched tool. already_loaded=true tells the model it
// was loaded earlier in this conversation and should be called directly.
//
// InputSchema is populated only in DispatchToolCalls mode: since the loaded
// tool is not advertised as an individual function, the model needs its schema
// here to build the call_tool "params".
type toolSummary struct {
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	InputSchema   *tool.Schema `json:"input_schema,omitempty"`
	AlreadyLoaded bool         `json:"already_loaded,omitempty"`
}

// formatSearchResult formats search results as JSON:
//   - tools: entries callable after this search, each carrying already_loaded;
//   - additional_candidates: lower-ranked, name-only overflow the model may
//     load via tool_names instead of re-searching;
//   - status: a short instruction line for the model.
func (p *Plugin) formatSearchResult(
	tools, overflow []string,
	previouslyLoaded map[string]struct{},
) string {
	summaries := make([]toolSummary, 0, len(tools))
	allAlreadyLoaded := len(tools) > 0
	anyAlreadyLoaded := false
	p.mu.RLock()
	for _, name := range tools {
		desc := ""
		if meta, ok := p.metaByName[name]; ok {
			desc = meta.Description
		}
		_, loaded := previouslyLoaded[name]
		if loaded {
			anyAlreadyLoaded = true
		} else {
			allAlreadyLoaded = false
		}
		summary := toolSummary{Name: name, Description: desc, AlreadyLoaded: loaded}
		// In DispatchToolCalls mode the loaded tool is not advertised as an
		// individual function, so surface its input schema here for building
		// call_tool params.
		if p.invocationMode == DispatchToolCalls {
			if t, ok := p.toolsByName[name]; ok && t != nil {
				if decl := t.Declaration(); decl != nil {
					summary.InputSchema = decl.InputSchema
				}
			}
		}
		summaries = append(summaries, summary)
	}
	p.mu.RUnlock()

	var status string
	if p.invocationMode == DispatchToolCalls {
		switch {
		case len(tools) == 0:
			status = "No matches. Try different keywords, another namespace, or tool_names."
		case allAlreadyLoaded:
			status = "All matches were loaded earlier — invoke them with call_tool using their input_schema."
		case anyAlreadyLoaded:
			status = "Tools loaded. Invoke each with call_tool (tool_name + params matching its input_schema); entries with already_loaded=true were loaded earlier."
		default:
			status = "Tools loaded — invoke each with call_tool (tool_name + params matching its input_schema)."
		}
	} else {
		switch {
		case len(tools) == 0:
			status = "No matches. Try different keywords, another namespace, or tool_names."
		case allAlreadyLoaded:
			status = "All matches were loaded earlier — call them directly."
		case anyAlreadyLoaded:
			status = "Tools loaded. Entries with already_loaded=true were loaded earlier — call them directly."
		default:
			status = "Tools loaded — call them directly."
		}
	}
	if len(overflow) > 0 {
		status += " More matches in additional_candidates; load one with tool_names instead of re-searching."
	}

	result := struct {
		Status               string        `json:"status"`
		Tools                []toolSummary `json:"tools"`
		AdditionalCandidates []string      `json:"additional_candidates,omitempty"`
	}{
		Status:               status,
		Tools:                summaries,
		AdditionalCandidates: overflow,
	}
	data, _ := json.Marshal(result)
	return string(data)
}
