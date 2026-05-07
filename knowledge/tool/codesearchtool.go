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

	agenticFilterInfo := codeSearchAgenticFilterInfo(o.repoInfos, o.extraFields)

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
// When used through llmagent.WithToolSets, the exposed tool names are derived
// from the set name (default "code_graph") by appending _search, _traverse,
// and _find_paths. For example, with the default set name, the tools are
// code_graph_search, code_graph_traverse, and code_graph_find_paths. The search
// tool locates AST symbol nodes, while traverse and find_paths use graph edges
// such as CALLS, METHOD, FIELD, PARAM, RETURNS, ALIAS_OF, IMPLEMENTS, and
// CONTAINS to inspect local code relationships.
//
// Cross-tool references inside descriptions (e.g. "call code_graph_traverse")
// are automatically adjusted when the set name is customized via
// WithCodeSearchToolName.
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

	setName := strings.TrimSpace(o.toolName)
	if setName == "" {
		setName = defaultCodeGraphSearchToolName
	}
	resolver := newCodeGraphNameResolver(setName)

	description := o.toolDescription
	if description == "" {
		description = resolver.resolve(codeGraphSearchToolDescription)
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

	return &graphToolSet{
		name: setName,
		tools: []tool.Tool{
			NewAgenticFilterSearchTool(kb, codeSearchAgenticFilterInfo(o.repoInfos, o.extraFields), wrappedSearchOpts...),
			NewGraphTraverseTool(kb,
				WithGraphToolName(graphTraverseToolName),
				WithGraphToolDescription(resolver.resolve(codeGraphTraverseToolDescription)),
			),
			NewGraphFindPathsTool(kb,
				WithGraphToolName(graphFindPathsToolName),
				WithGraphToolDescription(resolver.resolve(codeGraphFindPathsToolDescription)),
			),
		},
	}
}

func codeSearchAgenticFilterInfo(repoInfos []CodeRepoInfo, extraFields map[string][]any) map[string][]any {
	agenticFilterInfo := map[string][]any{
		"metadata.trpc_ast_type":      CodeEntityTypes,
		"metadata.trpc_ast_scope":     CodeScopeTypes,
		"content":                     {},
		"metadata.trpc_ast_full_name": {},
		"metadata.trpc_ast_package":   {},
		"metadata.trpc_ast_file_path": {},
		"metadata.trpc_ast_signature": {},
	}
	if len(repoInfos) > 0 {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = codeRepoNamesToAnySlice(repoInfos)
	} else {
		agenticFilterInfo["metadata.trpc_ast_repo_name"] = []any{}
	}
	for k, v := range extraFields {
		if _, collides := agenticFilterInfo[k]; collides {
			log.Warnf("code search: extra filter field %q overrides the built-in entry; "+
				"make sure this is intentional (e.g. for metadata.trpc_ast_repo_name)", k)
		}
		agenticFilterInfo[k] = v
	}
	return agenticFilterInfo
}

// codeGraphNameResolver replaces {{SEARCH_TOOL}}, {{TRAVERSE_TOOL}}, and
// {{FIND_PATHS_TOOL}} placeholders in description templates with the actual
// tool names derived from the ToolSet name.
type codeGraphNameResolver struct {
	replacer *strings.Replacer
}

func newCodeGraphNameResolver(setName string) *codeGraphNameResolver {
	return &codeGraphNameResolver{
		replacer: strings.NewReplacer(
			"{{SEARCH_TOOL}}", setName+"_"+graphSearchToolName,
			"{{TRAVERSE_TOOL}}", setName+"_"+graphTraverseToolName,
			"{{FIND_PATHS_TOOL}}", setName+"_"+graphFindPathsToolName,
		),
	}
}

func (r *codeGraphNameResolver) resolve(template string) string {
	return r.replacer.Replace(template)
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

3. Content return:
   - include_content controls whether full document content is returned.
   - Default is true for this search tool, so returned results include code/document content unless you set include_content=false.
   - Set include_content=false when you only need IDs, names, metadata, scores, or when the content would be too verbose.

== IMPORTANT: scope selection ==

- scope="code" means AST-parsed implementation code.
- scope="example" means content recognized as example/example-style code.
- scope="" or leaving scope unset means search all available content, including AST-labelled code, Markdown files in code sources, and user-added documents. Add scope only when you explicitly need to restrict results to AST-labelled code or examples.

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
- Important: embedding text is built from structured semantic fields and does NOT contain the concrete code logic or full raw code body. Exact error text, log text, SQL fragments, HTTP paths, branch logic, and concrete API calls may only exist in content.
- Therefore, when the user asks about a specific error message, exact code fragment, or concrete implementation logic, use content + like instead of relying on semantic query alone.

Examples:
- Search for a concrete API call:
  {"filter": {"operator": "and", "value": [
    {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "trpc-agent-go"},
    {"field": "content", "operator": "like", "value": "%context.WithTimeout%"}
  ]}}

- Search for a concrete error string:
  {"filter": {"operator": "and", "value": [
    {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "trpc-agent-go"},
    {"field": "content", "operator": "like", "value": "%connection refused%"}
  ]}}

Useful supporting fields:
- metadata.trpc_ast_signature: function or method signature

== BEST PRACTICES ==

1. Prefer combined search (query + filter) for the best recall and precision.
2. For code pattern search, exact error text, or literal snippets, use content with like.
3. Add metadata.trpc_ast_scope only when you intentionally want only AST-labelled code or example results.
4. Call this tool multiple times with different filters when you need broad coverage across repos or symbol categories.

== MULTI-CALL STRATEGY ==

This tool keeps track of the AST chunks it has already returned within the current user turn and will NOT return the same chunk twice. That means:

1. If you need more context after the first call, you MUST vary the query, not just rephrase it with synonyms. Change the angle: ask about callers, about the struct definition, about the interface it implements, about an adjacent package, etc.
2. Split broad questions into focused sub-queries instead of repeating one big query. For example, when asked "how does X work", run separate calls for:
   - "Where is X defined and what is its public API?"
   - "Who calls X and in what order?"
   - "How is X configured or initialized at startup?"
3. When the tool responds with a message saying all top results were already returned, do NOT call it again with a near-identical query. Either pick a different angle (different symbol / package / scope filter) or stop searching.
4. Prefer issuing a small number of well-differentiated queries (2-4) over many similar ones.`

// codeGraphSearchToolDescription uses {{SEARCH_TOOL}}, {{TRAVERSE_TOOL}},
// and {{FIND_PATHS_TOOL}} placeholders that are resolved at runtime by
// codeGraphNameResolver so that cross-tool references match the actual
// ToolSet name.
const codeGraphSearchToolDescription = `Search for code graph nodes using hybrid search (semantic query, metadata filter, or both).
Each node is an AST-parsed code entity (function, method, struct, class, interface, etc.) with structured metadata.

Graph note: returned documents carry an "id" field — the graph node ID. Pass those IDs to {{TRAVERSE_TOOL}} or {{FIND_PATHS_TOOL}} for relationship queries.

== WHAT THIS TOOL IS NOT ==

This tool searches indexed AST symbol nodes. It is NOT a filesystem browser and CANNOT enumerate the repository tree, list directory contents, or show all files in a package.
Do NOT fabricate a directory tree from metadata.trpc_ast_file_path values — that only surfaces files with indexed nodes, never the full tree.

To find AST symbols in a known file or package, use metadata.trpc_ast_file_path or metadata.trpc_ast_package filters.

== HOW TO SEARCH ==

1. Semantic search (query): natural-language intent — "what code handles X", "where is Y implemented". Embedding text is built from AST fields (name, signature, comment, package), NOT from raw code bodies.
2. Filter search: exact names, patterns, metadata fields.
3. Combined (recommended): query + filter for best recall and precision.
4. Literal code text (error strings, logs, SQL, API calls): use content with like — embeddings do not cover raw code.
   Example: {"filter": {"operator": "and", "value": [
     {"field": "metadata.trpc_ast_repo_name", "operator": "eq", "value": "trpc-agent-go"},
     {"field": "content", "operator": "like", "value": "%context.WithTimeout%"}
   ]}}

Scope: scope="code" for AST-parsed code only; scope="example" for example snippets; omit scope to search all content.
Content: include_content defaults to true; set false when you only need node IDs/metadata for subsequent traversal.

== IMPORTANT ==

1. Do not guess graph node IDs. Search first, then use returned IDs for traversal/path queries.
2. When the user asks about callers, callees, dependencies, implementations, or paths between symbols, resolve node IDs here first, then call {{TRAVERSE_TOOL}} or {{FIND_PATHS_TOOL}}.

== MULTI-CALL STRATEGY ==

This tool keeps track of the AST chunks it has already returned within the current user turn and will NOT return the same chunk twice. That means:

1. If you need more context after the first call, you MUST vary the query — do not just rephrase with synonyms. Change the angle: ask about callers, about the struct definition, about the interface it implements, about an adjacent package, etc. Or better, use the returned node IDs with {{TRAVERSE_TOOL}} to explore relationships directly.
2. Split broad questions into focused sub-queries instead of repeating one big query.
3. When the tool responds saying all top results were already returned, do NOT call it again with a near-identical query. Either pick a different angle (different symbol / package / scope filter) or stop searching and use {{TRAVERSE_TOOL}} / {{FIND_PATHS_TOOL}} on the IDs you already have.
4. Prefer issuing a small number of well-differentiated queries (2-4) over many similar ones.`

const codeGraphTraverseToolDescription = `Traverse an AST-backed code graph from known node IDs to inspect local relationships such as callers, callees, methods, fields, type dependencies, or interface implementations.

== INPUT MODEL — READ THIS FIRST ==

This tool ONLY accepts known graph node IDs in start_ids. It does NOT accept query, filter, names, package paths, or natural-language descriptions. There is no query/filter parameter on this tool.

If start_ids are unknown, call {{SEARCH_TOOL}} FIRST (with metadata.trpc_ast_full_name, metadata.trpc_ast_package, metadata.trpc_ast_file_path, etc.) and pass the returned node IDs as start_ids.

The traversal is shaped ONLY by start_ids, edge_types, direction, max_depth, and max_nodes. To restrict the output, choose more specific seeds (search with a tighter filter first, e.g. metadata.trpc_ast_type) or tighten edge_types / max_depth / max_nodes. Returned nodes carry their own metadata, so the caller can also ignore unwanted entries after the fact.

== WHAT THIS TOOL IS NOT ==

- NOT a search tool. It does NOT accept query text, filter conditions, symbol names, or package paths. If you do not already have concrete graph node IDs, call {{SEARCH_TOOL}} FIRST and use the returned IDs as start_ids.
- NOT a directory walker. Do not use it to list files under a directory or members of a package. To enumerate AST symbols inside a known package or file, use {{SEARCH_TOOL}} with metadata.trpc_ast_package or metadata.trpc_ast_file_path filters instead.

== EDGE TYPE GLOSSARY ==

- CALLS: function/method invocation. A --CALLS--> B means A calls B.
- METHOD: type-to-method declaration. Struct --METHOD--> its method.
- FIELD: type-to-field declaration. Struct --FIELD--> its field.
- PARAM: function/method parameter type dependency. Func --PARAM--> ParamType.
- RETURNS: function/method return type dependency. Func --RETURNS--> ReturnType.
- ALIAS_OF: type alias target. AliasType --ALIAS_OF--> UnderlyingType.
- IMPLEMENTS: interface implementation. ConcreteType --IMPLEMENTS--> Interface.
- CONTAINS: structural containment (e.g. package contains top-level declarations, or a nested scope). Prefer {{SEARCH_TOOL}} filters over CONTAINS traversal for package/file enumeration.

== EXAMPLES ==

Find who calls a function (callers, one hop):
  {"start_ids": ["<func-id>"], "edge_types": ["CALLS"], "direction": "in", "max_depth": 1}

Find what a function calls (callees, one hop):
  {"start_ids": ["<func-id>"], "edge_types": ["CALLS"], "direction": "out", "max_depth": 1}

Short call chain downstream of a function (two hops):
  {"start_ids": ["<func-id>"], "edge_types": ["CALLS"], "direction": "out", "max_depth": 2}

List a struct's methods:
  {"start_ids": ["<struct-id>"], "edge_types": ["METHOD"], "direction": "out", "max_depth": 1}

List a struct's fields:
  {"start_ids": ["<struct-id>"], "edge_types": ["FIELD"], "direction": "out", "max_depth": 1}

A function's parameter and return type dependencies:
  {"start_ids": ["<func-id>"], "edge_types": ["PARAM", "RETURNS"], "direction": "out", "max_depth": 1}

Concrete implementations of an interface (and the reverse — interfaces a type implements):
  {"start_ids": ["<iface-id>"], "edge_types": ["IMPLEMENTS"], "direction": "in", "max_depth": 1}
  {"start_ids": ["<type-id>"], "edge_types": ["IMPLEMENTS"], "direction": "out", "max_depth": 1}

Mixed local context around an entity (methods + fields one hop out):
  {"start_ids": ["<struct-id>"], "edge_types": ["METHOD", "FIELD"], "direction": "out", "max_depth": 1}

Multiple seeds at once (e.g. several methods returned by an earlier search):
  {"start_ids": ["<id-1>", "<id-2>"], "edge_types": ["CALLS"], "direction": "out", "max_depth": 1}

== PARAMETERS ==

- start_ids: required. Resolve via {{SEARCH_TOOL}} when not yet known.
- max_depth: 1 for precise local context; 2 for a short chain; avoid larger depths unless explicitly asked.
- max_nodes: keeps the result explainable.
- include_content: defaults to false; set true only when the code body is required.
  ** TWO-STEP STRATEGY (recommended): First call with include_content=false to discover the graph
  structure (node names, IDs, edges). Then make a targeted follow-up call with include_content=true
  on only the specific node IDs whose source code you need to read. This significantly reduces
  response size and token usage. Skip the two-step approach only when you already know the exact
  node IDs and are certain you need their code body immediately. **
- edge_types: see EDGE TYPE GLOSSARY above. Pick edge types by the question; do not traverse all types unless the user asks for broad context.

== RESULT GUIDANCE ==

The response contains nodes and edges. Interpret edges by from_id, to_id, and type. Ground the answer in returned node names, metadata, and edge directions. Do not invent relationships that are not present in the returned edges.`

const codeGraphFindPathsToolDescription = `Find paths between two AST-backed code graph nodes — i.e., the chain of edges that connects from_id to to_id (e.g., how function A reaches function B, or why type T depends on type U).

== INPUT MODEL — READ THIS FIRST ==

This tool ONLY accepts exact graph node IDs as from_id and to_id. It does NOT accept query, filter, names, full names, package paths, or natural-language descriptions, and it does NOT have any filter parameter at all.

If from_id or to_id is unknown, call {{SEARCH_TOOL}} FIRST and pass the returned IDs.

== WHEN NOT TO USE ==

Do NOT use this tool for open-ended exploration such as "show X's call chain", "who calls X", or "what does X call". Those are traversal questions — use {{TRAVERSE_TOOL}} instead.

This tool is also NOT a directory or package browser. Do not use it to list files in a directory or members of a package; that information is not encoded as a single from→to path.

== EXAMPLES ==

How function A reaches function B (direct or indirect call chain):
  {"from_id": "<a-id>", "to_id": "<b-id>", "edge_types": ["CALLS"], "direction": "out", "max_depth": 5, "max_paths": 3}

Broaden depth/paths if the first attempt finds nothing:
  {"from_id": "<a-id>", "to_id": "<b-id>", "edge_types": ["CALLS"], "direction": "out", "max_depth": 8, "max_paths": 5}

How B traces back to A (reverse call path):
  {"from_id": "<a-id>", "to_id": "<b-id>", "edge_types": ["CALLS"], "direction": "in", "max_depth": 5, "max_paths": 3}

Type-to-method-to-call flow (struct T → its method → some call):
  {"from_id": "<struct-id>", "to_id": "<func-id>", "edge_types": ["METHOD", "CALLS"], "direction": "out", "max_depth": 4, "max_paths": 5}

Why type X depends on type Y (parameter / return / field / alias paths):
  {"from_id": "<type-x-id>", "to_id": "<type-y-id>", "edge_types": ["PARAM", "RETURNS", "FIELD", "ALIAS_OF"], "direction": "out", "max_depth": 5, "max_paths": 5}

Concrete-type-to-interface implementation path:
  {"from_id": "<impl-id>", "to_id": "<iface-id>", "edge_types": ["IMPLEMENTS"], "direction": "out", "max_depth": 1, "max_paths": 3}

Direction unknown — broaden first, then increase depth if needed:
  {"from_id": "<a-id>", "to_id": "<b-id>", "edge_types": ["CALLS"], "direction": "both", "max_depth": 5, "max_paths": 5}

== HOW TO RESOLVE IDs WITH {{SEARCH_TOOL}} ==

When you do not yet have from_id / to_id, call {{SEARCH_TOOL}} first. Useful filter fields there:
- metadata.trpc_ast_full_name: exact functions/types/methods.
- metadata.trpc_ast_package: package/module paths.
- metadata.trpc_ast_file_path: repository-relative file paths.
- metadata.trpc_ast_repo_name, metadata.trpc_ast_scope, metadata.trpc_ast_type: narrow the endpoint search.
- content with like: exact strings in raw code/document content.

Pick concrete node IDs from the search results and pass them as from_id / to_id here. Do not make up IDs, and do not pass package paths or names as IDs.

== EDGE TYPE GLOSSARY ==

- CALLS: function/method invocation. A --CALLS--> B means A calls B.
- METHOD: type-to-method declaration. Struct --METHOD--> its method.
- FIELD: type-to-field declaration. Struct --FIELD--> its field.
- PARAM: function/method parameter type dependency. Func --PARAM--> ParamType.
- RETURNS: function/method return type dependency. Func --RETURNS--> ReturnType.
- ALIAS_OF: type alias target. AliasType --ALIAS_OF--> UnderlyingType.
- IMPLEMENTS: interface implementation. ConcreteType --IMPLEMENTS--> Interface.
- CONTAINS: structural containment (e.g. package contains top-level declarations).

== EDGE TYPE PICKER ==

- ["CALLS"] for call paths.
- ["METHOD", "CALLS"] for type -> method -> call flows.
- ["PARAM", "RETURNS", "FIELD", "ALIAS_OF"] for type dependency paths.
- ["IMPLEMENTS"] for interface implementation paths.

== SEARCH STRATEGY ==

- direction "out" when asking how A reaches B.
- direction "in" when asking how B traces back to A.
- direction "both" only when the dependency direction is unknown.
- Start with max_depth 3-5 and max_paths 3-10.
- If no path is found, broaden direction to "both" or relax edge_types before increasing depth.
- include_content defaults to false; set true only when the code body is required.

== RESULT GUIDANCE ==

Each returned path contains ordered nodes and edges. Explain the path as a chain of relationships using edge types and directions. If multiple paths are returned, prefer the shortest or most semantically direct path and mention when alternatives exist.`
