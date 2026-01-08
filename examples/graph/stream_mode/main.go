//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName        = "stream-mode-demo"
	graphAgentName = "stream-mode-agent"
	userID         = "user"
	sessionID      = "session"
	nodeAsk        = "ask"
	userInputText  = "Explain StreamMode in one sentence."
	chunk1         = "Toy stream: "
	chunk2         = "hello"
	finalMsg       = "Toy stream: hello"
)

func main() {
	sg := graph.NewStateGraph(graph.NewStateSchema())
	sg.AddNode(nodeAsk, askNode)
	sg.SetEntryPoint(nodeAsk)
	sg.SetFinishPoint(nodeAsk)
	g := sg.MustCompile()

	ga, err := graphagent.New(graphAgentName, g)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}

	sess := sessioninmemory.NewSessionService()
	r := runner.NewRunner(appName, ga, runner.WithSessionService(sess))
	defer r.Close()

	eventCh, err := r.Run(context.Background(), userID, sessionID,
		model.NewUserMessage(userInputText),
		agent.WithStreamMode(agent.StreamModeMessages))
	if err != nil {
		log.Fatalf("runner run failed: %v", err)
	}

	for e := range eventCh {
		switch e.Object {
		case model.ObjectTypeChatCompletionChunk:
			if len(e.Choices) > 0 {
				fmt.Print(e.Choices[0].Delta.Content)
			}
		case model.ObjectTypeChatCompletion:
			if len(e.Choices) > 0 {
				fmt.Printf("\n(final) %s\n", e.Choices[0].Message.Content)
			}
		default:
			fmt.Printf("\n[%s]\n", e.Object)
		}
	}
	fmt.Println()
}

func askNode(ctx context.Context, state graph.State) (any, error) {
	emitter := graph.GetEventEmitter(state)
	_ = emitter.Emit(&event.Event{
		Response: &model.Response{
			Object:  model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{Delta: model.NewAssistantMessage(chunk1)}},
		},
	})
	_ = emitter.Emit(&event.Event{
		Response: &model.Response{
			Object:  model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{Delta: model.NewAssistantMessage(chunk2)}},
		},
	})
	_ = emitter.Emit(&event.Event{
		Response: &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage(finalMsg)}},
		},
	})
	return graph.State{}, nil
}
