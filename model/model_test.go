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

// TestModelInterface tests the Model interface definition.
func TestModelInterface(t *testing.T) {
	// Test that the interface is properly defined.
	// Actual implementations are tested in their respective packages.

	// Create a mock implementation for testing.
	mock := &mockModel{}
	var _ Model = mock

	// Test with nil request.
	ctx := context.Background()
	responseChan, err := mock.GenerateContent(ctx, nil)

	require.Error(t, err)
	require.Nil(t, responseChan)
}

type requestStructuredOutputPayload struct {
	Answer   string `json:"answer"`
	Optional *int   `json:"optional,omitempty"`
}

func TestRequestOptions_WithStructuredOutputJSON(t *testing.T) {
	req := NewRequest(
		[]Message{NewUserMessage("hello")},
		WithStructuredOutputJSON(
			new(requestStructuredOutputPayload),
			true,
			"Return a typed payload.",
		),
	)

	require.Len(t, req.Messages, 1)
	require.NotNil(t, req.StructuredOutput)
	require.Equal(t, StructuredOutputJSONSchema, req.StructuredOutput.Type)
	require.NotNil(t, req.StructuredOutput.JSONSchema)
	require.Equal(t, "requestStructuredOutputPayload", req.StructuredOutput.JSONSchema.Name)
	require.True(t, req.StructuredOutput.JSONSchema.Strict)
	require.Equal(t, "Return a typed payload.", req.StructuredOutput.JSONSchema.Description)

	schema := req.StructuredOutput.JSONSchema.Schema
	require.Equal(t, "object", schema["type"])
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, properties, "answer")
	require.Contains(t, properties, "optional")
	required, ok := schema["required"].([]string)
	require.True(t, ok)
	require.Contains(t, required, "answer")
	require.Contains(t, required, "optional")
}

func TestRequestOptions_IgnoresNilStructuredOutputInputs(t *testing.T) {
	req := NewRequest(
		nil,
		WithStructuredOutputJSON(nil, true, "ignored"),
	)

	require.Nil(t, req.StructuredOutput)
}

// mockModel is a simple mock implementation for testing the interface.
type mockModel struct{}

func (m *mockModel) Info() Info {
	return Info{
		Name: "mock",
	}
}
func (m *mockModel) GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	// Return a simple mock response.
	responseChan := make(chan *Response, 1)
	responseChan <- &Response{
		ID:    "test-response",
		Model: "test-model",
		Done:  true,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: "Test response",
				},
			},
		},
	}
	close(responseChan)

	return responseChan, nil
}
