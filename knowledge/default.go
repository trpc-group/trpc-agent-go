//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	// Apply load options with defaults.
	config := &loadConfig{
		showProgress:     true,
		progressStepSize: 10,
		showStats:        true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Derive automatic defaults when the caller did not specify explicit values.
	if config.srcParallelism == 0 {
		// Use the smaller of default cap or number of available sources.
		if len(dk.sources) < maxDefaultSourceParallel {
			config.srcParallelism = len(dk.sources)
		} else {
			config.srcParallelism = maxDefaultSourceParallel
		}
	}

	if config.docParallelism == 0 {
		// Match logical CPUs for CPU-bound embedding work.
		config.docParallelism = runtime.NumCPU()
	}

	// Use the concurrent loader when there is any real parallelism to gain.
	if config.srcParallelism > 1 || config.docParallelism > 1 {
		return dk.loadConcurrent(ctx, config)
	}

	// Timing variables.
	startTime := time.Now()

	// Initialise statistics helpers.
	sizeBuckets := defaultSizeBuckets
	stats := newSizeStats(sizeBuckets)

	totalSources := len(dk.sources)
	log.Infof("Starting knowledge base loading with %d sources", totalSources)

	var processedDocs int
	for i, src := range dk.sources {
		sourceName := src.Name()
		sourceType := src.Type()
		log.Infof("Loading source %d/%d: %s (type: %s)", i+1, totalSources, sourceName, sourceType)

		srcStartTime := time.Now()
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			log.Errorf("Failed to read documents from source %s: %v", sourceName, err)
			return fmt.Errorf("failed to read documents from source %s: %w", sourceName, err)
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
				return fmt.Errorf("failed to add document from source %s: %w", sourceName, err)
			}

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
	return nil
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

	// Enhanced query using conversation context.
	finalQuery := req.Query
	if dk.queryEnhancer != nil {
		queryReq := &query.Request{
			Query:     req.Query,
			History:   convertConversationHistory(req.History),
			UserID:    req.UserID,
			SessionID: req.SessionID,
		}
		enhanced, err := dk.queryEnhancer.EnhanceQuery(ctx, queryReq)
		if err != nil {
			return nil, fmt.Errorf("query enhancement failed: %w", err)
		}
		finalQuery = enhanced.Enhanced
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

	// Use built-in retriever for RAG pipeline.
	retrieverReq := &retriever.Query{
		Text:     finalQuery,
		Limit:    limit,
		MinScore: minScore,
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

// convertConversationHistory converts conversation messages to query format.
func convertConversationHistory(history []ConversationMessage) []query.ConversationMessage {
	result := make([]query.ConversationMessage, len(history))
	for i, msg := range history {
		result[i] = query.ConversationMessage{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: msg.Timestamp,
		}
	}
	return result
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

// loadConcurrent processes sources and documents concurrently according to the
// given configuration. It shares the majority of logic with the sequential
// loader but uses errgroup and semaphores to control parallelism, and a
// dedicated aggregator goroutine to avoid lock-based sharing.
func (dk *BuiltinKnowledge) loadConcurrent(ctx context.Context, config *loadConfig) error {
	startTime := time.Now()

	aggr := loader.NewAggregator(defaultSizeBuckets, config.showStats,
		config.showProgress, config.progressStepSize)
	defer aggr.Close()

	totalSources := len(dk.sources)
	log.Infof("Starting knowledge base loading with %d sources", totalSources)

	srcCap := max(1, config.srcParallelism)
	semSrc := make(chan struct{}, srcCap)

	var wgSrc sync.WaitGroup
	errCh := make(chan error, 1)

	for idx, src := range dk.sources {
		semSrc <- struct{}{}
		wgSrc.Add(1)

		submitErr := ants.Submit(func(idx int, src source.Source) func() {
			return func() {
				defer func() {
					<-semSrc
					wgSrc.Done()
				}()

				if err := dk.processSource(ctx, src, idx, totalSources, config, aggr); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		}(idx, src))

		if submitErr != nil {
			wgSrc.Done()
			<-semSrc
			return fmt.Errorf("submit source task: %w", submitErr)
		}
	}

	wgSrc.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	log.Infof("Knowledge base loading completed in %s (%d sources)",
		time.Since(startTime), totalSources)
	return nil
}

// processSource handles reading documents from one source and feeding them to
// the document-level workers.
func (dk *BuiltinKnowledge) processSource(
	ctx context.Context,
	src source.Source,
	idx, total int,
	cfg *loadConfig,
	aggr *loader.Aggregator,
) error {
	sourceName := src.Name()
	log.Infof("Loading source %d/%d: %s (type: %s)",
		idx+1, total, sourceName, src.Type())

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		return fmt.Errorf("failed to read documents from source %s: %w", sourceName, err)
	}

	log.Infof("Fetched %d document(s) from source %s", len(docs), sourceName)

	if err := dk.processDocuments(ctx, sourceName, docs, cfg, aggr); err != nil {
		return err
	}

	log.Infof("Successfully loaded source %s", sourceName)
	return nil
}

// processDocuments embeds and stores all documents from a single source using
// document-level parallelism.
func (dk *BuiltinKnowledge) processDocuments(
	ctx context.Context,
	sourceName string,
	docs []*document.Document,
	cfg *loadConfig,
	aggr *loader.Aggregator,
) error {
	docCap := max(1, cfg.docParallelism)
	semDoc := make(chan struct{}, docCap)
	var wgDoc sync.WaitGroup
	errCh := make(chan error, 1)

	for i, doc := range docs {
		semDoc <- struct{}{}
		wgDoc.Add(1)

		if submitErr := ants.Submit(func(idx int, d *document.Document) func() {
			return func() {
				defer func() {
					<-semDoc
					wgDoc.Done()
				}()

				if err := dk.addDocument(ctx, d); err != nil {
					select {
					case errCh <- fmt.Errorf("add document: %w", err):
					default:
					}
					return
				}

				aggr.StatCh() <- loader.StatEvent{Size: len(d.Content)}
				if cfg.showProgress {
					aggr.ProgCh() <- loader.ProgEvent{
						SrcName:      sourceName,
						SrcProcessed: idx + 1,
						SrcTotal:     len(docs),
					}
				}
			}
		}(i, doc)); submitErr != nil {
			wgDoc.Done()
			<-semDoc
			select {
			case errCh <- fmt.Errorf("submit doc task: %w", submitErr):
			default:
			}
		}
	}

	wgDoc.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}
