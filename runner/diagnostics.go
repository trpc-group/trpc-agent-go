//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

const (
	runnerLatencySpanRun             = "runner.run"
	runnerLatencySpanGetSession      = "runner.session.get_or_create"
	runnerLatencySpanAwaitRoute      = "runner.await_user_reply.route"
	runnerLatencySpanSelectAgent     = "runner.agent.select"
	runnerLatencySpanResolveMessages = "runner.messages.resolve_current_turn"
	runnerLatencySpanPersistTurn     = "runner.messages.persist_current_turn"
	runnerLatencySpanRegisterRun     = "runner.run.register"
	runnerLatencySpanStartAgent      = "runner.agent.start"
	runnerLatencySpanEventLoop       = "runner.event_loop"
	runnerLatencySpanProcessEvent    = "runner.event.process"
	runnerLatencySpanEventPlugins    = "runner.event.plugins"
	runnerLatencySpanPersistEvent    = "runner.event.persist"
	runnerLatencySpanEnqueueSummary  = "runner.summary.enqueue"
	runnerLatencySpanEmitEvent       = "runner.event.emit"
	runnerLatencySpanFlush           = "runner.flush"
	runnerLatencySpanCompletion      = "runner.completion.emit"
	runnerLatencySpanInterrupted     = "runner.interrupted.persist"
)

func runnerLatencyEnabled(inv *agent.Invocation) bool {
	return inv != nil && inv.RunOptions.LatencyDiagnosticsEnabled
}

func runnerRunOptionsLatencyEnabled(ro agent.RunOptions) bool {
	return ro.LatencyDiagnosticsEnabled && !ro.DisableTracing
}

func startRunnerLatencySpan(
	ctx context.Context,
	inv *agent.Invocation,
	name string,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span, bool) {
	if !runnerLatencyEnabled(inv) {
		return ctx, noop.Span{}, false
	}
	ctx, span, started := itrace.StartSpan(ctx, inv, name)
	if started && len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span, started
}

func startRunnerRunOptionsLatencySpan(
	ctx context.Context,
	ro agent.RunOptions,
	name string,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span, bool) {
	if !runnerRunOptionsLatencyEnabled(ro) {
		return ctx, noop.Span{}, false
	}
	ctx, span := telemetrytrace.Tracer.Start(ctx, name)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span, true
}

func finishRunnerLatencySpan(span oteltrace.Span, started bool, err error) {
	if !started {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func runnerRunAttrs(
	appName string,
	userID string,
	sessionID string,
	message model.Message,
	ro agent.RunOptions,
) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("runner.app", appName),
		attribute.String("runner.user_id", userID),
		attribute.String("runner.session_id", sessionID),
		attribute.String("runner.request_id", ro.RequestID),
		attribute.String("runner.message.role", string(message.Role)),
		attribute.Bool("runner.message.has_payload", model.HasPayload(message)),
		attribute.Int("runner.options.seed_messages", len(ro.Messages)),
	}
}

func runnerInvocationAttrs(inv *agent.Invocation) []attribute.KeyValue {
	if inv == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String("runner.invocation_id", inv.InvocationID),
		attribute.String("runner.agent", inv.AgentName),
		attribute.String("runner.request_id", inv.RunOptions.RequestID),
	}
}

func runnerSessionAttrs(key session.Key, sess *session.Session) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("runner.session.app", key.AppName),
		attribute.String("runner.session.user", key.UserID),
		attribute.String("runner.session.id", key.SessionID),
	}
	if sess != nil {
		attrs = append(
			attrs,
			attribute.Int("runner.session.events", sess.GetEventCount()),
		)
	}
	return attrs
}

func runnerEventAttrs(evt *event.Event) []attribute.KeyValue {
	if evt == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("runner.event.id", evt.ID),
		attribute.String("runner.event.object", evt.Object),
		attribute.Bool("runner.event.partial", evt.IsPartial),
		attribute.Bool("runner.event.done", evt.Done),
		attribute.Bool("runner.event.requires_completion", evt.RequiresCompletion),
		attribute.Int("runner.event.state_delta_keys", len(evt.StateDelta)),
	}
	if evt.Response != nil {
		attrs = append(
			attrs,
			attribute.Int("runner.event.choices", len(evt.Response.Choices)),
		)
	}
	return attrs
}
