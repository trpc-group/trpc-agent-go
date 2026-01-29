//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	// InstructionResolver, if provided, supplies the instruction for each
	// request based on the current invocation. When set, this takes
	// precedence over InstructionGetter and Instruction.
	InstructionResolver func(*agent.Invocation) string
	// InstructionGetter, if provided, dynamically supplies the instruction
	// each time a request is processed. When set, this takes precedence over
	// the static Instruction field.
	InstructionGetter func() string
	// SystemPrompt is the system prompt to add to requests.
	SystemPrompt string
	// SystemPromptResolver, if provided, supplies the system prompt for each
	// request based on the current invocation. When set, this takes
	// precedence over SystemPromptGetter and SystemPrompt.
	SystemPromptResolver func(*agent.Invocation) string
	// SystemPromptGetter, if provided, dynamically supplies the system prompt
	// each time a request is processed. When set, this takes precedence over
	// the static SystemPrompt field.
	SystemPromptGetter func() string
	// OutputSchema is the JSON schema for output validation.
	// When provided, JSON output instructions are automatically injected.
	OutputSchema map[string]any
	// StructuredOutputSchema is the JSON schema generated from structured_output.
	// When provided, it takes precedence over OutputSchema for instruction injection.
	StructuredOutputSchema map[string]any
}

const (
	jsonInstructionsStrictTemplate = `IMPORTANT: Return ONLY a JSON object that
conforms to the schema below.
- Do NOT include the schema itself in your output.
- Do NOT include explanations, comments, or markdown fences.
- Do NOT add keys other than those defined in the schema's properties.
- The response must be a single JSON object instance, not wrapped, and no
  trailing text.

Schema (for reference only, do not include this in your output):
%s
`

	jsonInstructionsToolsTemplate = `IMPORTANT:
- You MAY call tools when needed (function calling).
- While you are calling tools, do NOT provide a user-facing answer.
- When you are ready to provide the final answer, return ONLY a JSON object
  that conforms to the schema below.
- Do NOT include the schema itself in your output.
- Do NOT include explanations, comments, or markdown fences.
- Do NOT add keys other than those defined in the schema's properties.
- The final response must be a single JSON object instance, not wrapped, and
  no trailing text.

Schema (for reference only, do not include this in your output):
%s
`
)

// InstructionRequestProcessorOption is a function that can be used to configure the instruction request processor.
type InstructionRequestProcessorOption func(*InstructionRequestProcessor)

// WithOutputSchema adds the output schema to the instruction request processor.
func WithOutputSchema(outputSchema map[string]any) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.OutputSchema = outputSchema
	}
}

// WithStructuredOutputSchema adds the structured output schema to the instruction request processor.
// This is used as a fallback when the model provider does not natively enforce JSON Schema.
func WithStructuredOutputSchema(schema map[string]any) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.StructuredOutputSchema = schema
	}
}

// WithInstructionResolver configures a dynamic resolver for instruction
// content based on the current invocation.
func WithInstructionResolver(
	resolver func(*agent.Invocation) string,
) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.InstructionResolver = resolver
	}
}

// WithInstructionGetter configures a dynamic getter for instruction content.
// When provided, this getter is called for every request, allowing callers to
// update the instruction at runtime without reconstructing the processor/agent.
func WithInstructionGetter(getter func() string) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.InstructionGetter = getter
	}
}

// WithSystemPromptResolver configures a dynamic resolver for system prompt
// content based on the current invocation.
func WithSystemPromptResolver(
	resolver func(*agent.Invocation) string,
) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.SystemPromptResolver = resolver
	}
}

// WithSystemPromptGetter configures a dynamic getter for system prompt content.
// When provided, this getter is called for every request, allowing callers to
// update the system prompt at runtime without reconstructing the processor/agent.
func WithSystemPromptGetter(getter func() string) InstructionRequestProcessorOption {
	return func(p *InstructionRequestProcessor) {
		p.SystemPromptGetter = getter
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
	if invocation == nil {
		return
	}
	if req == nil {
		log.ErrorfContext(
			ctx,
			"Instruction request processor: request is nil",
		)
		return
	}

	agentName := invocation.AgentName
	log.DebugfContext(
		ctx,
		"Instruction request processor: processing request for agent %s",
		agentName,
	)

	// Process instruction and system prompt with state injection.
	processedInstruction, processedSystemPrompt := p.processInstructionsWithState(
		ctx,
		invocation,
	)

	// Update the request messages with processed instructions.
	p.updateRequestMessages(req, processedInstruction, processedSystemPrompt)

	// Send a preprocessing event.
	p.sendPreprocessingEvent(ctx, invocation, ch)
}

// processInstructionsWithState processes instruction and system prompt with
// state injection.
func (p *InstructionRequestProcessor) processInstructionsWithState(
	ctx context.Context,
	invocation *agent.Invocation,
) (string, string) {
	// Prefer invocation-based resolvers, then dynamic getters, then static
	// fields.
	var processedInstruction string
	if p.InstructionResolver != nil {
		processedInstruction = p.InstructionResolver(invocation)
	} else if p.InstructionGetter != nil {
		processedInstruction = p.InstructionGetter()
	} else {
		processedInstruction = p.Instruction
	}

	var processedSystemPrompt string
	if p.SystemPromptResolver != nil {
		processedSystemPrompt = p.SystemPromptResolver(invocation)
	} else if p.SystemPromptGetter != nil {
		processedSystemPrompt = p.SystemPromptGetter()
	} else {
		processedSystemPrompt = p.SystemPrompt
	}

	if invocation != nil {
		if invocation.RunOptions.Instruction != "" {
			processedInstruction = invocation.RunOptions.Instruction
		}
		if invocation.RunOptions.GlobalInstruction != "" {
			processedSystemPrompt =
				invocation.RunOptions.GlobalInstruction
		}
	}

	// Automatically inject JSON output instructions.
	// Precedence: StructuredOutputSchema > OutputSchema.
	if p.StructuredOutputSchema != nil {
		jsonInstructions := p.generateStructuredOutputJSONInstructions(
			invocation,
			p.StructuredOutputSchema,
		)
		processedInstruction = p.combineInstructions(
			processedInstruction,
			jsonInstructions,
		)
	} else if p.OutputSchema != nil {
		jsonInstructions := p.generateJSONInstructions(p.OutputSchema)
		processedInstruction = p.combineInstructions(
			processedInstruction,
			jsonInstructions,
		)
	}

	if invocation != nil {
		processedInstruction = p.injectStateIntoContent(
			ctx,
			invocation,
			processedInstruction,
			"instruction",
		)
		processedSystemPrompt = p.injectStateIntoContent(
			ctx,
			invocation,
			processedSystemPrompt,
			"system prompt",
		)
	}

	return processedInstruction, processedSystemPrompt
}

// combineInstructions combines existing instruction with new JSON
// instructions.
func (p *InstructionRequestProcessor) combineInstructions(
	existingInstruction, jsonInstructions string,
) string {
	if existingInstruction != "" {
		return existingInstruction + "\n\n" + jsonInstructions
	}
	return jsonInstructions
}

// injectStateIntoContent injects session state into the given content.
func (p *InstructionRequestProcessor) injectStateIntoContent(
	ctx context.Context,
	invocation *agent.Invocation,
	content, contentType string,
) string {
	if content == "" {
		return content
	}

	processedContent, err := state.InjectSessionState(content, invocation)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Failed to inject session state into %s: %v",
			contentType,
			err,
		)
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
		log.Debugf(
			"Instruction request processor: appended instruction to " +
				"existing system message",
		)
	}

	if processedSystemPrompt != "" && !containsInstruction(systemMsg.Content, processedSystemPrompt) {
		systemMsg.Content = processedSystemPrompt + "\n\n" + systemMsg.Content
		log.Debugf(
			"Instruction request processor: prepended system prompt to " +
				"existing system message",
		)
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
		log.Debugf(
			"Instruction request processor: added combined system message",
		)
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

	log.DebugfContext(
		ctx,
		"Instruction request processor: sent preprocessing event",
	)

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingInstruction),
	)); err != nil {
		log.DebugfContext(
			ctx,
			"Instruction request processor: context cancelled",
		)
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

func findLastSystemMessageIndex(messages []model.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleSystem {
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

func invocationHasTools(invocation *agent.Invocation) bool {
	if invocation == nil || invocation.Agent == nil {
		return false
	}
	return len(invocation.Agent.Tools()) > 0
}

func (p *InstructionRequestProcessor) generateStructuredOutputJSONInstructions(
	invocation *agent.Invocation,
	schema map[string]any,
) string {
	if schema == nil {
		return ""
	}
	if invocationHasTools(invocation) {
		return p.generateJSONInstructionsWithTemplate(
			schema,
			jsonInstructionsToolsTemplate,
		)
	}
	return p.generateJSONInstructions(schema)
}

// generateJSONInstructions generates JSON output instructions based on a schema.
func (p *InstructionRequestProcessor) generateJSONInstructions(schema map[string]any) string {
	if schema == nil {
		return ""
	}

	return p.generateJSONInstructionsWithTemplate(
		schema,
		jsonInstructionsStrictTemplate,
	)
}

func (p *InstructionRequestProcessor) generateJSONInstructionsWithTemplate(
	schema map[string]any,
	template string,
) string {
	// Convert schema to a readable format for the instruction.
	schemaStr := p.formatSchemaForInstruction(schema)
	return fmt.Sprintf(template, schemaStr)
}

// formatSchemaForInstruction formats the schema for inclusion in instructions.
func (p *InstructionRequestProcessor) formatSchemaForInstruction(schema map[string]any) string {
	// For now, we'll create a simple JSON representation.
	// In a more sophisticated implementation, we could parse the schema more intelligently.
	jsonBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		// Fallback to a simple string representation.
		return fmt.Sprintf("%v", schema)
	}
	return string(jsonBytes)
}
