//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestRunner_InterruptModelRequestCancellation(t *testing.T) {
	for _, mode := range []struct{ iter, deadline bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		mode := mode
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var cancelRequest context.CancelFunc
			callbacks := model.NewCallbacks()
			callbacks.RegisterBeforeModel(func(ctx context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				var requestCtx context.Context
				if mode.deadline {
					requestCtx, cancelRequest = context.WithTimeout(ctx, 20*time.Millisecond)
				} else {
					requestCtx, cancelRequest = context.WithCancel(ctx)
				}
				t.Cleanup(cancelRequest)
				return &model.BeforeModelResult{Context: requestCtx}, nil
			})
			m := &interruptTestModel{generate: func(ctx context.Context, _ int) model.Seq[*model.Response] {
				return func(func(*model.Response) bool) {
					if !mode.deadline {
						cancelRequest()
					}
					<-ctx.Done()
				}
			}}
			var provider model.Model = m
			if mode.iter {
				provider = interruptIterModel{m}
			}
			r := NewRunner("deadline", llmagent.New("agent", llmagent.WithModel(provider), llmagent.WithModelCallbacks(callbacks), llmagent.WithInterruptOnUserMessage(true)))
			defer r.Close()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"))
			require.NoError(t, err)
			events := interruptRead(t, ctx, ch, nil)
			require.NoError(t, ctx.Err())
			require.Len(t, m.snapshot(), 1)
			require.Zero(t, interruptCount(events, model.ObjectTypeModelInterrupted))
			found := false
			for _, evt := range events {
				if evt.Error != nil {
					want := context.Canceled
					if mode.deadline {
						want = context.DeadlineExceeded
					}
					require.Contains(t, evt.Error.Message, want.Error())
					found = true
				}
			}
			require.True(t, found, "request cancellation must not become successful completion")
		})
	}
}

func TestRunner_InterruptDeadlineAppendDedup(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(fmt.Sprint(inFlight), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			service := &blockingPersistenceSessionService{
				mockSessionService: &mockSessionService{},
				appendStarted:      make(chan struct{}), appendRelease: make(chan struct{}),
			}
			r := &runner{sessionService: service}
			sess, inv, evt := newPersistenceDedupTestInput()
			evt.Object = model.ObjectTypeToolResponse
			evt.Choices[0].Message = model.Message{Role: model.RoleTool, ToolID: "call", Content: "result"}
			steer.Attach(inv, steer.NewQueue())
			steer.EnableInterrupt(inv)
			r.attachSessionAppender(inv, sess)
			normal := make(chan bool, 1)
			go func() { normal <- r.handleEventPersistence(ctx, inv, sess, sess, evt) }()
			select {
			case <-service.appendStarted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !inFlight {
				close(service.appendRelease)
				require.True(t, receivePersistenceResult(t, normal))
			}
			// The deadline fallback uses the invocation appender, bypassing
			// the event loop's own deduper, possibly before acknowledgement.
			fallback := make(chan error, 1)
			fallbackEvent := evt.Clone()
			fallbackEvent.ID = evt.ID
			go func() { _, err := appender.Invoke(ctx, inv, fallbackEvent); fallback <- err }()
			if inFlight {
				close(service.appendRelease)
				require.True(t, receivePersistenceResult(t, normal))
			}
			select {
			case err := <-fallback:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.EqualValues(t, 1, service.appendCalls.Load())
		})
	}
}

type deadlineToolResultSession struct {
	session.Service
	results  atomic.Int32
	appended chan struct{}
}

func (s *deadlineToolResultSession) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option) error {
	if err := s.Service.AppendEvent(ctx, sess, evt, opts...); err != nil {
		return err
	}
	if evt.IsToolResultResponse() && s.results.Add(1) == 1 {
		close(s.appended)
		// Hold the receipt until the Run deadline. The durable write has
		// succeeded, but the fallback can race its return to Runner.
		<-ctx.Done()
	}
	return nil
}

func TestRunner_InterruptToolResultDeadlineExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runCtx, expire := context.WithTimeout(ctx, 100*time.Millisecond)
	defer expire()
	service := &deadlineToolResultSession{Service: sessioninmemory.NewSessionService(), appended: make(chan struct{})}
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "result", nil }, function.WithName("work"))
	m := &interruptTestModel{generate: func(context.Context, int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			resp := interruptFinal("batch", "")
			resp.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "A", Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(`{}`)}}}
			yield(resp)
		}
	}}
	r := NewRunner("deadline-result", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithInterruptOnUserMessage(true)), WithSessionService(service))
	defer r.Close()
	ch, err := r.Run(runCtx, "user", "session", model.NewUserMessage("Q1"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, nil)
	select {
	case <-service.appended:
	default:
		t.Fatal("tool result persistence was not reached")
	}
	require.EqualValues(t, 1, service.results.Load())
	require.Len(t, m.snapshot(), 1)
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
	sess, err := service.GetSession(ctx, session.Key{AppName: "deadline-result", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	results := 0
	for _, evt := range sess.Events {
		if evt.IsToolResultResponse() {
			results++
		}
	}
	require.Equal(t, 1, results)
}

func TestRunner_InterruptAfterModelCancellation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			calls := 0
			tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
				calls++
				return "unexpected", nil
			}, function.WithName("work"))
			callbacks := model.NewCallbacks()
			callbacks.RegisterAfterModel(func(ctx context.Context, _ *model.AfterModelArgs) (*model.AfterModelResult, error) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return &model.AfterModelResult{Context: ctx}, nil
			})
			m := &interruptTestModel{generate: func(context.Context, int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					resp := interruptFinal("batch", "")
					resp.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "A", Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(`{}`)}}}
					yield(resp)
				}
			}}
			r := NewRunner("callback-cancel", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithModelCallbacks(callbacks), llmagent.WithInterruptOnUserMessage(enabled)))
			defer r.Close()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"))
			require.NoError(t, err)
			events := interruptRead(t, ctx, ch, nil)
			require.Zero(t, calls)
			require.Len(t, m.snapshot(), 1)
			require.Zero(t, interruptCount(events, model.ObjectTypeModelInterrupted))
			if enabled {
				for _, evt := range events {
					require.False(t, evt.IsToolCallResponse(), "a cancelled callback must not publish tool calls")
				}
			}
		})
	}
}

func TestRunner_InterruptMessagesStreamOrdering(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &interruptTestModel{generate: func(ctx context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			if n == 1 {
				resp := interruptPartial("old", "text")
				resp.Object = model.ObjectTypeChatCompletionChunk
				yield(resp)
				<-ctx.Done()
				return
			}
			resp := interruptFinal("new", "done")
			resp.Object = model.ObjectTypeChatCompletion
			yield(resp)
		}
	}}
	r := NewRunner("filter", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"), agent.WithStreamMode(agent.StreamModeMessages))
	require.NoError(t, err)
	var objects []string
	interruptRead(t, ctx, ch, func(evt *event.Event) {
		if evt.Response == nil {
			return
		}
		objects = append(objects, evt.Object)
		if evt.IsPartial {
			require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
		}
	})
	require.Equal(t, []string{model.ObjectTypeChatCompletionChunk, model.ObjectTypeModelInterrupted, model.ObjectTypeChatCompletion, model.ObjectTypeRunnerCompletion}, objects)
}

func TestRunner_InterruptPluginFailureStopsStalledModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	m := &interruptTestModel{generate: func(ctx context.Context, _ int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			defer close(stopped)
			yield(interruptPartial("old", "text"))
			<-ctx.Done()
		}
	}}
	p := &testPlugin{name: "reject-partial", reg: func(reg *plugin.Registry) {
		reg.OnEvent(func(_ context.Context, _ *agent.Invocation, evt *event.Event) (*event.Event, error) {
			if evt.IsPartial {
				return nil, errors.New("partial plugin failure")
			}
			return evt, nil
		})
	}}
	r := NewRunner("plugin-failure", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithInterruptOnUserMessage(true)), WithPlugins(p))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, nil)
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("provider was not cancelled")
	}
	require.NoError(t, ctx.Err())
	require.Len(t, m.snapshot(), 1)
	require.Zero(t, interruptCount(events, model.ObjectTypeModelInterrupted))
	found := false
	for _, evt := range events {
		if evt.Error != nil {
			require.Contains(t, evt.Error.Message, "partial plugin failure")
			found = true
		}
	}
	require.True(t, found)
}

func TestRunner_InterruptMetadataReceipt(t *testing.T) {
	for _, iter := range []bool{false, true} {
		t.Run(fmt.Sprintf("iter=%t", iter), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			modelStopped := make(chan struct{})
			m := &interruptTestModel{generate: func(modelCtx context.Context, n int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					if n == 1 {
						defer close(modelStopped)
						yield(&model.Response{ID: "old", Object: "model.metadata"})
						<-modelCtx.Done()
						return
					}
					yield(interruptFinal("new", "done"))
				}
			}}
			var provider model.Model = m
			if iter {
				provider = interruptIterModel{m}
			}
			var r Runner
			p := &testPlugin{name: "enqueue-on-metadata", reg: func(reg *plugin.Registry) {
				reg.OnEvent(func(_ context.Context, _ *agent.Invocation, evt *event.Event) (*event.Event, error) {
					if evt.Response != nil && evt.Object == "model.metadata" {
						require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
						// Keep the receipt pending while cancellation is handled.
						// An iterator must not exit its yield before this receipt.
						if iter {
							select {
							case <-modelStopped:
								t.Error("iterator left the required receipt wait on user interruption")
							case <-time.After(20 * time.Millisecond):
							}
						} else {
							<-modelStopped
						}
					}
					return evt, nil
				})
			}}
			r = NewRunner("metadata", llmagent.New("agent", llmagent.WithModel(provider), llmagent.WithInterruptOnUserMessage(true)), WithPlugins(p))
			defer r.Close()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
			require.NoError(t, err)
			events := interruptRead(t, ctx, ch, nil)
			require.Len(t, m.snapshot(), 2)
			require.Equal(t, []string{"Q1", "Q2"}, interruptUsers(m.snapshot()[1]))
			require.Equal(t, 1, interruptCount(events, model.ObjectTypeModelInterrupted))
			require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
			for _, evt := range events {
				require.Nil(t, evt.Error)
				require.Equal(t, "req", evt.RequestID)
			}
		})
	}
}

// Closing done simulates a deadline without a racing receipt timer.
type interruptDeadlineContext struct {
	context.Context
	done chan struct{}
}

func (c interruptDeadlineContext) Done() <-chan struct{} { return c.done }
func (c interruptDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

type replacedDeadlineSession struct {
	session.Service
	results  atomic.Int32
	appended chan struct{}
	release  chan struct{}
}

func (s *replacedDeadlineSession) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option) error {
	if err := s.Service.AppendEvent(ctx, sess, evt, opts...); err != nil {
		return err
	}
	if evt.IsToolResultResponse() && s.results.Add(1) == 1 {
		close(s.appended)
		select {
		case <-s.release:
		case <-time.After(3 * time.Second):
			return errors.New("fallback was not reached")
		}
	}
	return nil
}

func TestRunner_InterruptDeadlineReplacementIdentity(t *testing.T) {
	for _, clone := range []bool{false, true} {
		for _, afterCleanup := range []bool{false, true} {
			t.Run(fmt.Sprintf("clone=%t/afterCleanup=%t", clone, afterCleanup), func(t *testing.T) {
				testInterruptDeadlineReplacementIdentity(t, clone, afterCleanup)
			})
		}
	}
}

func testInterruptDeadlineReplacementIdentity(t *testing.T, clone, afterCleanup bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runCtx := interruptDeadlineContext{Context: context.Background(), done: make(chan struct{})}
	service := &replacedDeadlineSession{Service: sessioninmemory.NewSessionService(), appended: make(chan struct{}), release: make(chan struct{})}
	fallbackCtx, releaseFallback := context.WithCancel(ctx)
	defer releaseFallback()
	fallbackInvocation := make(chan *agent.Invocation, 1)
	agentFinished := make(chan struct{})
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(context.Context, *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		close(agentFinished)
		return nil, nil
	})
	var replacements atomic.Int32
	ids := make(chan string, 2)
	p := &testPlugin{name: "replace-tool-result", reg: func(reg *plugin.Registry) {
		reg.OnEvent(func(pluginCtx context.Context, inv *agent.Invocation, evt *event.Event) (*event.Event, error) {
			if evt.IsToolResultResponse() {
				if clone {
					evt = evt.Clone()
				}
				evt.Choices[0].Message.Content = "redacted"
				ids <- evt.ID
				if replacements.Add(1) == 2 {
					fallbackInvocation <- inv
					close(service.release)
					if afterCleanup {
						// Let the normal append and Runner cleanup finish before
						// the detached fallback reaches persistence.
						select {
						case <-fallbackCtx.Done():
						case <-pluginCtx.Done():
							return nil, pluginCtx.Err()
						}
					}
				}
			}
			return evt, nil
		})
	}}
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "result", nil }, function.WithName("work"))
	m := &interruptTestModel{generate: func(context.Context, int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			resp := interruptFinal("batch", "")
			resp.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "A", Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(`{}`)}}}
			yield(resp)
		}
	}}
	r := NewRunner("replacement", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithInterruptOnUserMessage(true), llmagent.WithAgentCallbacks(callbacks)), WithSessionService(service), WithPlugins(p))
	defer r.Close()
	ch, err := r.Run(runCtx, "user", "session", model.NewUserMessage("Q1"))
	require.NoError(t, err)
	select {
	case <-service.appended:
	case <-ctx.Done():
		t.Fatal("normal append was not reached")
	}
	close(runCtx.done)
	events := interruptRead(t, ctx, ch, nil)
	if afterCleanup {
		select {
		case inv := <-fallbackInvocation:
			require.False(t, appender.IsAttached(inv), "Runner must clear the appender before fallback resumes")
			require.False(t, steer.IsAttached(inv), "Runner must clear the queue before fallback resumes")
		case <-ctx.Done():
			t.Fatal("fallback was not reached")
		}
		releaseFallback()
	}
	select {
	case <-agentFinished:
	case <-ctx.Done():
		t.Fatal("detached fallback did not finish")
	}
	require.EqualValues(t, 2, replacements.Load(), "normal and deadline paths must both run the plugin")
	require.EqualValues(t, 1, service.results.Load())
	firstID, secondID := <-ids, <-ids
	if clone {
		require.NotEqual(t, firstID, secondID)
	} else {
		require.Equal(t, firstID, secondID)
	}
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
	sess, err := service.GetSession(ctx, session.Key{AppName: "replacement", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	results := 0
	for _, evt := range sess.Events {
		if evt.IsToolResultResponse() {
			results++
			require.Equal(t, firstID, evt.ID, "plugin replacement IDs must remain public")
			require.Equal(t, "redacted", evt.Choices[0].Message.Content)
		}
	}
	require.Equal(t, 1, results)
}
