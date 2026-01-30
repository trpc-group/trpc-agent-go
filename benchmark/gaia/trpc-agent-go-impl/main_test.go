//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
)

func TestNewGAIAPlanner_DisabledReturnsReactPlanner(t *testing.T) {
	t.Parallel()

	p, err := newGAIAPlanner(false, 0)
	if err != nil {
		t.Fatalf("newGAIAPlanner: %v", err)
	}
	if _, ok := p.(*react.Planner); !ok {
		t.Fatalf("planner type = %T, want *react.Planner", p)
	}
}

func TestNewGAIAPlanner_EnabledForcesFinalAnswer(t *testing.T) {
	t.Parallel()

	p, err := newGAIAPlanner(true, 1)
	if err != nil {
		t.Fatalf("newGAIAPlanner: %v", err)
	}
	if _, ok := p.(*ralphLoopWrapperPlanner); !ok {
		t.Fatalf(
			"planner type = %T, want *ralphLoopWrapperPlanner",
			p,
		)
	}

	inv := &agent.Invocation{}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "still working",
			},
		}},
	}

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	if processed == nil {
		t.Fatalf("ProcessPlanningResponse returned nil")
	}
	if processed.Done {
		t.Fatalf("Done = true, want false")
	}

	req := &model.Request{}
	instruction := p.BuildPlanningInstruction(
		context.Background(),
		inv,
		req,
	)
	if !strings.Contains(instruction, "Ralph Loop mode") {
		t.Fatalf("instruction missing Ralph Loop text: %q", instruction)
	}
	if !strings.Contains(instruction, react.PlanningTag) {
		t.Fatalf("instruction missing react planning tag: %q", instruction)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("injected messages = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Role != model.RoleSystem {
		t.Fatalf(
			"injected message role = %q, want %q",
			req.Messages[0].Role,
			model.RoleSystem,
		)
	}
	if !strings.Contains(req.Messages[0].Content, gaiaFinalAnswerPrefix) {
		t.Fatalf(
			"injected message missing %q: %q",
			gaiaFinalAnswerPrefix,
			req.Messages[0].Content,
		)
	}
}

func TestGAIAFinalAnswerVerifier(t *testing.T) {
	t.Parallel()

	v := gaiaFinalAnswerVerifier{}

	okRsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "FINAL ANSWER: 42",
			},
		}},
	}
	res, err := v.Verify(context.Background(), &agent.Invocation{}, okRsp)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Passed {
		t.Fatalf("Passed = false, want true")
	}

	badRsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "Answer: 42",
			},
		}},
	}
	res, err = v.Verify(context.Background(), &agent.Invocation{}, badRsp)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Passed {
		t.Fatalf("Passed = true, want false")
	}
	if !strings.Contains(res.Feedback, gaiaFinalAnswerPrefix) {
		t.Fatalf("feedback missing %q: %q", gaiaFinalAnswerPrefix, res.Feedback)
	}
}
