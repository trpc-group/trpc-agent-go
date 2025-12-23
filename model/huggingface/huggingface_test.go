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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
		{
			name: "WithTRPC",
			opts: []Option{
				WithAPIKey("test-key"),
				WithTRPC("test-service", 5000),
			},
			validate: func(t *testing.T, m *Model) {
				assert.True(t, m.useTRPC)
				assert.Equal(t, "test-service", m.trpcServiceName)
				assert.Equal(t, 5000, m.trpcTimeout)
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
	// 创建 mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// 验证请求头
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// 返回模拟的成功响应
		mockResponse := `{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "meta-llama/Llama-3.1-8B-Instruct",
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

	// 使用一个测试模型名称
	testModelName := "meta-llama/Llama-3.1-8B-Instruct"

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
		testModelName,
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
		// 如果有错误，记录下来
		if resp.Error != nil {
			log.Errorf("Response error: %v", resp.Error)
		}
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
	
	// 检查响应是否有错误
	assert.Nil(t, responses[0].Error, "Response should not have error")
	
	// 检查是否有 Choices
	require.NotEmpty(t, responses[0].Choices, "Response should have choices")
	log.Info(responses[0].Choices[0].Message.Content)
	
	// 验证响应内容
	assert.NotEmpty(t, responses[0].Choices[0].Message.Content)
	assert.Equal(t, model.RoleAssistant, responses[0].Choices[0].Message.Role)
}

func TestModel_Multimodal_ImageURL(t *testing.T) {
	// Test sending a message with an image URL.
	//server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	//	w.Header().Set("Content-Type", "application/json").
	//	fmt.Fprint(w, `{.
	//		"id": "test-id",
	//		"object": "chat.completion",
	//		"created": 1699200000,
	//		"model": "test-model",
	//		"choices": [{
	//			"index": 0,
	//			"message": {
	//				"role": "assistant",
	//				"content": "I can see a beautiful landscape in the image."
	//			},
	//			"finish_reason": "stop"
	//		}],
	//		"usage": {
	//			"prompt_tokens": 100,
	//			"completion_tokens": 20,
	//			"total_tokens": 120
	//		}
	//	}`)
	//}))
	//defer server.Close().

	m, err := New(
		"zai-org/GLM-4.6V-Flash",
		WithAPIKey(ApiKey),
		//WithBaseURL(server.URL),
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
	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	assert.NotEmpty(t, responses[0].Choices)
	assert.Contains(t, responses[0].Choices[0].Message.Content, "image")
	log.Infof("%+v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_Base64Image(t *testing.T) {
	// Test sending a message with a base64-encoded image.
	//server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	//	w.Header().Set("Content-Type", "application/json").
	//	fmt.Fprint(w, `{.
	//		"id": "test-id",
	//		"object": "chat.completion",
	//		"created": 1699200000,
	//		"model": "test-model",
	//		"choices": [{
	//			"index": 0,
	//			"message": {
	//				"role": "assistant",
	//				"content": "This is a 1x1 red pixel image."
	//			},
	//			"finish_reason": "stop"
	//		}]
	//	}`)
	//}))
	//defer server.Close().

	m, err := New(
		"zai-org/GLM-4.6V-Flash",
		WithAPIKey(ApiKey),
		//WithBaseURL(server.URL),
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
	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	log.Infof("Responses: %v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_MultipleImages(t *testing.T) {
	// Test sending a message with multiple images.
	//server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	//	w.Header().Set("Content-Type", "application/json").
	//	fmt.Fprint(w, `{.
	//		"id": "test-id",
	//		"object": "chat.completion",
	//		"created": 1699200000,
	//		"model": "test-model",
	//		"choices": [{
	//			"index": 0,
	//			"message": {
	//				"role": "assistant",
	//				"content": "I can see two different images."
	//			},
	//			"finish_reason": "stop"
	//		}]
	//	}`)
	//}))
	//defer server.Close().

	m, err := New(
		"ServiceNow-AI/Apriel-1.6-15b-Thinker",
		WithAPIKey(ApiKey),
		//WithBaseURL(server.URL),
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
	require.NotEmpty(t, responses)
	assert.Nil(t, responses[0].Error)
	assert.NotEmpty(t, responses[0].Choices)
	log.Infof("responses: %v", responses[0].Choices[0].Message.Content)
}

func TestModel_Multimodal_StreamingWithImage(t *testing.T) {
	m, err := New(
		"ServiceNow-AI/Apriel-1.6-15b-Thinker",
		WithAPIKey(ApiKey),
		//WithBaseURL(server.URL),
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
							URL:    "data:image/jpeg;base64,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",
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