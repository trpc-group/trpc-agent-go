//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/debug/internal/schema"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) Tools() []tool.Tool { return nil }

func (m *mockAgent) SubAgents() []agent.Agent { return nil }

func (m *mockAgent) FindSubAgent(name string) agent.Agent { return nil }

func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// Return a simple event channel for testing.
	events := make(chan *event.Event, 1)
	go func() {
		defer close(events)
		events <- &event.Event{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "test response",
					},
				}},
			},
		}
	}()
	return events, nil
}

func TestNew(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	if server == nil {
		t.Fatal("New() returned nil")
	}

	if len(server.agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(server.agents))
	}

	if server.agents["test-agent"] == nil {
		t.Error("agent not found in server")
	}
}

func TestNew_WithOptions(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	// Test with custom session service.
	customSessionSvc := &mockSessionService{}
	server := New(agents, WithSessionService(customSessionSvc))

	if server.sessionSvc != customSessionSvc {
		t.Error("custom session service not set")
	}
}

func TestServer_Handler(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	handler := server.Handler()

	if handler == nil {
		t.Fatal("Handler() returned nil")
	}
}

func TestServer_handleListApps(t *testing.T) {
	agents := map[string]agent.Agent{
		"agent1": &mockAgent{name: "agent1", description: "first agent"},
		"agent2": &mockAgent{name: "agent2", description: "second agent"},
	}

	server := New(agents)
	req := httptest.NewRequest(http.MethodGet, "/list-apps", nil)
	w := httptest.NewRecorder()

	server.handleListApps(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var apps []string
	if err := json.Unmarshal(w.Body.Bytes(), &apps); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(apps))
	}

	// Check that both agent names are present.
	found := make(map[string]bool)
	for _, app := range apps {
		found[app] = true
	}

	if !found["agent1"] || !found["agent2"] {
		t.Error("expected agent names not found in response")
	}
}

func TestServer_handleCreateSession(t *testing.T) {
	agents := map[string]agent.Agent{
		"test-agent": &mockAgent{name: "test-agent", description: "test description"},
	}

	server := New(agents)
	req := httptest.NewRequest(http.MethodPost, "/apps/test-agent/users/test-user/sessions", nil)
	w := httptest.NewRecorder()

	// Set up the route variables that gorilla/mux would normally set.
	req = mux.SetURLVars(req, map[string]string{
		"appName": "test-agent",
		"userId":  "test-user",
	})

	server.handleCreateSession(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var session schema.ADKSession
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if session.AppName != "test-agent" {
		t.Errorf("expected appName 'test-agent', got '%s'", session.AppName)
	}

	if session.UserID != "test-user" {
		t.Errorf("expected userId 'test-user', got '%s'", session.UserID)
	}

	if session.ID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestServer_handleRun(t *testing.T) {
	// Create a real LLM agent for this test.
	modelInstance := openai.New("test-model", openai.Options{})
	llmAgent := llmagent.New(
		"test-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("test agent"),
	)

	agents := map[string]agent.Agent{
		"test-agent": llmAgent,
	}

	server := New(agents)

	// Create a test request.
	requestBody := schema.AgentRunRequest{
		AppName:   "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
		NewMessage: schema.Content{
			Role: "user",
			Parts: []schema.Part{
				{Text: "Hello, world!"},
			},
		},
		Streaming: false,
	}

	bodyBytes, _ := json.Marshal(requestBody)
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleRun(w, req)

	// The request should fail because the model is not properly configured,
	// but we can verify the request was processed.
	if w.Code == http.StatusOK {
		// If it succeeded, verify the response structure.
		var response map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
	} else {
		// Expected to fail due to model configuration, but should not be 500.
		if w.Code == http.StatusInternalServerError {
			t.Errorf("unexpected internal server error: %s", w.Body.String())
		}
	}
}

func TestConvertContentToMessage(t *testing.T) {
	content := schema.Content{
		Role: "user",
		Parts: []schema.Part{
			{Text: "Hello, world!"},
		},
	}

	message := convertContentToMessage(content)

	if message.Role != model.RoleUser {
		t.Errorf("expected role 'user', got '%s'", message.Role)
	}

	if message.Content != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got '%s'", message.Content)
	}
}

func TestConvertContentToMessage_Func(t *testing.T) {
	content := schema.Content{
		Role: "assistant",
		Parts: []schema.Part{
			{
				FunctionCall: &schema.FunctionCall{
					Name: "test_function",
					Args: map[string]interface{}{
						"param1": "value1",
						"param2": 42,
					},
				},
			},
		},
	}

	message := convertContentToMessage(content)

	if message.Role != model.RoleAssistant {
		t.Errorf("expected role 'assistant', got '%s'", message.Role)
	}

	if len(message.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(message.ToolCalls))
	}

	toolCall := message.ToolCalls[0]
	if toolCall.Type != "function" {
		t.Errorf("expected type 'function', got '%s'", toolCall.Type)
	}

	if toolCall.Function.Name != "test_function" {
		t.Errorf("expected function name 'test_function', got '%s'", toolCall.Function.Name)
	}
}

func TestConvertSessionToADKFormat(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:        "test-session-id",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: now,
		UpdatedAt: now,
		State:     map[string][]byte{"key1": []byte("value1")},
	}

	adkSession := convertSessionToADKFormat(sess)

	if adkSession.ID != "test-session-id" {
		t.Errorf("expected ID 'test-session-id', got '%s'", adkSession.ID)
	}

	if adkSession.AppName != "test-app" {
		t.Errorf("expected AppName 'test-app', got '%s'", adkSession.AppName)
	}

	if adkSession.UserID != "test-user" {
		t.Errorf("expected UserID 'test-user', got '%s'", adkSession.UserID)
	}

	if adkSession.CreateTime == 0 {
		t.Error("expected non-zero CreateTime")
	}

	if adkSession.LastUpdateTime == 0 {
		t.Error("expected non-zero LastUpdateTime")
	}

	if len(adkSession.State) != 1 {
		t.Errorf("expected 1 state entry, got %d", len(adkSession.State))
	}
}

// mockSessionService is a simple mock session service for testing.
type mockSessionService struct {
	sessions map[string]*session.Session
}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	now := time.Now()
	sess := &session.Session{
		ID:        "mock-session-id",
		AppName:   key.AppName,
		UserID:    key.UserID,
		CreatedAt: now,
		UpdatedAt: now,
		State:     state,
	}
	return sess, nil
}

func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, nil
}

func (m *mockSessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return []*session.Session{}, nil
}

func (m *mockSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}

func (m *mockSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}

func (m *mockSessionService) AppendEvent(ctx context.Context, session *session.Session, event *event.Event, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) Close() error {
	return nil
}
