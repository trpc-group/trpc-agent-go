//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// subAgentCall defines the input format for direct sub-agent tool calls.
// This handles cases where models call sub-agent names directly instead of using transfer_to_agent.
type subAgentCall struct {
	Message string `json:"message,omitempty"`
}

// findCompatibleTool attempts to map a requested (missing) tool name to a compatible tool.
// For models that directly call sub-agent names, map to transfer_to_agent when available.
func findCompatibleTool(requested string, tools map[string]tool.Tool, invocation *agent.Invocation) tool.Tool {
	transfer, ok := tools[transfer.TransferToolName]
	if !ok || invocation == nil || invocation.Agent == nil {
		return nil
	}
	for _, a := range invocation.Agent.SubAgents() {
		if a.Info().Name == requested {
			return transfer
		}
	}
	return nil
}

// convertToolArguments converts original args to the mapped tool args when needed.
// When mapping sub-agent name -> transfer_to_agent, wrap message and set agent_name.
func convertToolArguments(originalName string, originalArgs []byte, targetName string) []byte {
	if targetName != transfer.TransferToolName {
		return nil
	}

	var input subAgentCall
	if len(originalArgs) > 0 {
		if err := json.Unmarshal(originalArgs, &input); err != nil {
			log.Warnf("Failed to unmarshal sub-agent call arguments for %s: %v", originalName, err)
			return nil
		}
	}

	message := input.Message
	if message == "" {
		message = "Task delegated from coordinator"
	}

	req := &transfer.Request{
		AgentName:     originalName,
		Message:       message,
		EndInvocation: false,
	}

	b, err := json.Marshal(req)
	if err != nil {
		log.Warnf("Failed to marshal transfer request for %s: %v", originalName, err)
		return nil
	}
	return b
}
