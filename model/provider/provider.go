//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package provider provides a unified interface for constructing model.Model instances from different providers.
package provider

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/gemini"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan"
	"trpc.group/trpc-go/trpc-agent-go/model/ollama"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func init() {
	Register("openai", openaiProvider)
	Register("anthropic", anthropicProvider)
	Register("gemini", geminiProvider)
	Register("ollama", ollamaProvider)
	Register("hunyuan", hunyuanProvider)
}

// Provider builds a model.Model instance.
type Provider func(opts *Options) (model.Model, error)

var (
	providersMu sync.RWMutex                // providersMu guards providers access.
	providers   = make(map[string]Provider) // providers stores provider name to provider mappings.
)

// Register registers a provider by name.
func Register(name string, provider Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[name] = provider
}

// Get returns the provider by name or nil if not found.
func Get(name string) (Provider, bool) {
	providersMu.RLock()
	defer providersMu.RUnlock()
	provider, ok := providers[name]
	return provider, ok
}

// Model constructs a model.Model with the given provider name, model name and options.
func Model(providerName, modelName string, opt ...Option) (model.Model, error) {
	opts := &Options{
		ProviderName: providerName,
		ModelName:    modelName,
	}
	for _, o := range opt {
		o(opts)
	}
	provider, ok := Get(providerName)
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
	return provider(opts)
}

// openaiProvider builds an OpenAI-compatible model instance using the resolved options.
func openaiProvider(opts *Options) (model.Model, error) {
	var res []openai.Option
	if opts.APIKey != "" {
		res = append(res, openai.WithAPIKey(opts.APIKey))
	}
	if opts.BaseURL != "" {
		res = append(res, openai.WithBaseURL(opts.BaseURL))
	}
	var httpOpts []openai.HTTPClientOption
	if opts.HTTPClientName != "" {
		httpOpts = append(httpOpts, openai.WithHTTPClientName(opts.HTTPClientName))
	}
	if opts.HTTPClientTransport != nil {
		httpOpts = append(httpOpts, openai.WithHTTPClientTransport(opts.HTTPClientTransport))
	}
	if len(httpOpts) > 0 {
		res = append(res, openai.WithHTTPClientOptions(httpOpts...))
	}
	if len(opts.Headers) > 0 {
		res = append(res, openai.WithHeaders(opts.Headers))
	}
	if cb := opts.Callbacks; cb != nil {
		if cb.OpenAIChatRequest != nil {
			res = append(res, openai.WithChatRequestCallback(cb.OpenAIChatRequest))
		}
		if cb.OpenAIChatResponse != nil {
			res = append(res, openai.WithChatResponseCallback(cb.OpenAIChatResponse))
		}
		if cb.OpenAIChatChunk != nil {
			res = append(res, openai.WithChatChunkCallback(cb.OpenAIChatChunk))
		}
		if cb.OpenAIStreamComplete != nil {
			res = append(res, openai.WithChatStreamCompleteCallback(cb.OpenAIStreamComplete))
		}
	}
	if opts.ChannelBufferSize != nil {
		res = append(res, openai.WithChannelBufferSize(*opts.ChannelBufferSize))
	}
	if len(opts.ExtraFields) > 0 {
		res = append(res, openai.WithExtraFields(opts.ExtraFields))
	}
	if opts.EnableTokenTailoring != nil {
		res = append(res, openai.WithEnableTokenTailoring(*opts.EnableTokenTailoring))
	}
	if opts.MaxInputTokens != nil {
		res = append(res, openai.WithMaxInputTokens(*opts.MaxInputTokens))
	}
	if opts.TokenCounter != nil {
		res = append(res, openai.WithTokenCounter(opts.TokenCounter))
	}
	if opts.TailoringStrategy != nil {
		res = append(res, openai.WithTailoringStrategy(opts.TailoringStrategy))
	}
	if opts.TokenTailoringConfig != nil {
		res = append(res, openai.WithTokenTailoringConfig(opts.TokenTailoringConfig))
	}
	res = append(res, opts.OpenAIOption...)
	return openai.New(opts.ModelName, res...), nil
}

// anthropicProvider builds an Anthropic-compatible model instance using the resolved options.
func anthropicProvider(opts *Options) (model.Model, error) {
	var res []anthropic.Option
	if opts.APIKey != "" {
		res = append(res, anthropic.WithAPIKey(opts.APIKey))
	}
	if opts.BaseURL != "" {
		res = append(res, anthropic.WithBaseURL(opts.BaseURL))
	}
	var httpOpts []anthropic.HTTPClientOption
	if opts.HTTPClientName != "" {
		httpOpts = append(httpOpts, anthropic.WithHTTPClientName(opts.HTTPClientName))
	}
	if opts.HTTPClientTransport != nil {
		httpOpts = append(httpOpts, anthropic.WithHTTPClientTransport(opts.HTTPClientTransport))
	}
	if len(httpOpts) > 0 {
		res = append(res, anthropic.WithHTTPClientOptions(httpOpts...))
	}
	if len(opts.Headers) > 0 {
		res = append(res, anthropic.WithHeaders(opts.Headers))
	}
	if cb := opts.Callbacks; cb != nil {
		if cb.AnthropicChatRequest != nil {
			res = append(res, anthropic.WithChatRequestCallback(cb.AnthropicChatRequest))
		}
		if cb.AnthropicChatResponse != nil {
			res = append(res, anthropic.WithChatResponseCallback(cb.AnthropicChatResponse))
		}
		if cb.AnthropicChatChunk != nil {
			res = append(res, anthropic.WithChatChunkCallback(cb.AnthropicChatChunk))
		}
		if cb.AnthropicStreamComplete != nil {
			res = append(res, anthropic.WithChatStreamCompleteCallback(cb.AnthropicStreamComplete))
		}
	}
	if opts.ChannelBufferSize != nil {
		res = append(res, anthropic.WithChannelBufferSize(*opts.ChannelBufferSize))
	}
	if opts.EnableTokenTailoring != nil {
		res = append(res, anthropic.WithEnableTokenTailoring(*opts.EnableTokenTailoring))
	}
	if opts.MaxInputTokens != nil {
		res = append(res, anthropic.WithMaxInputTokens(*opts.MaxInputTokens))
	}
	if opts.TokenCounter != nil {
		res = append(res, anthropic.WithTokenCounter(opts.TokenCounter))
	}
	if opts.TailoringStrategy != nil {
		res = append(res, anthropic.WithTailoringStrategy(opts.TailoringStrategy))
	}
	if opts.TokenTailoringConfig != nil {
		res = append(res, anthropic.WithTokenTailoringConfig(opts.TokenTailoringConfig))
	}
	res = append(res, opts.AnthropicOption...)
	return anthropic.New(opts.ModelName, res...), nil
}

// geminiProvider builds an Gemini-compatible model instance using the resolved options.
func geminiProvider(opts *Options) (model.Model, error) {
	var res []gemini.Option
	if cb := opts.Callbacks; cb != nil {
		if cb.AnthropicChatRequest != nil {
			res = append(res, gemini.WithChatRequestCallback(cb.GeminiChatRequest))
		}
		if cb.AnthropicChatResponse != nil {
			res = append(res, gemini.WithChatResponseCallback(cb.GeminiChatResponse))
		}
		if cb.AnthropicChatChunk != nil {
			res = append(res, gemini.WithChatChunkCallback(cb.GeminiChatChunk))
		}
		if cb.AnthropicStreamComplete != nil {
			res = append(res, gemini.WithChatStreamCompleteCallback(cb.GeminiStreamComplete))
		}
	}
	if opts.ChannelBufferSize != nil {
		res = append(res, gemini.WithChannelBufferSize(*opts.ChannelBufferSize))
	}
	if opts.EnableTokenTailoring != nil {
		res = append(res, gemini.WithEnableTokenTailoring(*opts.EnableTokenTailoring))
	}
	if opts.MaxInputTokens != nil {
		res = append(res, gemini.WithMaxInputTokens(*opts.MaxInputTokens))
	}
	if opts.TokenCounter != nil {
		res = append(res, gemini.WithTokenCounter(opts.TokenCounter))
	}
	if opts.TailoringStrategy != nil {
		res = append(res, gemini.WithTailoringStrategy(opts.TailoringStrategy))
	}
	if opts.TokenTailoringConfig != nil {
		res = append(res, gemini.WithTokenTailoringConfig(opts.TokenTailoringConfig))
	}
	res = append(res, opts.GeminiOption...)
	return gemini.New(context.Background(), opts.ModelName, res...)
}

// ollamaProvider builds an Ollama-compatible model instance using the resolved options.
func ollamaProvider(opts *Options) (model.Model, error) {
	var res []ollama.Option
	if opts.BaseURL != "" {
		res = append(res, ollama.WithHost(opts.BaseURL))
	}
	if opts.ChannelBufferSize != nil {
		res = append(res, ollama.WithChannelBufferSize(*opts.ChannelBufferSize))
	}
	if cb := opts.Callbacks; cb != nil {
		if cb.OllamaChatRequest != nil {
			res = append(res, ollama.WithChatRequestCallback(cb.OllamaChatRequest))
		}
		if cb.OllamaChatResponse != nil {
			res = append(res, ollama.WithChatResponseCallback(cb.OllamaChatResponse))
		}
		if cb.OllamaChatChunk != nil {
			res = append(res, ollama.WithChatChunkCallback(cb.OllamaChatChunk))
		}
		if cb.OllamaStreamComplete != nil {
			res = append(res, ollama.WithChatStreamCompleteCallback(cb.OllamaStreamComplete))
		}
	}
	if opts.EnableTokenTailoring != nil {
		res = append(res, ollama.WithEnableTokenTailoring(*opts.EnableTokenTailoring))
	}
	if opts.MaxInputTokens != nil {
		res = append(res, ollama.WithMaxInputTokens(*opts.MaxInputTokens))
	}
	if opts.TokenCounter != nil {
		res = append(res, ollama.WithTokenCounter(opts.TokenCounter))
	}
	if opts.TailoringStrategy != nil {
		res = append(res, ollama.WithTailoringStrategy(opts.TailoringStrategy))
	}
	if opts.TokenTailoringConfig != nil {
		res = append(res, ollama.WithTokenTailoringConfig(opts.TokenTailoringConfig))
	}
	res = append(res, opts.OllamaOption...)
	return ollama.New(opts.ModelName, res...), nil
}

// hunyuanProvider builds a Hunyuan-compatible model instance using the resolved options.
func hunyuanProvider(opts *Options) (model.Model, error) {
	var res []hunyuan.Option
	if opts.BaseURL != "" {
		res = append(res, hunyuan.WithHost(opts.BaseURL))
	}
	if opts.ChannelBufferSize != nil {
		res = append(res, hunyuan.WithChannelBufferSize(*opts.ChannelBufferSize))
	}
	if cb := opts.Callbacks; cb != nil {
		if cb.HunyuanChatRequest != nil {
			res = append(res, hunyuan.WithChatRequestCallback(cb.HunyuanChatRequest))
		}
		if cb.HunyuanChatResponse != nil {
			res = append(res, hunyuan.WithChatResponseCallback(cb.HunyuanChatResponse))
		}
		if cb.HunyuanChatChunk != nil {
			res = append(res, hunyuan.WithChatChunkCallback(cb.HunyuanChatChunk))
		}
		if cb.HunyuanStreamComplete != nil {
			res = append(res, hunyuan.WithChatStreamCompleteCallback(cb.HunyuanStreamComplete))
		}
	}
	if opts.EnableTokenTailoring != nil {
		res = append(res, hunyuan.WithEnableTokenTailoring(*opts.EnableTokenTailoring))
	}
	if opts.MaxInputTokens != nil {
		res = append(res, hunyuan.WithMaxInputTokens(*opts.MaxInputTokens))
	}
	if opts.TokenCounter != nil {
		res = append(res, hunyuan.WithTokenCounter(opts.TokenCounter))
	}
	if opts.TailoringStrategy != nil {
		res = append(res, hunyuan.WithTailoringStrategy(opts.TailoringStrategy))
	}
	if opts.TokenTailoringConfig != nil {
		res = append(res, hunyuan.WithTokenTailoringConfig(opts.TokenTailoringConfig))
	}
	res = append(res, opts.HunyuanOption...)
	return hunyuan.New(opts.ModelName, res...), nil
}
