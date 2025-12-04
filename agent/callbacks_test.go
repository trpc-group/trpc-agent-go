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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
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

// TestAgentCallbacks_ContextPropagation tests that context values set in before
// callbacks can be accessed in after callbacks.
func TestAgentCallbacks_ContextPropagation(t *testing.T) {
	callbacks := NewCallbacks()

	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"

	// Register before callback that sets a context value.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		// Set a value in context.
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &BeforeAgentResult{
			Context: ctxWithValue,
		}, nil
	})

	// Register after callback that reads the context value.
	var capturedValue any
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		// Read the value from context.
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Execute before callback.
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
	require.NotNil(t, beforeResult.Context)

	// Use the context from before callback to run after callback.
	afterArgs := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error: nil,
	}
	_, err = callbacks.RunAfterAgent(beforeResult.Context, afterArgs)
	require.NoError(t, err)

	// Verify that the value was captured in after callback.
	require.Equal(t, testValue, capturedValue)
}

// TestAgentCallbacks_Before_EmptyResult tests that when a callback returns
// an empty result (no Context and no CustomResponse), RunBeforeAgent returns nil.
func TestAgentCallbacks_Before_EmptyResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		// Return empty result (no Context, no CustomResponse).
		return &BeforeAgentResult{}, nil
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
	require.Nil(t, result)
}

// TestAgentCallbacks_Before_NilResult tests that when a callback returns
// nil result, RunBeforeAgent continues to the next callback.
func TestAgentCallbacks_Before_NilResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		// Return nil result.
		return nil, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		// Second callback returns a custom response.
		return &BeforeAgentResult{
			CustomResponse: &model.Response{ID: "second"},
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
	require.Equal(t, "second", result.CustomResponse.ID)
}

// TestAgentCallbacks_After_EmptyResult tests that when a callback returns
// an empty result (no Context and no CustomResponse), RunAfterAgent returns nil.
func TestAgentCallbacks_After_EmptyResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		// Return empty result (no Context, no CustomResponse).
		return &AfterAgentResult{}, nil
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
	require.Nil(t, result)
}

// TestAgentCallbacks_After_NilResult tests that when a callback returns
// nil result, RunAfterAgent continues to the next callback.
func TestAgentCallbacks_After_NilResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		// Return nil result.
		return nil, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		// Second callback returns a custom response.
		return &AfterAgentResult{
			CustomResponse: &model.Response{ID: "second"},
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
	require.Equal(t, "second", result.CustomResponse.ID)
}

// =========================
// ContinueOnError Tests
// =========================

func TestAgentCallbacks_DefaultBehavior_StopOnError(t *testing.T) {
	callbacks := NewCallbacks()
	executed := []int{}

	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 2)
		return nil, nil
	})

	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	_, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1}, executed)
}

func TestAgentCallbacks_ContinueOnError_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 2)
		return nil, errors.New("error 2")
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &BeforeAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
	}
	_, err := callbacks.RunBeforeAgent(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1, 2, 3}, executed)
}

func TestAgentCallbacks_ContinueOnResponse_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 1)
		return &BeforeAgentResult{
			CustomResponse: &model.Response{ID: "response 1"},
		}, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 2)
		return &BeforeAgentResult{
			CustomResponse: &model.Response{ID: "response 2"},
		}, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 3)
		return nil, nil
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
	require.Equal(t, "response 2", result.CustomResponse.ID)
	require.Equal(t, []int{1, 2, 3}, executed)
}

func TestAgentCallbacks_BothOptions_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
		WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 2)
		return &BeforeAgentResult{
			CustomResponse: &model.Response{ID: "response 1"},
		}, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
		executed = append(executed, 3)
		return &BeforeAgentResult{
			CustomResponse: &model.Response{ID: "response 2"},
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
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result)
	require.Equal(t, "response 2", result.CustomResponse.ID)
	require.Equal(t, []int{1, 2, 3}, executed)
}

// TestAgentCallbacks_After_FullResponseEvent tests that FullResponseEvent
// is correctly passed to after agent callbacks.
func TestAgentCallbacks_After_FullResponseEvent(t *testing.T) {
	callbacks := NewCallbacks()
	fullRespEvent := event.NewResponseEvent(
		"test-invocation",
		"test-agent",
		&model.Response{
			ID:   "test-response",
			Done: true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Hello, world!",
					},
				},
			},
		},
	)

	var capturedEvent *event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		capturedEvent = args.FullResponseEvent
		return nil, nil
	})

	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		Error:             nil,
		FullResponseEvent: fullRespEvent,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, capturedEvent)
	require.Equal(t, fullRespEvent, capturedEvent)
	require.Equal(t, "test-response", capturedEvent.Response.ID)
	require.Equal(t, "Hello, world!", capturedEvent.Response.Choices[0].Message.Content)
	require.Nil(t, result)
}

// TestAgentCallbacks_After_FullResponseEvent_Nil tests that FullResponseEvent
// can be nil when no response event is available.
func TestAgentCallbacks_After_FullResponseEvent_Nil(t *testing.T) {
	callbacks := NewCallbacks()
	var capturedEvent *event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		capturedEvent = args.FullResponseEvent
		return nil, nil
	})

	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		FullResponseEvent: nil,
		Error:             nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, capturedEvent)
	require.Nil(t, result)
}

// TestAgentCallbacks_After_FullResponseEvent_WithError tests that FullResponseEvent
// and Error can coexist in AfterAgentArgs.
func TestAgentCallbacks_After_FullResponseEvent_WithError(t *testing.T) {
	callbacks := NewCallbacks()
	fullRespEvent := event.NewResponseEvent(
		"test-invocation",
		"test-agent",
		&model.Response{
			ID:   "test-response",
			Done: true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Error occurred",
					},
				},
			},
			Error: &model.ResponseError{
				Type:    "test_error",
				Message: "test error message",
			},
		},
	)

	var capturedEvent *event.Event
	var capturedError error
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		capturedEvent = args.FullResponseEvent
		capturedError = args.Error
		return nil, nil
	})

	testError := errors.New("agent execution error")
	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		FullResponseEvent: fullRespEvent,
		Error:             testError,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, capturedEvent)
	require.Equal(t, fullRespEvent, capturedEvent)
	require.Equal(t, testError, capturedError)
	require.Nil(t, result)
}

// TestAgentCallbacks_After_FullResponseEvent_MultiCallback tests that FullResponseEvent
// is correctly passed to multiple callbacks.
func TestAgentCallbacks_After_FullResponseEvent_MultiCallback(t *testing.T) {
	callbacks := NewCallbacks()
	fullRespEvent := event.NewResponseEvent(
		"test-invocation",
		"test-agent",
		&model.Response{
			ID:   "test-response",
			Done: true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Multi callback test",
					},
				},
			},
		},
	)

	var capturedEvents []*event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		capturedEvents = append(capturedEvents, args.FullResponseEvent)
		return nil, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
		capturedEvents = append(capturedEvents, args.FullResponseEvent)
		return nil, nil
	})

	args := &AfterAgentArgs{
		Invocation: &Invocation{
			InvocationID: "test-invocation",
			AgentName:    "test-agent",
			Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
		},
		FullResponseEvent: fullRespEvent,
		Error:             nil,
	}
	result, err := callbacks.RunAfterAgent(context.Background(), args)
	require.NoError(t, err)
	require.Len(t, capturedEvents, 2)
	require.Equal(t, fullRespEvent, capturedEvents[0])
	require.Equal(t, fullRespEvent, capturedEvents[1])
	require.Nil(t, result)
}
