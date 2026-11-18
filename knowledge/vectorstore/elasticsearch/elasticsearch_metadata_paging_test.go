//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// pagingClient serves Search like Elasticsearch does for metadata scans: it
// rejects from+size beyond max_result_window and supports sort plus
// search_after on the "id" field.
type pagingClient struct {
	*mockClient
	ids             []string
	invalid         map[string]bool
	maxResultWindow int
	requests        []map[string]any
}

func newPagingClient(n int) *pagingClient {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%06d", i)
	}
	// Index order is not ID order.
	sort.Slice(ids, func(i, j int) bool { return ids[i][len(ids[i])-1] < ids[j][len(ids[j])-1] })
	return &pagingClient{
		mockClient:      newMockClient(),
		ids:             ids,
		invalid:         map[string]bool{},
		maxResultWindow: 10000,
	}
}

func (c *pagingClient) Search(_ context.Context, _ string, body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	c.requests = append(c.requests, req)

	size, from := 10, 0
	if v, ok := req["size"].(float64); ok {
		size = int(v)
	}
	if v, ok := req["from"].(float64); ok {
		from = int(v)
	}
	if from+size > c.maxResultWindow {
		return nil, fmt.Errorf("Result window is too large, from + size must be less than or equal to: [%d]", c.maxResultWindow)
	}

	ids := append([]string(nil), c.ids...)
	if _, ok := req["sort"]; ok {
		sort.Strings(ids)
	}
	if after, ok := req["search_after"].([]any); ok && len(after) > 0 {
		last := after[0].(string)
		start := sort.SearchStrings(ids, last)
		for start < len(ids) && ids[start] <= last {
			start++
		}
		ids = ids[start:]
	}
	if from > len(ids) {
		from = len(ids)
	}
	ids = ids[from:]
	if size < len(ids) {
		ids = ids[:size]
	}

	hits := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		source := map[string]any{"id": id, "content": "c", "metadata": map[string]any{"k": id}}
		if c.invalid[id] {
			source = map[string]any{"id": id, "content": "c"}
		}
		hits = append(hits, map[string]any{"_id": id, "_source": source, "sort": []any{id}})
	}
	return json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
}

func newPagingVectorStore(t *testing.T, c *pagingClient) *VectorStore {
	t.Helper()
	vs := newTestVectorStore(t, c.mockClient)
	vs.client = c
	return vs
}

func TestGetMetadataReadsMoreThanMaxResultWindow(t *testing.T) {
	c := newPagingClient(12345)
	vs := newPagingVectorStore(t, c)

	metadata, err := vs.GetMetadata(context.Background())
	require.NoError(t, err)
	assert.Len(t, metadata, 12345)
	for _, req := range c.requests {
		assert.NotContains(t, req, "from")
	}
}

func TestGetMetadataDoesNotStopAtSkippedDocuments(t *testing.T) {
	c := newPagingClient(5010)
	// A document without metadata is skipped in the first batch.
	sorted := append([]string(nil), c.ids...)
	sort.Strings(sorted)
	c.invalid[sorted[0]] = true
	vs := newPagingVectorStore(t, c)

	metadata, err := vs.GetMetadata(context.Background())
	require.NoError(t, err)
	assert.Len(t, metadata, 5009)
	assert.Contains(t, metadata, sorted[len(sorted)-1])
}

func TestGetMetadataPagesAreOrderedByID(t *testing.T) {
	c := newPagingClient(30)
	vs := newPagingVectorStore(t, c)

	seen := map[string]bool{}
	for offset := 0; offset < 30; offset += 7 {
		page, err := vs.GetMetadata(
			context.Background(),
			vectorstore.WithGetMetadataLimit(7),
			vectorstore.WithGetMetadataOffset(offset),
		)
		require.NoError(t, err)
		for id := range page {
			assert.False(t, seen[id], "document %s returned twice", id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, 30)
	assert.Equal(t, []any{map[string]any{"id": map[string]any{"order": "asc"}}}, c.requests[0]["sort"])
}
