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

func TestCount(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{doc: &document.Document{ID: "doc-1"}}
		mock.documents[idToUUID("doc-2")] = &mockDoc{doc: &document.Document{ID: "doc-2"}}
		vs := newTestVectorStore(mock)

		count, err := vs.Count(context.Background())

		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("with filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		count, err := vs.Count(context.Background(),
			vectorstore.WithCountFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 0)
	})

	t.Run("count error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.countError = errors.New("count failed")
		vs := newTestVectorStore(mock)

		count, err := vs.Count(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "count documents")
		assert.Equal(t, 0, count)
	})
}

func TestGetMetadata(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		mock.documents[idToUUID("doc-1")] = &mockDoc{
			doc: &document.Document{ID: "doc-1"},
		}
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background())

		require.NoError(t, err)
		assert.NotNil(t, metadata)
		assert.Equal(t, 1, mock.scrollCalls)
	})

	t.Run("with IDs filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataIDs([]string{"doc-1", "doc-2"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, metadata)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataFilter(map[string]any{"category": "test"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, metadata)
	})

	t.Run("with limit", func(t *testing.T) {
		mock := newMockClient()
		var capturedLimit uint32
		mock.ScrollFn = func(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
			capturedLimit = *req.Limit
			return []*qdrant.RetrievedPoint{}, nil
		}
		vs := newTestVectorStore(mock)

		_, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataLimit(10),
		)

		require.NoError(t, err)
		assert.Equal(t, uint32(10), capturedLimit)
	})

	t.Run("scroll error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.scrollError = errors.New("scroll failed")
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "get metadata")
		assert.Nil(t, metadata)
	})

	t.Run("pagination stops when limit reached", func(t *testing.T) {
		mock := newMockClient()
		callCount := 0
		mock.ScrollFn = func(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
			callCount++
			results := make([]*qdrant.RetrievedPoint, int(*req.Limit))
			for i := range results {
				id := "doc-" + string(rune('a'+callCount)) + string(rune('a'+i))
				results[i] = &qdrant.RetrievedPoint{
					Id: qdrant.NewID(idToUUID(id)),
					Payload: map[string]*qdrant.Value{
						fieldID: {Kind: &qdrant.Value_StringValue{StringValue: id}},
					},
				}
			}
			return results, nil
		}
		vs := newTestVectorStore(mock)

		metadata, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataLimit(5),
		)

		require.NoError(t, err)
		assert.Equal(t, 5, len(metadata))
	})
}

func TestBuildMetadataFilter(t *testing.T) {
	t.Parallel()

	t.Run("with IDs", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &vectorstore.GetMetadataConfig{IDs: []string{"id1", "id2"}}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		require.NotNil(t, filter)
		require.Len(t, filter.Must, 1)
	})

	t.Run("with metadata filter", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &vectorstore.GetMetadataConfig{Filter: map[string]any{"key": "value"}}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		require.NotNil(t, filter)
	})

	t.Run("empty config returns nil", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())
		config := &vectorstore.GetMetadataConfig{}

		filter, err := vs.buildMetadataFilter(config)

		require.NoError(t, err)
		assert.Nil(t, filter)
	})
}

func TestUpdateMetadata(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		mock := newMockClient()
		docID := "doc-1"
		mock.documents[idToUUID(docID)] = &mockDoc{
			doc: &document.Document{ID: docID},
		}
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), docID, map[string]any{
			"category": "updated",
			"priority": 1,
		})

		require.NoError(t, err)
		assert.Equal(t, 1, mock.setPayloadCalls)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		vs := newTestVectorStore(newMockClient())

		err := vs.UpdateMetadata(context.Background(), "", map[string]any{"key": "value"})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidInput))
	})

	t.Run("empty metadata does nothing", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), "doc-1", map[string]any{})

		require.NoError(t, err)
		assert.Equal(t, 0, mock.setPayloadCalls)
	})

	t.Run("nil metadata does nothing", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), "doc-1", nil)

		require.NoError(t, err)
		assert.Equal(t, 0, mock.setPayloadCalls)
	})

	t.Run("set payload error is wrapped", func(t *testing.T) {
		mock := newMockClient()
		mock.setPayloadError = errors.New("set payload failed")
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), "doc-1", map[string]any{"key": "value"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "update metadata")
	})

	t.Run("metadata key is prefixed", func(t *testing.T) {
		mock := newMockClient()
		var capturedPayload map[string]*qdrant.Value
		mock.SetPayloadFn = func(ctx context.Context, req *qdrant.SetPayloadPoints) (*qdrant.UpdateResult, error) {
			capturedPayload = req.Payload
			return &qdrant.UpdateResult{}, nil
		}
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), "doc-1", map[string]any{
			"category": "test",
		})

		require.NoError(t, err)
		assert.Contains(t, capturedPayload, "metadata.category")
	})

	t.Run("various metadata types", func(t *testing.T) {
		mock := newMockClient()
		vs := newTestVectorStore(mock)

		err := vs.UpdateMetadata(context.Background(), "doc-1", map[string]any{
			"string": "value",
			"int":    42,
			"float":  3.14,
			"bool":   true,
			"list":   []any{"a", "b"},
			"nested": map[string]any{"inner": "value"},
			"nilval": nil,
		})

		require.NoError(t, err)
		assert.Equal(t, 1, mock.setPayloadCalls)
	})
}
