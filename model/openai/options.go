//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openai provides OpenAI-compatible model implementations.
package openai

import (
	"context"

	openai "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

const (
	// defaultChannelBufferSize is the default channel buffer size.
	defaultChannelBufferSize = 256
	// defaultBatchCompletionWindow is the default batch completion window.
	defaultBatchCompletionWindow = "24h"
)

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatResponse *openai.ChatCompletion,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatChunk *openai.ChatCompletionChunk,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
// This callback is invoked when streaming is completely finished (success or error).
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	accumulator *openai.ChatCompletionAccumulator, // nil if streamErr is not nil
	streamErr error, // nil if streaming completed successfully
)

// options contains configuration options for creating a Model.
type options struct {
	// API key for the OpenAI client.
	APIKey string
	// Base URL for the OpenAI client. It is optional for OpenAI-compatible APIs.
	BaseURL string
	// Buffer size for response channels (default: 256)
	ChannelBufferSize int
	// Options for the HTTP client.
	HTTPClientOptions []HTTPClientOption
	// Callback for the chat request.
	ChatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	ChatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	ChatChunkCallback ChatChunkCallbackFunc
	// Callback for the chat stream completion.
	ChatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	// Options for the OpenAI client.
	OpenAIOptions []openaiopt.RequestOption
	// Extra fields to be added to the HTTP request body.
	ExtraFields map[string]any
	// Variant for model-specific behavior.
	Variant Variant
	// Batch completion window for batch processing.
	BatchCompletionWindow openai.BatchNewParamsCompletionWindow
	// Batch metadata for batch processing.
	BatchMetadata map[string]string
	// BatchBaseURL overrides the base URL for batch requests (batches/files).
	BatchBaseURL string
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
	// ShowToolCallDelta controls whether to expose tool call
	// deltas in streaming responses. When true, raw tool_call
	// chunks from the provider will be forwarded via
	// Response.Choices[].Delta.ToolCalls instead of being
	// suppressed until the final aggregated response.
	ShowToolCallDelta    bool
	accumulateChunkUsage AccumulateChunkUsage
}

var (
	defaultOptions = options{
		Variant:               VariantOpenAI, // The default variant is VariantOpenAI.
		ChannelBufferSize:     defaultChannelBufferSize,
		BatchCompletionWindow: defaultBatchCompletionWindow,
		TokenCounter:          model.NewSimpleTokenCounter(),
		TokenTailoringConfig: &model.TokenTailoringConfig{
			ProtocolOverheadTokens: imodel.DefaultProtocolOverheadTokens,
			ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
			SafetyMarginRatio:      imodel.DefaultSafetyMarginRatio,
			InputTokensFloor:       imodel.DefaultInputTokensFloor,
			OutputTokensFloor:      imodel.DefaultOutputTokensFloor,
			MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
		},
	}
)

// Option is a function that configures an OpenAI model.
type Option func(*options)

// WithAPIKey sets the API key for the OpenAI client.
func WithAPIKey(key string) Option {
	return func(opts *options) {
		opts.APIKey = key
	}
}

// WithBaseURL sets the base URL for the OpenAI client.
func WithBaseURL(url string) Option {
	return func(opts *options) {
		opts.BaseURL = url
	}
}

// WithChannelBufferSize sets the channel buffer size for the OpenAI client.
func WithChannelBufferSize(size int) Option {
	return func(opts *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		opts.ChannelBufferSize = size
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

// WithHTTPClientOptions sets the HTTP client options for the OpenAI client.
func WithHTTPClientOptions(httpOpts ...HTTPClientOption) Option {
	return func(opts *options) {
		opts.HTTPClientOptions = httpOpts
	}
}

// WithOpenAIOptions sets the OpenAI options for the OpenAI client.
// E.g. use its middleware option:
//
//	import (
//		openai "github.com/openai/openai-go"
//		openaiopt "github.com/openai/openai-go/option"
//	)
//
//	WithOpenAIOptions(openaiopt.WithMiddleware(
//		func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
//			// do something
//			return next(req)
//		}
//	)))
func WithOpenAIOptions(openaiOpts ...openaiopt.RequestOption) Option {
	return func(opts *options) {
		opts.OpenAIOptions = append(opts.OpenAIOptions, openaiOpts...)
	}
}

// WithHeaders appends static HTTP headers to all OpenAI requests.
func WithHeaders(headers map[string]string) Option {
	return func(opts *options) {
		if len(headers) == 0 {
			return
		}
		for k, v := range headers {
			opts.OpenAIOptions = append(opts.OpenAIOptions, openaiopt.WithHeader(k, v))
		}
	}
}

// WithExtraFields sets extra fields to be added to the HTTP request body.
// These fields will be included in every chat completion request.
// E.g.:
//
//	WithExtraFields(map[string]any{
//		"custom_metadata": map[string]string{
//			"session_id": "abc",
//		},
//	})
//
// and "session_id" : "abc" will be added to the HTTP request json body.
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

// WithVariant sets the model variant for specific behavior.
// The default variant is VariantOpenAI.
// Optional variants are:
// - VariantHunyuan: Hunyuan variant with specific file handling.
func WithVariant(variant Variant) Option {
	return func(opts *options) {
		opts.Variant = variant
	}
}

// WithBatchCompletionWindow sets the batch completion window.
func WithBatchCompletionWindow(window openai.BatchNewParamsCompletionWindow) Option {
	return func(opts *options) {
		if window == "" {
			window = defaultBatchCompletionWindow
		}
		opts.BatchCompletionWindow = window
	}
}

// WithBatchMetadata sets the batch metadata.
func WithBatchMetadata(metadata map[string]string) Option {
	return func(opts *options) {
		opts.BatchMetadata = metadata
	}
}

// WithBatchBaseURL sets a base URL override for batch requests (batches/files).
// When set, batch operations will use this base URL via per-request override.
func WithBatchBaseURL(url string) Option {
	return func(opts *options) {
		opts.BatchBaseURL = url
	}
}

// WithEnableTokenTailoring enables automatic token tailoring based on model context window.
// When enabled, the system will automatically calculate max input tokens using the model's
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

// AccumulateChunkUsage is the function type for accumulating chunk usage.
type AccumulateChunkUsage func(u model.Usage, delta model.Usage) model.Usage

// WithAccumulateChunkTokenUsage sets the function to be called to accumulate chunk token usage.
func WithAccumulateChunkTokenUsage(a AccumulateChunkUsage) Option {
	return func(opts *options) {
		opts.accumulateChunkUsage = a
	}
}

// inverseOPENAISKDAddChunkUsage calculates the inverse of OPENAISKDAddChunkUsage, related to the current openai sdk version
func inverseOPENAISKDAddChunkUsage(u model.Usage, delta model.Usage) model.Usage {
	return model.Usage{
		PromptTokens:     u.PromptTokens - delta.PromptTokens,
		CompletionTokens: u.CompletionTokens - delta.CompletionTokens,
		TotalTokens:      u.TotalTokens - delta.TotalTokens,
	}
}

// completionUsageToModelUsage converts openai.CompletionUsage to model.Usage.
func completionUsageToModelUsage(usage openai.CompletionUsage) model.Usage {
	return model.Usage{
		PromptTokens:     int(usage.PromptTokens),
		CompletionTokens: int(usage.CompletionTokens),
		TotalTokens:      int(usage.TotalTokens),
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: int(usage.PromptTokensDetails.CachedTokens),
		},
	}
}

// modelUsageToCompletionUsage converts model.Usage to openai.CompletionUsage.
func modelUsageToCompletionUsage(usage model.Usage) openai.CompletionUsage {
	return openai.CompletionUsage{
		PromptTokens:     int64(usage.PromptTokens),
		CompletionTokens: int64(usage.CompletionTokens),
		TotalTokens:      int64(usage.TotalTokens),
		PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
			CachedTokens: int64(usage.PromptTokensDetails.CachedTokens),
		},
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
//
// Example:
//
//	openai.WithTokenTailoringConfig(&model.TokenTailoringConfig{
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
		opts.TokenTailoringConfig = config
	}
}

// WithShowToolCallDelta controls whether to expose tool call
// deltas in streaming responses. When enabled, the model will
// forward provider tool_call chunks via Delta.ToolCalls so
// callers can reconstruct arguments incrementally.
func WithShowToolCallDelta(show bool) Option {
	return func(opts *options) {
		opts.ShowToolCallDelta = show
	}
}
