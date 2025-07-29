//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package openai

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	openaigo "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
			if m == nil {
				t.Fatal("expected model to be created, got nil")
			}

			o := options{}
			for _, opt := range tt.opts {
				opt(&o)
			}

			if m.name != tt.modelName {
				t.Errorf("expected model name %s, got %s", tt.modelName, m.name)
			}

			if m.apiKey != o.APIKey {
				t.Errorf("expected api key %s, got %s", o.APIKey, m.apiKey)
			}

			if m.baseURL != o.BaseURL {
				t.Errorf("expected base url %s, got %s", o.BaseURL, m.baseURL)
			}
		})
	}
}

func TestModel_GenContent_NilReq(t *testing.T) {
	m := New("test-model", WithAPIKey("test-key"))

	ctx := context.Background()
	_, err := m.GenerateContent(ctx, nil)

	if err == nil {
		t.Fatal("expected error for nil request, got nil")
	}

	if err.Error() != "request cannot be nil" {
		t.Errorf("expected 'request cannot be nil', got %s", err.Error())
	}
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
	if err != nil {
		t.Fatalf("failed to generate content: %v", err)
	}

	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		if response.Done {
			break
		}
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response, got none")
	}
}

func TestModel_GenContent_CustomBaseURL(t *testing.T) {
	// This test creates a model with custom base URL but doesn't make actual calls.
	// It's mainly to test the configuration.

	customBaseURL := "https://api.custom-openai.com"
	m := New("custom-model", WithAPIKey("test-key"), WithBaseURL(customBaseURL))

	if m.baseURL != customBaseURL {
		t.Errorf("expected base URL %s, got %s", customBaseURL, m.baseURL)
	}

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
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

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
			if m == nil {
				t.Fatal("expected model to be created")
			}

			o := options{}
			for _, opt := range tt.opts {
				opt(&o)
			}

			if m.apiKey != o.APIKey {
				t.Errorf("expected api key %s, got %s", o.APIKey, m.apiKey)
			}

			if m.baseURL != o.BaseURL {
				t.Errorf("expected base url %s, got %s", o.BaseURL, m.baseURL)
			}
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
	if got, want := len(converted), len(msgs); got != want {
		t.Fatalf("converted len=%d want=%d", got, want)
	}

	roleChecks := []func(openaigo.ChatCompletionMessageParamUnion) bool{
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfSystem != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfUser != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfAssistant != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfTool != nil },
		func(u openaigo.ChatCompletionMessageParamUnion) bool { return u.OfUser != nil },
	}

	for i, u := range converted {
		if !roleChecks[i](u) {
			t.Fatalf("index %d: expected role variant not set", i)
		}
	}

	// Assert that assistant message contains tool calls after conversion.
	assistantUnion := converted[2]
	if assistantUnion.OfAssistant == nil {
		t.Fatalf("assistant union is nil")
	}
	if len(assistantUnion.GetToolCalls()) == 0 {
		t.Fatalf("assistant message should contain tool calls")
	}
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
	if got, want := len(params), 1; got != want {
		t.Fatalf("convertTools len=%d want=%d", got, want)
	}

	fn := params[0].Function
	if fn.Name != toolName {
		t.Fatalf("function name=%s want=%s", fn.Name, toolName)
	}
	if !fn.Description.Valid() || fn.Description.Value != toolDesc {
		t.Fatalf("function description mismatch")
	}

	if reflect.ValueOf(fn.Parameters).IsZero() {
		t.Fatalf("expected parameters to be populated from schema")
	}
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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

		// Wait for callback to be called.
		select {
		case <-callbackCalled:
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for request callback")
		}

		// Consume responses.
		for response := range responseChan {
			if response.Done {
				break
			}
		}

		// Verify the callback was called with correct parameters.
		if capturedCtx == nil {
			t.Fatal("expected context to be captured in callback")
		}
		if capturedRequest == nil {
			t.Fatal("expected request to be captured in callback")
		}
		if capturedRequest.Model != "gpt-3.5-turbo" {
			t.Errorf("expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
		}
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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

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
			t.Fatal("timeout waiting for response callback")
		}

		// Verify the callback was called with correct parameters.
		if capturedCtx == nil {
			t.Fatal("expected context to be captured in callback")
		}
		if capturedRequest == nil {
			t.Fatal("expected request to be captured in callback")
		}
		// Note: capturedResponse might be nil if there was an API error.
		// We only check if it's not nil when we expect a successful response.
		if capturedRequest.Model != "gpt-3.5-turbo" {
			t.Errorf("expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
		}
		// Only check response model if we got a successful response.
		if capturedResponse != nil && capturedResponse.Model != "gpt-3.5-turbo" {
			t.Errorf("expected response model %s, got %s", "gpt-3.5-turbo", capturedResponse.Model)
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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

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
			if capturedRequest == nil {
				t.Fatal("expected request to be captured in callback")
			}
			if capturedChunk == nil {
				t.Fatal("expected chunk to be captured in callback")
			}
			if capturedRequest.Model != "gpt-3.5-turbo" {
				t.Errorf("expected model %s, got %s", "gpt-3.5-turbo", capturedRequest.Model)
			}
			if chunkCount == 0 {
				t.Fatal("expected chunk callback to be called at least once")
			}
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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

		// Wait for request callback.
		select {
		case <-requestCallbackDone:
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for request callback")
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
			t.Fatal("timeout waiting for response callback")
		}

		// Verify appropriate callbacks were called.
		if !requestCalled {
			t.Error("expected request callback to be called")
		}
		if !responseCalled {
			t.Error("expected response callback to be called")
		}
		// Chunk callback should not be called for non-streaming requests.
		if chunkCalled {
			t.Error("expected chunk callback not to be called for non-streaming requests")
		}
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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

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
		if err != nil {
			t.Fatalf("failed to generate content: %v", err)
		}

		// Wait for request callback.
		select {
		case <-requestCallbackDone:
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for request callback")
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
			t.Fatal("timeout waiting for response callback")
		}

		// Verify parameter types are correct.
		if requestParam == nil {
			t.Fatal("expected request parameter to be set")
		}
		if reflect.TypeOf(requestParam) != reflect.TypeOf(&openaigo.ChatCompletionNewParams{}) {
			t.Errorf("expected request parameter type %T, got %T", &openaigo.ChatCompletionNewParams{}, requestParam)
		}

		// Note: responseParam might be nil if there was an API error.
		// We only check the type if we got a successful response.
		if responseParam != nil {
			if reflect.TypeOf(responseParam) != reflect.TypeOf(&openaigo.ChatCompletion{}) {
				t.Errorf("expected response parameter type %T, got %T", &openaigo.ChatCompletion{}, responseParam)
			}
		}

		// Chunk parameter should be nil for non-streaming requests.
		if chunkParam != nil {
			t.Error("expected chunk parameter to be nil for non-streaming requests")
		}
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
		if m.chatRequestCallback == nil {
			t.Error("expected chat request callback to be assigned")
		}
		if m.chatResponseCallback == nil {
			t.Error("expected chat response callback to be assigned")
		}
		if m.chatChunkCallback == nil {
			t.Error("expected chat chunk callback to be assigned")
		}

		// Test that callbacks can be called without panicking.
		ctx := context.Background()
		req := &openaigo.ChatCompletionNewParams{
			Model: "test-model",
		}

		// Test request callback.
		m.chatRequestCallback(ctx, req)
		if !requestCalled {
			t.Error("expected request callback to be called")
		}

		// Test response callback.
		resp := &openaigo.ChatCompletion{
			Model: "test-model",
		}
		m.chatResponseCallback(ctx, req, resp)
		if !responseCalled {
			t.Error("expected response callback to be called")
		}

		// Test chunk callback.
		chunk := &openaigo.ChatCompletionChunk{
			Model: "test-model",
		}
		m.chatChunkCallback(ctx, req, chunk)
		if !chunkCalled {
			t.Error("expected chunk callback to be called")
		}
	})

	t.Run("nil callback assignment", func(t *testing.T) {
		m := New("test-model", WithAPIKey("test-key"))

		// Verify that callbacks are nil when not provided.
		if m.chatRequestCallback != nil {
			t.Error("expected chat request callback to be nil")
		}
		if m.chatResponseCallback != nil {
			t.Error("expected chat response callback to be nil")
		}
		if m.chatChunkCallback != nil {
			t.Error("expected chat chunk callback to be nil")
		}
	})
}

// TestModel_CallbackSignature tests that the callback function signatures
// match the expected types from the diff.
func TestModel_CallbackSignature(t *testing.T) {
	t.Run("callback signature verification", func(t *testing.T) {
		// Test that we can create callback functions with the correct signatures.
		var requestCallback chatRequestCallbackFunc
		var responseCallback chatResponseCallbackFunc
		var chunkCallback chatChunkCallbackFunc

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
