//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// sessionTraceRunner deliberately starts distinct traces for different runs.
// The published first-run output must use that run's trace, not the last callback.
type sessionTraceRunner struct {
	mu       sync.Mutex
	sessions map[string]string
}

func (r *sessionTraceRunner) Run(ctx context.Context, _, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	r.mu.Lock()
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{TraceID: oteltrace.TraceID{byte(len(r.sessions) + 1)}, SpanID: oteltrace.SpanID{1}})
	r.sessions[sessionID] = sc.TraceID().String()
	r.mu.Unlock()
	opts := agent.NewRunOptions(runOpts...)
	for _, callback := range opts.TraceStartedCallbacks {
		callback(sc)
	}
	events := make(chan *event.Event, 1)
	events <- makeFinalEvent(message.Content)
	close(events)
	return events, nil
}
func (r *sessionTraceRunner) Close() error { return nil }

// TestBatchRejectsDuplicateCaseIDs prevents ambiguous trace/result associations
// from a custom case builder before evaluation or persistence can start.
func TestBatchRejectsDuplicateCaseIDs(t *testing.T) {
	called := false
	h := &Handler{agentEvaluator: &parallelEvaluator{evaluate: func(context.Context) (*coreevaluation.EvaluationResult, error) {
		called = true
		return nil, nil
	}}}
	response, err := h.executeRemoteExperiment(context.Background(), &remoteExperimentRequest{DatasetID: "set"}, executionOptions{},
		[]*CaseSpec{buildTestCaseSpec("duplicate"), buildTestCaseSpec("duplicate")})
	require.ErrorContains(t, err, "duplicate eval case id duplicate")
	require.Nil(t, response)
	require.False(t, called)
}

// TestBatchRunTraceAssociation covers concurrent runs, ordered publication, and
// callback failures without relying on the evaluator to reuse a parent trace ID.
func TestBatchRunTraceAssociation(t *testing.T) {
	for _, failCallback := range []bool{false, true} {
		name := "parallel runs"
		if failCallback {
			name = "callback error"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r := &sessionTraceRunner{sessions: make(map[string]string)}
			options := []coreevaluation.Option{
				coreevaluation.WithNumRuns(2), coreevaluation.WithNumRunsParallelEnabled(true),
				coreevaluation.WithEvalCaseParallelism(2), coreevaluation.WithEvalCaseParallelInferenceEnabled(true),
			}
			sentinel := errors.New("user callback failed")
			if failCallback {
				options = append(options, coreevaluation.WithCallbacks(service.NewCallbacks().RegisterBeforeInferenceSet("user", func(context.Context, *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
					return nil, sentinel
				})))
			}
			evaluator, err := coreevaluation.New("demo-app", r, options...)
			require.NoError(t, err)
			defer evaluator.Close()
			api := &recordedAPIServer{}
			server := httptest.NewServer(api)
			defer server.Close()
			metrics := newTestMetricManager(t)
			require.NoError(t, metrics.Add(ctx, "demo-app", "set", buildFinalResponseMetric()))
			saved := &recordingResultManager{Manager: newTestEvalResultManager(t)}
			h, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, saved, WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"))
			require.NoError(t, err)
			specs := []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two")}
			for _, spec := range specs {
				spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "user"}
			}
			require.NoError(t, h.syncEvalSet(ctx, "set", specs))
			response, err := h.executeRemoteExperiment(ctx, &remoteExperimentRequest{DatasetID: "set"}, executionOptions{runName: "run"}, specs)
			if failCallback {
				require.ErrorIs(t, err, sentinel)
				require.Zero(t, saved.calls)
				require.Empty(t, api.traceRequests)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, saved.calls)
			require.Len(t, saved.result.EvalCaseResults, 4)
			require.Len(t, r.sessions, 4)
			firstRun := make(map[string]string)
			for _, result := range saved.result.EvalCaseResults {
				if _, exists := firstRun[result.EvalID]; !exists {
					firstRun[result.EvalID] = r.sessions[result.SessionID]
				}
			}
			for i, result := range response.Cases {
				require.Equal(t, specs[i].EvalCase.EvalID, result.CaseID)
				require.Equal(t, firstRun[result.CaseID], result.TraceID)
			}
		})
	}
}
