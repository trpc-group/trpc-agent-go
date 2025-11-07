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
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Option configures how a model instance should be constructed.
type Option func(*Options)

// Options contains resolved settings used when constructing provider-backed models.
type Options struct {
	ProviderName         string                  // ProviderName is the provider identifier passed to Model.
	ModelName            string                  // ModelName is the concrete model identifier.
	APIKey               string                  // APIKey holds the credential used for downstream SDK initialization.
	BaseURL              string                  // BaseURL overrides the default endpoint when specified.
	HTTPClientName       string                  // HTTPClientName is the logical name applied to the HTTP client.
	HTTPClientTransport  http.RoundTripper       // HTTPClientTransport allows customizing the HTTP transport.
	Callbacks            *Callbacks              // Callbacks captures provider-specific callback hooks.
	ChannelBufferSize    *int                    // ChannelBufferSize is the response channel buffer size.
	ExtraFields          map[string]any          // ExtraFields are serialized into provider-specific request payloads.
	EnableTokenTailoring *bool                   // EnableTokenTailoring toggles automatic token tailoring.
	MaxInputTokens       *int                    // MaxInputTokens sets the maximum input tokens for token tailoring.
	TokenCounter         model.TokenCounter      // TokenCounter provides a custom token counting implementation.
	TailoringStrategy    model.TailoringStrategy // TailoringStrategy defines the strategy for token tailoring.
	OpenAIOption         []openai.Option         // OpenAIOption stores additional OpenAI options.
	AnthropicOption      []anthropic.Option      // AnthropicOption stores additional Anthropic options.
}

// Callbacks collects provider specific callback hooks.
type Callbacks struct {
	// OpenAIChatRequest runs before dispatching a chat request to OpenAI providers.
	OpenAIChatRequest openai.ChatRequestCallbackFunc
	// OpenAIChatResponse runs after receiving a full chat response from OpenAI providers.
	OpenAIChatResponse openai.ChatResponseCallbackFunc
	// OpenAIChatChunk runs for each streaming chunk from OpenAI providers.
	OpenAIChatChunk openai.ChatChunkCallbackFunc
	// OpenAIStreamComplete runs after an OpenAI streaming session completes.
	OpenAIStreamComplete openai.ChatStreamCompleteCallbackFunc
	// AnthropicChatRequest runs before dispatching a chat request to Anthropic providers.
	AnthropicChatRequest anthropic.ChatRequestCallbackFunc
	// AnthropicChatResponse runs after receiving a full chat response from Anthropic providers.
	AnthropicChatResponse anthropic.ChatResponseCallbackFunc
	// AnthropicChatChunk runs for each streaming chunk from Anthropic providers.
	AnthropicChatChunk anthropic.ChatChunkCallbackFunc
	// AnthropicStreamComplete runs after an Anthropic streaming session completes.
	AnthropicStreamComplete anthropic.ChatStreamCompleteCallbackFunc
}

// WithAPIKey records the API key for the provider.
func WithAPIKey(key string) Option {
	return func(o *Options) {
		o.APIKey = key
	}
}

// WithBaseURL records the base URL for the provider.
func WithBaseURL(url string) Option {
	return func(o *Options) {
		o.BaseURL = url
	}
}

// WithHTTPClientName records the logical HTTP client name.
func WithHTTPClientName(name string) Option {
	return func(o *Options) {
		o.HTTPClientName = name
	}
}

// WithHTTPClientTransport configures the HTTP transport for the provider.
func WithHTTPClientTransport(transport http.RoundTripper) Option {
	return func(o *Options) {
		o.HTTPClientTransport = transport
	}
}

// WithCallbacks registers provider specific callbacks.
func WithCallbacks(cb Callbacks) Option {
	return func(o *Options) {
		if o.Callbacks == nil {
			o.Callbacks = &Callbacks{}
		}
		if cb.OpenAIChatRequest != nil {
			o.Callbacks.OpenAIChatRequest = cb.OpenAIChatRequest
		}
		if cb.OpenAIChatResponse != nil {
			o.Callbacks.OpenAIChatResponse = cb.OpenAIChatResponse
		}
		if cb.OpenAIChatChunk != nil {
			o.Callbacks.OpenAIChatChunk = cb.OpenAIChatChunk
		}
		if cb.OpenAIStreamComplete != nil {
			o.Callbacks.OpenAIStreamComplete = cb.OpenAIStreamComplete
		}
		if cb.AnthropicChatRequest != nil {
			o.Callbacks.AnthropicChatRequest = cb.AnthropicChatRequest
		}
		if cb.AnthropicChatResponse != nil {
			o.Callbacks.AnthropicChatResponse = cb.AnthropicChatResponse
		}
		if cb.AnthropicChatChunk != nil {
			o.Callbacks.AnthropicChatChunk = cb.AnthropicChatChunk
		}
		if cb.AnthropicStreamComplete != nil {
			o.Callbacks.AnthropicStreamComplete = cb.AnthropicStreamComplete
		}
	}
}

// WithChannelBufferSize overrides the response channel buffer size for supported providers.
func WithChannelBufferSize(size int) Option {
	return func(o *Options) {
		o.ChannelBufferSize = &size
	}
}

// WithExtraFields stores provider-specific extra fields for request payload customization.
func WithExtraFields(fields map[string]any) Option {
	return func(o *Options) {
		if o.ExtraFields == nil {
			o.ExtraFields = make(map[string]any)
		}
		for k, v := range fields {
			o.ExtraFields[k] = v
		}
	}
}

// WithEnableTokenTailoring toggles automatic token tailoring for supported providers.
func WithEnableTokenTailoring(enabled bool) Option {
	return func(o *Options) {
		o.EnableTokenTailoring = &enabled
	}
}

// WithMaxInputTokens sets the maximum input tokens when token tailoring is enabled.
func WithMaxInputTokens(limit int) Option {
	return func(o *Options) {
		o.MaxInputTokens = &limit
	}
}

// WithTokenCounter supplies a custom token counter for token tailoring.
func WithTokenCounter(counter model.TokenCounter) Option {
	return func(o *Options) {
		o.TokenCounter = counter
	}
}

// WithTailoringStrategy supplies a custom token tailoring strategy.
func WithTailoringStrategy(strategy model.TailoringStrategy) Option {
	return func(o *Options) {
		o.TailoringStrategy = strategy
	}
}

// WithOpenAIOption appends raw OpenAI options.
func WithOpenAIOption(opt ...openai.Option) Option {
	return func(o *Options) {
		o.OpenAIOption = append(o.OpenAIOption, opt...)
	}
}

// WithAnthropicOption appends raw Anthropic options.
func WithAnthropicOption(opt ...anthropic.Option) Option {
	return func(o *Options) {
		o.AnthropicOption = append(o.AnthropicOption, opt...)
	}
}
