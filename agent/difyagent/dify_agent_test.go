//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package difyagent

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNew(t *testing.T) {
	t.Run("success with direct agent card", func(t *testing.T) {
		agent, err := New(
			WithName("direct-agent"),
			WithDescription("Direct agent card"),
		)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if agent == nil {
			t.Fatal("expected agent, got nil")
		}
		if agent.name != "direct-agent" {
			t.Errorf("expected name 'direct-agent', got %s", agent.name)
		}
		if agent.description != "Direct agent card" {
			t.Errorf("expected description 'Direct agent card', got %s", agent.description)
		}
	})

	t.Run("error when no agent card", func(t *testing.T) {
		agent, err := New()
		if err == nil {
			t.Error("expected error when no agent card is set")
		}
		if agent != nil {
			t.Error("expected agent to be nil on error")
		}
	})
}

func TestDifyAgent_Info(t *testing.T) {
	agent := &DifyAgent{
		name:        "test-agent",
		description: "test description",
	}

	info := agent.Info()
	if info.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got '%s'", info.Name)
	}
	if info.Description != "test description" {
		t.Errorf("expected description 'test description', got '%s'", info.Description)
	}
}

func TestDifyAgent_Tools(t *testing.T) {
	agent := &DifyAgent{}
	tools := agent.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestDifyAgent_SubAgents(t *testing.T) {
	agent := &DifyAgent{}

	subAgents := agent.SubAgents()
	if len(subAgents) != 0 {
		t.Errorf("expected 0 sub agents, got %d", len(subAgents))
	}

	foundAgent := agent.FindSubAgent("any-name")
	if foundAgent != nil {
		t.Error("expected nil agent")
	}
}

func TestDifyAgent_Streaming(t *testing.T) {
	t.Run("shouldUseStreaming with explicit true", func(t *testing.T) {
		enableStreaming := true
		agent := &DifyAgent{enableStreaming: &enableStreaming}
		if !agent.shouldUseStreaming() {
			t.Error("should use streaming when explicitly enabled")
		}
	})

	t.Run("shouldUseStreaming with explicit false", func(t *testing.T) {
		enableStreaming := false
		agent := &DifyAgent{enableStreaming: &enableStreaming}
		if agent.shouldUseStreaming() {
			t.Error("should not use streaming when explicitly disabled")
		}
	})

	t.Run("shouldUseStreaming with nil defaults to false", func(t *testing.T) {
		agent := &DifyAgent{enableStreaming: nil}
		if agent.shouldUseStreaming() {
			t.Error("should default to non-streaming")
		}
	})
}

func TestDifyAgent_GetClient(t *testing.T) {
	t.Run("uses custom client function", func(t *testing.T) {
		expectedClient := &dify.Client{}
		difyAgent := &DifyAgent{
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return expectedClient, nil
			},
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if client != expectedClient {
			t.Error("should return client from custom function")
		}
	})

	t.Run("creates default client", func(t *testing.T) {
		difyAgent := &DifyAgent{
			baseUrl:   "http://test.com",
			apiSecret: "test-secret",
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if client == nil {
			t.Error("should return a client")
		}
	})

	t.Run("custom function returns error", func(t *testing.T) {
		expectedErr := fmt.Errorf("custom client error")
		difyAgent := &DifyAgent{
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return nil, expectedErr
			},
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != expectedErr {
			t.Errorf("expected custom error, got: %v", err)
		}
		if client != nil {
			t.Error("should not return client on error")
		}
	})
}

func TestDifyAgent_RequestConverter(t *testing.T) {
	t.Run("buildDifyRequest with nil converter returns error", func(t *testing.T) {
		difyAgent := &DifyAgent{
			requestConverter: nil,
		}

		invocation := &agent.Invocation{}
		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err == nil {
			t.Error("expected error when request converter is nil")
		}
		if req != nil {
			t.Error("expected nil request when converter is nil")
		}
	})
}

func TestDifyAgent_ValidateRequestOptions(t *testing.T) {
	difyAgent := &DifyAgent{}
	invocation := &agent.Invocation{}

	err := difyAgent.validateRequestOptions(invocation)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestDifyAgent_BuildDifyRequest(t *testing.T) {
	t.Run("with transfer state keys", func(t *testing.T) {
		converter := &defaultEventDifyConverter{}
		difyAgent := &DifyAgent{
			requestConverter: converter,
			transferStateKey: []string{"key1", "key2"},
		}

		invocation := &agent.Invocation{
			Message: model.Message{
				Content: "test message",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]interface{}{
					"key1": "value1",
					"key2": "value2",
					"key3": "value3", // should not be transferred
				},
			},
		}

		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req == nil {
			t.Fatal("expected request, got nil")
		}
		if req.Inputs["key1"] != "value1" {
			t.Errorf("expected key1 to be value1, got %v", req.Inputs["key1"])
		}
		if req.Inputs["key2"] != "value2" {
			t.Errorf("expected key2 to be value2, got %v", req.Inputs["key2"])
		}
		if _, exists := req.Inputs["key3"]; exists {
			t.Error("key3 should not be transferred")
		}
	})

	t.Run("with nil inputs initialization", func(t *testing.T) {
		customConverter := &customNilInputsConverter{}
		difyAgent := &DifyAgent{
			requestConverter: customConverter,
		}

		invocation := &agent.Invocation{
			Message: model.Message{
				Content: "test",
			},
		}

		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req.Inputs == nil {
			t.Error("inputs should be initialized")
		}
	})

	t.Run("converter returns error", func(t *testing.T) {
		customConverter := &errorRequestConverter{}
		difyAgent := &DifyAgent{
			requestConverter: customConverter,
		}

		invocation := &agent.Invocation{}

		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err == nil {
			t.Error("expected error from converter")
		}
		if req != nil {
			t.Error("expected nil request on error")
		}
	})
}

// Helper converters for testing
type customNilInputsConverter struct{}

func (c *customNilInputsConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	return &dify.ChatMessageRequest{
		Query:  invocation.Message.Content,
		Inputs: nil, // nil inputs to test initialization
	}, nil
}

type errorRequestConverter struct{}

func (e *errorRequestConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	return nil, fmt.Errorf("converter error")
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

func TestDifyAgentOptions(t *testing.T) {
	t.Run("WithStreamingChannelBufSize", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithStreamingChannelBufSize(2048)(difyAgent)
		if difyAgent.streamingBufSize != 2048 {
			t.Errorf("expected streamingBufSize 2048, got %d", difyAgent.streamingBufSize)
		}
	})

	t.Run("WithEnableStreaming true", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithEnableStreaming(true)(difyAgent)
		if difyAgent.enableStreaming == nil || !*difyAgent.enableStreaming {
			t.Error("enableStreaming should be true")
		}
	})

	t.Run("WithEnableStreaming false", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithEnableStreaming(false)(difyAgent)
		if difyAgent.enableStreaming == nil || *difyAgent.enableStreaming {
			t.Error("enableStreaming should be false")
		}
	})

	t.Run("WithTransferStateKey", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithTransferStateKey("key1", "key2")(difyAgent)
		if len(difyAgent.transferStateKey) != 2 {
			t.Errorf("expected 2 transfer keys, got %d", len(difyAgent.transferStateKey))
		}
		if difyAgent.transferStateKey[0] != "key1" || difyAgent.transferStateKey[1] != "key2" {
			t.Error("transfer keys not set correctly")
		}
	})

	t.Run("WithBaseUrl", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithBaseUrl("http://example.com")(difyAgent)
		if difyAgent.baseUrl != "http://example.com" {
			t.Errorf("expected baseUrl 'http://example.com', got %s", difyAgent.baseUrl)
		}
	})

	t.Run("WithCustomEventConverter", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		customConverter := &defaultDifyEventConverter{}
		WithCustomEventConverter(customConverter)(difyAgent)
		if difyAgent.eventConverter != customConverter {
			t.Error("event converter not set correctly")
		}
	})

	t.Run("WithCustomRequestConverter", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		customConverter := &defaultEventDifyConverter{}
		WithCustomRequestConverter(customConverter)(difyAgent)
		if difyAgent.requestConverter != customConverter {
			t.Error("request converter not set correctly")
		}
	})

	t.Run("WithStreamingRespHandler", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		handler := func(resp *model.Response) (string, error) {
			return "test", nil
		}
		WithStreamingRespHandler(handler)(difyAgent)
		if difyAgent.streamingRespHandler == nil {
			t.Error("streaming response handler not set")
		}
	})

	t.Run("WithGetDifyClientFunc", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		clientFunc := func(*agent.Invocation) (*dify.Client, error) {
			return &dify.Client{}, nil
		}
		WithGetDifyClientFunc(clientFunc)(difyAgent)
		if difyAgent.getDifyClientFunc == nil {
			t.Error("getDifyClientFunc not set")
		}
	})
}

func TestDifyAgent_Run(t *testing.T) {
	t.Run("error when getDifyClient fails", func(t *testing.T) {
		expectedErr := fmt.Errorf("client error")
		difyAgent := &DifyAgent{
			name: "test-agent",
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return nil, expectedErr
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != expectedErr {
			t.Errorf("expected error %v, got: %v", expectedErr, err)
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})
}

func TestDifyAgent_RunStreaming_Errors(t *testing.T) {
	t.Run("error when event converter not set", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			eventConverter:   nil, // Not set
			streamingBufSize: 10,
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := difyAgent.runStreaming(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when event converter not set")
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})

	t.Run("error when buildDifyRequest fails", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			eventConverter:   &defaultDifyEventConverter{},
			requestConverter: nil, // Will cause buildDifyRequest to fail
			streamingBufSize: 10,
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := difyAgent.runStreaming(context.Background(), invocation)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Should receive error event
		if eventChan == nil {
			t.Fatal("expected event channel")
		}

		var receivedError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				receivedError = true
				if evt.Response.Error.Message == "" {
					t.Error("expected error message")
				}
			}
		}
		if !receivedError {
			t.Error("expected to receive error event")
		}
	})
}

func TestDifyAgent_RunNonStreaming_Errors(t *testing.T) {
	t.Run("error when buildDifyRequest fails", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			eventConverter:   &defaultDifyEventConverter{},
			requestConverter: nil, // Will cause buildDifyRequest to fail
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := difyAgent.runNonStreaming(context.Background(), invocation)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Should receive error event
		if eventChan == nil {
			t.Fatal("expected event channel")
		}

		var receivedError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				receivedError = true
			}
		}
		if !receivedError {
			t.Error("expected to receive error event")
		}
	})
}

func TestDifyAgent_SendErrorEvent(t *testing.T) {
	difyAgent := &DifyAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	difyAgent.sendErrorEvent(context.Background(), eventChan, invocation, "test error message")
	close(eventChan)

	evt := <-eventChan
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Response == nil {
		t.Fatal("expected response")
	}
	if evt.Response.Error == nil {
		t.Fatal("expected error in response")
	}
	if evt.Response.Error.Message != "test error message" {
		t.Errorf("expected error message 'test error message', got: %s", evt.Response.Error.Message)
	}
	if evt.Author != "test-agent" {
		t.Errorf("expected author 'test-agent', got: %s", evt.Author)
	}
	if evt.InvocationID != "test-inv" {
		t.Errorf("expected invocation ID 'test-inv', got: %s", evt.InvocationID)
	}
}
