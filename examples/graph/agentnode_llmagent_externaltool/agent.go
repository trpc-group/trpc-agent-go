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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	graphAgentName    = "agentnode-llmagent-externaltool-graph"
	researchAgentName = "research_agent"
	nodeGate          = "external_tool_gate"
	externalToolName  = "external_search"
	keyToolRequest    = "external_tool_request"
	keyToolMessage    = "external_tool_message"
)

type toolRequest struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Args       string `json:"args"`
}

func newGraphAgent(m model.Model, cfg model.GenerationConfig) (agent.Agent, error) {
	research := llmagent.New(
		researchAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(`You are the research child agent.
For a user request, call external_search exactly once with {"query":"<concise query>"}.
When you receive the external_search tool result, answer directly and do not call external_search again.`),
		llmagent.WithGenerationConfig(cfg),
	)
	g, err := buildGraph()
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		graphAgentName,
		g,
		graphagent.WithDescription("GraphAgent AgentNode external tool interrupt example."),
		graphagent.WithSubAgents([]agent.Agent{research}),
		graphagent.WithCheckpointSaver(checkpointinmemory.NewSaver()),
	)
}

func buildGraph() (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(
		researchAgentName,
		graph.WithAgentNodeRunOptions(agent.WithExternalTools([]tool.Tool{externalSearchTool()})),
		graph.WithAgentNodeInputMapper(mapExternalToolMessage),
		graph.WithSubgraphOutputMapper(storeResearchToolCall),
	)
	sg.AddNode(nodeGate, interruptForExternalTool)
	sg.SetEntryPoint(researchAgentName)
	sg.AddEdge(researchAgentName, nodeGate)
	sg.AddConditionalEdges(nodeGate, routeAfterGate, map[string]string{
		researchAgentName: researchAgentName,
		graph.End:         graph.End,
	})
	sg.SetFinishPoint(researchAgentName)
	return sg.Compile()
}

func storeResearchToolCall(_ graph.State, result graph.SubgraphResult) graph.State {
	for _, call := range result.ToolCalls {
		if call.ID == "" || call.Function.Name != externalToolName {
			continue
		}
		return graph.State{keyToolRequest: toolRequest{
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Args:       string(call.Function.Arguments),
		}}
	}
	return graph.State{keyToolMessage: nil}
}

func interruptForExternalTool(ctx context.Context, state graph.State) (any, error) {
	request, ok, err := toolRequestFromState(state)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
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
		keyToolMessage: &toolMessage,
		keyToolRequest: nil,
	}, nil
}

func routeAfterGate(_ context.Context, state graph.State) (string, error) {
	if state[keyToolMessage] != nil {
		return researchAgentName, nil
	}
	return graph.End, nil
}

func mapExternalToolMessage(state graph.State) graph.State {
	if state[keyToolMessage] == nil {
		return nil
	}
	return graph.State{graph.StateKeyAgentInputMessage: state[keyToolMessage]}
}

func toolRequestFromState(state graph.State) (toolRequest, bool, error) {
	raw, ok := state[keyToolRequest]
	if !ok || raw == nil {
		return toolRequest{}, false, nil
	}
	request, err := decodeValue[toolRequest](raw)
	if err != nil {
		return toolRequest{}, false, err
	}
	if request.ToolCallID == "" {
		return toolRequest{}, false, errors.New("external tool call missing id")
	}
	if request.Name != externalToolName {
		return toolRequest{}, false, fmt.Errorf("unsupported external tool %q", request.Name)
	}
	return request, true, nil
}

func decodeValue[T any](raw any) (T, error) {
	if typed, ok := raw.(T); ok {
		return typed, nil
	}
	var value T
	b, err := json.Marshal(raw)
	if err != nil {
		return value, err
	}
	return value, json.Unmarshal(b, &value)
}

func externalSearchTool() tool.Tool {
	return function.NewFunctionTool(
		externalSearchNotImplemented,
		function.WithName(externalToolName),
		function.WithDescription("Searches a caller-owned external knowledge source."),
	)
}

func externalSearchNotImplemented(context.Context, externalSearchArgs) (externalSearchResult, error) {
	return externalSearchResult{}, errors.New("external_search is executed by the caller")
}

type externalSearchArgs struct {
	Query string `json:"query" description:"The search query."`
}

type externalSearchResult struct {
	Result string `json:"result" description:"The tool result content."`
}
