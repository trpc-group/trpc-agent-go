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
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// InstructionRequestProcessor implements instruction processing logic.
type InstructionRequestProcessor struct {
	// Instruction is the instruction to add to requests.
	Instruction string
	// SystemPrompt is the system prompt to add to requests.
	SystemPrompt string
	// OutputSchema is the JSON schema for output validation.
	// When provided, JSON output instructions are automatically injected.
	OutputSchema map[string]interface{}
}

// InstructionRequestProcessorOption is a function that can be used to configure the instruction request processor.
type InstructionRequestProcessorOption func(*InstructionRequestProcessor)

// WithOutputSchema adds the output schema to the instruction request processor.
func WithOutputSchema(outputSchema map[string]interface{}) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.OutputSchema = outputSchema
	}
}

// NewInstructionRequestProcessor creates a new instruction request processor.
func NewInstructionRequestProcessor(
	instruction, systemPrompt string,
	opts ...InstructionRequestProcessorOption,
) *InstructionRequestProcessor {
	p := &InstructionRequestProcessor{
		Instruction:  instruction,
		SystemPrompt: systemPrompt,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds instruction content and system prompt to the request if provided.
// State variables in instructions are automatically replaced with values from session state.
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

	// Process instruction and system prompt with state injection.
	processedInstruction := p.Instruction
	processedSystemPrompt := p.SystemPrompt

	// Automatically inject JSON output instructions if output schema is provided.
	if p.OutputSchema != nil {
		jsonInstructions := p.generateJSONInstructions()
		if processedInstruction != "" {
			processedInstruction += "\n\n" + jsonInstructions
		} else {
			processedInstruction = jsonInstructions
		}
	}

	if invocation != nil {
		var err error
		if processedInstruction != "" {
			processedInstruction, err = state.InjectSessionState(processedInstruction, invocation)
			if err != nil {
				log.Errorf("Failed to inject session state into instruction: %v", err)
			}
		}
		if processedSystemPrompt != "" {
			processedSystemPrompt, err = state.InjectSessionState(processedSystemPrompt, invocation)
			if err != nil {
				log.Errorf("Failed to inject session state into system prompt: %v", err)
			}
		}
	}

	// Find existing system message or create new one
	systemMsgIndex := findSystemMessageIndex(req.Messages)

	if systemMsgIndex >= 0 {
		// There's already a system message, check if it contains instruction
		if processedInstruction != "" && !containsInstruction(req.Messages[systemMsgIndex].Content, processedInstruction) {
			// Append instruction to existing system message
			req.Messages[systemMsgIndex].Content += "\n\n" + processedInstruction
			log.Debugf("Instruction request processor: appended instruction to existing system message")
		}
		// Also check if SystemPrompt needs to be added
		if processedSystemPrompt != "" && !containsInstruction(req.Messages[systemMsgIndex].Content, processedSystemPrompt) {
			// Prepend SystemPrompt to existing system message
			req.Messages[systemMsgIndex].Content = processedSystemPrompt + "\n\n" + req.Messages[systemMsgIndex].Content
			log.Debugf("Instruction request processor: prepended system prompt to existing system message")
		}
	} else {
		// No existing system message, create a combined one if needed
		var systemContent string
		if processedSystemPrompt != "" {
			systemContent = processedSystemPrompt
		}
		if processedInstruction != "" {
			if systemContent != "" {
				systemContent += "\n\n" + processedInstruction
			} else {
				systemContent = processedInstruction
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

// generateJSONInstructions generates JSON output instructions based on the output schema.
func (p *InstructionRequestProcessor) generateJSONInstructions() string {
	if p.OutputSchema == nil {
		return ""
	}

	// Convert schema to a readable format for the instruction
	schemaStr := p.formatSchemaForInstruction(p.OutputSchema)

	return fmt.Sprintf("IMPORTANT: You must respond with valid JSON in the following format:\n%s\n\n"+
		"Your response must be valid JSON that matches this schema exactly. "+
		"Do not include ```json or ``` in the beginning or end of the response.", schemaStr)
}

// formatSchemaForInstruction formats the schema for inclusion in instructions.
func (p *InstructionRequestProcessor) formatSchemaForInstruction(schema map[string]interface{}) string {
	// For now, we'll create a simple JSON representation.
	// In a more sophisticated implementation, we could parse the schema more intelligently.
	jsonBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		// Fallback to a simple string representation.
		return fmt.Sprintf("%v", schema)
	}
	return string(jsonBytes)
}
