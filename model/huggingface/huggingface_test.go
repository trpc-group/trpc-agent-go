//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const ApiKey = "*****"

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		opts      []Option
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "empty model name",
			modelName: "",
			opts:      []Option{WithAPIKey(ApiKey)},
			wantErr:   true,
			errMsg:    "model name cannot be empty",
		},
		{
			name:      "missing API key",
			modelName: "test-model",
			opts:      []Option{},
			wantErr:   true,
			errMsg:    "API key is required",
		},
		{
			name:      "valid configuration",
			modelName: "meta-llama/Llama-2-7b-chat-hf",
			opts:      []Option{WithAPIKey(ApiKey)},
			wantErr:   false,
		},
		{
			name:      "with custom base URL",
			modelName: "test-model",
			opts: []Option{
				WithAPIKey(ApiKey),
				WithBaseURL("https://custom.api.com"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.modelName, tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, m)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, m)
				assert.Equal(t, tt.modelName, m.name)
			}
		})
	}
}

func TestModel_Info(t *testing.T) {
	m, err := New("test-model", WithAPIKey(ApiKey))
	require.NoError(t, err)

	info := m.Info()
	assert.Equal(t, "test-model", info.Name)
}

func TestWithOptions(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		validate func(t *testing.T, m *Model)
	}{
		{
			name: "WithChannelBufferSize",
			opts: []Option{
				WithAPIKey("test-key"),
				WithChannelBufferSize(512),
			},
			validate: func(t *testing.T, m *Model) {
				assert.Equal(t, 512, m.channelBufferSize)
			},
		},
		{
			name: "WithExtraHeaders",
			opts: []Option{
				WithAPIKey("test-key"),
				WithExtraHeaders(map[string]string{
					"X-Custom-Header": "custom-value",
				}),
			},
			validate: func(t *testing.T, m *Model) {
				assert.Equal(t, "custom-value", m.extraHeaders["X-Custom-Header"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New("test-model", tt.opts...)
			require.NoError(t, err)
			tt.validate(t, m)
		})
	}
}

func TestModel_GenerateContent_NonStreaming(t *testing.T) {
	tests := []struct {
		name           string
		request        *model.Request
		mockResponse   string
		expectedError  bool
		validateResult func(t *testing.T, responses []*model.Response)
	}{
		{
			name: "successful_non_streaming_response",
			request: &model.Request{
				Messages: []model.Message{
					{Role: model.RoleUser, Content: "Hello, how are you?"},
				},
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			},
			mockResponse: `{
				"id": "test-id-123",
				"object": "chat.completion",
				"created": 1699200000,
				"model": "mistralai/Mistral-7B-Instruct-v0.2",
				"choices": [
					{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "I'm doing well, thank you for asking!"
						},
						"finish_reason": "stop"
					}
				],
				"usage": {
					"prompt_tokens": 10,
					"completion_tokens": 15,
					"total_tokens": 25
				}
			}`,
			expectedError: false,
			validateResult: func(t *testing.T, responses []*model.Response) {
				require.Len(t, responses, 1)
				resp := responses[0]
				assert.Nil(t, resp.Error)
				require.Len(t, resp.Choices, 1)
				assert.Equal(t, "I'm doing well, thank you for asking!", resp.Choices[0].Message.Content)
				assert.Equal(t, model.RoleAssistant, resp.Choices[0].Message.Role)
				assert.NotNil(t, resp.Usage)
				assert.Equal(t, 10, resp.Usage.PromptTokens)
				assert.Equal(t, 15, resp.Usage.CompletionTokens)
				assert.Equal(t, 25, resp.Usage.TotalTokens)
			},
		},
		{
			name:          "nil_request",
			request:       nil,
			expectedError: true,
		},
		{
			name: "empty_messages",
			request: &model.Request{
				Messages: []model.Message{},
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			},
			mockResponse:  `{"error": {"message": "messages cannot be empty", "type": "invalid_request_error"}}`,
			expectedError: false, // Error will be in response channel
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}

				// Verify request headers.
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

				w.Header().Set("Content-Type", "application/json")

				// Return error status for empty messages test.
				if tt.name == "empty_messages" {
					w.WriteHeader(http.StatusBadRequest)
				}

				fmt.Fprint(w, tt.mockResponse)
			}))
			defer server.Close()

			// Create model with mock server.
			m, err := New(
				"mistralai/Mistral-7B-Instruct-v0.2",
				WithAPIKey("test-api-key"),
				WithBaseURL(server.URL),
			)
			require.NoError(t, err)

			// Execute GenerateContent.
			ctx := context.Background()
			responseChan, err := m.GenerateContent(ctx, tt.request)

			if tt.expectedError {
				if err == nil {
					// Error might come through channel.
					resp := <-responseChan
					assert.NotNil(t, resp.Error)
				} else {
					assert.Error(t, err)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, responseChan)

			// Collect all responses.
			var responses []*model.Response
			for response := range responseChan {
				responses = append(responses, response)
			}

			// Validate results.
			if tt.validateResult != nil {
				tt.validateResult(t, responses)
			}
		})
	}
}

func TestModel_GenerateContent_Streaming(t *testing.T) {
	tests := []struct {
		name           string
		request        *model.Request
		mockChunks     []string
		expectedError  bool
		validateResult func(t *testing.T, responses []*model.Response)
	}{
		{
			name: "successful_streaming_response",
			request: &model.Request{
				Messages: []model.Message{
					{Role: model.RoleUser, Content: "Tell me a story"},
				},
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
			mockChunks: []string{
				`data: {"id":"test-1","object":"chat.completion.chunk","created":1699200000,"model":"mistralai/Mistral-7B-Instruct-v0.2","choices":[{"index":0,"delta":{"role":"assistant","content":"Once"},"finish_reason":null}]}`,
				`data: {"id":"test-1","object":"chat.completion.chunk","created":1699200000,"model":"mistralai/Mistral-7B-Instruct-v0.2","choices":[{"index":0,"delta":{"content":" upon"},"finish_reason":null}]}`,
				`data: {"id":"test-1","object":"chat.completion.chunk","created":1699200000,"model":"mistralai/Mistral-7B-Instruct-v0.2","choices":[{"index":0,"delta":{"content":" a time"},"finish_reason":null}]}`,
				`data: {"id":"test-1","object":"chat.completion.chunk","created":1699200000,"model":"mistralai/Mistral-7B-Instruct-v0.2","choices":[{"index":0,"delta":{"content":"..."},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			},
			expectedError: false,
			validateResult: func(t *testing.T, responses []*model.Response) {
				assert.GreaterOrEqual(t, len(responses), 4, "Expected at least 4 streaming chunks")

				// Verify first chunk has role (in Delta field for streaming).
				assert.Equal(t, model.RoleAssistant, responses[0].Choices[0].Delta.Role)
				assert.Equal(t, "Once", responses[0].Choices[0].Delta.Content)

				// Verify intermediate chunks.
				assert.Equal(t, " upon", responses[1].Choices[0].Delta.Content)
				assert.Equal(t, " a time", responses[2].Choices[0].Delta.Content)

				// Verify last chunk has finish reason.
				lastResp := responses[len(responses)-1]
				assert.Equal(t, "...", lastResp.Choices[0].Delta.Content)
				require.NotNil(t, lastResp.Choices[0].FinishReason)
				assert.Equal(t, "stop", *lastResp.Choices[0].FinishReason)
			},
		},
		{
			name: "streaming_with_empty_chunks",
			request: &model.Request{
				Messages: []model.Message{
					{Role: model.RoleUser, Content: "Test"},
				},
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
			mockChunks: []string{
				`data: {"id":"test-2","object":"chat.completion.chunk","created":1699200000,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
				`data: {"id":"test-2","object":"chat.completion.chunk","created":1699200000,"model":"test-model","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			},
			expectedError: false,
			validateResult: func(t *testing.T, responses []*model.Response) {
				assert.GreaterOrEqual(t, len(responses), 1)
				// Find non-empty content response (use Delta for streaming).
				var foundContent bool
				for _, resp := range responses {
					if len(resp.Choices) > 0 && resp.Choices[0].Delta.Content == "Hello" {
						foundContent = true
						break
					}
				}
				assert.True(t, foundContent, "Expected to find 'Hello' in responses")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server for streaming.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}

				// Verify streaming request.
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")

				flusher, ok := w.(http.Flusher)
				require.True(t, ok, "Expected http.ResponseWriter to be an http.Flusher")

				// Send chunks.
				for _, chunk := range tt.mockChunks {
					fmt.Fprintf(w, "%s\n\n", chunk)
					flusher.Flush()
					time.Sleep(5 * time.Millisecond)
				}
			}))
			defer server.Close()

			// Create model with mock server.
			m, err := New(
				"mistralai/Mistral-7B-Instruct-v0.2",
				WithAPIKey("test-api-key"),
				WithBaseURL(server.URL),
			)
			require.NoError(t, err)

			// Execute GenerateContent.
			ctx := context.Background()
			responseChan, err := m.GenerateContent(ctx, tt.request)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, responseChan)

			// Collect all responses.
			var responses []*model.Response
			for response := range responseChan {
				responses = append(responses, response)
				if response.Error != nil {
					t.Logf("Response error: %v", response.Error)
				}
			}

			// Validate results.
			if tt.validateResult != nil {
				tt.validateResult(t, responses)
			}
		})
	}
}

func TestModel_GenerateContent_WithCallbacks(t *testing.T) {
	var requestCallbackCalled bool
	var chunkCallbackCalled bool
	var streamCompleteCallbackCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, `data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"test","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithChatRequestCallback(func(ctx context.Context, req *ChatCompletionRequest) {
			requestCallbackCalled = true
			assert.NotNil(t, req)
		}),
		WithChatChunkCallback(func(ctx context.Context, req *ChatCompletionRequest, chunk *ChatCompletionChunk) {
			chunkCallbackCalled = true
			assert.NotNil(t, chunk)
		}),
		WithChatStreamCompleteCallback(func(ctx context.Context, req *ChatCompletionRequest, streamErr error) {
			streamCompleteCallbackCalled = true
			assert.Nil(t, streamErr)
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Test"}},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume all responses.
	for range responseChan {
	}

	// Give callbacks time to execute.
	time.Sleep(50 * time.Millisecond)

	// Verify callbacks were called.
	assert.True(t, requestCallbackCalled, "Request callback should be called")
	assert.True(t, chunkCallbackCalled, "Chunk callback should be called")
	assert.True(t, streamCompleteCallbackCalled, "Stream complete callback should be called")
}

func TestModel_TokenTailoring(t *testing.T) {
	tests := []struct {
		name            string
		opts            []Option
		messages        []model.Message
		expectTailoring bool
		expectMaxTokens bool
		validateRequest func(t *testing.T, req *model.Request)
	}{
		{
			name: "token_tailoring_enabled_with_auto_calculation",
			opts: []Option{
				WithAPIKey("test-key"),
				WithEnableTokenTailoring(true),
			},
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
				{Role: model.RoleAssistant, Content: "Hi there!"},
				{Role: model.RoleUser, Content: "How are you?"},
			},
			expectTailoring: true,
			expectMaxTokens: true,
			validateRequest: func(t *testing.T, req *model.Request) {
				// Messages should remain the same for small conversations.
				assert.Len(t, req.Messages, 3)
				// MaxTokens should be set automatically.
				assert.NotNil(t, req.GenerationConfig.MaxTokens)
				assert.Greater(t, *req.GenerationConfig.MaxTokens, 0)
			},
		},
		{
			name: "token_tailoring_with_max_input_tokens",
			opts: []Option{
				WithAPIKey("test-key"),
				WithMaxInputTokens(100),
			},
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
				{Role: model.RoleAssistant, Content: "Hi there!"},
				{Role: model.RoleUser, Content: "How are you?"},
			},
			expectTailoring: true,
			expectMaxTokens: true,
			validateRequest: func(t *testing.T, req *model.Request) {
				// Messages should remain the same for small conversations.
				assert.LessOrEqual(t, len(req.Messages), 3)
				// MaxTokens should be set.
				assert.NotNil(t, req.GenerationConfig.MaxTokens)
			},
		},
		{
			name: "token_tailoring_disabled",
			opts: []Option{
				WithAPIKey("test-key"),
			},
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
				{Role: model.RoleAssistant, Content: "Hi there!"},
			},
			expectTailoring: false,
			expectMaxTokens: false,
			validateRequest: func(t *testing.T, req *model.Request) {
				// Messages should remain unchanged.
				assert.Len(t, req.Messages, 2)
				// MaxTokens should not be set.
				assert.Nil(t, req.GenerationConfig.MaxTokens)
			},
		},
		{
			name: "token_tailoring_with_custom_config",
			opts: []Option{
				WithAPIKey("test-key"),
				WithEnableTokenTailoring(true),
				WithTokenTailoringConfig(&model.TokenTailoringConfig{
					ProtocolOverheadTokens: 256,
					ReserveOutputTokens:    1024,
					SafetyMarginRatio:      0.05,
					InputTokensFloor:       512,
					OutputTokensFloor:      256,
					MaxInputTokensRatio:    0.9,
				}),
			},
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Test message"},
			},
			expectTailoring: true,
			expectMaxTokens: true,
			validateRequest: func(t *testing.T, req *model.Request) {
				assert.NotEmpty(t, req.Messages)
				assert.NotNil(t, req.GenerationConfig.MaxTokens)
			},
		},
		{
			name: "token_tailoring_respects_user_max_tokens",
			opts: []Option{
				WithAPIKey("test-key"),
				WithEnableTokenTailoring(true),
			},
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			expectTailoring: true,
			expectMaxTokens: false, // User already set it
			validateRequest: func(t *testing.T, req *model.Request) {
				// User's MaxTokens should be preserved.
				assert.NotNil(t, req.GenerationConfig.MaxTokens)
				assert.Equal(t, 500, *req.GenerationConfig.MaxTokens)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{
					"id": "test-id",
					"object": "chat.completion",
					"created": 1699200000,
					"model": "test-model",
					"choices": [{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "Test response"
						},
						"finish_reason": "stop"
					}],
					"usage": {
						"prompt_tokens": 10,
						"completion_tokens": 5,
						"total_tokens": 15
					}
				}`)
			}))
			defer server.Close()

			// Add base URL to options.
			opts := append(tt.opts, WithBaseURL(server.URL))

			// Create model.
			m, err := New("meta-llama/Llama-2-7b-chat-hf", opts...)
			require.NoError(t, err)

			// Create request.
			request := &model.Request{
				Messages:         tt.messages,
				GenerationConfig: model.GenerationConfig{},
			}

			// Set user's MaxTokens for the specific test case.
			if tt.name == "token_tailoring_respects_user_max_tokens" {
				maxTokens := 500
				request.GenerationConfig.MaxTokens = &maxTokens
			}

			// Execute GenerateContent.
			ctx := context.Background()
			responseChan, err := m.GenerateContent(ctx, request)
			require.NoError(t, err)

			// Consume responses.
			for range responseChan {
			}

			// Validate request modifications.
			if tt.validateRequest != nil {
				tt.validateRequest(t, request)
			}
		})
	}
}

func TestModel_TokenTailoring_Integration(t *testing.T) {
	// create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// 返回模拟的成功响应
		mockResponse := `{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "这是一个测试响应，用于验证 token tailoring 功能正常工作。"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 100,
				"completion_tokens": 20,
				"total_tokens": 120
			}
		}`
		fmt.Fprint(w, mockResponse)
	}))
	defer server.Close()

	// Create a large conversation that exceeds token limit.
	var messages []model.Message
	for i := 0; i < 100; i++ {
		messages = append(messages,
			model.Message{
				Role:    model.RoleUser,
				Content: fmt.Sprintf("这是用户消息：number %d 和一些内容", i),
			},
			model.Message{
				Role:    model.RoleAssistant,
				Content: fmt.Sprintf("这是助手回复：number %d 和一些内容", i),
			},
		)
	}

	m, err := New(
		"test-model",
		WithAPIKey("test-api-key"),
		WithBaseURL(server.URL),
		WithMaxInputTokens(500), // Set a low limit to trigger tailoring
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages:         messages,
		GenerationConfig: model.GenerationConfig{},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume responses.
	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	log.Infof("request.Messages: %d", len(request.Messages))
	log.Infof("request.GenerationConfig MaxTokens: %d", *request.GenerationConfig.MaxTokens)
	// Verify that messages were tailored (reduced).
	assert.Less(t, len(request.Messages), 200, "Messages should be tailored to fit token limit")
	assert.Greater(t, len(request.Messages), 0, "Should have at least some messages")
	// Verify that MaxTokens was set.
	assert.NotNil(t, request.GenerationConfig.MaxTokens)
	assert.Greater(t, *request.GenerationConfig.MaxTokens, 0)

	// Verify response was received.
	require.NotEmpty(t, responses, "Should receive at least one response")
	assert.Nil(t, responses[0].Error, "Response should not have error")
	require.NotEmpty(t, responses[0].Choices, "Response should have choices")
	log.Info(responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_ImageURL(t *testing.T) {
	// Test sending a message with an image URL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "I can see a beautiful image."
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 100,
				"completion_tokens": 20,
				"total_tokens": 120
			}
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-api-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	// Create a message with image URL.
	imageURL := "data:image/jpeg;base64,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"
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("这个图片是狗么?"),
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    imageURL,
							Detail: "high",
						},
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume responses.
	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Verify response.
	require.NotEmpty(t, responses, "Should receive at least one response")
	assert.Nil(t, responses[0].Error, "Response should not have error")
	require.NotEmpty(t, responses[0].Choices, "Response should have choices")
	assert.Contains(t, responses[0].Choices[0].Message.Content, "image")
	log.Infof("%+v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_Base64Image(t *testing.T) {
	// Test sending a message with a base64-encoded image.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "This is a 1x1 red pixel image."
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-api-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	// Create a message with base64-encoded image (1x1 red pixel PNG).
	base64Image := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("描述这张图片"),
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							Data:   []byte(base64Image),
							Format: "png",
							Detail: "auto",
						},
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume responses.
	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Verify response.
	require.NotEmpty(t, responses, "Should receive at least one response")
	assert.Nil(t, responses[0].Error, "Response should not have error")
	require.NotEmpty(t, responses[0].Choices, "Response should have choices")
	log.Infof("Responses: %v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_MultipleImages(t *testing.T) {
	// Test sending a message with multiple images.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "I can see two different images."
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-api-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	// Create a message with multiple images.
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("比较这两张图片"),
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "data:image/jpeg;base64,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",
							Detail: "high",
						},
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "data:image/jpeg;base64,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",
							Detail: "high",
						},
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume responses.
	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Verify response.
	require.NotEmpty(t, responses, "Should receive at least one response")
	assert.Nil(t, responses[0].Error, "Response should not have error")
	require.NotEmpty(t, responses[0].Choices, "Response should have choices")
	log.Infof("responses: %v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_StreamingWithImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send streaming chunks
		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"I can"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"content":" see"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"content":" the image"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-api-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	// Create a streaming request with image.
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("这张图片里面有什么?"),
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD/2wCEAAkGBwgHBgkIBwgKCgkLDRYPDQwMDRsUFRAWIB0iIiAdHx8kKDQsJCYxJx8fLT0tMTU3Ojo6Iys/RD84QzQ5OjcBCgoKDQwNGg8PGjclHyU3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3N//AABEIAIAAwAMBIgACEQEDEQH/xAAbAAABBQEBAAAAAAAAAAAAAAAGAQIDBAUAB//EADsQAAIBAwMBBQUGBQMFAQAAAAECAwAEEQUSITEGE0FRYSIycYGRFBUjobHRB0LB4fAzUvEkYnKCsiX/xAAYAQADAQEAAAAAAAAAAAAAAAAAAQIDBP/EAB0RAQEBAQEAAwEBAAAAAAAAAAABAhExAxIhUUH/2gAMAwEAAhEDEQA/ACqkrq6kHV1dXUgr39wlrayzyttRFLMfQV54O0+sXd9JJpoAQ+6rcjHwoq7bPIdFa3iPt3EiRZ9Cef0pul6JHp0MaMqs5XkgfnUarTOendm9U1a6lEGp2aLu4WVTgfOiG4ikgkKSoVYdQajjuUhj2yINgA5Oenl0zWlBdWd9bpDLcIw47t+jIfI55IqZuz1Wvj/2MzPnSGpJ4XgmaKQYZTg1Gela9YkNNNKaQ0A00004000gSmmn4rK13WIdJgyfbmY4SMdSaB6lv7+2sITJdShB4etDN52vmLk2FmWXPDPxn5Vk5n1S8725/Ek6gZ9lB+9bNvYxd3tk94+IqLptMO0LtS19fLa3MRSZgSADkH+9EpPmaBLK2EXbCyRF4BcsVHGNpo4b0pyo3OUldSGupocajenmo3NICWkpaStCLXV1JSNh6zifWtHtCSAZzKQBkYAwPzNEV5bQSOFKlmPPFD+qxS/f2ktGCF9tWfwVfZP9KKRHFHbM8jMsajIAPtHP6frUVrnxj3mnS3MZRJCrge6Bu4/zzoXlW90a8Ms6iSLHIyCSPkTWlrPaDY7RQIFTONoHH0/ehfUtYaVMSYjJPG0YFRWk69Dm13T7vTbaVp8y7hHv6ZGP5qa7KuMnivIhdXMU/DMRnOCeDx41s2XaC5fdZR54JaIMemedufQ5/Kql5EazLej+SeJX2lhnGaR5Y1QOWG09Dng0L2F5FdxR75SWUDOfENnGaJ9Y7NreaLGtsdkijMZXox8RSvycKfH1H9qh4/EXn1p6yRsdobnGa8/s5Ze6jEjHeQDzwVPQj/POptSu7qylG2Y5wMAnwp/YvoLtW1KGwtmd25I4XqSfIV548s+pai7Ow7x2x190eVOOrSy28veHLtwCeT51f7OWx9kx5M0vPCjIXxPNFvVZnGxpekRxwgsybgOgB/Oppd0Y2ggEnHpW1byx28BKlgEGCzkAfIY5+dVIZEnlNzc25SH+WQchvUr0qWkYcFpMO1sLDb7MD5APhiiCQAeIqe30yMJPqNq4liZQCvXZ548s8VVbqcZx61UYbvaQ0ldSVSCU1qcajakBPSVxpK0ItdXV1BoLqUpNarGgMkkwjUk4AJ+PFWzptxPFPDIzJKOY5AePn8aztYMX2F++GVGD9DS9lNYfUIJmunmmaNhGsjEYTyz4k+FZavK2x4He0ttBYo7z5Scrkqo+VA88onJwNx8Vz4UV9q9Q+36tCsqnAVldR0Zs4/Shl544fwO6xG38w6k+WaS+orIqJI1keMq2eC3Hwrh+FIylSjhT18eoGDVZWxKI9qkk8BvE1es7OWcJE6MysvKHqP8AuX+ooTWnpMve6hC5Uj7VCWJU5HeIfa/+Qf8A3Fe16Nj7phjYBpYwrMPI8nivMey3Z+a2m23I9qO5WeNscEY2n6gDj0r0PSZHyzRnlGMZH/jx9aw+StMT8D992YWHVLtsAxNJ30fmVK4YfIgGhjtxpEkSiNU8A8bY8P8ADXpnaC47izhvACp3YyfL/DVLUYbbXLOBNoaRY8EA8g46flTzuwrl4dJpzpNErOUVzmVv9qgZY/IA1v6Hq32S4McqgSPy46GMfyxjHkMZ9ePCiO50OIRXM0OWbCxqH55yD9M7c+lDK6LJbX+yUMV35Jxgu3x8q0mpxPgsitl1FTNPKRCmOC3U1n69qQ7nDZJThI06GnrC0kYhSZY08NvTPxoc1iQxTABdpRsJxy3mTT6qCrs9cy22lwzQON1xmSXHRieMEegAFWpCkoLRDD+K1BZWMtjpttC+OFJHPgSTXetXHPr0tcaTNdmqS6onp5NRuaQFFdXGuqycKUnApBTZV3Dg0Uw32ruEjtz3l2yLj/TVc7hUH8PdRjXUri1kPszR5RiCQR1Gatavpr3l3FGFYluBVkadF2eikeYR986jadoLVhptkLduIlOqm6gMYcn8UD3SR4geHFYOHJPfQiRXznZzn1qxqssjTM5ZXByeDkCqUTI6f6mD4U4drQ0+yhucoSrjPBY4I+dHWiaPbxxxtespwPYkTr50G6Y9tZJHLLJM6nqMZz8AK37DtX3lwUtNJmnjT38ZBH0JqNDL0NoE+6zMAuOGWRR0OMfpxVjS4ylgWA/1ZS+5eM5xmsDTu1el3kLWjxzWUpxuhn43c/kfjxRQCfs8Rh24TqPMVjY1l5FTX4e+7KT98MEqNvnmg7QL6WHX41lAOY8EevTJ+lelanB9o0iSNF95RwPiKErzS3+/7NoNqF1kRmXrtwTk+oOa11JIyzrtaV5ZRPM8BBXd7fx460H6yrXEwTuipU7SdvB/ejjV72z0ySPvy7zBMRwRDc7jpn4UKv2jup9ShiGgd1ESQHbliPPOODUZaeq2m2iRRtJLHlQMAdOP1od1iy+8tTWzsYG3ucFj0UeJr0s6XC8bSm1IZxnazEmptKt7NlkSC2jhkK7d23kfOrzU3wI6hOiqsUechQCazsnHmRyas6nbyWt3JFMrAg8Z8RVMHa4ZfgRW8c99PzSU3NdnNMnE1FIakJqGQ0AWmkrqTNUktL4UzNLmg0VzefYFMwHu+OM4oG7T6+9/OS+Y0A9k7hmjXUlD2Uqt4qa8l1MOLkxsOhzk1lr1pnxNbzWb/iXZZ4yxAQEc1p21xZ3Uoitoy3gVIwo+VD2xdvujJHB64p0MrQyBoyqHzJwKOmP4BJChYW8YO1hGdg67Tj481Pc3J03sjE+lIO8FtujAHO7HJ+OawdG17KhJmSXBxhgcHPkaJLHu0Rvs87RozZaCVN8anrlfEZ+NTuW+HKbqMS6h2Uja93C7FqGaRxgrLt6geFRaD20uvuVNOQATxRFO9Y5Px+NJqcV1NAwnvEC/yJFHtx8ck0L3UUWlCF0AkZyW9CfSjOf6Lp7P/DjVH1DRhDM5aSHA55wuMAZ+RremtP8A9CCfoEDYx5kf80Kfwutlt+z7z78m4PPpgHn6k0YhvtFuV3FHK9euD51d/Yl5z20a5WO/ubBWM7OrAxH2nRSMqD4ZAI9M0zUtZW30uO9VDFL3qiBGOTuPGM/AmsiRtRt7otd3bRFWZLhVGRvB5ODx+XlUEwilkW7kE17JGp7p5GBWM+igAflWUxfGn2kvY9BfVEa3t5/aVymWQL7LD+hxTbTUlu4xcxJtdWwRnj5ivN5dSdJv+pkVmPREX961dCv5E02aTdn8TGCelFzzwfb8SazePeX7ySYz6DFUq533uWx1pM1swOzSZpM0maYKTUL9akNRMaQFuabmkzSZrQjs0maZmlzSCLUD/wBK/I6V5XrbYvCf5SeteqXOGhZRjOPGvPO0enTO7SMM+gFZb9aZ8ZMqYiRw4wei1T2hpMHhj41ZSCcLskZI0boZGwT8AMn8qf3dmhxJLNP6IoQfUk5+goMyO3kRhubjOB15rb06SS3wVbaQeQKxxNZRvkWc+DwQbnI+PAq5aXdut3DHBYXEkkowu24zj1IwePM0yb76rLLHt3bQeM+melVdYVJbOKTYGdefQfKpJIPwwzRuh2+1lTx9QKLuw3Z+21YPcXqmW3j47vHDH4/tQFD+HWuJZpcd/Osdu0mI1xyzYHPw5FepxXcbxbllUhl3BhXnPbf+F9pHB94dmxJA0TBpLcOzqV8cZyRxWto7zal3dtEGS3jwrMB0UDpmo3r6un4vjzvFtp/adDLKZpYFXvBuV1OVY9OmOuAPGhGW3ZvxVOUPVV4Ar0ntPHFF2ekRVbamApVcn/PWvPJQ/wB2yJBNBG5OEWV1xn/2B/Wqzeuez9Yl0bNJy2/8QnhdwI/StS2Ajs1HTed1VrG27QvdJ3rMyZyGXbtYeQAHNbl9NPbBVuC48+qn6Hj8qfBfGYCPOlqRmDj2WRl8yoB/Koyhx7IPHUGqZkzSE0hNJmgFJqJzhTTmPFRyHC/GgCrdTd1R767NWSTdXbqZSE0gjnMjYEQ6+dT/AHQ0sQaTDv4YHSsDVtRlgm/AfkemaItC1l7hEikQK5A9kDJ6eNZX9q4H9S7KyvvMaFmbksx6+lZa9mZuRLEIk8C2Ofl+5Ar1yOGEe+wZx1B6Co7i0ifJIV+PEcU+Drw67srPTLzf9huL2Q9O8UhM+gxgfMPW9YDV7iJ8rHYQAcrAoXAH+4/06j0yK9Gk0mBTvKKSPHy/z/PKs+5tieSNsajKIqjgDxI8fQftydMF3VqcgwySSIBlmk90HyweSenl1FFPYXVjZb7aeVgh9oewBxj9ahMkLEd6gTqeRnAzwPXx+eKu6dLpqZ70JvPUdSBR0cFE2vxrbM0cm8gcYXrQtoHbO2gkuYXV8h2YhV9etbh1C0e1lLRrHEFwVwM4/ehi1Gm6c8i7Vbco9oDmhUvJwus6/dayxhiDx26+0QvQ49arw28U9sIVVZYpPeZVyW8gRn2h8CD8ariIGYvbsUI5AQdPlW1o9ltly8eI35CjwJ6j4ZoJHpeki0UNaeyre6Q3st8+PoQKXVdQuLVlilQOg/kcfp4UWkJDEeBu8f8Au/vQdrrpJKWRmaIn3G6pTibVB302655tnPiBx9KrS2ssS97GRJGDjehyP7VVmj2HKnK/pTYZpYH3xOVPkOh+NNKQnIyKbmpWdJSW27N3XHSq/unGaAcaimOWAFPZsCmWsZmucAZA5oA9m0Bx/puRVOTSbqPoA1F26lyCORmrQCGtrlPeiao23KCWUjA8aOGjjI5UVgdpmgt7NyMByMLSvhx53qExnuynfrEN3+zJ+lasWo2Wh2wZTczXcw2oka7mGBz04B6fWsUW1tJN3kxd+cnPkK2NKtY7pkco0cYUt3pHiSayjVtaDcavejvbu2NlBuwoeQFiP/EUXW0qoAoyzcZPl/esjT1jyJGQiNOFUnlj6nyrZjCMQF4bqQB0qkp3hV8Ajw5xVee1D8Y4PJ9f+KvRh4xjZknxpxyPeXBpWDrBn0eGQ8pzVGTstFuWRAeCMgGjBYlcAeJ6U77MR0OceFLg6C37OysqhpGxuycHGas23ZiBJSXXeMYOepooMAJ5+lKIgPGmfWTHosAOdox4VcFvFAN3dsceIq7t4z0qvcSOqHjjzxTHWNrk9vLbNtJDgcFT0NA9zcFnL5zg8/Ci3WnLQvkKScY2jB+dB/2K5e4bbGcNThVFcMjAOh9k9RVRiB0q9HpN2WZNoC+tSroNw3vPimTOibnGetRu+GPNbkegKp9uQmpvui1QZI3H1pANqklw22NSR51q29utpDyPbNX2WKBcRoF+VZ1zNk9amh//2Q==",
							Detail: "auto",
						},
					},
				},
			},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Collect streaming responses.
	var responses []*model.Response
	var fullContent strings.Builder
	for resp := range responseChan {
		responses = append(responses, resp)
		if len(resp.Choices) > 0 && resp.Choices[0].Delta.Content != "" {
			fullContent.WriteString(resp.Choices[0].Delta.Content)
		}
	}

	// Verify streaming responses.
	assert.NotEmpty(t, responses)
	assert.Equal(t, "I can see the image", fullContent.String())
}

func TestConvertContentPart_Image(t *testing.T) {
	// Test converting image content part with URL.
	t.Run("image_with_url", func(t *testing.T) {
		part := model.ContentPart{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				URL:    "https://example.com/image.jpg",
				Detail: "high",
			},
		}

		hfPart, err := convertContentPart(part)
		require.NoError(t, err)
		assert.Equal(t, "image_url", hfPart.Type)
		assert.NotNil(t, hfPart.ImageURL)
		assert.Equal(t, "https://example.com/image.jpg", hfPart.ImageURL.URL)
		assert.Equal(t, "high", hfPart.ImageURL.Detail)
	})

	// Test converting image content part with base64 data.
	t.Run("image_with_base64", func(t *testing.T) {
		base64Data := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
		part := model.ContentPart{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				Data:   []byte(base64Data),
				Format: "png",
				Detail: "auto",
			},
		}

		hfPart, err := convertContentPart(part)
		require.NoError(t, err)
		assert.Equal(t, "image_url", hfPart.Type)
		assert.NotNil(t, hfPart.ImageURL)
		assert.Contains(t, hfPart.ImageURL.URL, "data:image/png;base64,")
		assert.Contains(t, hfPart.ImageURL.URL, base64Data)
		assert.Equal(t, "auto", hfPart.ImageURL.Detail)
	})

	// Test error case: nil image.
	t.Run("nil_image", func(t *testing.T) {
		part := model.ContentPart{
			Type:  model.ContentTypeImage,
			Image: nil,
		}

		_, err := convertContentPart(part)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "image is nil")
	})
}

func TestConvertMessage_Multimodal(t *testing.T) {
	// Test converting a message with mixed content (text + image).
	t.Run("text_and_image", func(t *testing.T) {
		msg := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: stringPtr("Describe this image:"),
				},
				{
					Type: model.ContentTypeImage,
					Image: &model.Image{
						URL:    "https://example.com/test.jpg",
						Detail: "high",
					},
				},
			},
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)
		assert.Equal(t, "user", hfMsg.Role)

		// Content should be an array.
		contentParts, ok := hfMsg.Content.([]ContentPart)
		require.True(t, ok, "Content should be []ContentPart")
		require.Len(t, contentParts, 2)

		// Verify text part.
		assert.Equal(t, "text", contentParts[0].Type)
		assert.Equal(t, "Describe this image:", contentParts[0].Text)

		// Verify image part.
		assert.Equal(t, "image_url", contentParts[1].Type)
		assert.NotNil(t, contentParts[1].ImageURL)
		assert.Equal(t, "https://example.com/test.jpg", contentParts[1].ImageURL.URL)
	})

	// Test converting a message with only text (should use string format).
	t.Run("text_only", func(t *testing.T) {
		msg := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: stringPtr("Hello"),
				},
			},
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)

		// Single text content should be string.
		content, ok := hfMsg.Content.(string)
		require.True(t, ok, "Single text content should be string")
		assert.Equal(t, "Hello", content)
	})

	// Test converting a message with multiple images.
	t.Run("multiple_images", func(t *testing.T) {
		msg := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: stringPtr("Compare these images:"),
				},
				{
					Type: model.ContentTypeImage,
					Image: &model.Image{
						URL: "https://example.com/image1.jpg",
					},
				},
				{
					Type: model.ContentTypeImage,
					Image: &model.Image{
						URL: "https://example.com/image2.jpg",
					},
				},
			},
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)

		contentParts, ok := hfMsg.Content.([]ContentPart)
		require.True(t, ok)
		require.Len(t, contentParts, 3)

		// Verify all parts.
		assert.Equal(t, "text", contentParts[0].Type)
		assert.Equal(t, "image_url", contentParts[1].Type)
		assert.Equal(t, "image_url", contentParts[2].Type)
	})
}

// Helper function to create string pointer.
func stringPtr(s string) *string {
	return &s
}

// TestModel_ExtraFields tests the extra fields functionality
func TestModel_ExtraFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and verify request body contains extra fields
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		json.Unmarshal(body, &reqMap)

		// Verify extra fields are present
		assert.Equal(t, "custom_value", reqMap["custom_field"])
		assert.Equal(t, float64(123), reqMap["custom_number"])

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Test response"
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithExtraFields(map[string]any{
			"custom_field":  "custom_value",
			"custom_number": 123,
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_StreamingError tests streaming request error handling
func TestModel_StreamingError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": {"message": "Unauthorized", "type": "auth_error"}}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("invalid-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, "Unauthorized")
}

// TestModel_NonStreamingError tests non-streaming request error handling
func TestModel_NonStreamingError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": {"message": "Bad request", "type": "invalid_request_error"}}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, "Bad request")
}

// TestModel_InvalidJSON tests handling of invalid JSON responses
func TestModel_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{invalid json}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
}

// TestModel_ContextCancellation tests context cancellation
func TestModel_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": "test"}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
}

// TestModel_StreamingInvalidChunk tests handling of invalid streaming chunks
func TestModel_StreamingInvalidChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Send a valid chunk first
		fmt.Fprint(w, `data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"valid"},"finish_reason":null}]}`+"\n\n")
		// Then send an invalid chunk (will be logged as warning but not stop the stream)
		fmt.Fprint(w, "data: {invalid json}\n\n")
		// End the stream
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should receive at least the valid chunk
	require.NotEmpty(t, responses)
	// The first response should be valid
	if len(responses) > 0 && responses[0].Error == nil {
		assert.NotEmpty(t, responses[0].Choices)
	}
}

// TestModel_WithCallbacks tests callback functionality
func TestModel_WithCallbacks(t *testing.T) {
	var requestCallbackCalled bool
	var chunkCallbackCalled bool
	var streamCompleteCallbackCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithChatRequestCallback(func(ctx context.Context, req *ChatCompletionRequest) {
			requestCallbackCalled = true
		}),
		WithChatChunkCallback(func(ctx context.Context, req *ChatCompletionRequest, chunk *ChatCompletionChunk) {
			chunkCallbackCalled = true
		}),
		WithChatStreamCompleteCallback(func(ctx context.Context, req *ChatCompletionRequest, err error) {
			streamCompleteCallbackCalled = true
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
	}

	assert.True(t, requestCallbackCalled)
	assert.True(t, chunkCallbackCalled)
	assert.True(t, streamCompleteCallbackCalled)
}

// TestModel_WithTools tests tool calling functionality
func TestModel_WithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and verify request contains tools
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		json.Unmarshal(body, &reqMap)

		// Verify tools are present
		tools, ok := reqMap["tools"].([]any)
		assert.True(t, ok)
		assert.NotEmpty(t, tools)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{
						"id": "call_123",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"location\":\"Beijing\"}"
						}
					}]
				},
				"finish_reason": "tool_calls"
			}]
		}`)
	}))
	defer server.Close()

	// Create a simple mock tool
	mockTool := &simpleMockTool{
		name:        "get_weather",
		description: "Get weather information",
	}

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What's the weather in Beijing?"},
		},
		Tools: map[string]tool.Tool{
			"get_weather": mockTool,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	require.NotEmpty(t, responses[0].Choices)
	assert.NotEmpty(t, responses[0].Choices[0].Message.ToolCalls)
	assert.Equal(t, "get_weather", responses[0].Choices[0].Message.ToolCalls[0].Function.Name)
}

// simpleMockTool is a simple mock tool for testing
type simpleMockTool struct {
	name        string
	description string
}

func (t *simpleMockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.description,
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"location": {
					Type:        "string",
					Description: "City name",
				},
			},
			Required: []string{"location"},
		},
	}
}

func (t *simpleMockTool) Execute(ctx context.Context, input string) (string, error) {
	return "Sunny, 25°C", nil
}

// TestModel_ToolCallResponse tests responding to tool calls
func TestModel_ToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "The weather in Beijing is sunny."
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What's the weather?"},
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID:   "call_123",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "get_weather",
							Arguments: []byte(`{"location":"Beijing"}`),
						},
					},
				},
			},
			{
				Role:     model.RoleTool,
				Content:  "Sunny, 25°C",
				ToolID:   "call_123",
				ToolName: "get_weather",
			},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestConvertRequest tests the convertRequest function with various scenarios
func TestConvertRequest(t *testing.T) {
	m, _ := New("test-model", WithAPIKey("test-key"))

	t.Run("with_generation_config", func(t *testing.T) {
		maxTokens := 100
		temp := 0.7
		topP := 0.9
		request := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleUser, Content: "test"},
			},
			GenerationConfig: model.GenerationConfig{
				MaxTokens:   &maxTokens,
				Temperature: &temp,
				TopP:        &topP,
				Stop:        []string{"stop1", "stop2"},
			},
		}

		hfReq, err := m.convertRequest(request)
		require.NoError(t, err)
		assert.Equal(t, &maxTokens, hfReq.MaxTokens)
		assert.Equal(t, &temp, hfReq.Temperature)
		assert.Equal(t, &topP, hfReq.TopP)
		assert.Equal(t, []string{"stop1", "stop2"}, hfReq.Stop)
	})

	t.Run("with_system_message", func(t *testing.T) {
		request := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleSystem, Content: "You are a helpful assistant"},
				{Role: model.RoleUser, Content: "Hello"},
			},
		}

		hfReq, err := m.convertRequest(request)
		require.NoError(t, err)
		assert.Len(t, hfReq.Messages, 2)
		assert.Equal(t, "system", hfReq.Messages[0].Role)
	})
}

// TestConvertMessage tests message conversion with different content types
func TestConvertMessage(t *testing.T) {
	t.Run("simple_text_message", func(t *testing.T) {
		msg := model.Message{
			Role:    model.RoleUser,
			Content: "Hello",
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)
		assert.Equal(t, "user", hfMsg.Role)
		assert.Equal(t, "Hello", hfMsg.Content)
	})

	t.Run("message_with_tool_id", func(t *testing.T) {
		msg := model.Message{
			Role:     model.RoleUser,
			Content:  "Hello",
			ToolID:   "tool_123",
			ToolName: "test_tool",
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)
		assert.Equal(t, "user", hfMsg.Role)
	})

	t.Run("tool_message", func(t *testing.T) {
		msg := model.Message{
			Role:     model.RoleTool,
			Content:  "Tool result",
			ToolID:   "call_123",
			ToolName: "get_weather",
		}

		hfMsg, err := convertMessage(msg)
		require.NoError(t, err)
		assert.Equal(t, "tool", hfMsg.Role)
	})
}

// TestMarshalRequest tests the marshalRequest function
func TestMarshalRequest(t *testing.T) {
	t.Run("without_extra_fields", func(t *testing.T) {
		m, _ := New("test-model", WithAPIKey("test-key"))

		hfReq := &ChatCompletionRequest{
			Model: "test-model",
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
		}

		data, err := m.marshalRequest(hfReq)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	})

	t.Run("with_model_extra_fields", func(t *testing.T) {
		m, _ := New(
			"test-model",
			WithAPIKey("test-key"),
			WithExtraFields(map[string]any{
				"custom_field": "value",
			}),
		)

		hfReq := &ChatCompletionRequest{
			Model: "test-model",
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
		}

		data, err := m.marshalRequest(hfReq)
		require.NoError(t, err)

		var result map[string]any
		json.Unmarshal(data, &result)
		assert.Equal(t, "value", result["custom_field"])
	})

	t.Run("with_request_extra_fields", func(t *testing.T) {
		m, _ := New("test-model", WithAPIKey("test-key"))

		hfReq := &ChatCompletionRequest{
			Model: "test-model",
			Messages: []ChatMessage{
				{Role: "user", Content: "test"},
			},
			ExtraFields: map[string]any{
				"request_field": "request_value",
			},
		}

		data, err := m.marshalRequest(hfReq)
		require.NoError(t, err)

		var result map[string]any
		json.Unmarshal(data, &result)
		assert.Equal(t, "request_value", result["request_field"])
	})
}

// TestModel_MultimodalResponse tests handling of multimodal responses
func TestModel_MultimodalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": [
						{"type": "text", "text": "Here is an image:"},
						{"type": "image_url", "image_url": {"url": "https://example.com/image.jpg", "detail": "high"}}
					]
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Show me an image"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	require.NotEmpty(t, responses[0].Choices)
	assert.Len(t, responses[0].Choices[0].Message.ContentParts, 2)
	assert.Equal(t, model.ContentTypeText, responses[0].Choices[0].Message.ContentParts[0].Type)
	assert.Equal(t, model.ContentTypeImage, responses[0].Choices[0].Message.ContentParts[1].Type)
}

// TestModel_MultimodalResponseWithUnsupportedType tests handling of unsupported content types
func TestModel_MultimodalResponseWithUnsupportedType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": [
						{"type": "unknown_type", "text": "test"}
					]
				},
				"finish_reason": "stop"
			}]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	require.NotEmpty(t, responses[0].Choices)
	// Should convert unsupported type to text
	assert.Len(t, responses[0].Choices[0].Message.ContentParts, 1)
	assert.Equal(t, model.ContentTypeText, responses[0].Choices[0].Message.ContentParts[0].Type)
}

// TestModel_AdditionalOptions tests additional model options
func TestModel_AdditionalOptions(t *testing.T) {
	t.Run("with_presence_penalty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var reqMap map[string]any
			json.Unmarshal(body, &reqMap)

			assert.Equal(t, 0.5, reqMap["presence_penalty"])

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		m, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithBaseURL(server.URL),
		)
		require.NoError(t, err)

		presencePenalty := 0.5
		request := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleUser, Content: "test"},
			},
			GenerationConfig: model.GenerationConfig{
				PresencePenalty: &presencePenalty,
			},
		}

		ctx := context.Background()
		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err)

		var responses []*model.Response
		for resp := range responseChan {
			responses = append(responses, resp)
		}

		require.NotEmpty(t, responses)
		assert.Nil(t, responses[0].Error)
	})

	t.Run("with_frequency_penalty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var reqMap map[string]any
			json.Unmarshal(body, &reqMap)

			assert.Equal(t, 0.3, reqMap["frequency_penalty"])

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		m, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithBaseURL(server.URL),
		)
		require.NoError(t, err)

		frequencyPenalty := 0.3
		request := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleUser, Content: "test"},
			},
			GenerationConfig: model.GenerationConfig{
				FrequencyPenalty: &frequencyPenalty,
			},
		}

		ctx := context.Background()
		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err)

		var responses []*model.Response
		for resp := range responseChan {
			responses = append(responses, resp)
		}

		require.NotEmpty(t, responses)
		assert.Nil(t, responses[0].Error)
	})
}

// TestModel_RequestWithContentParts tests sending messages with content parts
func TestModel_RequestWithContentParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		json.Unmarshal(body, &reqMap)

		// Verify messages contain content parts
		messages := reqMap["messages"].([]any)
		assert.NotEmpty(t, messages)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	textContent := "What's in this image?"
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &textContent,
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "https://example.com/image.jpg",
							Detail: "high",
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_StreamingWithMultipleChunks tests streaming with multiple chunks
func TestModel_StreamingWithMultipleChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should receive multiple chunks
	assert.GreaterOrEqual(t, len(responses), 3)
}

// TestModel_EmptyMessages tests handling of empty messages
func TestModel_EmptyMessages(t *testing.T) {
	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should handle empty messages gracefully
	require.NotEmpty(t, responses)
}

// TestModel_LargeResponse tests handling of large responses
func TestModel_LargeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Create a large response
		largeContent := strings.Repeat("This is a test. ", 1000)
		response := fmt.Sprintf(`{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "%s"
				},
				"finish_reason": "stop"
			}]
		}`, largeContent)

		fmt.Fprint(w, response)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	assert.Greater(t, len(responses[0].Choices[0].Message.Content), 1000)
}

// TestModel_WithHTTPClient tests using custom HTTP client
func TestModel_WithHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	customClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithHTTPClient(customClient),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_ResponseWithUsage tests response with usage information
func TestModel_ResponseWithUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "test"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15
			}
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	assert.NotNil(t, responses[0].Usage)
	assert.Equal(t, 10, responses[0].Usage.PromptTokens)
	assert.Equal(t, 5, responses[0].Usage.CompletionTokens)
	assert.Equal(t, 15, responses[0].Usage.TotalTokens)
}

// TestModel_WithExtraHeaders tests using extra headers
func TestModel_WithExtraHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify extra headers are present
		assert.Equal(t, "custom-value", r.Header.Get("X-Custom-Header"))
		assert.Equal(t, "another-value", r.Header.Get("X-Another-Header"))

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithExtraHeaders(map[string]string{
			"X-Custom-Header":  "custom-value",
			"X-Another-Header": "another-value",
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_StreamingWithEmptyDelta tests streaming with empty delta
func TestModel_StreamingWithEmptyDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"test"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
}

// TestModel_NonStreamingWithError tests non-streaming request with HTTP error
func TestModel_NonStreamingWithError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "Internal server error", "type": "server_error"}}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, "Internal server error")
}

// TestModel_StreamingReadError tests streaming with read error
func TestModel_StreamingReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Send one valid chunk then close connection abruptly
		fmt.Fprint(w, `data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"test"},"finish_reason":null}]}`+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Connection will be closed by server shutdown
	}))

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Close server to simulate connection error
	server.Close()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should receive at least one response (could be error or valid chunk)
	require.NotEmpty(t, responses)
}

// TestModel_TokenTailoringWithCustomConfig tests token tailoring with custom config
func TestModel_TokenTailoringWithCustomConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEnableTokenTailoring(true),
		WithTokenTailoringConfig(&model.TokenTailoringConfig{
			ProtocolOverheadTokens: 100,
			ReserveOutputTokens:    500,
			OutputTokensFloor:      100,
			SafetyMarginRatio:      0.1,
		}),
	)
	require.NoError(t, err)

	// Create messages that exceed token limit
	var messages []model.Message
	for i := 0; i < 50; i++ {
		messages = append(messages,
			model.Message{
				Role:    model.RoleUser,
				Content: fmt.Sprintf("User message %d with some content", i),
			},
			model.Message{
				Role:    model.RoleAssistant,
				Content: fmt.Sprintf("Assistant response %d with some content", i),
			},
		)
	}

	request := &model.Request{
		Messages: messages,
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_TokenTailoringDisabled tests with token tailoring disabled
func TestModel_TokenTailoringDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEnableTokenTailoring(false),
	)
	require.NoError(t, err)

	// Create many messages
	var messages []model.Message
	for i := 0; i < 50; i++ {
		messages = append(messages,
			model.Message{
				Role:    model.RoleUser,
				Content: fmt.Sprintf("User message %d", i),
			},
		)
	}

	request := &model.Request{
		Messages: messages,
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	// Messages should not be tailored
	assert.Equal(t, 50, len(request.Messages))
}

// TestModel_WithTailoringStrategy tests token tailoring with default strategy
func TestModel_WithTailoringStrategy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
	)
	require.NoError(t, err)

	// Create messages that exceed token limit
	var messages []model.Message
	for i := 0; i < 30; i++ {
		messages = append(messages,
			model.Message{
				Role:    model.RoleUser,
				Content: fmt.Sprintf("User message %d with content", i),
			},
		)
	}

	request := &model.Request{
		Messages: messages,
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	// Messages should be tailored
	assert.Less(t, len(request.Messages), 30)
}

// TestModel_ResponseWithMultipleChoices tests response with multiple choices
func TestModel_ResponseWithMultipleChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "test-id",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [
				{
					"index": 0,
					"message": {"role": "assistant", "content": "Response 1"},
					"finish_reason": "stop"
				},
				{
					"index": 1,
					"message": {"role": "assistant", "content": "Response 2"},
					"finish_reason": "stop"
				}
			]
		}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	assert.Len(t, responses[0].Choices, 2)
	assert.Equal(t, "Response 1", responses[0].Choices[0].Message.Content)
	assert.Equal(t, "Response 2", responses[0].Choices[1].Message.Content)
}

// TestModel_TokenTailoringWithAutoCalculation tests token tailoring with auto-calculated limits
func TestModel_TokenTailoringWithAutoCalculation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEnableTokenTailoring(true),
		// Don't set MaxInputTokens, let it auto-calculate
	)
	require.NoError(t, err)

	// Create many messages
	var messages []model.Message
	for i := 0; i < 100; i++ {
		messages = append(messages,
			model.Message{
				Role:    model.RoleUser,
				Content: fmt.Sprintf("User message %d with some content to make it longer", i),
			},
		)
	}

	request := &model.Request{
		Messages: messages,
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	// Messages should be tailored (or at least not increased)
	assert.LessOrEqual(t, len(request.Messages), 100)
	// MaxTokens should be set automatically
	assert.NotNil(t, request.GenerationConfig.MaxTokens)
}

// TestModel_RequestWithMaxTokensSet tests that user-specified MaxTokens is respected
func TestModel_RequestWithMaxTokensSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqMap map[string]any
		json.Unmarshal(body, &reqMap)

		// Verify user-specified max_tokens is used
		assert.Equal(t, float64(200), reqMap["max_tokens"])

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":1234567890,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"test"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithEnableTokenTailoring(true),
	)
	require.NoError(t, err)

	maxTokens := 200
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "test"},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	// User-specified MaxTokens should be preserved
	assert.Equal(t, 200, *request.GenerationConfig.MaxTokens)
}

// TestModel_StreamingWithToolCalls tests streaming response with tool calls
func TestModel_StreamingWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\""}}]},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"test","object":"chat.completion.chunk","created":1234567890,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Beijing\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}

		for _, chunk := range chunks {
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What's the weather?"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	// Last response should have tool calls
	lastResp := responses[len(responses)-1]
	if len(lastResp.Choices) > 0 && len(lastResp.Choices[0].Message.ToolCalls) > 0 {
		assert.Equal(t, "get_weather", lastResp.Choices[0].Message.ToolCalls[0].Function.Name)
	}
}

// TestModel_CustomTokenTailoringConfig tests token tailoring with custom configuration.
func TestModel_CustomTokenTailoringConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	// Create model with custom token tailoring config
	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithTokenTailoringConfig(&model.TokenTailoringConfig{
			ProtocolOverheadTokens: 100,
			ReserveOutputTokens:    500,
			InputTokensFloor:       100,
			OutputTokensFloor:      50,
			SafetyMarginRatio:      0.1,
			MaxInputTokensRatio:    0.8,
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
}

// TestModel_ChatCallbacks tests request and response callbacks.
func TestModel_ChatCallbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	var requestCallbackCalled bool
	var responseCallbackCalled bool

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithChatRequestCallback(func(ctx context.Context, req *ChatCompletionRequest) {
			requestCallbackCalled = true
			assert.Equal(t, "test-model", req.Model)
		}),
		WithChatResponseCallback(func(ctx context.Context, req *ChatCompletionRequest, resp *ChatCompletionResponse) {
			responseCallbackCalled = true
			assert.Equal(t, "test-id", resp.ID)
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}

	assert.True(t, requestCallbackCalled, "Request callback should be called")
	assert.True(t, responseCallbackCalled, "Response callback should be called")
}

// TestModel_StreamingErrorResponseWithJSON tests streaming request with JSON error response.
func TestModel_StreamingErrorResponseWithJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error: ErrorDetail{
				Message: "Invalid request",
				Type:    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, "Invalid request")
}

// TestModel_StreamingErrorResponseWithoutJSON tests streaming request with non-JSON error response.
func TestModel_StreamingErrorResponseWithoutJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	assert.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, "500")
}

// TestModel_TokenTailoringWithMaxTokensSet tests that token tailoring respects user-set MaxTokens.
func TestModel_TokenTailoringWithMaxTokensSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify that MaxTokens is set to user's value
		assert.NotNil(t, req.MaxTokens)
		assert.Equal(t, 100, *req.MaxTokens)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
	)
	require.NoError(t, err)

	maxTokens := 100
	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

// TestModel_TokenTailoringCountTokensError tests token tailoring when CountTokens fails.
func TestModel_TokenTailoringCountTokensError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	// Create a mock token counter that returns an error
	mockCounter := &mockTokenCounter{
		countTokensRangeFunc: func(ctx context.Context, messages []model.Message, start, end int) (int, error) {
			return 0, fmt.Errorf("count tokens error")
		},
	}

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithTokenCounter(mockCounter),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	require.NotEmpty(t, responses)
	// Should still work even if count tokens fails
	assert.Nil(t, responses[0].Error)
}

// mockTokenCounter is a mock implementation of token.Counter for testing.
type mockTokenCounter struct {
	countTokensRangeFunc func(ctx context.Context, messages []model.Message, start, end int) (int, error)
}

func (m *mockTokenCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
	return 0, nil
}

func (m *mockTokenCounter) CountTokensRange(ctx context.Context, messages []model.Message, start, end int) (int, error) {
	if m.countTokensRangeFunc != nil {
		return m.countTokensRangeFunc(ctx, messages, start, end)
	}
	return 0, nil
}

// TestWithChannelBufferSize tests the WithChannelBufferSize option with various inputs.
func TestWithChannelBufferSize(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		expected int
	}{
		{
			name:     "positive size",
			size:     512,
			expected: 512,
		},
		{
			name:     "zero size uses default",
			size:     0,
			expected: defaultChannelBufferSize,
		},
		{
			name:     "negative size uses default",
			size:     -10,
			expected: defaultChannelBufferSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(
				"test-model",
				WithAPIKey("test-key"),
				WithChannelBufferSize(tt.size),
			)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, m.channelBufferSize)
		})
	}
}

// TestWithTokenCounter_Options tests the WithTokenCounter option.
func TestWithTokenCounter_Options(t *testing.T) {
	t.Run("with valid counter", func(t *testing.T) {
		counter := &mockTokenCounter{}
		m, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithTokenCounter(counter),
		)
		require.NoError(t, err)
		assert.NotNil(t, m.tokenCounter)
	})

	t.Run("with nil counter", func(t *testing.T) {
		_, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithTokenCounter(nil),
		)
		require.NoError(t, err)
		// Default counter is used
	})
}

// TestWithTokenTailoringConfig_Options tests the WithTokenTailoringConfig option with various inputs.
func TestWithTokenTailoringConfig_Options(t *testing.T) {
	t.Run("with nil config", func(t *testing.T) {
		_, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithTokenTailoringConfig(nil),
		)
		require.NoError(t, err)
		// Default config is used
	})

	t.Run("with partial config uses defaults", func(t *testing.T) {
		config := &model.TokenTailoringConfig{
			ProtocolOverheadTokens: 0,
			ReserveOutputTokens:    0,
		}
		m, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithTokenTailoringConfig(config),
		)
		require.NoError(t, err)
		assert.Equal(t, 512, config.ProtocolOverheadTokens)
		assert.Equal(t, 2048, config.ReserveOutputTokens)
		assert.Equal(t, 0.1, config.SafetyMarginRatio)
		assert.Equal(t, 1024, config.InputTokensFloor)
		assert.Equal(t, 512, config.OutputTokensFloor)
		assert.Equal(t, 0.8, config.MaxInputTokensRatio)
		_ = m
	})

	t.Run("with full config", func(t *testing.T) {
		config := &model.TokenTailoringConfig{
			ProtocolOverheadTokens: 100,
			ReserveOutputTokens:    500,
			SafetyMarginRatio:      0.2,
			InputTokensFloor:       200,
			OutputTokensFloor:      100,
			MaxInputTokensRatio:    0.9,
		}
		m, err := New(
			"test-model",
			WithAPIKey("test-key"),
			WithTokenTailoringConfig(config),
		)
		require.NoError(t, err)
		assert.Equal(t, 100, config.ProtocolOverheadTokens)
		assert.Equal(t, 500, config.ReserveOutputTokens)
		assert.Equal(t, 0.2, config.SafetyMarginRatio)
		assert.Equal(t, 200, config.InputTokensFloor)
		assert.Equal(t, 100, config.OutputTokensFloor)
		assert.Equal(t, 0.9, config.MaxInputTokensRatio)
		_ = m
	})
}

// TestConvertMessageToModel_WithContentParts tests convertMessageToModel with []ContentPart type.
func TestConvertMessageToModel_WithContentParts(t *testing.T) {
	tests := []struct {
		name     string
		hfMsg    ChatMessage
		validate func(t *testing.T, result model.Message)
	}{
		{
			name: "with text content part",
			hfMsg: ChatMessage{
				Role: "user",
				Content: []ContentPart{
					{
						Type: "text",
						Text: "Hello",
					},
				},
			},
			validate: func(t *testing.T, result model.Message) {
				assert.Equal(t, model.Role("user"), result.Role)
				require.Len(t, result.ContentParts, 1)
				assert.Equal(t, model.ContentTypeText, result.ContentParts[0].Type)
				require.NotNil(t, result.ContentParts[0].Text)
				assert.Equal(t, "Hello", *result.ContentParts[0].Text)
			},
		},
		{
			name: "with image_url content part",
			hfMsg: ChatMessage{
				Role: "user",
				Content: []ContentPart{
					{
						Type: "image_url",
						ImageURL: &ImageURL{
							URL:    "https://example.com/image.jpg",
							Detail: "high",
						},
					},
				},
			},
			validate: func(t *testing.T, result model.Message) {
				assert.Equal(t, model.Role("user"), result.Role)
				require.Len(t, result.ContentParts, 1)
				assert.Equal(t, model.ContentTypeImage, result.ContentParts[0].Type)
				require.NotNil(t, result.ContentParts[0].Image)
				assert.Equal(t, "https://example.com/image.jpg", result.ContentParts[0].Image.URL)
				assert.Equal(t, "high", result.ContentParts[0].Image.Detail)
			},
		},
		{
			name: "with nil image_url",
			hfMsg: ChatMessage{
				Role: "user",
				Content: []ContentPart{
					{
						Type:     "image_url",
						ImageURL: nil,
					},
				},
			},
			validate: func(t *testing.T, result model.Message) {
				assert.Equal(t, model.Role("user"), result.Role)
				require.Len(t, result.ContentParts, 1)
				assert.Equal(t, model.ContentTypeText, result.ContentParts[0].Type)
				require.NotNil(t, result.ContentParts[0].Text)
				assert.Equal(t, "image_url is nil", *result.ContentParts[0].Text)
			},
		},
		{
			name: "with unsupported content type",
			hfMsg: ChatMessage{
				Role: "user",
				Content: []ContentPart{
					{
						Type: "video",
					},
				},
			},
			validate: func(t *testing.T, result model.Message) {
				assert.Equal(t, model.Role("user"), result.Role)
				require.Len(t, result.ContentParts, 1)
				assert.Equal(t, model.ContentTypeText, result.ContentParts[0].Type)
				require.NotNil(t, result.ContentParts[0].Text)
				assert.Contains(t, *result.ContentParts[0].Text, "unsupported content type: video")
			},
		},
		{
			name: "with tool call id",
			hfMsg: ChatMessage{
				Role:       "tool",
				ToolCallID: "call_123",
				Name:       "get_weather",
				Content:    "sunny",
			},
			validate: func(t *testing.T, result model.Message) {
				assert.Equal(t, model.Role("tool"), result.Role)
				assert.Equal(t, "call_123", result.ToolID)
				assert.Equal(t, "get_weather", result.ToolName)
				assert.Equal(t, "sunny", result.Content)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertMessageToModel(tt.hfMsg)
			tt.validate(t, result)
		})
	}
}

// TestConvertTool_Cases tests convertTool with various cases.
func TestConvertTool_Cases(t *testing.T) {
	t.Run("with valid tool", func(t *testing.T) {
		mockTool := &mockToolForConvert{
			declaration: &tool.Declaration{
				Name:        "test_tool",
				Description: "A test tool",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"param1": {
							Type: "string",
						},
					},
				},
			},
		}

		result, err := convertTool(mockTool)
		require.NoError(t, err)
		assert.Equal(t, "function", result.Type)
		assert.Equal(t, "test_tool", result.Function.Name)
		assert.Equal(t, "A test tool", result.Function.Description)
		assert.NotNil(t, result.Function.Parameters)
	})

	t.Run("with nil input schema", func(t *testing.T) {
		mockTool := &mockToolForConvert{
			declaration: &tool.Declaration{
				Name:        "test_tool",
				Description: "A test tool",
				InputSchema: nil,
			},
		}

		result, err := convertTool(mockTool)
		require.NoError(t, err)
		assert.Equal(t, "function", result.Type)
		assert.Equal(t, "test_tool", result.Function.Name)
		assert.Nil(t, result.Function.Parameters)
	})
}

// mockToolForConvert is a mock implementation of tool.Tool for testing.
type mockToolForConvert struct {
	declaration *tool.Declaration
}

func (m *mockToolForConvert) Declaration() *tool.Declaration {
	return m.declaration
}

// TestWithTailoringStrategy tests the WithTailoringStrategy option.
func TestWithTailoringStrategy_Option(t *testing.T) {
	mockStrategy := &mockTailoringStrategyImpl{}
	m, err := New(
		"test-model",
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithTailoringStrategy(mockStrategy),
	)
	require.NoError(t, err)
	assert.NotNil(t, m.tailoringStrategy)
}

// mockTailoringStrategyImpl is a mock implementation of model.TailoringStrategy for testing.
type mockTailoringStrategyImpl struct{}

func (m *mockTailoringStrategyImpl) TailorMessages(ctx context.Context, messages []model.Message, maxTokens int) ([]model.Message, error) {
	return messages, nil
}

// TestConvertContentPart_WithImageData tests convertContentPart with base64 image data.
func TestConvertContentPart_WithImageData(t *testing.T) {
	part := model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			Data:   []byte("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="),
			Format: "png",
			Detail: "high",
		},
	}

	result, err := convertContentPart(part)
	require.NoError(t, err)
	assert.Equal(t, "image_url", result.Type)
	require.NotNil(t, result.ImageURL)
	assert.Contains(t, result.ImageURL.URL, "data:image/png;base64,")
	assert.Equal(t, "high", result.ImageURL.Detail)
}

// TestConvertContentPart_WithNilImage tests convertContentPart with nil image.
func TestConvertContentPart_WithNilImage(t *testing.T) {
	part := model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: nil,
	}

	_, err := convertContentPart(part)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "image is nil")
}

// TestConvertContentPart_WithUnsupportedType tests convertContentPart with unsupported type.
func TestConvertContentPart_WithUnsupportedType(t *testing.T) {
	part := model.ContentPart{
		Type: model.ContentType("unsupported"),
	}

	_, err := convertContentPart(part)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content type")
}

// TestGenerateContent_WithExtraFields tests GenerateContent with extra fields.
func TestGenerateContent_WithExtraFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)

		// Verify extra fields are present
		assert.Equal(t, "custom_value", req["custom_field"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithExtraFields(map[string]any{
			"custom_field": "custom_value",
		}),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

// TestApplyTokenTailoring_WithCustomStrategy tests token tailoring with custom strategy.
func TestApplyTokenTailoring_WithCustomStrategy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	mockStrategy := &mockTailoringStrategyImpl{}
	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithTailoringStrategy(mockStrategy),
		WithMaxInputTokens(1000),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

// TestConvertMessage_WithContentPartsError tests convertMessage with content parts that cause errors.
func TestConvertMessage_WithContentPartsError(t *testing.T) {
	msg := model.Message{
		Role: "user",
		ContentParts: []model.ContentPart{
			{
				Type:  model.ContentTypeImage,
				Image: nil, // This will cause an error
			},
		},
	}

	_, err := convertMessage(msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to convert content part")
}

// TestGenerateContent_WithStructuredOutput tests GenerateContent with structured output.
func TestGenerateContent_WithStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify response format is set
		assert.NotNil(t, req.ResponseFormat)
		assert.Equal(t, "json_object", req.ResponseFormat.Type)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: `{"result": "success"}`,
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

// TestGenerateContent_WithContentParts tests GenerateContent with content parts.
func TestGenerateContent_WithContentParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)

	textContent := "Hello"
	request := &model.Request{
		Messages: []model.Message{
			{
				Role: "user",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &textContent,
					},
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "https://example.com/image.jpg",
							Detail: "high",
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

// TestConvertChunk_WithToolCalls tests convertChunk with tool calls.

// TestGenerateContent_WithMaxInputTokens tests GenerateContent with max input tokens.
func TestGenerateContent_WithMaxInputTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithMaxInputTokens(1000),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
		// Consume responses
	}
}

func TestGenerateContent_TokenTailoringAppliedToRequest(t *testing.T) {
	var capturedRequest *ChatCompletionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		capturedRequest = &ChatCompletionRequest{}
		err = json.Unmarshal(body, capturedRequest)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(50),
	)
	require.NoError(t, err)

	request := &model.Request{
		Messages: []model.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "This is a very long message that should be tailored because it exceeds the max input tokens limit."},
			{Role: "assistant", Content: "I understand."},
			{Role: "user", Content: "Another message."},
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
	}

	require.NotNil(t, capturedRequest, "应该捕获到 HTTP 请求")

	t.Logf("原始消息数: %d, 实际发送消息数: %d", len(request.Messages), len(capturedRequest.Messages))

	require.NotNil(t, capturedRequest.MaxTokens, "MaxTokens 应该被自动设置")
	assert.Greater(t, *capturedRequest.MaxTokens, 0, "MaxTokens 应该大于 0")
	t.Logf("自动设置的 MaxTokens: %d", *capturedRequest.MaxTokens)

	if len(capturedRequest.Messages) < len(request.Messages) {
		t.Logf("消息已被裁剪，从 %d 条减少到 %d 条", len(request.Messages), len(capturedRequest.Messages))
	}
}

func TestGenerateContent_TokenTailoringWithUserMaxTokens(t *testing.T) {
	var capturedRequest *ChatCompletionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		capturedRequest = &ChatCompletionRequest{}
		err = json.Unmarshal(body, capturedRequest)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Response",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer server.Close()

	m, err := New(
		"test-model",
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(50),
	)
	require.NoError(t, err)

	userMaxTokens := 100
	request := &model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &userMaxTokens,
		},
	}

	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	for range responseChan {
	}

	require.NotNil(t, capturedRequest, "应该捕获到 HTTP 请求")

	require.NotNil(t, capturedRequest.MaxTokens, "MaxTokens 不应该为 nil")
	assert.Equal(t, userMaxTokens, *capturedRequest.MaxTokens, "应该使用用户指定的 MaxTokens")
	t.Logf("用户指定的 MaxTokens 被正确保留: %d", *capturedRequest.MaxTokens)
}
