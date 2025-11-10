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
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func init() {
	Register("openai", openaiProvider)
	Register("anthropic", anthropicProvider)
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
	res = append(res, opts.AnthropicOption...)
	return anthropic.New(opts.ModelName, res...), nil
}
