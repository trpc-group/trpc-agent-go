//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package huggingface provides HuggingFace-compatible model implementations.
package huggingface

import (
	"context"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// defaultChannelBufferSize is the default channel buffer size.
	defaultChannelBufferSize = 256
	// defaultBaseURL is the default HuggingFace API base URL.
	defaultBaseURL = "https://router.huggingface.co"
	// defaultAPIKeyEnvVar is the default environment variable for HuggingFace API key.
	defaultAPIKeyEnvVar = "HUGGINGFACE_API_KEY"
)

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *ChatCompletionRequest,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *ChatCompletionRequest,
	chatResponse *ChatCompletionResponse,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *ChatCompletionRequest,
	chatChunk *ChatCompletionChunk,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
// This callback is invoked when streaming is completely finished (success or error).
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest *ChatCompletionRequest,
	streamErr error, // nil if streaming completed successfully
)

// options contains configuration options for creating a Model.
type options struct {
	// API key for the HuggingFace client.
	APIKey string
	// Base URL for the HuggingFace API. Default is https://router.huggingface.co.
	BaseURL string
	// Buffer size for response channels (default: 256).
	ChannelBufferSize int
	// HTTP client for making requests.
	HTTPClient *http.Client
	// Callback for the chat request.
	ChatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	ChatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	ChatChunkCallback ChatChunkCallbackFunc
	// Callback for the chat stream completion.
	ChatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	// Extra headers to be added to HTTP requests.
	ExtraHeaders map[string]string
	// Extra fields to be added to the HTTP request body.
	ExtraFields map[string]any
	// EnableTokenTailoring enables automatic token tailoring based on model context window.
	EnableTokenTailoring bool
	// TokenCounter count tokens for token tailoring.
	TokenCounter model.TokenCounter
	// TailoringStrategy defines the strategy for token tailoring.
	TailoringStrategy model.TailoringStrategy
	// MaxInputTokens is the max input tokens for token tailoring.
	MaxInputTokens int
	// TokenTailoringConfig allows customization of token tailoring parameters.
	TokenTailoringConfig *model.TokenTailoringConfig
}

var (
	defaultOptions = options{
		BaseURL:           defaultBaseURL,
		ChannelBufferSize: defaultChannelBufferSize,
		TokenCounter:      model.NewSimpleTokenCounter(),
		TokenTailoringConfig: &model.TokenTailoringConfig{
			ProtocolOverheadTokens: 512,
			ReserveOutputTokens:    2048,
			SafetyMarginRatio:      0.1,
			InputTokensFloor:       1024,
			OutputTokensFloor:      512,
			MaxInputTokensRatio:    0.8,
		},
	}
)

// Option is a function that configures a HuggingFace model.
type Option func(*options)

// WithAPIKey sets the API key for the HuggingFace client.
func WithAPIKey(key string) Option {
	return func(opts *options) {
		opts.APIKey = key
	}
}

// WithBaseURL sets the base URL for the HuggingFace API.
func WithBaseURL(url string) Option {
	return func(opts *options) {
		opts.BaseURL = url
	}
}

// WithChannelBufferSize sets the channel buffer size for the HuggingFace client.
func WithChannelBufferSize(size int) Option {
	return func(opts *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		opts.ChannelBufferSize = size
	}
}

// WithHTTPClient sets the HTTP client for making requests.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) {
		opts.HTTPClient = client
	}
}

// WithChatRequestCallback sets the function to be called before sending a chat request.
func WithChatRequestCallback(fn ChatRequestCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatRequestCallback = fn
	}
}

// WithChatResponseCallback sets the function to be called after receiving a chat response.
// Used for non-streaming responses.
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
// Used for streaming responses.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatChunkCallback = fn
	}
}

// WithChatStreamCompleteCallback sets the function to be called when streaming is completed.
// Called for both successful and failed streaming completions.
func WithChatStreamCompleteCallback(fn ChatStreamCompleteCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatStreamCompleteCallback = fn
	}
}

// WithExtraHeaders sets extra headers to be added to HTTP requests.
func WithExtraHeaders(headers map[string]string) Option {
	return func(opts *options) {
		if opts.ExtraHeaders == nil {
			opts.ExtraHeaders = make(map[string]string)
		}
		for k, v := range headers {
			opts.ExtraHeaders[k] = v
		}
	}
}

// WithExtraFields sets extra fields to be added to the HTTP request body.
// These fields will be included in every chat completion request.
func WithExtraFields(extraFields map[string]any) Option {
	return func(opts *options) {
		if opts.ExtraFields == nil {
			opts.ExtraFields = make(map[string]any)
		}
		for k, v := range extraFields {
			opts.ExtraFields[k] = v
		}
	}
}

// WithEnableTokenTailoring enables automatic token tailoring based on model context window.
// When enabled, the system will automatically calculate max input tokens using the model's.
// context window minus reserved tokens and protocol overhead.
func WithEnableTokenTailoring(enabled bool) Option {
	return func(opts *options) {
		opts.EnableTokenTailoring = enabled
	}
}

// WithMaxInputTokens sets only the input token limit for token tailoring.
// The counter/strategy will be lazily initialized if not provided.
// Defaults to SimpleTokenCounter and MiddleOutStrategy.
func WithMaxInputTokens(limit int) Option {
	return func(opts *options) {
		opts.MaxInputTokens = limit
	}
}

// WithTokenCounter sets the TokenCounter used for token tailoring.
// If not provided and token limit is enabled, a SimpleTokenCounter will be used.
func WithTokenCounter(counter model.TokenCounter) Option {
	return func(opts *options) {
		if counter == nil {
			return
		}
		opts.TokenCounter = counter
	}
}

// WithTailoringStrategy sets the TailoringStrategy used for token tailoring.
// If not provided and token limit is enabled, a MiddleOutStrategy will be used.
func WithTailoringStrategy(strategy model.TailoringStrategy) Option {
	return func(opts *options) {
		opts.TailoringStrategy = strategy
	}
}

// WithTokenTailoringConfig sets custom token tailoring budget parameters.
// This allows advanced users to fine-tune the token allocation strategy.
func WithTokenTailoringConfig(config *model.TokenTailoringConfig) Option {
	return func(opts *options) {
		if config == nil {
			return
		}
		if config.ProtocolOverheadTokens <= 0 {
			config.ProtocolOverheadTokens = 512
		}
		if config.ReserveOutputTokens <= 0 {
			config.ReserveOutputTokens = 2048
		}
		if config.SafetyMarginRatio <= 0 {
			config.SafetyMarginRatio = 0.1
		}
		if config.InputTokensFloor <= 0 {
			config.InputTokensFloor = 1024
		}
		if config.OutputTokensFloor <= 0 {
			config.OutputTokensFloor = 512
		}
		if config.MaxInputTokensRatio <= 0 {
			config.MaxInputTokensRatio = 0.8
		}
		opts.TokenTailoringConfig = config
	}
}
