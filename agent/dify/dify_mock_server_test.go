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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// MockDifyServer provides a mock Dify API server for testing
type MockDifyServer struct {
	Server          *httptest.Server
	ChatflowHandler func(w http.ResponseWriter, r *http.Request)
	WorkflowHandler func(w http.ResponseWriter, r *http.Request)
	CustomResponse  map[string]any
	ErrorResponse   error
	ErrorStatusCode int
}

// NewMockDifyServer creates a new mock Dify server
func NewMockDifyServer() *MockDifyServer {
	mock := &MockDifyServer{}

	// Set default handlers
	mock.ChatflowHandler = defaultChatflowHandler
	mock.WorkflowHandler = func(w http.ResponseWriter, r *http.Request) {
		// If custom response is set, use it
		if mock.CustomResponse != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mock.CustomResponse)
			return
		}
		defaultWorkflowHandler(w, r)
	}

	mux := http.NewServeMux()

	// Chatflow endpoints
	mux.HandleFunc("/v1/chat-messages", func(w http.ResponseWriter, r *http.Request) {
		mock.ChatflowHandler(w, r)
	})

	// Workflow endpoints
	mux.HandleFunc("/v1/workflows/run", func(w http.ResponseWriter, r *http.Request) {
		mock.WorkflowHandler(w, r)
	})

	mock.Server = httptest.NewServer(mux)
	return mock
}

// Close shuts down the mock server
func (m *MockDifyServer) Close() {
	m.Server.Close()
}

// URL returns the mock server's URL
func (m *MockDifyServer) URL() string {
	return m.Server.URL
}

// defaultChatflowHandler is the default Chatflow handler
func defaultChatflowHandler(w http.ResponseWriter, r *http.Request) {
	// Check if it's a streaming request
	query := r.URL.Query()
	responseMode := query.Get("response_mode")

	if responseMode == "streaming" {
		handleStreamingChatflow(w, r)
	} else {
		handleNonStreamingChatflow(w, r)
	}
}

// defaultWorkflowHandler is the default Workflow handler
func defaultWorkflowHandler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseMode, _ := req["response_mode"].(string)
	if responseMode == "streaming" {
		handleStreamingWorkflow(w, r)
	} else {
		handleNonStreamingWorkflow(w, r)
	}
}

// handleNonStreamingChatflow handles non-streaming Chatflow requests
func handleNonStreamingChatflow(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"event":           "message",
		"message_id":      "mock-message-id",
		"conversation_id": "mock-conversation-id",
		"mode":            "chat",
		"answer":          "This is a mock response from Dify chatflow",
		"created_at":      1234567890,
		"metadata": map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		},
	}
	json.NewEncoder(w).Encode(response)
}

// handleStreamingChatflow handles streaming Chatflow requests
func handleStreamingChatflow(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send SSE events
	// In the real Dify API, all chunks of the same streaming reply share the same message_id
	events := []string{
		`data: {"event": "message", "message_id": "mock-msg-1", "conversation_id": "mock-conv-1", "answer": "Hello "}`,
		`data: {"event": "message", "message_id": "mock-msg-1", "conversation_id": "mock-conv-1", "answer": "from "}`,
		`data: {"event": "message", "message_id": "mock-msg-1", "conversation_id": "mock-conv-1", "answer": "Dify!"}`,
		`data: {"event": "message_end", "message_id": "mock-msg-1", "conversation_id": "mock-conv-1", "metadata": {"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}}}`,
	}

	for _, event := range events {
		w.Write([]byte(event + "\n\n"))
		flusher.Flush()
	}
}

// handleNonStreamingWorkflow handles non-streaming Workflow requests
func handleNonStreamingWorkflow(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"workflow_run_id": "mock-workflow-run-id",
		"task_id":         "mock-task-id",
		"data": map[string]any{
			"id":     "mock-workflow-id",
			"status": "succeeded",
			"outputs": map[string]any{
				"answer": "This is a mock workflow response",
			},
			"created_at": 1234567890,
		},
	}
	json.NewEncoder(w).Encode(response)
}

// handleStreamingWorkflow handles streaming Workflow requests
func handleStreamingWorkflow(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send SSE events
	events := []string{
		`data: {"event": "workflow_started", "task_id": "mock-task-1", "workflow_run_id": "mock-run-1"}`,
		`data: {"event": "node_started", "task_id": "mock-task-1", "workflow_run_id": "mock-run-1", "data": {"id": "node-1", "node_type": "llm"}}`,
		`data: {"event": "node_finished", "task_id": "mock-task-1", "workflow_run_id": "mock-run-1", "data": {"id": "node-1", "outputs": {"answer": "Workflow result"}}}`,
		`data: {"event": "workflow_finished", "task_id": "mock-task-1", "workflow_run_id": "mock-run-1", "data": {"status": "succeeded", "outputs": {"answer": "Workflow result"}}}`,
	}

	for _, event := range events {
		w.Write([]byte(event + "\n\n"))
		flusher.Flush()
	}
}

// WithCustomChatflowResponse sets a custom Chatflow response
func (m *MockDifyServer) WithCustomChatflowResponse(response map[string]any) {
	m.ChatflowHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// WithChatflowError sets the Chatflow handler to return an error
func (m *MockDifyServer) WithChatflowError(statusCode int, message string) {
	m.ChatflowHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]any{
			"code":    statusCode,
			"message": message,
		})
	}
}

// WithWorkflowError sets the Workflow handler to return an error
func (m *MockDifyServer) WithWorkflowError(statusCode int, message string) {
	m.WorkflowHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]any{
			"code":    statusCode,
			"message": message,
		})
	}
}

// SetCustomResponse sets a custom Workflow response
func (m *MockDifyServer) SetCustomResponse(response map[string]any) {
	m.CustomResponse = response
}

// ResetHandlers resets handlers to default values
func (m *MockDifyServer) ResetHandlers() {
	m.ChatflowHandler = defaultChatflowHandler
	m.WorkflowHandler = defaultWorkflowHandler
	m.CustomResponse = nil
}

// createMockDifyAgent creates a DifyAgent for testing, connected to the mock server
func createMockDifyAgent(t *testing.T, mockServer *MockDifyServer, opts ...Option) *DifyAgent {
	defaultOpts := []Option{
		WithName("test-agent"),
		WithBaseUrl(mockServer.URL()),
		WithGetDifyClientFunc(func(*agent.Invocation) (*dify.Client, error) {
			client := dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             mockServer.URL(),
				DefaultAPISecret: "mock-api-key",
			})
			return client, nil
		}),
	}

	allOpts := append(defaultOpts, opts...)
	difyAgent, err := New(allOpts...)
	if err != nil {
		t.Fatalf("Failed to create mock agent: %v", err)
	}

	return difyAgent
}
