//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// metadata.go implements metadata operations: Count, GetMetadata, UpdateMetadata.
package qdrant

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

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
			Exact:          qdrant.PtrOf(true),
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

	maxResults := config.Limit
	results := make(map[string]vectorstore.DocumentMetadata)
	var offset *qdrant.PointId

	for {
		// Optimize batch size: don't fetch more than needed
		batchSize := uint32(defaultBatchSize)
		if maxResults > 0 {
			remaining := maxResults - len(results)
			if remaining < defaultBatchSize {
				batchSize = uint32(remaining)
			}
		}

		points, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.RetrievedPoint, error) {
			return vs.client.Scroll(ctx, &qdrant.ScrollPoints{
				CollectionName: vs.opts.collectionName,
				Filter:         filter,
				Limit:          qdrant.PtrOf(batchSize),
				Offset:         offset,
				WithPayload:    qdrant.NewWithPayload(true),
			})
		})
		if err != nil {
			return nil, fmt.Errorf("get metadata from %q: %w", vs.opts.collectionName, err)
		}

		if len(points) == 0 {
			break
		}

		for _, pt := range points {
			docID := getPayloadString(pt.Payload, fieldID)
			if docID == "" {
				docID = pointIDToStr(pt.Id)
			}
			results[docID] = vectorstore.DocumentMetadata{
				Metadata: extractPayloadMetadata(pt.Payload),
			}
		}

		// Stop if we've reached the limit or exhausted results
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
		if len(points) < int(batchSize) {
			break
		}

		offset = points[len(points)-1].Id
	}

	return results, nil
}

// buildMetadataFilter builds a filter for GetMetadata.
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

// UpdateMetadata updates the metadata of an existing document without changing its vector.
// The metadata is merged with existing metadata; to remove a field, set it to nil.
func (vs *VectorStore) UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error {
	if id == "" {
		return errIDRequired
	}
	if len(metadata) == 0 {
		return nil // Nothing to update
	}

	payload := make(map[string]*qdrant.Value)
	for key, value := range metadata {
		v, err := qdrant.NewValue(sanitizeValue(value))
		if err != nil {
			return err
		}
		payload[source.MetadataFieldPrefix+key] = v
	}

	err := retryVoid(ctx, vs.retryCfg, func() error {
		_, err := vs.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
			CollectionName: vs.opts.collectionName,
			Payload:        payload,
			PointsSelector: qdrant.NewPointsSelector(qdrant.NewID(idToUUID(id))),
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("update metadata for %q in %q: %w", id, vs.opts.collectionName, err)
	}
	return nil
}
