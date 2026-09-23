//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package usage contains helpers for aggregating model token usage.
package usage

import "trpc.group/trpc-go/trpc-agent-go/model"

// Add adds src to dst and returns the resulting usage. A nil or zero-token
// source is ignored, and the first non-zero source is copied so aggregators do
// not mutate source results. Timing information is intentionally not
// accumulated because it describes an individual model request rather than
// token consumption.
func Add(dst, src *model.Usage) *model.Usage {
	if !hasTokens(src) {
		return dst
	}
	if dst == nil {
		dst = Clone(src)
		dst.TimingInfo = nil
		return dst
	}
	dst.TimingInfo = nil
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	dst.PromptTokensDetails.CachedTokens += src.PromptTokensDetails.CachedTokens
	dst.PromptTokensDetails.CacheCreationTokens += src.PromptTokensDetails.CacheCreationTokens
	dst.PromptTokensDetails.CacheReadTokens += src.PromptTokensDetails.CacheReadTokens
	dst.CompletionTokensDetails.ReasoningTokens += src.CompletionTokensDetails.ReasoningTokens
	return dst
}

func hasTokens(usage *model.Usage) bool {
	if usage == nil {
		return false
	}
	return usage.PromptTokens != 0 ||
		usage.CompletionTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.PromptTokensDetails.CachedTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationTokens != 0 ||
		usage.PromptTokensDetails.CacheReadTokens != 0 ||
		usage.CompletionTokensDetails.ReasoningTokens != 0
}

// Clone returns a deep copy of src.
func Clone(src *model.Usage) *model.Usage {
	if src == nil {
		return nil
	}
	copied := *src
	if src.TimingInfo != nil {
		timingInfo := *src.TimingInfo
		copied.TimingInfo = &timingInfo
	}
	return &copied
}
