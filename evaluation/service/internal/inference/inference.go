//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inference runs agent sessions to generate invocations for evaluation.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	runOptions []agent.RunOption,
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
		responseInvocation, err := inferenceInvocation(ctx, runner, sessionID, initialSession, invocation, runOptions)
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
	runOptions []agent.RunOption,
) (*evalset.Invocation, error) {
	if invocation.UserContent == nil {
		return nil, fmt.Errorf("invocation user content is nil for eval case invocation %q", invocation.InvocationID)
	}
	mergedOpts := make([]agent.RunOption, 0, 1+len(runOptions))
	mergedOpts = append(mergedOpts, runOptions...)
	if initialSession.State != nil {
		mergedOpts = append(mergedOpts, agent.WithRuntimeState(initialSession.State))
	}
	events, err := r.Run(
		ctx,
		initialSession.UserID,
		sessionID,
		*invocation.UserContent,
		mergedOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	// Capture the invocation ID, final response, tool uses, and tool responses.
	var (
		invocationID  string
		finalResponse *model.Message
		tools         = make([]*evalset.Tool, 0)
		toolIDIdx     = make(map[string]int)
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
			toolcalls, err := convertTools(event)
			if err != nil {
				return nil, fmt.Errorf("convert tool call response: %w", err)
			}
			for _, toolcall := range toolcalls {
				tools = append(tools, toolcall)
				toolIDIdx[toolcall.ID] = len(tools) - 1
			}
		}
		// Capture tool call responses.
		if event.IsToolResultResponse() {
			err := mergeToolResultResponse(event, toolIDIdx, tools)
			if err != nil {
				return nil, fmt.Errorf("convert tool result response: %w", err)
			}
		}
	}
	return &evalset.Invocation{
		InvocationID:  invocationID,
		UserContent:   invocation.UserContent,
		FinalResponse: finalResponse,
		Tools:         tools,
	}, nil
}

// convertTools converts the tool call to tools.
func convertTools(event *event.Event) ([]*evalset.Tool, error) {
	tools := []*evalset.Tool{}
	for _, choice := range event.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			tool := &evalset.Tool{
				ID:        toolCall.ID,
				Name:      toolCall.Function.Name,
				Arguments: parseToolCallArguments(toolCall.Function.Arguments),
			}
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

func parseToolCallArguments(arguments []byte) any {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		return value
	}
	return string(arguments)
}

// mergeToolResultResponse merges the tool result response into the tools.
func mergeToolResultResponse(event *event.Event, toolIDIdx map[string]int, tools []*evalset.Tool) error {
	for _, choice := range event.Response.Choices {
		toolID := choice.Message.ToolID
		idx, ok := toolIDIdx[toolID]
		if !ok {
			return fmt.Errorf("tool ID %s not found in tool ID index for tool result response", toolID)
		}
		tools[idx].Result = parseToolResultContent(choice.Message.Content)
	}
	return nil
}

func parseToolResultContent(content string) any {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err == nil {
		return value
	}
	return content
}
