//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestReviewTracerEmitsLowCardinalityLifecycleSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	tracer := New(provider)
	ctx, root := tracer.StartReview(context.Background(), review.ModeFakeModel)
	phaseCtx, phase := tracer.StartPhase(ctx, review.PhaseSandbox)
	tracer.AddOutcome(phase, "timeout", "sandbox_timeout")
	phase.End()
	root.End()
	require.NotNil(t, phaseCtx)

	spans := recorder.Ended()
	require.Len(t, spans, 2)
	require.Equal(t, "review.phase", spans[0].Name())
	require.Equal(t, "review.run", spans[1].Name())
	attributes := spans[0].Attributes()
	require.Len(t, attributes, 3)
	require.Equal(t, "sandbox", attributes[0].Value.AsString())
	require.Equal(t, "timeout", attributes[1].Value.AsString())
	require.Equal(t, "sandbox_timeout", attributes[2].Value.AsString())
}
