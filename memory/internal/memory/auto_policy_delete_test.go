//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPreserveHistoryDeleteAuthorization_EpisodicMetadata(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		request    string
		mem        *memory.Memory
		wantDelete bool
	}{
		{
			name:       "participant",
			request:    "Please forget everything about Alice.",
			wantDelete: true,
			mem: &memory.Memory{
				Memory:       "User visited the park.",
				Kind:         memory.KindEpisode,
				Participants: []string{"Alice"},
			},
		},
		{
			name:       "location",
			request:    "Please forget everything about Kyoto.",
			wantDelete: true,
			mem: &memory.Memory{
				Memory:   "User attended a conference.",
				Kind:     memory.KindEpisode,
				Location: "Kyoto",
			},
		},
		{
			name:       "event time",
			request:    "Please forget everything from 2025-12-01.",
			wantDelete: true,
			mem: &memory.Memory{
				Memory:    "User attended a conference.",
				Kind:      memory.KindEpisode,
				EventTime: &eventTime,
			},
		},
		{
			name:    "tokens cannot combine across metadata fields",
			request: "Please forget Alice Kyoto.",
			mem: &memory.Memory{
				Memory:       "User attended a conference.",
				Kind:         memory.KindEpisode,
				Participants: []string{"Alice"},
				Location:     "Kyoto",
			},
		},
	}

	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := &memory.Entry{ID: "target", Memory: test.mem}
			out := worker.reconcilePreserveHistoryOps(
				context.Background(),
				reconcileUserKey(),
				[]*extractor.Operation{
					{Type: extractor.OperationDelete, MemoryID: entry.ID},
					{Type: extractor.OperationDelete, MemoryID: "unrelated"},
				},
				[]*memory.Entry{
					entry,
					{ID: "unrelated", Memory: &memory.Memory{Memory: "User enjoys hiking."}},
				},
				[]model.Message{model.NewUserMessage(test.request)},
			)
			if !test.wantDelete {
				assert.Empty(t, out)
				return
			}
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationDelete, out[0].Type)
			assert.Equal(t, entry.ID, out[0].MemoryID)
		})
	}
}

func TestPreserveHistoryForgetWriteFilter_EpisodicMetadata(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		request  string
		op       *extractor.Operation
		existing []*memory.Entry
	}{
		{
			name:    "participant add",
			request: "Please forget everything about Alice.",
			op: &extractor.Operation{
				Type:         extractor.OperationAdd,
				Memory:       "User visited the park.",
				MemoryKind:   memory.KindEpisode,
				Participants: []string{"Alice"},
			},
		},
		{
			name:    "location update",
			request: "Please forget everything about Kyoto.",
			op: &extractor.Operation{
				Type:       extractor.OperationUpdate,
				MemoryID:   "target",
				Memory:     "User attended a conference.",
				MemoryKind: memory.KindEpisode,
				Location:   "Kyoto",
			},
			existing: []*memory.Entry{{
				ID: "target",
				Memory: &memory.Memory{
					Memory:   "User attended a conference.",
					Kind:     memory.KindEpisode,
					Location: "Kyoto",
				},
			}},
		},
		{
			name:    "event time update",
			request: "Please forget everything from 2025-12-01.",
			op: &extractor.Operation{
				Type:       extractor.OperationUpdate,
				MemoryID:   "target",
				Memory:     "User attended a conference.",
				MemoryKind: memory.KindEpisode,
				EventTime:  &eventTime,
			},
			existing: []*memory.Entry{{
				ID: "target",
				Memory: &memory.Memory{
					Memory:    "User attended a conference.",
					Kind:      memory.KindEpisode,
					EventTime: &eventTime,
				},
			}},
		},
	}

	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := worker.reconcilePreserveHistoryOps(
				context.Background(),
				reconcileUserKey(),
				[]*extractor.Operation{test.op},
				test.existing,
				[]model.Message{model.NewUserMessage(test.request)},
			)
			assert.Empty(t, out)
		})
	}
}

func TestPreserveHistoryDeleteAuthorization_CoordinatedTargets(t *testing.T) {
	existing := []*memory.Entry{
		{ID: "alice", Memory: &memory.Memory{Memory: "User spoke with Alice."}},
		{ID: "bob", Memory: &memory.Memory{Memory: "User spoke with Bob."}},
		{ID: "carol", Memory: &memory.Memory{Memory: "User spoke with Carol."}},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(),
		reconcileUserKey(),
		[]*extractor.Operation{
			{Type: extractor.OperationDelete, MemoryID: "alice"},
			{Type: extractor.OperationDelete, MemoryID: "bob"},
			{Type: extractor.OperationDelete, MemoryID: "carol"},
			{Type: extractor.OperationAdd, Memory: "User spoke with Alice."},
			{Type: extractor.OperationAdd, Memory: "User spoke with Bob."},
			{Type: extractor.OperationAdd, Memory: "User plans to meet Carol next week."},
		},
		existing,
		[]model.Message{model.NewUserMessage("Please forget Alice and Bob.")},
	)
	require.Len(t, out, 3)
	assert.Equal(t, extractor.OperationDelete, out[0].Type)
	assert.Equal(t, "alice", out[0].MemoryID)
	assert.Equal(t, extractor.OperationDelete, out[1].Type)
	assert.Equal(t, "bob", out[1].MemoryID)
	assert.Equal(t, extractor.OperationAdd, out[2].Type)
	assert.Equal(t, "User plans to meet Carol next week.", out[2].Memory)
}
