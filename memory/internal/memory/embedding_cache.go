//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"reflect"
	"sync"
)

type requestEmbeddingCacheContextKey struct{}

type requestEmbeddingCacheKey struct {
	scope any
	text  string
}

type requestEmbeddingCache struct {
	mu     sync.RWMutex
	values map[requestEmbeddingCacheKey][]float64
}

// WithRequestEmbeddingCache enables exact embedding reuse for the lifetime of
// ctx. Reapplying it to an enabled context preserves the existing cache.
func WithRequestEmbeddingCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(requestEmbeddingCacheContextKey{}).(*requestEmbeddingCache); ok {
		return ctx
	}
	return context.WithValue(ctx, requestEmbeddingCacheContextKey{},
		&requestEmbeddingCache{
			values: make(map[requestEmbeddingCacheKey][]float64),
		})
}

// GetOrComputeRequestEmbedding reuses an exact embedding within one
// auto-memory request. Scope must be a comparable service identity; invalid
// scopes bypass the cache. Failed computations are never cached.
func GetOrComputeRequestEmbedding(
	ctx context.Context,
	scope any,
	text string,
	compute func() ([]float64, error),
) ([]float64, error) {
	cache, ok := ctx.Value(requestEmbeddingCacheContextKey{}).(*requestEmbeddingCache)
	if !ok || cache == nil || scope == nil ||
		!reflect.TypeOf(scope).Comparable() {
		return compute()
	}

	key := requestEmbeddingCacheKey{scope: scope, text: text}
	cache.mu.RLock()
	value, found := cache.values[key]
	cache.mu.RUnlock()
	if found {
		return cloneEmbedding(value), nil
	}

	value, err := compute()
	if err != nil {
		return nil, err
	}
	stored := cloneEmbedding(value)
	cache.mu.Lock()
	if existing, exists := cache.values[key]; exists {
		stored = existing
	} else {
		cache.values[key] = stored
	}
	cache.mu.Unlock()
	return cloneEmbedding(stored), nil
}

func cloneEmbedding(value []float64) []float64 {
	return append([]float64(nil), value...)
}
