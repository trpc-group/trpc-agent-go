// Package knowledge provides the default implementation of the Knowledge interface.
package knowledge

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/retriever"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/storage"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/vectorstore"
)

// BuiltinKnowledge implements the Knowledge interface with a built-in retriever.
type BuiltinKnowledge struct {
	storage       storage.Storage
	vectorStore   vectorstore.VectorStore
	embedder      embedder.Embedder
	retriever     retriever.Retriever
	queryEnhancer query.Enhancer
	reranker      reranker.Reranker
}

// Option represents a functional option for configuring BuiltinKnowledge.
type Option func(*BuiltinKnowledge)

// WithStorage sets the storage backend for document persistence.
func WithStorage(s storage.Storage) Option {
	return func(dk *BuiltinKnowledge) {
		dk.storage = s
	}
}

// WithVectorStore sets the vector store for similarity search.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(dk *BuiltinKnowledge) {
		dk.vectorStore = vs
	}
}

// WithEmbedder sets the embedder for generating document embeddings.
func WithEmbedder(e embedder.Embedder) Option {
	return func(dk *BuiltinKnowledge) {
		dk.embedder = e
	}
}

// WithQueryEnhancer sets a custom query enhancer (optional).
func WithQueryEnhancer(qe query.Enhancer) Option {
	return func(dk *BuiltinKnowledge) {
		dk.queryEnhancer = qe
	}
}

// WithReranker sets a custom reranker (optional).
func WithReranker(r reranker.Reranker) Option {
	return func(dk *BuiltinKnowledge) {
		dk.reranker = r
	}
}

// WithRetriever sets a custom retriever (optional).
func WithRetriever(r retriever.Retriever) Option {
	return func(dk *BuiltinKnowledge) {
		dk.retriever = r
	}
}

// New creates a new BuiltinKnowledge instance with the given options.
func New(opts ...Option) *BuiltinKnowledge {
	dk := &BuiltinKnowledge{}

	// Apply options
	for _, opt := range opts {
		opt(dk)
	}

	// Create built-in retriever if not provided
	if dk.retriever == nil {
		// Use defaults if not specified
		if dk.queryEnhancer == nil {
			dk.queryEnhancer = query.NewPassthroughEnhancer()
		}
		if dk.reranker == nil {
			dk.reranker = reranker.NewTop1Reranker()
		}

		dk.retriever = retriever.New(
			retriever.WithEmbedder(dk.embedder),
			retriever.WithVectorStore(dk.vectorStore),
			retriever.WithQueryEnhancer(dk.queryEnhancer),
			retriever.WithReranker(dk.reranker),
		)
	}

	return dk
}

// AddDocument implements the Knowledge interface.
// It stores the document in storage AND adds its embedding to the vector store.
func (dk *BuiltinKnowledge) AddDocument(ctx context.Context, doc *document.Document) error {
	// Step 1: Store document in storage backend
	if err := dk.storage.Store(ctx, doc); err != nil {
		return fmt.Errorf("failed to store document: %w", err)
	}

	// Step 2: Generate embedding and store in vector store
	if dk.embedder != nil && dk.vectorStore != nil {
		embedding, err := dk.embedder.GetEmbedding(ctx, doc.Content)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %w", err)
		}

		if err := dk.vectorStore.Add(ctx, doc, embedding); err != nil {
			return fmt.Errorf("failed to store embedding: %w", err)
		}
	}

	return nil
}

// Search implements the Knowledge interface.
// It uses the built-in retriever for the complete RAG pipeline.
func (dk *BuiltinKnowledge) Search(ctx context.Context, query string) (*SearchResult, error) {
	if dk.retriever == nil {
		return nil, fmt.Errorf("retriever not configured")
	}

	// Use built-in retriever for RAG pipeline
	result, err := dk.retriever.Retrieve(ctx, &retriever.Query{
		Text:     query,
		Limit:    1, // Return only the best result
		MinScore: 0.0,
	})
	if err != nil {
		return nil, err
	}

	// Return the top result if available
	if len(result.Documents) == 0 {
		return nil, nil
	}

	topDoc := result.Documents[0]
	return &SearchResult{
		Document: topDoc.Document,
		Score:    topDoc.Score,
		Text:     topDoc.Document.Content,
	}, nil
}

// Close implements the Knowledge interface.
func (dk *BuiltinKnowledge) Close() error {
	// Close all components
	if dk.storage != nil {
		if err := dk.storage.Close(); err != nil {
			return fmt.Errorf("failed to close storage: %w", err)
		}
	}

	if dk.vectorStore != nil {
		if err := dk.vectorStore.Close(); err != nil {
			return fmt.Errorf("failed to close vector store: %w", err)
		}
	}

	return nil
}
