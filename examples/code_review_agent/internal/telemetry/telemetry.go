//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telemetry provides low-cardinality tracing helpers for review runs.
package telemetry

import (
	"context"
	"regexp"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

var labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ReviewTracer creates spans without source text, model content, terminal
// output, task identifiers, or other high-cardinality data.
type ReviewTracer struct {
	tracer trace.Tracer
}

// New constructs a ReviewTracer from provider. A nil provider uses the global
// OpenTelemetry provider.
func New(provider trace.TracerProvider) *ReviewTracer {
	if provider == nil {
		provider = trace.NewNoopTracerProvider()
	}
	return &ReviewTracer{tracer: provider.Tracer("trpc-agent-go/code-review-agent")}
}

// StartReview starts the root review span with the selected mode.
func (t *ReviewTracer) StartReview(
	ctx context.Context,
	mode review.Mode,
) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	return t.tracer.Start(ctx, "review.run", trace.WithAttributes(
		attribute.String("review.mode", safeLabel(string(mode))),
	))
}

// StartPhase starts one lifecycle phase span.
func (t *ReviewTracer) StartPhase(
	ctx context.Context,
	phase review.Phase,
) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, "review.phase", trace.WithAttributes(
		attribute.String("review.phase", safeLabel(string(phase))),
	))
}

// AddOutcome records bounded classification labels without error text.
func (t *ReviewTracer) AddOutcome(span trace.Span, outcome, errorType string) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String("review.outcome", safeLabel(outcome)),
		attribute.String("review.error_type", safeLabel(errorType)),
	)
}

func safeLabel(value string) string {
	if labelPattern.MatchString(value) {
		return value
	}
	return "other"
}
