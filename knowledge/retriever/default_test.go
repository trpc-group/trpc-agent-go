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

// TestDefaultRetriever_WithNilEmbedder tests retrieving without embedder.
func TestDefaultRetriever_WithNilEmbedder(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc5", Content: "test content"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(nil),
		WithVectorStore(vs),
	)

	// Query without embedder - should return error since vector search needs embeddings
	_, err := d.Retrieve(context.Background(), &Query{
		Text:  "test",
		Limit: 5,
	})
	// Without embedder, vector will be empty which should fail for hybrid/vector search
	if err == nil {
		t.Fatal("expected error when embedder is nil")
	}
}

// mockQueryEnhancer is a mock query enhancer for testing.
type mockQueryEnhancer struct {
	enhanced string
	err      error
}

func (m *mockQueryEnhancer) EnhanceQuery(ctx context.Context, req *q.Request) (*q.Enhanced, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &q.Enhanced{Enhanced: m.enhanced}, nil
}

// TestDefaultRetriever_WithQueryEnhancer tests retrieving with query enhancer.
func TestDefaultRetriever_WithQueryEnhancer(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc6", Content: "enhanced content"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	enhancer := &mockQueryEnhancer{enhanced: "enhanced query"}
	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithQueryEnhancer(enhancer),
	)

	// Query with query enhancer
	res, err := d.Retrieve(context.Background(), &Query{
		Text:      "original query",
		Limit:     5,
		History:   []ConversationMessage{{Role: "user", Content: "hi"}},
		UserID:    "user123",
		SessionID: "session456",
	})
	if err != nil {
		t.Fatalf("retrieve with query enhancer err: %v", err)
	}
	if len(res.Documents) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Documents))
	}
}

// TestDefaultRetriever_QueryEnhancerError tests error handling from query enhancer.
func TestDefaultRetriever_QueryEnhancerError(t *testing.T) {
	vs := inmemory.New()
	enhancer := &mockQueryEnhancer{err: context.Canceled}
	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithQueryEnhancer(enhancer),
	)

	// Should return error from query enhancer
	_, err := d.Retrieve(context.Background(), &Query{Text: "test", Limit: 5})
	if err == nil {
		t.Fatal("expected error from query enhancer")
	}
	if err != context.Canceled {
		t.Fatalf("expected Canceled error, got: %v", err)
	}
}

// mockReranker is a mock reranker for testing.
type mockReranker struct {
	err error
}

func (m *mockReranker) Rerank(ctx context.Context, query *r.Query, results []*r.Result) ([]*r.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Simply reverse the order for testing
	reversed := make([]*r.Result, len(results))
	for i, result := range results {
		reversed[len(results)-1-i] = result
	}
	return reversed, nil
}

// TestDefaultRetriever_WithReranker tests retrieving with reranker.
func TestDefaultRetriever_WithReranker(t *testing.T) {
	vs := inmemory.New()
	doc1 := &document.Document{ID: "doc7", Content: "first"}
	doc2 := &document.Document{ID: "doc8", Content: "second"}
	if err := vs.Add(context.Background(), doc1, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc1: %v", err)
	}
	if err := vs.Add(context.Background(), doc2, []float64{0.9, 0.1, 0}); err != nil {
		t.Fatalf("add doc2: %v", err)
	}

	reranker := &mockReranker{}
	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithReranker(reranker),
	)

	// Query with reranker
	res, err := d.Retrieve(context.Background(), &Query{Text: "test", Limit: 5})
	if err != nil {
		t.Fatalf("retrieve with reranker err: %v", err)
	}
	if len(res.Documents) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Documents))
	}
}

// TestDefaultRetriever_RerankError tests error handling from reranker.
func TestDefaultRetriever_RerankError(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc9", Content: "content"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	reranker := &mockReranker{err: context.DeadlineExceeded}
	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
		WithReranker(reranker),
	)

	// Should return error from reranker
	_, err := d.Retrieve(context.Background(), &Query{Text: "test", Limit: 5})
	if err == nil {
		t.Fatal("expected error from reranker")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded error, got: %v", err)
	}
}

// TestDefaultRetriever_WithFilterAndMetadata tests filter conversion.
func TestDefaultRetriever_WithFilterAndMetadata(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{
		ID:       "doc10",
		Content:  "metadata content",
		Metadata: map[string]any{"tag": "important", "category": "test"},
	}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Query with metadata filter
	res, err := d.Retrieve(context.Background(), &Query{
		Text:  "test",
		Limit: 5,
		Filter: &QueryFilter{
			Metadata: map[string]any{"tag": "important"},
		},
	})
	if err != nil {
		t.Fatalf("retrieve with metadata filter err: %v", err)
	}
	if len(res.Documents) == 0 {
		t.Fatal("expected at least one result")
	}
}

// TestDefaultRetriever_WithSearchMode tests different search modes.
func TestDefaultRetriever_WithSearchMode(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc11", Content: "search mode test"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Test vector search mode
	res, err := d.Retrieve(context.Background(), &Query{
		Text:       "test",
		Limit:      5,
		SearchMode: 0, // 0 for vector mode
	})
	if err != nil {
		t.Fatalf("retrieve with vector mode err: %v", err)
	}
	if len(res.Documents) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Documents))
	}
}

// TestDefaultRetriever_WithMinScore tests minimum score filtering.
func TestDefaultRetriever_WithMinScore(t *testing.T) {
	vs := inmemory.New()
	doc := &document.Document{ID: "doc12", Content: "score test"}
	if err := vs.Add(context.Background(), doc, []float64{1, 0, 0}); err != nil {
		t.Fatalf("add doc: %v", err)
	}

	d := New(
		WithEmbedder(dummyEmbedder{}),
		WithVectorStore(vs),
	)

	// Query with minimum score
	res, err := d.Retrieve(context.Background(), &Query{
		Text:     "test",
		Limit:    5,
		MinScore: 0.5,
	})
	if err != nil {
		t.Fatalf("retrieve with min score err: %v", err)
	}
	if res != nil && len(res.Documents) > 0 {
		// Verify all results meet minimum score
		for _, doc := range res.Documents {
			if doc.Score < 0.5 {
				t.Fatalf("result score %f below minimum 0.5", doc.Score)
			}
		}
	}
}

// TestConvertQueryFilter tests the filter conversion function.
func TestConvertQueryFilter(t *testing.T) {
	tests := []struct {
		name   string
		input  *QueryFilter
		expect bool // expect non-nil result
	}{
		{
			name:   "nil filter",
			input:  nil,
			expect: false,
		},
		{
			name: "filter with document IDs",
			input: &QueryFilter{
				DocumentIDs: []string{"doc1", "doc2"},
			},
			expect: true,
		},
		{
			name: "filter with metadata",
			input: &QueryFilter{
				Metadata: map[string]any{"key": "value"},
			},
			expect: true,
		},
		{
			name: "complete filter",
			input: &QueryFilter{
				DocumentIDs: []string{"doc3"},
				Metadata:    map[string]any{"tag": "important"},
			},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertQueryFilter(tt.input)
			if tt.expect && result == nil {
				t.Error("expected non-nil result")
			}
			if !tt.expect && result != nil {
				t.Error("expected nil result")
			}
			if result != nil && tt.input != nil {
				// Verify conversion is correct
				if len(result.IDs) != len(tt.input.DocumentIDs) {
					t.Errorf("expected %d IDs, got %d", len(tt.input.DocumentIDs), len(result.IDs))
				}
				if len(result.Metadata) != len(tt.input.Metadata) {
					t.Errorf("expected %d metadata entries, got %d", len(tt.input.Metadata), len(result.Metadata))
				}
			}
		})
	}
}
