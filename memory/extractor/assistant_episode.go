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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	metadataKeyConversationExtraction = "conversation_extraction"
	assistantEpisodeMetadataValue     = "assistant-episode"
	assistantEpisodeToolName          = "memory_assistant_episode"
	assistantEpisodeMaxBytes          = 4096
	assistantEpisodePrefix            = "Assistant-provided conversation episode: "
)

// WithAssistantEpisodeExtraction enables extraction of reusable assistant
// responses as ordinary episodic memory. The option only affects the extractor
// being constructed and cannot be changed at runtime.
func WithAssistantEpisodeExtraction() Option {
	return func(e *memoryExtractor) {
		e.assistantEpisodeExtraction = true
	}
}

func (e *memoryExtractor) extractionTools() map[string]tool.Tool {
	tools := backgroundTools
	if len(e.enabledTools) > 0 {
		tools = filterTools(backgroundTools, e.enabledTools)
	}
	if !e.assistantEpisodeExtraction || !e.assistantEpisodeAddEnabled() {
		return tools
	}
	if _, ok := tools[memory.AddToolName]; !ok {
		return tools
	}
	result := make(map[string]tool.Tool, len(tools)+1)
	for name, existing := range tools {
		result[name] = existing
	}
	result[assistantEpisodeToolName] = assistantEpisodeTool
	return result
}

var assistantEpisodeTool = &declarationOnlyTool{
	decl: &tool.Declaration{
		Name: assistantEpisodeToolName,
		Description: "Record reusable information supplied by an assistant response as attributed conversation history. " +
			"Use one assistant source segment to ground a concise, self-contained episode that preserves the user's request " +
			"context and the assistant's response. Use memory_add for ordinary user facts and events.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				argKeyMemory: {
					Type: "string",
					Description: "A concise, self-contained description of what the user needed and what the assistant supplied. " +
						"Preserve exact names, quantities, dates, negation, and qualifications. Describe attributed conversation " +
						"history, not verified truth or a user preference.",
				},
				argKeyTopics: {
					Type:        "array",
					Description: "Optional topics that make the episode easier to retrieve.",
					Items:       &tool.Schema{Type: "string"},
				},
			},
			Required:             []string{argKeyMemory},
			AdditionalProperties: false,
		},
	},
}

const assistantEpisodePrompt = `

<assistant_episode_policy>
- Use memory_assistant_episode only when an assistant response supplied durable,
  reusable information that may be needed in a later follow-up.
- Store a concise, self-contained episode that preserves the user's request
  context and the assistant's response, including exact names, quantities,
  dates, negation, and qualifications that affect meaning.
- Treat the record as attributed conversation history. Do not rewrite the
  assistant output as verified truth, a user preference, or a user action.
- Emit at most one episode for one assistant response. Do not record generic
  filler, acknowledgments, hidden reasoning, credentials, raw tool-call
  arguments, or raw tool results.
- Continue to use the standard memory tools for ordinary user facts and events.
</assistant_episode_policy>
`

func (e *memoryExtractor) parseToolCallWithMessages(
	ctx context.Context,
	call model.ToolCall,
	messages []model.Message,
) *Operation {
	if !e.assistantEpisodeExtraction || call.Function.Name != assistantEpisodeToolName {
		return e.parseToolCall(ctx, call)
	}
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		log.WarnfContext(ctx, "extractor: failed to parse assistant episode args: %v", err)
		return nil
	}
	op, err := e.parseAssistantEpisode(ctx, args, messages)
	if err != nil {
		log.WarnfContext(ctx, "extractor: invalid assistant episode: %v", err)
		return nil
	}
	return op
}

func (e *memoryExtractor) parseAssistantEpisode(
	ctx context.Context,
	args map[string]any,
	messages []model.Message,
) (*Operation, error) {
	if !e.assistantEpisodeExtraction {
		return nil, errors.New("assistant episode extraction is not enabled")
	}
	if !e.assistantEpisodeAddEnabled() {
		return nil, errors.New("memory_add is not enabled")
	}
	if !hasAssistantEpisodeSource(messages) {
		return nil, errors.New("assistant episode requires an assistant response")
	}
	memoryText, _ := args[argKeyMemory].(string)
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return nil, errors.New("memory is required")
	}
	if len(memoryText) > assistantEpisodeMaxBytes {
		return nil, fmt.Errorf("assistant episode exceeds %d bytes", assistantEpisodeMaxBytes)
	}
	op := &Operation{
		Type:         OperationAdd,
		Memory:       assistantEpisodePrefix + memoryText,
		Topics:       toStringSlice(args[argKeyTopics]),
		MemoryKind:   memory.KindEpisode,
		Participants: []string{"User", "Assistant"},
	}
	if eventTime, ok := ReferenceDateFromContext(ctx); ok {
		eventTime = eventTime.UTC()
		op.EventTime = &eventTime
	}
	return op, nil
}

func (e *memoryExtractor) assistantEpisodeAddEnabled() bool {
	// Match the extractor's existing tool-filter behavior: a nil or empty
	// enabled set leaves all background tools available.
	if len(e.enabledTools) == 0 {
		return true
	}
	_, ok := e.enabledTools[memory.AddToolName]
	return ok
}

func hasAssistantEpisodeSource(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role != model.RoleAssistant {
			continue
		}
		if strings.TrimSpace(message.Content) != "" {
			return true
		}
		for _, part := range message.ContentParts {
			if part.Type == model.ContentTypeText && part.Text != nil &&
				strings.TrimSpace(*part.Text) != "" {
				return true
			}
		}
	}
	return false
}
