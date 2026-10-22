//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestChatAttributeObserverBeforeSDKLimits(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder),
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{AttributeCountLimit: 2, AttributeValueLengthLimit: 64}))
	defer provider.Shutdown(context.Background())
	_, span := provider.Tracer("test").Start(context.Background(), "chat")
	span.SetAttributes(attribute.String("existing", "kept"))
	var observed []byte
	stop := atrace.RegisterChatAttributeObserver(func(got trace.Span, key string, value []byte) {
		require.Same(t, span, got)
		if key == string(atrace.AttrLLMRequest) {
			observed = append([]byte(nil), value...)
		}
	})
	defer stop()
	req := &model.Request{Messages: []model.Message{model.NewUserMessage(strings.Repeat("长", 5000))},
		ExtraFields: map[string]any{"secret": "must not be observed"}}
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Request: req})
	span.End()
	require.True(t, json.Valid(observed))
	require.NotContains(t, string(observed), "secret")
	var decoded model.Request
	require.NoError(t, json.Unmarshal(observed, &decoded))
	require.Equal(t, req.Messages, decoded.Messages)
	require.Len(t, recorder.Ended()[0].Attributes(), 2)
	for _, a := range recorder.Ended()[0].Attributes() {
		require.NotEqual(t, string(atrace.AttrLLMRequest), string(a.Key))
	}
}

func TestChatAttributeObserverPolicyAndRequestCache(t *testing.T) {
	defer atrace.SetSpanAttributePolicy(atrace.SpanAttributePolicy{})
	for _, tc := range []struct {
		name    string
		options []atrace.AttributeOption
		allowed bool
	}{
		{"capture", nil, true},
		{"drop", []atrace.AttributeOption{atrace.Drop()}, false},
		{"omit", []atrace.AttributeOption{atrace.Omit()}, false},
		{"omit-overflow", []atrace.AttributeOption{atrace.Omit(), atrace.MaxBytes(1)}, false},
		{"omit-fits", []atrace.AttributeOption{atrace.Omit(), atrace.MaxBytes(10000)}, true},
		{"truncate-overflow", []atrace.AttributeOption{atrace.Truncate(1)}, false},
		{"truncate-fits", []atrace.AttributeOption{atrace.Truncate(10000)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atrace.SetSpanAttributePolicy(atrace.SpanAttributePolicy{})
			provider := sdktrace.NewTracerProvider()
			defer provider.Shutdown(context.Background())
			_, span := provider.Tracer("test").Start(context.Background(), "chat")
			defer span.End()
			var values [][]byte
			stop := atrace.RegisterChatAttributeObserver(func(_ trace.Span, key string, value []byte) {
				if key == string(atrace.AttrInputMessages) {
					values = append(values, append([]byte(nil), value...))
				}
			})
			defer stop()
			state := &itelemetry.ChatTraceState{}
			attrs := &itelemetry.TraceChatAttributes{Request: &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}}
			state.TraceChat(span, attrs)
			state.TraceChat(span, attrs)
			require.Len(t, values, 1)
			policy := atrace.SpanAttributePolicy{}
			atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrInputMessages, tc.options...)(&policy)
			atrace.SetSpanAttributePolicy(policy)
			state.TraceChat(span, attrs)
			require.Len(t, values, 2)
			require.Equal(t, tc.allowed, values[1] != nil)
		})
	}
}

func TestChatAttributeObserverStreamingAndUnregister(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	_, span := provider.Tracer("test").Start(context.Background(), "chat")
	var responses []model.Response
	stop := atrace.RegisterChatAttributeObserver(func(_ trace.Span, key string, value []byte) {
		if key == string(atrace.AttrLLMResponse) {
			var rsp model.Response
			require.NoError(t, json.Unmarshal(value, &rsp))
			responses = append(responses, rsp)
		}
	})
	defer stop()
	for _, rsp := range []*model.Response{
		{IsPartial: true, Choices: []model.Choice{{Delta: model.NewAssistantMessage("a")}}},
		{Choices: []model.Choice{{Message: model.NewAssistantMessage("answer")}}},
		{Usage: &model.Usage{TotalTokens: 10}},
	} {
		itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Response: rsp})
	}
	require.Len(t, responses, 3)
	require.True(t, responses[0].IsPartial)
	require.Equal(t, "answer", responses[1].Choices[0].Message.Content)
	require.Equal(t, 10, responses[2].Usage.TotalTokens)
	stop()
	stop()
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Response: &model.Response{}})
	require.Len(t, responses, 3)
	span.End()
}

func TestChatAttributeObserverConcurrentRegistrationAndSampling(t *testing.T) {
	atrace.RegisterChatAttributeObserver(nil)()
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	var calls atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stop := atrace.RegisterChatAttributeObserver(func(trace.Span, string, []byte) { calls.Add(1) })
			defer stop()
			_, span := provider.Tracer("test").Start(context.Background(), "chat")
			itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Request: &model.Request{}})
			span.End()
		}()
	}
	wg.Wait()
	require.Positive(t, calls.Load())
	stop := atrace.RegisterChatAttributeObserver(func(trace.Span, string, []byte) { t.Error("non-recording span observed") })
	defer stop()
	unsampled := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	defer unsampled.Shutdown(context.Background())
	_, span := unsampled.Tracer("test").Start(context.Background(), "chat")
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Request: &model.Request{}})
	span.End()
}

func TestChatAttributeObserverSerializationFailureInvalidatesSource(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	_, span := provider.Tracer("test").Start(context.Background(), "chat")
	defer span.End()
	var called bool
	stop := atrace.RegisterChatAttributeObserver(func(_ trace.Span, key string, value []byte) {
		if key == string(atrace.AttrLLMRequest) {
			called = true
			require.Nil(t, value)
		}
	})
	defer stop()
	req := &model.Request{Messages: []model.Message{{ToolCalls: []model.ToolCall{{ExtraFields: map[string]any{"invalid": make(chan int)}}}}}}
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Request: req})
	require.True(t, called)
}

func TestChatAttributeObserverUsageOnlyDropInvalidatesOutput(t *testing.T) {
	defer atrace.SetSpanAttributePolicy(atrace.SpanAttributePolicy{})
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	_, span := provider.Tracer("test").Start(context.Background(), "chat")
	defer span.End()
	var values [][]byte
	stop := atrace.RegisterChatAttributeObserver(func(_ trace.Span, key string, value []byte) {
		if key == string(atrace.AttrOutputMessages) {
			values = append(values, append([]byte(nil), value...))
		}
	})
	defer stop()
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Response: &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("answer")}},
	}})
	policy := atrace.SpanAttributePolicy{}
	atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrOutputMessages, atrace.Drop())(&policy)
	atrace.SetSpanAttributePolicy(policy)
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{Response: &model.Response{Usage: &model.Usage{TotalTokens: 4}}})
	require.Len(t, values, 2)
	require.NotNil(t, values[0])
	require.Nil(t, values[1])
}
