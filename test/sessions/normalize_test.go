//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestNormalizeSnapshotOrdersSameContentMemoriesByBusinessTuple(t *testing.T) {
	earlier := time.Date(2026, 1, 20, 9, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	later := time.Date(2026, 1, 20, 15, 0, 0, 0, time.UTC)
	content := "参加项目同步会"

	reference := Snapshot{
		CaseID: "same-content-memory", Backend: "reference",
		Memories: []MemorySnapshot{
			{
				ID: "backend-a-001", Content: content, Kind: memory.KindEpisode,
				EventTime: &later, Participants: []string{"bob", "alice"},
				Location: "beijing", Topics: []string{"release", "meeting"},
			},
			{
				ID: "backend-a-999", Content: content, Kind: memory.KindEpisode,
				EventTime: &earlier, Participants: []string{"alice"},
				Location: "shenzhen", Topics: []string{"meeting"},
			},
		},
	}
	actual := Snapshot{
		CaseID: "same-content-memory", Backend: "actual",
		Memories: []MemorySnapshot{
			{
				ID: "backend-b-001", Content: content, Kind: memory.KindEpisode,
				EventTime: &earlier, Participants: []string{"alice"},
				Location: "shenzhen", Topics: []string{"meeting"},
			},
			{
				ID: "backend-b-999", Content: content, Kind: memory.KindEpisode,
				EventTime: &later, Participants: []string{"alice", "bob"},
				Location: "beijing", Topics: []string{"meeting", "release"},
			},
		},
	}

	opts := NormalizeOptions{
		NormalizeGeneratedMemoryIDs: true,
		NilEqualsEmpty:              true,
	}
	normalizedReference, err := NormalizeSnapshot(reference, opts)
	require.NoError(t, err)
	normalizedActual, err := NormalizeSnapshot(actual, opts)
	require.NoError(t, err)
	require.Equal(t, normalizedReference, normalizedActual)

	memories := normalizedReference.Memories
	require.Len(t, memories, 2)
	require.Equal(t, "memory-001", memories[0].ID)
	require.True(t, memories[0].EventTime.Before(*memories[1].EventTime))
	require.Equal(t, "memory-002", memories[1].ID)
	require.Equal(t, []string{"alice", "bob"}, memories[1].Participants)
	require.Equal(t, []string{"meeting", "release"}, memories[1].Topics)
}
