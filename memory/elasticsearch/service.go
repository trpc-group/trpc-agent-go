//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides an Elasticsearch-based memory service.
// It supports vector similarity search, hybrid search and soft deletion.
package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	istorage "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// Service is the Elasticsearch memory service.
//
// Storage structure. Index: configurable (default trpc_agent_memories).
// Documents hold the canonical memory ID, the mandatory app/user scope,
// the memory content and metadata, the embedding vector and the
// created/updated/deleted timestamps. The document ID is the canonical
// memory ID, which already hashes the app name and user ID, so equal
// memory content in different scopes never collides.
type Service struct {
	opts           ServiceOpts
	client         istorage.Client
	indexName      string
	mappingVersion storage.ESVersion

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new Elasticsearch memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	// Validate embedder is provided.
	if opts.embedder == nil {
		return nil, fmt.Errorf("embedder is required for elasticsearch memory service")
	}

	// Apply auto mode defaults after all options are applied.
	// User settings via WithToolEnabled take precedence regardless of option
	// order.
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	client := opts.client
	if client == nil {
		// Map the zero value to the storage package's unspecified version
		// so the client default selection applies when no version is set.
		version := opts.version
		if version == "" {
			version = storage.ESVersionUnspecified
		}
		builderOpts := []storage.ClientBuilderOpt{
			storage.WithAddresses(opts.addresses),
			storage.WithUsername(opts.username),
			storage.WithPassword(opts.password),
			storage.WithAPIKey(opts.apiKey),
			storage.WithCertificateFingerprint(opts.certificateFingerprint),
			storage.WithCompressRequestBody(opts.compressRequestBody),
			storage.WithEnableMetrics(opts.enableMetrics),
			storage.WithEnableDebugLogger(opts.enableDebugLogger),
			storage.WithRetryOnStatus(opts.retryOnStatus),
			storage.WithMaxRetries(opts.maxRetries),
			storage.WithVersion(version),
		}
		if len(opts.extraOptions) > 0 {
			builderOpts = append(builderOpts, storage.WithExtraOptions(opts.extraOptions...))
		}
		sdkClient, err := storage.GetClientBuilder()(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create elasticsearch client failed: %w", err)
		}
		wrapped, err := storage.WrapSDKClient(sdkClient)
		if err != nil {
			return nil, fmt.Errorf("wrap elasticsearch client failed: %w", err)
		}
		client = wrapped
	}

	s := &Service{
		opts:           opts,
		client:         client,
		indexName:      opts.indexName,
		mappingVersion: effectiveMappingVersion(opts.version),
		cachedTools:    make(map[string]tool.Tool),
	}

	// Initialize the index unless skipped.
	if !opts.skipIndexInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultIndexInitTimeout)
		defer cancel()
		if err := s.ensureIndex(ctx); err != nil {
			return nil, fmt.Errorf("init index failed: %w", err)
		}
	}

	// Pre-compute tools list to avoid lock contention in Tools() method.
	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)

	// Initialize auto memory worker if extractor is configured.
	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor, opts.enabledTools,
		)
		config := imemory.AutoMemoryConfig{
			Extractor:                opts.extractor,
			AsyncMemoryNum:           opts.asyncMemoryNum,
			MemoryQueueSize:          opts.memoryQueueSize,
			MemoryJobTimeout:         opts.memoryJobTimeout,
			DisableOnExternalContext: opts.disableAutoMemoryOnExternalContext,
			EnabledTools:             opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

// AddMemory adds or updates a memory for a user (idempotent).
// Options may include WithMetadata for episodic metadata.
// Indexing a full document clears a matching tombstone, so an identical
// add reactivates a soft-deleted memory.
func (s *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryStr string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	ep := memory.ResolveAddOptions(opts)

	embedding, err := s.embedMemory(ctx, memoryStr)
	if err != nil {
		return err
	}

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	imemory.ApplyMetadata(mem, ep)
	imemory.NormalizeMemory(mem)
	memoryID := imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID)

	// Preserve the original creation timestamp across idempotent re-adds
	// and tombstone reactivations.
	createdAt := now
	existing, err := s.findDoc(ctx, userKey, memoryID, false)
	if err != nil {
		return fmt.Errorf("load existing memory failed: %w", err)
	}
	if existing != nil {
		createdAt = existing.CreatedAt
	}

	doc := newESDocument(memoryID, userKey, mem, embedding, createdAt, now)
	return s.indexDoc(ctx, doc)
}

// UpdateMemory updates an existing active memory for a user.
// Options may include WithUpdateMetadata for episodic metadata. When the
// canonical ID changes, a missing or soft-deleted target becomes active and
// the source is removed according to the delete policy. If the target is
// already active, UpdateMemory returns an error without modifying either
// memory. In soft-delete mode, a successful rotation retains the source
// tombstone.
//
// The Elasticsearch storage client exposes no transactional update, so a
// rotation creates or reactivates the target before removing the source.
// A failure between the two steps can leave both documents active, which
// the next rotation of either memory resolves.
func (s *Service) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryStr string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	ep := memory.ResolveUpdateOptions(opts)

	// A soft-deleted source is treated as not found.
	userKey := memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID}
	sourceDoc, err := s.findDoc(ctx, userKey, memoryKey.MemoryID, true)
	if err != nil {
		return fmt.Errorf("load memory entry failed: %w", err)
	}
	if sourceDoc == nil {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	embedding, err := s.embedMemory(ctx, memoryStr)
	if err != nil {
		return err
	}

	now := time.Now()
	entry := buildEntry(sourceDoc)
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)
	imemory.NormalizeMemory(entry.Memory)

	if newID == memoryKey.MemoryID {
		doc := newESDocument(newID, userKey, entry.Memory, embedding, sourceDoc.CreatedAt, now)
		if err := s.indexDoc(ctx, doc); err != nil {
			return fmt.Errorf("update memory entry failed: %w", err)
		}
	} else if err := s.rotateMemory(ctx, userKey, sourceDoc, entry.Memory, newID, embedding, now); err != nil {
		return fmt.Errorf("rotate memory entry failed: %w", err)
	}

	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// rotateMemory moves an updated memory to a new canonical ID.
func (s *Service) rotateMemory(
	ctx context.Context,
	userKey memory.UserKey,
	sourceDoc *esDocument,
	mem *memory.Memory,
	newID string,
	embedding []float32,
	now time.Time,
) error {
	// An already-active target must not be overwritten; a tombstoned
	// target is reactivated and keeps its original creation timestamp.
	target, err := s.findDoc(ctx, userKey, newID, false)
	if err != nil {
		return fmt.Errorf("check rotated memory target failed: %w", err)
	}
	if target != nil && target.DeletedAt == nil {
		return fmt.Errorf("memory with id %s already exists", newID)
	}
	targetCreatedAt := now
	if target != nil {
		targetCreatedAt = target.CreatedAt
	}

	doc := newESDocument(newID, userKey, mem, embedding, targetCreatedAt, now)
	if err := s.indexDoc(ctx, doc); err != nil {
		return fmt.Errorf("insert rotated memory target failed: %w", err)
	}

	// Remove the source according to the delete policy.
	if s.opts.softDelete {
		return s.updateDoc(ctx, sourceDoc.MemoryID, tombstoneUpdate(now))
	}
	return s.deleteDoc(ctx, sourceDoc.MemoryID)
}

// DeleteMemory deletes a memory for a user.
// Deleting an unknown memory is a no-op, matching the SQL backends.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	userKey := memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID}

	// Resolve the scoped document first: the delete policy update carries
	// no scope filter of its own and an unknown memory is a no-op.
	doc, err := s.findDoc(ctx, userKey, memoryKey.MemoryID, false)
	if err != nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}
	if doc == nil {
		return nil
	}

	if s.opts.softDelete {
		if err := s.updateDoc(ctx, memoryKey.MemoryID, tombstoneUpdate(time.Now())); err != nil {
			return fmt.Errorf("delete memory entry failed: %w", err)
		}
		return nil
	}
	if err := s.deleteDoc(ctx, memoryKey.MemoryID); err != nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
// With soft deletion enabled, every active document of the scope is
// tombstoned in bounded batches; partial failures are reported together.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	if s.opts.softDelete {
		if err := s.tombstoneScope(ctx, userKey, time.Now()); err != nil {
			return fmt.Errorf("clear memories failed: %w", err)
		}
		return nil
	}

	// A just-written document may not be searchable yet; without the
	// refresh the delete-by-query could miss it and leave it stored.
	if err := s.client.Refresh(ctx, s.indexName); err != nil {
		return fmt.Errorf("refresh index failed: %w", err)
	}
	body, err := json.Marshal(buildDeleteByQueryRequest(userKey))
	if err != nil {
		return fmt.Errorf("marshal clear memories request failed: %w", err)
	}
	if err := s.client.DeleteByQuery(ctx, s.indexName, body); err != nil {
		return fmt.Errorf("clear memories failed: %w", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
// Active entries are returned in deterministic order (updated_at, then
// memory ID, both descending) and cut to the requested limit.
func (s *Service) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	docs, _, err := s.search(ctx, s.buildReadRequest(userKey, limit))
	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}
	return docEntries(docs), nil
}

// defaultRRFK is the standard Reciprocal Rank Fusion constant.
const defaultRRFK = imemory.DefaultHybridRRFK

// SearchMemories searches memories for a user using vector similarity.
// Options may include WithSearchOptions for advanced filtering
// (kind, time range, hybrid search, etc.).
func (s *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	searchOpts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	opts := memory.ResolveSearchOptions(query, searchOpts)
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query = strings.TrimSpace(opts.Query)
	if query == "" {
		return []*memory.Entry{}, nil
	}

	// Generate embedding for the query (reused across fallback searches).
	queryEmbedding, err := s.opts.embedder.GetEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding failed: %w", err)
	}
	if len(queryEmbedding) != s.opts.indexDimension {
		return nil, fmt.Errorf("query embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(queryEmbedding))
	}
	queryVector := convertToFloat32(queryEmbedding)

	maxResults := s.opts.maxResults
	if opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}
	candidates := maxResults
	if opts.HybridSearch && s.opts.hybridCandidateLimit > 0 {
		candidates = s.opts.hybridCandidateLimit
	}

	results, err := s.executeDenseSearch(ctx, userKey, opts, queryVector, candidates)
	if err != nil {
		return nil, err
	}

	// Kind fallback: when kind filter was applied but returned too few
	// results, retry without the kind filter and merge both result sets.
	if opts.Kind != "" && opts.KindFallback && len(results) < imemory.MinKindFallbackResults {
		fallbackOpts := opts
		fallbackOpts.Kind = ""
		fallbackOpts.KindFallback = false
		fallbackResults, fallbackErr := s.executeDenseSearch(
			ctx, userKey, fallbackOpts, queryVector, candidates,
		)
		if fallbackErr == nil && len(fallbackResults) > 0 {
			results = imemory.MergeSearchResults(results, fallbackResults, opts.Kind, maxResults)
		}
	}

	// Hybrid search: run the lexical leg and merge with vector results
	// using Reciprocal Rank Fusion (RRF) to improve recall for exact
	// entity names, book titles, etc.
	if opts.HybridSearch {
		keywordResults, kwErr := s.executeKeywordSearch(ctx, userKey, opts, query, candidates)
		if kwErr != nil {
			log.WarnfContext(ctx, "elasticsearch memory keyword search failed: %v", kwErr)
		}
		if len(keywordResults) > 0 {
			rrfK := opts.HybridRRFK
			if rrfK <= 0 {
				rrfK = defaultRRFK
			}
			results = imemory.MergeHybridResults(results, keywordResults, rrfK, maxResults)
		}
	}

	// Apply similarity threshold filtering.
	// Skip when hybrid search is active because RRF scores use a
	// different range than cosine similarity.
	threshold := s.opts.similarityThreshold
	if opts.SimilarityThreshold > 0 {
		threshold = opts.SimilarityThreshold
	}
	if threshold > 0 && len(results) > 0 && !opts.HybridSearch {
		filtered := results[:0]
		for _, r := range results {
			if r.Score >= threshold {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	if len(results) > 1 {
		if opts.Kind != "" && opts.KindFallback {
			imemory.SortSearchResultsWithKindPriority(
				results,
				opts.Kind,
				opts.OrderByEventTime,
			)
		} else {
			imemory.SortSearchResults(results, opts.OrderByEventTime)
		}
	}

	// Content-based deduplication of near-identical memories.
	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

// executeDenseSearch runs a single vector similarity search.
func (s *Service) executeDenseSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryVector []float32,
	maxResults int,
) ([]*memory.Entry, error) {
	docs, scores, err := s.search(
		ctx, s.buildDenseSearchRequest(userKey, opts, queryVector, maxResults),
	)
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}
	entries := docEntries(docs)
	for i, entry := range entries {
		entry.Score = scores[i]
	}
	return entries, nil
}

// executeKeywordSearch runs the lexical leg of hybrid search.
func (s *Service) executeKeywordSearch(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
	query string,
	maxResults int,
) ([]*memory.Entry, error) {
	docs, _, err := s.search(
		ctx, s.buildKeywordSearchRequest(userKey, opts, query, maxResults),
	)
	if err != nil {
		return nil, fmt.Errorf("keyword search memories failed: %w", err)
	}
	return docEntries(docs), nil
}

// embedMemory generates and validates an embedding for memory content.
func (s *Service) embedMemory(ctx context.Context, content string) ([]float32, error) {
	embedding, err := s.opts.embedder.GetEmbedding(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("generate embedding failed: %w", err)
	}
	if len(embedding) != s.opts.indexDimension {
		return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d",
			s.opts.indexDimension, len(embedding))
	}
	return convertToFloat32(embedding), nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), memory_search is exposed by default,
// memory_load is exposed once enabled, and other enabled tools remain hidden
// unless explicitly exposed.
// Without an extractor, enabled tools are exposed directly.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async processing.
// The session contains the full transcript and state for incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close stops async workers. The Elasticsearch client owns no resources
// that require explicit closing.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	return nil
}

// joinErrors merges partial failures, dropping nil errors.
func joinErrors(errs ...error) error {
	return errors.Join(errs...)
}
