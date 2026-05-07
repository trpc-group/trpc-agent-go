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
	"errors"
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

type stubGraphStore struct {
	nodes        []*graph.Node
	edges        []*graph.Edge
	traverseReq  *graph.TraverseQuery
	traverseResp *graph.TraverseResult
	pathReq      *graph.PathQuery
	pathResp     *graph.PathResult
}

func (s *stubGraphStore) AddNodes(
	ctx context.Context,
	nodes []*graph.Node,
) error {
	s.nodes = append(s.nodes, nodes...)
	return nil
}

func (s *stubGraphStore) AddEdges(
	ctx context.Context,
	edges []*graph.Edge,
) error {
	s.edges = append(s.edges, edges...)
	return nil
}

func (s *stubGraphStore) Traverse(
	ctx context.Context,
	query *graph.TraverseQuery,
) (*graph.TraverseResult, error) {
	s.traverseReq = query
	if s.traverseResp != nil {
		return s.traverseResp, nil
	}
	return &graph.TraverseResult{}, nil
}

func (s *stubGraphStore) FindPaths(
	ctx context.Context,
	query *graph.PathQuery,
) (*graph.PathResult, error) {
	s.pathReq = query
	if s.pathResp != nil {
		return s.pathResp, nil
	}
	return &graph.PathResult{}, nil
}

func (s *stubGraphStore) Close() error { return nil }

type stubGraphSource struct {
	data *graph.Data
}

func (s *stubGraphSource) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	return s.data, nil
}

type stubGraphEmbedder struct{}

func (stubGraphEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	return []float64{1}, nil
}

func (stubGraphEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	return []float64{1}, nil, nil
}

func (stubGraphEmbedder) GetDimensions() int {
	return 1
}

type recordingGraphEmbedder struct {
	texts []string
}

func (e *recordingGraphEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.texts = append(e.texts, text)
	return []float64{1}, nil
}

func (e *recordingGraphEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (e *recordingGraphEmbedder) GetDimensions() int {
	return 1
}

type fixedScoreGraphVectorStore struct {
	score float64
	doc   *document.Document
}

func (s *fixedScoreGraphVectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	return nil
}

func (s *fixedScoreGraphVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) Delete(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) Search(
	ctx context.Context,
	query *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	return &vectorstore.SearchResult{Results: []*vectorstore.ScoredDocument{{
		Document: s.doc,
		Score:    s.score,
	}}}, nil
}

func (s *fixedScoreGraphVectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	return errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) UpdateByFilter(
	ctx context.Context,
	opts ...vectorstore.UpdateByFilterOption,
) (int64, error) {
	return 0, errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	return 0, errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, errors.New("not implemented")
}

func (s *fixedScoreGraphVectorStore) Close() error {
	return nil
}

func TestBuiltinGraphKnowledge_LoadGraphSourceAndSearch(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{
			{
				ID:      "example.com/demo.Service.Do",
				Name:    "Do",
				Content: "Service Do method body",
			},
		},
		Edges: []*graph.Edge{
			{
				FromID: "example.com/demo.Service",
				ToID:   "example.com/demo.Service.Do",
				Type:   "METHOD",
			},
		},
	}}

	if err := gk.LoadGraphSource(context.Background(), src); err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	if len(store.nodes) == 0 {
		t.Fatal("expected AddNodes to receive graph nodes")
	}
	if len(store.edges) == 0 {
		t.Fatal("expected AddEdges to receive graph edges")
	}

	result, err := gk.Search(context.Background(), &SearchRequest{Query: "Do", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result == nil || len(result.Documents) == 0 {
		t.Fatalf("unexpected search result: %+v", result)
	}
}

func TestBuiltinGraphKnowledge_SearchPreservesVectorScore(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{
			score: 0.42,
			doc: &document.Document{
				ID:      "node-1",
				Name:    "node",
				Content: "node content",
			},
		}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)

	result, err := gk.Search(context.Background(), &SearchRequest{Query: "node"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Score != 0.42 {
		t.Fatalf("Score = %v, want 0.42", result.Score)
	}
	if len(result.Documents) != 1 || result.Documents[0].Score != 0.42 {
		t.Fatalf("Documents = %+v, want preserved score", result.Documents)
	}
}

func TestNewGraphLoadConfig_OverrideConcurrency(t *testing.T) {
	want := GraphLoadConcurrency{AddNodeRoutines: 3, AddEdgeRoutines: 4, EmbeddingRoutines: 5}
	cfg := newGraphLoadConfig(WithGraphLoadConcurrency(want))
	if cfg.concurrency != want {
		t.Fatalf("concurrency = %+v, want %+v", cfg.concurrency, want)
	}
}

func TestNewGraphLoadConfig_DefaultConcurrency(t *testing.T) {
	cfg := newGraphLoadConfig()
	if cfg.concurrency.AddNodeRoutines != defaultGraphStoreRoutines {
		t.Fatalf("AddNodeRoutines = %d, want %d", cfg.concurrency.AddNodeRoutines, defaultGraphStoreRoutines)
	}
	if cfg.concurrency.AddEdgeRoutines != defaultGraphStoreRoutines {
		t.Fatalf("AddEdgeRoutines = %d, want %d", cfg.concurrency.AddEdgeRoutines, defaultGraphStoreRoutines)
	}
	if cfg.concurrency.EmbeddingRoutines != defaultGraphDocumentRoutines {
		t.Fatalf("EmbeddingRoutines = %d, want %d", cfg.concurrency.EmbeddingRoutines, defaultGraphDocumentRoutines)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceRequiresConsistentBackends(t *testing.T) {
	src := &stubGraphSource{data: &graph.Data{}}
	tests := []struct {
		name string
		gk   *BuiltinGraphKnowledge
		want string
	}{
		{
			name: "missing graph store",
			gk: NewGraphKnowledge(
				WithGraphVectorStore(inmemory.New()),
				WithGraphEmbedder(stubGraphEmbedder{}),
			),
			want: "graph store is not configured",
		},
		{
			name: "graph only",
			gk: NewGraphKnowledge(
				WithGraphStore(&stubGraphStore{}),
			),
			want: "graph vector store is not configured",
		},
		{
			name: "embedder without vector store",
			gk: NewGraphKnowledge(
				WithGraphStore(&stubGraphStore{}),
				WithGraphEmbedder(stubGraphEmbedder{}),
			),
			want: "graph vector store is not configured",
		},
		{
			name: "missing embedder",
			gk: NewGraphKnowledge(
				WithGraphStore(&stubGraphStore{}),
				WithGraphVectorStore(inmemory.New()),
			),
			want: "graph embedder is not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.gk.LoadGraphSource(context.Background(), src)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("LoadGraphSource() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceIndexesVectorStoreDocuments(t *testing.T) {
	vectorStore := inmemory.New()
	embedder := &recordingGraphEmbedder{}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(vectorStore),
		WithGraphEmbedder(embedder),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{
			ID:      "example.com/demo.Service.Do",
			Name:    "Do",
			Content: "Service Do method body",
		}},
	}}

	if err := gk.LoadGraphSource(context.Background(), src); err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	doc, _, err := vectorStore.Get(context.Background(), "example.com/demo.Service.Do")
	if err != nil {
		t.Fatalf("vectorStore.Get() error = %v", err)
	}
	if doc.EmbeddingText != "" {
		t.Fatalf("EmbeddingText = %q, want empty graph document embedding text", doc.EmbeddingText)
	}
	if len(embedder.texts) != 1 || embedder.texts[0] != "Service Do method body" {
		t.Fatalf("embedding texts = %+v, want graph node content", embedder.texts)
	}
}

func TestGraphSeedEmbeddingTextUsesStructuredFieldsAndTruncatesContent(t *testing.T) {
	longContent := strings.Repeat("x", defaultGraphNodeContentRunes+1)
	doc := &document.Document{
		ID:      "node-1",
		Name:    "adminPageHTML",
		Content: longContent,
		Metadata: map[string]any{
			codeast.TrpcAstMetaPrefix + "type":      "Variable",
			codeast.TrpcAstMetaPrefix + "full_name": "trpc.group/trpc-go/trpc-agent-go/openclaw.adminPageHTML",
			codeast.TrpcAstMetaPrefix + "file_path": "openclaw/admin.go",
			codeast.TrpcAstMetaPrefix + "signature": "const adminPageHTML = `...`",
			codeast.TrpcAstMetaPrefix + "comment":   "admin page template",
		},
	}

	text := graphSeedEmbeddingText(doc)
	for _, want := range []string{
		"type: Variable",
		"name: adminPageHTML",
		"full_name: trpc.group/trpc-go/trpc-agent-go/openclaw.adminPageHTML",
		"file_path: openclaw/admin.go",
		"signature: const adminPageHTML = `...`",
		"comment: admin page template",
		"...<truncated>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("graphSeedEmbeddingText() missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, longContent) {
		t.Fatalf("graphSeedEmbeddingText() contains full oversized content")
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceTruncatesStoredContent(t *testing.T) {
	store := &stubGraphStore{}
	vectorStore := inmemory.New()
	embedder := &recordingGraphEmbedder{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(vectorStore),
		WithGraphEmbedder(embedder),
	)
	longContent := strings.Repeat("x", defaultGraphNodeContentRunes+1)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{
			ID:      "node-1",
			Name:    "node",
			Content: longContent,
		}},
	}}

	if err := gk.LoadGraphSource(context.Background(), src); err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("stored nodes = %d, want 1", len(store.nodes))
	}
	if !strings.HasSuffix(store.nodes[0].Content, "...<truncated>") {
		t.Fatalf("stored graph content was not truncated")
	}
	if strings.Contains(store.nodes[0].Content, longContent) {
		t.Fatalf("stored graph content contains full oversized content")
	}
	doc, _, err := vectorStore.Get(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("vectorStore.Get() error = %v", err)
	}
	if !strings.HasSuffix(doc.Content, "...<truncated>") {
		t.Fatalf("vector document content was not truncated")
	}
	if len(embedder.texts) != 1 || !strings.HasSuffix(embedder.texts[0], "...<truncated>") {
		t.Fatalf("embedding texts = %+v, want truncated content", embedder.texts)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceAndVectorSeeds(t *testing.T) {
	store := &stubGraphStore{}
	vectorStore := inmemory.New()
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(vectorStore),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{
			ID:      "example.com/demo.Service.Do",
			Name:    "Do",
			Content: "Service Do method body",
		}},
		Edges: []*graph.Edge{{
			FromID: "example.com/demo.Service.Do",
			ToID:   "example.com/demo.Helper",
			Type:   "CALLS",
		}},
	}}

	if err := gk.LoadGraphSource(context.Background(), src); err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	result, err := gk.Search(context.Background(), &SearchRequest{
		Query:      "natural language",
		MaxResults: 1,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Documents) != 1 || result.Documents[0].Document.ID != "example.com/demo.Service.Do" {
		t.Fatalf("unexpected search documents: %+v", result.Documents)
	}
}

type failingGraphEmbedder struct {
	failAfter int
	count     int
}

func (e *failingGraphEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.count++
	if e.count > e.failAfter {
		return nil, errors.New("embedding failure")
	}
	return []float64{1}, nil
}

func (e *failingGraphEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (e *failingGraphEmbedder) GetDimensions() int {
	return 1
}

func TestBuiltinGraphKnowledge_LoadGraphSourceWorkerFailure(t *testing.T) {
	store := &stubGraphStore{}
	nodes := make([]*graph.Node, 10)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("node-%d", i),
			Name:    fmt.Sprintf("Node%d", i),
			Content: "content",
		}
	}
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes}}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(&failingGraphEmbedder{failAfter: 3}),
	)

	err := gk.LoadGraphSource(context.Background(), src, WithGraphLoadConcurrency(GraphLoadConcurrency{
		EmbeddingRoutines: 4,
	}))
	if err == nil {
		t.Fatal("expected error from worker failure")
	}
	if !strings.Contains(err.Error(), "embedding failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceContextCancellation(t *testing.T) {
	store := &stubGraphStore{}
	nodes := make([]*graph.Node, 20)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("node-%d", i),
			Name:    fmt.Sprintf("Node%d", i),
			Content: "content",
		}
	}
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes}}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := gk.LoadGraphSource(ctx, src, WithGraphLoadConcurrency(GraphLoadConcurrency{
		AddNodeRoutines:   2,
		EmbeddingRoutines: 4,
	}))
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}
