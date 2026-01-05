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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestEnsureCollection(t *testing.T) {
	t.Parallel()

	t.Run("collection exists - validates config", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return true, nil
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err)
	})

	t.Run("collection does not exist - creates it", func(t *testing.T) {
		mock := newMockClient()
		createCalled := false
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			createCalled = true
			assert.Equal(t, defaultCollectionName, req.CollectionName)
			return nil
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err)
		assert.True(t, createCalled)
	})

	t.Run("check exists error", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, errors.New("connection error")
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "check collection")
	})

	t.Run("create collection error", func(t *testing.T) {
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
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get collection")
	})

	t.Run("handles already exists race condition", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			return errors.New("collection already exists")
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err) // Should succeed by validating existing collection
	})

	t.Run("handles AlreadyExists error", func(t *testing.T) {
		mock := newMockClient()
		mock.CollectionExistsFn = func(ctx context.Context, name string) (bool, error) {
			return false, nil
		}
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			return errors.New("AlreadyExists: collection test exists")
		}
		vs := newTestVectorStore(mock)

		err := vs.ensureCollection(context.Background())

		require.NoError(t, err)
	})
}

func TestCreateCollection(t *testing.T) {
	t.Parallel()

	t.Run("creates collection without BM25", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.CreateCollection
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			capturedReq = req
			return nil
		}
		vs := newTestVectorStore(mock)

		err := vs.createCollection(context.Background())

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		assert.Equal(t, defaultCollectionName, capturedReq.CollectionName)
		assert.NotNil(t, capturedReq.VectorsConfig.GetParams())
	})

	t.Run("creates collection with BM25", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.CreateCollection
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			capturedReq = req
			return nil
		}
		vs := newTestVectorStoreWithBM25(mock)

		err := vs.createCollection(context.Background())

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		assert.NotNil(t, capturedReq.VectorsConfig.GetParamsMap())
		assert.NotNil(t, capturedReq.SparseVectorsConfig)
	})

	t.Run("creates collection with on-disk vectors", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.CreateCollection
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			capturedReq = req
			return nil
		}
		vs := &VectorStore{
			client:          mock,
			ownsClient:      true,
			opts:            options{collectionName: "test", dimension: testDimension, distance: qdrant.Distance_Cosine, onDiskVectors: true},
			filterConverter: newFilterConverter(),
			retryCfg:        retryConfig{maxRetries: 0},
		}

		err := vs.createCollection(context.Background())

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		assert.True(t, *capturedReq.VectorsConfig.GetParams().OnDisk)
	})

	t.Run("creates collection with BM25 and on-disk vectors", func(t *testing.T) {
		mock := newMockClient()
		var capturedReq *qdrant.CreateCollection
		mock.CreateCollectionFn = func(ctx context.Context, req *qdrant.CreateCollection) error {
			capturedReq = req
			return nil
		}
		vs := &VectorStore{
			client:     mock,
			ownsClient: true,
			opts: options{
				collectionName: "test",
				dimension:      testDimension,
				distance:       qdrant.Distance_Cosine,
				onDiskVectors:  true,
				bm25Enabled:    true,
			},
			filterConverter: newFilterConverter(),
			retryCfg:        retryConfig{maxRetries: 0},
		}

		err := vs.createCollection(context.Background())

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		denseParams := capturedReq.VectorsConfig.GetParamsMap().Map[vectorNameDense]
		assert.True(t, *denseParams.OnDisk)
	})
}

func TestValidateCollectionConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil config returns error", func(t *testing.T) {
		mock := newMockClient()
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return &qdrant.CollectionInfo{Config: nil}, nil
		}
		vs := newTestVectorStore(mock)

		err := vs.validateCollectionConfig(context.Background())

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Contains(t, err.Error(), "no configuration")
	})

	t.Run("nil params returns error", func(t *testing.T) {
		mock := newMockClient()
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return &qdrant.CollectionInfo{
				Config: &qdrant.CollectionConfig{Params: nil},
			}, nil
		}
		vs := newTestVectorStore(mock)

		err := vs.validateCollectionConfig(context.Background())

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
	})

	t.Run("get collection info error", func(t *testing.T) {
		mock := newMockClient()
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return nil, errors.New("connection failed")
		}
		vs := newTestVectorStore(mock)

		err := vs.validateCollectionConfig(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get collection")
	})

	t.Run("validates BM25 sparse config when enabled", func(t *testing.T) {
		mock := newMockClient()
		mock.GetCollectionInfoFn = func(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
			return &qdrant.CollectionInfo{
				Config: &qdrant.CollectionConfig{
					Params: &qdrant.CollectionParams{
						VectorsConfig: &qdrant.VectorsConfig{
							Config: &qdrant.VectorsConfig_ParamsMap{
								ParamsMap: &qdrant.VectorParamsMap{
									Map: map[string]*qdrant.VectorParams{
										vectorNameDense: {Size: testDimension, Distance: qdrant.Distance_Cosine},
									},
								},
							},
						},
						// Missing sparse config
					},
				},
			}, nil
		}
		vs := newTestVectorStoreWithBM25(mock)

		err := vs.validateCollectionConfig(context.Background())

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Contains(t, err.Error(), "no sparse vectors config")
	})
}

func TestValidateVectorConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil config returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		err := vs.validateVectorConfig(nil)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Contains(t, err.Error(), "no vectors config")
	})

	t.Run("nil params in single vector mode", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_Params{Params: nil},
		}

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil vector params")
	})

	t.Run("nil params map in named vectors mode", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_ParamsMap{ParamsMap: nil},
		}

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
	})

	t.Run("missing dense vector in params map", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_ParamsMap{
				ParamsMap: &qdrant.VectorParamsMap{
					Map: map[string]*qdrant.VectorParams{
						"other": {Size: 128, Distance: qdrant.Distance_Cosine},
					},
				},
			},
		}

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("nil dense params in map", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_ParamsMap{
				ParamsMap: &qdrant.VectorParamsMap{
					Map: map[string]*qdrant.VectorParams{vectorNameDense: nil},
				},
			},
		}

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil dense params")
	})

	t.Run("dimension mismatch", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     1536, // different from testDimension
			Distance: qdrant.Distance_Cosine,
		})

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected dimension")
	})

	t.Run("distance mismatch", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     testDimension,
			Distance: qdrant.Distance_Euclid, // different from Cosine
		})

		err := vs.validateVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected distance")
	})

	t.Run("valid single vector config", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     testDimension,
			Distance: qdrant.Distance_Cosine,
		})

		err := vs.validateVectorConfig(config)

		require.NoError(t, err)
	})

	t.Run("valid named vectors config", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_ParamsMap{
				ParamsMap: &qdrant.VectorParamsMap{
					Map: map[string]*qdrant.VectorParams{
						vectorNameDense: {Size: testDimension, Distance: qdrant.Distance_Cosine},
					},
				},
			},
		}

		err := vs.validateVectorConfig(config)

		require.NoError(t, err)
	})
}

func TestValidateSparseVectorConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil config returns error", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())

		err := vs.validateSparseVectorConfig(nil)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
		assert.Contains(t, err.Error(), "no sparse vectors config")
	})

	t.Run("nil map returns error", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())
		config := &qdrant.SparseVectorConfig{Map: nil}

		err := vs.validateSparseVectorConfig(config)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionMismatch))
	})

	t.Run("missing sparse vector returns error", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())
		config := &qdrant.SparseVectorConfig{
			Map: map[string]*qdrant.SparseVectorParams{"other": {}},
		}

		err := vs.validateSparseVectorConfig(config)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("valid config", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())
		config := &qdrant.SparseVectorConfig{
			Map: map[string]*qdrant.SparseVectorParams{vectorNameSparse: {}},
		}

		err := vs.validateSparseVectorConfig(config)

		require.NoError(t, err)
	})
}

func TestDeleteCollection(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{doc: &document.Document{ID: "doc-1"}}
		mock.documents[idToUUID("doc-2")] = &mockDoc{doc: &document.Document{ID: "doc-2"}}
		vs := newTestVectorStore(mock)

		err := vs.DeleteCollection(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCollectionCalls)
		assert.Empty(t, mock.documents)
	})

	t.Run("error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteCollectionError = errors.New("delete collection failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteCollection(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete collection")
	})

	t.Run("uses correct collection name", func(t *testing.T) {
		mock := newMockClient()
		var capturedName string
		mock.DeleteCollectionFn = func(ctx context.Context, name string) error {
			capturedName = name
			return nil
		}
		vs := newTestVectorStore(mock)

		err := vs.DeleteCollection(context.Background())

		require.NoError(t, err)
		assert.Equal(t, testOptions.collectionName, capturedName)
	})
}
