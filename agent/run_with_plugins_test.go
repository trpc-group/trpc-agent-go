//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testAgent struct {
	mu     sync.Mutex
	called bool
}

func (a *testAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.called = true
	a.mu.Unlock()

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("orig"),
			}},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *testAgent) Tools() []tool.Tool { return nil }

func (a *testAgent) Info() agent.Info {
	return agent.Info{Name: "a", Description: "test"}
}

func (a *testAgent) SubAgents() []agent.Agent        { return nil }
func (a *testAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *testAgent) wasCalled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.called
}

type cbPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *cbPlugin) Name() string { return p.name }

func (p *cbPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

func TestRunWithPlugins_BeforeAgentCanShortCircuit(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"early",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.False(t, ag.wasCalled())
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Response)
	require.Equal(
		t,
		"early",
		events[0].Response.Choices[0].Message.Content,
	)
}

func TestRunWithPlugins_AfterAgentCanAppendEvent(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return &agent.AfterAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"after",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.True(t, ag.wasCalled())
	require.Len(t, events, 2)
	require.Equal(
		t,
		"orig",
		events[0].Response.Choices[0].Message.Content,
	)
	require.Equal(
		t,
		"after",
		events[1].Response.Choices[0].Message.Content,
	)
}

type ctxValueAgent struct {
	wantKey any
	wantVal any
}

func (a *ctxValueAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	got := ctx.Value(a.wantKey)
	if got != a.wantVal {
		return nil, errors.New("context value missing")
	}
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{Done: true}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *ctxValueAgent) Tools() []tool.Tool { return nil }

func (a *ctxValueAgent) Info() agent.Info {
	return agent.Info{Name: "ctx-agent", Description: "test"}
}

func (a *ctxValueAgent) SubAgents() []agent.Agent        { return nil }
func (a *ctxValueAgent) FindSubAgent(string) agent.Agent { return nil }

type testCtxKey struct{}

func TestRunWithPlugins_NilAgent_ReturnsError(t *testing.T) {
	_, err := agent.RunWithPlugins(context.Background(), nil, nil)
	require.Error(t, err)
}

func TestRunWithPlugins_NoPlugins_RunsAgentDirectly(t *testing.T) {
	ag := &testAgent{}
	inv := agent.NewInvocation(agent.WithInvocationAgent(ag))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
	require.True(t, ag.wasCalled())
}

func TestRunWithPlugins_BeforeAgentContextPropagates(t *testing.T) {
	const pluginName = "p"
	ag := &ctxValueAgent{wantKey: testCtxKey{}, wantVal: "v"}

	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					Context: context.WithValue(ctx, testCtxKey{}, "v"),
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
}

func TestRunWithPlugins_AfterAgentError_EmitsErrorEvent(t *testing.T) {
	const pluginName = "p"
	ag := &testAgent{}
	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return nil, errors.New("boom")
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 2)
	require.NotNil(t, events[1].Error)
	require.Equal(
		t,
		agent.ErrorTypeAgentCallbackError,
		events[1].Error.Type,
	)
}

type errorResponseAgent struct{}

func (a *errorResponseAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Done: true,
			Error: &model.ResponseError{
				Type:    "test",
				Message: "boom",
			},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *errorResponseAgent) Tools() []tool.Tool { return nil }

func (a *errorResponseAgent) Info() agent.Info {
	return agent.Info{Name: "err-agent", Description: "test"}
}

func (a *errorResponseAgent) SubAgents() []agent.Agent        { return nil }
func (a *errorResponseAgent) FindSubAgent(string) agent.Agent { return nil }

func TestRunWithPlugins_AfterAgentReceivesResponseError(t *testing.T) {
	const pluginName = "p"
	sawError := ""
	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				if args != nil && args.Error != nil {
					sawError = args.Error.Error()
				}
				return nil, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	ag := &errorResponseAgent{}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
	require.Contains(t, sawError, "test")
	require.Contains(t, sawError, "boom")
}

// parkedStreamAgent produces events while ignoring cancellation and parks on a
// test-owned gate. It records producer-done independently of any downstream
// consumer, so a test can observe whether a wrapper that forwards its stream
// closes its own output before this producer has actually exited.
type parkedStreamAgent struct {
	started      chan struct{}
	proceed      chan struct{} // closed by the test after cancelling, releasing the second send
	hold         chan struct{} // producer parks here (ignoring ctx) until released
	producerDone atomic.Bool
	releaseOnce  sync.Once
}

func (a *parkedStreamAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event) // unbuffered: becomes the wrapper's src
	rsp := &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage("orig")}},
	}
	evt := event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp)
	go func() {
		defer close(out)
		defer a.producerDone.Store(true)
		out <- evt // first event: forwarded and read by the test
		close(a.started)
		<-a.proceed // wait until the test has cancelled the context
		out <- evt  // second event: forwarded on a cancelled context -> emit fails
		<-a.hold    // park, keeping the producer (and src) alive
	}()
	return out, nil
}

func (a *parkedStreamAgent) Tools() []tool.Tool { return nil }
func (a *parkedStreamAgent) Info() agent.Info {
	return agent.Info{Name: "parked-stream", Description: "test"}
}
func (a *parkedStreamAgent) SubAgents() []agent.Agent        { return nil }
func (a *parkedStreamAgent) FindSubAgent(string) agent.Agent { return nil }
func (a *parkedStreamAgent) release() {
	a.releaseOnce.Do(func() { close(a.hold) })
}

// TestRunWithPlugins_WrapperCloseImpliesProducerDone pins the same producer-done
// contract the runner relies on, one layer down: when wrapAfterAgentCallbacks
// stops forwarding on a cancelled emit, it must drain the wrapped agent's stream
// before closing its own output. Closing output early lets the runner treat the
// stream as producer-done and free resources while the wrapped agent is still
// alive (and blocked sending into the abandoned stream).
func TestRunWithPlugins_WrapperCloseImpliesProducerDone(t *testing.T) {
	ag := &parkedStreamAgent{
		started: make(chan struct{}),
		proceed: make(chan struct{}),
		hold:    make(chan struct{}),
	}
	// An AfterAgent callback forces RunWithPlugins through wrapAfterAgentCallbacks.
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				context.Context,
				*agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return nil, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx, cancel := context.WithCancel(
		agent.NewInvocationContext(context.Background(), inv),
	)
	defer cancel()

	out, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	<-out // read the first forwarded event
	<-ag.started

	// Cancel, then release the producer's second send so the wrapper's emit for
	// it fails on a cancelled context.
	cancel()
	close(ag.proceed)

	// While the producer is parked, the wrapper stream must NOT close. Without
	// the drain the failed emit makes the wrapper close(out) immediately.
	select {
	case _, ok := <-out:
		ag.release()
		require.True(
			t,
			ok,
			"wrapper closed its stream while the producer goroutine was still running",
		)
	case <-time.After(300 * time.Millisecond):
	}

	// Once the producer exits, the stream must close with the producer done.
	ag.release()
	for range out {
	}
	require.True(
		t,
		ag.producerDone.Load(),
		"producer-done must hold at the moment the wrapper stream closes",
	)
}
