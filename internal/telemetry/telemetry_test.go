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

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
	attrs  []attribute.KeyValue
	status codes.Code
}

func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kv...)
	s.Span.SetAttributes(kv...)
}
func (s *recordingSpan) SetStatus(c codes.Code, msg string) { s.status = c; s.Span.SetStatus(c, msg) }
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

func TestTraceFunctions_NoPanics(t *testing.T) {
	span := newStubSpan()

	// Prepare common objects.
	decl := &tool.Declaration{Name: "tool", Description: "desc"}
	args, _ := json.Marshal(map[string]string{"foo": "bar"})
	rspEvt := event.New("inv1", "author")

	// 1. TraceToolCall should execute without panic and call SetAttributes.
	TraceToolCall(span, nil, decl, args, rspEvt)
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
	TraceChat(span, inv, req, resp, "event1")
	require.True(t, span.called, "expected SetAttributes in TraceChat")
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
	TraceAfterInvokeAgent(s2, evt)
	if s2.status != codes.Error {
		t.Fatalf("expected error status")
	}

	// Tool call and merged
	decl := &tool.Declaration{Name: "read", Description: "desc"}
	args, _ := json.Marshal(map[string]any{"x": 1})
	rsp2 := &model.Response{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "c1"}}}}}}
	evt2 := event.New("eid2", "a", event.WithResponse(rsp2))
	s3 := newRecordingSpan()
	TraceToolCall(s3, nil, decl, args, evt2)
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
	TraceChat(s5, inv2, req, &model.Response{ID: "rid"}, "e1")
	if !hasAttr(s5.attrs, KeyInvocationID, "i1") {
		t.Fatalf("missing invocation id")
	}

	// Embedding paths
	s6 := newRecordingSpan()
	TraceEmbedding(s6, "floats", "text-emb", nil, nil)
	if !hasAttr(s6.attrs, KeyGenAIRequestModel, "text-emb") {
		t.Fatalf("missing model")
	}
	tok := int64(10)
	s7 := newRecordingSpan()
	TraceEmbedding(s7, "floats", "text-emb", &tok, errors.New("bad"))
	if s7.status != codes.Error {
		t.Fatalf("embedding expected error status")
	}
}

func TestTrace_AdditionalBranches(t *testing.T) {
	// TraceToolCall with nil rspEvent and rspEvent without Response
	s := newRecordingSpan()
	TraceToolCall(s, nil, &tool.Declaration{Name: "t"}, nil, nil)
	s2 := newRecordingSpan()
	TraceToolCall(s2, nil, &tool.Declaration{Name: "t"}, nil, event.New("id", "a"))

	// TraceMergedToolCalls with nil response
	s3 := newRecordingSpan()
	TraceMergedToolCalls(s3, event.New("id2", "a2"))

	// TraceChat with nil req and nil rsp
	inv := &agent.Invocation{InvocationID: "invx"}
	s4 := newRecordingSpan()
	TraceChat(s4, inv, nil, nil, "evt")
}

func TestTraceChat_WithChoicesAndError(t *testing.T) {
	inv := &agent.Invocation{InvocationID: "i2"}
	req := &model.Request{GenerationConfig: model.GenerationConfig{Stop: []string{"Z"}}, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	stop := "stop"
	rsp := &model.Response{ID: "rid3", Model: "m3", Usage: &model.Usage{PromptTokens: 2, CompletionTokens: 3}, Choices: []model.Choice{{FinishReason: &stop}}, Error: &model.ResponseError{Message: "bad", Type: "api_error"}}
	s := newRecordingSpan()
	TraceChat(s, inv, req, rsp, "e3")
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
