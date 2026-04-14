//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package n8n

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		a, err := New(
			WithName("my-agent"),
			WithWebhookURL("http://localhost:5678/webhook/test"),
		)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if a == nil {
			t.Fatal("expected agent, got nil")
		}
		if a.name != "my-agent" {
			t.Errorf("expected name 'my-agent', got '%s'", a.name)
		}
		if a.webhookURL != "http://localhost:5678/webhook/test" {
			t.Errorf("expected webhookURL set, got '%s'", a.webhookURL)
		}
	})

	t.Run("error no name", func(t *testing.T) {
		a, err := New(
			WithWebhookURL("http://localhost:5678/webhook/test"),
		)
		if err == nil {
			t.Error("expected error when no name is set")
		}
		if a != nil {
			t.Error("expected nil agent on error")
		}
	})

	t.Run("error no webhookURL", func(t *testing.T) {
		a, err := New(
			WithName("my-agent"),
		)
		if err == nil {
			t.Error("expected error when no webhookURL is set")
		}
		if a != nil {
			t.Error("expected nil agent on error")
		}
	})
}

func TestN8nAgent_Info(t *testing.T) {
	a := &N8nAgent{
		name:        "test-agent",
		description: "test description",
	}

	info := a.Info()
	if info.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got '%s'", info.Name)
	}
	if info.Description != "test description" {
		t.Errorf("expected description 'test description', got '%s'", info.Description)
	}
}

func TestN8nAgent_Tools(t *testing.T) {
	a := &N8nAgent{}
	tools := a.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestN8nAgent_SubAgents(t *testing.T) {
	a := &N8nAgent{}

	subAgents := a.SubAgents()
	if len(subAgents) != 0 {
		t.Errorf("expected 0 sub agents, got %d", len(subAgents))
	}

	foundAgent := a.FindSubAgent("any-name")
	if foundAgent != nil {
		t.Error("expected nil agent")
	}
}

func TestN8nAgent_Streaming(t *testing.T) {
	t.Run("shouldUseStreaming with explicit true", func(t *testing.T) {
		enableStreaming := true
		a := &N8nAgent{enableStreaming: &enableStreaming}
		if !a.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should use streaming when explicitly enabled")
		}
	})

	t.Run("shouldUseStreaming with explicit false", func(t *testing.T) {
		enableStreaming := false
		a := &N8nAgent{enableStreaming: &enableStreaming}
		if a.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should not use streaming when explicitly disabled")
		}
	})

	t.Run("shouldUseStreaming with nil defaults to false", func(t *testing.T) {
		a := &N8nAgent{enableStreaming: nil}
		if a.shouldUseStreaming(&agent.Invocation{}) {
			t.Error("should default to non-streaming")
		}
	})

	t.Run("shouldUseStreaming per-run override true wins", func(t *testing.T) {
		enableStreaming := false
		a := &N8nAgent{enableStreaming: &enableStreaming}
		inv := &agent.Invocation{RunOptions: agent.RunOptions{Stream: boolPtr(true)}}
		if !a.shouldUseStreaming(inv) {
			t.Error("should use streaming when overridden per-run")
		}
	})

	t.Run("shouldUseStreaming per-run override false wins", func(t *testing.T) {
		enableStreaming := true
		a := &N8nAgent{enableStreaming: &enableStreaming}
		inv := &agent.Invocation{RunOptions: agent.RunOptions{Stream: boolPtr(false)}}
		if a.shouldUseStreaming(inv) {
			t.Error("should not use streaming when overridden per-run")
		}
	})
}

func TestN8nAgent_GetHTTPClient(t *testing.T) {
	t.Run("uses custom function", func(t *testing.T) {
		expectedClient := &http.Client{}
		a := &N8nAgent{
			getHTTPClientFunc: func(*agent.Invocation) (*http.Client, error) {
				return expectedClient, nil
			},
		}

		client, err := a.getHTTPClient(&agent.Invocation{})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if client != expectedClient {
			t.Error("should return client from custom function")
		}
	})

	t.Run("uses static client", func(t *testing.T) {
		expectedClient := &http.Client{}
		a := &N8nAgent{
			httpClient: expectedClient,
		}

		client, err := a.getHTTPClient(&agent.Invocation{})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if client != expectedClient {
			t.Error("should return static client")
		}
	})

	t.Run("returns default client", func(t *testing.T) {
		a, err := New(WithName("test"), WithWebhookURL("http://example.com"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		client, err := a.getHTTPClient(&agent.Invocation{})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if client == nil {
			t.Error("should return a default client")
		}
	})

	t.Run("custom function returns error", func(t *testing.T) {
		expectedErr := fmt.Errorf("custom client error")
		a := &N8nAgent{
			getHTTPClientFunc: func(*agent.Invocation) (*http.Client, error) {
				return nil, expectedErr
			},
		}

		client, err := a.getHTTPClient(&agent.Invocation{})
		if err != expectedErr {
			t.Errorf("expected custom error, got: %v", err)
		}
		if client != nil {
			t.Error("should not return client on error")
		}
	})
}

func TestN8nAgent_SendErrorEvent(t *testing.T) {
	a := &N8nAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	a.sendErrorEvent(context.Background(), eventChan, invocation, "test error message")
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

func TestN8nAgent_SendFinalStreamingEvent(t *testing.T) {
	a := &N8nAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	a.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "aggregated content")
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
	if evt.Response.Choices[0].Message.Role != model.RoleAssistant {
		t.Errorf("expected role assistant, got: %s", evt.Response.Choices[0].Message.Role)
	}
}

func TestN8nAgentOptions(t *testing.T) {
	t.Run("WithName", func(t *testing.T) {
		a := &N8nAgent{}
		WithName("my-agent")(a)
		if a.name != "my-agent" {
			t.Errorf("expected name 'my-agent', got '%s'", a.name)
		}
	})

	t.Run("WithWebhookURL", func(t *testing.T) {
		a := &N8nAgent{}
		WithWebhookURL("http://example.com/webhook")(a)
		if a.webhookURL != "http://example.com/webhook" {
			t.Errorf("expected webhookURL set, got '%s'", a.webhookURL)
		}
	})

	t.Run("WithDescription", func(t *testing.T) {
		a := &N8nAgent{}
		WithDescription("my description")(a)
		if a.description != "my description" {
			t.Errorf("expected description 'my description', got '%s'", a.description)
		}
	})

	t.Run("WithEnableStreaming true", func(t *testing.T) {
		a := &N8nAgent{}
		WithEnableStreaming(true)(a)
		if a.enableStreaming == nil || !*a.enableStreaming {
			t.Error("enableStreaming should be true")
		}
	})

	t.Run("WithEnableStreaming false", func(t *testing.T) {
		a := &N8nAgent{}
		WithEnableStreaming(false)(a)
		if a.enableStreaming == nil || *a.enableStreaming {
			t.Error("enableStreaming should be false")
		}
	})

	t.Run("WithStreamingChannelBufSize", func(t *testing.T) {
		a := &N8nAgent{}
		WithStreamingChannelBufSize(2048)(a)
		if a.streamingBufSize != 2048 {
			t.Errorf("expected streamingBufSize 2048, got %d", a.streamingBufSize)
		}
	})

	t.Run("WithStreamingRespHandler", func(t *testing.T) {
		a := &N8nAgent{}
		handler := func(resp *model.Response) (string, error) {
			return "test", nil
		}
		WithStreamingRespHandler(handler)(a)
		if a.streamingRespHandler == nil {
			t.Error("streaming response handler not set")
		}
	})

	t.Run("WithAuthType", func(t *testing.T) {
		a := &N8nAgent{}
		WithAuthType(AuthBasic)(a)
		if a.authType != AuthBasic {
			t.Errorf("expected authType AuthBasic, got '%s'", a.authType)
		}
	})

	t.Run("WithAuthConfig", func(t *testing.T) {
		a := &N8nAgent{}
		config := &AuthConfig{Username: "user", Password: "pass"}
		WithAuthConfig(config)(a)
		if a.authConfig != config {
			t.Error("authConfig not set correctly")
		}
	})

	t.Run("WithHTTPClient", func(t *testing.T) {
		a := &N8nAgent{}
		client := &http.Client{}
		WithHTTPClient(client)(a)
		if a.httpClient != client {
			t.Error("httpClient not set correctly")
		}
	})

	t.Run("WithGetHTTPClientFunc", func(t *testing.T) {
		a := &N8nAgent{}
		fn := func(*agent.Invocation) (*http.Client, error) {
			return &http.Client{}, nil
		}
		WithGetHTTPClientFunc(fn)(a)
		if a.getHTTPClientFunc == nil {
			t.Error("getHTTPClientFunc not set")
		}
	})

	t.Run("WithTransferStateKey", func(t *testing.T) {
		a := &N8nAgent{}
		WithTransferStateKey("key1", "key2")(a)
		if len(a.transferStateKey) != 2 {
			t.Errorf("expected 2 transfer keys, got %d", len(a.transferStateKey))
		}
		if a.transferStateKey[0] != "key1" || a.transferStateKey[1] != "key2" {
			t.Error("transfer keys not set correctly")
		}
	})

	t.Run("WithCustomRequestConverter", func(t *testing.T) {
		a := &N8nAgent{}
		converter := &defaultRequestConverter{}
		WithCustomRequestConverter(converter)(a)
		if a.requestConverter != converter {
			t.Error("request converter not set correctly")
		}
	})

	t.Run("WithCustomResponseConverter", func(t *testing.T) {
		a := &N8nAgent{}
		converter := &defaultResponseConverter{}
		WithCustomResponseConverter(converter)(a)
		if a.responseConverter != converter {
			t.Error("response converter not set correctly")
		}
	})
}

func TestN8nAgent_Run_NonStreaming(t *testing.T) {
	mockServer := NewMockN8nServer()
	defer mockServer.Close()

	t.Run("success", func(t *testing.T) {
		a := createMockN8nAgent(t, mockServer)

		invocation := &agent.Invocation{
			InvocationID: "test-inv-1",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Hello n8n",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Fatal("expected at least one event")
		}

		// Verify response content
		lastEvt := events[len(events)-1]
		if lastEvt.Response == nil {
			t.Fatal("expected response")
		}
		if len(lastEvt.Response.Choices) == 0 {
			t.Fatal("expected choices")
		}
		content := lastEvt.Response.Choices[0].Message.Content
		if content != "This is a mock response from n8n webhook" {
			t.Errorf("expected mock response content, got: %s", content)
		}
	})

	t.Run("error response", func(t *testing.T) {
		mockServer.WithError(http.StatusInternalServerError, "internal server error")
		defer mockServer.ResetHandlers()

		a := createMockN8nAgent(t, mockServer)

		invocation := &agent.Invocation{
			InvocationID: "test-inv-err",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "This should fail",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run should not return error directly: %v", err)
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

	t.Run("with transfer state keys", func(t *testing.T) {
		a := createMockN8nAgent(t, mockServer,
			WithTransferStateKey("room_id", "user_context"),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-inv-state",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test with state",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]any{
					"room_id":      "room-123",
					"user_context": "context-456",
					"ignored_key":  "should not transfer",
				},
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) == 0 {
			t.Error("expected at least one event")
		}
	})
}

func TestN8nAgent_Run_Streaming(t *testing.T) {
	mockServer := NewMockN8nServer()
	defer mockServer.Close()

	t.Run("success", func(t *testing.T) {
		a := createMockN8nAgent(t, mockServer,
			WithEnableStreaming(true),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-stream-1",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream me",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		// Should get streaming chunk events + final event
		if len(events) < 2 {
			t.Fatalf("expected at least 2 events (chunks + final), got %d", len(events))
		}

		// The last event should be the final aggregated event
		lastEvt := events[len(events)-1]
		if lastEvt.Response == nil {
			t.Fatal("expected response in final event")
		}
		if !lastEvt.Response.Done {
			t.Error("expected final event to be done")
		}
		if len(lastEvt.Response.Choices) == 0 {
			t.Fatal("expected choices in final event")
		}
		aggregated := lastEvt.Response.Choices[0].Message.Content
		if aggregated != "Hello from n8n!" {
			t.Errorf("expected aggregated content 'Hello from n8n!', got: '%s'", aggregated)
		}
	})

	t.Run("streaming with custom handler", func(t *testing.T) {
		handler := func(resp *model.Response) (string, error) {
			if len(resp.Choices) > 0 {
				return "[" + resp.Choices[0].Delta.Content + "]", nil
			}
			return "", nil
		}

		a := createMockN8nAgent(t, mockServer,
			WithEnableStreaming(true),
			WithStreamingRespHandler(handler),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-stream-custom",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream with handler",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var events []*event.Event
		for evt := range eventChan {
			events = append(events, evt)
		}

		if len(events) < 2 {
			t.Fatalf("expected at least 2 events, got %d", len(events))
		}

		// The final event should have the custom handler's aggregation
		lastEvt := events[len(events)-1]
		if lastEvt.Response == nil || len(lastEvt.Response.Choices) == 0 {
			t.Fatal("expected final event with choices")
		}
		aggregated := lastEvt.Response.Choices[0].Message.Content
		if aggregated != "[Hello ][from ][n8n!]" {
			t.Errorf("expected custom aggregated content '[Hello ][from ][n8n!]', got: '%s'", aggregated)
		}
	})
}

func TestN8nAgent_Run_Auth(t *testing.T) {
	mockServer := NewMockN8nServer()
	defer mockServer.Close()

	t.Run("basic auth success", func(t *testing.T) {
		mockServer.ResetHandlers()
		mockServer.WithAuthValidation(AuthBasic, "user:pass")

		a := createMockN8nAgent(t, mockServer,
			WithAuthType(AuthBasic),
			WithAuthConfig(&AuthConfig{
				Username: "user",
				Password: "pass",
			}),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-auth-basic",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Auth test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var hasError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				hasError = true
			}
		}
		if hasError {
			t.Error("expected successful auth, got error event")
		}

		mockServer.ResetHandlers()
	})

	t.Run("header auth success", func(t *testing.T) {
		mockServer.ResetHandlers()
		mockServer.WithAuthValidation(AuthHeader, "Bearer my-token")

		a := createMockN8nAgent(t, mockServer,
			WithAuthType(AuthHeader),
			WithAuthConfig(&AuthConfig{
				HeaderName:  "Authorization",
				HeaderValue: "Bearer my-token",
			}),
		)

		invocation := &agent.Invocation{
			InvocationID: "test-auth-header",
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Auth header test",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		var hasError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				hasError = true
			}
		}
		if hasError {
			t.Error("expected successful auth, got error event")
		}

		mockServer.ResetHandlers()
	})
}

func TestN8nAgent_BuildHTTPRequest(t *testing.T) {
	t.Run("nil converter returns error", func(t *testing.T) {
		a := &N8nAgent{
			requestConverter: nil,
			webhookURL:       "http://example.com/webhook",
		}

		invocation := &agent.Invocation{
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		req, err := a.buildHTTPRequest(context.Background(), invocation, false)
		if err == nil {
			t.Error("expected error when request converter is nil")
		}
		if req != nil {
			t.Error("expected nil request when converter is nil")
		}
	})

	t.Run("streaming sets Accept header", func(t *testing.T) {
		a := &N8nAgent{
			requestConverter: &defaultRequestConverter{},
			webhookURL:       "http://example.com/webhook",
		}

		invocation := &agent.Invocation{
			Message: model.Message{Content: "test"},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		req, err := a.buildHTTPRequest(context.Background(), invocation, true)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("expected Accept header 'text/event-stream', got '%s'", req.Header.Get("Accept"))
		}
	})

	t.Run("non-streaming no Accept header", func(t *testing.T) {
		a := &N8nAgent{
			requestConverter: &defaultRequestConverter{},
			webhookURL:       "http://example.com/webhook",
		}

		invocation := &agent.Invocation{
			Message: model.Message{Content: "test"},
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		req, err := a.buildHTTPRequest(context.Background(), invocation, false)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req.Header.Get("Accept") != "" {
			t.Errorf("expected no Accept header for non-streaming, got '%s'", req.Header.Get("Accept"))
		}
	})
}

func TestN8nAgent_RunStreaming_Errors(t *testing.T) {
	t.Run("nil response converter returns error", func(t *testing.T) {
		a := &N8nAgent{
			name:              "test-agent",
			responseConverter: nil,
			streamingBufSize:  10,
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := a.runStreaming(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when response converter is nil")
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})

	t.Run("nil request converter emits error event", func(t *testing.T) {
		a := &N8nAgent{
			name:              "test-agent",
			responseConverter: &defaultResponseConverter{},
			requestConverter:  nil,
			streamingBufSize:  10,
			webhookURL:        "http://example.com/webhook",
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			RunOptions: agent.RunOptions{
				RuntimeState: make(map[string]any),
			},
		}

		eventChan, err := a.runStreaming(context.Background(), invocation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

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

func boolPtr(b bool) *bool { return &b }

func TestN8nAgent_Run_Streaming_ContextCancellation(t *testing.T) {
	mockServer := NewMockN8nServer()
	defer mockServer.Close()

	// Use per-event delay so cancellation can take effect between events.
	mockServer.WithStreamingEventsDelay([]string{
		`data: {"output": "chunk1"}`,
		`data: {"output": "chunk2"}`,
		`data: {"output": "chunk3"}`,
		`data: {"output": "chunk4"}`,
		`data: {"output": "chunk5"}`,
		`data: [DONE]`,
	}, 50*time.Millisecond)

	a := createMockN8nAgent(t, mockServer, WithEnableStreaming(true))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	invocation := &agent.Invocation{
		InvocationID: "test-cancel",
		Message:      model.Message{Content: "test"},
		RunOptions:   agent.RunOptions{RuntimeState: make(map[string]any)},
	}

	eventChan, err := a.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Cancel after first event
	var count int
	for range eventChan {
		count++
		if count == 1 {
			cancel()
		}
	}

	// With per-event delay and cancellation after the first event,
	// we should get fewer events than the full set (5 chunks + 1 final = 6).
	if count >= 6 {
		t.Errorf("expected early termination, got %d events (full set is 6)", count)
	}
}

func TestN8nAgent_Run_Streaming_ErrorResponse(t *testing.T) {
	mockServer := NewMockN8nServer()
	defer mockServer.Close()

	// Override to return error for streaming requests too
	mockServer.WithError(http.StatusInternalServerError, "workflow failed")

	a := createMockN8nAgent(t, mockServer, WithEnableStreaming(true))

	invocation := &agent.Invocation{
		InvocationID: "test-stream-error",
		Message:      model.Message{Content: "test"},
		RunOptions:   agent.RunOptions{RuntimeState: make(map[string]any)},
	}

	eventChan, err := a.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var receivedError bool
	for evt := range eventChan {
		if evt.Response != nil && evt.Response.Error != nil {
			receivedError = true
		}
	}
	if !receivedError {
		t.Error("expected error event for non-2xx streaming response")
	}
}
