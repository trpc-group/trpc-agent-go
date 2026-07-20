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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	minimumStructuredAssistantResultItems = 3
	assistantResultRecoveryUserSuffix     = "Re-check the structured assistant " +
		"response above and store only a concrete requested result, if present."
)

const assistantResultRecoveryPrompt = `You are an Assistant Result Memory Manager.
Today's date is {current_date}.

A broader memory pass did not identify an assistant result. Re-check the
structured assistant response and extract ONLY a concrete result supplied in
direct response to the user's request. Use memory_add_assistant_result for an
eligible result and emit no tool call otherwise.

<assistant_result_recovery>
- Eligible results include requested named entities, extracted fields,
  classifications, transformations, final recommendations, selected
  conclusions, ordered plans, and cohesive lists or mappings.
- A requested structured extraction remains eligible even when its source is
  non-personal or the response is framed as analysis or opinion.
- Preserve exact names, ordering, negation, quantities, and item-to-detail
  relationships. Keep a cohesive result together when splitting loses those
  relationships.
- Do not store the request itself, generic definitions, tutorial steps,
  unselected alternatives, brainstorming, acknowledgments, or filler.
- Do not duplicate a result already present in existing memories.
- Every memory must begin with "Assistant result:" so its provenance remains
  explicit after persistence.
</assistant_result_recovery>`

func (e *memoryExtractor) recoverStructuredAssistantResults(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) (context.Context, []*Operation, error) {
	req := &model.Request{
		Messages: e.buildAssistantResultRecoveryMessages(
			ctx, messages, existing,
		),
		Tools: map[string]tool.Tool{
			assistantResultAddToolName: assistantResultAddTool,
		},
	}
	ctx, operations, err := e.generateOperations(ctx, req)
	if err != nil {
		return ctx, nil, err
	}
	_, assistantResults := splitExtractionOperations(operations)
	return ctx, assistantResults, nil
}

func (e *memoryExtractor) buildAssistantResultRecoveryMessages(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+2)
	result = append(result, model.NewSystemMessage(
		e.buildAssistantResultRecoveryPrompt(ctx, existing),
	))
	for _, message := range messages {
		if message.Role != model.RoleUser &&
			message.Role != model.RoleAssistant {
			continue
		}
		if message.ToolID != "" || len(message.ToolCalls) > 0 ||
			!messageHasText(message) {
			continue
		}
		result = append(result, message)
	}
	result = append(result,
		model.NewUserMessage(assistantResultRecoveryUserSuffix))
	return result
}

func (e *memoryExtractor) buildAssistantResultRecoveryPrompt(
	ctx context.Context,
	existing []*memory.Entry,
) string {
	var result strings.Builder
	result.WriteString(strings.ReplaceAll(
		assistantResultRecoveryPrompt,
		currentDatePlaceholder,
		referenceDate(ctx).UTC().Format(time.DateOnly),
	))
	result.WriteString("\n<available_actions>\n- ")
	result.WriteString(assistantResultAddToolName)
	result.WriteString(": Add a concrete result provided by the assistant.\n")
	result.WriteString("</available_actions>\n")
	if len(existing) == 0 {
		return result.String()
	}
	result.WriteString("\n<existing_memories>\n")
	for _, entry := range existing {
		if entry != nil && entry.Memory != nil {
			result.WriteString(formatExistingMemory(entry))
		}
	}
	result.WriteString("</existing_memories>\n")
	return result.String()
}

func hasStructuredAssistantResultCandidate(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role != model.RoleAssistant || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		items := 0
		for _, line := range strings.Split(extractionMessageText(message), "\n") {
			if !isStructuredListItem(line) {
				continue
			}
			items++
			if items >= minimumStructuredAssistantResultItems {
				return true
			}
		}
	}
	return false
}

func extractionMessageText(message model.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+1)
	if text := strings.TrimSpace(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, part := range message.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if text := strings.TrimSpace(*part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func isStructuredListItem(line string) bool {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "+ ", "\u2022 "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	digitEnd := 0
	for digitEnd < len(line) && line[digitEnd] >= '0' &&
		line[digitEnd] <= '9' {
		digitEnd++
	}
	return digitEnd > 0 && digitEnd+1 < len(line) &&
		(line[digitEnd] == '.' || line[digitEnd] == ')') &&
		line[digitEnd+1] == ' '
}
