//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/retriever"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// mockSource is a simple mock source for testing.
type mockSource struct {
	name     string
	docCount int
}

func (m *mockSource) Name() string {
	return m.name
}

func (m *mockSource) Type() string {
	return "mock"
}

func (m *mockSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	docs := make([]*document.Document, m.docCount)
	for i := 0; i < m.docCount; i++ {
		docs[i] = &document.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Name:    fmt.Sprintf("Document %d", i),
			Content: fmt.Sprintf("Content for document %d", i),
			Metadata: map[string]interface{}{
				"category": fmt.Sprintf("cat-%d", i%3), // Categories: cat-0, cat-1, cat-2
				"level":    i%2 + 1,                    // Levels: 1, 2
				"source":   "test",
			},
		}
	}
	return docs, nil
}

func (m *mockSource) SourceID() string {
	return "test"
}

func (m *mockSource) GetMetadata() map[string]any {
	return map[string]any{
		"name":     []string{m.name},
		"docCount": []any{m.docCount},
		"type":     []string{"mock"},
		"category": []string{"test", "demo"},
	}
}

func TestBuiltinKnowledge_LoadOptions(t *testing.T) {
	// Create a knowledge instance with mock sources.
	kb := New(
		WithSources([]source.Source{
			&mockSource{name: "test-source-1", docCount: 5},
			&mockSource{name: "test-source-2", docCount: 3},
		}),
	)
	kb.vectorStore = &stubVectorStore{}

	ctx := context.Background()

	// Test with default options (should show progress).
	err := kb.Load(ctx)
	if err != nil {
		t.Errorf("Load with default options failed: %v", err)
	}

	// Test with progress disabled.
	err = kb.Load(ctx, WithShowProgress(false))
	if err != nil {
		t.Errorf("Load with progress disabled failed: %v", err)
	}

	// Test with custom progress step size.
	err = kb.Load(ctx, WithProgressStepSize(2))
	if err != nil {
		t.Errorf("Load with custom progress step size failed: %v", err)
	}

	// Test with multiple options.
	err = kb.Load(ctx, WithShowProgress(true), WithProgressStepSize(1))
	if err != nil {
		t.Errorf("Load with multiple options failed: %v", err)
	}
}

func TestBuiltinKnowledge_LoadNoSources(t *testing.T) {
	// Create a knowledge instance with no sources.
	kb := New()
	kb.vectorStore = &stubVectorStore{}
	kb.embedder = stubEmbedder{} // Add embedder to ensure consistency

	ctx := context.Background()

	// Should not fail when there are no sources.
	err := kb.Load(ctx)
	if err != nil {
		t.Errorf("Load with no sources failed: %v", err)
	}
}

func TestSizeStatsAddAndAvg(t *testing.T) {
	buckets := []int{10, 20, 30}
	ss := newSizeStats(buckets)

	sizes := []int{5, 15, 25, 35}
	for _, sz := range sizes {
		ss.add(sz, buckets)
	}

	if ss.totalDocs != len(sizes) {
		t.Fatalf("expected totalDocs %d, got %d", len(sizes), ss.totalDocs)
	}

	if ss.minSize != 5 {
		t.Fatalf("expected minSize 5, got %d", ss.minSize)
	}

	if ss.maxSize != 35 {
		t.Fatalf("expected maxSize 35, got %d", ss.maxSize)
	}

	wantAvg := float64(5+15+25+35) / 4
	if got := ss.avg(); got != wantAvg {
		t.Fatalf("unexpected avg: want %.2f, got %.2f", wantAvg, got)
	}

	// Ensure bucket counts add up.
	totalBucketed := 0
	for _, c := range ss.bucketCnts {
		totalBucketed += c
	}
	if totalBucketed != len(sizes) {
		t.Fatalf("bucket counts mismatch: want %d, got %d", len(sizes), totalBucketed)
	}
}

func TestCalcETA(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	eta := calcETA(start, 5, 10)
	// ETA should be roughly 5s because processed 50% in 5s.
	if eta < 4*time.Second || eta > 6*time.Second {
		t.Fatalf("unexpected ETA: %v", eta)
	}
}

func TestSizeStatsLog(t *testing.T) {
	buckets := []int{10}
	ss := newSizeStats(buckets)
	ss.add(5, buckets)

	// Ensure ss.log does not panic.
	ss.log(buckets)
}

// stubEmbedder returns a fixed embedding.

type stubEmbedder struct{}

func (stubEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return []float64{1, 2, 3}, nil
}
func (stubEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	return []float64{1, 2, 3}, nil, nil
}
func (stubEmbedder) GetDimensions() int { return 3 }

// stubVectorStore stores whether Add was invoked.

type stubVectorStore struct {
	added bool
}

func (s *stubVectorStore) Add(ctx context.Context, doc *document.Document, emb []float64) error {
	s.added = true
	return nil
}
func (*stubVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	return nil, nil, nil
}
func (*stubVectorStore) Update(ctx context.Context, doc *document.Document, emb []float64) error {
	return nil
}
func (*stubVectorStore) Delete(ctx context.Context, id string) error { return nil }
func (*stubVectorStore) DeleteByFilter(ctx context.Context, filter map[string]interface{}) (int, error) {
	return 0, nil
}
func (*stubVectorStore) FlushAll(ctx context.Context) error { return nil }
func (*stubVectorStore) Search(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return nil, nil
}
func (*stubVectorStore) Close() error { return nil }

// TestConversationMessageTypes verifies that knowledge and retriever use the same type.
func TestConversationMessageTypes(t *testing.T) {
	// Create a knowledge ConversationMessage
	kmsg := ConversationMessage{Role: "user", Content: "hi", Timestamp: 1}

	// Should be directly assignable to retriever.ConversationMessage
	// This test ensures they're the same type (via type alias to internal/types)
	var rmsg retriever.ConversationMessage = kmsg

	if rmsg.Role != "user" || rmsg.Content != "hi" || rmsg.Timestamp != 1 {
		t.Fatalf("unexpected message after assignment: %+v", rmsg)
	}
}

func TestCalcETA_Boundaries(t *testing.T) {
	if d := calcETA(time.Now(), 0, 0); d != 0 {
		t.Fatalf("expected 0 duration when processed 0, got %v", d)
	}
}

func TestAddDocument_EmbedderStore(t *testing.T) {
	kb := &BuiltinKnowledge{}
	kb.embedder = stubEmbedder{}
	store := &stubVectorStore{}
	kb.vectorStore = store

	doc := &document.Document{ID: "id", Content: "text"}
	if err := kb.addDocument(context.Background(), doc); err != nil {
		t.Fatalf("addDocument returned error: %v", err)
	}
	if !store.added {
		t.Fatalf("expected vector store Add to be called")
	}
}

// Test configuration options using table-driven tests
func TestConfigurationOptions(t *testing.T) {
	tests := []struct {
		name    string
		setupFn func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool)
	}{
		{
			name: "WithVectorStore",
			setupFn: func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool) {
				store := &stubVectorStore{}
				kb := New(WithVectorStore(store))
				validator := func(kb *BuiltinKnowledge) bool {
					return kb.vectorStore == store
				}
				return kb, validator
			},
		},
		{
			name: "WithEmbedder",
			setupFn: func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool) {
				embedder := stubEmbedder{}
				kb := New(WithEmbedder(embedder))
				validator := func(kb *BuiltinKnowledge) bool {
					return kb.embedder != nil
				}
				return kb, validator
			},
		},
		{
			name: "WithQueryEnhancer",
			setupFn: func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool) {
				mockEnhancer := &mockQueryEnhancer{}
				kb := New(WithQueryEnhancer(mockEnhancer))
				validator := func(kb *BuiltinKnowledge) bool {
					return kb.queryEnhancer != nil
				}
				return kb, validator
			},
		},
		{
			name: "WithReranker",
			setupFn: func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool) {
				mockReranker := &mockReranker{}
				kb := New(WithReranker(mockReranker))
				validator := func(kb *BuiltinKnowledge) bool {
					return kb.reranker != nil
				}
				return kb, validator
			},
		},
		{
			name: "WithRetriever",
			setupFn: func() (*BuiltinKnowledge, func(*BuiltinKnowledge) bool) {
				mockRetriever := &mockRetriever{}
				kb := New(WithRetriever(mockRetriever))
				validator := func(kb *BuiltinKnowledge) bool {
					return kb.retriever == mockRetriever
				}
				return kb, validator
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb, validator := tt.setupFn()
			if !validator(kb) {
				t.Errorf("%s did not set the component correctly", tt.name)
			}
		})
	}
}

// Test load options using table-driven tests
func TestLoadOptions(t *testing.T) {
	tests := []struct {
		name        string
		setupKB     func() *BuiltinKnowledge
		loadOptions []LoadOption
		expectError bool
	}{
		{
			name: "WithShowStats_enabled",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{&mockSource{name: "test", docCount: 1}}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{WithShowStats(true)},
			expectError: false,
		},
		{
			name: "WithShowStats_disabled",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{&mockSource{name: "test", docCount: 1}}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{WithShowStats(false)},
			expectError: false,
		},
		{
			name: "WithSourceConcurrency",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{
					&mockSource{name: "test1", docCount: 1},
					&mockSource{name: "test2", docCount: 1},
				}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{WithSourceConcurrency(2)},
			expectError: false,
		},
		{
			name: "WithDocConcurrency",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{&mockSource{name: "test", docCount: 2}}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{WithDocConcurrency(2)},
			expectError: false,
		},
		{
			name: "WithRecreate",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{&mockSource{name: "test", docCount: 1}}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{WithRecreate(true)},
			expectError: false,
		},
		{
			name: "MultipleOptions",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithSources([]source.Source{&mockSource{name: "test", docCount: 2}}))
				kb.vectorStore = &stubVectorStore{}
				kb.embedder = stubEmbedder{}
				return kb
			},
			loadOptions: []LoadOption{
				WithShowStats(true),
				WithSourceConcurrency(1),
				WithDocConcurrency(1),
				WithShowProgress(true),
				WithProgressStepSize(1),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			err := kb.Load(context.Background(), tt.loadOptions...)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// Test Search functionality using table-driven tests
func TestBuiltinKnowledge_SearchTableDriven(t *testing.T) {
	tests := []struct {
		name           string
		setupKB        func() *BuiltinKnowledge
		request        *SearchRequest
		expectError    bool
		expectedErrMsg string
		validateResult func(*SearchResult) bool
	}{
		{
			name: "no_retriever_configured",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{}
			},
			request:        &SearchRequest{Query: "test"},
			expectError:    true,
			expectedErrMsg: "retriever not configured",
		},
		{
			name: "successful_search",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					retriever: &mockRetriever{
						result: &retriever.Result{
							Documents: []*retriever.RelevantDocument{
								{
									Document: &document.Document{
										ID:      "doc1",
										Content: "test content",
									},
									Score: 0.9,
								},
							},
						},
					},
				}
			},
			request: &SearchRequest{
				Query:      "test query",
				MaxResults: 5,
				MinScore:   0.5,
			},
			expectError: false,
			validateResult: func(result *SearchResult) bool {
				return result.Text == "test content" && result.Score == 0.9
			},
		},
		{
			name: "no_results_found",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					retriever: &mockRetriever{
						result: &retriever.Result{Documents: []*retriever.RelevantDocument{}},
					},
				}
			},
			request:        &SearchRequest{Query: "test"},
			expectError:    true,
			expectedErrMsg: "no relevant documents found",
		},
		{
			name: "retriever_error",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					retriever: &mockRetriever{
						err: fmt.Errorf("retriever error"),
					},
				}
			},
			request:        &SearchRequest{Query: "test"},
			expectError:    true,
			expectedErrMsg: "retrieval failed",
		},
		{
			name: "search_with_query_enhancer",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					queryEnhancer: &mockQueryEnhancer{
						result: &query.Enhanced{Enhanced: "enhanced query"},
					},
					retriever: &mockRetriever{
						result: &retriever.Result{
							Documents: []*retriever.RelevantDocument{
								{
									Document: &document.Document{
										ID:      "doc1",
										Content: "enhanced content",
									},
									Score: 0.8,
								},
							},
						},
					},
				}
			},
			request: &SearchRequest{
				Query: "original query",
				History: []ConversationMessage{
					{Role: "user", Content: "previous message", Timestamp: 123},
				},
				UserID:    "user123",
				SessionID: "session456",
			},
			expectError: false,
			validateResult: func(result *SearchResult) bool {
				return result.Text == "enhanced content" && result.Score == 0.8
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			result, err := kb.Search(context.Background(), tt.request)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !containsString(err.Error(), tt.expectedErrMsg) {
					t.Errorf("Expected error containing '%s', got: %v", tt.expectedErrMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
					return
				}
				if tt.validateResult != nil && !tt.validateResult(result) {
					t.Errorf("Result validation failed for test case: %s", tt.name)
				}
			}
		})
	}
}

// Test Close functionality using table-driven tests
func TestBuiltinKnowledge_Close(t *testing.T) {
	tests := []struct {
		name           string
		setupKB        func() *BuiltinKnowledge
		expectError    bool
		expectedErrMsg string
	}{
		{
			name: "no_components",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{}
			},
			expectError: false,
		},
		{
			name: "successful_close",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					retriever:   &mockRetriever{},
					vectorStore: &stubVectorStore{},
				}
			},
			expectError: false,
		},
		{
			name: "vector_store_close_error",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					retriever:   &mockRetriever{},
					vectorStore: &errorVectorStore{},
				}
			},
			expectError:    true,
			expectedErrMsg: "failed to close vector store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			err := kb.Close()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !containsString(err.Error(), tt.expectedErrMsg) {
					t.Errorf("Expected error containing '%s', got: %v", tt.expectedErrMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// Test sequential loading
func TestBuiltinKnowledge_LoadSequential(t *testing.T) {
	kb := New(WithSources([]source.Source{
		&mockSource{name: "test-source", docCount: 2},
	}))
	kb.vectorStore = &stubVectorStore{}
	kb.embedder = stubEmbedder{}

	// Force sequential loading by setting concurrency to 1
	err := kb.Load(context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowStats(true),
		WithShowProgress(true),
		WithProgressStepSize(1))
	if err != nil {
		t.Errorf("Sequential load failed: %v", err)
	}
}

// Test convertQueryFilter
func TestConvertQueryFilter(t *testing.T) {
	// Test nil filter
	result := convertQueryFilter(nil)
	if result != nil {
		t.Errorf("Expected nil result for nil input, got: %v", result)
	}

	// Test filter with data
	filter := &SearchFilter{
		DocumentIDs: []string{"doc1", "doc2"},
		Metadata: map[string]interface{}{
			"category": "test",
			"level":    1,
		},
	}

	result = convertQueryFilter(filter)
	if result == nil {
		t.Fatalf("Expected non-nil result")
	}
	if len(result.DocumentIDs) != 2 {
		t.Errorf("Expected 2 document IDs, got: %d", len(result.DocumentIDs))
	}
	if result.Metadata["category"] != "test" {
		t.Errorf("Expected category 'test', got: %v", result.Metadata["category"])
	}
}

// Test addDocument using table-driven tests
func TestAddDocument(t *testing.T) {
	doc := &document.Document{ID: "id", Content: "text"}

	tests := []struct {
		name           string
		setupKB        func() *BuiltinKnowledge
		expectError    bool
		expectedErrMsg string
	}{
		{
			name: "embedder_error",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					embedder:    &errorEmbedder{},
					vectorStore: &stubVectorStore{},
				}
			},
			expectError:    true,
			expectedErrMsg: "failed to generate embedding",
		},
		{
			name: "vector_store_error",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					embedder:    stubEmbedder{},
					vectorStore: &errorVectorStore{},
				}
			},
			expectError:    true,
			expectedErrMsg: "failed to store embedding",
		},
		{
			name: "no_embedder_or_vectorstore",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{}
			},
			expectError: false,
		},
		{
			name: "successful_add",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					embedder:    stubEmbedder{},
					vectorStore: &stubVectorStore{},
				}
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			err := kb.addDocument(context.Background(), doc)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !containsString(err.Error(), tt.expectedErrMsg) {
					t.Errorf("Expected error containing '%s', got: %v", tt.expectedErrMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// Helper function to check if string contains substring
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

// Mock implementations for testing

type mockQueryEnhancer struct {
	result      *query.Enhanced
	err         error
	lastRequest *query.Request
}

func (m *mockQueryEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
	m.lastRequest = req
	return m.result, m.err
}

type mockReranker struct{}

func (m *mockReranker) Rerank(ctx context.Context, results []*reranker.Result) ([]*reranker.Result, error) {
	return results, nil
}

type mockRetriever struct {
	result   *retriever.Result
	err      error
	closeErr error
}

func (m *mockRetriever) Retrieve(ctx context.Context, req *retriever.Query) (*retriever.Result, error) {
	return m.result, m.err
}

func (m *mockRetriever) Close() error {
	return m.closeErr
}

type errorEmbedder struct{}

func (e *errorEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return nil, fmt.Errorf("embedding error")
}

func (e *errorEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	return nil, nil, fmt.Errorf("embedding error")
}

func (e *errorEmbedder) GetDimensions() int {
	return 0
}

type errorVectorStore struct{}

func (e *errorVectorStore) Add(ctx context.Context, doc *document.Document, emb []float64) error {
	return fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	return nil, nil, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Update(ctx context.Context, doc *document.Document, emb []float64) error {
	return fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Delete(ctx context.Context, id string) error {
	return fmt.Errorf("vector store error")
}

func (e *errorVectorStore) DeleteByFilter(ctx context.Context, filter map[string]interface{}) (int, error) {
	return 0, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) FlushAll(ctx context.Context) error {
	return fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Search(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return nil, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Close() error {
	return fmt.Errorf("vector store close error")
}
