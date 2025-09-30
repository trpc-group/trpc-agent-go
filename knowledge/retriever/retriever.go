//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package retriever provides interfaces for knowledge retrieval operations.
package retriever

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
)

// Retriever defines the interface for retrieving relevant documents based on queries.
type Retriever interface {
	// Retrieve finds the most relevant documents for a given query.
	Retrieve(ctx context.Context, query *Query) (*Result, error)

	// Close closes the retriever and releases resources.
	Close() error
}

// ConversationMessage represents a message in a conversation history.
// It's an alias to the query package type for API compatibility.
type ConversationMessage = query.ConversationMessage

// Query represents a retrieval query.
type Query struct {
	// Text is the query text for semantic search.
	Text string

	// History contains recent conversation messages for context.
	// Should be limited to last N messages for performance.
	History []ConversationMessage

	// UserID can help with personalized search results.
	UserID string

	// SessionID can help with session-specific context.
	SessionID string

	// Limit specifies the number of documents to retrieve.
	Limit int

	// MinScore specifies the minimum relevance score threshold (0.0 to 1.0).
	MinScore float64

	// Filter specifies additional filtering criteria.
	Filter *QueryFilter
}

// QueryFilter represents filtering criteria for retrieval.
type QueryFilter struct {
	// DocumentIDs filters to specific document IDs.
	DocumentIDs []string

	// Metadata filters documents by metadata key-value pairs.
	Metadata map[string]any
}

// Result represents the result of a retrieval operation.
type Result struct {
	// Documents contains the retrieved documents with relevance scores.
	Documents []*RelevantDocument
}

// RelevantDocument represents a document with its relevance information.
type RelevantDocument struct {
	// Document is the retrieved document.
	Document *document.Document

	// Score is the relevance score (0.0 to 1.0, higher is more relevant).
	Score float64
}
