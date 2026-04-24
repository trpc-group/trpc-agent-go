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
	defaultCodeSearchToolName = "code_search"
	defaultCodeSearchMinScore = 0.1
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
		"metadata.trpc_ast_type":      CodeEntityTypes,
		"metadata.trpc_ast_scope":     CodeScopeTypes,
		"content":                     {},
		"metadata.trpc_ast_full_name": {},
		"metadata.trpc_ast_package":   {},
		"metadata.trpc_ast_file_path": {},
		"metadata.trpc_ast_signature": {},
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
