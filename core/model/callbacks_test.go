package model

import (
	"context"
	"testing"
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

	callbacks.AddBeforeModel(func(ctx context.Context, req *Request) (*Response, bool, error) {
		return customResponse, false, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	resp, skip, err := callbacks.RunBeforeModel(context.Background(), req)
	if err != nil {
		t.Errorf("RunBeforeModel() error = %v", err)
		return
	}
	if skip {
		t.Error("RunBeforeModel() should not skip")
	}
	if resp == nil {
		t.Error("RunBeforeModel() should return custom response")
	}
	if resp.ID != "custom-response" {
		t.Errorf("RunBeforeModel() returned wrong response ID: %s", resp.ID)
	}
}

func TestModelCallbacks_BeforeModelSkip(t *testing.T) {
	callbacks := NewModelCallbacks()

	callbacks.AddBeforeModel(func(ctx context.Context, req *Request) (*Response, bool, error) {
		return nil, true, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	resp, skip, err := callbacks.RunBeforeModel(context.Background(), req)
	if err != nil {
		t.Errorf("RunBeforeModel() error = %v", err)
		return
	}
	if !skip {
		t.Error("RunBeforeModel() should skip")
	}
	if resp != nil {
		t.Error("RunBeforeModel() should not return response when skipping")
	}
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

	callbacks.AddAfterModel(func(ctx context.Context, resp *Response, modelErr error) (*Response, bool, error) {
		return customResponse, true, nil
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

	resp, override, err := callbacks.RunAfterModel(context.Background(), originalResponse, nil)
	if err != nil {
		t.Errorf("RunAfterModel() error = %v", err)
		return
	}
	if !override {
		t.Error("RunAfterModel() should override")
	}
	if resp == nil {
		t.Error("RunAfterModel() should return custom response")
	}
	if resp.ID != "custom-response" {
		t.Errorf("RunAfterModel() returned wrong response ID: %s", resp.ID)
	}
}

func TestModelCallbacks_MultipleCallbacks(t *testing.T) {
	callbacks := NewModelCallbacks()

	// Add multiple callbacks - the first one should be called and stop execution.
	callbacks.AddBeforeModel(func(ctx context.Context, req *Request) (*Response, bool, error) {
		return &Response{ID: "first"}, false, nil
	})

	callbacks.AddBeforeModel(func(ctx context.Context, req *Request) (*Response, bool, error) {
		return &Response{ID: "second"}, false, nil
	})

	req := &Request{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
		},
	}

	resp, skip, err := callbacks.RunBeforeModel(context.Background(), req)
	if err != nil {
		t.Errorf("RunBeforeModel() error = %v", err)
		return
	}
	if skip {
		t.Error("RunBeforeModel() should not skip")
	}
	if resp == nil {
		t.Error("RunBeforeModel() should return response")
	}
	if resp.ID != "first" {
		t.Errorf("RunBeforeModel() should return first callback response, got: %s", resp.ID)
	}
}
 