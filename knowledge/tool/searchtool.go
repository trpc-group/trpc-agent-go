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
// Supports operators: eq, ne, gt, gte, lt, lte, in, not in, like, and, or.
// For simple AND-only filters, use WithFilter instead.
func WithConditionedFilter(filterCondition *searchfilter.UniversalFilterCondition) Option {
	return func(opts *options) {
		opts.conditionedFilter = filterCondition
	}
}

// WithMaxResults sets the maximum number of documents to return.
// Default is 0, which means no limit (controlled by knowledge base settings).
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
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		} else {
			runnerFilter = invocation.RunOptions.KnowledgeFilter
		}
		finalFilter := getStaticFilter(opt.staticFilter, runnerFilter)

		// Create search request - for tools, we don't have conversation history yet.
		// This could be enhanced in the future to extract context from the agent's session.
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata:        finalFilter,
				FilterCondition: opt.conditionedFilter,
			},
			MaxResults: opt.maxResults,
			// History, UserID, SessionID could be filled from agent context in the future.
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
		if result == nil || len(result.Documents) == 0 {
			return nil, errors.New("no relevant information found")
		}

		// Convert results to DocumentResult array
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
	Query  string                    `json:"query,omitempty" jsonschema:"description=The search query to find relevant information in the knowledge base. Can be empty when using only filters."`
	Filter *ConditionedFilterRequest `json:"filter,omitempty" jsonschema:"description=Filter conditions to apply to the search query. Use lowercase operators: 'eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not in', 'like', 'not like', 'between', 'and', 'or'."`
}

// ConditionedFilterRequest represents an advanced filter condition that can be used in tool requests.
type ConditionedFilterRequest struct {
	// Field is the metadata field to filter on (not used for logical operators like AND/OR).
	Field string `json:"field,omitempty" jsonschema:"description=The metadata field to filter on"`

	// Operator is the comparison or logical operator.
	// Comparison operators: "eq", "ne", "gt", "gte", "lt", "lte", "in", "not in", "like", "not like", "between"
	// Logical operators: "and", "or"
	Operator string `json:"operator" jsonschema:"description=The operator to use (eq/ne/gt/gte/lt/lte/in/not in/like/not like/between/and/or),enum=eq,enum=ne,enum=gt,enum=gte,enum=lt,enum=lte,enum=in,enum=not in,enum=like,enum=not like,enum=between,enum=and,enum=or"`

	// Value is the value to compare against.
	// For comparison operators: single value or array for "in"/"not in"/"between"
	// For logical operators (and/or): array of ConditionedFilterRequest
	Value any `json:"value,omitempty" jsonschema:"description=The value to compare against or array of sub-conditions for logical operators"`

	// Conditions is used for logical operators (and/or) to specify sub-conditions.
	// This is an alternative to using Value for better type safety.
	Conditions []*ConditionedFilterRequest `json:"conditions,omitempty" jsonschema:"description=Sub-conditions for logical operators (and/or)"`
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
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		} else {
			runnerFilter = invocation.RunOptions.KnowledgeFilter
		}

		// Convert request filter to UniversalFilterCondition if provided
		var requestFilterCondition *searchfilter.UniversalFilterCondition
		if req.Filter != nil {
			var err error
			requestFilterCondition, err = convertConditionedFilterToUniversal(req.Filter)
			if err != nil {
				return nil, fmt.Errorf("invalid filter: %w", err)
			}
		}

		finalConditionedFilter := combineFilterConditions(opt.conditionedFilter, requestFilterCondition)
		metaDataFilter := getStaticFilter(opt.staticFilter, runnerFilter)

		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata:        metaDataFilter,
				FilterCondition: finalConditionedFilter,
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
		if result == nil || len(result.Documents) == 0 {
			return nil, errors.New("no relevant information found")
		}

		// Convert results to DocumentResult array
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

func getStaticFilter(
	agentFilter map[string]any,
	runnerFilter map[string]any,
) map[string]any {
	filter := make(map[string]any)
	for k, v := range runnerFilter {
		filter[k] = v
	}
	for k, v := range agentFilter {
		filter[k] = v
	}
	return filter
}

// filterMetadata removes internal metadata keys with MetaPrefix from the metadata map.
func filterMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	filtered := make(map[string]any)
	for k, v := range metadata {
		// Skip internal metadata keys with trpc_agent_go_ prefix
		if !strings.HasPrefix(k, source.MetaPrefix) {
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
1. Query parameter:
   - Can be empty when using only metadata filters
   - Provide semantic search query when available for semantic matching

2. Filter object: Use 'filter' field for filter conditions (both simple and complex)
   - Supported operators (case insensitive): 'eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not in', 'like', 'not like', 'between', 'and', 'or'
   - Single condition: Use field, operator, and value directly
   - Multiple conditions: Use 'and'/'or' operator with 'conditions' array to combine multiple filters
   - Nested conditions: Combine 'and'/'or' operators for complex logic

Filter Examples:
- Single condition: {'field': 'category', 'operator': 'eq', 'value': 'documentation'}
- OR condition: {'operator': 'or', 'conditions': [{'field': 'content_type', 'operator': 'eq', 'value': 'golang'}, {'field': 'content_type', 'operator': 'eq', 'value': 'llm'}]}
- AND condition: {'operator': 'and', 'conditions': [{'field': 'category', 'operator': 'eq', 'value': 'documentation'}, {'field': 'topic', 'operator': 'eq', 'value': 'programming'}]}
- IN operator: {'field': 'content_type', 'operator': 'in', 'value': ['golang', 'llm', 'wiki']}
- NOT IN operator: {'field': 'status', 'operator': 'not in', 'value': ['archived', 'deleted']}
- LIKE operator: {'field': 'title', 'operator': 'like', 'value': '%%tutorial%%'}
- BETWEEN operator: {'field': 'score', 'operator': 'between', 'value': [0.5, 0.9]}
- Nested: {'operator': 'and', 'conditions': [{'field': 'category', 'operator': 'eq', 'value': 'documentation'}, {'operator': 'or', 'conditions': [{'field': 'topic', 'operator': 'eq', 'value': 'programming'}, {'field': 'topic', 'operator': 'eq', 'value': 'machine_learning'}]}]}

Query Examples:
1. "find golang or llm content" -> query='golang llm', filter={'operator': 'or', 'conditions': [{'field': 'content_type', 'operator': 'eq', 'value': 'golang'}, {'field': 'content_type', 'operator': 'eq', 'value': 'llm'}]}
2. "show documentation" -> query='', filter={'field': 'category', 'operator': 'eq', 'value': 'documentation'}
3. "programming or machine learning docs" -> query='', filter={'operator': 'and', 'conditions': [{'field': 'category', 'operator': 'eq', 'value': 'documentation'}, {'operator': 'or', 'conditions': [{'field': 'topic', 'operator': 'eq', 'value': 'programming'}, {'field': 'topic', 'operator': 'eq', 'value': 'machine_learning'}]}]}

IMPORTANT - Available Filter Values:
The following metadata keys and values are extracted from ALL documents in the knowledge base.
These are the ACTUAL metadata tags that exist in the documents.
You MUST use these exact keys and values when constructing filters.

`, keysStr)

	for k, v := range agenticFilterInfo {
		if len(v) == 0 {
			fmt.Fprintf(&b, "- %s: [] (metadata key exists, any value accepted)\n", k)
		} else {
			fmt.Fprintf(&b, "- %s: %v (use these exact values only)\n", k, v)
		}
	}

	return b.String()
}

// combineFilterConditions combines multiple filter conditions using AND operator.
// Returns nil if all conditions are nil.
func combineFilterConditions(
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

// convertConditionedFilterToUniversal converts a ConditionedFilterRequest to UniversalFilterCondition.
func convertConditionedFilterToUniversal(filter *ConditionedFilterRequest) (*searchfilter.UniversalFilterCondition, error) {
	if filter == nil {
		return nil, nil
	}

	// Validate operator
	if filter.Operator == "" {
		return nil, fmt.Errorf("operator is required")
	}

	// Normalize operator to lowercase
	normalizedOp := strings.ToLower(filter.Operator)

	// Map common operator aliases to standard operators
	operatorMap := map[string]string{
		"=":     searchfilter.OperatorEqual,
		"==":    searchfilter.OperatorEqual,
		"!=":    searchfilter.OperatorNotEqual,
		">":     searchfilter.OperatorGreaterThan,
		">=":    searchfilter.OperatorGreaterThanOrEqual,
		"<":     searchfilter.OperatorLessThan,
		"<=":    searchfilter.OperatorLessThanOrEqual,
		"&&":    searchfilter.OperatorAnd,
		"||":    searchfilter.OperatorOr,
		"equal": searchfilter.OperatorEqual,
	}
	if mappedOp, ok := operatorMap[normalizedOp]; ok {
		normalizedOp = mappedOp
	}

	// Handle logical operators (and/or)
	if normalizedOp == searchfilter.OperatorAnd || normalizedOp == searchfilter.OperatorOr {
		var subConditions []*searchfilter.UniversalFilterCondition

		// Use Conditions field if provided
		if len(filter.Conditions) > 0 {
			for _, subFilter := range filter.Conditions {
				subCond, err := convertConditionedFilterToUniversal(subFilter)
				if err != nil {
					return nil, fmt.Errorf("invalid sub-condition: %w", err)
				}
				subConditions = append(subConditions, subCond)
			}
		} else if filter.Value != nil {
			// Try to parse Value as array of conditions
			valueSlice, ok := filter.Value.([]any)
			if !ok {
				return nil, fmt.Errorf("logical operator %s requires an array of conditions", filter.Operator)
			}

			for i, v := range valueSlice {
				// Try to convert to ConditionedFilterRequest
				vMap, ok := v.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("condition at index %d is not a valid object", i)
				}

				subFilter := &ConditionedFilterRequest{}
				if field, ok := vMap["field"].(string); ok {
					subFilter.Field = field
				}
				if operator, ok := vMap["operator"].(string); ok {
					subFilter.Operator = operator
				}
				if value, ok := vMap["value"]; ok {
					subFilter.Value = value
				}
				if conditions, ok := vMap["conditions"].([]any); ok {
					for _, c := range conditions {
						if cMap, ok := c.(map[string]any); ok {
							cond := &ConditionedFilterRequest{}
							if f, ok := cMap["field"].(string); ok {
								cond.Field = f
							}
							if o, ok := cMap["operator"].(string); ok {
								cond.Operator = o
							}
							if v, ok := cMap["value"]; ok {
								cond.Value = v
							}
							subFilter.Conditions = append(subFilter.Conditions, cond)
						}
					}
				}

				subCond, err := convertConditionedFilterToUniversal(subFilter)
				if err != nil {
					return nil, fmt.Errorf("invalid sub-condition at index %d: %w", i, err)
				}
				subConditions = append(subConditions, subCond)
			}
		}

		if len(subConditions) == 0 {
			return nil, fmt.Errorf("logical operator %s requires at least one sub-condition", normalizedOp)
		}

		return &searchfilter.UniversalFilterCondition{
			Operator: normalizedOp,
			Value:    subConditions,
		}, nil
	}

	// Handle comparison operators
	if filter.Field == "" {
		return nil, fmt.Errorf("field is required for comparison operator %s", normalizedOp)
	}

	return &searchfilter.UniversalFilterCondition{
		Field:    filter.Field,
		Operator: normalizedOp,
		Value:    filter.Value,
	}, nil
}
