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
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type caseStart struct {
	id      string
	traceID string
	release chan struct{}
}

type gatedRunner struct {
	started chan caseStart
	mu      sync.Mutex
	active  int
	peak    int
}

func (r *gatedRunner) Run(ctx context.Context, _, _ string, message model.Message, _ ...agent.RunOption) (<-chan *event.Event, error) {
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

func TestRemoteExperimentCaseParallelism(t *testing.T) {
	const count = 4
	for _, configured := range []int{0, 1, 2, 8} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			parallelism := max(1, configured)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &gatedRunner{started: make(chan caseStart, count)}
			evaluator, err := coreevaluation.New("demo-app", r,
				coreevaluation.WithEvalCaseParallelism(parallelism),
				coreevaluation.WithEvalCaseParallelInferenceEnabled(true),
				coreevaluation.WithEvalCaseParallelEvaluationEnabled(true),
			)
			require.NoError(t, err)
			defer evaluator.Close()
			api := &recordedAPIServer{dataset: &dataset{ID: "dataset-1", Name: "demo-dataset"}}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("item-%d", i)
				api.dataset.Items = append(api.dataset.Items, &DatasetItem{
					ID: id, DatasetID: "dataset-1", Input: id, ExpectedOutput: id,
				})
			}
			server := httptest.NewServer(api)
			defer server.Close()
			metrics := newTestMetricManager(t)
			require.NoError(t, metrics.Add(ctx, "demo-app", "dataset-1", buildFinalResponseMetric()))
			opts := []Option{
				WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"),
			}
			if configured > 0 {
				opts = append(opts, WithCaseParallelism(configured))
			}
			handler, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, newTestEvalResultManager(t), opts...)
			require.NoError(t, err)
			body := []byte(`{"datasetId":"dataset-1","datasetName":"demo-dataset","payload":{"runName":"parallel-run"}}`)
			request := httptest.NewRequest(http.MethodPost, handler.Path(), bytes.NewReader(body)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(recorder, request)
			}()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("handler did not stop on cancellation")
				}
			}()
			traces := make(map[string]string)
			for completed := 0; completed < count; {
				wave := min(parallelism, count-completed)
				starts := make([]caseStart, 0, wave)
				for i := 0; i < wave; i++ {
					select {
					case start := <-r.started:
						starts = append(starts, start)
						traces[start.id] = start.traceID
					case <-time.After(5 * time.Second):
						t.Fatalf("only %d cases started concurrently; want %d", len(starts), wave)
					}
				}
				select {
				case extra := <-r.started:
					t.Fatalf("case %s exceeded concurrency limit %d", extra.id, parallelism)
				case <-time.After(20 * time.Millisecond):
				}
				for i := len(starts) - 1; i >= 0; i-- {
					close(starts[i].release)
				}
				completed += wave
			}
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
			uniqueTraces := make(map[string]bool)
			for i, summary := range response.Cases {
				id := fmt.Sprintf("item-%d", i)
				assert.Equal(t, id, summary.CaseID)
				assert.Equal(t, traces[id], summary.TraceID)
				uniqueTraces[summary.TraceID] = true
			}
			assert.Len(t, uniqueTraces, count)
			api.mu.Lock()
			defer api.mu.Unlock()
			for _, trace := range api.traceRequests {
				assert.Equal(t, traces[trace.Input.(string)], trace.ID)
				assert.Equal(t, trace.Input, trace.Output)
				assert.Equal(t, "parallel-run/"+trace.Input.(string), trace.Name)
			}
			for _, item := range api.runItemRequest {
				assert.Equal(t, traces[item.DatasetItemID], item.TraceID)
			}
			r.mu.Lock()
			assert.Equal(t, min(parallelism, count), r.peak)
			r.mu.Unlock()
		})
	}
}

type parallelEvaluator struct {
	evaluate func(context.Context) (*coreevaluation.EvaluationResult, error)
}

func (e *parallelEvaluator) Evaluate(ctx context.Context, _ string, _ ...coreevaluation.Option) (*coreevaluation.EvaluationResult, error) {
	return e.evaluate(ctx)
}

func (e *parallelEvaluator) Close() error { return nil }

func TestRemoteExperimentParallelFailureCancelsAndWaits(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{}, 3)
			fail := make(chan struct{})
			wantErr := errors.New("evaluation failed")
			var calls, active atomic.Int32
			evaluator := &parallelEvaluator{evaluate: func(ctx context.Context) (*coreevaluation.EvaluationResult, error) {
				call := calls.Add(1)
				active.Add(1)
				defer active.Add(-1)
				started <- struct{}{}
				if call == 1 && !cancelRequest {
					select {
					case <-fail:
						return nil, wantErr
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			handler := &Handler{agentEvaluator: evaluator, caseParallelism: 2}
			done := make(chan error, 1)
			go func() {
				_, err := handler.executeRemoteExperiment(ctx, &remoteExperimentRequest{DatasetID: "dataset-1"}, executionOptions{}, []*CaseSpec{
					buildTestCaseSpec("item-1"), buildTestCaseSpec("item-2"), buildTestCaseSpec("item-3"),
				})
				done <- err
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("parallel case did not start")
				}
			}
			if cancelRequest {
				cancel()
				wantErr = context.Canceled
			} else {
				close(fail)
			}
			select {
			case err := <-done:
				assert.ErrorIs(t, err, wantErr)
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not stop after failure")
			}
			assert.Equal(t, int32(2), calls.Load(), "pending case must not start after failure")
			assert.Zero(t, active.Load(), "handler must await in-flight cases")
		})
	}
}

func TestRemoteExperimentParallelAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := &Handler{caseParallelism: 2}
	_, err := handler.executeRemoteExperiment(ctx, &remoteExperimentRequest{}, executionOptions{}, []*CaseSpec{buildTestCaseSpec("item-1")})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewRejectsInvalidCaseParallelism(t *testing.T) {
	for _, parallelism := range []int{0, -1} {
		_, err := New("demo-app", &fakeAgentEvaluator{}, newTestEvalSetManager(t), newTestMetricManager(t), newTestEvalResultManager(t),
			WithBaseURL("http://example.com"), WithPublicKey("pk"), WithSecretKey("sk"), WithCaseParallelism(parallelism),
		)
		require.ErrorContains(t, err, "case parallelism must be positive")
	}
}
