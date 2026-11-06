//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type enqueueRequestProcessor struct{ queue *steer.Queue }

func (p enqueueRequestProcessor) ProcessRequest(_ context.Context, _ *agent.Invocation, req *model.Request, _ chan<- *event.Event) {
	req.Messages = []model.Message{model.NewUserMessage("Q1")}
	p.queue.Enqueue(model.NewUserMessage("Q2"))
}
func TestInterruptInputDuringPreprocess(t *testing.T) {
	inv := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(inv, q)
	steer.EnableInterrupt(inv)
	f := New([]flow.RequestProcessor{enqueueRequestProcessor{q}}, nil, Options{})
	// A nil model would fail if inference were reached with the stale request.
	evt, err := f.runOneStep(context.Background(), inv, make(chan *event.Event, 4))
	require.Nil(t, evt)
	require.ErrorIs(t, err, errQueuedInput)
	require.Len(t, q.Drain(), 1)
}
func TestInterruptContextsPreserveRunCauseAndCallbackValues(t *testing.T) {
	type key struct{}
	run, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)
	modelCtx, cancelModel := context.WithCancel(run)
	values := context.WithValue(modelCtx, key{}, "callback")
	tools := contextWithRunLifetime(run, values)
	cancelModel()
	require.NoError(t, tools.Err())
	require.Nil(t, context.Cause(tools))
	require.Equal(t, "callback", tools.Value(key{}))
	cause := errors.New("caller stopped")
	cancelRun(cause)
	require.ErrorIs(t, tools.Err(), context.Canceled)
	require.ErrorIs(t, context.Cause(tools), cause)
}

func TestInterruptCycleNotInheritedByChildFlow(t *testing.T) {
	parent := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(parent, q)
	steer.EnableInterrupt(parent)
	parentCtx, cycle, err := beginModelCycle(context.Background(), parent)
	require.NoError(t, err)
	defer cycle.control.End()
	defer cycle.cancel()
	child := agent.NewInvocation()
	child.Model = &mockModel{responses: []*model.Response{{ID: "child", Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}}}
	f := New([]flow.RequestProcessor{&seedMessagesRequestProcessor{messages: []model.Message{model.NewUserMessage("child input")}}}, nil, Options{})
	events, err := f.Run(parentCtx, child)
	require.NoError(t, err)
	for evt := range events {
		if evt.RequiresCompletion {
			child.NotifyCompletion(context.Background(), agent.GetAppendEventNoticeKey(evt.ID))
		}
	}
	require.Empty(t, cycle.responseID)
	require.Empty(t, cycle.cleanup)
	require.True(t, q.Enqueue(model.NewUserMessage("parent input")))
	require.True(t, cycle.control.Interrupted(), "child completion must not accept the parent's response")
}

func TestInterruptDoesNotHideUnrelatedResponseError(t *testing.T) {
	inv := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(inv, q)
	steer.EnableInterrupt(inv)
	_, cycle, err := beginModelCycle(context.Background(), inv)
	require.NoError(t, err)
	defer cycle.cancel()
	defer cycle.control.End()
	require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
	for _, responseError := range []*model.ResponseError{
		{Type: model.ErrorTypeAPIError, Message: "database failed (context canceled)"},
		{Type: model.ErrorTypeCancelled, Message: context.DeadlineExceeded.Error()},
	} {
		var result error
		p := &streamingResponseProcessor{cycle: cycle, err: &result}
		require.True(t, p.stopForInterruptedResponse(&model.Response{Error: responseError}))
		require.ErrorContains(t, result, responseError.Message)
	}
	var result error
	p := &streamingResponseProcessor{cycle: cycle, err: &result}
	require.True(t, p.stopForInterruptedResponse(&model.Response{Error: &model.ResponseError{Type: model.ErrorTypeStreamError, Message: "stream: context canceled"}}))
	require.NoError(t, result)
}

type interruptFlowModel struct {
	calls    atomic.Int32
	generate func(context.Context, int) model.Seq[*model.Response]
}

func (m *interruptFlowModel) Info() model.Info { return model.Info{Name: "interrupt-flow"} }
func (m *interruptFlowModel) sequence(ctx context.Context) model.Seq[*model.Response] {
	return m.generate(ctx, int(m.calls.Add(1)))
}
func (m *interruptFlowModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	seq := m.sequence(ctx)
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		seq(func(resp *model.Response) bool {
			select {
			case out <- resp:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return out, nil
}

type interruptFlowIterModel struct{ *interruptFlowModel }

func (m interruptFlowIterModel) GenerateContentIter(ctx context.Context, _ *model.Request) (model.Seq[*model.Response], error) {
	return m.sequence(ctx), nil
}

func readInterruptFlow(t *testing.T, ctx context.Context, inv *agent.Invocation, events <-chan *event.Event, receipt func(*event.Event) error) []*event.Event {
	t.Helper()
	var result []*event.Event
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return result
			}
			result = append(result, evt)
			require.Equal(t, inv.InvocationID, evt.InvocationID)
			err := receipt(evt)
			if evt.RequiresCompletion {
				steer.Acknowledge(inv, evt.ID, err)
			}
		case <-ctx.Done():
			t.Fatal("flow did not finish", ctx.Err())
		}
	}
}

func TestInterruptFlowResumesAtInputBoundary(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, stage := range []string{"stream", "metadata", "before_model", "completed"} {
			iter, stage := iter, stage
			t.Run(fmt.Sprintf("iter=%t/%s", iter, stage), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				inv := agent.NewInvocation()
				q := steer.NewQueue()
				steer.Attach(inv, q)
				m := &interruptFlowModel{generate: func(ctx context.Context, n int) model.Seq[*model.Response] {
					return func(yield func(*model.Response) bool) {
						if n == 1 && stage != "completed" {
							if stage != "before_model" {
								resp := &model.Response{ID: "old", IsPartial: true, Choices: []model.Choice{{Delta: model.NewAssistantMessage("partial")}}}
								if stage == "metadata" {
									resp = &model.Response{ID: "old", Object: "model.metadata"}
								}
								yield(resp)
							}
							<-ctx.Done()
							yield(&model.Response{ID: "old", IsPartial: true, Choices: []model.Choice{{Delta: model.NewAssistantMessage("late")}}})
							return
						}
						yield(&model.Response{ID: fmt.Sprint(n), Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}})
					}
				}}
				inv.Model = m
				if iter {
					inv.Model = interruptFlowIterModel{m}
				}
				callbacks := model.NewCallbacks()
				callbacks.RegisterBeforeModel(func(context.Context, *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					if stage == "before_model" && m.calls.Load() == 0 {
						require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
						// Even a callback that replaces its context must receive
						// cancellation from the registered model cycle.
						return &model.BeforeModelResult{Context: context.Background()}, nil
					}
					return nil, nil
				})
				f := New([]flow.RequestProcessor{&seedMessagesRequestProcessor{messages: []model.Message{model.NewUserMessage("Q1")}}}, nil, Options{InterruptOnUserMessage: true, ModelCallbacks: callbacks})
				ch, err := f.Run(ctx, inv)
				require.NoError(t, err)
				queued, consumed, interrupted := false, false, 0
				events := readInterruptFlow(t, ctx, inv, ch, func(evt *event.Event) error {
					if stage != "before_model" && !queued && evt.Response != nil && (evt.IsPartial || evt.Object == "model.metadata" || evt.IsFinalResponse()) {
						queued = true
						require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
					}
					if evt.IsUserMessage() {
						require.Equal(t, "Q2", evt.Choices[0].Message.Content)
						require.EqualValues(t, 1, m.calls.Load(), "input must be acknowledged before the second request")
						consumed = true
					}
					if evt.Response != nil && evt.Object == model.ObjectTypeModelInterrupted {
						interrupted++
					}
					if interrupted > 0 && evt.Response != nil && evt.IsPartial {
						t.Error("old generation emitted after interruption")
					}
					return nil
				})
				require.True(t, consumed)
				require.EqualValues(t, 2, m.calls.Load())
				wantMarkers := 1
				if stage == "completed" {
					wantMarkers = 0
				}
				require.Equal(t, wantMarkers, interrupted)
				for _, evt := range events {
					require.Nil(t, evt.Error)
				}
				require.False(t, q.Enqueue(model.NewUserMessage("late")))
			})
		}
	}
}

func TestInterruptFlowReceiptFailureStopsInference(t *testing.T) {
	for _, stage := range []string{"start", "input", "response", "interruption"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			inv := agent.NewInvocation()
			q := steer.NewQueue()
			steer.Attach(inv, q)
			if stage == "input" {
				q.Enqueue(model.NewUserMessage("Q2"))
			}
			m := &interruptFlowModel{generate: func(ctx context.Context, _ int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					if stage == "interruption" {
						yield(&model.Response{ID: "old", IsPartial: true})
						<-ctx.Done()
						return
					}
					yield(&model.Response{ID: "final", Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}})
				}
			}}
			inv.Model = interruptFlowIterModel{m}
			f := New([]flow.RequestProcessor{&seedMessagesRequestProcessor{messages: []model.Message{model.NewUserMessage("Q1")}}}, nil, Options{InterruptOnUserMessage: true})
			ch, err := f.Run(ctx, inv)
			require.NoError(t, err)
			failure := errors.New("required history unavailable")
			failed := false
			events := readInterruptFlow(t, ctx, inv, ch, func(evt *event.Event) error {
				if evt.Response != nil && evt.IsPartial {
					require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
				}
				if !failed && evt.RequiresCompletion && (stage == "start" || stage == "input" && evt.IsUserMessage() || stage == "response" && evt.IsFinalResponse() || stage == "interruption" && evt.Response != nil && evt.Object == model.ObjectTypeModelInterrupted) {
					failed = true
					return failure
				}
				return nil
			})
			require.True(t, failed)
			require.ErrorIs(t, steer.Err(inv), failure)
			wantCalls := 1
			if stage == "start" || stage == "input" {
				wantCalls = 0
			}
			require.EqualValues(t, wantCalls, m.calls.Load())
			found := false
			for _, evt := range events {
				if evt.Error != nil {
					require.Contains(t, evt.Error.Message, failure.Error())
					found = true
				}
			}
			require.True(t, found, "receipt failures must be observable")
		})
	}
}

func TestInterruptRejectsInferenceAfterFailureOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inv := agent.NewInvocation()
			steer.Attach(inv, steer.NewQueue())
			steer.EnableInterrupt(inv)
			var cause error = errors.New("history write failed")
			if cancelled {
				cancel()
				cause = context.Canceled
			} else {
				steer.Fail(inv, cause)
			}
			m := &interruptFlowModel{generate: func(context.Context, int) model.Seq[*model.Response] {
				t.Error("terminal state must not start a model request")
				return nil
			}}
			f := New(nil, nil, Options{})
			_, seq, called, cycle, err := f.callSteeredLLM(ctx, inv, &model.Request{Messages: []model.Message{model.NewUserMessage("Q1")}}, m)
			require.ErrorIs(t, err, cause)
			require.Nil(t, seq)
			require.False(t, called)
			require.Nil(t, cycle)
			require.Zero(t, m.calls.Load())
		})
	}
}

func TestInterruptDoesNotRestartAfterProviderFailure(t *testing.T) {
	inv := agent.NewInvocation()
	q := steer.NewQueue()
	steer.Attach(inv, q)
	steer.EnableInterrupt(inv)
	ctx, cycle, err := beginModelCycle(context.Background(), inv)
	require.NoError(t, err)
	require.True(t, q.Enqueue(model.NewUserMessage("Q2")))
	failure := errors.New("provider unavailable")
	err = failure
	out := make(chan *event.Event, 1)
	finishModelCycle(context.Background(), inv, out, cycle, &err)
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Empty(t, out, "a fatal provider failure must not become an interruption marker")
	require.Len(t, q.Drain(), 1, "terminal failure leaves unconsumed input for caller recovery")
}
