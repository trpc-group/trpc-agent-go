//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestLLMNodeResponseErrorTelemetry(t *testing.T) {
	code := "429"
	for _, path := range []string{"fast", "callbacks", "iterator", "direct", "done"} {
		for _, tc := range []struct {
			name      string
			err       *model.ResponseError
			wantLabel string
		}{
			{"untyped", &model.ResponseError{Message: "stream failed"}, "_OTHER"},
			{"typed", &model.ResponseError{Type: "stream_error", Message: "stream failed"}, "stream_error"},
			{"coded", &model.ResponseError{Type: "api_error", Code: &code, Message: "rate limited"}, "api_error_429"},
			{"code_only", &model.ResponseError{Code: &code, Message: "rate limited"}, "_OTHER_429"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				reader := useChatErrorMetricReader(t)
				recorder := useSpanRecorder(t)
				llm := &multiResponseModel{responses: []*model.Response{
					{ID: "partial", Model: "response-model", IsPartial: true,
						Choices: []model.Choice{{Delta: model.NewAssistantMessage("hello")}}},
					{ID: "failure", Model: "response-model", Error: tc.err},
				}}
				if path == "direct" || path == "done" {
					llm.responses = llm.responses[1:]
					llm.responses[0].Done = path == "done"
				}
				var callModel model.Model = llm
				if path == "iterator" {
					callModel = &chatErrorIterModel{llm}
				}
				state := State{StateKeyExecContext: &ExecutionContext{
					InvocationID: "inv-error", EventChan: make(chan *event.Event, 8),
				}}
				if path == "callbacks" {
					state[StateKeyModelCallbacks] = model.NewCallbacks().RegisterAfterModel(
						func(context.Context, *model.Request, *model.Response, error) (*model.Response, error) {
							return nil, nil
						})
				}
				invocation := agent.NewInvocation(agent.WithInvocationModel(callModel))
				ctx := agent.NewInvocationContext(context.Background(), invocation)
				_, err := NewLLMNodeFunc(callModel, "", nil)(ctx, state)
				require.ErrorContains(t, err, "model API error: "+tc.err.Message)

				metricAttrs := collectChatErrorMetricAttributes(t, reader)
				t.Logf("chat metric error.type=%q", metricAttrs[semconvtrace.KeyErrorType].AsString())
				found := false
				for _, span := range recorder.Ended() {
					if span.Name() != itelemetry.NewChatSpanName(llm.Info().Name) {
						continue
					}
					found = true
					attrs := make(map[string]attribute.Value)
					for _, attr := range span.Attributes() {
						attrs[string(attr.Key)] = attr.Value
					}
					t.Logf("chat span error.type=%q status=%s", attrs[semconvtrace.KeyErrorType].AsString(), span.Status().Code)
					require.Equal(t, tc.wantLabel, metricAttrs[semconvtrace.KeyErrorType].AsString())
					require.Equal(t, tc.wantLabel, attrs[semconvtrace.KeyErrorType].AsString())
					require.Equal(t, "chat", attrs[semconvtrace.KeyGenAIOperationName].AsString())
					require.Equal(t, "failure", attrs[semconvtrace.KeyGenAIResponseID].AsString())
					require.Equal(t, "response-model", attrs[semconvtrace.KeyGenAIResponseModel].AsString())
					require.Equal(t, llm.Info().Name, attrs[semconvtrace.KeyGenAIRequestModel].AsString())
					require.Equal(t, codes.Error, span.Status().Code)
					require.Equal(t, tc.err.Message, span.Status().Description)
				}
				require.True(t, found, "chat span must be exported")
			})
		}
	}
}

type chatErrorIterModel struct {
	*multiResponseModel
}

func (m *chatErrorIterModel) GenerateContentIter(
	context.Context, *model.Request,
) (model.Seq[*model.Response], error) {
	return func(yield func(*model.Response) bool) {
		for _, response := range m.responses {
			if !yield(response) {
				return
			}
		}
	}, nil
}

func TestLLMNodeCallbackResponseErrorTelemetry(t *testing.T) {
	for _, scenario := range []string{"inject", "replace", "recover", "success"} {
		t.Run(scenario, func(t *testing.T) {
			reader := useChatErrorMetricReader(t)
			recorder := useSpanRecorder(t)
			original := &model.Response{
				ID: "original", Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
			}
			processed := &model.Response{
				ID: "processed", Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
			}
			if scenario == "replace" || scenario == "recover" {
				original.Error = &model.ResponseError{Type: "original_error", Message: "original failure"}
			}
			wantError := scenario == "inject" || scenario == "replace"
			if wantError {
				processed.Error = &model.ResponseError{Type: "processed_error", Message: "processed failure"}
			}
			callbacks := model.NewCallbacks().RegisterAfterModel(
				func(context.Context, *model.Request, *model.Response, error) (*model.Response, error) {
					return processed, nil
				})
			llm := &stubModel{resp: original}
			_, err := NewLLMNodeFunc(llm, "", nil)(context.Background(), State{StateKeyModelCallbacks: callbacks})
			if wantError {
				require.ErrorContains(t, err, processed.Error.Message)
			} else {
				require.NoError(t, err)
			}
			metricAttrs := collectChatErrorMetricAttributes(t, reader)
			found := false
			for _, span := range recorder.Ended() {
				if span.Name() != itelemetry.NewChatSpanName(llm.Info().Name) {
					continue
				}
				found = true
				attrs := make(map[string]attribute.Value)
				for _, attr := range span.Attributes() {
					attrs[string(attr.Key)] = attr.Value
				}
				require.Equal(t, processed.ID, attrs[semconvtrace.KeyGenAIResponseID].AsString())
				if wantError {
					require.Equal(t, "processed_error", metricAttrs[semconvtrace.KeyErrorType].AsString())
					require.Equal(t, "processed_error", attrs[semconvtrace.KeyErrorType].AsString())
					require.Equal(t, codes.Error, span.Status().Code)
				} else {
					require.NotContains(t, metricAttrs, semconvtrace.KeyErrorType)
					require.NotContains(t, attrs, semconvtrace.KeyErrorType)
					require.NotEqual(t, codes.Error, span.Status().Code)
				}
			}
			require.True(t, found)
		})
	}
}

func TestLLMNodeResponseErrorMetricsWithTracingDisabled(t *testing.T) {
	reader := useChatErrorMetricReader(t)
	recorder := useSpanRecorder(t)
	llm := &stubModel{resp: &model.Response{
		Error: &model.ResponseError{Type: "stream_error", Message: "stream failed"},
	}}
	invocation := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}))
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	_, err := NewLLMNodeFunc(llm, "", nil)(ctx, State{})
	require.ErrorContains(t, err, "stream failed")
	require.Empty(t, recorder.Ended())
	attrs := collectChatErrorMetricAttributes(t, reader)
	require.Equal(t, "stream_error", attrs[semconvtrace.KeyErrorType].AsString())
}

func TestGraphChatAndWorkflowResponseErrorTelemetry(t *testing.T) {
	reader := useChatErrorMetricReader(t)
	recorder := useSpanRecorder(t)
	llm := &stubModel{resp: &model.Response{Error: &model.ResponseError{Message: "stream failed"}}}
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", llm, "", nil).SetEntryPoint("llm").SetFinishPoint("llm")
	executor := compileExecutorForWorkflowMetric(t, sg)
	ch, err := executor.Execute(context.Background(), State{}, agent.NewInvocation())
	require.NoError(t, err)
	var failed bool
	for ev := range ch {
		if ev.Error != nil {
			failed = true
		}
	}
	require.True(t, failed, "the model failure must still propagate to graph events")
	metricAttrs := collectChatErrorMetricAttributes(t, reader)
	require.Equal(t, "_OTHER", metricAttrs[semconvtrace.KeyErrorType].AsString())
	operations := make(map[string]bool)
	for _, span := range recorder.Ended() {
		attrs := make(map[string]attribute.Value)
		for _, attr := range span.Attributes() {
			attrs[string(attr.Key)] = attr.Value
		}
		operation := attrs[semconvtrace.KeyGenAIOperationName].AsString()
		if operation != "chat" && operation != "workflow" {
			continue
		}
		operations[operation] = true
		require.Equal(t, "_OTHER", attrs[semconvtrace.KeyErrorType].AsString())
		require.Equal(t, codes.Error, span.Status().Code)
	}
	require.True(t, operations["chat"], "the chat span must also record the model failure")
	require.True(t, operations["workflow"], "parent workflow error reporting must be preserved")
}

func TestGraphChatFinalizationErrorTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name           string
		response       *model.Response
		nilIterator    bool
		recover        bool
		wantError      string
		wantResponseID string
	}{
		{
			name: "callback_recovery",
			response: &model.Response{ID: "original", Done: true,
				Error: &model.ResponseError{Type: "api_error", Message: "original failure"}},
			recover: true, wantError: errMsgNoModelChoices, wantResponseID: "recovered",
		},
		{
			name: "no_choices", response: &model.Response{ID: "empty", Done: true},
			wantError: errMsgNoModelChoices, wantResponseID: "empty",
		},
		{name: "no_response", wantError: errMsgNoModelResponse},
		{name: "nil_iterator", nilIterator: true, wantError: errMsgNoModelResponse},
	} {
		for _, disableTracing := range []bool{false, true} {
			name := tc.name
			if disableTracing {
				name += "_tracing_disabled"
			}
			t.Run(name, func(t *testing.T) {
				reader := useChatErrorMetricReader(t)
				recorder := useSpanRecorder(t)
				responses := &multiResponseModel{}
				if tc.response != nil {
					response := *tc.response
					responses.responses = []*model.Response{&response}
				}
				var llm model.Model = responses
				if tc.nilIterator {
					llm = &nilIterModel{}
				}
				var callbacks *model.Callbacks
				if tc.recover {
					callbacks = model.NewCallbacks().RegisterAfterModel(
						func(context.Context, *model.Request, *model.Response, error) (*model.Response, error) {
							return &model.Response{ID: "recovered", Done: true,
								Choices: []model.Choice{{Message: model.NewAssistantMessage("fallback")}}}, nil
						})
				}
				sg := NewStateGraph(MessagesStateSchema())
				sg.AddLLMNode("llm", llm, "", nil, WithModelCallbacks(callbacks)).
					SetEntryPoint("llm").SetFinishPoint("llm")
				executor := compileExecutorForWorkflowMetric(t, sg)
				invocation := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
					DisableTracing: disableTracing,
				}))
				ch, err := executor.Execute(context.Background(), State{}, invocation)
				require.NoError(t, err)
				var failure string
				for ev := range ch {
					if ev.Error != nil {
						failure = ev.Error.Message
					}
				}
				require.Contains(t, failure, tc.wantError, "finalization must preserve the existing failure")
				metricAttrs := collectChatErrorMetricAttributes(t, reader)
				require.Equal(t, "_OTHER", metricAttrs[semconvtrace.KeyErrorType].AsString())
				if disableTracing {
					require.Empty(t, recorder.Ended())
					return
				}
				var chatSpans int
				for _, span := range recorder.Ended() {
					if span.Name() != itelemetry.NewChatSpanName(llm.Info().Name) {
						continue
					}
					chatSpans++
					attrs := make(map[string]attribute.Value)
					for _, attr := range span.Attributes() {
						attrs[string(attr.Key)] = attr.Value
					}
					require.Equal(t, "chat", attrs[semconvtrace.KeyGenAIOperationName].AsString())
					require.Equal(t, llm.Info().Name, attrs[semconvtrace.KeyGenAIRequestModel].AsString())
					require.Equal(t, tc.wantResponseID, attrs[semconvtrace.KeyGenAIResponseID].AsString())
					require.Equal(t, metricAttrs[semconvtrace.KeyErrorType], attrs[semconvtrace.KeyErrorType])
					require.Equal(t, tc.wantError, attrs[semconvtrace.KeyErrorMessage].AsString())
					require.Equal(t, codes.Error, span.Status().Code)
					require.Equal(t, tc.wantError, span.Status().Description)
					require.Len(t, span.Events(), 1)
					require.Equal(t, "exception", span.Events()[0].Name)
				}
				require.Equal(t, 1, chatSpans)
			})
		}
	}
}

func useChatErrorMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalCounter := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalCounter
	})
	itelemetry.MeterProvider = provider
	counter, err := provider.Meter(semconvmetrics.MeterNameChat).Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = counter
	return reader
}

func collectChatErrorMetricAttributes(t *testing.T, reader *sdkmetric.ManualReader) map[string]attribute.Value {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	attrs := make(map[string]attribute.Value)
	var count int64
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "trpc_agent_go.client.request.cnt" {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			require.Len(t, sum.DataPoints, 1)
			count += sum.DataPoints[0].Value
			for _, attr := range sum.DataPoints[0].Attributes.ToSlice() {
				attrs[string(attr.Key)] = attr.Value
			}
		}
	}
	require.EqualValues(t, 1, count, "one chat request metric must be recorded")
	return attrs
}
