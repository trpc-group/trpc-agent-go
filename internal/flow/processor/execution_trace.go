//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type executionTraceToolMessage struct {
	id      string
	name    string
	content string
}

func recordExecutionTraceToolResults(
	ctx context.Context,
	invocation *agent.Invocation,
	results []toolResult,
	finalEvent *event.Event,
) {
	if invocation == nil || !invocation.RunOptions.ExecutionTraceEnabled || len(results) == 0 {
		return
	}
	tools := executionTraceToolsFromResults(results, finalEvent)
	if len(tools) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	traceCtx := agent.NewInvocationContext(ctx, invocation)
	tracecapture.AddInvocationStepTools(traceCtx, tools)
	for _, recordedTool := range tools {
		skill, ok := tracecapture.LoadedSkillFromToolRecord(recordedTool)
		if ok {
			tracecapture.AddInvocationStepSkill(traceCtx, skill)
		}
	}
}

func executionTraceToolsFromResults(
	results []toolResult,
	finalEvent *event.Event,
) []atrace.Tool {
	finalMessages := executionTraceToolMessagesByID(finalEvent)
	tools := make([]atrace.Tool, 0, len(results))
	for _, result := range results {
		recordedTool, ok := executionTraceToolFromResult(result, finalMessages)
		if ok {
			tools = append(tools, recordedTool)
		}
	}
	return tools
}

func executionTraceToolFromResult(
	result toolResult,
	finalMessages map[string]executionTraceToolMessage,
) (atrace.Tool, bool) {
	original, ok := executionTraceFirstToolMessage(result.event)
	if !ok {
		return atrace.Tool{}, false
	}
	message := original
	if finalMessage, exists := finalMessages[original.id]; exists {
		message = finalMessage
	}
	if message.name == "" {
		message.name = original.name
	}
	if message.name == "" {
		message.name = result.toolName
	}
	recordedTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            message.id,
		Name:          message.name,
		Arguments:     result.toolArgs,
		ResultContent: []byte(message.content),
		Error:         result.traceError,
	})
	return recordedTool, true
}

func executionTraceToolMessagesByID(
	ev *event.Event,
) map[string]executionTraceToolMessage {
	if ev == nil || ev.Response == nil {
		return nil
	}
	messages := make(map[string]executionTraceToolMessage, len(ev.Response.Choices))
	for _, choice := range ev.Response.Choices {
		message, ok := executionTraceToolMessageFromChoice(choice)
		if ok {
			messages[message.id] = message
		}
	}
	return messages
}

func executionTraceFirstToolMessage(
	ev *event.Event,
) (executionTraceToolMessage, bool) {
	if ev == nil || ev.Response == nil {
		return executionTraceToolMessage{}, false
	}
	for _, choice := range ev.Response.Choices {
		message, ok := executionTraceToolMessageFromChoice(choice)
		if ok {
			return message, true
		}
	}
	return executionTraceToolMessage{}, false
}

func executionTraceToolMessageFromChoice(
	choice model.Choice,
) (executionTraceToolMessage, bool) {
	msg := choice.Message
	if msg.ToolID == "" && choice.Delta.ToolID != "" {
		msg = choice.Delta
	}
	if msg.ToolID == "" {
		return executionTraceToolMessage{}, false
	}
	return executionTraceToolMessage{
		id:      msg.ToolID,
		name:    msg.ToolName,
		content: msg.Content,
	}, true
}
