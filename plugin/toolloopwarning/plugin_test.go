//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

func TestPluginAddsOneTransientWarningAndCleansUp(t *testing.T) {
	manager, err := plugin.NewManager(New())
	if err != nil {
		t.Fatal(err)
	}
	invocation := &agent.Invocation{}
	response := toolResponse("search", []byte(`{"query":"x"}`))
	results := []model.Message{model.NewToolMessage("call", "search", "same")}
	args := &plugin.AfterToolRoundArgs{
		Invocation:         invocation,
		ToolCallResponse:   response,
		ToolResultMessages: results,
		Complete:           true,
	}
	manager.AfterToolRound(context.Background(), args)
	manager.AfterToolRound(context.Background(), args)

	ctx := agent.NewInvocationContext(context.Background(), invocation)
	request := &model.Request{}
	if _, err := manager.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 1 || request.Messages[0].Content != warning {
		t.Fatalf("warning was not appended: %+v", request.Messages)
	}

	secondRequest := &model.Request{}
	if _, err := manager.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: secondRequest},
	); err != nil {
		t.Fatal(err)
	}
	if len(secondRequest.Messages) != 0 {
		t.Fatalf("warning was appended more than once: %+v", secondRequest.Messages)
	}

	if _, err := manager.AgentCallbacks().RunAfterAgent(
		context.Background(),
		&agent.AfterAgentArgs{Invocation: invocation},
	); err != nil {
		t.Fatal(err)
	}
	thirdRequest := &model.Request{}
	if _, err := manager.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: thirdRequest},
	); err != nil {
		t.Fatal(err)
	}
	if len(thirdRequest.Messages) != 0 {
		t.Fatalf("warning state survived after agent: %+v", thirdRequest.Messages)
	}
}

func TestPluginIgnoresIncompleteRound(t *testing.T) {
	manager, err := plugin.NewManager(New())
	if err != nil {
		t.Fatal(err)
	}
	invocation := &agent.Invocation{}
	args := &plugin.AfterToolRoundArgs{
		Invocation:         invocation,
		ToolCallResponse:   toolResponse("search", []byte(`{"query":"x"}`)),
		ToolResultMessages: []model.Message{model.NewToolMessage("call", "search", "same")},
	}
	args.Complete = false
	manager.AfterToolRound(context.Background(), args)
	if _, ok := agent.GetStateValue[detectorState](invocation, stateKey); !ok {
		// An empty state is acceptable, but the plugin must not create a pending warning.
		return
	}
	state, _ := agent.GetStateValue[detectorState](invocation, stateKey)
	if state.Pending {
		t.Fatal("incomplete round created a pending warning")
	}
}
