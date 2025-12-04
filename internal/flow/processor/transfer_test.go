//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent minimal implementation for transfer tests.
type mockAgent struct {
	name             string
	emit             bool
	gotEndInvocation bool
}

func (m *mockAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockAgent) Tools() []tool.Tool              { return nil }
func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		// Record whether the invocation was incorrectly marked as ended.
		m.gotEndInvocation = inv.EndInvocation
		if m.emit {
			ch <- event.New(inv.InvocationID, m.name)
		}
	}()
	return ch, nil
}

// parentAgent implements FindSubAgent
type parentAgent struct{ child agent.Agent }

func (p *parentAgent) Info() agent.Info         { return agent.Info{Name: "parent"} }
func (p *parentAgent) SubAgents() []agent.Agent { return []agent.Agent{p.child} }
func (p *parentAgent) FindSubAgent(name string) agent.Agent {
	if p.child != nil && p.child.Info().Name == name {
		return p.child
	}
	return nil
}
func (p *parentAgent) Tools() []tool.Tool { return nil }
func (p *parentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func TestTransferResponseProc_Successful(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv"),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{
			TargetAgentName:     "child",
			Message:             "hi",
			ToolResponseEventID: "tool-evt",
		}),
	)
	toolKey := agent.GetAppendEventNoticeKey(inv.TransferInfo.ToolResponseEventID)
	inv.AddNoticeChannel(context.Background(), toolKey)

	rsp := &model.Response{ID: "r1", Created: time.Now().Unix(), Model: "m"}

	out := make(chan *event.Event, 10)
	proc := NewTransferResponseProcessor(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		// Unblock tool.response persistence wait.
		time.Sleep(10 * time.Millisecond)
		_ = inv.NotifyCompletion(context.Background(), toolKey)
	}()
	go func() {
		defer wg.Done()
		proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
		close(out)
	}()

	// Expect transfer event + child event
	evts := []*event.Event{}
	for e := range out {
		if e.RequiresCompletion {
			_ = inv.NotifyCompletion(context.Background(), agent.GetAppendEventNoticeKey(e.ID))
		}
		evts = append(evts, e)
	}
	wg.Wait()
	require.Len(t, evts, 3)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, "child", evts[1].Author)
	// Successful transfer should end the original invocation when configured.
	require.True(t, inv.EndInvocation)
}

func TestTransferResponseProc_Target404(t *testing.T) {
	parent := &parentAgent{child: nil}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv"),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{
			TargetAgentName:     "missing",
			ToolResponseEventID: "tool-evt",
		}),
	)
	toolKey := agent.GetAppendEventNoticeKey(inv.TransferInfo.ToolResponseEventID)
	inv.AddNoticeChannel(context.Background(), toolKey)

	rsp := &model.Response{ID: "r"}
	out := make(chan *event.Event, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
		close(out)
	}()
	time.Sleep(10 * time.Millisecond)
	_ = inv.NotifyCompletion(context.Background(), toolKey)
	evt := <-out
	wg.Wait()
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
	// Even on target missing, the invocation should end to avoid re-entry.
	require.True(t, inv.EndInvocation)
}

func TestTransferResponseProc_ToolResponsePersistenceError(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	// Use zero-value Invocation so waitForEventPersistence fails when creating notice channel.
	inv := &agent.Invocation{
		InvocationID: "inv-tool",
		AgentName:    "parent",
		TransferInfo: &agent.TransferInfo{
			TargetAgentName:     "child",
			ToolResponseEventID: "evt-tool",
		},
	}
	rsp := &model.Response{ID: "r-tool", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 1)

	proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)

	evt := <-out
	require.NotNil(t, evt)
	require.Equal(t, model.ObjectTypeError, evt.Object)
	require.NotNil(t, evt.Error)
	require.Equal(t, "Transfer failed: waiting for tool response persistence timed out", evt.Error.Message)
	// Error during tool.response persistence should end the invocation.
	require.True(t, inv.EndInvocation)
}

func TestTransferResponseProc_TransferEventPersistenceError(t *testing.T) {
	target := &mockAgent{name: "child", emit: false}
	parent := &parentAgent{child: target}

	// Zero-value Invocation with valid Agent; ToolResponseEventID is empty so the
	// first waitForEventPersistence returns nil, while the second will fail when
	// creating a notice channel.
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv-transfer"),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{
			TargetAgentName:     "child",
			ToolResponseEventID: "evt-tool",
		}),
	)
	toolKey := agent.GetAppendEventNoticeKey(inv.TransferInfo.ToolResponseEventID)
	inv.AddNoticeChannel(context.Background(), toolKey)

	rsp := &model.Response{ID: "r-transfer", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 4)

	proc := NewTransferResponseProcessor(true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, out)
		close(out)
	}()
	time.Sleep(10 * time.Millisecond)
	_ = inv.NotifyCompletion(context.Background(), toolKey)
	wg.Wait()

	var sawTransfer bool
	var errEvt *event.Event
	for e := range out {
		if e.Object == model.ObjectTypeTransfer {
			sawTransfer = true
		}
		if e.Error != nil && e.Error.Type == model.ErrorTypeFlowError {
			errEvt = e
		}
	}

	require.True(t, sawTransfer, "expected transfer event before error")
	if errEvt != nil {
		require.Equal(t, "Transfer failed: waiting for transfer event persistence timed out", errEvt.Error.Message)
	}
	require.True(t, sawTransfer, "expected transfer event before error")
	require.True(t, inv.EndInvocation)
}

func TestTransferResponseProc_TransferEchoPersistenceError(t *testing.T) {
	target := &mockAgent{name: "child", emit: false}
	parent := &parentAgent{child: target}

	// ToolResponseEventID empty so the first waitForEventPersistence is a no-op.
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv-echo"),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{
			TargetAgentName:     "child",
			Message:             "hi",
			ToolResponseEventID: "tool-evt",
		}),
	)
	toolKey := agent.GetAppendEventNoticeKey(inv.TransferInfo.ToolResponseEventID)
	inv.AddNoticeChannel(context.Background(), toolKey)

	rsp := &model.Response{ID: "r-echo", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	proc := NewTransferResponseProcessor(true)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		proc.ProcessResponse(ctx, inv, &model.Request{}, rsp, out)
		close(out)
	}()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = inv.NotifyCompletion(context.Background(), toolKey)
	}()

	var sawTransfer bool
	var errEvt *event.Event

	for e := range out {
		// The first transfer event (ObjectTypeTransfer, no error) should succeed.
		if !sawTransfer && e.Object == model.ObjectTypeTransfer && e.Error == nil {
			sawTransfer = true
			key := agent.GetAppendEventNoticeKey(e.ID)
			// Give ProcessResponse time to enter waitForEventPersistence before notifying.
			time.Sleep(20 * time.Millisecond)
			_ = inv.NotifyCompletion(context.Background(), key)
			continue
		}
		// We expect a flow error when echo event persistence fails.
		if e.Error != nil && e.Error.Type == model.ErrorTypeFlowError {
			errEvt = e
		}
	}

	wg.Wait()
	require.True(t, sawTransfer, "expected transfer event before echo persistence error")
	// When the context is cancelled before the error event can be emitted,
	// errEvt can be nil. If we did receive the error event, validate its message.
	if errEvt != nil {
		require.Equal(t, "Transfer failed: waiting for transfer echo persistence timed out", errEvt.Error.Message)
	}
	// Error during echo persistence (or its context) should also end the invocation.
	require.True(t, inv.EndInvocation)
}

// Verify that when endInvocationAfterTransfer is false, the processor
// does not mark the invocation as ended even after a successful transfer.
func TestTransferResponseProc_EndInvocationFalse(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv"),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{
			TargetAgentName:     "child",
			Message:             "hi",
			ToolResponseEventID: "tool-evt",
		}),
	)
	toolKey := agent.GetAppendEventNoticeKey(inv.TransferInfo.ToolResponseEventID)
	inv.AddNoticeChannel(context.Background(), toolKey)

	rsp := &model.Response{ID: "r1", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 10)
	proc := NewTransferResponseProcessor(false)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
		close(out)
	}()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = inv.NotifyCompletion(context.Background(), toolKey)
	}()

	for e := range out {
		if e.RequiresCompletion {
			_ = inv.NotifyCompletion(context.Background(), agent.GetAppendEventNoticeKey(e.ID))
		}
	}
	wg.Wait()
	// endInvocationAfterTransfer=false => EndInvocation should remain false.
	require.False(t, inv.EndInvocation)
}

func TestWaitForEventPersistence_DeadlineAlreadyExceeded(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	inv := agent.NewInvocation()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := proc.waitForEventPersistence(ctx, inv, "evt-2")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitForEventPersistence_NoticeChannelCreateFailed(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	// Use a zero-value Invocation to simulate missing noticeMu so that
	// AddNoticeChannelAndWait returns an internal error.
	inv := &agent.Invocation{}

	err := proc.waitForEventPersistence(context.Background(), inv, "evt-3")
	require.Error(t, err)
	require.Contains(t, err.Error(), "notice channel create failed")
}

func TestWaitForEventPersistence_TimeoutOrContextError(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	inv := agent.NewInvocation()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := proc.waitForEventPersistence(ctx, inv, "evt-4")
	require.Error(t, err)
}

// Verify that a failed transfer does not re-enter wait logic on subsequent responses.
func TestTransferResponseProc_FailedThenNoReentry(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	inv := &agent.Invocation{
		InvocationID: "inv-fail-retry",
		AgentName:    "parent",
		TransferInfo: &agent.TransferInfo{
			TargetAgentName:     "child",
			ToolResponseEventID: "evt-stale",
		},
	}
	rsp := &model.Response{ID: "r-fail", Created: time.Now().Unix(), Model: "m"}

	// First call: will fail on waitForEventPersistence due to zero-value Invocation
	// (noticeMu is nil), emit one error event, and mark EndInvocation.
	out1 := make(chan *event.Event, 1)
	proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out1)
	close(out1)

	var evt1 *event.Event
	for e := range out1 {
		evt1 = e
	}
	require.NotNil(t, evt1)
	require.NotNil(t, inv)
	require.True(t, inv.EndInvocation)
	require.Nil(t, inv.TransferInfo, "TransferInfo should be cleared after failed transfer")

	// Second call with the same invocation should be a no-op (no events).
	out2 := make(chan *event.Event, 1)
	proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out2)
	close(out2)
	var count int
	for range out2 {
		count++
	}
	require.Equal(t, 0, count, "Subsequent calls should not emit events when TransferInfo is cleared")
}
