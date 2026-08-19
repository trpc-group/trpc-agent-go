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
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrComputeRequestEmbedding(t *testing.T) {
	ctx := WithRequestEmbeddingCache(context.Background())
	scope := new(int)
	calls := 0
	compute := func() ([]float64, error) {
		calls++
		return []float64{1, 2, 3}, nil
	}

	first, err := GetOrComputeRequestEmbedding(
		ctx, scope, "same text", compute,
	)
	require.NoError(t, err)
	ctx = WithRequestEmbeddingCache(ctx)
	second, err := GetOrComputeRequestEmbedding(
		ctx, scope, "same text", compute,
	)
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
	assert.Equal(t, []float64{1, 2, 3}, first)
	assert.Equal(t, []float64{1, 2, 3}, second)
}

func TestGetOrComputeRequestEmbeddingIsolation(t *testing.T) {
	ctx := WithRequestEmbeddingCache(context.Background())
	calls := 0
	compute := func() ([]float64, error) {
		calls++
		return []float64{float64(calls)}, nil
	}

	first, err := GetOrComputeRequestEmbedding(
		ctx, new(int), "same text", compute,
	)
	require.NoError(t, err)
	second, err := GetOrComputeRequestEmbedding(
		ctx, new(int), "same text", compute,
	)
	require.NoError(t, err)

	assert.Equal(t, 2, calls)
	assert.NotEqual(t, first, second)
}

func TestGetOrComputeRequestEmbeddingBypassesInvalidCache(t *testing.T) {
	calls := 0
	compute := func() ([]float64, error) {
		calls++
		return []float64{1}, nil
	}

	_, err := GetOrComputeRequestEmbedding(
		context.Background(), new(int), "same text", compute,
	)
	require.NoError(t, err)
	_, err = GetOrComputeRequestEmbedding(
		WithRequestEmbeddingCache(context.Background()),
		[]string{"not comparable"}, "same text", compute,
	)
	require.NoError(t, err)
	_, err = GetOrComputeRequestEmbedding(
		WithRequestEmbeddingCache(context.Background()),
		struct{ Value any }{Value: []byte("not comparable at runtime")},
		"same text", compute,
	)
	require.NoError(t, err)

	assert.Equal(t, 3, calls)
}

func TestGetOrComputeRequestEmbeddingDoesNotCacheErrors(t *testing.T) {
	ctx := WithRequestEmbeddingCache(context.Background())
	scope := new(int)
	calls := 0
	wantErr := errors.New("embedding failed")
	compute := func() ([]float64, error) {
		calls++
		return nil, wantErr
	}

	for i := 0; i < 2; i++ {
		_, err := GetOrComputeRequestEmbedding(
			ctx, scope, "same text", compute,
		)
		require.ErrorIs(t, err, wantErr)
	}
	assert.Equal(t, 2, calls)
}

func TestGetOrComputeRequestEmbeddingCoalescesConcurrentMisses(t *testing.T) {
	ctx := WithRequestEmbeddingCache(context.Background())
	scope := new(int)
	computeStarted := make(chan struct{})
	releaseCompute := make(chan struct{})
	var calls atomic.Int32
	type result struct {
		embedding []float64
		err       error
	}
	firstResult := make(chan result, 1)
	go func() {
		embedding, err := GetOrComputeRequestEmbedding(
			ctx, scope, "same text", func() ([]float64, error) {
				calls.Add(1)
				close(computeStarted)
				<-releaseCompute
				return []float64{1, 2, 3}, nil
			},
		)
		firstResult <- result{embedding: embedding, err: err}
	}()
	<-computeStarted

	waiterCtx, cancelWaiter := context.WithCancel(ctx)
	waiterStarted := make(chan struct{})
	waiterResult := make(chan error, 1)
	go func() {
		close(waiterStarted)
		_, err := GetOrComputeRequestEmbedding(
			waiterCtx, scope, "same text", func() ([]float64, error) {
				calls.Add(1)
				return nil, errors.New("duplicate computation")
			},
		)
		waiterResult <- err
	}()
	<-waiterStarted
	cancelWaiter()

	require.ErrorIs(t, <-waiterResult, context.Canceled)
	assert.Equal(t, int32(1), calls.Load())

	close(releaseCompute)
	got := <-firstResult
	require.NoError(t, got.err)
	assert.Equal(t, []float64{1, 2, 3}, got.embedding)
	assert.Equal(t, int32(1), calls.Load())
}

func BenchmarkGetOrComputeRequestEmbedding(b *testing.B) {
	embedding := make([]float64, 1536)
	compute := func() ([]float64, error) {
		return embedding, nil
	}
	scope := new(int)

	b.Run("disabled", func(b *testing.B) {
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			_, _ = GetOrComputeRequestEmbedding(
				ctx, scope, "same text", compute,
			)
		}
	})
	b.Run("miss", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ctx := WithRequestEmbeddingCache(context.Background())
			_, _ = GetOrComputeRequestEmbedding(
				ctx, scope, "same text", compute,
			)
		}
	})
	b.Run("hit", func(b *testing.B) {
		ctx := WithRequestEmbeddingCache(context.Background())
		_, _ = GetOrComputeRequestEmbedding(
			ctx, scope, "same text", compute,
		)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = GetOrComputeRequestEmbedding(
				ctx, scope, "same text", compute,
			)
		}
	})
}
