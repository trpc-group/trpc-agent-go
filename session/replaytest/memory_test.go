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
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestMemoryCaseE2E(t *testing.T) {
	report := runReplayCaseReport(t, CaseMemoryWriteAndRead)
	require.Equal(t, 1, report.PassedCases)

	snapshot := runReplayCaseSnapshot(t, CaseMemoryWriteAndRead)
	require.NotEmpty(t, snapshot.Memories)
	require.NotEmpty(t, snapshot.MemSearchResults)

	written := snapshot.Memories[0]
	found := snapshot.MemSearchResults[0]
	require.Equal(t, written.ID, found.ID)
	require.Contains(t, found.Memory.Memory, "User likes Go replay tests")
}

func TestMemoryCaseMultiE2E(t *testing.T) {
	report := runReplayCaseReport(t, CaseMemoryMulti)
	require.Equal(t, 1, report.PassedCases)

	snapshot := runReplayCaseSnapshot(t, CaseMemoryMulti)
	require.Len(t, snapshot.Memories, 3)
	require.NotEmpty(t, snapshot.MemSearchResults)
	require.True(t, containsMemoryText(snapshot.MemSearchResults, "User writes Go daily"))
}

func TestMemoryFaultDetection(t *testing.T) {
	base := memorySnapshot("a", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	})

	tests := []struct {
		name string
		mut  func(*SessionSnapshot)
	}{
		{
			name: "lost",
			mut: func(s *SessionSnapshot) {
				s.MemSearchResults = nil
			},
		},
		{
			name: "content_tampered",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].Memory.Memory = "User likes Rust"
			},
		},
		{
			name: "topics_wrong",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].Memory.Topics = []string{"rust"}
			},
		},
		{
			name: "wrong_id",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].ID = "other"
			},
		},
		{
			name: "app_name_wrong",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].AppName = "other-app"
			},
		},
		{
			name: "user_id_wrong",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].UserID = "other-user"
			},
		},
		{
			name: "memory_payload_nil",
			mut: func(s *SessionSnapshot) {
				s.Memories[0].Memory = nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneMemorySnapshot(base, "b")
			tc.mut(changed)
			result := NewComparator().Compare(base, changed, nil, InMemoryProfile(), InMemoryProfile())
			require.Equal(t, StatusFailed, result.Status)
			require.NotEmpty(t, result.Diffs)
		})
	}
}

func TestMemorySameProfileStrict(t *testing.T) {
	a := memorySnapshot("a", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	})
	b := cloneMemorySnapshot(a, "b")

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusPassed, result.Status)

	b = cloneMemorySnapshot(a, "b")
	b.Memories = append(b.Memories, testMemoryEntry("extra", "extra", nil, 0))
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)

	b = cloneMemorySnapshot(a, "b")
	b.Memories[0].Memory.Memory = "changed"
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)

	b = cloneMemorySnapshot(a, "b")
	b.MemSearchResults[0].Score = 0.5
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
}

func TestMemoryCrossProfileSentinel(t *testing.T) {
	a := memorySnapshot("a", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.9),
	})
	b := memorySnapshot("b", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.2),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.3),
	})
	vector := InMemoryProfile()
	vector.RetrievalProfile.Algorithm = "cosine_vector"
	vector.RetrievalProfile.DistanceMetric = "cosine"

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), vector)
	require.Equal(t, StatusPassed, result.Status)

	b = memorySnapshot("b", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.3),
		testMemoryEntry("extra", "extra write", nil, 0.1),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.3),
		testMemoryEntry("extra-search", "extra search result", nil, 0.1),
	})
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), vector)
	require.Equal(t, StatusFailed, result.Status)
	requireDiff(t, result.Diffs, "memories[extra]", "target missing", "target present")

	b = memorySnapshot("b", []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.3),
	}, []*memory.Entry{
		testMemoryEntry("target", "User likes Go", []string{"go"}, 0.3),
		testMemoryEntry("extra-search", "extra search result", nil, 0.1),
	})
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), vector)
	require.Equal(t, StatusPassed, result.Status)
}

func containsMemoryText(entries []*memory.Entry, text string) bool {
	for _, entry := range entries {
		if entry != nil && entry.Memory != nil && entry.Memory.Memory == text {
			return true
		}
	}
	return false
}

func memorySnapshot(backend string, memories, results []*memory.Entry) *SessionSnapshot {
	return &SessionSnapshot{
		BackendName:      backend,
		Memories:         memories,
		MemSearchResults: results,
	}
}

func cloneMemorySnapshot(snapshot *SessionSnapshot, backend string) *SessionSnapshot {
	return memorySnapshot(
		backend,
		cloneMemoryEntries(snapshot.Memories),
		cloneMemoryEntries(snapshot.MemSearchResults),
	)
}

func cloneMemoryEntries(entries []*memory.Entry) []*memory.Entry {
	out := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			out = append(out, nil)
			continue
		}
		copied := *entry
		if entry.Memory != nil {
			mem := *entry.Memory
			mem.Topics = append([]string(nil), entry.Memory.Topics...)
			copied.Memory = &mem
		}
		out = append(out, &copied)
	}
	return out
}

func testMemoryEntry(id, text string, topics []string, score float64) *memory.Entry {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	return &memory.Entry{
		ID:        id,
		AppName:   "app",
		UserID:    "user",
		Memory:    &memory.Memory{Memory: text, Topics: append([]string(nil), topics...)},
		CreatedAt: now,
		UpdatedAt: now,
		Score:     score,
	}
}
