//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/retriever"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

func TestWithStoresOptionOrder(t *testing.T) {
	vectorA := inmemory.New()
	vectorB := inmemory.New()
	graphA := &storesTestGraphStore{}
	graphB := &storesTestGraphStore{}
	resourceA := &resourceTestStore{}
	resourceB := &resourceTestStore{}

	tests := []struct {
		name         string
		opts         []Option
		wantVector   vectorstore.VectorStore
		wantGraph    graphstore.Store
		wantResource *resourceTestStore
	}{
		{
			name: "later vector option only replaces vector",
			opts: []Option{
				WithStores(Stores{Vector: vectorA, Graph: graphA, Resource: resourceA}),
				WithVectorStore(vectorB),
			},
			wantVector:   vectorB,
			wantGraph:    graphA,
			wantResource: resourceA,
		},
		{
			name: "later stores option replaces vector",
			opts: []Option{
				WithVectorStore(vectorB),
				WithStores(Stores{Vector: vectorA, Graph: graphA, Resource: resourceA}),
			},
			wantVector:   vectorA,
			wantGraph:    graphA,
			wantResource: resourceA,
		},
		{
			name: "later stores option replaces all fields",
			opts: []Option{
				WithStores(Stores{Vector: vectorA, Graph: graphA, Resource: resourceA}),
				WithStores(Stores{Vector: vectorB, Graph: graphB, Resource: resourceB}),
			},
			wantVector:   vectorB,
			wantGraph:    graphB,
			wantResource: resourceB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New(tt.opts...)
			if kb.vectorStore != tt.wantVector {
				t.Fatalf("vector store = %p, want %p", kb.vectorStore, tt.wantVector)
			}
			if kb.graphStore != tt.wantGraph {
				t.Fatalf("graph store = %p, want %p", kb.graphStore, tt.wantGraph)
			}
			if kb.resourceStore != tt.wantResource {
				t.Fatalf("resource store = %p, want %p", kb.resourceStore, tt.wantResource)
			}
		})
	}
}

func TestBuiltinKnowledgeGraphAvailability(t *testing.T) {
	tests := []struct {
		name      string
		graph     graphstore.Store
		available bool
	}{
		{name: "not configured"},
		{name: "store without native search", graph: &storesTestGraphStore{}},
		{name: "store with native search", graph: &storesTestSearchableGraphStore{}, available: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := New(WithStores(Stores{Graph: tt.graph}))
			view, ok := kb.Graph()
			if ok != tt.available {
				t.Fatalf("Graph() available = %v, want %v", ok, tt.available)
			}
			if (view != nil) != tt.available {
				t.Fatalf("Graph() view nil = %v, want availability %v", view == nil, tt.available)
			}
			if tt.available {
				builtin, ok := view.(*BuiltinGraphKnowledge)
				if !ok {
					t.Fatalf("Graph() view type = %T, want *BuiltinGraphKnowledge", view)
				}
				if builtin.store != tt.graph {
					t.Fatal("Graph() view does not borrow the configured graph store")
				}
			}
		})
	}
}

func TestBuiltinKnowledgeGraphOnlySearchFailsExplicitly(t *testing.T) {
	kb := New(WithStores(Stores{Graph: &storesTestSearchableGraphStore{}}))
	_, err := kb.Search(context.Background(), &SearchRequest{Query: "document query"})
	if err == nil || err.Error() != "retrieval failed: vector store not configured" {
		t.Fatalf("Search() error = %v, want missing vector store error", err)
	}
}

func TestBuiltinKnowledgeGraphNativeSearchMappingAndDeduplication(t *testing.T) {
	store := &storesTestSearchableGraphStore{
		searchResult: &graphstore.SearchNodesResult{Nodes: []*graphstore.ScoredNode{
			{Node: &graph.Node{ID: "node-a", Name: "first", Content: "first content", Metadata: map[string]any{"kind": "method"}}, Score: 0.91},
			nil,
			{Node: nil, Score: 0.8},
			{Node: &graph.Node{}, Score: 0.7},
			{Node: &graph.Node{ID: "node-a", Name: "duplicate", Content: "duplicate content"}, Score: 0.99},
			{Node: &graph.Node{ID: "node-b", Name: "second", Content: "second content"}, Score: 0.62},
		}},
	}
	kb := New(WithStores(Stores{Graph: store}))
	view, ok := kb.Graph()
	if !ok {
		t.Fatal("Graph() is unavailable for a native searchable store")
	}

	filter := &SearchFilter{
		DocumentIDs: []string{"node-a", "node-b"},
		Metadata:    map[string]any{"visibility": "public"},
	}
	result, err := view.Search(context.Background(), &SearchRequest{
		Query:        "  graph query  ",
		MaxResults:   7,
		MinScore:     0.4,
		SearchFilter: filter,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	wantRequest := &graphstore.SearchNodesRequest{
		Query:    "graph query",
		Mode:     graphstore.SearchModeHybrid,
		Limit:    7,
		MinScore: 0.4,
		Filter: &graphstore.SearchNodesFilter{
			NodeIDs:  []string{"node-a", "node-b"},
			Metadata: map[string]any{"visibility": "public"},
		},
	}
	if !reflect.DeepEqual(store.searchRequest, wantRequest) {
		t.Fatalf("SearchNodes() request = %#v, want %#v", store.searchRequest, wantRequest)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("Search() document count = %d, want 2", len(result.Documents))
	}
	if got := result.Documents[0]; got.Document.ID != "node-a" || got.Document.Name != "first" || got.Score != 0.91 {
		t.Fatalf("first result = %+v, want first occurrence of node-a", got)
	}
	if got := result.Documents[1]; got.Document.ID != "node-b" || got.Score != 0.62 {
		t.Fatalf("second result = %+v, want node-b", got)
	}
	if result.Document != result.Documents[0].Document || result.Score != 0.91 || result.Text != "first content" {
		t.Fatalf("top-level result = %+v, want first deduplicated node", result)
	}
}

func TestNativeGraphSearchModeMapping(t *testing.T) {
	tests := []struct {
		name string
		req  *SearchRequest
		want graphstore.SearchMode
	}{
		{name: "default hybrid", req: &SearchRequest{Query: "q"}, want: graphstore.SearchModeHybrid},
		{name: "vector", req: &SearchRequest{Query: "q", SearchMode: vectorstore.SearchModeVector}, want: graphstore.SearchModeVector},
		{name: "keyword", req: &SearchRequest{Query: "q", SearchMode: vectorstore.SearchModeKeyword}, want: graphstore.SearchModeKeyword},
		{name: "explicit filter", req: &SearchRequest{Query: "q", SearchMode: vectorstore.SearchModeFilter}, want: graphstore.SearchModeFilter},
		{name: "filter only", req: &SearchRequest{SearchFilter: &SearchFilter{DocumentIDs: []string{"n1"}}}, want: graphstore.SearchModeFilter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nativeGraphSearchMode(tt.req); got != tt.want {
				t.Fatalf("nativeGraphSearchMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuiltinKnowledgeCloseOnceAggregatesErrorsAndClosesGraph(t *testing.T) {
	retrieverErr := errors.New("retriever close failed")
	vectorErr := errors.New("vector close failed")
	graphErr := errors.New("graph close failed")
	resourceErr := errors.New("resource close failed")
	r := &storesTestRetriever{closeErr: retrieverErr}
	v := &storesTestVectorStore{closeErr: vectorErr}
	g := &storesTestSearchableGraphStore{
		storesTestGraphStore: storesTestGraphStore{closeErr: graphErr},
	}
	resource := &resourceTestStore{closeErr: resourceErr}
	kb := New(
		WithRetriever(r),
		WithStores(Stores{Vector: v, Graph: g, Resource: resource}),
	)

	firstErr := kb.Close()
	secondErr := kb.Close()
	if firstErr == nil {
		t.Fatal("Close() error = nil, want joined close errors")
	}
	if secondErr != firstErr {
		t.Fatalf("second Close() error = %v, want cached error %v", secondErr, firstErr)
	}
	for _, wantErr := range []error{retrieverErr, vectorErr, graphErr, resourceErr} {
		if !errors.Is(firstErr, wantErr) {
			t.Errorf("Close() error %q does not contain %q", firstErr, wantErr)
		}
	}
	if r.closeCalls != 1 || v.closeCalls != 1 || g.closeCalls != 1 || resource.closeCalls != 1 {
		t.Fatalf(
			"close calls = retriever:%d vector:%d graph:%d resource:%d, want each exactly once",
			r.closeCalls, v.closeCalls, g.closeCalls, resource.closeCalls,
		)
	}
}

type storesTestGraphStore struct {
	closeErr   error
	closeCalls int
}

func (*storesTestGraphStore) AddNodes(context.Context, []*graph.Node) error { return nil }

func (*storesTestGraphStore) AddEdges(context.Context, []*graph.Edge) error { return nil }

func (*storesTestGraphStore) Traverse(context.Context, *graph.TraverseQuery) (*graph.TraverseResult, error) {
	return &graph.TraverseResult{}, nil
}

func (*storesTestGraphStore) FindPaths(context.Context, *graph.PathQuery) (*graph.PathResult, error) {
	return &graph.PathResult{}, nil
}

func (s *storesTestGraphStore) Close() error {
	s.closeCalls++
	return s.closeErr
}

type storesTestSearchableGraphStore struct {
	storesTestGraphStore
	searchRequest *graphstore.SearchNodesRequest
	searchResult  *graphstore.SearchNodesResult
	searchErr     error
}

func (s *storesTestSearchableGraphStore) SearchNodes(
	_ context.Context,
	req *graphstore.SearchNodesRequest,
) (*graphstore.SearchNodesResult, error) {
	s.searchRequest = req
	return s.searchResult, s.searchErr
}

type storesTestRetriever struct {
	closeErr   error
	closeCalls int
}

func (*storesTestRetriever) Retrieve(context.Context, *retriever.Query) (*retriever.Result, error) {
	return &retriever.Result{}, nil
}

func (r *storesTestRetriever) Close() error {
	r.closeCalls++
	return r.closeErr
}

type storesTestVectorStore struct {
	closeErr   error
	closeCalls int
}

func (*storesTestVectorStore) Add(context.Context, *document.Document, []float64) error { return nil }

func (*storesTestVectorStore) Get(context.Context, string) (*document.Document, []float64, error) {
	return nil, nil, nil
}

func (*storesTestVectorStore) Update(context.Context, *document.Document, []float64) error {
	return nil
}

func (*storesTestVectorStore) Delete(context.Context, string) error { return nil }

func (*storesTestVectorStore) Search(context.Context, *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	return &vectorstore.SearchResult{}, nil
}

func (*storesTestVectorStore) DeleteByFilter(context.Context, ...vectorstore.DeleteOption) error {
	return nil
}

func (*storesTestVectorStore) UpdateByFilter(context.Context, ...vectorstore.UpdateByFilterOption) (int64, error) {
	return 0, nil
}

func (*storesTestVectorStore) Count(context.Context, ...vectorstore.CountOption) (int, error) {
	return 0, nil
}

func (*storesTestVectorStore) GetMetadata(context.Context, ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, nil
}

func (s *storesTestVectorStore) Close() error {
	s.closeCalls++
	return s.closeErr
}
