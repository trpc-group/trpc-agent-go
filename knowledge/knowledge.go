//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package knowledge provides the main knowledge management interface for trpc-agent-go.
package knowledge

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Knowledge is the main interface for knowledge management operations.
type Knowledge interface {
	// Search performs semantic search and returns the best result.
	// This is the main method used by agents for RAG.
	// Context includes conversation history for better search results.
	Search(ctx context.Context, req *SearchRequest) (*SearchResult, error)
}

// SearchRequest represents a search request with context.
type SearchRequest struct {
	// Query is the search query text.
	Query string

	// History contains recent conversation messages for context.
	// Should be limited to last N messages for performance.
	History []ConversationMessage

	// UserID can help with personalized search results.
	UserID string

	// SessionID can help with session-specific context.
	SessionID string

	// MaxResults limits the number of results returned (optional).
	MaxResults int

	// MinScore sets minimum relevance score threshold (optional).
	MinScore float64
}

// ConversationMessage represents a message in conversation history.
type ConversationMessage struct {
	// Role indicates if this is from user or assistant.
	Role string

	// Content is the message content.
	Content string

	// Timestamp when the message was sent.
	Timestamp int64
}

// SearchResult represents the result of a knowledge search.
type SearchResult struct {
	// Document is the best matching document.
	Document *document.Document

	// Score is the relevance score (0.0 to 1.0).
	Score float64

	// Text is the document content for agent context.
	Text string
}
