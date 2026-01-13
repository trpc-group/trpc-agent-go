//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates one_shot_messages_by_node for parallel branches.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName          = "oneshot-by-node"
	defaultModelName = "deepseek-chat"

	nodeStart = "start"
	nodePrepA = "prep_a"
	nodePrepB = "prep_b"
	nodeLLM1  = "llm1"
	nodeLLM2  = "llm2"

	runtimeStateUserIDKey = "user_id"
)

var (
	modelName = flag.String(
		"model",
		defaultModelName,
		"Name of the model to use",
	)
	q1 = flag.String(
		"q1",
		"What is 1+1? Reply with prefix LLM1:",
		"One-shot user prompt for llm1",
	)
	q2 = flag.String(
		"q2",
		"What is 2+2? Reply with prefix LLM2:",
		"One-shot user prompt for llm2",
	)
)

func main() {
	flag.Parse()
	ctx := context.Background()
	if err := runOnce(ctx); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

func runOnce(ctx context.Context) error {
	schema := graph.MessagesStateSchema()
	mdl := openai.New(*modelName)

	sg := graph.NewStateGraph(schema)
	sg.
		AddNode(nodeStart, startNode).
		AddNode(nodePrepA, prepareLLM1).
		AddNode(nodePrepB, prepareLLM2).
		AddLLMNode(
			nodeLLM1,
			mdl,
			"Answer using the provided one-shot messages.",
			map[string]tool.Tool{},
		).
		AddLLMNode(
			nodeLLM2,
			mdl,
			"Answer using the provided one-shot messages.",
			map[string]tool.Tool{},
		).
		SetEntryPoint(nodeStart)

	sg.AddEdge(nodeStart, nodePrepA)
	sg.AddEdge(nodeStart, nodePrepB)
	sg.AddEdge(nodePrepA, nodeLLM1)
	sg.AddEdge(nodePrepB, nodeLLM2)
	sg.AddEdge(nodeLLM1, graph.End)
	sg.AddEdge(nodeLLM2, graph.End)

	g, err := sg.Compile()
	if err != nil {
		return fmt.Errorf("compile graph: %w", err)
	}

	gagent, err := graphagent.New(
		"oneshot-by-node-agent",
		g,
		graphagent.WithDescription(
			"Demonstrate one_shot_messages_by_node for parallel branches.",
		),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		gagent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("oneshot-by-node-%d", time.Now().Unix())
	msg := model.NewUserMessage("")

	ch, err := r.Run(
		ctx,
		userID,
		sessionID,
		msg,
		agent.WithRuntimeState(map[string]any{
			runtimeStateUserIDKey: userID,
		}),
	)
	if err != nil {
		return fmt.Errorf("runner run: %w", err)
	}
	for range ch {
	}

	return printSessionState(ctx, sessionService, userID, sessionID)
}

func startNode(ctx context.Context, state graph.State) (any, error) {
	return nil, nil
}

func prepareLLM1(ctx context.Context, state graph.State) (any, error) {
	msgs := []model.Message{
		model.NewSystemMessage(
			"You are llm1. Keep the answer short. Prefix with LLM1:",
		),
		model.NewUserMessage(*q1),
	}
	return graph.SetOneShotMessagesForNode(nodeLLM1, msgs), nil
}

func prepareLLM2(ctx context.Context, state graph.State) (any, error) {
	msgs := []model.Message{
		model.NewSystemMessage(
			"You are llm2. Keep the answer short. Prefix with LLM2:",
		),
		model.NewUserMessage(*q2),
	}
	return graph.SetOneShotMessagesForNode(nodeLLM2, msgs), nil
}

func printSessionState(
	ctx context.Context,
	svc session.Service,
	userID string,
	sessionID string,
) error {
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	var nodeResponses map[string]string
	if b, ok := sess.State[graph.StateKeyNodeResponses]; ok && len(b) > 0 {
		if err := json.Unmarshal(b, &nodeResponses); err != nil {
			return fmt.Errorf("decode node_responses: %w", err)
		}
	}
	fmt.Printf("node_responses: %v\n", nodeResponses)

	var byNode map[string][]model.Message
	raw, ok := sess.State[graph.StateKeyOneShotMessagesByNode]
	if ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &byNode); err != nil {
			return fmt.Errorf("decode one_shot_messages_by_node: %w", err)
		}
	}
	fmt.Printf(
		"one_shot_messages_by_node remaining entries: %d\n",
		len(byNode),
	)
	return nil
}
