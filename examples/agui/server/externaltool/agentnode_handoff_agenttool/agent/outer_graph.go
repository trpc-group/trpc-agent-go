//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func buildOuterGraph(
	planner coreagent.Agent,
	handoffTool tool.Tool,
	agentTools map[string]tool.Tool,
) (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(
		planner.Info().Name,
		graph.WithAgentNodeRunOptions(coreagent.WithExternalTools([]tool.Tool{handoffTool})),
		graph.WithAgentNodeInputMapper(mapHandoffToolMessage),
		graph.WithSubgraphOutputMapper(storeHandoffRequest),
	)
	sg.AddNode(resolveHandoffNode, resolveHandoffToAgentToolCall)
	sg.AddToolsNode(executeAgentToolNode, agentTools)
	sg.AddNode(returnHandoffNode, returnAgentToolResultToPlanner)
	sg.SetEntryPoint(planner.Info().Name)
	sg.AddConditionalEdges(planner.Info().Name, routeAfterPlanner, map[string]string{
		resolveHandoffNode: resolveHandoffNode,
		graph.End:          graph.End,
	})
	sg.AddEdge(resolveHandoffNode, executeAgentToolNode)
	sg.AddEdge(executeAgentToolNode, returnHandoffNode)
	sg.AddEdge(returnHandoffNode, planner.Info().Name)
	sg.SetFinishPoint(planner.Info().Name)
	return sg.Compile()
}

func storeHandoffRequest(_ graph.State, result graph.SubgraphResult) graph.State {
	for _, call := range result.ToolCalls {
		if call.ID == "" || call.Function.Name != handoffToolName {
			continue
		}
		var req handoffRequest
		if err := json.Unmarshal(call.Function.Arguments, &req); err != nil {
			return nil
		}
		req.ToolCallID = call.ID
		req.Name = call.Function.Name
		return graph.State{
			stateKeyHandoffRequest:     req,
			stateKeyHandoffToolMessage: nil,
		}
	}
	return graph.State{
		stateKeyHandoffRequest:     nil,
		stateKeyHandoffToolMessage: nil,
	}
}

func mapHandoffToolMessage(state graph.State) graph.State {
	message, ok := handoffToolMessageFromState(state)
	if !ok {
		return nil
	}
	return graph.State{graph.StateKeyAgentInputMessage: &message}
}

func handoffToolMessageFromState(state graph.State) (model.Message, bool) {
	raw, ok := state[stateKeyHandoffToolMessage]
	if !ok || raw == nil {
		return model.Message{}, false
	}
	switch msg := raw.(type) {
	case model.Message:
		return msg, msg.Role == model.RoleTool
	case *model.Message:
		if msg == nil {
			return model.Message{}, false
		}
		return *msg, msg.Role == model.RoleTool
	default:
		return model.Message{}, false
	}
}

func routeAfterPlanner(_ context.Context, state graph.State) (string, error) {
	if _, ok, err := handoffRequestFromState(state); err != nil || ok {
		if err != nil {
			return "", err
		}
		return resolveHandoffNode, nil
	}
	return graph.End, nil
}

func resolveHandoffToAgentToolCall(_ context.Context, state graph.State) (any, error) {
	req, ok, err := handoffRequestFromState(state)
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("missing handoff_task request")
	}
	args, err := json.Marshal(map[string]string{"request": req.Task})
	if err != nil {
		return nil, fmt.Errorf("marshal AgentTool args: %w", err)
	}
	callID := req.ToolCallID + ":agenttool"
	message := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   callID,
			Function: model.FunctionDefinitionParam{
				Name:      agentToolNameForHandoff(req.AgentID),
				Arguments: args,
			},
		}},
	}
	return graph.State{
		graph.StateKeyMessages: graph.AppendMessages{Items: []model.Message{message}},
	}, nil
}

func returnAgentToolResultToPlanner(_ context.Context, state graph.State) (any, error) {
	req, ok, err := handoffRequestFromState(state)
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("missing handoff_task request")
	}
	if strings.TrimSpace(req.ToolCallID) == "" {
		return nil, errors.New("handoff_task tool call id is empty")
	}
	result, _ := graph.GetStateValue[string](state, graph.StateKeyLastToolResponse)
	if strings.TrimSpace(result) == "" {
		return nil, errors.New("AgentTool result is empty")
	}
	toolMessage := model.NewToolMessage(req.ToolCallID, handoffToolName, result)
	return graph.State{
		stateKeyHandoffRequest:         nil,
		stateKeyHandoffToolMessage:     &toolMessage,
		graph.StateKeyLastToolResponse: result,
	}, nil
}

func agentToolNameForHandoff(agentID string) string {
	if agentID == handoffAgentID {
		return innerGraphAgentName
	}
	return agentID
}

func handoffRequestFromState(state graph.State) (handoffRequest, bool, error) {
	raw, ok := state[stateKeyHandoffRequest]
	if !ok || raw == nil {
		return handoffRequest{}, false, nil
	}
	req, err := decodeValue[handoffRequest](raw)
	if err != nil {
		return handoffRequest{}, false, err
	}
	if strings.TrimSpace(req.AgentID) == "" {
		return handoffRequest{}, false, errors.New("handoff_task agent_id is empty")
	}
	if strings.TrimSpace(req.Name) != "" && req.Name != handoffToolName {
		return handoffRequest{}, false, fmt.Errorf("unexpected handoff tool %q", req.Name)
	}
	if strings.TrimSpace(req.Task) == "" {
		return handoffRequest{}, false, errors.New("handoff_task task is empty")
	}
	return req, true, nil
}
