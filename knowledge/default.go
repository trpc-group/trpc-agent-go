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

	// incremental sync related fields
	cacheURIInfo    map[string][]DocumentInfo // cached document info grouped by URI, generated from vectorMetadata
	cacheSourceInfo map[string][]DocumentInfo // cached document info grouped by source name, generated from vectorMetadata

	cacheMetaInfo    map[string]vectorstore.DocumentMetadata // cached vectorstore metadata
	processedDocIDs  sync.Map                                // processed doc IDs, used to avoid duplicate processing
	enableSourceSync bool                                    // enable source sync, if true, will keep document in vectorstore be synced with source
}

// DocumentInfo stores the basic information of a document for incremental sync
type DocumentInfo struct {
	DocumentID string
	SourceName string
	ChunkIndex int
	URI        string
	AllMeta    map[string]interface{}
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

// WithEnableSourceSync sets the enable source sync.
func WithEnableSourceSync(enable bool) Option {
	return func(dk *BuiltinKnowledge) {
		dk.enableSourceSync = enable
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

// AddSource adds a source to the knowledge base.
func (dk *BuiltinKnowledge) AddSource(source source.Source) error {
	dk.sources = append(dk.sources, source)
	return nil
}

// ReloadSource reloads the source to the knowledge base.
func (dk *BuiltinKnowledge) ReloadSource(ctx context.Context, sourceName string, opts ...LoadOption) error {
	// Find the source by name
	var targetSource source.Source
	for _, src := range dk.sources {
		if src.Name() == sourceName {
			targetSource = src
			break
		}
	}

	if targetSource == nil {
		return fmt.Errorf("source with name %s not found", sourceName)
	}

	// Create filter for source documents
	filter := map[string]interface{}{
		source.MetaSourceName: sourceName,
	}

	if dk.enableSourceSync {
		// Case 1: sourceSync enabled - use incremental sync logic
		log.Infof("Reloading source %s with incremental sync enabled", sourceName)

		// Find related document IDs from cacheSourceInfo
		if sourceDocs, exists := dk.cacheSourceInfo[sourceName]; exists {
			// Remove related documents from processedDocIDs to mark them for reprocessing
			for _, docInfo := range sourceDocs {
				dk.processedDocIDs.Delete(docInfo.DocumentID)
				log.Debugf("Marked document %s for reprocessing", docInfo.DocumentID)
			}
		}

		// Load the source (will use incremental sync logic)
		config := dk.buildLoadConfig(1, opts...)
		if config.srcParallelism > 1 || config.docParallelism > 1 {
			if _, err := dk.loadConcurrent(ctx, config, []source.Source{targetSource}); err != nil {
				return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
			}
		} else {
			if _, err := dk.loadSequential(ctx, config, []source.Source{targetSource}); err != nil {
				return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
			}
		}

		// Cleanup orphan documents after reload
		if err := dk.cleanupOrphanDocuments(ctx); err != nil {
			log.Warnf("Failed to cleanup orphan documents after reloading source %s: %v", sourceName, err)
		}

	} else {
		// Case 2: sourceSync disabled - direct delete and reload
		log.Infof("Reloading source %s with direct delete and reload", sourceName)

		// Delete existing documents for this source from vector store
		if err := dk.vectorStore.DeleteByFilter(ctx, nil, filter, false); err != nil {
			return fmt.Errorf("failed to delete existing documents for source %s: %w", sourceName, err)
		}

		// Load the source
		config := dk.buildLoadConfig(1, opts...)
		if config.srcParallelism > 1 || config.docParallelism > 1 {
			if _, err := dk.loadConcurrent(ctx, config, []source.Source{targetSource}); err != nil {
				return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
			}
		} else {
			if _, err := dk.loadSequential(ctx, config, []source.Source{targetSource}); err != nil {
				return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
			}
		}
	}

	log.Infof("Successfully reloaded source %s", sourceName)
	return nil
}

// RemoveSourceByName removes a source from the knowledge base by name.
func (dk *BuiltinKnowledge) RemoveSourceByName(ctx context.Context, sourceName string) error {
	// Find the source by name
	var targetSource source.Source
	var sourceIndex int = -1

	for i, src := range dk.sources {
		if src.Name() == sourceName {
			targetSource = src
			sourceIndex = i
			break
		}
	}

	if targetSource == nil {
		return fmt.Errorf("source with name %s not found", sourceName)
	}

	// Remove source from sources list
	dk.sources = append(dk.sources[:sourceIndex], dk.sources[sourceIndex+1:]...)

	// Create filter for source documents
	filter := map[string]interface{}{
		source.MetaSourceName: sourceName,
	}

	// Remove from caches if sourceSync is enabled (do this BEFORE deleting from vector store)
	if dk.enableSourceSync {
		// Get metadata for documents to be removed
		metas, err := dk.vectorStore.GetMetadata(ctx, nil, filter, -1, -1)
		if err != nil {
			// If GetMetadata fails, we still continue with cache cleanup
			log.Warnf("Failed to get metadata for source %s during removal: %v", sourceName, err)
		} else {
			// Remove from cacheURIInfo
			for _, meta := range metas {
				if uri, ok := meta.Metadata[source.MetaURI].(string); ok {
					delete(dk.cacheURIInfo, uri)
				}
			}

			// Remove from cacheSourceInfo
			delete(dk.cacheSourceInfo, sourceName)

			// Remove from cacheMetaInfo and processedDocIDs
			for docID := range metas {
				delete(dk.cacheMetaInfo, docID)
				dk.processedDocIDs.Delete(docID)
			}
		}
	}

	// Delete documents from vector store by source name filter
	if err := dk.vectorStore.DeleteByFilter(ctx, nil, filter, false); err != nil {
		return fmt.Errorf("failed to delete documents from vector store: %w", err)
	}

	return nil
}

// loadSource loads one or more source
func (dk *BuiltinKnowledge) loadSource(
	ctx context.Context,
	sources []source.Source,
	opts ...LoadOption,
) error {
	if dk.vectorStore == nil {
		return fmt.Errorf("vector store not configured")
	}
	config := dk.buildLoadConfig(len(sources), opts...)

	if err := dk.sourcesValidate(); err != nil {
		return fmt.Errorf("sources validate failed, ")
	}
	if config.recreate {
		// clear vector store data
		count, err := dk.vectorStore.Count(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to count documents in vector store: %w", err)
		}
		log.Infof("Recreating vector store, deleting %d documents", count)
		if err := dk.vectorStore.DeleteByFilter(ctx, nil, nil, true); err != nil {
			return fmt.Errorf("failed to flush vector store: %w", err)
		}
		// clear vector store metadata
		dk.clearVectorStoreMetadata()

		if config.srcParallelism > 1 || config.docParallelism > 1 {
			if _, err := dk.loadConcurrent(ctx, config, sources); err != nil {
				return err
			}
		} else {
			if _, err := dk.loadSequential(ctx, config, sources); err != nil {
				return err
			}
		}
		return nil
	}

	// if source sync is enabled, update vector store metadata
	if dk.enableSourceSync {
		if err := dk.updateVectorStoreMetadata(ctx); err != nil {
			return fmt.Errorf("failed to prepare incremental sync: %w", err)
		}
	}

	if config.srcParallelism > 1 || config.docParallelism > 1 {
		if _, err := dk.loadConcurrent(ctx, config, sources); err != nil {
			return err
		}
	} else {
		if _, err := dk.loadSequential(ctx, config, sources); err != nil {
			return err
		}
	}

	// if source sync is enabled, cleanup orphan documents and update vector store metadata after data is loaded
	if dk.enableSourceSync {
		if err := dk.cleanupOrphanDocuments(ctx); err != nil {
			return fmt.Errorf("failed to cleanup orphan documents: %w", err)
		}
		if err := dk.updateVectorStoreMetadata(ctx); err != nil {
			return fmt.Errorf("failed to update vector store metadata: %w", err)
		}
	}
	return nil
}

func (dk *BuiltinKnowledge) sourcesValidate() error {
	sourceMap := make(map[string]struct{})
	for _, src := range dk.sources {
		if _, ok := sourceMap[src.Name()]; ok {
			return fmt.Errorf("source name %s is duplicate", src.Name())
		}
		sourceMap[src.Name()] = struct{}{}
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
			if err := dk.addDocumentWithSync(ctx, doc, src); err != nil {
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
			if err := dk.processDocuments(ctx, docs, config, aggr, docPool, source); err != nil {
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
	docs []*document.Document,
	cfg *loadConfig,
	aggr *loader.Aggregator,
	pool *ants.Pool,
	src source.Source,
) error {
	var wgDoc sync.WaitGroup
	errCh := make(chan error, len(docs))

	processDoc := func(doc *document.Document, docIndex int) func() {
		return func() {
			defer wgDoc.Done()
			if err := dk.addDocumentWithSync(ctx, doc, src); err != nil {
				errCh <- fmt.Errorf("add document: %w", err)
				return
			}

			aggr.StatCh() <- loader.StatEvent{Size: len(doc.Content)}
			if cfg.showProgress {
				aggr.ProgCh() <- loader.ProgEvent{
					SrcName:      src.Name(),
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

// addDocumentWithSync adds a document to the knowledge base with incremental sync support.
func (dk *BuiltinKnowledge) addDocumentWithSync(
	ctx context.Context,
	doc *document.Document,
	src source.Source,
) error {
	if !dk.enableSourceSync {
		return dk.addDocument(ctx, doc)
	}

	// check document metadata
	if err := dk.resetDocumentID(doc, src); err != nil {
		return fmt.Errorf("failed to check document metadata: %w", err)
	}

	// check if document should be processed
	shouldProcess, err := dk.shouldProcessDocument(doc)
	if err != nil {
		return fmt.Errorf("failed to check if document should be processed: %w", err)
	}

	// if document should not be processed, skip
	if !shouldProcess {
		return nil
	}

	// add document
	return dk.addDocument(ctx, doc)
}

// enrichDocumentMetadata adds incremental sync required metadata to the document
func (dk *BuiltinKnowledge) resetDocumentID(doc *document.Document, src source.Source) error {
	if doc.Metadata == nil {
		return fmt.Errorf("document metadata is nil")
	}

	// set source name
	doc.Metadata[source.MetaSourceName] = src.Name()

	_, ok := doc.Metadata[source.MetaURI]
	if !ok {
		return fmt.Errorf("document missing URI metadata")
	}

	// set chunk index (if not exists, set to 0)
	chunkIndex, ok := doc.Metadata[source.MetaChunkIndex]
	if !ok {
		return fmt.Errorf("document missing chunk index metadata")
	}

	// generate and set document ID
	chunkIndexInt, ok := chunkIndex.(int)
	if !ok {
		return fmt.Errorf("chunk index is not an integer")
	}

	// generate document ID by source name, content, chunk index and source metadata
	// we cal hash with metadata that user set
	doc.ID = source.GenerateDocumentID(src.Name(), doc.Content, chunkIndexInt, src.GetMetadata())
	return nil
}

// prepareIncrementalSync prepare incremental sync
func (dk *BuiltinKnowledge) updateVectorStoreMetadata(ctx context.Context) error {
	log.Infof("Updating vector store metadata...")

	// initialize incremental sync related fields
	if dk.cacheURIInfo == nil {
		dk.cacheURIInfo = make(map[string][]DocumentInfo)
	}
	if dk.cacheSourceInfo == nil {
		dk.cacheSourceInfo = make(map[string][]DocumentInfo)
	}

	// get all existing documents metadata and cache it
	allMeta, err := dk.vectorStore.GetMetadata(ctx, nil, nil, -1, -1)
	if err != nil {
		return fmt.Errorf("failed to get existing metadata: %w", err)
	}
	// cache metadata
	dk.cacheMetaInfo = allMeta
	log.Infof("Found %d existing documents in vector store", len(allMeta))

	// group existing documents by URI
	for docID, meta := range allMeta {
		uri, ok := meta.Metadata[source.MetaURI].(string)
		if !ok {
			log.Debugf("Document %s missing URI metadata", docID)
			continue
		}
		sourceName, ok := meta.Metadata[source.MetaSourceName].(string)
		if !ok {
			log.Debugf("Document %s missing source name metadata", docID)
			continue
		}

		chunkIndex, _ := meta.Metadata[source.MetaChunkIndex].(int)
		docInfo := DocumentInfo{
			DocumentID: docID,
			SourceName: sourceName,
			ChunkIndex: chunkIndex,
			URI:        uri,
			AllMeta:    meta.Metadata,
		}

		dk.cacheURIInfo[uri] = append(dk.cacheURIInfo[uri], docInfo)
	}

	// group existing documents by source name
	for docID, meta := range allMeta {
		uri, ok := meta.Metadata[source.MetaURI].(string)
		if !ok {
			log.Debugf("Document %s missing URI metadata", docID)
			continue
		}
		sourceName, ok := meta.Metadata[source.MetaSourceName].(string)
		if !ok {
			log.Debugf("Document %s missing source name metadata", docID)
			continue
		}

		chunkIndex, _ := meta.Metadata[source.MetaChunkIndex].(int)
		docInfo := DocumentInfo{
			DocumentID: docID,
			SourceName: sourceName,
			ChunkIndex: chunkIndex,
			URI:        uri,
			AllMeta:    meta.Metadata,
		}

		dk.cacheSourceInfo[sourceName] = append(dk.cacheSourceInfo[sourceName], docInfo)
	}

	return nil
}

// shouldProcessDocument checks if the document should be processed (incremental sync logic)
func (dk *BuiltinKnowledge) shouldProcessDocument(doc *document.Document) (bool, error) {
	docID := doc.ID
	uri, ok := doc.Metadata[source.MetaURI].(string)
	if !ok {
		return false, fmt.Errorf("document missing or invalid URI metadata")
	}

	// check if document has been processed in current sync
	if _, exists := dk.processedDocIDs.Load(docID); exists {
		return false, nil // already processed, skip
	}

	// Mark as processed regardless of the decision to avoid duplicate processing
	dk.processedDocIDs.Store(docID, struct{}{})

	// get existing documents by URI
	existingDocs, exists := dk.cacheURIInfo[uri]
	if !exists {
		// new file, process it
		log.Debugf("New file detected: %s", uri)
		return true, nil
	}

	// check if document ID exists in existing documents
	for _, existingDoc := range existingDocs {
		if existingDoc.DocumentID == docID {
			// document ID exists and unchanged, skip processing
			log.Infof("Document unchanged: %s", docID)
			return false, nil
		}
	}

	// document ID does not exist in existing documents, file has changed
	log.Infof("File changed detected: %s, will update documents", uri)
	return true, nil
}

// cleanupOrphanDocuments cleanup orphan documents
func (dk *BuiltinKnowledge) cleanupOrphanDocuments(ctx context.Context) error {
	log.Infof("Starting orphan document cleanup...")

	toDeleteDocIDs := make([]string, 0)
	// find all documents that are not processed
	for docID := range dk.cacheMetaInfo {
		if _, exists := dk.processedDocIDs.Load(docID); !exists {
			toDeleteDocIDs = append(toDeleteDocIDs, docID)
		}
	}

	if len(toDeleteDocIDs) == 0 {
		log.Infof("No orphan documents to cleanup")
		return nil
	}

	log.Infof("Cleaning up %d orphan/outdated documents", len(toDeleteDocIDs))
	if err := dk.vectorStore.DeleteByFilter(ctx, toDeleteDocIDs, nil, false); err != nil {
		return fmt.Errorf("failed to delete orphan documents: %w", err)
	}

	// Clean up cache info for successfully deleted documents
	for _, docID := range toDeleteDocIDs {
		if meta, exists := dk.cacheMetaInfo[docID]; exists {
			// Remove from cacheURIInfo
			if uri, ok := meta.Metadata[source.MetaURI].(string); ok {
				if uriDocs, exists := dk.cacheURIInfo[uri]; exists {
					// Remove the specific document from URI cache
					filtered := make([]DocumentInfo, 0, len(uriDocs))
					for _, doc := range uriDocs {
						if doc.DocumentID != docID {
							filtered = append(filtered, doc)
						}
					}
					if len(filtered) == 0 {
						delete(dk.cacheURIInfo, uri)
					} else {
						dk.cacheURIInfo[uri] = filtered
					}
				}
			}

			// Remove from cacheSourceInfo
			if sourceName, ok := meta.Metadata[source.MetaSourceName].(string); ok {
				if sourceDocs, exists := dk.cacheSourceInfo[sourceName]; exists {
					// Remove the specific document from source cache
					filtered := make([]DocumentInfo, 0, len(sourceDocs))
					for _, doc := range sourceDocs {
						if doc.DocumentID != docID {
							filtered = append(filtered, doc)
						}
					}
					if len(filtered) == 0 {
						delete(dk.cacheSourceInfo, sourceName)
					} else {
						dk.cacheSourceInfo[sourceName] = filtered
					}
				}
			}

			// Remove from cacheMetaInfo
			delete(dk.cacheMetaInfo, docID)
		}
	}

	log.Infof("Successfully deleted %d documents", len(toDeleteDocIDs))
	return nil
}

// clearVectorStoreMetadata clear vector store metadata cache
func (dk *BuiltinKnowledge) clearVectorStoreMetadata() {
	dk.cacheURIInfo = make(map[string][]DocumentInfo)
	dk.cacheSourceInfo = make(map[string][]DocumentInfo)
	dk.processedDocIDs.Clear()
	dk.cacheMetaInfo = make(map[string]vectorstore.DocumentMetadata)
	log.Infof("Cleared all incremental sync cache")
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
