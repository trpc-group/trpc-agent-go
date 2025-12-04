//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelCallbacks_BeforeModel(t *testing.T) {
	callbacks := NewCallbacks()

	// Test callback that returns a custom response.
	customResponse := &Response{
		ID:      "custom-response",
		Object:  "test",
		Created: 1234567890,
		Model:   "test-model",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleUser,
					Content: "Custom response from callback",
				},
			},
		},
	}

	callbacks.RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
		return customResponse, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	args := &BeforeModelArgs{Request: req}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, "custom-response", result.CustomResponse.ID)
}

func TestModelCallbacks_BeforeModelSkip(t *testing.T) {
	callbacks := NewCallbacks()

	callbacks.RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
		return nil, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	args := &BeforeModelArgs{Request: req}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)

	require.Nil(t, result)
}

func TestModelCallbacks_AfterModel(t *testing.T) {
	callbacks := NewCallbacks()

	// Test callback that overrides the response.
	customResponse := &Response{
		ID:      "custom-response",
		Object:  "test",
		Created: 1234567890,
		Model:   "test-model",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: "Overridden response from callback",
				},
			},
		},
	}

	callbacks.RegisterAfterModel(func(
		ctx context.Context, req *Request, resp *Response, modelErr error,
	) (*Response, error) {
		return customResponse, nil
	})

	originalResponse := &Response{
		ID:      "original-response",
		Object:  "test",
		Created: 1234567890,
		Model:   "test-model",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: "Original response",
				},
			},
		},
	}

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	args := &AfterModelArgs{
		Request:  req,
		Response: originalResponse,
		Error:    nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, "custom-response", result.CustomResponse.ID)
}

func TestModelCallbacks_Multi(t *testing.T) {
	callbacks := NewCallbacks()

	// Add multiple callbacks - the first one should be called and stop execution.
	callbacks.RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{ID: "first"}, nil
	})

	callbacks.RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{ID: "second"}, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	args := &BeforeModelArgs{Request: req}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)

	require.NotNil(t, result)
	require.Equal(t, "first", result.CustomResponse.ID)
}

func TestCallbacksChainRegistration(t *testing.T) {
	// Test chain registration.
	callbacks := NewCallbacks().
		RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
			return nil, nil
		}).
		RegisterAfterModel(func(ctx context.Context, req *Request, rsp *Response, modelErr error) (*Response, error) {
			return nil, nil
		})

	// Verify that both callbacks were registered.
	if len(callbacks.BeforeModel) != 1 {
		t.Errorf("Expected 1 before model callback, got %d", len(callbacks.BeforeModel))
	}
	if len(callbacks.AfterModel) != 1 {
		t.Errorf("Expected 1 after model callback, got %d", len(callbacks.AfterModel))
	}
}

// TestCallbacks_BeforeModel_WithError tests error handling in before model callbacks.
func TestCallbacks_BeforeModel_WithError(t *testing.T) {
	callbacks := NewCallbacks()

	// Register a callback that returns an error.
	expectedErr := errors.New("test error")
	callbacks.RegisterBeforeModel(func(ctx context.Context, req *Request) (*Response, error) {
		return nil, expectedErr
	})

	req := &Request{
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}

	args := &BeforeModelArgs{Request: req}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, expectedErr, err)
}

// TestCallbacks_AfterModel_WithError tests error handling in after model callbacks.
func TestCallbacks_AfterModel_WithError(t *testing.T) {
	callbacks := NewCallbacks()

	// Register a callback that returns an error.
	expectedErr := errors.New("test error")
	callbacks.RegisterAfterModel(func(
		ctx context.Context, req *Request, rsp *Response, modelErr error,
	) (*Response, error) {
		return nil, expectedErr
	})

	req := &Request{
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}

	originalResponse := &Response{
		ID:    "original-response",
		Model: "test-model",
	}

	args := &AfterModelArgs{
		Request:  req,
		Response: originalResponse,
		Error:    nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, expectedErr, err)
}

// TestCallbacks_AfterModel_PassThrough tests when callbacks return nil (pass through).
func TestCallbacks_AfterModel_PassThrough(t *testing.T) {
	callbacks := NewCallbacks()

	// Register callbacks that don't modify response.
	callbacks.RegisterAfterModel(func(
		ctx context.Context, req *Request, rsp *Response, modelErr error,
	) (*Response, error) {
		return nil, nil
	})

	req := &Request{
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}

	originalResponse := &Response{
		ID:    "original-response",
		Model: "test-model",
	}

	args := &AfterModelArgs{
		Request:  req,
		Response: originalResponse,
		Error:    nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, result)
}

// =========================
// Structured Callback Tests
// =========================

func TestModelCallbacks_Structured_Before_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &Response{ID: "custom-structured-response"}
	ctxWithValue := context.WithValue(context.Background(), "model_id", "123")

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		return &BeforeModelResult{
			Context:        ctxWithValue,
			CustomResponse: customResponse,
		}, nil
	})

	req := &Request{
		Messages: []Message{{Role: RoleUser, Content: "Hello"}},
	}
	args := &BeforeModelArgs{Request: req}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
	require.Equal(t, ctxWithValue, result.Context)
}

func TestModelCallbacks_Structured_After_Custom(t *testing.T) {
	callbacks := NewCallbacks()
	customResponse := &Response{ID: "custom-structured-after"}
	ctxWithValue := context.WithValue(context.Background(), "trace_id", "456")

	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		return &AfterModelResult{
			Context:        ctxWithValue,
			CustomResponse: customResponse,
		}, nil
	})

	req := &Request{Messages: []Message{{Role: RoleUser, Content: "Hello"}}}
	originalResponse := &Response{ID: "original"}
	args := &AfterModelArgs{
		Request:  req,
		Response: originalResponse,
		Error:    nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, customResponse, result.CustomResponse)
	require.Equal(t, ctxWithValue, result.Context)
}

// TestModelCallbacks_ContextPropagation tests that context values set in before
// callbacks can be accessed in after callbacks.
func TestModelCallbacks_ContextPropagation(t *testing.T) {
	callbacks := NewCallbacks()

	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"

	// Register before callback that sets a context value.
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		// Set a value in context.
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &BeforeModelResult{
			Context: ctxWithValue,
		}, nil
	})

	// Register after callback that reads the context value.
	var capturedValue any
	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		// Read the value from context.
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Execute before callback.
	beforeArgs := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
	}
	beforeResult, err := callbacks.RunBeforeModel(context.Background(), beforeArgs)
	require.NoError(t, err)
	require.NotNil(t, beforeResult)
	require.NotNil(t, beforeResult.Context)

	// Use the context from before callback to run after callback.
	afterArgs := &AfterModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
		Response: &Response{
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    RoleAssistant,
						Content: "Hi",
					},
				},
			},
		},
		Error: nil,
	}
	_, err = callbacks.RunAfterModel(beforeResult.Context, afterArgs)
	require.NoError(t, err)

	// Verify that the value was captured in after callback.
	require.Equal(t, testValue, capturedValue)
}

// TestModelCallbacks_Before_EmptyResult tests that when a callback returns
// an empty result (no Context and no CustomResponse), RunBeforeModel returns nil.
func TestModelCallbacks_Before_EmptyResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		// Return empty result (no Context, no CustomResponse).
		return &BeforeModelResult{}, nil
	})
	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestModelCallbacks_Before_NilResult tests that when a callback returns
// nil result, RunBeforeModel continues to the next callback.
func TestModelCallbacks_Before_NilResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		// Return nil result.
		return nil, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		// Second callback returns a custom response.
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "second"},
		}, nil
	})
	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "second", result.CustomResponse.ID)
}

// TestModelCallbacks_After_EmptyResult tests that when a callback returns
// an empty result (no Context and no CustomResponse), RunAfterModel returns nil.
func TestModelCallbacks_After_EmptyResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		// Return empty result (no Context, no CustomResponse).
		return &AfterModelResult{}, nil
	})
	args := &AfterModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
		Response: &Response{
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    RoleAssistant,
						Content: "Hi",
					},
				},
			},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestModelCallbacks_After_NilResult tests that when a callback returns
// nil result, RunAfterModel continues to the next callback.
func TestModelCallbacks_After_NilResult(t *testing.T) {
	callbacks := NewCallbacks()
	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		// Return nil result.
		return nil, nil
	})
	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		// Second callback returns a custom response.
		return &AfterModelResult{
			CustomResponse: &Response{ID: "second"},
		}, nil
	})
	args := &AfterModelArgs{
		Request: &Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "Hello",
				},
			},
		},
		Response: &Response{
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    RoleAssistant,
						Content: "Hi",
					},
				},
			},
		},
		Error: nil,
	}
	result, err := callbacks.RunAfterModel(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "second", result.CustomResponse.ID)
}

// =========================
// ContinueOnError Tests
// =========================

// TestCallbacks_DefaultBehavior_StopOnError tests default behavior (stop on first error).
func TestCallbacks_DefaultBehavior_StopOnError(t *testing.T) {
	callbacks := NewCallbacks()
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return nil, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	_, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1}, executed) // Only first callback executed
}

// TestCallbacks_ContinueOnError_ContinuesExecution tests continueOnError behavior.
func TestCallbacks_ContinueOnError_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return nil, errors.New("error 2")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	_, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())   // Return first error
	require.Equal(t, []int{1, 2, 3}, executed) // All callbacks executed
}

// TestCallbacks_ContinueOnResponse_ContinuesExecution tests continueOnResponse behavior.
func TestCallbacks_ContinueOnResponse_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 1", Choices: []Choice{{Message: Message{Content: "response 1"}}}},
		}, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 2", Choices: []Choice{{Message: Message{Content: "response 2"}}}},
		}, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response 2", result.CustomResponse.ID) // Use last response
	require.Equal(t, []int{1, 2, 3}, executed)               // All callbacks executed
}

// TestCallbacks_BothOptions_ContinuesExecution tests both options enabled.
func TestCallbacks_BothOptions_ContinuesExecution(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
		WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 1", Choices: []Choice{{Message: Message{Content: "response 1"}}}},
		}, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 3)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 2", Choices: []Choice{{Message: Message{Content: "response 2"}}}},
		}, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err) // Return first error
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result)
	require.Equal(t, "response 2", result.CustomResponse.ID) // Use last response
	require.Equal(t, []int{1, 2, 3}, executed)               // All callbacks executed
}

// TestCallbacks_Priority_CustomResponseOverError tests priority rules.
func TestCallbacks_Priority_CustomResponseOverError(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
		WithContinueOnResponse(true),
	)

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "custom", Choices: []Choice{{Message: Message{Content: "custom"}}}},
		}, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err) // Return error (because continueOnError=true)
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result) // But also return CustomResponse
	require.NotNil(t, result.CustomResponse)
}

// TestCallbacks_ContinueOnError_CustomResponseStops tests that CustomResponse stops execution when continueOnResponse=false.
func TestCallbacks_ContinueOnError_CustomResponseStops(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "custom", Choices: []Choice{{Message: Message{Content: "custom"}}}},
		}, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 3)
		return nil, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	result, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err) // Return first error
	require.Equal(t, "error 1", err.Error())
	require.NotNil(t, result)
	require.Equal(t, "custom", result.CustomResponse.ID)
	require.Equal(t, []int{1, 2}, executed) // Third callback not executed
}

// TestCallbacks_ContinueOnResponse_ErrorStops tests that error stops execution when continueOnError=false.
func TestCallbacks_ContinueOnResponse_ErrorStops(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnResponse(true),
	)
	executed := []int{}

	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 1)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 1", Choices: []Choice{{Message: Message{Content: "response 1"}}}},
		}, nil
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 2)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
		executed = append(executed, 3)
		return &BeforeModelResult{
			CustomResponse: &Response{ID: "response 2", Choices: []Choice{{Message: Message{Content: "response 2"}}}},
		}, nil
	})

	args := &BeforeModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
	}
	_, err := callbacks.RunBeforeModel(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1, 2}, executed) // Third callback not executed
}

// TestCallbacks_AfterModel_ContinueOnError tests continueOnError for after model callbacks.
func TestCallbacks_AfterModel_ContinueOnError(t *testing.T) {
	callbacks := NewCallbacks(
		WithContinueOnError(true),
	)
	executed := []int{}

	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		executed = append(executed, 1)
		return nil, errors.New("error 1")
	})
	callbacks.RegisterAfterModel(func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
		executed = append(executed, 2)
		return nil, nil
	})

	args := &AfterModelArgs{
		Request: &Request{
			Messages: []Message{{Role: RoleUser, Content: "Hello"}},
		},
		Response: &Response{ID: "original"},
		Error:    nil,
	}
	_, err := callbacks.RunAfterModel(context.Background(), args)
	require.Error(t, err)
	require.Equal(t, "error 1", err.Error())
	require.Equal(t, []int{1, 2}, executed) // All callbacks executed
}
