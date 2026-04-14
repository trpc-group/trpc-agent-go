//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// setupTracingProvider installs an in-memory span exporter and returns
// the exporter (for reading completed spans) and a cleanup function that
// restores the original global tracer.
func setupTracingProvider(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	t.Helper()

	origTracer := atrace.Tracer
	origProvider := atrace.TracerProvider

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	atrace.TracerProvider = tp
	atrace.Tracer = tp.Tracer("test")

	return exporter, func() {
		_ = tp.Shutdown(context.Background())
		atrace.Tracer = origTracer
		atrace.TracerProvider = origProvider
	}
}

// findSpan returns the first span with the given name, or nil.
func findSpan(spans tracetest.SpanStubs, name string) *tracetest.SpanStub {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

// spanAttr returns the string value of an attribute on a span, or "".
func spanAttr(s *tracetest.SpanStub, key string) string {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

// ============================================================================
// WithEnableTracing option
// ============================================================================

func TestWithEnableTracing(t *testing.T) {
	opts := ServiceOpts{}
	WithEnableTracing(true)(&opts)
	assert.True(t, opts.enableTracing)

	WithEnableTracing(false)(&opts)
	assert.False(t, opts.enableTracing)
}

// ============================================================================
// startSpan: tracing disabled -> no-op span
// ============================================================================

func TestStartSpan_TracingDisabled(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(false))
	require.NoError(t, err)
	defer svc.Close()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	ctx, span := svc.startSpan(context.Background(), "test_op", key)
	span.End()

	// No span should be recorded because tracing is disabled.
	assert.Empty(t, exporter.GetSpans())
	// ctx should be returned unchanged (no new span injected).
	assert.NotNil(t, ctx)
}

// ============================================================================
// startSpan: tracing enabled -> real span with attributes
// ============================================================================

func TestStartSpan_TracingEnabled(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	key := session.Key{AppName: "myapp", UserID: "u1", SessionID: "s1"}
	_, span := svc.startSpan(context.Background(), "test_op", key)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "test_op", spans[0].Name)
	assert.Equal(t, "myapp", spanAttr(&spans[0], "app_name"))
	assert.Equal(t, "u1", spanAttr(&spans[0], "user_id"))
	assert.Equal(t, "s1", spanAttr(&spans[0], "session_id"))
}

// ============================================================================
// CreateSession span
// ============================================================================

func TestCreateSession_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "cs1"}
	_, err = svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "create_session")
	require.NotNil(t, s, "expected create_session span")
	assert.Equal(t, "app", spanAttr(s, "app_name"))
	assert.Equal(t, "u1", spanAttr(s, "user_id"))
	assert.Equal(t, "cs1", spanAttr(s, "session_id"))
}

// ============================================================================
// GetSession span
// ============================================================================

func TestGetSession_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "gs1"}
	_, err = svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	_, err = svc.GetSession(context.Background(), key)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "get_session")
	require.NotNil(t, s, "expected get_session span")
	assert.Equal(t, "gs1", spanAttr(s, "session_id"))
}

// ============================================================================
// persistEvent span (append_event)
// ============================================================================

func TestPersistEvent_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "pe1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	e := createTestEvent("ev1", "agent", "hello", time.Now(), false)
	err = svc.AppendEvent(context.Background(), sess, e)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "append_event")
	require.NotNil(t, s, "expected append_event span from persistEvent")
	assert.Equal(t, "pe1", spanAttr(s, "session_id"))

	// Verify storage attribute is set.
	storageVal := spanAttr(s, "storage")
	assert.NotEmpty(t, storageVal)
}

// ============================================================================
// DeleteSession span
// ============================================================================

func TestDeleteSession_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ds1"}
	_, err = svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	err = svc.DeleteSession(context.Background(), key)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "delete_session")
	require.NotNil(t, s, "expected delete_session span")
	assert.Equal(t, "ds1", spanAttr(s, "session_id"))
}

// ============================================================================
// AppendTrackEvent span
// ============================================================================

func TestAppendTrackEvent_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "at1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`{"x":1}`),
		Timestamp: time.Now(),
	}
	err = svc.AppendTrackEvent(context.Background(), sess, te)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "append_track_event")
	require.NotNil(t, s, "expected append_track_event span")
	assert.Equal(t, "at1", spanAttr(s, "session_id"))
}

// ============================================================================
// CreateSessionSummary span
// ============================================================================

func TestCreateSessionSummary_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableTracing(true),
		WithSummarizer(&fakeSummarizer{allow: true, out: "traced-summary"}),
	)
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "css1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{
		{Message: model.Message{Role: model.RoleUser, Content: "hello"}},
	}}
	require.NoError(t, svc.AppendEvent(context.Background(), sess, e))

	sessGet, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)

	exporter.Reset()

	err = svc.CreateSessionSummary(context.Background(), sessGet, "", false)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "create_session_summary")
	require.NotNil(t, s, "expected create_session_summary span")
	assert.Equal(t, "css1", spanAttr(s, "session_id"))
}

// ============================================================================
// GetSessionSummaryText span
// ============================================================================

func TestGetSessionSummaryText_WithTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "gst1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	_, _ = svc.GetSessionSummaryText(context.Background(), sess)

	spans := exporter.GetSpans()
	s := findSpan(spans, "get_session_summary_text")
	require.NotNil(t, s, "expected get_session_summary_text span")
	assert.Equal(t, "gst1", spanAttr(s, "session_id"))
}

// ============================================================================
// Operations without tracing produce no spans
// ============================================================================

func TestOperations_TracingDisabled_NoSpans(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(false))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nospans"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	e := createTestEvent("ev1", "agent", "hello", time.Now(), false)
	_ = svc.AppendEvent(context.Background(), sess, e)
	_, _ = svc.GetSession(context.Background(), key)
	_ = svc.DeleteSession(context.Background(), key)

	assert.Empty(t, exporter.GetSpans(), "no spans should be recorded when tracing is disabled")
}

// ============================================================================
// persistEvent span with storage attribute on different paths
// ============================================================================

func TestPersistEvent_WithTracing_StorageAttribute_Zset(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
		WithEnableTracing(true),
	)
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "zset-traced"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	e := createTestEvent("ev1", "agent", "hello", time.Now(), false)
	err = svc.AppendEvent(context.Background(), sess, e)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "append_event")
	require.NotNil(t, s)
	assert.Equal(t, "zset", spanAttr(s, "storage"))
}

func TestPersistEvent_WithTracing_StorageAttribute_Hashidx(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeNone),
		WithEnableTracing(true),
	)
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "hashidx-traced"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	exporter.Reset()

	e := createTestEvent("ev1", "agent", "hello", time.Now(), false)
	err = svc.AppendEvent(context.Background(), sess, e)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	s := findSpan(spans, "append_event")
	require.NotNil(t, s)
	assert.Equal(t, "hashidx", spanAttr(s, "storage"))
}

// ============================================================================
// Span parent-child: persistEvent span is child of root span
// ============================================================================

func TestPersistEvent_SpanParentChild(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "parent-child"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	exporter.Reset()

	// Create a parent span.
	ctx, parentSpan := atrace.Tracer.Start(context.Background(), "parent_op")
	e := createTestEvent("ev1", "agent", "hello", time.Now(), false)
	err = svc.AppendEvent(ctx, sess, e)
	require.NoError(t, err)
	parentSpan.End()

	spans := exporter.GetSpans()
	parent := findSpan(spans, "parent_op")
	child := findSpan(spans, "append_event")
	require.NotNil(t, parent)
	require.NotNil(t, child)
	assert.Equal(t, parent.SpanContext.TraceID(), child.SpanContext.TraceID(),
		"child span should share the same trace ID as parent")
	assert.Equal(t, parent.SpanContext.SpanID(), child.Parent.SpanID(),
		"child span's parent should be the parent span")
}

// ============================================================================
// NewService with WithEnableTracing in integration test
// ============================================================================

func TestNewService_WithEnableTracing(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableTracing(true),
	)
	require.NoError(t, err)
	defer svc.Close()

	assert.True(t, svc.opts.enableTracing)
}

// ============================================================================
// startSpan attributes include all key fields even when empty
// ============================================================================

func TestStartSpan_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	exporter, cleanupTP := setupTracingProvider(t)
	defer cleanupTP()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{}
	_, span := svc.startSpan(context.Background(), "empty_key_op", key)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	// All three attributes should be present, even with empty values.
	found := map[string]bool{}
	for _, a := range spans[0].Attributes {
		if a.Key == attribute.Key("app_name") ||
			a.Key == attribute.Key("user_id") ||
			a.Key == attribute.Key("session_id") {
			found[string(a.Key)] = true
		}
	}
	assert.Len(t, found, 3, "all three key attributes should be present")
}
