//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package builtin implements the built-in planner that uses model's built-in
// thinking features.
//
// The builtin planner is specifically designed for models that have native
// thinking capabilities. It does not generate explicit planning instructions
// but instead configures the model to use its internal thinking mechanisms.
//
// Supported models:
//   - OpenAI o-series models (uses reasoning_effort parameter)
//   - DeepSeek v4 models, e.g. deepseek-v4-pro / deepseek-v4-flash
//     (uses reasoning_effort; thinking can be toggled via thinking_enabled.
//     When ThinkingEnabled is left unset, trpc-agent-go does not emit the
//     "thinking" field, and the DeepSeek service applies its server-side
//     default, which is "enabled".)
//   - Claude models via OpenAI API (uses thinking_enabled and thinking_tokens)
//   - Gemini models via OpenAI API (uses thinking_enabled and thinking_tokens)
//
// For models without thinking capabilities, consider using other planner
// implementations that provide explicit planning instructions.
package builtin

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

// Verify that Planner implements the planner.Planner interface.
var _ planner.Planner = (*Planner)(nil)

// Planner represents the built-in planner that uses model's built-in thinking features.
//
// This planner is intended for models that have native thinking capabilities.
// It configures these models to engage their internal reasoning mechanisms
// rather than providing explicit planning prompts.
//
// The planner applies thinking configuration to requests and returns empty
// planning instructions, as the actual planning is handled internally by
// the thinking-capable models.
type Planner struct {
	// reasoningEffort limits the reasoning effort for reasoning models.
	// The accepted values depend on the provider:
	//   - OpenAI o-series: "low", "medium", "high".
	//   - DeepSeek v4 (deepseek-v4-pro / deepseek-v4-flash): "high", "max"
	//     (server maps "low"/"medium" -> "high" and "xhigh" -> "max" for
	//     backward compatibility).
	reasoningEffort *string

	// thinkingEnabled toggles thinking mode for models that support it.
	// Effective for DeepSeek v4 models, and for Claude / Gemini models via
	// the OpenAI API. When left nil, trpc-agent-go does not emit the
	// "thinking" field at all; the provider then applies its own default
	// (DeepSeek v4's server-side default is "enabled").
	thinkingEnabled *bool

	// thinkingTokens controls the length of thinking.
	// Only effective for Claude and Gemini models via OpenAI API.
	thinkingTokens *int
}

// Options contains configuration options for creating a Planner.
//
// Configure these options based on your model type:
//   - For OpenAI o-series: set ReasoningEffort
//   - For DeepSeek v4: set ReasoningEffort, and optionally ThinkingEnabled to
//     explicitly toggle thinking mode. Leaving ThinkingEnabled unset means
//     trpc-agent-go will not emit the "thinking" field; the DeepSeek service
//     then applies its server-side default ("enabled").
//   - For Claude/Gemini via OpenAI API: set ThinkingEnabled and/or ThinkingTokens
type Options struct {
	// ReasoningEffort limits the reasoning effort for reasoning models.
	// The accepted values depend on the provider:
	//   - OpenAI o-series: "low", "medium", "high".
	//   - DeepSeek v4 (deepseek-v4-pro / deepseek-v4-flash): "high", "max"
	//     (server maps "low"/"medium" -> "high" and "xhigh" -> "max" for
	//     backward compatibility).
	ReasoningEffort *string

	// ThinkingEnabled enables thinking mode for models that support it.
	// Effective for DeepSeek v4 models, and for Claude / Gemini models via
	// the OpenAI API. When left nil, trpc-agent-go does not emit the
	// "thinking" field at all; the provider then applies its own default
	// (DeepSeek v4's server-side default is "enabled").
	ThinkingEnabled *bool

	// ThinkingTokens controls the length of thinking for Claude and Gemini models via OpenAI API.
	// Higher values allow for more detailed internal reasoning.
	// Not applicable to DeepSeek v4 (which uses ReasoningEffort instead).
	ThinkingTokens *int
}

// New creates a new Planner with the given options.
//
// The returned planner is designed to work with thinking-capable models.
// Ensure your model supports the configured thinking parameters before use.
func New(opts Options) *Planner {
	return &Planner{
		reasoningEffort: opts.ReasoningEffort,
		thinkingEnabled: opts.ThinkingEnabled,
		thinkingTokens:  opts.ThinkingTokens,
	}
}

// BuildPlanningInstruction applies thinking configuration to the LLM request
// and builds the system instruction. For the built-in planner, this applies
// thinking config and returns empty string as the model handles planning
// internally through its thinking capabilities.
//
// This method configures the request with appropriate thinking parameters
// based on the model type, then relies on the model's internal mechanisms
// for actual planning rather than providing explicit instructions.
func (p *Planner) BuildPlanningInstruction(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) string {
	// Apply thinking configuration to the request.
	// The model will use these parameters to engage its internal thinking.
	if p.reasoningEffort != nil {
		llmRequest.ReasoningEffort = p.reasoningEffort
	}
	if p.thinkingEnabled != nil {
		llmRequest.ThinkingEnabled = p.thinkingEnabled
	}
	if p.thinkingTokens != nil {
		llmRequest.ThinkingTokens = p.thinkingTokens
	}

	// Return empty string as thinking-capable models handle planning internally.
	// No explicit planning instruction is needed.
	return ""
}

// ProcessPlanningResponse processes the LLM response for planning.
// For the built-in planner, this returns nil as the model handles the
// response processing internally through its thinking capabilities.
//
// Thinking-capable models integrate planning directly into their response
// generation, so no additional post-processing is required.
func (p *Planner) ProcessPlanningResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) *model.Response {
	// No response processing needed for thinking-capable models.
	// The planning is already integrated into the model's response.
	return nil
}
