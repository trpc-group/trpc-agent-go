//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := defaultOptions

	assert.Equal(t, "trpc_agent_documents", opts.collectionName)
	assert.Equal(t, 1536, opts.dimension)
	assert.Equal(t, DistanceCosine, opts.distance)
	assert.Equal(t, 16, opts.hnswM)
	assert.Equal(t, 128, opts.hnswEfConstruct)
	assert.Equal(t, 10, opts.maxResults)
	assert.Equal(t, 3, opts.maxRetries)
	assert.Equal(t, 100*time.Millisecond, opts.baseRetryDelay)
	assert.Equal(t, 5*time.Second, opts.maxRetryDelay)
	assert.Equal(t, 2, opts.prefetchMultiplier)
	assert.False(t, opts.onDiskVectors)
	assert.False(t, opts.onDiskPayload)
	assert.Empty(t, opts.clientBuilderOpts)
}

func TestWithHost(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithHost("custom-host")(&opts)
	assert.Len(t, opts.clientBuilderOpts, 1)
}

func TestWithPort(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithPort(9999)(&opts)
	assert.Len(t, opts.clientBuilderOpts, 1)
}

func TestWithCollectionName(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithCollectionName("my_collection")(&opts)
	assert.Equal(t, "my_collection", opts.collectionName)
}

func TestWithDimension(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithDimension(768)(&opts)
	assert.Equal(t, 768, opts.dimension)
}

func TestWithAPIKey(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithAPIKey("secret-key")(&opts)
	assert.Len(t, opts.clientBuilderOpts, 1)
}

func TestWithTLS(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithTLS(true)(&opts)
	assert.Len(t, opts.clientBuilderOpts, 1)
}

func TestWithDistance(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithDistance(DistanceEuclid)(&opts)
	assert.Equal(t, DistanceEuclid, opts.distance)

	WithDistance(DistanceDot)(&opts)
	assert.Equal(t, DistanceDot, opts.distance)
}

func TestWithHNSWConfig(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithHNSWConfig(32, 256)(&opts)
	assert.Equal(t, 32, opts.hnswM)
	assert.Equal(t, 256, opts.hnswEfConstruct)
}

func TestWithOnDiskVectors(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithOnDiskVectors(true)(&opts)
	assert.True(t, opts.onDiskVectors)
}

func TestWithOnDiskPayload(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithOnDiskPayload(true)(&opts)
	assert.True(t, opts.onDiskPayload)
}

func TestWithMaxResults(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithMaxResults(50)(&opts)
	assert.Equal(t, 50, opts.maxResults)
}

func TestWithMaxRetries(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithMaxRetries(5)(&opts)
	assert.Equal(t, 5, opts.maxRetries)

	WithMaxRetries(0)(&opts)
	assert.Equal(t, 0, opts.maxRetries)
}

func TestMultipleOptions(t *testing.T) {
	t.Parallel()
	opts := defaultOptions

	options := []Option{
		WithHost("qdrant.example.com"),
		WithPort(6335),
		WithAPIKey("my-api-key"),
		WithTLS(true),
		WithCollectionName("test_collection"),
		WithDimension(3072),
		WithDistance(DistanceDot),
		WithHNSWConfig(64, 512),
		WithOnDiskVectors(true),
		WithOnDiskPayload(true),
		WithMaxResults(100),
		WithMaxRetries(5),
	}

	for _, opt := range options {
		opt(&opts)
	}

	// Connection options are forwarded to storage layer
	assert.Len(t, opts.clientBuilderOpts, 4) // host, port, apiKey, tls
	assert.Equal(t, "test_collection", opts.collectionName)
	assert.Equal(t, 3072, opts.dimension)
	assert.Equal(t, DistanceDot, opts.distance)
	assert.Equal(t, 64, opts.hnswM)
	assert.Equal(t, 512, opts.hnswEfConstruct)
	assert.True(t, opts.onDiskVectors)
	assert.True(t, opts.onDiskPayload)
	assert.Equal(t, 100, opts.maxResults)
	assert.Equal(t, 5, opts.maxRetries)
}

func TestOptionsValidation(t *testing.T) {
	t.Parallel()
	t.Run("empty collection name ignored", func(t *testing.T) {
		opts := defaultOptions
		WithCollectionName("")(&opts)
		assert.Equal(t, defaultCollectionName, opts.collectionName)
	})

	t.Run("invalid dimension ignored", func(t *testing.T) {
		opts := defaultOptions
		WithDimension(-1)(&opts)
		assert.Equal(t, defaultDimension, opts.dimension)

		WithDimension(0)(&opts)
		assert.Equal(t, defaultDimension, opts.dimension)
	})

	t.Run("invalid HNSW config ignored", func(t *testing.T) {
		opts := defaultOptions
		WithHNSWConfig(-1, -1)(&opts)
		assert.Equal(t, defaultHNSWM, opts.hnswM)
		assert.Equal(t, defaultHNSWEfConstruct, opts.hnswEfConstruct)

		WithHNSWConfig(32, 0)(&opts)
		assert.Equal(t, 32, opts.hnswM)
		assert.Equal(t, defaultHNSWEfConstruct, opts.hnswEfConstruct)
	})

	t.Run("invalid max results ignored", func(t *testing.T) {
		opts := defaultOptions
		WithMaxResults(-1)(&opts)
		assert.Equal(t, defaultMaxResults, opts.maxResults)

		WithMaxResults(0)(&opts)
		assert.Equal(t, defaultMaxResults, opts.maxResults)
	})

	t.Run("negative max retries ignored", func(t *testing.T) {
		opts := defaultOptions
		WithMaxRetries(-1)(&opts)
		assert.Equal(t, defaultMaxRetries, opts.maxRetries)
	})

	t.Run("invalid base retry delay ignored", func(t *testing.T) {
		opts := defaultOptions
		WithBaseRetryDelay(0)(&opts)
		assert.Equal(t, defaultBaseRetryDelay, opts.baseRetryDelay)

		WithBaseRetryDelay(-1 * time.Second)(&opts)
		assert.Equal(t, defaultBaseRetryDelay, opts.baseRetryDelay)
	})

	t.Run("invalid max retry delay ignored", func(t *testing.T) {
		opts := defaultOptions
		WithMaxRetryDelay(0)(&opts)
		assert.Equal(t, defaultMaxRetryDelay, opts.maxRetryDelay)

		WithMaxRetryDelay(-1 * time.Second)(&opts)
		assert.Equal(t, defaultMaxRetryDelay, opts.maxRetryDelay)
	})
}

func TestWithBaseRetryDelay(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithBaseRetryDelay(200 * time.Millisecond)(&opts)
	assert.Equal(t, 200*time.Millisecond, opts.baseRetryDelay)
}

func TestWithMaxRetryDelay(t *testing.T) {
	t.Parallel()
	opts := defaultOptions
	WithMaxRetryDelay(10 * time.Second)(&opts)
	assert.Equal(t, 10*time.Second, opts.maxRetryDelay)
}

func TestWithClient(t *testing.T) {
	t.Parallel()
	opts := defaultOptions

	WithClient(nil)(&opts)
	assert.Nil(t, opts.client)
}

func TestWithPrefetchMultiplier(t *testing.T) {
	t.Parallel()

	t.Run("sets valid multiplier", func(t *testing.T) {
		opts := defaultOptions
		WithPrefetchMultiplier(5)(&opts)
		assert.Equal(t, 5, opts.prefetchMultiplier)
	})

	t.Run("ignores zero", func(t *testing.T) {
		opts := defaultOptions
		WithPrefetchMultiplier(0)(&opts)
		assert.Equal(t, 2, opts.prefetchMultiplier)
	})

	t.Run("ignores negative", func(t *testing.T) {
		opts := defaultOptions
		WithPrefetchMultiplier(-1)(&opts)
		assert.Equal(t, 2, opts.prefetchMultiplier)
	})
}
