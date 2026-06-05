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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	graphcheckpoint "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	researchAgentName  = "research_graph_agent"
	reviewAgentName    = "review_agent"
	nodeResearchLLM    = "research_llm"
	nodeExternalGate   = "external_tool_interrupt"
	externalToolName   = "external_search"
	keyToolResultReady = "external_tool_result_ready"
)

type toolRequest struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Args       string `json:"args"`
}

func newGraphAgent(m model.Model, cfg model.GenerationConfig) (agent.Agent, error) {
	saver := graphcheckpoint.NewSaver()
	research, err := newResearchGraphAgent(m, cfg, saver)
	if err != nil {
		return nil, err
	}
	review := newReviewAgent(m, cfg)
	parentGraph, err := buildParentGraph()
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		agentName,
		parentGraph,
		graphagent.WithDescription("AG-UI parent GraphAgent demo with an AgentNode child GraphAgent interrupt."),
		graphagent.WithSubAgents([]agent.Agent{research, review}),
		graphagent.WithCheckpointSaver(saver),
	)
}

func newResearchGraphAgent(
	m model.Model,
	cfg model.GenerationConfig,
	saver graph.CheckpointSaver,
) (agent.Agent, error) {
	childGraph, err := buildResearchGraph(m, cfg)
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		researchAgentName,
		childGraph,
		graphagent.WithDescription("Research GraphAgent that interrupts for caller-executed external_search."),
		graphagent.WithCheckpointSaver(saver),
	)
}

func newReviewAgent(m model.Model, cfg model.GenerationConfig) agent.Agent {
	return llmagent.New(
		reviewAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(reviewInstruction()),
		llmagent.WithGenerationConfig(cfg),
	)
}

func buildParentGraph() (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(researchAgentName)
	sg.AddAgentNode(reviewAgentName)
	sg.SetEntryPoint(researchAgentName)
	sg.AddEdge(researchAgentName, reviewAgentName)
	sg.SetFinishPoint(reviewAgentName)
	return sg.Compile()
}

func buildResearchGraph(m model.Model, cfg model.GenerationConfig) (*graph.Graph, error) {
	tools := map[string]tool.Tool{
		externalToolName: externalSearchTool(),
	}
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		nodeResearchLLM,
		m,
		childInstruction(),
		tools,
		graph.WithGenerationConfig(cfg),
	)
	sg.AddNode(nodeExternalGate, interruptForExternalTool)
	sg.SetEntryPoint(nodeResearchLLM)
	sg.AddEdge(nodeResearchLLM, nodeExternalGate)
	sg.AddConditionalEdges(nodeExternalGate, routeAfterExternalGate, map[string]string{
		nodeResearchLLM: nodeResearchLLM,
		graph.End:       graph.End,
	})
	sg.SetFinishPoint(nodeResearchLLM)
	return sg.Compile()
}

func childInstruction() string {
	return strings.Join([]string{
		"You are the child GraphAgent research model.",
		"For each user request, call external_search exactly once with {\"query\":\"<concise query>\"}.",
		"When you receive the external_search tool result, answer directly.",
		"Do not call external_search again after receiving its tool result.",
	}, "\n")
}

func reviewInstruction() string {
	return strings.Join([]string{
		"You are the review agent.",
		"Ignore the user message.",
		"Reply with exactly this line:",
		"Parent GraphAgent continued to review_agent after child GraphAgent resume.",
	}, "\n")
}

func interruptForExternalTool(ctx context.Context, state graph.State) (any, error) {
	request, ok, err := pendingExternalToolRequest(state)
	if err != nil {
		return nil, err
	}
	if !ok {
		return graph.State{keyToolResultReady: nil}, nil
	}
	resumeValue, err := graph.Interrupt(ctx, state, request.ToolCallID, request)
	if err != nil {
		return nil, err
	}
	result, ok := resumeValue.(string)
	if !ok {
		return nil, fmt.Errorf("resume value for tool call %s must be a string", request.ToolCallID)
	}
	if strings.TrimSpace(result) == "" {
		return nil, fmt.Errorf("resume value for tool call %s cannot be empty", request.ToolCallID)
	}
	toolMessage := model.NewToolMessage(request.ToolCallID, request.Name, result)
	return graph.State{
		graph.StateKeyMessages: graph.AppendMessages{Items: []model.Message{toolMessage}},
		keyToolResultReady:     true,
	}, nil
}

func pendingExternalToolRequest(state graph.State) (toolRequest, bool, error) {
	msgs, _ := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	if len(msgs) == 0 {
		return toolRequest{}, false, nil
	}
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleAssistant || len(last.ToolCalls) == 0 {
		return toolRequest{}, false, nil
	}
	call := last.ToolCalls[len(last.ToolCalls)-1]
	if call.ID == "" {
		return toolRequest{}, false, fmt.Errorf("external tool call missing id")
	}
	if call.Function.Name != externalToolName {
		return toolRequest{}, false, fmt.Errorf("unsupported external tool %q", call.Function.Name)
	}
	return toolRequest{
		ToolCallID: call.ID,
		Name:       call.Function.Name,
		Args:       string(call.Function.Arguments),
	}, true, nil
}

func routeAfterExternalGate(_ context.Context, state graph.State) (string, error) {
	if ready, _ := state[keyToolResultReady].(bool); ready {
		return nodeResearchLLM, nil
	}
	return graph.End, nil
}

func externalSearchTool() tool.Tool {
	return function.NewFunctionTool(
		externalSearchNotImplemented,
		function.WithName(externalToolName),
		function.WithDescription("Searches a caller-owned external knowledge source."),
	)
}

func externalSearchNotImplemented(context.Context, externalSearchArgs) (externalSearchResult, error) {
	return externalSearchResult{}, errors.New("external_search is executed by the AG-UI caller")
}

type externalSearchArgs struct {
	Query string `json:"query" description:"The search query."`
}

type externalSearchResult struct {
	Result string `json:"result" description:"The tool result content."`
}
