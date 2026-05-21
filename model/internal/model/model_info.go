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

// ModelContextWindows holds known model name -> context window size (tokens).
var ModelContextWindows = map[string]int{
	// OpenAI.
	// Provider page: https://developers.openai.com/api/docs/models
	// Per-model pages: https://developers.openai.com/api/docs/models/<slug>

	// OpenAI O-series
	"o1-preview": 128000, // https://developers.openai.com/api/docs/models/o1-preview
	"o1-mini":    128000, // https://developers.openai.com/api/docs/models/o1-mini
	"o1":         200000, // https://developers.openai.com/api/docs/models/o1
	"o3-mini":    200000, // https://developers.openai.com/api/docs/models/o3-mini
	"o3":         200000, // https://developers.openai.com/api/docs/models/o3
	"o4-mini":    200000, // https://developers.openai.com/api/docs/models/o4-mini

	// OpenAI GPT-5.5
	"gpt-5.5":     1050000, // https://developers.openai.com/api/docs/models/gpt-5.5
	"gpt-5.5-pro": 1050000, // https://developers.openai.com/api/docs/models/gpt-5.5-pro

	// OpenAI GPT-5.4
	"gpt-5.4":      1050000, // https://developers.openai.com/api/docs/models/gpt-5.4
	"gpt-5.4-pro":  1050000, // https://developers.openai.com/api/docs/models/gpt-5.4-pro
	"gpt-5.4-mini": 400000,  // https://developers.openai.com/api/docs/models/gpt-5.4-mini
	"gpt-5.4-nano": 400000,  // https://developers.openai.com/api/docs/models/gpt-5.4-nano

	// OpenAI GPT-5.2
	"gpt-5.2":             400000, // https://developers.openai.com/api/docs/models/gpt-5.2
	"gpt-5.2-codex":       400000, // https://developers.openai.com/api/docs/models/gpt-5.2-codex
	"gpt-5.2-chat-latest": 128000, // https://developers.openai.com/api/docs/models/gpt-5.2-chat-latest

	// OpenAI GPT-5.1
	"gpt-5.1":             400000, // https://developers.openai.com/api/docs/models/gpt-5.1
	"gpt-5.1-codex-max":   400000, // https://developers.openai.com/api/docs/models/gpt-5.1-codex-max
	"gpt-5.1-codex":       400000, // https://developers.openai.com/api/docs/models/gpt-5.1-codex
	"gpt-5.1-codex-mini":  400000, // https://developers.openai.com/api/docs/models/gpt-5.1-codex-mini
	"gpt-5.1-chat-latest": 128000, // https://developers.openai.com/api/docs/models/gpt-5.1-chat-latest

	// OpenAI GPT-5
	"gpt-5":             400000, // https://developers.openai.com/api/docs/models/gpt-5
	"gpt-5-pro":         400000, // https://developers.openai.com/api/docs/models/gpt-5-pro
	"gpt-5-codex":       400000, // https://developers.openai.com/api/docs/models/gpt-5-codex
	"gpt-5-mini":        400000, // https://developers.openai.com/api/docs/models/gpt-5-mini
	"gpt-5-nano":        400000, // https://developers.openai.com/api/docs/models/gpt-5-nano
	"gpt-5-chat-latest": 128000, // https://developers.openai.com/api/docs/models/gpt-5-chat-latest

	// OpenAI GPT-4.5
	"gpt-4.5-preview": 128000, // https://developers.openai.com/api/docs/models/gpt-4.5-preview

	// OpenAI GPT-4.1 (1,047,576 = 1M-1024, the documented limit)
	"gpt-4.1":      1047576, // https://developers.openai.com/api/docs/models/gpt-4.1
	"gpt-4.1-mini": 1047576, // https://developers.openai.com/api/docs/models/gpt-4.1-mini
	"gpt-4.1-nano": 1047576, // https://developers.openai.com/api/docs/models/gpt-4.1-nano

	// OpenAI GPT-4o
	"gpt-4o":      128000, // https://developers.openai.com/api/docs/models/gpt-4o
	"gpt-4o-mini": 128000, // https://developers.openai.com/api/docs/models/gpt-4o-mini

	// OpenAI GPT-4
	"gpt-4":       8192,   // https://developers.openai.com/api/docs/models/gpt-4
	"gpt-4-turbo": 128000, // https://developers.openai.com/api/docs/models/gpt-4-turbo
	"gpt-4-32k":   32768,  // https://developers.openai.com/api/docs/models/gpt-4-32k

	// OpenAI GPT-3.5
	"gpt-3.5-turbo":          16385, // https://developers.openai.com/api/docs/models/gpt-3.5-turbo
	"gpt-3.5-turbo-instruct": 4096,  // https://developers.openai.com/api/docs/models/gpt-3.5-turbo-instruct
	"gpt-3.5-turbo-16k":      16385, // https://developers.openai.com/api/docs/models/gpt-3.5-turbo-16k

	// OpenAI legacy completions — https://platform.openai.com/docs/deprecations
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

	// Anthropic Claude.
	// Provider page: https://docs.anthropic.com/en/docs/about-claude/models/overview
	// Beta context-window header (1M tier on Sonnet 4 / 4.5):
	//   https://docs.anthropic.com/en/docs/build-with-claude/context-windows
	// Default windows shown below; 1M on Sonnet 4 / 4.5 requires the
	// `context-1m-2025-08-07` beta header.

	// Anthropic Claude 4.7
	"claude-4.7-opus": 1000000,
	"claude-opus-4-7": 1000000,

	// Anthropic Claude 4.6
	"claude-4.6-opus":   1000000,
	"claude-opus-4-6":   1000000,
	"claude-4.6-sonnet": 1000000,
	"claude-sonnet-4-6": 1000000,

	// Anthropic Claude 4.5
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

	// Anthropic Claude legacy
	"claude-2.1":         200000,
	"claude-2.0":         100000,
	"claude-instant-1.2": 100000,

	// Google Gemini.
	// Provider page: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini
	// Per-model pages: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/<version>
	// Version segment uses dashes (3-pro, 2-5-pro, 2-0-flash). Older 1.5 pages
	// have been retired from Vertex docs, so 1.5 entries fall back to the
	// Gemini API model cards on ai.google.dev.

	// Google Gemini 3.x
	"gemini-3.5-flash":       1048576, // https://ai.google.dev/gemini-api/docs/models/gemini-3.5-flash
	"gemini-3.1-pro-preview": 1048576, // https://ai.google.dev/gemini-api/docs/models/gemini-3.1-pro-preview
	"gemini-3-pro-preview":   1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/3-pro
	"gemini-3-flash-preview": 1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/3-flash
	"gemini-3.0-pro":         1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/3-pro
	"gemini-3.0-flash":       1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/3-flash

	// Google Gemini 2.5
	"gemini-2.5-pro":   1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/2-5-pro
	"gemini-2.5-flash": 1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/2-5-flash

	// Google Gemini 2.0
	"gemini-2.0-flash": 1048576, // https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/gemini/2-0-flash

	// Google Gemini 1.5 (retired from Vertex docs; using Gemini API model cards)
	"gemini-1.5-pro":      2097152, // https://ai.google.dev/gemini-api/docs/models/gemini-1.5-pro
	"gemini-1.5-flash":    1048576, // https://ai.google.dev/gemini-api/docs/models/gemini-1.5-flash
	"gemini-1.5-flash-8b": 1048576, // https://ai.google.dev/gemini-api/docs/models/gemini-1.5-flash-8b

	// Google Gemma.
	// Provider page: https://huggingface.co/google
	// Per-model pages: https://huggingface.co/google/<repo>
	"gemma-3-27b-it": 131072, // https://huggingface.co/google/gemma-3-27b-it
	"gemma-3-12b-it": 131072, // https://huggingface.co/google/gemma-3-12b-it
	"gemma-3-4b-it":  131072, // https://huggingface.co/google/gemma-3-4b-it
	"gemma-3-1b-it":  32000,  // https://huggingface.co/google/gemma-3-1b-it
	"gemma2-9b-it":   8192,   // https://huggingface.co/google/gemma-2-9b-it
	"gemma-7b-it":    8192,   // https://huggingface.co/google/gemma-7b-it

	// Meta Llama.
	// Provider page: https://www.llama.com/docs/model-cards-and-prompt-formats
	// Per-version pages: https://www.llama.com/docs/model-cards-and-prompt-formats/<version>
	// (mirrored at https://github.com/meta-llama/llama-models)
	// Some rows record narrower hosted-deployment windows (e.g. Groq's 8k/16k
	// deployment of an upstream 128k model); those rows link to
	// https://console.groq.com/docs/models and call out the gap inline.

	// Meta Llama 4 — https://www.llama.com/docs/model-cards-and-prompt-formats/llama4
	"llama-4-scout":    10485760, // 10M per model card
	"llama-4-maverick": 1048576,  // 1M per model card

	// Meta Llama 3.3 — https://www.llama.com/docs/model-cards-and-prompt-formats/llama3_3
	// Meta only released a 70B size for the 3.3 generation (model card:
	// https://github.com/meta-llama/llama-models/blob/main/models/llama3_3/MODEL_CARD.md
	// — 70B Instruct, December 6 2024). Some hosted providers expose a
	// `meta-llama/llama-3.3-8b-instruct` slug, but it is not an official Meta
	// release; treat such names as unknown and let them fall back to the
	// default window rather than encoding a fabricated value here.
	"llama-3.3-70b-instruct":  131072, // 128k per model card
	"llama-3.3-70b-versatile": 128000, // Groq deployment of llama-3.3-70b — https://console.groq.com/docs/models

	// Meta Llama 3.2 — https://www.llama.com/docs/model-cards-and-prompt-formats/llama3_2
	// Vision card: https://github.com/meta-llama/llama-models/blob/main/models/llama3_2/MODEL_CARD_VISION.md
	// All instruct variants are 128k per the model cards; smaller windows
	// below come from Groq's hosted deployment.
	"llama-3.2-90b-vision-instruct": 16384,  // Groq deployment — https://console.groq.com/docs/models
	"llama-3.2-90b-text-preview":    8192,   // Groq preview — https://console.groq.com/docs/models
	"llama-3.2-11b-vision-instruct": 131072, // 128k per model card
	"llama-3.2-11b-text-preview":    8192,   // Groq preview — https://console.groq.com/docs/models
	"llama-3.2-3b-preview":          8192,   // Groq preview — https://console.groq.com/docs/models
	"llama-3.2-3b-instruct":         4096,   // Groq deployment — https://console.groq.com/docs/models (model card is 128k)
	"llama-3.2-1b-preview":          8192,   // Groq preview — https://console.groq.com/docs/models
	"llama-3.2-1b-instruct":         16384,  // Groq deployment — https://console.groq.com/docs/models (model card is 128k)

	// Meta Llama 3.1 — https://www.llama.com/docs/model-cards-and-prompt-formats/llama3_1
	// All sizes (8B/70B/405B) are 128k per the model card.
	"llama-3.1-405b-instruct": 8192,   // Groq deployment — https://console.groq.com/docs/models (model card is 128k)
	"llama-3.1-70b-instruct":  131072, // 128k per model card
	"llama-3.1-70b-versatile": 131072, // Groq deployment of llama-3.1-70b — https://console.groq.com/docs/models
	"llama-3.1-8b-instruct":   128000, // 128k per model card
	"llama-3.1-8b-instant":    131072, // Groq deployment of llama-3.1-8b — https://console.groq.com/docs/models

	// Meta Llama 3.0 — https://www.llama.com/docs/model-cards-and-prompt-formats/meta-llama-3
	// The original Llama 3 (8B/70B) is natively 8k; the `-8192` suffix is just
	// how hosted providers (e.g. Groq) encode it into the alias.
	"llama3-70b-8192": 8192,
	"llama3-8b-8192":  8192,

	// Mistral / Mixtral / Codestral / Pixtral / Ministral / Devstral / Magistral.
	// Provider page: https://openrouter.ai/mistralai
	// Per-model pages: https://openrouter.ai/mistralai/<slug>
	// Each row's window matches what OpenRouter's provider page lists.
	// When first-party API IDs and OpenRouter slugs differ, both are listed.
	// Open-weight checkpoints additionally have a Hugging Face model card; the
	// HF link is inlined when it differs meaningfully from OpenRouter.

	// Mistral Large
	"mistral-large-3":              262144,
	"mistral-large-2512":           262144, // https://docs.mistral.ai/models/model-cards/mistral-large-3-25-12
	"mistral-large-latest":         262144, // latest Large 3 API alias; https://docs.mistral.ai/studio-api/conversations/function-calling
	"mistral-large":                131072, // mistral-large-2411
	"mistral-large-2411":           131072,
	"mistral-large-2407":           131072,
	"mistralai/mistral-large-2512": 262144, // https://openrouter.ai/mistralai/mistral-large-2512

	// Mistral Medium
	"mistral-medium-3.5":           262144,
	"mistral-medium-3-5":           262144, // https://docs.mistral.ai/models/model-cards/mistral-medium-3-5-26-04
	"mistral-medium-latest":        131072, // tracks Medium 3.1
	"mistral-medium-3.1":           131072,
	"mistral-medium-3":             131072,
	"mistralai/mistral-medium-3-5": 262144, // https://openrouter.ai/mistralai/mistral-medium-3-5

	// Mistral Small
	"mistral-small-4":              262144,
	"mistral-small-2603":           262144, // https://docs.mistral.ai/models/model-cards/mistral-small-4-0-26-03
	"mistral-small-creative":       33000,
	"mistral-small-3.2":            131072, // mistral-small-3.2-24b
	"mistral-small-3.1":            128000, // mistral-small-3.1-24b
	"mistral-small-latest":         128000, // tracks Mistral Small 3.1 24B
	"mistral-small-3":              33000,
	"mistralai/mistral-small-2603": 262144, // https://openrouter.ai/mistralai/mistral-small-2603

	// Mistral Nemo
	"mistral-nemo": 131072,

	// Mistral Saba
	"mistral-saba": 33000,

	// Mistral 7B family
	"mistral-tiny":            32768,
	"mistral-7b-instruct":     32000, // open-weight: https://huggingface.co/mistralai/Mistral-7B-Instruct-v0.3
	"mistral-7b-instruct-v03": 32768,
	"mistral-7b-instruct-v02": 32768,

	// Codestral
	"codestral-2508":  256000,
	"codestral-2501":  256000,
	"codestral-mamba": 256000,

	// Pixtral
	"pixtral-large-2411":         131072,
	"pixtral-12b":                4096,
	"pixtral-12b-2409":           131072, // https://huggingface.co/mistralai/Pixtral-12B-2409
	"mistralai/pixtral-12b-2409": 131072, // https://huggingface.co/mistralai/Pixtral-12B-2409

	// Ministral
	"ministral-3-14b": 262144, // ministral-3-14b-2512
	"ministral-3-8b":  262144, // ministral-3-8b-2512
	"ministral-3-3b":  131072, // ministral-3-3b-2512
	"ministral-8b":    128000, // ministral-8b (2024-10)
	"ministral-3b":    128000, // ministral-3b (2024-10)

	// Devstral
	"devstral-2":         262144, // devstral-2-2512
	"devstral-medium":    131072,
	"devstral-small-1.1": 131072,
	"devstral-small":     131072, // devstral-small-2505

	// Magistral
	"magistral-small":  40960,
	"magistral-medium": 41984,

	// Mixtral
	"mixtral-8x22b-instruct": 65536, // OpenRouter lists 66K
	"mixtral-8x22b":          65536,
	"mixtral-8x7b-instruct":  32768,
	"mixtral-8x7b-32768":     32768, // Groq deployment — https://console.groq.com/docs/models

	// Alibaba Qwen / QwQ.
	// Provider page: https://openrouter.ai/qwen
	// Per-model pages: https://openrouter.ai/qwen/<slug>
	// Each row's window matches what OpenRouter's provider page lists.
	// Aliyun Model Studio docs (https://help.aliyun.com/zh/model-studio/getting-started/models)
	// list the same hosted-API limits but distinguish thinking vs non-thinking
	// modes for some aliases — see notes inline.

	// Qwen 3.6
	"qwen3.6-plus":        1000000,
	"qwen3.6-flash":       1000000,
	"qwen3.6-max-preview": 262144,
	"qwen3.6-35b-a3b":     262144,
	"qwen3.6-27b":         262144,

	// Qwen 3.5 — OpenRouter publishes dated snapshots
	// (e.g. qwen3.5-plus-2026-04-20); bare aliases track the latest snapshot.
	"qwen3.5-plus":      1000000,
	"qwen3.5-flash":     1000000,
	"qwen3.5-9b":        262144,
	"qwen3.5-27b":       262144,
	"qwen3.5-35b-a3b":   262144,
	"qwen3.5-122b-a10b": 262144,
	"qwen3.5-397b-a17b": 262144,

	// Qwen 3 hosted API and frontier MoE
	"qwen3-max":               262144,
	"qwen3-max-thinking":      262144,
	"qwen3-coder-next":        262144,
	"qwen3-coder-plus":        1000000,
	"qwen3-coder-flash":       1000000,
	"qwen3-coder-480b":        262144, // qwen3-coder-480b-a35b
	"qwen3-coder-30b":         163840, // qwen3-coder-30b-a3b-instruct (160K)
	"qwen3-next-80b-instruct": 262144,
	"qwen3-next-80b-thinking": 131072,

	// Qwen 3 235B / 30B (open-weight + 2507 refresh)
	"qwen3-235b-a22b":          131072,
	"qwen3-235b-a22b-instruct": 262144, // qwen3-235b-a22b-instruct-2507
	"qwen3-235b-a22b-thinking": 131072, // qwen3-235b-a22b-thinking-2507
	"qwen3-30b-a3b":            41984,
	"qwen3-30b-a3b-instruct":   262144, // qwen3-30b-a3b-instruct-2507
	"qwen3-30b-a3b-thinking":   131072, // qwen3-30b-a3b-thinking-2507

	// Qwen 3 dense
	"qwen3-32b":  41984,
	"qwen3-14b":  41984,
	"qwen3-8b":   41984,
	"qwen3-4b":   128000,
	"qwen3-1.7b": 32000,
	"qwen3-0.6b": 32000,

	// Qwen 3 VL
	"qwen3-vl-235b-a22b-instruct": 262144,
	"qwen3-vl-235b-a22b-thinking": 131072,
	"qwen3-vl-30b-a3b-instruct":   131072,
	"qwen3-vl-30b-a3b-thinking":   131072,
	"qwen3-vl-32b-instruct":       131072,
	"qwen3-vl-8b-instruct":        131072,
	"qwen3-vl-8b-thinking":        131072,

	// Qwen Plus / Max / Turbo / VL hosted API.
	// qwen-max ships a 32k context per OpenRouter and the base Aliyun snapshot;
	// qwen-max-latest is the rolling 131k variant. Codepath uses longest-prefix
	// match, so an explicit qwen-max-latest entry is added to keep both correct.
	// qwen-turbo supports 1,000,000 tokens in non-thinking mode but a smaller
	// 131,072 window in thinking mode — we use the smaller documented limit
	// as the safe default for token budgeting.
	"qwen-max":        32768,
	"qwen-max-latest": 131072, // https://help.aliyun.com/zh/model-studio/getting-started/models
	"qwen-plus":       1000000,
	"qwen-turbo":      131072, // thinking-mode limit
	"qwen-vl-max":     131072,
	"qwen-vl-plus":    131072,

	// QwQ
	"qwq-32b":         131072,
	"qwq-32b-preview": 33000, // OpenRouter window (model card itself is 32k)

	// Qwen 2.5 (open-weight checkpoints).
	// Generic IDs use the model card's full context length (131,072 tokens,
	// reachable via YaRN extension). Narrower hosted-deployment windows
	// (Groq / Aliyun default config / OpenRouter etc.) should be expressed
	// either through provider-specific aliases or `WithContextWindow` on the
	// caller side, mirroring the Llama 3.x convention above.
	"qwen2.5-72b-instruct":       131072, // https://huggingface.co/Qwen/Qwen2.5-72B-Instruct (full 128k via YaRN)
	"qwen2.5-32b-instruct":       131072,
	"qwen2.5-14b-instruct":       128000, // open-weight: https://huggingface.co/Qwen/Qwen2.5-14B-Instruct
	"qwen2.5-7b-instruct":        131072, // https://huggingface.co/Qwen/Qwen2.5-7B-Instruct (full 128k via YaRN)
	"qwen2.5-coder-32b-instruct": 131072, // https://huggingface.co/Qwen/Qwen2.5-Coder-32B-Instruct (full 128k via YaRN)
	"qwen2.5-coder-7b-instruct":  131072,
	"qwen2.5-vl-72b-instruct":    32000, // VL series defaults to 32k; YaRN degrades temporal/spatial localization
	"qwen2.5-vl-32b-instruct":    33000,
	"qwen2.5-vl-7b-instruct":     33000,
	"qwen2.5-vl-3b-instruct":     64000,

	// Qwen 2 (open-weight checkpoints).
	// Generic IDs follow the same convention as Qwen 2.5 above.
	"qwen2-72b-instruct": 131072, // https://huggingface.co/Qwen/Qwen2-72B-Instruct (full 128k via YaRN)
	"qwen2-7b-instruct":  131072, // https://huggingface.co/Qwen/Qwen2-7B-Instruct  (full 128k via YaRN)

	// Qwen 1.5
	"qwen1.5-110b-chat": 33000,
	"qwen1.5-72b-chat":  33000,
	"qwen1.5-32b-chat":  33000,
	"qwen1.5-14b-chat":  33000,
	"qwen1.5-7b-chat":   33000,
	"qwen1.5-4b-chat":   33000,

	// DeepSeek.
	// Provider page: https://api-docs.deepseek.com/quick_start/pricing
	// Thinking-mode details: https://api-docs.deepseek.com/guides/thinking_mode
	// `deepseek-chat` and `deepseek-reasoner` are deprecated aliases that route
	// to `deepseek-v4-flash` (non-thinking and thinking mode respectively),
	// which now ships a 1M window. The legacy aliases keep the previously
	// documented 128k as a conservative default — older instances may still
	// serve them with that limit during the compatibility period.
	"deepseek-chat":     131072, // legacy alias; previously documented as 128k
	"deepseek-reasoner": 131072, // legacy alias; previously documented as 128k
	"deepseek-v4-pro":   1048576,
	"deepseek-v4-flash": 1048576,

	// Amazon Bedrock.
	// Nova family — https://docs.aws.amazon.com/nova/latest/userguide/modalities.html
	"nova-pro-v1":   300000,
	"nova-micro-v1": 128000,
	"nova-lite-v1":  300000,
	// Titan family — https://docs.aws.amazon.com/bedrock/latest/userguide/titan-text-models.html
	"titan-text-express": 8000,
	"titan-text-lite":    4000,

	// AI21
	"jamba-instruct": 256000, // https://docs.ai21.com/docs/jamba-15-models
	// Jurassic-2 family — https://docs.ai21.com/docs/jurassic-2-models
	"j2-ultra": 8191,
	"j2-mid":   8191,

	// Cohere
	"command-text": 4000, // https://docs.cohere.com/docs/models

	// Tencent Hunyuan — https://hunyuan.cloud.tencent.com/#/app/modelSquare
	"hunyuan-translation":  8192,
	"hunyuan-2.0-instruct": 147456,
	"hunyuan-2.0-thinking": 196608,
	"hunyuan-t1":           65536,
	"hunyuan-turbos":       32768,
	"hunyuan-a13b":         229376,

	// Z.AI GLM (hosted API).
	// Provider page: https://docs.z.ai/guides/llm
	// Per-model pages: https://docs.z.ai/guides/llm/<slug>
	"glm-5.1":             200000, // https://docs.z.ai/guides/llm/glm-5.1
	"glm-5":               200000, // https://docs.z.ai/guides/llm/glm-5
	"glm-5-turbo":         200000, // https://docs.z.ai/guides/llm/glm-5-turbo
	"glm-4.7":             200000, // https://docs.z.ai/guides/llm/glm-4.7
	"glm-4.7-flashx":      200000, // https://docs.z.ai/guides/llm/glm-4.7
	"glm-4.7-flash":       200000, // https://docs.z.ai/guides/llm/glm-4.7
	"glm-4.6":             200000, // https://docs.z.ai/guides/llm/glm-4.6
	"glm-4.5":             128000, // https://docs.z.ai/guides/llm/glm-4.5
	"glm-4.5-air":         128000, // https://docs.z.ai/guides/llm/glm-4.5
	"glm-4.5-x":           128000, // https://docs.z.ai/guides/llm/glm-4.5
	"glm-4.5-airx":        128000, // https://docs.z.ai/guides/llm/glm-4.5
	"glm-4.5-flash":       128000, // https://docs.z.ai/guides/llm/glm-4.5
	"glm-4-32b-0414-128k": 128000, // https://docs.z.ai/guides/llm/glm-4-32b-0414-128k

	// Z.AI GLM (Hugging Face repository IDs).
	// Windows match the matching docs.z.ai page; when the HF model card itself
	// does not state a window, the value defers to docs.z.ai for that family.
	"zai-org/glm-5.1":       200000, // https://huggingface.co/zai-org/GLM-5.1 (window per docs.z.ai/guides/llm/glm-5.1)
	"zai-org/glm-5":         200000, // https://huggingface.co/zai-org/GLM-5
	"zai-org/glm-4.7":       200000, // https://huggingface.co/zai-org/GLM-4.7
	"zai-org/glm-4.7-flash": 200000, // https://huggingface.co/zai-org/GLM-4.7-Flash
	"zai-org/glm-4.6":       200000, // https://huggingface.co/zai-org/GLM-4.6 (model card: "expanded from 128K to 200K")
	"zai-org/glm-4.5-air":   128000, // https://huggingface.co/zai-org/GLM-4.5-Air

	// Moonshot Kimi.
	// Provider page: https://platform.kimi.com/docs/models
	// Per-family pricing pages:
	//   - https://platform.kimi.com/docs/pricing/chat-k26  (Kimi K2.6)
	//   - https://platform.kimi.com/docs/pricing/chat-k25  (Kimi K2.5)
	//   - https://platform.kimi.com/docs/pricing/chat-k2   (Kimi K2 family; deprecating 2026-05-25)
	//   - https://platform.kimi.com/docs/pricing/chat-v1   (Moonshot V1)
	// "256K" on these pages is documented as 256,000 tokens on the pricing
	// pages and as 262,144 tokens on a few quickstart pages — we use 256,000
	// for consistency with the pricing/billing page across the family.

	// Kimi K2.6 (current flagship)
	"kimi-k2.6": 256000,

	// Kimi K2.5
	"kimi-k2.5": 256000,

	// Kimi K2 (scheduled for deprecation 2026-05-25; migrate to k2.6)
	"kimi-k2-0905-preview":   256000,
	"kimi-k2-turbo-preview":  256000,
	"kimi-k2-thinking":       256000,
	"kimi-k2-thinking-turbo": 256000,
	"kimi-k2-0711-preview":   128000,

	// Moonshot V1 — text models
	"moonshot-v1-8k":   8192,
	"moonshot-v1-32k":  32768,
	"moonshot-v1-128k": 131072,
	// Moonshot V1 — vision-preview variants (windows match their text twin)
	"moonshot-v1-8k-vision-preview":   8192,
	"moonshot-v1-32k-vision-preview":  32768,
	"moonshot-v1-128k-vision-preview": 131072,

	// MiniMax — https://platform.minimax.io/docs/guides/text-generation
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
