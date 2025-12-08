//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package ollama provides Ollama-compatible model implementations.
package ollama

import (
	"context"
	"net/http"
	"time"

	"github.com/ollama/ollama/api"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

const (
	defaultChannelBufferSize = 256
	functionToolType         = "function"
	// OllamaHost is the environment variable for the Ollama host.
	OllamaHost = "OLLAMA_HOST"
)

var (
	// DefaultHost is the default Ollama host port.
	defaultPort = "11434"
	// DefaultHost is the default Ollama host.
	defaultHost = "http://localhost:11434"
)

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *api.ChatRequest,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *api.ChatRequest,
	chatResponse *api.ChatResponse,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *api.ChatRequest,
	chatChunk *api.ChatResponse,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest *api.ChatRequest,
	streamErr error,
)

// options contains configuration options for creating an Ollama model.
type options struct {
	// Host URL for the Ollama server.
	host string
	// HTTP client for the Ollama client.
	httpClient *http.Client
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
	// Additional options for Ollama API.
	options   map[string]any
	keepAlive *api.Duration
}

var (
	defaultOptions = options{
		channelBufferSize: defaultChannelBufferSize,
		httpClient:        http.DefaultClient,
		tokenTailoringConfig: &model.TokenTailoringConfig{
			ProtocolOverheadTokens: imodel.DefaultProtocolOverheadTokens,
			ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
			SafetyMarginRatio:      imodel.DefaultSafetyMarginRatio,
			InputTokensFloor:       imodel.DefaultInputTokensFloor,
			OutputTokensFloor:      imodel.DefaultOutputTokensFloor,
			MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
		},
		host:         defaultHost,
		tokenCounter: model.NewSimpleTokenCounter(),
	}
)

// Option is a function that configures an Ollama model.
type Option func(*options)

// WithHost sets the host URL for the Ollama server.
func WithHost(host string) Option {
	return func(o *options) {
		o.host = host
	}
}

// withHttpClient sets the HTTP client to use.
// The site is temporarily not open to the public, as we may implement injection of an internal http client.
func withHttpClient(client *http.Client) Option {
	return func(o *options) {
		o.httpClient = client
	}
}

// WithChannelBufferSize sets the channel buffer size for the Ollama client, 256 by default.
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
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.chatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.chatChunkCallback = fn
	}
}

// WithChatStreamCompleteCallback sets the function to be called when streaming is completed.
func WithChatStreamCompleteCallback(fn ChatStreamCompleteCallbackFunc) Option {
	return func(opts *options) {
		opts.chatStreamCompleteCallback = fn
	}
}

// WithEnableTokenTailoring enables automatic token tailoring based on model context window.
func WithEnableTokenTailoring(enabled bool) Option {
	return func(opts *options) {
		opts.enableTokenTailoring = enabled
	}
}

// WithMaxInputTokens sets only the input token limit for token tailoring.
func WithMaxInputTokens(limit int) Option {
	return func(opts *options) {
		opts.maxInputTokens = limit
	}
}

// WithTokenCounter sets the TokenCounter used for token tailoring.
func WithTokenCounter(counter model.TokenCounter) Option {
	return func(opts *options) {
		if counter == nil {
			return
		}
		opts.tokenCounter = counter
	}
}

// WithTailoringStrategy sets the TailoringStrategy used for token tailoring.
func WithTailoringStrategy(strategy model.TailoringStrategy) Option {
	return func(opts *options) {
		opts.tailoringStrategy = strategy
	}
}

// WithTokenTailoringConfig sets custom token tailoring budget parameters.
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

// WithOptions sets additional options for Ollama API.
func WithOptions(opt map[string]any) Option {
	return func(opts *options) {
		opts.options = opt
	}
}

// WithKeepAlive sets the keep alive duration for the Ollama API.
func WithKeepAlive(duration time.Duration) Option {
	return func(opts *options) {
		d := api.Duration{Duration: duration}
		opts.keepAlive = &d
	}
}
