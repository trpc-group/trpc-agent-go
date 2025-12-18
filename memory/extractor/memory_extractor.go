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
	model  model.Model
	prompt string
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
		Tools:    buildToolsMap(),
	}

	// Call model.
	rspChan, err := e.model.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model call failed: %w", err)
	}

	// Parse tool calls into operations.
	var ops []*Operation
	for rsp := range rspChan {
		if rsp.Error != nil {
			return nil, fmt.Errorf("model error: %s", rsp.Error.Message)
		}
		if len(rsp.Choices) == 0 {
			continue
		}
		for _, call := range rsp.Choices[0].Message.ToolCalls {
			op := e.parseToolCall(call)
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
func (e *memoryExtractor) parseToolCall(call model.ToolCall) *Operation {
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		log.Warnf("extractor: failed to parse tool args: %v", err)
		return nil
	}
	return parseToolCallArgs(call.Function.Name, args)
}

// defaultPrompt is the default system prompt for memory extraction.
const defaultPrompt = `You are a Memory Manager responsible for managing information about the user.

## Your Task
Analyze the conversation and decide what memory operations to perform using the available tools.

## When to add or update memories
- Decide if a memory needs to be added, updated, or deleted based on the conversation.
- If the user shares new personal information not in existing memories, add it.
- Capture: name, age, occupation, location, interests, preferences, opinions, significant life events.
- If existing memories already capture all relevant information, no updates are needed.

## How to add or update memories
- Create brief, third-person statements: "User's name is John Doe".
- Don't make a single memory too long; create multiple if needed.
- Don't repeat information in multiple memories; update existing ones instead.
- When updating, append new information rather than completely overwriting.

## Available operations
- Use memory_add to add new information.
- Use memory_update to modify existing information (requires memory_id).
- Use memory_delete to remove information when user requests to forget.

## Important guidelines
- Only create memories for personal, meaningful information.
- Do not create memories for transient requests or generic questions.
- If no personal information is shared, do not call any tool.
- Memories should be de-duplicated; check existing memories first.
`
