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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
)

// fakePlanner implements planner.Planner for testing non-builtin branch.
type fakePlanner struct {
	instr     string
	processed bool
}

func (f *fakePlanner) BuildPlanningInstruction(ctx context.Context, inv *agent.Invocation, req *model.Request) string {
	return f.instr
}
func (f *fakePlanner) ProcessPlanningResponse(ctx context.Context, inv *agent.Invocation, rsp *model.Response) *model.Response {
	f.processed = true
	out := rsp.Clone()
	out.ID = "processed"
	return out
}

func TestPlanningRequestProcessor_AllBranches(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 4)

	// 1) Nil request early return
	NewPlanningRequestProcessor(nil).ProcessRequest(ctx, &agent.Invocation{}, nil, ch)

	// 2) No planner configured -> return
	req := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
	NewPlanningRequestProcessor(nil).ProcessRequest(ctx, &agent.Invocation{AgentName: "a"}, req, ch)

	// 3) Builtin planner path -> applies thinking config and returns
	eff := "low"
	think := true
	tokens := 5
	bp := builtin.New(builtin.Options{ReasoningEffort: &eff, ThinkingEnabled: &think, ThinkingTokens: &tokens})
	req3 := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
	NewPlanningRequestProcessor(bp).ProcessRequest(ctx, &agent.Invocation{AgentName: "a"}, req3, ch)
	require.NotNil(t, req3.ReasoningEffort)
	assert.Equal(t, "low", *req3.ReasoningEffort)

	// 4) Non-builtin planner: adds instruction if not present, emits event
	fp := &fakePlanner{instr: "PLAN: do X"}
	req4 := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "ping"}}}
	inv := &agent.Invocation{AgentName: "agent1", InvocationID: "inv1"}
	NewPlanningRequestProcessor(fp).ProcessRequest(ctx, inv, req4, ch)
	require.NotEmpty(t, req4.Messages)
	assert.Equal(t, model.RoleSystem, req4.Messages[0].Role)

	// 5) Non-builtin but same instruction content already present -> no duplication
	req5 := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("PLAN: do X and more"),
		{Role: model.RoleUser, Content: "msg"},
	}}
	NewPlanningRequestProcessor(fp).ProcessRequest(ctx, inv, req5, ch)
	// Still one system message at front
	count := 0
	for _, m := range req5.Messages {
		if m.Role == model.RoleSystem {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestPlanningResponseProcessor_AllBranches(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 4)
	pr := NewPlanningResponseProcessor(nil)
	// 1) invocation nil
	pr.ProcessResponse(ctx, nil, nil, nil, ch)
	// 2) rsp nil
	pr.ProcessResponse(ctx, &agent.Invocation{}, nil, nil, ch)
	// 3) planner nil
	pr.ProcessResponse(ctx, &agent.Invocation{}, nil, &model.Response{}, ch)
	// 4) no choices
	pr2 := NewPlanningResponseProcessor(&fakePlanner{})
	pr2.ProcessResponse(ctx, &agent.Invocation{AgentName: "a"}, nil, &model.Response{}, ch)

	// 5) process with choices and verify replacement
	rsp := &model.Response{ID: "orig", Choices: []model.Choice{{}}}
	pr2.ProcessResponse(ctx, &agent.Invocation{AgentName: "a", InvocationID: "i1"}, nil, rsp, ch)
	assert.Equal(t, "processed", rsp.ID)

	// Verify that the postprocessing event is marked as partial
	select {
	case evt := <-ch:
		require.NotNil(t, evt)
		require.NotNil(t, evt.Response)
		assert.True(t, evt.Response.IsPartial)
		assert.Equal(t, model.ObjectTypePostprocessingPlanning, evt.Object)
	default:
		t.Fatal("expected postprocessing event")
	}
}

func TestPlanningRequestProcessor_InvocationNil(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 4)

	fp := &fakePlanner{instr: "PLAN: do X"}
	req := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "ping"}}}

	// invocation is nil - should not send event
	NewPlanningRequestProcessor(fp).ProcessRequest(ctx, nil, req, ch)

	// Should not have sent any event
	select {
	case <-ch:
		t.Fatal("should not have sent event when invocation is nil")
	default:
		// Expected - no event sent
	}
}

func TestPlanningRequestProcessor_EmitEventError(t *testing.T) {
	// Create a cancelled context to force EmitEvent to return an error
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	ch := make(chan *event.Event, 4)
	fp := &fakePlanner{instr: "PLAN: do X"}
	req := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "ping"}}}
	inv := &agent.Invocation{AgentName: "agent1", InvocationID: "inv1"}

	// This should trigger the error path in agent.EmitEvent
	NewPlanningRequestProcessor(fp).ProcessRequest(ctx, inv, req, ch)

	// Should have processed the request but failed to emit event
	require.NotEmpty(t, req.Messages)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "PLAN: do X")

	// Channel should be empty due to EmitEvent error
	select {
	case <-ch:
		t.Fatal("should not have sent event due to EmitEvent error")
	default:
		// Expected - no event sent due to error
	}
}

func TestPlanningResponseProcessor_PartialResponse(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 4)

	pr := NewPlanningResponseProcessor(&fakePlanner{})
	rsp := &model.Response{
		ID:        "test",
		Choices:   []model.Choice{{}},
		IsPartial: true, // Partial response should be ignored
	}

	pr.ProcessResponse(ctx, &agent.Invocation{AgentName: "a", InvocationID: "i1"}, nil, rsp, ch)

	// Should not have processed or sent event
	select {
	case <-ch:
		t.Fatal("should not have sent event for partial response")
	default:
		// Expected - no event sent
	}
}

func TestPlanningResponseProcessor_EmitEventError(t *testing.T) {
	// Create a cancelled context to force EmitEvent to return an error
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	ch := make(chan *event.Event, 4)
	pr := NewPlanningResponseProcessor(&fakePlanner{})
	rsp := &model.Response{ID: "test", Choices: []model.Choice{{}}}

	// This should trigger the error path in agent.EmitEvent
	pr.ProcessResponse(ctx, &agent.Invocation{AgentName: "a", InvocationID: "i1"}, nil, rsp, ch)

	// Should have processed the response but failed to emit event
	assert.Equal(t, "processed", rsp.ID)
	// Channel should be empty due to EmitEvent error
	select {
	case <-ch:
		t.Fatal("should not have sent event due to EmitEvent error")
	default:
		// Expected - no event sent due to error
	}
}
