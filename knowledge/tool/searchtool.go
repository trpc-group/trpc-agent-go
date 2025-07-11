// Package tool provides knowledge search tools for agents.
package tool

import (
	"context"
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
	Success bool    `json:"success"`
	Text    string  `json:"text,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Message string  `json:"message,omitempty"`
}

// NewKnowledgeSearchTool creates a function tool for knowledge search using
// the Knowledge interface.
// This tool allows agents to search for relevant information in the knowledge base.
func NewKnowledgeSearchTool(kb knowledge.Knowledge) tool.Tool {
	searchFunc := func(req KnowledgeSearchRequest) KnowledgeSearchResponse {
		ctx := context.Background()

		// Validate input.
		if req.Query == "" {
			return KnowledgeSearchResponse{
				Success: false,
				Message: "Query cannot be empty",
			}
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
			return KnowledgeSearchResponse{
				Success: false,
				Message: fmt.Sprintf("Search failed: %v", err),
			}
		}

		// Handle no results.
		if result == nil {
			return KnowledgeSearchResponse{
				Success: true,
				Message: "No relevant information found",
			}
		}

		// Return successful result.
		return KnowledgeSearchResponse{
			Success: true,
			Text:    result.Text,
			Score:   result.Score,
			Message: fmt.Sprintf("Found relevant content (score: %.2f)", result.Score),
		}
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName("knowledge_search"),
		function.WithDescription("Search for relevant information in the knowledge base. Use this tool to find context and facts to help answer user questions."),
	)
}
