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

	"github.com/stretchr/testify/assert"
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

	resp, err := callbacks.RunBeforeModel(context.Background(), req)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "custom-response", resp.ID)
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

	resp, err := callbacks.RunBeforeModel(context.Background(), req)
	require.NoError(t, err)

	require.Nil(t, resp)
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

	resp, err := callbacks.RunAfterModel(context.Background(), req, originalResponse, nil)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "custom-response", resp.ID)
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

	resp, err := callbacks.RunBeforeModel(context.Background(), req)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "first", resp.ID)
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

	resp, err := callbacks.RunBeforeModel(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)
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

	resp, err := callbacks.RunAfterModel(context.Background(), req, originalResponse, nil)
	require.Error(t, err)
	require.Nil(t, resp)
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

	resp, err := callbacks.RunAfterModel(context.Background(), req, originalResponse, nil)
	require.NoError(t, err)
	require.Nil(t, resp)
}

// TestCallbackMessage_SharedBetweenBeforeAndAfter verifies that the callback
// message created in BeforeModel is the same instance in AfterModel.
func TestCallbackMessage_SharedBetweenBeforeAndAfter(t *testing.T) {
	callbacks := NewCallbacks()
	req := &Request{
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}
	rsp := &Response{
		ID:    "test-response",
		Model: "test-model",
	}

	// Track the message from Before callback.
	var beforeMsg interface{}

	// Register Before callback that stores data in message.
	callbacks.RegisterBeforeModel(func(ctx context.Context, r *Request) (*Response, error) {
		msg := CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in BeforeModel")

		// Store the message for comparison.
		beforeMsg = msg

		// Store test values.
		msg.Set("test_key", "test_value")
		msg.Set("message_count", len(r.Messages))

		// Verify we can retrieve it immediately.
		val, ok := msg.Get("test_key")
		require.True(t, ok, "should be able to get the value we just set")
		require.Equal(t, "test_value", val.(string))

		return nil, nil
	})

	// Register After callback that retrieves data from message.
	callbacks.RegisterAfterModel(func(ctx context.Context, r *Request, resp *Response, modelErr error) (*Response, error) {
		msg := CallbackMessage(ctx)
		require.NotNil(t, msg, "callback message should not be nil in AfterModel")

		// Check if it's the same message instance by comparing pointers.
		assert.Same(t, beforeMsg, msg,
			"callback message in AfterModel should be the same instance as in BeforeModel")

		// Retrieve the value stored in Before callback.
		val, ok := msg.Get("test_key")
		require.True(t, ok, "should be able to get the value set in BeforeModel")
		require.Equal(t, "test_value", val.(string))

		// Verify message_count matches.
		count, ok := msg.Get("message_count")
		require.True(t, ok)
		require.Equal(t, len(r.Messages), count.(int))

		return nil, nil
	})

	// Inject callback message into context (simulating what callLLM() does).
	ctx := WithCallbackMessage(context.Background())

	// Run Before callback.
	_, err := callbacks.RunBeforeModel(ctx, req)
	require.NoError(t, err)

	// Run After callback.
	_, err = callbacks.RunAfterModel(ctx, req, rsp, nil)
	require.NoError(t, err)
}
