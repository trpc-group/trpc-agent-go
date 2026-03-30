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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// MockN8nServer provides a mock n8n webhook server for testing.
type MockN8nServer struct {
	Server         *httptest.Server
	WebhookHandler func(w http.ResponseWriter, r *http.Request)
}

// NewMockN8nServer creates a new mock n8n server with default handlers.
func NewMockN8nServer() *MockN8nServer {
	mock := &MockN8nServer{}
	mock.WebhookHandler = mock.defaultWebhookHandler

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/test", func(w http.ResponseWriter, r *http.Request) {
		mock.WebhookHandler(w, r)
	})

	mock.Server = httptest.NewServer(mux)
	return mock
}

// Close closes the mock server.
func (m *MockN8nServer) Close() {
	m.Server.Close()
}

// WebhookURL returns the full webhook URL for tests.
func (m *MockN8nServer) WebhookURL() string {
	return m.Server.URL + "/webhook/test"
}

// defaultWebhookHandler routes to streaming or non-streaming based on Accept header.
func (m *MockN8nServer) defaultWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Accept") == "text/event-stream" {
		handleStreamingWebhook(w, r)
	} else {
		handleNonStreamingWebhook(w, r)
	}
}

// handleNonStreamingWebhook returns a standard JSON response.
func handleNonStreamingWebhook(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"output": "This is a mock response from n8n webhook",
	})
}

// handleStreamingWebhook returns SSE events.
func handleStreamingWebhook(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	events := []string{
		`data: {"output": "Hello "}`,
		`data: {"output": "from "}`,
		`data: {"output": "n8n!"}`,
		`data: [DONE]`,
	}

	for _, event := range events {
		w.Write([]byte(event + "\n\n"))
		flusher.Flush()
	}
}

// WithCustomResponse overrides the handler to return a custom JSON response.
func (m *MockN8nServer) WithCustomResponse(response map[string]any) {
	m.WebhookHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// WithError overrides the handler to return an error response.
func (m *MockN8nServer) WithError(statusCode int, message string) {
	m.WebhookHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]any{
			"code":    statusCode,
			"message": message,
		})
	}
}

// WithAuthValidation wraps the current handler with authentication validation.
func (m *MockN8nServer) WithAuthValidation(authType AuthType, expectedValue string) {
	originalHandler := m.WebhookHandler
	m.WebhookHandler = func(w http.ResponseWriter, r *http.Request) {
		switch authType {
		case AuthBasic:
			username, password, ok := r.BasicAuth()
			if !ok || fmt.Sprintf("%s:%s", username, password) != expectedValue {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{
					"error": "unauthorized",
				})
				return
			}
		case AuthHeader:
			authHeader := r.Header.Get("Authorization")
			apiKeyHeader := r.Header.Get("X-API-Key")
			if authHeader != expectedValue && apiKeyHeader != expectedValue {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{
					"error": "unauthorized",
				})
				return
			}
		}
		originalHandler(w, r)
	}
}

// WithStreamingEvents overrides the handler to return custom SSE events.
func (m *MockN8nServer) WithStreamingEvents(events []string) {
	m.WebhookHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		for _, event := range events {
			w.Write([]byte(event + "\n\n"))
			flusher.Flush()
		}
	}
}

// WithStreamingEventsDelay overrides the handler to return custom SSE events with a delay between each event.
func (m *MockN8nServer) WithStreamingEventsDelay(events []string, delay time.Duration) {
	m.WebhookHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		for _, event := range events {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
			w.Write([]byte(event + "\n\n"))
			flusher.Flush()
		}
	}
}

// ResetHandlers resets the webhook handler to the default.
func (m *MockN8nServer) ResetHandlers() {
	m.WebhookHandler = m.defaultWebhookHandler
}

// createMockN8nAgent creates a test agent connected to the mock server.
func createMockN8nAgent(t *testing.T, mockServer *MockN8nServer, opts ...Option) *N8nAgent {
	defaultOpts := []Option{
		WithName("test-agent"),
		WithWebhookURL(mockServer.WebhookURL()),
	}

	allOpts := append(defaultOpts, opts...)
	a, err := New(allOpts...)
	if err != nil {
		t.Fatalf("Failed to create mock agent: %v", err)
	}

	return a
}
