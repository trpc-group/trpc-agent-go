//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hunyuan

import (
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

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
		tokenCounter: model.NewSimpleTokenCounter(),
	}
)

// Option is a function that configures a Hunyuan model.
type Option func(*options)

// WithSecretId sets the secret ID for Hunyuan API authentication.
func WithSecretId(secretId string) Option {
	return func(o *options) {
		o.secretId = secretId
	}
}

// WithSecretKey sets the secret key for Hunyuan API authentication.
func WithSecretKey(secretKey string) Option {
	return func(o *options) {
		o.secretKey = secretKey
	}
}

// WithBaseUrl sets the base URL for the Hunyuan server.
func WithBaseUrl(baseUrl string) Option {
	return func(o *options) {
		o.baseUrl = baseUrl
	}
}

// WithHost sets the host for the Hunyuan server.
func WithHost(host string) Option {
	return func(o *options) {
		o.host = host
	}
}

// WithHttpClient sets the HTTP client to use.
func WithHttpClient(client *http.Client) Option {
	return func(o *options) {
		o.httpClient = client
	}
}

// WithChannelBufferSize sets the channel buffer size for the Hunyuan client, 256 by default.
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
