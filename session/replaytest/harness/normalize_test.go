//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeStripsVolatileFields(t *testing.T) {
	s := &Snapshot{
		State: map[string]string{"lang": "en", "tracks": "[\"x\"]"},
		Memories: []MemoryView{
			{ID: "raw-2", Content: "b", Score: 0.1234567},
			{ID: "raw-1", Content: "a", Score: 0.7654321},
		},
		Summaries: []SummaryView{{FilterKey: "", Text: "x", UpdatedAt: time.Now()}},
		Tracks:    []TrackView{{Name: "t", Timestamp: time.Now()}},
	}
	Normalize(s)
	_, hasTracks := s.State["tracks"]
	require.False(t, hasTracks)
	require.Equal(t, "a", s.Memories[0].Content) // sorted by content
	require.Equal(t, "mem#0", s.Memories[0].ID)
	require.InDelta(t, 0.765432, s.Memories[0].Score, 1e-9)
	require.True(t, s.Summaries[0].UpdatedAt.IsZero())
	require.True(t, s.Tracks[0].Timestamp.IsZero())
}
