//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type caseStart struct {
	id      string
	traceID string
	release chan struct{}
}

type callbackContextKey struct{}

type gatedRunner struct {
	started chan caseStart
	mu      sync.Mutex
	active  int
	peak    int
}

func (r *gatedRunner) Run(ctx context.Context, _, _ string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	opts := agent.NewRunOptions(runOpts...)
	if opts.RequestID != message.Content || ctx.Value(callbackContextKey{}) != message.Content {
		return nil, errors.New("case callback context or run options were lost")
	}
	attrs := make(map[string]string)
	for _, attr := range opts.SpanAttributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["user.attribute"] != "preserved" || attrs["langfuse.trace.name"] != "parallel-run/"+message.Content || !opts.ExecutionTraceEnabled {
		return nil, errors.New("case trace options were lost or shared")
	}
	for _, callback := range opts.TraceStartedCallbacks {
		callback(oteltrace.SpanContextFromContext(ctx))
	}
	r.mu.Lock()
	r.active++
	r.peak = max(r.peak, r.active)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	start := caseStart{
		id:      message.Content,
		traceID: oteltrace.SpanContextFromContext(ctx).TraceID().String(),
		release: make(chan struct{}),
	}
	r.started <- start
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-start.release:
	}
	ch := make(chan *event.Event, 1)
	ch <- makeFinalEvent(message.Content)
	close(ch)
	return ch, nil
}

func (r *gatedRunner) Close() error { return nil }

// TestRemoteExperimentCaseParallelism uses only existing evaluator settings.
// Each stage is gated to prove its independent concurrency limit and ordering.
func TestRemoteExperimentCaseParallelism(t *testing.T) {
	const count = 4
	for _, tc := range []struct {
		name                  string
		inference, evaluation bool
		limit                 int
	}{
		{"default", false, false, 2},
		{"inference only", true, false, 2},
		{"evaluation only", false, true, 2},
		{"both", true, true, 2},
		{"one", true, true, 1},
		{"above case count", true, true, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &gatedRunner{started: make(chan caseStart, count)}
			evaluated := make(chan caseStart, count)
			var beforeSet, afterSet, beforeCase, afterCase, beforeEvalSet, afterEvalSet, beforeEvalCase, afterEvalCase, traceCallbacks atomic.Int32
			callbacks := service.NewCallbacks().Register("user", &service.Callback{
				BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
					beforeSet.Add(1)
					return nil, nil
				},
				AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
					afterSet.Add(1)
					return nil, nil
				},
				BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
					beforeCase.Add(1)
					args.RunOptions = append(args.RunOptions, agent.WithRequestID(args.EvalCaseID))
					return &service.BeforeInferenceCaseResult{Context: context.WithValue(ctx, callbackContextKey{}, args.EvalCaseID)}, nil
				},
				AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
					afterCase.Add(1)
					return nil, nil
				},
				BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
					beforeEvalSet.Add(1)
					return nil, nil
				},
				AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
					afterEvalSet.Add(1)
					return nil, nil
				},
				BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
					beforeEvalCase.Add(1)
					start := caseStart{id: args.EvalCaseID, release: make(chan struct{})}
					evaluated <- start
					select {
					case <-start.release:
						return nil, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
				AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
					afterEvalCase.Add(1)
					return nil, nil
				},
			})
			evalOptions := []coreevaluation.Option{
				coreevaluation.WithEvalCaseParallelism(tc.limit),
				coreevaluation.WithEvalCaseParallelInferenceEnabled(tc.inference),
				coreevaluation.WithEvalCaseParallelEvaluationEnabled(tc.evaluation),
				coreevaluation.WithRunOptions(agent.WithTraceStartedCallback(func(oteltrace.SpanContext) { traceCallbacks.Add(1) })),
			}
			if tc.name == "evaluation only" {
				// Callbacks configured directly on a custom service must also survive.
				svc, err := local.New(r, service.WithCallbacks(callbacks))
				require.NoError(t, err)
				evalOptions = append(evalOptions, coreevaluation.WithEvaluationService(svc))
			} else {
				evalOptions = append(evalOptions, coreevaluation.WithCallbacks(callbacks))
			}
			evaluator, err := coreevaluation.New("demo-app", r, evalOptions...)
			require.NoError(t, err)
			defer evaluator.Close()
			api := &recordedAPIServer{dataset: &dataset{ID: "dataset-1", Name: "demo-dataset"}}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("item-%d", i)
				api.dataset.Items = append(api.dataset.Items, &DatasetItem{ID: id, DatasetID: "dataset-1", Input: id, ExpectedOutput: id})
			}
			server := httptest.NewServer(api)
			defer server.Close()
			metrics := newTestMetricManager(t)
			require.NoError(t, metrics.Add(ctx, "demo-app", "dataset-1", buildFinalResponseMetric()))
			saved := &recordingResultManager{Manager: newTestEvalResultManager(t)}
			handler, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, saved,
				WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"),
				WithRunOptions(agent.WithSpanAttributes(attribute.String("user.attribute", "preserved"))),
			)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewBufferString(`{"datasetId":"dataset-1","datasetName":"demo-dataset","payload":{"runName":"parallel-run"}}`)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handler.ServeHTTP(recorder, request) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("handler did not stop")
				}
			}()
			inferenceLimit, evaluationLimit := 1, 1
			if tc.inference {
				inferenceLimit = tc.limit
			}
			if tc.evaluation {
				evaluationLimit = tc.limit
			}
			traces := releaseStage(t, r.started, count, inferenceLimit)
			releaseStage(t, evaluated, count, evaluationLimit)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not complete")
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response remoteExperimentResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Len(t, response.Cases, count)
			assert.Equal(t, count, response.TraceCount)
			assert.Equal(t, count+2, response.ScoreCount)
			assert.Equal(t, 1.0, response.AggregateScores["pass_rate"])
			unique := make(map[string]bool)
			for i, summary := range response.Cases {
				id := fmt.Sprintf("item-%d", i)
				assert.Equal(t, id, summary.CaseID)
				assert.Equal(t, traces[id], summary.TraceID)
				unique[summary.TraceID] = true
			}
			assert.Len(t, unique, count)
			api.mu.Lock()
			for _, trace := range api.traceRequests {
				assert.Equal(t, traces[trace.Input.(string)], trace.ID)
				assert.Equal(t, trace.Input, trace.Output)
				assert.Equal(t, "parallel-run/"+trace.Input.(string), trace.Name)
			}
			for _, item := range api.runItemRequest {
				assert.Equal(t, traces[item.DatasetItemID], item.TraceID)
			}
			api.mu.Unlock()
			for _, counter := range []*atomic.Int32{&beforeSet, &afterSet, &beforeEvalSet, &afterEvalSet} {
				assert.Equal(t, int32(1), counter.Load())
			}
			for _, counter := range []*atomic.Int32{&beforeCase, &afterCase, &beforeEvalCase, &afterEvalCase, &traceCallbacks} {
				assert.Equal(t, int32(count), counter.Load())
			}
			assert.Equal(t, 1, saved.calls)
			require.Len(t, saved.result.EvalCaseResults, count)
		})
	}
}

// releaseStage holds each wave until the configured number of cases arrives.
func releaseStage(t *testing.T, started <-chan caseStart, count, limit int) map[string]string {
	t.Helper()
	traces := make(map[string]string)
	for completed := 0; completed < count; {
		wave := min(limit, count-completed)
		starts := make([]caseStart, 0, wave)
		for i := 0; i < wave; i++ {
			select {
			case start := <-started:
				starts = append(starts, start)
				traces[start.id] = start.traceID
			case <-time.After(5 * time.Second):
				t.Fatalf("only %d cases started concurrently; want %d", len(starts), wave)
			}
		}
		select {
		case extra := <-started:
			t.Fatalf("case %s exceeded concurrency limit %d", extra.id, limit)
		case <-time.After(20 * time.Millisecond):
		}
		for i := len(starts) - 1; i >= 0; i-- {
			close(starts[i].release)
		}
		completed += wave
	}
	return traces
}

type recordingResultManager struct {
	evalresult.Manager
	calls  int
	result *evalresult.EvalSetResult
}

func (m *recordingResultManager) Save(ctx context.Context, appName string, result *evalresult.EvalSetResult) (string, error) {
	m.calls++
	m.result = result
	return m.Manager.Save(ctx, appName, result)
}

type parallelEvaluator struct {
	evaluate func(context.Context) (*coreevaluation.EvaluationResult, error)
}

func (e *parallelEvaluator) Evaluate(ctx context.Context, _ string, _ ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
	return e.evaluate(ctx)
}

func (e *parallelEvaluator) Close() error { return nil }

// TestRemoteExperimentBatchFailure preserves the evaluator error and does not
// start additional Evaluate calls or publish an incomplete batch.
func TestRemoteExperimentBatchFailure(t *testing.T) {
	wantErr := errors.New("evaluation failed")
	var calls int
	h := &Handler{agentEvaluator: &parallelEvaluator{evaluate: func(context.Context) (*coreevaluation.EvaluationResult, error) { calls++; return nil, wantErr }}}
	_, err := h.executeRemoteExperiment(context.Background(), &remoteExperimentRequest{DatasetID: "set"}, executionOptions{}, []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two")})
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, calls)
}

func TestRemoteExperimentBatchAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Handler{}).executeRemoteExperiment(ctx, &remoteExperimentRequest{}, executionOptions{}, []*CaseSpec{buildTestCaseSpec("item-1")})
	require.ErrorIs(t, err, context.Canceled)
}

// cancelRunner blocks until canceled so cleanup can be observed through Evaluate.
type cancelRunner struct {
	started chan struct{}
	active  atomic.Int32
}

func (r *cancelRunner) Run(ctx context.Context, _, _ string, _ model.Message, _ ...agent.RunOption) (<-chan *event.Event, error) {
	r.active.Add(1)
	defer r.active.Add(-1)
	r.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r *cancelRunner) Close() error { return nil }

// TestRemoteExperimentBatchCancellation waits for the evaluator to drain its work.
func TestRemoteExperimentBatchCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelRunner{started: make(chan struct{}, 4)}
	evaluator, err := coreevaluation.New("demo-app", r, coreevaluation.WithEvalCaseParallelism(2), coreevaluation.WithEvalCaseParallelInferenceEnabled(true))
	require.NoError(t, err)
	defer evaluator.Close()
	metrics := newTestMetricManager(t)
	require.NoError(t, metrics.Add(ctx, "demo-app", "set", buildFinalResponseMetric()))
	h, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, newTestEvalResultManager(t), WithBaseURL("http://unused.invalid"), WithPublicKey("pk"), WithSecretKey("sk"))
	require.NoError(t, err)
	specs := []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two"), buildTestCaseSpec("three")}
	for _, spec := range specs {
		spec.EvalCase.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "user"}
	}
	require.NoError(t, h.syncEvalSet(ctx, "set", specs))
	done := make(chan error, 1)
	go func() {
		_, err := h.executeRemoteExperiment(ctx, &remoteExperimentRequest{DatasetID: "set"}, executionOptions{}, specs)
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-r.started:
		case err := <-done:
			t.Fatalf("evaluation ended before inference started: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("inference did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation did not stop")
	}
	require.Zero(t, r.active.Load())
}
