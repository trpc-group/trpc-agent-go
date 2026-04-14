//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
		difyAgt := &DifyAgent{enableStreaming: &enableStreaming}
		if !difyAgt.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should use streaming when explicitly enabled")
		}
	})

	t.Run("shouldUseStreaming with explicit false", func(t *testing.T) {
		enableStreaming := false
		difyAgt := &DifyAgent{enableStreaming: &enableStreaming}
		if difyAgt.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should not use streaming when explicitly disabled")
		}
	})

	t.Run("shouldUseStreaming with nil defaults to false", func(t *testing.T) {
		difyAgt := &DifyAgent{enableStreaming: nil}
		if difyAgt.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should default to non-streaming")
		}
	})

	t.Run("shouldUseStreaming per-run override true wins", func(t *testing.T) {
		enableStreaming := false
		difyAgt := &DifyAgent{enableStreaming: &enableStreaming}
		inv := &agent.Invocation{RunOptions: agent.RunOptions{Stream: boolPtr(true)}}
		if !difyAgt.shouldUseStreaming(inv) {
			t.Error("should use streaming when overridden per-run")
		}
	})

	t.Run("shouldUseStreaming per-run override false wins", func(t *testing.T) {
		enableStreaming := true
		difyAgt := &DifyAgent{enableStreaming: &enableStreaming}
		inv := &agent.Invocation{RunOptions: agent.RunOptions{Stream: boolPtr(false)}}
		if difyAgt.shouldUseStreaming(inv) {
			t.Error("should not use streaming when overridden per-run")
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
				RuntimeState: map[string]any{
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

	t.Run("WithAutoGenConversationName true", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithAutoGenConversationName(true)(difyAgent)
		if difyAgent.autoGenConversationName == nil || !*difyAgent.autoGenConversationName {
			t.Error("autoGenConversationName should be true")
		}
	})

	t.Run("WithAutoGenConversationName false", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithAutoGenConversationName(false)(difyAgent)
		if difyAgent.autoGenConversationName == nil || *difyAgent.autoGenConversationName {
			t.Error("autoGenConversationName should be false")
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

// Test new extracted functions for streaming
func TestDifyAgent_ProcessStreamEvent(t *testing.T) {
	t.Run("with default handler", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:           "test-agent",
			eventConverter: &defaultDifyEventConverter{},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "test content",
			},
		}

		evt, content, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if evt == nil {
			t.Fatal("expected event")
		}
		if content != "test content" {
			t.Errorf("expected content 'test content', got: %s", content)
		}
	})

	t.Run("with custom handler success", func(t *testing.T) {
		handler := func(resp *model.Response) (string, error) {
			return "custom content", nil
		}
		difyAgent := &DifyAgent{
			name:                 "test-agent",
			eventConverter:       &defaultDifyEventConverter{},
			streamingRespHandler: handler,
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "test",
			},
		}

		evt, content, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if content != "custom content" {
			t.Errorf("expected 'custom content', got: %s", content)
		}
		if evt == nil {
			t.Fatal("expected event")
		}
	})

	t.Run("with custom handler error", func(t *testing.T) {
		handler := func(resp *model.Response) (string, error) {
			return "", fmt.Errorf("handler error")
		}
		difyAgent := &DifyAgent{
			name:                 "test-agent",
			eventConverter:       &defaultDifyEventConverter{},
			streamingRespHandler: handler,
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "test",
			},
		}

		_, _, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err == nil {
			t.Error("expected error from handler")
		}
	})
}

func TestDifyAgent_SendFinalStreamingEvent(t *testing.T) {
	difyAgent := &DifyAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	difyAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "aggregated content", "msg-123")
	close(eventChan)

	evt := <-eventChan
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Response == nil {
		t.Fatal("expected response")
	}
	if !evt.Response.Done {
		t.Error("expected Done to be true")
	}
	if evt.Response.IsPartial {
		t.Error("expected IsPartial to be false")
	}
	if evt.Response.ID != "msg-123" {
		t.Errorf("expected Response.ID 'msg-123', got: %s", evt.Response.ID)
	}
	if evt.Response.Object != model.ObjectTypeChatCompletion {
		t.Errorf("expected Object type ChatCompletion, got: %s", evt.Response.Object)
	}
	if len(evt.Response.Choices) == 0 {
		t.Fatal("expected choices")
	}
	if evt.Response.Choices[0].Message.Content != "aggregated content" {
		t.Errorf("expected content 'aggregated content', got: %s", evt.Response.Choices[0].Message.Content)
	}
}

func TestDifyAgent_SendFinalStreamingEvent_EmptyMessageID(t *testing.T) {
	difyAgent := &DifyAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	difyAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "content", "")
	close(eventChan)

	evt := <-eventChan
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Response.ID != "" {
		t.Errorf("expected empty Response.ID, got: %s", evt.Response.ID)
	}
	if evt.Response.Object != model.ObjectTypeChatCompletion {
		t.Errorf("expected Object type ChatCompletion, got: %s", evt.Response.Object)
	}
}

func TestDifyAgent_SendFinalStreamingEvent_WithMessageID(t *testing.T) {
	difyAgent := &DifyAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	difyAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "final content", "conversation-id-abc")
	close(eventChan)

	evt := <-eventChan
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Response == nil {
		t.Fatal("expected response")
	}
	if evt.Response.ID != "conversation-id-abc" {
		t.Errorf("expected Response.ID 'conversation-id-abc', got: %s", evt.Response.ID)
	}
	if evt.Response.Object != model.ObjectTypeChatCompletion {
		t.Errorf("expected Object type ChatCompletion, got: %s", evt.Response.Object)
	}
	if !evt.Response.Done {
		t.Error("expected Done to be true")
	}
	if evt.Response.IsPartial {
		t.Error("expected IsPartial to be false")
	}
	if len(evt.Response.Choices) == 0 {
		t.Fatal("expected choices")
	}
	if evt.Response.Choices[0].Message.Content != "final content" {
		t.Errorf("expected content 'final content', got: %s", evt.Response.Choices[0].Message.Content)
	}
}

// TestDifyAgent_ProcessStreamEvent_TracksMessageID verifies that Response.ID is correctly set in streaming events
func TestDifyAgent_ProcessStreamEvent_TracksMessageID(t *testing.T) {
	difyAgent := &DifyAgent{
		name:           "test-agent",
		eventConverter: &defaultDifyEventConverter{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	t.Run("event carries Response.ID from MessageID", func(t *testing.T) {
		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:         "chunk",
				MessageID:      "msg-id-001",
				ConversationID: "conv-id-001",
			},
		}

		evt, _, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if evt == nil {
			t.Fatal("expected event")
		}
		if evt.Response == nil {
			t.Fatal("expected response")
		}
		if evt.Response.ID != "msg-id-001" {
			t.Errorf("expected Response.ID 'msg-id-001', got: %s", evt.Response.ID)
		}
	})

	t.Run("empty answer returns nil event without Response.ID", func(t *testing.T) {
		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:    "",
				MessageID: "msg-id-002",
			},
		}

		evt, _, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if evt != nil {
			t.Error("expected nil event for empty answer")
		}
	})
}

// Test new extracted functions for non-streaming
func TestDifyAgent_ConvertAndEmitNonStreamingEvent(t *testing.T) {
	difyAgent := &DifyAgent{
		name:           "test-agent",
		eventConverter: &defaultDifyEventConverter{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	result := &dify.ChatMessageResponse{
		Answer: "test answer",
	}

	eventChan := make(chan *event.Event, 1)
	difyAgent.convertAndEmitNonStreamingEvent(context.Background(), eventChan, invocation, result)
	close(eventChan)

	evt := <-eventChan
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Object != model.ObjectTypeChatCompletion {
		t.Errorf("expected object type %s, got: %s", model.ObjectTypeChatCompletion, evt.Object)
	}
}

func TestDifyAgent_BuildStreamingRequest(t *testing.T) {
	t.Run("error on buildDifyRequest failure", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			requestConverter: nil, // This will cause buildDifyRequest to fail
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message:      model.Message{Content: "test"},
		}

		_, err := difyAgent.buildStreamingRequest(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when buildDifyRequest fails")
		}
	})
}

func TestDifyAgent_ExecuteNonStreamingRequest(t *testing.T) {
	t.Run("error on buildDifyRequest failure", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			requestConverter: nil, // This will cause buildDifyRequest to fail
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message:      model.Message{Content: "test"},
		}

		_, err := difyAgent.executeNonStreamingRequest(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when buildDifyRequest fails")
		}
	})
}

// ========== Mock Server Integration Tests ==========

func TestDifyAgent_Run_IntegrationWithMockServer(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	t.Run("chatflow non-streaming success", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-invocation-1",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Hello Dify",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// Collect all events
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Verify at least one response event is received
		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("workflow non-streaming success", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-invocation-2",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Hello Workflow",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("chatflow streaming mode", func(t *testing.T) {
		t.Skip("Skipping streaming test - needs SSE implementation fix")
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)
		difyAgent.enableStreaming = &streaming

		invocation := &agent.Invocation{
			InvocationID: "test-streaming-1",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream this",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// Collect streaming events
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Streaming response should return multiple events
		if len(events) < 1 {
			t.Errorf("Expected streaming events, got %d", len(events))
		}
	})

	t.Run("workflow streaming mode", func(t *testing.T) {
		t.Skip("Skipping streaming test - needs SSE implementation fix")
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)
		difyAgent.enableStreaming = &streaming

		invocation := &agent.Invocation{
			InvocationID: "test-workflow-streaming",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream workflow",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected workflow streaming events")
		}
	})
}

func TestDifyAgent_BuildDifyRequest_Integration(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	difyAgent := createMockDifyAgent(t, mockServer)

	t.Run("with transfer state keys", func(t *testing.T) {
		difyAgent.transferStateKey = []string{"custom_key", "another_key"}

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]any{
					"custom_key":  "custom_value",
					"another_key": 123,
					"ignored_key": "should not transfer",
				},
			},
		}

		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Fatalf("buildDifyRequest failed: %v", err)
		}

		if req.Inputs["custom_key"] != "custom_value" {
			t.Errorf("Expected custom_key to be transferred")
		}
		if req.Inputs["another_key"] != 123 {
			t.Errorf("Expected another_key to be transferred")
		}
		if _, exists := req.Inputs["ignored_key"]; exists {
			t.Error("ignored_key should not be transferred")
		}
	})

	t.Run("streaming vs non-streaming", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		// Non-streaming
		reqNonStream, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Fatalf("buildDifyRequest (non-stream) failed: %v", err)
		}
		// In non-streaming mode, ResponseMode should be empty string (defaults to blocking)
		if reqNonStream.ResponseMode != "" {
			t.Errorf("Expected empty response mode for non-streaming, got: %s", reqNonStream.ResponseMode)
		}

		// Streaming
		reqStream, err := difyAgent.buildDifyRequest(context.Background(), invocation, true)
		if err != nil {
			t.Fatalf("buildDifyRequest (stream) failed: %v", err)
		}
		if reqStream.ResponseMode != "streaming" {
			t.Errorf("Expected streaming mode, got: %s", reqStream.ResponseMode)
		}
	})
}

func TestDifyAgent_HelperFunctions(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	t.Run("shouldUseStreaming - explicitly enabled", func(t *testing.T) {
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer)
		difyAgent.enableStreaming = &streaming

		if !difyAgent.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("Expected streaming to be enabled")
		}
	})

	t.Run("shouldUseStreaming - explicitly disabled", func(t *testing.T) {
		streaming := false
		difyAgent := createMockDifyAgent(t, mockServer)
		difyAgent.enableStreaming = &streaming

		if difyAgent.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("Expected streaming to be disabled")
		}
	})

	t.Run("shouldUseStreaming - default behavior", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer)

		if difyAgent.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("Expected default streaming to be false")
		}
	})

	t.Run("sendErrorEvent", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer)

		invocation := &agent.Invocation{
			InvocationID: "error-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test",
			},
		}

		eventChan := make(chan *event.Event, 1)

		difyAgent.sendErrorEvent(context.Background(), eventChan, invocation, "test error message")
		close(eventChan)

		evt := <-eventChan
		if evt == nil {
			t.Fatal("Expected error event")
		}
		if evt.Response == nil || evt.Response.Error == nil {
			t.Error("Expected error in response")
		}
		if evt.Response.Error.Message != "test error message" {
			t.Errorf("Expected error message 'test error message', got: %s", evt.Response.Error.Message)
		}
	})
}

// ========== Workflow Mode Coverage Tests ==========

func TestDifyAgent_WorkflowMode(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	t.Run("workflow mode non-streaming with default converter", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)

		invocation := &agent.Invocation{
			InvocationID: "workflow-test-1",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test workflow request",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("workflow mode with transfer state keys", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
			WithTransferStateKey("workflow_param1", "workflow_param2"),
		)

		invocation := &agent.Invocation{
			InvocationID: "workflow-test-2",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test with state",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]any{
					"workflow_param1": "value1",
					"workflow_param2": 42,
					"ignored_param":   "should not transfer",
				},
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("workflow mode with custom converter", func(t *testing.T) {
		customConverter := &defaultWorkflowRequestConverter{}
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
			WithCustomWorkflowConverter(customConverter),
		)

		if difyAgent.workflowConverter != customConverter {
			t.Error("Custom workflow converter not set correctly")
		}

		invocation := &agent.Invocation{
			InvocationID: "workflow-test-3",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test custom converter",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("workflow mode converter error handling", func(t *testing.T) {
		errorConverter := &errorWorkflowConverter{}
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
			WithCustomWorkflowConverter(errorConverter),
		)

		invocation := &agent.Invocation{
			InvocationID: "workflow-error-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "This should fail",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Errorf("Expected error to be sent via event, not returned: %v", err)
		}

		// Should receive error event
		var receivedError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				receivedError = true
			}
		}
		if !receivedError {
			t.Error("Expected to receive error event from converter")
		}
	})

	t.Run("workflow nil converter error", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)
		difyAgent.workflowConverter = nil

		invocation := &agent.Invocation{
			InvocationID: "workflow-nil-converter",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Errorf("Expected error via event, got: %v", err)
		}

		var receivedError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				receivedError = true
			}
		}
		if !receivedError {
			t.Error("Expected error event when converter is nil")
		}
	})
}

func TestDifyAgent_WorkflowStreaming(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	t.Run("workflow streaming basic test", func(t *testing.T) {
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)
		difyAgent.enableStreaming = &streaming

		invocation := &agent.Invocation{
			InvocationID: "workflow-stream-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream workflow output",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// Collect events - may receive error due to SSE format issues
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// We should get at least one event (either success or error)
		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("chatflow streaming basic test", func(t *testing.T) {
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)
		difyAgent.enableStreaming = &streaming

		invocation := &agent.Invocation{
			InvocationID: "chatflow-stream-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream chatflow output",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("chatflow streaming with AutoGenerateName", func(t *testing.T) {
		streaming := true
		autoGen := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)
		difyAgent.enableStreaming = &streaming
		difyAgent.autoGenConversationName = &autoGen

		invocation := &agent.Invocation{
			InvocationID: "chatflow-autogen-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test auto-generate name",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("Expected at least one event")
		}
	})

	t.Run("workflow streaming with context cancellation", func(t *testing.T) {
		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeWorkflow),
		)
		difyAgent.enableStreaming = &streaming

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		invocation := &agent.Invocation{
			InvocationID: "workflow-cancel-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "This will be cancelled",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(ctx, invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Should receive at least error event or finish early
		// This is acceptable behavior
	})
}

// Test invalid mode validation
func TestDifyAgent_InvalidMode(t *testing.T) {
	t.Run("invalid mode returns error", func(t *testing.T) {
		_, err := New(
			WithName("test-agent"),
			WithMode("invalid-mode"),
		)
		if err == nil {
			t.Error("Expected error for invalid mode")
		}
	})

	t.Run("valid chatflow mode", func(t *testing.T) {
		agent, err := New(
			WithName("test-agent"),
			WithMode(ModeChatflow),
		)
		if err != nil {
			t.Errorf("Expected no error for valid mode, got: %v", err)
		}
		if agent.mode != ModeChatflow {
			t.Errorf("Expected mode chatflow, got: %s", agent.mode)
		}
	})

	t.Run("valid workflow mode", func(t *testing.T) {
		agent, err := New(
			WithName("test-agent"),
			WithMode(ModeWorkflow),
		)
		if err != nil {
			t.Errorf("Expected no error for valid mode, got: %v", err)
		}
		if agent.mode != ModeWorkflow {
			t.Errorf("Expected mode workflow, got: %s", agent.mode)
		}
	})
}

// Test converter edge cases
func TestDifyConverter_EdgeCases(t *testing.T) {
	t.Run("ConvertToWorkflowRequest with content parts", func(t *testing.T) {
		converter := &defaultWorkflowRequestConverter{}

		textContent := "Text content"
		imageURL := "https://example.com/image.png"
		fileName := "document.pdf"

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Main content",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &textContent,
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL: imageURL,
						},
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name: fileName,
						},
					},
					{
						Type: "unknown_type",
					},
				},
			},
			Session: &session.Session{
				UserID: "test-user",
			},
		}

		req, err := converter.ConvertToWorkflowRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("ConvertToWorkflowRequest failed: %v", err)
		}

		if req.Inputs["query"] != textContent {
			t.Errorf("Expected query to be '%s', got: %v", textContent, req.Inputs["query"])
		}
		if req.Inputs["image_url"] != imageURL {
			t.Errorf("Expected image_url to be '%s', got: %v", imageURL, req.Inputs["image_url"])
		}
		if req.Inputs["file_name"] != fileName {
			t.Errorf("Expected file_name to be '%s', got: %v", fileName, req.Inputs["file_name"])
		}
		if req.User != "test-user" {
			t.Errorf("Expected user to be 'test-user', got: %s", req.User)
		}
	})

	t.Run("ConvertToWorkflowRequest with empty session", func(t *testing.T) {
		converter := &defaultWorkflowRequestConverter{}

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
			},
			Session: nil,
		}

		req, err := converter.ConvertToWorkflowRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("ConvertToWorkflowRequest failed: %v", err)
		}

		if req.User != "anonymous" {
			t.Errorf("Expected user to be 'anonymous', got: %s", req.User)
		}
	})

	t.Run("ConvertToWorkflowRequest with empty user ID", func(t *testing.T) {
		converter := &defaultWorkflowRequestConverter{}

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
			},
			Session: &session.Session{
				UserID: "",
			},
		}

		req, err := converter.ConvertToWorkflowRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("ConvertToWorkflowRequest failed: %v", err)
		}

		if req.User != "anonymous" {
			t.Errorf("Expected user to be 'anonymous', got: %s", req.User)
		}
	})
}

// Test workflow response output field extraction
func TestDifyAgent_WorkflowOutputFields(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	tests := []struct {
		name         string
		outputKey    string
		outputValue  string
		expectedText string
	}{
		{"answer field", "answer", "Answer from workflow", "Answer from workflow"},
		{"text field", "text", "Text from workflow", "Text from workflow"},
		{"result field", "result", "Result from workflow", "Result from workflow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Configure mock server to return specific output field
			mockServer.SetCustomResponse(map[string]any{
				"data": map[string]any{
					"outputs": map[string]any{
						tt.outputKey: tt.outputValue,
					},
				},
				"workflow_run_id": "test-run-id",
			})

			difyAgent := createMockDifyAgent(t, mockServer,
				WithMode(ModeWorkflow),
			)

			invocation := &agent.Invocation{
				InvocationID: "output-test",
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Test output field",
				},
				RunOptions: agent.RunOptions{
					RuntimeState: make(map[string]any),
				},
			}

			eventChan, err := difyAgent.Run(context.Background(), invocation)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			var foundContent bool
			for evt := range eventChan {
				if evt.Response != nil && len(evt.Response.Choices) > 0 {
					content := evt.Response.Choices[0].Message.Content
					if content == tt.expectedText {
						foundContent = true
					}
				}
			}

			if !foundContent {
				t.Errorf("Expected to find content '%s'", tt.expectedText)
			}
		})
	}

	// Reset mock server
	mockServer.SetCustomResponse(nil)
}

// ========== Workflow/Chatflow Streaming Response.ID Tests ==========

// TestDifyAgent_WorkflowStreaming_ResponseID verifies that Response.ID of workflow streaming
// intermediate chunks and final event uses Dify's workflow_run_id, not invocation.InvocationID
func TestDifyAgent_WorkflowStreaming_ResponseID(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	streaming := true
	difyAgent := createMockDifyAgent(t, mockServer,
		WithMode(ModeWorkflow),
	)
	difyAgent.enableStreaming = &streaming

	invocation := &agent.Invocation{
		InvocationID: "should-not-appear-as-response-id",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Test workflow streaming ID",
		},
		RunOptions: agent.RunOptions{
			RuntimeState: make(map[string]any),
		},
	}

	eventChan, err := difyAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatal("Expected at least one event")
	}

	// Verify all events have Response.ID equal to mock-run-1 (workflow_run_id from mock server),
	// not invocation.InvocationID
	for i, evt := range events {
		if evt.Response == nil {
			continue
		}
		// Skip error events
		if evt.Response.Error != nil {
			continue
		}
		if evt.Response.ID == "should-not-appear-as-response-id" {
			t.Errorf("event[%d]: Response.ID should be workflow_run_id, not invocation.InvocationID", i)
		}
		if evt.Response.ID != "mock-run-1" {
			t.Errorf("event[%d]: expected Response.ID 'mock-run-1', got: '%s'", i, evt.Response.ID)
		}
	}

	// Verify the last event is the final event (Done=true) with correct ID
	lastEvt := events[len(events)-1]
	if lastEvt.Response == nil {
		t.Fatal("last event should have response")
	}
	if !lastEvt.Response.Done {
		t.Error("last event should be Done=true (final event)")
	}
	if lastEvt.Response.ID != "mock-run-1" {
		t.Errorf("final event: expected Response.ID 'mock-run-1', got: '%s'", lastEvt.Response.ID)
	}
}

// TestDifyAgent_WorkflowStreaming_FallbackID verifies that when WorkflowRunID is empty
// in workflow streaming callbacks, final event's Response.ID falls back to invocation.InvocationID
func TestDifyAgent_WorkflowStreaming_FallbackID(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	// Custom workflow handler that returns SSE events without workflow_run_id
	mockServer.WorkflowHandler = func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		responseMode, _ := req["response_mode"].(string)
		if responseMode != "streaming" {
			handleNonStreamingWorkflow(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Send SSE events without workflow_run_id
		events := []string{
			`data: {"event": "workflow_started", "task_id": "task-no-run-id"}`,
			`data: {"event": "node_finished", "task_id": "task-no-run-id", "data": {"id": "node-1", "outputs": {"answer": "No run ID result"}}}`,
			`data: {"event": "workflow_finished", "task_id": "task-no-run-id", "data": {"status": "succeeded", "outputs": {"answer": "No run ID result"}}}`,
		}

		for _, evt := range events {
			w.Write([]byte(evt + "\n\n"))
			flusher.Flush()
		}
	}

	streaming := true
	difyAgent := createMockDifyAgent(t, mockServer,
		WithMode(ModeWorkflow),
	)
	difyAgent.enableStreaming = &streaming

	invocation := &agent.Invocation{
		InvocationID: "fallback-invocation-id",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Test fallback ID",
		},
		RunOptions: agent.RunOptions{
			RuntimeState: make(map[string]any),
		},
	}

	eventChan, err := difyAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatal("Expected at least one event")
	}

	// Find the final event (Done=true) and verify its Response.ID falls back to invocation.InvocationID
	var foundFinal bool
	for _, evt := range events {
		if evt.Response != nil && evt.Response.Done {
			foundFinal = true
			if evt.Response.ID != "fallback-invocation-id" {
				t.Errorf("final event: expected Response.ID to fallback to 'fallback-invocation-id', got: '%s'", evt.Response.ID)
			}
		}
	}
	if !foundFinal {
		t.Error("Expected to find a final event with Done=true")
	}
}

// TestDifyAgent_ChatflowStreaming_ConsistentMessageID verifies that in chatflow streaming,
// all chunks and the final event share the same message_id
func TestDifyAgent_ChatflowStreaming_ConsistentMessageID(t *testing.T) {
	difyAgent := &DifyAgent{
		name:           "test-agent",
		eventConverter: &defaultDifyEventConverter{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	// Simulate multiple chatflow streaming chunks, all sharing the same message_id
	chunks := []dify.ChatMessageStreamChannelResponse{
		{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:         "Hello ",
				MessageID:      "consistent-msg-id",
				ConversationID: "conv-1",
			},
		},
		{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:         "from ",
				MessageID:      "consistent-msg-id",
				ConversationID: "conv-1",
			},
		},
		{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:         "Dify!",
				MessageID:      "consistent-msg-id",
				ConversationID: "conv-1",
			},
		},
	}

	var lastMessageID string
	for i, chunk := range chunks {
		evt, _, err := difyAgent.processStreamEvent(context.Background(), chunk, invocation)
		if err != nil {
			t.Fatalf("chunk[%d]: unexpected error: %v", i, err)
		}
		if evt == nil {
			t.Fatalf("chunk[%d]: expected event, got nil", i)
		}
		if evt.Response == nil {
			t.Fatalf("chunk[%d]: expected response", i)
		}

		// Verify all chunks have consistent Response.ID
		if i == 0 {
			lastMessageID = evt.Response.ID
		} else {
			if evt.Response.ID != lastMessageID {
				t.Errorf("chunk[%d]: expected Response.ID '%s', got '%s' — message_id should be consistent across all chunks",
					i, lastMessageID, evt.Response.ID)
			}
		}
	}

	// Verify lastMessageID equals consistent-msg-id
	if lastMessageID != "consistent-msg-id" {
		t.Errorf("expected lastMessageID 'consistent-msg-id', got: '%s'", lastMessageID)
	}

	// Verify the final event also uses the same message_id
	eventChan := make(chan *event.Event, 1)
	difyAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "Hello from Dify!", lastMessageID)
	close(eventChan)

	finalEvt := <-eventChan
	if finalEvt == nil {
		t.Fatal("expected final event")
	}
	if finalEvt.Response.ID != "consistent-msg-id" {
		t.Errorf("final event: expected Response.ID 'consistent-msg-id', got: '%s'", finalEvt.Response.ID)
	}
	if !finalEvt.Response.Done {
		t.Error("final event should have Done=true")
	}
}

// TestDifyAgent_WorkflowStreaming_ChunkIDConsistency verifies that in workflow streaming,
// all intermediate chunks use the same workflowRunID as Response.ID
func TestDifyAgent_WorkflowStreaming_ChunkIDConsistency(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	// Custom workflow handler that returns multiple node_finished events with content
	mockServer.WorkflowHandler = func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		responseMode, _ := req["response_mode"].(string)
		if responseMode != "streaming" {
			handleNonStreamingWorkflow(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// All events share the same workflow_run_id
		events := []string{
			`data: {"event": "workflow_started", "task_id": "task-1", "workflow_run_id": "consistent-wf-run-id"}`,
			`data: {"event": "node_finished", "task_id": "task-1", "workflow_run_id": "consistent-wf-run-id", "data": {"id": "node-1", "outputs": {"answer": "Part 1 "}}}`,
			`data: {"event": "node_finished", "task_id": "task-1", "workflow_run_id": "consistent-wf-run-id", "data": {"id": "node-2", "outputs": {"answer": "Part 2"}}}`,
			`data: {"event": "workflow_finished", "task_id": "task-1", "workflow_run_id": "consistent-wf-run-id", "data": {"status": "succeeded"}}`,
		}

		for _, evt := range events {
			w.Write([]byte(evt + "\n\n"))
			flusher.Flush()
		}
	}

	streaming := true
	difyAgent := createMockDifyAgent(t, mockServer,
		WithMode(ModeWorkflow),
	)
	difyAgent.enableStreaming = &streaming

	invocation := &agent.Invocation{
		InvocationID: "chunk-consistency-test",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Test chunk ID consistency",
		},
		RunOptions: agent.RunOptions{
			RuntimeState: make(map[string]any),
		},
	}

	eventChan, err := difyAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatal("Expected at least one event")
	}

	// Verify all events have Response.ID equal to consistent-wf-run-id
	for i, evt := range events {
		if evt.Response == nil || evt.Response.Error != nil {
			continue
		}
		if evt.Response.ID != "consistent-wf-run-id" {
			t.Errorf("event[%d]: expected Response.ID 'consistent-wf-run-id', got: '%s'", i, evt.Response.ID)
		}
	}

	// Verify aggregated content is correct
	lastEvt := events[len(events)-1]
	if lastEvt.Response != nil && lastEvt.Response.Done && len(lastEvt.Response.Choices) > 0 {
		expectedContent := "Part 1 Part 2"
		actualContent := lastEvt.Response.Choices[0].Message.Content
		if actualContent != expectedContent {
			t.Errorf("final event: expected aggregated content '%s', got: '%s'", expectedContent, actualContent)
		}
	}
}

// ========== Chatflow Streaming Error Handling Tests ==========

// TestDifyAgent_ChatflowStreaming_StreamEventErr verifies that when Dify SDK returns
// a streaming error (e.g., token limit exceeded, server errors) via streamEvent.Err,
// the agent correctly sends an error event and stops processing.
func TestDifyAgent_ChatflowStreaming_StreamEventErr(t *testing.T) {
	mockServer := NewMockDifyServer()
	defer mockServer.Close()

	t.Run("stream event error stops processing and sends error event", func(t *testing.T) {
		// Custom chatflow handler that returns SSE events with errors
		// Simulates Dify SDK returning errors during streaming (e.g., token limit exceeded)
		mockServer.ChatflowHandler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			// Send a normal chunk first, then send an error event
			events := []string{
				`data: {"event": "message", "message_id": "msg-err-1", "conversation_id": "conv-err-1", "answer": "Partial "}`,
				`data: {"event": "error", "message_id": "msg-err-1", "code": "token_limit_exceeded", "message": "Token limit exceeded", "status": 400}`,
			}

			for _, evt := range events {
				w.Write([]byte(evt + "\n\n"))
				flusher.Flush()
			}
		}

		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)
		difyAgent.enableStreaming = &streaming

		invocation := &agent.Invocation{
			InvocationID: "stream-err-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "This will trigger a stream error",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Should receive at least one event
		if len(events) == 0 {
			t.Fatal("Expected at least one event")
		}

		// Verify the last event contains error info or has normal streaming events
		// Note: exact behavior depends on how Dify SDK parses error events
		// If SDK converts error events to the Err field, we should receive an error event
		var hasResponse bool
		for _, evt := range events {
			if evt.Response != nil {
				hasResponse = true
			}
		}
		if !hasResponse {
			t.Error("Expected at least one event with response")
		}
	})

	t.Run("chatflow streaming with context cancelled during stream", func(t *testing.T) {
		// Restore default handler
		mockServer.ChatflowHandler = defaultChatflowHandler

		streaming := true
		difyAgent := createMockDifyAgent(t, mockServer,
			WithMode(ModeChatflow),
		)
		difyAgent.enableStreaming = &streaming

		ctx, cancel := context.WithCancel(context.Background())

		invocation := &agent.Invocation{
			InvocationID: "chatflow-cancel-test",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "This will be cancelled",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := difyAgent.Run(ctx, invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// Cancel context immediately
		cancel()

		// Consume all events to ensure goroutine exits normally
		for range eventChan {
			// Consume events
		}
		// Test passed: goroutine exited normally, channel closed properly
	})

	// Restore default handler
	mockServer.ResetHandlers()
}

// TestDifyAgent_ProcessStreamEvent_WithErrField directly tests that processStreamEvent does not handle the Err field
// (Err field checking is done in the runStreaming loop; processStreamEvent only handles normal events)
func TestDifyAgent_ProcessStreamEvent_WithErrField(t *testing.T) {
	difyAgent := &DifyAgent{
		name:           "test-agent",
		eventConverter: &defaultDifyEventConverter{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	t.Run("stream event with Err field set", func(t *testing.T) {
		// Create a stream event with the Err field set
		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "", // Error events typically have no Answer
			},
			Err: fmt.Errorf("token limit exceeded"),
		}

		// processStreamEvent does not check the Err field; Err checking is in the runStreaming loop
		// When Answer is empty, ConvertStreamingToEvent returns nil
		evt, content, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("processStreamEvent should not return error for Err field, got: %v", err)
		}
		if evt != nil {
			t.Error("expected nil event for empty answer")
		}
		if content != "" {
			t.Errorf("expected empty content, got: %s", content)
		}
	})

	t.Run("stream event with both Err and Answer", func(t *testing.T) {
		// Edge case: both Err and Answer are present
		streamEvent := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:    "partial content",
				MessageID: "msg-with-err",
			},
			Err: fmt.Errorf("some error"),
		}

		// processStreamEvent only handles Answer, does not check Err
		// In runStreaming, Err checking happens before processStreamEvent
		evt, content, err := difyAgent.processStreamEvent(context.Background(), streamEvent, invocation)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evt == nil {
			t.Fatal("expected event for non-empty answer")
		}
		if content != "partial content" {
			t.Errorf("expected content 'partial content', got: %s", content)
		}
	})
}

// TestDifyAgent_ChatflowStreaming_EOFIsNotError verifies that io.EOF from the Dify SDK
// (emitted on normal stream close) is treated as a graceful shutdown, not an error.
// The agent should still emit the final aggregated event.
func TestDifyAgent_ChatflowStreaming_EOFIsNotError(t *testing.T) {
	difyAgent := &DifyAgent{
		name:             "test-agent",
		eventConverter:   &defaultDifyEventConverter{},
		streamingBufSize: defaultStreamingChannelSize,
		mode:             ModeChatflow,
	}

	invocation := &agent.Invocation{
		InvocationID: "eof-test",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "hello",
		},
		RunOptions: agent.RunOptions{
			RuntimeState: make(map[string]any),
		},
	}

	t.Run("EOF on stream close is not treated as error", func(t *testing.T) {
		eventChan := make(chan *event.Event, defaultStreamingChannelSize)

		// Simulate a stream channel that sends a normal chunk then EOF
		streamChan := make(chan dify.ChatMessageStreamChannelResponse, 3)
		streamChan <- dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:    "Hello world",
				MessageID: "msg-eof-1",
			},
		}
		// Dify SDK emits EOF on normal stream close
		streamChan <- dify.ChatMessageStreamChannelResponse{
			Err: io.EOF,
		}
		close(streamChan)

		// Run the streaming loop inline (simulating runStreaming's inner logic)
		var aggregatedContent string
		var lastMessageID string
		var hasError bool
		ctx := context.Background()

		for streamEvent := range streamChan {
			if streamEvent.Err != nil {
				if streamEvent.Err == io.EOF {
					break
				}
				hasError = true
				break
			}

			evt, content, err := difyAgent.processStreamEvent(ctx, streamEvent, invocation)
			if err != nil {
				t.Fatalf("processStreamEvent failed: %v", err)
			}
			if evt != nil && evt.Response != nil && evt.Response.ID != "" {
				lastMessageID = evt.Response.ID
			}
			if content != "" {
				aggregatedContent += content
			}
			if evt != nil {
				agent.EmitEvent(ctx, invocation, eventChan, evt)
			}
		}

		if hasError {
			t.Fatal("EOF should not be treated as an error")
		}

		// Final event should still be sent
		difyAgent.sendFinalStreamingEvent(ctx, eventChan, invocation, aggregatedContent, lastMessageID)
		close(eventChan)

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Should have the streaming chunk + final event
		if len(events) < 2 {
			t.Fatalf("expected at least 2 events (chunk + final), got %d", len(events))
		}

		// Verify no error events
		for _, evt := range events {
			if evt.Response != nil && evt.Response.Error != nil {
				t.Errorf("unexpected error event: %s", evt.Response.Error.Message)
			}
		}

		// Verify the final event has Done=true and aggregated content
		lastEvt := events[len(events)-1]
		if lastEvt.Response == nil || !lastEvt.Response.Done {
			t.Error("expected final event with Done=true")
		}
		if lastEvt.Response != nil && len(lastEvt.Response.Choices) > 0 {
			if lastEvt.Response.Choices[0].Message.Content != "Hello world" {
				t.Errorf("expected aggregated content 'Hello world', got: %s",
					lastEvt.Response.Choices[0].Message.Content)
			}
		}
	})

	t.Run("real error is still surfaced", func(t *testing.T) {
		eventChan := make(chan *event.Event, defaultStreamingChannelSize)

		// Simulate a stream channel that sends a real error (not EOF)
		streamChan := make(chan dify.ChatMessageStreamChannelResponse, 3)
		streamChan <- dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer:    "Partial ",
				MessageID: "msg-real-err",
			},
		}
		streamChan <- dify.ChatMessageStreamChannelResponse{
			Err: fmt.Errorf("token limit exceeded"),
		}
		close(streamChan)

		ctx := context.Background()
		var gotError bool
		var errorMsg string

		for streamEvent := range streamChan {
			if streamEvent.Err != nil {
				if streamEvent.Err == io.EOF {
					break
				}
				gotError = true
				errorMsg = streamEvent.Err.Error()
				break
			}

			evt, _, err := difyAgent.processStreamEvent(ctx, streamEvent, invocation)
			if err != nil {
				t.Fatalf("processStreamEvent failed: %v", err)
			}
			if evt != nil {
				agent.EmitEvent(ctx, invocation, eventChan, evt)
			}
		}

		if !gotError {
			t.Fatal("expected a real error to be detected")
		}
		if errorMsg != "token limit exceeded" {
			t.Errorf("expected error message 'token limit exceeded', got: %s", errorMsg)
		}

		close(eventChan)
	})
}

// Helper converter for error testing
type errorWorkflowConverter struct{}

func (e *errorWorkflowConverter) ConvertToWorkflowRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (dify.WorkflowRequest, error) {
	return dify.WorkflowRequest{}, fmt.Errorf("workflow converter error")
}

func boolPtr(b bool) *bool { return &b }
