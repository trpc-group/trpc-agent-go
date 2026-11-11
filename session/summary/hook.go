//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// PreSummaryHookContext carries all inputs for pre-summary hooks. Events and
// Text reflect the conversation rendered for summarization. With request-side
// projection, that conversation is model-visible while SourceEvents retains
// the stored source events through the selected summary boundary. Without
// request-side projection, SourceEvents equals Events. When the prompt contains
// {previous_summary}, Events and Text contain only newly uncovered conversation
// content and PreviousSummary carries the prior rolling summary. Prompts
// without that placeholder retain the legacy merged view in Events and Text.
type PreSummaryHookContext struct {
	Ctx     context.Context
	Session *session.Session
	Events  []event.Event
	// SourceEvents contains stored source events through the selected summary
	// boundary. Without a request-side projection, it is the same as Events.
	SourceEvents    []event.Event
	Text            string
	PreviousSummary string
}

// PreSummaryHook adjusts or enriches input text before summarization, e.g. add tool-call info, redact, or reorder events.
type PreSummaryHook func(in *PreSummaryHookContext) error

// InputOverflow describes the conversation text of a complete summary batch
// that the framework estimates to exceed the summary model's input budget.
// When the prompt uses {previous_summary}, Text excludes the previous summary.
// Otherwise Text may include the previous summary merged into the conversation.
type InputOverflow struct {
	Text string
}

// OverflowDecision authorizes one summary model attempt with Text replacing the
// batch's conversation text. Returning the original Text authorizes an unchanged
// attempt. Text must contain non-whitespace content and represent the complete
// batch, because a successful summary advances its original event boundary.
type OverflowDecision struct {
	Text string
}

// InputOverflowHook handles an estimated overflow after complete-prefix fallback
// cannot select a batch within budget. A non-nil decision authorizes one
// standalone model attempt without token-budget rejection or token tailoring by
// the framework's model adapters. Model callbacks still run; callbacks delegating
// the approved request must pass their supplied context and request pointer.
// Other requests sharing that context retain token tailoring. Provider errors
// and empty summaries are returned without an automatic summary retry.
//
// Returning nil, nil preserves the original overflow error. A non-nil error
// rejects the batch even when a decision is also returned, independently of
// WithSummaryHookAbortOnError. Cancellation also aborts the attempt.
//
// The hook may run concurrently for different sessions and may receive the same
// batch again in a later summary task. Registering it preserves prefix fallback.
// WithPreSummaryHook disables that fallback, so the overflow hook then receives
// the entire conversation selected and rewritten by the pre-summary hook.
type InputOverflowHook func(context.Context, InputOverflow) (*OverflowDecision, error)

// PostSummaryHookContext post-processes model output, e.g. append tags, trim, or add checklists.
type PostSummaryHookContext struct {
	Ctx     context.Context
	Session *session.Session
	Summary string
}

// PostSummaryHook post-processes model output, e.g. append tags, trim, or add checklists.
type PostSummaryHook func(in *PostSummaryHookContext) error
