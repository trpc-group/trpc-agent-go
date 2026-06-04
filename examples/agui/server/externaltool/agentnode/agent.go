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
	graphcheckpoint "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	researchAgentName  = "research_agent"
	finalAgentName     = "final_agent"
	nodeExternalGate   = "external_tool_gate"
	externalToolName   = "external_search"
	keyToolRequest     = "external_tool_request"
	keyFinalAgentInput = "final_agent_input"
)

type toolRequest struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Args       string `json:"args"`
}

func newGraphAgent(m model.Model, cfg model.GenerationConfig) (agent.Agent, error) {
	research := newLLMAgent(researchAgentName, `You are the research child agent.
For every user request, call external_search exactly once with {"query":"<concise query>"}.
Do not answer directly. The parent graph will pause and let the AG-UI caller execute the tool.`, m, cfg)
	final := newLLMAgent(finalAgentName, `You are the final answer child agent.
Use only the external tool result in the input.
Mention that the graph resumed after caller-side external tool execution.`, m, cfg)
	g, err := buildGraph([]tool.Tool{externalSearchTool()})
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		agentName,
		g,
		graphagent.WithDescription("AG-UI GraphAgent demo for AgentNode external tool interrupt and resume."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{research, final}),
		graphagent.WithCheckpointSaver(graphcheckpoint.NewSaver()),
	)
}

func newLLMAgent(name string, instruction string, m model.Model, cfg model.GenerationConfig) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(cfg),
	)
}

func buildGraph(externalTools []tool.Tool) (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(
		researchAgentName,
		graph.WithAgentNodeRunOptions(agent.WithExternalTools(externalTools)),
		graph.WithSubgraphOutputMapper(storeResearchResult),
	)
	sg.AddNode(nodeExternalGate, interruptForExternalTool)
	sg.AddAgentNode(
		finalAgentName,
		graph.WithUserInputKey(keyFinalAgentInput),
	)
	sg.SetEntryPoint(researchAgentName)
	sg.AddEdge(researchAgentName, nodeExternalGate)
	sg.AddConditionalEdges(nodeExternalGate, routeAfterGate, map[string]string{
		finalAgentName: finalAgentName,
		graph.End:      graph.End,
	})
	sg.SetFinishPoint(finalAgentName)
	return sg.Compile()
}

func storeResearchResult(_ graph.State, result graph.SubgraphResult) graph.State {
	for _, call := range result.ToolCalls {
		if call.ID == "" || call.Function.Name != externalToolName {
			continue
		}
		return graph.State{
			keyToolRequest: toolRequest{
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Args:       string(call.Function.Arguments),
			},
		}
	}
	return nil
}

func interruptForExternalTool(ctx context.Context, state graph.State) (any, error) {
	raw, ok := state[keyToolRequest]
	if !ok {
		return nil, nil
	}
	request, err := decodeValue[toolRequest](raw)
	if err != nil {
		return nil, err
	}
	if request.ToolCallID == "" {
		return nil, errors.New("external tool call missing id")
	}
	if request.Name != externalToolName {
		return nil, fmt.Errorf("unsupported external tool %q", request.Name)
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
	userInput, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
	return graph.State{keyFinalAgentInput: finalInput(userInput, request, result)}, nil
}

func routeAfterGate(_ context.Context, state graph.State) (string, error) {
	input, ok := graph.GetStateValue[string](state, keyFinalAgentInput)
	if ok && strings.TrimSpace(input) != "" {
		return finalAgentName, nil
	}
	return graph.End, nil
}

func finalInput(userInput string, request toolRequest, result string) string {
	return strings.Join([]string{
		"Original user request:",
		userInput,
		"",
		fmt.Sprintf("External tool call: %s id=%s args=%s", request.Name, request.ToolCallID, request.Args),
		"",
		"External tool result:",
		result,
		"",
		"Answer using only the external tool result.",
	}, "\n")
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
	return externalSearchResult{}, errors.New("external_search is executed by the AG-UI caller")
}

type externalSearchArgs struct {
	Query string `json:"query" description:"The search query."`
}

type externalSearchResult struct {
	Result string `json:"result" description:"The tool result content."`
}
