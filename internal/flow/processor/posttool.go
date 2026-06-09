//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const systemPromptSeparator = "\n\n"

// DefaultPostToolPrompt is the default stable tool-result guidance injected
// into the system prompt. It is present from the first model request so the
// prompt prefix remains cacheable across later tool-call turns.
const DefaultPostToolPrompt = "[Tool Prompt] Analyze the tool result. " +
	"If requirements are met, provide the Final Answer. " +
	"Otherwise, call the next tool. \n" +
	"Final Answer Requirement:\n" +
	"Only output the exact answer the user needs. " +
	"Answer as if you already know the information. Do NOT expose any " +
	"internal process, tool usage, or source of information. " +
	"Do not use phrases that reference tools, searches, or retrieved " +
	"data, like \"according to xxx, based on xxx\". Keep your answer " +
	"concise and to the point.\n"

// PostToolRequestProcessor appends stable tool-result guidance to the
// system message. The guidance is injected even before the first tool result
// so later tool-loop requests do not change the earliest prompt prefix and
// invalidate provider-side prompt caches.
//
// The guidance is injected via system prompt instead of a user message for
// better cross-model compatibility and to avoid inserting a user message
// between tool results and the assistant turn.
//
// This processor MUST be registered after ContentRequestProcessor so that
// req.Messages already contains any request-level system message created by
// earlier processors.
type PostToolRequestProcessor struct {
	// prompt is the stable prompt text appended to the system message.
	// When empty, no prompt is injected.
	prompt string
	// promptBeforeToolResult controls whether the prompt is injected before
	// the first tool result. Disabling this preserves legacy behavior for
	// agents that cannot call tools.
	promptBeforeToolResult bool
	// createSystemMessage controls whether the processor may prepend a new
	// system message before the first tool result when none exists yet.
	createSystemMessage bool
}

// PostToolOption is a functional option for PostToolRequestProcessor.
type PostToolOption func(*PostToolRequestProcessor)

// WithPostToolPrompt overrides the default stable prompt text.
// Set to empty string to disable prompt injection entirely.
func WithPostToolPrompt(prompt string) PostToolOption {
	return func(p *PostToolRequestProcessor) {
		p.prompt = prompt
	}
}

// WithPostToolPromptBeforeResult controls whether post-tool guidance is
// injected before the first tool result.
func WithPostToolPromptBeforeResult(enable bool) PostToolOption {
	return func(p *PostToolRequestProcessor) {
		p.promptBeforeToolResult = enable
	}
}

// WithPostToolPromptCreateSystemMessage controls whether post-tool guidance
// may create a system message when none exists yet.
func WithPostToolPromptCreateSystemMessage(enable bool) PostToolOption {
	return func(p *PostToolRequestProcessor) {
		p.createSystemMessage = enable
	}
}

// NewPostToolRequestProcessor creates a new PostToolRequestProcessor.
func NewPostToolRequestProcessor(
	opts ...PostToolOption,
) *PostToolRequestProcessor {
	p := &PostToolRequestProcessor{
		prompt:                 DefaultPostToolPrompt,
		promptBeforeToolResult: true,
		createSystemMessage:    true,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessRequest implements flow.RequestProcessor.
// It appends the prompt to the system message once whenever the processor is
// enabled.
func (p *PostToolRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil || p.prompt == "" {
		return
	}

	if hasPostToolPrompt(req.Messages, p.prompt) {
		return
	}

	hasToolResult := hasPendingToolResultMessages(req.Messages) ||
		hasCompactedToolResultMessages(invocation)
	if !hasToolResult && !p.promptBeforeToolResult {
		return
	}

	systemMsgIndex := findSystemMessageIndex(req.Messages)
	if systemMsgIndex >= 0 {
		appendPostToolPrompt(&req.Messages[systemMsgIndex], p.prompt)
		return
	}

	if !hasToolResult && !p.createSystemMessage {
		return
	}

	req.Messages = append(
		[]model.Message{model.NewSystemMessage(p.prompt)},
		req.Messages...,
	)
}

// SupportsContextCompactionRebuild reports that post-tool prompting can be
// safely replayed during the sync-summary rebuild path.
func (p *PostToolRequestProcessor) SupportsContextCompactionRebuild(
	_ *agent.Invocation,
) bool {
	return true
}

// RebuildRequestForContextCompaction re-applies post-tool prompting during
// the safe sync-summary rebuild path without replaying the full processor
// chain.
func (p *PostToolRequestProcessor) RebuildRequestForContextCompaction(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	p.ProcessRequest(ctx, invocation, req, nil)
}

func appendPostToolPrompt(msg *model.Message, prompt string) {
	if msg.Content == "" {
		msg.Content = prompt
		return
	}
	msg.Content += systemPromptSeparator + prompt
}

func hasPostToolPrompt(msgs []model.Message, prompt string) bool {
	for _, msg := range msgs {
		if msg.Role == model.RoleSystem &&
			strings.Contains(msg.Content, prompt) {
			return true
		}
	}
	return false
}

// hasPendingToolResultMessages returns true when the latest non-system
// message in the request is a tool result. Historical tool results that are
// followed by assistant or user messages do not count as pending.
func hasPendingToolResultMessages(msgs []model.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		switch msgs[i].Role {
		case model.RoleSystem:
			continue
		case model.RoleTool:
			return true
		default:
			return false
		}
	}
	return false
}

func hasCompactedToolResultMessages(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	raw, ok := inv.GetState(contentHasCompactedToolResultsStateKey)
	if !ok {
		return false
	}
	v, ok := raw.(bool)
	return ok && v
}
