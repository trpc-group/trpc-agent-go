//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryinject

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const summaryBlock = "Session summary:\nthe user asked about billing"

func TestRecordAndFromInvocation(t *testing.T) {
	inv := agent.NewInvocation()
	_, ok := FromInvocation(inv)
	require.False(t, ok, "an untouched invocation reports no selection")

	Record(inv, Selection{
		LookupStrategy:     LookupStrategyPrefix,
		LookupResult:       LookupResultExact,
		Selected:           true,
		StoredSummaries:    2,
		MatchingCandidates: 1,
		Block:              summaryBlock,
	})
	selection, ok := FromInvocation(inv)
	require.True(t, ok)
	require.Equal(t, LookupStrategyPrefix, selection.LookupStrategy)
	require.Equal(t, LookupResultExact, selection.LookupResult)
	require.Equal(t, 2, selection.StoredSummaries)
	require.Equal(t, 1, selection.MatchingCandidates)

	Record(inv, Selection{
		LookupStrategy: LookupStrategyPrefix,
		LookupResult:   LookupResultNone,
	})
	selection, ok = FromInvocation(inv)
	require.True(t, ok, "a later request replaces the previous selection")
	require.Equal(t, LookupResultNone, selection.LookupResult)
	require.False(t, selection.Selected)

	Clear(inv)
	_, ok = FromInvocation(inv)
	require.False(t, ok)
}

func TestSelectionScopeMismatch(t *testing.T) {
	tests := []struct {
		name      string
		selection Selection
		want      bool
	}{
		{
			name: "scoped request misses an existing full-session summary",
			selection: Selection{
				ScopedRequest:      true,
				FullSessionPresent: true,
			},
			want: true,
		},
		{
			name: "unrelated branch summary is a legitimate miss",
			selection: Selection{
				ScopedRequest:   true,
				StoredSummaries: 1,
			},
			want: false,
		},
		{
			name: "full-session request cannot mismatch its own scope",
			selection: Selection{
				FullSessionPresent: true,
			},
			want: false,
		},
		{
			name: "a selected summary is never a mismatch",
			selection: Selection{
				Selected:           true,
				ScopedRequest:      true,
				FullSessionPresent: true,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.selection.ScopeMismatch())
		})
	}
}

func TestRecordToleratesNilInvocation(t *testing.T) {
	require.NotPanics(t, func() {
		Record(nil, Selection{})
		Clear(nil)
		_, ok := FromInvocation(nil)
		require.False(t, ok)
	})
}

func TestSelectionBlockPresent(t *testing.T) {
	tests := []struct {
		name      string
		selection Selection
		messages  []model.Message
		want      bool
	}{
		{
			name:      "block is the whole message",
			selection: Selection{Selected: true, Block: summaryBlock},
			messages: []model.Message{
				model.NewSystemMessage(summaryBlock),
			},
			want: true,
		},
		{
			name:      "block merged behind other context",
			selection: Selection{Selected: true, Block: summaryBlock},
			messages: []model.Message{
				model.NewSystemMessage("instructions"),
				model.NewUserMessage("recalled memory\n\n" + summaryBlock),
			},
			want: true,
		},
		{
			name:      "block dropped from the request",
			selection: Selection{Selected: true, Block: summaryBlock},
			messages: []model.Message{
				model.NewSystemMessage("instructions"),
				model.NewUserMessage("current question"),
			},
			want: false,
		},
		{
			name:      "nothing was selected",
			selection: Selection{Selected: false, Block: summaryBlock},
			messages: []model.Message{
				model.NewSystemMessage(summaryBlock),
			},
			want: false,
		},
		{
			name:      "selected without a recorded block",
			selection: Selection{Selected: true},
			messages: []model.Message{
				model.NewSystemMessage("instructions"),
			},
			want: false,
		},
		{
			name:      "empty request",
			selection: Selection{Selected: true, Block: summaryBlock},
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.selection.BlockPresent(tt.messages))
		})
	}
}
