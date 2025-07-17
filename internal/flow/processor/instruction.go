//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package processor

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// InstructionRequestProcessor implements instruction processing logic.
type InstructionRequestProcessor struct {
	// Instruction is the instruction to add to requests.
	Instruction string
	// SystemPrompt is the system prompt to add to requests.
	SystemPrompt string
}

// NewInstructionRequestProcessor creates a new instruction request processor.
func NewInstructionRequestProcessor(instruction, systemPrompt string) *InstructionRequestProcessor {
	return &InstructionRequestProcessor{
		Instruction:  instruction,
		SystemPrompt: systemPrompt,
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds instruction content and system prompt to the request if provided.
func (p *InstructionRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Instruction request processor: request is nil")
		return
	}

	agentName := ""
	if invocation != nil {
		agentName = invocation.AgentName
	}
	log.Debugf("Instruction request processor: processing request for agent %s", agentName)

	// Initialize messages slice if nil.
	if req.Messages == nil {
		req.Messages = make([]model.Message, 0)
	}

	// Find existing system message or create new one
	systemMsgIndex := findSystemMessageIndex(req.Messages)

	if systemMsgIndex >= 0 {
		// There's already a system message, check if it contains instruction
		if p.Instruction != "" && !containsInstruction(req.Messages[systemMsgIndex].Content, p.Instruction) {
			// Append instruction to existing system message
			req.Messages[systemMsgIndex].Content += "\n\n" + p.Instruction
			log.Debugf("Instruction request processor: appended instruction to existing system message")
		}
		// Also check if SystemPrompt needs to be added
		if p.SystemPrompt != "" && !containsInstruction(req.Messages[systemMsgIndex].Content, p.SystemPrompt) {
			// Prepend SystemPrompt to existing system message
			req.Messages[systemMsgIndex].Content = p.SystemPrompt + "\n\n" + req.Messages[systemMsgIndex].Content
			log.Debugf("Instruction request processor: prepended system prompt to existing system message")
		}
	} else {
		// No existing system message, create a combined one if needed
		var systemContent string
		if p.SystemPrompt != "" {
			systemContent = p.SystemPrompt
		}
		if p.Instruction != "" {
			if systemContent != "" {
				systemContent += "\n\n" + p.Instruction
			} else {
				systemContent = p.Instruction
			}
		}
		if systemContent != "" {
			systemMsg := model.NewSystemMessage(systemContent)
			req.Messages = append([]model.Message{systemMsg}, req.Messages...)
			log.Debugf("Instruction request processor: added combined system message")
		}
	}

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = model.ObjectTypePreprocessingInstruction

		select {
		case ch <- evt:
			log.Debugf("Instruction request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Instruction request processor: context cancelled")
		}
	}
}

// findSystemMessageIndex finds the index of the first system message in the messages slice.
// Returns -1 if no system message is found.
func findSystemMessageIndex(messages []model.Message) int {
	for i, msg := range messages {
		if msg.Role == model.RoleSystem {
			return i
		}
	}
	return -1
}

// containsInstruction checks if the given content already contains the instruction.
func containsInstruction(content, instruction string) bool {
	// strings.Contains handles both exact match and substring cases
	return strings.Contains(content, instruction)
}
