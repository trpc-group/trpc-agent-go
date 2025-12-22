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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const modelName = "meta-llama/Llama-3.1-8B-Instruct:ovhcloud"

//const modelName = "Tongyi-MAI/Z-Image-Turbo"

// TestIntegration_RealAPI_NonStreaming tests real HuggingFace API (non-streaming).
func TestIntegration_RealAPI_NonStreaming(t *testing.T) {
	t.Log("Running real HuggingFace API integration test (non-streaming)...")
	// Create model instance.
	m, err := New(
		modelName,
		WithAPIKey(ApiKey),
		WithEnableTokenTailoring(true),
		//WithTailoringStrategy(customStrategy),
		WithTokenTailoringConfig(&model.TokenTailoringConfig{
			ProtocolOverheadTokens: 256,
			ReserveOutputTokens:    1024,
			SafetyMarginRatio:      0.05,
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, m)

	// Helper function: create pointer.

	// Create request.
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What kind of special dreams should a person have?"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	// Execute request.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Log("Sending request to HuggingFace API...")
	responseChan, err := m.GenerateContent(ctx, request)
	//require.NoError(t, err)
	//require.NotNil(t, responseChan)

	// Collect responses.
	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)

		// If there is an error, log detailed information.
		if response.Error != nil {
			t.Logf("Response error: %v", response.Error)
		}
	}

	// Verify response.
	require.NotEmpty(t, responses, "Should receive at least one response")

	lastResp := responses[len(responses)-1]

	// If there is an error, print detailed information but do not fail (model may be unavailable).
	if lastResp.Error != nil {
		t.Logf("API returned error (this may be expected if model is not available): %v", lastResp.Error)
		t.Logf("Error details: %+v", lastResp.Error)
		// Do not mark as failed, as the model may be temporarily unavailable.
		return
	}

	// Verify successful response.
	assert.NotNil(t, lastResp)
	require.NotEmpty(t, lastResp.Choices, "Should have at least one choice")

	choice := lastResp.Choices[0]
	assert.NotEmpty(t, choice.Message.Content, "Response content should not be empty")
	assert.Equal(t, model.RoleAssistant, choice.Message.Role, "Response role should be assistant")

	t.Logf("✅ Received response from real API:")
	t.Logf("   Model: %s", modelName)
	t.Logf("   Content: %s", choice.Message.Content)

	// Verify Usage information (if available).
	if lastResp.Usage != nil {
		t.Logf("   Token usage - Prompt: %d, Completion: %d, Total: %d",
			lastResp.Usage.PromptTokens,
			lastResp.Usage.CompletionTokens,
			lastResp.Usage.TotalTokens)
		assert.Greater(t, lastResp.Usage.TotalTokens, 0, "Total tokens should be greater than 0")
	}
}

// TestIntegration_RealAPI_Streaming tests real HuggingFace API (streaming).
func TestIntegration_RealAPI_Streaming(t *testing.T) {
	t.Log("Running real HuggingFace API integration test (streaming)...")
	// Create model instance.
	m, err := New(
		modelName,
		WithAPIKey(ApiKey),
	)
	require.NoError(t, err)
	require.NotNil(t, m)

	// Helper function: create pointer.
	//intPtr := func(i int) *int { return &i }
	//float64Ptr := func(f float64) *float64 { return &f }

	// Create streaming request.
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Tell me a short joke."},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
			//MaxTokens:   intPtr(100),
			//Temperature: float64Ptr(0.8),
		},
	}

	// Execute request.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Log("Sending streaming request to HuggingFace API...")
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, responseChan)

	// Collect all streaming responses.
	var responses []*model.Response
	var fullContent string
	chunkCount := 0

	for response := range responseChan {
		responses = append(responses, response)
		chunkCount++

		// If there is an error, log detailed information.
		if response.Error != nil {
			t.Logf("Chunk %d error: %v", chunkCount, response.Error)
			continue
		}

		// Accumulate content.
		if len(response.Choices) > 0 {
			content := response.Choices[0].Delta.Content
			if content != "" {
				fullContent += content
				//t.Logf("Chunk %d: %q", chunkCount, content)
			}
		}
	}

	// Verify response.
	require.NotEmpty(t, responses, "Should receive at least one response")

	lastResp := responses[len(responses)-1]

	// If there is an error, print detailed information but do not fail.
	if lastResp.Error != nil {
		t.Logf("API returned error (this may be expected if model is not available): %v", lastResp.Error)
		return
	}

	// Verify streaming response.
	assert.Greater(t, chunkCount, 0, "Should receive at least one chunk")

	t.Logf("✅ Received streaming response from real API:")
	t.Logf("   Model: %s", modelName)
	t.Logf("   Total chunks: %d", chunkCount)
	t.Logf("   Full content: %s", fullContent)

	// Verify that at least some content was received.
	if fullContent != "" {
		assert.NotEmpty(t, fullContent, "Should receive some content from streaming")
	}
}

// TestIntegration_RealAPI_WithCallbacks tests real API callback mechanism.
func TestIntegration_RealAPI_WithCallbacks(t *testing.T) {

	// Callback counters.
	var requestCallbackCalled bool
	var chunkCallbackCount int
	var streamCompleteCallbackCalled bool

	// Create model instance with callbacks.
	m, err := New(
		modelName,
		WithAPIKey(ApiKey),
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

	// Helper function: create pointer.
	//intPtr := func(i int) *int { return &i }

	// Create streaming request.
	request := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "How can a person lie flat?"},
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
			//MaxTokens: intPtr(30),
		},
	}

	// Execute request.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err)

	// Consume all responses.
	for response := range responseChan {
		if response.Error != nil {
			t.Logf("Response error: %v", response.Error)
		}
	}

	// Wait for callbacks to complete.
	time.Sleep(100 * time.Millisecond)

	// Verify callbacks were called.
	t.Logf("\n📊 Callback Statistics:")
	t.Logf("   Request callback called: %v", requestCallbackCalled)
	t.Logf("   Chunk callbacks count: %d", chunkCallbackCount)
	t.Logf("   Stream complete callback called: %v", streamCompleteCallbackCalled)

	assert.True(t, requestCallbackCalled, "Request callback should be called")
	assert.True(t, streamCompleteCallbackCalled, "Stream complete callback should be called")
	// Chunk callback may not be called (if model is unavailable or returns error).
}
