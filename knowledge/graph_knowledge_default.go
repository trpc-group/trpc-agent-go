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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	defaultGraphSearchMaxSeeds   = 5
	defaultGraphStoreRoutines    = 4
	defaultGraphDocumentRoutines = 30
	defaultGraphNodeContentRunes = 16 * 1024
)

// GraphKnowledgeOption configures BuiltinGraphKnowledge.
type GraphKnowledgeOption func(*BuiltinGraphKnowledge)

// GraphLoadOption configures graph source loading behavior.
type GraphLoadOption func(*graphLoadConfig)

// GraphLoadConcurrency configures concurrency for graph knowledge ingestion stages.
// Source-specific parsing concurrency is configured on the source itself.
type GraphLoadConcurrency struct {
	// AddNodeRoutines controls graph node insertion routines.
	AddNodeRoutines int
	// AddEdgeRoutines controls graph edge insertion routines.
	AddEdgeRoutines int
	// EmbeddingRoutines controls graph seed document embedding and vector insertion routines.
	EmbeddingRoutines int
}

type graphLoadConfig struct {
	showProgress     bool
	progressStepSize int
	concurrency      GraphLoadConcurrency
	readGraphOpts    []source.ReadGraphOption
}

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

// WithGraphLoadProgress enables or disables progress logging during graph source load.
func WithGraphLoadProgress(show bool) GraphLoadOption {
	return func(config *graphLoadConfig) {
		config.showProgress = show
	}
}

// WithGraphLoadProgressStepSize sets the graph source load progress update granularity.
func WithGraphLoadProgressStepSize(stepSize int) GraphLoadOption {
	return func(config *graphLoadConfig) {
		config.progressStepSize = stepSize
	}
}

// WithGraphLoadConcurrency configures concurrency for graph source loading stages.
func WithGraphLoadConcurrency(concurrency GraphLoadConcurrency) GraphLoadOption {
	return func(config *graphLoadConfig) {
		config.concurrency = concurrency
	}
}

// WithGraphLoadReadGraphOpts passes options to the GraphSource.ReadGraph call.
func WithGraphLoadReadGraphOpts(opts ...source.ReadGraphOption) GraphLoadOption {
	return func(config *graphLoadConfig) {
		config.readGraphOpts = append(config.readGraphOpts, opts...)
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
	opts ...GraphLoadOption,
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
	truncateGraphDataContent(data)
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

func newGraphLoadConfig(opts ...GraphLoadOption) *graphLoadConfig {
	config := &graphLoadConfig{}
	for _, opt := range opts {
		opt(config)
	}
	if config.concurrency.AddNodeRoutines == 0 {
		config.concurrency.AddNodeRoutines = defaultGraphStoreRoutines
	}
	if config.concurrency.AddEdgeRoutines == 0 {
		config.concurrency.AddEdgeRoutines = defaultGraphStoreRoutines
	}
	if config.concurrency.EmbeddingRoutines == 0 {
		config.concurrency.EmbeddingRoutines = defaultGraphDocumentRoutines
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
	config *graphLoadConfig,
	start time.Time,
) (*graph.Data, error) {
	if config.showProgress {
		log.InfofContext(ctx, "Reading graph source %s", sourceName)
	}
	data, err := src.ReadGraph(ctx, config.readGraphOpts...)
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

func (gk *BuiltinGraphKnowledge) storeGraphData(ctx context.Context, data *graph.Data, config *graphLoadConfig) error {
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
	config *graphLoadConfig,
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

func (gk *BuiltinGraphKnowledge) addGraphNodes(ctx context.Context, nodes []*graph.Node, config *graphLoadConfig) error {
	concurrency := config.concurrency.AddNodeRoutines
	if concurrency <= 1 && !config.showProgress {
		return gk.store.AddNodes(ctx, nodes)
	}
	return runGraphBatches(ctx, len(nodes), config.progressStepSize, concurrency, func(start, end int) error {
		return gk.store.AddNodes(ctx, nodes[start:end])
	}, func(processed int) {
		if config.showProgress {
			log.InfofContext(ctx, "Added %d/%d graph node(s)", processed, len(nodes))
		}
	})
}

func (gk *BuiltinGraphKnowledge) addGraphEdges(ctx context.Context, edges []*graph.Edge, config *graphLoadConfig) error {
	concurrency := config.concurrency.AddEdgeRoutines
	if concurrency <= 1 && !config.showProgress {
		return gk.store.AddEdges(ctx, edges)
	}
	return runGraphBatches(ctx, len(edges), config.progressStepSize, concurrency, func(start, end int) error {
		return gk.store.AddEdges(ctx, edges[start:end])
	}, func(processed int) {
		if config.showProgress {
			log.InfofContext(ctx, "Added %d/%d graph edge(s)", processed, len(edges))
		}
	})
}

func runGraphBatches(
	ctx context.Context,
	total int,
	batchSize int,
	concurrency int,
	process func(start, end int) error,
	report func(processed int),
) error {
	if total == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = total
	}
	if concurrency <= 1 {
		for start := 0; start < total; start += batchSize {
			end := start + batchSize
			if end > total {
				end = total
			}
			if err := process(start, end); err != nil {
				return err
			}
			report(end)
		}
		return nil
	}

	type batch struct {
		start int
		end   int
	}
	jobs := make(chan batch)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var processed int64
	if maxWorkers := (total + batchSize - 1) / batchSize; concurrency > maxWorkers {
		concurrency = maxWorkers
	}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := process(job.start, job.end); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				current := int(atomic.AddInt64(&processed, int64(job.end-job.start)))
				report(current)
			}
		}()
	}
	for start := 0; start < total; start += batchSize {
		end := start + batchSize
		if end > total {
			end = total
		}
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return err
		case jobs <- batch{start: start, end: end}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
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
	config *graphLoadConfig,
) error {
	concurrency := config.concurrency.EmbeddingRoutines
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
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
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

func (gk *BuiltinGraphKnowledge) reportGraphDocumentProgress(ctx context.Context, config *graphLoadConfig, processed, total int) {
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
	embeddingText := graphSeedEmbeddingText(doc)
	embedding, err := gk.embedder.GetEmbedding(ctx, embeddingText)
	if err != nil {
		return fmt.Errorf("generate graph seed embedding: %w", err)
	}
	if err := gk.vectorStore.Add(ctx, doc, embedding); err != nil {
		return fmt.Errorf("add graph seed document: %w", err)
	}
	return nil
}

func graphSeedEmbeddingText(doc *document.Document) string {
	if doc == nil {
		return ""
	}
	if doc.EmbeddingText != "" {
		return doc.EmbeddingText
	}
	if doc.Metadata == nil {
		return truncateGraphSeedContent(doc.Content)
	}

	var builder strings.Builder
	appendGraphSeedField(&builder, "id", doc.ID)
	appendGraphSeedField(&builder, "type", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"type"))
	name := doc.Name
	if name == "" {
		name = graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"name")
	}
	appendGraphSeedField(&builder, "name", name)
	appendGraphSeedField(&builder, "full_name", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"full_name"))
	appendGraphSeedField(&builder, "package", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"package"))
	appendGraphSeedField(&builder, "file_path", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"file_path"))
	appendGraphSeedField(&builder, "signature", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"signature"))
	appendGraphSeedField(&builder, "comment", graphSeedMetadataString(doc.Metadata, codeast.TrpcAstMetaPrefix+"comment"))

	content := truncateGraphSeedContent(doc.Content)
	if content != "" {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString("code:\n")
		builder.WriteString(content)
	}
	if builder.Len() == 0 {
		return doc.Content
	}
	return builder.String()
}

func appendGraphSeedField(builder *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString(key)
	builder.WriteString(": ")
	builder.WriteString(value)
}

func graphSeedMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func truncateGraphSeedContent(content string) string {
	if defaultGraphNodeContentRunes <= 0 {
		return ""
	}
	count := 0
	for index := range content {
		if count == defaultGraphNodeContentRunes {
			return content[:index] + "\n...<truncated>"
		}
		count++
	}
	return content
}

func truncateGraphDataContent(data *graph.Data) {
	if data == nil {
		return
	}
	for _, node := range data.Nodes {
		if node == nil {
			continue
		}
		node.Content = truncateGraphSeedContent(node.Content)
	}
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
