//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// =========================
// BeforeAgent Callback Tests
// =========================

func TestAgentCallbacks_Before_NoCb(t *testing.T) {
	callbacks := NewCallbacks()
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.Nil(t, resp)
}

func TestAgentCallbacks_Before_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-agent-response"}
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return customResponse, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, customResponse, resp)
}

func TestAgentCallbacks_Before_Err(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return nil, context.DeadlineExceeded
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestAgentCallbacks_Before_Multi(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return nil, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return &model.Response{ID: "second"}, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "second", resp.ID)
}

// =========================
// AfterAgent Callback Tests
// =========================

func TestAgentCallbacks_After_NoCb(t *testing.T) {
	callbacks := NewCallbacks()
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)

	require.Nil(t, resp)
}

func TestAgentCallbacks_After_CustomResp(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-after-response"}
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return customResponse, nil
	})
	invocation := &Invocation{InvocationID: "test-invocation", AgentName: "test-agent", Message: model.Message{Role: model.RoleUser, Content: "Hello"}}
	resp, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)

	require.Equal(t, customResponse, resp)
}

func TestAgentCallbacks_AfterAgent_Error(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return nil, context.DeadlineExceeded
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.Error(t, err)

	require.Nil(t, resp)
}

func TestAgentCallbacks_After_RunErr(t *testing.T) {
	callbacks := NewCallbacks()
	runError := context.DeadlineExceeded
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		require.Equal(t, runError, runErr)
		return nil, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunAfterAgent(context.Background(), invocation, runError)
	require.NoError(t, err)

	require.Nil(t, resp)
}

func TestAgentCallbacks_After_Multi(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return nil, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return &model.Response{ID: "second"}, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "second", resp.ID)
}

func TestCallbacksChainRegistration(t *testing.T) {
	// Test chain registration.
	callbacks := NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
			return nil, nil
		}).
		RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
			return nil, nil
		})

	// Verify that both callbacks were registered.
	if len(callbacks.BeforeAgent) != 1 {
		t.Errorf("Expected 1 before agent callback, got %d", len(callbacks.BeforeAgent))
	}
	if len(callbacks.AfterAgent) != 1 {
		t.Errorf("Expected 1 after agent callback, got %d", len(callbacks.AfterAgent))
	}
}

// TestCallbackMessage_SharedBetweenBeforeAndAfter verifies that the callback
// message created in BeforeAgent is the same instance in AfterAgent.
func TestCallbackMessage_SharedBetweenBeforeAndAfter(t *testing.T) {
	callbacks := NewCallbacks()
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}

	// Track the message from Before callback.
	var beforeMsg any

	// Register Before callback that stores data in message.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *Invocation) (*model.Response, error) {
		msg := CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in BeforeAgent")

		// Store the message for comparison.
		beforeMsg = msg

		// Store test values.
		msg.Set("test_key", "test_value")
		msg.Set("invocation_id", inv.InvocationID)

		// Verify we can retrieve it immediately.
		val, ok := msg.Get("test_key")
		require.True(t, ok, "should be able to get the value we just set")
		require.Equal(t, "test_value", val.(string))

		return nil, nil
	})

	// Register After callback that retrieves data from message.
	callbacks.RegisterAfterAgent(func(ctx context.Context, inv *Invocation, runErr error) (*model.Response, error) {
		msg := CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in AfterAgent")

		// Check if it's the same message instance by comparing pointers.
		assert.Same(t, beforeMsg, msg,
			"callback message in AfterAgent should be the same instance as in BeforeAgent")

		// Retrieve the value stored in Before callback.
		val, ok := msg.Get("test_key")
		require.True(t, ok, "should be able to get the value set in BeforeAgent")
		require.Equal(t, "test_value", val.(string))

		// Verify invocation_id matches.
		invID, ok := msg.Get("invocation_id")
		require.True(t, ok)
		require.Equal(t, inv.InvocationID, invID.(string))

		return nil, nil
	})

	// Inject callback message into context (simulating what agent.Run() does).
	ctx := WithCallbackMessage(context.Background())

	// Run Before callback.
	_, err := callbacks.RunBeforeAgent(ctx, invocation)
	require.NoError(t, err)

	// Run After callback.
	_, err = callbacks.RunAfterAgent(ctx, invocation, nil)
	require.NoError(t, err)
}
