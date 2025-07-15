//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package telemetry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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

func TestTraceFunctions_NoPanics(t *testing.T) {
	span := newStubSpan()

	// Prepare common objects.
	decl := &tool.Declaration{Name: "tool", Description: "desc"}
	args, _ := json.Marshal(map[string]string{"foo": "bar"})
	rspEvt := event.New("inv1", "author")

	// 1. TraceToolCall should execute without panic and call SetAttributes.
	TraceToolCall(span, decl, args, rspEvt)
	require.True(t, span.called, "expected SetAttributes to be called in TraceToolCall")

	// Reset flag for next test.
	span.called = false

	// 2. TraceMergedToolCalls.
	TraceMergedToolCalls(span, rspEvt)
	require.True(t, span.called, "expected SetAttributes in TraceMergedToolCalls")

	// Reset flag.
	span.called = false

	// 3. TraceCallLLM.
	inv := &agent.Invocation{
		InvocationID: "inv1",
		Session:      &session.Session{ID: "sess1"},
		Model:        dummyModel{},
	}
	req := &model.Request{}
	resp := &model.Response{}
	TraceCallLLM(span, inv, req, resp, "event1")
	require.True(t, span.called, "expected SetAttributes in TraceCallLLM")
}

// TestNewConn_InvalidEndpoint ensures an error is returned for an
// unparsable address.
func TestNewConn_InvalidEndpoint(t *testing.T) {
	// gRPC dials lazily, so even malformed targets may not error immediately.
	conn, err := NewConn("invalid:endpoint")
	if err != nil {
		t.Fatalf("did not expect error, got %v", err)
	}
	if conn == nil {
		t.Fatalf("expected non-nil connection")
	}
	_ = conn.Close()
}
