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

type requestEmbeddingCacheEntry struct {
	ready     chan struct{}
	embedding []float64
	err       error
	done      bool
}

type requestEmbeddingCache struct {
	mu      sync.Mutex
	entries map[requestEmbeddingCacheKey]*requestEmbeddingCacheEntry
}

// WithRequestEmbeddingCache enables exact embedding reuse for the lifetime of
// ctx. Reapplying it to an enabled context preserves the existing cache.
func WithRequestEmbeddingCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(requestEmbeddingCacheContextKey{}).(*requestEmbeddingCache); ok {
		return ctx
	}
	return context.WithValue(ctx, requestEmbeddingCacheContextKey{},
		&requestEmbeddingCache{
			entries: make(map[requestEmbeddingCacheKey]*requestEmbeddingCacheEntry),
		})
}

// GetOrComputeRequestEmbedding reuses an embedding for the exact (scope, text)
// key over the lifetime of the request cache in ctx. A nil or non-comparable
// scope bypasses the cache. Concurrent misses for the same key share one
// computation; a waiting caller may cancel independently through ctx. Failed
// computations are removed so a later call can retry. The returned embedding
// is shared and must be treated as read-only.
func GetOrComputeRequestEmbedding(
	ctx context.Context,
	scope any,
	text string,
	compute func() ([]float64, error),
) ([]float64, error) {
	cache, ok := ctx.Value(requestEmbeddingCacheContextKey{}).(*requestEmbeddingCache)
	scopeValue := reflect.ValueOf(scope)
	if !ok || cache == nil || !scopeValue.IsValid() ||
		!scopeValue.Comparable() {
		return compute()
	}

	key := requestEmbeddingCacheKey{scope: scope, text: text}
	cache.mu.Lock()
	entry, found := cache.entries[key]
	if found {
		if entry.done {
			value, err := entry.embedding, entry.err
			cache.mu.Unlock()
			return value, err
		}
		cache.mu.Unlock()
		select {
		case <-entry.ready:
			return entry.embedding, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entry = &requestEmbeddingCacheEntry{ready: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()

	value, err := compute()
	cache.mu.Lock()
	entry.embedding = value
	entry.err = err
	entry.done = true
	if err != nil {
		delete(cache.entries, key)
	}
	close(entry.ready)
	cache.mu.Unlock()
	return value, err
}
