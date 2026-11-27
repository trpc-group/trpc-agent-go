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
	sessions map[string]oteltrace.SpanContext
}

// idForSession selects the published run, falling back to the injected parent
// when a runner does not emit a trace-started callback.
func (t *caseTrace) idForSession(sessionID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sc := t.sessions[sessionID]; sc.IsValid() {
		return sc.TraceID().String()
	}
	return t.fallback
}

// record retains the first actual turn's valid span for each inference session.
// Later turns and invalid notifications must not change the published trace.
func (t *caseTrace) record(sessionID string, sc oteltrace.SpanContext) {
	if !sc.IsValid() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.sessions[sessionID].IsValid() {
		t.sessions[sessionID] = sc
	}
}

// spanForSession restores the actual-run trace for scoring, or the injected
// case parent when the actual runner did not emit a trace (including trace mode).
func (t *caseTrace) spanForSession(sessionID string) oteltrace.SpanContext {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sc := t.sessions[sessionID]; sc.IsValid() {
		return sc
	}
	return t.parent
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
		traces[id] = &caseTrace{parent: oteltrace.SpanContextFromContext(traceCtx), fallback: traceID, sessions: make(map[string]oteltrace.SpanContext)}
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
				trace.record(args.SessionID, sc)
			}),
		)
		return &service.BeforeInferenceCaseResult{Context: oteltrace.ContextWithRemoteSpanContext(ctx, trace.parent)}, nil
	})
	callbacks.RegisterBeforeEvaluateCase("langfuse", func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
		trace, ok := traces[args.EvalCaseID]
		if !ok {
			return nil, fmt.Errorf("unknown eval case %s", args.EvalCaseID)
		}
		if args.Request != nil {
			for _, result := range args.Request.InferenceResults {
				if result != nil && result.EvalCaseID == args.EvalCaseID {
					return &service.BeforeEvaluateCaseResult{Context: oteltrace.ContextWithRemoteSpanContext(ctx, trace.spanForSession(result.SessionID))}, nil
				}
			}
		}
		return nil, fmt.Errorf("inference result missing for case %s", args.EvalCaseID)
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
