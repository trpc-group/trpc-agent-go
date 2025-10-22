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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	openai "github.com/openai/openai-go"
	openaigo "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Setup.
	os.Exit(m.Run())
}

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		opts        []Option
		expectError bool
	}{
		{
			name:      "valid openai model",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey("test-key"),
			},
			expectError: false,
		},
		{
			name:      "valid model with base url",
			modelName: "custom-model",
			opts: []Option{
				WithAPIKey("test-key"),
				WithBaseURL("https://api.custom.com"),
			},
			expectError: false,
		},
		{
			name:      "empty api key",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey(""),
			},
			expectError: false, // Should still create model, but may fail on actual calls
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.modelName, tt.opts...)
			require.NotNil(t, m, "expected model to be created, got nil")

			o := options{}
			for _, opt := range tt.opts {
				opt(&o)
			}

			assert.Equal(t, tt.modelName, m.name, "expected model name %s, got %s", tt.modelName, m.name)
			assert.Equal(t, o.APIKey, m.apiKey, "expected api key %s, got %s", o.APIKey, m.apiKey)
			assert.Equal(t, o.BaseURL, m.baseURL, "expected base url %s, got %s", o.BaseURL, m.baseURL)
		})
	}
}

func TestModel_GenContent_NilReq(t *testing.T) {
	m := New("test-model", WithAPIKey("test-key"))

	ctx := context.Background()
	_, err := m.GenerateContent(ctx, nil)

	require.Error(t, err, "expected error for nil request, got nil")
	assert.Equal(t, "request cannot be nil", err.Error(), "expected 'request cannot be nil', got %s", err.Error())
}

func TestModel_GenContent_ValidReq(t *testing.T) {
	// Skip this test if no API key is provided.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	m := New("gpt-3.5-turbo", WithAPIKey(apiKey))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	temperature := 0.7
	maxTokens := 50

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("Say hello in exactly 3 words."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
	}

	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err, "failed to generate content: %v", err)

	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		if response.Done {
			break
		}
	}

	require.NotEmpty(t, responses, "expected at least one response, got none")
}

func TestModel_GenContent_CustomBaseURL(t *testing.T) {
	// This test creates a model with custom base URL but doesn't make actual calls.
	// It's mainly to test the configuration.

	customBaseURL := "https://api.custom-openai.com"
	m := New("custom-model", WithAPIKey("test-key"), WithBaseURL(customBaseURL))

	assert.Equal(t, customBaseURL, m.baseURL, "expected base URL %s, got %s", customBaseURL, m.baseURL)

	// Test that the model can be created without errors.
	ctx := context.Background()
	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("test"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	// This will likely fail due to invalid API key/URL, but should not panic.
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err, "failed to create request: %v", err)

	// Just consume one response to test the channel setup.
	select {
	case response := <-responseChan:
		if response != nil && response.Error == nil {
			t.Log("Unexpected success with test credentials")
		}
	case <-time.After(5 * time.Second):
		t.Log("Request timed out as expected with test credentials")
	}
}

func TestOptions_Validation(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{
			name: "empty options",
			opts: []Option{},
		},
		{
			name: "only api key",
			opts: []Option{
				WithAPIKey("test-key"),
			},
		},
		{
			name: "only base url",
			opts: []Option{
				WithBaseURL("https://api.example.com"),
			},
		},
		{
			name: "both api key and base url",
			opts: []Option{
				WithAPIKey("test-key"),
				WithBaseURL("https://api.example.com"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("test-model", tt.opts...)
			require.NotNil(t, m, "expected model to be created")

			o := options{}
			for _, opt := range tt.opts {
				opt(&o)
			}

			assert.Equal(t, o.APIKey, m.apiKey, "expected api key %s, got %s", o.APIKey, m.apiKey)
			assert.Equal(t, o.BaseURL, m.baseURL, "expected base url %s, got %s", o.BaseURL, m.baseURL)
		})
	}
}

// stubTool implements tool.Tool for testing purposes.
type stubTool struct{ decl *tool.Declaration }

func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }
func (s stubTool) Declaration() *tool.Declaration                { return s.decl }

// TestModel_convertMessages verifies that messages are converted to the
// openai-go request format with the expected roles and fields.
func TestModel_convertMessages(t *testing.T) {
	m := New("dummy-model")

	// Prepare test messages covering all branches.
	msgs := []model.Message{
		model.NewSystemMessage("system content"),
		model.NewUserMessage("user content"),
		{
			Role:    model.RoleAssistant,
			Content: "assistant content",
			ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "hello",
					Arguments: []byte("{\"a\":1}"),
				},
			}},
		},
		{
			Role:    model.RoleTool,
			Content: "tool response",
			ToolID:  "call-1",
		},
		{
			Role:    "unknown",
			Content: "fallback content",
		},
	}

	converted := m.convertMessages(msgs)
	require.Len(t, converted, len(msgs), "converted len=%d want=%d", len(converted), len(msgs))

	roleChecks := []func(openaigo.ChatCompletionMessageParamUnion) bool{
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfSystem != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfUser != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfAssistant != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfTool != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfUser != nil },
	}

	for i, u := range converted {
		require.Truef(t, roleChecks[i](u), "index %d: expected role variant not set", i)
	}

	// Assert that assistant message contains tool calls after conversion.
	assistantUnion := converted[2]
	require.NotNil(t, assistantUnion.OfAssistant, "assistant union is nil")
	require.NotEmpty(t, assistantUnion.GetToolCalls(), "assistant message should contain tool calls")
}

// TestModel_convertTools ensures that tool declarations are mapped to the
// expected OpenAI function definitions.
func TestModel_convertTools(t *testing.T) {
	m := New("dummy")

	const toolName = "test_tool"
	const toolDesc = "test description"

	schema := &tool.Schema{Type: "object"}

	toolsMap := map[string]tool.Tool{
		toolName: stubTool{decl: &tool.Declaration{
			Name:        toolName,
			Description: toolDesc,
			InputSchema: schema,
		}},
	}

	params := m.convertTools(toolsMap)
	require.Len(t, params, 1, "convertTools len=%d want=%d", len(params), 1)

	fn := params[0].Function
	assert.Equal(t, toolName, fn.Name, "function name=%s want=%s", fn.Name, toolName)
	require.True(t, fn.Description.Valid() && fn.Description.Value == toolDesc, "function description mismatch")

	require.False(t, reflect.ValueOf(fn.Parameters).IsZero(), "expected parameters to be populated from schema")
}

// TestModel_Callbacks tests that callback functions are properly called with
// the correct parameters including the request parameter.
func TestModel_Callbacks(t *testing.T) {
	// Skip this test if no API key is provided.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	t.Run("chat request callback", func(t *testing.T) {
		var capturedRequest *openaigo.ChatCompletionNewParams
		var capturedCtx context.Context
		callbackCalled := make(chan struct{})

		requestCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			capturedCtx = ctx
			capturedRequest = req
			close(callbackCalled)
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatRequestCallback(requestCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: false,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Wait for callback to be called.
		select {
		case <-callbackCalled:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for request callback")
		}

		// Consume responses.
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Verify the callback was called with correct parameters.
		require.NotNil(t, capturedCtx, "expected context to be captured in callback")
		require.NotNil(t, capturedRequest, "expected request to be captured in callback")
		assert.Equal(t, "gpt-3.5-turbo", capturedRequest.Model, "expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
	})

	t.Run("chat response callback", func(t *testing.T) {
		var capturedRequest *openaigo.ChatCompletionNewParams
		var capturedResponse *openaigo.ChatCompletion
		var capturedCtx context.Context
		callbackCalled := make(chan struct{})

		responseCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			capturedCtx = ctx
			capturedRequest = req
			capturedResponse = resp
			close(callbackCalled)
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatResponseCallback(responseCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: false,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Consume responses to trigger the callback.
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Wait for callback to be called.
		select {
		case <-callbackCalled:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for response callback")
		}

		// Verify the callback was called with correct parameters.
		require.NotNil(t, capturedCtx, "expected context to be captured in callback")
		require.NotNil(t, capturedRequest, "expected request to be captured in callback")
		// Note: capturedResponse might be nil if there was an API error.
		// We only check if it's not nil when we expect a successful response.
		assert.Equal(t, "gpt-3.5-turbo", capturedRequest.Model, "expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
		// Only check response model if we got a successful response.
		// Note: OpenAI now returns gpt-3.5-turbo-1106 when requesting gpt-3.5-turbo.
		if capturedResponse != nil {
			assert.Equal(t, "gpt-3.5-turbo-1106", capturedResponse.Model, "expected response model %s, got %s", "gpt-3.5-turbo-1106", capturedResponse.Model)
		}
	})

	t.Run("chat chunk callback", func(t *testing.T) {
		var capturedRequest *openaigo.ChatCompletionNewParams
		var capturedChunk *openaigo.ChatCompletionChunk
		var capturedCtx context.Context
		chunkCount := 0
		callbackCalled := make(chan struct{})

		chunkCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, chunk *openaigo.ChatCompletionChunk) {
			capturedCtx = ctx
			capturedRequest = req
			capturedChunk = chunk
			chunkCount++
			if chunkCount == 1 {
				close(callbackCalled)
			}
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatChunkCallback(chunkCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Wait for callback to be called or for responses to complete.
		select {
		case <-callbackCalled:
			// Callback was called, consume remaining responses.
			for response := range responseChan {
				if response.Done {
					break
				}
			}
		case <-time.After(15 * time.Second):
			// Timeout - consume responses anyway to avoid blocking.
			t.Log("timeout waiting for chunk callback, consuming responses")
			for response := range responseChan {
				if response.Done {
					break
				}
			}
			// Don't fail the test if callback wasn't called - it might be due to API issues.
			t.Skip("skipping chunk callback test due to timeout - API might be slow or failing")
		}

		// Verify the callback was called with correct parameters (if it was called).
		if capturedCtx != nil {
			require.NotNil(t, capturedRequest, "expected request to be captured in callback")
			require.NotNil(t, capturedChunk, "expected chunk to be captured in callback")
			assert.Equal(t, "gpt-3.5-turbo", capturedRequest.Model, "expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
			require.Greater(t, chunkCount, 0, "expected chunk callback to be called at least once")
		}
	})

	t.Run("chat stream complete callback", func(t *testing.T) {
		var capturedRequest *openaigo.ChatCompletionNewParams
		var capturedAccumulator *openaigo.ChatCompletionAccumulator
		var capturedStreamErr error
		var capturedCtx context.Context
		callbackCalled := make(chan struct{})

		streamCompleteCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, acc *openaigo.ChatCompletionAccumulator, streamErr error) {
			capturedCtx = ctx
			capturedRequest = req
			capturedAccumulator = acc
			capturedStreamErr = streamErr
			close(callbackCalled)
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatStreamCompleteCallback(streamCompleteCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("hello"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: true, // Enable streaming for this test
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Consume all responses
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Wait for callback with timeout
		select {
		case <-callbackCalled:
			// Success - callback was called
		case <-time.After(30 * time.Second):
			// Timeout - this is expected when API key is invalid
			t.Skip("skipping stream complete callback test due to timeout - API might be slow or failing")
		}

		// Verify the callback was called with correct parameters (if it was called)
		if capturedCtx != nil {
			require.NotNil(t, capturedRequest, "expected request to be captured in callback")
			assert.Equal(t, "gpt-3.5-turbo", capturedRequest.Model, "expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
			// Either accumulator should be non-nil (success) or streamErr should be non-nil (failure)
			require.True(t, capturedAccumulator != nil || capturedStreamErr != nil, "expected either accumulator or streamErr to be set")
		}
	})

	t.Run("multiple callbacks", func(t *testing.T) {
		requestCalled := false
		responseCalled := false
		chunkCalled := false
		requestCallbackDone := make(chan struct{})
		responseCallbackDone := make(chan struct{})

		requestCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			requestCalled = true
			close(requestCallbackDone)
		}

		responseCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			responseCalled = true
			close(responseCallbackDone)
		}

		chunkCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, chunk *openaigo.ChatCompletionChunk) {
			chunkCalled = true
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatRequestCallback(requestCallback),
			WithChatResponseCallback(responseCallback),
			WithChatChunkCallback(chunkCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: false,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Wait for request callback.
		select {
		case <-requestCallbackDone:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for request callback")
		}

		// Consume responses to trigger the response callback.
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Wait for response callback.
		select {
		case <-responseCallbackDone:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for response callback")
		}

		// Verify appropriate callbacks were called.
		assert.True(t, requestCalled, "expected request callback to be called")
		assert.True(t, responseCalled, "expected response callback to be called")
		// Chunk callback should not be called for non-streaming requests.
		assert.False(t, chunkCalled, "expected chunk callback not to be called for non-streaming requests")
	})

	t.Run("nil callbacks", func(t *testing.T) {
		// Test that the model works correctly when callbacks are nil.
		m := New("gpt-3.5-turbo", WithAPIKey(apiKey))

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: false,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Should not panic when callbacks are nil.
		for response := range responseChan {
			if response.Done {
				break
			}
		}
	})
}

// TestModel_CallbackParameters verifies that callback functions receive the
// correct parameter types and values.
func TestModel_CallbackParameters(t *testing.T) {
	// Skip this test if no API key is provided.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	t.Run("callback parameter types", func(t *testing.T) {
		var requestParam *openaigo.ChatCompletionNewParams
		var responseParam *openaigo.ChatCompletion
		var chunkParam *openaigo.ChatCompletionChunk
		requestCallbackDone := make(chan struct{})
		responseCallbackDone := make(chan struct{})

		requestCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			requestParam = req
			close(requestCallbackDone)
		}

		responseCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			responseParam = resp
			close(responseCallbackDone)
		}

		chunkCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, chunk *openaigo.ChatCompletionChunk) {
			chunkParam = chunk
		}

		m := New("gpt-3.5-turbo",
			WithAPIKey(apiKey),
			WithChatRequestCallback(requestCallback),
			WithChatResponseCallback(responseCallback),
			WithChatChunkCallback(chunkCallback),
		)

		ctx := context.Background()
		request := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test message"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: false,
			},
		}

		responseChan, err := m.GenerateContent(ctx, request)
		require.NoError(t, err, "failed to generate content: %v", err)

		// Wait for request callback.
		select {
		case <-requestCallbackDone:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for request callback")
		}

		// Consume responses to trigger the response callback.
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Wait for response callback.
		select {
		case <-responseCallbackDone:
		case <-time.After(10 * time.Second):
			require.Fail(t, "timeout waiting for response callback")
		}

		// Verify parameter types are correct.
		require.NotNil(t, requestParam, "expected request parameter to be set")
		assert.Equal(t, reflect.TypeOf(&openaigo.ChatCompletionNewParams{}), reflect.TypeOf(requestParam), "expected request parameter type %T, got %T", &openaigo.ChatCompletionNewParams{}, requestParam)

		// Note: responseParam might be nil if there was an API error.
		// We only check the type if we got a successful response.
		if responseParam != nil {
			assert.Equal(t, reflect.TypeOf(&openaigo.ChatCompletion{}), reflect.TypeOf(responseParam), "expected response parameter type %T, got %T", &openaigo.ChatCompletion{}, responseParam)
		}

		// Chunk parameter should be nil for non-streaming requests.
		assert.Nil(t, chunkParam, "expected chunk parameter to be nil for non-streaming requests")
	})
}

// TestModel_CallbackAssignment tests that callback functions are properly
// assigned to the model during creation.
func TestModel_CallbackAssignment(t *testing.T) {
	t.Run("callback assignment", func(t *testing.T) {
		requestCalled := false
		responseCalled := false
		chunkCalled := false

		requestCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			requestCalled = true
		}

		responseCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			responseCalled = true
		}

		chunkCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, chunk *openaigo.ChatCompletionChunk) {
			chunkCalled = true
		}

		m := New("test-model",
			WithAPIKey("test-key"),
			WithChatRequestCallback(requestCallback),
			WithChatResponseCallback(responseCallback),
			WithChatChunkCallback(chunkCallback),
		)

		// Verify that callbacks are assigned.
		assert.NotNil(t, m.chatRequestCallback, "expected chat request callback to be assigned")
		assert.NotNil(t, m.chatResponseCallback, "expected chat response callback to be assigned")
		assert.NotNil(t, m.chatChunkCallback, "expected chat chunk callback to be assigned")

		// Test that callbacks can be called without panicking.
		ctx := context.Background()
		req := &openaigo.ChatCompletionNewParams{
			Model: "test-model",
		}

		// Test request callback.
		m.chatRequestCallback(ctx, req)
		assert.True(t, requestCalled, "expected request callback to be called")

		// Test response callback.
		resp := &openaigo.ChatCompletion{
			Model: "test-model",
		}
		m.chatResponseCallback(ctx, req, resp)
		assert.True(t, responseCalled, "expected response callback to be called")

		// Test chunk callback.
		chunk := &openaigo.ChatCompletionChunk{
			Model: "test-model",
		}
		m.chatChunkCallback(ctx, req, chunk)
		assert.True(t, chunkCalled, "expected chunk callback to be called")
	})

	t.Run("nil callback assignment", func(t *testing.T) {
		m := New("test-model", WithAPIKey("test-key"))

		// Verify that callbacks are nil when not provided.
		assert.Nil(t, m.chatRequestCallback, "expected chat request callback to be nil")
		assert.Nil(t, m.chatResponseCallback, "expected chat response callback to be nil")
		assert.Nil(t, m.chatChunkCallback, "expected chat chunk callback to be nil")
	})
}

// testStubCounter implements model.TokenCounter for unit tests.
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

// testStubStrategy implements model.TailoringStrategy for unit tests.
type testStubStrategy struct{}

func (testStubStrategy) TailorMessages(ctx context.Context, messages []model.Message, maxTokens int) ([]model.Message, error) {
	if len(messages) <= 1 {
		return messages, nil
	}
	// Drop the second message to make tailoring observable.
	return append([]model.Message{messages[0]}, messages[2:]...), nil
}

// TestWithTokenTailoring ensures messages are tailored before request is built.
func TestWithTokenTailoring(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("test-model",
		WithEnableTokenTailoring(true), // Enable token tailoring.
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Two user messages; strategy will drop the second one.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key, we just consume.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, OpenAI messages should be 1 user message (system omitted in this test).
	require.Len(t, captured.Messages, 1, "expected 1 message after tailoring, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_SimpleMode tests the simple mode of token tailoring.
func TestWithEnableTokenTailoring_SimpleMode(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("gpt-4o-mini", // Known model with 200000 context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger tailoring.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 100; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 100))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key, we just consume.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be reduced.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestWithEnableTokenTailoring_AdvancedMode tests the advanced mode with custom parameters.
func TestWithEnableTokenTailoring_AdvancedMode(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("gpt-4o-mini",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(1000), // Custom max input tokens
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Two user messages; strategy will drop the second one.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key, we just consume.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, OpenAI messages should be 1 user message.
	require.Len(t, captured.Messages, 1, "expected 1 message after tailoring, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_Disabled tests that token tailoring is disabled when flag is false.
func TestWithEnableTokenTailoring_Disabled(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("gpt-4o-mini",
		WithEnableTokenTailoring(false),
		WithMaxInputTokens(100), // This should be ignored when tailoring is disabled
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Two user messages; should NOT be tailored when disabled.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key, we just consume.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring disabled, OpenAI messages should be unchanged (2 messages).
	require.Len(t, captured.Messages, 2, "expected 2 messages when tailoring disabled, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_UnknownModel tests behavior with unknown model.
func TestWithEnableTokenTailoring_UnknownModel(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("unknown-model-xyz", // Unknown model should fallback to default context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger tailoring.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 50; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 50))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key, we just consume.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be reduced even with unknown model.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored with unknown model, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestModel_CallbackSignature tests that the callback function signatures
// match the expected types from the diff.
func TestModel_CallbackSignature(t *testing.T) {
	t.Run("callback signature verification", func(t *testing.T) {
		// Test that we can create callback functions with the correct signatures.
		var requestCallback ChatRequestCallbackFunc
		var responseCallback ChatResponseCallbackFunc
		var chunkCallback ChatChunkCallbackFunc

		// These assignments should compile without errors.
		requestCallback = func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			// Callback implementation.
		}

		responseCallback = func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			// Callback implementation.
		}

		chunkCallback = func(ctx context.Context, req *openaigo.ChatCompletionNewParams, chunk *openaigo.ChatCompletionChunk) {
			// Callback implementation.
		}

		// Verify that the callbacks can be called with the correct parameters.
		ctx := context.Background()
		req := &openaigo.ChatCompletionNewParams{
			Model: "test-model",
		}
		resp := &openaigo.ChatCompletion{
			Model: "test-model",
		}
		chunk := &openaigo.ChatCompletionChunk{
			Model: "test-model",
		}

		// These calls should not panic.
		requestCallback(ctx, req)
		responseCallback(ctx, req, resp)
		chunkCallback(ctx, req, chunk)

		// Test passes if no panic occurs.
	})
}

// TestWithOpenAIOptions tests the WithOpenAIOptions functionality.
func TestWithOpenAIOptions(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		opts        []Option
		expectError bool
	}{
		{
			name:      "with middleware option",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey("test-key"),
				WithOpenAIOptions(
					openaiopt.WithMiddleware(
						func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
							// Test middleware that adds a custom header.
							req.Header.Set("X-Test-Middleware", "test-value")
							return next(req)
						},
					),
				),
			},
			expectError: false,
		},
		{
			name:      "with multiple openai options",
			modelName: "gpt-4",
			opts: []Option{
				WithAPIKey("test-key"),
				WithOpenAIOptions(
					openaiopt.WithHeader("User-Agent", "test-user-agent"),
					openaiopt.WithMaxRetries(3),
				),
			},
			expectError: false,
		},
		{
			name:      "with empty openai options",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey("test-key"),
				WithOpenAIOptions(),
			},
			expectError: false,
		},
		{
			name:      "with openai options and other options",
			modelName: "custom-model",
			opts: []Option{
				WithAPIKey("test-key"),
				WithBaseURL("https://api.custom.com"),
				WithChannelBufferSize(512),
				WithOpenAIOptions(
					openaiopt.WithMaxRetries(5),
				),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.modelName, tt.opts...)
			require.NotNil(t, m, "expected model to be created, got nil")

			// Verify that the model was created with the expected name.
			assert.Equal(t, tt.modelName, m.name, "expected model name %s, got %s", tt.modelName, m.name)

			// Verify that the model was created successfully.
			assert.Equal(t, tt.modelName, m.name, "expected model name %s, got %s", tt.modelName, m.name)
		})
	}
}

// TestWithOpenAIOptions_OptionsStruct tests that the options struct correctly
// stores and applies OpenAI options.
func TestWithOpenAIOptions_OptionsStruct(t *testing.T) {
	// Create options with OpenAI options.
	o := &options{}

	// Apply WithOpenAIOptions.
	WithOpenAIOptions(
		openaiopt.WithMaxRetries(3),
		openaiopt.WithHeader("User-Agent", "test-agent"),
	)(o)

	// Verify that OpenAI options were stored.
	assert.Len(t, o.OpenAIOptions, 2, "expected 2 OpenAI options, got %d", len(o.OpenAIOptions))

	// Apply additional options.
	WithOpenAIOptions(
		openaiopt.WithRequestTimeout(30 * time.Second),
	)(o)

	// Verify that options were appended.
	assert.Len(t, o.OpenAIOptions, 3, "expected 3 OpenAI options after append, got %d", len(o.OpenAIOptions))
}

// TestWithOpenAIOptions_Integration tests that OpenAI options are properly
// integrated into the client creation process.
func TestWithOpenAIOptions_Integration(t *testing.T) {
	// Create a model with OpenAI options.
	m := New("test-model",
		WithAPIKey("test-key"),
		WithOpenAIOptions(
			openaiopt.WithMaxRetries(5),
			openaiopt.WithHeader("User-Agent", "integration-test"),
		),
	)

	require.NotNil(t, m, "expected model to be created")

	// Verify that the model can be used to create a request.
	ctx := context.Background()
	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("test message"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	// This should not panic and should create a response channel.
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err, "failed to create request: %v", err)

	// Verify that the response channel was created.
	require.NotNil(t, responseChan, "expected response channel to be created")

	// Clean up by consuming the channel.
	go func() {
		for range responseChan {
			// Consume all responses.
		}
	}()
}

// TestWithOpenAIOptions_Middleware tests the middleware functionality
// specifically.
func TestWithOpenAIOptions_Middleware(t *testing.T) {
	middlewareCalled := false

	// Create a model with middleware that tracks calls.
	m := New("test-model",
		WithAPIKey("test-key"),
		WithOpenAIOptions(
			openaiopt.WithMiddleware(
				func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
					middlewareCalled = true
					// Add a test header to verify middleware is working.
					req.Header.Set("X-Middleware-Test", "test-value")
					return next(req)
				},
			),
		),
	)

	require.NotNil(t, m, "expected model to be created")

	// Create a request to trigger the middleware.
	ctx := context.Background()
	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("test message"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	// This will likely fail due to invalid API key, but middleware should be called.
	responseChan, err := m.GenerateContent(ctx, request)
	require.NoError(t, err, "failed to create request: %v", err)

	// Consume one response to trigger the middleware.
	select {
	case <-responseChan:
		// Middleware should have been called during the request.
		assert.True(t, middlewareCalled, "expected middleware to be called")
	case <-time.After(5 * time.Second):
		t.Log("Request timed out as expected with test credentials")
	}
}

// TestWithOpenAIOptions_CombinedOptions tests that OpenAI options work
// correctly when combined with other options.
func TestWithOpenAIOptions_CombinedOptions(t *testing.T) {
	// Test with all available options.
	m := New("test-model",
		WithAPIKey("test-key"),
		WithBaseURL("https://api.example.com"),
		WithChannelBufferSize(1024),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			// Test callback.
		}),
		WithChatResponseCallback(func(
			ctx context.Context,
			req *openaigo.ChatCompletionNewParams,
			resp *openaigo.ChatCompletion,
		) {
			// Test callback.
		}),
		WithChatChunkCallback(func(
			ctx context.Context,
			req *openaigo.ChatCompletionNewParams,
			chunk *openaigo.ChatCompletionChunk,
		) {
			// Test callback.
		}),
		WithHTTPClientOptions(
			WithHTTPClientName("test-client"),
		),
		WithOpenAIOptions(
			openaiopt.WithMaxRetries(3),
			openaiopt.WithHeader("User-Agent", "combined-test"),
		),
	)

	require.NotNilf(t, m, "expected model to be created")

	// Verify that all options were applied correctly.
	assert.Equal(t, "test-model", m.name, "expected model name 'test-model', got %s", m.name)
	assert.Equal(t, "test-key", m.apiKey, "expected API key 'test-key', got %s", m.apiKey)
	assert.Equal(t, "https://api.example.com", m.baseURL, "expected base URL 'https://api.example.com', got %s", m.baseURL)

	assert.Equal(t, 1024, m.channelBufferSize, "expected channel buffer size 1024, got %d", m.channelBufferSize)

	// Verify that callbacks were set.
	assert.NotNil(t, m.chatRequestCallback, "expected chat request callback to be set")

	assert.NotNil(t, m.chatResponseCallback, "expected chat response callback to be set")

	assert.NotNil(t, m.chatChunkCallback, "expected chat chunk callback to be set")
}

func TestConvertSystemMessageContent(t *testing.T) {
	// Test converting message with text content parts
	textPart := model.ContentPart{
		Type: "text",
		Text: stringPtr("System instruction"),
	}

	message := model.Message{
		Role:         model.RoleSystem,
		ContentParts: []model.ContentPart{textPart},
	}

	m := &Model{}
	content := m.convertSystemMessageContent(message)

	// System messages should convert text content parts to array of content parts
	assert.Len(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))

	assert.Equal(t, "System instruction", content.OfArrayOfContentParts[0].Text, "Expected text content to be 'System instruction', got %s", content.OfArrayOfContentParts[0].Text)
}

func TestConvertUserMessageContent(t *testing.T) {
	// Test converting user message with text content parts
	textPart := model.ContentPart{
		Type: "text",
		Text: stringPtr("Hello, world!"),
	}

	message := model.Message{
		Role:         model.RoleUser,
		ContentParts: []model.ContentPart{textPart},
	}

	model := &Model{}
	content, _ := model.convertUserMessageContent(message)

	// Check that content parts are converted
	assert.Lenf(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))
	contentPart := content.OfArrayOfContentParts[0]
	assert.NotNilf(t, contentPart.OfText, "Expected text content part to be set")
	assert.Equalf(t, "Hello, world!", contentPart.OfText.Text, "Expected text content to be 'Hello, world!', got %s", contentPart.OfText.Text)
}

func TestConvertUserMessageContentWithImage(t *testing.T) {
	// Test converting user message with image content parts
	imagePart := model.ContentPart{
		Type: "image",
		Image: &model.Image{
			URL:    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
			Detail: "high",
		},
	}

	message := model.Message{
		Role:         model.RoleUser,
		ContentParts: []model.ContentPart{imagePart},
	}

	model := &Model{}
	content, _ := model.convertUserMessageContent(message)

	assert.Lenf(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))
	contentPart := content.OfArrayOfContentParts[0]
	assert.NotNilf(t, contentPart.OfImageURL, "Expected image content part to be set")
	assert.Equalf(t, "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", contentPart.OfImageURL.ImageURL.URL, "Expected image URL to match")
}

func TestConvertUserMessageContentWithAudio(t *testing.T) {
	// Test converting user message with audio content parts
	audioPart := model.ContentPart{
		Type: "audio",
		Audio: &model.Audio{
			Data:   []byte("data:audio/wav;base64,UklGRnoGAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQoGAACBhYqFbF1fdJivrJBhNjVgodDbq2EcBj+a2/LDciUFLIHO8tiJNwgZaLvt559NEAxQp+PwtmMcBjiR1/LMeSwFJHfH8N2QQAoUXrTp66hVFApGn+DyvmwhBSuBzvLZiTYIG2m98OScTgwOUarm7blmGgU7k9n1unEiBC13yO/eizEIHWq+8+OWT"),
			Format: "wav",
		},
	}

	message := model.Message{
		Role:         model.RoleUser,
		ContentParts: []model.ContentPart{audioPart},
	}

	model := &Model{}
	content, _ := model.convertUserMessageContent(message)

	assert.Lenf(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))
	contentPart := content.OfArrayOfContentParts[0]
	assert.NotNilf(t, contentPart.OfInputAudio, "Expected audio content part to be set")
}

func TestConvertUserMessageContentWithFile(t *testing.T) {
	// Test converting user message with file content parts
	filePart := model.ContentPart{
		Type: "file",
		File: &model.File{
			FileID: "file-abc123",
		},
	}

	message := model.Message{
		Role:         model.RoleUser,
		ContentParts: []model.ContentPart{filePart},
	}

	model := &Model{}
	content, _ := model.convertUserMessageContent(message)

	assert.Lenf(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))

	contentPart := content.OfArrayOfContentParts[0]
	assert.NotNilf(t, contentPart.OfFile, "Expected file content part to be set")

	assert.Equalf(t, "file-abc123", contentPart.OfFile.File.FileID.Value, "Expected file ID to be 'file-abc123', got %s", contentPart.OfFile.File.FileID.Value)
}

func TestConvertAssistantMessageContent(t *testing.T) {
	// Test converting assistant message with text content parts
	textPart := model.ContentPart{
		Type: "text",
		Text: stringPtr("I can help you with that."),
	}

	message := model.Message{
		Role:         model.RoleAssistant,
		ContentParts: []model.ContentPart{textPart},
	}

	model := &Model{}
	content := model.convertAssistantMessageContent(message)

	// Assistant messages should only support text content
	assert.Lenf(t, content.OfArrayOfContentParts, 1, "Expected 1 content part, got %d", len(content.OfArrayOfContentParts))

	contentPart := content.OfArrayOfContentParts[0]
	assert.NotNilf(t, contentPart.OfText, "Expected text content part to be set")

	assert.Equalf(t, "I can help you with that.", contentPart.OfText.Text, "Expected text to be 'I can help you with that.', got %s", contentPart.OfText.Text)
}

func TestConvertAssistantMessageContentWithNonText(t *testing.T) {
	// Test converting assistant message with non-text content parts (should be ignored)
	imagePart := model.ContentPart{
		Type: "image",
		Image: &model.Image{
			URL:    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
			Detail: "high",
		},
	}

	message := model.Message{
		Role:         model.RoleAssistant,
		ContentParts: []model.ContentPart{imagePart},
	}

	model := &Model{}
	content := model.convertAssistantMessageContent(message)

	// Assistant messages should ignore non-text content parts
	assert.Lenf(t, content.OfArrayOfContentParts, 0, "Expected 0 content parts for non-text content, got %d", len(content.OfArrayOfContentParts))
}

func TestConvertContentPart(t *testing.T) {
	m := &Model{}

	// Test text content part
	textPart := model.ContentPart{
		Type: "text",
		Text: stringPtr("Test text"),
	}

	contentPart := m.convertContentPart(textPart)
	assert.NotNilf(t, contentPart, "Expected content part to be converted")

	assert.NotNilf(t, contentPart.OfText, "Expected text content part to be set")

	assert.Equalf(t, "Test text", contentPart.OfText.Text, "Expected text to be 'Test text', got %s", contentPart.OfText.Text)

	// Test image content part
	imagePart := model.ContentPart{
		Type: "image",
		Image: &model.Image{
			URL:    "data:image/png;base64,test",
			Detail: "high",
		},
	}

	contentPart = m.convertContentPart(imagePart)
	assert.NotNilf(t, contentPart, "Expected image content part to be converted")

	assert.NotNilf(t, contentPart.OfImageURL, "Expected image content part to be set")

	// Test unknown content part type
	unknownPart := model.ContentPart{
		Type: "unknown",
	}

	contentPart = m.convertContentPart(unknownPart)
	assert.Nilf(t, contentPart, "Expected unknown content part to return nil")
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// TestModel_GenerateContent_StreamingBatchProcessing tests our handleStreamingResponse batch processing logic
func TestModel_GenerateContent_StreamingBatchProcessing(t *testing.T) {
	// Test cases covering different streaming scenarios.
	tests := []struct {
		name          string
		chunks        []string
		expectedTool  string
		expectedArgs  string
		expectedCount int
	}{
		{
			name: "Normal_Sandwich_Mode", // Normal "sandwich" mode: content does not exist in tool call chunks {0}.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,                                                                                                         // Start boundary: empty content marker
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Beijing\"}"}}]},"finish_reason":null}]}`, // Pure tool_calls chunk - content field does not exist
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,                                                                                                                    // End boundary: empty content marker
			},
			expectedTool:  "get_weather",
			expectedArgs:  `{"location":"Beijing"}`,
			expectedCount: 1,
		},
		{
			name: "Abnormal_Mixed_Mode", // other "mixed" mode: content exists in tool call chunks {3 ""}.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"index":0,"id":"call_456","type":"function","function":{"name":"calculate","arguments":"{\"expr\":\"2+2\"}"}}]},"finish_reason":null}]}`, // Field conflict: content and tool_calls simultaneously present
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
			},
			expectedTool:  "calculate",
			expectedArgs:  `{"expr":"2+2"}`,
			expectedCount: 1,
		},
		{
			name: "Multiple_ToolCalls_Sandwich", // multiple tool calls in normal mode.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,                                                                                                         // Start boundary
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_111","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Beijing\"}"}}]},"finish_reason":null}]}`, // First tool
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_222","type":"function","function":{"name":"get_time","arguments":"{\"timezone\":\"UTC\"}"}}]},"finish_reason":null}]}`,        // Second tool
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,                                                                                                                    // End boundary
			},
			expectedTool:  "get_weather", // First tool.
			expectedArgs:  `{"location":"Beijing"}`,
			expectedCount: 2,
		},
		{
			name: "Incremental_Arguments_Streaming", // incremental arguments streaming.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,                                                                                          // Start boundary
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_streaming_123","type":"function","function":{"name":"record_score","arguments":""}}]},"finish_reason":null}]}`, // Tool call starts, arguments are empty
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\""}}]},"finish_reason":null}]}`,                                                                // arguments: {"
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"feedback"}}]},"finish_reason":null}]}`,                                                           // arguments: feedback
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\":\""}}]},"finish_reason":null}]}`,                                                              // arguments: ":"
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"The analysis"}}]},"finish_reason":null}]}`,                                                       // arguments: The analysis
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" shows"}}]},"finish_reason":null}]}`,                                                             // arguments:  shows
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" good quality"}}]},"finish_reason":null}]}`,                                                      // arguments:  good quality
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\",\""}}]},"finish_reason":null}]}`,                                                              // arguments: ","
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"score"}}]},"finish_reason":null}]}`,                                                              // arguments: score
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\":85"}}]},"finish_reason":null}]}`,                                                              // arguments: ":85
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":null}]}`,                                                                  // arguments: }
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,                                                                                                     // End boundary
			},
			expectedTool:  "record_score",
			expectedArgs:  `{"feedback":"The analysis shows good quality","score":85}`,
			expectedCount: 1,
		},
		{
			name: "ToolCall_NoID_Synthesized", // first tool call lacks id entirely; expect synthesized auto_call_0.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"calc","arguments":"{\"a\":1}"}}]},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
			},
			expectedTool:  "calc",
			expectedArgs:  `{"a":1}`,
			expectedCount: 1,
		},
		{
			name: "ID_Index_Mapping_Verification", // Test ID -> Index mapping with multiple tool calls.
			chunks: []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,                                                                                       // Start boundary
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`, // First tool call starts (ID + index 0)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]},"finish_reason":null}]}`,                                                  // First tool args part 1 (no ID)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Tokyo\"}"}}]},"finish_reason":null}]}`,                                                      // First tool args part 2 (no ID)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_time_xyz","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}`,       // Second tool call starts (ID + index 1)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"timezone\":"}}]},"finish_reason":null}]}`,                                                  // Second tool args part 1 (no ID)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"UTC\"}"}}]},"finish_reason":null}]}`,                                                        // Second tool args part 2 (no ID)
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,                                                                                                  // End boundary
			},
			expectedTool:  "get_weather",
			expectedArgs:  `{"location":"Tokyo"}`,
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")

				for _, chunk := range tt.chunks {
					fmt.Fprintf(w, "%s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					time.Sleep(5 * time.Millisecond)
				}
			}))
			defer server.Close()

			// Create model with mock server.
			m := New("gpt-3.5-turbo", WithBaseURL(server.URL), WithAPIKey("test-key"))

			// Make streaming request.
			req := &model.Request{
				Messages:         []model.Message{{Role: model.RoleUser, Content: "Test"}},
				GenerationConfig: model.GenerationConfig{Stream: true},
			}

			ctx := context.Background()
			responseChan, err := m.GenerateContent(ctx, req)
			require.NoErrorf(t, err, "GenerateContent failed: %v", err)

			// Collect all responses.
			var responses []*model.Response
			for response := range responseChan {
				responses = append(responses, response)
				require.Nilf(t, response.Error, "Response error: %v", response.Error)
			}

			// Find response with tool calls.
			var toolCallResponse *model.Response
			for _, response := range responses {
				if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
					toolCallResponse = response
					break
				}
			}

			// Verify our batch processing worked.
			require.NotNilf(t, toolCallResponse, "No tool calls found - batch processing failed")

			toolCalls := toolCallResponse.Choices[0].Message.ToolCalls
			assert.Equalf(t, tt.expectedCount, len(toolCalls), "Expected %d tool calls, got %d", tt.expectedCount, len(toolCalls))

			// Verify first tool call details.
			if len(toolCalls) > 0 {
				tc := toolCalls[0]
				assert.Equalf(t, tt.expectedTool, tc.Function.Name, "Expected tool '%s', got '%s'", tt.expectedTool, tc.Function.Name)
				assert.Equalf(t, tt.expectedArgs, string(tc.Function.Arguments), "Expected args '%s', got '%s'", tt.expectedArgs, string(tc.Function.Arguments))
				// Verify Done=false for tool call responses.
				assert.Falsef(t, toolCallResponse.Done, "Tool call response should have Done=false")
			}

			// Special verification for ID_Index_Mapping_Verification test case.
			if tt.name == "ID_Index_Mapping_Verification" && len(toolCalls) >= 2 {
				// Verify that Index values are correctly preserved from the original streaming chunks.
				require.NotNilf(t, toolCalls[0].Index, "Expected first tool call index to be set")
				assert.Equalf(t, 0, *toolCalls[0].Index, "Expected first tool call index to be 0, got %v", *toolCalls[0].Index)
				require.NotNilf(t, toolCalls[1].Index, "Expected second tool call index to be set")
				assert.Equalf(t, 1, *toolCalls[1].Index, "Expected second tool call index to be 1, got %v", *toolCalls[1].Index)

				// Verify both tool calls have correct IDs and functions.
				assert.Equalf(t, "call_weather_abc", toolCalls[0].ID, "Expected first tool call ID 'call_weather_abc', got '%s'", toolCalls[0].ID)
				assert.Equalf(t, "call_time_xyz", toolCalls[1].ID, "Expected second tool call ID 'call_time_xyz', got '%s'", toolCalls[1].ID)
				assert.Equalf(t, "get_weather", toolCalls[0].Function.Name, "Expected first tool call function 'get_weather', got '%s'", toolCalls[0].Function.Name)
				assert.Equalf(t, "get_time", toolCalls[1].Function.Name, "Expected second tool call function 'get_time', got '%s'", toolCalls[1].Function.Name)

				// Verify arguments were correctly assembled despite missing IDs in data chunks.
				expectedWeatherArgs := `{"location":"Tokyo"}`
				expectedTimeArgs := `{"timezone":"UTC"}`
				assert.Equalf(t, expectedWeatherArgs, string(toolCalls[0].Function.Arguments), "Expected first tool args '%s', got '%s'", expectedWeatherArgs, string(toolCalls[0].Function.Arguments))
				assert.Equalf(t, expectedTimeArgs, string(toolCalls[1].Function.Arguments), "Expected second tool args '%s', got '%s'", expectedTimeArgs, string(toolCalls[1].Function.Arguments))
			}
		})
	}
}

func TestModel_GenerateContent_WithReasoningContent(t *testing.T) {
	// Create a mock server that returns streaming responses with reasoning_content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send response chunks with reasoning_content
		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"deepseek-chat","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"First part of reasoning"},"finish_reason":null}]}`,
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"deepseek-chat","choices":[{"index":0,"delta":{"reasoning_content":" second part of reasoning"},"finish_reason":null}]}`,
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"deepseek-chat","choices":[{"index":0,"delta":{"reasoning_content":" final part of reasoning"},"finish_reason":"stop"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()

	// Create model instance
	m := New("deepseek-chat", WithBaseURL(server.URL), WithAPIKey("test-key"))

	// Create request
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "Test with reasoning content"}},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}

	// Send request
	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, req)
	require.NoErrorf(t, err, "GenerateContent failed: %v", err)

	// Collect responses
	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		require.Nilf(t, response.Error, "Response error: %v", response.Error)
	}

	// Verify responses contain reasoning_content
	require.GreaterOrEqualf(t, len(responses), 3, "Expected at least 3 responses, got %d", len(responses))

	// Check the first response chunk
	if len(responses) > 0 && len(responses[0].Choices) > 0 {
		assert.Equalf(t, "First part of reasoning", responses[0].Choices[0].Delta.ReasoningContent, "Expected reasoning_content 'First part of reasoning', got '%s'", responses[0].Choices[0].Delta.ReasoningContent)
	}

	// Check if any responses contain reasoning_content
	foundReasoningContent := false
	for _, response := range responses {
		if len(response.Choices) > 0 && response.Choices[0].Delta.ReasoningContent != "" {
			foundReasoningContent = true
			break
		}
	}

	assert.Truef(t, foundReasoningContent, "Expected to find responses with reasoning_content, but none were found")
}

func TestModel_GenerateContent_WithReasoningContent_NonStreaming(t *testing.T) {
	// Create a mock server that returns non-streaming responses with reasoning_content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Send non-streaming response with reasoning_content
		response := `{
			"id": "test",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "deepseek-chat",
			"choices": [
				{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Final answer",
						"reasoning_content": "Complete reasoning process"
					},
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 20,
				"total_tokens": 30
			}
		}`

		fmt.Fprint(w, response)
	}))
	defer server.Close()

	// Create model instance
	m := New("deepseek-chat", WithBaseURL(server.URL), WithAPIKey("test-key"))

	// Create request (non-streaming)
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "Test with reasoning content"}},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}

	// Send request
	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, req)
	require.NoErrorf(t, err, "GenerateContent failed: %v", err)

	// Collect responses
	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		require.Nilf(t, response.Error, "Response error: %v", response.Error)
	}

	// Verify response contains reasoning_content
	require.Lenf(t, responses, 1, "Expected 1 response, got %d", len(responses))

	require.NotEmptyf(t, responses[0].Choices, "Expected at least one choice in response")

	// Check that reasoning content is present in the final message
	assert.Equalf(t, "Complete reasoning process", responses[0].Choices[0].Message.ReasoningContent, "Expected reasoning_content 'Complete reasoning process', got '%s'", responses[0].Choices[0].Message.ReasoningContent)

	// Check that content is also present
	assert.Equalf(t, "Final answer", responses[0].Choices[0].Message.Content, "Expected content 'Final answer', got '%s'", responses[0].Choices[0].Message.Content)
}

// TestModel_GenerateContent_NonStreaming_ToolCallNoID_Synthesized verifies that
// when the provider omits tool_call.id in a non-streaming response, we synthesize
// a stable ID (auto_call_<index>). This covers the non-streaming code path in
// model/openai/openai.go around mapping ChatCompletion -> model.Response.
func TestModel_GenerateContent_NonStreaming_ToolCallNoID_Synthesized(t *testing.T) {
	// Mock server returning a non-streaming chat completion with tool_calls
	// where the tool_call lacks an ID.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		response := `{
            "id": "cmpl-ns-1",
            "object": "chat.completion",
            "created": 1699200001,
            "model": "deepseek-chat",
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": "Non-streaming with tool call",
                        "tool_calls": [
                            {
                                "id": "",
                                "type": "function",
                                "function": {"name": "calc", "arguments": "{\"a\":1}"}
                            }
                        ]
                    },
                    "finish_reason": "stop"
                }
            ],
            "usage": {"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15}
        }`

		fmt.Fprint(w, response)
	}))
	defer server.Close()

	// Create model instance
	m := New("deepseek-chat", WithBaseURL(server.URL), WithAPIKey("test-key"))

	// Create non-streaming request
	req := &model.Request{
		Messages:         []model.Message{{Role: model.RoleUser, Content: "Test non streaming tool call no id"}},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}

	// Send request
	ctx := context.Background()
	responseChan, err := m.GenerateContent(ctx, req)
	require.NoErrorf(t, err, "GenerateContent failed: %v", err)

	// Collect single response
	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		if response.Done {
			break
		}
	}

	require.NotEmptyf(t, responses, "Expected at least 1 response, got %d", len(responses))
	resp := responses[0]
	require.Nilf(t, resp.Error, "Unexpected model error: %v", resp.Error)
	require.NotEmptyf(t, resp.Choices, "Expected choices in response")

	toolCalls := resp.Choices[0].Message.ToolCalls
	require.Lenf(t, toolCalls, 1, "Expected 1 tool call, got %d", len(toolCalls))
	// The adapter should synthesize an ID for the first tool call.
	assert.Equalf(t, "auto_call_0", toolCalls[0].ID, "Expected synthesized tool call ID 'auto_call_0', got '%s'", toolCalls[0].ID)
	assert.Equalf(t, "calc", toolCalls[0].Function.Name, "Expected function name 'calc', got '%s'", toolCalls[0].Function.Name)
	assert.Equalf(t, `{"a":1}`, string(toolCalls[0].Function.Arguments), "Expected function arguments '{\"a\":1}', got '%s'", string(toolCalls[0].Function.Arguments))
}

// TestFileOptions verifies the FileOptions struct and its option functions.
func TestFileOptions(t *testing.T) {
	t.Run("default_options", func(t *testing.T) {
		opts := &FileOptions{}

		assert.Empty(t, opts.Path, "expected Path to be empty, got %q", opts.Path)
		assert.Equal(t, openaigo.FilePurpose(""), opts.Purpose, "expected Purpose to be empty, got %q", opts.Purpose)
		assert.Empty(t, opts.Method, "expected Method to be empty, got %q", opts.Method)
		assert.Nil(t, opts.Body, "expected Body to be nil, got %v", opts.Body)
		assert.Empty(t, opts.BaseURL, "expected BaseURL to be empty, got %q", opts.BaseURL)
	})

	t.Run("with_path_option", func(t *testing.T) {
		opts := &FileOptions{}
		WithPath("/custom/files")(opts)

		assert.Equal(t, "/custom/files", opts.Path, "expected Path '/custom/files', got %q", opts.Path)
		assert.Equal(t, openaigo.FilePurpose(""), opts.Purpose, "expected Purpose to be empty, got %q", opts.Purpose)
		assert.Empty(t, opts.Method, "expected Method to be empty, got %q", opts.Method)
		assert.Nil(t, opts.Body, "expected Body to be nil, got %v", opts.Body)
		assert.Empty(t, opts.BaseURL, "expected BaseURL to be empty, got %q", opts.BaseURL)
	})

	t.Run("with_purpose_option", func(t *testing.T) {
		opts := &FileOptions{}
		WithPurpose(openaigo.FilePurposeBatch)(opts)

		assert.Emptyf(t, opts.Path, "expected Path to be empty, got %q", opts.Path)
		assert.Equalf(t, openaigo.FilePurposeBatch, opts.Purpose, "expected Purpose 'batch', got %q", opts.Purpose)
		assert.Emptyf(t, opts.Method, "expected Method to be empty, got %q", opts.Method)
		assert.Nilf(t, opts.Body, "expected Body to be nil, got %v", opts.Body)
		assert.Emptyf(t, opts.BaseURL, "expected BaseURL to be empty, got %q", opts.BaseURL)
	})

	t.Run("with_method_option", func(t *testing.T) {
		opts := &FileOptions{}
		WithMethod("PUT")(opts)

		assert.Empty(t, opts.Path, "expected Path to be empty, got %q", opts.Path)
		assert.Equal(t, openaigo.FilePurpose(""), opts.Purpose, "expected Purpose to be empty, got %q", opts.Purpose)
		assert.Equal(t, "PUT", opts.Method, "expected Method 'PUT', got %q", opts.Method)
		assert.Nil(t, opts.Body, "expected Body to be nil, got %v", opts.Body)
	})

	t.Run("with_body_option", func(t *testing.T) {
		opts := &FileOptions{}
		testBody := []byte("{\"test\":\"data\"}")
		WithBody(testBody)(opts)

		assert.Emptyf(t, opts.Path, "expected Path to be empty, got %q", opts.Path)
		assert.Equalf(t, openaigo.FilePurpose(""), opts.Purpose, "expected Purpose to be empty, got %q", opts.Purpose)
		assert.Emptyf(t, opts.Method, "expected Method to be empty, got %q", opts.Method)
		assert.Equalf(t, string(testBody), string(opts.Body), "expected Body %q, got %q", string(testBody), string(opts.Body))
		assert.Emptyf(t, opts.BaseURL, "expected BaseURL to be empty, got %q", opts.BaseURL)
	})

	t.Run("multiple_options", func(t *testing.T) {
		opts := &FileOptions{}
		testBody := []byte("{\"test\":\"data\"}")

		WithPath("/custom/path")(opts)
		WithPurpose(openaigo.FilePurposeUserData)(opts)
		WithMethod("POST")(opts)
		WithBody(testBody)(opts)

		assert.Equalf(t, "/custom/path", opts.Path, "expected Path '/custom/path', got %q", opts.Path)
		assert.Equalf(t, openaigo.FilePurposeUserData, opts.Purpose, "expected Purpose 'user_data', got %q", opts.Purpose)
		assert.Equalf(t, "POST", opts.Method, "expected Method 'POST', got %q", opts.Method)
		assert.Equalf(t, string(testBody), string(opts.Body), "expected Body %q, got %q", string(testBody), string(opts.Body))
	})

	t.Run("option_functions_return_functions", func(t *testing.T) {
		pathOpt := WithPath("/test")
		purposeOpt := WithPurpose(openaigo.FilePurposeBatch)
		methodOpt := WithMethod("DELETE")
		bodyOpt := WithBody([]byte("test"))
		baseURLOpt := WithFileBaseURL("http://example.com/v1")

		require.NotNilf(t, pathOpt, "expected option functions to be non-nil")
		require.NotNilf(t, purposeOpt, "expected option functions to be non-nil")
		require.NotNilf(t, methodOpt, "expected option functions to be non-nil")
		require.NotNilf(t, bodyOpt, "expected option functions to be non-nil")
		require.NotNilf(t, baseURLOpt, "expected option functions to be non-nil")

		opts := &FileOptions{}
		pathOpt(opts)
		purposeOpt(opts)
		methodOpt(opts)
		bodyOpt(opts)
		baseURLOpt(opts)

		assert.Equalf(t, "/test", opts.Path, "expected Path '/test', got %q", opts.Path)
		assert.Equalf(t, openaigo.FilePurposeBatch, opts.Purpose, "expected Purpose 'batch', got %q", opts.Purpose)
		assert.Equalf(t, "DELETE", opts.Method, "expected Method 'DELETE', got %q", opts.Method)
		assert.Equalf(t, "test", string(opts.Body), "expected Body 'test', got %q", string(opts.Body))
		assert.Equalf(t, "http://example.com/v1", opts.BaseURL, "expected BaseURL 'http://example.com/v1', got %q", opts.BaseURL)
	})
}

// TestUploadFileData_Success tests UploadFileData end-to-end with a mock server.
func TestUploadFileData_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/files") {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse", http.StatusBadRequest)
			return
		}
		vals := r.MultipartForm.Value["purpose"]
		if len(vals) == 0 || vals[0] != string(openaigo.FilePurposeBatch) {
			http.Error(w, "purpose", http.StatusBadRequest)
			return
		}
		files := r.MultipartForm.File["file"]
		if len(files) == 0 {
			http.Error(w, "file", http.StatusBadRequest)
			return
		}
		fh := files[0]
		f, err := fh.Open()
		if err != nil {
			http.Error(w, "open", http.StatusBadRequest)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "hello jsonl" {
			http.Error(w, "content", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"id\":\"file_test_1\",\"object\":\"file\",\"bytes\":%d,\"created_at\":123,\"filename\":%q,\"purpose\":%q}", len(b), fh.Filename, string(openaigo.FilePurposeBatch))
	}))
	defer server.Close()

	// Model base URL is intentionally wrong; we rely on WithFileBaseURL override.
	m := New("test-model", WithAPIKey("k"), WithBaseURL("http://wrong-base"))
	id, err := m.UploadFileData(context.Background(), "batch_input.jsonl", []byte("hello jsonl"), WithPurpose(openaigo.FilePurposeBatch), WithPath(""), WithFileBaseURL(server.URL))
	require.NoErrorf(t, err, "UploadFileData failed: %v", err)
	assert.Equalf(t, "file_test_1", id, "expected id=file_test_1, got %s", id)
}

// TestUploadFile_Success tests UploadFile with a temp file and mock server.
func TestUploadFile_Success(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "batch_input_*.jsonl")
	require.NoErrorf(t, err, "temp: %v", err)
	defer tmp.Close()
	_, err = tmp.WriteString("hello jsonl")
	require.NoErrorf(t, err, "write: %v", err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/files") {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "parse", http.StatusBadRequest)
			return
		}
		files := r.MultipartForm.File["file"]
		if len(files) == 0 {
			http.Error(w, "file", http.StatusBadRequest)
			return
		}
		fh := files[0]
		f, _ := fh.Open()
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "hello jsonl" {
			http.Error(w, "content", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"id\":\"file_test_2\",\"object\":\"file\",\"bytes\":%d,\"created_at\":123,\"filename\":%q,\"purpose\":%q}", len(b), fh.Filename, string(openaigo.FilePurposeBatch))
	}))
	defer server.Close()

	m := New("test-model", WithAPIKey("k"), WithBaseURL(server.URL))
	id, err := m.UploadFile(context.Background(), tmp.Name(), WithPurpose(openaigo.FilePurposeBatch), WithPath(""), WithFileBaseURL(server.URL))
	require.NoErrorf(t, err, "UploadFile failed: %v", err)
	assert.Equalf(t, "file_test_2", id, "expected id=file_test_2, got %s", id)
}

// TestGetFile_Success tests GetFile using a mock server.
func TestGetFile_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any path and method; return a valid file object JSON.
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_x","object":"file","bytes":10,"created_at":1,"filename":"a.jsonl","purpose":"batch"}`)
	}))
	defer server.Close()

	m := New("test-model", WithAPIKey("k"), WithBaseURL(server.URL))
	obj, err := m.GetFile(context.Background(), "file_x")
	require.NoErrorf(t, err, "GetFile failed: %v", err)
	require.NotNilf(t, obj, "unexpected file object: %#v", obj)
	assert.Equalf(t, "file_x", obj.ID, "unexpected file object: %#v", obj)
}

// TestDeleteFile_Success tests DeleteFile using a mock server.
func TestDeleteFile_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any path and method; return a valid deletion JSON.
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_z","object":"file","deleted":true}`)
	}))
	defer server.Close()

	m := New("test-model", WithAPIKey("k"), WithBaseURL(server.URL))
	err := m.DeleteFile(context.Background(), "file_z")
	require.NoErrorf(t, err, "DeleteFile failed: %v", err)
}

// TestModel_GenerateContent_Streaming_FinalReasoningAggregated
// ensures streaming reasoning deltas are aggregated into final response.Message.ReasoningContent.
func TestModel_GenerateContent_Streaming_FinalReasoningAggregated(t *testing.T) {
	// Mock SSE server that emits reasoning_content only in deltas (accumulator won't retain it)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		chunks := []string{
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"First"},"finish_reason":null}]}`,
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200001,"model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":" second"},"finish_reason":null}]}`,
			`data: {"id":"test","object":"chat.completion.chunk","created":1699200002,"model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":" final"},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()

	m := New("deepseek-reasoner", WithBaseURL(server.URL), WithAPIKey("test-key"))

	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Test streaming reasoning aggregation"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	ctx := context.Background()
	ch, err := m.GenerateContent(ctx, req)
	require.NoErrorf(t, err, "GenerateContent error: %v", err)

	var final *model.Response
	var partials int
	for rsp := range ch {
		require.Nilf(t, rsp.Error, "response error: %v", rsp.Error)
		if rsp.IsPartial {
			partials++
			continue
		}
		// capture final
		if !rsp.IsPartial {
			final = rsp
		}
	}

	require.Greaterf(t, partials, 0, "expected at least one partial delta with reasoning_content")
	require.NotNilf(t, final, "expected a final response")
	require.NotEmptyf(t, final.Choices, "expected choices in final response")
	got := final.Choices[0].Message.ReasoningContent
	want := "First second final"
	assert.Equalf(t, want, got, "final ReasoningContent mismatch: got %q want %q", got, want)
	assert.Truef(t, final.Done, "expected final.Done == true")
	assert.Falsef(t, final.IsPartial, "expected final.IsPartial == false")
}

func TestOpenAI_TokenCounterInitialization(t *testing.T) {
	tests := []struct {
		name                 string
		initialTokenCounter  model.TokenCounter
		expectedTokenCounter bool // true if should be set, false if should be nil initially
	}{
		{
			name:                 "with user provided token counter",
			initialTokenCounter:  model.NewSimpleTokenCounter(),
			expectedTokenCounter: true,
		},
		{
			name:                 "without user provided token counter",
			initialTokenCounter:  nil,
			expectedTokenCounter: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create OpenAI client
			client := &Model{
				name:                 "gpt-3.5-turbo",
				apiKey:               "test-api-key",
				enableTokenTailoring: true, // Enable token tailoring to trigger initialization
				tokenCounter:         tt.initialTokenCounter,
			}

			// Create a simple conversation to trigger the initialization logic
			conv := []model.Message{
				model.NewUserMessage("test message"),
			}

			// Create a mock HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := `{
					"id": "chatcmpl-123",
					"object": "chat.completion",
					"created": 1677652288,
					"model": "gpt-3.5-turbo",
					"choices": [{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "test response"
						},
						"finish_reason": "stop"
					}],
					"usage": {
						"prompt_tokens": 10,
						"completion_tokens": 5,
						"total_tokens": 15
					}
				}`
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, response)
			}))
			defer server.Close()

			client.baseURL = server.URL
			client.client = openai.NewClient(openaiopt.WithBaseURL(server.URL), openaiopt.WithAPIKey("test-key"))

			// Create request to trigger initialization
			request := &model.Request{
				Messages: conv,
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			}

			// Apply token tailoring to trigger initialization
			client.applyTokenTailoring(context.Background(), request)

			// Verify token counter initialization
			if tt.expectedTokenCounter {
				assert.Equal(t, tt.initialTokenCounter, client.tokenCounter, "expected tokenCounter to remain as provided, got different instance")
			} else {
				require.NotNil(t, client.tokenCounter, "expected tokenCounter to be initialized with default, got nil")
				// Should be initialized with default SimpleTokenCounter
				assert.IsType(t, &model.SimpleTokenCounter{}, client.tokenCounter, "expected SimpleTokenCounter, got %T", client.tokenCounter)
			}
		})
	}
}

func TestOpenAI_TailoringStrategyInitialization(t *testing.T) {
	tests := []struct {
		name                      string
		initialTailoringStrategy  model.TailoringStrategy
		expectedTailoringStrategy bool // true if should be set, false if should be nil initially
	}{
		{
			name:                      "with user provided tailoring strategy",
			initialTailoringStrategy:  model.NewMiddleOutStrategy(model.NewSimpleTokenCounter()),
			expectedTailoringStrategy: true,
		},
		{
			name:                      "without user provided tailoring strategy",
			initialTailoringStrategy:  nil,
			expectedTailoringStrategy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create OpenAI client
			client := &Model{
				name:                 "gpt-3.5-turbo",
				apiKey:               "test-api-key",
				enableTokenTailoring: true, // Enable token tailoring to trigger initialization
				tailoringStrategy:    tt.initialTailoringStrategy,
			}

			// Create a simple conversation to trigger the initialization logic
			conv := []model.Message{
				model.NewUserMessage("test message"),
			}

			// Create a mock HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := `{
					"id": "chatcmpl-123",
					"object": "chat.completion",
					"created": 1677652288,
					"model": "gpt-3.5-turbo",
					"choices": [{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "test response"
						},
						"finish_reason": "stop"
					}],
					"usage": {
						"prompt_tokens": 10,
						"completion_tokens": 5,
						"total_tokens": 15
					}
				}`
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, response)
			}))
			defer server.Close()

			client.baseURL = server.URL
			client.client = openai.NewClient(openaiopt.WithBaseURL(server.URL), openaiopt.WithAPIKey("test-key"))

			// Create request to trigger initialization
			request := &model.Request{
				Messages: conv,
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			}

			// Apply token tailoring to trigger initialization
			client.applyTokenTailoring(context.Background(), request)

			// Verify tailoring strategy initialization
			if tt.expectedTailoringStrategy {
				assert.Equal(t, tt.initialTailoringStrategy, client.tailoringStrategy, "expected tailoringStrategy to remain as provided, got different instance")
			} else {
				require.NotNil(t, client.tailoringStrategy, "expected tailoringStrategy to be initialized with default, got nil")
				// Should be initialized with default MiddleOutStrategy
				assert.IsType(t, &model.MiddleOutStrategy{}, client.tailoringStrategy, "expected MiddleOutStrategy, got %T", client.tailoringStrategy)
			}
		})
	}
}

func TestOpenAI_ConcurrentInitialization(t *testing.T) {
	// Test concurrent access to ensure sync.Once works correctly
	client := &Model{
		name:                 "gpt-3.5-turbo",
		apiKey:               "test-api-key",
		enableTokenTailoring: true, // Enable token tailoring to trigger initialization
		// Don't set tokenCounter or tailoringStrategy to test default initialization
	}

	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
			"id": "chatcmpl-123",
			"object": "chat.completion",
			"created": 1677652288,
			"model": "gpt-3.5-turbo",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "test response"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer server.Close()

	client.baseURL = server.URL
	client.client = openai.NewClient(openaiopt.WithBaseURL(server.URL), openaiopt.WithAPIKey("test-key"))

	// Run multiple goroutines concurrently
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			conv := []model.Message{
				model.NewUserMessage("test message"),
			}
			request := &model.Request{
				Messages: conv,
				GenerationConfig: model.GenerationConfig{
					Stream: false,
				},
			}
			client.applyTokenTailoring(context.Background(), request)
		}()
	}

	wg.Wait()

	// Verify that initialization happened only once and both are set
	assert.NotNilf(t, client.tokenCounter, "expected tokenCounter to be initialized")
	assert.NotNilf(t, client.tailoringStrategy, "expected tailoringStrategy to be initialized")
	assert.IsType(t, &model.SimpleTokenCounter{}, client.tokenCounter, "expected SimpleTokenCounter, got %T", client.tokenCounter)
	assert.IsType(t, &model.MiddleOutStrategy{}, client.tailoringStrategy, "expected MiddleOutStrategy, got %T", client.tailoringStrategy)
}

func TestOpenAI_InitializationPriority(t *testing.T) {
	// Test that user-provided components take priority over defaults
	userTokenCounter := model.NewSimpleTokenCounter()
	userTailoringStrategy := model.NewMiddleOutStrategy(userTokenCounter)

	client := &Model{
		name:                 "gpt-3.5-turbo",
		apiKey:               "test-api-key",
		enableTokenTailoring: true, // Enable token tailoring to trigger initialization
		tokenCounter:         userTokenCounter,
		tailoringStrategy:    userTailoringStrategy,
	}

	// Create request
	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("test message"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	// Apply token tailoring
	client.applyTokenTailoring(context.Background(), request)

	// Verify that user-provided components are preserved
	assert.Equalf(t, userTokenCounter, client.tokenCounter, "expected user-provided tokenCounter to be preserved")
	assert.Equalf(t, userTailoringStrategy, client.tailoringStrategy, "expected user-provided tailoringStrategy to be preserved")
}
