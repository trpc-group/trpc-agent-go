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
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// toolMetadata holds pre-computed, lowercase-normalized search fields for a tool.
type toolMetadata struct {
	Parts       []string // name parts split by CamelCase and underscores
	Full        string   // parts joined by spaces (fallback matching)
	Description string   // original tool description (surfaced in search results)
	descLower   string   // lowercase description
	nameLower   string   // lowercase name
}

// parsedName holds the result of parsing a tool name into searchable parts.
type parsedName struct {
	Parts []string // lowercase name parts
	Full  string   // full name joined by spaces (lowercase)
}

// parseToolName parses a tool name into searchable parts. It handles both MCP
// tools (mcp__server__action) and regular tools (CamelCase + underscores).
func parseToolName(name string) parsedName {
	if name == "" {
		return parsedName{Parts: []string{}, Full: ""}
	}

	// Split by CamelCase and underscores: insert a space before an uppercase
	// letter that follows a lowercase letter, then replace underscores.
	var sb strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(name[i-1])
			if prev >= 'a' && prev <= 'z' {
				sb.WriteRune(' ')
			}
		}
		sb.WriteRune(r)
	}
	expanded := strings.ToLower(strings.ReplaceAll(sb.String(), "_", " "))

	parts := make([]string, 0)
	for _, p := range strings.Fields(expanded) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parsedName{Parts: parts, Full: strings.Join(parts, " ")}
}

// tokenizeQueryPattern treats every character that is NOT a Unicode letter or
// digit, underscore, hyphen, or plus as a separator. This naturally handles
// spaces, commas (English and Chinese), semicolons, pipes, and other
// punctuation.
var tokenizeQueryPattern = regexp.MustCompile(`[^\p{L}\p{N}_\-+]+`)

// tokenizeQuery splits a query into lowercase terms.
func tokenizeQuery(query string) []string {
	parts := tokenizeQueryPattern.Split(strings.ToLower(query), -1)
	result := parts[:0]
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseQueryTerms splits tokens into required terms (with a "+" prefix) and
// optional terms.
func parseQueryTerms(terms []string) (required, optional []string) {
	for _, term := range terms {
		if strings.HasPrefix(term, "+") && len(term) > 1 {
			required = append(required, term[1:])
		} else {
			optional = append(optional, term)
		}
	}
	return
}

// splitRequiredTerm splits a required term into atomic sub-terms on `_`/`-`.
func splitRequiredTerm(termLower string) []string {
	if !strings.ContainsAny(termLower, "_-") {
		return []string{termLower}
	}
	parts := strings.FieldsFunc(termLower, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return []string{termLower}
	}
	return parts
}

// buildScoringTerms merges required and optional terms into the scoring list.
// Required terms are split on `_`/`-` so they can match at the parts level.
func buildScoringTerms(required, optional []string) []string {
	if len(required) == 0 {
		return optional
	}
	terms := make([]string, 0, len(required)+len(optional))
	for _, t := range required {
		terms = append(terms, splitRequiredTerm(strings.ToLower(t))...)
	}
	return append(terms, optional...)
}

// compileTermPatterns pre-compiles `\b` word-boundary regexes for search terms.
// Non-ASCII terms (e.g. CJK) are mapped to nil → matchTermInText falls back to
// strings.Contains since Go's `\b` only fires at ASCII word boundaries.
func compileTermPatterns(terms []string) map[string]*regexp.Regexp {
	patterns := make(map[string]*regexp.Regexp, len(terms))
	for _, term := range terms {
		termLower := strings.ToLower(term)
		if _, exists := patterns[termLower]; exists {
			continue
		}
		if !isASCII(termLower) {
			patterns[termLower] = nil
			continue
		}
		pattern, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(termLower) + `\b`)
		if err != nil {
			patterns[termLower] = nil
			continue
		}
		patterns[termLower] = pattern
	}
	return patterns
}

// isASCII reports whether s contains only ASCII bytes (used to decide `\b` vs substring matching).
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return s != ""
}

// matchTermInText matches a term against text using a word-boundary regex,
// falling back to a substring check for non-ASCII terms.
func matchTermInText(termLower, textLower string, patterns map[string]*regexp.Regexp) bool {
	if pattern, ok := patterns[termLower]; ok && pattern != nil {
		return pattern.MatchString(textLower)
	}
	return strings.Contains(textLower, termLower)
}

// matchesAllRequired reports whether a tool satisfies every required term.
// Terms containing `_`/`-` are split into sub-terms; each must match a name
// part or appear in the description.
func matchesAllRequired(meta *toolMetadata, requiredTerms []string, patterns map[string]*regexp.Regexp) bool {
	for _, term := range requiredTerms {
		for _, sub := range splitRequiredTerm(strings.ToLower(term)) {
			if !nameOrDescHasTerm(meta, sub, patterns) {
				return false
			}
		}
	}
	return true
}

// nameOrDescHasTerm checks whether a single atomic term appears in the tool's
// name parts (exact match) or description (word-boundary / substring).
func nameOrDescHasTerm(meta *toolMetadata, termLower string, patterns map[string]*regexp.Regexp) bool {
	for _, part := range meta.Parts {
		if part == termLower {
			return true
		}
	}
	return matchTermInText(termLower, meta.descLower, patterns)
}

// scoreToolForQuery computes a tool's relevance score for a set of query terms.
//
// Scoring (accumulated per term):
//   - compound term (contains `_`/`-`) substring-matches the tool name:  +10
//   - tool name part exact match:                                        +10
//   - description word-boundary match:                                    +3
//   - full-name substring match (fallback, only when total is still 0):   +3
func scoreToolForQuery(meta *toolMetadata, terms []string, patterns map[string]*regexp.Regexp) int {
	totalScore := 0
	toolName := meta.nameLower
	for _, term := range terms {
		termLower := strings.ToLower(term)
		termScore := 0

		if strings.ContainsAny(termLower, "_-") && strings.Contains(toolName, termLower) {
			termScore += 10
		}

		for _, part := range meta.Parts {
			if part == termLower {
				termScore += 10
				break
			}
		}

		if meta.descLower != "" && matchTermInText(termLower, meta.descLower, patterns) {
			termScore += 3
		}

		totalScore += termScore
	}

	// Full-name fallback fires only when no term scored above.
	if totalScore == 0 {
		for _, term := range terms {
			if matchTermInText(strings.ToLower(term), meta.Full, patterns) {
				totalScore += 3
			}
		}
	}
	return totalScore
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
	if maxResults <= 0 {
		maxResults = p.maxResults
	}
	if maxResults > maxMaxResults {
		maxResults = maxMaxResults
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
	// Embedding path: when a ToolKnowledge is configured (WithToolKnowledge) and
	// the model issued a keyword query, rank deferred tools by semantic (vector)
	// similarity instead of the built-in keyword text matching. Exact tool_names
	// loads and namespace-only listings keep the deterministic index path.
	useEmbedding := p.knowledge != nil && !req.isSelect() && req.hasQuery()
	if useEmbedding {
		var err error
		selectedTools, overflow, errPayload, err = p.searchToolsByEmbedding(ctx, req, allAllowed)
		if err != nil {
			if !p.failOpen {
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

	// tool_names is name-anchored, so namespace validation is skipped. An empty
	// namespace falls through to the global keyword path in candidateSetWithBias.
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
	candidatesSet, namespaceBias := p.candidateSetWithBias(namespace, queries)
	if candidatesSet == nil {
		return nil, nil
	}

	// Drop permission-denied tools from the candidate set so they never
	// consume max_results slots ahead of allowed tools.
	if allAllowed != nil {
		for name := range candidatesSet {
			if !allAllowed[name] {
				delete(candidatesSet, name)
			}
		}
	}
	if len(candidatesSet) == 0 {
		return nil, nil
	}

	best := make(map[string]int, len(candidatesSet))
	for _, q := range queries {
		p.scoreQueryInto(candidatesSet, q, best)
	}
	if len(best) == 0 {
		return nil, nil
	}

	// Apply per-tool namespace bias so ties are broken by owning-toolbox match.
	if len(namespaceBias) > 0 {
		for name, raw := range best {
			if ns, ok := p.namespaceByTool[name]; ok {
				if bonus := namespaceBias[ns]; bonus > 0 {
					best[name] = raw + bonus
				}
			}
		}
	}

	type candidate struct {
		name     string
		score    int
		partsLen int
	}
	candidates := make([]candidate, 0, len(best))
	for name, score := range best {
		partsLen := 0
		if meta := p.allMeta[name]; meta != nil {
			partsLen = len(meta.Parts)
		}
		candidates = append(candidates, candidate{name: name, score: score, partsLen: partsLen})
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.partsLen != b.partsLen {
			return a.partsLen < b.partsLen
		}
		return a.name < b.name
	})

	ranked := make([]string, len(candidates))
	for i := range ranked {
		ranked[i] = candidates[i].name
	}
	return splitByCap(ranked, maxResults)
}

// namespaceBiasFloor is the minimum toolbox description-match score required
// before it contributes as a per-tool tiebreaker on a namespace-less search.
const namespaceBiasFloor = 1

// candidateSetWithBias resolves the candidate tool set for a keyword search
// and, when namespace is empty, also returns a per-namespace bias derived from
// the queries. With a namespace, only that toolbox's tools are candidates;
// without one, every deferred tool participates and the bias tiebreaks tools
// whose owning toolbox description matches the queries.
func (p *Plugin) candidateSetWithBias(
	namespace string,
	queries []string,
) (map[string]struct{}, map[string]int) {
	if namespace != "" {
		if box, ok := p.toolboxByName[namespace]; ok {
			return box.toolNames, nil
		}
		return nil, nil
	}
	// No namespace: search the entire deferred catalog (the empty-namespace
	// contract: "look everywhere").
	return p.deferredNames, p.scoreNamespacesByQueries(queries)
}

// scoreNamespacesByQueries assigns each non-default toolbox a score equal to
// the number of distinct query terms matching its description (word-boundary
// for ASCII, substring for CJK). Toolboxes below namespaceBiasFloor are
// dropped. The caller must hold p.mu (read).
func (p *Plugin) scoreNamespacesByQueries(queries []string) map[string]int {
	if len(queries) == 0 || len(p.toolboxes) == 0 {
		return nil
	}

	// Tokenize once and compile word-boundary patterns. "+term" prefixes are
	// treated like other terms here — they gate per-tool scoring elsewhere,
	// and counting them again strengthens bias toward the chosen domain.
	termSet := make(map[string]struct{})
	for _, q := range queries {
		for _, tok := range tokenizeQuery(q) {
			if strings.HasPrefix(tok, "+") && len(tok) > 1 {
				tok = tok[1:]
			}
			if tok == "" {
				continue
			}
			termSet[tok] = struct{}{}
		}
	}
	if len(termSet) == 0 {
		return nil
	}
	terms := make([]string, 0, len(termSet))
	for t := range termSet {
		terms = append(terms, t)
	}
	patterns := compileTermPatterns(terms)

	scores := make(map[string]int, len(p.toolboxes))
	for _, box := range p.toolboxes {
		if box.name == defaultNamespace || box.description == "" {
			continue
		}
		descLower := strings.ToLower(box.description)
		hits := 0
		for _, term := range terms {
			if matchTermInText(term, descLower, patterns) {
				hits++
			}
		}
		if hits >= namespaceBiasFloor {
			scores[box.name] = hits
		}
	}
	return scores
}

// scoreQueryInto scores a single query within the candidate set and merges each
// hit into best by max score: exact-name fast path → tokenize → required-term
// gating → scoring.
func (p *Plugin) scoreQueryInto(candidatesSet map[string]struct{}, query string, best map[string]int) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}

	// Fast path: the whole query exactly matches a tool name (case-insensitive).
	if canonical, ok := p.nameByLower[strings.ToLower(query)]; ok {
		if _, inScope := candidatesSet[canonical]; inScope {
			if cur, ok := best[canonical]; !ok || exactNameScore > cur {
				best[canonical] = exactNameScore
			}
			return
		}
	}

	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return
	}

	requiredTerms, optionalTerms := parseQueryTerms(tokens)
	scoringTerms := buildScoringTerms(requiredTerms, optionalTerms)
	patterns := compileTermPatterns(scoringTerms)

	for name := range candidatesSet {
		meta := p.allMeta[name]
		if meta == nil {
			continue
		}
		if len(requiredTerms) > 0 && !matchesAllRequired(meta, requiredTerms, patterns) {
			continue
		}
		if score := scoreToolForQuery(meta, scoringTerms, patterns); score > 0 {
			if cur, ok := best[name]; !ok || score > cur {
				best[name] = score
			}
		}
	}
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
// InputSchema is populated only in call_tool mode (EnableCallTool): since the
// loaded tool is not advertised as an individual function, the model needs its
// schema here to build the call_tool "params".
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
		if meta, ok := p.allMeta[name]; ok {
			desc = meta.Description
		}
		_, loaded := previouslyLoaded[name]
		if loaded {
			anyAlreadyLoaded = true
		} else {
			allAlreadyLoaded = false
		}
		summary := toolSummary{Name: name, Description: desc, AlreadyLoaded: loaded}
		// In call_tool mode the loaded tool is not advertised as an individual
		// function, so surface its input schema here for building call_tool params.
		if p.enableCallTool {
			if t, ok := p.toolBox[name]; ok && t != nil {
				if decl := t.Declaration(); decl != nil {
					summary.InputSchema = decl.InputSchema
				}
			}
		}
		summaries = append(summaries, summary)
	}
	p.mu.RUnlock()

	var status string
	if p.enableCallTool {
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
