//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNew(t *testing.T) {
	type testCase struct {
		name         string
		opts         []Option
		setupFunc    func(tc *testCase) *httptest.Server
		validateFunc func(t *testing.T, agent *A2AAgent, err error)
	}

	tests := []testCase{
		{
			name: "success with agent URL",
			opts: []Option{},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/.well-known/agent-card.json" {
						agentCard := server.AgentCard{
							Name:        "test-agent",
							Description: "A test agent",
							URL:         "http://test.com",
							Capabilities: server.AgentCapabilities{
								Streaming: boolPtr(true),
							},
						}
						json.NewEncoder(w).Encode(agentCard)
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
				tc.opts = append(tc.opts, WithAgentCardURL(server.URL))
				return server
			},
			validateFunc: func(t *testing.T, agent *A2AAgent, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if agent == nil {
					t.Fatal("expected agent, got nil")
				}
				if agent.name != "test-agent" {
					t.Errorf("expected name 'test-agent', got %s", agent.name)
				}
				if agent.description != "A test agent" {
					t.Errorf("expected description 'A test agent', got %s", agent.description)
				}
				if agent.agentCard == nil {
					t.Error("expected agent card to be set")
				}
				if agent.a2aClient == nil {
					t.Error("expected A2A client to be initialized")
				}
			},
		},
		{
			name: "success with direct agent card",
			opts: []Option{
				WithName("direct-agent"),
				WithDescription("Direct agent card"),
				WithAgentCard(&server.AgentCard{
					Name:        "card-agent",
					Description: "Card description",
					URL:         "http://direct.com",
				}),
			},
			setupFunc: func(tc *testCase) *httptest.Server {
				return nil
			},
			validateFunc: func(t *testing.T, agent *A2AAgent, err error) {
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
				if agent.agentCard == nil {
					t.Error("expected agent card to be set")
				}
			},
		},
		{
			name: "error when no agent card",
			opts: []Option{},
			setupFunc: func(tc *testCase) *httptest.Server {
				return nil
			},
			validateFunc: func(t *testing.T, agent *A2AAgent, err error) {
				if err == nil {
					t.Error("expected error when no agent card is set")
				}
				if agent != nil {
					t.Error("expected agent to be nil on error")
				}
			},
		},
		{
			name: "error when agent card fetch fails",
			opts: []Option{},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
				tc.opts = append(tc.opts, WithAgentCardURL(server.URL))
				return server
			},
			validateFunc: func(t *testing.T, agent *A2AAgent, err error) {
				if err == nil {
					t.Error("expected error when agent card fetch fails")
				}
				if agent != nil {
					t.Error("expected agent to be nil on error")
				}
			},
		},
		{
			name: "success with transfer state keys",
			opts: []Option{
				WithTransferStateKey("session_key", "user_id"),
			},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/.well-known/agent-card.json" {
						agentCard := server.AgentCard{
							Name:        "test-agent",
							Description: "Test agent",
							URL:         "http://test.com",
						}
						json.NewEncoder(w).Encode(agentCard)
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
				tc.opts = append(tc.opts, WithAgentCardURL(server.URL))
				return server
			},
			validateFunc: func(t *testing.T, agent *A2AAgent, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if agent == nil {
					t.Fatal("expected agent, got nil")
				}
				if len(agent.transferStateKey) != 2 {
					t.Errorf("expected 2 transfer state keys, got %d", len(agent.transferStateKey))
				}
				if agent.transferStateKey[0] != "session_key" || agent.transferStateKey[1] != "user_id" {
					t.Error("transfer state keys not set correctly")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.setupFunc(&tc)
			if server != nil {
				defer server.Close()
			}
			agent, err := New(tc.opts...)
			tc.validateFunc(t, agent, err)
		})
	}
}

func TestA2AAgent_Info(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, info agent.Info)
	}

	tests := []testCase{
		{
			name: "returns correct info",
			agent: &A2AAgent{
				name:        "test-agent",
				description: "Test description",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, info agent.Info) {
				if info.Name != "test-agent" {
					t.Errorf("expected name 'test-agent', got %s", info.Name)
				}
				if info.Description != "Test description" {
					t.Errorf("expected description 'Test description', got %s", info.Description)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			info := tc.agent.Info()
			tc.validateFunc(t, info)
		})
	}
}

func TestA2AAgent_Tools(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, tools []tool.Tool)
	}

	tests := []testCase{
		{
			name:      "returns empty tools",
			agent:     &A2AAgent{},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, tools []tool.Tool) {
				if len(tools) != 0 {
					t.Errorf("expected 0 tools, got %d", len(tools))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			tools := tc.agent.Tools()
			tc.validateFunc(t, tools)
		})
	}
}

func TestA2AAgent_SubAgents(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, subAgents []agent.Agent)
	}

	tests := []testCase{
		{
			name:      "returns empty sub agents",
			agent:     &A2AAgent{},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, subAgents []agent.Agent) {
				if len(subAgents) != 0 {
					t.Errorf("expected 0 sub agents, got %d", len(subAgents))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			subAgents := tc.agent.SubAgents()
			tc.validateFunc(t, subAgents)
		})
	}
}

func TestA2AAgent_FindSubAgent(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		agentName    string
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, foundAgent agent.Agent)
	}

	tests := []testCase{
		{
			name:      "returns nil for any name",
			agent:     &A2AAgent{},
			agentName: "any-name",
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, foundAgent agent.Agent) {
				if foundAgent != nil {
					t.Error("expected nil agent")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			foundAgent := tc.agent.FindSubAgent(tc.agentName)
			tc.validateFunc(t, foundAgent)
		})
	}
}

func TestA2AAgent_GetAgentCard(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, agentCard *server.AgentCard)
	}

	tests := []testCase{
		{
			name: "returns agent card",
			agent: &A2AAgent{
				agentCard: &server.AgentCard{
					Name: "test-card",
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard) {
				if agentCard == nil {
					t.Fatal("expected agent card, got nil")
				}
				if agentCard.Name != "test-card" {
					t.Errorf("expected name 'test-card', got %s", agentCard.Name)
				}
			},
		},
		{
			name:      "returns nil when no card set",
			agent:     &A2AAgent{},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard) {
				if agentCard != nil {
					t.Error("expected nil agent card")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			agentCard := tc.agent.GetAgentCard()
			tc.validateFunc(t, agentCard)
		})
	}
}

func TestWrapEventChannelWithTelemetry_AccumulatesTokenUsage(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(spanRecorder))
	ctx, span := tp.Tracer("test").Start(context.Background(), "wrap")
	sdkSpan := span

	originalChan := make(chan *event.Event, 2)
	wrappedChan := (&A2AAgent{}).wrapEventChannelWithTelemetry(ctx, &agent.Invocation{}, originalChan, sdkSpan)

	partialEvent := &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Usage: &model.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}
	finalEvent := &event.Event{
		Response: &model.Response{
			IsPartial: false,
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
		},
	}

	originalChan <- partialEvent
	originalChan <- finalEvent
	close(originalChan)

	var received []*event.Event
	for evt := range wrappedChan {
		received = append(received, evt)
	}

	if len(received) != 2 || received[0] != partialEvent || received[1] != finalEvent {
		t.Fatalf("wrapped channel did not preserve events")
	}

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected one ended span, got %d", len(spans))
	}

	attrs := spans[0].Attributes()
	if !hasAttr(attrs, attribute.Int(itelemetry.KeyGenAIUsageInputTokens, finalEvent.Response.Usage.PromptTokens)) {
		t.Fatalf("expected input token usage to be recorded, attrs=%v", attrs)
	}
	if !hasAttr(attrs, attribute.Int(itelemetry.KeyGenAIUsageOutputTokens, finalEvent.Response.Usage.CompletionTokens)) {
		t.Fatalf("expected output token usage to be recorded, attrs=%v", attrs)
	}
}

func TestA2AAgent_shouldUseStreaming(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, useStreaming bool)
	}

	tests := []testCase{
		{
			name: "returns true when streaming enabled",
			agent: &A2AAgent{
				agentCard: &server.AgentCard{
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(true),
					},
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, useStreaming bool) {
				if !useStreaming {
					t.Error("expected streaming to be enabled")
				}
			},
		},
		{
			name: "returns false when streaming disabled",
			agent: &A2AAgent{
				agentCard: &server.AgentCard{
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(false),
					},
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, useStreaming bool) {
				if useStreaming {
					t.Error("expected streaming to be disabled")
				}
			},
		},
		{
			name: "returns false when capabilities not specified",
			agent: &A2AAgent{
				agentCard: &server.AgentCard{},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, useStreaming bool) {
				if useStreaming {
					t.Error("expected streaming to be disabled by default")
				}
			},
		},
		{
			name:      "returns false when no agent card",
			agent:     &A2AAgent{},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, useStreaming bool) {
				if useStreaming {
					t.Error("expected streaming to be disabled when no agent card")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			useStreaming := tc.agent.shouldUseStreaming()
			tc.validateFunc(t, useStreaming)
		})
	}
}

func TestA2AAgent_buildA2AMessage(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		invocation   *agent.Invocation
		isStream     bool
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, msg *protocol.Message, err error)
	}

	tests := []testCase{
		{
			name: "success with default converter",
			agent: &A2AAgent{
				name:                "test-agent",
				a2aMessageConverter: &defaultEventA2AConverter{},
			},
			invocation: &agent.Invocation{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "test content",
				},
			},
			isStream:  false,
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if msg == nil {
					t.Fatal("expected message, got nil")
				}
				if msg.Role != protocol.MessageRoleUser {
					t.Errorf("expected role User, got %s", msg.Role)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
			},
		},
		{
			name: "error when converter is nil",
			agent: &A2AAgent{
				a2aMessageConverter: nil,
			},
			invocation: &agent.Invocation{},
			isStream:   false,
			setupFunc:  func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err == nil {
					t.Error("expected error when converter is nil")
				}
				if msg != nil {
					t.Error("expected message to be nil on error")
				}
			},
		},
		{
			name: "error when converter fails",
			agent: &A2AAgent{
				a2aMessageConverter: &mockFailingConverter{},
			},
			invocation: &agent.Invocation{},
			isStream:   false,
			setupFunc:  func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err == nil {
					t.Error("expected error when converter fails")
				}
				if msg != nil {
					t.Error("expected message to be nil on error")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			msg, err := tc.agent.buildA2AMessage(tc.invocation, tc.isStream)
			tc.validateFunc(t, msg, err)
		})
	}
}

func TestA2AAgent_Run_ErrorCases(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		invocation   *agent.Invocation
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, eventChan <-chan *event.Event, err error)
	}

	tests := []testCase{
		{
			name:       "error when A2A client not initialized",
			agent:      &A2AAgent{a2aClient: nil},
			invocation: &agent.Invocation{},
			setupFunc:  func(tc *testCase) {},
			validateFunc: func(t *testing.T, eventChan <-chan *event.Event, err error) {
				if err == nil {
					t.Error("expected error when A2A client not initialized")
				}
				if eventChan != nil {
					t.Error("expected event channel to be nil on error")
				}
			},
		},
		{
			name: "error when streaming event converter is nil",
			agent: &A2AAgent{
				name:             "test-agent",
				eventConverter:   nil, // force runStreaming to fail immediately
				streamingBufSize: defaultStreamingChannelSize,
				enableStreaming:  boolPtr(true),
				a2aClient:        &client.A2AClient{}, // non-nil so Run() proceeds to runStreaming
			},
			invocation: &agent.Invocation{
				InvocationID: "test-inv",
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "test message",
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, eventChan <-chan *event.Event, err error) {
				if err == nil {
					t.Fatal("expected error when event converter is nil in streaming mode")
				}
				if !strings.Contains(err.Error(), "event converter not set") {
					t.Errorf("unexpected error message: %v", err)
				}
				if eventChan != nil {
					t.Error("expected event channel to be nil on error")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			eventChan, err := tc.agent.Run(context.Background(), tc.invocation)
			tc.validateFunc(t, eventChan, err)
		})
	}
}

func TestWithTransferStateKey(t *testing.T) {
	t.Run("transfer state keys are set correctly", func(t *testing.T) {
		agent := &A2AAgent{}

		// Apply option
		WithTransferStateKey("key1", "key2", "key3")(agent)

		if len(agent.transferStateKey) != 3 {
			t.Errorf("expected 3 transfer state keys, got %d", len(agent.transferStateKey))
		}

		expectedKeys := []string{"key1", "key2", "key3"}
		for i, key := range agent.transferStateKey {
			if key != expectedKeys[i] {
				t.Errorf("expected key %s at index %d, got %s", expectedKeys[i], i, key)
			}
		}

		// Test adding more keys
		WithTransferStateKey("key4")(agent)
		if len(agent.transferStateKey) != 4 {
			t.Errorf("expected 4 transfer state keys after adding more, got %d", len(agent.transferStateKey))
		}
	})

	t.Run("transfer state keys work with buildA2AMessage", func(t *testing.T) {
		a2aAgent := &A2AAgent{
			a2aMessageConverter: &defaultEventA2AConverter{},
			transferStateKey:    []string{"session_key", "user_pref"},
		}

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test message",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]any{
					"session_key": "session_value",
					"user_pref":   "dark_mode",
					"other_key":   "should_not_transfer",
				},
			},
		}

		msg, err := a2aAgent.buildA2AMessage(invocation, false)
		if err != nil {
			t.Fatalf("buildA2AMessage failed: %v", err)
		}

		if msg.Metadata == nil {
			t.Fatal("expected metadata to be set")
		}

		// Check that only the specified keys are transferred
		if len(msg.Metadata) != 2 {
			t.Errorf("expected 2 metadata items, got %d", len(msg.Metadata))
		}

		if msg.Metadata["session_key"] != "session_value" {
			t.Error("session_key not transferred correctly")
		}

		if msg.Metadata["user_pref"] != "dark_mode" {
			t.Error("user_pref not transferred correctly")
		}

		// Make sure other keys are not transferred
		if _, exists := msg.Metadata["other_key"]; exists {
			t.Error("other_key should not be transferred")
		}
	})
}

func TestWithStreamingRespHandler(t *testing.T) {
	t.Run("streaming response handler is set correctly", func(t *testing.T) {
		agent := &A2AAgent{}

		// Mock handler
		handler := func(resp *model.Response) (string, error) {
			return "processed_content", nil
		}

		// Apply option
		WithStreamingRespHandler(handler)(agent)

		if agent.streamingRespHandler == nil {
			t.Error("streaming response handler should be set")
		}

		// Test that the handler works
		result, err := agent.streamingRespHandler(&model.Response{})
		if err != nil {
			t.Errorf("handler should not return error: %v", err)
		}
		if result != "processed_content" {
			t.Errorf("expected 'processed_content', got '%s'", result)
		}
	})

	t.Run("streaming response handler can be nil", func(t *testing.T) {
		agent := &A2AAgent{}

		// Apply nil handler
		WithStreamingRespHandler(nil)(agent)

		if agent.streamingRespHandler != nil {
			t.Error("streaming response handler should be nil")
		}
	})
}

func TestA2ARequestOptions(t *testing.T) {
	t.Run("invocation can store A2A request options", func(t *testing.T) {
		// Create invocation
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test message",
			},
			RunOptions: agent.RunOptions{},
		}

		// Verify A2ARequestOptions can be set
		invocation.RunOptions.A2ARequestOptions = []any{
			"option1",
			"option2",
		}

		if len(invocation.RunOptions.A2ARequestOptions) != 2 {
			t.Errorf("Expected 2 options, got %d", len(invocation.RunOptions.A2ARequestOptions))
		}
	})

	t.Run("WithA2ARequestOptions sets options correctly", func(t *testing.T) {
		opts := agent.RunOptions{}

		// Apply the option
		agent.WithA2ARequestOptions("opt1", "opt2")(&opts)

		if len(opts.A2ARequestOptions) != 2 {
			t.Errorf("Expected 2 options, got %d", len(opts.A2ARequestOptions))
		}
	})

	t.Run("can use client.RequestOption", func(t *testing.T) {
		// Create invocation with actual client.RequestOption
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test message",
			},
			RunOptions: agent.RunOptions{},
		}

		// Use client.RequestOption directly
		invocation.RunOptions.A2ARequestOptions = []any{
			client.WithRequestHeader("X-Custom-Header", "test-value"),
			client.WithRequestHeaders(map[string]string{
				"X-User-ID": "user-123",
			}),
		}

		if len(invocation.RunOptions.A2ARequestOptions) != 2 {
			t.Errorf("Expected 2 options, got %d", len(invocation.RunOptions.A2ARequestOptions))
		}

		// Verify type assertion works in a2aagent
		for _, opt := range invocation.RunOptions.A2ARequestOptions {
			if _, ok := opt.(client.RequestOption); !ok {
				t.Errorf("Option is not a client.RequestOption")
			}
		}
	})

	t.Run("validates option types and returns error for invalid types", func(t *testing.T) {
		// Create test server
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent-card.json" {
				agentCard := server.AgentCard{
					Name:        "test-agent",
					Description: "A test agent",
					URL:         "http://test.com",
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(false),
					},
				}
				json.NewEncoder(w).Encode(agentCard)
				return
			}
		}))
		defer testServer.Close()

		// Create A2A agent
		a2aAgent, err := New(WithAgentCardURL(testServer.URL))
		if err != nil {
			t.Fatalf("Failed to create A2A agent: %v", err)
		}

		// Create invocation with invalid option type
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "test message",
			},
			RunOptions: agent.RunOptions{
				A2ARequestOptions: []any{
					"invalid-string-option", // This is not a client.RequestOption
				},
			},
		}

		// Run the agent - should return error immediately
		eventChan, err := a2aAgent.Run(context.Background(), invocation)
		if err == nil {
			t.Fatal("Expected error for invalid option type, but got none")
		}

		// Verify error message contains type information
		if !strings.Contains(err.Error(), "A2ARequestOptions[0]") ||
			!strings.Contains(err.Error(), "not a valid client.RequestOption") {
			t.Errorf("Error message doesn't contain expected information: %s", err.Error())
		}

		// Event channel should be nil
		if eventChan != nil {
			t.Error("Expected nil event channel when validation fails")
		}
	})
}

// Mock converter that always fails
type mockFailingConverter struct{}

func (m *mockFailingConverter) ConvertToA2AMessage(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error) {
	return nil, fmt.Errorf("mock converter error")
}

func TestWithUserIDHeader(t *testing.T) {
	tests := []struct {
		name           string
		header         string
		expectedHeader string
	}{
		{
			name:           "set custom header",
			header:         "X-Custom-User-ID",
			expectedHeader: "X-Custom-User-ID",
		},
		{
			name:           "empty header not set",
			header:         "",
			expectedHeader: "",
		},
		{
			name:           "another custom header",
			header:         "X-User-Identifier",
			expectedHeader: "X-User-Identifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a2aAgent := &A2AAgent{}
			option := WithUserIDHeader(tt.header)
			option(a2aAgent)

			if tt.expectedHeader == "" {
				// Empty header should not be set
				if a2aAgent.userIDHeader != "" {
					t.Errorf("WithUserIDHeader() with empty string should not set header, got %v", a2aAgent.userIDHeader)
				}
			} else {
				if a2aAgent.userIDHeader != tt.expectedHeader {
					t.Errorf("WithUserIDHeader() userIDHeader = %v, want %v", a2aAgent.userIDHeader, tt.expectedHeader)
				}
			}
		})
	}
}

func TestUserIDHeaderInRequest(t *testing.T) {
	tests := []struct {
		name               string
		userIDHeader       string
		sessionUserID      string
		expectedHeaderName string
		shouldSendHeader   bool
	}{
		{
			name:               "default header with UserID",
			userIDHeader:       "",
			sessionUserID:      "user-123",
			expectedHeaderName: defaultUserIDHeader,
			shouldSendHeader:   true,
		},
		{
			name:               "custom header with UserID",
			userIDHeader:       "X-Custom-User-ID",
			sessionUserID:      "user-456",
			expectedHeaderName: "X-Custom-User-ID",
			shouldSendHeader:   true,
		},
		{
			name:               "no UserID in session",
			userIDHeader:       "X-Custom-User-ID",
			sessionUserID:      "",
			expectedHeaderName: "",
			shouldSendHeader:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track received headers for message requests (not agent card requests)
			var receivedHeaders http.Header
			var headersMu sync.Mutex
			var serverURL string

			// Create mock A2A server
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/.well-known/agent-card.json" {
					// Return agent card with the mock server's URL
					agentCard := server.AgentCard{
						Name:        "test-agent",
						Description: "A test agent",
						URL:         serverURL, // Use mock server URL
						Capabilities: server.AgentCapabilities{
							Streaming: boolPtr(false),
						},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(agentCard)
					return
				}

				// Capture headers for message requests
				headersMu.Lock()
				receivedHeaders = r.Header.Clone()
				headersMu.Unlock()

				// Return a simple response
				response := protocol.Message{
					Role: protocol.MessageRoleAgent,
					Parts: []protocol.Part{
						protocol.NewTextPart("test response"),
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			}))
			defer mockServer.Close()
			serverURL = mockServer.URL

			// Create A2A agent
			opts := []Option{
				WithAgentCardURL(mockServer.URL),
			}
			if tt.userIDHeader != "" {
				opts = append(opts, WithUserIDHeader(tt.userIDHeader))
			}

			a2aAgent, err := New(opts...)
			if err != nil {
				t.Fatalf("Failed to create A2A agent: %v", err)
			}

			// Create invocation with session
			invocation := &agent.Invocation{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "test message",
				},
			}
			if tt.sessionUserID != "" {
				invocation.Session = &session.Session{
					UserID: tt.sessionUserID,
				}
			}

			// Run the agent
			eventChan, err := a2aAgent.Run(context.Background(), invocation)
			if err != nil {
				t.Fatalf("Run() failed: %v", err)
			}

			// Consume events
			for range eventChan {
			}

			// Verify headers
			if tt.shouldSendHeader {
				actualUserID := receivedHeaders.Get(tt.expectedHeaderName)
				if actualUserID != tt.sessionUserID {
					t.Errorf("Expected UserID header %s = %v, got %v", tt.expectedHeaderName, tt.sessionUserID, actualUserID)
				}
			} else {
				// Should not send any UserID header
				if tt.expectedHeaderName != "" {
					actualUserID := receivedHeaders.Get(tt.expectedHeaderName)
					if actualUserID != "" {
						t.Errorf("Expected no UserID header, but got %s = %v", tt.expectedHeaderName, actualUserID)
					}
				}
			}
		})
	}
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

// TestStreamingConfiguration tests streaming-related configuration
func TestStreamingConfiguration(t *testing.T) {
	t.Run("handler_execution", func(t *testing.T) {
		handler := func(resp *model.Response) (string, error) {
			if len(resp.Choices) > 0 {
				return "processed:" + resp.Choices[0].Delta.Content, nil
			}
			return "empty", nil
		}

		agent := &A2AAgent{
			streamingRespHandler: handler,
		}

		if agent.streamingRespHandler == nil {
			t.Error("streaming response handler should be set")
		}

		// Test handler with empty response
		result, err := agent.streamingRespHandler(&model.Response{})
		if err != nil {
			t.Errorf("handler should not error: %v", err)
		}
		if result != "empty" {
			t.Errorf("expected 'empty', got '%s'", result)
		}

		// Test handler with content
		result, err = agent.streamingRespHandler(&model.Response{
			Choices: []model.Choice{{Delta: model.Message{Content: "test"}}},
		})
		if err != nil {
			t.Errorf("handler should not error: %v", err)
		}
		if result != "processed:test" {
			t.Errorf("expected 'processed:test', got '%s'", result)
		}
	})
}

// TestOptionFunctions tests uncovered option functions
func TestOptionFunctions(t *testing.T) {
	t.Run("WithCustomEventConverter", func(t *testing.T) {
		converter := &defaultA2AEventConverter{}
		agent := &A2AAgent{}
		WithCustomEventConverter(converter)(agent)
		if agent.eventConverter != converter {
			t.Error("custom event converter not set")
		}
	})

	t.Run("WithCustomA2AConverter", func(t *testing.T) {
		converter := &defaultEventA2AConverter{}
		agent := &A2AAgent{}
		WithCustomA2AConverter(converter)(agent)
		if agent.a2aMessageConverter != converter {
			t.Error("custom A2A converter not set")
		}
	})

	t.Run("WithA2AClientExtraOptions", func(t *testing.T) {
		agent := &A2AAgent{}
		opt1 := client.WithTimeout(10 * time.Second)
		opt2 := client.WithTimeout(5 * time.Second)
		WithA2AClientExtraOptions(opt1, opt2)(agent)
		if len(agent.extraA2AOptions) != 2 {
			t.Errorf("expected 2 extra options, got %d", len(agent.extraA2AOptions))
		}
	})

	t.Run("WithStreamingChannelBufSize", func(t *testing.T) {
		agent := &A2AAgent{}
		WithStreamingChannelBufSize(2048)(agent)
		if agent.streamingBufSize != 2048 {
			t.Errorf("expected buffer size 2048, got %d", agent.streamingBufSize)
		}
	})

	t.Run("WithEnableStreaming", func(t *testing.T) {
		agent := &A2AAgent{}
		WithEnableStreaming(true)(agent)
		if agent.enableStreaming == nil || !*agent.enableStreaming {
			t.Error("streaming should be enabled")
		}

		agent2 := &A2AAgent{}
		WithEnableStreaming(false)(agent2)
		if agent2.enableStreaming == nil || *agent2.enableStreaming {
			t.Error("streaming should be disabled")
		}
	})
}

// TestShouldUseStreaming_WithExplicitOption tests the explicit streaming option
func TestShouldUseStreaming_WithExplicitOption(t *testing.T) {
	tests := []struct {
		name            string
		enableStreaming *bool
		agentCard       *server.AgentCard
		expected        bool
	}{
		{
			name:            "explicit_true_overrides_card",
			enableStreaming: boolPtr(true),
			agentCard: &server.AgentCard{
				Capabilities: server.AgentCapabilities{
					Streaming: boolPtr(false),
				},
			},
			expected: true,
		},
		{
			name:            "explicit_false_overrides_card",
			enableStreaming: boolPtr(false),
			agentCard: &server.AgentCard{
				Capabilities: server.AgentCapabilities{
					Streaming: boolPtr(true),
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &A2AAgent{
				enableStreaming: tt.enableStreaming,
				agentCard:       tt.agentCard,
			}
			result := agent.shouldUseStreaming()
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestConverterEdgeCases tests edge cases in converter functions
func TestConverterEdgeCases(t *testing.T) {
	t.Run("ConvertToEvent_with_nil_result", func(t *testing.T) {
		converter := &defaultA2AEventConverter{}
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}
		// Nil result should be handled gracefully
		result := protocol.MessageResult{
			Result: nil,
		}
		events, err := converter.ConvertToEvents(result, "test-agent", invocation)
		if err != nil {
			t.Errorf("should handle nil result gracefully, got error: %v", err)
		}
		if len(events) == 0 {
			t.Error("expected at least one event, got none")
		}
	})

	t.Run("ConvertStreamingToEvents_with_nil_result", func(t *testing.T) {
		converter := &defaultA2AEventConverter{}
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}
		result := protocol.StreamingMessageEvent{
			Result: nil,
		}
		events, err := converter.ConvertStreamingToEvents(result, "test-agent", invocation)
		if err != nil {
			t.Errorf("should handle nil result gracefully, got error: %v", err)
		}
		if len(events) == 0 {
			t.Error("expected at least one event, got none")
		}
	})

	t.Run("ConvertToA2AMessage_with_all_content_types", func(t *testing.T) {
		converter := &defaultEventA2AConverter{}
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "main content",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("text part"),
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							Data:   []byte("image-data"),
							Format: "png",
						},
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "http://example.com/image.jpg",
							Format: "jpg",
						},
					},
					{
						Type: model.ContentTypeAudio,
						Audio: &model.Audio{
							Data:   []byte("audio-data"),
							Format: "mp3",
						},
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name:     "test.txt",
							Data:     []byte("file-data"),
							MimeType: "text/plain",
						},
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Data:     []byte("unnamed-file"),
							MimeType: "application/octet-stream",
						},
					},
				},
			},
			Session: &session.Session{
				ID: "session-123",
			},
		}

		msg, err := converter.ConvertToA2AMessage(false, "test-agent", invocation)
		if err != nil {
			t.Fatalf("ConvertToA2AMessage failed: %v", err)
		}
		if msg == nil {
			t.Fatal("expected message, got nil")
		}

		// Should have multiple parts
		if len(msg.Parts) < 5 {
			t.Errorf("expected at least 5 parts, got %d", len(msg.Parts))
		}

		// Check context ID
		if msg.ContextID == nil || *msg.ContextID != "session-123" {
			t.Error("context ID not set correctly")
		}
	})

	t.Run("ConvertToA2AMessage_with_empty_content", func(t *testing.T) {
		converter := &defaultEventA2AConverter{}
		invocation := &agent.Invocation{
			Message: model.Message{
				Role: model.RoleUser,
			},
		}

		msg, err := converter.ConvertToA2AMessage(false, "test-agent", invocation)
		if err != nil {
			t.Fatalf("ConvertToA2AMessage failed: %v", err)
		}
		if msg == nil {
			t.Fatal("expected message, got nil")
		}

		// Should have at least one empty text part
		if len(msg.Parts) == 0 {
			t.Error("expected at least one part for empty content")
		}
	})
}

// TestRunNonStreaming_AdditionalCases tests additional non-streaming scenarios
func TestRunNonStreaming_AdditionalCases(t *testing.T) {
	t.Run("non_streaming_with_error_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent-card.json" {
				agentCard := server.AgentCard{
					Name: "test-agent",
					URL:  "http://test.com",
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(false),
					},
				}
				json.NewEncoder(w).Encode(agentCard)
				return
			}
			// Return error response
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		a2aAgent, err := New(WithAgentCardURL(server.URL))
		if err != nil {
			t.Fatalf("Failed to create A2A agent: %v", err)
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message:      model.Message{Role: model.RoleUser, Content: "test"},
		}

		eventChan, err := a2aAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}

		var hasError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				hasError = true
			}
		}

		if !hasError {
			t.Error("expected error event, got none")
		}
	})

	t.Run("non_streaming_with_task_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent-card.json" {
				agentCard := server.AgentCard{
					Name: "test-agent",
					URL:  "http://test.com",
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(false),
					},
				}
				json.NewEncoder(w).Encode(agentCard)
				return
			}
			// Return task response
			task := protocol.Task{
				Artifacts: []protocol.Artifact{
					{
						Parts: []protocol.Part{
							protocol.NewTextPart("task result"),
						},
					},
				},
			}
			json.NewEncoder(w).Encode(task)
		}))
		defer server.Close()

		a2aAgent, err := New(WithAgentCardURL(server.URL))
		if err != nil {
			t.Fatalf("Failed to create A2A agent: %v", err)
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message:      model.Message{Role: model.RoleUser, Content: "test"},
		}

		eventChan, err := a2aAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
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

// TestNew_URLChanges tests New when agent card URL differs from initial URL
func TestNew_URLChanges(t *testing.T) {
	t.Run("agent_card_url_differs_from_initial", func(t *testing.T) {
		initialURL := ""
		finalURL := ""

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent-card.json" {
				// Return agent card with a different URL
				agentCard := server.AgentCard{
					Name:        "test-agent",
					Description: "Test agent",
					URL:         finalURL, // Different URL
					Capabilities: server.AgentCapabilities{
						Streaming: boolPtr(false),
					},
				}
				json.NewEncoder(w).Encode(agentCard)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		initialURL = server.URL
		finalURL = server.URL + "/v2" // Different URL path

		agent, err := New(WithAgentCardURL(initialURL))
		if err != nil {
			t.Fatalf("Failed to create A2A agent: %v", err)
		}

		if agent == nil {
			t.Fatal("expected agent, got nil")
		}

		// The agent should have been recreated with the new URL
		if agent.agentCard == nil {
			t.Error("expected agent card to be set")
		}
	})

	t.Run("agent_card_empty_url", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent-card.json" {
				// Return agent card with empty URL
				agentCard := server.AgentCard{
					Name:        "test-agent",
					Description: "Test agent",
					URL:         "", // Empty URL
				}
				json.NewEncoder(w).Encode(agentCard)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		agent, err := New(WithAgentCardURL(server.URL))
		if err != nil {
			t.Fatalf("Failed to create A2A agent: %v", err)
		}

		// Should use the initial URL
		if agent.agentCard.URL != server.URL {
			t.Errorf("expected agent card URL to be %s, got %s", server.URL, agent.agentCard.URL)
		}
	})
}

func TestA2AAgentRunStreamingPreservesResponseID(t *testing.T) {
	t.Run("preserves_response_id_when_following_chunks_are_empty", func(t *testing.T) {
		sseBody := mustBuildSSEBody(t, []sseEvent{
			{
				eventType: protocol.EventMessage,
				payload: protocol.Message{
					Kind:      protocol.KindMessage,
					Role:      protocol.MessageRoleAgent,
					MessageID: "resp-1",
					Parts: []protocol.Part{
						&protocol.TextPart{Kind: protocol.KindText, Text: "partial response"},
					},
				},
			},
			{
				eventType: protocol.EventStatusUpdate,
				payload: protocol.TaskStatusUpdateEvent{
					Kind:      protocol.KindTaskStatusUpdate,
					TaskID:    "task-1",
					ContextID: "ctx-1",
					Status: protocol.TaskStatus{
						State: protocol.TaskStateCompleted,
					},
				},
			},
		})

		testClient := newTestStreamClient(t, sseBody)
		a := &A2AAgent{
			name:                "test-agent",
			agentCard:           &server.AgentCard{URL: "http://stream.test/"},
			eventConverter:      &defaultA2AEventConverter{},
			a2aMessageConverter: stubInvocationConverter{},
			streamingBufSize:    4,
			a2aClient:           testClient,
		}
		invocation := &agent.Invocation{
			InvocationID: "inv-test",
			Message:      model.Message{Role: model.RoleUser, Content: "hello"},
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		eventCh, err := a.runStreaming(ctx, invocation)
		if err != nil {
			t.Fatalf("runStreaming() error = %v", err)
		}

		var finalResponse *model.Response
		for evt := range eventCh {
			if evt.Response != nil && evt.Response.Done {
				finalResponse = evt.Response
			}
		}

		if finalResponse == nil {
			t.Fatal("expected final response event, got nil")
		}
		if finalResponse.ID != "resp-1" {
			t.Fatalf("expected final response ID 'resp-1', got %q", finalResponse.ID)
		}
		if finalResponse.Object != model.ObjectTypeChatCompletion {
			t.Fatalf("expected final response object %s, got %s", model.ObjectTypeChatCompletion, finalResponse.Object)
		}
		if len(finalResponse.Choices) == 0 {
			t.Fatal("expected final response choices, got none")
		}
		if finalResponse.Choices[0].Message.Content != "partial response" {
			t.Fatalf("expected aggregated content 'partial response', got %q", finalResponse.Choices[0].Message.Content)
		}
	})
}

// TestValidateA2ARequestOptions tests validation logic for A2A request options
func TestValidateA2ARequestOptions(t *testing.T) {
	tests := []struct {
		name        string
		options     []any
		expectError bool
	}{
		{
			name:        "nil_options",
			options:     nil,
			expectError: false,
		},
		{
			name:        "empty_options",
			options:     []any{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &A2AAgent{}
			invocation := &agent.Invocation{
				RunOptions: agent.RunOptions{
					A2ARequestOptions: tt.options,
				},
			}

			err := a.validateA2ARequestOptions(invocation)
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

type staticStreamHandler struct {
	body string
}

func (h *staticStreamHandler) Handle(
	ctx context.Context,
	_ *http.Client,
	_ *http.Request,
) (*http.Response, error) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(h.body)),
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp, nil
}

func newTestStreamClient(t *testing.T, body string) *client.A2AClient {
	t.Helper()
	cli, err := client.NewA2AClient(
		"http://stream.test/",
		client.WithHTTPReqHandler(&staticStreamHandler{body: body}),
	)
	if err != nil {
		t.Fatalf("failed to create test A2A client: %v", err)
	}
	return cli
}

type sseEvent struct {
	eventType string
	payload   any
}

func mustBuildSSEBody(t *testing.T, events []sseEvent) string {
	t.Helper()
	var builder strings.Builder
	for _, evt := range events {
		data, err := json.Marshal(evt.payload)
		if err != nil {
			t.Fatalf("failed to marshal SSE payload: %v", err)
		}
		builder.WriteString("event: ")
		builder.WriteString(evt.eventType)
		builder.WriteByte('\n')
		builder.WriteString("data: ")
		builder.Write(data)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

type stubInvocationConverter struct{}

func (stubInvocationConverter) ConvertToA2AMessage(
	_ bool,
	_ string,
	_ *agent.Invocation,
) (*protocol.Message, error) {
	msg := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "test message"}},
	)
	return &msg, nil
}

func hasAttr(attrs []attribute.KeyValue, target attribute.KeyValue) bool {
	for _, attr := range attrs {
		if attr.Key == target.Key && attr.Value == target.Value {
			return true
		}
	}
	return false
}
