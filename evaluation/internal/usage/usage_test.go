//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License 2.0.
//

package usage

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAdd(t *testing.T) {
	src := &model.Usage{
		PromptTokens:     2,
		CompletionTokens: 3,
		TotalTokens:      5,
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens:        1,
			CacheCreationTokens: 2,
			CacheReadTokens:     3,
		},
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 4},
		TimingInfo:              &model.TimingInfo{},
	}
	got := Add(nil, src)
	require.Equal(t, &model.Usage{
		PromptTokens:     2,
		CompletionTokens: 3,
		TotalTokens:      5,
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens:        1,
			CacheCreationTokens: 2,
			CacheReadTokens:     3,
		},
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 4},
	}, got)
	require.NotSame(t, src, got)
	require.NotSame(t, src.TimingInfo, got.TimingInfo)

	Add(got, &model.Usage{PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18})
	require.Equal(t, 9, got.PromptTokens)
	require.Equal(t, 14, got.CompletionTokens)
	require.Equal(t, 23, got.TotalTokens)
	require.Same(t, got, Add(got, &model.Usage{TimingInfo: &model.TimingInfo{}}))
}

func TestClone(t *testing.T) {
	original := &model.Usage{TimingInfo: &model.TimingInfo{FirstTokenDuration: 1}}
	cloned := Clone(original)
	require.Equal(t, original, cloned)
	require.NotSame(t, original, cloned)
	require.NotSame(t, original.TimingInfo, cloned.TimingInfo)
}
