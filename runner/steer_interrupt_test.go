//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type interruptTestModel struct {
	mu       sync.Mutex
	requests [][]model.Message
	generate func(context.Context, int) model.Seq[*model.Response]
}

func (m *interruptTestModel) Info() model.Info { return model.Info{Name: "interrupt-test"} }
func (m *interruptTestModel) sequence(ctx context.Context, req *model.Request) model.Seq[*model.Response] {
	m.mu.Lock()
	m.requests = append(m.requests, append([]model.Message(nil), req.Messages...))
	n := len(m.requests)
	m.mu.Unlock()
	return m.generate(ctx, n)
}
func (m *interruptTestModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	seq := m.sequence(ctx, req)
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		seq(func(r *model.Response) bool {
			select {
			case out <- r:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return out, nil
}
func (m *interruptTestModel) snapshot() [][]model.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]model.Message(nil), m.requests...)
}

type interruptIterModel struct{ *interruptTestModel }

func (m interruptIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return m.sequence(ctx, req), nil
}
func interruptFinal(id, text string) *model.Response {
	return &model.Response{ID: id, Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage(text)}}}
}
func interruptPartial(id, text string) *model.Response {
	return &model.Response{ID: id, IsPartial: true, Choices: []model.Choice{{Delta: model.NewAssistantMessage(text)}}}
}
func interruptRead(t *testing.T, ctx context.Context, ch <-chan *event.Event, hook func(*event.Event)) []*event.Event {
	t.Helper()
	var events []*event.Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, e)
			if hook != nil {
				hook(e)
			}
		case <-ctx.Done():
			t.Fatal("run did not complete", ctx.Err())
			return nil
		}
	}
}
func interruptUsers(messages []model.Message) []string {
	var out []string
	for _, m := range messages {
		if m.Role == model.RoleUser {
			out = append(out, m.Content)
		}
	}
	return out
}
func interruptCount(events []*event.Event, object string) int {
	n := 0
	for _, e := range events {
		if e.Response != nil && e.Object == object {
			n++
		}
	}
	return n
}

func TestRunner_InterruptUserMessage(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			for _, persist := range []bool{false, true} {
				t.Run(fmt.Sprintf("iter=%v/partial=%v/persist=%v", iter, partial, persist), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					started := make(chan struct{})
					stopped := make(chan struct{})
					m := &interruptTestModel{}
					m.generate = func(ctx context.Context, n int) model.Seq[*model.Response] {
						return func(yield func(*model.Response) bool) {
							if n == 1 {
								if partial && !yield(interruptPartial("old", "unfinished")) {
									close(stopped)
									return
								}
								close(started)
								<-ctx.Done()
								close(stopped)
								// A provider can race cancellation with a final buffered response.
								yield(interruptFinal("old", "stale"))
								return
							}
							yield(interruptFinal("new", "answer"))
						}
					}
					var provider model.Model = m
					if iter {
						provider = interruptIterModel{m}
					}
					r := NewRunner("interrupt", llmagent.New("agent", llmagent.WithModel(provider), llmagent.WithInterruptOnUserMessage(true)), WithPersistInterruptedAssistant(persist))
					defer r.Close()
					ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
					require.NoError(t, err)
					if !partial {
						select {
						case <-started:
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
						require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
					}
					sent := false
					events := interruptRead(t, ctx, ch, func(e *event.Event) {
						if partial && !sent && e.Response != nil && e.IsPartial {
							sent = true
							require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
						}
					})
					select {
					case <-stopped:
					case <-ctx.Done():
						t.Fatal("provider not stopped")
					}
					require.Len(t, m.snapshot(), 2)
					require.Equal(t, []string{"Q1", "Q2"}, interruptUsers(m.snapshot()[1]))
					require.Equal(t, 1, interruptCount(events, model.ObjectTypeModelInterrupted))
					require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
					var invocationID string
					seenMarker := false
					for _, e := range events {
						require.Nil(t, e.Error)
						require.Equal(t, "req", e.RequestID)
						if invocationID == "" {
							invocationID = e.InvocationID
						}
						require.Equal(t, invocationID, e.InvocationID)
						if e.Object == model.ObjectTypeModelInterrupted {
							seenMarker = true
							require.False(t, e.Done)
							require.Empty(t, e.Choices)
							if partial {
								require.Equal(t, "old", e.Response.ID)
							}
							continue
						}
						if seenMarker && e.Response != nil {
							require.NotEqual(t, "old", e.Response.ID)
						}
					}
					found := false
					for _, msg := range m.snapshot()[1] {
						if msg.Role == model.RoleAssistant && strings.Contains(msg.Content, "unfinished") {
							found = true
						}
					}
					require.Equal(t, partial && persist, found)
					require.ErrorIs(t, EnqueueUserMessage(r, "req", model.NewUserMessage("late")), ErrRunNotFound)
				})
			}
		}
	}
}

func TestRunner_InterruptUserMessageToolBatch(t *testing.T) {
	for _, mode := range []struct{ parallel, perResult bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		parallel := mode.parallel
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var r Runner
			var mu sync.Mutex
			counts := map[string]int{}
			var toolErr error
			type args struct {
				Name string `json:"name"`
			}
			tl := function.NewFunctionTool(func(ctx context.Context, in args) (string, error) {
				mu.Lock()
				counts[in.Name]++
				mu.Unlock()
				if in.Name == "A" {
					err := EnqueueUserMessage(r, "req", model.NewUserMessage("discard"))
					if err == nil && !CancelQueuedUserMessages(r, "req") {
						err = errors.New("discard failed")
					}
					for _, text := range []string{"Q2", "Q3", "Q4"} {
						if e := EnqueueUserMessage(r, "req", model.NewUserMessage(text)); e != nil {
							err = e
						}
					}
					if ctx.Err() != nil {
						err = ctx.Err()
					}
					mu.Lock()
					toolErr = err
					mu.Unlock()
				}
				return in.Name, nil
			}, function.WithName("work"))
			m := &interruptTestModel{}
			m.generate = func(_ context.Context, n int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					if n == 1 {
						resp := interruptFinal("batch", "")
						for _, name := range []string{"A", "B", "C"} {
							resp.Choices[0].Message.ToolCalls = append(resp.Choices[0].Message.ToolCalls, model.ToolCall{ID: name, Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(fmt.Sprintf(`{"name":%q}`, name))}})
						}
						yield(resp)
						return
					}
					yield(interruptFinal("answer", "done"))
				}
			}
			r = NewRunner("batch", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithInterruptOnUserMessage(true), llmagent.WithEnableParallelTools(parallel)))
			defer r.Close()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"), agent.WithToolResultEventPerCallEnabled(mode.perResult))
			require.NoError(t, err)
			events := interruptRead(t, ctx, ch, nil)
			require.NoError(t, toolErr)
			require.Equal(t, map[string]int{"A": 1, "B": 1, "C": 1}, counts)
			require.Equal(t, 0, interruptCount(events, model.ObjectTypeModelInterrupted))
			require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
			requests := m.snapshot()
			require.Len(t, requests, 2)
			require.Equal(t, []string{"Q1", "Q2", "Q3", "Q4"}, interruptUsers(requests[1]))
			results := 0
			for _, msg := range requests[1] {
				if msg.Role == model.RoleTool {
					results++
				}
				if msg.Role == model.RoleUser && msg.Content == "Q2" {
					require.Equal(t, 3, results)
				}
			}
		})
	}
}

func TestRunner_InterruptUserMessageFinalAdmission(t *testing.T) {
	// The final event is accepted before the provider sequence enqueues Q2.
	// The finish check must observe Q2 even though the model has already ended.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r Runner
	var enqueueErr error
	m := &interruptTestModel{}
	m.generate = func(_ context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			if n == 1 {
				yield(interruptFinal("first", "first"))
				enqueueErr = EnqueueUserMessage(r, "req", model.NewUserMessage("Q2"))
				return
			}
			yield(interruptFinal("last", "last"))
		}
	}
	// Iteration guarantees enqueue happens before the sequence returns.
	r = NewRunner("finish", llmagent.New("agent", llmagent.WithModel(interruptIterModel{m}), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, nil)
	require.NoError(t, enqueueErr)
	require.Len(t, m.snapshot(), 2)
	require.Equal(t, 0, interruptCount(events, model.ObjectTypeModelInterrupted))
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
}

type interruptFailSession struct {
	session.Service
	failure error
}

func (s interruptFailSession) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option) error {
	if e.IsUserMessage() && len(e.Choices) > 0 && e.Choices[0].Message.Content == "Q2" {
		return s.failure
	}
	return s.Service.AppendEvent(ctx, sess, e, opts...)
}
func TestRunner_InterruptUserMessagePersistenceFailures(t *testing.T) {
	for _, failure := range []string{"append", "plugin_error", "plugin_filter"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			m := &interruptTestModel{}
			m.generate = func(ctx context.Context, n int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					if n == 1 {
						yield(interruptPartial("old", "text"))
						<-ctx.Done()
						return
					}
					yield(interruptFinal("new", "must not happen"))
				}
			}
			cause := errors.New("storage unavailable")
			opts := []Option{}
			if failure == "append" {
				opts = append(opts, WithSessionService(interruptFailSession{Service: sessioninmemory.NewSessionService(), failure: cause}))
			} else {
				p := &testPlugin{name: "reject-input", reg: func(reg *plugin.Registry) {
					reg.OnEvent(func(_ context.Context, _ *agent.Invocation, e *event.Event) (*event.Event, error) {
						if e.IsUserMessage() && len(e.Choices) > 0 && e.Choices[0].Message.Content == "Q2" {
							if failure == "plugin_error" {
								return nil, cause
							}
							e.Response.Choices = nil
						}
						return e, nil
					})
				}}
				opts = append(opts, WithPlugins(p))
			}
			r := NewRunner("failure", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithInterruptOnUserMessage(true)), opts...)
			defer r.Close()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
			require.NoError(t, err)
			sent := false
			events := interruptRead(t, ctx, ch, func(e *event.Event) {
				if e.Response != nil && e.IsPartial && !sent {
					sent = true
					require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
				}
			})
			require.Len(t, m.snapshot(), 1)
			observedError := false
			for _, e := range events {
				if e.Error != nil {
					observedError = true
				}
				require.False(t, e.IsUserMessage(), "failed consumption must not be visible")
			}
			require.True(t, observedError)
			require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
		})
	}
}

func TestRunner_InterruptUserMessageTerminalConditions(t *testing.T) {
	for _, terminal := range []string{"cancel", "deadline", "budget", "model_error", "callback_error"} {
		terminal := terminal
		t.Run(terminal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			m := &interruptTestModel{}
			m.generate = func(ctx context.Context, n int) model.Seq[*model.Response] {
				return func(yield func(*model.Response) bool) {
					if n == 1 {
						yield(interruptPartial("old", "text"))
						<-ctx.Done()
						return
					}
					if terminal == "model_error" {
						yield(&model.Response{Done: true, Error: &model.ResponseError{Type: model.ErrorTypeFlowError, Message: "real failure"}})
						return
					}
					<-ctx.Done()
				}
			}
			callbacks := model.NewCallbacks()
			calls := 0
			callbacks.RegisterBeforeModel(func(ctx context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				calls++
				if calls == 2 && terminal == "callback_error" {
					return nil, errors.New("real callback failure")
				}
				return nil, nil
			})
			opts := []llmagent.Option{llmagent.WithModel(m), llmagent.WithInterruptOnUserMessage(true), llmagent.WithModelCallbacks(callbacks)}
			if terminal == "budget" {
				opts = append(opts, llmagent.WithMaxLLMCalls(1))
			}
			r := NewRunner("terminal", llmagent.New("agent", opts...))
			defer r.Close()
			runCtx := ctx
			var expire context.CancelFunc
			if terminal == "deadline" {
				runCtx, expire = context.WithDeadline(ctx, time.Now().Add(200*time.Millisecond))
				defer expire()
			}
			ch, err := r.Run(runCtx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
			require.NoError(t, err)
			sent := false
			events := interruptRead(t, ctx, ch, func(e *event.Event) {
				if e.Response != nil && e.IsPartial && !sent {
					sent = true
					require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
					if terminal == "cancel" {
						require.True(t, r.(ManagedRunner).Cancel("req"))
					}
				}
			})
			require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
			require.LessOrEqual(t, len(m.snapshot()), 2)
			if terminal == "budget" || terminal == "callback_error" {
				require.Len(t, m.snapshot(), 1)
			}
			if terminal == "model_error" || terminal == "callback_error" {
				found := false
				for _, e := range events {
					if e.Error != nil && strings.Contains(e.Error.Message, "real") {
						found = true
					}
				}
				require.True(t, found)
			}
		})
	}
}

func TestRunner_InterruptBeforeToolAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r Runner
	calls := 0
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { calls++; return "unexpected", nil }, function.WithName("work"))
	m := &interruptTestModel{}
	m.generate = func(_ context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			resp := interruptFinal("new", "done")
			if n == 1 {
				resp = interruptFinal("batch", "")
				resp.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "A", Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(`{}`)}}}
			}
			yield(resp)
		}
	}
	callbacks := model.NewCallbacks()
	callbacks.RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if args.Response != nil && args.Response.ID == "batch" {
			return nil, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2"))
		}
		return nil, nil
	})
	r = NewRunner("admission", llmagent.New("agent", llmagent.WithModel(interruptIterModel{m}), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithModelCallbacks(callbacks), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, nil)
	require.Zero(t, calls)
	require.Len(t, m.snapshot(), 2)
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeModelInterrupted))
	for _, msg := range m.snapshot()[1] {
		require.Empty(t, msg.ToolCalls)
		require.NotEqual(t, model.RoleTool, msg.Role)
	}
}

func TestRunner_InterruptPartialAccumulatorIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &interruptTestModel{}
	m.generate = func(ctx context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			if n <= 2 {
				yield(interruptPartial(fmt.Sprint(n), fmt.Sprintf("partial-%d", n)))
				<-ctx.Done()
				return
			}
			yield(interruptFinal("final", "done"))
		}
	}
	r := NewRunner("partials", llmagent.New("agent", llmagent.WithModel(interruptIterModel{m}), llmagent.WithInterruptOnUserMessage(true)), WithPersistInterruptedAssistant(true))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, func(e *event.Event) {
		if e.Response != nil && e.IsPartial {
			require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("next-"+e.Response.ID)))
		}
	})
	require.Equal(t, 2, interruptCount(events, model.ObjectTypeModelInterrupted))
	require.Len(t, m.snapshot(), 3)
	var assistant []string
	for _, msg := range m.snapshot()[2] {
		if msg.Role == model.RoleAssistant {
			assistant = append(assistant, msg.Content)
		}
	}
	require.Equal(t, []string{"partial-1", "partial-2"}, assistant)
}

func TestRunner_InterruptAfterNonterminalMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &interruptTestModel{}
	m.generate = func(ctx context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			if n == 1 {
				yield(&model.Response{ID: "old", Object: "model.metadata"})
				<-ctx.Done()
				return
			}
			yield(interruptFinal("new", "done"))
		}
	}
	r := NewRunner("metadata", llmagent.New("agent", llmagent.WithModel(interruptIterModel{m}), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, func(e *event.Event) {
		if e.Response != nil && e.Object == "model.metadata" {
			require.NoError(t, EnqueueUserMessage(r, "req", model.NewUserMessage("Q2")))
		}
	})
	require.Len(t, m.snapshot(), 2)
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeModelInterrupted))
}

func TestRunner_InterruptManyDeltasPreservesToolContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r Runner
	var toolErr error
	called := false
	tl := function.NewFunctionTool(func(ctx context.Context, _ struct{}) (string, error) {
		// Tool contexts must support absent values after arbitrarily many chunks.
		type missingKey struct{}
		if ctx.Value(missingKey{}) != nil {
			return "", errors.New("unexpected context value")
		}
		called = true
		toolErr = EnqueueUserMessage(r, "req", model.NewUserMessage("Q2"))
		if ctx.Err() != nil {
			toolErr = ctx.Err()
		}
		return "done", nil
	}, function.WithName("work"))
	m := &interruptTestModel{}
	m.generate = func(_ context.Context, n int) model.Seq[*model.Response] {
		return func(yield func(*model.Response) bool) {
			if n == 1 {
				for i := 0; i < 100; i++ {
					if !yield(interruptPartial("old", "x")) {
						return
					}
				}
				resp := interruptFinal("old", "")
				resp.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "A", Type: "function", Function: model.FunctionDefinitionParam{Name: "work", Arguments: []byte(`{}`)}}}
				yield(resp)
				return
			}
			yield(interruptFinal("new", "done"))
		}
	}
	r = NewRunner("many-deltas", llmagent.New("agent", llmagent.WithModel(interruptIterModel{m}), llmagent.WithTools([]tool.Tool{tl}), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("Q1"), agent.WithRequestID("req"))
	require.NoError(t, err)
	events := interruptRead(t, ctx, ch, nil)
	require.True(t, called)
	require.NoError(t, toolErr)
	require.Len(t, m.snapshot(), 2)
	require.Equal(t, 1, interruptCount(events, model.ObjectTypeRunnerCompletion))
}
