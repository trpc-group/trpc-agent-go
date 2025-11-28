//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reranker provides result re-ranking for knowledge systems.
package reranker

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
)

// Reranker re-ranks search results based on various criteria.
type Reranker interface {
	// Rerank re-orders search results based on ranking criteria.
	Rerank(ctx context.Context, query *Query, results []*Result) ([]*Result, error)
}

// ConversationMessage represents a message in a conversation history.
// It's an alias to the query package type for API compatibility.
type ConversationMessage = query.ConversationMessage

// Query represents a search query for re-ranking.
type Query struct {
	// Text is the query text for semantic search.
	Text string
	// FinalQuery is the final processed query after enhancements.
	FinalQuery string
	// History contains recent conversation messages for context.
	// Should be limited to last N messages for performance.
	History []ConversationMessage

	// UserID can help with personalized search results.
	UserID string

	// SessionID can help with session-specific context.
	SessionID string
}

// Result represents a rankable search result.
type Result struct {
	// Document is the search result document.
	Document *document.Document

	// Score is the relevance score.
	Score float64
}
