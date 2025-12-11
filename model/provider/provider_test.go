//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package provider

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ollama/ollama/api"
	openaisdk "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/gemini"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan"
	"trpc.group/trpc-go/trpc-agent-go/model/ollama"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func TestModelUnknownProvider(t *testing.T) {
	_, err := Model("not-exist", "gpt")
	assert.Error(t, err)
}

func TestRegisterFactoryOverridesDefault(t *testing.T) {
	original, ok := Get("openai")
	assert.True(t, ok)

	var captured *Options
	Register("openai", func(opts *Options) (model.Model, error) {
		captured = opts
		return testModel{}, nil
	})
	defer Register("openai", original)

	_, err := Model("openai", "test-model", WithAPIKey("key"), WithChannelBufferSize(16))
	assert.NoError(t, err)
	assert.NotNil(t, captured)
	assert.Equal(t, "openai", captured.ProviderName)
	assert.Equal(t, "test-model", captured.ModelName)
	assert.Equal(t, "key", captured.APIKey)
	assert.NotNil(t, captured.ChannelBufferSize)
	assert.Equal(t, 16, *captured.ChannelBufferSize)
}

func TestOpenAIFactoryAppliesOptions(t *testing.T) {
	counter := model.NewSimpleTokenCounter()
	strategy := model.NewMiddleOutStrategy(counter)

	cb := Callbacks{
		OpenAIChatRequest:  openai.ChatRequestCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams) {}),
		OpenAIChatResponse: openai.ChatResponseCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams, *openaisdk.ChatCompletion) {}),
		OpenAIChatChunk:    openai.ChatChunkCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams, *openaisdk.ChatCompletionChunk) {}),
		OpenAIStreamComplete: openai.ChatStreamCompleteCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams, *openaisdk.ChatCompletionAccumulator, error) {
		}),
	}

	fields := map[string]any{"tenant": "internal"}
	bufSize := 42
	enabled := true
	maxTokens := 256

	opts := &Options{ModelName: "gpt-4"}
	WithAPIKey("openai-key")(opts)
	WithBaseURL("https://api.example.com")(opts)
	WithHTTPClientName("custom-client")(opts)
	WithHTTPClientTransport(http.DefaultTransport)(opts)
	WithCallbacks(cb)(opts)
	WithChannelBufferSize(bufSize)(opts)
	WithExtraFields(fields)(opts)
	WithEnableTokenTailoring(enabled)(opts)
	WithMaxInputTokens(maxTokens)(opts)
	WithTokenCounter(counter)(opts)
	WithTailoringStrategy(strategy)(opts)

	modelInstance, err := openaiProvider(opts)
	assert.NoError(t, err)

	// Ensure original map mutation doesn't leak
	fields["tenant"] = "changed"

	openaiModel, ok := modelInstance.(*openai.Model)
	assert.True(t, ok)

	assert.Equal(t, "gpt-4", modelInstance.Info().Name)
	assert.Equal(t, "https://api.example.com", readStringField(openaiModel, "baseURL"))
	assert.Equal(t, "openai-key", readStringField(openaiModel, "apiKey"))
	assert.Equal(t, 42, readIntField(openaiModel, "channelBufferSize"))
	assert.True(t, readBoolField(openaiModel, "enableTokenTailoring"))
	assert.Equal(t, 256, readIntField(openaiModel, "maxInputTokens"))
	assert.Equal(t, "internal", readMapField(openaiModel, "extraFields")["tenant"])
	assert.Equal(t, counter, readInterfaceField(openaiModel, "tokenCounter"))
	assert.Equal(t, strategy, readInterfaceField(openaiModel, "tailoringStrategy"))
	assert.NotNil(t, readInterfaceField(openaiModel, "chatRequestCallback"))
	assert.NotNil(t, readInterfaceField(openaiModel, "chatResponseCallback"))
	assert.NotNil(t, readInterfaceField(openaiModel, "chatChunkCallback"))
	assert.NotNil(t, readInterfaceField(openaiModel, "chatStreamCompleteCallback"))
}

func TestAnthropicFactoryAppliesOptions(t *testing.T) {
	cb := Callbacks{
		AnthropicChatRequest:    anthropic.ChatRequestCallbackFunc(func(context.Context, *anthropicsdk.MessageNewParams) {}),
		AnthropicChatResponse:   anthropic.ChatResponseCallbackFunc(func(context.Context, *anthropicsdk.MessageNewParams, *anthropicsdk.Message) {}),
		AnthropicChatChunk:      anthropic.ChatChunkCallbackFunc(func(context.Context, *anthropicsdk.MessageNewParams, *anthropicsdk.MessageStreamEventUnion) {}),
		AnthropicStreamComplete: anthropic.ChatStreamCompleteCallbackFunc(func(context.Context, *anthropicsdk.MessageNewParams, *anthropicsdk.Message, error) {}),
	}

	bufSize := 64
	opts := &Options{ModelName: "claude"}
	WithAPIKey("anthropic-key")(opts)
	WithBaseURL("https://anthropic.example.com")(opts)
	WithHTTPClientName("anthropic-client")(opts)
	WithHTTPClientTransport(http.DefaultTransport)(opts)
	WithCallbacks(cb)(opts)
	WithChannelBufferSize(bufSize)(opts)
	WithAnthropicOption(anthropic.WithAnthropicRequestOptions(option.WithJSONSet("tenant", "internal")))(opts)

	modelInstance, err := anthropicProvider(opts)
	assert.NoError(t, err)

	anthropicModel, ok := modelInstance.(*anthropic.Model)
	assert.True(t, ok)

	assert.Equal(t, "claude", modelInstance.Info().Name)
	assert.Equal(t, "https://anthropic.example.com", readStringField(anthropicModel, "baseURL"))
	assert.Equal(t, "anthropic-key", readStringField(anthropicModel, "apiKey"))
	assert.Equal(t, 64, readIntField(anthropicModel, "channelBufferSize"))
	assert.NotNil(t, readInterfaceField(anthropicModel, "chatRequestCallback"))
	assert.NotNil(t, readInterfaceField(anthropicModel, "chatResponseCallback"))
	assert.NotNil(t, readInterfaceField(anthropicModel, "chatChunkCallback"))
	assert.NotNil(t, readInterfaceField(anthropicModel, "chatStreamCompleteCallback"))
	assert.Equal(t, 1, readSliceLen(anthropicModel, "anthropicRequestOptions"))
}

func TestOpenAIFactoryWithHeaders(t *testing.T) {
	var captured http.Header
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r.Header.Clone()
		body := `{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"created":0,
			"model":"gpt-4o-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	opts := &Options{ModelName: "gpt-4o-mini"}
	WithAPIKey("test-key")(opts)
	WithHeaders(map[string]string{
		"X-Custom": "value",
		"X-Tenant": "t1",
	})(opts)
	WithHTTPClientTransport(rt)(opts)

	m, err := openaiProvider(opts)
	assert.NoError(t, err)

	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	assert.NoError(t, err)
	resp := <-ch
	assert.NotNil(t, resp)
	assert.True(t, resp.Done)
	assert.Equal(t, "value", captured.Get("X-Custom"))
	assert.Equal(t, "t1", captured.Get("X-Tenant"))
	assert.Equal(t, "Bearer test-key", captured.Get("Authorization"))
}

func TestAnthropicFactoryWithHeaders(t *testing.T) {
	var captured http.Header
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r.Header.Clone()
		body := `{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"hello"}],
			"model":"claude-3-5-sonnet-latest",
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":2}
		}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	opts := &Options{ModelName: "claude-3-5-sonnet-latest"}
	WithAPIKey("anthropic-key")(opts)
	WithHeaders(map[string]string{
		"X-Trace-ID": "trace-1",
		"X-Tenant":   "buyer",
	})(opts)
	WithHTTPClientTransport(rt)(opts)

	m, err := anthropicProvider(opts)
	assert.NoError(t, err)

	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	assert.NoError(t, err)
	resp := <-ch
	assert.NotNil(t, resp)
	assert.True(t, resp.Done)
	assert.Equal(t, "trace-1", captured.Get("X-Trace-ID"))
	assert.Equal(t, "buyer", captured.Get("X-Tenant"))
	assert.Equal(t, "anthropic-key", captured.Get("x-api-key"))
}

func TestWithOpenAIOptionOverwrites(t *testing.T) {
	model, err := Model(
		"openai",
		"gpt-4",
		WithBaseURL("a"),
		WithOpenAIOption(openai.WithBaseURL("b")),
		WithBaseURL("c"),
	)
	assert.NoError(t, err)
	openaiModel, ok := model.(*openai.Model)
	assert.True(t, ok)
	assert.Equal(t, "b", readStringField(openaiModel, "baseURL"))
}

func TestWithAnthropicOptionOverwrites(t *testing.T) {
	model, err := Model(
		"anthropic",
		"claude",
		WithBaseURL("a"),
		WithAnthropicOption(anthropic.WithBaseURL("b")),
		WithBaseURL("c"),
	)
	assert.NoError(t, err)
	anthropicModel, ok := model.(*anthropic.Model)
	assert.True(t, ok)
	assert.Equal(t, "b", readStringField(anthropicModel, "baseURL"))
}

func TestWithExtraFieldsCopiesInput(t *testing.T) {
	opts := &Options{}
	source := map[string]any{"trace": "id"}
	WithExtraFields(source)(opts)
	source["trace"] = "changed"

	assert.Equal(t, "id", opts.ExtraFields["trace"])
}

func TestProviderWithTokenTailoringConfig(t *testing.T) {
	config := &model.TokenTailoringConfig{
		ProtocolOverheadTokens: 1024,
		ReserveOutputTokens:    4096,
		SafetyMarginRatio:      0.15,
	}

	// Test OpenAI provider
	opts := &Options{ModelName: "gpt-4"}
	WithTokenTailoringConfig(config)(opts)
	modelInstance, err := openaiProvider(opts)
	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	openaiModel, ok := modelInstance.(*openai.Model)
	assert.True(t, ok)
	assert.NotNil(t, openaiModel)

	// Test Anthropic provider
	opts = &Options{ModelName: "claude"}
	WithTokenTailoringConfig(config)(opts)
	modelInstance, err = anthropicProvider(opts)
	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	anthropicModel, ok := modelInstance.(*anthropic.Model)
	assert.True(t, ok)
	assert.NotNil(t, anthropicModel)
}

func TestGetProvider(t *testing.T) {
	provider, ok := Get("openai")
	assert.True(t, ok)
	assert.NotNil(t, provider)

	provider, ok = Get("anthropic")
	assert.True(t, ok)
	assert.NotNil(t, provider)

	provider, ok = Get("gemini")
	assert.True(t, ok)
	assert.NotNil(t, provider)

	provider, ok = Get("not-exist")
	assert.False(t, ok)
	assert.Nil(t, provider)
}

func TestRegisterProvider(t *testing.T) {
	var captured *Options
	testProvider := func(opts *Options) (model.Model, error) {
		captured = opts
		return testModel{}, nil
	}

	Register("test-provider", testProvider)
	defer func() {
		providersMu.Lock()
		delete(providers, "test-provider")
		providersMu.Unlock()
	}()

	provider, ok := Get("test-provider")
	assert.True(t, ok)
	assert.NotNil(t, provider)

	_, err := provider(&Options{ModelName: "test-model"})
	assert.NoError(t, err)
	assert.NotNil(t, captured)
	assert.Equal(t, "test-model", captured.ModelName)
}

func TestModelWithAllOptions(t *testing.T) {
	counter := model.NewSimpleTokenCounter()
	strategy := model.NewMiddleOutStrategy(counter)
	config := &model.TokenTailoringConfig{
		ProtocolOverheadTokens: 1024,
		ReserveOutputTokens:    4096,
	}

	modelInstance, err := Model(
		"openai",
		"gpt-4",
		WithAPIKey("test-key"),
		WithBaseURL("https://test.example.com"),
		WithChannelBufferSize(128),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(2048),
		WithTokenCounter(counter),
		WithTailoringStrategy(strategy),
		WithTokenTailoringConfig(config),
	)
	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	assert.Equal(t, "gpt-4", modelInstance.Info().Name)

	modelInstance, err = Model(
		"anthropic",
		"claude",
		WithAPIKey("test-key"),
		WithBaseURL("https://test.example.com"),
		WithChannelBufferSize(128),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(2048),
		WithTokenCounter(counter),
		WithTailoringStrategy(strategy),
		WithTokenTailoringConfig(config),
	)
	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	assert.Equal(t, "claude", modelInstance.Info().Name)

	modelInstance, err = Model(
		"gemini",
		"gemini-pro",
		WithChannelBufferSize(128),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(2048),
		WithTokenCounter(counter),
		WithTailoringStrategy(strategy),
		WithTokenTailoringConfig(config),
		WithCallbacks(Callbacks{
			GeminiChatChunk:      nil,
			GeminiChatRequest:    nil,
			GeminiStreamComplete: nil,
			GeminiChatResponse:   nil,
		}),
		WithGeminiOption(gemini.WithGeminiClientConfig(nil)),
	)
	assert.Error(t, err)
	assert.Nil(t, modelInstance)

	modelInstance, err = Model(
		"ollama",
		"llama3.2:latest",
		WithBaseURL("https://test.example.com"),
		WithChannelBufferSize(128),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(2048),
		WithTokenCounter(counter),
		WithTailoringStrategy(strategy),
		WithTokenTailoringConfig(config),
		WithCallbacks(
			Callbacks{
				OllamaChatRequest:    ollama.ChatRequestCallbackFunc(func(context.Context, *api.ChatRequest) {}),
				OllamaChatResponse:   ollama.ChatResponseCallbackFunc(func(context.Context, *api.ChatRequest, *api.ChatResponse) {}),
				OllamaChatChunk:      ollama.ChatChunkCallbackFunc(func(context.Context, *api.ChatRequest, *api.ChatResponse) {}),
				OllamaStreamComplete: ollama.ChatStreamCompleteCallbackFunc(func(context.Context, *api.ChatRequest, error) {}),
			},
		),
		WithOllamaOption(ollama.WithKeepAlive(10*time.Second)),
	)
	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	assert.Equal(t, "llama3.2:latest", modelInstance.Info().Name)

	modelInstance, err = Model(
		"hunyuan",
		"hunyuan-t1-latest",
		WithBaseURL("https://test.example.com"),
		WithChannelBufferSize(128),
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(2048),
		WithTokenCounter(counter),
		WithTailoringStrategy(strategy),
		WithTokenTailoringConfig(config),
		WithHunyuanOption(hunyuan.WithSecretId("test-secret-id"),
			hunyuan.WithSecretKey("test-secret-key")),
	)

	assert.NoError(t, err)
	assert.NotNil(t, modelInstance)
	assert.Equal(t, "hunyuan-t1-latest", modelInstance.Info().Name)
}

func readStringField(obj any, name string) string {
	return getField(obj, name).String()
}

func readIntField(obj any, name string) int {
	return int(getField(obj, name).Int())
}

func readBoolField(obj any, name string) bool {
	return getField(obj, name).Bool()
}

func readInterfaceField(obj any, name string) any {
	return getField(obj, name).Interface()
}

func readMapField(obj any, name string) map[string]any {
	value := getField(obj, name)
	if value.IsNil() {
		return nil
	}
	return value.Interface().(map[string]any)
}

func readSliceLen(obj any, name string) int {
	return getField(obj, name).Len()
}

func getField(obj any, name string) reflect.Value {
	v := reflect.ValueOf(obj).Elem().FieldByName(name)
	if !v.IsValid() {
		panic("field " + name + " not found")
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type testModel struct{}

func (testModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (testModel) Info() model.Info {
	return model.Info{Name: "test"}
}
