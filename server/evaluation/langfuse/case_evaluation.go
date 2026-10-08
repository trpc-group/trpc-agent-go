//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// caseTrace associates run callbacks with their inference session. Multiple runs
// may execute concurrently, and publication selects the session in RunDetails.
type caseTrace struct {
	parent   oteltrace.SpanContext
	fallback string
	mu       sync.Mutex
	sessions map[string]string
}

// idForSession selects the published run, falling back to the injected parent
// when a runner does not emit a trace-started callback.
func (t *caseTrace) idForSession(sessionID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id := t.sessions[sessionID]; id != "" {
		return id
	}
	return t.fallback
}

// evaluateCases submits one batch so the evaluator owns both stage schedulers.
// Per-call additive callbacks preserve callbacks configured by the application
// and keep case metadata out of the shared inference request.
func (h *Handler) evaluateCases(ctx context.Context, datasetID string, specs []*CaseSpec) (
	*coreevaluation.EvaluationResult, map[string]*caseTrace, error,
) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(specs))
	traces := make(map[string]*caseTrace, len(specs))
	byID := make(map[string]*CaseSpec, len(specs))
	for _, spec := range specs {
		id := spec.EvalCase.EvalID
		if _, exists := byID[id]; exists {
			return nil, nil, fmt.Errorf("duplicate eval case id %s", id)
		}
		traceCtx, traceID, err := injectRemoteTraceParent(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("inject remote trace parent for case %s: %w", id, err)
		}
		ids = append(ids, id)
		byID[id] = spec
		traces[id] = &caseTrace{parent: oteltrace.SpanContextFromContext(traceCtx), fallback: traceID, sessions: make(map[string]string)}
	}
	callbacks := service.NewCallbacks().RegisterBeforeInferenceCase("langfuse", func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spec, ok := byID[args.EvalCaseID]
		if !ok {
			return nil, fmt.Errorf("unknown eval case %s", args.EvalCaseID)
		}
		trace := traces[args.EvalCaseID]
		args.RunOptions = append(args.RunOptions,
			agent.WithExecutionTraceEnabled(true),
			func(opts *agent.RunOptions) {
				attrs := append([]attribute.KeyValue(nil), opts.SpanAttributes...)
				opts.SpanAttributes = append(attrs,
					attribute.String("langfuse.trace.name", spec.TraceName),
					attribute.String("langfuse.user.id", spec.UserID),
					attribute.String("langfuse.environment", h.environment))
			},
			agent.WithTraceStartedCallback(func(sc oteltrace.SpanContext) {
				if sc.IsValid() {
					trace.mu.Lock()
					trace.sessions[args.SessionID] = sc.TraceID().String()
					trace.mu.Unlock()
				}
			}),
		)
		return &service.BeforeInferenceCaseResult{Context: oteltrace.ContextWithRemoteSpanContext(ctx, trace.parent)}, nil
	})
	result, err := h.agentEvaluator.Evaluate(ctx, datasetID,
		coreevaluation.WithEvalCaseIDs(ids...),
		coreevaluation.WithRunDetailsEnabled(true),
		coreevaluation.WithEvalSetManager(h.evalSetManager),
		coreevaluation.WithMetricManager(h.metricManager),
		coreevaluation.WithEvalResultManager(h.resultManager),
		coreevaluation.WithRunOptions(h.runOptions...),
		coreevaluation.WithAdditionalCallbacks(callbacks),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("evaluate dataset %s: %w", datasetID, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return result, traces, nil
}
