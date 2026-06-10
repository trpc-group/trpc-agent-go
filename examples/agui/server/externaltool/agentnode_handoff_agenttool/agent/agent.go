//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent builds the GraphAgent used by the AgentNode handoff AgentTool example.
package agent

import (
	"strings"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// AgentName is the name exposed by the example GraphAgent.
const AgentName = "agui-agentnode-handoff-agenttool"

const (
	plannerAgentName     = "handoff_planner"
	handoffToolName      = "handoff_task"
	handoffAgentID       = "research"
	resolveHandoffNode   = "resolve_handoff"
	executeAgentToolNode = "execute_agenttool"
	returnHandoffNode    = "return_handoff_result"
	innerGraphAgentName  = "dynamic_research_graph"
	innerWorkerName      = "research_worker"
	innerExternalTool    = "inner_external_search"
	innerExternalGate    = "inner_external_gate"
)

const (
	stateKeyHandoffRequest     = "handoff_request"
	stateKeyHandoffToolMessage = "handoff_tool_message"
	stateKeyInnerToolCall      = "inner_tool_call"
	stateKeyInnerToolMessage   = "inner_tool_message"
)

type handoffRequest struct {
	ToolCallID string `json:"toolCallId,omitempty" description:"The outer handoff tool call id."`
	Name       string `json:"name,omitempty" description:"The outer handoff tool name."`
	AgentID    string `json:"agent_id" description:"The target agent id."`
	Task       string `json:"task" description:"The task to hand off."`
}

type innerToolRequest struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Args       string `json:"args"`
}

// NewGraphAgent creates the example GraphAgent and its nested AgentTool child graph.
func NewGraphAgent(saver graph.CheckpointSaver, m model.Model, cfg model.GenerationConfig) (coreagent.Agent, error) {
	innerAgent, err := newInnerGraphAgent(saver, m, cfg)
	if err != nil {
		return nil, err
	}
	handoffTool := newHandoffTool()
	agentTools := map[string]tool.Tool{
		innerGraphAgentName: agenttool.NewTool(innerAgent),
	}
	planner := newPlannerAgent(m, cfg)
	g, err := buildOuterGraph(planner, handoffTool, agentTools)
	if err != nil {
		return nil, err
	}
	return graphagent.New(
		AgentName,
		g,
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(saver),
		graphagent.WithSubAgents([]coreagent.Agent{planner}),
	)
}

func newPlannerAgent(m model.Model, cfg model.GenerationConfig) coreagent.Agent {
	return llmagent.New(
		plannerAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(strings.Join([]string{
			"You are a handoff planner.",
			"For every user request, call handoff_task exactly once.",
			"Use agent_id \"research\" and put the user's request into task.",
			"When you receive the handoff_task tool result, answer directly and do not call handoff_task again.",
			"Your final answer after the tool result must summarize the tool result and preserve the exact tokens research, dynamic_research_graph, and inner_external_search when they appear in that result.",
		}, "\n")),
		llmagent.WithGenerationConfig(cfg),
	)
}
