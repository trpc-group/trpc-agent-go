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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const groundedStateRecoveryPrompt = `You are a Grounded State Memory Corrector.
Today's date is {current_date}.

A broader memory pass emitted one or more candidate memories that added a
state-transition or lifecycle relationship not explicitly stated by the user.
Correct ONLY those candidates using the conversation as evidence.

<grounded_state_recovery>
- Preserve every explicit fact, entity, identifier, quantity, and date from
  the candidate that the user's messages support.
- Remove inferred relationships such as replacement, moving on, loss of
  ownership, coexistence, or supersession when the user did not state them.
- Write independent entities as separate atomic memories. Do not relate two
  objects merely because they share a category or one is described as old and
  the other as new.
- Preserve an explicit transition when the user actually said they sold,
  traded, replaced, moved, switched, stopped, or no longer owned something.
- Emit only corrected operations for the listed candidates. Do not re-extract
  unrelated conversation details. Emit no tool call if no grounded correction
  can be made.
</grounded_state_recovery>`

var stateRelationFragments = []string{
	"replac",
	"move on from",
	"moved on from",
	"moving on from",
	"previously had",
	"no longer",
	"does not own",
	"doesn't own",
	"do not own",
	"don't own",
	"gave away",
	"got rid",
	"traded",
	"sold",
	"discard",
	"retir",
	"switch from",
	"switched from",
	"switching from",
	"upgrade from",
	"upgraded from",
	"upgrading from",
	"instead of",
	"in addition to",
	"alongside",
	"while keeping",
	"moved from",
	"changed from",
	"changed to",
	"left the",
	"stopped ",
}

func (e *memoryExtractor) recoverUngroundedStateOperations(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
	operations []*Operation,
) (context.Context, []*Operation, error) {
	if e.updatePolicy != UpdatePolicyHistoryPreserving {
		return ctx, operations, nil
	}
	userSource := userExtractionSourceText(messages)
	if containsStateRelation(userSource) {
		return ctx, operations, nil
	}
	safe := make([]*Operation, 0, len(operations))
	suspect := make([]*Operation, 0, 1)
	for _, operation := range operations {
		if operationHasStateRelation(operation) {
			suspect = append(suspect, operation)
			continue
		}
		safe = append(safe, operation)
	}
	if len(suspect) == 0 {
		return ctx, operations, nil
	}
	if !e.actionEnabled(memory.AddToolName) {
		return ctx, operations, nil
	}
	req := &model.Request{
		Messages: e.buildGroundedStateRecoveryMessages(
			ctx, messages, existing, suspect,
		),
		Tools: map[string]tool.Tool{
			groundedStateAddToolName: groundedStateAddTool,
		},
	}
	ctx, recovered, err := e.generateOperations(ctx, req)
	if err != nil {
		return ctx, operations, err
	}
	primary, _ := splitExtractionOperations(recovered)
	grounded := make([]*Operation, 0, len(primary))
	for _, operation := range primary {
		if operationHasStateRelation(operation) {
			continue
		}
		grounded = append(grounded, operation)
	}
	if len(grounded) == 0 {
		return ctx, operations, nil
	}
	return ctx, append(safe,
		uniqueExtractionOperations(safe, grounded)...), nil
}

func (e *memoryExtractor) buildGroundedStateRecoveryMessages(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
	suspect []*Operation,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+2)
	result = append(result, model.NewSystemMessage(
		e.buildGroundedStateRecoveryPrompt(ctx, existing),
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
	var candidates strings.Builder
	candidates.WriteString("Correct these candidate operations:\n")
	for i, operation := range suspect {
		fmt.Fprintf(&candidates, "%d. type=%s memory=%q\n",
			i+1, operation.Type, operation.Memory)
	}
	candidates.WriteString(
		"Return only grounded corrections for these candidates.",
	)
	result = append(result, model.NewUserMessage(candidates.String()))
	return result
}

func (e *memoryExtractor) buildGroundedStateRecoveryPrompt(
	ctx context.Context,
	existing []*memory.Entry,
) string {
	var result strings.Builder
	result.WriteString(strings.ReplaceAll(
		groundedStateRecoveryPrompt,
		currentDatePlaceholder,
		referenceDate(ctx).UTC().Format(time.DateOnly),
	))
	result.WriteString("\n<available_actions>\n")
	result.WriteString("- ")
	result.WriteString(groundedStateAddToolName)
	result.WriteString(
		": Add a corrected state memory grounded in the user's words.\n",
	)
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

func operationHasStateRelation(operation *Operation) bool {
	if operation == nil ||
		(operation.Type != OperationAdd &&
			operation.Type != OperationUpdate) {
		return false
	}
	return containsStateRelation(operation.Memory)
}

func containsStateRelation(text string) bool {
	text = strings.ToLower(text)
	for _, fragment := range stateRelationFragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func userExtractionSourceText(messages []model.Message) string {
	var source strings.Builder
	for _, message := range messages {
		if message.Role != model.RoleUser || message.ToolID != "" ||
			len(message.ToolCalls) > 0 {
			continue
		}
		appendSourceText(&source, message.Content)
		for _, part := range message.ContentParts {
			if part.Type == model.ContentTypeText && part.Text != nil {
				appendSourceText(&source, *part.Text)
			}
		}
	}
	return source.String()
}
