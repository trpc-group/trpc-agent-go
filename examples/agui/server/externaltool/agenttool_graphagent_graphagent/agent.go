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
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	parentAgentName     = "agui-agenttool-parent"
	childAgentName      = "review_graph_tool"
	childReviewToolName = "request_review_decision"
	nodeCallReviewGraph = "call_review_graph"
	nodeExecuteTools    = "execute_tools"
	nodeFinalAnswer     = "final_answer"
	childNodeCallReview = "call_review_decision"
	childNodeReview     = "review"
	childInterruptKey   = "review_decision"
)

func newParentGraphAgent(
	childAgent *graphagent.GraphAgent,
	saver graph.CheckpointSaver,
	modelInstance model.Model,
	generationConfig model.GenerationConfig,
) (*graphagent.GraphAgent, error) {
	g, err := buildParentGraph(childAgent, modelInstance, generationConfig)
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		parentAgentName,
		g,
		graphagent.WithDescription("AG-UI demo for GraphAgent ToolsNode running AgentTool with child graph interrupts."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(saver),
	)
}

func newChildReviewAgent(
	saver graph.CheckpointSaver,
	modelInstance model.Model,
	generationConfig model.GenerationConfig,
) (*graphagent.GraphAgent, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		childNodeCallReview,
		modelInstance,
		"You are a helpful assistant.",
		map[string]tool.Tool{childReviewToolName: newReviewDecisionTool()},
		graph.WithGenerationConfig(generationConfig),
	)
	sg.AddNode(childNodeReview, childReviewNode)
	g, err := sg.SetEntryPoint(childNodeCallReview).
		AddEdge(childNodeCallReview, childNodeReview).
		SetFinishPoint(childNodeReview).
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		childAgentName,
		g,
		graphagent.WithDescription("Child GraphAgent wrapped by AgentTool that waits for a review decision."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(saver),
	)
}

func buildParentGraph(
	childAgent *graphagent.GraphAgent,
	modelInstance model.Model,
	generationConfig model.GenerationConfig,
) (*graph.Graph, error) {
	tools := map[string]tool.Tool{
		childAgentName: agenttool.NewTool(childAgent),
	}
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		nodeCallReviewGraph,
		modelInstance,
		"You are a helpful assistant.",
		tools,
		graph.WithGenerationConfig(generationConfig),
	)
	sg.AddToolsNode(nodeExecuteTools, tools)
	sg.AddLLMNode(
		nodeFinalAnswer,
		modelInstance,
		"You are a helpful assistant.",
		nil,
		graph.WithGenerationConfig(generationConfig),
	)
	sg.SetEntryPoint(nodeCallReviewGraph)
	sg.AddEdge(nodeCallReviewGraph, nodeExecuteTools)
	sg.AddEdge(nodeExecuteTools, nodeFinalAnswer)
	sg.SetFinishPoint(nodeFinalAnswer)
	return sg.Compile()
}

func childReviewNode(ctx context.Context, state graph.State) (any, error) {
	messages, _ := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	if len(messages) == 0 {
		return nil, errors.New("missing review tool call message")
	}
	last := messages[len(messages)-1]
	if last.Role != model.RoleAssistant || len(last.ToolCalls) != 1 {
		return nil, errors.New("last message must contain one review tool call")
	}
	toolCall := last.ToolCalls[0]
	if toolCall.Function.Name != childReviewToolName {
		return nil, fmt.Errorf("unexpected tool call %q", toolCall.Function.Name)
	}
	value, err := graph.Interrupt(ctx, state, childInterruptKey, "Review decision is required.")
	if err != nil {
		return nil, err
	}
	decision, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("review decision must be a string, got %T", value)
	}
	decision = strings.TrimSpace(decision)
	if decision == "" {
		return nil, errors.New("review decision cannot be empty")
	}
	result := "child graph resumed with review decision: " + decision
	return graph.State{
		graph.StateKeyMessages: graph.AppendMessages{
			Items: []model.Message{
				model.NewToolMessage(toolCall.ID, toolCall.Function.Name, result),
			},
		},
		graph.StateKeyLastResponse: result,
	}, nil
}

func newReviewDecisionTool() tool.Tool {
	return function.NewFunctionTool(
		func(context.Context, reviewDecisionArgs) (reviewDecisionResult, error) {
			return reviewDecisionResult{}, errors.New("request_review_decision is handled by the graph interrupt node")
		},
		function.WithName(childReviewToolName),
		function.WithDescription("Ask a human reviewer for the review decision."),
	)
}

type reviewDecisionArgs struct {
	Request string `json:"request" description:"The review request that needs a human decision."`
}

type reviewDecisionResult struct {
	Decision string `json:"decision" description:"The human review decision."`
}

func newGenerationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0),
		Stream:      *isStream,
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
