//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubTool implements tool.Tool for testing purposes.
type stubTool struct{ decl *tool.Declaration }

// Call implements tool.Tool for testing.
func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }

// Declaration returns the tool declaration.
func (s stubTool) Declaration() *tool.Declaration { return s.decl }

type stubLogger struct {
	debugfCalled bool
	debugfMsg    string
}

func (stubLogger) Debug(args ...any) {}
func (l *stubLogger) Debugf(format string, args ...any) {
	l.debugfCalled = true
	l.debugfMsg = fmt.Sprintf(format, args...)
}
func (stubLogger) Info(args ...any)                  {}
func (stubLogger) Infof(format string, args ...any)  {}
func (stubLogger) Warn(args ...any)                  {}
func (stubLogger) Warnf(format string, args ...any)  {}
func (stubLogger) Error(args ...any)                 {}
func (stubLogger) Errorf(format string, args ...any) {}
func (stubLogger) Fatal(args ...any)                 {}
func (stubLogger) Fatalf(format string, args ...any) {}

// Test_Model_Info tests the Info method.
func Test_Model_Info(t *testing.T) {
	m := New("llama3.2:latest")
	info := m.Info()
	assert.Equal(t, "llama3.2:latest", info.Name)
}

// TestNew tests the constructor with various options.
func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		fn       func()
		teardown func()
		expected *Model
	}{
		{
			name: "default options",
			opts: []Option{},
			expected: &Model{
				name:              "test-model",
				host:              "http://localhost:11434",
				channelBufferSize: defaultChannelBufferSize,
			},
		},
		{
			name: "custom options",
			opts: []Option{
				WithHost("http://custom:8080"),
				WithChannelBufferSize(512),
				WithEnableTokenTailoring(true),
				WithMaxInputTokens(1000),
				WithOptions(map[string]any{"temperature": 0.7}),
				WithKeepAlive(30 * time.Second),
				withHttpClient(http.DefaultClient),
			},
			expected: &Model{
				name:                 "test-model",
				host:                 "http://custom:8080",
				channelBufferSize:    512,
				enableTokenTailoring: true,
				maxInputTokens:       1000,
				options:              map[string]any{"temperature": 0.7},
			},
		},
		{
			name: "set host from env",
			fn: func() {
				os.Setenv(OllamaHost, "http://ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			expected: &Model{
				name:              "test-model",
				host:              "http://ollama.com:80",
				channelBufferSize: defaultChannelBufferSize,
			},
		},
		{
			name: "set host env but override with option",
			fn: func() {
				os.Setenv(OllamaHost, "http://ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			opts: []Option{
				WithHost("https://localhost:443"),
			},
			expected: &Model{
				name:              "test-model",
				host:              "https://localhost:443",
				channelBufferSize: defaultChannelBufferSize,
			},
		},
		{
			name: "ollama.com host",
			fn: func() {
				os.Setenv(OllamaHost, "ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			expected: &Model{
				name:              "test-model",
				host:              "https://ollama.com:443",
				channelBufferSize: defaultChannelBufferSize,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.fn != nil {
				tt.fn()
			}
			if tt.teardown != nil {
				defer tt.teardown()
			}
			m := New("test-model", tt.opts...)
			assert.Equal(t, tt.expected.name, m.name)
			assert.Equal(t, tt.expected.host, m.host)
			assert.Equal(t, tt.expected.channelBufferSize, m.channelBufferSize)
			assert.Equal(t, tt.expected.enableTokenTailoring, m.enableTokenTailoring)
			assert.Equal(t, tt.expected.maxInputTokens, m.maxInputTokens)
			if tt.expected.options != nil {
				assert.Equal(t, tt.expected.options, m.options)
			}
		})
	}
}

// Test_Model_GenerateContent_NilRequest tests nil request handling.
func Test_Model_GenerateContent_NilRequest(t *testing.T) {
	m := New("llama3.2:latest")
	ctx := context.Background()
	ch, err := m.GenerateContent(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, ch)
}

// Test_convertMessages tests message conversion.
func Test_convertMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		validate func(t *testing.T, messages []api.Message)
		wantLen  int
		wantErr  bool
	}{
		{
			name: "user message",
			messages: []model.Message{
				model.NewUserMessage("hello"),
			},
			wantLen: 1,
			validate: func(t *testing.T, messages []api.Message) {
				assert.Equal(t, "user", messages[0].Role)
				assert.Equal(t, "hello", messages[0].Content)
			},
			wantErr: false,
		},
		{
			name: "system and user messages",
			messages: []model.Message{
				model.NewSystemMessage("You are helpful"),
				model.NewUserMessage("hello"),
			},
			validate: func(t *testing.T, messages []api.Message) {
				assert.Equal(t, "system", messages[0].Role)
				assert.Equal(t, "You are helpful", messages[0].Content)
				assert.Equal(t, "user", messages[1].Role)
				assert.Equal(t, "hello", messages[1].Content)
			},
			wantLen: 2,
			wantErr: false,
		},
		{
			name: "assistant message with tool calls",
			messages: []model.Message{
				{
					Role:    model.RoleAssistant,
					Content: "Let me help",
					ToolCalls: []model.ToolCall{
						{
							ID:   "call1",
							Type: functionToolType,
							Function: model.FunctionDefinitionParam{
								Name:      "get_weather",
								Arguments: []byte(`{"city":"Beijing"}`),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, messages []api.Message) {
				assert.Equal(t, "assistant", messages[0].Role)
				assert.Equal(t, "Let me help", messages[0].Content)
				assert.Equal(t, 1, len(messages[0].ToolCalls))
				assert.Equal(t, "get_weather", messages[0].ToolCalls[0].Function.Name)
				assert.Equal(t, map[string]any{"city": "Beijing"}, messages[0].ToolCalls[0].Function.Arguments)
			},
			wantLen: 1,
			wantErr: false,
		},
		{
			name: "tool result message",
			messages: []model.Message{
				{
					Role:    model.RoleTool,
					Content: "Weather is sunny",
					ToolID:  "call1",
				},
			},
			wantLen: 1,
			wantErr: false,
		},
		{
			name: "image data",
			messages: []model.Message{
				{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: "image",
							Image: &model.Image{
								Data: []byte("fake image data"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, messages []api.Message) {
				assert.Equal(t, "user", messages[0].Role)
				assert.Equal(t, 1, len(messages[0].Images))
				assert.Equal(t, "ZmFrZSBpbWFnZSBkYXRh", string(messages[0].Images[0]))
			},
			wantLen: 1,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertMessages(tt.messages)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantLen, len(result))
			}
		})
	}
}

// Test_convertMessage tests single message conversion.
func Test_convertMessage(t *testing.T) {
	tests := []struct {
		name    string
		msg     model.Message
		wantErr bool
	}{
		{
			name: "user message with text",
			msg: model.Message{
				Role:    model.RoleUser,
				Content: "hello",
			},
			wantErr: false,
		},
		{
			name: "user message with content parts",
			msg: model.Message{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: func() *string { s := "part1"; return &s }()},
					{Type: model.ContentTypeText, Text: func() *string { s := "part2"; return &s }()},
				},
			},
			wantErr: false,
		},
		{
			name: "assistant message with tool calls",
			msg: model.Message{
				Role:    model.RoleAssistant,
				Content: "Using tool",
				ToolCalls: []model.ToolCall{
					{
						ID:   "call1",
						Type: functionToolType,
						Function: model.FunctionDefinitionParam{
							Name:      "fn",
							Arguments: []byte(`{"x":1}`),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "tool message",
			msg: model.Message{
				Role:    model.RoleTool,
				Content: "result",
				ToolID:  "call1",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertMessage(tt.msg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result.Role)
			}
		})
	}
}

// Test_convertTools tests tool conversion.
func Test_convertTools(t *testing.T) {
	toolsMap := map[string]tool.Tool{
		"get_weather": stubTool{
			decl: &tool.Declaration{
				Name:        "get_weather",
				Description: "Get weather info",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"city": {Type: "string", Description: "City name"},
					},
				},
			},
		},
	}

	result := convertTools(toolsMap)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, functionToolType, result[0].Type)
	assert.Equal(t, "get_weather", result[0].Function.Name)
}

// Test_buildToolDescription tests tool description building.
func Test_buildToolDescription(t *testing.T) {
	tests := []struct {
		name string
		decl *tool.Declaration
		want string
	}{
		{
			name: "without output schema",
			decl: &tool.Declaration{
				Name:        "foo",
				Description: "desc",
			},
			want: "desc",
		},
		{
			name: "with output schema",
			decl: &tool.Declaration{
				Name:        "foo",
				Description: "desc",
				OutputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"status": {Type: "string"},
					},
				},
			},
			want: "descOutput schema:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildToolDescription(tt.decl)
			assert.Contains(t, result, tt.want)
		})
	}
}

// Test_buildToolDescription_MarshalError tests marshal error handling.
func Test_buildToolDescription_MarshalError(t *testing.T) {
	logger := &stubLogger{}
	original := agentlog.Default
	agentlog.Default = logger
	defer func() { agentlog.Default = original }()

	decl := &tool.Declaration{
		Name:        "foo",
		Description: "desc",
		OutputSchema: &tool.Schema{
			Type:                 "object",
			AdditionalProperties: func() {},
		},
	}

	desc := buildToolDescription(decl)
	assert.Equal(t, "desc", desc)
	assert.True(t, logger.debugfCalled)
}

// Test_HandleNonStreamingResponse tests non-streaming response.
func Test_HandleNonStreamingResponse(t *testing.T) {
	// Create mock server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/chat") && !strings.HasPrefix(r.URL.Path, "/api/show") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/show" {
			resp := map[string]any{
				"license":    "xxx",
				"modelfile":  "xxx",
				"parameters": "xxx",
				"template":   "xxx",
				"model_info": map[string]any{
					"llama.context_length": 131072,
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp := map[string]any{
			"model":                "llama3.2:latest",
			"created_at":           "2024-01-01T00:00:00Z",
			"message":              map[string]any{"role": "assistant", "content": "Hello!"},
			"done":                 true,
			"total_duration":       1000000000,
			"load_duration":        500000000,
			"prompt_eval_count":    10,
			"prompt_eval_duration": 200000000,
			"eval_count":           5,
			"eval_duration":        300000000,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var calledRequest, calledResponse bool
	m := New("llama3.2:latest",
		WithHost(srv.URL),
		WithChatRequestCallback(func(ctx context.Context, req *api.ChatRequest) {
			calledRequest = true
		}),
		WithChatResponseCallback(func(ctx context.Context, req *api.ChatRequest, resp *api.ChatResponse) {
			calledResponse = true
		}),
	)

	assert.Equal(t, 131072, m.contextWindow)

	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	ch, err := m.GenerateContent(ctx, req)
	require.NoError(t, err)

	var got *model.Response
	select {
	case got = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	assert.NotNil(t, got)
	assert.True(t, got.Done)
	assert.Nil(t, got.Error)
	assert.Equal(t, "Hello!", got.Choices[0].Message.Content)
	assert.NotNil(t, got.Usage)
	assert.Equal(t, 10, got.Usage.PromptTokens)
	assert.Equal(t, 5, got.Usage.CompletionTokens)
	assert.True(t, calledRequest)
	assert.True(t, calledResponse)
}

// Test_HandleStreamingResponse tests streaming response.
func Test_HandleStreamingResponse(t *testing.T) {
	// Create mock server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/chat") && !strings.HasPrefix(r.URL.Path, "/api/show") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/show" {
			resp := map[string]any{
				"license":    "xxx",
				"modelfile":  "xxx",
				"parameters": "xxx",
				"template":   "xxx",
				"model_info": map[string]any{
					"gptoss.context_length": 131072,
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// Send streaming chunks
		chunks := []map[string]any{
			{
				"model":      "llama3.2:latest",
				"created_at": "2024-01-01T00:00:00Z",
				"message":    map[string]any{"role": "assistant", "content": "Hello"},
				"done":       false,
			},
			{
				"model":      "llama3.2:latest",
				"created_at": "2024-01-01T00:00:00Z",
				"message":    map[string]any{"role": "assistant", "content": " World"},
				"done":       false,
			},
			{
				"model":             "llama3.2:latest",
				"created_at":        "2024-01-01T00:00:00Z",
				"message":           map[string]any{"role": "assistant", "content": "!"},
				"done":              true,
				"total_duration":    1000000000,
				"prompt_eval_count": 10,
				"eval_count":        5,
			},
		}

		for _, chunk := range chunks {
			json.NewEncoder(w).Encode(chunk)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	var chunkCalled, streamCompleteCalled bool
	m := New("gpt-oss:20b",
		WithHost(srv.URL),
		WithChatChunkCallback(func(ctx context.Context, req *api.ChatRequest, chunk *api.ChatResponse) {
			chunkCalled = true
		}),
		WithChatStreamCompleteCallback(func(ctx context.Context, req *api.ChatRequest, err error) {
			streamCompleteCalled = true
		}),
	)

	assert.Equal(t, 131072, m.contextWindow)

	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ch, err := m.GenerateContent(ctx, req)
	require.NoError(t, err)

	var partials int
	var final *model.Response
	for resp := range ch {
		if resp.Done {
			final = resp
			time.Sleep(time.Second)
			break
		}
		if resp.IsPartial {
			partials++
		}
	}

	assert.Equal(t, partials, 2)
	assert.NotNil(t, final)
	assert.True(t, final.Done)
	assert.True(t, chunkCalled)
	assert.True(t, streamCompleteCalled)
}

// Test_HandleErrorResponse tests error response handling.
func Test_HandleErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := New("llama3.2:latest", WithHost(srv.URL))

	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
	}

	ch, err := m.GenerateContent(ctx, req)
	require.NoError(t, err)

	var got *model.Response
	select {
	case got = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	assert.NotNil(t, got)
	assert.NotNil(t, got.Error)
	assert.True(t, got.Done)
}

// Test_buildChatRequest tests chat request building.
func Test_buildChatRequest(t *testing.T) {
	m := New("llama3.2:latest")

	temp := 0.7
	topP := 0.9
	maxTokens := 100
	thinking := true

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful"),
			model.NewUserMessage("Hi"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature:     &temp,
			TopP:            &topP,
			MaxTokens:       &maxTokens,
			Stop:            []string{"STOP"},
			Stream:          true,
			ThinkingEnabled: &thinking,
		},
		Tools: map[string]tool.Tool{},
	}

	chatReq, err := m.buildChatRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "llama3.2:latest", chatReq.Model)
	assert.NotNil(t, chatReq.Stream)
	assert.True(t, *chatReq.Stream)
	assert.Equal(t, temp, chatReq.Options["temperature"])
	assert.Equal(t, topP, chatReq.Options["top_p"])
	assert.Equal(t, maxTokens, chatReq.Options["num_predict"])
	assert.Equal(t, []string{"STOP"}, chatReq.Options["stop"])
	assert.NotNil(t, chatReq.Think)
	assert.True(t, chatReq.Think.Bool())
}

// Test_buildChatRequest_EmptyMessages tests error when no messages.
func Test_buildChatRequest_EmptyMessages(t *testing.T) {
	m := New("llama3.2:latest")

	req := &model.Request{
		Messages: []model.Message{},
	}

	chatReq, err := m.buildChatRequest(req)
	assert.Error(t, err)
	assert.Nil(t, chatReq)
}

// testStubCounter is a stub TokenCounter for testing token tailoring.
type testStubCounter struct{}

func (testStubCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
	return 1, nil
}

func (testStubCounter) CountTokensRange(ctx context.Context, messages []model.Message, start, end int) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}
	return end - start, nil
}

// testStubStrategy is a stub TailoringStrategy for testing.
type testStubStrategy struct{}

func (testStubStrategy) TailorMessages(ctx context.Context, messages []model.Message, maxTokens int) ([]model.Message, error) {
	if len(messages) <= 1 {
		return messages, nil
	}
	return append([]model.Message{messages[0]}, messages[2:]...), nil
}

// TestWithTokenTailoring tests token tailoring functionality.
func TestWithTokenTailoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"model":      "llama3.2:latest",
			"message":    map[string]any{"role": "assistant", "content": "OK"},
			"done":       true,
			"eval_count": 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var capturedReq *api.ChatRequest
	m := New("llama3.2:latest",
		WithHost(srv.URL),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *api.ChatRequest) {
			capturedReq = req
		}),
	)

	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("A"),
			model.NewUserMessage("B"),
		},
	}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
	}

	require.NotNil(t, capturedReq)
	// After tailoring, messages should be reduced (1 message after tailoring).
	require.Len(t, capturedReq.Messages, 1)
}

// TestWithEnableTokenTailoring_SimpleMode tests simple token tailoring mode.
func TestWithEnableTokenTailoring_SimpleMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"model":      "llama3.2:latest",
			"message":    map[string]any{"role": "assistant", "content": "OK"},
			"done":       true,
			"eval_count": 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var capturedReq *api.ChatRequest
	m := New("llama3.2:latest",
		WithHost(srv.URL),
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *api.ChatRequest) {
			capturedReq = req
		}),
	)

	messages := []model.Message{model.NewSystemMessage("You are helpful")}
	for i := 0; i < 500; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 100))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
	}

	require.NotNil(t, capturedReq)
	// After tailoring, messages should be reduced.
	require.Less(t, len(capturedReq.Messages), len(messages))
}

// TestWithEnableTokenTailoring_Disabled tests disabled token tailoring.
func TestWithEnableTokenTailoring_Disabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"model":      "llama3.2:latest",
			"message":    map[string]any{"role": "assistant", "content": "OK"},
			"done":       true,
			"eval_count": 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var capturedReq *api.ChatRequest
	m := New("llama3.2:latest",
		WithHost(srv.URL),
		WithEnableTokenTailoring(false),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *api.ChatRequest) {
			capturedReq = req
		}),
	)

	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("A"),
			model.NewUserMessage("B"),
		},
	}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
	}

	require.NotNil(t, capturedReq)
	require.Len(t, capturedReq.Messages, 2)
}

// Test_convertChatResponse tests chat response conversion.
func Test_convertChatResponse(t *testing.T) {
	resp := api.ChatResponse{
		Model:     "llama3.2:latest",
		CreatedAt: time.Now(),
		Message: api.Message{
			Role:    "assistant",
			Content: "Hello",
		},
		Done: true,
		Metrics: api.Metrics{
			PromptEvalCount: 10,
			EvalCount:       5,
		},
	}

	result, err := convertChatResponse(resp)
	require.NoError(t, err)
	assert.True(t, result.Done)
	assert.Equal(t, "Hello", result.Choices[0].Message.Content)
	assert.Equal(t, 10, result.Usage.PromptTokens)
	assert.Equal(t, 5, result.Usage.CompletionTokens)
	assert.Equal(t, 15, result.Usage.TotalTokens)
}

// Test_convertChatResponse_WithToolCalls tests response with tool calls.
func Test_convertChatResponse_WithToolCalls(t *testing.T) {
	resp := api.ChatResponse{
		Model:     "llama3.2:latest",
		CreatedAt: time.Now(),
		Message: api.Message{
			Role:    "assistant",
			Content: "Using tool",
			ToolCalls: []api.ToolCall{
				{
					ID: "call1",
					Function: api.ToolCallFunction{
						Name:      "get_weather",
						Arguments: map[string]any{"city": "Beijing"},
					},
				},
			},
		},
		Done: true,
		Metrics: api.Metrics{
			PromptEvalCount: 10,
			EvalCount:       5,
		},
	}

	result, err := convertChatResponse(resp)
	require.NoError(t, err)
	assert.True(t, result.Done)
	assert.Equal(t, 1, len(result.Choices[0].Message.ToolCalls))
	assert.Equal(t, "call1", result.Choices[0].Message.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", result.Choices[0].Message.ToolCalls[0].Function.Name)
}

// Test_imageToURLOrBase64 tests image conversion.
func Test_imageToURLOrBase64(t *testing.T) {
	tests := []struct {
		name  string
		image *model.Image
		want  string
	}{
		{
			name: "with URL",
			image: &model.Image{
				URL: "http://example.com/image.jpg",
			},
			want: "",
		},
		{
			name: "with data",
			image: &model.Image{
				Format: "png",
				Data:   []byte("test"),
			},
			want: "dGVzdA==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := imageToURLOrBase64(tt.image)
			assert.Contains(t, result, tt.want)
		})
	}
}

// Test_argsToObject tests arguments conversion.
func Test_argsToObject(t *testing.T) {
	tests := []struct {
		name    string
		args    []byte
		wantErr bool
	}{
		{
			name:    "valid JSON",
			args:    []byte(`{"x":1,"y":"test"}`),
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			args:    []byte(`not-json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := argsToObject(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// Test_WithChannelBufferSize tests channel buffer size option.
func Test_WithChannelBufferSize(t *testing.T) {
	tests := []struct {
		name string
		size int
		want int
	}{
		{
			name: "positive size",
			size: 512,
			want: 512,
		},
		{
			name: "zero size",
			size: 0,
			want: defaultChannelBufferSize,
		},
		{
			name: "negative size",
			size: -1,
			want: defaultChannelBufferSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("test-model", WithChannelBufferSize(tt.size))
			assert.Equal(t, tt.want, m.channelBufferSize)
		})
	}
}

// Test_WithKeepAlive tests keep alive option.
func Test_WithKeepAlive(t *testing.T) {
	duration := 30 * time.Second
	m := New("test-model", WithKeepAlive(duration))
	assert.NotNil(t, m.keepAlive)
	assert.Equal(t, duration, m.keepAlive.Duration)
}
