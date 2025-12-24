//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package qdrant provides a Qdrant-based implementation of the VectorStore interface.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

// VectorStore implements vectorstore.VectorStore using Qdrant.
type VectorStore struct {
	client          Client
	opts            options
	filterConverter searchfilter.Converter[*qdrant.Filter]
	retryCfg        retryConfig
}

// New creates a new Qdrant VectorStore.
func New(ctx context.Context, opts ...Option) (*VectorStore, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	config := &qdrant.Config{
		Host: o.host,
		Port: o.port,
	}
	if o.apiKey != "" {
		config.APIKey = o.apiKey
	}
	if o.useTLS {
		config.UseTLS = true
	}

	client, err := NewClient(config)
	if err != nil {
		return nil, errors.Join(ErrConnectionFailed, err)
	}

	vs := &VectorStore{
		client:          client,
		opts:            o,
		filterConverter: newFilterConverter(),
		retryCfg: retryConfig{
			maxRetries:     o.maxRetries,
			baseRetryDelay: o.baseRetryDelay,
			maxRetryDelay:  o.maxRetryDelay,
		},
	}

	if err := vs.ensureCollection(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}

	return vs, nil
}

func (vs *VectorStore) ensureCollection(ctx context.Context) error {
	exists, err := retry(ctx, vs.retryCfg, func() (bool, error) {
		return vs.client.CollectionExists(ctx, vs.opts.collectionName)
	})
	if err != nil {
		return fmt.Errorf("check collection %q exists: %w", vs.opts.collectionName, err)
	}
	if exists {
		return nil
	}

	vectorsConfig := qdrant.NewVectorsConfig(&qdrant.VectorParams{
		Size:     uint64(vs.opts.dimension),
		Distance: vs.opts.distance,
	})

	if vs.opts.onDiskVectors {
		vectorsConfig.GetParams().OnDisk = ptrBool(true)
	}

	err = retryVoid(ctx, vs.retryCfg, func() error {
		return vs.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: vs.opts.collectionName,
			VectorsConfig:  vectorsConfig,
			OnDiskPayload:  ptrBool(vs.opts.onDiskPayload),
			HnswConfig: &qdrant.HnswConfigDiff{
				M:           ptrUint64(uint64(vs.opts.hnswM)),
				EfConstruct: ptrUint64(uint64(vs.opts.hnswEfConstruct)),
			},
		})
	})
	if err != nil {
		return fmt.Errorf("create collection %q: %w", vs.opts.collectionName, err)
	}
	return nil
}

// Add stores a document with its embedding vector.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if err := vs.validateEmbedding(embedding); err != nil {
		return err
	}

	err := retryVoid(ctx, vs.retryCfg, func() error {
		_, err := vs.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: vs.opts.collectionName,
			Points:         []*qdrant.PointStruct{toPoint(doc, embedding)},
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("add document %q to %q: %w", doc.ID, vs.opts.collectionName, err)
	}
	return nil
}

// Get retrieves a document by ID.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errIDRequired
	}

	points, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.RetrievedPoint, error) {
		return vs.client.Get(ctx, &qdrant.GetPoints{
			CollectionName: vs.opts.collectionName,
			Ids:            []*qdrant.PointId{qdrant.NewID(idToUUID(id))},
			WithPayload:    qdrant.NewWithPayload(true),
			WithVectors:    qdrant.NewWithVectors(true),
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("get document %q from %q: %w", id, vs.opts.collectionName, err)
	}
	if len(points) == 0 {
		return nil, nil, ErrNotFound
	}

	return fromRetrievedPoint(points[0])
}

// validateEmbedding checks that the embedding is valid.
func (vs *VectorStore) validateEmbedding(embedding []float64) error {
	if embedding == nil {
		return errEmbeddingRequired
	}
	if len(embedding) != vs.opts.dimension {
		return fmt.Errorf("%w: expected %d dimensions, got %d",
			ErrInvalidInput, vs.opts.dimension, len(embedding))
	}
	return nil
}

// Update modifies an existing document.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return vs.Add(ctx, doc, embedding)
}

// Delete removes a document by ID.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errIDRequired
	}

	err := retryVoid(ctx, vs.retryCfg, func() error {
		_, err := vs.client.Delete(ctx, &qdrant.DeletePoints{
			CollectionName: vs.opts.collectionName,
			Points:         qdrant.NewPointsSelector(qdrant.NewID(idToUUID(id))),
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("delete document %q from %q: %w", id, vs.opts.collectionName, err)
	}
	return nil
}

// Search performs similarity search.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errQueryRequired
	}

	filter, err := vs.buildSearchFilter(query.Filter)
	if err != nil {
		return nil, err
	}

	limit := uint64(query.Limit)
	if limit == 0 {
		limit = uint64(vs.opts.maxResults)
	}

	results, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.ScoredPoint, error) {
		return vs.client.Query(ctx, &qdrant.QueryPoints{
			CollectionName: vs.opts.collectionName,
			Query:          qdrant.NewQuery(toFloat32Slice(query.Vector)...),
			Filter:         filter,
			Limit:          ptrUint64(limit),
			WithPayload:    qdrant.NewWithPayload(true),
			ScoreThreshold: ptrFloat32If(query.MinScore > 0, query.MinScore),
		})
	})
	if err != nil {
		return nil, fmt.Errorf("search in %q: %w", vs.opts.collectionName, err)
	}

	return toSearchResult(results), nil
}

func (vs *VectorStore) buildSearchFilter(filter *vectorstore.SearchFilter) (*qdrant.Filter, error) {
	if filter == nil {
		return nil, nil
	}

	var filters []*searchfilter.UniversalFilterCondition

	if len(filter.IDs) > 0 {
		return &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_HasId{
						HasId: &qdrant.HasIdCondition{
							HasId: stringsToPointIDs(filter.IDs),
						},
					},
				},
			},
		}, nil
	}

	for key, value := range filter.Metadata {
		if !strings.HasPrefix(key, source.MetadataFieldPrefix) {
			key = source.MetadataFieldPrefix + key
		}
		filters = append(filters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorEqual,
			Field:    key,
			Value:    value,
		})
	}

	if filter.FilterCondition != nil {
		filters = append(filters, filter.FilterCondition)
	}

	if len(filters) == 0 {
		return nil, nil
	}

	return vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    filters,
	})
}

// DeleteByFilter deletes documents matching criteria.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)

	switch {
	case len(config.DocumentIDs) > 0:
		err := retryVoid(ctx, vs.retryCfg, func() error {
			_, err := vs.client.Delete(ctx, &qdrant.DeletePoints{
				CollectionName: vs.opts.collectionName,
				Points:         qdrant.NewPointsSelector(stringsToPointIDs(config.DocumentIDs)...),
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("delete by IDs from %q: %w", vs.opts.collectionName, err)
		}
		return nil

	case config.Filter != nil:
		filter, err := vs.filterConverter.Convert(metadataToCondition(config.Filter))
		if err != nil {
			return err
		}
		err = retryVoid(ctx, vs.retryCfg, func() error {
			_, err := vs.client.Delete(ctx, &qdrant.DeletePoints{
				CollectionName: vs.opts.collectionName,
				Points:         qdrant.NewPointsSelectorFilter(filter),
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("delete by filter from %q: %w", vs.opts.collectionName, err)
		}
		return nil

	case config.DeleteAll:
		err := retryVoid(ctx, vs.retryCfg, func() error {
			_, err := vs.client.Delete(ctx, &qdrant.DeletePoints{
				CollectionName: vs.opts.collectionName,
				Points:         qdrant.NewPointsSelectorFilter(&qdrant.Filter{}),
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("delete all from %q: %w", vs.opts.collectionName, err)
		}
		return nil

	default:
		return nil
	}
}

// Count returns the number of documents.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)

	var filter *qdrant.Filter
	if config.Filter != nil {
		var err error
		filter, err = vs.filterConverter.Convert(metadataToCondition(config.Filter))
		if err != nil {
			return 0, err
		}
	}

	count, err := retry(ctx, vs.retryCfg, func() (uint64, error) {
		return vs.client.Count(ctx, &qdrant.CountPoints{
			CollectionName: vs.opts.collectionName,
			Filter:         filter,
			Exact:          ptrBool(true),
		})
	})
	if err != nil {
		return 0, fmt.Errorf("count documents in %q: %w", vs.opts.collectionName, err)
	}
	return int(count), nil
}

// GetMetadata retrieves document metadata.
func (vs *VectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	filter, err := vs.buildMetadataFilter(config)
	if err != nil {
		return nil, err
	}

	// Batch size for scrolling (internal pagination)
	const batchSize = uint32(100)

	// Max results to return (0 means unlimited)
	maxResults := config.Limit

	results := make(map[string]vectorstore.DocumentMetadata)
	var offset *qdrant.PointId

	for {
		points, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.RetrievedPoint, error) {
			return vs.client.Scroll(ctx, &qdrant.ScrollPoints{
				CollectionName: vs.opts.collectionName,
				Filter:         filter,
				Limit:          ptrUint32(batchSize),
				Offset:         offset,
				WithPayload:    qdrant.NewWithPayload(true),
			})
		})
		if err != nil {
			return nil, fmt.Errorf("get metadata from %q: %w", vs.opts.collectionName, err)
		}

		for _, pt := range points {
			results[pointIDToStr(pt.Id)] = vectorstore.DocumentMetadata{
				Metadata: extractPayloadMetadata(pt.Payload),
			}
			// Stop if we've reached the max results
			if maxResults > 0 && len(results) >= maxResults {
				return results, nil
			}
		}

		// No more results to fetch
		if len(points) < int(batchSize) {
			break
		}
		if len(points) > 0 {
			offset = points[len(points)-1].Id
		} else {
			break
		}
	}

	return results, nil
}

func (vs *VectorStore) buildMetadataFilter(config *vectorstore.GetMetadataConfig) (*qdrant.Filter, error) {
	if len(config.IDs) > 0 {
		return &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_HasId{
						HasId: &qdrant.HasIdCondition{
							HasId: stringsToPointIDs(config.IDs),
						},
					},
				},
			},
		}, nil
	}

	if config.Filter != nil {
		return vs.filterConverter.Convert(metadataToCondition(config.Filter))
	}

	return nil, nil
}

// Close closes the connection.
func (vs *VectorStore) Close() error {
	if vs.client == nil {
		return nil
	}
	return vs.client.Close()
}
