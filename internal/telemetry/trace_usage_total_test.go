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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestChatTraceState_TotalTokensReplacement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		usage     *model.Usage
		wantTotal int64
	}{
		{name: "prompt and completion snapshot clears total", usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11}},
		{name: "prompt-only snapshot clears total", usage: &model.Usage{PromptTokens: 100}},
		{name: "completion-only snapshot clears total", usage: &model.Usage{CompletionTokens: 11}},
		{name: "cached detail snapshot clears total", usage: &model.Usage{PromptTokensDetails: model.PromptTokensDetails{CachedTokens: 80}}},
		{name: "cache read detail snapshot clears total", usage: &model.Usage{PromptTokensDetails: model.PromptTokensDetails{CacheReadTokens: 80}}},
		{name: "cache creation detail snapshot clears total", usage: &model.Usage{PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80}}},
		{name: "reasoning detail snapshot clears total", usage: &model.Usage{CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 4}}},
		{name: "nonzero total replaces previous total", usage: &model.Usage{TotalTokens: 111}, wantTotal: 111},
		{name: "nil usage preserves total", wantTotal: 110},
		{name: "empty usage preserves total", usage: &model.Usage{}, wantTotal: 110},
		{name: "timing-only usage preserves total", usage: &model.Usage{TimingInfo: &model.TimingInfo{FirstTokenDuration: 123}}, wantTotal: 110},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer provider.Shutdown(context.Background())
			_, span := provider.Tracer("test").Start(context.Background(), "chat")
			state := &ChatTraceState{}
			initial := &model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}
			state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: initial}})
			// The previous response may be reused and overwritten in place.
			initial.TotalTokens = 0
			state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: tc.usage}})
			span.End()
			attrs := map[string]attribute.Value{}
			for _, kv := range recorder.Ended()[0].Attributes() {
				attrs[string(kv.Key)] = kv.Value
			}
			total, present := attrs[semconvtrace.KeyGenAIUsageTotalTokens]
			require.True(t, present)
			require.Equal(t, tc.wantTotal, total.AsInt64())
			if tc.usage != nil && (tc.usage.PromptTokens != 0 || tc.usage.CompletionTokens != 0) {
				require.Equal(t, int64(tc.usage.PromptTokens), attrs[semconvtrace.KeyGenAIUsageInputTokens].AsInt64())
				require.Equal(t, int64(tc.usage.CompletionTokens), attrs[semconvtrace.KeyGenAIUsageOutputTokens].AsInt64())
			}
		})
	}
}

func TestChatTraceState_TotalTokensLifecycle(t *testing.T) {
	span := newRecordingSpan()
	state := &ChatTraceState{}
	for _, step := range []struct {
		usage   *model.Usage
		present bool
		total   int64
	}{
		{usage: &model.Usage{PromptTokens: 100, CompletionTokens: 10}},
		{usage: &model.Usage{TotalTokens: 110}, present: true, total: 110},
		{usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11}, present: true},
		{usage: &model.Usage{TimingInfo: &model.TimingInfo{FirstTokenDuration: 123}}, present: true},
		{usage: &model.Usage{TotalTokens: 120}, present: true, total: 120},
		{usage: nil, present: true, total: 120},
		{usage: &model.Usage{}, present: true, total: 120},
	} {
		state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: step.usage}})
		var total int64
		var present bool
		for _, kv := range span.attrs {
			if string(kv.Key) == semconvtrace.KeyGenAIUsageTotalTokens {
				total = kv.Value.AsInt64()
				present = true
			}
		}
		require.Equal(t, step.present, present)
		require.Equal(t, step.total, total)
	}
	// Each chat span gets its own state; a new span still omits an unknown total.
	other := newRecordingSpan()
	(&ChatTraceState{}).TraceChat(other, &TraceChatAttributes{Response: &model.Response{Usage: &model.Usage{PromptTokens: 1}}})
	require.Zero(t, countAttr(other.attrs, semconvtrace.KeyGenAIUsageTotalTokens))
}

func TestTraceChat_StatelessZeroTotalRemainsOmitted(t *testing.T) {
	span := newRecordingSpan()
	var state *ChatTraceState
	state.TraceChat(span, &TraceChatAttributes{Response: &model.Response{Usage: &model.Usage{PromptTokens: 1}}})
	require.Zero(t, countAttr(span.attrs, semconvtrace.KeyGenAIUsageTotalTokens))
}
