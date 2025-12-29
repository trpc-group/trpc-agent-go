//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	"go.opentelemetry.io/otel/trace/noop"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

type errorModel struct {
	name string
	err  error
}

func (m *errorModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return nil, m.err
}

func (m *errorModel) Info() model.Info { return model.Info{Name: m.name} }

// recordingSpan is a minimal Span implementation that records calls relevant to our tests.
type recordingSpan struct {
	embedded.Span

	mu sync.Mutex

	recordedErrors []error
	statusCode     codes.Code
	statusDesc     string
}

func (s *recordingSpan) End(options ...oteltrace.SpanEndOption)                 {}
func (s *recordingSpan) AddEvent(name string, options ...oteltrace.EventOption) {}
func (s *recordingSpan) AddLink(link oteltrace.Link)                            {}
func (s *recordingSpan) IsRecording() bool                                      { return true }

func (s *recordingSpan) RecordError(err error, options ...oteltrace.EventOption) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordedErrors = append(s.recordedErrors, err)
}

func (s *recordingSpan) SpanContext() oteltrace.SpanContext {
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{})
}

func (s *recordingSpan) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCode = code
	s.statusDesc = description
}

func (s *recordingSpan) SetName(name string)                      {}
func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue)   {}
func (s *recordingSpan) TracerProvider() oteltrace.TracerProvider { return noop.NewTracerProvider() }

type recordingTracer struct {
	embedded.Tracer

	mu    sync.Mutex
	spans map[string]*recordingSpan
}

func (t *recordingTracer) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.spans == nil {
		t.spans = make(map[string]*recordingSpan)
	}
	sp := &recordingSpan{}
	t.spans[spanName] = sp
	return ctx, sp
}

func TestNewLLMNodeFunc_RunModelError_RecordsSpanAndWrapsError(t *testing.T) {
	origTracer := trace.Tracer
	rt := &recordingTracer{}
	trace.Tracer = rt
	defer func() { trace.Tracer = origTracer }()

	llm := &errorModel{name: "bad-model", err: errors.New("boom")}
	fn := NewLLMNodeFunc(llm, "", nil)

	_, err := fn(context.Background(), State{})
	require.Error(t, err)
	// This path wraps at executeModelWithEvents AND again at NewLLMNodeFunc.
	require.Contains(t, err.Error(), "failed to run model:")
	require.Contains(t, err.Error(), "failed to generate content:")

	chatSpanName := itelemetry.NewChatSpanName(llm.Info().Name)
	rt.mu.Lock()
	sp := rt.spans[chatSpanName]
	rt.mu.Unlock()
	require.NotNil(t, sp, "expected chat span %q to be started", chatSpanName)

	sp.mu.Lock()
	defer sp.mu.Unlock()
	require.NotEmpty(t, sp.recordedErrors, "expected span.RecordError to be called")
	require.Equal(t, codes.Error, sp.statusCode)
	require.NotEmpty(t, sp.statusDesc)
	// Status is set on the intermediate error (before the outer wrapper returns).
	require.Contains(t, sp.statusDesc, "failed to run model:")
}

func TestExecuteModelWithEvents_RunModelError_RecordsSpanAndWrapsError(t *testing.T) {
	llm := &errorModel{name: "bad-model", err: errors.New("boom")}
	sp := &recordingSpan{}

	_, err := executeModelWithEvents(context.Background(), modelExecutionConfig{
		LLMModel: llm,
		Request:  &model.Request{},
		Span:     sp,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "failed to run model:"), err.Error())
	require.True(t, strings.Contains(err.Error(), "failed to generate content:"), err.Error())

	sp.mu.Lock()
	defer sp.mu.Unlock()
	require.NotEmpty(t, sp.recordedErrors, "expected span.RecordError to be called")
	require.Equal(t, codes.Error, sp.statusCode)
	require.True(t, strings.Contains(sp.statusDesc, "failed to generate content:"), sp.statusDesc)
}
