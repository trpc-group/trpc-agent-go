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
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	openaigo "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
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
			if m == nil {
				t.Fatal("expected model to be created, got nil")
			}

			// Verify that the model was created with the expected name.
			if m.name != tt.modelName {
				t.Errorf("expected model name %s, got %s", tt.modelName, m.name)
			}

			// Verify that the model was created successfully.
			if m.name != tt.modelName {
				t.Errorf("expected model name %s, got %s", tt.modelName, m.name)
			}
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
	if len(o.OpenAIOptions) != 2 {
		t.Errorf("expected 2 OpenAI options, got %d", len(o.OpenAIOptions))
	}

	// Apply additional options.
	WithOpenAIOptions(
		openaiopt.WithRequestTimeout(30 * time.Second),
	)(o)

	// Verify that options were appended.
	if len(o.OpenAIOptions) != 3 {
		t.Errorf("expected 3 OpenAI options after append, got %d", len(o.OpenAIOptions))
	}
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

	if m == nil {
		t.Fatal("expected model to be created")
	}

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
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Verify that the response channel was created.
	if responseChan == nil {
		t.Fatal("expected response channel to be created")
	}

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

	if m == nil {
		t.Fatal("expected model to be created")
	}

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
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Consume one response to trigger the middleware.
	select {
	case <-responseChan:
		// Middleware should have been called during the request.
		if !middlewareCalled {
			t.Error("expected middleware to be called")
		}
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
		WithChatResponseCallback(func(ctx context.Context, resp *openaigo.ChatCompletion) {
			// Test callback.
		}),
		WithChatChunkCallback(func(ctx context.Context, chunk *openaigo.ChatCompletionChunk) {
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

	if m == nil {
		t.Fatal("expected model to be created")
	}

	// Verify that all options were applied correctly.
	if m.name != "test-model" {
		t.Errorf("expected model name 'test-model', got %s", m.name)
	}

	if m.apiKey != "test-key" {
		t.Errorf("expected API key 'test-key', got %s", m.apiKey)
	}

	if m.baseURL != "https://api.example.com" {
		t.Errorf("expected base URL 'https://api.example.com', got %s", m.baseURL)
	}

	if m.channelBufferSize != 1024 {
		t.Errorf("expected channel buffer size 1024, got %d", m.channelBufferSize)
	}

	// Verify that callbacks were set.
	if m.chatRequestCallback == nil {
		t.Error("expected chat request callback to be set")
	}

	if m.chatResponseCallback == nil {
		t.Error("expected chat response callback to be set")
	}

	if m.chatChunkCallback == nil {
		t.Error("expected chat chunk callback to be set")
	}
}
