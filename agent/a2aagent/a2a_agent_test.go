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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
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
					if r.URL.Path == AgentCardWellKnownPath {
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
					if r.URL.Path == AgentCardWellKnownPath {
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

func TestA2AAgent_resolveAgentCardFromURL(t *testing.T) {
	type testCase struct {
		name         string
		agent        *A2AAgent
		setupFunc    func(tc *testCase) *httptest.Server
		validateFunc func(t *testing.T, agentCard *server.AgentCard, err error)
	}

	tests := []testCase{
		{
			name:  "success with valid agent card",
			agent: &A2AAgent{},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == AgentCardWellKnownPath {
						agentCard := server.AgentCard{
							Name:        "resolved-agent",
							Description: "Resolved from URL",
							URL:         "http://resolved.com",
						}
						json.NewEncoder(w).Encode(agentCard)
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
				tc.agent.agentURL = server.URL
				return server
			},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if agentCard == nil {
					t.Fatal("expected agent card, got nil")
				}
				if agentCard.Name != "resolved-agent" {
					t.Errorf("expected name 'resolved-agent', got %s", agentCard.Name)
				}
				if agentCard.Description != "Resolved from URL" {
					t.Errorf("expected description 'Resolved from URL', got %s", agentCard.Description)
				}
			},
		},
		{
			name: "fills agent name and description when empty",
			agent: &A2AAgent{
				name:        "",
				description: "",
			},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					agentCard := server.AgentCard{
						Name:        "auto-filled",
						Description: "Auto-filled description",
					}
					json.NewEncoder(w).Encode(agentCard)
				}))
				tc.agent.agentURL = server.URL
				return server
			},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if agentCard.Name != "auto-filled" {
					t.Errorf("expected name 'auto-filled', got %s", agentCard.Name)
				}
			},
		},
		{
			name:  "error when HTTP request fails",
			agent: &A2AAgent{agentURL: "http://nonexistent.local"},
			setupFunc: func(tc *testCase) *httptest.Server {
				return nil
			},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard, err error) {
				if err == nil {
					t.Error("expected error when HTTP request fails")
				}
				if agentCard != nil {
					t.Error("expected agent card to be nil on error")
				}
			},
		},
		{
			name:  "error when HTTP status not OK",
			agent: &A2AAgent{},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
				tc.agent.agentURL = server.URL
				return server
			},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard, err error) {
				if err == nil {
					t.Error("expected error when HTTP status not OK")
				}
				if agentCard != nil {
					t.Error("expected agent card to be nil on error")
				}
			},
		},
		{
			name:  "error when invalid JSON",
			agent: &A2AAgent{},
			setupFunc: func(tc *testCase) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("invalid json"))
				}))
				tc.agent.agentURL = server.URL
				return server
			},
			validateFunc: func(t *testing.T, agentCard *server.AgentCard, err error) {
				if err == nil {
					t.Error("expected error when JSON is invalid")
				}
				if agentCard != nil {
					t.Error("expected agent card to be nil on error")
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
			agentCard, err := tc.agent.resolveAgentCardFromURL()
			tc.validateFunc(t, agentCard, err)
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
			if r.URL.Path == AgentCardWellKnownPath {
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
				if r.URL.Path == AgentCardWellKnownPath {
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
