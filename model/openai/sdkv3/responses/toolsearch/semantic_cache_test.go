//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsearch

import (
	"context"
	"crypto/sha256"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
)

type cacheTestEmbedder struct {
	embedder.Embedder
}

func (cacheTestEmbedder) GetEmbedding(context.Context, string) ([]float64, error) {
	return []float64{1, 0}, nil
}

func TestSemanticCandidateCacheBounds(t *testing.T) {
	search := &semanticSearch{
		embedder: cacheTestEmbedder{},
		vectors:  make(map[[sha256.Size]byte][]float64),
	}
	for i := range semanticCacheCapacity + 1 {
		_, err := search.candidateVector(context.Background(), strconv.Itoa(i), 2)
		require.NoError(t, err)
	}
	require.Len(t, search.vectors, semanticCacheCapacity)
}
