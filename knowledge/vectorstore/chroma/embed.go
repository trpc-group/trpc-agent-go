//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"context"
	"fmt"
	"math"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

// SparseVector is a Chroma sparse embedding. Indices and Values must have the
// same length. Indices must be unique, strictly increasing, and fit in int32;
// Values must be finite and fit in float32.
type SparseVector struct {
	// Indices contains the non-zero dimensions in strictly increasing order.
	Indices []int
	// Values contains one finite weight for each entry in Indices.
	Values []float64
}

// SparseEmbedder turns documents and queries into sparse vectors in one
// compatible vector space. Implementations must be safe for concurrent use.
type SparseEmbedder interface {
	// EmbedDocument returns the sparse encoding of a stored document.
	EmbedDocument(ctx context.Context, text string) (SparseVector, error)
	// EmbedQuery returns the sparse encoding of a search query.
	EmbedQuery(ctx context.Context, text string) (SparseVector, error)
}

// knnQuery returns an explicitly embedded sparse $knn query payload.
func (vs *VectorStore) knnQuery(ctx context.Context, query string) (any, error) {
	if vs.opts.sparseEmbedder == nil {
		return nil, fmt.Errorf("chroma: sparse search is not configured")
	}
	vec, err := vs.opts.sparseEmbedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("chroma: sparse query embedder: %w", err)
	}
	return sparseVectorValue(vec)
}

func sparseVectorValue(vec SparseVector) (map[string]any, error) {
	if len(vec.Indices) != len(vec.Values) {
		return nil, fmt.Errorf("chroma: sparse embedder returned %d indices and %d values", len(vec.Indices), len(vec.Values))
	}
	previous := -1
	for i, index := range vec.Indices {
		if index < 0 || int64(index) > math.MaxInt32 {
			return nil, fmt.Errorf("chroma: sparse embedder returned invalid index %d at position %d", index, i)
		}
		if i > 0 && index <= previous {
			return nil, fmt.Errorf(
				"chroma: sparse embedder indices must be strictly increasing: index %d at position %d follows %d",
				index,
				i,
				previous,
			)
		}
		previous = index
		value := vec.Values[i]
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
			return nil, fmt.Errorf("chroma: sparse embedder returned invalid value at position %d", i)
		}
	}
	return map[string]any{
		"#type":   "sparse_vector",
		"indices": append([]int(nil), vec.Indices...),
		"values":  append([]float64(nil), vec.Values...),
	}, nil
}

func (vs *VectorStore) attachSparseEmbedding(ctx context.Context, rec storage.RecordBatch, content string) error {
	if !vs.opts.sparseSearch || content == "" {
		return nil
	}
	embedding, err := vs.opts.sparseEmbedder.EmbedDocument(ctx, content)
	if err != nil {
		return fmt.Errorf("chroma: sparse document embedding: %w", err)
	}
	vec, err := sparseVectorValue(embedding)
	if err != nil {
		return fmt.Errorf("chroma: sparse document embedding: %w", err)
	}
	if len(rec.Metadatas) == 0 || rec.Metadatas[0] == nil {
		return nil
	}
	rec.Metadatas[0][vs.opts.sparseSearchKey] = vec
	return nil
}

func isSparseVectorValue(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if t, _ := m["#type"].(string); t == "sparse_vector" {
		return true
	}
	_, hasIdx := m["indices"]
	_, hasVal := m["values"]
	return hasIdx && hasVal
}
