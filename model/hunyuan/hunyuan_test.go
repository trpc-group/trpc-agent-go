//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hunyuan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan/internal/hunyuan"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubTool implements tool.Tool for testing purposes.
type stubTool struct{ decl *tool.Declaration }

// Call implements tool.Tool for testing.
func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }

// Declaration returns the tool declaration.
func (s stubTool) Declaration() *tool.Declaration { return s.decl }

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

func TestNew(t *testing.T) {
	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithChannelBufferSize(128),
		WithHttpClient(http.DefaultClient),
		WithChannelBufferSize(-1),
		WithChatChunkCallback(func(ctx context.Context, chatRequest *hunyuan.ChatCompletionNewParams, chatChunk *hunyuan.ChatCompletionResponse) {
		}),
		WithChatResponseCallback(func(ctx context.Context, chatRequest *hunyuan.ChatCompletionNewParams, chatResponse *hunyuan.ChatCompletionResponse) {
		}),
		WithChatStreamCompleteCallback(func(ctx context.Context, chatRequest *hunyuan.ChatCompletionNewParams, streamErr error) {}),
		WithChatRequestCallback(func(ctx context.Context, chatRequest *hunyuan.ChatCompletionNewParams) {}),
		WithTokenCounter(&testStubCounter{}),
		WithTailoringStrategy(&testStubStrategy{}),
	)

	if m == nil {
		t.Fatal("New returned nil")
	}

	if m.name != "hunyuan-lite" {
		t.Errorf("Expected name 'hunyuan-lite', got %s", m.name)
	}

	if m.channelBufferSize != defaultChannelBufferSize {
		t.Errorf("Expected channelBufferSize 128, got %d", m.channelBufferSize)
	}
}

func TestInfo(t *testing.T) {
	m := New("hunyuan-lite")
	info := m.Info()

	if info.Name != "hunyuan-lite" {
		t.Errorf("Expected name 'hunyuan-lite', got %s", info.Name)
	}
}

func TestGenerateContentNilRequest(t *testing.T) {
	m := New("hunyuan-lite")
	ctx := context.Background()

	_, err := m.GenerateContent(ctx, nil)
	if err == nil {
		t.Error("Expected error for nil request, got nil")
	}
}

func TestGenerateContentWithMockServer(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := struct {
			Response hunyuan.ChatCompletionResponse `json:"Response"`
		}{
			Response: hunyuan.ChatCompletionResponse{
				Id:      "test-id-123",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: "stop",
						Message: &hunyuan.ChatCompletionMessageParam{
							Role:    "assistant",
							Content: "Hello! This is a test response.",
						},
					},
				},
				Usage: hunyuan.ChatCompletionResponseUsage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
				},
				RequestId: "test-request-id",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer mockServer.Close()

	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
		WithEnableTokenTailoring(true),
	)

	ctx := context.Background()
	temp := 0.7
	request := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleUser,
				Content: "Hello",
			},
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temp,
			Stream:      false,
		},
	}

	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	if len(responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(responses))
	}

	resp := responses[0]
	if resp.Error != nil {
		t.Fatalf("Unexpected error in response: %v", resp.Error)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(resp.Choices))
	}

	if resp.Choices[0].Message.Content != "Hello! This is a test response." {
		t.Errorf("Unexpected message content: %s", resp.Choices[0].Message.Content)
	}

	if resp.Usage == nil {
		t.Fatal("Expected usage information, got nil")
	}

	if resp.Usage.TotalTokens != 30 {
		t.Errorf("Expected TotalTokens 30, got %d", resp.Usage.TotalTokens)
	}
}

func TestGenerateContentStreamWithMockServer(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Expected http.ResponseWriter to be an http.Flusher")
		}

		chunks := []hunyuan.ChatCompletionResponse{
			{
				Id:      "stream-id-1",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &hunyuan.ChatCompletionResponseDelta{
							Role:    "assistant",
							Content: "Hello",
						},
					},
				},
			},
			{
				Id:      "stream-id-2",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &hunyuan.ChatCompletionResponseDelta{
							Content: " from",
						},
					},
				},
			},
			{
				Id:      "stream-id-3",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &hunyuan.ChatCompletionResponseDelta{
							Content: " Hunyuan!",
						},
						FinishReason: "stop",
					},
				},
			},
		}

		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	request := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleUser,
				Content: "Hello",
			},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	var responses []*model.Response
	var fullContent string
	for resp := range responseChan {
		responses = append(responses, resp)
		if resp.Error != nil {
			t.Fatalf("Unexpected error in response: %v", resp.Error)
		}
		if len(resp.Choices) > 0 {
			fullContent += resp.Choices[0].Delta.Content
		}
	}

	if len(responses) != 3 {
		t.Errorf("Expected 3 responses, got %d", len(responses))
	}

	expectedContent := "Hello from Hunyuan!"
	if fullContent != expectedContent {
		t.Errorf("Expected content '%s', got '%s'", expectedContent, fullContent)
	}
}

func TestConvertMessage(t *testing.T) {
	msg := model.Message{
		Role:    model.RoleUser,
		Content: "Hello",
	}

	hMsg, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage failed: %v", err)
	}

	if hMsg.Role != "user" {
		t.Errorf("Expected role 'user', got %s", hMsg.Role)
	}

	if hMsg.Content != "Hello" {
		t.Errorf("Expected content 'Hello', got %s", hMsg.Content)
	}

	msg = model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{
				Type: "function",
				ID:   "call_123",
				Function: model.FunctionDefinitionParam{
					Name:      "get_weather",
					Arguments: []byte(`{"location":"Beijing"}`),
				},
			},
		},
	}

	hMsg, err = convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage failed: %v", err)
	}

	if len(hMsg.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(hMsg.ToolCalls))
	}

	if hMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("Expected function name 'get_weather', got %s", hMsg.ToolCalls[0].Function.Name)
	}

	msg.ContentParts = []model.ContentPart{
		{
			Type: model.ContentTypeText,
			Text: &[]string{"Hello", "world"}[0],
		},
		{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				URL: "https://example.com/image.png",
			},
		},
		{
			Type: model.ContentTypeAudio,
			Audio: &model.Audio{
				Data: []byte("audio data"),
			},
		},
	}
	hMsg, err = convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage failed: %v", err)
	}

	if len(hMsg.Contents) != 3 {
		t.Fatalf("Expected 3 content parts, got %d", len(hMsg.Contents))
	}
}

func TestTokenTailoringOptions(t *testing.T) {
	config := &model.TokenTailoringConfig{
		ProtocolOverheadTokens: 1024,
		ReserveOutputTokens:    4096,
		InputTokensFloor:       2048,
		OutputTokensFloor:      512,
		SafetyMarginRatio:      0.15,
		MaxInputTokensRatio:    0.9,
	}

	m := New("hunyuan-lite",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100000),
		WithTokenTailoringConfig(config),
	)

	if !m.enableTokenTailoring {
		t.Error("Expected enableTokenTailoring to be true")
	}

	if m.maxInputTokens != 100000 {
		t.Errorf("Expected maxInputTokens 100000, got %d", m.maxInputTokens)
	}

	if m.protocolOverheadTokens != 1024 {
		t.Errorf("Expected protocolOverheadTokens 1024, got %d", m.protocolOverheadTokens)
	}

	if m.reserveOutputTokens != 4096 {
		t.Errorf("Expected reserveOutputTokens 4096, got %d", m.reserveOutputTokens)
	}
}

func TestConvertMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		validate func(t *testing.T, messages []*hunyuan.ChatCompletionMessageParam)
		wantLen  int
		wantErr  bool
	}{
		{
			name: "user message",
			messages: []model.Message{
				model.NewUserMessage("hello"),
			},
			wantLen: 1,
			validate: func(t *testing.T, messages []*hunyuan.ChatCompletionMessageParam) {
				if messages[0].Role != "user" {
					t.Errorf("Expected role 'user', got %s", messages[0].Role)
				}
				if messages[0].Content != "hello" {
					t.Errorf("Expected content 'hello', got %s", messages[0].Content)
				}
			},
			wantErr: false,
		},
		{
			name: "system and user messages",
			messages: []model.Message{
				model.NewSystemMessage("You are helpful"),
				model.NewUserMessage("hello"),
			},
			validate: func(t *testing.T, messages []*hunyuan.ChatCompletionMessageParam) {
				if messages[0].Role != "system" {
					t.Errorf("Expected role 'system', got %s", messages[0].Role)
				}
				if messages[0].Content != "You are helpful" {
					t.Errorf("Expected content 'You are helpful', got %s", messages[0].Content)
				}
				if messages[1].Role != "user" {
					t.Errorf("Expected role 'user', got %s", messages[1].Role)
				}
				if messages[1].Content != "hello" {
					t.Errorf("Expected content 'hello', got %s", messages[1].Content)
				}
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
			validate: func(t *testing.T, messages []*hunyuan.ChatCompletionMessageParam) {
				if messages[0].Role != "assistant" {
					t.Errorf("Expected role 'assistant', got %s", messages[0].Role)
				}
				if messages[0].Content != "Let me help" {
					t.Errorf("Expected content 'Let me help', got %s", messages[0].Content)
				}
				if len(messages[0].ToolCalls) != 1 {
					t.Fatalf("Expected 1 tool call, got %d", len(messages[0].ToolCalls))
				}
				if messages[0].ToolCalls[0].Function.Name != "get_weather" {
					t.Errorf("Expected function name 'get_weather', got %s", messages[0].ToolCalls[0].Function.Name)
				}
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
							Type: model.ContentTypeImage,
							Image: &model.Image{
								Data: []byte("fake image data"),
							},
						},
					},
				},
			},
			validate: func(t *testing.T, messages []*hunyuan.ChatCompletionMessageParam) {
				if messages[0].Role != "user" {
					t.Errorf("Expected role 'user', got %s", messages[0].Role)
				}
				if len(messages[0].Contents) != 1 {
					t.Fatalf("Expected 1 content part, got %d", len(messages[0].Contents))
				}
				if messages[0].Contents[0].Type != "image_url" {
					t.Errorf("Expected type 'image_url', got %s", messages[0].Contents[0].Type)
				}
			},
			wantLen: 1,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertMessages(tt.messages)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if len(result) != tt.wantLen {
					t.Errorf("Expected %d messages, got %d", tt.wantLen, len(result))
				}
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestBuildChatRequest(t *testing.T) {
	m := New("hunyuan-lite")

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
		Tools: map[string]tool.Tool{
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
		},
	}

	chatReq, err := m.buildChatRequest(req)
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}

	if chatReq.Model != "hunyuan-lite" {
		t.Errorf("Expected model 'hunyuan-lite', got %s", chatReq.Model)
	}

	if !chatReq.Stream {
		t.Error("Expected Stream to be true")
	}

	if chatReq.Temperature != temp {
		t.Errorf("Expected temperature %f, got %f", temp, chatReq.Temperature)
	}

	if chatReq.TopP != topP {
		t.Errorf("Expected topP %f, got %f", topP, chatReq.TopP)
	}

	if len(chatReq.Stop) != 1 || chatReq.Stop[0] != "STOP" {
		t.Errorf("Expected stop ['STOP'], got %v", chatReq.Stop)
	}

	if !chatReq.EnableThinking {
		t.Error("Expected EnableThinking to be true")
	}
}

func TestBuildChatRequestEmptyMessages(t *testing.T) {
	m := New("hunyuan-lite")

	req := &model.Request{
		Messages: []model.Message{},
	}

	chatReq, err := m.buildChatRequest(req)
	if err == nil {
		t.Error("Expected error for empty messages, got nil")
	}
	if chatReq != nil {
		t.Error("Expected nil chatReq for empty messages")
	}
}

func TestHandleErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(srv.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
	}

	ch, err := m.GenerateContent(ctx, req)
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	var got *model.Response
	select {
	case got = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	if got == nil {
		t.Fatal("Expected response, got nil")
	}
	if got.Error == nil {
		t.Error("Expected error in response, got nil")
	}
	if !got.Done {
		t.Error("Expected Done to be true")
	}
}

func TestWithTokenTailoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := struct {
			Response hunyuan.ChatCompletionResponse `json:"Response"`
		}{
			Response: hunyuan.ChatCompletionResponse{
				Id:      "test-id",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: "stop",
						Message: &hunyuan.ChatCompletionMessageParam{
							Role:    "assistant",
							Content: "OK",
						},
					},
				},
				Usage: hunyuan.ChatCompletionResponseUsage{
					PromptTokens:     5,
					CompletionTokens: 1,
					TotalTokens:      6,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer srv.Close()

	var capturedReq *hunyuan.ChatCompletionNewParams
	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(srv.URL),
		WithHost("test-host"),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *hunyuan.ChatCompletionNewParams) {
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
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	if capturedReq == nil {
		t.Fatal("Expected captured request, got nil")
	}
	if len(capturedReq.Messages) != 1 {
		t.Errorf("Expected 1 message after tailoring, got %d", len(capturedReq.Messages))
	}
}

func TestWithEnableTokenTailoringDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := struct {
			Response hunyuan.ChatCompletionResponse `json:"Response"`
		}{
			Response: hunyuan.ChatCompletionResponse{
				Id:      "test-id",
				Created: time.Now().Unix(),
				Choices: []*hunyuan.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: "stop",
						Message: &hunyuan.ChatCompletionMessageParam{
							Role:    "assistant",
							Content: "OK",
						},
					},
				},
				Usage: hunyuan.ChatCompletionResponseUsage{
					PromptTokens:     5,
					CompletionTokens: 1,
					TotalTokens:      6,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer srv.Close()

	var capturedReq *hunyuan.ChatCompletionNewParams
	m := New("hunyuan-lite",
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(srv.URL),
		WithHost("test-host"),
		WithEnableTokenTailoring(false),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *hunyuan.ChatCompletionNewParams) {
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
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	if capturedReq == nil {
		t.Fatal("Expected captured request, got nil")
	}
	if len(capturedReq.Messages) != 2 {
		t.Errorf("Expected 2 messages without tailoring, got %d", len(capturedReq.Messages))
	}
}

func TestWithChannelBufferSize(t *testing.T) {
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
			m := New("hunyuan-lite", WithChannelBufferSize(tt.size))
			if m.channelBufferSize != tt.want {
				t.Errorf("Expected channelBufferSize %d, got %d", tt.want, m.channelBufferSize)
			}
		})
	}
}

func TestConvertChatResponse(t *testing.T) {
	resp := &hunyuan.ChatCompletionResponse{
		Id:      "test-id",
		Created: time.Now().Unix(),
		Choices: []*hunyuan.ChatCompletionResponseChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: &hunyuan.ChatCompletionMessageParam{
					Role:    "assistant",
					Content: "Hello",
				},
			},
		},
		Usage: hunyuan.ChatCompletionResponseUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	result, err := convertChatResponse(resp)
	if err != nil {
		t.Fatalf("convertChatResponse failed: %v", err)
	}

	if !result.Done {
		t.Error("Expected Done to be true")
	}

	if len(result.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(result.Choices))
	}

	if result.Choices[0].Message.Content != "Hello" {
		t.Errorf("Expected content 'Hello', got %s", result.Choices[0].Message.Content)
	}

	if result.Usage == nil {
		t.Fatal("Expected usage information, got nil")
	}

	if result.Usage.PromptTokens != 10 {
		t.Errorf("Expected PromptTokens 10, got %d", result.Usage.PromptTokens)
	}

	if result.Usage.CompletionTokens != 5 {
		t.Errorf("Expected CompletionTokens 5, got %d", result.Usage.CompletionTokens)
	}

	if result.Usage.TotalTokens != 15 {
		t.Errorf("Expected TotalTokens 15, got %d", result.Usage.TotalTokens)
	}
}

func TestConvertChatResponseWithToolCalls(t *testing.T) {
	resp := &hunyuan.ChatCompletionResponse{
		Id:      "test-id",
		Created: time.Now().Unix(),
		Choices: []*hunyuan.ChatCompletionResponseChoice{
			{
				Index:        0,
				FinishReason: "tool_calls",
				Message: &hunyuan.ChatCompletionMessageParam{
					Role:    "assistant",
					Content: "Using tool",
					ToolCalls: []*hunyuan.ChatCompletionMessageToolCall{
						{
							Id:   "call1",
							Type: functionToolType,
							Function: &hunyuan.ChatCompletionMessageToolCallFunction{
								Name:      "get_weather",
								Arguments: `{"city":"Beijing"}`,
							},
						},
					},
				},
			},
		},
		Usage: hunyuan.ChatCompletionResponseUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	result, err := convertChatResponse(resp)
	if err != nil {
		t.Fatalf("convertChatResponse failed: %v", err)
	}

	if !result.Done {
		t.Error("Expected Done to be true")
	}

	if len(result.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(result.Choices))
	}

	if len(result.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(result.Choices[0].Message.ToolCalls))
	}

	toolCall := result.Choices[0].Message.ToolCalls[0]
	if toolCall.ID != "call1" {
		t.Errorf("Expected tool call ID 'call1', got %s", toolCall.ID)
	}

	if toolCall.Function.Name != "get_weather" {
		t.Errorf("Expected function name 'get_weather', got %s", toolCall.Function.Name)
	}
}

func TestImageToURLOrBase64(t *testing.T) {
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
			want: "http://example.com/image.jpg",
		},
		{
			name: "with data",
			image: &model.Image{
				Format: "png",
				Data:   []byte("test"),
			},
			want: "data:image/png;base64,dGVzdA==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := imageToURLOrBase64(tt.image)
			if result != tt.want {
				t.Errorf("Expected %s, got %s", tt.want, result)
			}
		})
	}
}

func TestAudioToBase64(t *testing.T) {
	audio := &model.Audio{
		Format: "audio/mp3",
		Data:   []byte("test audio data"),
	}

	result := audioToBase64(audio)
	expected := "data:audio/mp3;base64,dGVzdCBhdWRpbyBkYXRh"

	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
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
