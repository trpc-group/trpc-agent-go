package flow

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestNewSequentialFlow(t *testing.T) {
	name := "test-sequential"
	desc := "Test sequential flow"

	flow1 := NewMockFlow("flow1", "First flow", nil)
	flow2 := NewMockFlow("flow2", "Second flow", nil)

	seqFlow := NewSequentialFlow(name, desc, flow1, flow2)

	if seqFlow.Name() != name {
		t.Errorf("Expected name to be %s, got %s", name, seqFlow.Name())
	}

	if seqFlow.Description() != desc {
		t.Errorf("Expected description to be %s, got %s", desc, seqFlow.Description())
	}

	flows := seqFlow.Flows()
	if len(flows) != 2 {
		t.Errorf("Expected 2 flows, got %d", len(flows))
	}

	if flows[0].Name() != "flow1" {
		t.Errorf("Expected first flow to be 'flow1', got '%s'", flows[0].Name())
	}

	if flows[1].Name() != "flow2" {
		t.Errorf("Expected second flow to be 'flow2', got '%s'", flows[1].Name())
	}
}

func TestSequentialFlow_AddFlow(t *testing.T) {
	seqFlow := NewSequentialFlow("test", "test")

	if len(seqFlow.Flows()) != 0 {
		t.Errorf("Expected 0 flows, got %d", len(seqFlow.Flows()))
	}

	flow1 := NewMockFlow("flow1", "First flow", nil)
	seqFlow.AddFlow(flow1)

	if len(seqFlow.Flows()) != 1 {
		t.Errorf("Expected 1 flow, got %d", len(seqFlow.Flows()))
	}

	flow2 := NewMockFlow("flow2", "Second flow", nil)
	seqFlow.AddFlow(flow2)

	if len(seqFlow.Flows()) != 2 {
		t.Errorf("Expected 2 flows, got %d", len(seqFlow.Flows()))
	}

	if seqFlow.Flows()[0].Name() != "flow1" {
		t.Errorf("Expected first flow to be 'flow1', got '%s'", seqFlow.Flows()[0].Name())
	}

	if seqFlow.Flows()[1].Name() != "flow2" {
		t.Errorf("Expected second flow to be 'flow2', got '%s'", seqFlow.Flows()[1].Name())
	}
}

func TestSequentialFlow_Run_Empty(t *testing.T) {
	seqFlow := NewSequentialFlow("test", "test")

	// With no flows, the input message should be returned unmodified
	input := mockMessage("test-input")
	output, err := seqFlow.Run(context.Background(), input)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if output != input {
		t.Error("Expected output to be the same as input")
	}
}

func TestSequentialFlow_Run_Success(t *testing.T) {
	flow1 := NewMockFlow("flow1", "First flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Return a new message with content "step1"
		return mockMessage("step1"), nil
	})

	flow2 := NewMockFlow("flow2", "Second flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Check that we received the output from flow1
		content := getMockMessageContent(msg)
		if content != "step1" {
			t.Errorf("Expected 'step1', got '%s'", content)
		}

		// Return a new message with content "step2"
		return mockMessage("step2"), nil
	})

	seqFlow := NewSequentialFlow("test", "test", flow1, flow2)

	// Run the sequential flow
	output, err := seqFlow.Run(context.Background(), mockMessage("input"))

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check the final output
	content := getMockMessageContent(output)
	if content != "step2" {
		t.Errorf("Expected 'step2', got '%s'", content)
	}
}

func TestSequentialFlow_Run_Error(t *testing.T) {
	expectedErr := errors.New("flow error")

	flow1 := NewMockFlow("flow1", "First flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Return an error
		return nil, expectedErr
	})

	flow2 := NewMockFlow("flow2", "Second flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// This should not be called
		t.Error("flow2 should not be called")
		return mockMessage("step2"), nil
	})

	seqFlow := NewSequentialFlow("test", "test", flow1, flow2)

	// Run the sequential flow
	_, err := seqFlow.Run(context.Background(), mockMessage("input"))

	if err == nil {
		t.Error("Expected error, got nil")
	}

	// The error should contain the flow name
	if err.Error() == "" || err.Error() == expectedErr.Error() {
		t.Errorf("Error does not contain flow name: %v", err)
	}
}

func TestSequentialFlow_Run_NilMessage(t *testing.T) {
	flow1 := NewMockFlow("flow1", "First flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Return nil message
		return nil, nil
	})

	flow2 := NewMockFlow("flow2", "Second flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// This should not be called
		t.Error("flow2 should not be called")
		return mockMessage("step2"), nil
	})

	seqFlow := NewSequentialFlow("test", "test", flow1, flow2)

	// Run the sequential flow
	_, err := seqFlow.Run(context.Background(), mockMessage("input"))

	if err == nil {
		t.Error("Expected error for nil message, got nil")
	}
}

func TestSequentialFlow_Run_Cancellation(t *testing.T) {
	flow1 := NewMockFlow("flow1", "First flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Simulate a long-running task
			time.Sleep(100 * time.Millisecond)
			return mockMessage("step1"), nil
		}
	})

	flow2 := NewMockFlow("flow2", "Second flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// This should not be called if we cancel before this point
		return mockMessage("step2"), nil
	})

	seqFlow := NewSequentialFlow("test", "test", flow1, flow2)

	// Create a context that will be canceled
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Run the sequential flow
	_, err := seqFlow.Run(ctx, mockMessage("input"))

	if err == nil {
		t.Error("Expected error from cancellation, got nil")
	}

	// The error should be a context error
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestSequentialFlow_RunAsync(t *testing.T) {
	flow1 := NewMockFlow("flow1", "First flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		return mockMessage("step1"), nil
	})

	flow2 := NewMockFlow("flow2", "Second flow", func(ctx context.Context, msg *message.Message) (*message.Message, error) {
		// Simulate a delay
		time.Sleep(50 * time.Millisecond)
		return mockMessage("step2"), nil
	})

	seqFlow := NewSequentialFlow("test", "test", flow1, flow2)

	// Run the sequential flow asynchronously
	respCh, errCh := seqFlow.RunAsync(context.Background(), mockMessage("input"))

	// Wait for the response
	select {
	case resp := <-respCh:
		content := getMockMessageContent(resp)
		if content != "step2" {
			t.Errorf("Expected 'step2', got '%s'", content)
		}
	case err := <-errCh:
		t.Errorf("Unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Error("Timed out waiting for response")
	}
}
