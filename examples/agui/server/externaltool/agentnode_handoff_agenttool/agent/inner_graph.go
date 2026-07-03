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
	"errors"
	"fmt"
	"strings"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newInnerGraphAgent(saver graph.CheckpointSaver, m model.Model, cfg model.GenerationConfig) (coreagent.Agent, error) {
	worker := newBusinessWrappedAgent(m, cfg)
	g, err := buildInnerGraph(worker)
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		innerGraphAgentName,
		g,
		graphagent.WithDescription("Dynamic research GraphAgent selected by handoff_task."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(saver),
		graphagent.WithSubAgents([]coreagent.Agent{worker}),
	)
}

func newBusinessWrappedAgent(m model.Model, cfg model.GenerationConfig) coreagent.Agent {
	inner := llmagent.New(
		innerWorkerName+"-llm",
		llmagent.WithModel(m),
		llmagent.WithInstruction(strings.Join([]string{
			"You are a research worker.",
			"For version comparison tasks, call inner_external_search as needed.",
			"Search for the old version and the new version separately.",
			"When the collected results are sufficient, answer directly with the old version and the new version.",
		}, "\n")),
		llmagent.WithGenerationConfig(cfg),
	)
	return &businessAgent{name: innerWorkerName, inner: inner}
}

func buildInnerGraph(worker coreagent.Agent) (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(
		worker.Info().Name,
		graph.WithAgentNodeRunOptions(coreagent.WithExternalTools([]tool.Tool{innerExternalSearchTool()})),
		graph.WithAgentNodeInputMapper(mapInnerToolMessage),
		graph.WithSubgraphOutputMapper(storeInnerToolRequest),
	)
	sg.AddNode(innerExternalGate, interruptForInnerExternalTool)
	sg.SetEntryPoint(worker.Info().Name)
	sg.AddEdge(worker.Info().Name, innerExternalGate)
	sg.AddConditionalEdges(innerExternalGate, routeAfterInnerGate, map[string]string{
		worker.Info().Name: worker.Info().Name,
		graph.End:          graph.End,
	})
	sg.SetFinishPoint(worker.Info().Name)
	return sg.Compile()
}

func storeInnerToolRequest(_ graph.State, result graph.SubgraphResult) graph.State {
	for _, call := range result.ToolCalls {
		if call.ID == "" || call.Function.Name != innerExternalTool {
			continue
		}
		return graph.State{
			stateKeyInnerToolCall: innerToolRequest{
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Args:       string(call.Function.Arguments),
			},
			stateKeyInnerToolMessage: nil,
			graph.StateKeyMessages:   appendInnerAssistantMessage(result.LastMessage),
		}
	}
	if strings.TrimSpace(result.LastResponse) != "" {
		return graph.State{
			stateKeyInnerToolCall:      nil,
			stateKeyInnerToolMessage:   nil,
			graph.StateKeyLastResponse: result.LastResponse,
		}
	}
	return graph.State{stateKeyInnerToolCall: nil}
}

func mapInnerToolMessage(state graph.State) graph.State {
	message, ok := innerToolMessageFromState(state)
	if !ok {
		return nil
	}
	return graph.State{graph.StateKeyAgentInputMessage: &message}
}

func innerToolMessageFromState(state graph.State) (model.Message, bool) {
	raw, ok := state[stateKeyInnerToolMessage]
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

func interruptForInnerExternalTool(ctx context.Context, state graph.State) (any, error) {
	req, ok, err := innerToolRequestFromState(state)
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	resumeValue, err := graph.Interrupt(ctx, state, req.ToolCallID, req)
	if err != nil {
		return nil, err
	}
	result, ok := resumeValue.(string)
	if !ok {
		return nil, fmt.Errorf("resume value for tool call %s must be a string", req.ToolCallID)
	}
	if strings.TrimSpace(result) == "" {
		return nil, fmt.Errorf("resume value for tool call %s cannot be empty", req.ToolCallID)
	}
	toolMessage := model.NewToolMessage(req.ToolCallID, req.Name, result)
	return graph.State{
		stateKeyInnerToolCall:    nil,
		stateKeyInnerToolMessage: &toolMessage,
		graph.StateKeyMessages: graph.AppendMessages{
			Items: []model.Message{toolMessage},
		},
	}, nil
}

func appendInnerAssistantMessage(message *model.Message) graph.AppendMessages {
	if message == nil || message.Role != model.RoleAssistant || len(message.ToolCalls) == 0 {
		return graph.AppendMessages{}
	}
	return graph.AppendMessages{Items: []model.Message{*message}}
}

func routeAfterInnerGate(_ context.Context, state graph.State) (string, error) {
	if state[stateKeyInnerToolMessage] != nil {
		return innerWorkerName, nil
	}
	return graph.End, nil
}

func innerToolRequestFromState(state graph.State) (innerToolRequest, bool, error) {
	raw, ok := state[stateKeyInnerToolCall]
	if !ok || raw == nil {
		return innerToolRequest{}, false, nil
	}
	req, err := decodeValue[innerToolRequest](raw)
	if err != nil {
		return innerToolRequest{}, false, err
	}
	if req.ToolCallID == "" {
		return innerToolRequest{}, false, errors.New("inner external tool call missing id")
	}
	if req.Name != innerExternalTool {
		return innerToolRequest{}, false, fmt.Errorf("unexpected inner external tool %q", req.Name)
	}
	return req, true, nil
}

type businessAgent struct {
	name  string
	inner coreagent.Agent
}

func (a *businessAgent) Run(ctx context.Context, inv *coreagent.Invocation) (<-chan *event.Event, error) {
	return a.inner.Run(ctx, inv)
}

func (a *businessAgent) Tools() []tool.Tool {
	return a.inner.Tools()
}

func (a *businessAgent) Info() coreagent.Info {
	info := a.inner.Info()
	info.Name = a.name
	info.Description = "Business wrapper around an LLMAgent."
	return info
}

func (a *businessAgent) SubAgents() []coreagent.Agent {
	return a.inner.SubAgents()
}

func (a *businessAgent) FindSubAgent(name string) coreagent.Agent {
	return a.inner.FindSubAgent(name)
}

func innerExternalSearchTool() tool.Tool {
	return function.NewFunctionTool(
		func(context.Context, innerExternalSearchArgs) (innerExternalSearchResult, error) {
			return innerExternalSearchResult{}, errors.New("inner_external_search is caller-executed")
		},
		function.WithName(innerExternalTool),
		function.WithDescription("Searches a caller-owned source for the delegated agent."),
	)
}

type innerExternalSearchArgs struct {
	Query string `json:"query" description:"The delegated search query."`
}

type innerExternalSearchResult struct {
	Result string `json:"result" description:"The delegated search result."`
}
