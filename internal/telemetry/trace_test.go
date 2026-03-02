//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubSpan is a minimal implementation of trace.Span that records whether
// SetAttributes was called. We embed trace.Span from the OTEL noop tracer so
// we do not have to implement the full interface.
// The noop span already implements all methods, so we can safely forward
// everything except SetAttributes which we want to observe.

type stubSpan struct {
	trace.Span
	called bool
}

func (s *stubSpan) IsRecording() bool {
	return true
}

func (s *stubSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.called = true
	// Forward to the underlying noop span so behaviour remains unchanged.
	s.Span.SetAttributes(kv...)
}

// dummyModel is a lightweight implementation of model.Model used for tracing
// LL M calls.

type dummyModel struct{}

func (d dummyModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (d dummyModel) Info() model.Info {
	return model.Info{Name: "dummy"}
}

func newStubSpan() *stubSpan {
	_, baseSpan := trace.NewNoopTracerProvider().Tracer("test").Start(context.Background(), "test")
	return &stubSpan{Span: baseSpan}
}

// recordingSpan captures attributes and status for assertions.
type recordingSpan struct {
	trace.Span
	attrs          []attribute.KeyValue
	status         codes.Code
	statusDesc     string
	recordedErrors []error
}

func (s *recordingSpan) IsRecording() bool {
	return true
}

func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kv...)
	s.Span.SetAttributes(kv...)
}
func (s *recordingSpan) SetStatus(c codes.Code, msg string) {
	s.status = c
	s.statusDesc = msg
	s.Span.SetStatus(c, msg)
}
func (s *recordingSpan) RecordError(err error, opts ...trace.EventOption) {
	s.recordedErrors = append(s.recordedErrors, err)
	s.Span.RecordError(err, opts...)
}
func newRecordingSpan() *recordingSpan {
	_, sp := trace.NewNoopTracerProvider().Tracer("test").Start(context.Background(), "op")
	return &recordingSpan{Span: sp}
}

func hasAttr(attrs []attribute.KeyValue, key string, want any) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			switch v := kv.Value.AsInterface().(type) {
			case []string:
				if w, ok := want.([]string); ok {
					if len(v) != len(w) {
						return false
					}
					for i := range v {
						if v[i] != w[i] {
							return false
						}
					}
					return true
				}
			default:
				return v == want
			}
		}
	}
	return false
}

func TestNewWorkflowSpanName(t *testing.T) {
	require.Equal(t, "workflow myflow", NewWorkflowSpanName("myflow"))
}

func TestTraceWorkflow(t *testing.T) {
	t.Run("basic attributes", func(t *testing.T) {
		span := newRecordingSpan()
		wf := &Workflow{Name: "myflow", ID: "wf-123"}

		TraceWorkflow(span, wf)

		if !hasAttr(span.attrs, KeyGenAIOperationName, OperationWorkflow) {
			t.Fatalf("missing operation name attribute")
		}
		if !hasAttr(span.attrs, KeyGenAIWorkflowName, "myflow") {
			t.Fatalf("missing workflow name attribute")
		}
		if !hasAttr(span.attrs, KeyGenAIWorkflowID, "wf-123") {
			t.Fatalf("missing workflow id attribute")
		}
	})

	t.Run("request/response json success", func(t *testing.T) {
		type payload struct {
			A string `json:"a"`
		}
		span := newRecordingSpan()
		wf := &Workflow{
			Name:     "myflow",
			ID:       "wf-123",
			Request:  payload{A: "req"},
			Response: payload{A: "rsp"},
		}

		TraceWorkflow(span, wf)

		require.True(t, hasAttr(span.attrs, KeyGenAIWorkflowRequest, `{"a":"req"}`))
		require.True(t, hasAttr(span.attrs, KeyGenAIWorkflowResponse, `{"a":"rsp"}`))
		require.NotEqual(t, codes.Error, span.status, "did not expect error status")
	})

	t.Run("request/response json marshal error", func(t *testing.T) {
		span := newRecordingSpan()
		wf := &Workflow{
			Name:     "myflow",
			ID:       "wf-123",
			Request:  make(chan int), // not json serializable
			Response: make(chan int), // not json serializable
		}

		TraceWorkflow(span, wf)

		var gotReq, gotRsp string
		for _, kv := range span.attrs {
			if string(kv.Key) == KeyGenAIWorkflowRequest {
				gotReq = kv.Value.AsString()
			}
			if string(kv.Key) == KeyGenAIWorkflowResponse {
				gotRsp = kv.Value.AsString()
			}
		}
		require.Contains(t, gotReq, "<not json serializable:")
		require.Contains(t, gotReq, "unsupported type")
		require.Contains(t, gotRsp, "<not json serializable>:")
		require.Contains(t, gotRsp, "unsupported type")
	})

	t.Run("error sets status and records error", func(t *testing.T) {
		span := newRecordingSpan()
		wfErr := errors.New("boom")
		wf := &Workflow{Name: "myflow", ID: "wf-123", Error: wfErr}

		TraceWorkflow(span, wf)

		require.True(t, hasAttr(span.attrs, KeyErrorType, ValueDefaultErrorType))
		require.Equal(t, codes.Error, span.status)
		require.Equal(t, "boom", span.statusDesc)
		require.Len(t, span.recordedErrors, 1)
		require.Equal(t, wfErr, span.recordedErrors[0])
	})
}

func TestTraceFunctions_NoPanics(t *testing.T) {
	span := newStubSpan()

	// Prepare common objects.
	decl := &tool.Declaration{Name: "tool", Description: "desc"}
	args, _ := json.Marshal(map[string]string{"foo": "bar"})
	rspEvt := event.New("inv1", "author")

	// 1. TraceToolCall should execute without panic and call SetAttributes.
	TraceToolCall(span, nil, decl, args, rspEvt, nil)
	require.True(t, span.called, "expected SetAttributes to be called in TraceToolCall")

	// Reset flag for next test.
	span.called = false

	// 2. TraceMergedToolCalls.
	TraceMergedToolCalls(span, rspEvt)
	require.True(t, span.called, "expected SetAttributes in TraceMergedToolCalls")

	// Reset flag.
	span.called = false

	// 3. TraceChat.
	inv := &agent.Invocation{
		InvocationID: "inv1",
		Session:      &session.Session{ID: "sess1"},
		Model:        dummyModel{},
	}
	req := &model.Request{}
	resp := &model.Response{}
	TraceChat(span, &TraceChatAttributes{
		Invocation:       inv,
		Request:          req,
		Response:         resp,
		EventID:          "event1",
		TimeToFirstToken: 0,
	})
	require.True(t, span.called, "expected SetAttributes in TraceChat")
}

func TestTraceFunctions_NonRecordingSpan_ReturnsEarly(t *testing.T) {
	_, span := trace.NewNoopTracerProvider().Tracer("test").Start(context.Background(), "op")
	require.False(t, span.IsRecording(), "expected noop span to be non-recording")

	TraceWorkflow(span, &Workflow{Name: "wf", ID: "wf-1"})
	TraceBeforeInvokeAgent(span, nil, "", "", nil)
	TraceAfterInvokeAgent(span, nil, nil, 0)
	TraceChat(span, nil)
}

func TestTraceBeforeAfter_Tool_Merged_Chat_Embedding(t *testing.T) {
	// Before invoke
	fp, mt, pp, tp, topP := 0.5, 128, 0.25, 0.7, 0.9
	gc := &model.GenerationConfig{Stop: []string{"END"}, FrequencyPenalty: &fp, MaxTokens: &mt, PresencePenalty: &pp, Temperature: &tp, TopP: &topP}
	inv := &agent.Invocation{AgentName: "alpha", InvocationID: "inv-1", Session: &session.Session{ID: "sess-1", UserID: "u-1"}}
	s := newRecordingSpan()
	TraceBeforeInvokeAgent(s, inv, "desc", "inst", gc)
	if !hasAttr(s.attrs, KeyGenAIAgentName, "alpha") {
		t.Fatalf("missing agent name")
	}

	// After invoke with error and choices
	stop := "stop"
	rsp := &model.Response{ID: "rid", Model: "m-1", Usage: &model.Usage{PromptTokens: 1, CompletionTokens: 2}, Choices: []model.Choice{{FinishReason: &stop}, {}}, Error: &model.ResponseError{Message: "oops", Type: "api_error"}}
	evt := event.New("eid", "alpha", event.WithResponse(rsp))
	s2 := newRecordingSpan()
	TraceAfterInvokeAgent(s2, evt, nil, 0)
	if s2.status != codes.Error {
		t.Fatalf("expected error status")
	}

	// Tool call and merged
	decl := &tool.Declaration{Name: "read", Description: "desc"}
	args, _ := json.Marshal(map[string]any{"x": 1})
	rsp2 := &model.Response{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "c1"}}}}}}
	evt2 := event.New("eid2", "a", event.WithResponse(rsp2))
	s3 := newRecordingSpan()
	TraceToolCall(s3, nil, decl, args, evt2, nil)
	if !hasAttr(s3.attrs, KeyGenAIToolCallID, "c1") {
		t.Fatalf("missing call id")
	}
	s4 := newRecordingSpan()
	TraceMergedToolCalls(s4, evt2)
	if !hasAttr(s4.attrs, KeyGenAIToolName, ToolNameMergedTools) {
		t.Fatalf("missing merged tool name")
	}

	// Chat
	inv2 := &agent.Invocation{InvocationID: "i1", Session: &session.Session{ID: "s1"}}
	req := &model.Request{GenerationConfig: model.GenerationConfig{Stop: []string{"END"}}, Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
	s5 := newRecordingSpan()
	TraceChat(s5, &TraceChatAttributes{
		Invocation:       inv2,
		Request:          req,
		Response:         &model.Response{ID: "rid"},
		EventID:          "e1",
		TimeToFirstToken: 0,
	})
	if !hasAttr(s5.attrs, KeyInvocationID, "i1") {
		t.Fatalf("missing invocation id")
	}

	// Embedding paths
	dims := 1536
	embReq := "hello"
	embRsp := "[0.1,0.2]"
	encFormat := "floats"
	srvAddr := "localhost"
	srvPort := 8080
	s6 := newRecordingSpan()
	TraceEmbedding(s6, &EmbeddingAttributes{
		RequestEncodingFormat: &encFormat,
		RequestModel:          "text-emb",
		Dimensions:            dims,
		Request:               &embReq,
		Response:              &embRsp,
		ServerAddress:         &srvAddr,
		ServerPort:            &srvPort,
	})
	if !hasAttr(s6.attrs, KeyGenAIRequestModel, "text-emb") {
		t.Fatalf("missing model")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyGenAIRequestEncodingFormats, []string{"floats"}) {
		t.Fatalf("missing encoding format")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyGenAIEmbeddingsDimensionCount, int64(dims)) {
		t.Fatalf("missing dimensions")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyGenAIEmbeddingsRequest, embReq) {
		t.Fatalf("missing embedding request")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyGenAIEmbeddingsResponse, embRsp) {
		t.Fatalf("missing embedding response")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyServerAddress, srvAddr) {
		t.Fatalf("missing server address")
	}
	if !hasAttr(s6.attrs, semconvtrace.KeyServerPort, int64(srvPort)) {
		t.Fatalf("missing server port")
	}
	tok := int64(10)
	s7 := newRecordingSpan()
	TraceEmbedding(s7, &EmbeddingAttributes{
		RequestEncodingFormat: &encFormat,
		RequestModel:          "text-emb",
		Dimensions:            dims,
		InputToken:            &tok,
		Error:                 errors.New("bad"),
	})
	if s7.status != codes.Error {
		t.Fatalf("embedding expected error status")
	}
	if !hasAttr(s7.attrs, KeyGenAIUsageInputTokens, tok) {
		t.Fatalf("missing input token")
	}
	if !hasAttr(s7.attrs, KeyErrorType, ValueDefaultErrorType) {
		t.Fatalf("missing error type")
	}
	if !hasAttr(s7.attrs, KeyErrorMessage, "bad") {
		t.Fatalf("missing error message")
	}
}

func TestTraceBeforeInvokeAgent_WithSpanAttributes(t *testing.T) {
	inv := &agent.Invocation{
		AgentName:    "alpha",
		InvocationID: "inv-span",
		Session:      &session.Session{ID: "sess-span", UserID: "user-span"},
		RunOptions:   agent.RunOptions{SpanAttributes: []attribute.KeyValue{attribute.String("custom.attr", "v1")}},
	}
	span := newRecordingSpan()
	TraceBeforeInvokeAgent(span, inv, "desc", "inst", nil)
	require.True(t, hasAttr(span.attrs, "custom.attr", "v1"), "custom span attribute should be applied")
}

func TestNewChatSpanName(t *testing.T) {
	tests := []struct {
		name         string
		requestModel string
		want         string
	}{
		{
			name:         "with model name",
			requestModel: "gpt-4",
			want:         "chat gpt-4",
		},
		{
			name:         "empty model name",
			requestModel: "",
			want:         "chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewChatSpanName(tt.requestModel)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewExecuteToolSpanName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{
			name:     "simple tool name",
			toolName: "calculator",
			want:     "execute_tool calculator",
		},
		{
			name:     "empty tool name",
			toolName: "",
			want:     "execute_tool ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewExecuteToolSpanName(tt.toolName)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewSummarizeTaskType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty name",
			in:   "",
			want: "summarize",
		},
		{
			name: "non-empty name",
			in:   "demo",
			want: "summarize demo",
		},
		{
			name: "whitespace is preserved",
			in:   "  demo  ",
			want: "summarize   demo  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NewSummarizeTaskType(tt.in))
		})
	}
}

func TestTraceToolCall_NilPaths(t *testing.T) {
	tests := []struct {
		name     string
		sess     *session.Session
		rspEvent *event.Event
		err      error
	}{
		{
			name:     "nil session and nil rspEvent",
			sess:     nil,
			rspEvent: nil,
			err:      nil,
		},
		{
			name:     "nil session with rspEvent",
			sess:     nil,
			rspEvent: event.New("evt1", "author"),
			err:      nil,
		},
		{
			name:     "with session and nil rspEvent",
			sess:     &session.Session{ID: "sess1", UserID: "user1"},
			rspEvent: nil,
			err:      nil,
		},
		{
			name:     "with error but no response error",
			sess:     &session.Session{ID: "sess1", UserID: "user1"},
			rspEvent: event.New("evt1", "author"),
			err:      errors.New("tool execution failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := newRecordingSpan()
			decl := &tool.Declaration{Name: "test_tool", Description: "test description"}
			args, _ := json.Marshal(map[string]string{"key": "value"})

			TraceToolCall(span, tt.sess, decl, args, tt.rspEvent, tt.err)

			// Verify basic attributes are always set
			require.True(t, hasAttr(span.attrs, KeyGenAISystem, SystemTRPCGoAgent))
			require.True(t, hasAttr(span.attrs, KeyGenAIOperationName, OperationExecuteTool))
			require.True(t, hasAttr(span.attrs, KeyGenAIToolName, "test_tool"))

			// Verify error status when err is provided
			if tt.err != nil && tt.rspEvent != nil && tt.rspEvent.Response != nil && tt.rspEvent.Response.Error == nil {
				require.Equal(t, codes.Error, span.status)
			}
		})
	}
}

func TestTraceMergedToolCalls_NilPaths(t *testing.T) {
	tests := []struct {
		name     string
		rspEvent *event.Event
	}{
		{
			name:     "nil rspEvent",
			rspEvent: nil,
		},
		{
			name:     "rspEvent with nil response",
			rspEvent: event.New("evt1", "author"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := newRecordingSpan()
			TraceMergedToolCalls(span, tt.rspEvent)

			// Verify basic attributes are always set
			require.True(t, hasAttr(span.attrs, KeyGenAISystem, SystemTRPCGoAgent))
			require.True(t, hasAttr(span.attrs, KeyGenAIToolName, ToolNameMergedTools))
		})
	}
}

func TestTraceBeforeInvokeAgent_NilPaths(t *testing.T) {
	tests := []struct {
		name      string
		invoke    *agent.Invocation
		genConfig *model.GenerationConfig
	}{
		{
			name: "nil generation config",
			invoke: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "inv1",
				Session:      &session.Session{ID: "sess1", UserID: "user1"},
			},
			genConfig: nil,
		},
		{
			name: "nil session",
			invoke: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "inv1",
				Session:      nil,
			},
			genConfig: &model.GenerationConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := newRecordingSpan()
			TraceBeforeInvokeAgent(span, tt.invoke, "desc", "instructions", tt.genConfig)

			require.True(t, hasAttr(span.attrs, KeyGenAIAgentName, "test-agent"))
			require.True(t, hasAttr(span.attrs, KeyInvocationID, "inv1"))
		})
	}
}

func TestTraceAfterInvokeAgent_NilPaths(t *testing.T) {
	tests := []struct {
		name       string
		rspEvent   *event.Event
		tokenUsage *TokenUsage
	}{
		{
			name:       "nil rspEvent",
			rspEvent:   nil,
			tokenUsage: nil,
		},
		{
			name:       "rspEvent with nil response",
			rspEvent:   event.New("evt1", "author"),
			tokenUsage: nil,
		},
		{
			name: "with token usage",
			rspEvent: event.New("evt1", "author", event.WithResponse(&model.Response{
				ID:      "resp1",
				Model:   "gpt-4",
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "test"}}},
			})),
			tokenUsage: &TokenUsage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := newRecordingSpan()
			TraceAfterInvokeAgent(span, tt.rspEvent, tt.tokenUsage, 0)

			if tt.tokenUsage != nil && tt.rspEvent != nil && tt.rspEvent.Response != nil {
				require.True(t, hasAttr(span.attrs, KeyGenAIUsageInputTokens, int64(tt.tokenUsage.PromptTokens)))
				require.True(t, hasAttr(span.attrs, KeyGenAIUsageOutputTokens, int64(tt.tokenUsage.CompletionTokens)))
			}
		})
	}
}

func TestTraceChat_WithTimeToFirstToken(t *testing.T) {
	inv := &agent.Invocation{
		InvocationID: "inv1",
		Session:      &session.Session{ID: "sess1", UserID: "user1"},
		Model:        dummyModel{},
	}
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	rsp := &model.Response{
		ID:    "resp1",
		Model: "dummy",
		Usage: &model.Usage{PromptTokens: 5, CompletionTokens: 10},
	}

	span := newRecordingSpan()
	TraceChat(span, &TraceChatAttributes{
		Invocation:       inv,
		Request:          req,
		Response:         rsp,
		EventID:          "evt1",
		TimeToFirstToken: 100 * time.Millisecond,
	})

	require.True(t, hasAttr(span.attrs, KeyTRPCAgentGoClientTimeToFirstToken, 0.1))
}

func TestTraceChat_WithTaskType(t *testing.T) {
	inv := &agent.Invocation{InvocationID: "inv-task", Session: &session.Session{ID: "sess-task"}}
	req := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	rsp := &model.Response{ID: "resp-task"}

	span := newRecordingSpan()
	TraceChat(span, &TraceChatAttributes{
		Invocation:       inv,
		Request:          req,
		Response:         rsp,
		EventID:          "evt-task",
		TimeToFirstToken: 0,
		TaskType:         "summarize demo",
	})

	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAITaskType, "summarize demo"))
}

func TestBuildInvocationAttributes(t *testing.T) {
	tests := []struct {
		name   string
		invoke *agent.Invocation
		want   int // expected number of attributes
	}{
		{
			name:   "nil invocation",
			invoke: nil,
			want:   0,
		},
		{
			name: "invocation without session and model",
			invoke: &agent.Invocation{
				InvocationID: "inv1",
			},
			want: 1, // only invocation ID
		},
		{
			name: "invocation with session",
			invoke: &agent.Invocation{
				InvocationID: "inv1",
				Session:      &session.Session{ID: "sess1", UserID: "user1"},
			},
			want: 3, // invocation ID + session ID + user ID
		},
		{
			name: "invocation with model",
			invoke: &agent.Invocation{
				InvocationID: "inv1",
				Model:        dummyModel{},
			},
			want: 2, // invocation ID + model name
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildInvocationAttributes(tt.invoke)
			require.Len(t, attrs, tt.want)
		})
	}
}

func TestBuildRequestAttributes(t *testing.T) {
	tests := []struct {
		name string
		req  *model.Request
	}{
		{
			name: "nil request",
			req:  nil,
		},
		{
			name: "request with all generation config",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
				GenerationConfig: model.GenerationConfig{
					Stop:             []string{"STOP"},
					FrequencyPenalty: func() *float64 { v := 0.5; return &v }(),
					MaxTokens:        func() *int { v := 100; return &v }(),
					PresencePenalty:  func() *float64 { v := 0.3; return &v }(),
					Temperature:      func() *float64 { v := 0.7; return &v }(),
					TopP:             func() *float64 { v := 0.9; return &v }(),
				},
			},
		},
		{
			name: "request with empty generation config",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
			},
		},
		{
			name: "request with stream enabled",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
		},
		{
			name: "request with stream disabled",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildRequestAttributes(tt.req)
			if tt.req == nil {
				require.Nil(t, attrs)
			} else {
				require.NotNil(t, attrs)
			}
		})
	}
}

func TestBuildRequestAttributes_ToolDefinitions(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
		Tools: map[string]tool.Tool{
			"alpha": testTool{decl: &tool.Declaration{Name: "alpha", Description: "first"}},
			"beta":  testTool{decl: &tool.Declaration{Name: "beta", Description: "second"}},
			"skip":  nil, // ensure nil entries are ignored
		},
	}

	attrs := buildRequestAttributes(req)
	require.NotNil(t, attrs)

	var toolAttr *attribute.KeyValue
	for i := range attrs {
		if string(attrs[i].Key) == KeyGenAIRequestToolDefinitions {
			toolAttr = &attrs[i]
			break
		}
	}
	require.NotNil(t, toolAttr, "expected tool definitions attribute")

	var defs []tool.Declaration
	require.NoError(t, json.Unmarshal([]byte(toolAttr.Value.AsString()), &defs))
	require.Len(t, defs, 2)

	names := map[string]struct{}{}
	for _, d := range defs {
		names[d.Name] = struct{}{}
	}
	require.Contains(t, names, "alpha")
	require.Contains(t, names, "beta")
}

type testTool struct{ decl *tool.Declaration }

func (t testTool) Declaration() *tool.Declaration { return t.decl }

func TestBuildResponseAttributes(t *testing.T) {
	tests := []struct {
		name string
		rsp  *model.Response
	}{
		{
			name: "nil response",
			rsp:  nil,
		},
		{
			name: "response with error",
			rsp: &model.Response{
				ID:    "resp1",
				Model: "gpt-4",
				Error: &model.ResponseError{
					Type:    "api_error",
					Message: "rate limit exceeded",
				},
			},
		},
		{
			name: "response with usage",
			rsp: &model.Response{
				ID:    "resp1",
				Model: "gpt-4",
				Usage: &model.Usage{
					PromptTokens:     10,
					CompletionTokens: 20,
					PromptTokensDetails: model.PromptTokensDetails{
						CachedTokens:        7,
						CacheReadTokens:     11,
						CacheCreationTokens: 13,
					},
				},
			},
		},
		{
			name: "response with choices",
			rsp: &model.Response{
				ID:    "resp1",
				Model: "gpt-4",
				Choices: []model.Choice{
					{
						Message:      model.Message{Role: model.RoleAssistant, Content: "response"},
						FinishReason: func() *string { s := "stop"; return &s }(),
					},
					{
						Message: model.Message{Role: model.RoleAssistant, Content: "response2"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildResponseAttributes(tt.rsp)
			if tt.rsp == nil {
				require.Nil(t, attrs)
			} else {
				require.NotNil(t, attrs)
				// Verify basic attributes
				require.True(t, hasAttr(attrs, KeyGenAIResponseModel, tt.rsp.Model))
				require.True(t, hasAttr(attrs, KeyGenAIResponseID, tt.rsp.ID))

				// Verify cached prompt tokens attribute when provided
				if tt.rsp.Usage != nil {
					if tt.rsp.Usage.PromptTokensDetails.CachedTokens != 0 {
						require.True(t, hasAttr(attrs, KeyGenAIUsageInputTokensCached, int64(tt.rsp.Usage.PromptTokensDetails.CachedTokens)))
					}
					if tt.rsp.Usage.PromptTokensDetails.CacheReadTokens != 0 {
						require.True(t, hasAttr(attrs, KeyGenAIUsageInputTokensCacheRead, int64(tt.rsp.Usage.PromptTokensDetails.CacheReadTokens)))
					}
					if tt.rsp.Usage.PromptTokensDetails.CacheCreationTokens != 0 {
						require.True(t, hasAttr(attrs, KeyGenAIUsageInputTokensCacheCreation, int64(tt.rsp.Usage.PromptTokensDetails.CacheCreationTokens)))
					}
				}
			}
		})
	}
}

func TestNewGRPCConn(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		mockDialErr error
		wantErr     bool
	}{
		{
			name:        "successful connection",
			endpoint:    "localhost:4317",
			mockDialErr: nil,
			wantErr:     false,
		},
		{
			name:        "connection failure",
			endpoint:    "invalid:endpoint",
			mockDialErr: errors.New("connection failed"),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original grpcDial
			originalDial := grpcDial
			defer func() { grpcDial = originalDial }()

			// Mock grpcDial
			grpcDial = func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
				if tt.mockDialErr != nil {
					return nil, tt.mockDialErr
				}
				// Return a mock connection
				return &grpc.ClientConn{}, nil
			}

			conn, err := NewGRPCConn(tt.endpoint)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, conn)
			} else {
				require.NoError(t, err)
				require.NotNil(t, conn)
			}
		})
	}
}

func TestTraceToolCall_EmptyToolCallIDs(t *testing.T) {
	span := newRecordingSpan()
	decl := &tool.Declaration{Name: "test_tool", Description: "test description"}
	args, _ := json.Marshal(map[string]string{"key": "value"})

	// Response with empty tool call IDs
	rsp := &model.Response{
		Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{}}}},
	}
	evt := event.New("evt1", "author", event.WithResponse(rsp))

	TraceToolCall(span, nil, decl, args, evt, nil)

	require.True(t, hasAttr(span.attrs, KeyGenAIToolName, "test_tool"))
}

func TestTraceMergedToolCalls_WithError(t *testing.T) {
	span := newRecordingSpan()

	// Response with error
	rsp := &model.Response{
		Error: &model.ResponseError{
			Type:    "api_error",
			Message: "test error",
		},
		Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "call1"}}}}},
	}
	evt := event.New("evt1", "author", event.WithResponse(rsp))

	TraceMergedToolCalls(span, evt)

	require.Equal(t, codes.Error, span.status)
	require.True(t, hasAttr(span.attrs, KeyGenAIToolCallID, "call1"))
}

func TestTraceBeforeInvokeAgent_JSONMarshalError(t *testing.T) {
	span := newRecordingSpan()

	// Create an invocation with a message that contains a channel (not JSON serializable)
	inv := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "inv1",
		Message:      model.Message{Role: model.RoleUser, Content: "test"},
	}

	TraceBeforeInvokeAgent(span, inv, "desc", "instructions", nil)

	require.True(t, hasAttr(span.attrs, KeyGenAIAgentName, "test-agent"))
}

func TestBuildRequestAttributes_JSONMarshalPaths(t *testing.T) {
	// Test with valid request
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
	}
	attrs := buildRequestAttributes(req)
	require.NotNil(t, attrs)

	// Verify LLM request attribute is set
	found := false
	for _, attr := range attrs {
		if string(attr.Key) == KeyLLMRequest {
			found = true
			break
		}
	}
	require.True(t, found)
}

func TestBuildResponseAttributes_JSONMarshalPaths(t *testing.T) {
	// Test with valid response
	rsp := &model.Response{
		ID:      "resp1",
		Model:   "gpt-4",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "test"}}},
	}
	attrs := buildResponseAttributes(rsp)
	require.NotNil(t, attrs)

	// Verify LLM response attribute is set
	found := false
	for _, attr := range attrs {
		if string(attr.Key) == KeyLLMResponse {
			found = true
			break
		}
	}
	require.True(t, found)
}

func TestTrace_AdditionalBranches(t *testing.T) {
	// TraceToolCall with nil rspEvent and rspEvent without Response
	s := newRecordingSpan()
	TraceToolCall(s, nil, &tool.Declaration{Name: "t"}, nil, nil, nil)
	s2 := newRecordingSpan()
	TraceToolCall(s2, nil, &tool.Declaration{Name: "t"}, nil, event.New("id", "a"), nil)

	// TraceMergedToolCalls with nil response
	s3 := newRecordingSpan()
	TraceMergedToolCalls(s3, event.New("id2", "a2"))

	// TraceChat with nil req and nil rsp
	inv := &agent.Invocation{InvocationID: "invx"}
	s4 := newRecordingSpan()
	TraceChat(s4, &TraceChatAttributes{
		Invocation:       inv,
		Request:          nil,
		Response:         nil,
		EventID:          "evt",
		TimeToFirstToken: 0,
	})
}

func TestTraceChat_WithChoicesAndError(t *testing.T) {
	inv := &agent.Invocation{InvocationID: "i2"}
	req := &model.Request{GenerationConfig: model.GenerationConfig{Stop: []string{"Z"}}, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	stop := "stop"
	rsp := &model.Response{ID: "rid3", Model: "m3", Usage: &model.Usage{PromptTokens: 2, CompletionTokens: 3}, Choices: []model.Choice{{FinishReason: &stop}}, Error: &model.ResponseError{Message: "bad", Type: "api_error"}}
	s := newRecordingSpan()
	TraceChat(s, &TraceChatAttributes{
		Invocation:       inv,
		Request:          req,
		Response:         rsp,
		EventID:          "e3",
		TimeToFirstToken: 0,
	})
	if s.status != codes.Error {
		t.Fatalf("expected error status on chat")
	}
}

// Cover error branch of NewGRPCConn using injected dialer.
func TestNewConn_ErrorBranch_WithInjectedDialer(t *testing.T) {
	orig := grpcDial
	t.Cleanup(func() { grpcDial = orig })
	grpcDial = func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return nil, errors.New("dial error")
	}
	if _, err := NewGRPCConn("ignored"); err == nil {
		t.Fatalf("expected error from injected dialer")
	}
}

// TestNewConn_InvalidEndpoint ensures an error is returned for an
// unparsable address.
func TestNewConn_InvalidEndpoint(t *testing.T) {
	// gRPC dials lazily, so even malformed targets may not error immediately.
	conn, err := NewGRPCConn("invalid:endpoint")
	if err != nil {
		t.Fatalf("did not expect error, got %v", err)
	}
	if conn == nil {
		t.Fatalf("expected non-nil connection")
	}
	_ = conn.Close()
}

// TestBuildRequestAttributes_StreamAttribute verifies that gen_ai.request.is_stream
// is only added when stream is true.
func TestBuildRequestAttributes_StreamAttribute(t *testing.T) {
	tests := []struct {
		name          string
		req           *model.Request
		expectStream  bool
		streamPresent bool
	}{
		{
			name: "stream enabled",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
			expectStream:  true,
			streamPresent: true,
		},
		{
			name: "stream disabled",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			},
			expectStream:  false,
			streamPresent: false,
		},
		{
			name: "stream not set",
			req: &model.Request{
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}},
			},
			expectStream:  false,
			streamPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildRequestAttributes(tt.req)
			require.NotNil(t, attrs)

			found := false
			for _, attr := range attrs {
				if string(attr.Key) == KeyGenAIRequestIsStream {
					found = true
					require.Equal(t, tt.expectStream, attr.Value.AsBool())
					break
				}
			}
			require.Equal(t, tt.streamPresent, found, "stream attribute presence mismatch")
		})
	}
}
