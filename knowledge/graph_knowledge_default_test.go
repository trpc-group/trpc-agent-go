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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

type stubGraphStore struct {
	mu           sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = append(s.nodes, nodes...)
	return nil
}

func (s *stubGraphStore) AddEdges(
	ctx context.Context,
	edges []*graph.Edge,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	mu    sync.Mutex
	texts []string
}

func (e *recordingGraphEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	count     int64
}

func (e *failingGraphEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	n := atomic.AddInt64(&e.count, 1)
	if n > int64(e.failAfter) {
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

// --- stubs for error paths ---

type failingGraphStore struct {
	stubGraphStore
	addNodesErr error
	addEdgesErr error
	nodeCount   int
	failAfterN  int
}

func (s *failingGraphStore) AddNodes(ctx context.Context, nodes []*graph.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addNodesErr != nil {
		s.nodeCount += len(nodes)
		if s.failAfterN > 0 && s.nodeCount <= s.failAfterN {
			s.nodes = append(s.nodes, nodes...)
			return nil
		}
		return s.addNodesErr
	}
	s.nodes = append(s.nodes, nodes...)
	return nil
}

func (s *failingGraphStore) AddEdges(ctx context.Context, edges []*graph.Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addEdgesErr != nil {
		return s.addEdgesErr
	}
	s.edges = append(s.edges, edges...)
	return nil
}

type failingGraphSource struct {
	err error
}

func (s *failingGraphSource) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	return nil, s.err
}

type nilDataGraphSource struct{}

func (s *nilDataGraphSource) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	return nil, nil
}

type emptyResultVectorStore struct {
	fixedScoreGraphVectorStore
}

func (s *emptyResultVectorStore) Search(
	ctx context.Context,
	query *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	return &vectorstore.SearchResult{Results: nil}, nil
}

type nilResultVectorStore struct {
	fixedScoreGraphVectorStore
}

func (s *nilResultVectorStore) Search(
	ctx context.Context,
	query *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	return nil, nil
}

type failingVectorStore struct {
	fixedScoreGraphVectorStore
	searchErr error
	addErr    error
}

func (s *failingVectorStore) Search(
	ctx context.Context,
	query *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	return nil, s.searchErr
}

func (s *failingVectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if s.addErr != nil {
		return s.addErr
	}
	return nil
}

type duplicateResultVectorStore struct {
	fixedScoreGraphVectorStore
}

func (s *duplicateResultVectorStore) Search(
	ctx context.Context,
	query *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	doc := &document.Document{ID: "dup-1", Name: "dup", Content: "content"}
	return &vectorstore.SearchResult{Results: []*vectorstore.ScoredDocument{
		{Document: doc, Score: 0.9},
		{Document: doc, Score: 0.8},
		{Document: nil, Score: 0.7},
		{Document: &document.Document{}, Score: 0.6},
	}}, nil
}

// --- Search error path tests ---

func TestBuiltinGraphKnowledge_SearchEmptyResults(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&emptyResultVectorStore{}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	_, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for empty search results")
	}
	if !strings.Contains(err.Error(), "no relevant information found") {
		t.Fatalf("error = %v, want 'no relevant information found'", err)
	}
}

func TestBuiltinGraphKnowledge_SearchNilVectorStoreResult(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&nilResultVectorStore{}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	_, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for nil search result")
	}
	if !strings.Contains(err.Error(), "no relevant information found") {
		t.Fatalf("error = %v, want 'no relevant information found'", err)
	}
}

func TestBuiltinGraphKnowledge_SearchEmbeddingError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(&failingGraphEmbedder{failAfter: 0}),
	)
	_, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error from embedding failure")
	}
	if !strings.Contains(err.Error(), "generate graph search embedding") {
		t.Fatalf("error = %v, want embedding error", err)
	}
}

func TestBuiltinGraphKnowledge_SearchVectorStoreError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&failingVectorStore{searchErr: errors.New("search boom")}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	_, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error from vector store search")
	}
	if !strings.Contains(err.Error(), "search graph seeds") {
		t.Fatalf("error = %v, want 'search graph seeds'", err)
	}
}

func TestBuiltinGraphKnowledge_SearchFilterOnlyWithoutQuery(t *testing.T) {
	doc := &document.Document{ID: "filtered-1", Name: "filtered", Content: "filtered content"}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{
			score: 0.95,
			doc:   doc,
		}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	result, err := gk.Search(context.Background(), &SearchRequest{
		Query: "",
		SearchFilter: &SearchFilter{
			DocumentIDs: []string{"filtered-1"},
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result == nil || len(result.Documents) == 0 {
		t.Fatal("expected search results with filter-only search")
	}
	if result.Documents[0].Document.ID != "filtered-1" {
		t.Fatalf("Document.ID = %q, want 'filtered-1'", result.Documents[0].Document.ID)
	}
}

func TestBuiltinGraphKnowledge_SearchDeduplicatesAndFiltersNils(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&duplicateResultVectorStore{}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	result, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("Documents count = %d, want 1 (deduplicated)", len(result.Documents))
	}
	if result.Documents[0].Document.ID != "dup-1" {
		t.Fatalf("Document.ID = %q, want 'dup-1'", result.Documents[0].Document.ID)
	}
}

func TestBuiltinGraphKnowledge_SearchResultFields(t *testing.T) {
	doc := &document.Document{
		ID:       "node-x",
		Name:     "NodeX",
		Content:  "node x content",
		Metadata: map[string]any{"key": "val"},
	}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{score: 0.88, doc: doc}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	result, err := gk.Search(context.Background(), &SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Text != "node x content" {
		t.Fatalf("Text = %q, want 'node x content'", result.Text)
	}
	if result.Document.ID != "node-x" {
		t.Fatalf("Document.ID = %q, want 'node-x'", result.Document.ID)
	}
	if result.Score != 0.88 {
		t.Fatalf("Score = %v, want 0.88", result.Score)
	}
	if result.Document.Metadata["key"] != "val" {
		t.Fatal("metadata not preserved in search result")
	}
}

// --- LoadGraphSource error path tests ---

func TestBuiltinGraphKnowledge_LoadGraphSourceNilSource(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.LoadGraphSource(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil source")
	}
	if !strings.Contains(err.Error(), "graph source cannot be nil") {
		t.Fatalf("error = %v, want 'graph source cannot be nil'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceReadError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.LoadGraphSource(context.Background(), &failingGraphSource{err: errors.New("read boom")})
	if err == nil {
		t.Fatal("expected error from ReadGraph failure")
	}
	if !strings.Contains(err.Error(), "read graph source") {
		t.Fatalf("error = %v, want 'read graph source'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceNilData(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.LoadGraphSource(context.Background(), &nilDataGraphSource{})
	if err == nil {
		t.Fatal("expected error for nil graph data")
	}
	if !strings.Contains(err.Error(), "graph data cannot be nil") {
		t.Fatalf("error = %v, want 'graph data cannot be nil'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceEmptyData(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.LoadGraphSource(context.Background(), &stubGraphSource{data: &graph.Data{}})
	if err != nil {
		t.Fatalf("LoadGraphSource() error = %v, want nil for empty data", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceWithProgress(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	nodes := make([]*graph.Node, 5)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("node-%d", i),
			Name:    fmt.Sprintf("Node%d", i),
			Content: "content",
		}
	}
	edges := make([]*graph.Edge, 3)
	for i := range edges {
		edges[i] = &graph.Edge{
			FromID: fmt.Sprintf("node-%d", i),
			ToID:   fmt.Sprintf("node-%d", i+1),
			Type:   "CALLS",
		}
	}
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes, Edges: edges}}
	err := gk.LoadGraphSource(context.Background(), src,
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(2),
		WithGraphLoadConcurrency(GraphLoadConcurrency{
			AddNodeRoutines:   1,
			AddEdgeRoutines:   1,
			EmbeddingRoutines: 1,
		}),
	)
	if err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	if len(store.nodes) != 5 {
		t.Fatalf("stored nodes = %d, want 5", len(store.nodes))
	}
	if len(store.edges) != 3 {
		t.Fatalf("stored edges = %d, want 3", len(store.edges))
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceWithProgressAndConcurrency(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	nodes := make([]*graph.Node, 10)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("cn-%d", i),
			Name:    fmt.Sprintf("CN%d", i),
			Content: "content",
		}
	}
	edges := make([]*graph.Edge, 6)
	for i := range edges {
		edges[i] = &graph.Edge{
			FromID: fmt.Sprintf("cn-%d", i),
			ToID:   fmt.Sprintf("cn-%d", i+1),
			Type:   "CALLS",
		}
	}
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes, Edges: edges}}
	err := gk.LoadGraphSource(context.Background(), src,
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(3),
		WithGraphLoadConcurrency(GraphLoadConcurrency{
			AddNodeRoutines:   2,
			AddEdgeRoutines:   2,
			EmbeddingRoutines: 3,
		}),
	)
	if err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	if len(store.nodes) != 10 {
		t.Fatalf("stored nodes = %d, want 10", len(store.nodes))
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceNamedSource(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &namedStubGraphSource{
		stubGraphSource: stubGraphSource{data: &graph.Data{
			Nodes: []*graph.Node{{ID: "ns-1", Name: "NS1", Content: "c"}},
		}},
		name: "test-source",
	}
	err := gk.LoadGraphSource(context.Background(), src, WithGraphLoadProgress(true))
	if err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
}

// --- addGraphNodes / addGraphEdges error tests ---

func TestBuiltinGraphKnowledge_LoadGraphSourceAddNodesError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&failingGraphStore{addNodesErr: errors.New("add nodes boom")}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{ID: "n1", Name: "N1", Content: "c"}},
	}}
	err := gk.LoadGraphSource(context.Background(), src)
	if err == nil {
		t.Fatal("expected error from AddNodes failure")
	}
	if !strings.Contains(err.Error(), "add graph nodes") {
		t.Fatalf("error = %v, want 'add graph nodes'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceAddEdgesError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&failingGraphStore{addEdgesErr: errors.New("add edges boom")}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{ID: "n1", Name: "N1", Content: "c"}},
		Edges: []*graph.Edge{{FromID: "n1", ToID: "n2", Type: "CALLS"}},
	}}
	err := gk.LoadGraphSource(context.Background(), src)
	if err == nil {
		t.Fatal("expected error from AddEdges failure")
	}
	if !strings.Contains(err.Error(), "add graph edges") {
		t.Fatalf("error = %v, want 'add graph edges'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceAddNodesBatchError(t *testing.T) {
	nodes := make([]*graph.Node, 20)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("bn-%d", i),
			Name:    fmt.Sprintf("BN%d", i),
			Content: "content",
		}
	}
	gk := NewGraphKnowledge(
		WithGraphStore(&failingGraphStore{
			addNodesErr: errors.New("batch node error"),
			failAfterN:  5,
		}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes}}
	err := gk.LoadGraphSource(context.Background(), src,
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddNodeRoutines: 3}),
		WithGraphLoadProgressStepSize(5),
	)
	if err == nil {
		t.Fatal("expected error from batch node failure")
	}
	if !strings.Contains(err.Error(), "add graph nodes") {
		t.Fatalf("error = %v, want 'add graph nodes'", err)
	}
}

// --- indexGraphDataDocuments error test ---

func TestBuiltinGraphKnowledge_LoadGraphSourceIndexError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&failingVectorStore{addErr: errors.New("add doc boom")}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{ID: "idx-1", Name: "Idx1", Content: "c"}},
	}}
	err := gk.LoadGraphSource(context.Background(), src)
	if err == nil {
		t.Fatal("expected error from addGraphDocument failure")
	}
	if !strings.Contains(err.Error(), "add graph seed document") {
		t.Fatalf("error = %v, want 'add graph seed document'", err)
	}
}

func TestBuiltinGraphKnowledge_LoadGraphSourceIndexErrorConcurrent(t *testing.T) {
	nodes := make([]*graph.Node, 10)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:      fmt.Sprintf("ci-%d", i),
			Name:    fmt.Sprintf("CI%d", i),
			Content: "content",
		}
	}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&failingVectorStore{addErr: errors.New("concurrent add boom")}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	src := &stubGraphSource{data: &graph.Data{Nodes: nodes}}
	err := gk.LoadGraphSource(context.Background(), src,
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 4}),
	)
	if err == nil {
		t.Fatal("expected error from concurrent addGraphDocument failure")
	}
}

// --- graphDataDocuments / graphNodeFromDocument edge cases ---

func TestGraphDataDocuments_NilAndEmptyNodes(t *testing.T) {
	docs := graphDataDocuments(nil)
	if docs != nil {
		t.Fatalf("graphDataDocuments(nil) = %v, want nil", docs)
	}

	docs = graphDataDocuments(&graph.Data{
		Nodes: []*graph.Node{nil, {ID: ""}, {ID: "valid", Name: "V", Content: "c"}},
	})
	if len(docs) != 1 {
		t.Fatalf("graphDataDocuments() = %d docs, want 1 (skip nil and empty ID)", len(docs))
	}
	if docs[0].ID != "valid" {
		t.Fatalf("doc.ID = %q, want 'valid'", docs[0].ID)
	}
}

func TestGraphNodeFromDocument_EdgeCases(t *testing.T) {
	if n := graphNodeFromDocument(nil); n != nil {
		t.Fatalf("graphNodeFromDocument(nil) = %v, want nil", n)
	}
	if n := graphNodeFromDocument(&document.Document{}); n != nil {
		t.Fatalf("graphNodeFromDocument(empty) = %v, want nil", n)
	}
	node := graphNodeFromDocument(&document.Document{ID: "abc"})
	if node == nil {
		t.Fatal("graphNodeFromDocument() = nil, want non-nil")
	}
	if node.Name != "abc" {
		t.Fatalf("node.Name = %q, want 'abc' (fallback to ID)", node.Name)
	}
}

// --- truncateGraphDataContent edge cases ---

func TestTruncateGraphDataContent_NilData(t *testing.T) {
	truncateGraphDataContent(nil)
}

func TestTruncateGraphDataContent_NilNodes(t *testing.T) {
	data := &graph.Data{Nodes: []*graph.Node{nil, {ID: "a", Content: "short"}}}
	truncateGraphDataContent(data)
	if data.Nodes[1].Content != "short" {
		t.Fatalf("Content = %q, want 'short'", data.Nodes[1].Content)
	}
}

// --- graphSeedEmbeddingText edge cases ---

func TestGraphSeedEmbeddingText_NilDoc(t *testing.T) {
	text := graphSeedEmbeddingText(nil)
	if text != "" {
		t.Fatalf("graphSeedEmbeddingText(nil) = %q, want empty", text)
	}
}

func TestGraphSeedEmbeddingText_WithEmbeddingText(t *testing.T) {
	doc := &document.Document{
		ID:            "x",
		EmbeddingText: "custom embedding",
		Content:       "content",
	}
	text := graphSeedEmbeddingText(doc)
	if text != "custom embedding" {
		t.Fatalf("graphSeedEmbeddingText() = %q, want 'custom embedding'", text)
	}
}

func TestGraphSeedEmbeddingText_NilMetadata(t *testing.T) {
	doc := &document.Document{
		ID:      "x",
		Content: "raw content",
	}
	text := graphSeedEmbeddingText(doc)
	if text != "raw content" {
		t.Fatalf("graphSeedEmbeddingText() = %q, want 'raw content'", text)
	}
}

func TestGraphSeedEmbeddingText_EmptyMetadataFallsBackToContent(t *testing.T) {
	doc := &document.Document{
		ID:       "x",
		Content:  "fallback",
		Metadata: map[string]any{},
	}
	text := graphSeedEmbeddingText(doc)
	if !strings.Contains(text, "fallback") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want containing 'fallback'", text)
	}
}

func TestGraphSeedEmbeddingText_EmptyContentNoMetadataFields(t *testing.T) {
	doc := &document.Document{
		ID:       "x",
		Name:     "xname",
		Content:  "",
		Metadata: map[string]any{codeast.TrpcAstMetaPrefix + "package": "mypkg"},
	}
	text := graphSeedEmbeddingText(doc)
	if !strings.Contains(text, "name: xname") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want containing 'name: xname'", text)
	}
	if !strings.Contains(text, "package: mypkg") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want containing 'package: mypkg'", text)
	}
}

// --- graphSeedMetadataString edge cases ---

func TestGraphSeedMetadataString_EdgeCases(t *testing.T) {
	if v := graphSeedMetadataString(nil, "any"); v != "" {
		t.Fatalf("graphSeedMetadataString(nil, ...) = %q, want empty", v)
	}
	if v := graphSeedMetadataString(map[string]any{"k": nil}, "k"); v != "" {
		t.Fatalf("graphSeedMetadataString(nil value) = %q, want empty", v)
	}
	if v := graphSeedMetadataString(map[string]any{}, "missing"); v != "" {
		t.Fatalf("graphSeedMetadataString(missing key) = %q, want empty", v)
	}
	if v := graphSeedMetadataString(map[string]any{"k": 42}, "k"); v != "42" {
		t.Fatalf("graphSeedMetadataString(int) = %q, want '42'", v)
	}
}

// --- convertSearchFilter ---

func TestConvertSearchFilter(t *testing.T) {
	if f := convertSearchFilter(nil); f != nil {
		t.Fatalf("convertSearchFilter(nil) = %v, want nil", f)
	}
	f := convertSearchFilter(&SearchFilter{
		DocumentIDs: []string{"a", "b"},
		Metadata:    map[string]any{"key": "val"},
	})
	if f == nil {
		t.Fatal("convertSearchFilter() = nil, want non-nil")
	}
	if len(f.IDs) != 2 {
		t.Fatalf("IDs = %v, want 2 items", f.IDs)
	}
	if f.Metadata["key"] != "val" {
		t.Fatal("Metadata not preserved")
	}
}

// --- resolvePositiveInt ---

func TestResolvePositiveInt(t *testing.T) {
	if v := resolvePositiveInt(10, 5); v != 10 {
		t.Fatalf("resolvePositiveInt(10, 5) = %d, want 10", v)
	}
	if v := resolvePositiveInt(0, 5); v != 5 {
		t.Fatalf("resolvePositiveInt(0, 5) = %d, want 5", v)
	}
	if v := resolvePositiveInt(-1, 5); v != 5 {
		t.Fatalf("resolvePositiveInt(-1, 5) = %d, want 5", v)
	}
}

// --- cloneMetadata ---

func TestCloneMetadata(t *testing.T) {
	if m := cloneMetadata(nil); m != nil {
		t.Fatalf("cloneMetadata(nil) = %v, want nil", m)
	}
	orig := map[string]any{"k": "v"}
	cloned := cloneMetadata(orig)
	if cloned["k"] != "v" {
		t.Fatal("cloned metadata missing key")
	}
	cloned["k"] = "modified"
	if orig["k"] != "v" {
		t.Fatal("clone mutated original")
	}
}

// --- runGraphBatches edge cases ---

func TestRunGraphBatches_ZeroTotal(t *testing.T) {
	err := runGraphBatches(context.Background(), 0, 10, 2,
		func(start, end int) error { return nil },
		func(processed int) {},
	)
	if err != nil {
		t.Fatalf("runGraphBatches(0) error = %v", err)
	}
}

func TestRunGraphBatches_SingleConcurrencyWithProgress(t *testing.T) {
	var processed []int
	err := runGraphBatches(context.Background(), 7, 3, 1,
		func(start, end int) error { return nil },
		func(p int) { processed = append(processed, p) },
	)
	if err != nil {
		t.Fatalf("runGraphBatches() error = %v", err)
	}
	if len(processed) == 0 {
		t.Fatal("expected progress reports")
	}
	last := processed[len(processed)-1]
	if last != 7 {
		t.Fatalf("last reported = %d, want 7", last)
	}
}

func TestRunGraphBatches_SingleConcurrencyError(t *testing.T) {
	err := runGraphBatches(context.Background(), 5, 2, 1,
		func(start, end int) error {
			if start >= 2 {
				return errors.New("batch fail")
			}
			return nil
		},
		func(p int) {},
	)
	if err == nil {
		t.Fatal("expected error from batch processing")
	}
}

func TestRunGraphBatches_ConcurrentError(t *testing.T) {
	err := runGraphBatches(context.Background(), 20, 3, 4,
		func(start, end int) error {
			if start >= 6 {
				return errors.New("concurrent batch fail")
			}
			return nil
		},
		func(p int) {},
	)
	if err == nil {
		t.Fatal("expected error from concurrent batch processing")
	}
}

func TestRunGraphBatches_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runGraphBatches(ctx, 100, 5, 3,
		func(start, end int) error { return nil },
		func(p int) {},
	)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

// --- addGraphDocument edge cases ---

func TestBuiltinGraphKnowledge_AddGraphDocumentNilDoc(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.addGraphDocument(context.Background(), nil)
	if err != nil {
		t.Fatalf("addGraphDocument(nil) error = %v, want nil", err)
	}
}

func TestBuiltinGraphKnowledge_AddGraphDocumentEmptyID(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.addGraphDocument(context.Background(), &document.Document{})
	if err == nil {
		t.Fatal("expected error for empty doc ID")
	}
	if !strings.Contains(err.Error(), "graph document id cannot be empty") {
		t.Fatalf("error = %v, want 'graph document id cannot be empty'", err)
	}
}

func TestBuiltinGraphKnowledge_AddGraphDocumentEmbeddingError(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(&failingGraphEmbedder{failAfter: 0}),
	)
	err := gk.addGraphDocument(context.Background(), &document.Document{ID: "d1", Content: "c"})
	if err == nil {
		t.Fatal("expected error from embedding failure")
	}
	if !strings.Contains(err.Error(), "generate graph seed embedding") {
		t.Fatalf("error = %v, want 'generate graph seed embedding'", err)
	}
}

// --- readGraphSourceData with showProgress ---

func TestReadGraphSourceData_WithProgress(t *testing.T) {
	src := &stubGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{ID: "rp-1", Name: "RP1", Content: "c"}},
		Edges: []*graph.Edge{{FromID: "rp-1", ToID: "rp-2", Type: "X"}},
	}}
	config := newGraphLoadConfig(WithGraphLoadProgress(true))
	data, err := readGraphSourceData(context.Background(), src, "test-src", config, time.Now())
	if err != nil {
		t.Fatalf("readGraphSourceData() error = %v", err)
	}
	if len(data.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(data.Nodes))
	}
}

func TestReadGraphSourceData_ReadError(t *testing.T) {
	src := &failingGraphSource{err: errors.New("read error")}
	config := newGraphLoadConfig()
	_, err := readGraphSourceData(context.Background(), src, "fail-src", config, time.Now())
	if err == nil {
		t.Fatal("expected error from ReadGraph failure")
	}
	if !strings.Contains(err.Error(), "read graph source") {
		t.Fatalf("error = %v, want 'read graph source'", err)
	}
}

func TestReadGraphSourceData_NilData(t *testing.T) {
	src := &nilDataGraphSource{}
	config := newGraphLoadConfig()
	_, err := readGraphSourceData(context.Background(), src, "nil-src", config, time.Now())
	if err == nil {
		t.Fatal("expected error for nil data")
	}
	if !strings.Contains(err.Error(), "graph data cannot be nil") {
		t.Fatalf("error = %v, want 'graph data cannot be nil'", err)
	}
}

// --- truncateGraphSeedContent ---

func TestTruncateGraphSeedContent_ShortContent(t *testing.T) {
	s := truncateGraphSeedContent("hello")
	if s != "hello" {
		t.Fatalf("truncateGraphSeedContent('hello') = %q, want 'hello'", s)
	}
}

func TestTruncateGraphSeedContent_ExactLimit(t *testing.T) {
	content := strings.Repeat("a", defaultGraphNodeContentRunes)
	s := truncateGraphSeedContent(content)
	if s != content {
		t.Fatal("content at exact limit should not be truncated")
	}
}

func TestTruncateGraphSeedContent_OverLimit(t *testing.T) {
	content := strings.Repeat("b", defaultGraphNodeContentRunes+10)
	s := truncateGraphSeedContent(content)
	if !strings.HasSuffix(s, "...<truncated>") {
		t.Fatalf("expected truncation suffix, got %q", s[len(s)-20:])
	}
	if strings.Contains(s, content) {
		t.Fatal("truncated content should not contain original")
	}
}

func TestTruncateGraphSeedContent_EmptyContent(t *testing.T) {
	s := truncateGraphSeedContent("")
	if s != "" {
		t.Fatalf("truncateGraphSeedContent('') = %q, want empty", s)
	}
}

// --- LoadGraphSource with concurrency=1 but showProgress=true ---

func TestBuiltinGraphKnowledge_AddGraphNodesLowConcurrencyProgress(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	nodes := make([]*graph.Node, 5)
	for i := range nodes {
		nodes[i] = &graph.Node{ID: fmt.Sprintf("lc-%d", i), Name: fmt.Sprintf("LC%d", i), Content: "c"}
	}
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(2),
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddNodeRoutines: 1}),
	)
	err := gk.addGraphNodes(context.Background(), nodes, config)
	if err != nil {
		t.Fatalf("addGraphNodes() error = %v", err)
	}
	if len(store.nodes) != 5 {
		t.Fatalf("nodes = %d, want 5", len(store.nodes))
	}
}

func TestBuiltinGraphKnowledge_AddGraphEdgesLowConcurrencyProgress(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	edges := make([]*graph.Edge, 5)
	for i := range edges {
		edges[i] = &graph.Edge{FromID: fmt.Sprintf("le-%d", i), ToID: fmt.Sprintf("le-%d", i+1), Type: "X"}
	}
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(2),
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddEdgeRoutines: 1}),
	)
	err := gk.addGraphEdges(context.Background(), edges, config)
	if err != nil {
		t.Fatalf("addGraphEdges() error = %v", err)
	}
	if len(store.edges) != 5 {
		t.Fatalf("edges = %d, want 5", len(store.edges))
	}
}

// --- Search with MaxResults custom value ---

func TestBuiltinGraphKnowledge_SearchRespectsMaxResults(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	for i := 0; i < 10; i++ {
		if err := gk.addGraphDocument(context.Background(), &document.Document{
			ID:      fmt.Sprintf("mr-%d", i),
			Name:    fmt.Sprintf("MR%d", i),
			Content: "search content",
		}); err != nil {
			t.Fatalf("addGraphDocument() error = %v", err)
		}
	}
	result, err := gk.Search(context.Background(), &SearchRequest{Query: "search content", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Documents) > 3 {
		t.Fatalf("Documents = %d, want <= 3", len(result.Documents))
	}
}

// --- Context cancellation during addGraphDocuments ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsContextCancel(t *testing.T) {
	docs := make([]*document.Document, 20)
	for i := range docs {
		docs[i] = &document.Document{
			ID:      fmt.Sprintf("cc-%d", i),
			Name:    fmt.Sprintf("CC%d", i),
			Content: "content",
		}
	}

	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := newGraphLoadConfig(WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 4}))
	err := gk.addGraphDocuments(ctx, docs, config)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

// --- storeGraphData edge cases ---

func TestBuiltinGraphKnowledge_StoreGraphDataNodesOnly(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	config := newGraphLoadConfig()
	err := gk.storeGraphData(context.Background(), &graph.Data{
		Nodes: []*graph.Node{{ID: "s1", Name: "S1", Content: "c"}},
	}, config)
	if err != nil {
		t.Fatalf("storeGraphData() error = %v", err)
	}
	if len(store.edges) != 0 {
		t.Fatal("expected no edges stored")
	}
}

func TestBuiltinGraphKnowledge_StoreGraphDataEdgesOnly(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	config := newGraphLoadConfig()
	err := gk.storeGraphData(context.Background(), &graph.Data{
		Edges: []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}},
	}, config)
	if err != nil {
		t.Fatalf("storeGraphData() error = %v", err)
	}
	if len(store.nodes) != 0 {
		t.Fatal("expected no nodes stored")
	}
}

func TestBuiltinGraphKnowledge_StoreGraphDataEmpty(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	config := newGraphLoadConfig()
	err := gk.storeGraphData(context.Background(), &graph.Data{}, config)
	if err != nil {
		t.Fatalf("storeGraphData() error = %v", err)
	}
}

// --- indexGraphDataDocuments ---

func TestBuiltinGraphKnowledge_IndexGraphDataDocumentsEmpty(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	config := newGraphLoadConfig(WithGraphLoadProgress(true))
	n, err := gk.indexGraphDataDocuments(context.Background(), &graph.Data{}, config)
	if err != nil {
		t.Fatalf("indexGraphDataDocuments() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("indexed = %d, want 0", n)
	}
}

func TestBuiltinGraphKnowledge_IndexGraphDataDocumentsWithProgress(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(1),
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 1}),
	)
	data := &graph.Data{
		Nodes: []*graph.Node{
			{ID: "ip-1", Name: "IP1", Content: "c1"},
			{ID: "ip-2", Name: "IP2", Content: "c2"},
			{ID: "ip-3", Name: "IP3", Content: "c3"},
		},
	}
	n, err := gk.indexGraphDataDocuments(context.Background(), data, config)
	if err != nil {
		t.Fatalf("indexGraphDataDocuments() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("indexed = %d, want 3", n)
	}
}

// --- addGraphNodes / addGraphEdges direct path (concurrency<=1, no progress) ---

func TestBuiltinGraphKnowledge_AddGraphNodesDirectPath(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	nodes := []*graph.Node{
		{ID: "dp-1", Name: "DP1", Content: "c1"},
		{ID: "dp-2", Name: "DP2", Content: "c2"},
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddNodeRoutines: 1}),
	)
	if err := gk.addGraphNodes(context.Background(), nodes, config); err != nil {
		t.Fatalf("addGraphNodes() error = %v", err)
	}
	if len(store.nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(store.nodes))
	}
}

func TestBuiltinGraphKnowledge_AddGraphEdgesDirectPath(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	edges := []*graph.Edge{
		{FromID: "dp-1", ToID: "dp-2", Type: "CALLS"},
		{FromID: "dp-2", ToID: "dp-3", Type: "CALLS"},
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddEdgeRoutines: 1}),
	)
	if err := gk.addGraphEdges(context.Background(), edges, config); err != nil {
		t.Fatalf("addGraphEdges() error = %v", err)
	}
	if len(store.edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(store.edges))
	}
}

func TestBuiltinGraphKnowledge_AddGraphNodesConcurrentNoProgress(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	nodes := make([]*graph.Node, 8)
	for i := range nodes {
		nodes[i] = &graph.Node{ID: fmt.Sprintf("cnp-%d", i), Name: fmt.Sprintf("CNP%d", i), Content: "c"}
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddNodeRoutines: 3}),
		WithGraphLoadProgressStepSize(2),
	)
	if err := gk.addGraphNodes(context.Background(), nodes, config); err != nil {
		t.Fatalf("addGraphNodes() error = %v", err)
	}
	if len(store.nodes) != 8 {
		t.Fatalf("nodes = %d, want 8", len(store.nodes))
	}
}

func TestBuiltinGraphKnowledge_AddGraphEdgesConcurrentNoProgress(t *testing.T) {
	store := &stubGraphStore{}
	gk := NewGraphKnowledge(WithGraphStore(store))
	edges := make([]*graph.Edge, 8)
	for i := range edges {
		edges[i] = &graph.Edge{FromID: fmt.Sprintf("enp-%d", i), ToID: fmt.Sprintf("enp-%d", i+1), Type: "CALLS"}
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddEdgeRoutines: 3}),
		WithGraphLoadProgressStepSize(2),
	)
	if err := gk.addGraphEdges(context.Background(), edges, config); err != nil {
		t.Fatalf("addGraphEdges() error = %v", err)
	}
	if len(store.edges) != 8 {
		t.Fatalf("edges = %d, want 8", len(store.edges))
	}
}

// --- runGraphBatches with batchSize <= 0 ---

func TestRunGraphBatches_ZeroBatchSizeUsesTotal(t *testing.T) {
	var calls int
	err := runGraphBatches(context.Background(), 5, 0, 1,
		func(start, end int) error {
			calls++
			if start != 0 || end != 5 {
				t.Fatalf("expected single batch [0,5), got [%d,%d)", start, end)
			}
			return nil
		},
		func(p int) {},
	)
	if err != nil {
		t.Fatalf("runGraphBatches() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("process calls = %d, want 1", calls)
	}
}

func TestRunGraphBatches_NegativeBatchSizeUsesTotal(t *testing.T) {
	var calls int
	err := runGraphBatches(context.Background(), 3, -1, 1,
		func(start, end int) error {
			calls++
			return nil
		},
		func(p int) {},
	)
	if err != nil {
		t.Fatalf("runGraphBatches() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("process calls = %d, want 1", calls)
	}
}

// --- addGraphDocuments concurrency capped to docs length ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsConcurrencyCapToDocs(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	docs := []*document.Document{
		{ID: "cap-1", Name: "Cap1", Content: "c1"},
		{ID: "cap-2", Name: "Cap2", Content: "c2"},
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 100}),
	)
	if err := gk.addGraphDocuments(context.Background(), docs, config); err != nil {
		t.Fatalf("addGraphDocuments() error = %v", err)
	}
}

// --- graphSeedEmbeddingText builder.Len()==0 fallback ---

func TestGraphSeedEmbeddingText_EmptyFieldsWithMetadataAndContent(t *testing.T) {
	doc := &document.Document{
		ID:       "",
		Name:     "",
		Content:  "fallback content",
		Metadata: map[string]any{"unrelated": "value"},
	}
	text := graphSeedEmbeddingText(doc)
	if !strings.Contains(text, "code:\nfallback content") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want containing 'code:\\nfallback content'", text)
	}
}

func TestGraphSeedEmbeddingText_AllEmptyReturnsEmptyContent(t *testing.T) {
	doc := &document.Document{
		ID:       "",
		Name:     "",
		Content:  "",
		Metadata: map[string]any{},
	}
	text := graphSeedEmbeddingText(doc)
	if text != "" {
		t.Fatalf("graphSeedEmbeddingText() = %q, want empty", text)
	}
}

// --- graphSeedMetadataString with fmt.Stringer ---

type testStringer struct{ val string }

func (s testStringer) String() string { return s.val }

func TestGraphSeedMetadataString_Stringer(t *testing.T) {
	m := map[string]any{"k": testStringer{val: "stringer-value"}}
	v := graphSeedMetadataString(m, "k")
	if v != "stringer-value" {
		t.Fatalf("graphSeedMetadataString(Stringer) = %q, want 'stringer-value'", v)
	}
}

// --- graphSeedEmbeddingText with metadata name fallback ---

func TestGraphSeedEmbeddingText_MetadataNameFallback(t *testing.T) {
	doc := &document.Document{
		ID:      "mn-1",
		Name:    "",
		Content: "code body",
		Metadata: map[string]any{
			codeast.TrpcAstMetaPrefix + "name": "MetaName",
		},
	}
	text := graphSeedEmbeddingText(doc)
	if !strings.Contains(text, "name: MetaName") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want containing 'name: MetaName'", text)
	}
}

// --- addGraphDocuments sequential with nil doc ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsSequentialSkipsNil(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	docs := []*document.Document{
		nil,
		{ID: "seq-1", Name: "Seq1", Content: "c1"},
	}
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 1}),
	)
	if err := gk.addGraphDocuments(context.Background(), docs, config); err != nil {
		t.Fatalf("addGraphDocuments() error = %v", err)
	}
}

// --- LoadGraphSource with readGraphOpts passed through ---

type recordingGraphSource struct {
	data *graph.Data
	opts []source.ReadGraphOption
}

func (s *recordingGraphSource) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	s.opts = opts
	return s.data, nil
}

func TestBuiltinGraphKnowledge_LoadGraphSourcePassesReadGraphOpts(t *testing.T) {
	store := &stubGraphStore{}
	src := &recordingGraphSource{data: &graph.Data{
		Nodes: []*graph.Node{{ID: "rgo-1", Name: "RGO1", Content: "c"}},
	}}
	gk := NewGraphKnowledge(
		WithGraphStore(store),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	err := gk.LoadGraphSource(context.Background(), src,
		WithGraphLoadReadGraphOpts(source.WithReadGraphParseConcurrency(16)),
	)
	if err != nil {
		t.Fatalf("LoadGraphSource() error = %v", err)
	}
	if len(src.opts) != 1 {
		t.Fatalf("ReadGraph opts = %d, want 1", len(src.opts))
	}
}

// --- storeGraphData with progress and concurrent node/edge errors ---

func TestBuiltinGraphKnowledge_StoreGraphDataAddNodesErrorWithProgress(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&failingGraphStore{addNodesErr: errors.New("node fail")}),
	)
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddNodeRoutines: 2}),
		WithGraphLoadProgressStepSize(2),
	)
	err := gk.storeGraphData(context.Background(), &graph.Data{
		Nodes: []*graph.Node{{ID: "sfp-1", Name: "SFP1", Content: "c"}},
	}, config)
	if err == nil {
		t.Fatal("expected error from AddNodes")
	}
}

func TestBuiltinGraphKnowledge_StoreGraphDataAddEdgesErrorWithProgress(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&failingGraphStore{addEdgesErr: errors.New("edge fail")}),
	)
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadConcurrency(GraphLoadConcurrency{AddEdgeRoutines: 2}),
		WithGraphLoadProgressStepSize(2),
	)
	err := gk.storeGraphData(context.Background(), &graph.Data{
		Edges: []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}},
	}, config)
	if err == nil {
		t.Fatal("expected error from AddEdges")
	}
}

// --- runGraphBatches with concurrency > batches (worker cap) ---

func TestRunGraphBatches_ConcurrencyExceedsBatches(t *testing.T) {
	var calls int64
	err := runGraphBatches(context.Background(), 3, 2, 10,
		func(start, end int) error {
			atomic.AddInt64(&calls, 1)
			return nil
		},
		func(p int) {},
	)
	if err != nil {
		t.Fatalf("runGraphBatches() error = %v", err)
	}
	if atomic.LoadInt64(&calls) != 2 {
		t.Fatalf("process calls = %d, want 2", atomic.LoadInt64(&calls))
	}
}

// --- addGraphDocuments concurrent with progress ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsConcurrentWithProgress(t *testing.T) {
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(inmemory.New()),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	docs := make([]*document.Document, 5)
	for i := range docs {
		docs[i] = &document.Document{
			ID:      fmt.Sprintf("cdp-%d", i),
			Name:    fmt.Sprintf("CDP%d", i),
			Content: "c",
		}
	}
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(2),
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 3}),
	)
	if err := gk.addGraphDocuments(context.Background(), docs, config); err != nil {
		t.Fatalf("addGraphDocuments() error = %v", err)
	}
}

// --- reportGraphDocumentProgress specific branches ---

func TestBuiltinGraphKnowledge_ReportGraphDocumentProgressSteps(t *testing.T) {
	gk := &BuiltinGraphKnowledge{}
	config := newGraphLoadConfig(
		WithGraphLoadProgress(true),
		WithGraphLoadProgressStepSize(3),
	)
	gk.reportGraphDocumentProgress(context.Background(), config, 1, 10)
	gk.reportGraphDocumentProgress(context.Background(), config, 3, 10)
	gk.reportGraphDocumentProgress(context.Background(), config, 10, 10)
}

func TestBuiltinGraphKnowledge_ReportGraphDocumentProgressNoProgress(t *testing.T) {
	gk := &BuiltinGraphKnowledge{}
	config := newGraphLoadConfig()
	gk.reportGraphDocumentProgress(context.Background(), config, 1, 10)
}

// --- Search with SearchMode set ---

func TestBuiltinGraphKnowledge_SearchWithSearchMode(t *testing.T) {
	doc := &document.Document{ID: "sm-1", Name: "SM1", Content: "search mode content"}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{score: 0.8, doc: doc}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	result, err := gk.Search(context.Background(), &SearchRequest{
		Query:      "test",
		SearchMode: 1,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Documents[0].Document.ID != "sm-1" {
		t.Fatalf("Document.ID = %q, want 'sm-1'", result.Documents[0].Document.ID)
	}
}

// --- Search with filter and query both set ---

func TestBuiltinGraphKnowledge_SearchWithFilterAndQuery(t *testing.T) {
	doc := &document.Document{ID: "fq-1", Name: "FQ1", Content: "filtered query content"}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&fixedScoreGraphVectorStore{score: 0.75, doc: doc}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	result, err := gk.Search(context.Background(), &SearchRequest{
		Query:    "test",
		MinScore: 0.5,
		SearchFilter: &SearchFilter{
			DocumentIDs: []string{"fq-1"},
			Metadata:    map[string]any{"k": "v"},
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("Documents = %d, want 1", len(result.Documents))
	}
}

// --- addGraphDocuments concurrent error detected after dispatch completes ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsConcurrentLateError(t *testing.T) {
	docs := []*document.Document{
		{ID: "late-1", Name: "Late1", Content: "c1"},
		{ID: "late-2", Name: "Late2", Content: "c2"},
	}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&failingVectorStore{addErr: errors.New("late add error")}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 2}),
	)
	err := gk.addGraphDocuments(context.Background(), docs, config)
	if err == nil {
		t.Fatal("expected error from concurrent doc add failure")
	}
}

// --- addGraphDocuments sequential error path ---

func TestBuiltinGraphKnowledge_AddGraphDocumentsSequentialError(t *testing.T) {
	docs := []*document.Document{
		{ID: "se-1", Name: "SE1", Content: "c1"},
		{ID: "se-2", Name: "SE2", Content: "c2"},
	}
	gk := NewGraphKnowledge(
		WithGraphStore(&stubGraphStore{}),
		WithGraphVectorStore(&failingVectorStore{addErr: errors.New("seq add error")}),
		WithGraphEmbedder(stubGraphEmbedder{}),
	)
	config := newGraphLoadConfig(
		WithGraphLoadConcurrency(GraphLoadConcurrency{EmbeddingRoutines: 1}),
	)
	err := gk.addGraphDocuments(context.Background(), docs, config)
	if err == nil {
		t.Fatal("expected error from sequential doc add failure")
	}
}

// --- graphSeedEmbeddingText with content but no id in builder ---

func TestGraphSeedEmbeddingText_ContentWithCodePrefix(t *testing.T) {
	doc := &document.Document{
		ID:       "cp-1",
		Name:     "MyFunc",
		Content:  "func MyFunc() {}",
		Metadata: map[string]any{codeast.TrpcAstMetaPrefix + "type": "Function"},
	}
	text := graphSeedEmbeddingText(doc)
	if !strings.Contains(text, "code:\nfunc MyFunc() {}") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want code prefix section", text)
	}
	if !strings.Contains(text, "id: cp-1") {
		t.Fatalf("graphSeedEmbeddingText() = %q, want id field", text)
	}
}

// --- validateGraphSource ---

func TestBuiltinGraphKnowledge_ValidateGraphSource(t *testing.T) {
	tests := []struct {
		name    string
		gk      *BuiltinGraphKnowledge
		src     source.GraphSource
		wantErr string
	}{
		{
			name:    "nil source",
			gk:      NewGraphKnowledge(WithGraphStore(&stubGraphStore{}), WithGraphVectorStore(inmemory.New()), WithGraphEmbedder(stubGraphEmbedder{})),
			src:     nil,
			wantErr: "graph source cannot be nil",
		},
		{
			name:    "nil store",
			gk:      NewGraphKnowledge(WithGraphVectorStore(inmemory.New()), WithGraphEmbedder(stubGraphEmbedder{})),
			src:     &stubGraphSource{data: &graph.Data{}},
			wantErr: "graph store is not configured",
		},
		{
			name:    "nil vector store",
			gk:      NewGraphKnowledge(WithGraphStore(&stubGraphStore{}), WithGraphEmbedder(stubGraphEmbedder{})),
			src:     &stubGraphSource{data: &graph.Data{}},
			wantErr: "graph vector store is not configured",
		},
		{
			name:    "nil embedder",
			gk:      NewGraphKnowledge(WithGraphStore(&stubGraphStore{}), WithGraphVectorStore(inmemory.New())),
			src:     &stubGraphSource{data: &graph.Data{}},
			wantErr: "graph embedder is not configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.gk.validateGraphSource(tt.src)
			if err == nil {
				t.Fatalf("expected error %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
