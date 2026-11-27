//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestBuildResponseAttributes_PromptCacheAccounting(t *testing.T) {
	withCache := func(accounting model.PromptCacheAccounting) *model.Usage {
		u := &model.Usage{
			PromptTokens:        100,
			CompletionTokens:    10,
			TotalTokens:         110,
			PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
		}
		u.PromptCacheAccounting = accounting
		return u
	}
	tests := []struct {
		name           string
		usage          *model.Usage
		wantPresent    bool
		wantAccounting string
	}{
		{
			name:           "inclusive",
			usage:          withCache(model.PromptCacheAccountingInclusive),
			wantPresent:    true,
			wantAccounting: semconvtrace.ValueGenAIUsageInputTokensCacheAccountingInclusive,
		},
		{
			name:           "exclusive",
			usage:          withCache(model.PromptCacheAccountingExclusive),
			wantPresent:    true,
			wantAccounting: semconvtrace.ValueGenAIUsageInputTokensCacheAccountingExclusive,
		},
		{
			name:           "undeclared accounting with cache counters is written as empty",
			usage:          withCache(model.PromptCacheAccountingUnspecified),
			wantPresent:    true,
			wantAccounting: "",
		},
		{
			name:           "unknown accounting falls back to unspecified",
			usage:          withCache(model.PromptCacheAccounting("future-mode")),
			wantPresent:    true,
			wantAccounting: "",
		},
		{
			name:        "declaration without cache counters is ignored",
			usage:       &model.Usage{PromptTokens: 100, PromptCacheAccounting: model.PromptCacheAccountingInclusive},
			wantPresent: false,
		},
		{
			name:        "no cache counters omits the attribute",
			usage:       &model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
			wantPresent: false,
		},
		{
			name:        "nil usage omits the attribute",
			wantPresent: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildResponseAttributes(&model.Response{Usage: tt.usage}, semconvtrace.ValueDefaultErrorType, nil)
			got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIUsageInputTokensCacheAccounting)
			require.Equal(t, tt.wantPresent, ok)
			if tt.wantPresent {
				require.Equal(t, tt.wantAccounting, got)
			}
		})
	}
}

func TestChatTraceState_ReplacesCacheUsageSnapshot(t *testing.T) {
	initialCache := model.PromptTokensDetails{CachedTokens: 10, CacheReadTokens: 10, CacheCreationTokens: 20}
	for _, tc := range []struct {
		name           string
		usage          *model.Usage
		accounting     model.PromptCacheAccounting
		wantCache      model.PromptTokensDetails
		wantAccounting string
		preserve       bool
	}{
		{
			name:  "clear all cache buckets",
			usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11, TotalTokens: 111},
		},
		{
			name:  "clear total and all cache buckets together",
			usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11},
		},
		{
			name: "clear read buckets while retaining writes",
			usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11, TotalTokens: 111,
				PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 5}},
			accounting:     model.PromptCacheAccountingInclusive,
			wantCache:      model.PromptTokensDetails{CacheCreationTokens: 5},
			wantAccounting: "inclusive",
		},
		{
			name: "custom usage clears declaration and removed buckets",
			usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11, TotalTokens: 111,
				PromptTokensDetails: model.PromptTokensDetails{CachedTokens: 30}},
			wantCache: model.PromptTokensDetails{CachedTokens: 30},
		},
		{name: "nil usage preserves snapshot", preserve: true},
		{name: "empty usage preserves snapshot", usage: &model.Usage{}, preserve: true},
		{name: "timing-only usage preserves snapshot", usage: &model.Usage{TimingInfo: &model.TimingInfo{FirstTokenDuration: 123}}, preserve: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			span := newRecordingSpan()
			state := &ChatTraceState{}
			initial := &model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, PromptTokensDetails: initialCache}
			initial.PromptCacheAccounting = model.PromptCacheAccountingInclusive
			state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: initial}})
			// Mutating the prior response must not change which span attributes
			// the next snapshot needs to clear.
			initial.PromptTokensDetails = model.PromptTokensDetails{}
			if tc.usage != nil {
				tc.usage.PromptCacheAccounting = tc.accounting
			}
			state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: tc.usage}})
			// Timing-only trailing chunks must also preserve a replacement.
			state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: &model.Usage{TimingInfo: &model.TimingInfo{}}}})
			attrs := make(map[string]attribute.Value)
			for _, kv := range span.attrs {
				attrs[string(kv.Key)] = kv.Value
			}
			wantCache, wantAccounting := tc.wantCache, tc.wantAccounting
			wantOutput, wantTotal := int64(10), int64(110)
			if tc.preserve {
				wantCache, wantAccounting = initialCache, "inclusive"
			} else {
				wantOutput, wantTotal = int64(tc.usage.CompletionTokens), int64(tc.usage.TotalTokens)
			}
			require.Equal(t, int64(100), attrs[semconvtrace.KeyGenAIUsageInputTokens].AsInt64())
			require.Equal(t, wantOutput, attrs[semconvtrace.KeyGenAIUsageOutputTokens].AsInt64())
			require.Equal(t, wantTotal, attrs[semconvtrace.KeyGenAIUsageTotalTokens].AsInt64())
			require.Equal(t, int64(wantCache.CachedTokens), attrs[semconvtrace.KeyGenAIUsageInputTokensCached].AsInt64())
			require.Equal(t, int64(wantCache.CacheReadTokens), attrs[semconvtrace.KeyGenAIUsageInputTokensCacheRead].AsInt64())
			require.Equal(t, int64(wantCache.CacheCreationTokens), attrs[semconvtrace.KeyGenAIUsageInputTokensCacheCreation].AsInt64())
			require.Equal(t, wantAccounting, attrs[semconvtrace.KeyGenAIUsageInputTokensCacheAccounting].AsString())
		})
	}
}
