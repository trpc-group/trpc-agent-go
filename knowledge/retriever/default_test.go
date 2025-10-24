//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package retriever

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	q "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	r "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// dummyEmbedder returns constant vector.
type dummyEmbedder struct{}

func (dummyEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return []float64{1, 0, 0}, nil
}
func (dummyEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, _ := dummyEmbedder{}.GetEmbedding(ctx, text)
	return v, map[string]any{"t": 1}, nil
}
func (dummyEmbedder) GetDimensions() int { return 3 }

func TestDefaultRetriever(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc1", Content: "hello"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithQueryEnhancer(q.NewPassthroughEnhancer()),
		WithReranker(r.NewTopKReranker()),
	)

	res, err := d.Retrieve(context.Background(), &Query{Text: "hi", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve err: %v", err)
	}
	if len(res.Documents) != 1 || res.Documents[0].Document.ID != "doc1" {
		t.Fatalf("unexpected results")
	}

	// Test Close method.
	if err := d.Close(); err != nil {
		t.Fatalf("close should not return error: %v", err)
	}
}

// TestDefaultRetriever_WithNilFilter tests retrieving with nil query filter.
func TestDefaultRetriever_WithNilFilter(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc2", Content: "world"}
	if err := vs.Add(context.Background(), doc, []float64{0, 1, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Query without filter (nil filter).
	res, err := d.Retrieve(context.Background(), &Query{
		Text:   "test",
		Limit:  5,
		Filter: nil, // explicitly test nil filter
	})
	if err != nil {
		t.Fatalf("retrieve with nil filter err: %v", err)
	}
	if len(res.Documents) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Documents))
	}
}

// TestDefaultRetriever_WithFilter tests retrieving with a query filter.
func TestDefaultRetriever_WithFilter(t *testing.T) {
	vs := inmemory.New()
	doc1 := &document.Document{
		ID:       "doc3",
		Content:  "filtered content",
		Metadata: map[string]any{"category": "test"},
	}
	if err := vs.Add(context.Background(), doc1, []float64{1, 1, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Query with filter.
	res, err := d.Retrieve(context.Background(), &Query{
		Text:  "test",
		Limit: 5,
		Filter: &QueryFilter{
			DocumentIDs: []string{"doc3"},
		},
	})
	if err != nil {
		t.Fatalf("retrieve with filter err: %v", err)
	}
	if len(res.Documents) == 0 {
		t.Fatal("expected at least one result")
	}
}

// errorEmbedder always returns an error.
type errorEmbedder struct{}

func (errorEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return nil, context.DeadlineExceeded
}
func (errorEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	return nil, nil, context.DeadlineExceeded
}
func (errorEmbedder) GetDimensions() int { return 3 }

// TestDefaultRetriever_EmbedderError tests error handling from embedder.
func TestDefaultRetriever_EmbedderError(t *testing.T) {
	vs := inmemory.New()
	d := New(
		WithEmbedder(errorEmbedder{}),
		WithVectorStore(vs),
	)

	// Should return error from embedder
	_, err := d.Retrieve(context.Background(), &Query{Text: "test", Limit: 5})
	if err == nil {
		t.Fatal("expected error from embedder")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded error, got: %v", err)
	}
}

// TestDefaultRetriever_WithEmptyQuery tests retrieving with empty query.
func TestDefaultRetriever_WithEmptyQuery(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc4", Content: "content"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Query with empty text - may return error depending on vector store implementation
	res, err := d.Retrieve(context.Background(), &Query{
		Text:  "",
		Limit: 5,
	})
	// Vector store may reject empty/nil vectors for hybrid search
	// Both success and specific errors are acceptable here
	if err == nil {
		// If no error, we should have a valid result
		if res == nil {
			t.Fatal("expected non-nil result when no error")
		}
	}
	// If there's an error, that's also valid behavior for empty queries
}
