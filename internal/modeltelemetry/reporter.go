//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modeltelemetry provides opt-in telemetry helpers for direct model usage.
package modeltelemetry

import (
	"context"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Reporter records chat trace and metrics for one direct model call.
type Reporter struct {
	ctx          context.Context
	invocation   *agent.Invocation
	request      *model.Request
	span         oteltrace.Span
	startedSpan  bool
	tracker      *itelemetry.ChatMetricsTracker
	recordMetric func()
	ended        bool
	err          error
}

// StartChat starts opt-in chat telemetry for direct model usage.
func StartChat(
	ctx context.Context,
	llm model.Model,
	request *model.Request,
	enabled bool,
) *Reporter {
	if !enabled {
		return &Reporter{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	invocation := invocationView(ctx, llm)
	modelName := ""
	if llm != nil {
		modelName = llm.Info().Name
	}
	ctx, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewChatSpanName(modelName))
	reporter := &Reporter{
		ctx:         ctx,
		invocation:  invocation,
		request:     request,
		span:        span,
		startedSpan: startedSpan,
	}
	reporter.tracker = itelemetry.NewChatMetricsTracker(
		ctx,
		invocation,
		request,
		nil,
		nil,
		&reporter.err,
	)
	reporter.recordMetric = reporter.tracker.RecordMetrics()
	return reporter
}

func invocationView(ctx context.Context, llm model.Model) *agent.Invocation {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return &agent.Invocation{Model: llm}
	}
	modelValue := inv.Model
	if modelValue == nil {
		modelValue = llm
	}
	return &agent.Invocation{
		AgentName:    inv.AgentName,
		InvocationID: inv.InvocationID,
		Session:      inv.Session,
		Model:        modelValue,
		RunOptions:   inv.RunOptions,
	}
}

// TrackResponse records telemetry state for one model response.
func (r *Reporter) TrackResponse(response *model.Response) {
	if r == nil || response == nil {
		return
	}
	if r.tracker != nil {
		r.tracker.TrackResponse(response)
		r.tracker.SetLastEvent(&event.Event{Response: response})
	}
	if r.startedSpan {
		var ttfb time.Duration
		if r.tracker != nil {
			ttfb = r.tracker.FirstTokenTimeDuration()
		}
		itelemetry.TraceChat(r.span, &itelemetry.TraceChatAttributes{
			Invocation:       r.invocation,
			Request:          r.request,
			Response:         response,
			TimeToFirstToken: ttfb,
		})
	}
}

// End finishes chat metrics and trace span recording.
func (r *Reporter) End() {
	if r == nil || r.ended {
		return
	}
	r.ended = true
	if r.err == nil && r.ctx != nil {
		r.err = r.ctx.Err()
	}
	if r.recordMetric != nil {
		r.recordMetric()
	}
	if r.startedSpan {
		r.span.End()
	}
}
