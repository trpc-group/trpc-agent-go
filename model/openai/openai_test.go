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
	"github.com/openai/openai-go/packages/respjson"
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
	var testKey = "test-key"
	t.Setenv(deepSeekAPIKeyName, testKey)
	tests := []struct {
		name       string
		modelName  string
		opts       []Option
		expectOpts []Option
	}{
		{
			name:      "valid openai model",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey(testKey),
			},
		},
		{
			name:      "valid model with base url",
			modelName: "custom-model",
			opts: []Option{
				WithAPIKey(testKey),
				WithBaseURL("https://api.custom.com"),
			},
		},
		{
			name:      "empty api key",
			modelName: "gpt-3.5-turbo",
			opts: []Option{
				WithAPIKey(""),
			},
		},
		{
			name:      "variant deepseek",
			modelName: "deepseek",
			opts: []Option{
				WithVariant(VariantDeepSeek),
			},
			expectOpts: []Option{
				WithAPIKey(testKey),
				WithBaseURL(defaultDeepSeekBaseURL),
			},
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
			for _, opt := range tt.expectOpts {
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
		var capturedCtx context.Context
		callbackCalled := make(chan struct{})

		responseCallback := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, resp *openaigo.ChatCompletion) {
			capturedCtx = ctx
			capturedRequest = req
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
		// Note: Due to potential OpenAI API/SDK issues, we only verify the callback was called.
		// The actual model field validation is skipped as it may be empty due to external factors.
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
	// With gpt-4o-mini (contextWindow=200000), maxInputTokens=130000 (65% ratio).
	// Need ~500 messages * 300 tokens each = ~150000 tokens to exceed limit.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 500; i++ {
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

// TestWithHTTPClientTransport tests the WithHTTPClientTransport option.
func TestWithHTTPClientTransport(t *testing.T) {
	// Create a custom transport
	customTransport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
	}

	// Test WithHTTPClientTransport option
	clientOpt := WithHTTPClientTransport(customTransport)
	require.NotNil(t, clientOpt, "expected WithHTTPClientTransport to return non-nil option")

	// Apply the option
	httpOpts := &HTTPClientOptions{}
	WithHTTPClientTransport(customTransport)(httpOpts)

	// Verify the transport was set
	assert.Equal(t, customTransport, httpOpts.Transport, "expected transport to be set correctly")
}

// TestWithExtraFields tests the WithExtraFields option.
func TestWithExtraFields(t *testing.T) {
	t.Run("single extra fields", func(t *testing.T) {
		extraFields := map[string]any{
			"custom_field": "custom_value",
			"session_id":   "abc123",
		}

		opts := &options{}
		WithExtraFields(extraFields)(opts)

		assert.NotNil(t, opts.ExtraFields, "expected ExtraFields to be set")
		assert.Equal(t, "custom_value", opts.ExtraFields["custom_field"], "expected custom_field to be 'custom_value'")
		assert.Equal(t, "abc123", opts.ExtraFields["session_id"], "expected session_id to be 'abc123'")
	})

	t.Run("multiple extra fields", func(t *testing.T) {
		opts := &options{}

		// Apply first set of fields
		WithExtraFields(map[string]any{
			"field1": "value1",
		})(opts)

		// Apply second set of fields
		WithExtraFields(map[string]any{
			"field2": "value2",
		})(opts)

		assert.Len(t, opts.ExtraFields, 2, "expected 2 extra fields")
		assert.Equal(t, "value1", opts.ExtraFields["field1"], "expected field1 to be 'value1'")
		assert.Equal(t, "value2", opts.ExtraFields["field2"], "expected field2 to be 'value2'")
	})

	t.Run("overwrite existing field", func(t *testing.T) {
		opts := &options{}

		// Set initial value
		WithExtraFields(map[string]any{
			"field": "initial",
		})(opts)

		// Overwrite
		WithExtraFields(map[string]any{
			"field": "overwritten",
		})(opts)

		assert.Equal(t, "overwritten", opts.ExtraFields["field"], "expected field to be overwritten")
	})
}

// TestWithVariant tests the WithVariant option.
func TestWithVariant(t *testing.T) {
	t.Run("openai variant", func(t *testing.T) {
		opts := &options{}
		WithVariant(VariantOpenAI)(opts)

		assert.Equal(t, VariantOpenAI, opts.Variant, "expected variant to be VariantOpenAI")
	})

	t.Run("hunyuan variant", func(t *testing.T) {
		opts := &options{}
		WithVariant(VariantHunyuan)(opts)

		assert.Equal(t, VariantHunyuan, opts.Variant, "expected variant to be VariantHunyuan")
	})

	t.Run("variant in model creation", func(t *testing.T) {
		m := New("test-model", WithAPIKey("test-key"), WithVariant(VariantHunyuan))
		require.NotNil(t, m, "expected model to be created")

		assert.Equal(t, VariantHunyuan, m.variant, "expected model variant to be VariantHunyuan")
	})
}

// TestWithBatchCompletionWindow tests the WithBatchCompletionWindow option.
func TestWithBatchCompletionWindow(t *testing.T) {
	window := openai.BatchNewParamsCompletionWindow24h

	opts := &options{}
	WithBatchCompletionWindow(window)(opts)

	assert.Equal(t, window, opts.BatchCompletionWindow, "expected BatchCompletionWindow to be set")
}

// TestWithBatchMetadata tests the WithBatchMetadata option.
func TestWithBatchMetadata(t *testing.T) {
	metadata := map[string]string{
		"batch_id": "batch_123",
		"user_id":  "user_456",
	}

	opts := &options{}
	WithBatchMetadata(metadata)(opts)

	assert.Equal(t, metadata, opts.BatchMetadata, "expected BatchMetadata to be set")
	assert.Equal(t, "batch_123", opts.BatchMetadata["batch_id"], "expected batch_id to be 'batch_123'")
	assert.Equal(t, "user_456", opts.BatchMetadata["user_id"], "expected user_id to be 'user_456'")
}

// TestInfo tests the Info method.
func TestInfo(t *testing.T) {
	modelName := "gpt-4"
	m := New(modelName, WithAPIKey("test-key"))

	info := m.Info()

	assert.Equal(t, modelName, info.Name, "expected Info.Name to be %s, got %s", modelName, info.Name)
}

// TestConvertUserMessageContent_AllContentTypes tests all content types in user messages.
func TestConvertUserMessageContent_AllContentTypes(t *testing.T) {
	m := &Model{}

	t.Run("multiple content parts", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Hello")},
				{Type: "text", Text: stringPtr("World")},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 2, "expected 2 content parts")
	})

	t.Run("empty content parts", func(t *testing.T) {
		message := model.Message{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Empty(t, content.OfArrayOfContentParts, "expected empty content parts")
	})

	t.Run("mixed content types", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Text content")},
				{
					Type: "image",
					Image: &model.Image{
						URL:    "https://example.com/image.png",
						Detail: "high",
					},
				},
				{
					Type: "audio",
					Audio: &model.Audio{
						Data:   []byte("audio data"),
						Format: "wav",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 3, "expected 3 content parts")
	})
}

// TestBuildChatRequest_EdgeCases tests edge cases in buildChatRequest.
func TestBuildChatRequest_EdgeCases(t *testing.T) {
	m := New("gpt-3.5-turbo", WithAPIKey("test-key"))

	t.Run("empty messages", func(t *testing.T) {
		req := &model.Request{
			Messages:         []model.Message{},
			GenerationConfig: model.GenerationConfig{},
		}

		chatReq, _ := m.buildChatRequest(req)
		assert.Empty(t, chatReq.Messages, "expected empty messages")
	})

	t.Run("with all generation config options", func(t *testing.T) {
		temperature := 0.8
		maxTokens := 1000
		topP := 0.9
		frequencyPenalty := 0.5
		presencePenalty := 0.3

		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test"),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature:      &temperature,
				MaxTokens:        &maxTokens,
				TopP:             &topP,
				FrequencyPenalty: &frequencyPenalty,
				PresencePenalty:  &presencePenalty,
				Stream:           true,
			},
		}

		chatReq, _ := m.buildChatRequest(req)
		assert.Equal(t, "gpt-3.5-turbo", chatReq.Model, "expected model to be gpt-3.5-turbo")
		// Verify at least one parameter is set
		assert.NotEmpty(t, chatReq.Messages, "expected messages to be set")
	})

	t.Run("with tools", func(t *testing.T) {
		tools := map[string]tool.Tool{
			"test_tool": stubTool{
				decl: &tool.Declaration{
					Name:        "test_tool",
					Description: "A test tool",
					InputSchema: &tool.Schema{Type: "object"},
				},
			},
		}

		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("test"),
			},
			Tools:            tools,
			GenerationConfig: model.GenerationConfig{},
		}

		chatReq, _ := m.buildChatRequest(req)
		assert.NotEmpty(t, chatReq.Tools, "expected tools to be present")
	})
}

// TestConvertUserMessageContent_WithImage tests image content conversion.
func TestConvertUserMessageContent_WithImage(t *testing.T) {
	m := &Model{}

	t.Run("image with URL", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: "image",
					Image: &model.Image{
						URL:    "https://example.com/image.png",
						Detail: "high",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 1, "expected 1 content part")
		assert.NotNil(t, content.OfArrayOfContentParts[0].OfImageURL, "expected image URL part")
	})

	t.Run("image with data", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: "image",
					Image: &model.Image{
						Data:   []byte("fake image data"),
						Format: "png",
						Detail: "low",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 1, "expected 1 content part")
		assert.NotNil(t, content.OfArrayOfContentParts[0].OfImageURL, "expected image URL part")
	})
}

// TestConvertUserMessageContent_WithFile tests file content conversion.
func TestConvertUserMessageContent_WithFile(t *testing.T) {
	m := &Model{}

	t.Run("file with file ID", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: "file",
					File: &model.File{
						FileID: "file-abc123",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 1, "expected 1 content part")
		assert.NotNil(t, content.OfArrayOfContentParts[0].OfFile, "expected file part")
	})

	t.Run("file with data", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: "file",
					File: &model.File{
						Name:     "document.pdf",
						Data:     []byte("fake file data"),
						MimeType: "application/pdf",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		assert.Len(t, content.OfArrayOfContentParts, 1, "expected 1 content part")
		assert.NotNil(t, content.OfArrayOfContentParts[0].OfFile, "expected file part")
	})
}

// TestConvertTools_EmptyTools tests convertTools with empty tool map.
func TestConvertTools_EmptyTools(t *testing.T) {
	m := New("dummy")

	tools := map[string]tool.Tool{}
	params := m.convertTools(tools)

	assert.Empty(t, params, "expected empty tools params")
}

// TestConvertTools_MultipleTools tests convertTools with multiple tools.
func TestConvertTools_MultipleTools(t *testing.T) {
	m := New("dummy")

	tools := map[string]tool.Tool{
		"tool1": stubTool{decl: &tool.Declaration{
			Name:        "tool1",
			Description: "First tool",
			InputSchema: &tool.Schema{Type: "object"},
		}},
		"tool2": stubTool{decl: &tool.Declaration{
			Name:        "tool2",
			Description: "Second tool",
			InputSchema: &tool.Schema{Type: "object"},
		}},
	}

	params := m.convertTools(tools)

	assert.Len(t, params, 2, "expected 2 tools")
}

// TestWithChatStreamCompleteCallback_OptionSetting tests that the callback is properly set.
func TestWithChatStreamCompleteCallback_OptionSetting(t *testing.T) {
	callbackFunc := func(ctx context.Context, req *openaigo.ChatCompletionNewParams, acc *openaigo.ChatCompletionAccumulator, streamErr error) {
		// Callback implementation
	}

	opts := &options{}
	WithChatStreamCompleteCallback(callbackFunc)(opts)

	assert.NotNil(t, opts.ChatStreamCompleteCallback, "expected ChatStreamCompleteCallback to be set")
}

// TestWithChannelBufferSize_EdgeCases tests WithChannelBufferSize with edge cases.
func TestWithChannelBufferSize_EdgeCases(t *testing.T) {
	t.Run("zero size should use default", func(t *testing.T) {
		opts := &options{}
		WithChannelBufferSize(0)(opts)
		assert.Equal(t, defaultChannelBufferSize, opts.ChannelBufferSize, "expected default buffer size")
	})

	t.Run("negative size should use default", func(t *testing.T) {
		opts := &options{}
		WithChannelBufferSize(-10)(opts)
		assert.Equal(t, defaultChannelBufferSize, opts.ChannelBufferSize, "expected default buffer size")
	})

	t.Run("positive size should be preserved", func(t *testing.T) {
		opts := &options{}
		WithChannelBufferSize(512)(opts)
		assert.Equal(t, 512, opts.ChannelBufferSize, "expected buffer size to be 512")
	})
}

// TestConvertUserMessageContent_EdgeCases tests edge cases in user message conversion.
func TestConvertUserMessageContent_EdgeCases(t *testing.T) {
	m := &Model{}

	t.Run("message with only Content string", func(t *testing.T) {
		message := model.Message{
			Role:    model.RoleUser,
			Content: "Simple text message",
		}

		content, _ := m.convertUserMessageContent(message)
		assert.NotNil(t, content.OfString, "expected string content")
	})

	t.Run("message with nil image", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Text")},
				{Type: "image", Image: nil}, // nil image
			},
		}

		content, _ := m.convertUserMessageContent(message)
		// Should not panic and should skip nil image
		assert.NotEmpty(t, content.OfArrayOfContentParts, "expected content parts")
	})

	t.Run("message with unknown content type", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Text")},
				{Type: "unknown_type"}, // unknown type
			},
		}

		content, _ := m.convertUserMessageContent(message)
		// Should handle unknown types gracefully
		assert.NotEmpty(t, content.OfArrayOfContentParts, "expected content parts")
	})
}

// TestBuildChatRequest_WithExtraFields tests buildChatRequest with extra fields.
func TestBuildChatRequest_WithExtraFields(t *testing.T) {
	extraFields := map[string]any{
		"custom_field": "custom_value",
		"session_id":   "abc123",
	}
	m := New("gpt-3.5-turbo", WithAPIKey("test-key"), WithExtraFields(extraFields))

	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("test"),
		},
		GenerationConfig: model.GenerationConfig{},
	}

	chatReq, reqOpts := m.buildChatRequest(req)
	assert.Equal(t, "gpt-3.5-turbo", chatReq.Model, "expected model to be gpt-3.5-turbo")
	// Extra fields should be included in reqOpts
	assert.NotEmpty(t, reqOpts, "expected request options to be present")
}

// TestNew_WithAllOptions tests creating a model with all available options.
func TestNew_WithAllOptions(t *testing.T) {
	m := New("gpt-4",
		WithAPIKey("test-key"),
		WithBaseURL("https://api.example.com"),
		WithChannelBufferSize(512),
		WithVariant(VariantOpenAI),
		WithBatchCompletionWindow(openai.BatchNewParamsCompletionWindow24h),
		WithBatchMetadata(map[string]string{"key": "value"}),
		WithExtraFields(map[string]any{"field": "value"}),
	)

	require.NotNil(t, m, "expected model to be created")
	assert.Equal(t, "gpt-4", m.name, "expected model name to be gpt-4")
	assert.Equal(t, "test-key", m.apiKey, "expected API key to be test-key")
	assert.Equal(t, "https://api.example.com", m.baseURL, "expected base URL to match")
	assert.Equal(t, 512, m.channelBufferSize, "expected channel buffer size to be 512")
	assert.Equal(t, VariantOpenAI, m.variant, "expected variant to be OpenAI")
}

// TestConvertUserMessageContent_WithContentAndParts tests content and parts together.
func TestConvertUserMessageContent_WithContentAndParts(t *testing.T) {
	m := &Model{}

	t.Run("content string with content parts", func(t *testing.T) {
		message := model.Message{
			Role:    model.RoleUser,
			Content: "Text content",
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Additional text")},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		assert.Empty(t, extraFields, "expected no extra fields")
		// Should include both Content and ContentParts
		assert.NotEmpty(t, content.OfArrayOfContentParts, "expected content parts")
		assert.Len(t, content.OfArrayOfContentParts, 2, "expected 2 content parts (Content + part)")
	})
}

// TestConvertUserMessageContent_HunyuanVariant tests Hunyuan variant file handling.
func TestConvertUserMessageContent_HunyuanVariant(t *testing.T) {
	m := &Model{
		variant:       VariantHunyuan,
		variantConfig: variantConfigs[VariantHunyuan],
	}

	t.Run("file content with Hunyuan variant", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Check this file")},
				{
					Type: "file",
					File: &model.File{
						FileID: "file-123",
					},
				},
			},
		}

		content, extraFields := m.convertUserMessageContent(message)
		// Hunyuan variant should skip file type in content and add to extraFields
		assert.NotEmpty(t, extraFields, "expected extra fields for Hunyuan variant")
		fileIDs, ok := extraFields["file_ids"].([]string)
		assert.True(t, ok, "expected file_ids in extra fields")
		assert.Contains(t, fileIDs, "file-123", "expected file-123 in file_ids")
		// File should not be in content parts
		assert.Len(t, content.OfArrayOfContentParts, 1, "expected only text part in content")
	})

	t.Run("multiple files with Hunyuan variant", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: "file", File: &model.File{FileID: "file-1"}},
				{Type: "file", File: &model.File{FileID: "file-2"}},
			},
		}

		_, extraFields := m.convertUserMessageContent(message)
		fileIDs, ok := extraFields["file_ids"].([]string)
		assert.True(t, ok, "expected file_ids in extra fields")
		assert.Len(t, fileIDs, 2, "expected 2 file IDs")
		assert.Contains(t, fileIDs, "file-1", "expected file-1")
		assert.Contains(t, fileIDs, "file-2", "expected file-2")
	})
}

// TestConvertAssistantMessageContent_WithContentAndParts tests assistant message with both content and parts.
func TestConvertAssistantMessageContent_WithContentAndParts(t *testing.T) {
	m := &Model{}

	t.Run("content string with text parts", func(t *testing.T) {
		message := model.Message{
			Role:    model.RoleAssistant,
			Content: "Main content",
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("Additional text")},
			},
		}

		content := m.convertAssistantMessageContent(message)
		// Should include both Content and text ContentParts
		assert.Len(t, content.OfArrayOfContentParts, 2, "expected 2 content parts")
	})

	t.Run("only content string", func(t *testing.T) {
		message := model.Message{
			Role:    model.RoleAssistant,
			Content: "Simple content",
		}

		content := m.convertAssistantMessageContent(message)
		assert.NotNil(t, content.OfString, "expected string content")
	})
}

// TestConvertTools_ErrorCases tests error handling in convertTools.
func TestConvertTools_ErrorCases(t *testing.T) {
	m := New("dummy")

	t.Run("valid tool schema", func(t *testing.T) {
		tools := map[string]tool.Tool{
			"valid_tool": stubTool{decl: &tool.Declaration{
				Name:        "valid_tool",
				Description: "A valid tool",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"param1": {Type: "string"},
					},
				},
			}},
		}

		params := m.convertTools(tools)
		assert.Len(t, params, 1, "expected 1 tool")
		assert.Equal(t, "valid_tool", params[0].Function.Name, "expected tool name to be valid_tool")
	})

	t.Run("tool with complex schema", func(t *testing.T) {
		tools := map[string]tool.Tool{
			"complex_tool": stubTool{decl: &tool.Declaration{
				Name:        "complex_tool",
				Description: "A complex tool",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"nested": {
							Type: "object",
							Properties: map[string]*tool.Schema{
								"field": {Type: "string"},
							},
						},
						"array": {
							Type:  "array",
							Items: &tool.Schema{Type: "string"},
						},
					},
					Required: []string{"nested"},
				},
			}},
		}

		params := m.convertTools(tools)
		assert.Len(t, params, 1, "expected 1 tool")
		assert.Equal(t, "complex_tool", params[0].Function.Name, "expected tool name")
	})
}

// TestConvertSystemMessageContent_WithParts tests system message with content parts.
func TestConvertSystemMessageContent_WithParts(t *testing.T) {
	m := &Model{}

	t.Run("system message with content parts", func(t *testing.T) {
		message := model.Message{
			Role: model.RoleSystem,
			ContentParts: []model.ContentPart{
				{Type: "text", Text: stringPtr("System instruction 1")},
				{Type: "text", Text: stringPtr("System instruction 2")},
			},
		}

		content := m.convertSystemMessageContent(message)
		assert.Len(t, content.OfArrayOfContentParts, 2, "expected 2 content parts")
	})

	t.Run("system message with content string", func(t *testing.T) {
		message := model.Message{
			Role:    model.RoleSystem,
			Content: "System prompt",
		}

		content := m.convertSystemMessageContent(message)
		assert.NotNil(t, content.OfString, "expected string content")
	})
}

// TestWithEnableTokenTailoring_SafetyMarginAndRatioLimit tests the safety margin and ratio limit logic.
func TestWithEnableTokenTailoring_SafetyMarginAndRatioLimit(t *testing.T) {
	// Capture the built OpenAI request to check messages count reflects tailoring.
	var captured *openaigo.ChatCompletionNewParams
	m := New("deepseek-chat", // Known model with 131072 context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger aggressive tailoring.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 1200; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 40))))
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
	// After tailoring with safety margin and ratio limit, messages should be significantly reduced.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored, got %d (original: %d)", len(captured.Messages), len(messages))
	// With 65% ratio limit (safetyMargin=10%, protocolOverhead=512, reserveOutput=2048),
	// we expect roughly 55-65% of the original messages depending on token distribution.
	require.LessOrEqual(t, len(captured.Messages), int(float64(len(messages))*0.70), "expected messages to be reduced to at most 70%% due to ratio limit, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestHasReasoningContent tests the hasReasoningContent method.
func TestHasReasoningContent(t *testing.T) {
	m := &Model{}

	// Test with nil ExtraFields.
	delta1 := openai.ChatCompletionChunkChoiceDelta{}
	// Since we can't easily construct the JSON field in tests,
	// we'll test the method doesn't panic with empty delta.
	assert.False(t, m.hasReasoningContent(delta1))

	// Note: Testing with actual reasoning content would require
	// integration tests with real API responses, as the JSON field
	// structure is internal to the OpenAI SDK.
}

// TestExtractReasoningContent tests the extractReasoningContent function.
func TestExtractReasoningContent(t *testing.T) {
	t.Run("returns empty string when extraFields is nil", func(t *testing.T) {
		result := extractReasoningContent(nil)
		assert.Equal(t, "", result)
	})

	t.Run("returns empty string when reasoning_content key is missing", func(t *testing.T) {
		extraFields := make(map[string]respjson.Field)
		result := extractReasoningContent(extraFields)
		assert.Equal(t, "", result)
	})

	t.Run("returns empty string when field value cannot be unquoted", func(t *testing.T) {
		// Create a field with invalid JSON that cannot be unquoted.
		invalidField := respjson.Field{}
		// We can't easily set the internal Raw value, but we can test
		// the behavior by ensuring the function handles errors gracefully.
		// This test documents the expected behavior even if we can't
		// fully exercise it without SDK internals.
		extraFields := map[string]respjson.Field{
			model.ReasoningContentKey: invalidField,
		}
		result := extractReasoningContent(extraFields)
		assert.Equal(t, "", result)
	})

	// Note: Testing with valid reasoning content would require
	// integration tests with real API responses, as the respjson.Field
	// structure is internal to the OpenAI SDK and difficult to mock.
}

// TestShouldSkipEmptyChunk tests the shouldSkipEmptyChunk method.
func TestShouldSkipEmptyChunk(t *testing.T) {
	m := &Model{}

	// Test with no choices - should not skip (return false)
	chunk1 := openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{},
	}
	assert.False(t, m.shouldSkipEmptyChunk(chunk1))

	// Note: Testing with actual content requires proper JSON field setup
	// which is complex due to OpenAI SDK internal structures.
	// Integration tests with real API responses would be more appropriate
	// for testing the full shouldSkipEmptyChunk logic.
}

// TestShouldSuppressChunk tests the shouldSuppressChunk method.
func TestShouldSuppressChunk(t *testing.T) {
	m := &Model{}

	// Test with no choices
	chunk1 := openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{},
	}
	assert.True(t, m.shouldSuppressChunk(chunk1))

	// Test with content
	chunk2 := openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{
			{
				Delta: openai.ChatCompletionChunkChoiceDelta{
					Content: "Hello",
				},
			},
		},
	}
	assert.False(t, m.shouldSuppressChunk(chunk2))
}

// TestCreatePartialResponse tests basic partial response creation.
func TestCreatePartialResponse(t *testing.T) {
	m := &Model{}

	t.Run("basic chunk with content", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						Content: "Hello",
					},
				},
			},
		}

		response := m.createPartialResponse(chunk)

		assert.Equal(t, "test-id", response.ID)
		assert.Equal(t, "test-model", response.Model)
		assert.True(t, response.IsPartial)
		assert.False(t, response.Done)
		assert.Len(t, response.Choices, 1)
		assert.Equal(t, "Hello", response.Choices[0].Delta.Content)
	})

	t.Run("chunk with empty object", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "",
			Created: 1234567890,
			Model:   "test-model",
		}

		response := m.createPartialResponse(chunk)
		assert.Equal(t, model.ObjectTypeChatCompletionChunk, response.Object)
	})

	t.Run("chunk with no choices", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []openai.ChatCompletionChunkChoice{},
		}

		response := m.createPartialResponse(chunk)
		assert.NotNil(t, response)
		assert.Empty(t, response.Choices)
	})

	t.Run("chunk with finish reason", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						Content: "",
					},
					FinishReason: "stop",
				},
			},
		}

		response := m.createPartialResponse(chunk)
		assert.NotNil(t, response)
		assert.Len(t, response.Choices, 1)
		assert.NotNil(t, response.Choices[0].FinishReason)
		assert.Equal(t, "stop", *response.Choices[0].FinishReason)
	})

	t.Run("chunk with empty delta", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{},
				},
			},
		}

		response := m.createPartialResponse(chunk)
		assert.NotNil(t, response)
		assert.Len(t, response.Choices, 1)
		assert.Empty(t, response.Choices[0].Delta.Content)
		assert.Empty(t, response.Choices[0].Delta.ReasoningContent)
	})

	t.Run("chunk with multiple content parts", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:      "test-id",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						Content: "Part 1",
					},
				},
			},
		}

		response1 := m.createPartialResponse(chunk)
		assert.Equal(t, "Part 1", response1.Choices[0].Delta.Content)

		chunk.Choices[0].Delta.Content = " Part 2"
		response2 := m.createPartialResponse(chunk)
		assert.Equal(t, " Part 2", response2.Choices[0].Delta.Content)
	})
}

// Integration test for reasoning content processing
func TestReasoningContentIntegration(t *testing.T) {
	// This test would require actual API responses with reasoning content
	// For now, we'll just test that our methods don't panic with empty data
	m := &Model{}

	// Test empty chunk processing
	emptyChunk := openai.ChatCompletionChunk{}
	assert.NotPanics(t, func() {
		m.shouldSkipEmptyChunk(emptyChunk)
		m.shouldSuppressChunk(emptyChunk)
		m.createPartialResponse(emptyChunk)
	})
}

// TestReasoningContentChunkHandling tests that chunks with reasoning content
// are properly handled without causing panics in the accumulator.
func TestReasoningContentChunkHandling(t *testing.T) {
	m := &Model{}

	t.Run("hasReasoningContent returns false for empty delta", func(t *testing.T) {
		delta := openai.ChatCompletionChunkChoiceDelta{}
		assert.False(t, m.hasReasoningContent(delta))
	})

	t.Run("shouldSkipEmptyChunk handles chunks without choices", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{},
		}
		// Should not skip chunks with no choices (let them be processed normally)
		assert.False(t, m.shouldSkipEmptyChunk(chunk))
	})

	t.Run("shouldSuppressChunk suppresses empty chunks", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{},
		}
		// Should suppress chunks with no choices
		assert.True(t, m.shouldSuppressChunk(chunk))
	})

	t.Run("createPartialResponse handles chunks with content", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			ID:    "chunk-1",
			Model: "test-model",
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						Content: "test content",
					},
				},
			},
		}
		response := m.createPartialResponse(chunk)
		assert.NotNil(t, response)
		assert.Equal(t, "chunk-1", response.ID)
		assert.Equal(t, "test content", response.Choices[0].Delta.Content)
	})
}

// TestEmptyChunkHandling tests the handling of empty chunks in streaming responses.
func TestEmptyChunkHandling(t *testing.T) {
	m := New("test-model")

	t.Run("shouldSkipEmptyChunk with no choices", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{},
		}
		// Should not skip chunks with no choices.
		assert.False(t, m.shouldSkipEmptyChunk(chunk))
	})

	t.Run("shouldSkipEmptyChunk with empty delta", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{},
				},
			},
		}
		// Should skip chunks with completely empty delta.
		assert.True(t, m.shouldSkipEmptyChunk(chunk))
	})

	t.Run("shouldSuppressChunk with empty delta", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{},
				},
			},
		}
		// Should suppress chunks with empty delta and no finish reason.
		assert.True(t, m.shouldSuppressChunk(chunk))
	})

	t.Run("shouldSuppressChunk with finish reason", func(t *testing.T) {
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					FinishReason: "stop",
				},
			},
		}
		// Should not suppress chunks with finish reason.
		assert.False(t, m.shouldSuppressChunk(chunk))
	})

	t.Run("hasReasoningContent returns false for empty delta", func(t *testing.T) {
		delta := openai.ChatCompletionChunkChoiceDelta{}
		assert.False(t, m.hasReasoningContent(delta))
	})
}

// TestToolCallIndexMapping tests the tool call index mapping functionality.
func TestToolCallIndexMapping(t *testing.T) {
	m := New("test-model")

	t.Run("updateToolCallIndexMapping with valid tool call", func(t *testing.T) {
		idToIndexMap := make(map[string]int)
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
							{
								Index: 1,
								ID:    "call-123",
								Type:  "function",
							},
						},
					},
				},
			},
		}
		m.updateToolCallIndexMapping(chunk, idToIndexMap)
		assert.Equal(t, 1, idToIndexMap["call-123"])
	})

	t.Run("updateToolCallIndexMapping with empty tool calls", func(t *testing.T) {
		idToIndexMap := make(map[string]int)
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{
				{
					Delta: openai.ChatCompletionChunkChoiceDelta{
						ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{},
					},
				},
			},
		}
		m.updateToolCallIndexMapping(chunk, idToIndexMap)
		assert.Empty(t, idToIndexMap)
	})

	t.Run("updateToolCallIndexMapping with no choices", func(t *testing.T) {
		idToIndexMap := make(map[string]int)
		chunk := openai.ChatCompletionChunk{
			Choices: []openai.ChatCompletionChunkChoice{},
		}
		m.updateToolCallIndexMapping(chunk, idToIndexMap)
		assert.Empty(t, idToIndexMap)
	})
}

// TestStreamingCallbackIntegration tests the integration of streaming callbacks.
func TestStreamingCallbackIntegration(t *testing.T) {
	t.Run("streaming with chat stream complete callback", func(t *testing.T) {
		var callbackCalled bool
		var capturedRequest *openai.ChatCompletionNewParams

		callback := func(ctx context.Context, req *openai.ChatCompletionNewParams, acc *openai.ChatCompletionAccumulator, err error) {
			callbackCalled = true
			capturedRequest = req
			_ = acc // unused in this test
			_ = err // unused in this test
		}

		// Create mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Send a simple streaming response
			chunks := []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
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

		m := New("gpt-3.5-turbo",
			WithBaseURL(server.URL),
			WithAPIKey("test-key"),
			WithChatStreamCompleteCallback(callback),
		)

		ctx := context.Background()
		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("Hello"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		responseChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)

		// Consume all responses
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Give some time for the callback to be called
		time.Sleep(100 * time.Millisecond)

		// Verify callback was called
		assert.True(t, callbackCalled, "expected chat stream complete callback to be called")
		assert.NotNil(t, capturedRequest, "expected request to be captured in callback")
	})

	t.Run("streaming with reasoning content handling", func(t *testing.T) {
		// Create mock server with reasoning content
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Send chunks with reasoning content
			chunks := []string{
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"\"Let me think about this...\""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"I think","reasoning_content":"\"I need to consider the context\""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" the answer is","reasoning_content":"\"Based on my analysis\""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" 42."},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
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

		m := New("gpt-3.5-turbo",
			WithBaseURL(server.URL),
			WithAPIKey("test-key"),
		)

		ctx := context.Background()
		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("What is the meaning of life?"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		responseChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)

		var responses []*model.Response
		for response := range responseChan {
			responses = append(responses, response)
			if response.Done {
				break
			}
		}

		// Verify that we received responses with reasoning content
		assert.NotEmpty(t, responses, "expected to receive responses")

		// Check that at least one response has reasoning content
		var hasReasoning bool
		for _, resp := range responses {
			if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].Delta.ReasoningContent != "" {
				hasReasoning = true
				break
			}
		}
		assert.True(t, hasReasoning, "expected at least one response to contain reasoning content")
	})

	t.Run("streaming with malformed reasoning content", func(t *testing.T) {
		// Create mock server with malformed reasoning content to test error handling.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Send chunks with various malformed reasoning content formats.
			chunks := []string{
				// Reasoning content without quotes (raw value).
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"raw_reasoning_without_quotes"},"finish_reason":null}]}`,
				// Reasoning content with quotes that should be stripped.
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"test","reasoning_content":"\"quoted_reasoning\""},"finish_reason":null}]}`,
				`data: {"id":"test","object":"chat.completion.chunk","created":1699200000,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" content"},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
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

		m := New("gpt-3.5-turbo",
			WithBaseURL(server.URL),
			WithAPIKey("test-key"),
		)

		ctx := context.Background()
		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("Test malformed reasoning"),
			},
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		responseChan, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)

		var responses []*model.Response
		for response := range responseChan {
			responses = append(responses, response)
			if response.Done {
				break
			}
		}

		// Verify that we received responses and they were processed without panic.
		assert.NotEmpty(t, responses, "expected to receive responses")

		// Check that reasoning content was extracted even from malformed formats.
		var hasReasoning bool
		for _, resp := range responses {
			if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].Delta.ReasoningContent != "" {
				hasReasoning = true
				// Verify the reasoning content was properly extracted.
				t.Logf("Extracted reasoning content: %q", resp.Choices[0].Delta.ReasoningContent)
			}
		}
		assert.True(t, hasReasoning, "expected at least one response to contain reasoning content")
	})
}
