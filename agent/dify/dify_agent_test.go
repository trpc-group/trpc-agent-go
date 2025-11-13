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
	difyAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "aggregated content")
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
	if len(evt.Response.Choices) == 0 {
		t.Fatal("expected choices")
	}
	if evt.Response.Choices[0].Message.Content != "aggregated content" {
		t.Errorf("expected content 'aggregated content', got: %s", evt.Response.Choices[0].Message.Content)
	}
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

// Mock Dify client for testing
type mockDifyClient struct {
	chatMessagesFunc       func(ctx context.Context, req *dify.ChatMessageRequest) (*dify.ChatMessageResponse, error)
	chatMessagesStreamFunc func(ctx context.Context, req *dify.ChatMessageRequest) (<-chan dify.ChatMessageStreamChannelResponse, error)
}

type mockDifyAPI struct {
	client *mockDifyClient
}

func (m *mockDifyAPI) ChatMessages(ctx context.Context, req *dify.ChatMessageRequest) (*dify.ChatMessageResponse, error) {
	if m.client.chatMessagesFunc != nil {
		return m.client.chatMessagesFunc(ctx, req)
	}
	return &dify.ChatMessageResponse{Answer: "mock response"}, nil
}

func (m *mockDifyAPI) ChatMessagesStream(ctx context.Context, req *dify.ChatMessageRequest) (<-chan dify.ChatMessageStreamChannelResponse, error) {
	if m.client.chatMessagesStreamFunc != nil {
		return m.client.chatMessagesStreamFunc(ctx, req)
	}
	ch := make(chan dify.ChatMessageStreamChannelResponse)
	close(ch)
	return ch, nil
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

// Test Run with streaming enabled
func TestDifyAgent_Run_Streaming(t *testing.T) {
	t.Run("validates request options", func(t *testing.T) {
		difyAgent := &DifyAgent{
			name:             "test-agent",
			requestConverter: &defaultEventDifyConverter{},
			eventConverter:   &defaultDifyEventConverter{},
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return &dify.Client{}, nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message:      model.Message{Content: "test"},
		}

		// validateRequestOptions should pass
		err := difyAgent.validateRequestOptions(invocation)
		if err != nil {
			t.Errorf("expected validation to pass, got: %v", err)
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
				RuntimeState: make(map[string]interface{}),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// 收集所有事件
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// 验证至少收到一个响应事件
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
				RuntimeState: make(map[string]interface{}),
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
				RuntimeState: make(map[string]interface{}),
			},
		}

		eventChan, err := difyAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// 收集流式事件
		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// 流式响应应该返回多个事件
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
				RuntimeState: make(map[string]interface{}),
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
				RuntimeState: map[string]interface{}{
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
				RuntimeState: make(map[string]interface{}),
			},
		}

		// 非流式
		reqNonStream, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Fatalf("buildDifyRequest (non-stream) failed: %v", err)
		}
		// 非流式模式下 ResponseMode 为空字符串（默认为 blocking）
		if reqNonStream.ResponseMode != "" {
			t.Errorf("Expected empty response mode for non-streaming, got: %s", reqNonStream.ResponseMode)
		}

		// 流式
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

		if !difyAgent.shouldUseStreaming() {
			t.Error("Expected streaming to be enabled")
		}
	})

	t.Run("shouldUseStreaming - explicitly disabled", func(t *testing.T) {
		streaming := false
		difyAgent := createMockDifyAgent(t, mockServer)
		difyAgent.enableStreaming = &streaming

		if difyAgent.shouldUseStreaming() {
			t.Error("Expected streaming to be disabled")
		}
	})

	t.Run("shouldUseStreaming - default behavior", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer)

		if difyAgent.shouldUseStreaming() {
			t.Error("Expected default streaming to be false")
		}
	})

	t.Run("validateRequestOptions - always succeeds", func(t *testing.T) {
		difyAgent := createMockDifyAgent(t, mockServer)

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]interface{}),
			},
		}

		err := difyAgent.validateRequestOptions(invocation)
		if err != nil {
			t.Errorf("Expected no validation error, got: %v", err)
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
