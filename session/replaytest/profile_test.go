//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltInProfiles(t *testing.T) {
	t.Run("inmemory", func(t *testing.T) {
		p := InMemoryProfile()
		require.Equal(t, "inmemory", p.Name)
		require.True(t, p.SupportsTrack)
		require.True(t, p.SupportsWindow)
		require.False(t, p.SupportsSearch)
		require.True(t, p.SupportsSessionState)
		require.False(t, p.SupportsSoftDelete)
		require.True(t, p.SupportsAsyncSummary)
		require.Equal(t, "bm25", p.RetrievalProfile.Algorithm)
	})

	t.Run("sqlite", func(t *testing.T) {
		p := SQLiteProfile()
		require.Equal(t, "sqlite", p.Name)
		require.True(t, p.SupportsTrack)
		require.True(t, p.SupportsSoftDelete)
		require.Equal(t, "bm25", p.RetrievalProfile.Algorithm)
	})

	t.Run("redis", func(t *testing.T) {
		p := RedisProfile()
		require.Equal(t, "redis", p.Name)
		require.True(t, p.SupportsTrack)
		require.False(t, p.SupportsSoftDelete)
		require.Equal(t, "bm25", p.RetrievalProfile.Algorithm)
		require.Equal(t, "gse_cjk", p.RetrievalProfile.Tokenizer)
	})
}
