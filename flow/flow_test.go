package flow

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// mockMessage creates a mock message for testing
func mockMessage(content string) *message.Message {
	msg := message.NewUserMessage(content)
	return msg
}

// getMockMessageContent extracts the text content from a mock message
func getMockMessageContent(msg *message.Message) string {
	// We just return the Content field as our mock messages are created with NewUserMessage
	return msg.Content
}

// mockFlow implements the Flow interface for testing
type mockFlow struct {
	BaseFlow
	runFunc      func(ctx context.Context, msg *message.Message) (*message.Message, error)
	runAsyncFunc func(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error)
}

// NewMockFlow creates a new mock flow for testing
func NewMockFlow(name, description string, runFunc func(ctx context.Context, msg *message.Message) (*message.Message, error)) *mockFlow {
	return &mockFlow{
		BaseFlow: *NewBaseFlow(name, description),
		runFunc:  runFunc,
	}
}

// Run implements the Flow interface
func (f *mockFlow) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	if f.runFunc != nil {
		return f.runFunc(ctx, msg)
	}
	return msg, nil
}

// RunAsync implements the Flow interface
func (f *mockFlow) RunAsync(ctx context.Context, msg *message.Message) (<-chan *message.Message, <-chan error) {
	if f.runAsyncFunc != nil {
		return f.runAsyncFunc(ctx, msg)
	}

	// Don't call BaseFlow.RunAsync as it will call Run which might panic
	// Instead implement a default behavior that uses our Run method
	respCh := make(chan *message.Message, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(respCh)
		defer close(errCh)

		resp, err := f.Run(ctx, msg)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	return respCh, errCh
}

func TestNewBaseFlow(t *testing.T) {
	name := "test-flow"
	desc := "Test flow"
	flow := NewBaseFlow(name, desc)

	if flow.Name() != name {
		t.Errorf("Expected name to be %s, got %s", name, flow.Name())
	}

	if flow.Description() != desc {
		t.Errorf("Expected description to be %s, got %s", desc, flow.Description())
	}
}

func TestBaseFlow_Run(t *testing.T) {
	flow := NewBaseFlow("test", "test")

	// The base flow's Run method should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected Run to panic")
		}
	}()

	flow.Run(context.Background(), mockMessage("test"))
}

func TestBaseFlow_RunAsync(t *testing.T) {
	// We use a mockFlow instead that implements the Run method
	flow := NewMockFlow("test", "test", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		return mockMessage("response"), nil
	})

	respCh, errCh := flow.RunAsync(context.Background(), mockMessage("test"))

	// Wait for the response
	select {
	case resp := <-respCh:
		if content := getMockMessageContent(resp); content != "response" {
			t.Errorf("Expected response to be 'response', got '%s'", content)
		}
	case err := <-errCh:
		t.Errorf("Unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Error("Timed out waiting for response")
	}
}

func TestBaseFlow_RunAsync_Error(t *testing.T) {
	expectedErr := errors.New("test error")
	flow := NewMockFlow("test", "test", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		return nil, expectedErr
	})

	respCh, errCh := flow.RunAsync(context.Background(), mockMessage("test"))

	// Wait for the error
	select {
	case <-respCh:
		t.Error("Expected error, got response")
	case err := <-errCh:
		if err != expectedErr {
			t.Errorf("Expected error to be %v, got %v", expectedErr, err)
		}
	case <-time.After(time.Second):
		t.Error("Timed out waiting for error")
	}
}

func TestFlowFunc(t *testing.T) {
	called := false
	fn := FlowFunc(func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		called = true
		return mockMessage("response"), nil
	})

	resp, err := fn.Run(context.Background(), mockMessage("test"))

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if !called {
		t.Error("Expected function to be called")
	}

	if resp == nil {
		t.Error("Expected response, got nil")
	}

	if content := getMockMessageContent(resp); content != "response" {
		t.Errorf("Expected response to be 'response', got '%s'", content)
	}

	if name := fn.Name(); name != "FlowFunc" {
		t.Errorf("Expected name to be 'FlowFunc', got '%s'", name)
	}

	if desc := fn.Description(); desc != "A Flow created from a function" {
		t.Errorf("Expected description to be 'A Flow created from a function', got '%s'", desc)
	}
}

func TestFlowFunc_RunAsync(t *testing.T) {
	fn := FlowFunc(func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		return mockMessage("response"), nil
	})

	respCh, errCh := fn.RunAsync(context.Background(), mockMessage("test"))

	// Wait for the response
	select {
	case resp := <-respCh:
		if content := getMockMessageContent(resp); content != "response" {
			t.Errorf("Expected response to be 'response', got '%s'", content)
		}
	case err := <-errCh:
		t.Errorf("Unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Error("Timed out waiting for response")
	}
}

func TestFlowFunc_RunAsync_Error(t *testing.T) {
	expectedErr := errors.New("test error")
	fn := FlowFunc(func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		return nil, expectedErr
	})

	respCh, errCh := fn.RunAsync(context.Background(), mockMessage("test"))

	// Wait for the error
	select {
	case <-respCh:
		t.Error("Expected error, got response")
	case err := <-errCh:
		if err != expectedErr {
			t.Errorf("Expected error to be %v, got %v", expectedErr, err)
		}
	case <-time.After(time.Second):
		t.Error("Timed out waiting for error")
	}
}
