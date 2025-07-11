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

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelCallbacks_BeforeModel(t *testing.T) {
	callbacks := NewModelCallbacks()

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
	callbacks := NewModelCallbacks()

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
	callbacks := NewModelCallbacks()

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

	callbacks.RegisterAfterModel(func(ctx context.Context, resp *Response, modelErr error) (*Response, error) {
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

	resp, err := callbacks.RunAfterModel(context.Background(), originalResponse, nil)
	require.NoError(t, err)

	require.NotNil(t, resp)
	require.Equal(t, "custom-response", resp.ID)
}

func TestModelCallbacks_Multi(t *testing.T) {
	callbacks := NewModelCallbacks()

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
