//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package weknora

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/client"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		agent, err := New(
			WithName("weknora-agent"),
			WithDescription("WeKnora agent"),
			WithBaseUrl("http://localhost:8080"),
			WithToken("test-token"),
			WithAgentID("agent-123"),
			WithKnowledgeBaseIDs([]string{"kb-1"}),
			WithWebSearchEnabled(true),
			WithTimeout(10*time.Minute),
			WithGetWeKnoraClientFunc(func(invocation *agent.Invocation) (*client.Client, error) {
				return &client.Client{}, nil
			}),
		)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if agent == nil {
			t.Fatal("expected agent, got nil")
		}
		if agent.name != "weknora-agent" {
			t.Errorf("expected name 'weknora-agent', got %s", agent.name)
		}
		if agent.description != "WeKnora agent" {
			t.Errorf("expected description 'WeKnora agent', got %s", agent.description)
		}
		if agent.baseUrl != "http://localhost:8080" {
			t.Errorf("expected baseUrl 'http://localhost:8080', got %s", agent.baseUrl)
		}
		if agent.token != "test-token" {
			t.Errorf("expected token 'test-token', got %s", agent.token)
		}
		if agent.agentID != "agent-123" {
			t.Errorf("expected agentID 'agent-123', got %s", agent.agentID)
		}
		if len(agent.knowledgeBaseIDs) != 1 || agent.knowledgeBaseIDs[0] != "kb-1" {
			t.Errorf("expected knowledgeBaseIDs ['kb-1'], got %v", agent.knowledgeBaseIDs)
		}
		if !agent.webSearchEnabled {
			t.Errorf("expected webSearchEnabled true, got false")
		}
		if agent.timeout != 10*time.Minute {
			t.Errorf("expected timeout 10m, got %v", agent.timeout)
		}
	})

	t.Run("error when no name", func(t *testing.T) {
		agent, err := New()
		if err == nil {
			t.Error("expected error when no name is set")
		}
		if agent != nil {
			t.Error("expected agent to be nil on error")
		}
	})
}

func TestWeKnoraAgent_Info(t *testing.T) {
	agent := &WeKnoraAgent{
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

func TestWeKnoraAgent_Tools(t *testing.T) {
	agent := &WeKnoraAgent{}
	tools := agent.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestWeKnoraAgent_SubAgents(t *testing.T) {
	agent := &WeKnoraAgent{}

	subAgents := agent.SubAgents()
	if len(subAgents) != 0 {
		t.Errorf("expected 0 sub agents, got %d", len(subAgents))
	}

	foundAgent := agent.FindSubAgent("any-name")
	if foundAgent != nil {
		t.Error("expected nil agent")
	}
}

func TestWeKnoraAgent_GetClient(t *testing.T) {
	t.Run("uses custom client function", func(t *testing.T) {
		expectedClient := client.NewClient("http://test.com")
		weknoraAgent := &WeKnoraAgent{
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return expectedClient, nil
			},
		}

		invocation := &agent.Invocation{}
		cli, err := weknoraAgent.getWeKnoraClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if cli != expectedClient {
			t.Error("should return client from custom function")
		}
	})

	t.Run("creates default client", func(t *testing.T) {
		weknoraAgent := &WeKnoraAgent{
			baseUrl: "http://test.com",
			token:   "test-token",
		}

		invocation := &agent.Invocation{}
		cli, err := weknoraAgent.getWeKnoraClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if cli == nil {
			t.Error("should return a client")
		}
	})
}

func TestWeKnoraAgent_BuildWeKnoraRequest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		weknoraAgent := &WeKnoraAgent{
			agentID:          "agent-123",
			knowledgeBaseIDs: []string{"kb-1"},
			webSearchEnabled: true,
		}

		invocation := &agent.Invocation{
			Message: model.Message{
				Content: "test query",
			},
		}

		req, err := weknoraAgent.buildWeKnoraRequest(context.Background(), invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req == nil {
			t.Fatal("expected request, got nil")
		}
		if req.Query != "test query" {
			t.Errorf("expected query 'test query', got %s", req.Query)
		}
		if !req.AgentEnabled {
			t.Errorf("expected AgentEnabled true, got false")
		}
		if req.AgentID != "agent-123" {
			t.Errorf("expected AgentID 'agent-123', got %s", req.AgentID)
		}
		if len(req.KnowledgeBaseIDs) != 1 || req.KnowledgeBaseIDs[0] != "kb-1" {
			t.Errorf("expected KnowledgeBaseIDs ['kb-1'], got %v", req.KnowledgeBaseIDs)
		}
		if !req.WebSearchEnabled {
			t.Errorf("expected WebSearchEnabled true, got false")
		}
	})

	t.Run("empty query", func(t *testing.T) {
		weknoraAgent := &WeKnoraAgent{}

		invocation := &agent.Invocation{
			Message: model.Message{
				Content: "",
			},
		}

		req, err := weknoraAgent.buildWeKnoraRequest(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when query is empty")
		}
		if req != nil {
			t.Error("expected nil request on error")
		}
	})
}

func TestWeKnoraAgent_SendErrorEvent(t *testing.T) {
	weknoraAgent := &WeKnoraAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	weknoraAgent.sendErrorEvent(context.Background(), eventChan, invocation, "test error message")
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

func TestWeKnoraAgent_SendFinalStreamingEvent(t *testing.T) {
	weknoraAgent := &WeKnoraAgent{
		name: "test-agent",
	}

	invocation := &agent.Invocation{
		InvocationID: "test-inv",
	}

	eventChan := make(chan *event.Event, 1)
	weknoraAgent.sendFinalStreamingEvent(context.Background(), eventChan, invocation, "aggregated content", "aggregated reasoning")
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
	if evt.Response.Choices[0].Message.ReasoningContent != "aggregated reasoning" {
		t.Errorf("expected reasoning content 'aggregated reasoning', got: %s", evt.Response.Choices[0].Message.ReasoningContent)
	}
}

func TestWeKnoraAgent_Run(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sessions/default-session" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"success": true, "data": {"id": "default-session"}}`))
				return
			}
			if r.URL.Path == "/api/v1/agent-chat/default-session" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"response_type\": \"thinking\", \"content\": \"thinking\"}\n\n"))
				w.Write([]byte("data: {\"response_type\": \"thinking\", \"content\": \"...\"}\n\n"))
				w.Write([]byte("data: {\"response_type\": \"answer\", \"content\": \"hello\"}\n\n"))
				w.Write([]byte("data: {\"response_type\": \"answer\", \"content\": \" world\"}\n\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		var partialContents []string
		var partialReasonings []string
		var finalContent string
		var finalReasoning string
		for evt := range eventChan {
			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				if evt.Response.IsPartial {
					if evt.Response.Choices[0].Delta.Content != "" {
						partialContents = append(partialContents, evt.Response.Choices[0].Delta.Content)
					}
					if evt.Response.Choices[0].Delta.ReasoningContent != "" {
						partialReasonings = append(partialReasonings, evt.Response.Choices[0].Delta.ReasoningContent)
					}
				} else {
					finalContent = evt.Response.Choices[0].Message.Content
					finalReasoning = evt.Response.Choices[0].Message.ReasoningContent
				}
			}
		}

		if len(partialContents) != 2 {
			t.Errorf("expected 2 partial events, got %d", len(partialContents))
		} else {
			if partialContents[0] != "hello" {
				t.Errorf("expected 'hello', got '%s'", partialContents[0])
			}
			if partialContents[1] != " world" {
				t.Errorf("expected ' world', got '%s'", partialContents[1])
			}
		}
		if len(partialReasonings) != 2 {
			t.Errorf("expected 2 partial reasoning events, got %d", len(partialReasonings))
		} else {
			if partialReasonings[0] != "thinking" {
				t.Errorf("expected 'thinking', got '%s'", partialReasonings[0])
			}
			if partialReasonings[1] != "..." {
				t.Errorf("expected '...', got '%s'", partialReasonings[1])
			}
		}
		if finalContent != "hello world" {
			t.Errorf("expected 'hello world', got '%s'", finalContent)
		}
		if finalReasoning != "thinking..." {
			t.Errorf("expected 'thinking...', got '%s'", finalReasoning)
		}
	})

	t.Run("success with create session", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sessions/default-session" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.URL.Path == "/api/v1/sessions" && r.Method == http.MethodPost {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"success": true, "data": {"id": "new-session"}}`))
				return
			}
			if r.URL.Path == "/api/v1/agent-chat/new-session" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"response_type\": \"answer\", \"content\": \"hello\"}\n\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-agent",
			UserID:  "test-user",
		}
		invocation := &agent.Invocation{
			InvocationID:   "test-inv",
			Session:        sess,
			SessionService: inmemory.NewSessionService(),
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		var finalContent string
		for evt := range eventChan {
			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				if !evt.Response.IsPartial {
					finalContent = evt.Response.Choices[0].Message.Content
				}
			}
		}

		if finalContent != "hello" {
			t.Errorf("expected 'hello', got '%s'", finalContent)
		}
		state, _ := invocation.SessionService.ListUserStates(context.Background(), session.UserKey{
			AppName: sess.AppName,
			UserID:  sess.UserID,
		})

		if stateSessionID, ok := state[genSessionKey(sess.ID)]; !ok || string(stateSessionID) != "new-session" {
			t.Errorf("expected session state to be 'new-session', got '%s'", string(stateSessionID))
		}
	})

	t.Run("success with existing session state", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sessions/existing-session" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"success": true, "data": {"id": "existing-session"}}`))
				return
			}
			if r.URL.Path == "/api/v1/agent-chat/existing-session" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"response_type\": \"answer\", \"content\": \"hello\"}\n\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		sessionService := inmemory.NewSessionService()
		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-agent",
			UserID:  "test-user",
		}
		_ = sessionService.UpdateUserState(context.Background(), session.UserKey{
			AppName: sess.AppName,
			UserID:  sess.UserID,
		}, session.StateMap{
			genSessionKey(sess.ID): []byte("existing-session"),
		})
		invocation := &agent.Invocation{
			InvocationID:   "test-inv",
			Session:        sess,
			SessionService: sessionService,
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		var finalContent string
		for evt := range eventChan {
			if evt.Response != nil && len(evt.Response.Choices) > 0 {
				if !evt.Response.IsPartial {
					finalContent = evt.Response.Choices[0].Message.Content
				}
			}
		}

		if finalContent != "hello" {
			t.Errorf("expected 'hello', got '%s'", finalContent)
		}
	})

	t.Run("error when create session fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err == nil {
			t.Error("expected error, got nil")
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})

	t.Run("error when stream fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sessions/default-session" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"success": true, "data": {"id": "default-session"}}`))
				return
			}
			if r.URL.Path == "/api/v1/agent-chat/default-session" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		var hasError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				hasError = true
			}
		}

		if !hasError {
			t.Error("expected error event")
		}
	})

	t.Run("error event from stream", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sessions/default-session" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"success": true, "data": {"id": "default-session"}}`))
				return
			}
			if r.URL.Path == "/api/v1/agent-chat/default-session" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"response_type\": \"error\", \"content\": \"some error\"}\n\n"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient(server.URL), nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message: model.Message{
				Content: "test query",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		var hasError bool
		for evt := range eventChan {
			if evt.Response != nil && evt.Response.Error != nil {
				hasError = true
				if evt.Response.Error.Message != "weknora agent error: some error" {
					t.Errorf("expected 'weknora agent error: some error', got '%s'", evt.Response.Error.Message)
				}
			}
		}

		if !hasError {
			t.Error("expected error event")
		}
	})

	t.Run("error when getWeKnoraClient fails", func(t *testing.T) {
		expectedErr := fmt.Errorf("client error")
		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return nil, expectedErr
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err != expectedErr {
			t.Errorf("expected error %v, got: %v", expectedErr, err)
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})

	t.Run("error when buildWeKnoraRequest fails", func(t *testing.T) {
		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
			getWeKnoraClientFunc: func(*agent.Invocation) (*client.Client, error) {
				return client.NewClient("http://test.com"), nil
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			Message: model.Message{
				Content: "",
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when buildWeKnoraRequest fails")
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})

	t.Run("error when stream is false", func(t *testing.T) {
		weknoraAgent := &WeKnoraAgent{
			name: "test-agent",
		}

		stream := false
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
			RunOptions: agent.RunOptions{
				Stream: &stream,
			},
		}

		eventChan, err := weknoraAgent.Run(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when stream is false")
		}
		if eventChan != nil {
			t.Error("expected nil event channel on error")
		}
	})
}
