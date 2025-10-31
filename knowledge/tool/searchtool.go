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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// KnowledgeSearchRequest represents the input for the knowledge search tool.
type KnowledgeSearchRequest struct {
	Query string `json:"query" jsonschema:"description=The search query to find relevant information in the knowledge base"`
}

// KnowledgeSearchResponse represents the response from the knowledge search tool.
type KnowledgeSearchResponse struct {
	Text    string  `json:"text,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Message string  `json:"message,omitempty"`
}

// Option is a function that configures the knowledge search tool.
type Option func(*options)

type options struct {
	toolName          string
	toolDescription   string
	staticFilter      map[string]any
	conditionedFilter *searchfilter.UniversalFilterCondition
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

// WithFilter sets a static metadata filter for the knowledge search tool.
// This filter is applied to all searches and uses simple AND logic.
//
// The filter is a map where:
//   - Key: metadata field name (e.g., "category", "content_type")
//   - Value: the value to match (exact match, case-sensitive)
//
// Multiple key-value pairs are combined with AND logic.
// For complex filters with OR/nested conditions, use WithConditionedFilter instead.
//
// Example:
//
//	// Only search documents with category="documentation" AND content_type="golang"
//	WithFilter(map[string]any{
//	    "category": "documentation",
//	    "content_type": "golang",
//	})
func WithFilter(filter map[string]any) Option {
	return func(opts *options) {
		opts.staticFilter = filter
	}
}

// WithConditionedFilter sets a static complex filter condition for the knowledge search tool.
// This filter is applied to all searches and supports advanced logic (AND/OR/nested conditions).
//
// Use this when you need:
//   - OR logic: match documents that satisfy ANY of multiple conditions
//   - Nested conditions: combine AND/OR logic in complex ways
//   - Comparison operators: gt, gte, lt, lte, ne, in, not in, like
//
// For simple AND-only filters, use WithFilter instead (simpler syntax).
//
// Example:
//
//	// Search documents where (category="documentation" OR category="tutorial") AND content_type="golang"
//	WithConditionedFilter(
//	    searchfilter.And(
//	        searchfilter.Or(
//	            searchfilter.Equal("category", "documentation"),
//	            searchfilter.Equal("category", "tutorial"),
//	        ),
//	        searchfilter.Equal("content_type", "golang"),
//	    ),
//	)
func WithConditionedFilter(filterCondition *searchfilter.UniversalFilterCondition) Option {
	return func(opts *options) {
		opts.conditionedFilter = filterCondition
	}
}

// NewKnowledgeSearchTool creates a function tool for knowledge search using
// the Knowledge interface.
// This tool allows agents to search for relevant information in the knowledge base.
func NewKnowledgeSearchTool(kb knowledge.Knowledge, opts ...Option) tool.Tool {
	opt := &options{}
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
		finalFilter := getFinalFilter(opt.staticFilter, runnerFilter, nil)
		log.Infof("knowledge search tool: final filter: %v", finalFilter)

		// Create search request - for tools, we don't have conversation history yet.
		// This could be enhanced in the future to extract context from the agent's session.
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata:        finalFilter,
				FilterCondition: opt.conditionedFilter,
			},
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
		if result == nil {
			return nil, errors.New("no relevant information found")
		}
		return &KnowledgeSearchResponse{
			Text:    result.Text,
			Score:   result.Score,
			Message: fmt.Sprintf("Found relevant content (score: %.2f)", result.Score),
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
	Query   string                    `json:"query,omitempty" jsonschema:"description=The search query to find relevant information in the knowledge base. Can be empty when using only filters."`
	Filters []KnowledgeFilter         `json:"filters,omitempty" jsonschema:"description=Simple key-value filters to apply to the search query. Note: multiple filters with the same key will be merged, use 'filter' field for OR conditions."`
	Filter  *ConditionedFilterRequest `json:"filter,omitempty" jsonschema:"description=Advanced filter conditions to apply to the search query. Use lowercase operators: 'eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not in', 'like', 'not like', 'between', 'and', 'or'."`
}

// KnowledgeFilter represents the filter for the knowledge search tool.
// The filter is a key-value pair.
type KnowledgeFilter struct {
	Key   string `json:"key" jsonschema:"description=The key of the filter"`
	Value string `json:"value" jsonschema:"description=The value of the filter"`
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

// NewAgenticFilterSearchTool creates a function tool for knowledge search with dynamic agent-controlled filtering.
// This tool allows agents to dynamically construct filters based on user queries.
//
// Unlike WithFilter (static simple filters) and WithConditionedFilter (static complex filters),
// this tool enables the agent to:
//   - Analyze the user's query and decide which metadata filters to apply
//   - Construct filters dynamically using the 'filters' array (simple AND logic) or 'filter' object (complex logic)
//   - Choose between semantic search (query), metadata filtering (filters/filter), or both
//
// Parameters:
//   - kb: The knowledge base to search
//   - agenticFilterInfo: Available metadata fields and their possible values (extracted from all documents)
//     This information is used to generate the prompt that guides the agent on which filters are available.
//     Example: {"category": ["documentation", "tutorial"], "content_type": ["golang", "python"]}
//   - opts: Optional static filters (WithFilter/WithConditionedFilter) that are always applied
//
// The agent can use:
//   - 'query': Semantic search query (optional)
//   - 'filters': Array of simple filters with AND logic, e.g., [{"key": "category", "value": "doc"}]
//   - 'filter': Complex filter object with OR/AND/nested logic
//
// Example:
//
//	// Create a tool where agent can dynamically filter by category and content_type
//	agenticFilterInfo := map[string][]any{
//	    "category": []any{"documentation", "tutorial", "api"},
//	    "content_type": []any{"golang", "python", "llm"},
//	}
//	tool := NewAgenticFilterSearchTool(kb, agenticFilterInfo)
//
//	// Agent can then decide to search like:
//	// - "find golang tutorials" -> query="golang", filter={"field": "category", "operator": "eq", "value": "tutorial"}
//	// - "show all documentation" -> query="", filter={"field": "category", "operator": "eq", "value": "documentation"}
func NewAgenticFilterSearchTool(
	kb knowledge.Knowledge,
	agenticFilterInfo map[string][]any,
	opts ...Option,
) tool.Tool {
	opt := &options{}
	for _, o := range opts {
		o(opt)
	}
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequestWithFilter) (*KnowledgeSearchResponse, error) {
		// Query can be empty when using only filters for metadata-based retrieval
		if req.Query == "" && len(req.Filters) == 0 && req.Filter == nil {
			return nil, errors.New("at least one of query, filters, or filter must be provided")
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

		// Convert request filters to map[string]any
		requestFilter := make(map[string]any)
		for _, f := range req.Filters {
			requestFilter[f.Key] = f.Value
		}

		finalConditionedFilter := combineFilterConditions(opt.conditionedFilter, requestFilterCondition)
		metaDataFilter := getFinalFilter(opt.staticFilter, runnerFilter, requestFilter)

		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata:        metaDataFilter,
				FilterCondition: finalConditionedFilter,
			},
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
		if result == nil {
			return nil, errors.New("no relevant information found")
		}
		return &KnowledgeSearchResponse{
			Text:    result.Text,
			Score:   result.Score,
			Message: fmt.Sprintf("Found relevant content (score: %.2f)", result.Score),
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

func getFinalFilter(
	agentFilter map[string]any,
	runnerFilter map[string]any,
	invocationFilter map[string]any,
) map[string]any {
	filter := make(map[string]any)
	for k, v := range invocationFilter {
		filter[k] = v
	}
	for k, v := range runnerFilter {
		filter[k] = v
	}
	for k, v := range agentFilter {
		filter[k] = v
	}
	return filter
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
1. Simple filters (AND logic): Use 'filters' array for multiple conditions that must ALL match
   - Example: [{'key': 'category', 'value': 'documentation'}, {'key': 'topic', 'value': 'programming'}]
   - Note: Multiple filters with same key will be merged (last value wins)

2. Complex filters (OR/AND logic): Use 'filter' object for advanced conditions
   - Operators: 'eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'not in', 'like', 'and', 'or'
   - Operator aliases accepted: '=', '==', '!=', '>', '>=', '<', '<=', '&&', '||'
   - Case insensitive: 'OR', 'or', 'Or' all work

3. Query parameter:
   - Can be empty when using only metadata filters
   - Provide semantic search query when available

Filter Examples:
- Single condition: {'field': 'category', 'operator': 'eq', 'value': 'documentation'}
- OR condition: {'operator': 'or', 'conditions': [{'field': 'content_type', 'operator': 'eq', 'value': 'golang'}, {'field': 'content_type', 'operator': 'eq', 'value': 'llm'}]}
- AND condition: {'operator': 'and', 'conditions': [{'field': 'category', 'operator': 'eq', 'value': 'documentation'}, {'field': 'topic', 'operator': 'eq', 'value': 'programming'}]}
- IN operator: {'field': 'content_type', 'operator': 'in', 'value': ['golang', 'llm', 'wiki']}
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
