//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMemoryEvents_UnmarshalDirect(t *testing.T) {
	var events createMemoryEvents
	require.NoError(t, events.UnmarshalJSON([]byte(`[{"id":"a","event_id":"e1","status":"SUCCEEDED"}]`)))
	require.Len(t, events, 1)
	assert.Equal(t, "a", events[0].ID)
	assert.Equal(t, "e1", events[0].EventID)
}

func TestCreateMemoryEvents_UnmarshalWrapped(t *testing.T) {
	var events createMemoryEvents
	require.NoError(t, events.UnmarshalJSON([]byte(`{"results":[{"id":"x","status":"PENDING"}]}`)))
	require.Len(t, events, 1)
	assert.Equal(t, "x", events[0].ID)
	assert.Equal(t, "PENDING", events[0].Status)
}

func TestCreateMemoryEvents_UnmarshalInvalid(t *testing.T) {
	var events createMemoryEvents
	assert.Error(t, events.UnmarshalJSON([]byte(`not-json`)))
}

func TestCreateMemoryEvents_ViaJSONUnmarshal(t *testing.T) {
	var events createMemoryEvents
	require.NoError(t, json.Unmarshal([]byte(`[{"id":"a"}]`), &events))
	require.Len(t, events, 1)
}

func TestListMemoriesResponse_UnmarshalDirectArray(t *testing.T) {
	var resp listMemoriesResponse
	require.NoError(t, resp.UnmarshalJSON([]byte(`[{"id":"a","memory":"m"}]`)))
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "a", resp.Results[0].ID)
}

func TestListMemoriesResponse_UnmarshalWrapped(t *testing.T) {
	var resp listMemoriesResponse
	body := `{"count":3,"results":[{"id":"x","memory":"y"}]}`
	require.NoError(t, resp.UnmarshalJSON([]byte(body)))
	assert.Equal(t, 3, resp.Count)
	require.Len(t, resp.Results, 1)
}

func TestListMemoriesResponse_UnmarshalInvalid(t *testing.T) {
	var resp listMemoriesResponse
	assert.Error(t, resp.UnmarshalJSON([]byte(`not-json`)))
}

func TestSearchV2Response_UnmarshalDirectArray(t *testing.T) {
	var resp searchV2Response
	body := `[{"id":"a","memory":"m","score":0.5}]`
	require.NoError(t, resp.UnmarshalJSON([]byte(body)))
	require.Len(t, resp.Memories, 1)
	assert.InDelta(t, 0.5, resp.Memories[0].Score, 1e-9)
}

func TestSearchV2Response_UnmarshalWrapped(t *testing.T) {
	var resp searchV2Response
	require.NoError(t, resp.UnmarshalJSON([]byte(`{"memories":[{"id":"a","memory":"m"}]}`)))
	require.Len(t, resp.Memories, 1)
}

func TestSearchV2Response_UnmarshalInvalid(t *testing.T) {
	var resp searchV2Response
	assert.Error(t, resp.UnmarshalJSON([]byte(`not-json`)))
}
