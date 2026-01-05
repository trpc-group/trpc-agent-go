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
	"errors"
	"testing"

	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("success with valid config", func(t *testing.T) {
		mock := newMockClient()

		vs, err := New(context.Background(),
			WithClient(mock),
			WithDimension(testDimension),
			WithCollectionName("test-collection"),
		)

		require.NoError(t, err)
		require.NotNil(t, vs)
		assert.False(t, vs.ownsClient)
	})

	t.Run("ensureCollection failure returns error", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, errors.New("connection failed")
		}

		vs, err := New(context.Background(),
			WithClient(mock),
			WithDimension(testDimension),
			WithCollectionName("test-collection"),
		)

		require.Error(t, err)
		assert.Nil(t, vs)
	})

	t.Run("dimension mismatch with existing collection", func(t *testing.T) {
		mock := newMockClient()
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return &qdrant.CollectionInfo{
				Config: &qdrant.CollectionConfig{
					Params: &qdrant.CollectionParams{
						VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
							Size:     4,
							Distance: qdrant.Distance_Cosine,
						}),
					},
				},
			}, nil
		}

		vs, err := New(context.Background(),
			WithClient(mock),
			WithDimension(128),
			WithCollectionName("test-collection"),
		)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Nil(t, vs)
	})

	t.Run("create collection failure", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			return errors.New("create failed")
		}
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return nil, errors.New("get info failed")
		}

		vs, err := New(context.Background(),
			WithClient(mock),
			WithDimension(testDimension),
			WithCollectionName("test-collection"),
		)

		require.Error(t, err)
		assert.Nil(t, vs)
	})

	t.Run("dimension mismatch detected during validation", func(t *testing.T) {
		mock := newMockClient()
		// Collection has dimension 4, but we request 1536 (default)
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return &qdrant.CollectionInfo{
				Config: &qdrant.CollectionConfig{
					Params: &qdrant.CollectionParams{
						VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
							Size:     4, // Different from default 1536
							Distance: qdrant.Distance_Cosine,
						}),
					},
				},
			}, nil
		}

		vs, err := New(context.Background(),
			WithClient(mock),
			// Default dimension is 1536, collection has 4
		)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Nil(t, vs)
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("closes owned client", func(t *testing.T) {
		mock := newMockClient()
		vs := &VectorStore{
			client:          mock,
			ownsClient:      true,
			opts:            testOptions,
			filterConverter: newFilterConverter(),
		}

		err := vs.Close()

		require.NoError(t, err)
		assert.Equal(t, 1, mock.closeCalls)
	})

	t.Run("does not close external client", func(t *testing.T) {
		mock := newMockClient()
		vs := &VectorStore{
			client:          mock,
			ownsClient:      false,
			opts:            testOptions,
			filterConverter: newFilterConverter(),
		}

		err := vs.Close()

		require.NoError(t, err)
		assert.Equal(t, 0, mock.closeCalls)
	})

	t.Run("multiple close calls are safe", func(t *testing.T) {
		mock := newMockClient()
		vs := &VectorStore{
			client:          mock,
			ownsClient:      true,
			opts:            testOptions,
			filterConverter: newFilterConverter(),
		}

		err1 := vs.Close()
		err2 := vs.Close()

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.Equal(t, 1, mock.closeCalls) // Only closed once
	})

	t.Run("nil client is safe", func(t *testing.T) {
		vs := &VectorStore{
			client:     nil,
			ownsClient: true,
		}

		err := vs.Close()

		require.NoError(t, err)
	})

	t.Run("close error is propagated", func(t *testing.T) {
		mock := newMockClient()
		mock.CloseFn = func() error {
			return errors.New("close failed")
		}
		vs := &VectorStore{
			client:          mock,
			ownsClient:      true,
			opts:            testOptions,
			filterConverter: newFilterConverter(),
		}

		err := vs.Close()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "close failed")
	})
}
