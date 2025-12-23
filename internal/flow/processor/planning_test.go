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
