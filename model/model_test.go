package model

import (
	"context"
	"errors"
	"testing"
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

	if err == nil {
		t.Error("Model.GenerateContent() with nil request should return error")
	}
	if responseChan != nil {
		t.Error("Model.GenerateContent() with nil request should return nil channel")
	}
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
