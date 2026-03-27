//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package transcript

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBuild_UserOverflowReturnsOmissionOnly(t *testing.T) {
	entries := Build(context.Background(), []Record{
		{
			Index:    0,
			Entry:    Entry{Role: model.RoleUser, Content: "very large user content"},
			Category: CategoryMessage,
		},
		{
			Index:    1,
			Entry:    Entry{Role: model.RoleAssistant, Content: "assistant context"},
			Category: CategoryMessage,
		},
	}, func(ctx context.Context, entry Entry) int {
		if entry.Role == model.RoleUser {
			return DefaultMessageTranscriptBudget + 1
		}
		return 1
	}, DefaultOptions())
	require.Len(t, entries, 1)
	assert.Equal(t, model.RoleAssistant, entries[0].Role)
	assert.Equal(t, DefaultOmissionNote, entries[0].Content)
}

func TestBuild_TruncatesOversizedEntriesAndPreservesOrder(t *testing.T) {
	entries := Build(context.Background(), []Record{
		{
			Index:    0,
			Entry:    Entry{Role: model.RoleUser, Content: repeat("u", DefaultMessageEntryCap+1)},
			Category: CategoryMessage,
		},
		{
			Index:    1,
			Entry:    Entry{Role: model.RoleAssistant, Content: "assistant context"},
			Category: CategoryMessage,
		},
	}, func(ctx context.Context, entry Entry) int {
		return 1
	}, DefaultOptions())
	require.Len(t, entries, 3)
	assert.Equal(t, model.RoleAssistant, entries[0].Role)
	assert.Equal(t, DefaultOmissionNote, entries[0].Content)
	assert.Equal(t, model.RoleUser, entries[1].Role)
	assert.Contains(t, entries[1].Content, DefaultTruncatedSuffix)
	assert.Equal(t, model.RoleAssistant, entries[2].Role)
	assert.Equal(t, "assistant context", entries[2].Content)
}

func TestBuild_UsesToolBudgetIndependently(t *testing.T) {
	entries := Build(context.Background(), []Record{
		{
			Index:    0,
			Entry:    Entry{Role: model.RoleUser, Content: "user context"},
			Category: CategoryMessage,
		},
		{
			Index:    1,
			Entry:    Entry{Role: model.RoleTool, Content: "tool context"},
			Category: CategoryTool,
		},
		{
			Index:    2,
			Entry:    Entry{Role: model.RoleAssistant, Content: "assistant context"},
			Category: CategoryMessage,
		},
	}, func(ctx context.Context, entry Entry) int {
		if entry.Role == model.RoleTool {
			return DefaultToolTranscriptBudget + 1
		}
		return 1
	}, DefaultOptions())
	require.Len(t, entries, 3)
	assert.Equal(t, model.RoleAssistant, entries[0].Role)
	assert.Equal(t, DefaultOmissionNote, entries[0].Content)
	assert.Equal(t, model.RoleUser, entries[1].Role)
	assert.Equal(t, "user context", entries[1].Content)
	assert.Equal(t, model.RoleAssistant, entries[2].Role)
	assert.Equal(t, "assistant context", entries[2].Content)
}

func TestBuild_WithoutTokenizerFailsClosedForToolEntries(t *testing.T) {
	options := DefaultOptions()
	options.MessageTranscriptBudget = 10
	options.ToolTranscriptBudget = 20
	entries := Build(context.Background(), []Record{
		{
			Index:    0,
			Entry:    Entry{Role: model.RoleTool, Content: "tool context"},
			Category: CategoryTool,
		},
	}, nil, options)
	require.Len(t, entries, 1)
	assert.Equal(t, model.RoleAssistant, entries[0].Role)
	assert.Equal(t, DefaultOmissionNote, entries[0].Content)
}

func repeat(value string, n int) string {
	if n <= 0 {
		return ""
	}
	result := make([]byte, 0, len(value)*n)
	for i := 0; i < n; i++ {
		result = append(result, value...)
	}
	return string(result)
}
