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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// =========================
// BeforeAgent Callback Tests
// =========================

func TestAgentCallbacks_Before_NoCb(t *testing.T) {
	callbacks := NewCallbacks()
	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestAgentCallbacks_Before_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-agent-response"}
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return customResponse, nil
	})
	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
}

func TestAgentCallbacks_Before_Structured(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-agent-response-structured"}
	ctxWithValue := context.WithValue(context.Background(), "user_id", "123")
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		require.Equal(t, "test-invocation", args.Invocation.InvocationID)
		require.Equal(t, "test-agent", args.Invocation.AgentName)
		return &BeforeAgentResult{
			Context:        ctxWithValue,
			CustomResponse: customResponse,
		}, nil
	})
	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
	require.Equal(t, ctxWithValue, result.Context)
}

func TestAgentCallbacks_Before_Err(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return nil, context.DeadlineExceeded
	})
	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, result)
}

func TestAgentCallbacks_Before_Multi(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return nil, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return &model.Response{ID: "second"}, nil
	})
	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, "second", result.CustomResponse.ID)
}

// =========================
// AfterAgent Callback Tests
// =========================

func TestAgentCallbacks_After_NoCb(t *testing.T) {
	callbacks := NewCallbacks()
	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)

	require.Nil(t, result)
}

func TestAgentCallbacks_After_CustomResp(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-after-response"}
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return customResponse, nil
	})
	args := &AfterAgentArgs{
		Invocation: &Invocation{InvocationID: "test-invocation", AgentName: "test-agent", Message: model.Message{Role: model.RoleUser, Content: "Hello"}},
		Error:      nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
}

func TestAgentCallbacks_AfterAgent_Error(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return nil, context.DeadlineExceeded
	})
	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.Error(t, err)

	require.Nil(t, result)
}

func TestAgentCallbacks_After_RunErr(t *testing.T) {
	callbacks := NewCallbacks()
	runError := context.DeadlineExceeded
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		require.Equal(t, runError, runErr)
		return nil, nil
	})
	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: runError,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)

	require.Nil(t, result)
}

func TestAgentCallbacks_After_Multi(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return nil, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error) {
		return &model.Response{ID: "second"}, nil
	})
	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, "second", result.CustomResponse.ID)
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

// =========================
// Structured Callback Tests
// =========================

func TestAgentCallbacks_Structured_Before_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-structured-response"}
	ctxWithValue := context.WithValue(context.Background(), "user_id", "123")

	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		return &BeforeAgentResult{
			Context:        ctxWithValue,
			CustomResponse: customResponse,
		}, nil
	})

	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	result, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
	require.Equal(t, ctxWithValue, result.Context)
}

func TestAgentCallbacks_Structured_After_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &model.Response{ID: "custom-structured-after"}
	ctxWithValue := context.WithValue(context.Background(), "trace_id", "456")

	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		return &AfterAgentResult{
			Context:        ctxWithValue,
			CustomResponse: customResponse,
		}, nil
	})

	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
	require.Equal(t, ctxWithValue, result.Context)
}

func TestAgentCallbacks_Mixed_Callbacks(t *testing.T) {
	// Test mixing old and new callback signatures
	callbacks := NewCallbacks()

	// Register old signature before callback
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, error) {
		return &model.Response{ID: "old-before"}, nil
	})

	// Register new signature after callback
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		return &AfterAgentResult{
			Context:        context.WithValue(ctx, "mixed", true),
			CustomResponse: &model.Response{ID: "new-after"},
		}, nil
	})

	// Test before callback (old signature)
	beforeArgs := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	beforeResult, err := callbacks.RunBeforeAgent(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.NotNil(t, beforeResult)
	require.Equal(t, "old-before", beforeResult.CustomResponse.ID)

	// Test after callback (new signature)
	afterArgs := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	afterResult, err := callbacks.RunAfterAgent(context.Background(), afterArgs)
	require.NoError(t, err)
	require.NotNil(t, afterResult)
	require.Equal(t, "new-after", afterResult.CustomResponse.ID)
	require.Equal(t, true, afterResult.Context.Value("mixed"))
}
