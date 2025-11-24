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
	"sync"
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
			Metadata: map[string]any{
				"category":            fmt.Sprintf("cat-%d", i%3), // Categories: cat-0, cat-1, cat-2
				"level":               i%2 + 1,                    // Levels: 1, 2
				source.MetaSourceName: "test",
				source.MetaURI:        "test-uri",
				source.MetaChunkIndex: i,
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

// TestBuiltinKnowledge_LoadOptions is redundant with TestLoadOptions, removed

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
func (*stubVectorStore) DeleteByFilter(
	ctx context.Context,
	opts ...vectorstore.DeleteOption) error {
	return nil
}
func (*stubVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, nil
}
func (*stubVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	return 0, nil
}
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
				if !strings.Contains(err.Error(), tt.expectedErrMsg) {
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
				if !strings.Contains(err.Error(), tt.expectedErrMsg) {
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
		Metadata: map[string]any{
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
			expectError:    true,
			expectedErrMsg: "vector store is not configured",
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
		{
			name: "nil_embedder_with_vectorstore",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{
					embedder:    nil,
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
				if !strings.Contains(err.Error(), tt.expectedErrMsg) {
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

// TestAddDocument_EmptyEmbedding tests adding document with empty embedding (for remote embedding)
func TestAddDocument_EmptyEmbedding(t *testing.T) {
	doc := &document.Document{
		ID:      "test-doc",
		Content: "test content",
	}

	// Create a vector store that tracks the embedding it receives
	vs := &trackingVectorStore{}

	kb := &BuiltinKnowledge{
		embedder:    nil, // No embedder - should pass empty embedding
		vectorStore: vs,
	}

	err := kb.addDocument(context.Background(), doc)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that empty embedding was passed
	if len(vs.lastEmbedding) != 0 {
		t.Errorf("Expected empty embedding, got length %d", len(vs.lastEmbedding))
	}
}

// trackingVectorStore is a mock vector store that tracks the last embedding it received
type trackingVectorStore struct {
	lastEmbedding []float64
}

func (t *trackingVectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	t.lastEmbedding = embedding
	return nil
}

func (t *trackingVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	return nil, nil, nil
}

func (t *trackingVectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return nil
}

func (t *trackingVectorStore) Delete(ctx context.Context, id string) error {
	return nil
}

func (t *trackingVectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return &vectorstore.SearchResult{}, nil
}

func (t *trackingVectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	return nil
}

func (t *trackingVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	return 0, nil
}

func (t *trackingVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, nil
}

func (t *trackingVectorStore) Close() error {
	return nil
}

// containsString helper removed - use strings.Contains directly

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

func (m *mockReranker) Rerank(ctx context.Context, query *reranker.Query, results []*reranker.Result) ([]*reranker.Result, error) {
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

func (e *errorVectorStore) DeleteByFilter(
	ctx context.Context,
	opts ...vectorstore.DeleteOption) error {
	return fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Search(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return nil, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	return 0, fmt.Errorf("vector store error")
}

func (e *errorVectorStore) Close() error {
	return fmt.Errorf("vector store close error")
}

// syncMockVectorStore is a mock vector store with support for incremental sync testing
type syncMockVectorStore struct {
	documents    map[string]vectorstore.DocumentMetadata
	deleteCalls  int
	addCalls     int
	getMetaCalls int
	// protect concurrent access to documents and counters
	mu sync.RWMutex
}

func newSyncMockVectorStore() *syncMockVectorStore {
	return &syncMockVectorStore{
		documents: make(map[string]vectorstore.DocumentMetadata),
	}
}

func (s *syncMockVectorStore) Add(ctx context.Context, doc *document.Document, emb []float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addCalls++
	meta := vectorstore.DocumentMetadata{
		Metadata: doc.Metadata,
	}
	s.documents[doc.ID] = meta
	return nil
}

func (s *syncMockVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	s.mu.RLock()
	meta, exists := s.documents[id]
	s.mu.RUnlock()
	if !exists {
		return nil, nil, nil
	}
	doc := &document.Document{
		ID:       id,
		Metadata: meta.Metadata,
	}
	return doc, []float64{1, 2, 3}, nil
}

func (s *syncMockVectorStore) Update(ctx context.Context, doc *document.Document, emb []float64) error {
	return nil
}

func (s *syncMockVectorStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	delete(s.documents, id)
	s.deleteCalls++
	s.mu.Unlock()
	return nil
}

func (s *syncMockVectorStore) DeleteByFilter(
	ctx context.Context,
	opts ...vectorstore.DeleteOption) error {
	s.mu.Lock()
	s.deleteCalls++
	config := &vectorstore.DeleteConfig{}
	for _, opt := range opts {
		opt(config)
	}

	if config.DeleteAll {
		s.documents = make(map[string]vectorstore.DocumentMetadata)
		s.mu.Unlock()
		return nil
	}

	for _, id := range config.DocumentIDs {
		delete(s.documents, id)
	}

	if config.Filter != nil {
		for id, meta := range s.documents {
			if sourceName, ok := meta.Metadata[source.MetaSourceName]; ok {
				if config.Filter[source.MetaSourceName] == sourceName {
					delete(s.documents, id)
				}
			}
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *syncMockVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	s.mu.RLock()
	s.getMetaCalls++
	config := &vectorstore.GetMetadataConfig{}
	for _, opt := range opts {
		opt(config)
	}

	result := make(map[string]vectorstore.DocumentMetadata)
	for id, meta := range s.documents {
		if config.Filter != nil {
			if sourceName, ok := meta.Metadata[source.MetaSourceName]; ok {
				if config.Filter[source.MetaSourceName] == sourceName {
					result[id] = meta
				}
			}
		} else {
			result[id] = meta
		}
	}
	s.mu.RUnlock()
	return result, nil
}

func (s *syncMockVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	s.mu.RLock()
	n := len(s.documents)
	s.mu.RUnlock()
	return n, nil
}

func (s *syncMockVectorStore) Search(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return nil, nil
}

func (s *syncMockVectorStore) Close() error {
	return nil
}

// TestAddSource tests the AddSource functionality
func TestBuiltinKnowledge_AddSource(t *testing.T) {
	tests := []struct {
		name        string
		enableSync  bool
		expectError bool
	}{
		{
			name:        "with_sync_enabled",
			enableSync:  true,
			expectError: false,
		},
		{
			name:        "with_sync_disabled",
			enableSync:  false,
			expectError: false,
		},
		{
			name:        "duplicate_source",
			enableSync:  false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New(
				WithEnableSourceSync(tt.enableSync),
			)
			kb.vectorStore = newSyncMockVectorStore()
			kb.embedder = stubEmbedder{}

			// Add first source
			src1 := &mockSource{name: "test-source", docCount: 2}
			err := kb.AddSource(context.Background(), src1)
			if err != nil {
				t.Fatalf("Failed to add first source: %v", err)
			}

			if tt.name == "duplicate_source" {
				// Try to add duplicate source
				src2 := &mockSource{name: "test-source", docCount: 3}
				err = kb.AddSource(context.Background(), src2)
				if tt.expectError && err == nil {
					t.Error("Expected error when adding duplicate source, but got none")
				}
				if !tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}

			// Verify source was added to internal list
			if len(kb.sources) != 1 {
				t.Errorf("Expected 1 source, got %d", len(kb.sources))
			}
			if kb.sources[0].Name() != "test-source" {
				t.Errorf("Expected source name 'test-source', got '%s'", kb.sources[0].Name())
			}
		})
	}
}

// TestRemoveSource tests the RemoveSource functionality
func TestBuiltinKnowledge_RemoveSource(t *testing.T) {
	tests := []struct {
		name        string
		enableSync  bool
		expectError bool
	}{
		{
			name:        "with_sync_enabled",
			enableSync:  true,
			expectError: false,
		},
		{
			name:        "with_sync_disabled",
			enableSync:  false,
			expectError: false,
		},
		{
			name:        "nonexistent_source_sync_disabled",
			enableSync:  false,
			expectError: true,
		},
		{
			name:        "nonexistent_source_sync_enabled",
			enableSync:  true,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New(
				WithEnableSourceSync(tt.enableSync),
			)
			store := newSyncMockVectorStore()
			kb.vectorStore = store
			kb.embedder = stubEmbedder{}

			// Add a source first
			src := &mockSource{name: "test-source", docCount: 2}
			err := kb.AddSource(context.Background(), src)
			if err != nil {
				t.Fatalf("Failed to add source: %v", err)
			}

			if strings.Contains(tt.name, "nonexistent_source") {
				// Try to remove non-existent source
				err = kb.RemoveSource(context.Background(), "nonexistent")
				if tt.expectError && err == nil {
					t.Error("Expected error when removing non-existent source, but got none")
				}
				if !tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			} else {
				// Remove the source
				err = kb.RemoveSource(context.Background(), "test-source")
				if tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				// Verify source was removed from internal list
				if len(kb.sources) != 0 {
					t.Errorf("Expected 0 sources, got %d", len(kb.sources))
				}

				// Verify vector store deletion was called
				if store.deleteCalls == 0 {
					t.Error("Expected vector store deletion to be called")
				}
			}
		})
	}
}

// TestReloadSource tests the ReloadSource functionality
func TestBuiltinKnowledge_ReloadSource(t *testing.T) {
	tests := []struct {
		name        string
		enableSync  bool
		expectError bool
	}{
		{
			name:        "with_sync_enabled",
			enableSync:  true,
			expectError: false,
		},
		{
			name:        "with_sync_disabled",
			enableSync:  false,
			expectError: false,
		},
		{
			name:        "nonexistent_source_sync_disabled",
			enableSync:  false,
			expectError: true,
		},
		{
			name:        "nonexistent_source_sync_enabled",
			enableSync:  true,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New(
				WithEnableSourceSync(tt.enableSync),
			)
			store := newSyncMockVectorStore()
			kb.vectorStore = store
			kb.embedder = stubEmbedder{}

			// Add a source first
			src := &mockSource{name: "test-source", docCount: 2}
			err := kb.AddSource(context.Background(), src)
			if err != nil {
				t.Fatalf("Failed to add source: %v", err)
			}

			if strings.Contains(tt.name, "nonexistent_source") {
				// Try to reload non-existent source
				err = kb.ReloadSource(context.Background(), &mockSource{name: "nonexistent", docCount: 1})
				if tt.expectError && err == nil {
					t.Error("Expected error when reloading non-existent source, but got none")
				}
				if !tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			} else {
				// Reload the source with different document count
				newSrc := &mockSource{name: "test-source", docCount: 3}
				err = kb.ReloadSource(context.Background(), newSrc)
				if tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !tt.expectError && err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				// Verify source is still in the list
				if len(kb.sources) != 1 {
					t.Errorf("Expected 1 source, got %d", len(kb.sources))
				}
			}
		})
	}
}

// TestIncrementalSyncFunctions tests the incremental sync helper functions
func TestIncrementalSyncFunctions(t *testing.T) {
	kb := New(WithEnableSourceSync(true))
	store := newSyncMockVectorStore()
	kb.vectorStore = store
	kb.embedder = stubEmbedder{}

	ctx := context.Background()

	// Test refreshSourceDocInfo
	err := kb.refreshSourceDocInfo(ctx, "test-source")
	if err != nil {
		t.Fatalf("refreshSourceDocInfo failed: %v", err)
	}

	// Test refreshAllDocInfo
	err = kb.refreshAllDocInfo(ctx)
	if err != nil {
		t.Fatalf("refreshAllDocInfo failed: %v", err)
	}

	// Test convertMetaToDocumentInfo
	meta := &vectorstore.DocumentMetadata{
		Metadata: map[string]any{
			source.MetaURI:        "test://uri",
			source.MetaSourceName: "test-source",
			source.MetaChunkIndex: 0,
		},
	}
	docInfo := convertMetaToDocumentInfo("doc-1", meta)
	if docInfo.URI != "test://uri" {
		t.Errorf("Expected URI 'test://uri', got '%s'", docInfo.URI)
	}
	if docInfo.SourceName != "test-source" {
		t.Errorf("Expected SourceName 'test-source', got '%s'", docInfo.SourceName)
	}
	if docInfo.ChunkIndex != 0 {
		t.Errorf("Expected ChunkIndex 0, got %d", docInfo.ChunkIndex)
	}

	// Test rebuildDocumentInfo
	kb.cacheMetaInfo = map[string]BuiltinDocumentInfo{
		"doc-1": docInfo,
	}
	kb.rebuildDocumentInfo()

	if len(kb.cacheURIInfo["test://uri"]) != 1 {
		t.Errorf("Expected 1 document in cacheURIInfo, got %d", len(kb.cacheURIInfo["test://uri"]))
	}
	if len(kb.cacheSourceInfo["test-source"]) != 1 {
		t.Errorf("Expected 1 document in cacheSourceInfo, got %d", len(kb.cacheSourceInfo["test-source"]))
	}

	// Test generateDocumentID
	docID := generateDocumentID("test-source", "test://uri", "content", 0, map[string]any{"key": "value"})
	if docID == "" {
		t.Error("generateDocumentID returned empty string")
	}

	// Test shouldProcessDocument
	doc := &document.Document{
		ID:      docID,
		Content: "content",
		Metadata: map[string]any{
			source.MetaURI:        "test://uri",
			source.MetaSourceName: "test-source",
			source.MetaChunkIndex: 0,
		},
	}
	shouldProcess, err := kb.shouldProcessDocument(doc)
	if err != nil {
		t.Fatalf("shouldProcessDocument failed: %v", err)
	}
	if !shouldProcess {
		t.Error("Expected document to be processed")
	}

	// Test cleanupOrphanDocuments
	err = kb.cleanupOrphanDocuments(ctx)
	if err != nil {
		t.Fatalf("cleanupOrphanDocuments failed: %v", err)
	}

	// Test clearVectorStoreMetadata
	kb.clearVectorStoreMetadata()
	if len(kb.cacheMetaInfo) != 0 {
		t.Error("Expected cacheMetaInfo to be cleared")
	}
}

// TestConvertMetaToDocumentInfo_EdgeCases tests convertMetaToDocumentInfo with various edge cases
func TestConvertMetaToDocumentInfo_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		docID    string
		meta     *vectorstore.DocumentMetadata
		validate func(*testing.T, BuiltinDocumentInfo)
	}{
		{
			name:  "missing_uri",
			docID: "doc-1",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaSourceName: "test-source",
					source.MetaChunkIndex: 0,
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.URI != "" {
					t.Errorf("Expected empty URI, got '%s'", info.URI)
				}
			},
		},
		{
			name:  "missing_source_name",
			docID: "doc-2",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaChunkIndex: 1,
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.SourceName != "" {
					t.Errorf("Expected empty SourceName, got '%s'", info.SourceName)
				}
			},
		},
		{
			name:  "missing_chunk_index",
			docID: "doc-3",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.ChunkIndex != 0 {
					t.Errorf("Expected ChunkIndex 0, got %d", info.ChunkIndex)
				}
			},
		},
		{
			name:  "float64_chunk_index",
			docID: "doc-4",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
					source.MetaChunkIndex: float64(5),
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.ChunkIndex != 5 {
					t.Errorf("Expected ChunkIndex 5, got %d", info.ChunkIndex)
				}
			},
		},
		{
			name:  "string_chunk_index",
			docID: "doc-5",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
					source.MetaChunkIndex: "10",
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.ChunkIndex != 10 {
					t.Errorf("Expected ChunkIndex 10, got %d", info.ChunkIndex)
				}
			},
		},
		{
			name:  "invalid_chunk_index",
			docID: "doc-6",
			meta: &vectorstore.DocumentMetadata{
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
					source.MetaChunkIndex: "invalid",
				},
			},
			validate: func(t *testing.T, info BuiltinDocumentInfo) {
				if info.ChunkIndex != 0 {
					t.Errorf("Expected ChunkIndex 0, got %d", info.ChunkIndex)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := convertMetaToDocumentInfo(tt.docID, tt.meta)
			if info.DocumentID != tt.docID {
				t.Errorf("Expected DocumentID '%s', got '%s'", tt.docID, info.DocumentID)
			}
			if tt.validate != nil {
				tt.validate(t, info)
			}
		})
	}
}

// TestLoad_WithRecreate tests Load with recreate option
func TestLoad_WithRecreate(t *testing.T) {
	kb := New(
		WithSources([]source.Source{
			&mockSource{name: "test-source", docCount: 3},
		}),
		WithEnableSourceSync(true),
	)
	store := newSyncMockVectorStore()
	kb.vectorStore = store
	kb.embedder = stubEmbedder{}

	ctx := context.Background()

	// Load first time with recreate
	err := kb.Load(ctx, WithRecreate(true))
	if err != nil {
		t.Fatalf("Load with recreate failed: %v", err)
	}

	// Verify documents were added
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 documents, got %d", count)
	}

	// Load again with recreate
	err = kb.Load(ctx, WithRecreate(true))
	if err != nil {
		t.Fatalf("Second load with recreate failed: %v", err)
	}
}

// TestLoad_WithSync tests Load with sync enabled
func TestLoad_WithSync(t *testing.T) {
	kb := New(
		WithSources([]source.Source{
			&mockSource{name: "test-source", docCount: 2},
		}),
		WithEnableSourceSync(true),
	)
	store := newSyncMockVectorStore()
	kb.vectorStore = store
	kb.embedder = stubEmbedder{}

	ctx := context.Background()

	// Load first time
	err := kb.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify documents were added
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 documents, got %d", count)
	}

	// Load again (should handle incremental sync)
	err = kb.Load(ctx)
	if err != nil {
		t.Fatalf("Second load failed: %v", err)
	}
}

// TestResetDocumentID tests resetDocumentID edge cases
func TestResetDocumentID(t *testing.T) {
	src := &mockSource{name: "test-source", docCount: 1}

	tests := []struct {
		name        string
		doc         *document.Document
		expectError bool
		errContains string
	}{
		{
			name: "nil_metadata",
			doc: &document.Document{
				ID:       "doc-1",
				Content:  "test",
				Metadata: nil,
			},
			expectError: true,
			errContains: "metadata is nil",
		},
		{
			name: "missing_uri",
			doc: &document.Document{
				ID:      "doc-2",
				Content: "test",
				Metadata: map[string]any{
					source.MetaChunkIndex: 0,
				},
			},
			expectError: true,
			errContains: "missing URI metadata",
		},
		{
			name: "missing_chunk_index",
			doc: &document.Document{
				ID:      "doc-3",
				Content: "test",
				Metadata: map[string]any{
					source.MetaURI: "test://uri",
				},
			},
			expectError: true,
			errContains: "missing chunk index metadata",
		},
		{
			name: "invalid_chunk_index_type",
			doc: &document.Document{
				ID:      "doc-4",
				Content: "test",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaChunkIndex: []int{1, 2, 3}, // invalid type
				},
			},
			expectError: true,
			errContains: "chunk index is not an integer",
		},
		{
			name: "success",
			doc: &document.Document{
				ID:      "doc-5",
				Content: "test",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaChunkIndex: 0,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New()
			err := kb.resetDocumentID(tt.doc, src)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				// Verify document ID was set
				if tt.doc.ID == "" {
					t.Error("Expected document ID to be set")
				}
				// Verify source name was set
				if tt.doc.Metadata[source.MetaSourceName] != src.Name() {
					t.Errorf("Expected source name '%s', got '%v'", src.Name(), tt.doc.Metadata[source.MetaSourceName])
				}
			}
		})
	}
}

// TestShouldProcessDocument tests shouldProcessDocument edge cases
func TestShouldProcessDocument(t *testing.T) {
	tests := []struct {
		name          string
		setupKB       func() *BuiltinKnowledge
		doc           *document.Document
		expectError   bool
		expectProcess bool
		errContains   string
	}{
		{
			name: "missing_uri",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
				return kb
			},
			doc: &document.Document{
				ID: "doc-1",
				Metadata: map[string]any{
					source.MetaSourceName: "test-source",
				},
			},
			expectError: true,
			errContains: "missing or invalid URI metadata",
		},
		{
			name: "missing_source_name",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
				return kb
			},
			doc: &document.Document{
				ID: "doc-2",
				Metadata: map[string]any{
					source.MetaURI: "test://uri",
				},
			},
			expectError: true,
			errContains: "missing or invalid source name metadata",
		},
		{
			name: "already_processed",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
				kb.processedDocIDs.Store("doc-3", struct{}{})
				return kb
			},
			doc: &document.Document{
				ID: "doc-3",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
				},
			},
			expectError:   false,
			expectProcess: false,
		},
		{
			name: "already_processing",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
				kb.processingDocIDs.Store("doc-4", struct{}{})
				return kb
			},
			doc: &document.Document{
				ID: "doc-4",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
				},
			},
			expectError:   false,
			expectProcess: false,
		},
		{
			name: "new_document",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
				return kb
			},
			doc: &document.Document{
				ID: "doc-5",
				Metadata: map[string]any{
					source.MetaURI:        "test://new-uri",
					source.MetaSourceName: "test-source",
				},
			},
			expectError:   false,
			expectProcess: true,
		},
		{
			name: "document_unchanged",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = map[string][]BuiltinDocumentInfo{
					"test://uri": {
						{
							DocumentID: "doc-6",
							URI:        "test://uri",
							SourceName: "test-source",
						},
					},
				}
				return kb
			},
			doc: &document.Document{
				ID: "doc-6",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
				},
			},
			expectError:   false,
			expectProcess: false,
		},
		{
			name: "document_changed",
			setupKB: func() *BuiltinKnowledge {
				kb := New(WithEnableSourceSync(true))
				kb.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
				kb.cacheURIInfo = map[string][]BuiltinDocumentInfo{
					"test://uri": {
						{
							DocumentID: "doc-7-old",
							URI:        "test://uri",
							SourceName: "test-source",
						},
					},
				}
				return kb
			},
			doc: &document.Document{
				ID: "doc-7-new",
				Metadata: map[string]any{
					source.MetaURI:        "test://uri",
					source.MetaSourceName: "test-source",
				},
			},
			expectError:   false,
			expectProcess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			shouldProcess, err := kb.shouldProcessDocument(tt.doc)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
					return
				}
				if shouldProcess != tt.expectProcess {
					t.Errorf("Expected shouldProcess=%v, got %v", tt.expectProcess, shouldProcess)
				}
			}
		})
	}
}

// TestSerializeMetadata tests serializeMetadata with various types
func TestSerializeMetadata(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{name: "simple_map", input: map[string]any{"key": "value"}},
		{name: "nested_map", input: map[string]any{"outer": map[string]any{"inner": "value"}}},
		{name: "slice", input: []any{"a", "b", "c"}},
		{name: "map_with_any_key", input: map[any]any{1: "one", "two": 2}},
		{name: "nil_value", input: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify it doesn't panic
			result := serializeMetadata(tt.input)
			if tt.input != nil && result == "" {
				t.Error("Expected non-empty result for non-nil input")
			}
		})
	}
}

// TestRebuildDocumentInfo_NilCache tests rebuildDocumentInfo with nil cache
func TestRebuildDocumentInfo_NilCache(t *testing.T) {
	kb := New()
	kb.cacheMetaInfo = nil

	// Should not panic
	kb.rebuildDocumentInfo()

	if kb.cacheURIInfo != nil || kb.cacheSourceInfo != nil {
		t.Error("Expected caches to remain nil")
	}
}

// mockVectorStoreWithMetadata is an enhanced mock that supports GetMetadata
type mockVectorStoreWithMetadata struct {
	stubVectorStore
	metadata map[string]vectorstore.DocumentMetadata
}

func (m *mockVectorStoreWithMetadata) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	if m.metadata == nil {
		return make(map[string]vectorstore.DocumentMetadata), nil
	}

	// Apply filters if any
	config := &vectorstore.GetMetadataConfig{}
	for _, opt := range opts {
		opt(config)
	}

	result := make(map[string]vectorstore.DocumentMetadata)
	for id, meta := range m.metadata {
		// Apply document ID filter
		if len(config.IDs) > 0 {
			found := false
			for _, docID := range config.IDs {
				if id == docID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Apply metadata filters
		if len(config.Filter) > 0 {
			match := true
			for key, value := range config.Filter {
				if metaValue, exists := meta.Metadata[key]; !exists || metaValue != value {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		result[id] = meta
	}

	return result, nil
}

// Test ShowDocumentInfo functionality using table-driven tests
func TestBuiltinKnowledge_ShowDocumentInfo(t *testing.T) {
	tests := []struct {
		name           string
		setupKB        func() *BuiltinKnowledge
		options        []ShowDocumentInfoOption
		expectError    bool
		expectedErrMsg string
		validateResult func([]BuiltinDocumentInfo) bool
	}{
		{
			name: "successful_show_all_documents",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: map[string]vectorstore.DocumentMetadata{
						"doc-1": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source-1",
								source.MetaURI:        "file:///test1.txt",
								source.MetaChunkIndex: 0,
								source.MetaChunkSize:  100,
								"category":            "test",
							},
						},
						"doc-2": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source-2",
								source.MetaURI:        "file:///test2.txt",
								source.MetaChunkIndex: 1,
								source.MetaChunkSize:  200,
								"category":            "demo",
							},
						},
					},
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options:     []ShowDocumentInfoOption{},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 2 &&
					docs[0].SourceName != "" &&
					docs[1].SourceName != ""
			},
		},
		{
			name: "filter_by_source_name",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: map[string]vectorstore.DocumentMetadata{
						"doc-1": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source-1",
								source.MetaURI:        "file:///test1.txt",
								source.MetaChunkIndex: 0,
							},
						},
						"doc-2": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source-2",
								source.MetaURI:        "file:///test2.txt",
								source.MetaChunkIndex: 1,
							},
						},
					},
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options: []ShowDocumentInfoOption{
				WithShowDocumentInfoSourceName("test-source-1"),
			},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 1 && docs[0].SourceName == "test-source-1"
			},
		},
		{
			name: "filter_by_document_ids",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: map[string]vectorstore.DocumentMetadata{
						"doc-1": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source",
								source.MetaURI:        "file:///test1.txt",
								source.MetaChunkIndex: 0,
							},
						},
						"doc-2": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source",
								source.MetaURI:        "file:///test2.txt",
								source.MetaChunkIndex: 1,
							},
						},
					},
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options: []ShowDocumentInfoOption{
				WithShowDocumentInfoIDs([]string{"doc-1"}),
			},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 1 && docs[0].URI == "file:///test1.txt"
			},
		},
		{
			name: "filter_by_metadata",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: map[string]vectorstore.DocumentMetadata{
						"doc-1": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source",
								source.MetaURI:        "file:///test1.txt",
								source.MetaChunkIndex: 0,
								"category":            "important",
							},
						},
						"doc-2": {
							Metadata: map[string]any{
								source.MetaSourceName: "test-source",
								source.MetaURI:        "file:///test2.txt",
								source.MetaChunkIndex: 1,
								"category":            "normal",
							},
						},
					},
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options: []ShowDocumentInfoOption{
				WithShowDocumentInfoFilter(map[string]any{"category": "important"}),
			},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 1 && docs[0].URI == "file:///test1.txt"
			},
		},
		{
			name: "no_vector_store_configured",
			setupKB: func() *BuiltinKnowledge {
				return &BuiltinKnowledge{}
			},
			options:        []ShowDocumentInfoOption{},
			expectError:    true,
			expectedErrMsg: "vector store not configured",
		},
		{
			name: "empty_result",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: make(map[string]vectorstore.DocumentMetadata),
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options:     []ShowDocumentInfoOption{},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 0
			},
		},
		{
			name: "multiple_filters_combined",
			setupKB: func() *BuiltinKnowledge {
				mockStore := &mockVectorStoreWithMetadata{
					metadata: map[string]vectorstore.DocumentMetadata{
						"doc-1": {
							Metadata: map[string]any{
								source.MetaSourceName: "source-1",
								source.MetaURI:        "file:///test1.txt",
								source.MetaChunkIndex: 0,
								"category":            "test",
							},
						},
						"doc-2": {
							Metadata: map[string]any{
								source.MetaSourceName: "source-2",
								source.MetaURI:        "file:///test2.txt",
								source.MetaChunkIndex: 1,
								"category":            "test",
							},
						},
						"doc-3": {
							Metadata: map[string]any{
								source.MetaSourceName: "source-1",
								source.MetaURI:        "file:///test3.txt",
								source.MetaChunkIndex: 2,
								"category":            "demo",
							},
						},
					},
				}
				kb := &BuiltinKnowledge{vectorStore: mockStore}
				return kb
			},
			options: []ShowDocumentInfoOption{
				WithShowDocumentInfoSourceName("source-1"),
				WithShowDocumentInfoFilter(map[string]any{"category": "test"}),
			},
			expectError: false,
			validateResult: func(docs []BuiltinDocumentInfo) bool {
				return len(docs) == 1 &&
					docs[0].SourceName == "source-1" &&
					docs[0].URI == "file:///test1.txt"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := tt.setupKB()
			result, err := kb.ShowDocumentInfo(context.Background(), tt.options...)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if tt.expectedErrMsg != "" && !strings.Contains(err.Error(), tt.expectedErrMsg) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tt.expectedErrMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.validateResult != nil && !tt.validateResult(result) {
				t.Errorf("Result validation failed for test case '%s'", tt.name)
			}
		})
	}
}
