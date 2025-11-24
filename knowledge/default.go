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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
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
	cacheURIInfo     map[string][]BuiltinDocumentInfo // cached document info grouped by URI, generated from vectorMetadata
	cacheSourceInfo  map[string][]BuiltinDocumentInfo // cached document info grouped by source name, generated from vectorMetadata
	cacheMetaInfo    map[string]BuiltinDocumentInfo   // cached vectorstore metadata
	processedDocIDs  sync.Map                         // processed doc IDs, used to avoid duplicate processing and cleanup orphan documents
	processingDocIDs sync.Map                         // processing doc IDs, used to avoid duplicate processing
	processingIDMu   sync.Mutex                       // mutex for make consistent of read and write processingDocIDs
	enableSourceSync bool                             // enable source sync, if true, will keep document in vectorstore be synced with source
	dataOperationMu  sync.RWMutex                     // mutex for make sequence of data operations
}

// BuiltinDocumentInfo stores the basic information of a document for incremental sync
type BuiltinDocumentInfo struct {
	DocumentID string
	SourceName string
	ChunkIndex int
	URI        string
	AllMeta    map[string]any
}

// convertMetaToDocumentInfo converts a vectorstore document metadata to a DocumentInfo
func convertMetaToDocumentInfo(docID string, meta *vectorstore.DocumentMetadata) BuiltinDocumentInfo {
	uri, hasURI := meta.Metadata[source.MetaURI].(string)
	sourceName, hasSource := meta.Metadata[source.MetaSourceName].(string)
	chunkIndex, hasChunkIndex := meta.Metadata[source.MetaChunkIndex]
	if !hasURI {
		log.Debugf("URI not found in metadata, setting to empty string")
		uri = ""
	}
	if !hasSource {
		log.Debugf("source name not found in metadata, setting to empty string")
		sourceName = ""
	}
	if !hasChunkIndex {
		log.Debugf("chunk index not found in metadata, setting to 0")
		chunkIndex = 0
	}
	chunkIndexInt, ok := convertToInt(chunkIndex)
	if !ok {
		log.Debugf("chunk index is not an integer, setting to 0")
		chunkIndexInt = 0
	}
	docInfo := BuiltinDocumentInfo{
		DocumentID: docID,
		SourceName: sourceName,
		ChunkIndex: chunkIndexInt,
		URI:        uri,
		AllMeta:    meta.Metadata,
	}
	return docInfo
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
			dk.reranker = reranker.NewTopKReranker()
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

// ShowDocumentInfo shows the document info from the vector store.
func (dk *BuiltinKnowledge) ShowDocumentInfo(
	ctx context.Context,
	opts ...ShowDocumentInfoOption,
) ([]BuiltinDocumentInfo, error) {
	dk.dataOperationMu.RLock()
	defer dk.dataOperationMu.RUnlock()

	if dk.vectorStore == nil {
		return nil, fmt.Errorf("vector store not configured")
	}

	config := &showDocumentInfoConfig{}
	for _, opt := range opts {
		opt(config)
	}
	filter := map[string]any{}
	if config.filter != nil {
		filter = config.filter
	}
	if config.sourceName != "" {
		filter[source.MetaSourceName] = config.sourceName
	}
	allMetadata, err := dk.vectorStore.GetMetadata(
		ctx,
		vectorstore.WithGetMetadataFilter(filter),
		vectorstore.WithGetMetadataIDs(config.ids),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	docInfos := make([]BuiltinDocumentInfo, 0, len(allMetadata))
	for docID := range allMetadata {
		meta := allMetadata[docID]
		docInfo := convertMetaToDocumentInfo(docID, &meta)
		docInfos = append(docInfos, docInfo)
	}
	return docInfos, nil
}

// AddSource adds a source to the knowledge base.
func (dk *BuiltinKnowledge) AddSource(ctx context.Context, src source.Source, opts ...LoadOption) error {
	dk.dataOperationMu.Lock()
	defer dk.dataOperationMu.Unlock()

	if src == nil {
		return fmt.Errorf("source cannot be nil")
	}

	// check if source already exists
	for _, dkSrc := range dk.sources {
		if src.Name() == dkSrc.Name() {
			return fmt.Errorf("source with name %s already exists", src.Name())
		}
	}
	if dk.enableSourceSync {
		if err := dk.refreshSourceDocInfo(ctx, src.Name()); err != nil {
			return fmt.Errorf("failed to update vector store metadata: %w", err)
		}
	}
	config := dk.buildLoadConfig(1, opts...)
	if err := dk.loadSourceInternal(ctx, []source.Source{src}, config); err != nil {
		return fmt.Errorf("failed to load source: %w", err)
	}
	if dk.enableSourceSync {
		if err := dk.refreshSourceDocInfo(ctx, src.Name()); err != nil {
			return fmt.Errorf("failed to update vector store metadata: %w", err)
		}
	}
	dk.sources = append(dk.sources, src)
	return nil
}

// ReloadSource reloads the source to the knowledge base.
func (dk *BuiltinKnowledge) ReloadSource(ctx context.Context, src source.Source, opts ...LoadOption) error {
	dk.dataOperationMu.Lock()
	defer dk.dataOperationMu.Unlock()

	if src == nil {
		return fmt.Errorf("source cannot be nil")
	}

	// Find the source by name
	sourceName := src.Name()
	var oldSource source.Source

	// find and remove old source from sources list
	for i, existingSource := range dk.sources {
		if existingSource.Name() == sourceName {
			oldSource = existingSource
			dk.sources = append(dk.sources[:i], dk.sources[i+1:]...)
			break
		}
	}
	if oldSource == nil {
		return fmt.Errorf("source with name %s not found", sourceName)
	}

	config := dk.buildLoadConfig(1, opts...)
	if dk.enableSourceSync {
		err := dk.syncReloadSource(ctx, src, sourceName, config)
		if err != nil {
			return err
		}
	} else {
		err := dk.reloadSource(ctx, src, sourceName, config)
		if err != nil {
			return err
		}
	}

	// Add the new source to the sources list
	dk.sources = append(dk.sources, src)
	return nil
}

// syncReloadSource reloads a source using incremental sync strategy
func (dk *BuiltinKnowledge) syncReloadSource(
	ctx context.Context,
	oldSource source.Source,
	sourceName string,
	config *loadConfig,
) error {
	log.Infof("Reloading source %s with incremental sync", sourceName)

	// refresh DocumentInfo by source name
	if err := dk.refreshSourceDocInfo(ctx, sourceName); err != nil {
		return fmt.Errorf("failed to update vector store metadata: %w", err)
	}
	// mark source unprocessed
	dk.markSourceUnprocessed(sourceName)

	// Load source with incremental sync
	if err := dk.loadSourceInternal(ctx, []source.Source{oldSource}, config); err != nil {
		return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
	}

	// Cleanup orphan documents
	if err := dk.cleanupOrphanDocuments(ctx); err != nil {
		log.Warnf("Failed to cleanup orphan documents after reloading source %s: %v", sourceName, err)
	}

	// refresh DocumentInfo to load latest document info
	if err := dk.refreshSourceDocInfo(ctx, sourceName); err != nil {
		return fmt.Errorf("failed to update vector store metadata: %w", err)
	}

	log.Infof("Successfully reloaded source %s with sync", sourceName)
	return nil
}

// reloadSource reloads a source using direct delete and reload strategy
func (dk *BuiltinKnowledge) reloadSource(
	ctx context.Context,
	targetSource source.Source,
	sourceName string,
	config *loadConfig,
) error {
	log.Infof("Reloading source %s with direct delete and add", sourceName)

	filter := map[string]any{
		source.MetaSourceName: sourceName,
	}
	// Delete existing documents
	if err := dk.vectorStore.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(filter)); err != nil {
		return fmt.Errorf("failed to delete existing documents for source %s: %w", sourceName, err)
	}

	// Load source
	if err := dk.loadSourceInternal(ctx, []source.Source{targetSource}, config); err != nil {
		return fmt.Errorf("failed to reload source %s: %w", sourceName, err)
	}

	log.Infof("Successfully reloaded source %s directly", sourceName)
	return nil
}

// loadSourceInternal loads sources with proper concurrency handling
func (dk *BuiltinKnowledge) loadSourceInternal(ctx context.Context, sources []source.Source, config *loadConfig) error {
	dk.processingDocIDs = sync.Map{}
	defer func() {
		// reset processingDocIDs after loading sources
		dk.processingDocIDs = sync.Map{}
	}()

	if config.srcParallelism > 1 || config.docParallelism > 1 {
		_, err := dk.loadConcurrent(ctx, config, sources)
		return err
	}
	_, err := dk.loadSequential(ctx, config, sources)
	return err
}

// RemoveSource removes a source from the knowledge base by name.
func (dk *BuiltinKnowledge) RemoveSource(ctx context.Context, sourceName string) error {
	dk.dataOperationMu.Lock()
	defer dk.dataOperationMu.Unlock()

	// Find and remove source from sources list
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

	// Create filter for source documents
	filter := map[string]any{
		source.MetaSourceName: sourceName,
	}

	// Delete documents from vector store by source name filter
	if err := dk.vectorStore.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(filter)); err != nil {
		return fmt.Errorf("failed to delete documents from vector store: %w", err)
	}

	if dk.enableSourceSync {
		dk.markSourceUnprocessed(sourceName)
		if err := dk.refreshSourceDocInfo(ctx, sourceName); err != nil {
			return fmt.Errorf("failed to update vector store metadata: %w", err)
		}
	}
	dk.sources = append(dk.sources[:sourceIndex], dk.sources[sourceIndex+1:]...)
	return nil
}

// Load loads one or more source
func (dk *BuiltinKnowledge) Load(ctx context.Context, opts ...LoadOption) error {
	dk.dataOperationMu.Lock()
	defer dk.dataOperationMu.Unlock()

	if dk.vectorStore == nil {
		return fmt.Errorf("vector store not configured")
	}
	config := dk.buildLoadConfig(len(dk.sources), opts...)

	if config.recreate {
		if err := dk.loadWithRecreate(ctx, config); err != nil {
			return fmt.Errorf("failed to load with recreate: %w", err)
		}
		return nil
	}

	if err := dk.load(ctx, config); err != nil {
		return fmt.Errorf("failed to load with sync: %w", err)
	}
	return nil
}

func (dk *BuiltinKnowledge) loadWithRecreate(ctx context.Context, config *loadConfig) error {
	// clear vector store data
	count, err := dk.vectorStore.Count(ctx)
	if err != nil {
		return fmt.Errorf("failed to count documents in vector store: %w", err)
	}

	log.Infof("Recreating vector store, deleting %d documents", count)
	if err := dk.vectorStore.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		return fmt.Errorf("failed to flush vector store: %w", err)
	}

	if dk.enableSourceSync {
		dk.clearVectorStoreMetadata()
	}

	if err := dk.loadSourceInternal(ctx, dk.sources, config); err != nil {
		return fmt.Errorf("failed to load source: %w", err)
	}

	if dk.enableSourceSync {
		if err := dk.refreshAllDocInfo(ctx); err != nil {
			return fmt.Errorf("failed to prepare incremental sync: %w", err)
		}
	}
	return nil
}

func (dk *BuiltinKnowledge) load(ctx context.Context, config *loadConfig) error {
	if dk.enableSourceSync {
		if err := dk.refreshAllDocInfo(ctx); err != nil {
			return fmt.Errorf("failed to prepare incremental sync: %w", err)
		}
	}

	if err := dk.loadSourceInternal(ctx, dk.sources, config); err != nil {
		return fmt.Errorf("failed to load source: %w", err)
	}

	if dk.enableSourceSync {
		if err := dk.cleanupOrphanDocuments(ctx); err != nil {
			return fmt.Errorf("failed to cleanup orphan documents: %w", err)
		}
		if err := dk.refreshAllDocInfo(ctx); err != nil {
			return fmt.Errorf("failed to update vector store metadata: %w", err)
		}
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

	// Check for any errors
	for err := range errCh {
		if err != nil {
			return nil, err
		}
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

	// Check for any errors
	for err := range errCh {
		if err != nil {
			return err
		}
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
	defer func() {
		dk.processingDocIDs.Delete(doc.ID)
	}()
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
	if err := dk.addDocument(ctx, doc); err != nil {
		return fmt.Errorf("failed to add document: %w", err)
	}
	dk.processedDocIDs.Store(doc.ID, struct{}{})
	return nil
}

// addDocument adds a document to the knowledge base (internal method).
func (dk *BuiltinKnowledge) addDocument(ctx context.Context, doc *document.Document) error {
	if dk.vectorStore == nil {
		return fmt.Errorf("vector store is not configured")
	}

	var embedding []float64
	if dk.embedder != nil {
		var err error
		embedding, err = dk.embedder.GetEmbedding(ctx, doc.Content)
		if err != nil {
			return fmt.Errorf("failed to generate embedding: %w", err)
		}
	} else {
		// When embedder is not set, pass empty slice for remote embedding
		embedding = []float64{}
	}

	if err := dk.vectorStore.Add(ctx, doc, embedding); err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}
	return nil
}

func (dk *BuiltinKnowledge) resetDocumentID(doc *document.Document, src source.Source) error {
	if doc.Metadata == nil {
		return fmt.Errorf("document metadata is nil")
	}

	doc.Metadata[source.MetaSourceName] = src.Name()

	_, ok := doc.Metadata[source.MetaURI]
	if !ok {
		return fmt.Errorf("document missing URI metadata")
	}

	chunkIndex, ok := doc.Metadata[source.MetaChunkIndex]
	if !ok {
		return fmt.Errorf("document missing chunk index metadata")
	}

	chunkIndexInt, ok := convertToInt(chunkIndex)
	if !ok {
		return fmt.Errorf("chunk index is not an integer")
	}

	uri, ok := doc.Metadata[source.MetaURI].(string)
	if !ok {
		return fmt.Errorf("document missing URI metadata")
	}

	// generate document ID by source name, uri, content, chunk index and source metadata
	// we cal hash with metadata that user set
	doc.ID = generateDocumentID(src.Name(), uri, doc.Content, chunkIndexInt, src.GetMetadata())
	return nil
}

// refreshSourceDocInfo refreshes metadata by source name
func (dk *BuiltinKnowledge) refreshSourceDocInfo(ctx context.Context, sourceName string) error {
	if dk.cacheMetaInfo == nil {
		dk.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
	}
	if dk.cacheSourceInfo == nil {
		dk.cacheSourceInfo = make(map[string][]BuiltinDocumentInfo)
	}
	if dk.cacheURIInfo == nil {
		dk.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
	}

	// get latest metadata by source name
	filter := map[string]any{
		source.MetaSourceName: sourceName,
	}
	metas, err := dk.vectorStore.GetMetadata(ctx, vectorstore.WithGetMetadataFilter(filter))
	if err != nil {
		return fmt.Errorf("failed to get metadata for source %s: %w", sourceName, err)
	}

	// remove old metadata with target source name
	toDeleteDocIDs := make([]string, 0)
	for docID, meta := range dk.cacheMetaInfo {
		if meta.SourceName == sourceName {
			toDeleteDocIDs = append(toDeleteDocIDs, docID)
		}
	}
	for _, docID := range toDeleteDocIDs {
		delete(dk.cacheMetaInfo, docID)
	}

	// add new metadata
	for docID := range metas {
		meta := metas[docID]
		dk.cacheMetaInfo[docID] = convertMetaToDocumentInfo(docID, &meta)
	}

	dk.rebuildDocumentInfo()
	return nil
}

// prepareIncrementalSync prepare incremental sync
func (dk *BuiltinKnowledge) refreshAllDocInfo(ctx context.Context) error {
	dk.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
	// get all existing documents metadata and cache it
	allMeta, err := dk.vectorStore.GetMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to get existing metadata: %w", err)
	}

	// cache metadata
	for docID := range allMeta {
		meta := allMeta[docID]
		docInfo := convertMetaToDocumentInfo(docID, &meta)
		dk.cacheMetaInfo[docID] = docInfo
	}
	dk.rebuildDocumentInfo()
	log.Infof("Found %d existing documents in vector store", len(allMeta))
	return nil
}

func (dk *BuiltinKnowledge) rebuildDocumentInfo() {
	if dk.cacheMetaInfo == nil {
		return
	}
	dk.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
	dk.cacheSourceInfo = make(map[string][]BuiltinDocumentInfo)
	for _, meta := range dk.cacheMetaInfo {
		dk.cacheURIInfo[meta.URI] = append(dk.cacheURIInfo[meta.URI], meta)
		dk.cacheSourceInfo[meta.SourceName] = append(dk.cacheSourceInfo[meta.SourceName], meta)
	}
}

func (dk *BuiltinKnowledge) markSourceUnprocessed(sourceName string) {
	for docID, meta := range dk.cacheMetaInfo {
		if meta.SourceName == sourceName {
			dk.processedDocIDs.Delete(docID)
		}
	}
}

// shouldProcessDocument checks if the document should be processed (incremental sync logic)
func (dk *BuiltinKnowledge) shouldProcessDocument(doc *document.Document) (bool, error) {
	dk.processingIDMu.Lock()
	defer dk.processingIDMu.Unlock()

	docID := doc.ID

	uri, ok := doc.Metadata[source.MetaURI].(string)
	if !ok {
		return false, fmt.Errorf("document missing or invalid URI metadata")
	}

	sourceName, ok := doc.Metadata[source.MetaSourceName].(string)
	if !ok {
		return false, fmt.Errorf("document missing or invalid source name metadata")
	}
	// check if document has been processed in current sync
	if _, exists := dk.processedDocIDs.Load(docID); exists {
		return false, nil // already processed, skip
	}
	// check if document has been processing in current sync
	if _, exists := dk.processingDocIDs.Load(docID); exists {
		return false, nil // processing, skip
	}

	// get existing documents by URI
	existingDocs, exists := dk.cacheURIInfo[uri]
	if !exists {
		// new file, process it, mark as processing
		log.Debugf("New file detected: %s:%s", sourceName, uri)
		dk.processingDocIDs.Store(docID, struct{}{})
		return true, nil
	}

	// check if document ID exists in existing documents
	for _, existingDoc := range existingDocs {
		if existingDoc.DocumentID == docID {
			// document ID exists and unchanged, skip processing, mark as processed
			log.Debugf("Document unchanged: %s", docID)
			dk.processedDocIDs.Store(docID, struct{}{})
			return false, nil
		}
	}

	// document ID does not exist in existing documents, file has changed, mark as processing
	log.Debugf("File changed detected: %s, will update documents", uri)
	dk.processingDocIDs.Store(docID, struct{}{})
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
	if err := dk.vectorStore.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs(toDeleteDocIDs)); err != nil {
		return fmt.Errorf("failed to delete orphan documents: %w", err)
	}

	log.Infof("Successfully deleted %d documents", len(toDeleteDocIDs))
	return nil
}

// clearVectorStoreMetadata clear vector store metadata cache
func (dk *BuiltinKnowledge) clearVectorStoreMetadata() {
	dk.cacheURIInfo = make(map[string][]BuiltinDocumentInfo)
	dk.cacheSourceInfo = make(map[string][]BuiltinDocumentInfo)
	dk.cacheMetaInfo = make(map[string]BuiltinDocumentInfo)
	dk.processedDocIDs = sync.Map{}
	dk.processingDocIDs = sync.Map{}
	log.Infof("Cleared all incremental sync cache")
}

// Search implements the Knowledge interface.
// It uses the built-in retriever for the complete RAG pipeline with context awareness.
func (dk *BuiltinKnowledge) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	// search don't need to lock, it will not modify or read incremental sync cache
	if req == nil {
		return nil, fmt.Errorf("search request cannot be nil")
	}
	if dk.retriever == nil {
		return nil, fmt.Errorf("retriever not configured")
	}

	minScore := req.MinScore
	if minScore < 0 {
		minScore = 0.0
	}

	// Use built-in retriever for RAG pipeline with full context.
	// The retriever will handle query enhancement if configured.
	retrieverReq := &retriever.Query{
		Text:       req.Query,
		History:    req.History, // Same type now, no conversion needed
		UserID:     req.UserID,
		SessionID:  req.SessionID,
		Filter:     convertQueryFilter(req.SearchFilter),
		Limit:      req.MaxResults,
		MinScore:   minScore,
		SearchMode: req.SearchMode,
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
	documents := make([]*Result, 0, len(result.Documents))
	for _, doc := range result.Documents {
		documents = append(documents, &Result{
			Document: doc.Document,
			Score:    doc.Score,
		})
	}

	return &SearchResult{
		Document:  bestDoc.Document,
		Score:     bestDoc.Score,
		Text:      content,
		Documents: documents,
	}, nil
}

// Close closes the knowledge base and releases resources.
func (dk *BuiltinKnowledge) Close() error {
	dk.dataOperationMu.Lock()
	defer dk.dataOperationMu.Unlock()

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
		DocumentIDs:     qf.DocumentIDs,
		Metadata:        qf.Metadata,
		FilterCondition: qf.FilterCondition,
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

// convertToInt converts any to int, handling JSON unmarshaling type conversion
func convertToInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case string:
		s, err := strconv.Atoi(v)
		if err != nil {
			log.Infof("convertToInt string to int error: %v", err)
			return 0, false
		}
		return s, true
	case json.Number:
		s, err := v.Int64()
		if err != nil {
			log.Infof("convertToInt json.Number to int error: %v", err)
			return 0, false
		}
		return int(s), true
	default:
		str := fmt.Sprintf("%v", v)
		s, err := strconv.Atoi(str)
		if err != nil {
			log.Infof("convertToInt default to int error: %v", err)
			return 0, false
		}
		return s, true
	}
}

// generateDocumentID generates a unique document ID based on source name, URI, content, chunk index and source metadata.
// Uses SHA256 hash to ensure uniqueness and avoid collisions.
func generateDocumentID(sourceName, uri, content string, chunkIndex int, sourceMetadata map[string]any) string {
	hasher := sha256.New()

	// Write source name
	hasher.Write([]byte(sourceName))
	hasher.Write([]byte(":"))

	// Write URI
	hasher.Write([]byte(uri))
	hasher.Write([]byte(":"))

	// Write content
	hasher.Write([]byte(content))
	hasher.Write([]byte(":"))

	// Write chunk index
	hasher.Write([]byte(fmt.Sprintf("%d", chunkIndex)))
	hasher.Write([]byte(":"))

	// Write source metadata (use deterministic serialization)
	if sourceMetadata != nil {
		serializedMetadata := serializeMetadata(sourceMetadata)
		hasher.Write([]byte(serializedMetadata))
		hasher.Write([]byte(":"))
	}

	return fmt.Sprintf("%x", hasher.Sum(nil))
}

// serializeMetadata recursively serializes a value in a deterministic way
func serializeMetadata(value any) string {
	var builder strings.Builder
	serializeMetadataToBuilder(value, &builder)
	return builder.String()
}

// serializeMetadataToBuilder recursively serializes a value to a strings.Builder
func serializeMetadataToBuilder(value any, builder *strings.Builder) {
	switch v := value.(type) {
	case map[string]any:
		// Handle nested map - sort keys and recursively serialize
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		builder.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(k)
			builder.WriteByte(':')
			serializeMetadataToBuilder(v[k], builder)
		}
		builder.WriteByte('}')
	case []any:
		// Handle slice - serialize each element in order
		builder.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				builder.WriteByte(',')
			}
			serializeMetadataToBuilder(item, builder)
		}
		builder.WriteByte(']')
	case map[any]any:
		// Handle map with any keys - convert to string keys first
		stringMap := make(map[string]any, len(v))
		for k, val := range v {
			stringMap[fmt.Sprintf("%v", k)] = val
		}
		serializeMetadataToBuilder(stringMap, builder)
	default:
		// Handle primitive types - use json.Marshal to avoid pointer issues
		if data, err := json.Marshal(v); err == nil {
			builder.Write(data)
		} else {
			// Fallback to fmt.Sprintf if json.Marshal fails
			builder.WriteString(fmt.Sprintf("%v", v))
		}
	}
}
