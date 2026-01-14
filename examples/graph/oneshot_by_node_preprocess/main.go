//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates preparing one_shot_messages_by_node from a single
// upstream node.
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
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName   = "oneshot-by-node-preprocess"
	agentName = "oneshot-by-node-preprocess-agent"

	nodeStart = "start"
	nodePrep  = "preprocess"
	nodeLLM1  = "llm1"
	nodeLLM2  = "llm2"

	defaultUserID = "user"
)

var (
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
	userInput = flag.String(
		"user_input",
		"fallback user input",
		"Fallback user input for the run",
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
	mdl := &echoModel{}

	sg := graph.NewStateGraph(schema)
	sg.
		AddNode(nodeStart, startNode).
		AddNode(nodePrep, preprocess).
		AddLLMNode(nodeLLM1, mdl, "", map[string]tool.Tool{}).
		AddLLMNode(nodeLLM2, mdl, "", map[string]tool.Tool{}).
		SetEntryPoint(nodeStart)

	sg.AddEdge(nodeStart, nodePrep)
	sg.AddEdge(nodePrep, nodeLLM1)
	sg.AddEdge(nodePrep, nodeLLM2)
	sg.AddEdge(nodeLLM1, graph.End)
	sg.AddEdge(nodeLLM2, graph.End)

	g, err := sg.Compile()
	if err != nil {
		return fmt.Errorf("compile graph: %w", err)
	}

	gagent, err := graphagent.New(
		agentName,
		g,
		graphagent.WithDescription(
			"Prepare one_shot_messages_by_node in one upstream node.",
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

	sessionID := fmt.Sprintf("oneshot-by-node-prep-%d", time.Now().Unix())
	msg := model.NewUserMessage(*userInput)

	ch, err := r.Run(ctx, defaultUserID, sessionID, msg,
		agent.WithRuntimeState(map[string]any{
			"user_id": defaultUserID,
		}),
	)
	if err != nil {
		return fmt.Errorf("runner run: %w", err)
	}
	for range ch {
	}

	return printSessionState(ctx, sessionService, defaultUserID, sessionID)
}

func startNode(ctx context.Context, state graph.State) (any, error) {
	return nil, nil
}

func preprocess(ctx context.Context, state graph.State) (any, error) {
	byNode := map[string][]model.Message{
		nodeLLM1: {
			model.NewSystemMessage(
				"You are llm1. Prefix your reply with LLM1:",
			),
			model.NewUserMessage(*q1),
		},
		nodeLLM2: {
			model.NewSystemMessage(
				"You are llm2. Prefix your reply with LLM2:",
			),
			model.NewUserMessage(*q2),
		},
	}
	return graph.SetOneShotMessagesByNode(byNode), nil
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

type echoModel struct{}

func (m *echoModel) Info() model.Info {
	return model.Info{Name: "echo-model"}
}

func (m *echoModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	sys := firstMessageByRole(req.Messages, model.RoleSystem)
	user := lastMessageByRole(req.Messages, model.RoleUser)
	content := fmt.Sprintf("SYS=%q USER=%q", sys, user)

	responseChan := make(chan *model.Response, 1)
	responseChan <- &model.Response{
		ID:        "echo-response",
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Model:     m.Info().Name,
		Timestamp: time.Now(),
		Done:      true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
	}
	close(responseChan)

	return responseChan, nil
}

func firstMessageByRole(msgs []model.Message, role model.Role) string {
	for _, msg := range msgs {
		if msg.Role == role {
			return msg.Content
		}
	}
	return ""
}

func lastMessageByRole(msgs []model.Message, role model.Role) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == role {
			return msgs[i].Content
		}
	}
	return ""
}
