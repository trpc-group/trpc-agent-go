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
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const defaultMaxResults = 10

// KnowledgeSearchRequest represents the input for the knowledge search tool.
type KnowledgeSearchRequest struct {
	Query string `json:"query" jsonschema:"description=The search query to find relevant information in the knowledge base"`
}

// KnowledgeSearchResponse represents the response from the knowledge search tool.
type KnowledgeSearchResponse struct {
	Documents []*DocumentResult `json:"documents"`
	Message   string            `json:"message,omitempty"`
}

// DocumentResult represents a single document result with metadata and score.
type DocumentResult struct {
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    float64        `json:"score"`
}

// Option is a function that configures the knowledge search tool.
type Option func(*options)

type options struct {
	toolName          string
	toolDescription   string
	staticFilter      map[string]any
	conditionedFilter *searchfilter.UniversalFilterCondition
	maxResults        int
}

// WithToolName sets the name of the knowledge search tool.
func WithToolName(toolName string) Option {
	return func(opts *options) {
		opts.toolName = toolName
	}
}

// WithToolDescription sets the description of the knowledge search tool.
func WithToolDescription(toolDescription string) Option {
	return func(opts *options) {
		opts.toolDescription = toolDescription
	}
}

// WithFilter sets a static metadata filter (simple AND logic).
// Multiple key-value pairs are combined with AND.
// For OR/nested conditions, use WithConditionedFilter.
func WithFilter(filter map[string]any) Option {
	return func(opts *options) {
		opts.staticFilter = filter
	}
}

// WithConditionedFilter sets a static complex filter with OR/AND/nested logic.
// Supports operators: eq, ne, gt, gte, lt, lte, in, not in, like, not like, between, and, or.
// For simple AND-only filters, use WithFilter instead.
func WithConditionedFilter(filterCondition *searchfilter.UniversalFilterCondition) Option {
	return func(opts *options) {
		opts.conditionedFilter = filterCondition
	}
}

// WithMaxResults sets the maximum number of documents to return.
// Default is 10 if not specified.
func WithMaxResults(maxResults int) Option {
	return func(opts *options) {
		opts.maxResults = maxResults
	}
}

// NewKnowledgeSearchTool creates a function tool for knowledge search using
// the Knowledge interface.
// This tool allows agents to search for relevant information in the knowledge base.
func NewKnowledgeSearchTool(kb knowledge.Knowledge, opts ...Option) tool.Tool {
	opt := &options{
		maxResults: defaultMaxResults,
	}
	for _, o := range opts {
		o(opt)
	}
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
		if req.Query == "" {
			return nil, errors.New("query cannot be empty")
		}
		invocation, ok := agent.InvocationFromContext(ctx)
		var runnerFilter map[string]any
		var runnerConditionedFilter *searchfilter.UniversalFilterCondition
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		} else {
			runnerFilter = invocation.RunOptions.KnowledgeFilter
			runnerConditionedFilter = invocation.RunOptions.KnowledgeConditionedFilter
		}

		agentFilterCondition := convertMetadataToFilterCondition(opt.staticFilter)
		runnerFilterCondition := convertMetadataToFilterCondition(runnerFilter)
		finalFilter := mergeFilterConditions(agentFilterCondition, opt.conditionedFilter, runnerFilterCondition, runnerConditionedFilter)

		// Create search request - for tools, we don't have conversation history yet.
		// This could be enhanced in the future to extract context from the agent's session.
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				FilterCondition: finalFilter,
			},
			MaxResults: opt.maxResults,
			// History, UserID, SessionID could be filled from agent context in the future.
		}

		result, err := kb.Search(ctx, searchReq)
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		return convertSearchResults(result)
	}

	toolName := opt.toolName
	if toolName == "" {
		toolName = "knowledge_search"
	}
	description := opt.toolDescription
	if description == "" {
		description = "Search for relevant information in the knowledge base. " +
			"Use this tool to find context and facts to help answer user questions."
	}
	return function.NewFunctionTool(
		searchFunc,
		function.WithName(toolName),
		function.WithDescription(description),
	)
}

// KnowledgeSearchRequestWithFilter represents the input with filter for the knowledge search tool.
type KnowledgeSearchRequestWithFilter struct {
	Query  string                                 `json:"query,omitempty" jsonschema:"description=The search query to find relevant information in the knowledge base. Can be empty when using only filters."`
	Filter *searchfilter.UniversalFilterCondition `json:"filter,omitempty" jsonschema:"description=Filter conditions to apply to the search query. Use lowercase operators: 'eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not in', 'like', 'not like', 'between', 'and', 'or'."`
}

// NewAgenticFilterSearchTool creates a knowledge search tool with dynamic agent-controlled filtering.
// The agent can analyze user queries and construct filters dynamically.
//
// Parameters:
//   - kb: The knowledge base to search
//   - agenticFilterInfo: Available metadata fields and values, e.g., {"category": ["doc", "tutorial"]}
//   - opts: Optional static filters (WithFilter/WithConditionedFilter) always applied
func NewAgenticFilterSearchTool(
	kb knowledge.Knowledge,
	agenticFilterInfo map[string][]any,
	opts ...Option,
) tool.Tool {
	opt := &options{
		maxResults: defaultMaxResults,
	}
	for _, o := range opts {
		o(opt)
	}
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequestWithFilter) (*KnowledgeSearchResponse, error) {
		// Query can be empty when using only filters for metadata-based retrieval
		if req.Query == "" && req.Filter == nil {
			return nil, errors.New("at least one of query or filter must be provided")
		}

		invocation, ok := agent.InvocationFromContext(ctx)
		var runnerFilter map[string]any
		var runnerConditionedFilter *searchfilter.UniversalFilterCondition
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		} else {
			runnerFilter = invocation.RunOptions.KnowledgeFilter
			runnerConditionedFilter = invocation.RunOptions.KnowledgeConditionedFilter
		}

		agentMetadataCondition := convertMetadataToFilterCondition(opt.staticFilter)
		runnerFilterCondition := convertMetadataToFilterCondition(runnerFilter)
		finalFilter := mergeFilterConditions(agentMetadataCondition, opt.conditionedFilter, runnerFilterCondition, runnerConditionedFilter, req.Filter)

		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				FilterCondition: finalFilter,
			},
			MaxResults: opt.maxResults,
		}

		// Set search mode based on whether query is provided
		// When query is empty, use filter-only search mode
		if req.Query == "" {
			searchReq.SearchMode = vectorstore.SearchModeFilter
		}

		result, err := kb.Search(ctx, searchReq)
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		return convertSearchResults(result)
	}

	toolName := opt.toolName
	if toolName == "" {
		toolName = "knowledge_search_with_agentic_filter"
	}
	filterInfo := generateAgenticFilterPrompt(agenticFilterInfo)
	description := ""
	if opt.toolDescription == "" {
		description = filterInfo
	} else {
		description = fmt.Sprintf("tool description:%s, filter info:%s", opt.toolDescription, filterInfo)
	}
	return function.NewFunctionTool(
		searchFunc,
		function.WithName(toolName),
		function.WithDescription(description),
	)
}

// convertSearchResults converts knowledge.SearchResult to KnowledgeSearchResponse.
func convertSearchResults(result *knowledge.SearchResult) (*KnowledgeSearchResponse, error) {
	if result == nil || len(result.Documents) == 0 {
		return nil, errors.New("no relevant information found")
	}

	documents := make([]*DocumentResult, 0, len(result.Documents))
	for _, doc := range result.Documents {
		documents = append(documents, &DocumentResult{
			Text:     doc.Document.Content,
			Metadata: filterMetadata(doc.Document.Metadata),
			Score:    doc.Score,
		})
	}

	return &KnowledgeSearchResponse{
		Documents: documents,
		Message:   fmt.Sprintf("Found %d relevant document(s)", len(documents)),
	}, nil
}

// convertMetadataToFilterCondition converts a metadata map to UniversalFilterCondition.
func convertMetadataToFilterCondition(metadata map[string]any) *searchfilter.UniversalFilterCondition {
	if len(metadata) == 0 {
		return nil
	}

	var conditions []*searchfilter.UniversalFilterCondition
	for k, v := range metadata {
		conditions = append(conditions, &searchfilter.UniversalFilterCondition{
			Field:    k,
			Operator: searchfilter.OperatorEqual,
			Value:    v,
		})
	}

	if len(conditions) == 0 {
		return nil
	}
	if len(conditions) == 1 {
		return conditions[0]
	}

	return &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    conditions,
	}
}

// mergeFilterConditions merges multiple filter conditions using AND logic.
// All non-nil conditions are combined with AND operator.
// Returns nil if all conditions are nil.
func mergeFilterConditions(
	conditions ...*searchfilter.UniversalFilterCondition,
) *searchfilter.UniversalFilterCondition {
	var nonNilConditions []*searchfilter.UniversalFilterCondition
	for _, cond := range conditions {
		if cond != nil {
			nonNilConditions = append(nonNilConditions, cond)
		}
	}

	if len(nonNilConditions) == 0 {
		return nil
	}
	if len(nonNilConditions) == 1 {
		return nonNilConditions[0]
	}

	return &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    nonNilConditions,
	}
}

// filterMetadata removes internal metadata keys with MetaPrefix from the metadata map.
func filterMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	filtered := make(map[string]any)
	for k, v := range metadata {
		// Skip internal metadata keys with trpc_agent_go_ prefix
		if !strings.HasPrefix(k, source.MetaPrefix) || k == source.MetaChunkIndex {
			filtered[k] = v
		}
	}
	return filtered
}

func generateAgenticFilterPrompt(agenticFilterInfo map[string][]any) string {
	if len(agenticFilterInfo) == 0 {
		return "You are a helpful assistant that can search for relevant information in the knowledge base."
	}

	// Build list of valid filter keys
	keys := make([]string, 0, len(agenticFilterInfo))
	for k := range agenticFilterInfo {
		keys = append(keys, k)
	}
	keysStr := fmt.Sprintf("%v", keys)

	var b strings.Builder

	fmt.Fprintf(&b, `You are a helpful assistant that can search for relevant information in the knowledge base. Available metadata filters: %s.

Filter Usage:
- Query: Can be empty when using only metadata filters
- Filter: Use "filter" field with standard operators (lowercase): eq, ne, gt, gte, lt, lte, in, not in, like, not like, between, and, or

Filter Examples (use double quotes for JSON):
- Single: {"field": "category", "operator": "eq", "value": "documentation"}
- OR: {"operator": "or", "value": [{"field": "type", "operator": "eq", "value": "golang"}, {"field": "type", "operator": "eq", "value": "llm"}]}
- AND: {"operator": "and", "value": [{"field": "category", "operator": "eq", "value": "doc"}, {"field": "topic", "operator": "eq", "value": "programming"}]}
- IN: {"field": "type", "operator": "in", "value": ["golang", "llm", "wiki"]}
- NOT IN: {"field": "status", "operator": "not in", "value": ["archived", "deleted"]}
- LIKE: {"field": "title", "operator": "like", "value": "%%tutorial%%"}
- BETWEEN: {"field": "score", "operator": "between", "value": [0.5, 0.9]}
- Nested: {"operator": "and", "value": [{"field": "category", "operator": "eq", "value": "doc"}, {"operator": "or", "value": [{"field": "topic", "operator": "eq", "value": "programming"}, {"field": "topic", "operator": "eq", "value": "ml"}]}]}

Note: For logical operators (and/or), use "value" field to specify an array of sub-conditions.

Available Filter Values:
`, keysStr)

	// Separate keys with and without predefined values
	var keysWithValues []string
	var keysWithoutValues []string
	for k, v := range agenticFilterInfo {
		if len(v) == 0 {
			keysWithoutValues = append(keysWithoutValues, k)
		} else {
			keysWithValues = append(keysWithValues, fmt.Sprintf("  - %s: %v", k, v))
		}
	}

	// Print keys with predefined values first
	if len(keysWithValues) > 0 {
		fmt.Fprintf(&b, "\nFields with predefined values (use exact values only):\n")
		for _, line := range keysWithValues {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}

	// Print keys without predefined values
	if len(keysWithoutValues) > 0 {
		fmt.Fprintf(&b, "\nFields accepting any value:\n")
		for _, k := range keysWithoutValues {
			fmt.Fprintf(&b, "  - %s\n", k)
		}
	}

	return b.String()
}
