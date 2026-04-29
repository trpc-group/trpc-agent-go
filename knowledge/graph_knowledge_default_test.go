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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
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

type stubGraphSource struct {
	data *graph.Data
}

func (s *stubGraphSource) ReadGraph(ctx context.Context) (*graph.Data, error) {
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
