//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TokenUsage holds accumulated token usage for a single QA or sample.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// LLMCalls is the number of model invocations
	// (may be >1 for tool-calling agents).
	LLMCalls int `json:"llm_calls"`
}

// Add merges another TokenUsage into the receiver.
func (u *TokenUsage) Add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.LLMCalls += other.LLMCalls
}

// TokenTracker accumulates token usage across multiple LLM calls
// in a thread-safe manner. It is designed to be wired into model
// callbacks (AfterModelCallbackStructured) so that every model
// invocation—including multi-turn tool-call loops—is captured.
type TokenTracker struct {
	mu    sync.Mutex
	usage TokenUsage
}

// NewTokenTracker creates a new empty tracker.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

// Record adds usage from a single model response.
func (t *TokenTracker) Record(u *model.Usage) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.PromptTokens += u.PromptTokens
	t.usage.CompletionTokens += u.CompletionTokens
	t.usage.TotalTokens += u.TotalTokens
	t.usage.LLMCalls++
}

// Snapshot returns a copy of the current accumulated usage and
// resets the tracker to zero. This is typically called after each
// QA question to capture per-question token usage.
func (t *TokenTracker) Snapshot() TokenUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.usage
	t.usage = TokenUsage{}
	return snap
}

// Peek returns a copy of the current accumulated usage without
// resetting. Useful for cumulative reporting.
func (t *TokenTracker) Peek() TokenUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usage
}

// AfterModelCallback returns an AfterModelCallbackStructured that
// records token usage from every model response into this tracker.
func (t *TokenTracker) AfterModelCallback() model.AfterModelCallbackStructured {
	return func(
		_ context.Context,
		args *model.AfterModelArgs,
	) (*model.AfterModelResult, error) {
		if args != nil && args.Response != nil {
			t.Record(args.Response.Usage)
		}
		return nil, nil
	}
}

// NewModelCallbacksWithTracker creates model.Callbacks pre-wired
// with the given tracker's AfterModelCallback.
func NewModelCallbacksWithTracker(
	tracker *TokenTracker,
) *model.Callbacks {
	cb := model.NewCallbacks()
	cb.AfterModel = append(
		cb.AfterModel, tracker.AfterModelCallback(),
	)
	return cb
}
