//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package tool provides knowledge search tools for agents.
package tool

import (
	"context"
	"errors"
	"fmt"

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
func NewKnowledgeSearchTool(kb knowledge.Knowledge) tool.Tool {
	searchFunc := func(ctx context.Context, req *KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {

		// Validate input.
		if req.Query == "" {
			return nil, errors.New("query cannot be empty")
		}

		// Create search request - for tools, we don't have conversation history yet.
		// This could be enhanced in the future to extract context from the agent's session.
		searchReq := &knowledge.SearchRequest{
			Query: req.Query,
			// History, UserID, SessionID could be filled from agent context in the future.
		}

		// Search using the knowledge interface.
		result, err := kb.Search(ctx, searchReq)
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		// Handle no results.
		if result == nil {
			return nil, errors.New("no relevant information found")
		}

		// Return successful result.
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
