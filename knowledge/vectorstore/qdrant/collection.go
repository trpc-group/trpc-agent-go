//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"
)

// ensureCollection checks if the collection exists and creates it if not.
// If the collection exists, it validates the configuration matches our settings.
func (vs *VectorStore) ensureCollection(ctx context.Context) error {
	exists, err := retry(ctx, vs.retryCfg, func() (bool, error) {
		return vs.client.CollectionExists(ctx, vs.opts.collectionName)
	})
	if err != nil {
		return fmt.Errorf("check collection %q exists: %w", vs.opts.collectionName, err)
	}
	if exists {
		return vs.validateCollectionConfig(ctx)
	}

	// Try to create the collection
	if err := vs.createCollection(ctx); err != nil {
		return vs.validateCollectionConfig(ctx)
	}

	return nil
}

// createCollection creates a new Qdrant collection with the configured settings.
func (vs *VectorStore) createCollection(ctx context.Context) error {
	createReq := &qdrant.CreateCollection{
		CollectionName: vs.opts.collectionName,
		OnDiskPayload:  qdrant.PtrOf(vs.opts.onDiskPayload),
		HnswConfig: &qdrant.HnswConfigDiff{
			M:           qdrant.PtrOf(uint64(vs.opts.hnswM)),
			EfConstruct: qdrant.PtrOf(uint64(vs.opts.hnswEfConstruct)),
		},
	}

	if vs.opts.bm25Enabled {
		// Named vectors configuration: dense + sparse for BM25
		denseParams := &qdrant.VectorParams{
			Size:     uint64(vs.opts.dimension),
			Distance: vs.opts.distance,
			OnDisk:   qdrant.PtrOf(vs.opts.onDiskVectors),
		}

		createReq.VectorsConfig = qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
			vectorNameDense: denseParams,
		})

		// Sparse vector config with IDF modifier for BM25
		createReq.SparseVectorsConfig = qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			vectorNameSparse: {
				Modifier: qdrant.Modifier_Idf.Enum(),
			},
		})
	} else {
		// Simple single vector configuration (no BM25)
		vectorsConfig := qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(vs.opts.dimension),
			Distance: vs.opts.distance,
			OnDisk:   qdrant.PtrOf(vs.opts.onDiskVectors),
		})
		createReq.VectorsConfig = vectorsConfig
	}

	err := vs.client.CreateCollection(ctx, createReq)

	if err != nil {
		return fmt.Errorf("create collection %q: %w", vs.opts.collectionName, err)
	}

	return nil
}

// validateCollectionConfig checks that an existing collection matches our expected configuration.
func (vs *VectorStore) validateCollectionConfig(ctx context.Context) error {
	info, err := retry(ctx, vs.retryCfg, func() (*qdrant.CollectionInfo, error) {
		return vs.client.GetCollectionInfo(ctx, vs.opts.collectionName)
	})
	if err != nil {
		return fmt.Errorf("get collection %q info: %w", vs.opts.collectionName, err)
	}
	if info == nil || info.Config == nil || info.Config.Params == nil {
		return fmt.Errorf("%w: collection %q has no configuration", ErrCollectionMismatch, vs.opts.collectionName)
	}

	// Validate vector configuration
	if err := vs.validateVectorConfig(info.Config.Params.VectorsConfig); err != nil {
		return err
	}

	// Validate sparse vector configuration if BM25 is enabled
	if vs.opts.bm25Enabled {
		if err := vs.validateSparseVectorConfig(info.Config.Params.SparseVectorsConfig); err != nil {
			return err
		}
	}

	return nil
}

// validateVectorConfig validates that the collection's vector configuration matches our settings.
func (vs *VectorStore) validateVectorConfig(config *qdrant.VectorsConfig) error {
	if config == nil {
		return fmt.Errorf("%w: collection has no vectors config", ErrCollectionMismatch)
	}

	var size uint64
	var distance qdrant.Distance

	switch cfg := config.Config.(type) {
	case *qdrant.VectorsConfig_Params:
		// Single vector mode (non-BM25)
		if cfg.Params == nil {
			return fmt.Errorf("%w: collection has nil vector params", ErrCollectionMismatch)
		}
		size = cfg.Params.Size
		distance = cfg.Params.Distance

	case *qdrant.VectorsConfig_ParamsMap:
		// Named vectors mode (BM25 enabled)
		if cfg.ParamsMap == nil || cfg.ParamsMap.Map == nil {
			return fmt.Errorf("%w: collection has nil vector params map", ErrCollectionMismatch)
		}
		denseParams, ok := cfg.ParamsMap.Map[vectorNameDense]
		if !ok {
			return fmt.Errorf("%w: collection missing %q vector", ErrCollectionMismatch, vectorNameDense)
		}
		if denseParams == nil {
			return fmt.Errorf("%w: collection has nil dense params", ErrCollectionMismatch)
		}
		size = denseParams.Size
		distance = denseParams.Distance

	default:
		return fmt.Errorf("%w: unknown vectors config type", ErrCollectionMismatch)
	}

	// Check dimension
	if size != uint64(vs.opts.dimension) {
		return fmt.Errorf("%w: expected dimension %d, got %d",
			ErrCollectionMismatch, vs.opts.dimension, size)
	}

	// Check distance metric
	if distance != vs.opts.distance {
		return fmt.Errorf("%w: expected distance %v, got %v",
			ErrCollectionMismatch, vs.opts.distance, distance)
	}

	return nil
}

// validateSparseVectorConfig validates that the collection has sparse vectors configured for BM25.
func (vs *VectorStore) validateSparseVectorConfig(config *qdrant.SparseVectorConfig) error {
	if config == nil || config.Map == nil {
		return fmt.Errorf("%w: BM25 enabled but collection has no sparse vectors config",
			ErrCollectionMismatch)
	}

	if _, ok := config.Map[vectorNameSparse]; !ok {
		return fmt.Errorf("%w: BM25 enabled but collection missing %q sparse vector",
			ErrCollectionMismatch, vectorNameSparse)
	}

	return nil
}

// DeleteCollection deletes the entire collection and all its data.
// Use with caution as this operation is irreversible.
func (vs *VectorStore) DeleteCollection(ctx context.Context) error {
	err := vs.client.DeleteCollection(ctx, vs.opts.collectionName)
	if err != nil {
		return fmt.Errorf("delete collection %q: %w", vs.opts.collectionName, err)
	}
	return nil
}
