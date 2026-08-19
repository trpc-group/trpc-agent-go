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
	first[0] = 99
	ctx = WithRequestEmbeddingCache(ctx)
	second, err := GetOrComputeRequestEmbedding(
		ctx, scope, "same text", compute,
	)
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
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

	assert.Equal(t, 2, calls)
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
