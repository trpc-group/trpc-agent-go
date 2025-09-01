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

	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
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
func NewKnowledgeSearchTool(kb knowledge.Knowledge, filter map[string]interface{}) tool.Tool {
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
		if req.Query == "" {
			return nil, errors.New("query cannot be empty")
		}
		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		}
		finalFilter := getFinalFilter(filter, invocation.RunOptions.KnowledgeFilter, nil)
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

// NewKnowledgeSearchToolWithAgenticFilter creates a function tool for knowledge search using
// the Knowledge interface with filter.
// This tool allows agents to search for relevant information in the knowledge base.
func NewKnowledgeSearchToolWithAgenticFilter(kb knowledge.Knowledge, filter map[string]interface{}) tool.Tool {
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequestWithFilter) (*KnowledgeSearchResponse, error) {
		if req.Query == "" {
			return nil, errors.New("query cannot be empty")
		}

		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok {
			log.Debugf("knowledge search tool: no invocation found in context")
		}

		// Convert request filters to map[string]interface{}
		requestFilter := make(map[string]interface{})
		for _, f := range req.Filters {
			requestFilter[f.Key] = f.Value
		}
		finalFilter := getFinalFilter(filter, invocation.RunOptions.KnowledgeFilter, requestFilter)
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

	return function.NewFunctionTool(
		searchFunc,
		function.WithName("knowledge_search_with_filter"),
		function.WithDescription("Search for relevant information in the knowledge base. "+
			"Use this tool to find context and facts to help answer user questions."),
	)
}

func getFinalFilter(
	agentFilter map[string]interface{},
	runnerFilter map[string]interface{},
	invocationFilter map[string]interface{},
) map[string]interface{} {
	filter := make(map[string]interface{})
	for k, v := range agentFilter {
		filter[k] = v
	}
	for k, v := range runnerFilter {
		filter[k] = v
	}
	for k, v := range invocationFilter {
		filter[k] = v
	}
	return filter
}
