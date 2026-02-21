//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeTool struct {
	decl *tool.Declaration
}

func (t fakeTool) Declaration() *tool.Declaration { return t.decl }

type fakeSearcher struct {
	mu sync.Mutex

	gotCandidates map[string]tool.Tool
	gotQuery      string
	gotTopK       int

	ret []string
	err error
}

func (s *fakeSearcher) Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) (context.Context, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gotCandidates = make(map[string]tool.Tool, len(candidates))
	for k, v := range candidates {
		s.gotCandidates[k] = v
	}
	s.gotQuery = query
	s.gotTopK = topK
	return ctx, append([]string(nil), s.ret...), s.err
}

type fakeModel struct {
	info model.Info

	// generate is invoked by GenerateContent; it may return a closed channel.
	generate func(ctx context.Context, req *model.Request) (<-chan *model.Response, error)

	lastReq *model.Request
}

func (m *fakeModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.lastReq = req
	if m.generate != nil {
		return m.generate(ctx, req)
	}
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *fakeModel) Info() model.Info { return m.info }

func respCh(resps ...*model.Response) <-chan *model.Response {
	ch := make(chan *model.Response, len(resps))
	for _, r := range resps {
		ch <- r
	}
	close(ch)
	return ch
}

type fakeEmbedder struct {
	mu sync.Mutex

	texts []string

	embedErr error
}

var _ embedder.Embedder = (*fakeEmbedder)(nil)

func (e *fakeEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	e.texts = append(e.texts, text)
	err := e.embedErr
	e.mu.Unlock()

	if err != nil {
		return nil, err
	}
	// Deterministic, non-empty embedding.
	return []float64{float64(len(text)), 1}, nil
}

func (e *fakeEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, err := e.GetEmbedding(ctx, text)
	if err != nil {
		return nil, nil, err
	}
	return v, map[string]any{"tokens": len(text)}, nil
}

func (e *fakeEmbedder) GetDimensions() int { return 2 }

type fakeVectorStore struct {
	mu sync.Mutex

	addErr error
	adds   []struct {
		id        string
		embedding []float64
	}

	searchErr error
	// searchResultIDs defines the IDs returned by Search, in order.
	searchResultIDs []string
	lastSearchQuery *vectorstore.SearchQuery
}

var _ vectorstore.VectorStore = (*fakeVectorStore)(nil)

func (s *fakeVectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addErr != nil {
		return s.addErr
	}
	s.adds = append(s.adds, struct {
		id        string
		embedding []float64
	}{id: doc.ID, embedding: append([]float64(nil), embedding...)})
	return nil
}

func (s *fakeVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	return nil, nil, nil
}

func (s *fakeVectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return nil
}

func (s *fakeVectorStore) Delete(ctx context.Context, id string) error { return nil }

func (s *fakeVectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSearchQuery = query
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	out := &vectorstore.SearchResult{Results: make([]*vectorstore.ScoredDocument, 0, len(s.searchResultIDs))}
	for i, id := range s.searchResultIDs {
		out.Results = append(out.Results, &vectorstore.ScoredDocument{
			Document: &document.Document{ID: id},
			Score:    1 - float64(i)*0.01,
		})
	}
	return out, nil
}

func (s *fakeVectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	return nil
}

func (s *fakeVectorStore) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	return 0, nil
}

func (s *fakeVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	return 0, nil
}
func (s *fakeVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	return nil, nil
}
func (s *fakeVectorStore) Close() error { return nil }
