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

// Package reranker provides result re-ranking for knowledge systems.
package reranker

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Reranker re-ranks search results based on various criteria.
type Reranker interface {
	// Rerank re-orders search results based on ranking criteria.
	Rerank(ctx context.Context, results []*Result) ([]*Result, error)
}

// Result represents a rankable search result.
type Result struct {
	// Document is the search result document.
	Document *document.Document

	// Score is the relevance score.
	Score float64
}
