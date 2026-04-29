//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides knowledge search tools for agents.
package tool

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultCodeSearchToolName        = "code_search"
	defaultCodeGraphSearchToolName   = "code_graph"
	defaultCodeGraphSearchMinScore   = 0.1
	defaultCodeGraphSearchMaxResults = 5
	defaultCodeSearchMinScore        = 0.1
)

// defaultCodeSearchExcludedMetadataKeys lists metadata fields that are stripped
// from each document before the result is returned to the LLM. These fields
// are either verbose (imports), redundant with content (comment, type), or
// purely internal bookkeeping (chunk index, language, go type kind).
var defaultCodeSearchExcludedMetadataKeys = []string{
	"trpc_ast_imports",
	"trpc_ast_import_count",
	"trpc_ast_language",
	"trpc_ast_go_type_kind",
	"trpc_ast_comment",
	"trpc_ast_type",
	"trpc_agent_go_chunk_index",
}

// CodeRepoInfo contains repository name and description for the agent.
type CodeRepoInfo struct {
	Name        string
	Description string
}

// CodeSearchOption configures the code search tool.
type CodeSearchOption func(*codeSearchOptions)

type codeSearchOptions struct {
	toolName                  string
	toolDescription           string
	staticFilter              map[string]any
	conditionedFilter         *searchfilter.UniversalFilterCondition
	maxResults                int
	minScore                  float64
	repoInfos                 []CodeRepoInfo
	extraFields               map[string][]any
	extraExcludeMetadataKeys  []string
	dedupEnabled              bool
	maxDedupKeysPerInvocation int
}

type sourceProvider interface {
	Sources() []source.Source
}

type repoDescriptorProvider interface {
	RepositoryDescriptor() (name, description string, ok bool)
}

// CodeEntityTypes contains all available entity types for filtering.
var CodeEntityTypes = []any{
	"Function",
	"Method",
	"Struct",
	"Interface",
	"Variable",
	"Alias",
	"Package",
	"Class",
	"Module",
	"Namespace",
	"Template",
	"Enum",
	"Service",
	"RPC",
	"Message",
	"enum",
	"service",
	"rpc",
	"message",
}

// CodeScopeTypes contains all available search scope types.
var CodeScopeTypes = []any{
	"code",
	"example",
}

// WithCodeSearchToolName sets the name of the code search tool.
func WithCodeSearchToolName(name string) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.toolName = name
	}
}

// WithCodeSearchToolDescription sets the description of the code search tool.
func WithCodeSearchToolDescription(description string) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.toolDescription = description
	}
}

// WithCodeSearchFilter sets a static metadata filter always applied to code search.
func WithCodeSearchFilter(filter map[string]any) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.staticFilter = filter
	}
}

// WithCodeSearchConditionedFilter sets a static complex filter always applied to code search.
func WithCodeSearchConditionedFilter(filter *searchfilter.UniversalFilterCondition) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.conditionedFilter = filter
	}
}

// WithCodeSearchMaxResults sets the maximum number of code search results.
func WithCodeSearchMaxResults(maxResults int) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.maxResults = maxResults
	}
}

// WithCodeSearchMinScore sets the minimum relevance score threshold.
func WithCodeSearchMinScore(minScore float64) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.minScore = minScore
	}
}

// WithCodeSearchRepoInfos sets the available repositories with descriptions.
func WithCodeSearchRepoInfos(infos []CodeRepoInfo) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.repoInfos = infos
	}
}

// WithCodeSearchExtraFilterFields adds extra filter fields to the tool.
//
// Note: keys passed here are merged on top of the built-in agentic filter
// info (which already exposes metadata.trpc_ast_repo_name and other AST
// fields). If a key here collides with a built-in one (for example
// "metadata.trpc_ast_repo_name"), the value supplied via this option will
// override the built-in value. A warning is logged in that case to avoid
// silent misconfiguration; callers that intentionally want to customize the
// set of repository names may ignore the warning.
func WithCodeSearchExtraFilterFields(fields map[string][]any) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.extraFields = fields
	}
}

// WithCodeSearchExtraExcludeMetadataKeys appends extra metadata keys to be
// stripped from each returned document, on top of the code-search default
// exclusion list (imports, language, chunk index, etc.).
func WithCodeSearchExtraExcludeMetadataKeys(keys ...string) CodeSearchOption {
	return func(o *codeSearchOptions) {
		if len(keys) == 0 {
			return
		}
		o.extraExcludeMetadataKeys = append(o.extraExcludeMetadataKeys, keys...)
	}
}

// WithCodeSearchDedup toggles invocation-scoped deduplication of code search
// results. When enabled (the default), documents already returned by a
// previous tool call within the same invocation are filtered out, so that
// multi-round tool use within a single user turn does not keep re-serving the
// same AST chunk to the LLM. Deduplication state is stored on the invocation's
// runtime state, so it is automatically scoped to a single user turn.
func WithCodeSearchDedup(enabled bool) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.dedupEnabled = enabled
	}
}

// WithCodeSearchMaxDedupKeysPerInvocation overrides the per-invocation cap on
// unique code chunk keys tracked by the dedup store. When n <= 0, the
// built-in default (defaultMaxDedupKeysPerInvocation) is used. Increasing the
// cap lets a single user turn surface more distinct chunks before eviction,
// at the cost of additional memory per in-flight invocation.
func WithCodeSearchMaxDedupKeysPerInvocation(n int) CodeSearchOption {
	return func(o *codeSearchOptions) {
		o.maxDedupKeysPerInvocation = n
	}
}

// NewCodeSearchTool creates a code-oriented search tool by reusing the generic
// agentic filter search flow and exposing AST metadata fields to the model.
func NewCodeSearchTool(kb knowledge.Knowledge, opts ...CodeSearchOption) tool.Tool {
	o := &codeSearchOptions{
		toolName:     defaultCodeSearchToolName,
		maxResults:   defaultMaxResults,
		minScore:     defaultCodeSearchMinScore,
		dedupEnabled: true,
	}
	for _, opt := range opts {
		opt(o)
	}
	if len(o.repoInfos) == 0 {
		o.repoInfos = deriveCodeRepoInfos(kb)
	}

	agenticFilterInfo := map[string][]any{
		"metadata.trpc_ast_type":           CodeEntityTypes,
		"metadata.trpc_ast_scope":          CodeScopeTypes,
		"content":                          {},
		"metadata.trpc_agent_go_source_id": {},
		"metadata.trpc_ast_full_name":      {},
		"metadata.trpc_ast_package":        {},
		"metadata.trpc_ast_file_path":      {},
		"metadata.trpc_ast_signature":      {},
	}

	if len(o.repoInfos) > 0 {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = codeRepoNamesToAnySlice(o.repoInfos)
	} else {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = []any{}
	}

	for k, v := range o.extraFields {
		if _, collides := agenticFilterInfo[k]; collides {
			log.Warnf("code_search: extra filter field %q overrides the built-in entry; "+
				"make sure this is intentional (e.g. for metadata.trpc_ast_repo_name)", k)
		}
		agenticFilterInfo[k] = v
	}

	description := o.toolDescription
	if description == "" {
		description = codeSearchToolDescription
		if len(o.repoInfos) > 0 {
			description += buildCodeRepoSection(o.repoInfos)
		}
	}

	wrappedOpts := []Option{
		WithToolName(o.toolName),
		WithToolDescription(description),
		WithMaxResults(o.maxResults),
		WithMinScore(o.minScore),
		WithExcludeMetadataKeys(defaultCodeSearchExcludedMetadataKeys...),
	}
	if len(o.extraExcludeMetadataKeys) > 0 {
		wrappedOpts = append(wrappedOpts, WithExcludeMetadataKeys(o.extraExcludeMetadataKeys...))
	}
	if o.staticFilter != nil {
		wrappedOpts = append(wrappedOpts, WithFilter(o.staticFilter))
	}
	if o.conditionedFilter != nil {
		wrappedOpts = append(wrappedOpts, WithConditionedFilter(o.conditionedFilter))
	}
	if o.dedupEnabled {
		dedup := newCodeDedupStoreWithCap(o.maxDedupKeysPerInvocation)
		wrappedOpts = append(wrappedOpts, WithResultPostProcessor(dedup.filter))
	}

	return NewAgenticFilterSearchTool(kb, agenticFilterInfo, wrappedOpts...)
}

// NewCodeGraphSearchTool creates a code-oriented graph tool set.
//
// When used through llmagent.WithToolSets, the exposed tool names become
// code_graph_search, code_graph_traverse, and code_graph_find_paths. The search
// tool locates AST symbol nodes, while traverse and find_paths use graph edges
// such as CALLS, METHOD, FIELD, PARAM, RETURNS, ALIAS_OF, IMPLEMENTS, and
// CONTAINS to inspect local code relationships.
func NewCodeGraphSearchTool(kb knowledge.GraphKnowledge, opts ...CodeSearchOption) tool.ToolSet {
	o := &codeSearchOptions{
		toolName:     defaultCodeGraphSearchToolName,
		maxResults:   defaultCodeGraphSearchMaxResults,
		minScore:     defaultCodeGraphSearchMinScore,
		dedupEnabled: true,
	}
	for _, opt := range opts {
		opt(o)
	}
	if len(o.repoInfos) == 0 {
		o.repoInfos = deriveCodeRepoInfos(kb)
	}

	description := o.toolDescription
	if description == "" {
		description = codeGraphSearchToolDescription
		if len(o.repoInfos) > 0 {
			description += buildCodeRepoSection(o.repoInfos)
		}
	}

	wrappedSearchOpts := []Option{
		WithToolName(graphSearchToolName),
		WithToolDescription(description),
		WithMaxResults(o.maxResults),
		WithMinScore(o.minScore),
		WithExcludeMetadataKeys(defaultCodeSearchExcludedMetadataKeys...),
	}
	if len(o.extraExcludeMetadataKeys) > 0 {
		wrappedSearchOpts = append(wrappedSearchOpts, WithExcludeMetadataKeys(o.extraExcludeMetadataKeys...))
	}
	if o.staticFilter != nil {
		wrappedSearchOpts = append(wrappedSearchOpts, WithFilter(o.staticFilter))
	}
	if o.conditionedFilter != nil {
		wrappedSearchOpts = append(wrappedSearchOpts, WithConditionedFilter(o.conditionedFilter))
	}
	if o.dedupEnabled {
		dedup := newCodeDedupStoreWithCap(o.maxDedupKeysPerInvocation)
		wrappedSearchOpts = append(wrappedSearchOpts, WithResultPostProcessor(dedup.filter))
	}

	setName := strings.TrimSpace(o.toolName)
	if setName == "" {
		setName = defaultCodeGraphSearchToolName
	}
	return &graphToolSet{
		name: setName,
		tools: []tool.Tool{
			NewAgenticFilterSearchTool(kb, codeSearchAgenticFilterInfo(o.repoInfos, o.extraFields), wrappedSearchOpts...),
			NewGraphTraverseTool(kb,
				WithGraphToolName(graphTraverseToolName),
				WithGraphToolDescription(codeGraphTraverseToolDescription),
			),
			NewGraphFindPathsTool(kb,
				WithGraphToolName(graphFindPathsToolName),
				WithGraphToolDescription(codeGraphFindPathsToolDescription),
			),
		},
	}
}

func codeSearchAgenticFilterInfo(repoInfos []CodeRepoInfo, extraFields map[string][]any) map[string][]any {
	agenticFilterInfo := map[string][]any{
		"metadata.trpc_ast_type":           CodeEntityTypes,
		"metadata.trpc_ast_scope":          CodeScopeTypes,
		"metadata.kind":                    {"code"},
		"content":                          {},
		"metadata.trpc_agent_go_source_id": {},
		"metadata.trpc_ast_full_name":      {},
		"metadata.trpc_ast_package":        {},
		"metadata.trpc_ast_file_path":      {},
		"metadata.trpc_ast_signature":      {},
	}
	if len(repoInfos) > 0 {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = codeRepoNamesToAnySlice(repoInfos)
	} else {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = []any{}
	}
	for k, v := range extraFields {
		if _, collides := agenticFilterInfo[k]; collides {
			log.Warnf("code_graph_search: extra filter field %q overrides the built-in entry; "+
				"make sure this is intentional (e.g. for metadata.trpc_ast_repo_name)", k)
		}
		agenticFilterInfo[k] = v
	}
	return agenticFilterInfo
}

func buildCodeRepoSection(infos []CodeRepoInfo) string {
	var sb strings.Builder
	sb.WriteString("\n\n== AVAILABLE REPOSITORIES ==\n\n")
	for _, info := range infos {
		if strings.TrimSpace(info.Description) == "" {
			sb.WriteString(fmt.Sprintf("- %s\n", info.Name))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", info.Name, info.Description))
	}
	return sb.String()
}

func codeRepoNamesToAnySlice(infos []CodeRepoInfo) []any {
	result := make([]any, len(infos))
	for i, info := range infos {
		result[i] = info.Name
	}
	return result
}

func deriveCodeRepoInfos(kb knowledge.Knowledge) []CodeRepoInfo {
	provider, ok := kb.(sourceProvider)
	if !ok {
		return nil
	}
	sources := provider.Sources()
	if len(sources) == 0 {
		return nil
	}
	infos := make([]CodeRepoInfo, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		repoSrc, ok := src.(repoDescriptorProvider)
		if !ok {
			continue
		}
		name, description, ok := repoSrc.RepositoryDescriptor()
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		infos = append(infos, CodeRepoInfo{
			Name:        name,
			Description: strings.TrimSpace(description),
		})
	}
	return infos
}

// codeSearchToolDescription is the single source of truth for the natural
// language prompt shown to the LLM by the code search tool. It is
// intentionally kept in sync with trpc-ast-rag/prompt_en.txt; when updating
// one, please update the other in the same change to avoid behavior drift
// between the standalone RAG prompt and the tool description embedded here.
const codeSearchToolDescription = `Search for code entities in the knowledge base using hybrid search.
Code in the knowledge base is parsed via AST (Abstract Syntax Tree), where each code block corresponds to a complete semantic unit (function, method, struct, class, interface, etc.) with structured metadata (signature, comment, package path, file location, etc.).
The knowledge base may also contain example code, and it may also contain data from other sources that do not have AST metadata labels.

== SEARCH PRIORITY ==

1. Narrow the search space only when it helps:
   {"operator": "and", "value": [
     {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "your-repo"},
     {"field": "metadata.trpc_ast_scope", "operator": "eq", "value": "code"}
   ]}

   Use repo_name when repository metadata exists and you need to restrict the search to one repo.
   Use scope="code" when you want AST-parsed implementation code only.
   Use scope="example" only when the user explicitly asks for examples or usage snippets.
   If you want to keep compatibility with other unlabelled sources, do not add a scope filter.

2. Choose search mode:
   - Semantic search (query): describe functionality in natural language
   - Filter search: match exact names, code patterns, metadata fields
   - Combined (recommended): query + filter for best results

== IMPORTANT: scope selection ==

- scope="code" means AST-parsed implementation code.
- scope="example" means content recognized as example/example-style code.
- Some sources may not have any trpc_ast_scope label at all. Do not force scope unless you specifically need to narrow to AST-labelled code or examples.

== HOW TO CHOOSE FILTERS ==

Do not blindly add many fields. Choose filters based on the user's intent:

1. To narrow the search space:
- metadata.trpc_ast_repo_name: repository name
- metadata.trpc_ast_scope: code, example
- metadata.trpc_ast_type: Function, Method, Struct, Interface, Class, Variable, Alias, Package, Namespace, Template, Enum, Service, RPC, Message, enum, service, rpc, message

2. To find an exact symbol or declaration:
- metadata.trpc_ast_full_name: exact fully-qualified symbol name
- metadata.trpc_ast_package: package or module path
- metadata.trpc_ast_file_path: file path

3. To search by semantic meaning:
- Use query first.
- Semantic retrieval is driven by AST-derived structured text, mainly symbol identity and documentation-oriented fields such as name, full_name, package, signature, and comment.
- This works well for “what code handles X”, “where is Y implemented”, “which method initializes Z”, and similar intent-level questions.

4. To search literal code, error strings, logs, or API call snippets:
- Use content with like.
- Important: embedding text does NOT contain the full raw code body. Exact error text, log text, SQL fragments, HTTP paths, and concrete API calls may only exist in content.
- Therefore, when the user asks about a specific error message or exact code fragment, use content + like instead of relying on semantic query alone.

Examples:
- Search for a concrete API call:
  {"filter": {"operator": "and", "value": [
    {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "trpc-go"},
    {"field": "content", "operator": "like", "value": "%context.WithTimeout%"}
  ]}}

- Search for a concrete error string:
  {"filter": {"operator": "and", "value": [
    {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "trpc-go"},
    {"field": "content", "operator": "like", "value": "%connection refused%"}
  ]}}

Useful supporting fields:
- metadata.trpc_ast_signature: function or method signature

== BEST PRACTICES ==

1. Prefer combined search (query + filter) for the best recall and precision.
2. For exact symbol lookup, use metadata.trpc_ast_full_name with eq.
3. For code pattern search, exact error text, or literal snippets, use content with like.
4. Add metadata.trpc_ast_scope only when you intentionally want only AST-labelled code or example results.
5. Call this tool multiple times with different filters when you need broad coverage across repos or symbol categories.

== QUERY GUIDELINES ==

When you issue a natural-language query, think of it as a full question you would ask a human expert, not a bag of keywords. The following style differences matter a lot for retrieval quality:

Good queries (specific, intent-level, full sentence):
- "Where is interface Knowledge implemented in the default package?"
- "How does the runner propagate session state across sub-agents?"
- "What happens when we encrypt user credentials before persisting them?"
- "Which function registers MCP tools on the streamable HTTP transport?"

Bad queries (too short, keyword-only, or ambiguous):
- "Knowledge"                          // single symbol: use filter on trpc_ast_full_name instead
- "session isolation"                  // two keywords: use a full question like "how is session state isolated between parallel sub-agents?"
- "error handling"                     // generic: add the component, e.g. "how are tool-call errors surfaced back to the LLM in runner?"
- "config"                             // meaningless alone

== MULTI-CALL STRATEGY ==

This tool keeps track of the AST chunks it has already returned within the current user turn and will NOT return the same chunk twice. That means:

1. If you need more context after the first call, you MUST vary the query, not just rephrase it with synonyms. Change the angle: ask about callers, about the struct definition, about the interface it implements, about an adjacent package, etc.
2. Split broad questions into focused sub-queries instead of repeating one big query. For example, when asked "how does X work", run separate calls for:
   - "Where is X defined and what is its public API?"
   - "Who calls X and in what order?"
   - "How is X configured or initialized at startup?"
3. When the tool responds with a message saying all top results were already returned, do NOT call it again with a near-identical query. Either pick a different angle (different symbol / package / scope filter) or stop searching.
4. Prefer issuing a small number of well-differentiated queries (2-4) over many similar ones.`

const codeGraphSearchToolDescription = `Search code graph nodes in an AST-backed code knowledge graph.
Use this tool first when you need graph reasoning over a codebase: callers/callees, struct methods, interface implementations, type dependencies, or paths between two code entities. Each returned document has an "id" field containing the generated graph node ID. Pass those IDs to code_graph_traverse.start_ids or code_graph_find_paths.from_id/to_id when you need relationships, not just matching code text. The original repository source key is stored in metadata.trpc_agent_go_source_id.

== CODE GRAPH WORKFLOW ==

1. Start with code_graph_search to find precise seed nodes.
   - Prefer a full natural-language query for semantic lookup, for example "where is the client request encoded before RPC send".
   - Use metadata filters when the user names an exact symbol, package, file, repo, or entity type.
   - Use content with like only for literal snippets, exact error strings, API names, or log text.

2. Use code_graph_traverse when the question asks for local relationships around one or more seed nodes.
   - "Who calls this function?" => direction "in", edge_types ["CALLS"].
   - "What does this function call?" => direction "out", edge_types ["CALLS"].
   - "What methods belong to this struct/interface?" => direction "out", edge_types ["METHOD"].
   - "What fields does this struct contain?" => direction "out", edge_types ["FIELD"].
   - "What are the parameter or return type dependencies?" => direction "out", edge_types ["PARAM", "RETURNS"].
   - "Which types implement this interface?" => try direction "in" or "both", edge_types ["IMPLEMENTS"].
   - Use max_depth=1 for focused answers. Increase depth only when the user asks for a wider dependency/call chain.

3. Use code_graph_find_paths when the user asks how two code entities are connected.
   - Resolve each endpoint with code_graph_search first unless you already have exact graph node IDs.
   - Use edge_types to constrain the path when the relationship is known, for example ["CALLS"] for call paths.
   - Keep max_depth modest first, such as 3-5, and increase only if no path is found.

== EDGE TYPES ==

- CALLS: function or method call relationship.
- METHOD: type to method relationship.
- FIELD: type to field relationship.
- PARAM: function/method to parameter type relationship.
- RETURNS: function/method to return type relationship.
- ALIAS_OF: alias type relationship.
- IMPLEMENTS: implementation relationship between concrete type and interface.
- CONTAINS: AST/code containment relationship when available.

== FILTER GUIDANCE ==

Use these metadata fields to make seed search precise:
- metadata.trpc_agent_go_source_id: original repository source key, formatted like repo:<repo-url-or-name>#<revision>#symbol:<fully-qualified-symbol>. Use "like" on this field for exact repo or symbol constraints.
- metadata.trpc_ast_scope: code or example. Use code for implementation, example only when the user asks for examples.
- metadata.trpc_ast_type: Function, Method, Struct, Interface, Variable, Alias, Package, Class, Module, Namespace, Template, Enum, Service, RPC, Message.
- metadata.trpc_ast_package: package or module path.
- metadata.trpc_ast_file_path: repository-relative file path.
- metadata.trpc_ast_signature: function/type signature.
- content: raw code content for literal matching.

Examples:
- Find exact method:
  {"query": "", "filter": {"field": "metadata.trpc_agent_go_source_id", "operator": "like", "value": "%#symbol:example.com/project.Client.Do"}}
- Find code in a file:
  {"query": "client request encoding", "filter": {"field": "metadata.trpc_ast_file_path", "operator": "eq", "value": "client/codec.go"}}
- Find interface implementations:
  First search the interface by source_id, package/name, or query, then traverse with direction "in" or "both" and edge_types ["IMPLEMENTS"].

== IMPORTANT ==

Do not guess graph node IDs. Search first, then use returned IDs for traversal/path queries.
Do not traverse all edge types unless the user asks for broad context. Pick edge types based on the question.
For exact code strings, use content like rather than semantic query alone.
For broad architectural questions, split the work: find main symbols, traverse relevant relationships, then summarize only grounded nodes and edges.`

const codeGraphTraverseToolDescription = `Traverse an AST-backed code graph from known node IDs or from nodes resolved by query/filter.
Use this after code_graph_search when the user asks about local code relationships such as callers, callees, methods, fields, type dependencies, or implementations.

Direction and edge type guide:
- callers: direction "in", edge_types ["CALLS"].
- callees/dependencies called by a function: direction "out", edge_types ["CALLS"].
- methods of a type: direction "out", edge_types ["METHOD"].
- fields of a type: direction "out", edge_types ["FIELD"].
- parameter and return type dependencies: direction "out", edge_types ["PARAM", "RETURNS"].
- interface implementations: direction "in" or "both", edge_types ["IMPLEMENTS"].

Prefer max_depth=1 for precise local context. Use max_depth=2 only for a short chain, and keep max_nodes bounded so the result stays explainable.`

const codeGraphFindPathsToolDescription = `Find paths between two AST-backed code graph nodes.
Use this when the user asks how two functions/types/packages are connected, why one symbol depends on another, or what call/type path links them.

Resolve endpoints with code_graph_search first unless exact graph node IDs are already known. Use edge_types to constrain the relationship:
- ["CALLS"] for call paths.
- ["METHOD", "CALLS"] for type-to-method-to-call flows.
- ["PARAM", "RETURNS", "FIELD", "ALIAS_OF"] for type dependency paths.
- ["IMPLEMENTS"] for interface implementation paths.

Start with max_depth 3-5 and max_paths 3-10. If no path is found, broaden direction to "both" or relax edge_types before increasing depth.`
