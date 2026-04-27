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

	// OpenAI GPT-5.4
	// ref: https://platform.openai.com/docs/models/compare?model=gpt-5.4
	// ref: https://platform.openai.com/docs/models/gpt-5.4-pro
	"gpt-5.4":      1050000,
	"gpt-5.4-pro":  1050000,
	"gpt-5.4-mini": 400000,
	"gpt-5.4-nano": 400000,

	// OpenAI GPT-5.2
	"gpt-5.2":           400000,
	"gpt-5.2-instant":   400000,
	"gpt-5.2-codex-max": 400000,
	"gpt-5.2-mini":      400000,
	"gpt-5.2-nano":      400000,

	// OpenAI GPT-5.1
	"gpt-5.1":           400000,
	"gpt-5.1-instant":   400000,
	"gpt-5.1-codex-max": 400000,
	"gpt-5.1-mini":      400000,
	"gpt-5.1-nano":      400000,

	// OpenAI GPT-5
	"gpt-5":      400000,
	"gpt-5-mini": 400000,
	"gpt-5-nano": 400000,

	// OpenAI GPT-4.5
	"gpt-4.5-preview": 128000,

	// OpenAI GPT-4.1
	"gpt-4.1":      1047576,
	"gpt-4.1-mini": 1047576,
	"gpt-4.1-nano": 1047576,

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

	// Anthropic Claude
	// ref: https://docs.anthropic.com/en/about-claude/models/overview
	// ref: https://docs.anthropic.com/en/docs/build-with-claude/context-windows

	// Anthropic Claude 4.6
	"claude-4.6-opus":   1000000,
	"claude-opus-4-6":   1000000,
	"claude-4.6-sonnet": 1000000,
	"claude-sonnet-4-6": 1000000,

	// Anthropic Claude 4.5
	// Claude Sonnet 4.5 defaults to 200k. 1M requires the
	// context-1m-2025-08-07 beta header.
	"claude-4.5-opus":   200000,
	"claude-opus-4-5":   200000,
	"claude-4.5-sonnet": 200000,
	"claude-sonnet-4-5": 200000,
	"claude-4.5-haiku":  200000,
	"claude-haiku-4-5":  200000,

	// Anthropic Claude 4
	"claude-4-opus":   200000,
	"claude-opus-4":   200000,
	"claude-sonnet-4": 200000,
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

	// Google Gemini 3.0
	"gemini-3.0-pro":   2097152,
	"gemini-3.0-flash": 1048576,

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

	// Alibaba Qwen / QwQ
	// ref: https://help.aliyun.com/zh/model-studio/user-guide/models
	// ref: https://help.aliyun.com/zh/model-studio/developer-reference/what-is-qwen-llm
	"qwen3-max":     262144,
	"qwen3.5-plus":  1000000,
	"qwen3.5-flash": 1000000,
	"qwen-max":      131072,
	"qwen-plus":     1000000,
	// qwen-turbo supports 1,000,000 in non-thinking mode but a smaller
	// 131,072 window in thinking mode. Use the smaller documented limit as
	// the safe default for token budgeting.
	"qwen-turbo":                 131072,
	"qwen2.5-72b-instruct":       8192,
	"qwen2.5-14b-instruct":       128000,
	"qwen2.5-7b-instruct":        32000,
	"qwen2.5-coder-32b-instruct": 8192,
	"qwq-32b-preview":            8192,

	// DeepSeek
	// ref: https://api-docs.deepseek.com/quick_start/pricing
	// ref: https://api-docs.deepseek.com/guides/thinking_mode
	// Note: per the official pricing page, "deepseek-chat" and
	// "deepseek-reasoner" are deprecated aliases that will be routed to
	// "deepseek-v4-flash" (non-thinking and thinking mode respectively).
	// During the compatibility period they may still be served by older
	// instances, so we keep their previously documented 131072 window
	// here as a conservative default.
	"deepseek-chat":     131072,
	"deepseek-reasoner": 131072,
	"deepseek-v4-pro":   1000000,
	"deepseek-v4-flash": 1000000,

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

	// Z.AI GLM
	// ref: https://docs.z.ai/guides/llm/glm-5
	// ref: https://docs.z.ai/guides/llm/glm-4.6
	// ref: https://docs.z.ai/guides/llm/glm-4.7
	// ref: https://docs.z.ai/devpack/using5.1
	"glm-5":          200000,
	"glm-5.1":        204800,
	"glm-4.7":        200000,
	"glm-4.7-flashx": 200000,
	"glm-4.7-flash":  200000,
	"glm-4.6":        200000,

	// Z.AI GLM (Hugging Face repository IDs; context windows follow the
	// matching model-family docs above)
	// ref: https://huggingface.co/zai-org/GLM-5
	// ref: https://huggingface.co/zai-org/GLM-4.7
	// ref: https://huggingface.co/zai-org/GLM-4.7-Flash
	// ref: https://huggingface.co/zai-org/GLM-4.5-Air
	// ref: https://docs.z.ai/guides/llm/glm-4.5
	"zai-org/glm-5":         200000,
	"zai-org/glm-4.7":       200000,
	"zai-org/glm-4.7-flash": 200000,
	"zai-org/glm-4.5-air":   128000,

	// Moonshot Kimi
	// ref: https://platform.moonshot.cn/docs/overview
	// ref: https://platform.moonshot.cn/docs/guide/kimi-k2-quickstart
	// ref: https://platform.moonshot.cn/docs/pricing/chat
	"kimi-k2.5":              256000,
	"kimi-k2-0905-preview":   256000,
	"kimi-k2-turbo-preview":  256000,
	"kimi-k2-thinking":       256000,
	"kimi-k2-thinking-turbo": 256000,
	"kimi-k2-0711-preview":   128000,

	// MiniMax
	// ref: https://platform.minimax.io/docs
	// ref: https://platform.minimax.io/docs/guides/text-generation
	"minimax-m2.7":           204800,
	"minimax-m2.7-highspeed": 204800,
	"minimax-m2.5":           204800,
	"minimax-m2.5-highspeed": 204800,
	"minimax-m2.1":           204800,
	"minimax-m2.1-highspeed": 204800,
	"minimax-m2":             204800,
}

// LookupContextWindow returns a known context window size for a given
// model name.
// - Exact match (case-insensitive) first
// - Prefix-based fallback second at a model-ID boundary
// - Returns ok=false when the model is unknown
func LookupContextWindow(modelName string) (int, bool) {
	if modelName == "" {
		return 0, false
	}

	ModelMutex.RLock()
	defer ModelMutex.RUnlock()

	key := strings.ToLower(modelName)
	if w, ok := ModelContextWindows[key]; ok {
		return w, true
	}

	// Prefer the longest matching prefix so specific snapshots/variants win.
	bestWindow := 0
	bestPrefixLen := 0
	for k, w := range ModelContextWindows {
		if !isModelPrefixMatch(key, k) {
			continue
		}
		if len(k) <= bestPrefixLen {
			continue
		}
		bestWindow = w
		bestPrefixLen = len(k)
	}
	if bestPrefixLen > 0 {
		return bestWindow, true
	}
	return 0, false
}

// ResolveContextWindow returns the context window size for a given model name.
// It falls back to defaultContextWindow when the model is unknown.
func ResolveContextWindow(modelName string) int {
	if w, ok := LookupContextWindow(modelName); ok {
		return w
	}
	return defaultContextWindow
}

// isModelPrefixMatch reports whether prefix matches a full model name or is
// followed by a separator used by common snapshot/provider suffixes.
func isModelPrefixMatch(modelName, prefix string) bool {
	if !strings.HasPrefix(modelName, prefix) {
		return false
	}
	if len(modelName) == len(prefix) {
		return true
	}
	switch modelName[len(prefix)] {
	case '-', '@', ':':
		return true
	default:
		return false
	}
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
