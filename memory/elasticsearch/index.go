//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

// Field names of the stored documents.
const (
	fieldMemoryID     = "memory_id"
	fieldAppName      = "app_name"
	fieldUserID       = "user_id"
	fieldContent      = "content"
	fieldTopics       = "topics"
	fieldKind         = "kind"
	fieldEventTime    = "event_time"
	fieldParticipants = "participants"
	fieldLocation     = "location"
	fieldEmbedding    = "embedding"
	fieldCreatedAt    = "created_at"
	fieldUpdatedAt    = "updated_at"
	fieldDeletedAt    = "deleted_at"
)

// ensureIndex creates the configured index with the memory mapping when it
// does not exist yet. Existing indexes are left untouched: Elasticsearch
// itself enforces the dense_vector dimension on every write, so an index
// with an incompatible mapping surfaces as a write error instead of being
// silently migrated.
func (s *Service) ensureIndex(ctx context.Context) error {
	exists, err := s.client.IndexExists(ctx, s.indexName)
	if err != nil {
		return fmt.Errorf("check index existence failed: %w", err)
	}
	if exists {
		return nil
	}
	body, err := buildIndexCreateBody(s.mappingVersion, s.opts.indexDimension)
	if err != nil {
		return fmt.Errorf("build index create body failed: %w", err)
	}
	if err := s.client.CreateIndex(ctx, s.indexName, body); err != nil {
		return fmt.Errorf("create index failed: %w", err)
	}
	return nil
}

// mappingVersion is the effective Elasticsearch major version used to build
// the index mapping. It mirrors the client default: an unspecified version
// builds a v9 client.
func effectiveMappingVersion(version storage.ESVersion) storage.ESVersion {
	if version == storage.ESVersionUnspecified || version == "" {
		return storage.ESVersionV9
	}
	return version
}

// buildIndexCreateBody builds the index creation payload with settings and
// mappings. Elasticsearch 7 does not support the index and similarity
// parameters on dense_vector fields, so they are only emitted for v8+.
func buildIndexCreateBody(version storage.ESVersion, dimension int) ([]byte, error) {
	embedding := map[string]any{
		"type": "dense_vector",
		"dims": dimension,
	}
	if version == storage.ESVersionV8 || version == storage.ESVersionV9 {
		embedding["index"] = true
		embedding["similarity"] = "cosine"
	}

	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   "1",
			"number_of_replicas": "0",
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				fieldMemoryID:     map[string]any{"type": "keyword"},
				fieldAppName:      map[string]any{"type": "keyword"},
				fieldUserID:       map[string]any{"type": "keyword"},
				fieldContent:      map[string]any{"type": "text"},
				fieldTopics:       map[string]any{"type": "keyword"},
				fieldKind:         map[string]any{"type": "keyword"},
				fieldEventTime:    map[string]any{"type": "date"},
				fieldParticipants: map[string]any{"type": "keyword"},
				fieldLocation: map[string]any{
					"type": "keyword",
					"fields": map[string]any{
						"text": map[string]any{"type": "text"},
					},
				},
				fieldEmbedding: embedding,
				fieldCreatedAt: map[string]any{"type": "date_nanos"},
				fieldUpdatedAt: map[string]any{"type": "date_nanos"},
				fieldDeletedAt: map[string]any{"type": "date_nanos"},
			},
		},
	}
	return json.Marshal(body)
}

// searchRequest is the subset of the Elasticsearch search request used by
// this package.
type searchRequest struct {
	Query map[string]any   `json:"query,omitempty"`
	Size  int              `json:"size,omitempty"`
	Sort  []map[string]any `json:"sort,omitempty"`
}

// termQuery builds a term query on a keyword field.
func termQuery(field, value string) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

// existsQuery builds an exists query.
func existsQuery(field string) map[string]any {
	return map[string]any{"exists": map[string]any{"field": field}}
}

// buildScopeFilter builds the mandatory app/user isolation filter plus the
// optional kind, event time and active-state filters. All reads, searches,
// updates, deletes and clear operations go through this filter so documents
// of another scope are never addressable.
func (s *Service) buildScopeFilter(
	userKey memory.UserKey,
	opts memory.SearchOptions,
	activeOnly bool,
) []map[string]any {
	filter := []map[string]any{
		termQuery(fieldAppName, userKey.AppName),
		termQuery(fieldUserID, userKey.UserID),
	}

	switch opts.Kind {
	case memory.KindFact:
		filter = append(filter, termQuery(fieldKind, string(memory.KindFact)))
	case memory.KindEpisode:
		filter = append(filter, termQuery(fieldKind, string(memory.KindEpisode)))
	}

	// Mirror the SQL backends: records without event_time always satisfy
	// an event time range filter.
	var timeRanges []map[string]any
	if opts.TimeAfter != nil {
		timeRanges = append(timeRanges, map[string]any{
			"range": map[string]any{fieldEventTime: map[string]any{"gte": *opts.TimeAfter}},
		})
	}
	if opts.TimeBefore != nil {
		timeRanges = append(timeRanges, map[string]any{
			"range": map[string]any{fieldEventTime: map[string]any{"lte": *opts.TimeBefore}},
		})
	}
	if len(timeRanges) > 0 {
		should := append(timeRanges, map[string]any{
			"bool": map[string]any{"must_not": []map[string]any{existsQuery(fieldEventTime)}},
		})
		filter = append(filter, map[string]any{
			"bool": map[string]any{
				"should":               should,
				"minimum_should_match": 1,
			},
		})
	}

	if activeOnly && s.opts.softDelete {
		filter = append(filter, map[string]any{
			"bool": map[string]any{"must_not": []map[string]any{existsQuery(fieldDeletedAt)}},
		})
	}

	return filter
}

// buildIDLookupRequest builds a search request resolving a single document
// by scope and canonical memory ID.
func (s *Service) buildIDLookupRequest(
	userKey memory.UserKey,
	memoryID string,
	activeOnly bool,
) *searchRequest {
	filter := append(s.buildScopeFilter(userKey, memory.SearchOptions{}, activeOnly),
		termQuery(fieldMemoryID, memoryID))
	return &searchRequest{
		Query: map[string]any{"bool": map[string]any{"filter": filter}},
		Size:  1,
	}
}

// buildReadRequest builds a search request listing active scoped entries
// with deterministic ordering.
func (s *Service) buildReadRequest(userKey memory.UserKey, limit int) *searchRequest {
	size := limit
	if size <= 0 {
		// Elasticsearch caps a single request at index.max_result_window
		// (10000 by default); without an explicit limit all entries within
		// one window are returned.
		size = 10000
	}
	return &searchRequest{
		Query: map[string]any{
			"bool": map[string]any{
				"filter": s.buildScopeFilter(userKey, memory.SearchOptions{}, true),
			},
		},
		Size: size,
		Sort: []map[string]any{
			{fieldUpdatedAt: map[string]any{"order": "desc"}},
			{fieldMemoryID: map[string]any{"order": "desc"}},
		},
	}
}

// buildDenseSearchRequest builds a script_score request that scores every
// matching document by cosine similarity normalized to the 0-1 range used
// across the memory backends. The script_score form works on Elasticsearch
// 7, 8 and 9 without relying on version-specific kNN endpoints.
func (s *Service) buildDenseSearchRequest(
	userKey memory.UserKey,
	opts memory.SearchOptions,
	queryVector []float32,
	size int,
) *searchRequest {
	script := map[string]any{
		"source": fmt.Sprintf(
			"if (doc['%s'].size() > 0) { (cosineSimilarity(params.query_vector, '%s') + 1.0) / 2.0 } else { 0.0 }",
			fieldEmbedding, fieldEmbedding,
		),
		"params": map[string]any{"query_vector": queryVector},
	}
	return &searchRequest{
		Query: map[string]any{
			"script_score": map[string]any{
				"query":  map[string]any{"bool": map[string]any{"filter": s.buildScopeFilter(userKey, opts, true)}},
				"script": script,
			},
		},
		Size: size,
	}
}

// buildKeywordSearchRequest builds a lexical request over the content field
// scoped like every other search. It is used for the lexical leg of hybrid
// search.
func (s *Service) buildKeywordSearchRequest(
	userKey memory.UserKey,
	opts memory.SearchOptions,
	query string,
	size int,
) *searchRequest {
	filter := s.buildScopeFilter(userKey, opts, true)
	return &searchRequest{
		Query: map[string]any{
			"bool": map[string]any{
				"filter": filter,
				"must": []map[string]any{
					{"multi_match": map[string]any{
						"query":  query,
						"fields": []string{fieldContent},
					}},
				},
			},
		},
		Size: size,
	}
}

// buildDeleteByQueryRequest builds the delete-by-query payload clearing
// every document of a scope.
func buildDeleteByQueryRequest(userKey memory.UserKey) map[string]any {
	return map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					termQuery(fieldAppName, userKey.AppName),
					termQuery(fieldUserID, userKey.UserID),
				},
			},
		},
	}
}

// search executes a search request and returns the decoded documents with
// their scores.
func (s *Service) search(
	ctx context.Context,
	req *searchRequest,
) ([]*esDocument, []float64, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal search request failed: %w", err)
	}
	data, err := s.client.Search(ctx, s.indexName, body)
	if err != nil {
		return nil, nil, fmt.Errorf("search documents failed: %w", err)
	}
	return decodeSearchDocs(data)
}

// findDoc resolves a single document by scope and memory ID.
// It returns nil without an error when no document matches.
func (s *Service) findDoc(
	ctx context.Context,
	userKey memory.UserKey,
	memoryID string,
	activeOnly bool,
) (*esDocument, error) {
	req := s.buildIDLookupRequest(userKey, memoryID, activeOnly)
	docs, _, err := s.search(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}
	return docs[0], nil
}

// indexDoc serializes and stores a document under its deterministic ID.
func (s *Service) indexDoc(ctx context.Context, doc *esDocument) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal memory document failed: %w", err)
	}
	if err := s.client.IndexDoc(ctx, s.indexName, doc.MemoryID, body); err != nil {
		return fmt.Errorf("index memory document failed: %w", err)
	}
	return nil
}

// updateDoc applies a partial update to the document with the given ID.
func (s *Service) updateDoc(ctx context.Context, memoryID string, patch map[string]any) error {
	body, err := json.Marshal(map[string]any{"doc": patch})
	if err != nil {
		return fmt.Errorf("marshal memory update failed: %w", err)
	}
	if err := s.client.UpdateDoc(ctx, s.indexName, memoryID, body); err != nil {
		return fmt.Errorf("update memory document failed: %w", err)
	}
	return nil
}

// deleteDoc removes the document with the given ID.
func (s *Service) deleteDoc(ctx context.Context, memoryID string) error {
	if err := s.client.DeleteDoc(ctx, s.indexName, memoryID); err != nil {
		return fmt.Errorf("delete memory document failed: %w", err)
	}
	return nil
}

// tombstoneBatch is the number of documents updated per round when a clear
// operation soft-deletes a scope without a bulk or update-by-query API.
const tombstoneBatchSize = 100

// tombstoneScope soft-deletes every active document of a scope in bounded
// batches and reports partial failures.
func (s *Service) tombstoneScope(ctx context.Context, userKey memory.UserKey, now time.Time) error {
	var joinedErr error
	for {
		entries, err := s.readBatch(ctx, userKey, tombstoneBatchSize)
		if err != nil {
			joinedErr = joinErrors(joinedErr, err)
			break
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if err := s.updateDoc(ctx, entry.ID, tombstoneUpdate(now)); err != nil {
				joinedErr = joinErrors(joinedErr, err)
			}
		}
		if len(entries) < tombstoneBatchSize {
			break
		}
	}
	return joinedErr
}

// readBatch reads one batch of active scoped entries in deterministic order.
func (s *Service) readBatch(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	req := s.buildReadRequest(userKey, limit)
	docs, _, err := s.search(ctx, req)
	if err != nil {
		return nil, err
	}
	return docEntries(docs), nil
}
