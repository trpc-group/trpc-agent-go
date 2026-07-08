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
	"encoding/json"
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

func TestCanonicalizeValueKeepsLargeIntegerExact(t *testing.T) {
	// epoch-ns timestamp: beyond float64 exact-integer range.
	in := map[string]any{"ts": json.Number("1720000000000000001")}
	out := canonicalizeValue(in).(map[string]any)
	require.Equal(t, "1720000000000000001", out["ts"].(json.Number).String())
}

func TestNormalizeZeroesMemoryMetadataTimes(t *testing.T) {
	s := &Snapshot{
		Memories: []MemoryView{
			{ID: "raw", Content: "a", Metadata: map[string]any{"eventTime": time.Now(), "kind": "episode"}},
		},
	}
	Normalize(s)
	require.Equal(t, "episode", s.Memories[0].Metadata["kind"])
	// The zeroed time is canonicalized through JSON, so it lands as the RFC3339
	// rendering of the zero instant. What matters is that it is deterministic and
	// no longer carries the volatile wall-clock value.
	require.Equal(t, "0001-01-01T00:00:00Z", s.Memories[0].Metadata["eventTime"])
}
