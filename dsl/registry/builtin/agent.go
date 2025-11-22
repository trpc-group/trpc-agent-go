//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Auto-register Agent component at package init time
	registry.MustRegister(&AgentComponent{})
}

// AgentComponent is a builtin component that wraps graph.NewAgentNodeFunc.
// It allows using a sub-agent as a node in the workflow.
//
// The agent is looked up by name from the parent GraphAgent's sub-agent list.
// This enables multi-agent composition where one agent can delegate to another.
//
// Configuration:
//   - agent_name: string (required) - Name of the sub-agent to invoke
//   - isolate_messages: bool (optional) - Whether to isolate subgraph from parent session messages
//   - event_scope: string (optional) - Event scope segment for subgraph events
//
// Example DSL:
//
//	{
//	  "id": "assistant",
//	  "component": {
//	    "type": "component",
//	    "ref": "builtin.agent"
//	  },
//	  "config": {
//	    "agent_name": "technical_support_agent"
//	  }
//	}
type AgentComponent struct{}

// Metadata returns the component metadata.
func (c *AgentComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.agent",
		DisplayName: "Agent Node",
		Description: "Invokes a sub-agent by name, enabling multi-agent composition",
		Category:    "Agent",
		Version:     "1.0.0",
		// Agent node does not introduce additional named state inputs; it reads
		// from the built-in graph state (messages/session). All parameters are
		// provided via config.
		Inputs: []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyLastResponse,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Last response from the sub-agent",
			},
			{
				Name:        graph.StateKeyNodeResponses,
				Type:        "map[string]any",
				GoType:      reflect.TypeOf(map[string]any{}),
				Description: "Node responses from the sub-agent execution",
				Reducer:     "merge",
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "agent_name",
				DisplayName: "Agent Name",
				Description: "Name of the sub-agent to invoke (must be registered in parent GraphAgent)",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "technical_support_agent",
			},
			{
				Name:        "isolate_messages",
				DisplayName: "Isolate Messages",
				Description: "Whether to isolate subgraph from parent session messages",
				Type:        "bool",
				TypeID:      "boolean",
				Kind:        "boolean",
				GoType:      reflect.TypeOf(false),
				Required:    false,
				Default:     false,
			},
			{
				Name:        "event_scope",
				DisplayName: "Event Scope",
				Description: "Event scope segment for subgraph events (empty disables scoping)",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Placeholder: "support_flow",
			},
		},
	}
}

// Execute runs the agent node.
// It creates a NodeFunc using graph.NewAgentNodeFunc and executes it.
func (c *AgentComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Extract agent_name from config
	agentName, ok := config["agent_name"].(string)
	if !ok || agentName == "" {
		return nil, fmt.Errorf("agent_name is required and must be a non-empty string")
	}

	// Build options for the agent node
	var opts []graph.Option

	// Handle isolate_messages option
	if isolate, ok := config["isolate_messages"].(bool); ok && isolate {
		opts = append(opts, graph.WithSubgraphIsolatedMessages(true))
	}

	// Handle event_scope option
	if scope, ok := config["event_scope"].(string); ok && scope != "" {
		opts = append(opts, graph.WithSubgraphEventScope(scope))
	}

	// Handle input_mapper option (if provided as a function in config)
	// Note: This is advanced usage and typically not used in DSL
	if inputMapper, ok := config["input_mapper"].(func(graph.State) graph.State); ok {
		opts = append(opts, graph.WithSubgraphInputMapper(inputMapper))
	}

	// Handle output_mapper option (if provided as a function in config)
	// Note: This is advanced usage and typically not used in DSL
	if outputMapper, ok := config["output_mapper"].(func(graph.State, graph.SubgraphResult) graph.State); ok {
		opts = append(opts, graph.WithSubgraphOutputMapper(outputMapper))
	}

	// Create the agent node function
	agentNodeFunc := graph.NewAgentNodeFunc(agentName, opts...)

	// Execute the agent node function
	result, err := agentNodeFunc(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("agent node execution failed: %w", err)
	}

	return result, nil
}

// Validate validates the component configuration.
func (c *AgentComponent) Validate(config registry.ComponentConfig) error {
	// Validate agent_name
	agentName, ok := config["agent_name"].(string)
	if !ok {
		return fmt.Errorf("agent_name must be a string")
	}
	if agentName == "" {
		return fmt.Errorf("agent_name cannot be empty")
	}

	// Validate isolate_messages if present
	if isolate, ok := config["isolate_messages"]; ok {
		if _, ok := isolate.(bool); !ok {
			return fmt.Errorf("isolate_messages must be a boolean")
		}
	}

	// Validate event_scope if present
	if scope, ok := config["event_scope"]; ok {
		if _, ok := scope.(string); !ok {
			return fmt.Errorf("event_scope must be a string")
		}
	}

	return nil
}

// NewAgentComponent creates a new AgentComponent instance.
func NewAgentComponent() *AgentComponent {
	return &AgentComponent{}
}
