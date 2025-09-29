//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package transfer provides transfer_to_agent tool implementation.
package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// TransferToolName is the name of the transfer_to_agent tool.
	TransferToolName = "transfer_to_agent"
	// FieldAgentName is the name of the agent_name field.
	FieldAgentName = "agent_name"
	// FieldMessage is the name of the message field.
	FieldMessage = "message"
)

// Request represents the request structure for transfer_to_agent tool.
type Request struct {
	// AgentName is the name of the target agent to transfer to.
	AgentName string `json:"agent_name" jsonschema:"description=Name of the agent to transfer control to"`
	// Message is the message to send to the target agent (optional).
	Message string `json:"message,omitempty" jsonschema:"description=Optional message to pass to the target agent"`
}

// Response represents the response from transfer_to_agent tool.
type Response struct {
	// Success indicates if the transfer was successful.
	Success bool `json:"success"`
	// Message provides details about the transfer.
	Message string `json:"message"`
	// TargetAgent is the name of the agent control was transferred to.
	TargetAgent string `json:"target_agent,omitempty"`
	// TransferType indicates the type of transfer performed.
	TransferType string `json:"transfer_type"`
}

// Tool implements the transfer_to_agent functionality.
type Tool struct {
	availableAgents []agent.Info
}

// New creates a new transfer_to_agent tool with the provided agent information.
func New(agents []agent.Info) *Tool {
	return &Tool{
		availableAgents: agents,
	}
}

// findAgentInfo finds agent information by name.
// Returns nil if no agent with the given name is found.
func (t *Tool) findAgentInfo(name string) *agent.Info {
	for _, agentInfo := range t.availableAgents {
		if agentInfo.Name == name {
			return &agentInfo
		}
	}
	return nil
}

// Declaration implements the tool.Tool interface.
func (t *Tool) Declaration() *tool.Declaration {
	// Build detailed agent descriptions.
	var agentDescriptions []string
	agentNames := make([]string, len(t.availableAgents))

	for i, agentInfo := range t.availableAgents {
		agentNames[i] = agentInfo.Name
		agentDescriptions = append(agentDescriptions,
			fmt.Sprintf("- %s: %s", agentInfo.Name, agentInfo.Description))
	}

	agentDetailsText := strings.Join(agentDescriptions, "\n")

	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			FieldAgentName: {
				Type: "string",
				Description: fmt.Sprintf(
					"Name of the agent to transfer control to.\n\nAvailable agents:\n%s\n\nValid agent names: %v",
					agentDetailsText, agentNames),
			},
			FieldMessage: {
				Type:        "string",
				Description: "Optional message to pass to the target agent",
			},
		},
		Required: []string{FieldAgentName},
	}

	return &tool.Declaration{
		Name:        TransferToolName,
		Description: "Transfer control to another agent. This will hand over the conversation to the specified agent.",
		InputSchema: schema,
	}
}

// Call implements the tool.CallableTool interface.
func (t *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var req Request
	if err := json.Unmarshal(jsonArgs, &req); err != nil {
		return Response{
			Success:      false,
			Message:      fmt.Sprintf("Invalid request format: %v", err),
			TransferType: "error",
		}, nil
	}

	// Find the target agent information.
	targetAgentInfo := t.findAgentInfo(req.AgentName)
	if targetAgentInfo == nil {
		availableAgents := make([]string, len(t.availableAgents))
		for i, agentInfo := range t.availableAgents {
			availableAgents[i] = agentInfo.Name
		}
		return Response{
			Success:      false,
			Message:      fmt.Sprintf("Agent '%s' not found. Available agents: %v", req.AgentName, availableAgents),
			TransferType: "error",
		}, nil
	}

	// Get invocation from context.
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return Response{
			Success:      false,
			Message:      "Transfer failed: no invocation context available",
			TransferType: "error",
		}, nil
	}

	// Set transfer information in the invocation with just the agent name.
	invocation.TransferInfo = &agent.TransferInfo{
		TargetAgentName: targetAgentInfo.Name,
		Message:         req.Message,
	}

	return Response{
		Success:      true,
		Message:      fmt.Sprintf("Transfer initiated to agent '%s'", req.AgentName),
		TargetAgent:  req.AgentName,
		TransferType: "agent_handoff",
	}, nil
}
