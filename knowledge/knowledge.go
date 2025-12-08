//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package knowledge provides the main knowledge management interface for trpc-agent-go.
package knowledge

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
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

	// MaxResults limits the maximum number of results returned by a vector store,
	// and the final number of results will be limited by Rerank (optional).
	MaxResults int

	// MinScore sets minimum relevance score threshold (optional).
	MinScore float64

	// SearchFilter is the search filter (optional).
	SearchFilter *SearchFilter

	// SearchMode specifies the search mode.
	SearchMode int
}

// ConversationMessage represents a message in conversation history.
// It's an alias to the query package type for API compatibility.
type ConversationMessage = query.ConversationMessage

// SearchResult represents the result of a knowledge search.
type SearchResult struct {
	// Document is the best matching document.
	Document *document.Document
	// Score is the relevance score (0.0 to 1.0).
	Score float64
	// Text is the document content for agent context.
	Text string

	// Documents is the top N documents
	Documents []*Result
}

// Result represents the result of a knowledge search.
type Result struct {
	// Document is the best matching document.
	Document *document.Document
	// Score is the relevance score (0.0 to 1.0).
	Score float64
}

// SearchFilter represents filtering criteria for vector search.
type SearchFilter struct {
	// DocumentIDs filters results to specific document DocumentIDs.
	DocumentIDs []string
	// Metadata filters results by metadata key-value pairs.
	Metadata map[string]any

	// FilterCondition is the universal condition applied to all search results.
	// It is compatible with all storage engines.
	FilterCondition *searchfilter.UniversalFilterCondition
}

// MetadataFieldPrefix is re-exported from source package for convenience.
// Fields with this prefix (e.g., "metadata.category") are treated as metadata fields
// and will be processed accordingly by each vector store implementation.
// Fields without this prefix are treated as system fields or custom top-level fields.
const MetadataFieldPrefix = source.MetadataFieldPrefix
