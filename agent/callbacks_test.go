//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// =========================
// BeforeAgent Callback Tests
// =========================

func TestAgentCallbacks_Before_NoCb(t *testing.T) {
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
	callbacks := NewAgentCallbacks()
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
