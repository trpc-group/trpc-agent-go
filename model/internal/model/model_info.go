//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides model-related functionality for internal usage.
package model

import (
	"strings"
	"sync"
)

// defaultContextWindow is the fallback context window size (tokens) when model is unknown.
const defaultContextWindow = 8192

// ModelMutex guards modelContextWindows.
var ModelMutex sync.RWMutex

// ModelContextWindows holds known model name -> context window size mappings (tokens).
var ModelContextWindows = map[string]int{
	// OpenAI O-series
	"o1-preview": 128000,
	"o1-mini":    128000,
	"o1":         200000,
	"o3-mini":    200000,
	"o3":         200000,
	"o4-mini":    200000,

	// OpenAI GPT-5
	"gpt-5":      400000,
	"gpt-5-mini": 400000,
	"gpt-5-nano": 400000,

	// OpenAI GPT-4.1
	"gpt-4.1":      1047576,
	"gpt-4.1-mini": 1047576,
	"gpt-4.1-nano": 1047576,

	// OpenAI GPT-4.5
	"gpt-4.5-preview": 128000,

	// OpenAI GPT-4o
	"gpt-4o":      128000,
	"gpt-4o-mini": 200000,

	// OpenAI GPT-4
	"gpt-4":       8192,
	"gpt-4-turbo": 128000,
	"gpt-4-32k":   32768,

	// OpenAI GPT-3.5
	"gpt-3.5-turbo":          16385,
	"gpt-3.5-turbo-instruct": 4096,
	"gpt-3.5-turbo-16k":      16385,

	// OpenAI Legacy
	"text-davinci-003": 4097,
	"text-davinci-002": 4097,
	"code-davinci-002": 8001,
	"code-davinci-001": 8001,
	"text-ada-001":     2049,
	"text-babbage-001": 2040,
	"text-curie-001":   2049,
	"code-cushman-002": 2048,
	"code-cushman-001": 2048,
	"ada":              2049,
	"babbage":          2049,
	"curie":            2049,
	"davinci":          2049,

	// Anthropic Claude 4
	"claude-4-opus":   200000,
	"claude-4-sonnet": 200000,

	// Anthropic Claude 3.7
	"claude-3-7-sonnet": 200000,

	// Anthropic Claude 3.5
	"claude-3-5-sonnet": 200000,
	"claude-3-5-haiku":  200000,

	// Anthropic Claude 3
	"claude-3-opus":   200000,
	"claude-3-sonnet": 200000,
	"claude-3-haiku":  200000,

	// Anthropic Claude Legacy
	"claude-2.1":         200000,
	"claude-2.0":         100000,
	"claude-instant-1.2": 100000,

	// Google Gemini 2.5
	"gemini-2.5-pro":   2097152,
	"gemini-2.5-flash": 1048576,

	// Google Gemini 2.0
	"gemini-2.0-flash": 1048576,

	// Google Gemini 1.5
	"gemini-1.5-pro":      2097152,
	"gemini-1.5-flash":    1048576,
	"gemini-1.5-flash-8b": 1048576,

	// Google Gemma
	"gemma-3-27b-it": 128000,
	"gemma-3-12b-it": 128000,
	"gemma-3-4b-it":  128000,
	"gemma-3-1b-it":  32000,
	"gemma2-9b-it":   8192,
	"gemma-7b-it":    8192,

	// Meta Llama 4
	"llama-4-scout":    128000,
	"llama-4-maverick": 128000,

	// Meta Llama 3.3
	"llama-3.3-70b-instruct":  128000,
	"llama-3.3-8b-instruct":   128000,
	"llama-3.3-70b-versatile": 128000,

	// Meta Llama 3.2
	"llama-3.2-90b-vision-instruct": 16384,
	"llama-3.2-90b-text-preview":    8192,
	"llama-3.2-11b-vision-instruct": 16384,
	"llama-3.2-11b-text-preview":    8192,
	"llama-3.2-3b-preview":          8192,
	"llama-3.2-3b-instruct":         4096,
	"llama-3.2-1b-preview":          8192,
	"llama-3.2-1b-instruct":         16384,

	// Meta Llama 3.1
	"llama-3.1-405b-instruct": 8192,
	"llama-3.1-70b-instruct":  128000,
	"llama-3.1-70b-versatile": 131072,
	"llama-3.1-8b-instruct":   128000,
	"llama-3.1-8b-instant":    131072,

	// Meta Llama 3.0
	"llama3-70b-8192": 8192,
	"llama3-8b-8192":  8192,

	// Mistral
	"mistral-large-latest":  32768,
	"mistral-medium-latest": 32768,
	"mistral-small-latest":  32768,
	"mistral-tiny":          32768,
	"mistral-7b-instruct":   32000,

	// Mixtral
	"mixtral-8x7b-instruct": 32000,
	"mixtral-8x7b-32768":    32768,

	// Qwen
	"qwen2.5-72b-instruct":       8192,
	"qwen2.5-14b-instruct":       128000,
	"qwen2.5-7b-instruct":        32000,
	"qwen2.5-coder-32b-instruct": 8192,
	"qwq-32b-preview":            8192,

	// DeepSeek
	"deepseek-chat":     131072,
	"deepseek-reasoner": 131072,

	// Amazon
	"nova-pro-v1":        300000,
	"nova-micro-v1":      128000,
	"nova-lite-v1":       300000,
	"titan-text-express": 8000,
	"titan-text-lite":    4000,

	// AI21
	"jamba-instruct": 256000,
	"j2-ultra":       8191,
	"j2-mid":         8191,

	// Cohere
	"command-text": 4000,

	// Tencent Hunyuan
	// ref: https://hunyuan.cloud.tencent.com/#/app/modelSquare
	"hunyuan-translation":  8192,
	"hunyuan-2.0-instruct": 147456,
	"hunyuan-2.0-thinking": 196608,
	"hunyuan-t1":           65536,
	"hunyuan-turbos":       32768,
	"hunyuan-a13b":         229376,
}

// ResolveContextWindow returns the context window size for a given model name.
// - Exact match (case-insensitive) first
// - Optional: prefix-based fallback (simple heuristic)
// - Fallback to defaultContextWindow
func ResolveContextWindow(modelName string) int {
	if modelName == "" {
		return defaultContextWindow
	}

	ModelMutex.RLock()
	defer ModelMutex.RUnlock()

	key := strings.ToLower(modelName)
	if w, ok := ModelContextWindows[key]; ok {
		return w
	}
	// Simple prefix heuristic.
	for k, w := range ModelContextWindows {
		if strings.HasPrefix(key, k) {
			return w
		}
	}
	return defaultContextWindow
}

// GetAllModelContextWindows returns a copy of all model context window mappings.
// This is useful for debugging and testing.
func GetAllModelContextWindows() map[string]int {
	ModelMutex.RLock()
	defer ModelMutex.RUnlock()

	result := make(map[string]int, len(ModelContextWindows))
	for k, v := range ModelContextWindows {
		result[k] = v
	}
	return result
}
