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
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// InvokeAgentSpanName returns the canonical span name used for an
// invoke_agent span. Callers pass this name to their tracer when starting
// the span (typically through internal/trace.StartSpan, which honors
// RunOptions.DisableTracing).
func InvokeAgentSpanName(invocation *agent.Invocation) string {
	if invocation == nil || invocation.AgentName == "" {
		return OperationInvokeAgent
	}
	return fmt.Sprintf("%s %s", OperationInvokeAgent, invocation.AgentName)
}

// InvokeAgentOptions carries optional inputs for invoke_agent instrumentation.
//
// All fields are optional. When a field is zero-valued, the recorder applies
// a reasonable default:
//   - Description: written as-is to gen_ai.agent.description (may be empty).
//   - Instructions: written as-is to gen_ai.system_instructions (may be empty).
//   - GenConfig: a minimal *model.GenerationConfig{Stream: Stream} is used.
//   - Stream: false, unless overridden or derived from GenConfig.
type InvokeAgentOptions struct {
	// Description is the agent description written to the invoke_agent span.
	Description string
	// Instructions is the system prompt text written to the invoke_agent span.
	Instructions string
	// GenConfig is the generation config used when writing request attributes
	// (temperature, max tokens, etc.) to the invoke_agent span. When nil, a
	// minimal config carrying only Stream is synthesized.
	GenConfig *model.GenerationConfig
	// Stream marks whether this invocation streams responses. It is recorded
	// on metric attributes regardless of whether tracing is enabled.
	Stream bool
}

// InvokeAgentRecorder ties together an invoke_agent span and its metrics
// tracker. It aggregates streaming events into a final response/token-usage
// summary and, on Finish, emits the TraceAfterInvokeAgent span attributes
// and records the associated metrics.
//
// Recorders are intended for single-goroutine use. Concurrent callers must
// serialize Observe invocations themselves.
//
// A nil *InvokeAgentRecorder is safe: all methods become no-ops. This lets
// call sites omit "if r != nil" boilerplate.
type InvokeAgentRecorder struct {
	started           bool
	span              oteltrace.Span
	tracker           *InvokeAgentTracker
	tokenUsage        TokenUsage
	fullRespEvent     *event.Event
	responseErrorType string
	finished          bool
}

// StartInvokeAgent begins invoke_agent telemetry for invocation, given an
// already-started span and the started flag returned by
// internal/trace.StartSpan (or any equivalent helper that honors
// RunOptions.DisableTracing).
//
// When started is false, span should be a no-op span (for example
// noop.Span{}). In that mode the recorder still records metrics through
// its InvokeAgentTracker but skips span writes.
//
// The returned context carries an "invoke_agent active" marker
// (see WithInvokeAgentActive). Callers MUST propagate the returned context
// down the stack so descendant agents/wrappers can detect the active scope
// and skip their own invoke_agent instrumentation to avoid duplicate
// span/metric reports.
//
// The returned recorder is always non-nil.
func StartInvokeAgent(
	ctx context.Context,
	invocation *agent.Invocation,
	span oteltrace.Span,
	started bool,
	opts InvokeAgentOptions,
) (context.Context, *InvokeAgentRecorder) {
	if span == nil {
		span = noop.Span{}
		started = false
	}

	if started {
		genConfig := opts.GenConfig
		if genConfig == nil {
			genConfig = &model.GenerationConfig{Stream: opts.Stream}
		}
		TraceBeforeInvokeAgent(
			span,
			invocation,
			opts.Description,
			opts.Instructions,
			genConfig,
		)
	}

	// NewInvokeAgentTracker dereferences *err at construction time, so we
	// supply a local variable instead of relying on later mutations. Error
	// classification flows through SetResponseErrorType.
	var trackerErr error
	tracker := NewInvokeAgentTracker(ctx, invocation, opts.Stream, &trackerErr)

	ctx = WithInvokeAgentActive(ctx)
	return ctx, &InvokeAgentRecorder{
		started: started,
		span:    span,
		tracker: tracker,
	}
}

// Observe feeds one streaming event into the recorder's aggregation state.
//
// It is safe to call Observe with a nil event or a nil recorder. When the
// event carries a non-partial model.Response, the recorder updates its
// running token-usage totals and retains the event as the candidate final
// response. When the event's Response carries an Error, the recorder
// records a fallback error type used in case no successful terminal
// response arrives later.
func (r *InvokeAgentRecorder) Observe(evt *event.Event) {
	if r == nil || evt == nil {
		return
	}
	resp := evt.Response
	if resp == nil {
		return
	}
	r.tracker.TrackResponse(resp)
	if !resp.IsPartial {
		if usage := resp.Usage; usage != nil {
			r.tokenUsage.PromptTokens += usage.PromptTokens
			r.tokenUsage.CompletionTokens += usage.CompletionTokens
			r.tokenUsage.TotalTokens += usage.TotalTokens
		}
		r.fullRespEvent = evt
	}
	if resp.Error != nil {
		r.responseErrorType = FormatResponseErrorLabel(
			resp.Error,
			model.ErrorTypeRunError,
		)
	}
}

// SetResponseErrorType overrides the metric error.type value reported on
// Finish. Use this when a terminal failure is not represented by any
// observed event (for example, a synchronous Agent.Run() error return).
//
// Calling SetResponseErrorType with an empty string clears any previously
// observed error-type classification.
func (r *InvokeAgentRecorder) SetResponseErrorType(errorType string) {
	if r == nil {
		return
	}
	r.responseErrorType = errorType
}

// Finish finalizes the invoke_agent span (TraceAfterInvokeAgent) and
// records the invoke_agent metrics.
//
// Finish is idempotent: subsequent calls are no-ops. Callers typically
// invoke it from defer:
//
//	ctx, rec := StartInvokeAgent(ctx, inv, span, started, opts)
//	defer rec.Finish()
func (r *InvokeAgentRecorder) Finish() {
	if r == nil || r.finished {
		return
	}
	r.finished = true

	// Prefer the terminal response event's error classification when
	// available. A successful final response wins over any earlier
	// transient error event observed on the stream (matches legacy
	// a2aagent/llmagent semantics).
	if r.fullRespEvent != nil && r.fullRespEvent.Response != nil {
		if respErr := r.fullRespEvent.Response.Error; respErr != nil {
			r.responseErrorType = FormatResponseErrorLabel(
				respErr,
				model.ErrorTypeRunError,
			)
		} else {
			r.responseErrorType = ""
		}
	}

	if r.started {
		if r.fullRespEvent != nil {
			TraceAfterInvokeAgent(
				r.span,
				r.fullRespEvent,
				&r.tokenUsage,
				r.tracker.FirstTokenTimeDuration(),
				model.ErrorTypeRunError,
			)
		} else if r.responseErrorType != "" {
			r.span.SetStatus(codes.Error, r.responseErrorType)
			r.span.SetAttributes(
				attribute.String(semconvtrace.KeyErrorType, r.responseErrorType),
			)
		}
	}

	if r.tracker != nil {
		r.tracker.SetResponseErrorType(r.responseErrorType)
		r.tracker.RecordMetrics()()
	}

	if r.started {
		r.span.End()
	}
}

// Span returns the underlying invoke_agent span. The returned span is a
// no-op when tracing is disabled for this invocation. Intended for advanced
// callers that need to attach additional attributes or events to the span
// (for example, recording a validation failure before Finish runs).
func (r *InvokeAgentRecorder) Span() oteltrace.Span {
	if r == nil {
		return noop.Span{}
	}
	return r.span
}

// TraceStarted reports whether the recorder was given a real (non no-op)
// span. Callers that want to gate expensive attribute computation can use
// this to skip work when tracing is disabled.
func (r *InvokeAgentRecorder) TraceStarted() bool {
	if r == nil {
		return false
	}
	return r.started
}
