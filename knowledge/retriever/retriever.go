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

// Package retriever provides interfaces for knowledge retrieval operations.
package retriever

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Retriever defines the interface for retrieving relevant documents based on queries.
type Retriever interface {
	// Retrieve finds the most relevant documents for a given query.
	Retrieve(ctx context.Context, query *Query) (*Result, error)

	// Close closes the retriever and releases resources.
	Close() error
}

// Query represents a retrieval query.
type Query struct {
	// Text is the query text for semantic search.
	Text string

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
	Metadata map[string]interface{}
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
