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

	"google.golang.org/genai"
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
	if len(invocation.UserContent.Parts) == 0 {
		return nil, fmt.Errorf("user content parts are empty for eval case invocation %q", invocation.InvocationID)
	}
	// Convert the evalset content into a model message.
	message, err := convertContentToMessage(invocation.UserContent)
	if err != nil {
		return nil, fmt.Errorf("convert content to message: %w", err)
	}
	events, err := r.Run(ctx, initialSession.UserID, sessionID, *message, agent.WithRuntimeState(initialSession.State))
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	// Capture the invocation ID, final response, and tool uses.
	var (
		invocationID  string
		finalResponse *genai.Content
		toolUses      []*genai.FunctionCall
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
			finalResponse, err = convertMessageToContent(&event.Response.Choices[0].Message)
			if err != nil {
				return nil, fmt.Errorf("convert message to content: %w", err)
			}
			continue
		}
		// Capture tool call uses.
		if event.IsToolCallResponse() {
			uses, err := convertToolCallResponse(event)
			if err != nil {
				return nil, fmt.Errorf("convert tool call response: %w", err)
			}
			toolUses = append(toolUses, uses...)
		}
	}
	// Convert the final response to evalset content.
	return &evalset.Invocation{
		InvocationID:  invocationID,
		UserContent:   invocation.UserContent,
		FinalResponse: finalResponse,
		IntermediateData: &evalset.IntermediateData{
			ToolUses: toolUses,
		},
	}, nil
}

// convertToolCallResponse converts the tool call response to function calls.
func convertToolCallResponse(event *event.Event) ([]*genai.FunctionCall, error) {
	toolUses := []*genai.FunctionCall{}
	for _, choice := range event.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			toolUse, err := convertToolCallsToFunctionCalls(&toolCall)
			if err != nil {
				return nil, fmt.Errorf("convert tool calls to function calls: %w", err)
			}
			toolUses = append(toolUses, toolUse)
		}
	}
	return toolUses, nil
}

// convertContentToMessage transforms evalset input content into a model message.
func convertContentToMessage(content *genai.Content) (*model.Message, error) {
	if content == nil {
		return nil, errors.New("content is nil")
	}
	if len(content.Parts) == 0 {
		return nil, errors.New("content parts are empty")
	}
	if content.Parts[0].Text == "" {
		return nil, errors.New("content part text is empty")
	}
	return &model.Message{
		Role:    model.Role(content.Role),
		Content: content.Parts[0].Text,
	}, nil
}

// convertMessageToContent converts the model response back into evalset content.
func convertMessageToContent(finalResponse *model.Message) (*genai.Content, error) {
	if finalResponse == nil {
		return nil, errors.New("final response is nil")
	}
	if finalResponse.Content == "" {
		return nil, errors.New("final response content is empty")
	}
	return &genai.Content{
		Role: string(finalResponse.Role),
		Parts: []*genai.Part{{
			Text: finalResponse.Content,
		}},
	}, nil
}

// convertToolCallsToFunctionCalls maps model-level tool calls to the evalset FunctionCall structure.
func convertToolCallsToFunctionCalls(toolCalls *model.ToolCall) (*genai.FunctionCall, error) {
	if toolCalls == nil {
		return nil, errors.New("tool calls is nil")
	}
	if toolCalls.Function.Name == "" {
		return nil, errors.New("tool call function name is empty")
	}
	var args map[string]interface{}
	if len(toolCalls.Function.Arguments) > 0 {
		if err := json.Unmarshal(toolCalls.Function.Arguments, &args); err != nil {
			return nil, fmt.Errorf("unmarshal tool arguments: %w", err)
		}
	}
	return &genai.FunctionCall{
		ID:   toolCalls.ID,
		Name: toolCalls.Function.Name,
		Args: args,
	}, nil
}
