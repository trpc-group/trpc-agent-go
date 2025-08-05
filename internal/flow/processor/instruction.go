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
	processedInstruction, processedSystemPrompt := p.processInstructionsWithState(invocation)

	// Update the request messages with processed instructions.
	p.updateRequestMessages(req, processedInstruction, processedSystemPrompt)

	// Send a preprocessing event.
	p.sendPreprocessingEvent(ctx, invocation, ch)
}

// processInstructionsWithState processes instruction and system prompt with state injection.
func (p *InstructionRequestProcessor) processInstructionsWithState(invocation *agent.Invocation) (string, string) {
	processedInstruction := p.Instruction
	processedSystemPrompt := p.SystemPrompt

	// Automatically inject JSON output instructions if output schema is provided.
	if p.OutputSchema != nil {
		jsonInstructions := p.generateJSONInstructions()
		processedInstruction = p.combineInstructions(processedInstruction, jsonInstructions)
	}

	if invocation != nil {
		processedInstruction = p.injectStateIntoContent(invocation, processedInstruction, "instruction")
		processedSystemPrompt = p.injectStateIntoContent(invocation, processedSystemPrompt, "system prompt")
	}

	return processedInstruction, processedSystemPrompt
}

// combineInstructions combines existing instruction with new JSON instructions.
func (p *InstructionRequestProcessor) combineInstructions(existingInstruction, jsonInstructions string) string {
	if existingInstruction != "" {
		return existingInstruction + "\n\n" + jsonInstructions
	}
	return jsonInstructions
}

// injectStateIntoContent injects session state into the given content.
func (p *InstructionRequestProcessor) injectStateIntoContent(
	invocation *agent.Invocation,
	content, contentType string,
) string {
	if content == "" {
		return content
	}

	processedContent, err := state.InjectSessionState(content, invocation)
	if err != nil {
		log.Errorf("Failed to inject session state into %s: %v", contentType, err)
		return content
	}
	return processedContent
}

// updateRequestMessages updates the request messages with processed instructions.
func (p *InstructionRequestProcessor) updateRequestMessages(req *model.Request, processedInstruction, processedSystemPrompt string) {
	systemMsgIndex := findSystemMessageIndex(req.Messages)

	if systemMsgIndex >= 0 {
		p.updateExistingSystemMessage(req, systemMsgIndex, processedInstruction, processedSystemPrompt)
	} else {
		p.createNewSystemMessage(req, processedInstruction, processedSystemPrompt)
	}
}

// updateExistingSystemMessage updates an existing system message with new instructions.
func (p *InstructionRequestProcessor) updateExistingSystemMessage(
	req *model.Request, systemMsgIndex int, processedInstruction, processedSystemPrompt string,
) {
	systemMsg := &req.Messages[systemMsgIndex]

	if processedInstruction != "" && !containsInstruction(systemMsg.Content, processedInstruction) {
		systemMsg.Content += "\n\n" + processedInstruction
		log.Debugf("Instruction request processor: appended instruction to existing system message")
	}

	if processedSystemPrompt != "" && !containsInstruction(systemMsg.Content, processedSystemPrompt) {
		systemMsg.Content = processedSystemPrompt + "\n\n" + systemMsg.Content
		log.Debugf("Instruction request processor: prepended system prompt to existing system message")
	}
}

// createNewSystemMessage creates a new system message with combined instructions.
func (p *InstructionRequestProcessor) createNewSystemMessage(
	req *model.Request, processedInstruction, processedSystemPrompt string,
) {
	systemContent := p.buildSystemContent(processedInstruction, processedSystemPrompt)

	if systemContent != "" {
		systemMsg := model.NewSystemMessage(systemContent)
		req.Messages = append([]model.Message{systemMsg}, req.Messages...)
		log.Debugf("Instruction request processor: added combined system message")
	}
}

// buildSystemContent builds the content for a new system message.
func (p *InstructionRequestProcessor) buildSystemContent(processedInstruction, processedSystemPrompt string) string {
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

	return systemContent
}

// sendPreprocessingEvent sends a preprocessing event if invocation is available.
func (p *InstructionRequestProcessor) sendPreprocessingEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	ch chan<- *event.Event,
) {
	if invocation == nil {
		return
	}

	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = model.ObjectTypePreprocessingInstruction

	select {
	case ch <- evt:
		log.Debugf("Instruction request processor: sent preprocessing event")
	case <-ctx.Done():
		log.Debugf("Instruction request processor: context cancelled")
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
