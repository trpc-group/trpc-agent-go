//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import "trpc.group/trpc-go/trpc-agent-go/tool"

// BaseSubAgentHolder provides common implementations for agents that manage sub-agents.
// This reduces code duplication across ChainAgent, ParallelAgent, and CycleAgent.
type BaseSubAgentHolder struct {
	subAgents []Agent
}

// NewBaseSubAgentHolder creates a new BaseSubAgentHolder with the given sub-agents.
func NewBaseSubAgentHolder(subAgents []Agent) *BaseSubAgentHolder {
	return &BaseSubAgentHolder{subAgents: subAgents}
}

// SubAgents returns the list of sub-agents available to this agent.
func (b *BaseSubAgentHolder) SubAgents() []Agent {
	return b.subAgents
}

// FindSubAgent finds a sub-agent by name and returns nil if not found.
func (b *BaseSubAgentHolder) FindSubAgent(name string) Agent {
	for _, subAgent := range b.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// Tools returns an empty tool list (sub-agent holders typically don't have their own tools).
func (b *BaseSubAgentHolder) Tools() []tool.Tool {
	return []tool.Tool{}
}
