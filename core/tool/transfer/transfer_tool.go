// Package transfer provides transfer_to_agent tool implementation.
package transfer

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// Request represents the request structure for transfer_to_agent tool.
type Request struct {
	// AgentName is the name of the target agent to transfer to.
	AgentName string `json:"agent_name" jsonschema:"description=Name of the agent to transfer control to"`
	// Message is the message to send to the target agent (optional).
	Message string `json:"message,omitempty" jsonschema:"description=Optional message to pass to the target agent"`
	// EndInvocation indicates whether to end the current invocation after transfer.
	EndInvocation bool `json:"end_invocation,omitempty" jsonschema:"description=Whether to end current invocation after transfer (default: true)"`
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
	agent agent.Agent
}

// New creates a new transfer_to_agent tool.
func New(a agent.Agent) *Tool {
	return &Tool{
		agent: a,
	}
}

// Declaration implements the tool.Tool interface.
func (t *Tool) Declaration() *tool.Declaration {
	// Get available agent names from the agent.
	subAgents := t.agent.SubAgents()
	agentNames := make([]string, len(subAgents))
	for i, subAgent := range subAgents {
		agentNames[i] = subAgent.Name()
	}

	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"agent_name": {
				Type:        "string",
				Description: fmt.Sprintf("Name of the agent to transfer control to. Available agents: %v", agentNames),
			},
			"message": {
				Type:        "string",
				Description: "Optional message to pass to the target agent",
			},
			"end_invocation": {
				Type:        "boolean",
				Description: "Whether to end current invocation after transfer (default: true)",
			},
		},
		Required: []string{"agent_name"},
	}

	return &tool.Declaration{
		Name:        "transfer_to_agent",
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

	// Find the target agent.
	targetAgent := t.agent.FindSubAgent(req.AgentName)
	if targetAgent == nil {
		subAgents := t.agent.SubAgents()
		availableAgents := make([]string, len(subAgents))
		for i, subAgent := range subAgents {
			availableAgents[i] = subAgent.Name()
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

	// Set transfer information in the invocation.
	invocation.TransferInfo = &agent.TransferInfo{
		TargetAgent:   targetAgent,
		Message:       req.Message,
		EndInvocation: req.EndInvocation,
	}

	return Response{
		Success:      true,
		Message:      fmt.Sprintf("Transfer initiated to agent '%s'", req.AgentName),
		TargetAgent:  req.AgentName,
		TransferType: "agent_handoff",
	}, nil
}
