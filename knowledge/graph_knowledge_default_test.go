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

func TestNewGraphKnowledgeOptions(t *testing.T) {
	gs := &stubGraphStore{}
	vs := &fixedScoreGraphVectorStore{}
	emb := stubGraphEmbedder{}

	tests := []struct {
		name  string
		opts  []GraphKnowledgeOption
		check func(t *testing.T, gk *BuiltinGraphKnowledge)
	}{
		{
			name: "no options leaves all fields nil",
			opts: nil,
			check: func(t *testing.T, gk *BuiltinGraphKnowledge) {
				if gk.store != nil {
					t.Fatal("store should be nil")
				}
				if gk.vectorStore != nil {
					t.Fatal("vectorStore should be nil")
				}
				if gk.embedder != nil {
					t.Fatal("embedder should be nil")
				}
			},
		},
		{
			name: "WithGraphStore sets store",
			opts: []GraphKnowledgeOption{WithGraphStore(gs)},
			check: func(t *testing.T, gk *BuiltinGraphKnowledge) {
				if gk.store != gs {
					t.Fatal("store not set")
				}
			},
		},
		{
			name: "WithGraphVectorStore sets vectorStore",
			opts: []GraphKnowledgeOption{WithGraphVectorStore(vs)},
			check: func(t *testing.T, gk *BuiltinGraphKnowledge) {
				if gk.vectorStore != vs {
					t.Fatal("vectorStore not set")
				}
			},
		},
		{
			name: "WithGraphEmbedder sets embedder",
			opts: []GraphKnowledgeOption{WithGraphEmbedder(emb)},
			check: func(t *testing.T, gk *BuiltinGraphKnowledge) {
				if gk.embedder == nil {
					t.Fatal("embedder not set")
				}
			},
		},
		{
			name: "all options together",
			opts: []GraphKnowledgeOption{WithGraphStore(gs), WithGraphVectorStore(vs), WithGraphEmbedder(emb)},
			check: func(t *testing.T, gk *BuiltinGraphKnowledge) {
				if gk.store != gs {
					t.Fatal("store not set")
				}
				if gk.vectorStore != vs {
					t.Fatal("vectorStore not set")
				}
				if gk.embedder == nil {
					t.Fatal("embedder not set")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gk := NewGraphKnowledge(tt.opts...)
			tt.check(t, gk)
		})
	}
}

func TestGraphLoadOptions(t *testing.T) {
	tests := []struct {
		name  string
		opts  []GraphLoadOption
		check func(t *testing.T, cfg *graphLoadConfig)
	}{
		{
			name: "WithGraphLoadProgress enables progress",
			opts: []GraphLoadOption{WithGraphLoadProgress(true)},
			check: func(t *testing.T, cfg *graphLoadConfig) {
				if !cfg.showProgress {
					t.Fatal("showProgress should be true")
				}
			},
		},
		{
			name: "WithGraphLoadProgressStepSize sets step size",
			opts: []GraphLoadOption{WithGraphLoadProgressStepSize(50)},
			check: func(t *testing.T, cfg *graphLoadConfig) {
				if cfg.progressStepSize != 50 {
					t.Fatalf("progressStepSize = %d, want 50", cfg.progressStepSize)
				}
			},
		},
		{
			name: "WithGraphLoadReadGraphOpts appends options",
			opts: []GraphLoadOption{WithGraphLoadReadGraphOpts(source.WithReadGraphParseConcurrency(8))},
			check: func(t *testing.T, cfg *graphLoadConfig) {
				if len(cfg.readGraphOpts) != 1 {
					t.Fatalf("readGraphOpts length = %d, want 1", len(cfg.readGraphOpts))
				}
			},
		},
		{
			name: "default step size when not set or zero",
			opts: nil,
			check: func(t *testing.T, cfg *graphLoadConfig) {
				if cfg.progressStepSize != 100 {
					t.Fatalf("default progressStepSize = %d, want 100", cfg.progressStepSize)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newGraphLoadConfig(tt.opts...)
			tt.check(t, cfg)
		})
	}
}

func TestBuiltinGraphKnowledge_SearchEdgeCases(t *testing.T) {
	fullyConfigured := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{
			score: 0.9,
			doc:   &document.Document{ID: "n1", Name: "n1", Content: "c"},
		}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)

	tests := []struct {
		name    string
		gk      *BuiltinGraphKnowledge
		req     *SearchRequest
		wantErr string
	}{
		{
			name:    "nil request",
			gk:      fullyConfigured,
			req:     nil,
			wantErr: "search request cannot be nil",
		},
		{
			name:    "empty query without filter",
			gk:      fullyConfigured,
			req:     &SearchRequest{Query: ""},
			wantErr: "search query cannot be empty",
		},
		{
			name:    "whitespace-only query without filter",
			gk:      fullyConfigured,
			req:     &SearchRequest{Query: "   "},
			wantErr: "search query cannot be empty",
		},
		{
			name: "nil vector store",
			gk: NewGraphKnowledge(
				WithGraphStore(&stubGraphStore{}),
				WithGraphEmbedder(stubGraphEmbedder{}),
			),
			req:     &SearchRequest{Query: "test"},
			wantErr: "graph vector store is not configured",
		},
		{
			name: "nil embedder with non-empty query",
			gk: NewGraphKnowledge(
				WithGraphStore(&stubGraphStore{}),
				WithGraphVectorStore(&fixedScoreGraphVectorStore{
					score: 0.9,
					doc:   &document.Document{ID: "n1", Name: "n1", Content: "c"},
				}),
			),
			req:     &SearchRequest{Query: "test"},
			wantErr: "graph embedder is not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.gk.Search(context.Background(), tt.req)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestBuiltinGraphKnowledge_TraverseRequiresStore(t *testing.T) {
	gk := NewGraphKnowledge()
	_, err := gk.Traverse(context.Background(), &graph.TraverseQuery{StartIDs: []string{"a"}})
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
	if !strings.Contains(err.Error(), "graph store is not configured") {
		t.Fatalf("error = %v, want 'graph store is not configured'", err)
	}
}

func TestBuiltinGraphKnowledge_TraverseDelegates(t *testing.T) {
	store := &stubGraphStore{
		traverseResp: &graph.TraverseResult{
			Nodes: []*graph.Node{{ID: "a"}},
		},
	}
	gk := NewGraphKnowledge(WithGraphStore(store))
	q := &graph.TraverseQuery{StartIDs: []string{"a"}}
	result, err := gk.Traverse(context.Background(), q)
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if store.traverseReq != q {
		t.Fatal("query was not forwarded to store")
	}
	if len(result.Nodes) != 1 || result.Nodes[0].ID != "a" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBuiltinGraphKnowledge_FindPathsRequiresStore(t *testing.T) {
	gk := NewGraphKnowledge()
	_, err := gk.FindPaths(context.Background(), &graph.PathQuery{FromID: "a", ToID: "b"})
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
	if !strings.Contains(err.Error(), "graph store is not configured") {
		t.Fatalf("error = %v, want 'graph store is not configured'", err)
	}
}

func TestBuiltinGraphKnowledge_FindPathsDelegates(t *testing.T) {
	store := &stubGraphStore{
		pathResp: &graph.PathResult{
			Paths: []*graph.Path{{Nodes: []*graph.Node{{ID: "a"}, {ID: "b"}}}},
		},
	}
	gk := NewGraphKnowledge(WithGraphStore(store))
	q := &graph.PathQuery{FromID: "a", ToID: "b"}
	result, err := gk.FindPaths(context.Background(), q)
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if store.pathReq != q {
		t.Fatal("query was not forwarded to store")
	}
	if len(result.Paths) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

type namedStubGraphSource struct {
	stubGraphSource
	name string
}

func (s *namedStubGraphSource) Name() string { return s.name }

func TestGraphSourceName(t *testing.T) {
	tests := []struct {
		name string
		src  source.GraphSource
		want string
	}{
		{
			name: "unnamed source returns default",
			src:  &stubGraphSource{},
			want: "graph source",
		},
		{
			name: "named source returns its name",
			src:  &namedStubGraphSource{name: "my-source"},
			want: "my-source",
		},
		{
			name: "named source with empty name returns default",
			src:  &namedStubGraphSource{name: ""},
			want: "graph source",
		},
		{
			name: "named source with whitespace-only name returns default",
			src:  &namedStubGraphSource{name: "   "},
			want: "graph source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphSourceName(tt.src)
			if got != tt.want {
				t.Fatalf("graphSourceName() = %q, want %q", got, tt.want)
			}
		})
	}
}
