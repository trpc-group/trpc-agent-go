//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestCancelQueuedUserMessages_LeavesEnqueueOnlyRunnerCompatible(t *testing.T) {
	r := &enqueueOnlyRunner{}

	err := EnqueueUserMessage(
		r,
		"req-enqueue-only",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.False(t, CancelQueuedUserMessages(r, "req-enqueue-only"))
	require.Len(t, r.messages, 1)
}

func TestRunner_CancelQueuedUserMessages_DiscardsPendingAndKeepsQueueOpen(t *testing.T) {
	const (
		appName        = "runner-steer-cancel"
		userID         = "user-1"
		sessionID      = "session-1"
		requestID      = "req-steer-cancel"
		toolName       = "lookup"
		initialMessage = "Search alpha"
		discardedSteer = "Ignore this queued steer"
		keptSteer      = "Keep this queued steer"
		finalAnswer    = "done"
	)

	type lookupInput struct {
		Topic string `json:"topic"`
	}
	type lookupOutput struct {
		Result string `json:"result"`
	}

	modelStub := &sequentialModel{
		name: "sequential-steer-cancel-model",
		responses: []*model.Response{
			{
				ID:   "resp-tool-call",
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "tool-call-1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      toolName,
								Arguments: []byte(`{"topic":"alpha"}`),
							},
						}},
					},
				}},
			},
			{
				ID:   "resp-final",
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage(finalAnswer),
				}},
			},
		},
	}

	var (
		runnerInstance Runner
		firstErr       error
		secondErr      error
		cancelMissing  bool
		cancelOK       bool
	)

	toolImpl := function.NewFunctionTool(
		func(_ context.Context, input lookupInput) (lookupOutput, error) {
			firstErr = EnqueueUserMessage(
				runnerInstance,
				requestID,
				model.NewUserMessage(discardedSteer),
			)
			cancelMissing = CancelQueuedUserMessages(
				runnerInstance,
				"missing-request",
			)
			cancelOK = CancelQueuedUserMessages(runnerInstance, requestID)
			secondErr = EnqueueUserMessage(
				runnerInstance,
				requestID,
				model.NewUserMessage(keptSteer),
			)
			return lookupOutput{Result: "tool result for " + input.Topic}, nil
		},
		function.WithName(toolName),
		function.WithDescription("Looks up a topic"),
	)

	ag := llmagent.New(
		"steer-cancel-agent",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{toolImpl}),
	)

	runnerInstance = NewRunner(appName, ag)
	events, err := runnerInstance.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage(initialMessage),
		agent.WithRequestID(requestID),
	)
	require.NoError(t, err)

	for range events {
	}

	require.NoError(t, firstErr)
	require.False(t, cancelMissing)
	require.True(t, cancelOK)
	require.NoError(t, secondErr)

	requests := modelStub.Requests()
	require.Len(t, requests, 2)
	secondRequest := requests[1]
	require.NotNil(t, secondRequest)

	discardedIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleUser &&
				message.Content == discardedSteer
		},
	)
	keptIdx := findMessageIndex(
		secondRequest.messages,
		func(message model.Message) bool {
			return message.Role == model.RoleUser &&
				message.Content == keptSteer
		},
	)
	require.Equal(t, -1, discardedIdx)
	require.NotEqual(t, -1, keptIdx)
}

type enqueueOnlyRunner struct {
	messages []model.Message
}

func (r *enqueueOnlyRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event)
	close(events)
	return events, nil
}

func (r *enqueueOnlyRunner) Close() error {
	return nil
}

func (r *enqueueOnlyRunner) EnqueueUserMessage(
	_ string,
	message model.Message,
) error {
	r.messages = append(r.messages, message)
	return nil
}
