package agent

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TestAgentCallbacks_BeforeAgent(t *testing.T) {
	callbacks := NewAgentCallbacks()

	// Test callback that returns a custom response.
	customResponse := &model.Response{
		ID:      "custom-agent-response",
		Object:  "test",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Custom response from agent callback",
				},
			},
		},
	}

	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return customResponse, false, nil
	})

	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		},
	}

	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err != nil {
		t.Errorf("RunBeforeAgent() error = %v", err)
		return
	}
	if skip {
		t.Error("RunBeforeAgent() should not skip")
	}
	if resp == nil {
		t.Error("RunBeforeAgent() should return custom response")
	}
	if resp.ID != "custom-agent-response" {
		t.Errorf("RunBeforeAgent() returned wrong response ID: %s", resp.ID)
	}
}

func TestAgentCallbacks_BeforeAgentSkip(t *testing.T) {
	callbacks := NewAgentCallbacks()

	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return nil, true, nil
	})

	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		},
	}

	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err != nil {
		t.Errorf("RunBeforeAgent() error = %v", err)
		return
	}
	if !skip {
		t.Error("RunBeforeAgent() should skip")
	}
	if resp != nil {
		t.Error("RunBeforeAgent() should not return response when skipping")
	}
}

func TestAgentCallbacks_AfterAgent(t *testing.T) {
	callbacks := NewAgentCallbacks()

	// Test callback that overrides the response.
	customResponse := &model.Response{
		ID:      "custom-after-response",
		Object:  "test",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Overridden response from after agent callback",
				},
			},
		},
	}

	callbacks.AddAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return customResponse, true, nil
	})

	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		},
	}

	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	if err != nil {
		t.Errorf("RunAfterAgent() error = %v", err)
		return
	}
	if !override {
		t.Error("RunAfterAgent() should override")
	}
	if resp == nil {
		t.Error("RunAfterAgent() should return custom response")
	}
	if resp.ID != "custom-after-response" {
		t.Errorf("RunAfterAgent() returned wrong response ID: %s", resp.ID)
	}
}

func TestAgentCallbacks_MultipleCallbacks(t *testing.T) {
	callbacks := NewAgentCallbacks()

	// Add multiple callbacks - the first one should be called and stop execution.
	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return &model.Response{ID: "first"}, false, nil
	})

	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return &model.Response{ID: "second"}, false, nil
	})

	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		},
	}

	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err != nil {
		t.Errorf("RunBeforeAgent() error = %v", err)
		return
	}
	if skip {
		t.Error("RunBeforeAgent() should not skip")
	}
	if resp == nil {
		t.Error("RunBeforeAgent() should return response")
	}
	if resp.ID != "first" {
		t.Errorf("RunBeforeAgent() should return first callback response, got: %s", resp.ID)
	}
}

func TestAgentCallbacks_ErrorHandling(t *testing.T) {
	callbacks := NewAgentCallbacks()

	callbacks.AddBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return nil, false, context.DeadlineExceeded
	})

	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		},
	}

	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err == nil {
		t.Error("RunBeforeAgent() should return error")
	}
	if resp != nil {
		t.Error("RunBeforeAgent() should not return response when error occurs")
	}
	if skip {
		t.Error("RunBeforeAgent() should not skip when error occurs")
	}
}
