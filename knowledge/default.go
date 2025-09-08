//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package knowledge provides the default implementation of the Knowledge interface.
package knowledge

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/loader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/retriever"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// defaultSizeBuckets defines size boundaries (bytes) used for document
// size statistics.
var defaultSizeBuckets = []int{256, 512, 1024, 2048, 4096, 8192}

// Concurrency tuning defaults.
const (
	// maxDefaultSourceParallel limits how many sources we process in parallel
	// when the caller does not specify an explicit value.
	maxDefaultSourceParallel = 4
)

// BuiltinKnowledge implements the Knowledge interface with a built-in retriever.
type BuiltinKnowledge struct {
	vectorStore   vectorstore.VectorStore
	embedder      embedder.Embedder
	retriever     retriever.Retriever
	queryEnhancer query.Enhancer
	reranker      reranker.Reranker
	sources       []source.Source
}

// Option represents a functional option for configuring BuiltinKnowledge.
type Option func(*BuiltinKnowledge)

// WithVectorStore sets the vector store for similarity search.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(dk *BuiltinKnowledge) {
		dk.vectorStore = vs
	}
}

// WithEmbedder sets the embedder for generating document embeddings.
func WithEmbedder(e embedder.Embedder) Option {
	return func(dk *BuiltinKnowledge) {
		dk.embedder = e
	}
}

// WithQueryEnhancer sets a custom query enhancer (optional).
func WithQueryEnhancer(qe query.Enhancer) Option {
	return func(dk *BuiltinKnowledge) {
		dk.queryEnhancer = qe
	}
}

// WithReranker sets a custom reranker (optional).
func WithReranker(r reranker.Reranker) Option {
	return func(dk *BuiltinKnowledge) {
		dk.reranker = r
	}
}

// WithRetriever sets a custom retriever (optional).
func WithRetriever(r retriever.Retriever) Option {
	return func(dk *BuiltinKnowledge) {
		dk.retriever = r
	}
}

// WithSources sets the knowledge sources.
func WithSources(sources []source.Source) Option {
	return func(dk *BuiltinKnowledge) {
		dk.sources = sources
	}
}

// LoadOption represents a functional option for configuring load behavior.
type LoadOption func(*loadConfig)

// loadConfig holds the configuration for load behavior.
type loadConfig struct {
	showProgress     bool
	progressStepSize int
	showStats        bool
	srcParallelism   int
	docParallelism   int
	recreate         bool
}

// WithShowProgress enables or disables progress logging during load.
func WithShowProgress(show bool) LoadOption {
	return func(lc *loadConfig) {
		lc.showProgress = show
	}
}

// WithProgressStepSize sets the granularity of progress updates.
func WithProgressStepSize(stepSize int) LoadOption {
	return func(lc *loadConfig) {
		lc.progressStepSize = stepSize
	}
}

// WithShowStats enables or disables statistics logging during load.
// By default statistics are shown.
func WithShowStats(show bool) LoadOption {
	return func(lc *loadConfig) {
		lc.showStats = show
	}
}

// WithSourceConcurrency configures how many sources can be loaded in parallel.
// A value = 1 means sequential processing.
// The default is min(4, len(sources)) when value is not specified (=0).
func WithSourceConcurrency(n int) LoadOption {
	return func(lc *loadConfig) {
		lc.srcParallelism = n
	}
}

// WithDocConcurrency configures how many documents per source can be processed
// concurrently.
// A value = 1 means sequential processing.
// The default is runtime.NumCPU() when value is not specified (=0).
func WithDocConcurrency(n int) LoadOption {
	return func(lc *loadConfig) {
		lc.docParallelism = n
	}
}

// WithRecreate recreates the vector store before loading documents, be careful to use this option.
// ATTENTION! This option will delete all documents from the vector store and recreate it.
func WithRecreate(recreate bool) LoadOption {
	return func(lc *loadConfig) {
		lc.recreate = recreate
	}
}

// New creates a new BuiltinKnowledge instance with the given options.
func New(opts ...Option) *BuiltinKnowledge {
	dk := &BuiltinKnowledge{}

	// Apply options.
	for _, opt := range opts {
		opt(dk)
	}

	// Create built-in retriever if not provided.
	if dk.retriever == nil {
		// Use defaults if not specified.
		if dk.queryEnhancer == nil {
			dk.queryEnhancer = query.NewPassthroughEnhancer()
		}
		if dk.reranker == nil {
			dk.reranker = reranker.NewTopKReranker(reranker.WithK(1))
		}

		dk.retriever = retriever.New(
			retriever.WithEmbedder(dk.embedder),
			retriever.WithVectorStore(dk.vectorStore),
			retriever.WithQueryEnhancer(dk.queryEnhancer),
			retriever.WithReranker(dk.reranker),
		)
	}
	return dk
}

// Load processes all sources and adds their documents to the knowledge base.
func (dk *BuiltinKnowledge) Load(ctx context.Context, opts ...LoadOption) error {
	err := dk.loadSource(ctx, dk.sources, opts...)
	return err
}

// loadSource loads one or more source
func (dk *BuiltinKnowledge) loadSource(
	ctx context.Context,
	sources []source.Source,
	opts ...LoadOption) error {
	if dk.vectorStore == nil {
		return fmt.Errorf("vector store not configured")
	}
	config := dk.buildLoadConfig(len(sources), opts...)
	if config.srcParallelism > 1 || config.docParallelism > 1 {
		if _, err := dk.loadConcurrent(ctx, config, sources); err != nil {
			return err
		}
		return nil
	}
	if _, err := dk.loadSequential(ctx, config, sources); err != nil {
		return err
	}
	return nil
}

// loadSourcesSequential loads sources sequentially
func (dk *BuiltinKnowledge) loadSequential(
	ctx context.Context,
	config *loadConfig,
	sources []source.Source) ([]string, error) {
	// Timing variables.
	startTime := time.Now()

	// Initialise statistics helpers.
	sizeBuckets := defaultSizeBuckets
	stats := newSizeStats(sizeBuckets)

	totalSources := len(sources)
	log.Infof("Starting knowledge base loading with %d sources", totalSources)

	var allAddedIDs []string
	var processedDocs int
	for i, src := range sources {
		sourceName := src.Name()
		sourceType := src.Type()
		log.Infof("Loading source %d/%d: %s (type: %s)", i+1, totalSources, sourceName, sourceType)

		srcStartTime := time.Now()
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			log.Errorf("Failed to read documents from source %s: %v", sourceName, err)
			return nil, fmt.Errorf("failed to read documents from source %s: %w", sourceName, err)
		}

		log.Infof("Fetched %d document(s) from source %s", len(docs), sourceName)

		// Per-source statistics.
		srcStats := newSizeStats(sizeBuckets)
		for _, d := range docs {
			sz := len(d.Content)
			srcStats.add(sz, sizeBuckets)
			stats.add(sz, sizeBuckets)
		}

		if config.showStats {
			log.Infof("Statistics for source %s:", sourceName)
			srcStats.log(sizeBuckets)
		}

		log.Infof("Start embedding & storing documents from source %s...", sourceName)

		// Process documents with progress logging if enabled.
		for j, doc := range docs {
			if err := dk.addDocument(ctx, doc); err != nil {
				log.Errorf("Failed to add document from source %s: %v", sourceName, err)
				return nil, fmt.Errorf("failed to add document from source %s: %w", sourceName, err)
			}

			allAddedIDs = append(allAddedIDs, doc.ID)
			processedDocs++

			// Log progress based on configuration.
			if config.showProgress {
				srcProcessed := j + 1
				totalSrc := len(docs)

				// Respect progressStepSize for logging frequency.
				if srcProcessed%config.progressStepSize == 0 || srcProcessed == totalSrc {
					etaSrc := calcETA(srcStartTime, srcProcessed, totalSrc)
					elapsedSrc := time.Since(srcStartTime)

					log.Infof("Processed %d/%d doc(s) | source %s | elapsed %s | ETA %s",
						srcProcessed, totalSrc, sourceName,
						elapsedSrc.Truncate(time.Second),
						etaSrc.Truncate(time.Second))
				}
			}
		}
		log.Infof("Successfully loaded source %s", sourceName)
	}

	elapsedTotal := time.Since(startTime)
	log.Infof("Knowledge base loading completed in %s (%d sources)",
		elapsedTotal, totalSources)

	// Output statistics if requested.
	if config.showStats && stats.totalDocs > 0 {
		stats.log(sizeBuckets)
	}
	return allAddedIDs, nil
}

// loadSourcesConcurrent loads sources concurrently
func (dk *BuiltinKnowledge) loadConcurrent(
	ctx context.Context,
	config *loadConfig,
	sources []source.Source) ([]string, error) {
	// Create aggregator for collecting results
	aggr := loader.NewAggregator(defaultSizeBuckets, config.showStats, config.showProgress, config.progressStepSize)
	defer aggr.Close()

	// Create worker pool for source processing
	srcPool, err := ants.NewPool(config.srcParallelism)
	if err != nil {
		return nil, fmt.Errorf("failed to create source worker pool: %w", err)
	}
	defer srcPool.Release()

	// Create worker pool for document processing
	docPool, err := ants.NewPool(config.docParallelism)
	if err != nil {
		return nil, fmt.Errorf("failed to create document worker pool: %w", err)
	}
	defer docPool.Release()

	var wg sync.WaitGroup
	var allAddedIDs []string
	var mu sync.Mutex
	errCh := make(chan error, len(sources))

	for i, src := range sources {
		wg.Add(1)
		// Capture loop variables for the closure to avoid race conditions
		srcIdx := i
		source := src
		err := srcPool.Submit(func() {
			defer wg.Done()
			sourceName := source.Name()
			sourceType := source.Type()
			log.Infof("Loading source %d/%d: %s (type: %s)", srcIdx+1, len(sources), sourceName, sourceType)
			docs, err := source.ReadDocuments(ctx)
			if err != nil {
				errCh <- fmt.Errorf("failed to read documents from source %s: %w", sourceName, err)
				return
			}
			log.Infof("Fetched %d document(s) from source %s", len(docs), sourceName)
			if err := dk.processDocuments(ctx, sourceName, docs, config, aggr, docPool); err != nil {
				errCh <- fmt.Errorf("failed to process documents for source %s: %w", sourceName, err)
				return
			}
			log.Infof("Successfully loaded source %s", sourceName)

			// Collect document IDs
			mu.Lock()
			for _, doc := range docs {
				allAddedIDs = append(allAddedIDs, doc.ID)
			}
			mu.Unlock()
		})
		if err != nil {
			wg.Done()
			errCh <- fmt.Errorf("failed to submit source processing task: %w", err)
		}
	}

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return nil, err
	}

	return allAddedIDs, nil
}

// processDocuments embeds and stores all documents from a single source using
// document-level parallelism.
func (dk *BuiltinKnowledge) processDocuments(
	ctx context.Context,
	sourceName string,
	docs []*document.Document,
	cfg *loadConfig,
	aggr *loader.Aggregator,
	pool *ants.Pool,
) error {
	var wgDoc sync.WaitGroup
	errCh := make(chan error, len(docs))

	processDoc := func(doc *document.Document, docIndex int) func() {
		return func() {
			defer wgDoc.Done()
			if err := dk.addDocument(ctx, doc); err != nil {
				errCh <- fmt.Errorf("add document: %w", err)
				return
			}

			aggr.StatCh() <- loader.StatEvent{Size: len(doc.Content)}
			if cfg.showProgress {
				aggr.ProgCh() <- loader.ProgEvent{
					SrcName:      sourceName,
					SrcProcessed: docIndex + 1,
					SrcTotal:     len(docs),
				}
			}
		}
	}

	for i, doc := range docs {
		wgDoc.Add(1)
		task := processDoc(doc, i)
		if pool != nil {
			if err := pool.Submit(task); err != nil {
				wgDoc.Done()
				errCh <- fmt.Errorf("submit doc task: %w", err)
			}
		} else {
			task()
		}
	}

	wgDoc.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return err
	}
	return nil
}

// buildLoadConfig creates a load configuration with defaults and applies the given options.
func (dk *BuiltinKnowledge) buildLoadConfig(sourceCount int, opts ...LoadOption) *loadConfig {
	// Apply load options with defaults.
	config := &loadConfig{
		showProgress:     true,
		progressStepSize: 10,
		showStats:        true,
	}
	for _, opt := range opts {
		opt(config)
	}
	if config.srcParallelism == 0 {
		if sourceCount < maxDefaultSourceParallel {
			config.srcParallelism = sourceCount
		} else {
			config.srcParallelism = maxDefaultSourceParallel
		}
	}
	if config.docParallelism == 0 {
		config.docParallelism = runtime.NumCPU()
	}
	return config
}

// addDocument adds a document to the knowledge base (internal method).
func (dk *BuiltinKnowledge) addDocument(ctx context.Context, doc *document.Document) error {
	// Generate embedding and store in vector store.
	if dk.embedder != nil && dk.vectorStore != nil {
		// Get content directly as string for embedding generation.
		content := doc.Content

		embedding, err := dk.embedder.GetEmbedding(ctx, content)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %w", err)
		}

		if err := dk.vectorStore.Add(ctx, doc, embedding); err != nil {
			return fmt.Errorf("failed to store embedding: %w", err)
		}
	}
	return nil
}

// Search implements the Knowledge interface.
// It uses the built-in retriever for the complete RAG pipeline with context awareness.
func (dk *BuiltinKnowledge) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	if dk.retriever == nil {
		return nil, fmt.Errorf("retriever not configured")
	}

	// Set defaults for search parameters.
	limit := req.MaxResults
	if limit <= 0 {
		limit = 1 // Return only the best result by default.
	}

	minScore := req.MinScore
	if minScore < 0 {
		minScore = 0.0
	}

	// Use built-in retriever for RAG pipeline with full context.
	// The retriever will handle query enhancement if configured.
	retrieverReq := &retriever.Query{
		Text:      req.Query,
		History:   req.History, // Same type now, no conversion needed
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Filter:    convertQueryFilter(req.SearchFilter),
		Limit:     limit,
		MinScore:  minScore,
	}

	result, err := dk.retriever.Retrieve(ctx, retrieverReq)
	if err != nil {
		return nil, fmt.Errorf("retrieval failed: %w", err)
	}

	if len(result.Documents) == 0 {
		return nil, fmt.Errorf("no relevant documents found")
	}

	// Return the best result.
	bestDoc := result.Documents[0]
	content := bestDoc.Document.Content

	return &SearchResult{
		Document: bestDoc.Document,
		Score:    bestDoc.Score,
		Text:     content,
	}, nil
}

// Close closes the knowledge base and releases resources.
func (dk *BuiltinKnowledge) Close() error {
	// Close components if they support closing.
	if dk.retriever != nil {
		if err := dk.retriever.Close(); err != nil {
			return fmt.Errorf("failed to close retriever: %w", err)
		}
	}
	if dk.vectorStore != nil {
		if err := dk.vectorStore.Close(); err != nil {
			return fmt.Errorf("failed to close vector store: %w", err)
		}
	}
	return nil
}

// convertQueryFilter converts retriever.QueryFilter to vectorstore.SearchFilter.
func convertQueryFilter(qf *SearchFilter) *retriever.QueryFilter {
	if qf == nil {
		return nil
	}

	return &retriever.QueryFilter{
		DocumentIDs: qf.DocumentIDs,
		Metadata:    qf.Metadata,
	}
}

// sizeStats tracks statistics for document sizes during a load run.
type sizeStats struct {
	totalDocs  int
	totalSize  int
	minSize    int
	maxSize    int
	bucketCnts []int
}

// newSizeStats returns a sizeStats initialised for the provided buckets.
func newSizeStats(buckets []int) *sizeStats {
	return &sizeStats{
		minSize:    int(^uint(0) >> 1), // Initialise with max-int.
		bucketCnts: make([]int, len(buckets)+1),
	}
}

// add records the size of a document.
func (ss *sizeStats) add(size int, buckets []int) {
	ss.totalDocs++
	ss.totalSize += size
	if size < ss.minSize {
		ss.minSize = size
	}
	if size > ss.maxSize {
		ss.maxSize = size
	}

	placed := false
	for i, upper := range buckets {
		if size < upper {
			ss.bucketCnts[i]++
			placed = true
			break
		}
	}
	if !placed {
		ss.bucketCnts[len(ss.bucketCnts)-1]++
	}
}

// avg returns the average document size.
func (ss *sizeStats) avg() float64 {
	if ss.totalDocs == 0 {
		return 0
	}
	return float64(ss.totalSize) / float64(ss.totalDocs)
}

// log outputs the collected statistics.
func (ss *sizeStats) log(buckets []int) {
	log.Infof(
		"Document statistics - total: %d, avg: %.1f B, min: %d B, max: %d B",
		ss.totalDocs, ss.avg(), ss.minSize, ss.maxSize,
	)

	lower := 0
	for i, upper := range buckets {
		if ss.bucketCnts[i] == 0 {
			lower = upper
			continue
		}
		log.Infof("  [%d, %d): %d document(s)", lower, upper,
			ss.bucketCnts[i])
		lower = upper
	}

	lastCnt := ss.bucketCnts[len(ss.bucketCnts)-1]
	if lastCnt > 0 {
		log.Infof("  [>= %d]: %d document(s)", buckets[len(buckets)-1],
			lastCnt)
	}
}

// calcETA estimates the remaining time based on throughput so far.
func calcETA(start time.Time, processed, total int) time.Duration {
	if processed == 0 || total == 0 {
		return 0
	}
	elapsed := time.Since(start)
	expected := time.Duration(float64(elapsed) /
		float64(processed) * float64(total))
	if expected < elapsed {
		return 0
	}
	return expected - elapsed
}
