//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestLLMAgent_CallbacksStructured_BeforeAgent(t *testing.T) {
	t.Run("before agent callback (structured) returns custom response", func(t *testing.T) {
		customResp := &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "custom response from V2 callback",
					},
				},
			},
		}

		callbacks := agent.NewCallbacksStructured()
		callbacks.RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				return &agent.BeforeAgentResult{
					CustomResponse: customResp,
				}, nil
			},
		)

		m := newDummyModel()
		agt := New("test", WithModel(m), WithAgentCallbacks(callbacks))

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		eventChan, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)

		// Collect events.
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Should have exactly one response event with custom response.
		require.Len(t, events, 1)
		require.NotNil(t, events[0].Response)
		require.NotEmpty(t, events[0].Response.Choices)
		assert.Equal(t, customResp.Choices[0].Message.Content,
			events[0].Response.Choices[0].Message.Content)
	})

	t.Run("before agent callback (structured) returns error", func(t *testing.T) {
		callbacks := agent.NewCallbacksStructured()
		callbacks.RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				return nil, errors.New("callback error")
			},
		)

		m := newDummyModel()
		agt := New("test", WithModel(m), WithAgentCallbacks(callbacks))

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		_, err := agt.Run(context.Background(), inv)
		require.Error(t, err)
	})
}

func TestLLMAgent_CallbacksStructured_AfterAgent(t *testing.T) {
	t.Run("after agent callback (structured) returns custom response", func(t *testing.T) {
		customResp := &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "custom response from V2 after callback",
					},
				},
			},
		}

		callbacks := agent.NewCallbacksStructured()
		callbacks.RegisterAfterAgent(
			func(ctx context.Context, args *agent.AfterAgentArgs) (
				*agent.AfterAgentResult, error,
			) {
				return &agent.AfterAgentResult{
					CustomResponse: customResp,
				}, nil
			},
		)

		m := newDummyModel()
		agt := New("test", WithModel(m), WithAgentCallbacks(callbacks))

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		eventChan, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)

		// Collect events.
		var events []*event.Event
		var lastEvent *event.Event
		for evt := range eventChan {
			events = append(events, evt)
			lastEvent = evt
		}

		// Last event should be the custom response from after callback.
		require.NotNil(t, lastEvent)
		require.NotNil(t, lastEvent.Response)
		require.NotEmpty(t, lastEvent.Response.Choices)
		assert.Equal(t, customResp.Choices[0].Message.Content,
			lastEvent.Response.Choices[0].Message.Content)
	})

	t.Run("after agent callback (structured) returns error", func(t *testing.T) {
		callbacks := agent.NewCallbacksStructured()
		callbacks.RegisterAfterAgent(
			func(ctx context.Context, args *agent.AfterAgentArgs) (
				*agent.AfterAgentResult, error,
			) {
				return nil, errors.New("after callback error")
			},
		)

		m := newDummyModel()
		agt := New("test", WithModel(m), WithAgentCallbacks(callbacks))

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		eventChan, err := agt.Run(context.Background(), inv)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// Collect events.
		var events []*event.Event
		var lastEvent *event.Event
		for evt := range eventChan {
			events = append(events, evt)
			lastEvent = evt
		}

		// Last event should be an error event.
		require.NotNil(t, lastEvent)
		require.NotNil(t, lastEvent.Error)
	})
}

func TestLLMAgent_CallbacksStructured_CoexistWithV1(t *testing.T) {
	t.Run("V1 and V2 callbacks coexist", func(t *testing.T) {
		v1Called := false
		v2Called := false

		callbacksV1 := agent.NewCallbacks()
		callbacksV1.RegisterBeforeAgent(
			func(ctx context.Context, inv *agent.Invocation) (
				*model.Response, error,
			) {
				v1Called = true
				return nil, nil
			},
		)

		callbacksV2 := agent.NewCallbacksStructured()
		callbacksV2.RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				v2Called = true
				return nil, nil
			},
		)

		m := newDummyModel()
		agt := New("test",
			WithModel(m),
			WithAgentCallbacks(callbacksV1),
			WithAgentCallbacks(callbacksV2),
		)

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		eventChan, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)

		// Drain events.
		for range eventChan {
		}

		assert.True(t, v1Called, "V1 callback was not called")
		assert.True(t, v2Called, "V2 callback was not called")
	})

	t.Run("V1 callback returns custom response, V2 not called", func(t *testing.T) {
		v2Called := false

		customResp := &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "custom from V1",
					},
				},
			},
		}

		callbacksV1 := agent.NewCallbacks()
		callbacksV1.RegisterBeforeAgent(
			func(ctx context.Context, inv *agent.Invocation) (
				*model.Response, error,
			) {
				return customResp, nil
			},
		)

		callbacksV2 := agent.NewCallbacksStructured()
		callbacksV2.RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				v2Called = true
				return nil, nil
			},
		)

		m := newDummyModel()
		agt := New("test",
			WithModel(m),
			WithAgentCallbacks(callbacksV1),
			WithAgentCallbacks(callbacksV2),
		)

		inv := agent.NewInvocation(
			agent.WithInvocationID("test-inv"),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: "test input",
			}),
		)

		eventChan, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)

		// Collect events.
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		require.Len(t, events, 1)
		require.NotEmpty(t, events[0].Response.Choices)
		assert.Equal(t, customResp.Choices[0].Message.Content,
			events[0].Response.Choices[0].Message.Content)
		assert.False(t, v2Called,
			"V2 callback should not be called when V1 returns custom response")
	})
}
