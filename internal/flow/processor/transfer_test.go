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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/team"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testControllerRejectErr = "blocked"
	testNodeTimeout         = 10 * time.Second
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

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
	}

	rsp := &model.Response{ID: "r1", Created: time.Now().Unix(), Model: "m"}

	out := make(chan *event.Event, 10)
	proc := NewTransferResponseProcessor(true)
	proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)

	// Expect transfer event + child event
	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 3)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, "child", evts[1].Author)
}

func TestTransferResponseProc_Target404(t *testing.T) {
	parent := &parentAgent{child: nil}
	inv := &agent.Invocation{Agent: parent, AgentName: "parent", InvocationID: "inv", TransferInfo: &agent.TransferInfo{TargetAgentName: "missing"}}
	rsp := &model.Response{ID: "r"}
	out := make(chan *event.Event, 1)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	evt := <-out
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
	require.Nil(t, inv.TransferInfo)
}

type rejectTransferController struct{}

func (rejectTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, errors.New(testControllerRejectErr)
}

func TestTransferResponseProc_ControllerRejects(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-ctrl",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: rejectTransferController{},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}

	rsp := &model.Response{ID: "r-ctrl"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 1)
	require.NotNil(t, evts[0].Error)
	require.Nil(t, inv.TransferInfo)
}

type deadlineAgent struct {
	name        string
	gotDeadline bool
}

func (d *deadlineAgent) Info() agent.Info {
	return agent.Info{Name: d.name}
}

func (d *deadlineAgent) SubAgents() []agent.Agent { return nil }

func (d *deadlineAgent) FindSubAgent(string) agent.Agent { return nil }

func (d *deadlineAgent) Tools() []tool.Tool { return nil }

func (d *deadlineAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	_, d.gotDeadline = ctx.Deadline()
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, d.name)
	}()
	return ch, nil
}

type timeoutTransferController struct {
	timeout time.Duration
}

func (t timeoutTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return t.timeout, nil
}

func TestTransferResponseProc_ControllerNodeTimeout(t *testing.T) {
	target := &deadlineAgent{name: "child"}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-timeout",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: timeoutTransferController{
					timeout: testNodeTimeout,
				},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}

	rsp := &model.Response{ID: "r-timeout"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	for range out {
	}
	require.True(t, target.gotDeadline)
}

func TestTransferResponseProc_SetsTransferTags(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-tag",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
	}
	rsp := &model.Response{ID: "r-tag", Created: time.Now().Unix(), Model: "m"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)

	transferTagCount := 0
	for evt := range out {
		if evt.Tag == event.TransferTag {
			transferTagCount++
		}
	}

	require.GreaterOrEqual(t, transferTagCount, 2)
}

type doneResponseAgent struct {
	name string
}

func (d *doneResponseAgent) Info() agent.Info                { return agent.Info{Name: d.name} }
func (d *doneResponseAgent) SubAgents() []agent.Agent        { return nil }
func (d *doneResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *doneResponseAgent) Tools() []tool.Tool              { return nil }
func (d *doneResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			d.name,
			&model.Response{Done: true},
		)
	}()
	return ch, nil
}

func TestTransferResponseProc_CrossRequestTransfer_SetsStateDelta(t *testing.T) {
	target := &doneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}

	sess := session.NewSession("app", "user", "sess")
	sess.SetState(team.SwarmTeamNameKey, []byte("team"))

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-xrt",
		Session:      sess,
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r1"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	var doneEvt *event.Event
	for evt := range out {
		if evt != nil && evt.Author == "child" && evt.Response != nil && evt.Response.Done {
			doneEvt = evt
		}
	}
	require.NotNil(t, doneEvt)
	require.Equal(t, []byte("child"), doneEvt.StateDelta[team.SwarmActiveAgentKeyPrefix+"team"])
}

func TestTransferResponseProc_CrossRequestTransfer_SkipsStateDeltaWithoutMarker(t *testing.T) {
	target := &doneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}

	sess := session.NewSession("app", "user", "sess")

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-xrt-skip",
		Session:      sess,
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r1"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	for evt := range out {
		if evt == nil || evt.Author != "child" || evt.Response == nil || !evt.Response.Done {
			continue
		}
		_, ok := evt.StateDelta[team.SwarmActiveAgentKeyPrefix+"team"]
		require.False(t, ok)
		return
	}
	t.Fatal("missing done response event from child")
}

func TestTransferResponseProc_SaveActiveAgent_EarlyReturns(t *testing.T) {
	proc := NewTransferResponseProcessor(true)
	target := &doneResponseAgent{name: "child"}
	ctx := context.Background()

	require.NotPanics(t, func() {
		proc.saveActiveAgent(ctx, nil, target, &event.Event{})
	})

	require.NotPanics(t, func() {
		proc.saveActiveAgent(ctx, &agent.Invocation{}, target, &event.Event{})
	})

	sess := session.NewSession("app", "user", "sess")
	inv := &agent.Invocation{Session: sess}

	require.NotPanics(t, func() {
		proc.saveActiveAgent(ctx, inv, target, nil)
	})

	evt := &event.Event{}
	proc.saveActiveAgent(ctx, inv, target, evt)
	require.Nil(t, evt.StateDelta)
}

func TestSwarmActiveAgentKey_EmptyTeamName(t *testing.T) {
	require.Equal(t, swarmActiveAgentKeyPrefix, swarmActiveAgentKey(""))
}
