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
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	openaisdk "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func TestOptionsSetters(t *testing.T) {
	counter := model.NewSimpleTokenCounter()
	strategy := model.NewMiddleOutStrategy(counter)

	opts := &Options{}

	WithAPIKey("key")(opts)
	WithBaseURL("https://example.com")(opts)
	WithHTTPClientName("client")(opts)
	WithHTTPClientTransport(http.DefaultTransport)(opts)
	WithChannelBufferSize(128)(opts)
	WithEnableTokenTailoring(true)(opts)
	WithMaxInputTokens(512)(opts)
	WithTokenCounter(counter)(opts)
	WithTailoringStrategy(strategy)(opts)

	assert.Equal(t, "key", opts.APIKey)
	assert.Equal(t, "https://example.com", opts.BaseURL)
	assert.Equal(t, "client", opts.HTTPClientName)
	assert.Equal(t, http.DefaultTransport, opts.HTTPClientTransport)
	assert.NotNil(t, opts.ChannelBufferSize)
	assert.Equal(t, 128, *opts.ChannelBufferSize)
	assert.NotNil(t, opts.EnableTokenTailoring)
	assert.True(t, *opts.EnableTokenTailoring)
	assert.NotNil(t, opts.MaxInputTokens)
	assert.Equal(t, 512, *opts.MaxInputTokens)
	assert.Equal(t, counter, opts.TokenCounter)
	assert.Equal(t, strategy, opts.TailoringStrategy)
}

func TestWithExtraFieldsMergesAndCopies(t *testing.T) {
	opts := &Options{}
	source := map[string]any{"trace": "id"}
	WithExtraFields(source)(opts)
	source["trace"] = "changed"

	assert.Equal(t, "id", opts.ExtraFields["trace"])

	WithExtraFields(map[string]any{"tenant": "internal"})(opts)
	assert.Equal(t, "internal", opts.ExtraFields["tenant"])
	assert.Equal(t, 2, len(opts.ExtraFields))
}

func TestWithCallbacksAllocatesAndOverwrites(t *testing.T) {
	opts := &Options{}

	cb1 := Callbacks{
		OpenAIChatRequest:  openai.ChatRequestCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams) {}),
		OpenAIChatResponse: openai.ChatResponseCallbackFunc(func(context.Context, *openaisdk.ChatCompletionNewParams, *openaisdk.ChatCompletion) {}),
	}
	cb2 := Callbacks{
		AnthropicChatChunk: anthropic.ChatChunkCallbackFunc(func(context.Context, *anthropicsdk.MessageNewParams, *anthropicsdk.MessageStreamEventUnion) {}),
	}

	WithCallbacks(cb1)(opts)
	first := opts.Callbacks
	assert.NotNil(t, first)
	assert.NotNil(t, first.OpenAIChatRequest)
	assert.NotNil(t, first.OpenAIChatResponse)

	WithCallbacks(cb2)(opts)
	assert.Equal(t, first, opts.Callbacks)
	assert.NotNil(t, opts.Callbacks.AnthropicChatChunk)
	assert.NotNil(t, opts.Callbacks.OpenAIChatRequest)
}

func TestWithOpenAIOption(t *testing.T) {
	opts := &Options{}
	WithOpenAIOption(openai.WithBaseURL("https://example.com"))(opts)
	assert.Equal(t, 1, len(opts.OpenAIOption))
}

func TestWithAnthropicOption(t *testing.T) {
	opts := &Options{}
	WithAnthropicOption(anthropic.WithBaseURL("https://example.com"))(opts)
	assert.Equal(t, 1, len(opts.AnthropicOption))
}
