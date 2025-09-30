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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
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

// NewKnowledgeSearchTool creates a function tool for knowledge search using
// the Knowledge interface.
// This tool allows agents to search for relevant information in the knowledge base.
func NewKnowledgeSearchTool(kb knowledge.Knowledge, filter map[string]any) tool.Tool {
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
		finalFilter := getFinalFilter(filter, runnerFilter, nil)
		log.Infof("knowledge search tool: final filter: %v", finalFilter)

		// Create search request - for tools, we don't have conversation history yet.
		// This could be enhanced in the future to extract context from the agent's session.
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata: finalFilter,
			},
			// History, UserID, SessionID could be filled from agent context in the future.
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

	return function.NewFunctionTool(
		searchFunc,
		function.WithName("knowledge_search"),
		function.WithDescription("Search for relevant information in the knowledge base. "+
			"Use this tool to find context and facts to help answer user questions."),
	)
}

// KnowledgeSearchRequestWithFilter represents the input with filter for the knowledge search tool.
type KnowledgeSearchRequestWithFilter struct {
	Query   string            `json:"query" jsonschema:"description=The search query to find relevant information in the knowledge base"`
	Filters []KnowledgeFilter `json:"filters" jsonschema:"description=The filters to apply to the search query"`
}

// KnowledgeFilter represents the filter for the knowledge search tool.
// The filter is a key-value pair.
type KnowledgeFilter struct {
	Key   string `json:"key" jsonschema:"description=The key of the filter"`
	Value string `json:"value" jsonschema:"description=The value of the filter"`
}

// NewAgenticFilterSearchTool creates a function tool for knowledge search using
// the Knowledge interface with filter.
// This tool allows agents to search for relevant information in the knowledge base.
func NewAgenticFilterSearchTool(
	kb knowledge.Knowledge,
	filter map[string]any,
	agenticFilterInfo map[string][]any,
) tool.Tool {
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequestWithFilter) (*KnowledgeSearchResponse, error) {
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

		// Convert request filters to map[string]any
		requestFilter := make(map[string]any)
		for _, f := range req.Filters {
			requestFilter[f.Key] = f.Value
		}
		finalFilter := getFinalFilter(filter, runnerFilter, requestFilter)
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			SearchFilter: &knowledge.SearchFilter{
				Metadata: finalFilter,
			},
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

	description := generateAgenticFilterPrompt(agenticFilterInfo)
	return function.NewFunctionTool(
		searchFunc,
		function.WithName("knowledge_search_with_agentic_filter"),
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

	prompt := "You are a helpful assistant that can search for relevant information in the knowledge base. "
	prompt += fmt.Sprintf("Available filters: %s. Always use filters when the user query indicates specific metadata.\n\n", keysStr)

	prompt += "Usage Rules:\n"
	prompt += "- Explicit key=value pairs: Use directly (e.g., 'protocol=trpc-go' -> [{'key': 'protocol', 'value': 'trpc-go'}])\n"
	prompt += "- Key only queries: Choose from available values if provided; generate appropriate value if empty\n"
	prompt += "- Multiple filters: Combine in filters parameter array\n"
	prompt += "- Fallback strategy: If filtered search returns no results, retry without filters\n"
	prompt += "- Exception: For explicit key=value queries, do not use fallback (must respect user's specific requirement)\n\n"

	prompt += "Examples:\n"
	prompt += "1. \"show me tRPC gateway documentation\" -> [{'key': 'service_type', 'value': 'gateway'}] + fallback if needed\n"
	prompt += "2. \"find protocol=trpc-go docs\" -> [{'key': 'protocol', 'value': 'trpc-go'}] (no fallback, explicit requirement)\n"
	prompt += "3. \"what are the protocol options?\" -> choose appropriate protocol value from available options + fallback\n"
	prompt += "4. \"service_type=api and protocol=trpc-go\" -> [{'key': 'service_type', 'value': 'api'}, {'key': 'protocol', 'value': 'trpc-go'}]\n\n"

	prompt += "Available filter values for each key:\n"
	for k, v := range agenticFilterInfo {
		if len(v) == 0 {
			prompt += fmt.Sprintf("- %s: [] (generate appropriate value based on context)\n", k)
		} else {
			prompt += fmt.Sprintf("- %s: %v (choose from these options)\n", k, v)
		}
	}

	return prompt
}
