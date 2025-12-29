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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestAdd(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)
		doc := &document.Document{ID: "doc-1", Name: "Test", Content: "Content"}

		err := vs.Add(context.Background(), doc, make([]float64, testDimension))

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
	})

	t.Run("nil document returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		err := vs.Add(context.Background(), nil, make([]float64, testDimension))

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})

	t.Run("empty document ID returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		doc := &document.Document{ID: "", Name: "Test"}

		err := vs.Add(context.Background(), doc, make([]float64, testDimension))

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})

	t.Run("nil embedding returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		doc := &document.Document{ID: "doc-1"}

		err := vs.Add(context.Background(), doc, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "embedding is required")
	})

	t.Run("wrong dimension returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		doc := &document.Document{ID: "doc-1"}

		err := vs.Add(context.Background(), doc, []float64{0.1, 0.2})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected 4 dimensions")
	})

	t.Run("upsert error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.upsertError = errors.New("upsert failed")
		vs := newTestVectorStore(mock)
		doc := &document.Document{ID: "doc-1"}

		err := vs.Add(context.Background(), doc, make([]float64, testDimension))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "add document")
	})

	t.Run("with metadata", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)
		doc := &document.Document{
			ID:       "doc-1",
			Metadata: map[string]any{"category": "test"},
		}

		err := vs.Add(context.Background(), doc, make([]float64, testDimension))

		require.NoError(t, err)
	})
}

func TestAddBatch(t *testing.T) {
	t.Parallel()

	t.Run("success with multiple documents", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)
		docs := []*document.Document{
			{ID: "doc-1", Name: "Doc 1", Content: "Content 1"},
			{ID: "doc-2", Name: "Doc 2", Content: "Content 2"},
			{ID: "doc-3", Name: "Doc 3", Content: "Content 3"},
		}
		embeddings := [][]float64{
			make([]float64, testDimension),
			make([]float64, testDimension),
			make([]float64, testDimension),
		}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
		assert.Len(t, mock.documents, 3)
	})

	t.Run("empty documents list does nothing", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.AddBatch(context.Background(), []*document.Document{}, [][]float64{})

		require.NoError(t, err)
		assert.Equal(t, 0, mock.upsertCalls)
	})

	t.Run("mismatched counts returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		docs := []*document.Document{
			{ID: "doc-1"},
			{ID: "doc-2"},
		}
		embeddings := [][]float64{
			make([]float64, testDimension),
		}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "must match")
	})

	t.Run("nil document in list returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		docs := []*document.Document{
			{ID: "doc-1"},
			nil,
		}
		embeddings := [][]float64{
			make([]float64, testDimension),
			make([]float64, testDimension),
		}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "index 1 is nil")
	})

	t.Run("empty document ID returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		docs := []*document.Document{
			{ID: "doc-1"},
			{ID: ""},
		}
		embeddings := [][]float64{
			make([]float64, testDimension),
			make([]float64, testDimension),
		}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("invalid embedding dimension returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		docs := []*document.Document{
			{ID: "doc-1"},
			{ID: "doc-2"},
		}
		embeddings := [][]float64{
			make([]float64, testDimension),
			{0.1, 0.2}, // Wrong dimension
		}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "doc-2")
		assert.Contains(t, err.Error(), "expected 4 dimensions")
	})

	t.Run("upsert error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.upsertError = errors.New("upsert failed")
		vs := newTestVectorStore(mock)
		docs := []*document.Document{{ID: "doc-1"}}
		embeddings := [][]float64{make([]float64, testDimension)}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "add batch")
	})

	t.Run("with BM25 enabled", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStoreWithBM25(mock)
		docs := []*document.Document{
			{ID: "doc-1", Content: "Content for BM25"},
		}
		embeddings := [][]float64{make([]float64, testDimension)}

		err := vs.AddBatch(context.Background(), docs, embeddings)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
	})
}

func TestGet(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		docID := "doc-1"
		mock.documents[idToUUID(docID)] = &mockDoc{
			doc:       &document.Document{ID: docID, Name: "Test"},
			embedding: make([]float64, testDimension),
		}
		vs := newTestVectorStore(mock)

		doc, vec, err := vs.Get(context.Background(), docID)

		require.NoError(t, err)
		require.NotNil(t, doc)
		assert.Equal(t, docID, doc.ID)
		assert.Len(t, vec, testDimension)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		doc, vec, err := vs.Get(context.Background(), "")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
		assert.Nil(t, doc)
		assert.Nil(t, vec)
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		doc, vec, err := vs.Get(context.Background(), "non-existent")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
		assert.Nil(t, doc)
		assert.Nil(t, vec)
	})

	t.Run("get error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.getError = errors.New("get failed")
		vs := newTestVectorStore(mock)

		doc, vec, err := vs.Get(context.Background(), "doc-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get document")
		assert.Nil(t, doc)
		assert.Nil(t, vec)
	})
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)
		doc := &document.Document{ID: "doc-1", Name: "Updated"}

		err := vs.Update(context.Background(), doc, make([]float64, testDimension))

		require.NoError(t, err)
		assert.Equal(t, 1, mock.upsertCalls)
	})

	t.Run("nil document returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		err := vs.Update(context.Background(), nil, make([]float64, testDimension))

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})
}

func TestDelete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		docID := "doc-1"
		mock.documents[idToUUID(docID)] = &mockDoc{doc: &document.Document{ID: docID}}
		vs := newTestVectorStore(mock)

		err := vs.Delete(context.Background(), docID)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		err := vs.Delete(context.Background(), "")

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})

	t.Run("delete error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.Delete(context.Background(), "doc-1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete document")
	})
}

func TestDeleteByFilter(t *testing.T) {
	t.Parallel()

	t.Run("delete by IDs", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteDocumentIDs([]string{"doc-1", "doc-2"}),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("delete by filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("delete all", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteAll(true),
		)

		require.NoError(t, err)
		assert.Equal(t, 1, mock.deleteCalls)
	})

	t.Run("no options does nothing", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 0, mock.deleteCalls)
	})

	t.Run("delete by IDs error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteDocumentIDs([]string{"doc-1"}),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete by IDs")
	})

	t.Run("delete by filter error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteFilter(map[string]any{"key": "value"}),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete by filter")
	})

	t.Run("delete all error", func(t *testing.T) {
		mock := newMockClient()
		mock.deleteError = errors.New("delete failed")
		vs := newTestVectorStore(mock)

		err := vs.DeleteByFilter(context.Background(),
			vectorstore.WithDeleteAll(true),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete all")
	})
}

func TestExtractDenseVector(t *testing.T) {
	t.Parallel()

	t.Run("nil vectors returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		result := vs.extractDenseVector(nil)

		assert.Nil(t, result)
	})

	t.Run("extracts from named vectors (BM25 mode)", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		vectors := &qdrant.VectorsOutput{
			VectorsOptions: &qdrant.VectorsOutput_Vectors{
				Vectors: &qdrant.NamedVectorsOutput{
					Vectors: map[string]*qdrant.VectorOutput{
						vectorNameDense: {
							Vector: &qdrant.VectorOutput_Dense{
								Dense: &qdrant.DenseVector{Data: []float32{0.1, 0.2, 0.3, 0.4}},
							},
						},
					},
				},
			},
		}

		result := vs.extractDenseVector(vectors)

		require.Len(t, result, 4)
		assert.InDelta(t, 0.1, result[0], 0.001)
	})

	t.Run("extracts from single vector mode", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		vectors := &qdrant.VectorsOutput{
			VectorsOptions: &qdrant.VectorsOutput_Vector{
				Vector: &qdrant.VectorOutput{
					Vector: &qdrant.VectorOutput_Dense{
						Dense: &qdrant.DenseVector{Data: []float32{0.5, 0.6, 0.7, 0.8}},
					},
				},
			},
		}

		result := vs.extractDenseVector(vectors)

		require.Len(t, result, 4)
		assert.InDelta(t, 0.5, result[0], 0.001)
	})

	t.Run("extracts from single dense vector", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		vectors := &qdrant.VectorsOutput{
			VectorsOptions: &qdrant.VectorsOutput_Vector{
				Vector: &qdrant.VectorOutput{
					Vector: &qdrant.VectorOutput_Dense{
						Dense: &qdrant.DenseVector{Data: []float32{0.9, 0.8, 0.7, 0.6}},
					},
				},
			},
		}

		result := vs.extractDenseVector(vectors)

		require.Len(t, result, 4)
	})

	t.Run("returns nil for empty named vectors", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		vectors := &qdrant.VectorsOutput{
			VectorsOptions: &qdrant.VectorsOutput_Vectors{
				Vectors: &qdrant.NamedVectorsOutput{
					Vectors: map[string]*qdrant.VectorOutput{},
				},
			},
		}

		result := vs.extractDenseVector(vectors)

		assert.Nil(t, result)
	})
}

func TestBuildPoint(t *testing.T) {
	t.Parallel()

	t.Run("builds point without BM25", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		doc := &document.Document{ID: "test-doc", Content: "Test content"}

		point := vs.buildPoint(doc, make([]float64, testDimension))

		require.NotNil(t, point)
		assert.NotNil(t, point.Id)
		assert.NotNil(t, point.Payload)
		assert.NotNil(t, point.Vectors)
	})

	t.Run("builds point with BM25", func(t *testing.T) {
		vs := newTestVectorStoreWithBM25(newMockClient())
		doc := &document.Document{ID: "test-doc", Content: "Test content for BM25"}

		point := vs.buildPoint(doc, make([]float64, testDimension))

		require.NotNil(t, point)
		namedVectors := point.Vectors.GetVectors()
		require.NotNil(t, namedVectors)
		assert.Contains(t, namedVectors.Vectors, vectorNameDense)
		assert.Contains(t, namedVectors.Vectors, vectorNameSparse)
	})
}

func TestAddGetDeleteFlow(t *testing.T) {
	t.Parallel()

	mock := newMockClient()
	vs := newTestVectorStore(mock)

	// Add
	doc := &document.Document{
		ID:       "test-doc",
		Name:     "Test Document",
		Content:  "This is test content",
		Metadata: map[string]any{"category": "test"},
	}
	err := vs.Add(context.Background(), doc, make([]float64, testDimension))
	require.NoError(t, err)

	// Get
	retrieved, vec, err := vs.Get(context.Background(), "test-doc")
	require.NoError(t, err)
	assert.Equal(t, doc.ID, retrieved.ID)
	assert.Len(t, vec, testDimension)

	// Delete
	err = vs.Delete(context.Background(), "test-doc")
	require.NoError(t, err)

	// Verify counts
	assert.Equal(t, 1, mock.upsertCalls)
	assert.Equal(t, 1, mock.getCalls)
	assert.Equal(t, 1, mock.deleteCalls)
}
