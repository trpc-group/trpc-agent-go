//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAccumulateUsage_DedupByResponseID(t *testing.T) {
	totals := usageTotals{}
	seen := make(map[string]struct{})
	resp := &model.Response{
		ID: "r1",
		Usage: &model.Usage{
			PromptTokens:     1,
			CompletionTokens: 2,
			TotalTokens:      3,
		},
	}

	accumulateUsage(&totals, resp, seen)
	accumulateUsage(&totals, resp, seen)

	require.Equal(t, 1, totals.Steps)
	require.Equal(t, 1, totals.PromptTokens)
	require.Equal(t, 2, totals.CompletionTokens)
	require.Equal(t, 3, totals.TotalTokens)
}

func TestAccumulateUsage_EmptyIDCountsEveryTime(t *testing.T) {
	totals := usageTotals{}
	seen := make(map[string]struct{})
	resp := &model.Response{
		Usage: &model.Usage{
			PromptTokens:     1,
			CompletionTokens: 1,
			TotalTokens:      2,
		},
	}

	accumulateUsage(&totals, resp, seen)
	accumulateUsage(&totals, resp, seen)

	require.Equal(t, 2, totals.Steps)
	require.Equal(t, 2, totals.PromptTokens)
	require.Equal(t, 2, totals.CompletionTokens)
	require.Equal(t, 4, totals.TotalTokens)
}
