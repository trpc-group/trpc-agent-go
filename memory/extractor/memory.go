//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Common metadata field keys.
const (
	metadataKeyModelName      = "model_name"
	metadataKeyModelAvailable = "model_available"
)

// memoryExtractor implements the MemoryExtractor interface.
type memoryExtractor struct {
	model    model.Model
	prompt   string
	checkers []Checker

	enabledTools map[string]struct{}

	// modelCallbacks configures before/after model callbacks for extraction.
	modelCallbacks *model.Callbacks
}

// Option is a function that configures a MemoryExtractor.
type Option func(*memoryExtractor)

// WithPrompt sets the custom prompt for memory extraction.
// The prompt will be used as the system message when calling the LLM.
func WithPrompt(prompt string) Option {
	return func(e *memoryExtractor) {
		if prompt != "" {
			e.prompt = prompt
		}
	}
}

// WithChecker adds an extraction checker.
// Multiple calls append checkers, combined with AND logic by default.
// When checkers are configured, ShouldExtract returns true only if all
// checkers pass.
func WithChecker(c Checker) Option {
	return func(e *memoryExtractor) {
		if c != nil {
			e.checkers = append(e.checkers, c)
		}
	}
}

// WithModelCallbacks sets model callbacks for memory extraction.
// Only structured callbacks are supported.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(e *memoryExtractor) {
		e.modelCallbacks = callbacks
	}
}

// WithCheckersAny sets checkers with OR logic.
// Any checker passing will trigger extraction.
// This replaces any previously configured checkers.
func WithCheckersAny(checks ...Checker) Option {
	return func(e *memoryExtractor) {
		if len(checks) > 0 {
			e.checkers = []Checker{ChecksAny(checks...)}
		}
	}
}

// NewExtractor creates a new memory extractor.
func NewExtractor(m model.Model, opts ...Option) MemoryExtractor {
	e := &memoryExtractor{
		model:  m,
		prompt: defaultPrompt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Extract analyzes the conversation and returns memory operations.
func (e *memoryExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*Operation, error) {
	if e.model == nil {
		return nil, errors.New("no model configured for memory extraction")
	}
	if len(messages) == 0 {
		return nil, nil
	}

	// Build request with tool declarations.
	tools := backgroundTools
	if len(e.enabledTools) > 0 {
		tools = filterTools(backgroundTools, e.enabledTools)
	}
	req := &model.Request{
		Messages: e.buildMessages(messages, existing),
		Tools:    tools,
	}

	// Call model.
	ctx, rspChan, err := e.runBeforeModelCallbacks(ctx, req)
	if err != nil {
		return nil, err
	}
	if rspChan == nil {
		rspChan, err = e.model.GenerateContent(ctx, req)
		if err != nil {
			log.WarnfContext(ctx, "extractor: model call failed: %v", err)
			return nil, fmt.Errorf("model call failed: %w", err)
		}
	}

	// Parse tool calls into operations.
	var ops []*Operation
	for rsp := range rspChan {
		ctx, rsp, err = e.runAfterModelCallbacks(ctx, req, rsp)
		if err != nil {
			return nil, err
		}
		if rsp == nil {
			continue
		}
		if rsp.Error != nil {
			return nil, fmt.Errorf("model error: %s", rsp.Error.Message)
		}
		if len(rsp.Choices) == 0 {
			continue
		}
		for _, call := range rsp.Choices[0].Message.ToolCalls {
			op := e.parseToolCall(ctx, call)
			if op != nil {
				ops = append(ops, op)
			}
		}
	}
	return ops, nil
}

// SetPrompt updates the extractor's prompt dynamically.
func (e *memoryExtractor) SetPrompt(prompt string) {
	if prompt != "" {
		e.prompt = prompt
	}
}

// SetModel updates the extractor's model dynamically.
func (e *memoryExtractor) SetModel(m model.Model) {
	if m != nil {
		e.model = m
	}
}

// SetEnabledTools updates the enabled tool flags for background
// operations. The map is defensively copied to prevent external
// mutation.
func (e *memoryExtractor) SetEnabledTools(
	enabled map[string]struct{},
) {
	e.enabledTools = maps.Clone(enabled)
}

// ShouldExtract checks if extraction should be triggered based on context.
// Returns true if extraction should proceed, false to skip.
// When no checkers are configured, always returns true.
func (e *memoryExtractor) ShouldExtract(ctx *ExtractionContext) bool {
	if len(e.checkers) == 0 {
		return true
	}
	// All checkers must pass (AND logic).
	for _, check := range e.checkers {
		if !check(ctx) {
			return false
		}
	}
	return true
}

// Metadata returns metadata about the extractor configuration.
func (e *memoryExtractor) Metadata() map[string]any {
	var modelName string
	modelAvailable := false
	if e.model != nil {
		modelName = e.model.Info().Name
		modelAvailable = true
	}
	return map[string]any{
		metadataKeyModelName:      modelName,
		metadataKeyModelAvailable: modelAvailable,
	}
}

// buildMessages builds messages for auto memory extraction.
func (e *memoryExtractor) buildMessages(
	messages []model.Message,
	existing []*memory.Entry,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+1)

	// Add system prompt with existing memories.
	result = append(result, model.NewSystemMessage(
		e.buildSystemPrompt(existing),
	))

	// Add conversation messages.
	result = append(result, messages...)

	return result
}

// buildSystemPrompt builds the system prompt with existing memories
// and available actions based on enabled tools.
func (e *memoryExtractor) buildSystemPrompt(
	existing []*memory.Entry,
) string {
	var sb strings.Builder
	sb.WriteString(e.prompt)

	// Append available actions.
	sb.WriteString("\n<available_actions>\n")
	sb.WriteString(e.availableActionsBlock())
	sb.WriteString("</available_actions>\n")

	// Append existing memories.
	if len(existing) > 0 {
		sb.WriteString("\n<existing_memories>\n")
		for _, entry := range existing {
			if entry.Memory != nil {
				fmt.Fprintf(&sb,
					"- [%s] %s\n", entry.ID, entry.Memory.Memory)
			}
		}
		sb.WriteString("</existing_memories>\n")
	}

	return sb.String()
}

// toolActionDescriptions maps background tool names to their
// one-line descriptions shown in the system prompt.
var toolActionDescriptions = map[string]string{
	memory.AddToolName: "Add a new memory " +
		"(only if genuinely new information).",
	memory.UpdateToolName: "Update an existing memory " +
		"with new or corrected information. " +
		"Prefer updating over adding a near-duplicate.",
	memory.DeleteToolName: "Delete a memory " +
		"when the user explicitly asks to forget something.",
	memory.ClearToolName: "Clear all memories " +
		"only when the user explicitly asks to forget everything.",
}

// toolActionOrder controls the deterministic output order.
var toolActionOrder = []string{
	memory.AddToolName,
	memory.UpdateToolName,
	memory.DeleteToolName,
	memory.ClearToolName,
}

// availableActionsBlock returns the text lines describing which
// memory tools the model is allowed to call.
func (e *memoryExtractor) availableActionsBlock() string {
	var sb strings.Builder
	for _, name := range toolActionOrder {
		// Skip tools that are disabled.
		if e.enabledTools != nil {
			if _, ok := e.enabledTools[name]; !ok {
				continue
			}
		}
		desc, ok := toolActionDescriptions[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", name, desc)
	}
	if sb.Len() == 0 {
		sb.WriteString("No actions available.\n")
	}
	return sb.String()
}

// parseToolCall parses a tool call and returns a memory operation.
func (e *memoryExtractor) parseToolCall(ctx context.Context, call model.ToolCall) *Operation {
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		log.WarnfContext(ctx, "extractor: failed to parse tool args: %v", err)
		return nil
	}
	return parseToolCallArgs(call.Function.Name, args)
}

func (e *memoryExtractor) runBeforeModelCallbacks(
	ctx context.Context,
	request *model.Request,
) (context.Context, <-chan *model.Response, error) {
	if e.modelCallbacks == nil {
		return ctx, nil, nil
	}

	result, err := e.modelCallbacks.RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("before model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result == nil || result.CustomResponse == nil {
		return ctx, nil, nil
	}

	customChan := make(chan *model.Response, 1)
	customChan <- result.CustomResponse
	close(customChan)
	return ctx, customChan, nil
}

func modelErrFromResponse(resp *model.Response) error {
	if resp == nil || resp.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", resp.Error.Type, resp.Error.Message)
}

func (e *memoryExtractor) runAfterModelCallbacks(
	ctx context.Context,
	request *model.Request,
	response *model.Response,
) (context.Context, *model.Response, error) {
	if e.modelCallbacks == nil {
		return ctx, response, nil
	}

	result, err := e.modelCallbacks.RunAfterModel(
		ctx,
		&model.AfterModelArgs{
			Request:  request,
			Response: response,
			Error:    modelErrFromResponse(response),
		},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("after model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		response = result.CustomResponse
	}
	return ctx, response, nil
}

// defaultPrompt is the default system prompt for memory extraction.
const defaultPrompt = `You are a Memory Manager for an AI Assistant.
Your task is to analyze the conversation and manage user memories.

<instructions>
1. Analyze the conversation to identify any new or updated information about the
   user that should be remembered.
2. Check if this information is already captured in existing memories.
3. Determine if any memories need to be added, updated, or deleted.
4. You can call multiple tools in parallel to handle all necessary changes at once.
5. Use the available tools to make the necessary changes.
6. If no memory changes are needed, do not call any tools.
</instructions>

<guidelines>
- Create memories as brief, third-person statements that capture key
  information, e.g., "User enjoys hiking on weekends."
- Keep each memory focused on a single piece of information. Create
  multiple memories if needed rather than one long complex memory.
- Do not repeat the same information in multiple memories; update
  existing memories instead.
- When updating a memory, append new information to the existing
  memory rather than completely overwriting it.
- When a user's preferences change, update the relevant memory to
  reflect the new state.
- Only use delete when the user explicitly asks to forget something.
- Only use clear when the user explicitly asks to forget everything.
- Write memory content and topics in the same language as the user's
  input message. For example, if the user writes in Chinese, write
  memories and topics in Chinese.
- Do not create memories for:
  - Transient requests or questions
  - Information already captured in existing memories
  - Generic conversation that doesn't reveal personal information
</guidelines>

<memory_types>
Capture meaningful personal information such as:
- Personal details: name, age, location, occupation
- Preferences: likes, dislikes, favorites
- Interests and hobbies
- Goals and aspirations
- Important relationships
- Significant life events
- Opinions and beliefs
- Work and education background
</memory_types>
`
