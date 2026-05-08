//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package jsonmap

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloneNil(t *testing.T) {
	require.Nil(t, Clone(nil))
}

func TestCloneDeepCopiesJSONValues(t *testing.T) {
	src := map[string]any{
		"prompt_cache_key": "cache-1",
		"seed":             int64(9007199254740993),
		"metadata": map[string]any{
			"session_id": "session-1",
		},
		"tags": []any{"a", "b"},
	}

	cloned := Clone(src)
	require.NotNil(t, cloned)

	cloned["prompt_cache_key"] = "changed"
	clonedMetadata := cloned["metadata"].(map[string]any)
	clonedMetadata["session_id"] = "changed"
	clonedTags := cloned["tags"].([]any)
	clonedTags[0] = "changed"

	require.Equal(t, "cache-1", src["prompt_cache_key"])
	require.Equal(t, json.Number("9007199254740993"), cloned["seed"])
	metadata := src["metadata"].(map[string]any)
	require.Equal(t, "session-1", metadata["session_id"])
	tags := src["tags"].([]any)
	require.Equal(t, "a", tags[0])
}

func TestCloneFallbackKeepsNonJSONValues(t *testing.T) {
	ch := make(chan int)
	src := map[string]any{"bad": ch}

	cloned := Clone(src)
	require.NotNil(t, cloned)
	require.Equal(t, ch, cloned["bad"])

	cloned["extra"] = 1
	_, exists := src["extra"]
	require.False(t, exists)
}
