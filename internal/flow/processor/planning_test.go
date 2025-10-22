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
	if req3.ReasoningEffort == nil || *req3.ReasoningEffort != "low" {
		t.Fatalf("expected reasoning effort applied")
	}

	// 4) Non-builtin planner: adds instruction if not present, emits event
	fp := &fakePlanner{instr: "PLAN: do X"}
	req4 := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "ping"}}}
	inv := &agent.Invocation{AgentName: "agent1", InvocationID: "inv1"}
	NewPlanningRequestProcessor(fp).ProcessRequest(ctx, inv, req4, ch)
	if len(req4.Messages) == 0 || req4.Messages[0].Role != model.RoleSystem {
		t.Fatalf("expected planning instruction system message added")
	}

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
	if count != 1 {
		t.Fatalf("expected no duplicate system message, got %d", count)
	}
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
	if rsp.ID != "processed" {
		t.Fatalf("expected processed response id, got %s", rsp.ID)
	}
}
