//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package usagemeta reads and writes framework-internal metadata carried by
// model.Usage values without exposing it in the public model API.
//
// The metadata lives in unexported model.Usage fields, so it travels with the
// value through copies, Clone, callbacks, and model wrappers. Package model
// registers the accessors at init time; this package has no dependency on
// model to avoid an import cycle.
package usagemeta

// PromptCacheAccounting describes how model.Usage.PromptTokens relates to the
// prompt cache counters in model.Usage.PromptTokensDetails.
type PromptCacheAccounting string

const (
	// PromptCacheAccountingUnspecified means the producer did not declare the
	// accounting mode (for example, a Usage built by user code).
	PromptCacheAccountingUnspecified PromptCacheAccounting = ""
	// PromptCacheAccountingInclusive means PromptTokens includes cached,
	// cache-read, and cache-write tokens (OpenAI-compatible APIs, Gemini).
	PromptCacheAccountingInclusive PromptCacheAccounting = "inclusive"
	// PromptCacheAccountingExclusive means PromptTokens excludes cache-read
	// and cache-write tokens (Anthropic Messages API, AWS Bedrock Converse).
	PromptCacheAccountingExclusive PromptCacheAccounting = "exclusive"
)

// Valid reports whether a is a declared (non-empty, known) accounting mode.
func (a PromptCacheAccounting) Valid() bool {
	return a == PromptCacheAccountingInclusive || a == PromptCacheAccountingExclusive
}

var (
	getPromptCacheAccounting func(usage any) PromptCacheAccounting
	setPromptCacheAccounting func(usage any, accounting PromptCacheAccounting)
)

// Register installs the model.Usage accessors. It is called once by package
// model during initialization and is not intended for other callers.
func Register(
	get func(usage any) PromptCacheAccounting,
	set func(usage any, accounting PromptCacheAccounting),
) {
	getPromptCacheAccounting = get
	setPromptCacheAccounting = set
}

// GetPromptCacheAccounting returns the accounting mode carried by usage, which
// must be a *model.Usage. It returns PromptCacheAccountingUnspecified for nil,
// other types, or values without a declared mode.
func GetPromptCacheAccounting(usage any) PromptCacheAccounting {
	if usage == nil || getPromptCacheAccounting == nil {
		return PromptCacheAccountingUnspecified
	}
	return getPromptCacheAccounting(usage)
}

// SetPromptCacheAccounting declares the accounting mode on usage, which must be
// a *model.Usage. Model adapters call it on the Usage values they emit.
//
// The mode is only recorded when usage carries at least one prompt cache
// counter, because it is meaningless otherwise; this also keeps Usage values
// without cache counters comparable to their zero-valued literals. Passing
// PromptCacheAccountingUnspecified or usage without prompt cache counters clears
// any recorded mode.
func SetPromptCacheAccounting(usage any, accounting PromptCacheAccounting) {
	if usage == nil || setPromptCacheAccounting == nil {
		return
	}
	setPromptCacheAccounting(usage, accounting)
}
