//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini provides Gemini-compatible model implementations.
package gemini

import (
	"context"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

const (
	defaultChannelBufferSize = 256
)

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest []*genai.Content,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest []*genai.Content,
	generateConfig *genai.GenerateContentConfig,
	chatResponse *genai.GenerateContentResponse,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest []*genai.Content,
	generateConfig *genai.GenerateContentConfig,
	chatResponse *genai.GenerateContentResponse,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
// This callback is invoked when streaming is completely finished (success or error).
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest []*genai.Content,
	generateConfig *genai.GenerateContentConfig,
	chatResponse *model.Response,
)

// options contains configuration options for creating an Anthropic model.
type options struct {
	// Buffer size for response channels (default: 256)
	channelBufferSize int
	// Callback for the chat request.
	chatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	chatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	chatChunkCallback ChatChunkCallbackFunc
	// Callback for the chat stream completion.
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	// enableTokenTailoring enables automatic token tailoring based on model context window.
	enableTokenTailoring bool
	// tokenCounter count tokens for token tailoring.
	tokenCounter model.TokenCounter
	// tailoringStrategy defines the strategy for token tailoring.
	tailoringStrategy model.TailoringStrategy
	// maxInputTokens is the max input tokens for token tailoring.
	maxInputTokens int
	// tokenTailoringConfig allows customization of token tailoring parameters.
	tokenTailoringConfig *model.TokenTailoringConfig
	// geminiClientConfig for building gemini client.
	geminiClientConfig *genai.ClientConfig
}

var (
	defaultOptions = options{
		channelBufferSize: defaultChannelBufferSize,
		tokenTailoringConfig: &model.TokenTailoringConfig{
			ProtocolOverheadTokens: imodel.DefaultProtocolOverheadTokens,
			ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
			SafetyMarginRatio:      imodel.DefaultSafetyMarginRatio,
			InputTokensFloor:       imodel.DefaultInputTokensFloor,
			OutputTokensFloor:      imodel.DefaultOutputTokensFloor,
			MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
		},
		tokenCounter: model.NewSimpleTokenCounter(),
	}
)

// Option is a function that configures an Anthropic model.
type Option func(*options)

// WithChannelBufferSize sets the channel buffer size for the Anthropic client, 256 by default.
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithChatRequestCallback sets the function to be called before sending a chat request.
func WithChatRequestCallback(fn ChatRequestCallbackFunc) Option {
	return func(opts *options) {
		opts.chatRequestCallback = fn
	}
}

// WithChatResponseCallback sets the function to be called after receiving a chat response.
// Used for non-streaming responses.
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.chatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
// Used for streaming responses.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.chatChunkCallback = fn
	}
}

// WithChatStreamCompleteCallback sets the function to be called when streaming is completed.
// Called for both successful and failed streaming completions.
func WithChatStreamCompleteCallback(fn ChatStreamCompleteCallbackFunc) Option {
	return func(opts *options) {
		opts.chatStreamCompleteCallback = fn
	}
}

// WithEnableTokenTailoring enables automatic token tailoring based on model context window.
// When enabled, the system will automatically calculate max input tokens using the model's
// context window minus reserved tokens and protocol overhead.
func WithEnableTokenTailoring(enabled bool) Option {
	return func(opts *options) {
		opts.enableTokenTailoring = enabled
	}
}

// WithMaxInputTokens sets only the input token limit for token tailoring.
// The counter/strategy will be lazily initialized if not provided.
// Defaults to SimpleTokenCounter and MiddleOutStrategy.
func WithMaxInputTokens(limit int) Option {
	return func(opts *options) {
		opts.maxInputTokens = limit
	}
}

// WithTokenCounter sets the TokenCounter used for token tailoring.
// If not provided and token limit is enabled, a SimpleTokenCounter will be used.
func WithTokenCounter(counter model.TokenCounter) Option {
	return func(opts *options) {
		if counter == nil {
			return
		}
		opts.tokenCounter = counter
	}
}

// WithTailoringStrategy sets the TailoringStrategy used for token tailoring.
// If not provided and token limit is enabled, a MiddleOutStrategy will be used.
func WithTailoringStrategy(strategy model.TailoringStrategy) Option {
	return func(opts *options) {
		opts.tailoringStrategy = strategy
	}
}

// WithTokenTailoringConfig sets custom token tailoring budget parameters.
// This allows advanced users to fine-tune the token allocation strategy.
//
// Example:
//
//	anthropic.WithTokenTailoringConfig(&model.TokenTailoringConfig{
//	    ProtocolOverheadTokens: 1024,
//	    ReserveOutputTokens:    4096,
//	    SafetyMarginRatio:      0.15,
//	})
//
// Note: It is recommended to use the default values unless you have specific
// requirements.
func WithTokenTailoringConfig(config *model.TokenTailoringConfig) Option {
	return func(opts *options) {
		if config == nil {
			return
		}
		if config.ProtocolOverheadTokens <= 0 {
			config.ProtocolOverheadTokens = imodel.DefaultProtocolOverheadTokens
		}
		if config.ReserveOutputTokens <= 0 {
			config.ReserveOutputTokens = imodel.DefaultReserveOutputTokens
		}
		if config.SafetyMarginRatio <= 0 {
			config.SafetyMarginRatio = imodel.DefaultSafetyMarginRatio
		}
		if config.InputTokensFloor <= 0 {
			config.InputTokensFloor = imodel.DefaultInputTokensFloor
		}
		if config.OutputTokensFloor <= 0 {
			config.OutputTokensFloor = imodel.DefaultOutputTokensFloor
		}
		if config.MaxInputTokensRatio <= 0 {
			config.MaxInputTokensRatio = imodel.DefaultMaxInputTokensRatio
		}
		opts.tokenTailoringConfig = config
	}
}

// WithGeminiClientConfig sets the ClientConfig used for gemini Client initialization.
func WithGeminiClientConfig(c *genai.ClientConfig) Option {
	return func(opts *options) {
		opts.geminiClientConfig = c
	}
}
