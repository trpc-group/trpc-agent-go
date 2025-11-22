//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// AgentRegistry is a registry for managing sub-agents.
// It allows registering agents by name and looking them up later.
//
// This is used in multi-agent workflows where one agent can delegate
// to another agent as a sub-node in the graph.
//
// Example usage:
//
//	agentRegistry := registry.NewAgentRegistry()
//	agentRegistry.Register("technical_support", techSupportAgent)
//	agentRegistry.Register("sales_assistant", salesAgent)
//
//	// Later, in DSL:
//	{
//	  "id": "support_node",
//	  "component": {"type": "component", "ref": "builtin.agent"},
//	  "config": {"agent_name": "technical_support"}
//	}
type AgentRegistry struct {
	agents map[string]agent.Agent
}

// NewAgentRegistry creates a new AgentRegistry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]agent.Agent),
	}
}

// Register registers an agent with the given name.
// If an agent with the same name already exists, it will be overwritten.
func (r *AgentRegistry) Register(name string, agent agent.Agent) {
	r.agents[name] = agent
}

// Get retrieves an agent by name.
// Returns the agent and true if found, nil and false otherwise.
func (r *AgentRegistry) Get(name string) (agent.Agent, bool) {
	agent, ok := r.agents[name]
	return agent, ok
}

// List returns all registered agent names.
func (r *AgentRegistry) List() []string {
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}

// GetAll returns all registered agents as a slice.
// This is useful for passing to GraphAgent.WithSubAgents().
func (r *AgentRegistry) GetAll() []agent.Agent {
	agents := make([]agent.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// Has checks if an agent with the given name is registered.
func (r *AgentRegistry) Has(name string) bool {
	_, ok := r.agents[name]
	return ok
}

// Unregister removes an agent from the registry.
// Returns true if the agent was found and removed, false otherwise.
func (r *AgentRegistry) Unregister(name string) bool {
	if _, ok := r.agents[name]; ok {
		delete(r.agents, name)
		return true
	}
	return false
}

// Clear removes all agents from the registry.
func (r *AgentRegistry) Clear() {
	r.agents = make(map[string]agent.Agent)
}

// Count returns the number of registered agents.
func (r *AgentRegistry) Count() int {
	return len(r.agents)
}

