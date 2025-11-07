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
	"net/http"
	"reflect"
	"testing"
	"unsafe"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openaisdk "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
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

type testModel struct{}

func (testModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (testModel) Info() model.Info {
	return model.Info{Name: "test"}
}
