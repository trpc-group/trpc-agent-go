//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package processor

import (
	"context"
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
	name string
	emit bool
}

func (m *mockAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockAgent) Tools() []tool.Tool              { return nil }
func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
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
	proc := NewTransferResponseProcessor()
	proc.ProcessResponse(context.Background(), inv, rsp, out)
	close(out)

	// Expect transfer event + child event
	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 2)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, "child", evts[1].Author)
}

func TestTransferResponseProc_Target404(t *testing.T) {
	parent := &parentAgent{child: nil}
	inv := &agent.Invocation{Agent: parent, AgentName: "parent", InvocationID: "inv", TransferInfo: &agent.TransferInfo{TargetAgentName: "missing"}}
	rsp := &model.Response{ID: "r"}
	out := make(chan *event.Event, 1)
	NewTransferResponseProcessor().ProcessResponse(context.Background(), inv, rsp, out)
	close(out)
	evt := <-out
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
}
