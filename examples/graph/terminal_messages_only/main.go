//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	parentAgentName = "parent"
	draftAgentName  = "draft"
	finalAgentName  = "final"

	nodeEmit = "emit"

	userInputText = "Show me the final answer only."

	draftChunk = "draft: collecting context"
	draftFinal = "draft: collecting context"
	finalChunk = "final: user-visible answer"
	finalFinal = "final: user-visible answer"
)

func main() {
	parentAgent, err := buildParentAgent()
	if err != nil {
		log.Fatalf("build parent agent failed: %v", err)
	}

	if err := runDemo(parentAgent, false); err != nil {
		log.Fatalf("run default demo failed: %v", err)
	}
	fmt.Println()
	if err := runDemo(parentAgent, true); err != nil {
		log.Fatalf("run terminal-only demo failed: %v", err)
	}
}

func buildParentAgent() (*graphagent.GraphAgent, error) {
	draftAgent, err := buildChildAgent(
		draftAgentName,
		draftChunk,
		draftFinal,
	)
	if err != nil {
		return nil, err
	}
	finalAgent, err := buildChildAgent(
		finalAgentName,
		finalChunk,
		finalFinal,
	)
	if err != nil {
		return nil, err
	}

	parentGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(draftAgentName).
		AddAgentNode(
			finalAgentName,
			graph.WithSubgraphInputFromLastResponse(),
		).
		AddEdge(draftAgentName, finalAgentName).
		SetEntryPoint(draftAgentName).
		SetFinishPoint(finalAgentName).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		parentAgentName,
		parentGraph,
		graphagent.WithSubAgents([]agent.Agent{
			draftAgent,
			finalAgent,
		}),
	)
}

func buildChildAgent(
	name string,
	chunk string,
	final string,
) (*graphagent.GraphAgent, error) {
	childGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode(
			nodeEmit,
			func(ctx context.Context, state graph.State) (any, error) {
				return emitAssistantMessage(state, name, chunk, final)
			},
		).
		SetEntryPoint(nodeEmit).
		SetFinishPoint(nodeEmit).
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(name, childGraph)
}

func emitAssistantMessage(
	state graph.State,
	agentName string,
	chunk string,
	final string,
) (any, error) {
	emitter := graph.GetEventEmitter(state)
	responseID := "response-" + agentName
	if emitter != nil {
		_ = emitter.Emit(&event.Event{
			Response: &model.Response{
				ID:        responseID,
				IsPartial: true,
				Object:    model.ObjectTypeChatCompletionChunk,
				Choices: []model.Choice{{
					Delta: model.NewAssistantMessage(chunk),
				}},
			},
		})
		_ = emitter.Emit(&event.Event{
			Response: &model.Response{
				ID:     responseID,
				Done:   true,
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(final),
				}},
			},
		})
	}
	return graph.State{
		graph.StateKeyLastResponse:   final,
		graph.StateKeyLastResponseID: responseID,
	}, nil
}

func runDemo(
	parentAgent *graphagent.GraphAgent,
	terminalOnly bool,
) error {
	title := "default"
	if terminalOnly {
		title = "terminal-only"
	}
	fmt.Printf("== %s ==\n", title)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage(userInputText)),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphTerminalMessagesOnly(terminalOnly),
		)),
	)
	events, err := parentAgent.Run(context.Background(), invocation)
	if err != nil {
		return err
	}
	for evt := range events {
		if evt == nil || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		if evt.Response.IsPartial {
			fmt.Printf(
				"%s chunk: %s\n",
				evt.FilterKey,
				evt.Response.Choices[0].Delta.Content,
			)
			continue
		}
		text := evt.Response.Choices[0].Message.Content
		if text == "" {
			continue
		}
		fmt.Printf("%s final: %s\n", evt.FilterKey, text)
	}
	return nil
}
