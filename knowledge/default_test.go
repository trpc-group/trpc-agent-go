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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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

func TestGetAllMetadata(t *testing.T) {
	// Create sources with different metadata
	source1 := &mockSource{name: "source1", docCount: 5}
	source2 := &mockSource{name: "source2", docCount: 3}

	kb := New(WithSources([]source.Source{source1, source2}))

	allMetadata, err := kb.GetAllMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetAllMetadata returned error: %v", err)
	}

	// Verify category (both sources have ["test", "demo"] - should be 1 unique array)
	categories := allMetadata["category"]
	if len(categories) != 1 {
		t.Errorf("Expected 1 unique category array, got %d: %v", len(categories), categories)
	}

	// Verify names are collected from both sources (2 different arrays)
	names := allMetadata["name"]
	if len(names) != 2 {
		t.Errorf("Expected 2 unique name arrays, got %d: %v", len(names), names)
	}

	// Verify docCount (2 different arrays)
	docCounts := allMetadata["docCount"]
	if len(docCounts) != 2 {
		t.Errorf("Expected 2 unique docCount arrays, got %d: %v", len(docCounts), docCounts)
	}

	// Verify type (both sources have ["mock"] - should be 1 unique array)
	types := allMetadata["type"]
	if len(types) != 1 {
		t.Errorf("Expected 1 unique type array, got %d: %v", len(types), types)
	}
}

func TestGetAllMetadataKeys(t *testing.T) {
	// Create sources with metadata
	source1 := &mockSource{name: "source1", docCount: 5}
	source2 := &mockSource{name: "source2", docCount: 3}

	kb := New(WithSources([]source.Source{source1, source2}))

	allMetadataKeys, err := kb.GetAllMetadataKeys(context.Background())
	if err != nil {
		t.Fatalf("GetAllMetadataKeys returned error: %v", err)
	}

	// Verify that we get all expected keys
	expectedKeys := map[string]bool{
		"name":     true,
		"docCount": true,
		"type":     true,
		"category": true,
	}

	if len(allMetadataKeys) != len(expectedKeys) {
		t.Errorf("Expected %d unique keys, got %d: %v", len(expectedKeys), len(allMetadataKeys), allMetadataKeys)
	}

	for _, key := range allMetadataKeys {
		if !expectedKeys[key] {
			t.Errorf("Unexpected key: %s", key)
		}
	}
}
