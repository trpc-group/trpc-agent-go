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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	defaultGraphSearchMaxSeeds = 5
)

// GraphKnowledgeOption configures BuiltinGraphKnowledge.
type GraphKnowledgeOption func(*BuiltinGraphKnowledge)

// BuiltinGraphKnowledge is the default graph-plus-vector implementation of
// GraphKnowledge.
type BuiltinGraphKnowledge struct {
	store       graphstore.Store
	vectorStore vectorstore.VectorStore
	embedder    embedder.Embedder
}

// NewGraphKnowledge creates a new BuiltinGraphKnowledge.
func NewGraphKnowledge(opts ...GraphKnowledgeOption) *BuiltinGraphKnowledge {
	gk := &BuiltinGraphKnowledge{}
	for _, opt := range opts {
		opt(gk)
	}
	return gk
}

// WithGraphStore sets the graph store backend.
func WithGraphStore(store graphstore.Store) GraphKnowledgeOption {
	return func(gk *BuiltinGraphKnowledge) {
		gk.store = store
	}
}

// WithGraphVectorStore sets the vector store used for graph seed retrieval.
func WithGraphVectorStore(store vectorstore.VectorStore) GraphKnowledgeOption {
	return func(gk *BuiltinGraphKnowledge) {
		gk.vectorStore = store
	}
}

// WithGraphEmbedder sets the embedder used for graph seed retrieval.
func WithGraphEmbedder(e embedder.Embedder) GraphKnowledgeOption {
	return func(gk *BuiltinGraphKnowledge) {
		gk.embedder = e
	}
}

// Search implements Knowledge by using graph-native retrieval.
func (gk *BuiltinGraphKnowledge) Search(
	ctx context.Context,
	req *SearchRequest,
) (*SearchResult, error) {
	if req == nil {
		return nil, errors.New("search request cannot be nil")
	}
	if strings.TrimSpace(req.Query) == "" && !hasSearchFilter(req.SearchFilter) {
		return nil, errors.New("search query cannot be empty")
	}

	seeds, err := gk.retrieveSeedNodes(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return nil, errors.New("no relevant information found")
	}

	docResults := make([]*Result, 0, len(seeds))
	for _, seed := range seeds {
		docResults = append(docResults, &Result{
			Document: &document.Document{
				ID:       seed.node.ID,
				Name:     seed.node.Name,
				Content:  seed.node.Content,
				Metadata: cloneMetadata(seed.node.Metadata),
			},
			Score: seed.score,
		})
	}

	top := docResults[0]
	return &SearchResult{
		Document:  top.Document,
		Score:     top.Score,
		Text:      top.Document.Content,
		Documents: docResults,
	}, nil
}

// LoadGraphSource reads graph data from a graph-native source.
func (gk *BuiltinGraphKnowledge) LoadGraphSource(
	ctx context.Context,
	src source.GraphSource,
	opts ...LoadOption,
) error {
	if err := gk.validateGraphSource(src); err != nil {
		return err
	}
	config := newGraphLoadConfig(opts...)
	start := time.Now()
	sourceName := graphSourceName(src)
	data, err := readGraphSourceData(ctx, src, sourceName, config, start)
	if err != nil {
		return err
	}
	if err := gk.storeGraphData(ctx, data, config); err != nil {
		return err
	}
	indexedSeeds, err := gk.indexGraphDataDocuments(ctx, data, config)
	if err != nil {
		return err
	}
	if config.showProgress {
		log.InfofContext(ctx, "Loaded graph source %s | nodes %d | edges %d | seeds %d | elapsed %s",
			sourceName, len(data.Nodes), len(data.Edges), indexedSeeds, time.Since(start).Truncate(time.Second))
	}
	return nil
}

func (gk *BuiltinGraphKnowledge) validateGraphSource(src source.GraphSource) error {
	if src == nil {
		return errors.New("graph source cannot be nil")
	}
	if gk.store == nil {
		return errors.New("graph store is not configured")
	}
	if gk.vectorStore == nil {
		return errors.New("graph vector store is not configured")
	}
	if gk.embedder == nil {
		return errors.New("graph embedder is not configured")
	}
	return nil
}

func newGraphLoadConfig(opts ...LoadOption) *loadConfig {
	config := &loadConfig{}
	for _, opt := range opts {
		opt(config)
	}
	if config.docParallelism == 0 {
		config.docParallelism = runtime.NumCPU()
	}
	if config.progressStepSize <= 0 {
		config.progressStepSize = 100
	}
	return config
}

func readGraphSourceData(
	ctx context.Context,
	src source.GraphSource,
	sourceName string,
	config *loadConfig,
	start time.Time,
) (*graph.Data, error) {
	if config.showProgress {
		log.InfofContext(ctx, "Reading graph source %s", sourceName)
	}
	data, err := src.ReadGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("read graph source: %w", err)
	}
	if data == nil {
		return nil, errors.New("graph data cannot be nil")
	}
	if config.showProgress {
		log.InfofContext(ctx, "Read graph source %s: %d node(s), %d edge(s) | elapsed %s",
			sourceName, len(data.Nodes), len(data.Edges), time.Since(start).Truncate(time.Second))
	}
	return data, nil
}

func (gk *BuiltinGraphKnowledge) storeGraphData(ctx context.Context, data *graph.Data, config *loadConfig) error {
	if len(data.Nodes) > 0 {
		if config.showProgress {
			log.InfofContext(ctx, "Adding %d graph node(s)", len(data.Nodes))
		}
		if err := gk.addGraphNodes(ctx, data.Nodes, config); err != nil {
			return fmt.Errorf("add graph nodes: %w", err)
		}
	}
	if len(data.Edges) > 0 {
		if config.showProgress {
			log.InfofContext(ctx, "Adding %d graph edge(s)", len(data.Edges))
		}
		if err := gk.addGraphEdges(ctx, data.Edges, config); err != nil {
			return fmt.Errorf("add graph edges: %w", err)
		}
	}
	return nil
}

func (gk *BuiltinGraphKnowledge) indexGraphDataDocuments(
	ctx context.Context,
	data *graph.Data,
	config *loadConfig,
) (int, error) {
	docs := graphDataDocuments(data)
	if config.showProgress {
		log.InfofContext(ctx, "Indexing %d graph seed document(s)", len(docs))
	}
	if err := gk.addGraphDocuments(ctx, docs, config); err != nil {
		return 0, err
	}
	return len(docs), nil
}

func graphSourceName(src source.GraphSource) string {
	type namedSource interface {
		Name() string
	}
	if named, ok := src.(namedSource); ok {
		if name := strings.TrimSpace(named.Name()); name != "" {
			return name
		}
	}
	return "graph source"
}

func (gk *BuiltinGraphKnowledge) addGraphNodes(ctx context.Context, nodes []*graph.Node, config *loadConfig) error {
	if !config.showProgress {
		return gk.store.AddNodes(ctx, nodes)
	}
	for start := 0; start < len(nodes); start += config.progressStepSize {
		end := start + config.progressStepSize
		if end > len(nodes) {
			end = len(nodes)
		}
		if err := gk.store.AddNodes(ctx, nodes[start:end]); err != nil {
			return err
		}
		if config.showProgress {
			log.InfofContext(ctx, "Added %d/%d graph node(s)", end, len(nodes))
		}
	}
	return nil
}

func (gk *BuiltinGraphKnowledge) addGraphEdges(ctx context.Context, edges []*graph.Edge, config *loadConfig) error {
	if !config.showProgress {
		return gk.store.AddEdges(ctx, edges)
	}
	for start := 0; start < len(edges); start += config.progressStepSize {
		end := start + config.progressStepSize
		if end > len(edges) {
			end = len(edges)
		}
		if err := gk.store.AddEdges(ctx, edges[start:end]); err != nil {
			return err
		}
		if config.showProgress {
			log.InfofContext(ctx, "Added %d/%d graph edge(s)", end, len(edges))
		}
	}
	return nil
}

// Traverse runs graph traversal against the configured graph store.
func (gk *BuiltinGraphKnowledge) Traverse(
	ctx context.Context,
	query *graph.TraverseQuery,
) (*graph.TraverseResult, error) {
	if gk.store == nil {
		return nil, errors.New("graph store is not configured")
	}
	return gk.store.Traverse(ctx, query)
}

// FindPaths runs graph path search against the configured graph store.
func (gk *BuiltinGraphKnowledge) FindPaths(
	ctx context.Context,
	query *graph.PathQuery,
) (*graph.PathResult, error) {
	if gk.store == nil {
		return nil, errors.New("graph store is not configured")
	}
	return gk.store.FindPaths(ctx, query)
}

func (gk *BuiltinGraphKnowledge) retrieveSeedNodes(
	ctx context.Context,
	req *SearchRequest,
) ([]*graphSeed, error) {
	if gk.vectorStore == nil {
		return nil, errors.New("graph vector store is not configured")
	}
	maxSeeds := resolvePositiveInt(req.MaxResults, defaultGraphSearchMaxSeeds)

	query := strings.TrimSpace(req.Query)
	var embedding []float64
	if query != "" {
		if gk.embedder == nil {
			return nil, errors.New("graph embedder is not configured")
		}
		var err error
		embedding, err = gk.embedder.GetEmbedding(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("generate graph search embedding: %w", err)
		}
	}
	searchMode := vectorstore.SearchMode(req.SearchMode)
	if query == "" && hasSearchFilter(req.SearchFilter) {
		searchMode = vectorstore.SearchModeFilter
	}

	result, err := gk.vectorStore.Search(ctx, &vectorstore.SearchQuery{
		Query:      query,
		Vector:     embedding,
		Limit:      maxSeeds,
		MinScore:   req.MinScore,
		Filter:     convertSearchFilter(req.SearchFilter),
		SearchMode: searchMode,
	})
	if err != nil {
		return nil, fmt.Errorf("search graph seeds: %w", err)
	}
	if result == nil {
		return nil, nil
	}
	seeds := make([]*graphSeed, 0, len(result.Results))
	seen := make(map[string]struct{}, len(result.Results))
	for _, scored := range result.Results {
		if scored == nil || scored.Document == nil {
			continue
		}
		node := graphNodeFromDocument(scored.Document)
		if node == nil || node.ID == "" {
			continue
		}
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		seeds = append(seeds, &graphSeed{
			node:  node,
			score: scored.Score,
		})
	}
	return seeds, nil
}

type graphSeed struct {
	node  *graph.Node
	score float64
}

func convertSearchFilter(filter *SearchFilter) *vectorstore.SearchFilter {
	if filter == nil {
		return nil
	}
	return &vectorstore.SearchFilter{
		IDs:             filter.DocumentIDs,
		Metadata:        filter.Metadata,
		FilterCondition: filter.FilterCondition,
	}
}

func graphNodeFromDocument(doc *document.Document) *graph.Node {
	if doc == nil || doc.ID == "" {
		return nil
	}
	id := doc.ID
	name := doc.Name
	if name == "" {
		name = id
	}
	return &graph.Node{
		ID:       id,
		Name:     name,
		Content:  doc.Content,
		Metadata: cloneMetadata(doc.Metadata),
	}
}

func (gk *BuiltinGraphKnowledge) addGraphDocuments(
	ctx context.Context,
	docs []*document.Document,
	config *loadConfig,
) error {
	concurrency := config.docParallelism
	if concurrency <= 1 || len(docs) <= 1 {
		for i, doc := range docs {
			if err := gk.addGraphDocument(ctx, doc); err != nil {
				return err
			}
			gk.reportGraphDocumentProgress(ctx, config, i+1, len(docs))
		}
		return nil
	}
	if concurrency > len(docs) {
		concurrency = len(docs)
	}

	jobs := make(chan *document.Document)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var processed int64
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for doc := range jobs {
				if err := gk.addGraphDocument(ctx, doc); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				current := int(atomic.AddInt64(&processed, 1))
				gk.reportGraphDocumentProgress(ctx, config, current, len(docs))
			}
		}()
	}
	for _, doc := range docs {
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return err
		case jobs <- doc:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (gk *BuiltinGraphKnowledge) reportGraphDocumentProgress(ctx context.Context, config *loadConfig, processed, total int) {
	if !config.showProgress {
		return
	}
	if processed%config.progressStepSize != 0 && processed != total {
		return
	}
	log.InfofContext(ctx, "Indexed %d/%d graph seed document(s)", processed, total)
}

func (gk *BuiltinGraphKnowledge) addGraphDocument(ctx context.Context, doc *document.Document) error {
	if doc == nil {
		return nil
	}
	if doc.ID == "" {
		return errors.New("graph document id cannot be empty")
	}
	embedding, err := gk.embedder.GetEmbedding(ctx, buildEmbeddingText(doc))
	if err != nil {
		return fmt.Errorf("generate graph seed embedding: %w", err)
	}
	if err := gk.vectorStore.Add(ctx, doc, embedding); err != nil {
		return fmt.Errorf("add graph seed document: %w", err)
	}
	return nil
}

func graphDataDocuments(data *graph.Data) []*document.Document {
	if data == nil {
		return nil
	}
	docs := make([]*document.Document, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		if node == nil || node.ID == "" {
			continue
		}
		metadata := cloneMetadata(node.Metadata)
		docs = append(docs, &document.Document{
			ID:       node.ID,
			Name:     node.Name,
			Content:  node.Content,
			Metadata: metadata,
		})
	}
	return docs
}

func resolvePositiveInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	return cloned
}
