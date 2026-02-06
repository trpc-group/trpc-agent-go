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
	req := &model.Request{
		Messages: e.buildMessages(messages, existing),
		Tools:    backgroundTools,
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

// buildSystemPrompt builds the system prompt with existing memories.
func (e *memoryExtractor) buildSystemPrompt(existing []*memory.Entry) string {
	if len(existing) == 0 {
		return e.prompt
	}

	var sb strings.Builder
	sb.WriteString(e.prompt)
	sb.WriteString("\n<existing_memories>\n")
	for _, entry := range existing {
		if entry.Memory != nil {
			fmt.Fprintf(&sb, "- [%s] %s\n", entry.ID, entry.Memory.Memory)
		}
	}
	sb.WriteString("</existing_memories>\n")
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
</instructions>

<guidelines>
- Create memories in the third person, e.g., "User enjoys hiking on weekends."
- Keep each memory focused on a single piece of information.
- Make multiple tool calls in a single response when you identify multiple distinct pieces of information.
- Use update when information changes or needs to be appended.
- Only use delete when the user explicitly asks to forget something.
- Use the same language for topics as you use for the memory content.
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
