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

package retriever

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// DefaultRetriever implements the complete RAG pipeline.
type DefaultRetriever struct {
	embedder      embedder.Embedder
	vectorStore   vectorstore.VectorStore
	queryEnhancer query.Enhancer
	reranker      reranker.Reranker
}

// Option represents a functional option for configuring DefaultRetriever.
type Option func(*DefaultRetriever)

// WithEmbedder sets the embedder for the retriever.
func WithEmbedder(e embedder.Embedder) Option {
	return func(dr *DefaultRetriever) {
		dr.embedder = e
	}
}

// WithVectorStore sets the vector store for the retriever.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(dr *DefaultRetriever) {
		dr.vectorStore = vs
	}
}

// WithQueryEnhancer sets the query enhancer for the retriever.
func WithQueryEnhancer(qe query.Enhancer) Option {
	return func(dr *DefaultRetriever) {
		dr.queryEnhancer = qe
	}
}

// WithReranker sets the reranker for the retriever.
func WithReranker(r reranker.Reranker) Option {
	return func(dr *DefaultRetriever) {
		dr.reranker = r
	}
}

// New creates a new default retriever with the given options.
func New(opts ...Option) *DefaultRetriever {
	dr := &DefaultRetriever{}

	for _, opt := range opts {
		opt(dr)
	}

	return dr
}

// Retrieve implements the Retriever interface by executing the complete RAG pipeline.
func (dr *DefaultRetriever) Retrieve(ctx context.Context, q *Query) (*Result, error) {
	// Step 1: Enhance query (if enhancer is available).
	finalQuery := q.Text
	if dr.queryEnhancer != nil {
		// Create query request with context (retriever doesn't have full context info).
		queryReq := &query.Request{
			Query: q.Text,
			// History, UserID, SessionID would need to be passed from higher level.
		}
		enhanced, err := dr.queryEnhancer.EnhanceQuery(ctx, queryReq)
		if err != nil {
			return nil, err
		}
		finalQuery = enhanced.Enhanced
	}

	// Step 2: Generate embedding.
	embedding, err := dr.embedder.GetEmbedding(ctx, finalQuery)
	if err != nil {
		return nil, err
	}

	// Step 3: Search vector store.
	searchResults, err := dr.vectorStore.Search(ctx, &vectorstore.SearchQuery{
		Vector:   embedding,
		Limit:    q.Limit,
		MinScore: q.MinScore,
		Filter:   convertQueryFilter(q.Filter),
	})
	if err != nil {
		return nil, err
	}

	// Step 4: Convert to reranker format.
	rerankerResults := make([]*reranker.Result, len(searchResults.Results))
	for i, doc := range searchResults.Results {
		rerankerResults[i] = &reranker.Result{
			Document: doc.Document,
			Score:    doc.Score,
		}
	}

	// Step 5: Rerank results (if reranker is available).
	if dr.reranker != nil {
		rerankerResults, err = dr.reranker.Rerank(ctx, rerankerResults)
		if err != nil {
			return nil, err
		}
	}

	// Step 6: Convert back to retriever format.
	finalResults := make([]*RelevantDocument, len(rerankerResults))
	for i, result := range rerankerResults {
		finalResults[i] = &RelevantDocument{
			Document: result.Document,
			Score:    result.Score,
		}
	}

	return &Result{
		Documents: finalResults,
	}, nil
}

// Close implements the Retriever interface.
func (dr *DefaultRetriever) Close() error {
	// Close components if they support closing.
	return nil
}

// convertQueryFilter converts retriever.QueryFilter to vectorstore.SearchFilter.
func convertQueryFilter(qf *QueryFilter) *vectorstore.SearchFilter {
	if qf == nil {
		return nil
	}

	return &vectorstore.SearchFilter{
		IDs:      qf.DocumentIDs,
		Metadata: qf.Metadata,
	}
}
