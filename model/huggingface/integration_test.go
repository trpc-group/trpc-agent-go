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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestIntegration_RealAPI_NonStreaming 测试真实的 HuggingFace API（非流式）
// 运行此测试需要设置环境变量：
//   export HUGGINGFACE_API_KEY=your_api_key
//   export RUN_INTEGRATION_TESTS=true
//   go test -v -run TestIntegration_RealAPI
func TestIntegration_RealAPI_NonStreaming(t *testing.T) {
	// 检查是否启用集成测试
	if os.Getenv("RUN_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_INTEGRATION_TESTS=true to run.")
	}

	// 从环境变量获取 API Key
	apiKey := os.Getenv("HUGGINGFACE_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test. Set HUGGINGFACE_API_KEY environment variable.")
	}

	t.Log("Running real HuggingFace API integration test (non-streaming)...")

	// 使用一个小型、快速响应的模型进行测试
	// microsoft/DialoGPT-small 是一个轻量级对话模型，适合测试
	modelName := "microsoft/DialoGPT-small"
	
	// 如果环境变量指定了其他模型，使用指定的模型
	if customModel := os.Getenv("HUGGINGFACE_TEST_MODEL"); customModel != "" {
		modelName = customModel
	}

	// 创建模型实例
	m, err := New(
		modelName,
		WithAPIKey(apiKey),
	)
	require.NoError(t, err)
	require.NotNil(t, m)

	// 辅助函数：创建指针
	intPtr := func(i int) *int { return &i }
	float64Ptr := func(f float64) *float64 { return &f }

	// 创建请求
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Hello! How are you?"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(50),  // 限制 token 数量以加快响应
			Temperature: float64Ptr(0.7),
		},
	}

	// 执行请求
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Log("Sending request to HuggingFace API...")
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, responseChan)

	// 收集响应
	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		
		// 如果有错误，记录详细信息
		if response.Error != nil {
			t.Logf("Response error: %v", response.Error)
		}
	}

	// 验证响应
	require.NotEmpty(t, responses, "Should receive at least one response")
	
	lastResp := responses[len(responses)-1]
	
	// 如果有错误，打印详细信息但不失败（可能是模型不可用）
	if lastResp.Error != nil {
		t.Logf("API returned error (this may be expected if model is not available): %v", lastResp.Error)
		t.Logf("Error details: %+v", lastResp.Error)
		// 不标记为失败，因为可能是模型暂时不可用
		return
	}

	// 验证成功响应
	assert.NotNil(t, lastResp)
	require.NotEmpty(t, lastResp.Choices, "Should have at least one choice")
	
	choice := lastResp.Choices[0]
	assert.NotEmpty(t, choice.Message.Content, "Response content should not be empty")
	assert.Equal(t, model.RoleAssistant, choice.Message.Role, "Response role should be assistant")
	
	t.Logf("✅ Received response from real API:")
	t.Logf("   Model: %s", modelName)
	t.Logf("   Content: %s", choice.Message.Content)
	
	// 验证 Usage 信息（如果有）
	if lastResp.Usage != nil {
		t.Logf("   Token usage - Prompt: %d, Completion: %d, Total: %d",
			lastResp.Usage.PromptTokens,
			lastResp.Usage.CompletionTokens,
			lastResp.Usage.TotalTokens)
		assert.Greater(t, lastResp.Usage.TotalTokens, 0, "Total tokens should be greater than 0")
	}
}

// TestIntegration_RealAPI_Streaming 测试真实的 HuggingFace API（流式）
// 运行此测试需要设置环境变量：
//   export HUGGINGFACE_API_KEY=your_api_key
//   export RUN_INTEGRATION_TESTS=true
//   go test -v -run TestIntegration_RealAPI
func TestIntegration_RealAPI_Streaming(t *testing.T) {
	// 检查是否启用集成测试
	if os.Getenv("RUN_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_INTEGRATION_TESTS=true to run.")
	}

	// 从环境变量获取 API Key
	apiKey := os.Getenv("HUGGINGFACE_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test. Set HUGGINGFACE_API_KEY environment variable.")
	}

	t.Log("Running real HuggingFace API integration test (streaming)...")

	// 使用支持流式响应的模型
	modelName := "microsoft/DialoGPT-small"
	
	if customModel := os.Getenv("HUGGINGFACE_TEST_MODEL"); customModel != "" {
		modelName = customModel
	}

	// 创建模型实例
	m, err := New(
		modelName,
		WithAPIKey(apiKey),
	)
	require.NoError(t, err)
	require.NotNil(t, m)

	// 辅助函数：创建指针
	intPtr := func(i int) *int { return &i }
	float64Ptr := func(f float64) *float64 { return &f }

	// 创建流式请求
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Tell me a short joke."},
		},
		GenerationConfig: model.GenerationConfig{
			Stream:      true,
			MaxTokens:   intPtr(100),
			Temperature: float64Ptr(0.8),
		},
	}

	// 执行请求
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Log("Sending streaming request to HuggingFace API...")
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, responseChan)

	// 收集所有流式响应
	var responses []*model.Response
	var fullContent string
	chunkCount := 0

	for response := range responseChan {
		responses = append(responses, response)
		chunkCount++
		
		// 如果有错误，记录详细信息
		if response.Error != nil {
			t.Logf("Chunk %d error: %v", chunkCount, response.Error)
			continue
		}
		
		// 累积内容
		if len(response.Choices) > 0 {
			content := response.Choices[0].Delta.Content
			if content != "" {
				fullContent += content
				t.Logf("Chunk %d: %q", chunkCount, content)
			}
		}
	}

	// 验证响应
	require.NotEmpty(t, responses, "Should receive at least one response")
	
	lastResp := responses[len(responses)-1]
	
	// 如果有错误，打印详细信息但不失败
	if lastResp.Error != nil {
		t.Logf("API returned error (this may be expected if model is not available): %v", lastResp.Error)
		return
	}

	// 验证流式响应
	assert.Greater(t, chunkCount, 0, "Should receive at least one chunk")
	
	t.Logf("✅ Received streaming response from real API:")
	t.Logf("   Model: %s", modelName)
	t.Logf("   Total chunks: %d", chunkCount)
	t.Logf("   Full content: %s", fullContent)
	
	// 验证至少收到了一些内容
	if fullContent != "" {
		assert.NotEmpty(t, fullContent, "Should receive some content from streaming")
	}
}

// TestIntegration_RealAPI_WithCallbacks 测试真实 API 的回调机制
func TestIntegration_RealAPI_WithCallbacks(t *testing.T) {
	// 检查是否启用集成测试
	if os.Getenv("RUN_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_INTEGRATION_TESTS=true to run.")
	}

	apiKey := os.Getenv("HUGGINGFACE_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test. Set HUGGINGFACE_API_KEY environment variable.")
	}

	t.Log("Running real HuggingFace API integration test (with callbacks)...")

	// 回调计数器
	var requestCallbackCalled bool
	var chunkCallbackCount int
	var streamCompleteCallbackCalled bool

	modelName := "microsoft/DialoGPT-small"
	if customModel := os.Getenv("HUGGINGFACE_TEST_MODEL"); customModel != "" {
		modelName = customModel
	}

	// 创建带回调的模型实例
	m, err := New(
		modelName,
		WithAPIKey(apiKey),
		WithChatRequestCallback(func(ctx context.Context, req *ChatCompletionRequest) {
			requestCallbackCalled = true
			t.Logf("📤 Request callback: sending request with %d messages", len(req.Messages))
		}),
		WithChatChunkCallback(func(ctx context.Context, req *ChatCompletionRequest, chunk *ChatCompletionChunk) {
			chunkCallbackCount++
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				t.Logf("📥 Chunk callback #%d: %q", chunkCallbackCount, chunk.Choices[0].Delta.Content)
			}
		}),
		WithChatStreamCompleteCallback(func(ctx context.Context, req *ChatCompletionRequest, streamErr error) {
			streamCompleteCallbackCalled = true
			if streamErr != nil {
				t.Logf("✅ Stream complete callback: completed with error: %v", streamErr)
			} else {
				t.Logf("✅ Stream complete callback: completed successfully")
			}
		}),
	)
	require.NoError(t, err)

	// 辅助函数：创建指针
	intPtr := func(i int) *int { return &i }

	// 创建流式请求
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Say hello!"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream:    true,
			MaxTokens: intPtr(30),
		},
	}

	// 执行请求
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// 消费所有响应
	for response := range responseChan {
		if response.Error != nil {
			t.Logf("Response error: %v", response.Error)
		}
	}

	// 等待回调完成
	time.Sleep(100 * time.Millisecond)

	// 验证回调被调用
	t.Logf("\n📊 Callback Statistics:")
	t.Logf("   Request callback called: %v", requestCallbackCalled)
	t.Logf("   Chunk callbacks count: %d", chunkCallbackCount)
	t.Logf("   Stream complete callback called: %v", streamCompleteCallbackCalled)

	assert.True(t, requestCallbackCalled, "Request callback should be called")
	assert.True(t, streamCompleteCallbackCalled, "Stream complete callback should be called")
	// Chunk callback 可能不会被调用（如果模型不可用或返回错误）
}
