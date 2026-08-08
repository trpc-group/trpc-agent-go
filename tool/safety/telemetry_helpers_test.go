//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// recordingSpan is a test span that records SetAttributes calls. It
// implements trace.Span via the embedded embedded.Span shim; only the
// methods the guard touches are populated.
type recordingSpan struct {
	embedded.Span
	mu    sync.Mutex
	attrs map[string]any
}

func newRecordingSpan() (context.Context, *recordingSpan) {
	span := &recordingSpan{attrs: map[string]any{}}
	return trace.ContextWithSpan(context.Background(), span), span
}

func (s *recordingSpan) IsRecording() bool { return true }

func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range kv {
		s.attrs[string(k.Key)] = k.Value.AsInterface()
	}
}

func (s *recordingSpan) End(...trace.SpanEndOption) {}

// AddEvent, AddLink, RecordError, SpanContext, SetStatus, SetName, and
// TracerProvider are required by trace.Span but not used by the guard.
func (s *recordingSpan) AddEvent(string, ...trace.EventOption)   {}
func (s *recordingSpan) AddLink(trace.Link)                      {}
func (s *recordingSpan) RecordError(error, ...trace.EventOption) {}
func (s *recordingSpan) SpanContext() trace.SpanContext          { return trace.SpanContext{} }
func (s *recordingSpan) SetStatus(codes.Code, string)            {}
func (s *recordingSpan) SetName(string)                          {}
func (s *recordingSpan) TracerProvider() trace.TracerProvider    { return nil }

func (s *recordingSpan) attributes() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.attrs))
	for k, v := range s.attrs {
		out[k] = v
	}
	return out
}
