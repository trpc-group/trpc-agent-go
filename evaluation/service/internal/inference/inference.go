//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Inference executes the agent against the provided invocations.
func Inference(
	ctx context.Context,
	runner runner.Runner,
	invocations []*evalset.Invocation,
	initialSession *evalset.SessionInput,
	sessionID string,
) ([]*evalset.Invocation, error) {
	if len(invocations) == 0 {
		return nil, errors.New("invocations are empty")
	}
	if initialSession == nil {
		return nil, errors.New("session input is nil")
	}
	// Accumulate each invocation response.
	responseInvocations := make([]*evalset.Invocation, 0, len(invocations))
	for _, invocation := range invocations {
		responseInvocation, err := inferenceInvocation(ctx, runner, sessionID, initialSession, invocation)
		if err != nil {
			return nil, err
		}
		responseInvocations = append(responseInvocations, responseInvocation)
	}
	return responseInvocations, nil
}

// inferenceInvocation executes the agent for a single invocation.
func inferenceInvocation(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	initialSession *evalset.SessionInput,
	invocation *evalset.Invocation,
) (*evalset.Invocation, error) {
	if invocation.UserContent == nil {
		return nil, fmt.Errorf("invocation user content is nil for eval case invocation %q", invocation.InvocationID)
	}
	events, err := r.Run(ctx, initialSession.UserID, sessionID, *invocation.UserContent, agent.WithRuntimeState(initialSession.State))
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	// Capture the invocation ID, final response, tool uses, and tool responses.
	var (
		invocationID  string
		finalResponse *model.Message
		toolCalls     []*model.ToolCall
		toolResponses []*model.Message
	)
	for event := range events {
		if event == nil {
			continue
		}
		if event.Error != nil {
			return nil, fmt.Errorf("event: %v", event.Error)
		}
		// Capture the invocation ID.
		if invocationID == "" && event.InvocationID != "" {
			invocationID = event.InvocationID
		}
		// Capture the final response.
		if event.IsFinalResponse() {
			finalResponse = &event.Response.Choices[0].Message
			continue
		}
		// Capture tool call uses.
		if event.IsToolCallResponse() {
			calls, err := convertToolCallResponse(event)
			if err != nil {
				return nil, fmt.Errorf("convert tool call response: %w", err)
			}
			toolCalls = append(toolCalls, calls...)
		}
		// Capture tool call responses.
		if event.IsToolResultResponse() {
			responses, err := convertToolResultResponse(event)
			if err != nil {
				return nil, fmt.Errorf("convert tool result response: %w", err)
			}
			toolResponses = append(toolResponses, responses...)
		}
	}
	// Convert the final response to evalset content.
	return &evalset.Invocation{
		InvocationID:  invocationID,
		UserContent:   invocation.UserContent,
		FinalResponse: finalResponse,
		IntermediateData: &evalset.IntermediateData{
			ToolCalls:     toolCalls,
			ToolResponses: toolResponses,
		},
	}, nil
}

// convertToolCallResponse converts the tool call response to tool calls.
func convertToolCallResponse(event *event.Event) ([]*model.ToolCall, error) {
	toolCalls := []*model.ToolCall{}
	for _, choice := range event.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			call := toolCall
			call.Function.Arguments = append([]byte{}, call.Function.Arguments...)
			toolCalls = append(toolCalls, &call)
		}
	}
	return toolCalls, nil
}

// convertToolResultResponse converts the tool result response to tool response messages.
func convertToolResultResponse(event *event.Event) ([]*model.Message, error) {
	toolResponses := []*model.Message{}
	for _, choice := range event.Response.Choices {
		if choice.Message.ToolID == "" {
			continue
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(choice.Message.Content), &response); err != nil {
			return nil, fmt.Errorf("unmarshal tool result response: %w", err)
		}
		toolResponses = append(toolResponses, &model.Message{
			Role:     model.RoleTool,
			ToolID:   choice.Message.ToolID,
			ToolName: choice.Message.ToolName,
			Content:  choice.Message.Content,
		})
	}
	return toolResponses, nil
}
