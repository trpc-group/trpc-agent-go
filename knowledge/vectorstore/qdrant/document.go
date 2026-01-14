//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// document.go implements document CRUD operations for the VectorStore.
package qdrant

import (
	"context"
	"errors"
	"fmt"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

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

	point := vs.buildPoint(doc, embedding)

	err := retryVoid(ctx, vs.retryCfg, func() error {
		_, err := vs.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: vs.opts.collectionName,
			Points:         []*qdrant.PointStruct{point},
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("add document %q to %q: %w", doc.ID, vs.opts.collectionName, err)
	}
	return nil
}

// AddBatch stores multiple documents with their embedding vectors in a single operation.
// This is more efficient than calling Add() multiple times for bulk imports.
func (vs *VectorStore) AddBatch(ctx context.Context, docs []*document.Document, embeddings [][]float64) error {
	if len(docs) == 0 {
		return nil
	}
	if len(docs) != len(embeddings) {
		return fmt.Errorf("%w: documents count (%d) must match embeddings count (%d)",
			ErrInvalidInput, len(docs), len(embeddings))
	}

	points := make([]*qdrant.PointStruct, 0, len(docs))
	for i, doc := range docs {
		if doc == nil {
			return fmt.Errorf("%w: document at index %d is nil", ErrInvalidInput, i)
		}
		if doc.ID == "" {
			return fmt.Errorf("%w: document at index %d has empty ID", ErrInvalidInput, i)
		}
		if err := vs.validateEmbedding(embeddings[i]); err != nil {
			return fmt.Errorf("document %q at index %d: %w", doc.ID, i, err)
		}
		points = append(points, vs.buildPoint(doc, embeddings[i]))
	}

	err := retryVoid(ctx, vs.retryCfg, func() error {
		_, err := vs.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: vs.opts.collectionName,
			Points:         points,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("add batch of %d documents to %q: %w", len(docs), vs.opts.collectionName, err)
	}
	return nil
}

// buildPoint creates a Qdrant point from a document and embedding.
func (vs *VectorStore) buildPoint(doc *document.Document, embedding []float64) *qdrant.PointStruct {
	point := toPoint(doc, embedding)

	if vs.opts.bm25Enabled {
		point.Vectors = qdrant.NewVectorsMap(map[string]*qdrant.Vector{
			vectorNameDense: qdrant.NewVectorDense(toFloat32Slice(embedding)),
			vectorNameSparse: qdrant.NewVectorDocument(&qdrant.Document{
				Text:  doc.Content,
				Model: defaultBM25Model,
			}),
		})
	}

	return point
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

	return vs.fromRetrievedPoint(points[0])
}

// fromRetrievedPoint converts a Qdrant RetrievedPoint to a Document and vector.
func (vs *VectorStore) fromRetrievedPoint(pt *qdrant.RetrievedPoint) (*document.Document, []float64, error) {
	doc := payloadToDocument(pt.Id, pt.Payload)
	vec := vs.extractDenseVector(pt.Vectors)
	return doc, vec, nil
}

// Update modifies an existing document.
// This performs an upsert: the document is created if it doesn't exist.
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

// DeleteByFilter deletes documents matching criteria.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)

	switch {
	case len(config.DocumentIDs) > 0:
		err := retryVoid(ctx, vs.retryCfg, func() error {
			_, err := vs.client.Delete(ctx, &qdrant.DeletePoints{
				CollectionName: vs.opts.collectionName,
				Points:         qdrant.NewPointsSelectorIDs(stringsToPointIDs(config.DocumentIDs)),
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
		// No criteria specified: nothing to delete, succeed silently.
		return nil
	}
}

// UpdateByFilter updates documents matching the filter with the specified field values.
// This operation is not yet supported by Qdrant VectorStore.
func (vs *VectorStore) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	return 0, errors.New("UpdateByFilter is not implemented for Qdrant")
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

// extractDenseVector extracts the dense vector from Qdrant VectorsOutput.
func (vs *VectorStore) extractDenseVector(vectors *qdrant.VectorsOutput) []float64 {
	if vectors == nil {
		return nil
	}

	// Try named vectors first (BM25 mode)
	if named, ok := vectors.VectorsOptions.(*qdrant.VectorsOutput_Vectors); ok {
		if denseVec, exists := named.Vectors.Vectors[vectorNameDense]; exists {
			return extractVectorData(denseVec)
		}
	}

	// Fall back to single vector mode
	if v, ok := vectors.VectorsOptions.(*qdrant.VectorsOutput_Vector); ok {
		return extractVectorData(v.Vector)
	}

	return nil
}
