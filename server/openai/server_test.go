//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
	response    string
	streaming   bool
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	if m.streaming {
		// Send streaming events
		go func() {
			defer close(ch)
			words := []string{"Hello", " ", "world", "!"}
			for _, word := range words {
				select {
				case <-ctx.Done():
					return
				case ch <- &event.Event{
					ID: "test-event-id",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Role:    model.RoleAssistant,
									Content: word,
								},
							},
						},
						Created: time.Now().Unix(),
					},
				}:
				}
			}
			// Send final event
			finishReason := "stop"
			ch <- &event.Event{
				ID: "test-event-id-final",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    model.RoleAssistant,
								Content: "",
							},
							FinishReason: &finishReason,
						},
					},
					Done:    true,
					Created: time.Now().Unix(),
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
						TotalTokens:      15,
					},
				},
			}
		}()
	} else {
		// Send non-streaming event
		finishReason := "stop"
		ch <- &event.Event{
			ID: "test-event-id",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: m.response,
						},
						FinishReason: &finishReason,
					},
				},
				Done:    true,
				Created: time.Now().Unix(),
				Usage: &model.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
			},
		}
		close(ch)
	}
	return ch, nil
}

// mockRunner is a mock runner for testing.
type mockRunner struct {
	events chan *event.Event
	err    error
}

func (m *mockRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.events, nil
}

func (m *mockRunner) Close() error {
	if m.events != nil {
		close(m.events)
	}
	return nil
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		opts      []Option
		wantErr   bool
		errMsg    string
		checkFunc func(t *testing.T, s *Server)
	}{
		{
			name: "valid with agent",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test"}),
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s *Server) {
				assert.NotNil(t, s)
				assert.Equal(t, defaultBasePath, s.basePath)
				assert.Equal(t, defaultModelName, s.modelName)
				assert.NotNil(t, s.handler)
			},
		},
		{
			name: "valid with runner",
			opts: []Option{
				WithRunner(&mockRunner{events: make(chan *event.Event)}),
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s *Server) {
				assert.NotNil(t, s)
				assert.NotNil(t, s.runner)
			},
		},
		{
			name: "with custom base path",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1"),
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s *Server) {
				assert.Equal(t, "/api/v1", s.basePath)
			},
		},
		{
			name: "with custom model name",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithModelName("gpt-4"),
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s *Server) {
				assert.Equal(t, "gpt-4", s.modelName)
			},
		},
		{
			name: "with custom session service",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithSessionService(inmemory.NewSessionService()),
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s *Server) {
				assert.NotNil(t, s.sessionService)
			},
		},
		{
			name:    "missing agent and runner",
			opts:    []Option{},
			wantErr: true,
			errMsg:  "either agent or runner must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(tt.opts...)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				assert.Nil(t, s)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, s)
				if tt.checkFunc != nil {
					tt.checkFunc(t, s)
				}
			}
		})
	}
}

func TestServer_Handler(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)
	require.NotNil(t, s)

	handler := s.Handler()
	assert.NotNil(t, handler)
}

func TestServer_BasePath(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		expected string
	}{
		{
			name:     "default base path",
			opts:     []Option{WithAgent(&mockAgent{name: "test-agent"})},
			expected: defaultBasePath,
		},
		{
			name: "custom base path",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1"),
			},
			expected: "/api/v1",
		},
		{
			name: "base path with trailing slash",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1/"),
			},
			expected: "/api/v1/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(tt.opts...)
			require.NoError(t, err)
			require.NotNil(t, s)

			basePath := s.BasePath()
			assert.Equal(t, tt.expected, basePath)
		})
	}
}

func TestServer_Path(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		expected string
	}{
		{
			name:     "default path",
			opts:     []Option{WithAgent(&mockAgent{name: "test-agent"})},
			expected: "/v1/chat/completions",
		},
		{
			name: "custom base path",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1"),
			},
			expected: "/api/v1/chat/completions",
		},
		{
			name: "custom path",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithPath("/completions"),
			},
			expected: "/v1/completions",
		},
		{
			name: "custom base path and path",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1"),
				WithPath("/completions"),
			},
			expected: "/api/v1/completions",
		},
		{
			name: "base path with trailing slash",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithBasePath("/api/v1/"),
			},
			expected: "/api/v1/chat/completions",
		},
		{
			name: "path with leading slash",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent"}),
				WithPath("/completions"),
			},
			expected: "/v1/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(tt.opts...)
			require.NoError(t, err)
			require.NotNil(t, s)

			path := s.Path()
			assert.Equal(t, tt.expected, path)
		})
	}
}

func TestServer_Close(t *testing.T) {
	tests := []struct {
		name        string
		setupServer func() *Server
		checkClose  func(t *testing.T, s *Server)
	}{
		{
			name: "close server with owned runner",
			setupServer: func() *Server {
				s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
				require.NoError(t, err)
				return s
			},
			checkClose: func(t *testing.T, s *Server) {
				err := s.Close()
				assert.NoError(t, err)
				// Close again should be safe.
				err = s.Close()
				assert.NoError(t, err)
			},
		},
		{
			name: "close server with provided runner",
			setupServer: func() *Server {
				mockRunner := &mockRunner{events: make(chan *event.Event)}
				s, err := New(WithRunner(mockRunner))
				require.NoError(t, err)
				return s
			},
			checkClose: func(t *testing.T, s *Server) {
				// Server should not close runner provided by user.
				err := s.Close()
				assert.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.setupServer()
			require.NotNil(t, s)
			tt.checkClose(t, s)
		})
	}
}

func TestServer_handleCORS(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	s.handleCORS(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get(headerAccessControlOrigin))
	assert.Equal(t, http.MethodPost, w.Header().Get(headerAccessControlMethods))
}

func TestServer_handleChatCompletions_MethodNotAllowed(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, http.MethodPost, w.Header().Get(headerAllow))
}

func TestServer_handleChatCompletions_InvalidJSON(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))

	var errorResp openAIError
	err = json.NewDecoder(w.Body).Decode(&errorResp)
	require.NoError(t, err)
	assert.Equal(t, errorTypeInvalidRequest, errorResp.Error.Type)
}

func TestServer_handleNonStreaming(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello, world!",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))

	var response openAIResponse
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)
	assert.Equal(t, objectChatCompletion, response.Object)
	assert.NotEmpty(t, response.ID)
	assert.Len(t, response.Choices, 1)
	assert.Equal(t, "Hello, world!", response.Choices[0].Message.Content)
	assert.NotNil(t, response.Usage)
}

func TestServer_handleNonStreaming_EmptyMessages(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model:    defaultModelName,
		Messages: []openAIMessage{},
		Stream:   false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_handleStreaming_EmptyMessages(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model:    defaultModelName,
		Messages: []openAIMessage{},
		Stream:   true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(bodyBytes),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))

	var errorResp openAIError
	err = json.NewDecoder(w.Body).Decode(&errorResp)
	require.NoError(t, err)
	assert.Equal(t, errorTypeInvalidRequest, errorResp.Error.Type)
}

func TestServer_handleStreaming(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		streaming: true,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentTypeEventStream, w.Header().Get(headerContentType))
	assert.Equal(t, cacheControlNoCache, w.Header().Get(headerCacheControl))
	assert.Equal(t, connectionKeepAlive, w.Header().Get(headerConnection))

	body := w.Body.String()
	assert.Contains(t, body, sseDataPrefix)
	assert.Contains(t, body, sseDoneMarker)
}

func TestServer_handleStreaming_NoFlusher(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := &mockResponseWriter{ResponseWriter: httptest.NewRecorder()}

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_writeJSON(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	data := map[string]string{"message": "test"}

	s.writeJSON(w, data)

	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
	assert.Equal(t, http.StatusOK, w.Code)

	var result map[string]string
	err = json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "test", result["message"])
}

func TestServer_writeError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	testErr := errors.New("test error")

	s.writeError(w, testErr, errorTypeInvalidRequest, http.StatusBadRequest)

	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errorResp openAIError
	err = json.NewDecoder(w.Body).Decode(&errorResp)
	require.NoError(t, err)
	assert.Equal(t, errorTypeInvalidRequest, errorResp.Error.Type)
	assert.Equal(t, "test error", errorResp.Error.Message)
}

func TestServer_shouldSendChunk(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	tests := []struct {
		name  string
		chunk *openAIChunk
		want  bool
	}{
		{
			name: "chunk with content",
			chunk: &openAIChunk{
				Choices: []openAIChunkChoice{
					{
						Delta: openAIMessage{
							Content: "Hello",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "chunk with finish reason",
			chunk: &openAIChunk{
				Choices: []openAIChunkChoice{
					{
						FinishReason: stringPtr("stop"),
					},
				},
			},
			want: true,
		},
		{
			name: "empty chunk",
			chunk: &openAIChunk{
				Choices: []openAIChunkChoice{
					{
						Delta: openAIMessage{
							Role:    "", // Explicitly empty
							Content: "",
						},
						FinishReason: nil,
					},
				},
			},
			want: false,
		},
		{
			name: "no choices",
			chunk: &openAIChunk{
				Choices: []openAIChunkChoice{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.shouldSendChunk(tt.chunk)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestServer_writeChunk(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	chunk := &openAIChunk{
		ID:      "test-id",
		Object:  objectChatCompletionChunk,
		Created: time.Now().Unix(),
		Model:   "gpt-3.5-turbo",
		Choices: []openAIChunkChoice{
			{
				Delta: openAIMessage{
					Content: "Hello",
				},
			},
		},
	}

	result := s.writeChunk(w, flusher, chunk)

	assert.True(t, result)
	assert.True(t, flusher.flushed)
	body := w.Body.String()
	assert.Contains(t, body, sseDataPrefix)
	assert.Contains(t, body, "Hello")
}

// mockResponseWriter is a mock http.ResponseWriter that doesn't implement http.Flusher.
type mockResponseWriter struct {
	http.ResponseWriter
	Code int
}

func (m *mockResponseWriter) WriteHeader(code int) {
	m.Code = code
	m.ResponseWriter.WriteHeader(code)
}

// mockFlusher is a mock http.Flusher for testing.
type mockFlusher struct {
	http.ResponseWriter
	flushed bool
}

func (m *mockFlusher) Flush() {
	m.flushed = true
}

func stringPtr(s string) *string {
	return &s
}

// mockRunnerWithError is a mock runner that returns an error.
type mockRunnerWithError struct {
	err error
}

func (m *mockRunnerWithError) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return nil, m.err
}

func (m *mockRunnerWithError) Close() error {
	return errors.New("close error")
}

// mockRunnerWithEmptyEvents is a mock runner that returns empty events.
type mockRunnerWithEmptyEvents struct {
	events chan *event.Event
}

func (m *mockRunnerWithEmptyEvents) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithEmptyEvents) Close() error {
	return nil
}

// mockRunnerWithNilEvents is a mock runner that returns nil events.
type mockRunnerWithNilEvents struct {
	events chan *event.Event
}

func (m *mockRunnerWithNilEvents) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithNilEvents) Close() error {
	return nil
}

// mockRunnerWithAggregateError is a mock runner that returns events that will cause aggregate error.
type mockRunnerWithAggregateError struct {
	events chan *event.Event
}

func (m *mockRunnerWithAggregateError) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithAggregateError) Close() error {
	return nil
}

// mockRunnerWithPartialEvents is a mock runner that returns partial events.
type mockRunnerWithPartialEvents struct {
	events chan *event.Event
}

func (m *mockRunnerWithPartialEvents) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithPartialEvents) Close() error {
	return nil
}

// mockRunnerWithContextCancel is a mock runner that respects context cancellation.
type mockRunnerWithContextCancel struct {
	events chan *event.Event
}

func (m *mockRunnerWithContextCancel) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithContextCancel) Close() error {
	return nil
}

// mockRunnerWithFinalChunk is a mock runner that returns events with usage for final chunk.
type mockRunnerWithFinalChunk struct {
	events chan *event.Event
}

func (m *mockRunnerWithFinalChunk) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	return m.events, nil
}

func (m *mockRunnerWithFinalChunk) Close() error {
	return nil
}

// mockResponseWriterWithError is a mock ResponseWriter that fails on Encode.
type mockResponseWriterWithError struct {
	http.ResponseWriter
	Code int
}

func (m *mockResponseWriterWithError) WriteHeader(code int) {
	m.Code = code
}

func (m *mockResponseWriterWithError) Write([]byte) (int, error) {
	return 0, errors.New("write error")
}

func TestServer_Close_WithError(t *testing.T) {
	// Create a server with owned runner that will return error on Close.
	agent := &mockAgent{name: "test-agent"}
	s, err := New(WithAgent(agent))
	require.NoError(t, err)
	require.NotNil(t, s)

	// Replace runner with one that returns error on Close.
	s.runner = &mockRunnerWithError{err: errors.New("close error")}
	s.ownedRunner = true

	// Close should return error.
	err = s.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close error")
}

func TestServer_handleNonStreaming_RunnerError(t *testing.T) {
	s, err := New(WithRunner(&mockRunnerWithError{err: errors.New("runner error")}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_handleNonStreaming_InvalidRole(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "invalid",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(bodyBytes),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_handleNonStreaming_EmptyEvents(t *testing.T) {
	emptyCh := make(chan *event.Event)
	close(emptyCh)
	s, err := New(WithRunner(&mockRunnerWithEmptyEvents{events: emptyCh}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_handleNonStreaming_NilEvents(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ch <- nil
	close(ch)
	s, err := New(WithRunner(&mockRunnerWithNilEvents{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_handleNonStreaming_ResponseIDEmpty(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response openAIResponse
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)
	assert.NotEmpty(t, response.ID)
}

func TestServer_handleNonStreaming_WithHistory(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "system",
				Content: "You are a helpful assistant",
			},
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleNonStreaming_WithSessionID(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set(headerSessionID, "test-session-id")
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleNonStreaming_WithUserID(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
		User:   "test-user",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleStreaming_RunnerError(t *testing.T) {
	s, err := New(WithRunner(&mockRunnerWithError{err: errors.New("runner error")}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServer_handleStreaming_ContextCancel(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.
	s, err := New(WithRunner(&mockRunnerWithContextCancel{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	// Should return early due to context cancellation.
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleStreaming_NilEvent(t *testing.T) {
	ch := make(chan *event.Event, 2)
	ch <- nil
	ch <- &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "Hello",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}
	close(ch)
	s, err := New(WithRunner(&mockRunnerWithNilEvents{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleStreaming_NilResponse(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		ID:       "test-id",
		Response: nil,
	}
	close(ch)
	s, err := New(WithRunner(&mockRunnerWithNilEvents{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleStreaming_PartialEvent(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "Hello",
					},
				},
			},
			IsPartial: true,
			Done:      true,
			Created:   time.Now().Unix(),
		},
	}
	close(ch)
	s, err := New(WithRunner(&mockRunnerWithPartialEvents{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_handleStreaming_FinalChunk(t *testing.T) {
	ch := make(chan *event.Event, 2)
	finishReason := "stop"
	ch <- &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "Hello",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}
	ch <- &event.Event{
		ID: "test-id-final",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "",
					},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}
	close(ch)
	s, err := New(WithRunner(&mockRunnerWithFinalChunk{events: ch}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, sseDoneMarker)
}

func TestServer_sendFinalChunk(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	finishReason := "stop"
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					FinishReason: &finishReason,
				},
			},
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
	}

	s.sendFinalChunk(w, flusher, evt, "test-response-id", time.Now().Unix())

	assert.True(t, flusher.flushed)
	body := w.Body.String()
	assert.Contains(t, body, sseDataPrefix)
}

func TestServer_sendFinalChunk_NoUsage(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{},
		},
	}

	s.sendFinalChunk(w, flusher, evt, "test-response-id", time.Now().Unix())

	// Should return early without writing.
	body := w.Body.String()
	assert.Empty(t, body)
}

func TestServer_processStreamingChunk_ConvertError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	// Create an event that will cause convertToChunk to return error.
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "test",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}

	isFinal := s.processStreamingChunk(context.Background(), w, flusher, evt, "test-id", time.Now().Unix())

	assert.False(t, isFinal)
}

func TestServer_processStreamingChunk_NilChunk(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	// Create an event that will return nil chunk.
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "",
						Role:    "",
					},
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
		},
	}

	isFinal := s.processStreamingChunk(context.Background(), w, flusher, evt, "test-id", time.Now().Unix())

	assert.True(t, isFinal)
}

func TestServer_writeChunk_MarshalError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	// Create a chunk with invalid data that will cause marshal error.
	// Use a channel which cannot be marshaled.
	chunk := &openAIChunk{
		ID:      "test-id",
		Object:  objectChatCompletionChunk,
		Created: time.Now().Unix(),
		Model:   "gpt-3.5-turbo",
		Choices: []openAIChunkChoice{
			{
				Delta: openAIMessage{
					Content: make(chan int), // Invalid content type.
				},
			},
		},
	}

	result := s.writeChunk(w, flusher, chunk)

	assert.False(t, result)
}

func TestServer_writeJSON_EncodeError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := &mockResponseWriterWithError{ResponseWriter: httptest.NewRecorder()}
	// Use a value that cannot be encoded (channel).
	data := make(chan int)

	s.writeJSON(w, data)

	// Should handle error gracefully (just log).
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
}

func TestServer_writeError_EncodeError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := &mockResponseWriterWithError{ResponseWriter: httptest.NewRecorder()}
	testErr := errors.New("test error")

	s.writeError(w, testErr, errorTypeInvalidRequest, http.StatusBadRequest)

	// Should handle error gracefully (just log).
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_processStreamingChunk_WriteChunkError(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	// Create an event that will cause writeChunk to fail.
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "test",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}

	// Use a writer that fails on Write.
	w2 := &mockResponseWriterWithError{ResponseWriter: httptest.NewRecorder()}
	flusher2 := &mockFlusher{ResponseWriter: w2}

	isFinal := s.processStreamingChunk(context.Background(), w2, flusher2, evt, "test-id", time.Now().Unix())

	// writeChunk will fail, so should return false.
	assert.False(t, isFinal)
}

func TestServer_processStreamingChunk_NotFinal(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	flusher := &mockFlusher{ResponseWriter: w}
	evt := &event.Event{
		ID: "test-id",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: "test",
					},
				},
			},
			Done:    false,
			Created: time.Now().Unix(),
		},
	}

	isFinal := s.processStreamingChunk(context.Background(), w, flusher, evt, "test-id", time.Now().Unix())

	assert.False(t, isFinal)
}

func TestServer_handleChatCompletions_PathWithSlash(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{
		name:      "test-agent",
		response:  "Hello",
		streaming: false,
	}))
	require.NoError(t, err)

	reqBody := openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: false,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	// Test path with trailing slash.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions/", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// mockRunnerCapturing captures the RunOptions passed to Run and returns
// the pre-seeded events channel.
type mockRunnerCapturing struct {
	events  chan *event.Event
	gotOpts agent.RunOptions
	gotMsg  model.Message
}

func (m *mockRunnerCapturing) Run(
	ctx context.Context,
	userID, sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	m.gotOpts = agent.NewRunOptions(opts...)
	m.gotMsg = message
	return m.events, nil
}

func (m *mockRunnerCapturing) Close() error {
	return nil
}

// newToolCallEventsNonStreaming returns a channel with a single non-streaming
// event carrying an assistant tool_call message with finish_reason="tool_calls".
func newToolCallEventsNonStreaming(t *testing.T) chan *event.Event {
	t.Helper()
	ch := make(chan *event.Event, 1)
	finishReason := finishReasonToolCalls
	ch <- &event.Event{
		ID: "evt-tool-call",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "client_search",
									Arguments: []byte(`{"query":"go"}`),
								},
							},
						},
					},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage: &model.Usage{
				PromptTokens:     8,
				CompletionTokens: 4,
				TotalTokens:      12,
			},
		},
	}
	close(ch)
	return ch
}

func externalToolsRequestBody(t *testing.T, stream bool) []byte {
	t.Helper()
	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "search for go"},
		},
		Stream: stream,
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        "client_search",
					Description: "Search a frontend-owned source.",
					Parameters: json.RawMessage(
						`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`,
					),
				},
			},
		},
	})
	require.NoError(t, err)
	return body
}

func TestServer_handleNonStreaming_InjectsExternalTools(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(externalToolsRequestBody(t, false)),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, runner.gotOpts.ExternalTools, 1)
	decl := runner.gotOpts.ExternalTools[0].Declaration()
	require.NotNil(t, decl)
	assert.Equal(t, "client_search", decl.Name)
	assert.Equal(t, "Search a frontend-owned source.", decl.Description)
	require.NotNil(t, decl.InputSchema)
	assert.Equal(t, jsonSchemaTypeObject, decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, "query")
	assert.False(t, runner.gotOpts.ShouldExecuteTool(
		context.Background(),
		runner.gotOpts.ExternalTools[0],
	))
}

func TestServer_handleNonStreaming_SkipsExternalToolsWhenToolChoiceNone(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "search for go"},
		},
		ToolChoice: openAIToolChoiceNone,
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        "client_search",
					Description: "Search a frontend-owned source.",
					Parameters: json.RawMessage(
						`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`,
					),
				},
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(body),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, runner.gotOpts.ExternalTools)
}

func TestServer_handleNonStreaming_RejectsRequiredToolChoice(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "search for go"},
		},
		ToolChoice: "required",
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name: "client_search",
				},
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(body),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_handleNonStreaming_RejectsForcedFunctionToolChoice(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "search for go"},
		},
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "client_search"},
		},
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name: "client_search",
				},
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(body),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServer_handleNonStreaming_ToolCallResponse(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(externalToolsRequestBody(t, false)),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp openAIResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Choices, 1)
	require.NotNil(t, resp.Choices[0].FinishReason)
	assert.Equal(t, finishReasonToolCalls, *resp.Choices[0].FinishReason)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	tc := resp.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, "call-1", tc.ID)
	assert.Equal(t, "function", tc.Type)
	assert.Equal(t, "client_search", tc.Function.Name)
	assert.Equal(t, `{"query":"go"}`, tc.Function.Arguments)
}

func TestServer_handleStreaming_ToolCallResponse(t *testing.T) {
	ch := make(chan *event.Event, 2)
	ch <- &event.Event{
		ID: "evt-tool-delta",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      "client_search",
									Arguments: []byte(`{"query":"go"}`),
								},
							},
						},
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}
	finishReason := finishReasonToolCalls
	ch <- &event.Event{
		ID: "evt-tool-final",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta:        model.Message{},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage: &model.Usage{
				PromptTokens:     8,
				CompletionTokens: 4,
				TotalTokens:      12,
			},
		},
	}
	close(ch)

	runner := &mockRunnerCapturing{events: ch}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(externalToolsRequestBody(t, true)),
	)
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, runner.gotOpts.ExternalTools, 1)

	chunks, done := parseSSEChunks(t, w.Body.String())
	assert.True(t, done, "expected [DONE] marker in SSE stream")
	// The stream should contain at least one chunk with tool_calls in delta
	// and end with a chunk whose finish_reason is "tool_calls".
	var sawToolCallDelta bool
	var finalFinishReason string
	for _, c := range chunks {
		if len(c.Choices) == 0 {
			continue
		}
		if len(c.Choices[0].Delta.ToolCalls) > 0 {
			sawToolCallDelta = true
			tc := c.Choices[0].Delta.ToolCalls[0]
			assert.Equal(t, "call-1", tc.ID)
			assert.Equal(t, "client_search", tc.Function.Name)
			assert.Equal(t, `{"query":"go"}`, tc.Function.Arguments)
		}
		if c.Choices[0].FinishReason != nil {
			finalFinishReason = *c.Choices[0].FinishReason
		}
	}
	assert.True(t, sawToolCallDelta, "expected a chunk with delta.tool_calls")
	assert.Equal(t, finishReasonToolCalls, finalFinishReason)
}

func TestServer_handleChatCompletions_InvalidToolMissingName(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "hi"},
		},
		Tools: []openAITool{
			{Type: "function", Function: openAIFunction{Description: "no name"}},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp openAIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.Equal(t, errorTypeInvalidRequest, errResp.Error.Type)
	assert.Contains(t, errResp.Error.Message, "function.name")
}

func TestServer_handleChatCompletions_InvalidToolUnsupportedType(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "hi"},
		},
		Stream: true,
		Tools: []openAITool{
			{
				Type:     "code_interpreter",
				Function: openAIFunction{Name: "ignored"},
			},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp openAIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.Contains(t, errResp.Error.Message, "code_interpreter")
}

func TestServer_handleChatCompletions_InvalidToolMessageMissingID(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "assistant", Content: "calling tools"},
			{Role: "tool", Content: "result without id"},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp openAIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.Equal(t, errorTypeInvalidRequest, errResp.Error.Type)
	assert.Contains(t, errResp.Error.Message, errToolMessageMissingID)
}

func TestServer_handleChatCompletions_InvalidToolMessageMissingID_Streaming(t *testing.T) {
	s, err := New(WithAgent(&mockAgent{name: "test-agent"}))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model:  "gpt-3.5-turbo",
		Stream: true,
		Messages: []openAIMessage{
			{Role: "assistant", Content: "calling tools"},
			{Role: "tool", Content: "result without id"},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp openAIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.Contains(t, errResp.Error.Message, errToolMessageMissingID)
}

func TestServer_handleNonStreaming_ParallelToolResultResume(t *testing.T) {
	ch := make(chan *event.Event, 1)
	finishReason := finishReasonStop
	ch <- &event.Event{
		ID: "evt-final",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "combined answer",
					},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage:   &model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	close(ch)

	runner := &mockRunnerCapturing{events: ch}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "assistant", Content: "calling tools"},
			{
				Role:       "tool",
				ToolCallID: "call-1",
				Name:       "search",
				Content:    "result 1",
			},
			{
				Role:       "tool",
				ToolCallID: "call-2",
				Name:       "lookup",
				Content:    "result 2",
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, model.RoleTool, runner.gotMsg.Role)
	assert.Equal(t, "call-2", runner.gotMsg.ToolID)
	require.Len(t, runner.gotOpts.Messages, 1)
	assert.Equal(t, "assistant", string(runner.gotOpts.Messages[0].Role))
	require.NotNil(t, runner.gotOpts.UserMessageRewriter)

	currentTurn, err := runner.gotOpts.UserMessageRewriter(
		context.Background(),
		&agent.UserMessageRewriteArgs{OriginalMessage: runner.gotMsg},
	)
	require.NoError(t, err)
	require.Len(t, currentTurn, 2)
	assert.Equal(t, "call-1", currentTurn[0].ToolID)
	assert.Equal(t, "result 1", currentTurn[0].Content)
	assert.Equal(t, "call-2", currentTurn[1].ToolID)
	assert.Equal(t, "result 2", currentTurn[1].Content)

	var resp openAIResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "combined answer", resp.Choices[0].Message.Content)
}

func TestServer_handleStreaming_ParallelToolResultResume(t *testing.T) {
	ch := make(chan *event.Event, 2)
	finishReason := finishReasonStop
	ch <- &event.Event{
		ID: "evt-stream",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: "combined answer",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}
	ch <- &event.Event{
		ID: "evt-final",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta:        model.Message{},
					FinishReason: &finishReason,
				},
			},
			Done:    true,
			Created: time.Now().Unix(),
			Usage:   &model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	close(ch)

	runner := &mockRunnerCapturing{events: ch}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model:  "gpt-3.5-turbo",
		Stream: true,
		Messages: []openAIMessage{
			{Role: "assistant", Content: "calling tools"},
			{
				Role:       "tool",
				ToolCallID: "call-1",
				Name:       "search",
				Content:    "result 1",
			},
			{
				Role:       "tool",
				ToolCallID: "call-2",
				Name:       "lookup",
				Content:    "result 2",
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentTypeEventStream, w.Header().Get(headerContentType))
	assert.Equal(t, model.RoleTool, runner.gotMsg.Role)
	assert.Equal(t, "call-2", runner.gotMsg.ToolID)
	require.Len(t, runner.gotOpts.Messages, 1)
	require.NotNil(t, runner.gotOpts.UserMessageRewriter)

	currentTurn, err := runner.gotOpts.UserMessageRewriter(
		context.Background(),
		&agent.UserMessageRewriteArgs{OriginalMessage: runner.gotMsg},
	)
	require.NoError(t, err)
	require.Len(t, currentTurn, 2)
	assert.Equal(t, "call-1", currentTurn[0].ToolID)
	assert.Equal(t, "call-2", currentTurn[1].ToolID)

	chunks, done := parseSSEChunks(t, w.Body.String())
	assert.True(t, done)
	require.NotEmpty(t, chunks)
	var content string
	for _, chunk := range chunks {
		if len(chunk.Choices) == 0 {
			continue
		}
		if c, ok := chunk.Choices[0].Delta.Content.(string); ok {
			content += c
		}
	}
	assert.Equal(t, "combined answer", content)
}

func TestServer_handleNonStreaming_AssistantLastMessagePrefill(t *testing.T) {
	runner := &mockRunnerCapturing{events: newToolCallEventsNonStreaming(t)}
	s, err := New(WithRunner(runner))
	require.NoError(t, err)

	body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: "continue"},
			{Role: "assistant", Content: "partial "},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, model.RoleAssistant, runner.gotMsg.Role)
	assert.Equal(t, "partial ", runner.gotMsg.Content)
	require.Len(t, runner.gotOpts.Messages, 1)
	assert.Equal(t, model.RoleUser, runner.gotOpts.Messages[0].Role)
}

// parseSSEChunks extracts openAIChunk payloads from an SSE response body.
// It returns the parsed chunks and whether the [DONE] marker was seen.
func parseSSEChunks(t *testing.T, body string) ([]openAIChunk, bool) {
	t.Helper()
	var chunks []openAIChunk
	var sawDone bool
	for _, block := range strings.Split(body, sseLineEnding) {
		line := strings.TrimPrefix(strings.TrimSpace(block), sseDataPrefix)
		if line == "" {
			continue
		}
		if line == sseDoneMarker {
			sawDone = true
			continue
		}
		var chunk openAIChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("failed to unmarshal SSE chunk %q: %v", line, err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, sawDone
}
